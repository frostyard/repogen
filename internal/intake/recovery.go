package intake

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	schemaVersion = 1
	maxRecordSize = 4 << 20
)

// RequestSchema is the shared T0 producer and writer request schema.
const RequestSchema = "org.frostyard.repogen.request.v1"

type Request struct {
	Schema             string   `json:"schema"`
	Kind               string   `json:"kind"`
	Operation          string   `json:"operation"`
	Target             string   `json:"target"`
	Producer           string   `json:"producer"`
	ProvenanceDigest   string   `json:"provenance_digest"`
	ArtifactDigests    []string `json:"artifact_digests"`
	ExpectedPrior      *string  `json:"expected_prior"`
	Codename           string   `json:"codename"`
	Suite              string   `json:"suite"`
	Origin             string   `json:"origin"`
	Label              string   `json:"label"`
	Component          string   `json:"component"`
	Architectures      []string `json:"architectures"`
	ValidUntilPolicy   string   `json:"valid_until_policy"`
	ProductionEligible bool     `json:"production_eligible"`
}

type Receipt struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	Target        string `json:"target"`
	Sequence      uint64 `json:"sequence"`
	RequestSHA256 string `json:"request_sha256"`
	PolicySHA256  string `json:"policy_sha256"`
}

type Attempt struct {
	SchemaVersion int    `json:"schema_version"`
	RequestSHA256 string `json:"request_sha256"`
	AttemptID     string `json:"attempt_id"`
	StartedAt     string `json:"started_at"`
}

type Object struct {
	Key    string `json:"key"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type Result struct {
	SchemaVersion         int      `json:"schema_version"`
	RequestSHA256         string   `json:"request_sha256"`
	ProvenanceSHA256      string   `json:"provenance_sha256"`
	Target                string   `json:"target"`
	ExpectedPrior         *string  `json:"expected_prior"`
	AttemptID             string   `json:"attempt_id"`
	ActionCommit          string   `json:"action_commit"`
	RepogenVersion        string   `json:"repogen_version"`
	RepogenSHA256         string   `json:"repogen_sha256"`
	SigningKeyFingerprint string   `json:"signing_key_fingerprint"`
	StateSHA256           string   `json:"state_sha256"`
	CommitSHA256          string   `json:"commit_sha256"`
	Objects               []Object `json:"objects"`
}

type resultPointer struct {
	SchemaVersion int    `json:"schema_version"`
	ResultSHA256  string `json:"result_sha256"`
}

type submissionPointer struct {
	SchemaVersion int    `json:"schema_version"`
	RequestSHA256 string `json:"request_sha256"`
}

// Recorder conditionally creates retained requests and target-sequenced
// receipts after their referenced immutable objects pass read-back.
type Recorder struct {
	Store Store
}

// Accept records one authenticated producer request. The principal is an
// adapter-provided identity, not a request-controlled fallback.
func (r Recorder) Accept(
	ctx context.Context,
	principal string,
	submissionKey string,
	policySHA256 string,
	request Request,
) (_ *Receipt, retErr error) {
	if r.Store == nil {
		return nil, fmt.Errorf("%w: intake store is required", ErrState)
	}
	if principal == "" || principal != request.Producer {
		return nil, fmt.Errorf("%w: authenticated producer does not match request", ErrState)
	}
	if !safeSegment(submissionKey) || !validDigest(policySHA256) {
		return nil, fmt.Errorf("%w: invalid submission key or policy digest", ErrState)
	}
	if err := validateRequest(request); err != nil {
		return nil, err
	}
	if err := verifyRequestObjects(ctx, r.Store, request); err != nil {
		return nil, err
	}

	requestData, err := canonicalJSON(request)
	if err != nil {
		return nil, err
	}
	requestSHA256 := digestBytes(requestData)
	if err := createAndVerify(
		ctx,
		r.Store,
		requestKey(requestSHA256),
		requestData,
	); err != nil {
		return nil, err
	}

	submissionDigest := digestBytes([]byte(principal + "\x00" + submissionKey))
	submission := submissionPointer{
		SchemaVersion: schemaVersion,
		RequestSHA256: requestSHA256,
	}
	submissionData, err := canonicalJSON(submission)
	if err != nil {
		return nil, err
	}
	if err := createAndVerify(
		ctx,
		r.Store,
		path.Join("submissions/v1/sha256", submissionDigest+".json"),
		submissionData,
	); err != nil {
		if errors.Is(err, ErrConflict) {
			return nil, fmt.Errorf("%w: submission key was reused with different request bytes", err)
		}
		return nil, err
	}

	lock, err := r.Store.Acquire(ctx, "receipt-"+request.Kind+"-"+request.Target)
	if err != nil {
		return nil, fmt.Errorf("%w: acquire receipt sequence: %v", ErrState, err)
	}
	if lock == nil {
		return nil, fmt.Errorf("%w: store returned no receipt sequence lock", ErrState)
	}
	defer func() {
		if err := lock.Release(); retErr == nil && err != nil {
			retErr = fmt.Errorf("%w: release receipt sequence: %v", ErrState, err)
		}
	}()

	receipt, err := findReceipt(ctx, r.Store, request.Kind, request.Target, requestSHA256)
	if err == nil {
		return receipt, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	sequence, err := nextReceiptSequence(ctx, r.Store, request.Kind, request.Target)
	if err != nil {
		return nil, err
	}
	receipt = &Receipt{
		SchemaVersion: schemaVersion,
		Kind:          request.Kind,
		Target:        request.Target,
		Sequence:      sequence,
		RequestSHA256: requestSHA256,
		PolicySHA256:  policySHA256,
	}
	receiptData, err := canonicalJSON(receipt)
	if err != nil {
		return nil, err
	}
	if err := createAndVerify(ctx, r.Store, receiptKey(*receipt), receiptData); err != nil {
		return nil, err
	}
	return receipt, nil
}

// CreateImmutable stores one digest-addressed provenance or artifact object.
func CreateImmutable(ctx context.Context, store Store, key string, body []byte) error {
	return CreateImmutableStream(
		ctx,
		store,
		key,
		bytes.NewReader(body),
		int64(len(body)),
		digestBytes(body),
	)
}

// CreateImmutableStream stores and verifies one potentially large immutable
// object without buffering it in the intake implementation.
func CreateImmutableStream(
	ctx context.Context,
	store Store,
	key string,
	body io.Reader,
	size int64,
	expectedSHA256 string,
) error {
	if store == nil {
		return fmt.Errorf("%w: intake store is required", ErrState)
	}
	if body == nil || size < 0 || !validDigest(expectedSHA256) {
		return fmt.Errorf("%w: immutable body, size, and digest are required", ErrState)
	}
	_, err := store.CreateIfAbsent(ctx, key, body, size)
	if err != nil {
		return fmt.Errorf("%w: create %s: %v", ErrState, key, err)
	}
	observed, err := inspectDigestObject(ctx, store, key)
	if err != nil {
		return err
	}
	if observed.SHA256 != expectedSHA256 || observed.Size != size {
		return fmt.Errorf("%w: read-back bytes differ for %s", ErrIntegrity, key)
	}
	return nil
}

// Writer applies one request and verifies its public commit point. Apply must
// be idempotent when the intended complete generation is already visible.
type Writer interface {
	Apply(ctx context.Context, receipt Receipt, request Request) (*Result, error)
	Verify(ctx context.Context, receipt Receipt, request Request, result Result) error
}

type Authorizer interface {
	Authorize(ctx context.Context, receipt Receipt, request Request) error
}

type AuthorizeFunc func(context.Context, Receipt, Request) error

func (f AuthorizeFunc) Authorize(ctx context.Context, receipt Receipt, request Request) error {
	return f(ctx, receipt, request)
}

// Reconciler enumerates retained receipts and processes each target in receipt
// order. ReconcileAll is suitable for either scheduled or manual wake-ups.
type Reconciler struct {
	Store      Store
	Kind       string
	Writer     Writer
	Authorizer Authorizer
	Now        func() time.Time
	AttemptID  func() (string, error)
}

func (r Reconciler) ReconcileAll(ctx context.Context) error {
	if err := r.validate(); err != nil {
		return err
	}
	keys, err := r.Store.List(ctx, path.Join("receipts/v1", r.Kind))
	if err != nil {
		return fmt.Errorf("%w: enumerate receipts: %v", ErrState, err)
	}
	targetSet := make(map[string]struct{})
	for _, key := range keys {
		receipt, err := parseReceiptKey(key)
		if err != nil {
			return err
		}
		if receipt.Kind != r.Kind {
			return fmt.Errorf("%w: receipt escaped configured kind", ErrIntegrity)
		}
		targetSet[receipt.Target] = struct{}{}
	}
	targets := make([]string, 0, len(targetSet))
	for target := range targetSet {
		targets = append(targets, target)
	}
	sort.Strings(targets)

	var wait sync.WaitGroup
	errorsByTarget := make([]error, len(targets))
	for index, target := range targets {
		wait.Add(1)
		go func(index int, target string) {
			defer wait.Done()
			errorsByTarget[index] = r.ReconcileTarget(ctx, target)
		}(index, target)
	}
	wait.Wait()
	return errors.Join(errorsByTarget...)
}

func (r Reconciler) ReconcileTarget(ctx context.Context, target string) (retErr error) {
	if err := r.validate(); err != nil {
		return err
	}
	if !safeSegment(target) {
		return fmt.Errorf("%w: unsafe recovery target", ErrState)
	}
	lock, err := r.Store.Acquire(ctx, "writer-"+r.Kind+"-"+target)
	if err != nil {
		return fmt.Errorf("%w: acquire writer serialization: %v", ErrState, err)
	}
	if lock == nil {
		return fmt.Errorf("%w: store returned no writer serialization lock", ErrState)
	}
	defer func() {
		if err := lock.Release(); retErr == nil && err != nil {
			retErr = fmt.Errorf("%w: release writer serialization: %v", ErrState, err)
		}
	}()

	keys, err := r.Store.List(ctx, receiptPrefix(r.Kind, target))
	if err != nil {
		return fmt.Errorf("%w: enumerate target receipts: %v", ErrState, err)
	}
	receipts := make([]Receipt, 0, len(keys))
	for _, key := range keys {
		keyReceipt, err := parseReceiptKey(key)
		if err != nil {
			return err
		}
		data, err := readRecord(ctx, r.Store, key)
		if err != nil {
			return fmt.Errorf("%w: read receipt: %v", ErrState, err)
		}
		var receipt Receipt
		if err := decodeCanonical(data, &receipt); err != nil {
			return err
		}
		if !receiptMatchesKey(receipt, keyReceipt) ||
			receipt.SchemaVersion != schemaVersion ||
			!validDigest(receipt.PolicySHA256) {
			return fmt.Errorf("%w: receipt does not match its immutable key", ErrIntegrity)
		}
		receipts = append(receipts, receipt)
	}
	sort.Slice(receipts, func(i, j int) bool {
		return receipts[i].Sequence < receipts[j].Sequence
	})
	for index := 1; index < len(receipts); index++ {
		if receipts[index-1].Sequence == receipts[index].Sequence {
			return fmt.Errorf("%w: duplicate receipt sequence %d", ErrIntegrity, receipts[index].Sequence)
		}
	}

	var previousState string
	for _, receipt := range receipts {
		request, err := loadRequest(ctx, r.Store, receipt.RequestSHA256)
		if err != nil {
			return err
		}
		if request.Kind != receipt.Kind || request.Target != receipt.Target {
			return fmt.Errorf("%w: request and receipt target disagree", ErrIntegrity)
		}
		if err := verifyRequestObjects(ctx, r.Store, request); err != nil {
			return err
		}
		if previousState != "" &&
			(request.ExpectedPrior == nil || *request.ExpectedPrior != previousState) {
			return fmt.Errorf("%w: request sequence breaks prior-state continuity", ErrState)
		}
		result, err := r.loadResult(ctx, receipt, request)
		if errors.Is(err, ErrNotFound) {
			if err := r.Authorizer.Authorize(ctx, receipt, request); err != nil {
				return fmt.Errorf("%w: current policy denied request: %v", ErrState, err)
			}
			result, err = r.apply(ctx, receipt, request)
		}
		if err != nil {
			return err
		}
		previousState = result.StateSHA256
	}
	return nil
}

func (r Reconciler) validate() error {
	if r.Store == nil || r.Writer == nil || r.Authorizer == nil {
		return fmt.Errorf("%w: store, writer, and authorizer are required", ErrState)
	}
	if !safeSegment(r.Kind) || r.Kind != "debian" {
		return fmt.Errorf("%w: reconciler requires the separate debian writer kind", ErrState)
	}
	return nil
}

func (r Reconciler) loadResult(
	ctx context.Context,
	receipt Receipt,
	request Request,
) (*Result, error) {
	pointerData, err := readRecord(ctx, r.Store, resultPointerKey(receipt.RequestSHA256))
	if err != nil {
		return nil, err
	}
	var pointer resultPointer
	if err := decodeCanonical(pointerData, &pointer); err != nil {
		return nil, err
	}
	if pointer.SchemaVersion != schemaVersion || !validDigest(pointer.ResultSHA256) {
		return nil, fmt.Errorf("%w: invalid result pointer", ErrIntegrity)
	}
	resultData, err := readDigestObject(
		ctx,
		r.Store,
		resultManifestKey(pointer.ResultSHA256),
		pointer.ResultSHA256,
	)
	if err != nil {
		return nil, err
	}
	var result Result
	if err := decodeCanonical(resultData, &result); err != nil {
		return nil, err
	}
	if err := validateResult(receipt, request, result); err != nil {
		return nil, err
	}
	if err := r.Writer.Verify(ctx, receipt, request, result); err != nil {
		return nil, fmt.Errorf("%w: completed result failed public read-back: %v", ErrState, err)
	}
	return &result, nil
}

func (r Reconciler) apply(
	ctx context.Context,
	receipt Receipt,
	request Request,
) (*Result, error) {
	attemptID, err := r.newAttemptID()
	if err != nil {
		return nil, err
	}
	attempt := Attempt{
		SchemaVersion: schemaVersion,
		RequestSHA256: receipt.RequestSHA256,
		AttemptID:     attemptID,
		StartedAt:     r.now().UTC().Format(time.RFC3339Nano),
	}
	attemptData, err := canonicalJSON(attempt)
	if err != nil {
		return nil, err
	}
	if err := createAndVerify(
		ctx,
		r.Store,
		path.Join("attempts/v1", receipt.RequestSHA256, attemptID+".json"),
		attemptData,
	); err != nil {
		return nil, err
	}

	result, err := r.Writer.Apply(ctx, receipt, request)
	if err != nil {
		return nil, fmt.Errorf("%w: writer attempt failed: %v", ErrState, err)
	}
	if result == nil {
		return nil, fmt.Errorf("%w: writer returned no result", ErrState)
	}
	result.SchemaVersion = schemaVersion
	result.RequestSHA256 = receipt.RequestSHA256
	result.ProvenanceSHA256 = request.ProvenanceDigest
	result.Target = receipt.Target
	result.ExpectedPrior = request.ExpectedPrior
	result.AttemptID = attemptID
	if err := validateResult(receipt, request, *result); err != nil {
		return nil, err
	}
	if err := r.Writer.Verify(ctx, receipt, request, *result); err != nil {
		return nil, fmt.Errorf("%w: result failed public read-back: %v", ErrState, err)
	}
	resultData, err := canonicalJSON(result)
	if err != nil {
		return nil, err
	}
	resultSHA256 := digestBytes(resultData)
	if err := createAndVerify(
		ctx,
		r.Store,
		resultManifestKey(resultSHA256),
		resultData,
	); err != nil {
		return nil, err
	}
	pointer := resultPointer{
		SchemaVersion: schemaVersion,
		ResultSHA256:  resultSHA256,
	}
	pointerData, err := canonicalJSON(pointer)
	if err != nil {
		return nil, err
	}
	if err := createAndVerify(
		ctx,
		r.Store,
		resultPointerKey(receipt.RequestSHA256),
		pointerData,
	); err != nil {
		return nil, err
	}
	return result, nil
}

func (r Reconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r Reconciler) newAttemptID() (string, error) {
	if r.AttemptID != nil {
		return r.AttemptID()
	}
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("%w: allocate attempt ID: %v", ErrState, err)
	}
	return hex.EncodeToString(value[:]), nil
}

func validateRequest(request Request) error {
	if request.Schema != RequestSchema ||
		request.Kind != "debian" ||
		!safeSegment(request.Target) ||
		request.Codename != request.Target ||
		request.Suite != request.Target ||
		request.Component == "" ||
		len(request.Architectures) == 0 ||
		request.Origin == "" ||
		request.Label == "" ||
		request.ValidUntilPolicy == "" ||
		!safeIdentity(request.Producer) ||
		!validDigest(request.ProvenanceDigest) ||
		len(request.ArtifactDigests) == 0 {
		return fmt.Errorf("%w: invalid durable request", ErrIntegrity)
	}

	switch request.Operation {
	case "initialize":
		if request.ExpectedPrior != nil {
			return fmt.Errorf("%w: initialize request has expected prior state", ErrIntegrity)
		}
	case "reconcile":
		if request.ExpectedPrior == nil || !validDigest(*request.ExpectedPrior) {
			return fmt.Errorf("%w: reconcile request lacks exact prior state", ErrIntegrity)
		}
	default:
		return fmt.Errorf("%w: invalid durable request operation", ErrIntegrity)
	}
	seen := make(map[string]struct{}, len(request.ArtifactDigests))
	for _, digest := range request.ArtifactDigests {
		if !validDigest(digest) {
			return fmt.Errorf("%w: invalid artifact digest", ErrIntegrity)
		}
		if _, duplicate := seen[digest]; duplicate {
			return fmt.Errorf("%w: duplicate artifact digest", ErrIntegrity)
		}
		seen[digest] = struct{}{}
	}
	seenArchitectures := make(map[string]struct{}, len(request.Architectures))
	for _, architecture := range request.Architectures {
		if !safeSegment(architecture) {
			return fmt.Errorf("%w: invalid architecture", ErrIntegrity)
		}
		if _, duplicate := seenArchitectures[architecture]; duplicate {
			return fmt.Errorf("%w: duplicate architecture", ErrIntegrity)
		}
		seenArchitectures[architecture] = struct{}{}
	}
	return nil
}

func safeIdentity(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '.' || character == '_' || character == '-' ||
			character == '/' || character == ':' || character == '@' {
			continue
		}
		return false
	}
	return true
}

func validateResult(receipt Receipt, request Request, result Result) error {
	if result.SchemaVersion != schemaVersion ||
		result.RequestSHA256 != receipt.RequestSHA256 ||
		result.ProvenanceSHA256 != request.ProvenanceDigest ||
		result.Target != receipt.Target ||
		!sameOptionalDigest(result.ExpectedPrior, request.ExpectedPrior) ||
		!safeSegment(result.AttemptID) ||
		!validCommit(result.ActionCommit) ||
		!safeIdentity(result.RepogenVersion) ||
		!validDigest(result.RepogenSHA256) ||
		!validFingerprint(result.SigningKeyFingerprint) ||
		!validDigest(result.StateSHA256) ||
		!validDigest(result.CommitSHA256) ||
		len(result.Objects) == 0 {
		return fmt.Errorf("%w: invalid durable result", ErrIntegrity)
	}

	seen := make(map[string]struct{}, len(result.Objects))
	for _, object := range result.Objects {
		if !safeKey(object.Key) || !validDigest(object.SHA256) || object.Size < 0 {
			return fmt.Errorf("%w: invalid durable result object", ErrIntegrity)
		}
		if _, duplicate := seen[object.Key]; duplicate {
			return fmt.Errorf("%w: duplicate durable result object", ErrIntegrity)
		}
		seen[object.Key] = struct{}{}
	}
	return nil
}

func sameOptionalDigest(first, second *string) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func validCommit(value string) bool {
	if len(value) != 40 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validFingerprint(value string) bool {
	if len(value) != 40 || strings.ToUpper(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func verifyRequestObjects(ctx context.Context, store Store, request Request) error {
	if err := verifyDigestObject(
		ctx,
		store,
		path.Join("manifests/provenance/v1/sha256", request.ProvenanceDigest+".json"),
		request.ProvenanceDigest,
	); err != nil {
		return fmt.Errorf("%w: verify provenance: %v", ErrIntegrity, err)
	}
	for _, digest := range request.ArtifactDigests {
		if err := verifyDigestObject(
			ctx,
			store,
			path.Join("blobs/sha256", digest[:2], digest),
			digest,
		); err != nil {
			return fmt.Errorf("%w: verify artifact: %v", ErrIntegrity, err)
		}
	}
	return nil
}

func loadRequest(ctx context.Context, store Store, digest string) (Request, error) {
	data, err := readDigestObject(ctx, store, requestKey(digest), digest)
	if err != nil {
		return Request{}, err
	}
	var request Request
	if err := decodeCanonical(data, &request); err != nil {
		return Request{}, err
	}
	if err := validateRequest(request); err != nil {
		return Request{}, err
	}
	return request, nil
}

func findReceipt(
	ctx context.Context,
	store Store,
	kind string,
	target string,
	requestSHA256 string,
) (*Receipt, error) {
	keys, err := store.List(ctx, receiptPrefix(kind, target))
	if err != nil {
		return nil, fmt.Errorf("%w: enumerate receipts: %v", ErrState, err)
	}
	suffix := "-" + requestSHA256 + ".json"
	for _, key := range keys {
		if !strings.HasSuffix(key, suffix) {
			continue
		}
		data, err := readRecord(ctx, store, key)
		if err != nil {
			return nil, err
		}
		var receipt Receipt
		if err := decodeCanonical(data, &receipt); err != nil {
			return nil, err
		}
		keyReceipt, err := parseReceiptKey(key)
		if err != nil || !receiptMatchesKey(receipt, keyReceipt) {
			return nil, fmt.Errorf("%w: receipt does not match its immutable key", ErrIntegrity)
		}
		return &receipt, nil
	}
	return nil, ErrNotFound
}

func nextReceiptSequence(
	ctx context.Context,
	store Store,
	kind string,
	target string,
) (uint64, error) {
	keys, err := store.List(ctx, receiptPrefix(kind, target))
	if err != nil {
		return 0, fmt.Errorf("%w: enumerate receipt sequence: %v", ErrState, err)
	}
	var maximum uint64
	for _, key := range keys {
		receipt, err := parseReceiptKey(key)
		if err != nil {
			return 0, err
		}
		if receipt.Sequence > maximum {
			maximum = receipt.Sequence
		}
	}
	if maximum >= maxSafeJSONInteger {
		return 0, fmt.Errorf("%w: receipt sequence exhausted", ErrState)
	}
	return maximum + 1, nil
}

func parseReceiptKey(key string) (Receipt, error) {
	parts := strings.Split(key, "/")
	if len(parts) != 5 || parts[0] != "receipts" || parts[1] != "v1" ||
		!safeSegment(parts[2]) || !safeSegment(parts[3]) {
		return Receipt{}, fmt.Errorf("%w: malformed receipt key %q", ErrIntegrity, key)
	}
	name := strings.TrimSuffix(parts[4], ".json")
	separator := strings.IndexByte(name, '-')
	if separator != 20 || !strings.HasSuffix(parts[4], ".json") {
		return Receipt{}, fmt.Errorf("%w: malformed receipt key %q", ErrIntegrity, key)
	}
	sequence, err := strconv.ParseUint(name[:separator], 10, 64)
	if err != nil || sequence == 0 || sequence > maxSafeJSONInteger ||
		!validDigest(name[separator+1:]) {
		return Receipt{}, fmt.Errorf("%w: malformed receipt key %q", ErrIntegrity, key)
	}
	return Receipt{
		SchemaVersion: schemaVersion,
		Kind:          parts[2],
		Target:        parts[3],
		Sequence:      sequence,
		RequestSHA256: name[separator+1:],
	}, nil
}

func receiptPrefix(kind, target string) string {
	return path.Join("receipts/v1", kind, target)
}

func receiptKey(receipt Receipt) string {
	return path.Join(
		receiptPrefix(receipt.Kind, receipt.Target),
		fmt.Sprintf("%020d-%s.json", receipt.Sequence, receipt.RequestSHA256),
	)
}

func receiptMatchesKey(receipt, key Receipt) bool {
	return receipt.SchemaVersion == key.SchemaVersion &&
		receipt.Kind == key.Kind &&
		receipt.Target == key.Target &&
		receipt.Sequence == key.Sequence &&
		receipt.RequestSHA256 == key.RequestSHA256
}

func requestKey(digest string) string {
	return path.Join("manifests/request/v1/sha256", digest+".json")
}

func resultManifestKey(digest string) string {
	return path.Join("manifests/result/v1/sha256", digest+".json")
}

func resultPointerKey(requestDigest string) string {
	return path.Join("results/v1", requestDigest+".json")
}

func createAndVerify(ctx context.Context, store Store, key string, body []byte) error {
	created, err := store.CreateIfAbsent(ctx, key, bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return fmt.Errorf("%w: create %s: %v", ErrState, key, err)
	}
	observed, readErr := readRecord(ctx, store, key)
	if readErr != nil {
		return fmt.Errorf("%w: read back %s: %v", ErrIntegrity, key, readErr)
	}
	if !bytes.Equal(observed, body) {
		if !created {
			return fmt.Errorf("%w: existing bytes differ for %s", ErrConflict, key)
		}
		return fmt.Errorf("%w: read-back bytes differ for %s", ErrIntegrity, key)
	}
	return nil
}

func readDigestObject(
	ctx context.Context,
	store Store,
	key string,
	expectedSHA256 string,
) ([]byte, error) {
	body, err := readRecord(ctx, store, key)
	if err != nil {
		return nil, err
	}
	if digestBytes(body) != expectedSHA256 {
		return nil, fmt.Errorf("%w: digest mismatch for %s", ErrIntegrity, key)
	}
	return body, nil
}

func verifyDigestObject(
	ctx context.Context,
	store Store,
	key string,
	expectedSHA256 string,
) error {
	observed, err := inspectDigestObject(ctx, store, key)
	if err != nil {
		return err
	}
	if observed.SHA256 != expectedSHA256 {
		return fmt.Errorf("%w: digest mismatch for %s", ErrIntegrity, key)
	}
	return nil
}

func inspectDigestObject(
	ctx context.Context,
	store Store,
	key string,
) (Object, error) {
	body, err := store.Open(ctx, key)
	if err != nil {
		return Object{}, err
	}
	hash := sha256.New()
	size, readErr := io.Copy(hash, &contextReader{ctx: ctx, reader: body})
	closeErr := body.Close()
	if readErr != nil {
		return Object{}, fmt.Errorf("%w: read %s: %v", ErrIntegrity, key, readErr)
	}
	if closeErr != nil {
		return Object{}, fmt.Errorf("%w: close %s: %v", ErrIntegrity, key, closeErr)
	}
	return Object{
		Key:    key,
		SHA256: hex.EncodeToString(hash.Sum(nil)),
		Size:   size,
	}, nil
}

func readRecord(ctx context.Context, store Store, key string) ([]byte, error) {
	body, err := store.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(
		io.LimitReader(&contextReader{ctx: ctx, reader: body}, maxRecordSize+1),
	)
	closeErr := body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("%w: read record %s: %v", ErrIntegrity, key, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("%w: close record %s: %v", ErrIntegrity, key, closeErr)
	}
	if len(data) > maxRecordSize {
		return nil, fmt.Errorf("%w: record %s exceeds size limit", ErrIntegrity, key)
	}
	return data, nil
}

func decodeCanonical(data []byte, destination any) error {
	if _, err := canonicalizeJSON(data); err != nil {
		return fmt.Errorf("%w: decode canonical record: %v", ErrIntegrity, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("%w: decode canonical record: %v", ErrIntegrity, err)
	}
	canonical, err := canonicalJSON(destination)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, canonical) {
		return fmt.Errorf("%w: record is not canonical JSON", ErrIntegrity)
	}
	return nil
}

func digestBytes(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

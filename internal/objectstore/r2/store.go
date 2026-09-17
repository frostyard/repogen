package r2

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
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/frostyard/repogen/internal/generator/deb"
	"github.com/frostyard/repogen/internal/intake"
)

var (
	ErrAccess      = errors.New("R2 access denied")
	ErrConflict    = errors.New("R2 conditional write conflict")
	ErrIntegrity   = errors.New("R2 object integrity failure")
	ErrLockHeld    = errors.New("R2 target lock is held")
	ErrProvider    = errors.New("R2 provider operation failed")
	ErrScope       = errors.New("R2 object is outside the configured scope")
	ErrUnavailable = errors.New("R2 provider is unavailable")
)

type API interface {
	GetObject(
		context.Context,
		*s3.GetObjectInput,
		...func(*s3.Options),
	) (*s3.GetObjectOutput, error)
	PutObject(
		context.Context,
		*s3.PutObjectInput,
		...func(*s3.Options),
	) (*s3.PutObjectOutput, error)
	ListObjectsV2(
		context.Context,
		*s3.ListObjectsV2Input,
		...func(*s3.Options),
	) (*s3.ListObjectsV2Output, error)
}

type Target struct {
	Client            API
	Bucket            string
	Prefix            string
	Codename          string
	AllowedPoolObject map[string]deb.PoolDigest
	Locker            *Locker
}

type IntakeStore struct {
	Client API
	Bucket string
	Prefix string
	Locker *Locker
}

type PublicationStore struct {
	target Target
}

func NewPublicationStore(target Target) (*PublicationStore, error) {
	if target.Client == nil || target.Locker == nil || !safeName(target.Bucket) ||
		target.Codename != "trixie" || !safePrefix(target.Prefix) {
		return nil, fmt.Errorf("%w: invalid publication target", ErrScope)
	}
	allowed := make(map[string]deb.PoolDigest, len(target.AllowedPoolObject))
	for key, digest := range target.AllowedPoolObject {
		if !allowedPoolPath(key) || digest.Size < 0 || !validSHA256(digest.SHA256) {
			return nil, fmt.Errorf("%w: invalid approved pool object %q", ErrScope, key)
		}
		allowed[key] = digest
	}
	target.AllowedPoolObject = allowed
	return &PublicationStore{target: target}, nil
}

func NewIntakeStore(client API, bucket, prefix string, locker *Locker) (*IntakeStore, error) {
	if client == nil || locker == nil || !safeName(bucket) || !safePrefix(prefix) {
		return nil, fmt.Errorf("%w: invalid intake target", ErrScope)
	}
	return &IntakeStore{Client: client, Bucket: bucket, Prefix: prefix, Locker: locker}, nil
}

func (s *PublicationStore) Open(
	ctx context.Context,
	key string,
) (*deb.RemotePoolObject, error) {
	if err := s.validateReadKey(key); err != nil {
		return nil, err
	}
	output, err := get(ctx, s.target.Client, s.target.Bucket, joinPrefix(s.target.Prefix, key))
	if errors.Is(err, errNotFound) {
		return nil, deb.ErrPoolObjectNotFound
	}
	if err != nil {
		return nil, err
	}
	return &deb.RemotePoolObject{
		Body: output.Body,
		ETag: aws.ToString(output.ETag),
	}, nil
}

func (s *PublicationStore) Create(
	ctx context.Context,
	key string,
	body io.Reader,
	size int64,
) error {
	if err := s.validateCreateKey(key); err != nil {
		return err
	}
	err := put(ctx, s.target.Client, s.target.Bucket, joinPrefix(s.target.Prefix, key), body, size, "", "*")
	if errors.Is(err, ErrConflict) {
		return deb.ErrPoolObjectExists
	}
	return err
}

func (s *PublicationStore) Replace(
	ctx context.Context,
	key string,
	body io.Reader,
	size int64,
	expectedSHA256 string,
) error {
	if err := s.validateMutableKey(key); err != nil {
		return err
	}
	if !validSHA256(expectedSHA256) {
		return fmt.Errorf("%w: replacement digest is invalid", ErrIntegrity)
	}
	objectKey := joinPrefix(s.target.Prefix, key)
	prior, err := get(ctx, s.target.Client, s.target.Bucket, objectKey)
	if errors.Is(err, errNotFound) {
		return fmt.Errorf("%w: replacement target is absent", ErrConflict)
	}
	if err != nil {
		return err
	}
	observed, readErr := digestBody(ctx, prior.Body)
	if readErr != nil {
		return fmt.Errorf("%w: read replacement target: %v", ErrIntegrity, readErr)
	}
	if observed != expectedSHA256 {
		return fmt.Errorf("%w: replacement target digest changed", ErrConflict)
	}
	etag := aws.ToString(prior.ETag)
	if etag == "" {
		return fmt.Errorf("%w: replacement target has no ETag", ErrIntegrity)
	}
	return put(ctx, s.target.Client, s.target.Bucket, objectKey, body, size, etag, "")
}

func (s *PublicationStore) PrefixExists(ctx context.Context, prefix string) (bool, error) {
	keys, err := s.ListPrefix(ctx, prefix)
	return len(keys) > 0, err
}

func (s *PublicationStore) ListPrefix(ctx context.Context, prefix string) ([]string, error) {
	if !strings.HasPrefix(prefix, "dists/"+s.target.Codename+"/") {
		return nil, fmt.Errorf("%w: list prefix %q is outside dists/%s", ErrScope, prefix, s.target.Codename)
	}
	keys, err := list(ctx, s.target.Client, s.target.Bucket, joinPrefix(s.target.Prefix, prefix))
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		relative, ok := trimPrefix(s.target.Prefix, key)
		if !ok || !strings.HasPrefix(relative, prefix) {
			return nil, fmt.Errorf("%w: provider returned key outside requested prefix", ErrIntegrity)
		}
		result = append(result, relative)
	}
	return result, nil
}

func (s *PublicationStore) Acquire(
	ctx context.Context,
	target string,
) (deb.ProductionPublicationLock, error) {
	if target != s.target.Codename {
		return nil, fmt.Errorf("%w: lock target %q is not %s", ErrScope, target, s.target.Codename)
	}
	return s.target.Locker.Acquire(ctx, "publication-"+target)
}

func (s *PublicationStore) validateReadKey(key string) error {
	if strings.HasPrefix(key, "dists/"+s.target.Codename+"/") {
		return nil
	}
	if _, ok := s.target.AllowedPoolObject[key]; ok {
		return nil
	}
	return fmt.Errorf("%w: read key %q is not approved", ErrScope, key)
}

func (s *PublicationStore) validateCreateKey(key string) error {
	if digest, ok := s.target.AllowedPoolObject[key]; ok {
		if digest.Size < 0 || !validSHA256(digest.SHA256) {
			return fmt.Errorf("%w: approved pool digest is invalid", ErrScope)
		}
		return nil
	}
	prefix := "dists/" + s.target.Codename + "/"
	if strings.HasPrefix(key, prefix) && validObjectKey(key) {
		return nil
	}
	return fmt.Errorf("%w: create key %q is not immutable and approved", ErrScope, key)
}

func (s *PublicationStore) validateMutableKey(key string) error {
	prefix := "dists/" + s.target.Codename + "/"
	if !strings.HasPrefix(key, prefix) || !validObjectKey(key) ||
		strings.Contains(key, "/by-hash/") {
		return fmt.Errorf("%w: replacement key %q is outside mutable %s metadata", ErrScope, key, prefix)
	}
	return nil
}

func (s *IntakeStore) CreateIfAbsent(
	ctx context.Context,
	key string,
	body io.Reader,
	size int64,
) (bool, error) {
	if !validObjectKey(key) {
		return false, fmt.Errorf("%w: unsafe intake key", ErrScope)
	}
	err := put(ctx, s.Client, s.Bucket, joinPrefix(s.Prefix, key), body, size, "", "*")
	if errors.Is(err, ErrConflict) {
		return false, nil
	}
	return err == nil, err
}

func (s *IntakeStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if !validObjectKey(key) {
		return nil, fmt.Errorf("%w: unsafe intake key", ErrScope)
	}
	output, err := get(ctx, s.Client, s.Bucket, joinPrefix(s.Prefix, key))
	if errors.Is(err, errNotFound) {
		return nil, intake.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return output.Body, nil
}

func (s *IntakeStore) List(ctx context.Context, prefix string) ([]string, error) {
	if !validObjectKey(strings.TrimSuffix(prefix, "/")) {
		return nil, fmt.Errorf("%w: unsafe intake prefix", ErrScope)
	}
	fullPrefix := joinPrefix(s.Prefix, prefix)
	keys, err := list(ctx, s.Client, s.Bucket, fullPrefix)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		relative, ok := trimPrefix(s.Prefix, key)
		if !ok || !strings.HasPrefix(relative, prefix) {
			return nil, fmt.Errorf("%w: provider returned key outside intake prefix", ErrIntegrity)
		}
		result = append(result, relative)
	}
	return result, nil
}

func (s *IntakeStore) Acquire(ctx context.Context, target string) (intake.Lock, error) {
	if !safeName(target) {
		return nil, fmt.Errorf("%w: unsafe intake lock target", ErrScope)
	}
	return s.Locker.Acquire(ctx, "intake-"+target)
}

type Locker struct {
	Client API
	Bucket string
	Prefix string
	Random func([]byte) (int, error)
}

type lockRecord struct {
	Owner  string `json:"owner"`
	State  string `json:"state"`
	Target string `json:"target"`
}

type heldLock struct {
	mu       sync.Mutex
	locker   *Locker
	key      string
	record   lockRecord
	released bool
}

func NewLocker(client API, bucket, prefix string) (*Locker, error) {
	if client == nil || !safeName(bucket) || !safePrefix(prefix) {
		return nil, fmt.Errorf("%w: invalid lock target", ErrScope)
	}
	return &Locker{Client: client, Bucket: bucket, Prefix: prefix, Random: rand.Read}, nil
}

func (l *Locker) Acquire(ctx context.Context, target string) (*heldLock, error) {
	if !safeName(target) {
		return nil, fmt.Errorf("%w: unsafe lock target", ErrScope)
	}
	var nonce [32]byte
	if _, err := io.ReadFull(randomReader{read: l.Random}, nonce[:]); err != nil {
		return nil, fmt.Errorf("%w: allocate lock owner: %v", ErrProvider, err)
	}
	record := lockRecord{
		Owner:  hex.EncodeToString(nonce[:]),
		State:  "held",
		Target: target,
	}
	body, err := marshalLock(record)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(target))
	key := joinPrefix(l.Prefix, path.Join("locks", hex.EncodeToString(digest[:])+".json"))

	err = put(ctx, l.Client, l.Bucket, key, bytes.NewReader(body), int64(len(body)), "", "*")
	if err == nil {
		return &heldLock{locker: l, key: key, record: record}, nil
	}
	if !errors.Is(err, ErrConflict) {
		return nil, err
	}

	current, etag, err := l.read(ctx, key)
	if err != nil {
		return nil, err
	}
	if current.Target != target {
		return nil, fmt.Errorf("%w: lock target digest collision", ErrIntegrity)
	}
	if current.State == "held" {
		return nil, fmt.Errorf("%w: target %s", ErrLockHeld, target)
	}
	if current.State != "released" {
		return nil, fmt.Errorf("%w: invalid lock state", ErrIntegrity)
	}
	err = put(ctx, l.Client, l.Bucket, key, bytes.NewReader(body), int64(len(body)), etag, "")
	if err != nil {
		return nil, err
	}
	return &heldLock{locker: l, key: key, record: record}, nil
}

func (l *Locker) read(ctx context.Context, key string) (lockRecord, string, error) {
	output, err := get(ctx, l.Client, l.Bucket, key)
	if err != nil {
		return lockRecord{}, "", err
	}
	data, readErr := io.ReadAll(io.LimitReader(output.Body, 2049))
	closeErr := output.Body.Close()
	if readErr != nil {
		return lockRecord{}, "", fmt.Errorf("%w: read lock: %v", ErrIntegrity, readErr)
	}
	if closeErr != nil {
		return lockRecord{}, "", fmt.Errorf("%w: close lock: %v", ErrIntegrity, closeErr)
	}
	if len(data) > 2048 {
		return lockRecord{}, "", fmt.Errorf("%w: lock record is too large", ErrIntegrity)
	}
	var record lockRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return lockRecord{}, "", fmt.Errorf("%w: decode lock: %v", ErrIntegrity, err)
	}
	canonical, err := marshalLock(record)
	if err != nil || !bytes.Equal(canonical, data) {
		return lockRecord{}, "", fmt.Errorf("%w: lock record is not canonical", ErrIntegrity)
	}
	etag := aws.ToString(output.ETag)
	if etag == "" {
		return lockRecord{}, "", fmt.Errorf("%w: lock has no ETag", ErrIntegrity)
	}
	return record, etag, nil
}

func (l *heldLock) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.released {
		return nil
	}
	current, etag, err := l.locker.read(context.Background(), l.key)
	if err != nil {
		return err
	}
	if current != l.record {
		return fmt.Errorf("%w: lock ownership changed", ErrConflict)
	}
	current.State = "released"
	body, err := marshalLock(current)
	if err != nil {
		return err
	}
	if err := put(
		context.Background(),
		l.locker.Client,
		l.locker.Bucket,
		l.key,
		bytes.NewReader(body),
		int64(len(body)),
		etag,
		"",
	); err != nil {
		return err
	}
	l.released = true
	return nil
}

func marshalLock(record lockRecord) ([]byte, error) {
	if !validSHA256(record.Owner) || !safeName(record.Target) ||
		(record.State != "held" && record.State != "released") {
		return nil, fmt.Errorf("%w: invalid lock record", ErrIntegrity)
	}
	return json.Marshal(record)
}

func get(ctx context.Context, client API, bucket, key string) (*s3.GetObjectOutput, error) {
	output, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, mapProviderError("get", err)
	}
	if output == nil || output.Body == nil {
		return nil, fmt.Errorf("%w: get returned no body", ErrIntegrity)
	}
	return output, nil
}

func put(
	ctx context.Context,
	client API,
	bucket string,
	key string,
	body io.Reader,
	size int64,
	ifMatch string,
	ifNoneMatch string,
) error {
	if body == nil || size < 0 {
		return fmt.Errorf("%w: put body and size are required", ErrIntegrity)
	}
	input := &s3.PutObjectInput{
		Bucket:        aws.String(bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentLength: aws.Int64(size),
	}
	if ifMatch != "" {
		input.IfMatch = aws.String(ifMatch)
	}
	if ifNoneMatch != "" {
		input.IfNoneMatch = aws.String(ifNoneMatch)
	}
	if _, err := client.PutObject(ctx, input); err != nil {
		return mapProviderError("put", err)
	}
	return nil
}

func list(ctx context.Context, client API, bucket, prefix string) ([]string, error) {
	var token *string
	seenTokens := map[string]struct{}{}
	seenKeys := map[string]struct{}{}
	var keys []string
	for {
		output, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return nil, mapProviderError("list", err)
		}
		if output == nil {
			return nil, fmt.Errorf("%w: list returned no response", ErrIntegrity)
		}
		for _, object := range output.Contents {
			key := aws.ToString(object.Key)
			if key == "" || !strings.HasPrefix(key, prefix) {
				return nil, fmt.Errorf("%w: list returned an invalid key", ErrIntegrity)
			}
			if _, duplicate := seenKeys[key]; duplicate {
				return nil, fmt.Errorf("%w: list returned duplicate key %q", ErrIntegrity, key)
			}
			seenKeys[key] = struct{}{}
			keys = append(keys, key)
		}
		if !aws.ToBool(output.IsTruncated) {
			break
		}
		next := aws.ToString(output.NextContinuationToken)
		if next == "" || (token != nil && next == aws.ToString(token)) {
			return nil, fmt.Errorf("%w: truncated list made no progress", ErrIntegrity)
		}
		if _, duplicate := seenTokens[next]; duplicate {
			return nil, fmt.Errorf("%w: list repeated a continuation token", ErrIntegrity)
		}
		seenTokens[next] = struct{}{}
		token = aws.String(next)
	}
	sort.Strings(keys)
	return keys, nil
}

var errNotFound = errors.New("R2 object not found")

func mapProviderError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		switch apiError.ErrorCode() {
		case "NoSuchKey", "NotFound", "404":
			return fmt.Errorf("%w: %s", errNotFound, operation)
		case "AccessDenied", "Forbidden", "InvalidAccessKeyId", "SignatureDoesNotMatch":
			return fmt.Errorf("%w: %s", ErrAccess, operation)
		case "PreconditionFailed", "ConditionalRequestConflict":
			return fmt.Errorf("%w: %s", ErrConflict, operation)
		case "InternalError", "ServiceUnavailable", "SlowDown", "RequestTimeout":
			return fmt.Errorf("%w: %s", ErrUnavailable, operation)
		}
	}
	var statusError interface{ HTTPStatusCode() int }
	if errors.As(err, &statusError) {
		switch statusError.HTTPStatusCode() {
		case 403:
			return fmt.Errorf("%w: %s", ErrAccess, operation)
		case 404:
			return fmt.Errorf("%w: %s", errNotFound, operation)
		case 409, 412:
			return fmt.Errorf("%w: %s", ErrConflict, operation)
		default:
			if statusError.HTTPStatusCode() >= 500 {
				return fmt.Errorf("%w: %s", ErrUnavailable, operation)
			}
		}
	}
	return fmt.Errorf("%w: %s", ErrProvider, operation)
}

func digestBody(ctx context.Context, body io.ReadCloser) (string, error) {
	hash := sha256.New()
	_, readErr := io.Copy(hash, &contextReader{ctx: ctx, reader: body})
	closeErr := body.Close()
	if readErr != nil {
		return "", readErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func joinPrefix(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "/" + key
}

func trimPrefix(prefix, key string) (string, bool) {
	if prefix == "" {
		return key, true
	}
	return strings.CutPrefix(key, prefix+"/")
}

func safePrefix(prefix string) bool {
	return prefix == "" || validObjectKey(prefix)
}

func safeName(value string) bool {
	if value == "" || len(value) > 256 || strings.ContainsAny(value, "/\\\x00\r\n\t") {
		return false
	}
	return value != "." && value != ".."
}

func validObjectKey(key string) bool {
	if key == "" || strings.HasPrefix(key, "/") || path.Clean(key) != key {
		return false
	}
	for _, segment := range strings.Split(key, "/") {
		if !safeName(segment) {
			return false
		}
	}
	return true
}

func allowedPoolPath(key string) bool {
	return strings.HasPrefix(key, "pool/main/") && validObjectKey(key)
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(body []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(body)
}

type randomReader struct {
	read func([]byte) (int, error)
}

func (r randomReader) Read(body []byte) (int, error) {
	return r.read(body)
}

var _ intake.Store = (*IntakeStore)(nil)
var _ deb.ProductionPublicationStore = (*PublicationStore)(nil)

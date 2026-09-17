package production

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/frostyard/repogen/internal/generator/deb"
	"github.com/frostyard/repogen/internal/intake"
	"github.com/frostyard/repogen/internal/signer"
)

func TestVerifySignerIdentityRejectsWrongFingerprintAndPublicKeyDigest(t *testing.T) {
	publicKey := readTestPublicKey(t)
	fingerprint := fingerprintForTest(t, publicKey)
	value := publicKeySigner{publicKey: publicKey}

	if _, err := VerifySignerIdentity(value, fingerprint, digest(publicKey)); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifySignerIdentity(
		value,
		"0123456789ABCDEF0123456789ABCDEF01234567",
		digest(publicKey),
	); err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("wrong fingerprint error = %v", err)
	}
	if _, err := VerifySignerIdentity(
		value,
		fingerprint,
		strings.Repeat("a", 64),
	); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("wrong public key digest error = %v", err)
	}
}

func TestExecutionRejectsWrongSignerBeforeAnyStoreIO(t *testing.T) {
	publicKey := readTestPublicKey(t)
	config, policy := validConfigAndPolicy()
	policy.SigningPublicKeySHA256 = digest(publicKey)
	policy.SigningKeyFingerprint = "0123456789ABCDEF0123456789ABCDEF01234567"
	intakeStore := &countingIntakeStore{}
	publication := &countingPublicationStore{}
	err := (Execution{
		Config:         config,
		ConfigSHA256:   canonicalDigestForTest(t, config),
		Policy:         policy,
		PolicySHA256:   canonicalDigestForTest(t, policy),
		RequestSHA256:  policy.RequestSHA256,
		ProvenanceHash: policy.ProvenanceSHA256,
		Intake:         intakeStore,
		Publication:    publication,
		Signer:         publicKeySigner{publicKey: publicKey},
		WorkDir:        t.TempDir(),
		ExecutablePath: filepath.Join(t.TempDir(), "repogen"),
		Version:        policy.RepogenVersion,
		Commit:         policy.ActionCommit,
	}).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("Execution.Run() error = %v, want signer rejection", err)
	}
	if intakeStore.calls != 0 || publication.calls != 0 {
		t.Fatalf("wrong signer contacted stores: intake=%d publication=%d", intakeStore.calls, publication.calls)
	}
}

func TestExecutionRejectsChangedCanonicalInputsBeforeAnyStoreIO(t *testing.T) {
	config, policy := validConfigAndPolicy()
	configSHA256 := canonicalDigestForTest(t, config)
	policySHA256 := canonicalDigestForTest(t, policy)
	config.PublicationBucket = "different-publication-bucket"
	intakeStore := &countingIntakeStore{}
	publication := &countingPublicationStore{}

	err := (Execution{
		Config:         config,
		ConfigSHA256:   configSHA256,
		Policy:         policy,
		PolicySHA256:   policySHA256,
		RequestSHA256:  policy.RequestSHA256,
		ProvenanceHash: policy.ProvenanceSHA256,
		Intake:         intakeStore,
		Publication:    publication,
		Signer:         publicKeySigner{publicKey: readTestPublicKey(t)},
	}).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "digest pins disagree") {
		t.Fatalf("Execution.Run() error = %v, want canonical digest rejection", err)
	}
	if intakeStore.calls != 0 || publication.calls != 0 {
		t.Fatalf("changed config contacted stores: intake=%d publication=%d", intakeStore.calls, publication.calls)
	}
}

func TestVerifyExecutableIdentityBindsVersionCommitAndBytes(t *testing.T) {
	_, policy := validConfigAndPolicy()
	filename := filepath.Join(t.TempDir(), "repogen")
	body := []byte("fixture executable")
	if err := os.WriteFile(filename, body, 0o700); err != nil {
		t.Fatal(err)
	}
	policy.RepogenSHA256 = digest(body)
	if err := VerifyExecutableIdentity(
		filename,
		policy.RepogenVersion,
		policy.ActionCommit,
		policy,
	); err != nil {
		t.Fatal(err)
	}
	if err := VerifyExecutableIdentity(
		filename,
		"wrong",
		policy.ActionCommit,
		policy,
	); err == nil {
		t.Fatal("VerifyExecutableIdentity() accepted the wrong version")
	}
	if err := os.WriteFile(filename, []byte("different"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := VerifyExecutableIdentity(
		filename,
		policy.RepogenVersion,
		policy.ActionCommit,
		policy,
	); err == nil {
		t.Fatal("VerifyExecutableIdentity() accepted different bytes")
	}
}

func TestPolicyAuthorizerRequiresExactPolicyAndProductionEligibility(t *testing.T) {
	_, policy := validConfigAndPolicy()
	authorizer := PolicyAuthorizer{
		Policy:       policy,
		PolicySHA256: strings.Repeat("b", 64),
	}
	receipt := intake.Receipt{
		RequestSHA256: policy.RequestSHA256,
		PolicySHA256:  strings.Repeat("b", 64),
	}
	request := intake.Request{
		Producer:           policy.Producer,
		ProvenanceDigest:   policy.ProvenanceSHA256,
		Target:             policy.Target,
		Operation:          policy.Operation,
		ProductionEligible: true,
	}
	if err := authorizer.Authorize(context.Background(), receipt, request); err != nil {
		t.Fatal(err)
	}
	request.ProductionEligible = false
	if err := authorizer.Authorize(context.Background(), receipt, request); err == nil {
		t.Fatal("PolicyAuthorizer accepted a fixture request")
	}
	request.ProductionEligible = true
	receipt.PolicySHA256 = strings.Repeat("c", 64)
	if err := authorizer.Authorize(context.Background(), receipt, request); err == nil {
		t.Fatal("PolicyAuthorizer accepted a different policy digest")
	}
}

func TestCanonicalConfigurationRejectsUnknownAndNoncanonicalInput(t *testing.T) {
	config, _ := validConfigAndPolicy()
	data, err := intake.CanonicalJSON(config)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var loaded Config
	if _, err := LoadCanonicalFile(filename, digest(data), &loaded); err != nil {
		t.Fatal(err)
	}
	noncanonical := append([]byte("{ \"schema\":"), data[len(`{"schema":`):]...)
	if err := os.WriteFile(filename, noncanonical, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCanonicalFile(filename, digest(noncanonical), &loaded); err == nil {
		t.Fatal("LoadCanonicalFile() accepted noncanonical JSON")
	}
	unknown := append(data[:len(data)-1], []byte(`,"unknown":true}`)...)
	if err := os.WriteFile(filename, unknown, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCanonicalFile(filename, digest(unknown), &loaded); err == nil {
		t.Fatal("LoadCanonicalFile() accepted an unknown field")
	}
}

func TestExecutionPublishesOneExactReceiptWithInReleaseLastAndReplays(t *testing.T) {
	ctx := context.Background()
	intakeRoot := t.TempDir()
	intakeStore, err := intake.OpenFileStore(intakeRoot)
	if err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(
		"..",
		"..",
		"test",
		"fixtures",
		"debs",
		"repogen-test_1.0.0_amd64.deb",
	)
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := deb.ParsePackage(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	artifactSHA256 := digest(artifact)
	provenanceValue := provenance{
		Schema:   provenanceSchema,
		Evidence: "fixture",
		Producer: provenanceProducer{
			Repository: "frostyard/gchlog",
			Commit:     strings.Repeat("1", 40),
			Tree:       strings.Repeat("2", 40),
			Ref:        "refs/heads/fixture",
		},
		Builder: provenanceBuilder{
			Mode:                     "fixture",
			GoVersion:                "go1.27.0",
			GoReleaserVersion:        "2.18.1",
			GoReleaserBinarySHA256:   strings.Repeat("3", 64),
			Target:                   "linux/amd64",
			InstallVerificationImage: "fixture@sha256:" + strings.Repeat("4", 64),
			SourceCommitTimestampUTC: "2026-09-17T12:00:00Z",
		},
		Artifact: provenanceArtifact{
			Architecture: parsed.Architecture,
			Filename:     filepath.Base(artifactPath),
			MediaType:    "application/vnd.debian.binary-package",
			PackageName:  parsed.Name,
			SHA256:       artifactSHA256,
			Size:         int64(len(artifact)),
			Version:      parsed.Version,
		},
		SnapshotArtifacts: []snapshotArtifact{{
			MediaType:         "application/vnd.debian.binary-package",
			Name:              filepath.Base(artifactPath),
			SelectedForIntake: true,
			SHA256:            artifactSHA256,
			Size:              int64(len(artifact)),
		}},
	}
	provenanceData, err := intake.CanonicalJSON(provenanceValue)
	if err != nil {
		t.Fatal(err)
	}
	provenanceSHA256 := digest(provenanceData)
	if err := intake.CreateImmutable(
		ctx,
		intakeStore,
		"manifests/provenance/v1/sha256/"+provenanceSHA256+".json",
		provenanceData,
	); err != nil {
		t.Fatal(err)
	}
	if err := intake.CreateImmutable(
		ctx,
		intakeStore,
		"blobs/sha256/"+artifactSHA256[:2]+"/"+artifactSHA256,
		artifact,
	); err != nil {
		t.Fatal(err)
	}
	request := intake.Request{
		Schema:             intake.RequestSchema,
		Kind:               "debian",
		Operation:          "initialize",
		Target:             "trixie",
		Producer:           "frostyard/gchlog",
		ProvenanceDigest:   provenanceSHA256,
		ArtifactDigests:    []string{artifactSHA256},
		Codename:           "trixie",
		Suite:              "trixie",
		Origin:             "Repogen Repository",
		Label:              "Frostyard Repository",
		Component:          "main",
		Architectures:      []string{"all", "amd64"},
		ValidUntilPolicy:   "omit",
		ProductionEligible: true,
	}
	requestData, err := intake.CanonicalJSON(request)
	if err != nil {
		t.Fatal(err)
	}
	requestSHA256 := digest(requestData)

	privateKeyPath := filepath.Join("..", "..", "test", "fixtures", "gpg-keys", "test-key.asc")
	gpgSigner, err := signer.NewGPGSigner(privateKeyPath, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gpgSigner.Close() }()
	publicKey, err := gpgSigner.GetPublicKey()
	if err != nil {
		t.Fatal(err)
	}
	config, policy := validConfigAndPolicy()
	config.RequestSHA256 = requestSHA256
	policy.RequestSHA256 = requestSHA256
	policy.ProvenanceSHA256 = provenanceSHA256
	policy.ProducerCommit = provenanceValue.Producer.Commit
	policy.ProducerTree = provenanceValue.Producer.Tree
	policy.SigningKeyFingerprint = fingerprintForTest(t, publicKey)
	policy.SigningPublicKeySHA256 = digest(publicKey)
	policy.AllowedPoolObjects = []ApprovedObject{{
		Path:   poolPath(provenanceValue.Artifact),
		SHA256: artifactSHA256,
		Size:   int64(len(artifact)),
	}}
	executable := filepath.Join(t.TempDir(), "repogen")
	executableBytes := []byte("fixture repogen executable")
	if err := os.WriteFile(executable, executableBytes, 0o700); err != nil {
		t.Fatal(err)
	}
	policy.RepogenSHA256 = digest(executableBytes)
	policyData, err := intake.CanonicalJSON(policy)
	if err != nil {
		t.Fatal(err)
	}
	policySHA256 := digest(policyData)
	if _, err := (intake.Recorder{Store: intakeStore}).Accept(
		ctx,
		request.Producer,
		"exact-operation",
		policySHA256,
		request,
	); err != nil {
		t.Fatal(err)
	}

	publication := newMemoryPublicationStore()
	execution := Execution{
		Config:         config,
		ConfigSHA256:   canonicalDigestForTest(t, config),
		Policy:         policy,
		PolicySHA256:   policySHA256,
		RequestSHA256:  requestSHA256,
		ProvenanceHash: provenanceSHA256,
		Intake:         intakeStore,
		Publication:    publication,
		Signer:         gpgSigner,
		WorkDir:        t.TempDir(),
		ExecutablePath: executable,
		Version:        policy.RepogenVersion,
		Commit:         policy.ActionCommit,
	}
	if err := execution.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(publication.writes) == 0 {
		t.Fatal("production execution performed no publication writes")
	}
	if got := publication.writes[len(publication.writes)-1]; got != "dists/trixie/InRelease" {
		t.Fatalf("final publication write = %q, want dists/trixie/InRelease", got)
	}
	inReleaseCount := 0
	for _, key := range publication.writes {
		if key == "dists/trixie/InRelease" {
			inReleaseCount++
		}
		if strings.HasPrefix(key, "dists/stable/") ||
			strings.HasPrefix(key, "dists/forky/") ||
			strings.HasPrefix(key, "ext/") ||
			key == "public.key" {
			t.Fatalf("production execution wrote forbidden key %q", key)
		}
	}
	if inReleaseCount != 1 {
		t.Fatalf("InRelease write count = %d, want exactly 1", inReleaseCount)
	}
	writes := len(publication.writes)
	if err := execution.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(publication.writes) != writes {
		t.Fatalf("replay performed %d additional writes", len(publication.writes)-writes)
	}
}

func canonicalDigestForTest(t *testing.T, value any) string {
	t.Helper()
	data, err := intake.CanonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	return digest(data)
}

func validConfigAndPolicy() (Config, Policy) {
	request := strings.Repeat("a", 64)
	config := Config{
		Schema:             ConfigSchema,
		AccountID:          "account123",
		Endpoint:           "https://account123.r2.cloudflarestorage.com",
		Region:             "auto",
		PublicationBucket:  "repository",
		PublicationPrefix:  "repo",
		IntakeBucket:       "intake",
		IntakePrefix:       "records",
		CoordinationBucket: "coordination",
		CoordinationPrefix: "locks",
		Target:             "trixie",
		RequestSHA256:      request,
	}
	policy := Policy{
		Schema:                     PolicySchema,
		Operator:                   "fixture-operator",
		Target:                     "trixie",
		Operation:                  "initialize",
		RequestSHA256:              request,
		ProvenanceSHA256:           strings.Repeat("b", 64),
		Producer:                   "frostyard/gchlog",
		ProducerCommit:             strings.Repeat("1", 40),
		ProducerTree:               strings.Repeat("2", 40),
		ActionCommit:               strings.Repeat("3", 40),
		RepogenVersion:             "0.6.0",
		RepogenSHA256:              strings.Repeat("c", 64),
		SigningKeyFingerprint:      "0123456789ABCDEF0123456789ABCDEF01234567",
		SigningPublicKeySHA256:     strings.Repeat("d", 64),
		ReleaseTime:                "2026-09-17T12:00:00Z",
		AuthoritativeTargetAbsent:  true,
		ProviderPermissionEvidence: "fixture-provider-evidence",
		AllowedPoolObjects: []ApprovedObject{{
			Path:   "pool/main/f/frostyard-gchlog/frostyard-gchlog_0.1.1-1+fy13u1_amd64.deb",
			SHA256: strings.Repeat("e", 64),
			Size:   100,
		}},
	}
	return config, policy
}

func readTestPublicKey(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(
		filepath.Join("..", "..", "test", "fixtures", "gpg-keys", "test-key-pub.asc"),
	)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func fingerprintForTest(t *testing.T, publicKey []byte) string {
	t.Helper()
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(publicKey))
	if err != nil || len(entities) != 1 {
		t.Fatalf("parse test key: %v", err)
	}
	return strings.ToUpper(strings.TrimSpace(
		fmt.Sprintf("%X", entities[0].PrimaryKey.Fingerprint),
	))
}

type publicKeySigner struct {
	publicKey []byte
}

func (s publicKeySigner) SignCleartext([]byte) ([]byte, error) {
	return nil, errors.New("not implemented")
}

func (s publicKeySigner) SignDetached([]byte) ([]byte, error) {
	return nil, errors.New("not implemented")
}

func (s publicKeySigner) SignDetachedBinary([]byte) ([]byte, error) {
	return nil, errors.New("not implemented")
}

func (s publicKeySigner) SignDetachedBinaryFromFile(string) ([]byte, error) {
	return nil, errors.New("not implemented")
}

func (s publicKeySigner) GetPublicKey() ([]byte, error) {
	return append([]byte(nil), s.publicKey...), nil
}

type countingIntakeStore struct {
	calls int
}

func (s *countingIntakeStore) CreateIfAbsent(context.Context, string, io.Reader, int64) (bool, error) {
	s.calls++
	return false, errors.New("unexpected")
}

func (s *countingIntakeStore) Open(context.Context, string) (io.ReadCloser, error) {
	s.calls++
	return nil, errors.New("unexpected")
}

func (s *countingIntakeStore) List(context.Context, string) ([]string, error) {
	s.calls++
	return nil, errors.New("unexpected")
}

func (s *countingIntakeStore) Acquire(context.Context, string) (intake.Lock, error) {
	s.calls++
	return nil, errors.New("unexpected")
}

type countingPublicationStore struct {
	calls int
}

type memoryPublicationStore struct {
	mu      sync.Mutex
	objects map[string][]byte
	writes  []string
}

func newMemoryPublicationStore() *memoryPublicationStore {
	return &memoryPublicationStore{objects: make(map[string][]byte)}
}

func (s *memoryPublicationStore) Open(
	_ context.Context,
	key string,
) (*deb.RemotePoolObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.objects[key]
	if !ok {
		return nil, deb.ErrPoolObjectNotFound
	}
	return &deb.RemotePoolObject{
		Body: io.NopCloser(bytes.NewReader(append([]byte(nil), body...))),
		ETag: "fixture",
	}, nil
}

func (s *memoryPublicationStore) Create(
	_ context.Context,
	key string,
	body io.Reader,
	size int64,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.objects[key]; exists {
		return deb.ErrPoolObjectExists
	}
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return errors.New("size mismatch")
	}
	s.objects[key] = data
	s.writes = append(s.writes, key)
	return nil
}

func (s *memoryPublicationStore) Replace(
	context.Context,
	string,
	io.Reader,
	int64,
	string,
) error {
	return errors.New("unexpected replace")
}

func (s *memoryPublicationStore) PrefixExists(
	_ context.Context,
	prefix string,
) (bool, error) {
	keys, err := s.ListPrefix(context.Background(), prefix)
	return len(keys) > 0, err
}

func (s *memoryPublicationStore) ListPrefix(
	_ context.Context,
	prefix string,
) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var keys []string
	for key := range s.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func (s *memoryPublicationStore) Acquire(
	context.Context,
	string,
) (deb.ProductionPublicationLock, error) {
	return noOpLock{}, nil
}

type noOpLock struct{}

func (noOpLock) Release() error {
	return nil
}

func (s *countingPublicationStore) Open(context.Context, string) (*deb.RemotePoolObject, error) {
	s.calls++
	return nil, errors.New("unexpected")
}

func (s *countingPublicationStore) Create(context.Context, string, io.Reader, int64) error {
	s.calls++
	return errors.New("unexpected")
}

func (s *countingPublicationStore) Replace(context.Context, string, io.Reader, int64, string) error {
	s.calls++
	return errors.New("unexpected")
}

func (s *countingPublicationStore) PrefixExists(context.Context, string) (bool, error) {
	s.calls++
	return false, errors.New("unexpected")
}

func (s *countingPublicationStore) ListPrefix(context.Context, string) ([]string, error) {
	s.calls++
	return nil, errors.New("unexpected")
}

func (s *countingPublicationStore) Acquire(context.Context, string) (deb.ProductionPublicationLock, error) {
	s.calls++
	return nil, errors.New("unexpected")
}

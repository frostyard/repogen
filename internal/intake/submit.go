package intake

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
)

// ErrScope reports a submit-path write outside the producer prefixes.
var ErrScope = errors.New("durable intake submit out of scope")

var submitKeyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^blobs/sha256/([0-9a-f]{2})/([0-9a-f]{64})$`),
	regexp.MustCompile(`^manifests/provenance/v1/sha256/[0-9a-f]{64}\.json$`),
	regexp.MustCompile(`^manifests/request/v1/sha256/[0-9a-f]{64}\.json$`),
	regexp.MustCompile(`^submissions/v1/sha256/[0-9a-f]{64}\.json$`),
	regexp.MustCompile(`^receipts/v1/debian/trixie/[0-9]{20}-[0-9a-f]{64}\.json$`),
}

// SubmitOnly wraps store so that the only write it exposes is CreateIfAbsent
// under the T0 producer prefixes: blobs, provenance and request manifests,
// submission pointers and debian/trixie receipts. Every other key fails with
// ErrScope before the wrapped store is called. Open, List and Acquire
// delegate unchanged.
func SubmitOnly(store Store) Store {
	return submitOnlyStore{inner: store}
}

type submitOnlyStore struct {
	inner Store
}

func (s submitOnlyStore) CreateIfAbsent(
	ctx context.Context,
	key string,
	body io.Reader,
	size int64,
) (bool, error) {
	if !submitKeyAllowed(key) {
		return false, fmt.Errorf("%w: %q", ErrScope, key)
	}
	return s.inner.CreateIfAbsent(ctx, key, body, size)
}

func (s submitOnlyStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.inner.Open(ctx, key)
}

func (s submitOnlyStore) List(ctx context.Context, prefix string) ([]string, error) {
	return s.inner.List(ctx, prefix)
}

func (s submitOnlyStore) Acquire(ctx context.Context, target string) (Lock, error) {
	return s.inner.Acquire(ctx, target)
}

func submitKeyAllowed(key string) bool {
	for index, pattern := range submitKeyPatterns {
		match := pattern.FindStringSubmatch(key)
		if match == nil {
			continue
		}
		// A blob's fan-out directory must be its digest's first two characters.
		return index != 0 || strings.HasPrefix(match[2], match[1])
	}
	return false
}

// Artifact is one producer artifact named by its expected digest and size.
// Open is called once: Submit copies the bytes into a private snapshot,
// verifies it, and uploads only from that snapshot.
type Artifact struct {
	SHA256 string
	Size   int64
	Open   func() (io.ReadCloser, error)
}

// Submission is everything one producer submits for one request.
type Submission struct {
	Principal     string
	SubmissionKey string
	PolicySHA256  string
	Request       Request
	Provenance    []byte
	Artifacts     []Artifact
}

// Submit validates a complete submission before any write, then stores its
// artifacts and provenance as create-if-absent digest-addressed objects and
// records the request, submission pointer and receipt through Recorder.Accept.
// Every write goes through SubmitOnly. Replaying the same request with the
// same submission key returns the existing receipt.
func Submit(ctx context.Context, store Store, s Submission) (*Receipt, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: intake store is required", ErrState)
	}
	snapshotDir, err := os.MkdirTemp("", "repogen-submit-*")
	if err != nil {
		return nil, fmt.Errorf("%w: create private artifact snapshot: %v", ErrState, err)
	}
	defer func() { _ = os.RemoveAll(snapshotDir) }()
	if err := os.Chmod(snapshotDir, 0o700); err != nil {
		return nil, fmt.Errorf("%w: protect artifact snapshot: %v", ErrState, err)
	}
	snapshots, err := validateSubmission(ctx, s, snapshotDir)
	if err != nil {
		return nil, err
	}
	scoped := SubmitOnly(store)
	for index, artifact := range s.Artifacts {
		if err := storeArtifact(ctx, scoped, artifact, snapshots[index]); err != nil {
			return nil, err
		}
	}
	if err := CreateImmutable(
		ctx,
		scoped,
		path.Join("manifests/provenance/v1/sha256", s.Request.ProvenanceDigest+".json"),
		s.Provenance,
	); err != nil {
		return nil, err
	}
	return Recorder{Store: scoped}.Accept(
		ctx,
		s.Principal,
		s.SubmissionKey,
		s.PolicySHA256,
		s.Request,
	)
}

func validateSubmission(ctx context.Context, s Submission, snapshotDir string) ([]string, error) {
	request := s.Request
	for name, value := range map[string]string{
		"target":   request.Target,
		"codename": request.Codename,
		"suite":    request.Suite,
	} {
		if strings.EqualFold(strings.TrimSpace(value), "stable") {
			return nil, fmt.Errorf("%w: request %s 'stable' is refused: the signed stable suite is frozen", ErrScope, name)
		}
		if value != "trixie" {
			return nil, fmt.Errorf("%w: request %s must be exactly trixie", ErrScope, name)
		}
	}
	if request.Kind != "debian" {
		return nil, fmt.Errorf("%w: request kind must be debian", ErrScope)
	}
	if err := validateRequest(request); err != nil {
		return nil, err
	}
	if s.Principal == "" || s.Principal != request.Producer {
		return nil, fmt.Errorf("%w: authenticated producer does not match request", ErrState)
	}
	if !safeSegment(s.SubmissionKey) || !validDigest(s.PolicySHA256) {
		return nil, fmt.Errorf("%w: invalid submission key or policy digest", ErrState)
	}
	canonical, err := canonicalizeJSON(s.Provenance)
	if err != nil || !bytes.Equal(canonical, s.Provenance) {
		return nil, fmt.Errorf("%w: provenance is not canonical JSON", ErrIntegrity)
	}
	if len(s.Provenance) > maxRecordSize {
		return nil, fmt.Errorf("%w: provenance is too large", ErrIntegrity)
	}
	if digestBytes(s.Provenance) != request.ProvenanceDigest {
		return nil, fmt.Errorf("%w: provenance digest does not match the request", ErrIntegrity)
	}

	wanted := make(map[string]struct{}, len(request.ArtifactDigests))
	for _, digest := range request.ArtifactDigests {
		wanted[digest] = struct{}{}
	}
	if len(s.Artifacts) != len(wanted) {
		return nil, fmt.Errorf("%w: submitted artifacts do not match the request digests", ErrIntegrity)
	}
	seen := make(map[string]struct{}, len(s.Artifacts))
	snapshots := make([]string, 0, len(s.Artifacts))
	for _, artifact := range s.Artifacts {
		if _, ok := wanted[artifact.SHA256]; !ok {
			return nil, fmt.Errorf("%w: artifact %s is not in the request", ErrIntegrity, artifact.SHA256)
		}
		if _, duplicate := seen[artifact.SHA256]; duplicate {
			return nil, fmt.Errorf("%w: artifact %s is submitted twice", ErrIntegrity, artifact.SHA256)
		}
		seen[artifact.SHA256] = struct{}{}
		snapshot, err := snapshotArtifact(ctx, artifact, snapshotDir)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

// snapshotArtifact copies the artifact into a private file while hashing it,
// before any write. Only the verified snapshot is uploaded, so bytes that
// change at the source after verification can never occupy the
// create-if-absent key of the digest they claim.
func snapshotArtifact(ctx context.Context, artifact Artifact, directory string) (string, error) {
	if artifact.Open == nil || artifact.Size < 0 || !validDigest(artifact.SHA256) {
		return "", fmt.Errorf("%w: artifact %s has no readable body", ErrState, artifact.SHA256)
	}
	body, err := artifact.Open()
	if err != nil {
		return "", fmt.Errorf("%w: open artifact %s: %v", ErrState, artifact.SHA256, err)
	}
	defer func() { _ = body.Close() }()
	snapshot, err := os.CreateTemp(directory, "artifact-")
	if err != nil {
		return "", fmt.Errorf("%w: create artifact snapshot: %v", ErrState, err)
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(snapshot, hash), &contextReader{
		ctx:    ctx,
		reader: io.LimitReader(body, artifact.Size+1),
	})
	closeErr := snapshot.Close()
	if copyErr != nil {
		return "", fmt.Errorf("%w: read artifact %s: %v", ErrState, artifact.SHA256, copyErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("%w: write artifact snapshot: %v", ErrState, closeErr)
	}
	if size != artifact.Size || hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
		return "", fmt.Errorf("%w: artifact bytes do not match digest %s", ErrIntegrity, artifact.SHA256)
	}
	return snapshot.Name(), nil
}

func storeArtifact(ctx context.Context, store Store, artifact Artifact, snapshot string) error {
	body, err := os.Open(snapshot)
	if err != nil {
		return fmt.Errorf("%w: open artifact snapshot: %v", ErrState, err)
	}
	defer func() { _ = body.Close() }()
	return CreateImmutableStream(
		ctx,
		store,
		path.Join("blobs/sha256", artifact.SHA256[:2], artifact.SHA256),
		body,
		artifact.Size,
		artifact.SHA256,
	)
}

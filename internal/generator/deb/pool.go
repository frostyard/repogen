package deb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"unicode"
)

var (
	// ErrPoolObjectNotFound is returned by a PoolObjectStore when a key does
	// not exist. Implementations must not use it for permission or read errors.
	ErrPoolObjectNotFound = errors.New("pool object not found")
	// ErrPoolObjectExists is returned by a PoolObjectStore when a conditional
	// create loses a race. Implementations must never overwrite the object.
	ErrPoolObjectExists = errors.New("pool object already exists")
	// ErrPoolCollision means a pool path is already bound to different bytes.
	ErrPoolCollision = errors.New("pool object collision")
	// ErrPoolUnreadable means existing immutable bytes could not be read in
	// full and therefore could not be trusted or reused.
	ErrPoolUnreadable = errors.New("pool object unreadable")
	// ErrPoolCreate means conditional creation failed for a reason other than
	// the object already existing.
	ErrPoolCreate = errors.New("pool object create failed")
	// ErrPoolCandidate means the requested path, digest, size, or content is
	// invalid before any remote operation.
	ErrPoolCandidate = errors.New("invalid pool candidate")
)

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// RemotePoolObject is an opened object from immutable pool storage. ETag is
// informational only and is never used as a content digest.
type RemotePoolObject struct {
	Body io.ReadCloser
	ETag string
}

// PoolObjectStore is the minimum provider-neutral storage boundary required
// for immutable shared-pool handling. Create must be an atomic create-if-absent
// operation and return ErrPoolObjectExists without replacing existing bytes.
type PoolObjectStore interface {
	Open(ctx context.Context, key string) (*RemotePoolObject, error)
	Create(ctx context.Context, key string, body io.Reader, size int64) error
}

// PoolDigest is digest authority obtained from a signature- and
// checksum-verified Packages index.
type PoolDigest struct {
	SHA256 string
	Size   int64
}

// PoolCandidate describes one package staged for the shared pool. Content
// must be a stable, seekable staging stream for the duration of Ensure.
type PoolCandidate struct {
	Path    string
	SHA256  string
	Size    int64
	Content io.ReadSeeker
}

// PoolDisposition describes whether Ensure created or safely reused an
// immutable pool object.
type PoolDisposition string

const (
	// PoolCreated means the object was conditionally created.
	PoolCreated PoolDisposition = "created"
	// PoolReused means existing bytes were streamed and matched exactly.
	PoolReused PoolDisposition = "reused"
)

// SharedPool reconciles candidate package bytes against verified retained
// metadata and an immutable object store.
type SharedPool struct {
	store    PoolObjectStore
	verified map[string]PoolDigest
}

// NewSharedPool constructs a shared-pool reconciler from verified retained
// index authority. The map is copied so callers cannot change authority during
// reconciliation.
func NewSharedPool(store PoolObjectStore, verified map[string]PoolDigest) (*SharedPool, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: object store is required", ErrPoolCandidate)
	}

	copied := make(map[string]PoolDigest, len(verified))
	for key, digest := range verified {
		if err := validatePoolObject(key, digest); err != nil {
			return nil, fmt.Errorf("verified digest map: %w", err)
		}
		copied[key] = digest
	}

	return &SharedPool{
		store:    store,
		verified: copied,
	}, nil
}

// Ensure verifies candidate bytes before conditionally creating or reusing an
// immutable shared-pool object. Existing objects are always stream-hashed;
// ETags and other provider metadata are not content authority.
func (p *SharedPool) Ensure(ctx context.Context, candidate PoolCandidate) (PoolDisposition, error) {
	digest := PoolDigest{SHA256: candidate.SHA256, Size: candidate.Size}
	if err := validatePoolObject(candidate.Path, digest); err != nil {
		return "", err
	}
	if candidate.Content == nil {
		return "", fmt.Errorf("%w: %s has no staged content", ErrPoolCandidate, candidate.Path)
	}

	observed, err := hashSeekable(ctx, candidate.Content)
	if err != nil {
		return "", fmt.Errorf("%w: cannot hash staged content for %s: %w", ErrPoolCandidate, candidate.Path, err)
	}
	if observed != digest {
		return "", fmt.Errorf(
			"%w: staged content for %s is sha256 %s size %d, expected sha256 %s size %d",
			ErrPoolCandidate,
			candidate.Path,
			observed.SHA256,
			observed.Size,
			digest.SHA256,
			digest.Size,
		)
	}

	if retained, indexed := p.verified[candidate.Path]; indexed {
		if retained != digest {
			return "", fmt.Errorf(
				"%w: verified index binds %s to sha256 %s size %d, incoming bytes are sha256 %s size %d",
				ErrPoolCollision,
				candidate.Path,
				retained.SHA256,
				retained.Size,
				digest.SHA256,
				digest.Size,
			)
		}
		if err := p.verifyExisting(ctx, candidate.Path, retained); err != nil {
			return "", err
		}
		return PoolReused, nil
	}

	if _, err := candidate.Content.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("%w: cannot rewind staged content for %s: %w", ErrPoolCandidate, candidate.Path, err)
	}
	uploadHasher := sha256.New()
	err = p.store.Create(
		ctx,
		candidate.Path,
		&contextReader{
			ctx:    ctx,
			reader: io.TeeReader(io.LimitReader(candidate.Content, candidate.Size), uploadHasher),
		},
		candidate.Size,
	)
	if err == nil {
		offset, seekErr := candidate.Content.Seek(0, io.SeekCurrent)
		if seekErr != nil {
			return "", fmt.Errorf("%w: cannot confirm upload length for %s: %w", ErrPoolCreate, candidate.Path, seekErr)
		}
		uploadedSHA256 := hex.EncodeToString(uploadHasher.Sum(nil))
		if offset != candidate.Size || uploadedSHA256 != candidate.SHA256 {
			return "", fmt.Errorf(
				"%w: conditional create for %s consumed sha256 %s size %d, expected sha256 %s size %d",
				ErrPoolCreate,
				candidate.Path,
				uploadedSHA256,
				offset,
				candidate.SHA256,
				candidate.Size,
			)
		}
		return PoolCreated, nil
	}
	if !errors.Is(err, ErrPoolObjectExists) {
		return "", fmt.Errorf("%w: %s: %w", ErrPoolCreate, candidate.Path, err)
	}

	if err := p.verifyExisting(ctx, candidate.Path, digest); err != nil {
		return "", err
	}
	return PoolReused, nil
}

func (p *SharedPool) verifyExisting(ctx context.Context, key string, expected PoolDigest) error {
	object, err := p.store.Open(ctx, key)
	if err != nil {
		return fmt.Errorf("%w: cannot open %s: %w", ErrPoolUnreadable, key, err)
	}
	if object == nil || object.Body == nil {
		return fmt.Errorf("%w: object store returned no body for %s", ErrPoolUnreadable, key)
	}

	observed, err := hashRemote(ctx, object.Body)
	if err != nil {
		return fmt.Errorf("%w: cannot hash %s: %w", ErrPoolUnreadable, key, err)
	}
	if observed != expected {
		return fmt.Errorf(
			"%w: %s contains sha256 %s size %d, expected sha256 %s size %d",
			ErrPoolCollision,
			key,
			observed.SHA256,
			observed.Size,
			expected.SHA256,
			expected.Size,
		)
	}
	return nil
}

func validatePoolObject(key string, digest PoolDigest) error {
	const prefix = "pool/main/"
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, `\`) ||
		strings.IndexFunc(key, unicode.IsControl) >= 0 ||
		path.Clean(key) != key ||
		!strings.HasPrefix(key, prefix) {
		return fmt.Errorf("%w: unsafe shared-pool path %q", ErrPoolCandidate, key)
	}

	parts := strings.Split(strings.TrimPrefix(key, prefix), "/")
	if len(parts) != 3 ||
		parts[0] == "" ||
		parts[1] == "" ||
		(parts[2] != parts[1]+".deb" && !strings.HasPrefix(parts[2], parts[1]+"_")) ||
		!strings.HasSuffix(parts[2], ".deb") {
		return fmt.Errorf("%w: unsafe shared-pool path %q", ErrPoolCandidate, key)
	}
	expectedShard := "0"
	if parts[1][0] >= 'a' && parts[1][0] <= 'z' {
		expectedShard = parts[1][:1]
	}
	if parts[0] != expectedShard {
		return fmt.Errorf(
			"%w: shared-pool path %q uses shard %q, expected %q",
			ErrPoolCandidate,
			key,
			parts[0],
			expectedShard,
		)
	}
	if !sha256Pattern.MatchString(digest.SHA256) {
		return fmt.Errorf("%w: %s has invalid lowercase SHA-256 %q", ErrPoolCandidate, key, digest.SHA256)
	}
	if digest.Size < 0 {
		return fmt.Errorf("%w: %s has negative size %d", ErrPoolCandidate, key, digest.Size)
	}
	return nil
}

func hashSeekable(ctx context.Context, content io.ReadSeeker) (PoolDigest, error) {
	if _, err := content.Seek(0, io.SeekStart); err != nil {
		return PoolDigest{}, err
	}
	digest, err := hashReader(ctx, content)
	if err != nil {
		return PoolDigest{}, err
	}
	if _, err := content.Seek(0, io.SeekStart); err != nil {
		return PoolDigest{}, err
	}
	return digest, nil
}

func hashRemote(ctx context.Context, body io.ReadCloser) (PoolDigest, error) {
	digest, readErr := hashReader(ctx, body)
	closeErr := body.Close()
	if readErr != nil {
		return PoolDigest{}, readErr
	}
	if closeErr != nil {
		return PoolDigest{}, closeErr
	}
	return digest, nil
}

func hashReader(ctx context.Context, reader io.Reader) (PoolDigest, error) {
	hasher := sha256.New()
	size, err := io.Copy(hasher, &contextReader{ctx: ctx, reader: reader})
	if err != nil {
		return PoolDigest{}, err
	}
	return PoolDigest{
		SHA256: hex.EncodeToString(hasher.Sum(nil)),
		Size:   size,
	}, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(buffer)
	}
}

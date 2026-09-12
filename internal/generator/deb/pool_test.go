package deb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
)

const fixturePoolPath = "pool/main/r/repogen-test/repogen-test_1.0.0_amd64.deb"

func TestSharedPoolFakeS3IndexedReuseStreamsBytesAndIgnoresOpaqueETag(t *testing.T) {
	t.Parallel()

	content := []byte("indexed package bytes")
	store := newFakeS3Pool()
	store.objects[fixturePoolPath] = fakeS3Object{
		body: content,
		etag: `"opaque-multipart-etag-2"`,
	}
	pool := mustSharedPool(t, store, map[string]PoolDigest{
		fixturePoolPath: digestFor(content),
	})

	disposition, err := pool.Ensure(context.Background(), candidateFor(fixturePoolPath, content))
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if disposition != PoolReused {
		t.Fatalf("Ensure() disposition = %q, want %q", disposition, PoolReused)
	}
	if store.createCount() != 0 {
		t.Fatalf("conditional creates = %d, want 0 for an indexed object", store.createCount())
	}
	if store.openCount() != 1 {
		t.Fatalf("streamed opens = %d, want 1", store.openCount())
	}
}

func TestSharedPoolFakeS3CreatesUnindexedObjectConditionally(t *testing.T) {
	t.Parallel()

	content := []byte("new package bytes")
	store := newFakeS3Pool()
	pool := mustSharedPool(t, store, nil)

	disposition, err := pool.Ensure(context.Background(), candidateFor(fixturePoolPath, content))
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if disposition != PoolCreated {
		t.Fatalf("Ensure() disposition = %q, want %q", disposition, PoolCreated)
	}
	if got := store.objectBytes(fixturePoolPath); !bytes.Equal(got, content) {
		t.Fatalf("created bytes = %q, want %q", got, content)
	}
	if store.createCount() != 1 {
		t.Fatalf("conditional creates = %d, want 1", store.createCount())
	}
}

func TestSharedPoolFakeS3ReusesUnindexedObjectAfterCreateConflict(t *testing.T) {
	t.Parallel()

	content := []byte("already present but not indexed")
	store := newFakeS3Pool()
	store.objects[fixturePoolPath] = fakeS3Object{
		body: content,
		etag: `"not-a-content-digest"`,
	}
	pool := mustSharedPool(t, store, nil)

	disposition, err := pool.Ensure(context.Background(), candidateFor(fixturePoolPath, content))
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if disposition != PoolReused {
		t.Fatalf("Ensure() disposition = %q, want %q", disposition, PoolReused)
	}
	if store.createCount() != 1 {
		t.Fatalf("conditional creates = %d, want 1", store.createCount())
	}
	if store.openCount() != 1 {
		t.Fatalf("streamed opens = %d, want 1", store.openCount())
	}
}

func TestSharedPoolFakeS3FailsClosedOnCollisionAndUnreadableObjects(t *testing.T) {
	t.Parallel()

	incoming := []byte("incoming bytes")
	different := []byte("different retained bytes")

	tests := []struct {
		name     string
		verified map[string]PoolDigest
		prepare  func(*fakeS3Pool)
		want     error
	}{
		{
			name: "verified map conflicts with incoming digest",
			verified: map[string]PoolDigest{
				fixturePoolPath: digestFor(different),
			},
			prepare: func(*fakeS3Pool) {},
			want:    ErrPoolCollision,
		},
		{
			name: "indexed remote bytes conflict",
			verified: map[string]PoolDigest{
				fixturePoolPath: digestFor(incoming),
			},
			prepare: func(store *fakeS3Pool) {
				store.objects[fixturePoolPath] = fakeS3Object{body: different, etag: digestFor(incoming).SHA256}
			},
			want: ErrPoolCollision,
		},
		{
			name:     "unindexed remote bytes conflict",
			verified: nil,
			prepare: func(store *fakeS3Pool) {
				store.objects[fixturePoolPath] = fakeS3Object{body: different, etag: digestFor(incoming).SHA256}
			},
			want: ErrPoolCollision,
		},
		{
			name: "indexed object is missing",
			verified: map[string]PoolDigest{
				fixturePoolPath: digestFor(incoming),
			},
			prepare: func(*fakeS3Pool) {},
			want:    ErrPoolUnreadable,
		},
		{
			name: "indexed object read fails",
			verified: map[string]PoolDigest{
				fixturePoolPath: digestFor(incoming),
			},
			prepare: func(store *fakeS3Pool) {
				store.objects[fixturePoolPath] = fakeS3Object{body: incoming}
				store.readErrors[fixturePoolPath] = errors.New("fixture stream failed")
			},
			want: ErrPoolUnreadable,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := newFakeS3Pool()
			tt.prepare(store)
			pool := mustSharedPool(t, store, tt.verified)
			before, existedBefore := store.lookup(fixturePoolPath)

			_, err := pool.Ensure(context.Background(), candidateFor(fixturePoolPath, incoming))
			if !errors.Is(err, tt.want) {
				t.Fatalf("Ensure() error = %v, want errors.Is(%v)", err, tt.want)
			}
			after, existsAfter := store.lookup(fixturePoolPath)
			if existedBefore != existsAfter {
				t.Fatalf("object existence changed from %v to %v", existedBefore, existsAfter)
			}
			if existedBefore && !bytes.Equal(before.body, after.body) {
				t.Fatalf("existing object changed from %q to %q", before.body, after.body)
			}
		})
	}
}

func TestSharedPoolFakeS3HandlesConditionalCreateRaceByHashingWinner(t *testing.T) {
	t.Parallel()

	content := []byte("race winner bytes")
	store := newFakeS3Pool()
	store.raceObjects[fixturePoolPath] = fakeS3Object{
		body: content,
		etag: `"winner-etag-is-opaque"`,
	}
	pool := mustSharedPool(t, store, nil)

	disposition, err := pool.Ensure(context.Background(), candidateFor(fixturePoolPath, content))
	if err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	if disposition != PoolReused {
		t.Fatalf("Ensure() disposition = %q, want %q", disposition, PoolReused)
	}
	if got := store.objectBytes(fixturePoolPath); !bytes.Equal(got, content) {
		t.Fatalf("race winner bytes = %q, want %q", got, content)
	}
}

func TestSharedPoolFakeS3RejectsConditionalCreateRaceCollision(t *testing.T) {
	t.Parallel()

	incoming := []byte("incoming bytes")
	winner := []byte("different winner bytes")
	store := newFakeS3Pool()
	store.raceObjects[fixturePoolPath] = fakeS3Object{body: winner}
	pool := mustSharedPool(t, store, nil)

	_, err := pool.Ensure(context.Background(), candidateFor(fixturePoolPath, incoming))
	if !errors.Is(err, ErrPoolCollision) {
		t.Fatalf("Ensure() error = %v, want ErrPoolCollision", err)
	}
	if got := store.objectBytes(fixturePoolPath); !bytes.Equal(got, winner) {
		t.Fatalf("race winner was overwritten: %q", got)
	}
}

func TestSharedPoolFakeS3ConcurrentSameDigestNeverOverwrites(t *testing.T) {
	t.Parallel()

	content := []byte("concurrent package bytes")
	store := newFakeS3Pool()
	pool := mustSharedPool(t, store, nil)

	const writers = 8
	results := make(chan PoolDisposition, writers)
	errs := make(chan error, writers)
	var group sync.WaitGroup
	for i := 0; i < writers; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			disposition, err := pool.Ensure(context.Background(), candidateFor(fixturePoolPath, content))
			results <- disposition
			errs <- err
		}()
	}
	group.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Ensure() error = %v", err)
		}
	}
	created := 0
	reused := 0
	for disposition := range results {
		switch disposition {
		case PoolCreated:
			created++
		case PoolReused:
			reused++
		default:
			t.Fatalf("unexpected disposition %q", disposition)
		}
	}
	if created != 1 || reused != writers-1 {
		t.Fatalf("dispositions created=%d reused=%d, want created=1 reused=%d", created, reused, writers-1)
	}
	if got := store.objectBytes(fixturePoolPath); !bytes.Equal(got, content) {
		t.Fatalf("stored bytes = %q, want %q", got, content)
	}
}

func TestSharedPoolFakeS3CrossSuitePathRequiresIdenticalBytes(t *testing.T) {
	t.Parallel()

	trixieBytes := []byte("trixie package bytes")
	forkyBytes := []byte("forky package bytes")
	store := newFakeS3Pool()
	trixiePool := mustSharedPool(t, store, nil)
	forkyPool := mustSharedPool(t, store, nil)

	if disposition, err := trixiePool.Ensure(context.Background(), candidateFor(fixturePoolPath, trixieBytes)); err != nil || disposition != PoolCreated {
		t.Fatalf("Trixie Ensure() = %q, %v; want created, nil", disposition, err)
	}
	if disposition, err := forkyPool.Ensure(context.Background(), candidateFor(fixturePoolPath, trixieBytes)); err != nil || disposition != PoolReused {
		t.Fatalf("Forky same-byte Ensure() = %q, %v; want reused, nil", disposition, err)
	}
	if _, err := forkyPool.Ensure(context.Background(), candidateFor(fixturePoolPath, forkyBytes)); !errors.Is(err, ErrPoolCollision) {
		t.Fatalf("Forky different-byte Ensure() error = %v, want ErrPoolCollision", err)
	}
	if got := store.objectBytes(fixturePoolPath); !bytes.Equal(got, trixieBytes) {
		t.Fatalf("cross-suite collision overwrote shared bytes: %q", got)
	}
}

func TestSharedPoolRejectsInvalidCandidateBeforeFakeS3Access(t *testing.T) {
	t.Parallel()

	content := []byte("candidate bytes")
	candidate := candidateFor("../pool/main/package.deb", content)
	store := newFakeS3Pool()
	pool := mustSharedPool(t, store, nil)

	_, err := pool.Ensure(context.Background(), candidate)
	if !errors.Is(err, ErrPoolCandidate) {
		t.Fatalf("Ensure() error = %v, want ErrPoolCandidate", err)
	}
	if store.createCount() != 0 || store.openCount() != 0 {
		t.Fatalf("invalid candidate accessed fake S3: creates=%d opens=%d", store.createCount(), store.openCount())
	}

	candidate = candidateFor(fixturePoolPath, content)
	candidate.SHA256 = digestFor([]byte("not the candidate")).SHA256
	_, err = pool.Ensure(context.Background(), candidate)
	if !errors.Is(err, ErrPoolCandidate) {
		t.Fatalf("digest-mismatched Ensure() error = %v, want ErrPoolCandidate", err)
	}
	if store.createCount() != 0 || store.openCount() != 0 {
		t.Fatalf("digest-mismatched candidate accessed fake S3: creates=%d opens=%d", store.createCount(), store.openCount())
	}
}

func mustSharedPool(t *testing.T, store PoolObjectStore, verified map[string]PoolDigest) *SharedPool {
	t.Helper()
	pool, err := NewSharedPool(store, verified)
	if err != nil {
		t.Fatalf("NewSharedPool() error = %v", err)
	}
	return pool
}

func candidateFor(key string, body []byte) PoolCandidate {
	digest := digestFor(body)
	return PoolCandidate{
		Path:    key,
		SHA256:  digest.SHA256,
		Size:    digest.Size,
		Content: bytes.NewReader(body),
	}
}

func digestFor(body []byte) PoolDigest {
	sum := sha256.Sum256(body)
	return PoolDigest{
		SHA256: hex.EncodeToString(sum[:]),
		Size:   int64(len(body)),
	}
}

type fakeS3Object struct {
	body []byte
	etag string
}

type fakeS3Pool struct {
	mu          sync.Mutex
	objects     map[string]fakeS3Object
	raceObjects map[string]fakeS3Object
	readErrors  map[string]error
	creates     int
	opens       int
}

func newFakeS3Pool() *fakeS3Pool {
	return &fakeS3Pool{
		objects:     make(map[string]fakeS3Object),
		raceObjects: make(map[string]fakeS3Object),
		readErrors:  make(map[string]error),
	}
}

func (s *fakeS3Pool) Open(_ context.Context, key string) (*RemotePoolObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opens++

	object, ok := s.objects[key]
	if !ok {
		return nil, ErrPoolObjectNotFound
	}
	body := append([]byte(nil), object.body...)
	if readErr := s.readErrors[key]; readErr != nil {
		return &RemotePoolObject{
			Body: &fixtureFailingReadCloser{
				reader: bytes.NewReader(body),
				err:    readErr,
			},
			ETag: object.etag,
		}, nil
	}
	return &RemotePoolObject{
		Body: io.NopCloser(bytes.NewReader(body)),
		ETag: object.etag,
	}, nil
}

func (s *fakeS3Pool) Create(_ context.Context, key string, body io.Reader, size int64) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return fmt.Errorf("fixture create read %d bytes, want %d", len(data), size)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.creates++
	if winner, ok := s.raceObjects[key]; ok {
		s.objects[key] = fakeS3Object{
			body: append([]byte(nil), winner.body...),
			etag: winner.etag,
		}
		delete(s.raceObjects, key)
		return ErrPoolObjectExists
	}
	if _, exists := s.objects[key]; exists {
		return ErrPoolObjectExists
	}
	s.objects[key] = fakeS3Object{
		body: append([]byte(nil), data...),
		etag: `"fixture-etag-is-not-authority"`,
	}
	return nil
}

func (s *fakeS3Pool) objectBytes(key string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.objects[key].body...)
}

func (s *fakeS3Pool) lookup(key string) (fakeS3Object, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	object, ok := s.objects[key]
	object.body = append([]byte(nil), object.body...)
	return object, ok
}

func (s *fakeS3Pool) createCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.creates
}

func (s *fakeS3Pool) openCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opens
}

type fixtureFailingReadCloser struct {
	reader    *bytes.Reader
	err       error
	delivered bool
}

func (r *fixtureFailingReadCloser) Read(buffer []byte) (int, error) {
	if r.delivered {
		return 0, r.err
	}
	r.delivered = true
	if len(buffer) > 3 {
		buffer = buffer[:3]
	}
	return r.reader.Read(buffer)
}

func (r *fixtureFailingReadCloser) Close() error {
	return nil
}

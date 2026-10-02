package intake

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"sync"
	"testing"
)

type countingStore struct {
	Store
	mu     sync.Mutex
	writes []string
}

func (s *countingStore) CreateIfAbsent(ctx context.Context, key string, body io.Reader, size int64) (bool, error) {
	s.mu.Lock()
	s.writes = append(s.writes, key)
	s.mu.Unlock()
	return s.Store.CreateIfAbsent(ctx, key, body, size)
}

func (s *countingStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.writes)
}

func bytesArtifact(body []byte) Artifact {
	return Artifact{
		SHA256: digestBytes(body),
		Size:   int64(len(body)),
		Open:   func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(body)), nil },
	}
}

func fixtureSubmission() Submission {
	provenance := []byte(`{"builder":"fixture","producer":"frostyard/gchlog"}`)
	artifact := []byte("fixture trixie package\n")
	return Submission{
		Principal:     "frostyard/gchlog",
		SubmissionKey: "gchlog-v1.2.3-run-42",
		PolicySHA256:  strings.Repeat("c", 64),
		Provenance:    provenance,
		Artifacts:     []Artifact{bytesArtifact(artifact)},
		Request: Request{
			Schema:             RequestSchema,
			Kind:               "debian",
			Operation:          "initialize",
			Target:             "trixie",
			Producer:           "frostyard/gchlog",
			ProvenanceDigest:   digestBytes(provenance),
			ArtifactDigests:    []string{digestBytes(artifact)},
			Codename:           "trixie",
			Suite:              "trixie",
			Origin:             "Frostyard",
			Label:              "Frostyard",
			Component:          "main",
			Architectures:      []string{"amd64"},
			ValidUntilPolicy:   "omit",
			ProductionEligible: true,
		},
	}
}

func TestSubmitAcceptsAndReconcilerCanLoad(t *testing.T) {
	store := newFixtureFileStore(t)
	submission := fixtureSubmission()
	receipt, err := Submit(context.Background(), store, submission)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Kind != "debian" || receipt.Target != "trixie" || receipt.Sequence != 1 ||
		receipt.PolicySHA256 != submission.PolicySHA256 {
		t.Fatalf("unexpected receipt %+v", receipt)
	}
	keys, err := store.List(context.Background(), "receipts/v1/debian/trixie")
	if err != nil || len(keys) != 1 || keys[0] != receiptKey(*receipt) {
		t.Fatalf("receipt keys %v, %v", keys, err)
	}
	loaded, err := loadRequest(context.Background(), store, receipt.RequestSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Producer != "frostyard/gchlog" || loaded.Target != "trixie" {
		t.Fatalf("loaded %+v", loaded)
	}
	if err := verifyRequestObjects(context.Background(), store, loaded); err != nil {
		t.Fatal(err)
	}
}

func TestSubmitReplayReturnsSameReceipt(t *testing.T) {
	store := newFixtureFileStore(t)
	first, err := Submit(context.Background(), store, fixtureSubmission())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Submit(context.Background(), store, fixtureSubmission())
	if err != nil {
		t.Fatal(err)
	}
	if *first != *second {
		t.Fatalf("replay returned %+v, want %+v", second, first)
	}
	keys, _ := store.List(context.Background(), "receipts/v1/debian/trixie")
	if len(keys) != 1 {
		t.Fatalf("replay created receipts %v", keys)
	}
}

func TestSubmitReusedKeyDifferentBytesFails(t *testing.T) {
	store := newFixtureFileStore(t)
	if _, err := Submit(context.Background(), store, fixtureSubmission()); err != nil {
		t.Fatal(err)
	}
	changed := fixtureSubmission()
	changed.Request.Origin = "Other"
	if _, err := Submit(context.Background(), store, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("reused key returned %v", err)
	}
}

func TestSubmitExistingBlobDifferentBytesFails(t *testing.T) {
	store := newFixtureFileStore(t)
	submission := fixtureSubmission()
	digest := submission.Request.ArtifactDigests[0]
	other := []byte("different bytes at the same key\n")
	if _, err := store.CreateIfAbsent(context.Background(), path.Join("blobs/sha256", digest[:2], digest), bytes.NewReader(other), int64(len(other))); err != nil {
		t.Fatal(err)
	}
	if _, err := Submit(context.Background(), store, submission); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("returned %v", err)
	}
	if keys, _ := store.List(context.Background(), "receipts"); len(keys) != 0 {
		t.Fatalf("receipt written: %v", keys)
	}
}

func assertZeroWrites(t *testing.T, mutate func(*Submission), want error) {
	t.Helper()
	store := &countingStore{Store: newFixtureFileStore(t)}
	submission := fixtureSubmission()
	mutate(&submission)
	_, err := Submit(context.Background(), store, submission)
	if err == nil {
		t.Fatal("accepted")
	}
	if want != nil && !errors.Is(err, want) {
		t.Fatalf("returned %v, want %v", err, want)
	}
	if store.count() != 0 {
		t.Fatalf("wrote %v before refusing", store.writes)
	}
}

func TestSubmitRefusesStable(t *testing.T) {
	for name, mutate := range map[string]func(*Submission){
		"target stable":       func(s *Submission) { s.Request.Target = "stable" },
		"codename Stable pad": func(s *Submission) { s.Request.Codename = "Stable " },
		"suite STABLE":        func(s *Submission) { s.Request.Suite = "STABLE" },
		"all stable": func(s *Submission) {
			s.Request.Target, s.Request.Codename, s.Request.Suite = "stable", "stable", "stable"
		},
		"target forky": func(s *Submission) { s.Request.Target, s.Request.Codename, s.Request.Suite = "forky", "forky", "forky" },
	} {
		t.Run(name, func(t *testing.T) {
			store := &countingStore{Store: newFixtureFileStore(t)}
			submission := fixtureSubmission()
			mutate(&submission)
			_, err := Submit(context.Background(), store, submission)
			if err == nil {
				t.Fatal("accepted")
			}
			if strings.Contains(name, "stable") || strings.Contains(name, "Stable") || strings.Contains(name, "STABLE") {
				if !strings.Contains(err.Error(), "stable") {
					t.Fatalf("error does not name stable: %v", err)
				}
			}
			if store.count() != 0 {
				t.Fatalf("wrote %v", store.writes)
			}
		})
	}
}

func TestSubmitRejectsMalformed(t *testing.T) {
	cases := map[string]func(*Submission){
		"non-canonical provenance": func(s *Submission) {
			s.Provenance = []byte(`{"producer":"frostyard/gchlog","builder":"fixture"}`)
			s.Request.ProvenanceDigest = digestBytes(s.Provenance)
		},
		"provenance digest mismatch": func(s *Submission) { s.Request.ProvenanceDigest = strings.Repeat("e", 64) },
		"artifact bytes mismatch": func(s *Submission) {
			s.Artifacts[0].Open = func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("other bytes, same size!\n")), nil
			}
		},
		"artifact size mismatch": func(s *Submission) { s.Artifacts[0].Size++ },
		"missing artifact":       func(s *Submission) { s.Artifacts = nil },
		"extra artifact":         func(s *Submission) { s.Artifacts = append(s.Artifacts, bytesArtifact([]byte("extra"))) },
		"duplicate artifact": func(s *Submission) {
			s.Artifacts = append(s.Artifacts, s.Artifacts[0])
			s.Request.ArtifactDigests = append(s.Request.ArtifactDigests, digestBytes([]byte("x")))
		},
		"kind sysext":        func(s *Submission) { s.Request.Kind = "sysext" },
		"principal mismatch": func(s *Submission) { s.Principal = "frostyard/other" },
		"empty principal":    func(s *Submission) { s.Principal = "" },
		"bad submission key": func(s *Submission) { s.SubmissionKey = "../x" },
		"bad policy digest":  func(s *Submission) { s.PolicySHA256 = "abc" },
		"invalid request":    func(s *Submission) { s.Request.Schema = "x" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) { assertZeroWrites(t, mutate, nil) })
	}
}

func TestSubmitOnlyStoreRejectsOutOfScopeKeys(t *testing.T) {
	hex64 := strings.Repeat("a", 64)
	inner := &countingStore{Store: newFixtureFileStore(t)}
	store := SubmitOnly(inner)
	for _, key := range []string{
		"dists/trixie/InRelease",
		"pool/main/g/x.deb",
		"results/v1/" + hex64 + ".json",
		"attempts/v1/" + hex64 + "/x.json",
		"manifests/result/v1/sha256/" + hex64 + ".json",
		"receipts/v1/debian/stable/00000000000000000001-" + hex64 + ".json",
		"receipts/v1/sysext/trixie/00000000000000000001-" + hex64 + ".json",
		"../blobs/sha256/aa/" + hex64,
		"blobs/sha256/bb/" + hex64,
		"blobs/sha256/aa/" + hex64 + "/x",
		"",
	} {
		if _, err := store.CreateIfAbsent(context.Background(), key, strings.NewReader("x"), 1); !errors.Is(err, ErrScope) {
			t.Fatalf("%q returned %v", key, err)
		}
	}
	if inner.count() != 0 {
		t.Fatalf("inner store written: %v", inner.writes)
	}
	for _, key := range []string{
		"blobs/sha256/aa/" + hex64,
		"manifests/provenance/v1/sha256/" + hex64 + ".json",
		"manifests/request/v1/sha256/" + hex64 + ".json",
		"submissions/v1/sha256/" + hex64 + ".json",
		"receipts/v1/debian/trixie/00000000000000000001-" + hex64 + ".json",
	} {
		if !submitKeyAllowed(key) {
			t.Fatalf("%q refused", key)
		}
	}
}

func TestSubmitUploadsVerifiedSnapshotWhenSourceChanges(t *testing.T) {
	store := newFixtureFileStore(t)
	submission := fixtureSubmission()
	good := []byte("fixture trixie package\n")
	bad := []byte("FIXTURE TRIXIE PACKAGE\n")
	opens := 0
	submission.Artifacts[0].Open = func() (io.ReadCloser, error) {
		opens++
		if opens == 1 {
			return io.NopCloser(bytes.NewReader(good)), nil
		}
		return io.NopCloser(bytes.NewReader(bad)), nil
	}
	if _, err := Submit(context.Background(), store, submission); err != nil {
		t.Fatal(err)
	}
	if opens != 1 {
		t.Fatalf("source opened %d times; upload must come from the verified snapshot", opens)
	}
	digest := submission.Request.ArtifactDigests[0]
	if err := verifyDigestObject(context.Background(), store, path.Join("blobs/sha256", digest[:2], digest), digest); err != nil {
		t.Fatal(err)
	}
}

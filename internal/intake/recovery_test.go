package intake

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRecorderRetainsIdempotentSequencedReceipts(t *testing.T) {
	store := newFixtureFileStore(t)
	recorder := Recorder{Store: store}
	request := fixtureRequest(t, store, "trixie", "initialize", nil)
	policy := fixtureDigest("policy")

	first, err := recorder.Accept(context.Background(), request.Producer, "run-41", policy, request)
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	replayed, err := recorder.Accept(context.Background(), request.Producer, "run-41", policy, request)
	if err != nil {
		t.Fatalf("replayed Accept() error = %v", err)
	}
	if !reflect.DeepEqual(replayed, first) || first.Sequence != 1 {
		t.Fatalf("replayed receipt = %+v, first = %+v", replayed, first)
	}

	next := fixtureRequest(t, store, "forky", "initialize", nil)
	if _, err := recorder.Accept(
		context.Background(),
		next.Producer,
		"run-41",
		policy,
		next,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("submission-key reuse error = %v, want ErrConflict", err)
	}
	keys, err := store.List(context.Background(), receiptPrefix("debian", "trixie"))
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("receipt count = %d, want 1", len(keys))
	}
}

func TestRecorderConcurrentReplayCreatesOneReceipt(t *testing.T) {
	store := newFixtureFileStore(t)
	recorder := Recorder{Store: store}
	request := fixtureRequest(t, store, "trixie", "initialize", nil)
	policy := fixtureDigest("policy")

	var wait sync.WaitGroup
	errorsSeen := make([]error, 8)
	for index := range errorsSeen {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, errorsSeen[index] = recorder.Accept(
				context.Background(),
				request.Producer,
				"same-run",
				policy,
				request,
			)
		}(index)
	}
	wait.Wait()
	for _, err := range errorsSeen {
		if err != nil {
			t.Fatalf("concurrent Accept() error = %v", err)
		}
	}
	keys, err := store.List(context.Background(), receiptPrefix("debian", "trixie"))
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("receipt count = %d, want 1", len(keys))
	}
}

func TestRecorderDoesNotMisclassifySubmissionReadFailureAsConflict(t *testing.T) {
	base := newFixtureFileStore(t)
	request := fixtureRequest(t, base, "trixie", "initialize", nil)
	store := &faultStore{
		Store:      base,
		readPrefix: "submissions/v1/",
		readErr:    errors.New("503 unavailable"),
	}

	_, err := (Recorder{Store: store}).Accept(
		context.Background(),
		request.Producer,
		"run-1",
		fixtureDigest("policy"),
		request,
	)
	if err == nil {
		t.Fatal("Accept() unexpectedly succeeded")
	}
	if errors.Is(err, ErrConflict) {
		t.Fatalf("submission read failure misclassified as ErrConflict: %v", err)
	}
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("submission read failure = %v, want ErrIntegrity", err)
	}
}

func TestReconcilerEnumeratesMissedWakeupsInOrderAndReplaysAsNoOp(t *testing.T) {
	store := newFixtureFileStore(t)
	recorder := Recorder{Store: store}
	policy := fixtureDigest("policy")

	firstRequest := fixtureRequest(t, store, "trixie", "initialize", nil)
	firstReceipt, err := recorder.Accept(
		context.Background(),
		firstRequest.Producer,
		"run-1",
		policy,
		firstRequest,
	)
	if err != nil {
		t.Fatal(err)
	}
	firstState := fixtureState(*firstReceipt)
	secondRequest := fixtureRequest(t, store, "trixie", "reconcile", &firstState)
	secondReceipt, err := recorder.Accept(
		context.Background(),
		secondRequest.Producer,
		"run-2",
		policy,
		secondRequest,
	)
	if err != nil {
		t.Fatal(err)
	}

	writer := &fixtureWriter{}
	reconciler := fixtureReconciler(store, writer)
	if err := reconciler.ReconcileAll(context.Background()); err != nil {
		t.Fatalf("ReconcileAll() error = %v", err)
	}
	if got, want := writer.appliedSequences(), []uint64{firstReceipt.Sequence, secondReceipt.Sequence}; !reflect.DeepEqual(got, want) {
		t.Fatalf("apply order = %v, want %v", got, want)
	}

	if err := reconciler.ReconcileAll(context.Background()); err != nil {
		t.Fatalf("replayed ReconcileAll() error = %v", err)
	}
	if got := writer.appliedSequences(); len(got) != 2 {
		t.Fatalf("replay applied %d requests, want 2 total", len(got))
	}
	if got := writer.verifyCount(); got != 4 {
		t.Fatalf("verify count = %d, want 4", got)
	}
	attempts, err := store.List(context.Background(), "attempts/v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempt count = %d, want 2", len(attempts))
	}
}

func TestReconcilerSerializesSameTargetWithoutCancellation(t *testing.T) {
	store := newFixtureFileStore(t)
	recorder := Recorder{Store: store}
	request := fixtureRequest(t, store, "trixie", "initialize", nil)
	if _, err := recorder.Accept(
		context.Background(),
		request.Producer,
		"run-1",
		fixtureDigest("policy"),
		request,
	); err != nil {
		t.Fatal(err)
	}

	writer := &fixtureWriter{delay: 30 * time.Millisecond}
	first := fixtureReconciler(store, writer)
	second := fixtureReconciler(store, writer)
	var wait sync.WaitGroup
	errorsSeen := make([]error, 2)
	for index, reconciler := range []Reconciler{first, second} {
		wait.Add(1)
		go func(index int, reconciler Reconciler) {
			defer wait.Done()
			errorsSeen[index] = reconciler.ReconcileAll(context.Background())
		}(index, reconciler)
	}
	wait.Wait()
	for _, err := range errorsSeen {
		if err != nil {
			t.Fatalf("ReconcileAll() error = %v", err)
		}
	}
	if writer.maximumActive() != 1 {
		t.Fatalf("same-target active writers = %d, want 1", writer.maximumActive())
	}
	if got := writer.appliedSequences(); len(got) != 1 {
		t.Fatalf("same target applied %d times, want 1", len(got))
	}
}

func TestReconcilerAllowsIndependentTargetsToProgress(t *testing.T) {
	store := newFixtureFileStore(t)
	recorder := Recorder{Store: store}
	for _, target := range []string{"trixie", "forky"} {
		request := fixtureRequest(t, store, target, "initialize", nil)
		if _, err := recorder.Accept(
			context.Background(),
			request.Producer,
			"run-"+target,
			fixtureDigest("policy"),
			request,
		); err != nil {
			t.Fatal(err)
		}
	}

	writer := &fixtureWriter{delay: 40 * time.Millisecond}
	if err := fixtureReconciler(store, writer).ReconcileAll(context.Background()); err != nil {
		t.Fatalf("ReconcileAll() error = %v", err)
	}
	if writer.maximumActive() != 2 {
		t.Fatalf("cross-target active writers = %d, want 2", writer.maximumActive())
	}
	if got := writer.appliedTargets(); !reflect.DeepEqual(got, []string{"forky", "trixie"}) {
		t.Fatalf("applied targets = %v", got)
	}
}

func TestReconcilerRecoversAppendOnlyPartialAttempt(t *testing.T) {
	store := newFixtureFileStore(t)
	request := fixtureRequest(t, store, "trixie", "initialize", nil)
	if _, err := (Recorder{Store: store}).Accept(
		context.Background(),
		request.Producer,
		"run-1",
		fixtureDigest("policy"),
		request,
	); err != nil {
		t.Fatal(err)
	}

	writer := &fixtureWriter{applyFailures: 1}
	reconciler := fixtureReconciler(store, writer)
	if err := reconciler.ReconcileAll(context.Background()); err == nil {
		t.Fatal("first ReconcileAll() unexpectedly succeeded")
	}
	if _, err := store.Open(context.Background(), resultPointerKey(fixtureRequestDigest(t, request))); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed attempt result pointer error = %v, want ErrNotFound", err)
	}
	if err := reconciler.ReconcileAll(context.Background()); err != nil {
		t.Fatalf("recovered ReconcileAll() error = %v", err)
	}
	attempts, err := store.List(context.Background(), "attempts/v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 {
		t.Fatalf("attempt count = %d, want 2", len(attempts))
	}
}

func TestReconcilerFailsClosedOnStoreAndReadBackErrors(t *testing.T) {
	testCases := []struct {
		name      string
		configure func(*faultStore, *fixtureWriter, string)
	}{
		{
			name: "403-is-not-absence",
			configure: func(store *faultStore, _ *fixtureWriter, _ string) {
				store.readPrefix = "results/v1/"
				store.readErr = errors.New("403 forbidden")
			},
		},
		{
			name: "5xx-enumeration",
			configure: func(store *faultStore, _ *fixtureWriter, _ string) {
				store.listPrefix = "receipts/v1/debian/trixie"
				store.listErr = errors.New("503 unavailable")
			},
		},
		{
			name: "timeout-is-not-absence",
			configure: func(store *faultStore, _ *fixtureWriter, artifact string) {
				store.readPrefix = path.Join("blobs/sha256", artifact[:2], artifact)
				store.readErr = context.DeadlineExceeded
			},
		},
		{
			name: "incomplete-record-readback",
			configure: func(store *faultStore, _ *fixtureWriter, _ string) {
				store.truncatePrefix = "attempts/v1/"
			},
		},
		{
			name: "public-readback-failure",
			configure: func(_ *faultStore, writer *fixtureWriter, _ string) {
				writer.verifyErr = errors.New("incomplete public read-back")
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			base := newFixtureFileStore(t)
			request := fixtureRequest(t, base, "trixie", "initialize", nil)
			if _, err := (Recorder{Store: base}).Accept(
				context.Background(),
				request.Producer,
				"run-1",
				fixtureDigest("policy"),
				request,
			); err != nil {
				t.Fatal(err)
			}
			store := &faultStore{Store: base}
			writer := &fixtureWriter{}
			testCase.configure(store, writer, request.ArtifactSHA256s[0])
			reconciler := fixtureReconciler(store, writer)
			if err := reconciler.ReconcileAll(context.Background()); err == nil {
				t.Fatal("ReconcileAll() unexpectedly succeeded")
			}
			if _, err := base.Open(
				context.Background(),
				resultPointerKey(fixtureRequestDigest(t, request)),
			); !errors.Is(err, ErrNotFound) {
				t.Fatalf("result pointer error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestReconcilerRejectsArtifactChecksumMismatch(t *testing.T) {
	root := t.TempDir()
	store, err := OpenFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	request := fixtureRequest(t, store, "trixie", "initialize", nil)
	if _, err := (Recorder{Store: store}).Accept(
		context.Background(),
		request.Producer,
		"run-1",
		fixtureDigest("policy"),
		request,
	); err != nil {
		t.Fatal(err)
	}
	artifact := request.ArtifactSHA256s[0]
	if err := os.WriteFile(
		path.Join(root, "blobs/sha256", artifact[:2], artifact),
		[]byte("corrupt fixture"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	writer := &fixtureWriter{}
	if err := fixtureReconciler(store, writer).ReconcileAll(context.Background()); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("checksum mismatch error = %v, want ErrIntegrity", err)
	}
	if len(writer.appliedSequences()) != 0 {
		t.Fatal("checksum mismatch reached writer")
	}
}

func TestReconcilerRejectsBrokenPriorContinuity(t *testing.T) {
	store := newFixtureFileStore(t)
	recorder := Recorder{Store: store}
	first := fixtureRequest(t, store, "trixie", "initialize", nil)
	if _, err := recorder.Accept(
		context.Background(),
		first.Producer,
		"run-1",
		fixtureDigest("policy"),
		first,
	); err != nil {
		t.Fatal(err)
	}
	wrong := fixtureDigest("wrong prior")
	second := fixtureRequest(t, store, "trixie", "reconcile", &wrong)
	if _, err := recorder.Accept(
		context.Background(),
		second.Producer,
		"run-2",
		fixtureDigest("policy"),
		second,
	); err != nil {
		t.Fatal(err)
	}
	writer := &fixtureWriter{}
	if err := fixtureReconciler(store, writer).ReconcileAll(context.Background()); !errors.Is(err, ErrState) {
		t.Fatalf("continuity error = %v, want ErrState", err)
	}
	if got := writer.appliedSequences(); !reflect.DeepEqual(got, []uint64{1}) {
		t.Fatalf("applied sequences = %v, want [1]", got)
	}
}

func TestReconcilerRechecksCurrentPolicyBeforeAttempt(t *testing.T) {
	store := newFixtureFileStore(t)
	request := fixtureRequest(t, store, "trixie", "initialize", nil)
	if _, err := (Recorder{Store: store}).Accept(
		context.Background(),
		request.Producer,
		"run-1",
		fixtureDigest("policy"),
		request,
	); err != nil {
		t.Fatal(err)
	}
	writer := &fixtureWriter{}
	reconciler := fixtureReconciler(store, writer)
	reconciler.Authorizer = AuthorizeFunc(func(context.Context, Receipt, Request) error {
		return errors.New("producer revoked")
	})

	if err := reconciler.ReconcileAll(context.Background()); !errors.Is(err, ErrState) {
		t.Fatalf("policy denial error = %v, want ErrState", err)
	}
	if len(writer.appliedSequences()) != 0 {
		t.Fatal("denied request reached writer")
	}
	attempts, err := store.List(context.Background(), "attempts/v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 0 {
		t.Fatalf("policy denial created %d attempts", len(attempts))
	}
}

func TestReconcilerVerifiesCompletedResultAfterPolicyRevocation(t *testing.T) {
	store := newFixtureFileStore(t)
	request := fixtureRequest(t, store, "trixie", "initialize", nil)
	if _, err := (Recorder{Store: store}).Accept(
		context.Background(),
		request.Producer,
		"run-1",
		fixtureDigest("policy"),
		request,
	); err != nil {
		t.Fatal(err)
	}
	writer := &fixtureWriter{}
	reconciler := fixtureReconciler(store, writer)
	if err := reconciler.ReconcileAll(context.Background()); err != nil {
		t.Fatalf("initial ReconcileAll() error = %v", err)
	}
	reconciler.Authorizer = AuthorizeFunc(func(context.Context, Receipt, Request) error {
		return errors.New("producer revoked")
	})

	if err := reconciler.ReconcileAll(context.Background()); err != nil {
		t.Fatalf("completed-result ReconcileAll() error = %v", err)
	}
	if got := writer.appliedSequences(); !reflect.DeepEqual(got, []uint64{1}) {
		t.Fatalf("completed result triggered writer attempts %v, want [1]", got)
	}
	if got := writer.verifyCount(); got != 2 {
		t.Fatalf("completed result verify count = %d, want 2", got)
	}
}

func newFixtureFileStore(t *testing.T) *FileStore {
	t.Helper()
	store, err := OpenFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func fixtureRequest(
	t *testing.T,
	store Store,
	target string,
	operation string,
	expectedPrior *string,
) Request {
	t.Helper()
	provenance := []byte("fixture provenance for " + target + "\n")
	provenanceDigest := digestBytes(provenance)
	if err := CreateImmutable(
		context.Background(),
		store,
		path.Join("manifests/provenance/v1/sha256", provenanceDigest+".json"),
		provenance,
	); err != nil {
		t.Fatal(err)
	}
	artifact := []byte("fixture package for " + target + " " + operation + "\n")
	artifactDigest := digestBytes(artifact)
	if err := CreateImmutable(
		context.Background(),
		store,
		path.Join("blobs/sha256", artifactDigest[:2], artifactDigest),
		artifact,
	); err != nil {
		t.Fatal(err)
	}
	return Request{
		SchemaVersion:    schemaVersion,
		Kind:             "debian",
		Operation:        operation,
		Target:           target,
		Suite:            target,
		Component:        "main",
		Architectures:    []string{"all", "amd64"},
		Origin:           "Repogen Repository",
		Label:            "Frostyard Repository",
		ValidUntilPolicy: "omitted",
		Producer:         "frostyard/fixture",
		ProvenanceSHA256: provenanceDigest,
		ArtifactSHA256s:  []string{artifactDigest},
		ExpectedPrior:    expectedPrior,
		ActionCommit:     "0123456789abcdef0123456789abcdef01234567",
		RepogenVersion:   "v1.2.3",
		RepogenSHA256:    fixtureDigest("repogen binary"),
	}
}

func fixtureReconciler(store Store, writer *fixtureWriter) Reconciler {
	var attemptMu sync.Mutex
	attempt := 0
	return Reconciler{
		Store:      store,
		Kind:       "debian",
		Writer:     writer,
		Authorizer: AuthorizeFunc(func(context.Context, Receipt, Request) error { return nil }),
		Now:        func() time.Time { return time.Unix(1_800_000_000, 0) },
		AttemptID: func() (string, error) {
			attemptMu.Lock()
			defer attemptMu.Unlock()
			attempt++
			return fmt.Sprintf("%032x", attempt), nil
		},
	}
}

func fixtureRequestDigest(t *testing.T, request Request) string {
	t.Helper()
	data, err := canonicalJSON(request)
	if err != nil {
		t.Fatal(err)
	}
	return digestBytes(data)
}

func fixtureDigest(value string) string {
	return digestBytes([]byte(value))
}

func fixtureState(receipt Receipt) string {
	return fixtureDigest(fmt.Sprintf("%s/%s/%d", receipt.Kind, receipt.Target, receipt.Sequence))
}

type fixtureWriter struct {
	mu            sync.Mutex
	applied       []Receipt
	verified      int
	active        int
	maxActive     int
	applyFailures int
	verifyErr     error
	delay         time.Duration
}

func (w *fixtureWriter) Apply(
	_ context.Context,
	receipt Receipt,
	_ Request,
) (*Result, error) {
	w.mu.Lock()
	w.active++
	if w.active > w.maxActive {
		w.maxActive = w.active
	}
	w.applied = append(w.applied, receipt)
	fail := w.applyFailures > 0
	if fail {
		w.applyFailures--
	}
	w.mu.Unlock()
	if w.delay != 0 {
		time.Sleep(w.delay)
	}
	w.mu.Lock()
	w.active--
	w.mu.Unlock()
	if fail {
		return nil, errors.New("injected writer failure")
	}
	state := fixtureState(receipt)
	return &Result{
		SigningKeyFingerprint: "0123456789ABCDEF0123456789ABCDEF01234567",
		StateSHA256:           state,
		CommitSHA256:          fixtureDigest("commit-" + state),
		Objects: []Object{{
			Key:    path.Join("dists", receipt.Target, "InRelease"),
			SHA256: fixtureDigest("object-" + state),
			Size:   100,
		}},
	}, nil
}

func (w *fixtureWriter) Verify(
	_ context.Context,
	_ Receipt,
	_ Request,
	_ Result,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.verified++
	return w.verifyErr
}

func (w *fixtureWriter) appliedSequences() []uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	sequences := make([]uint64, 0, len(w.applied))
	for _, receipt := range w.applied {
		sequences = append(sequences, receipt.Sequence)
	}
	return sequences
}

func (w *fixtureWriter) appliedTargets() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	targets := make([]string, 0, len(w.applied))
	for _, receipt := range w.applied {
		targets = append(targets, receipt.Target)
	}
	sort.Strings(targets)
	return targets
}

func (w *fixtureWriter) maximumActive() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.maxActive
}

func (w *fixtureWriter) verifyCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.verified
}

type faultStore struct {
	Store
	readPrefix     string
	readErr        error
	listPrefix     string
	listErr        error
	truncatePrefix string
}

func (s *faultStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if s.readErr != nil && strings.HasPrefix(key, s.readPrefix) {
		return nil, s.readErr
	}
	body, err := s.Store.Open(ctx, key)
	if err != nil || !strings.HasPrefix(key, s.truncatePrefix) {
		return body, err
	}
	data, readErr := io.ReadAll(body)
	closeErr := body.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(data) > 0 {
		data = data[:len(data)-1]
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (s *faultStore) List(ctx context.Context, prefix string) ([]string, error) {
	if s.listErr != nil && strings.HasPrefix(prefix, s.listPrefix) {
		return nil, s.listErr
	}
	return s.Store.List(ctx, prefix)
}

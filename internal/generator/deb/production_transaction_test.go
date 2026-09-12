package deb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/frostyard/repogen/internal/models"
	"github.com/frostyard/repogen/internal/signer"
)

func TestProductionTransactionPublishesScopedObjectsWithInReleaseLast(t *testing.T) {
	transaction := stageProductionFixture(t, "initialize", nil)
	store := newProductionFixtureStore()
	stable := []byte("frozen stable bytes")
	store.objects["dists/stable/InRelease"] = stable

	result, err := PublishProductionTransaction(context.Background(), store, transaction)
	if err != nil {
		t.Fatalf("PublishProductionTransaction() error = %v", err)
	}
	if result.ReleaseSHA256 == "" || result.InReleaseSHA256 == "" {
		t.Fatalf("result lacks signed metadata digests: %+v", result)
	}
	if got := store.objectBytes("dists/stable/InRelease"); !bytes.Equal(got, stable) {
		t.Fatalf("stable changed: got %q, want %q", got, stable)
	}

	writes := store.writePaths()
	if got := writes[len(writes)-1]; got != "dists/trixie/InRelease" {
		t.Fatalf("final write = %q, want dists/trixie/InRelease\nwrites: %v", got, writes)
	}
	wantWrites := make([]string, 0, len(transaction.objects))
	for _, object := range transaction.objects {
		wantWrites = append(wantWrites, object.Path)
	}
	if !reflect.DeepEqual(writes, wantWrites) {
		t.Fatalf("write order does not match staged transaction\n got: %v\nwant: %v", writes, wantWrites)
	}
	for _, key := range writes {
		if !strings.HasPrefix(key, "pool/main/") && !strings.HasPrefix(key, "dists/trixie/") {
			t.Fatalf("write escaped target scope: %q", key)
		}
	}
	for _, object := range transaction.objects {
		got := store.objectBytes(object.Path)
		if sha256Hex(got) != object.SHA256 || int64(len(got)) != object.Size {
			t.Fatalf("published %s failed read-back identity", object.Path)
		}
	}

	release := store.objectBytes("dists/trixie/Release")
	if !bytes.Contains(release, []byte("Acquire-By-Hash: yes\n")) {
		t.Fatal("Release does not require Acquire-By-Hash")
	}
	for _, architecture := range ProductionArchitectures() {
		for _, name := range []string{"Packages", "Packages.gz"} {
			canonical := path.Join("dists", "trixie", "main", "binary-"+architecture, name)
			digest := sha256Hex(store.objectBytes(canonical))
			byHash := path.Join("dists", "trixie", "main", "binary-"+architecture, "by-hash", "SHA256", digest)
			if !bytes.Equal(store.objectBytes(canonical), store.objectBytes(byHash)) {
				t.Fatalf("by-hash object %s does not match %s", byHash, canonical)
			}
		}
	}

	requestData, err := os.ReadFile(filepath.Join(transaction.StageDir, "request.json"))
	if err != nil {
		t.Fatal(err)
	}
	if sha256Hex(requestData) != transaction.RequestSHA256 {
		t.Fatal("request manifest digest does not match staged bytes")
	}
	if bytes.Contains(requestData, []byte(transaction.StageDir)) ||
		bytes.Contains(requestData, []byte("PRIVATE KEY")) {
		t.Fatal("request manifest contains local paths or private key material")
	}
	resultData, err := result.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if len(resultData) > 16*1024 {
		t.Fatalf("result manifest is unexpectedly large: %d bytes", len(resultData))
	}
}

func TestProductionArchitecturesReturnsIndependentCopy(t *testing.T) {
	first := ProductionArchitectures()
	first[0] = "arm64"

	if got := ProductionArchitectures(); !reflect.DeepEqual(got, []string{"all", "amd64"}) {
		t.Fatalf("ProductionArchitectures() = %v, want [all amd64]", got)
	}
}

func TestProductionTransactionFailureInjectionNeverExposesIncompleteGeneration(t *testing.T) {
	transaction := stageProductionFixture(t, "initialize", nil)
	stable := []byte("stable must not drift")

	for failWrite := 1; failWrite <= len(transaction.objects); failWrite++ {
		t.Run(fmt.Sprintf("write-%02d", failWrite), func(t *testing.T) {
			store := newProductionFixtureStore()
			store.objects["dists/stable/InRelease"] = append([]byte(nil), stable...)
			store.failWriteAt = failWrite

			if _, err := PublishProductionTransaction(context.Background(), store, transaction); err == nil {
				t.Fatal("PublishProductionTransaction() unexpectedly succeeded")
			}
			assertNoIncompleteVisibleGeneration(t, store, transaction)
			if got := store.objectBytes("dists/stable/InRelease"); !bytes.Equal(got, stable) {
				t.Fatalf("stable changed after injected failure: %q", got)
			}
		})
	}

	for failOpen := 1; failOpen <= len(transaction.objects)*2+1; failOpen++ {
		t.Run(fmt.Sprintf("readback-%02d", failOpen), func(t *testing.T) {
			store := newProductionFixtureStore()
			store.objects["dists/stable/InRelease"] = append([]byte(nil), stable...)
			store.failOpenAt = failOpen

			_, _ = PublishProductionTransaction(context.Background(), store, transaction)
			assertNoIncompleteVisibleGeneration(t, store, transaction)
			if got := store.objectBytes("dists/stable/InRelease"); !bytes.Equal(got, stable) {
				t.Fatalf("stable changed after read-back failure: %q", got)
			}
		})
	}
}

func TestProductionTransactionPreservesStableTreeByteForByte(t *testing.T) {
	transaction := stageProductionFixture(t, "initialize", nil)
	store := newProductionFixtureStore()
	stableBefore := map[string][]byte{
		"dists/stable/Release":                       []byte("stable Release\n"),
		"dists/stable/InRelease":                     []byte("stable InRelease\n"),
		"dists/stable/Release.gpg":                   []byte("stable Release.gpg\n"),
		"dists/stable/main/binary-amd64/Packages":    []byte("stable Packages\n"),
		"dists/stable/main/binary-amd64/Packages.gz": []byte("stable Packages.gz\n"),
	}
	for key, data := range stableBefore {
		store.objects[key] = append([]byte(nil), data...)
	}
	for _, object := range transaction.objects {
		if object.kind != productionPoolObject {
			continue
		}
		data, err := os.ReadFile(object.localPath)
		if err != nil {
			t.Fatal(err)
		}
		store.objects[object.Path] = data
		stableBefore[object.Path] = append([]byte(nil), data...)
	}

	if _, err := PublishProductionTransaction(context.Background(), store, transaction); err != nil {
		t.Fatal(err)
	}
	for key, before := range stableBefore {
		if after := store.objectBytes(key); !bytes.Equal(after, before) {
			t.Fatalf("stable object %s changed\nbefore: %x\nafter:  %x", key, before, after)
		}
	}
}

func TestProductionTransactionRejectsDirtyStageAndPriorDriftBeforeWrites(t *testing.T) {
	request, closeSigner := productionFixtureRequest(t, "initialize", nil)
	defer closeSigner()

	dirty := filepath.Join(t.TempDir(), "dirty")
	if err := os.Mkdir(dirty, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := StageProductionTransaction(dirty, request); !errors.Is(err, ErrPublicationCandidate) {
		t.Fatalf("dirty stage error = %v, want ErrPublicationCandidate", err)
	}
	unsignedRequest := request
	unsignedRequest.Signer = nil
	unsignedStage := filepath.Join(t.TempDir(), "unsigned")
	if _, err := StageProductionTransaction(unsignedStage, unsignedRequest); !errors.Is(err, ErrPublicationCandidate) {
		t.Fatalf("unsigned stage error = %v, want ErrPublicationCandidate", err)
	}
	if _, err := os.Stat(unsignedStage); !os.IsNotExist(err) {
		t.Fatalf("unsigned request created staging path: %v", err)
	}

	initial := stageProductionFixture(t, "initialize", nil)
	unverifiedRequest, closeUnverifiedSigner := productionFixtureRequest(t, "initialize", nil)
	defer closeUnverifiedSigner()
	unverifiedRequest.Operation = "reconcile"
	unverifiedRequest.ExpectedPriorReleaseSHA256 = strings.Repeat("a", 64)
	unverifiedRequest.PriorState = &ProductionState{ReleaseSHA256: strings.Repeat("a", 64)}
	if _, err := StageProductionTransaction(
		filepath.Join(t.TempDir(), "unverified"),
		unverifiedRequest,
	); !errors.Is(err, ErrPublicationCandidate) {
		t.Fatalf("unverified prior error = %v, want ErrPublicationCandidate", err)
	}

	store := newProductionFixtureStore()
	if _, err := PublishProductionTransaction(context.Background(), store, initial); err != nil {
		t.Fatal(err)
	}
	prior := priorStateFor(t, initial)
	reconcile := stageProductionFixture(t, "reconcile", prior)

	releasePath := "dists/trixie/Release"
	store.objects[releasePath] = append(store.objects[releasePath], '\n')
	writesBefore := len(store.writePaths())
	if _, err := PublishProductionTransaction(context.Background(), store, reconcile); !errors.Is(err, ErrPublicationState) {
		t.Fatalf("prior drift error = %v, want ErrPublicationState", err)
	}
	if got := len(store.writePaths()); got != writesBefore {
		t.Fatalf("prior drift performed %d writes", got-writesBefore)
	}
}

func TestProductionTransactionSerializesSameTarget(t *testing.T) {
	first := stageProductionFixture(t, "initialize", nil)
	second := stageProductionFixture(t, "initialize", nil)
	store := newProductionFixtureStore()
	store.writeDelay = 2 * time.Millisecond

	var wg sync.WaitGroup
	for _, transaction := range []*ProductionTransaction{first, second} {
		wg.Add(1)
		go func(transaction *ProductionTransaction) {
			defer wg.Done()
			_, _ = PublishProductionTransaction(context.Background(), store, transaction)
		}(transaction)
	}
	wg.Wait()
	if store.maxActiveLocks != 1 {
		t.Fatalf("same-target active locks = %d, want 1", store.maxActiveLocks)
	}
}

func TestProductionTransactionRealGPGVAndAPTFixture(t *testing.T) {
	for _, command := range []string{"gpg", "gpgv", "apt-get"} {
		if _, err := exec.LookPath(command); err != nil {
			t.Skipf("%s is not available", command)
		}
	}

	transaction := stageProductionFixture(t, "initialize", nil)
	store := newProductionFixtureStore()
	stable := []byte("stable snapshot fixture")
	store.objects["dists/stable/InRelease"] = append([]byte(nil), stable...)
	if _, err := PublishProductionTransaction(context.Background(), store, transaction); err != nil {
		t.Fatal(err)
	}

	repository := filepath.Join(t.TempDir(), "repository")
	if err := materializeProductionFixture(repository, store); err != nil {
		t.Fatal(err)
	}
	keyring := filepath.Join(t.TempDir(), "trusted.gpg")
	dearmor := exec.Command(
		"gpg",
		"--batch",
		"--yes",
		"--dearmor",
		"--output",
		keyring,
		productionPublicKeyFixture(),
	)
	if output, err := dearmor.CombinedOutput(); err != nil {
		t.Fatalf("gpg --dearmor failed: %v\n%s", err, output)
	}
	release := filepath.Join(repository, "dists", "trixie", "Release")
	signature := filepath.Join(repository, "dists", "trixie", "Release.gpg")
	gpgv := exec.Command("gpgv", "--keyring", keyring, signature, release)
	if output, err := gpgv.CombinedOutput(); err != nil {
		t.Fatalf("gpgv failed: %v\n%s", err, output)
	}

	aptRoot := filepath.Join(t.TempDir(), "apt")
	for _, directory := range []string{
		filepath.Join(aptRoot, "lists", "partial"),
		filepath.Join(aptRoot, "archives", "partial"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sourceList := filepath.Join(aptRoot, "sources.list")
	statusFile := filepath.Join(aptRoot, "status")
	source := fmt.Sprintf("deb [signed-by=%s] file://%s trixie main\n", keyring, repository)
	if err := os.WriteFile(sourceList, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	apt := exec.Command(
		"apt-get",
		"-o", "Debug::NoLocking=true",
		"-o", "APT::Sandbox::User=root",
		"-o", "Dir::Etc::sourcelist="+sourceList,
		"-o", "Dir::Etc::sourceparts=-",
		"-o", "Dir::State::lists="+filepath.Join(aptRoot, "lists"),
		"-o", "Dir::Cache::archives="+filepath.Join(aptRoot, "archives"),
		"-o", "APT::Get::List-Cleanup=0",
		"update",
	)
	if output, err := apt.CombinedOutput(); err != nil {
		t.Fatalf("apt-get update failed: %v\n%s", err, output)
	}
	aptInstall := exec.Command(
		"apt-get",
		"-o", "Debug::NoLocking=true",
		"-o", "APT::Sandbox::User=root",
		"-o", "Dir::Etc::sourcelist="+sourceList,
		"-o", "Dir::Etc::sourceparts=-",
		"-o", "Dir::State::lists="+filepath.Join(aptRoot, "lists"),
		"-o", "Dir::State::status="+statusFile,
		"-o", "Dir::Cache::archives="+filepath.Join(aptRoot, "archives"),
		"--download-only",
		"--yes",
		"install",
		"repogen-test",
	)
	if output, err := aptInstall.CombinedOutput(); err != nil {
		t.Fatalf("apt-get download-only install failed: %v\n%s", err, output)
	}
	if got := store.objectBytes("dists/stable/InRelease"); !bytes.Equal(got, stable) {
		t.Fatalf("stable changed during apt fixture: %q", got)
	}
}

func assertNoIncompleteVisibleGeneration(
	t *testing.T,
	store *productionFixtureStore,
	transaction *ProductionTransaction,
) {
	t.Helper()
	if len(store.objectBytes("dists/trixie/InRelease")) == 0 {
		return
	}
	for _, object := range transaction.objects {
		got := store.objectBytes(object.Path)
		if int64(len(got)) != object.Size || sha256Hex(got) != object.SHA256 {
			t.Fatalf("InRelease visible while %s is incomplete", object.Path)
		}
	}
}

func stageProductionFixture(
	t *testing.T,
	operation string,
	prior *ProductionState,
) *ProductionTransaction {
	t.Helper()
	request, closeSigner := productionFixtureRequest(t, operation, prior)
	t.Cleanup(closeSigner)
	stageDir := filepath.Join(t.TempDir(), "stage")
	transaction, err := StageProductionTransaction(stageDir, request)
	if err != nil {
		t.Fatalf("StageProductionTransaction() error = %v", err)
	}
	return transaction
}

func productionFixtureRequest(
	t *testing.T,
	operation string,
	prior *ProductionState,
) (ProductionStageRequest, func()) {
	t.Helper()
	source := filepath.Join("..", "..", "..", "test", "fixtures", "debs", "repogen-test_1.0.0_amd64.deb")
	pkg, err := ParsePackage(source)
	if err != nil {
		t.Fatalf("ParsePackage() error = %v", err)
	}
	gpgSigner, err := signer.NewGPGSigner(productionPrivateKeyFixture(), "")
	if err != nil {
		t.Fatalf("NewGPGSigner() error = %v", err)
	}
	request := ProductionStageRequest{
		RequestID:   "fixture-request-0001",
		Operation:   operation,
		Codename:    "trixie",
		ReleaseTime: time.Date(2026, time.September, 12, 20, 0, 0, 0, time.UTC),
		Packages: []ProductionPackageInput{{
			Package:    *pkg,
			SourcePath: source,
		}},
		Signer:     gpgSigner,
		PriorState: prior,
	}
	if operation == "reconcile" {
		request.ExpectedPriorReleaseSHA256 = prior.ReleaseSHA256
	}
	return request, func() {
		if err := gpgSigner.Close(); err != nil {
			t.Errorf("close GPG signer: %v", err)
		}
	}
}

func priorStateFor(t *testing.T, transaction *ProductionTransaction) *ProductionState {
	t.Helper()
	digests := make(map[string]ProductionObjectDigest)
	var poolPath string
	for _, object := range transaction.objects {
		if object.kind >= productionIndexObject {
			digests[object.Path] = ProductionObjectDigest{SHA256: object.SHA256, Size: object.Size}
		}
		if object.kind == productionPoolObject {
			poolPath = object.Path
		}
	}
	source := filepath.Join("..", "..", "..", "test", "fixtures", "debs", "repogen-test_1.0.0_amd64.deb")
	pkg, err := ParsePackage(source)
	if err != nil {
		t.Fatal(err)
	}
	pkg.Filename = poolPath
	return &ProductionState{
		ReleaseSHA256: digests["dists/trixie/Release"].SHA256,
		Packages:      []models.Package{*pkg},
		objects:       digests,
		poolDigests: map[string]PoolDigest{
			poolPath: {
				SHA256: pkg.SHA256Sum,
				Size:   pkg.Size,
			},
		},
		verified: true,
	}
}

func materializeProductionFixture(root string, store *productionFixtureStore) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for key, data := range store.objects {
		destination := filepath.Join(root, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(destination, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

type productionFixtureLock struct {
	store  *productionFixtureStore
	target string
	once   sync.Once
}

func (l *productionFixtureLock) Release() error {
	l.once.Do(func() {
		l.store.mu.Lock()
		l.store.activeLocks--
		l.store.mu.Unlock()
		l.store.lockFor(l.target) <- struct{}{}
	})
	return nil
}

type productionFixtureStore struct {
	mu             sync.Mutex
	objects        map[string][]byte
	writes         []string
	writeCount     int
	openCount      int
	failWriteAt    int
	failOpenAt     int
	writeDelay     time.Duration
	targetLocks    map[string]chan struct{}
	activeLocks    int
	maxActiveLocks int
}

func newProductionFixtureStore() *productionFixtureStore {
	return &productionFixtureStore{
		objects:     make(map[string][]byte),
		targetLocks: make(map[string]chan struct{}),
	}
}

func (s *productionFixtureStore) Open(_ context.Context, key string) (*RemotePoolObject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.openCount++
	if s.failOpenAt > 0 && s.openCount == s.failOpenAt {
		return nil, errors.New("injected fixture read failure")
	}
	data, ok := s.objects[key]
	if !ok {
		return nil, ErrPoolObjectNotFound
	}
	return &RemotePoolObject{
		Body: io.NopCloser(bytes.NewReader(append([]byte(nil), data...))),
		ETag: `"not-a-digest"`,
	}, nil
}

func (s *productionFixtureStore) Create(_ context.Context, key string, body io.Reader, size int64) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return fmt.Errorf("create size %d, want %d", len(data), size)
	}
	if s.writeDelay > 0 {
		time.Sleep(s.writeDelay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeCount++
	if s.failWriteAt > 0 && s.writeCount == s.failWriteAt {
		return errors.New("injected fixture write failure")
	}
	if _, exists := s.objects[key]; exists {
		return ErrPoolObjectExists
	}
	s.objects[key] = append([]byte(nil), data...)
	s.writes = append(s.writes, key)
	return nil
}

func (s *productionFixtureStore) Replace(
	_ context.Context,
	key string,
	body io.Reader,
	size int64,
	expectedSHA256 string,
) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if int64(len(data)) != size {
		return fmt.Errorf("replace size %d, want %d", len(data), size)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeCount++
	if s.failWriteAt > 0 && s.writeCount == s.failWriteAt {
		return errors.New("injected fixture replace failure")
	}
	current, exists := s.objects[key]
	if !exists || sha256Hex(current) != expectedSHA256 {
		return ErrPublicationState
	}
	s.objects[key] = append([]byte(nil), data...)
	s.writes = append(s.writes, key)
	return nil
}

func (s *productionFixtureStore) PrefixExists(_ context.Context, prefix string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.objects {
		if strings.HasPrefix(key, prefix) {
			return true, nil
		}
	}
	return false, nil
}

func (s *productionFixtureStore) Acquire(ctx context.Context, target string) (ProductionPublicationLock, error) {
	lock := s.lockFor(target)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-lock:
	}
	s.mu.Lock()
	s.activeLocks++
	if s.activeLocks > s.maxActiveLocks {
		s.maxActiveLocks = s.activeLocks
	}
	s.mu.Unlock()
	return &productionFixtureLock{store: s, target: target}, nil
}

func (s *productionFixtureStore) lockFor(target string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, ok := s.targetLocks[target]
	if !ok {
		lock = make(chan struct{}, 1)
		lock <- struct{}{}
		s.targetLocks[target] = lock
	}
	return lock
}

func (s *productionFixtureStore) objectBytes(key string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.objects[key]...)
}

func (s *productionFixtureStore) writePaths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.writes...)
}

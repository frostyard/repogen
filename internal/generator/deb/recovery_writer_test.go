package deb

import (
	"context"
	"os"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/frostyard/repogen/internal/intake"
)

func TestProductionRecoveryWriterPublishesFromRetainedReceiptAndReplays(t *testing.T) {
	intakeStore, err := intake.OpenFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	provenance := []byte("fixture provenance\n")
	provenanceSHA256 := sha256Hex(provenance)
	if err := intake.CreateImmutable(
		context.Background(),
		intakeStore,
		path.Join("manifests/provenance/v1/sha256", provenanceSHA256+".json"),
		provenance,
	); err != nil {
		t.Fatal(err)
	}
	artifact, err := os.ReadFile(
		path.Join("..", "..", "..", "test", "fixtures", "debs", "repogen-test_1.0.0_amd64.deb"),
	)
	if err != nil {
		t.Fatal(err)
	}
	artifactSHA256 := sha256Hex(artifact)
	if err := intake.CreateImmutable(
		context.Background(),
		intakeStore,
		path.Join("blobs/sha256", artifactSHA256[:2], artifactSHA256),
		artifact,
	); err != nil {
		t.Fatal(err)
	}
	request := intake.Request{
		SchemaVersion:    1,
		Kind:             "debian",
		Operation:        "initialize",
		Target:           "trixie",
		Suite:            "trixie",
		Component:        "main",
		Architectures:    []string{"all", "amd64"},
		Origin:           "Repogen Repository",
		Label:            "Frostyard Repository",
		ValidUntilPolicy: "omitted",
		Producer:         "frostyard/fixture",
		ProvenanceSHA256: provenanceSHA256,
		ArtifactSHA256s:  []string{artifactSHA256},
		ActionCommit:     "0123456789abcdef0123456789abcdef01234567",
		RepogenVersion:   "v1.2.3",
		RepogenSHA256:    sha256Hex([]byte("repogen binary")),
	}
	if _, err := (intake.Recorder{Store: intakeStore}).Accept(
		context.Background(),
		request.Producer,
		"fixture-run",
		sha256Hex([]byte("fixture policy")),
		request,
	); err != nil {
		t.Fatal(err)
	}

	transaction := stageProductionFixture(t, "initialize", nil)
	publicStore := newProductionFixtureStore()
	writer := ProductionRecoveryWriter{
		Store: publicStore,
		Builder: ProductionRecoveryBuilderFunc(func(
			_ context.Context,
			_ intake.Receipt,
			got intake.Request,
		) (*ProductionTransaction, error) {
			if got.Target != request.Target {
				t.Fatalf("builder target = %q, want %q", got.Target, request.Target)
			}
			return transaction, nil
		}),
	}
	var attemptMu sync.Mutex
	attempt := 0
	reconciler := intake.Reconciler{
		Store:  intakeStore,
		Kind:   "debian",
		Writer: writer,
		Authorizer: intake.AuthorizeFunc(func(
			context.Context,
			intake.Receipt,
			intake.Request,
		) error {
			return nil
		}),
		Now: func() time.Time { return time.Unix(1_800_000_000, 0) },
		AttemptID: func() (string, error) {
			attemptMu.Lock()
			defer attemptMu.Unlock()
			attempt++
			return "00000000000000000000000000000001", nil
		},
	}
	if err := reconciler.ReconcileAll(context.Background()); err != nil {
		t.Fatalf("ReconcileAll() error = %v", err)
	}
	writes := len(publicStore.writePaths())
	if writes == 0 {
		t.Fatal("recovery writer published no scoped objects")
	}
	if got := publicStore.writePaths()[writes-1]; got != "dists/trixie/InRelease" {
		t.Fatalf("last publication object = %q, want dists/trixie/InRelease", got)
	}
	results, err := intakeStore.List(context.Background(), "results/v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("result pointer count = %d, want 1", len(results))
	}

	if err := reconciler.ReconcileAll(context.Background()); err != nil {
		t.Fatalf("replayed ReconcileAll() error = %v", err)
	}
	if got := len(publicStore.writePaths()); got != writes {
		t.Fatalf("replay performed %d additional writes", got-writes)
	}
}

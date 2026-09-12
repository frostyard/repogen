//go:build linux

package deb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/repogen/internal/models"
	"github.com/frostyard/repogen/internal/utils"
)

func TestLocalProductionStageFailurePreservesPriorTree(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "repository")
	writeStableLocalFixture(t, outputDir)
	before := localTreeSnapshotForTest(t, outputDir)

	request, closeSigner := productionFixtureRequest(t, "initialize", nil)
	defer closeSigner()
	var steps []string
	traceStage := filepath.Join(root, "trace-stage")
	transaction, err := stageProductionTransaction(
		context.Background(),
		traceStage,
		request,
		&productionLocalHooks{before: func(step string) error {
			steps = append(steps, step)
			return nil
		}},
	)
	if err != nil {
		t.Fatalf("trace StageProductionTransaction() error = %v", err)
	}
	if err := os.RemoveAll(transaction.StageDir); err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 {
		t.Fatal("staging exposed no injectable steps")
	}

	for failAt, step := range steps {
		t.Run(fmt.Sprintf("%02d-%s", failAt+1, sanitizeTestName(step)), func(t *testing.T) {
			call := 0
			stageDir := filepath.Join(root, fmt.Sprintf("failed-stage-%02d", failAt+1))
			_, err := stageProductionTransaction(
				context.Background(),
				stageDir,
				request,
				&productionLocalHooks{before: func(string) error {
					call++
					if call == failAt+1 {
						return errors.New("injected stage failure")
					}
					return nil
				}},
			)
			if !errors.Is(err, ErrLocalGeneration) {
				t.Fatalf("error = %v, want ErrLocalGeneration", err)
			}
			if _, statErr := os.Lstat(stageDir); !os.IsNotExist(statErr) {
				t.Fatalf("failed staging directory remains: %v", statErr)
			}
			after := localTreeSnapshotForTest(t, outputDir)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("prior tree changed after failure before %s", step)
			}
		})
	}
}

func TestLocalProductionCommitFailurePreservesPriorTree(t *testing.T) {
	transaction := stageProductionFixture(t, "initialize", nil)
	root := t.TempDir()

	traceOutput := filepath.Join(root, "trace-repository")
	writeStableLocalFixture(t, traceOutput)
	var steps []string
	if err := commitLocalProductionTransaction(
		context.Background(),
		traceOutput,
		transaction,
		&productionLocalHooks{before: func(step string) error {
			steps = append(steps, step)
			return nil
		}},
	); err != nil {
		t.Fatalf("trace CommitLocalProductionTransaction() error = %v", err)
	}
	assertCompleteLocalGeneration(t, traceOutput, transaction)
	assertStableLocalFixture(t, traceOutput)
	if len(steps) == 0 {
		t.Fatal("commit exposed no injectable steps")
	}

	for failAt, step := range steps {
		t.Run(fmt.Sprintf("%03d-%s", failAt+1, sanitizeTestName(step)), func(t *testing.T) {
			outputDir := filepath.Join(root, fmt.Sprintf("repository-%03d", failAt+1))
			writeStableLocalFixture(t, outputDir)
			before := localTreeSnapshotForTest(t, outputDir)
			call := 0
			err := commitLocalProductionTransaction(
				context.Background(),
				outputDir,
				transaction,
				&productionLocalHooks{before: func(string) error {
					call++
					if call == failAt+1 {
						return errors.New("injected commit failure")
					}
					return nil
				}},
			)
			if !errors.Is(err, ErrLocalGeneration) {
				t.Fatalf("error = %v, want ErrLocalGeneration", err)
			}
			after := localTreeSnapshotForTest(t, outputDir)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("prior tree changed after failure before %s", step)
			}
			assertNoGenerationScratch(t, filepath.Dir(outputDir))
		})
	}
}

func TestLocalProductionReconcileCommitsOneCompleteGeneration(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "repository")
	writeStableLocalFixture(t, outputDir)

	initial := stageProductionFixture(t, "initialize", nil)
	if err := CommitLocalProductionTransaction(context.Background(), outputDir, initial); err != nil {
		t.Fatalf("initial CommitLocalProductionTransaction() error = %v", err)
	}
	assertCompleteLocalGeneration(t, outputDir, initial)
	assertStableLocalFixture(t, outputDir)
	initialRelease, err := os.ReadFile(filepath.Join(outputDir, "dists", "trixie", "Release"))
	if err != nil {
		t.Fatal(err)
	}

	prior := priorStateFor(t, initial)
	request, closeSigner := productionFixtureRequest(t, "reconcile", prior)
	defer closeSigner()
	request.ReleaseTime = request.ReleaseTime.Add(24 * time.Hour)
	stageDir := filepath.Join(root, "reconcile-stage")
	reconcile, err := StageProductionTransaction(stageDir, request)
	if err != nil {
		t.Fatalf("reconcile StageProductionTransaction() error = %v", err)
	}
	if err := CommitLocalProductionTransaction(context.Background(), outputDir, reconcile); err != nil {
		t.Fatalf("reconcile CommitLocalProductionTransaction() error = %v", err)
	}

	assertCompleteLocalGeneration(t, outputDir, reconcile)
	assertStableLocalFixture(t, outputDir)
	reconciledRelease, err := os.ReadFile(filepath.Join(outputDir, "dists", "trixie", "Release"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(reconciledRelease, initialRelease) {
		t.Fatal("reconcile did not expose the new complete generation")
	}
	assertNoGenerationScratch(t, filepath.Dir(outputDir))
}

func TestLocalProductionInitializeCommitsToAbsentOutput(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "repository")
	transaction := stageProductionFixture(t, "initialize", nil)

	if err := CommitLocalProductionTransaction(context.Background(), outputDir, transaction); err != nil {
		t.Fatalf("CommitLocalProductionTransaction() error = %v", err)
	}

	assertCompleteLocalGeneration(t, outputDir, transaction)
	info, err := os.Stat(outputDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("output mode = %o, want 755", info.Mode().Perm())
	}
	assertNoGenerationScratch(t, filepath.Dir(outputDir))
}

func TestGenerateLocalProductionRepositoryStagesCommitsAndCleans(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "repository")
	stageDir := filepath.Join(root, "stage")
	request, closeSigner := productionFixtureRequest(t, "initialize", nil)
	defer closeSigner()

	if err := GenerateLocalProductionRepository(
		context.Background(),
		outputDir,
		stageDir,
		request,
	); err != nil {
		t.Fatalf("GenerateLocalProductionRepository() error = %v", err)
	}

	for _, relative := range []string{
		"dists/trixie/Release",
		"dists/trixie/Release.gpg",
		"dists/trixie/InRelease",
		"dists/trixie/main/binary-all/Packages",
		"dists/trixie/main/binary-amd64/Packages",
	} {
		if _, err := os.Stat(filepath.Join(outputDir, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("missing generated %s: %v", relative, err)
		}
	}
	if _, err := os.Lstat(stageDir); !os.IsNotExist(err) {
		t.Fatalf("staging directory remains after success: %v", err)
	}
}

func TestLocalProductionRejectsPriorDriftBeforeCommit(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "repository")
	initial := stageProductionFixture(t, "initialize", nil)
	if err := CommitLocalProductionTransaction(context.Background(), outputDir, initial); err != nil {
		t.Fatal(err)
	}

	reconcile := stageProductionFixture(t, "reconcile", priorStateFor(t, initial))
	releasePath := filepath.Join(outputDir, "dists", "trixie", "Release")
	if err := os.WriteFile(releasePath, []byte("drifted release\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := localTreeSnapshotForTest(t, outputDir)
	if err := CommitLocalProductionTransaction(
		context.Background(),
		outputDir,
		reconcile,
	); !errors.Is(err, ErrLocalGeneration) {
		t.Fatalf("prior drift error = %v, want ErrLocalGeneration", err)
	}
	if after := localTreeSnapshotForTest(t, outputDir); !reflect.DeepEqual(after, before) {
		t.Fatal("prior drift failure changed output")
	}
}

func TestLocalProductionRejectsUnsignedAndPoolCollision(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "repository")
	writeStableLocalFixture(t, outputDir)
	before := localTreeSnapshotForTest(t, outputDir)

	request, closeSigner := productionFixtureRequest(t, "initialize", nil)
	defer closeSigner()
	request.Signer = nil
	if err := GenerateLocalProductionRepository(
		context.Background(),
		outputDir,
		filepath.Join(root, "unsigned-stage"),
		request,
	); !errors.Is(err, ErrPublicationCandidate) {
		t.Fatalf("unsigned generation error = %v, want ErrPublicationCandidate", err)
	}
	if after := localTreeSnapshotForTest(t, outputDir); !reflect.DeepEqual(after, before) {
		t.Fatal("unsigned production request changed prior output")
	}

	transaction := stageProductionFixture(t, "initialize", nil)
	var poolObject stagedProductionObject
	for _, object := range transaction.objects {
		if object.kind == productionPoolObject {
			poolObject = object
			break
		}
	}
	collision := filepath.Join(outputDir, filepath.FromSlash(poolObject.Path))
	if err := os.MkdirAll(filepath.Dir(collision), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(collision, []byte("different package bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	beforeCollision := localTreeSnapshotForTest(t, outputDir)
	if err := CommitLocalProductionTransaction(
		context.Background(),
		outputDir,
		transaction,
	); !errors.Is(err, ErrLocalGeneration) {
		t.Fatalf("pool collision error = %v, want ErrLocalGeneration", err)
	}
	if after := localTreeSnapshotForTest(t, outputDir); !reflect.DeepEqual(after, beforeCollision) {
		t.Fatal("pool collision changed prior output")
	}
}

func TestGenericUnsignedGenerationRemainsSupported(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "sample_1.0_amd64.deb")
	if err := os.WriteFile(source, []byte("generic unsigned package"), 0o644); err != nil {
		t.Fatal(err)
	}
	checksums, err := utils.CalculateChecksums(source)
	if err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(root, "repository")
	config := &models.RepositoryConfig{
		OutputDir:  outputDir,
		Codename:   "testing",
		Suite:      "testing",
		Origin:     "Generic",
		Label:      "Generic",
		Components: []string{"main"},
		Arches:     []string{"amd64"},
	}
	packages := []models.Package{{
		Name:         "sample",
		Version:      "1.0",
		Architecture: "amd64",
		Filename:     source,
		Size:         checksums.Size,
		MD5Sum:       checksums.MD5,
		SHA1Sum:      checksums.SHA1,
		SHA256Sum:    checksums.SHA256,
		SHA512Sum:    checksums.SHA512,
	}}

	if err := NewGenerator(nil).Generate(context.Background(), config, packages); err != nil {
		t.Fatalf("generic unsigned Generate() error = %v", err)
	}
	release, err := os.ReadFile(filepath.Join(outputDir, "dists", "testing", "Release"))
	if err != nil {
		t.Fatal(err)
	}
	inRelease, err := os.ReadFile(filepath.Join(outputDir, "dists", "testing", "InRelease"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(inRelease, release) {
		t.Fatal("generic unsigned InRelease no longer matches Release")
	}
	if _, err := os.Lstat(filepath.Join(outputDir, "dists", "testing", "Release.gpg")); !os.IsNotExist(err) {
		t.Fatalf("generic unsigned generation emitted Release.gpg: %v", err)
	}
}

func writeStableLocalFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"dists/stable/Release":                       "stable Release\n",
		"dists/stable/InRelease":                     "stable InRelease\n",
		"dists/stable/Release.gpg":                   "stable Release.gpg\n",
		"dists/stable/main/binary-amd64/Packages":    "stable Packages\n",
		"dists/stable/main/binary-amd64/Packages.gz": "stable Packages.gz\n",
	}
	for relative, data := range files {
		destination := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func assertStableLocalFixture(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		"dists/stable/Release":                       "stable Release\n",
		"dists/stable/InRelease":                     "stable InRelease\n",
		"dists/stable/Release.gpg":                   "stable Release.gpg\n",
		"dists/stable/main/binary-amd64/Packages":    "stable Packages\n",
		"dists/stable/main/binary-amd64/Packages.gz": "stable Packages.gz\n",
	}
	for relative, want := range files {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("%s changed: got %q, want %q", relative, got, want)
		}
	}
}

func assertCompleteLocalGeneration(
	t *testing.T,
	root string,
	transaction *ProductionTransaction,
) {
	t.Helper()
	for _, object := range transaction.objects {
		observed, err := hashLocalRegularFile(filepath.Join(root, filepath.FromSlash(object.Path)))
		if err != nil {
			t.Fatalf("read generated %s: %v", object.Path, err)
		}
		if observed.SHA256 != object.SHA256 || observed.Size != object.Size {
			t.Fatalf("generated %s does not match transaction", object.Path)
		}
	}
	for _, name := range []string{"Release", "Release.gpg", "InRelease"} {
		if _, err := os.Stat(filepath.Join(root, "dists", transaction.Codename, name)); err != nil {
			t.Fatalf("missing signed commit object %s: %v", name, err)
		}
	}
}

func localTreeSnapshotForTest(t *testing.T, root string) map[string]localTreeEntry {
	t.Helper()
	snapshot, err := snapshotLocalTree(context.Background(), root, nil, "test:snapshot")
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func assertNoGenerationScratch(t *testing.T, parent string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".repogen-generation-") {
			t.Fatalf("generation scratch remains: %s", entry.Name())
		}
	}
}

func sanitizeTestName(value string) string {
	value = strings.ReplaceAll(value, string(filepath.Separator), "-")
	value = strings.ReplaceAll(value, ":", "-")
	return value
}

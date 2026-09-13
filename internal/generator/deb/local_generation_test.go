//go:build linux

package deb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
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

func TestLocalProductionReconcileStageFailurePreservesPriorTree(t *testing.T) {
	initial := stageProductionFixture(t, "initialize", nil)
	request, closeSigner := productionFixtureRequest(t, "reconcile", priorStateFor(t, initial))
	defer closeSigner()
	request.ReleaseTime = request.ReleaseTime.Add(24 * time.Hour)

	root := t.TempDir()
	outputDir := filepath.Join(root, "repository")
	if err := CommitLocalProductionTransaction(context.Background(), outputDir, initial); err != nil {
		t.Fatalf("initial CommitLocalProductionTransaction() error = %v", err)
	}
	before := localTreeSnapshotForTest(t, outputDir)

	var steps []string
	traceStage := filepath.Join(root, "trace-reconcile-stage")
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
		t.Fatalf("trace reconcile StageProductionTransaction() error = %v", err)
	}
	if err := os.RemoveAll(transaction.StageDir); err != nil {
		t.Fatal(err)
	}
	if len(steps) == 0 {
		t.Fatal("reconcile staging exposed no injectable steps")
	}

	for failAt, step := range steps {
		t.Run(fmt.Sprintf("%02d-%s", failAt+1, sanitizeTestName(step)), func(t *testing.T) {
			stageDir := filepath.Join(root, fmt.Sprintf("failed-reconcile-stage-%02d", failAt+1))
			call := 0
			_, err := stageProductionTransaction(
				context.Background(),
				stageDir,
				request,
				&productionLocalHooks{before: func(string) error {
					call++
					if call == failAt+1 {
						return errors.New("injected reconcile stage failure")
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
	assertStableGenerationSyncSteps(t, steps)

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

func TestLocalProductionReconcileFailurePreservesPriorTree(t *testing.T) {
	initial := stageProductionFixture(t, "initialize", nil)
	prior := priorStateFor(t, initial)
	request, closeSigner := productionFixtureRequest(t, "reconcile", prior)
	defer closeSigner()
	request.ReleaseTime = request.ReleaseTime.Add(24 * time.Hour)
	reconcileStage := filepath.Join(t.TempDir(), "reconcile-stage")
	reconcile, err := StageProductionTransaction(reconcileStage, request)
	if err != nil {
		t.Fatalf("reconcile StageProductionTransaction() error = %v", err)
	}
	root := t.TempDir()

	traceOutput := filepath.Join(root, "trace-repository")
	if err := CommitLocalProductionTransaction(context.Background(), traceOutput, initial); err != nil {
		t.Fatalf("initial CommitLocalProductionTransaction() error = %v", err)
	}
	var steps []string
	if err := commitLocalProductionTransaction(
		context.Background(),
		traceOutput,
		reconcile,
		&productionLocalHooks{before: func(step string) error {
			steps = append(steps, step)
			return nil
		}},
	); err != nil {
		t.Fatalf("trace reconcile CommitLocalProductionTransaction() error = %v", err)
	}
	assertCompleteLocalGeneration(t, traceOutput, reconcile)
	if len(steps) == 0 {
		t.Fatal("reconcile commit exposed no injectable steps")
	}
	assertStableGenerationSyncSteps(t, steps)

	for failAt, step := range steps {
		t.Run(fmt.Sprintf("%03d-%s", failAt+1, sanitizeTestName(step)), func(t *testing.T) {
			outputDir := filepath.Join(root, fmt.Sprintf("repository-%03d", failAt+1))
			if err := CommitLocalProductionTransaction(context.Background(), outputDir, initial); err != nil {
				t.Fatalf("initial CommitLocalProductionTransaction() error = %v", err)
			}
			before := localTreeSnapshotForTest(t, outputDir)
			call := 0
			err := commitLocalProductionTransaction(
				context.Background(),
				outputDir,
				reconcile,
				&productionLocalHooks{before: func(string) error {
					call++
					if call == failAt+1 {
						return errors.New("injected reconcile failure")
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

func TestLocalProductionRecoveryCompletesInterruptedSwitch(t *testing.T) {
	for _, switched := range []bool{false, true} {
		t.Run(fmt.Sprintf("switched-%t", switched), func(t *testing.T) {
			outputDir, reconcile := prepareLocalRecoveryFixture(t, switched)

			if err := RecoverLocalProductionRepository(
				context.Background(),
				outputDir,
			); err != nil {
				t.Fatalf("RecoverLocalProductionRepository() error = %v", err)
			}

			assertCompleteLocalGeneration(t, outputDir, reconcile)
			assertStableLocalFixture(t, outputDir)
			assertNoGenerationScratch(t, filepath.Dir(outputDir))
			if _, err := os.Lstat(localRecoveryJournalPath(
				filepath.Dir(outputDir),
				filepath.Base(outputDir),
			)); !os.IsNotExist(err) {
				t.Fatalf("recovery journal remains: %v", err)
			}
		})
	}
}

func TestLocalProductionRecoveryConvergesAfterPartialPostSwitchCleanup(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "repository")
	writeStableLocalFixture(t, outputDir)

	initial := stageProductionFixture(t, "initialize", nil)
	if err := CommitLocalProductionTransaction(context.Background(), outputDir, initial); err != nil {
		t.Fatal(err)
	}
	request, closeSigner := productionFixtureRequest(t, "reconcile", priorStateFor(t, initial))
	defer closeSigner()
	request.ReleaseTime = request.ReleaseTime.Add(24 * time.Hour)
	reconcile, err := StageProductionTransaction(filepath.Join(root, "reconcile-stage"), request)
	if err != nil {
		t.Fatal(err)
	}

	err = commitLocalProductionTransaction(
		context.Background(),
		outputDir,
		reconcile,
		&productionLocalHooks{
			finishRecovery: func(parent string, journal localRecoveryJournal) error {
				obsoleteRelease := filepath.Join(
					parent,
					journal.Generation,
					"dists",
					"stable",
					"Release",
				)
				if err := os.Remove(obsoleteRelease); err != nil {
					return fmt.Errorf("partially remove obsolete generation: %w", err)
				}
				return errors.New("injected cleanup failure")
			},
		},
	)
	if !errors.Is(err, ErrLocalGeneration) || !strings.Contains(err.Error(), "injected cleanup failure") {
		t.Fatalf("cleanup error = %v, want explicit ErrLocalGeneration", err)
	}
	assertCompleteLocalGeneration(t, outputDir, reconcile)
	assertStableLocalFixture(t, outputDir)
	if _, err := os.Lstat(localRecoveryJournalPath(root, filepath.Base(outputDir))); err != nil {
		t.Fatalf("cleanup failure did not preserve recovery journal: %v", err)
	}

	if err := RecoverLocalProductionRepository(context.Background(), outputDir); err != nil {
		t.Fatalf("RecoverLocalProductionRepository() error = %v", err)
	}
	assertNoGenerationScratch(t, root)

	nextRequest, closeNextSigner := productionFixtureRequest(
		t,
		"reconcile",
		priorStateFor(t, reconcile),
	)
	defer closeNextSigner()
	nextRequest.ReleaseTime = nextRequest.ReleaseTime.Add(48 * time.Hour)
	next, err := StageProductionTransaction(filepath.Join(root, "next-stage"), nextRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := CommitLocalProductionTransaction(context.Background(), outputDir, next); err != nil {
		t.Fatalf("subsequent CommitLocalProductionTransaction() error = %v", err)
	}
	assertCompleteLocalGeneration(t, outputDir, next)
	assertStableLocalFixture(t, outputDir)
	assertNoGenerationScratch(t, root)
}

func TestLocalProductionRecoveryFailsClosedOnUnknownState(t *testing.T) {
	outputDir, _ := prepareLocalRecoveryFixture(t, false)
	before := localTreeSnapshotForTest(t, outputDir)
	if err := os.WriteFile(
		filepath.Join(outputDir, "unexpected"),
		[]byte("drift"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	if err := RecoverLocalProductionRepository(
		context.Background(),
		outputDir,
	); !errors.Is(err, ErrLocalGeneration) {
		t.Fatalf("RecoverLocalProductionRepository() error = %v, want ErrLocalGeneration", err)
	}
	after := localTreeSnapshotForTest(t, outputDir)
	if reflect.DeepEqual(after, before) {
		t.Fatal("test fixture did not introduce output drift")
	}
	if _, err := os.Lstat(localRecoveryJournalPath(
		filepath.Dir(outputDir),
		filepath.Base(outputDir),
	)); err != nil {
		t.Fatalf("fail-closed recovery removed journal: %v", err)
	}
}

func TestLocalProductionInitializeModesIgnoreRestrictiveUmask(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "repository")
	transaction := stageProductionFixture(t, "initialize", nil)

	previousUmask := syscall.Umask(0o077)
	defer syscall.Umask(previousUmask)

	if err := CommitLocalProductionTransaction(context.Background(), outputDir, transaction); err != nil {
		t.Fatalf("CommitLocalProductionTransaction() error = %v", err)
	}

	assertLocalGenerationModes(t, outputDir, transaction, nil)
}

func prepareLocalRecoveryFixture(
	t *testing.T,
	switched bool,
) (string, *ProductionTransaction) {
	t.Helper()
	root := t.TempDir()
	outputDir := filepath.Join(root, "repository")
	writeStableLocalFixture(t, outputDir)

	initial := stageProductionFixture(t, "initialize", nil)
	if err := CommitLocalProductionTransaction(context.Background(), outputDir, initial); err != nil {
		t.Fatal(err)
	}
	request, closeSigner := productionFixtureRequest(t, "reconcile", priorStateFor(t, initial))
	t.Cleanup(closeSigner)
	request.ReleaseTime = request.ReleaseTime.Add(24 * time.Hour)
	reconcile, err := StageProductionTransaction(filepath.Join(root, "reconcile-stage"), request)
	if err != nil {
		t.Fatal(err)
	}
	generationDir, err := os.MkdirTemp(root, ".repository.repogen-generation-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(generationDir, 0o755); err != nil {
		t.Fatal(err)
	}
	prior, err := snapshotAndCopyLocalTree(
		context.Background(),
		outputDir,
		generationDir,
		filepath.Join("dists", reconcile.Codename),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := installLocalGenerationObjects(
		context.Background(),
		generationDir,
		reconcile,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if err := syncLocalGeneration(context.Background(), generationDir, nil); err != nil {
		t.Fatal(err)
	}
	generationSHA256, err := digestLocalTree(context.Background(), generationDir)
	if err != nil {
		t.Fatal(err)
	}
	priorSHA256, err := digestLocalTreeSnapshot(prior)
	if err != nil {
		t.Fatal(err)
	}
	journal := localRecoveryJournal{
		SchemaVersion:    localRecoveryVersion,
		Output:           filepath.Base(outputDir),
		Generation:       filepath.Base(generationDir),
		OutputExisted:    true,
		PriorSHA256:      priorSHA256,
		GenerationSHA256: generationSHA256,
	}
	if err := writeLocalRecoveryJournal(root, journal); err != nil {
		t.Fatal(err)
	}
	if switched {
		if err := atomicReplaceDirectory(generationDir, outputDir, true); err != nil {
			t.Fatal(err)
		}
	}
	return outputDir, reconcile
}

func TestLocalProductionReconcileModesIgnoreRestrictiveUmask(t *testing.T) {
	for _, umask := range []int{0o027, 0o077} {
		t.Run(fmt.Sprintf("%04o", umask), func(t *testing.T) {
			root := t.TempDir()
			outputDir := filepath.Join(root, "repository")
			initial := stageProductionFixture(t, "initialize", nil)
			if err := CommitLocalProductionTransaction(context.Background(), outputDir, initial); err != nil {
				t.Fatalf("initial CommitLocalProductionTransaction() error = %v", err)
			}
			prior := localTreeSnapshotForTest(t, outputDir)

			reconcile := stageProductionFixture(t, "reconcile", priorStateFor(t, initial))
			previousUmask := syscall.Umask(umask)
			defer syscall.Umask(previousUmask)

			if err := CommitLocalProductionTransaction(context.Background(), outputDir, reconcile); err != nil {
				t.Fatalf("reconcile CommitLocalProductionTransaction() error = %v", err)
			}

			assertLocalGenerationModes(t, outputDir, reconcile, prior)
		})
	}
}

func TestLocalProductionPreservesUnrelatedDirectoryModesWithRestrictiveUmask(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "repository")
	writeStableLocalFixture(t, outputDir)

	groupWritable := filepath.Join(outputDir, "dists", "stable", "group-writable")
	setgid := filepath.Join(outputDir, "dists", "stable", "setgid")
	for path, mode := range map[string]fs.FileMode{
		outputDir:     os.ModeSetgid | 0o775,
		groupWritable: 0o775,
		setgid:        os.ModeSetgid | 0o775,
	} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	prior := localTreeSnapshotForTest(t, outputDir)

	previousUmask := syscall.Umask(0o077)
	defer syscall.Umask(previousUmask)

	transaction := stageProductionFixture(t, "initialize", nil)
	if err := CommitLocalProductionTransaction(context.Background(), outputDir, transaction); err != nil {
		t.Fatalf("CommitLocalProductionTransaction() error = %v", err)
	}

	assertLocalMode(t, outputDir, os.ModeDir|os.ModeSetgid|0o775)
	assertLocalMode(t, groupWritable, os.ModeDir|0o775)
	assertLocalMode(t, setgid, os.ModeDir|os.ModeSetgid|0o775)
	assertLocalGenerationModes(t, outputDir, transaction, prior)
}

func TestLocalProductionRejectsPriorSpecialModeDrift(t *testing.T) {
	root := t.TempDir()
	outputDir := filepath.Join(root, "repository")
	writeStableLocalFixture(t, outputDir)
	watched := filepath.Join(outputDir, "dists", "stable")
	if err := os.Chmod(watched, os.ModeSetgid|0o755); err != nil {
		t.Fatal(err)
	}

	transaction := stageProductionFixture(t, "initialize", nil)
	changed := false
	err := commitLocalProductionTransaction(
		context.Background(),
		outputDir,
		transaction,
		&productionLocalHooks{before: func(step string) error {
			if step == "commit:verify-prior:." {
				changed = true
				return os.Chmod(watched, 0o755)
			}
			return nil
		}},
	)
	if !changed {
		t.Fatal("special-mode drift hook was not exercised")
	}
	if !errors.Is(err, ErrLocalGeneration) {
		t.Fatalf("special-mode drift error = %v, want ErrLocalGeneration", err)
	}
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

func assertLocalMode(t *testing.T, path string, want fs.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode() != want {
		t.Fatalf("%s mode = %v, want %v", path, info.Mode(), want)
	}
}

func assertLocalGenerationModes(
	t *testing.T,
	root string,
	transaction *ProductionTransaction,
	prior map[string]localTreeEntry,
) {
	t.Helper()
	target := filepath.Join("dists", transaction.Codename)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		priorEntry, retained := prior[relative]
		replaced := relative == target ||
			strings.HasPrefix(relative, target+string(filepath.Separator))
		if retained && !replaced {
			if info, err := entry.Info(); err != nil {
				return err
			} else if info.Mode() != priorEntry.Mode {
				t.Errorf("%s retained mode = %v, want %v", relative, info.Mode(), priorEntry.Mode)
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		want := fs.FileMode(0o644)
		if entry.IsDir() {
			want = os.ModeDir | 0o755
		}
		if info.Mode() != want {
			t.Errorf("%s generated mode = %v, want %v", relative, info.Mode(), want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
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

func assertStableGenerationSyncSteps(t *testing.T, steps []string) {
	t.Helper()
	const filePrefix = "commit:sync-file:"
	const directoryPrefix = "commit:sync-directory:"
	count := 0
	for _, step := range steps {
		relative := ""
		switch {
		case strings.HasPrefix(step, filePrefix):
			relative = strings.TrimPrefix(step, filePrefix)
		case strings.HasPrefix(step, directoryPrefix):
			relative = strings.TrimPrefix(step, directoryPrefix)
		default:
			continue
		}
		count++
		if filepath.IsAbs(filepath.FromSlash(relative)) ||
			strings.Contains(relative, ".repogen-generation-") {
			t.Errorf("unstable generation sync step %q", step)
		}
	}
	if count == 0 {
		t.Fatal("commit exposed no generation synchronization steps")
	}
}

func sanitizeTestName(value string) string {
	value = strings.ReplaceAll(value, string(filepath.Separator), "-")
	value = strings.ReplaceAll(value, ":", "-")
	return value
}

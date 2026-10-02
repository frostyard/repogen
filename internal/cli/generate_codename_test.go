package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func copyDebFixture(t *testing.T, inputDir string) {
	t.Helper()
	src := filepath.Join("..", "..", "test", "fixtures", "debs", "repogen-test_1.0.0_amd64.deb")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read deb fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inputDir, filepath.Base(src)), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateDebianRequiresExplicitCodename(t *testing.T) {
	inputDir := t.TempDir()
	copyDebFixture(t, inputDir)
	outputDir := filepath.Join(t.TempDir(), "repository")

	cmd := NewGenerateCmd()
	cmd.SetArgs([]string{"--input-dir", inputDir, "--output-dir", outputDir})
	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "--codename is required for Debian") {
		t.Fatalf("error = %v, want --codename is required for Debian", err)
	}
	if _, statErr := os.Stat(outputDir); !os.IsNotExist(statErr) {
		t.Fatalf("output dir exists after refused generation: %v", statErr)
	}
}

func TestGenerateDebianRejectsSuiteDifferentFromCodename(t *testing.T) {
	inputDir := t.TempDir()
	copyDebFixture(t, inputDir)
	outputDir := filepath.Join(t.TempDir(), "repository")

	cmd := NewGenerateCmd()
	cmd.SetArgs([]string{"--input-dir", inputDir, "--output-dir", outputDir, "--codename", "trixie", "--suite", "stable"})
	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "--suite must equal --codename") {
		t.Fatalf("error = %v, want --suite must equal --codename", err)
	}
	if _, statErr := os.Stat(outputDir); !os.IsNotExist(statErr) {
		t.Fatalf("output dir exists after refused generation: %v", statErr)
	}
}

func TestGenerateSysextNeedsNoCodename(t *testing.T) {
	inputDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(inputDir, "incus_7.3_13_x86-64.raw"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(t.TempDir(), "repository")

	cmd := NewGenerateCmd()
	cmd.SetArgs([]string{"--input-dir", inputDir, "--output-dir", outputDir, "--base-url", "https://example.com/repo"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("sysext generation without --codename failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "ext", "incus", "SHA256SUMS")); err != nil {
		t.Fatalf("sysext manifest missing: %v", err)
	}
}

func TestGenerateIncrementalDebianFailsOnCorruptArchWithoutMutation(t *testing.T) {
	inputDir := t.TempDir()
	copyDebFixture(t, inputDir)
	outputDir := filepath.Join(t.TempDir(), "repository")
	baseArgs := []string{"--input-dir", inputDir, "--output-dir", outputDir, "--codename", "testing", "--arch", "all,amd64"}

	initial := NewGenerateCmd()
	initial.SetArgs(baseArgs)
	if err := initial.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("initial generation failed: %v", err)
	}
	packagesPath := filepath.Join(outputDir, "dists", "testing", "main", "binary-amd64", "Packages")
	if err := os.WriteFile(packagesPath, []byte("garbage without colon\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotGenerateTree(t, outputDir)

	reconcile := NewGenerateCmd()
	reconcile.SetArgs(append(append([]string(nil), baseArgs...), "--incremental"))
	err := reconcile.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "incremental deb restore") {
		t.Fatalf("error = %v, want incremental deb restore failure", err)
	}
	assertGenerateTree(t, outputDir, before)
}

func TestGenerateIncrementalDebianInitializesEmptyOutput(t *testing.T) {
	inputDir := t.TempDir()
	copyDebFixture(t, inputDir)
	outputDir := filepath.Join(t.TempDir(), "repository")

	cmd := NewGenerateCmd()
	cmd.SetArgs([]string{"--input-dir", inputDir, "--output-dir", outputDir, "--codename", "testing", "--incremental"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("incremental generation into empty output failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "dists", "testing", "main", "binary-amd64", "Packages")); err != nil {
		t.Fatalf("Packages missing: %v", err)
	}
}

func TestGenerateIncrementalDebianCorruptionStopsMixedRunBeforeAnyWrite(t *testing.T) {
	inputDir := t.TempDir()
	copyDebFixture(t, inputDir)
	outputDir := filepath.Join(t.TempDir(), "repository")
	initial := NewGenerateCmd()
	initial.SetArgs([]string{"--input-dir", inputDir, "--output-dir", outputDir, "--codename", "testing"})
	if err := initial.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("initial generation failed: %v", err)
	}
	packagesPath := filepath.Join(outputDir, "dists", "testing", "main", "binary-amd64", "Packages")
	if err := os.WriteFile(packagesPath, []byte("garbage without colon\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inputDir, "incus_7.3_13_x86-64.raw"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotGenerateTree(t, outputDir)

	// Map iteration order is random; repeat so a sysext-first order would show.
	for i := 0; i < 8; i++ {
		cmd := NewGenerateCmd()
		cmd.SetArgs([]string{"--input-dir", inputDir, "--output-dir", outputDir, "--codename", "testing", "--base-url", "https://example.com/repo", "--incremental"})
		err := cmd.ExecuteContext(context.Background())
		if err == nil || !strings.Contains(err.Error(), "incremental deb restore") {
			t.Fatalf("error = %v, want incremental deb restore failure", err)
		}
		assertGenerateTree(t, outputDir, before)
	}
}

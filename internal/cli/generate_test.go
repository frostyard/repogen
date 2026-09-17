package cli

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateSkipDuplicatesRejectsChangedSignedSysextWithoutMutation(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "repository")
	imagePath := filepath.Join(inputDir, "incus_7.3_13_x86-64.raw")
	if err := os.WriteFile(imagePath, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join("..", "..", "test", "fixtures", "gpg-keys", "test-key.asc")
	baseArgs := []string{
		"--input-dir", inputDir,
		"--output-dir", outputDir,
		"--base-url", "https://example.com/repo",
		"--gpg-key", keyPath,
	}

	initial := NewGenerateCmd()
	initial.SetArgs(baseArgs)
	if err := initial.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("initial signed generation failed: %v", err)
	}
	before := snapshotGenerateTree(t, filepath.Join(outputDir, "ext"))

	if err := os.WriteFile(imagePath, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	reconcile := NewGenerateCmd()
	reconcile.SetArgs(append(append([]string(nil), baseArgs...), "--incremental", "--skip-duplicates"))
	err := reconcile.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "changed or unverifiable bytes") {
		t.Fatalf("signed --skip-duplicates reconciliation error = %v, want digest conflict", err)
	}
	assertGenerateTree(t, filepath.Join(outputDir, "ext"), before)
}

func TestGenerateIncrementalSignedSysextRetainsMetadataOnlyOSVersion(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "repository")
	keyPath := filepath.Join("..", "..", "test", "fixtures", "gpg-keys", "test-key.asc")
	baseArgs := []string{
		"--input-dir", inputDir,
		"--output-dir", outputDir,
		"--base-url", "https://example.com/repo",
		"--gpg-key", keyPath,
	}

	trixiePath := filepath.Join(inputDir, "incus_7.3_13_x86-64.raw")
	if err := os.WriteFile(trixiePath, []byte("trixie"), 0o644); err != nil {
		t.Fatal(err)
	}
	initial := NewGenerateCmd()
	initial.SetArgs(baseArgs)
	if err := initial.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("initial signed generation failed: %v", err)
	}

	retainedPayload := filepath.Join(outputDir, "ext", "incus", filepath.Base(trixiePath))
	if err := os.Remove(retainedPayload); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(trixiePath); err != nil {
		t.Fatal(err)
	}
	forkyPath := filepath.Join(inputDir, "incus_7.3_14_x86-64.raw")
	if err := os.WriteFile(forkyPath, []byte("forky"), 0o644); err != nil {
		t.Fatal(err)
	}

	reconcile := NewGenerateCmd()
	reconcile.SetArgs(append(append([]string(nil), baseArgs...), "--incremental", "--skip-duplicates"))
	if err := reconcile.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("metadata-only signed reconciliation failed: %v", err)
	}

	manifestPath := filepath.Join(outputDir, "ext", "incus", "SHA256SUMS")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	expectedEntries := map[string][32]byte{
		filepath.Base(trixiePath): sha256.Sum256([]byte("trixie")),
		filepath.Base(forkyPath):  sha256.Sum256([]byte("forky")),
	}
	for filename, digest := range expectedEntries {
		entry := fmt.Sprintf("%x  %s\n", digest, filename)
		if !strings.Contains(string(manifest), entry) {
			t.Fatalf("reconciled manifest missing %q:\n%s", entry, manifest)
		}
	}
	if _, err := os.Stat(retainedPayload); !os.IsNotExist(err) {
		t.Fatalf("metadata-only retained payload unexpectedly materialized: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "ext", "incus", filepath.Base(forkyPath))); err != nil {
		t.Fatalf("incoming Forky payload missing: %v", err)
	}
	if _, err := os.Stat(manifestPath + ".gpg"); err != nil {
		t.Fatalf("reconciled manifest signature missing: %v", err)
	}
	index, err := os.ReadFile(filepath.Join(outputDir, "ext", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(index), "incus\n"; got != want {
		t.Fatalf("reconciled index = %q, want %q", got, want)
	}
}

func snapshotGenerateTree(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	snapshot := make(map[string][32]byte)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		snapshot[relative] = sha256.Sum256(content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func assertGenerateTree(t *testing.T, root string, before map[string][32]byte) {
	t.Helper()
	after := snapshotGenerateTree(t, root)
	if len(after) != len(before) {
		t.Fatalf("failed reconciliation changed path count: before=%d after=%d", len(before), len(after))
	}
	for path, digest := range before {
		if after[path] != digest {
			t.Fatalf("failed reconciliation changed %s", path)
		}
	}
}

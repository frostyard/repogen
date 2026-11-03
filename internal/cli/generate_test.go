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

// skipPublishedFixture is the shared setup for the --skip-published-identities
// tests: an input directory, an output directory and the signed base arguments.
type skipPublishedFixture struct {
	t         *testing.T
	inputDir  string
	outputDir string
	baseArgs  []string
}

func newSkipPublishedFixture(t *testing.T) *skipPublishedFixture {
	t.Helper()

	fixture := &skipPublishedFixture{
		t:         t,
		inputDir:  t.TempDir(),
		outputDir: filepath.Join(t.TempDir(), "repository"),
	}
	fixture.baseArgs = []string{
		"--input-dir", fixture.inputDir,
		"--output-dir", fixture.outputDir,
		"--base-url", "https://example.com/repo",
		"--gpg-key", filepath.Join("..", "..", "test", "fixtures", "gpg-keys", "test-key.asc"),
	}

	return fixture
}

func (f *skipPublishedFixture) write(name, content string) string {
	f.t.Helper()

	path := filepath.Join(f.inputDir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}

	return path
}

func (f *skipPublishedFixture) run(args ...string) error {
	f.t.Helper()

	cmd := NewGenerateCmd()
	cmd.SetArgs(append(append([]string(nil), f.baseArgs...), args...))

	return cmd.ExecuteContext(context.Background())
}

func (f *skipPublishedFixture) mustRun(args ...string) {
	f.t.Helper()
	if err := f.run(args...); err != nil {
		f.t.Fatalf("generation with %v failed: %v", args, err)
	}
}

func (f *skipPublishedFixture) extDir() string {
	return filepath.Join(f.outputDir, "ext")
}

func (f *skipPublishedFixture) manifest() string {
	f.t.Helper()

	content, err := os.ReadFile(filepath.Join(f.extDir(), "incus", "SHA256SUMS"))
	if err != nil {
		f.t.Fatal(err)
	}

	return string(content)
}

func (f *skipPublishedFixture) reportContent(path string) string {
	f.t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatalf("skipped report %s: %v", path, err)
	}

	return string(content)
}

func TestGenerateSkipPublishedIdentitiesSkipsChangedBytes(t *testing.T) {
	fixture := newSkipPublishedFixture(t)
	imagePath := fixture.write("incus_7.3_13_x86-64.raw", "v1")
	fixture.mustRun()

	before := snapshotGenerateTree(t, fixture.extDir())
	reportPath := filepath.Join(t.TempDir(), "skipped.txt")

	if err := os.WriteFile(imagePath, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture.mustRun("--incremental", "--skip-published-identities", "--skipped-report", reportPath, "--html-index")

	assertGenerateTree(t, fixture.extDir(), before)
	if got, want := fixture.reportContent(reportPath), "ext/incus/incus_7.3_13_x86-64.raw\n"; got != want {
		t.Fatalf("skipped report = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(fixture.extDir(), "index.html")); !os.IsNotExist(err) {
		t.Fatalf("no-op run generated an HTML index: %v", err)
	}
}

func TestGenerateSkipPublishedIdentitiesKeepsUnskippedPackages(t *testing.T) {
	fixture := newSkipPublishedFixture(t)
	oldPath := fixture.write("incus_7.3_13_x86-64.raw", "v1")
	fixture.mustRun()
	reportPath := filepath.Join(t.TempDir(), "skipped.txt")

	if err := os.WriteFile(oldPath, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture.write("incus_7.4_13_x86-64.raw", "new")
	fixture.mustRun("--incremental", "--skip-published-identities", "--skipped-report", reportPath)

	manifest := fixture.manifest()
	wantOld := fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte("v1")), "incus_7.3_13_x86-64.raw")
	if !strings.Contains(manifest, wantOld) {
		t.Fatalf("manifest lost the published 7.3 entry with its old digest:\n%s", manifest)
	}
	wantNew := fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte("new")), "incus_7.4_13_x86-64.raw")
	if !strings.Contains(manifest, wantNew) {
		t.Fatalf("manifest is missing the new 7.4 entry:\n%s", manifest)
	}
	if _, err := os.Stat(filepath.Join(fixture.extDir(), "incus", "SHA256SUMS.gpg")); err != nil {
		t.Fatalf("reconciled manifest signature missing: %v", err)
	}
	if got, want := fixture.reportContent(reportPath), "ext/incus/incus_7.3_13_x86-64.raw\n"; got != want {
		t.Fatalf("skipped report = %q, want %q", got, want)
	}
}

func TestGenerateSkipPublishedIdentitiesComparesIdentityOnly(t *testing.T) {
	fixture := newSkipPublishedFixture(t)
	fixture.write("incus_7.3_13_x86-64.raw", "v1")
	fixture.mustRun()

	before := snapshotGenerateTree(t, fixture.extDir())
	reportPath := filepath.Join(t.TempDir(), "skipped.txt")

	// The same identity rebuilt with another compression suffix is a rebuild of
	// a published image, so it is skipped whatever the bytes.
	if err := os.Remove(filepath.Join(fixture.inputDir, "incus_7.3_13_x86-64.raw")); err != nil {
		t.Fatal(err)
	}
	fixture.write("incus_7.3_13_x86-64.raw.zst", "compressed v2")
	fixture.mustRun("--incremental", "--skip-published-identities", "--skipped-report", reportPath)

	assertGenerateTree(t, fixture.extDir(), before)
	if got, want := fixture.reportContent(reportPath), "ext/incus/incus_7.3_13_x86-64.raw\n"; got != want {
		t.Fatalf("skipped report = %q, want %q", got, want)
	}
}

func TestGenerateSkipPublishedIdentitiesFailsClosedOnTamperedManifest(t *testing.T) {
	fixture := newSkipPublishedFixture(t)
	imagePath := fixture.write("incus_7.3_13_x86-64.raw", "v1")
	fixture.mustRun()

	manifestPath := filepath.Join(fixture.extDir(), "incus", "SHA256SUMS")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(manifest))
	if len(fields) < 2 {
		t.Fatalf("fixture manifest has no digest: %q", manifest)
	}
	digest := fields[0]
	flipped := []byte{'0'}
	if digest[0] == '0' {
		flipped = []byte{'1'}
	}
	tampered := []byte(strings.Replace(string(manifest), digest, string(flipped)+digest[1:], 1))
	if err := os.WriteFile(manifestPath, tampered, 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotGenerateTree(t, fixture.extDir())
	reportPath := filepath.Join(t.TempDir(), "skipped.txt")

	if err := os.WriteFile(imagePath, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = fixture.run("--incremental", "--skip-published-identities", "--skipped-report", reportPath)
	if err == nil || !strings.Contains(err.Error(), "signature-verified") {
		t.Fatalf("tampered manifest error = %v, want a signature-verified read failure", err)
	}
	assertGenerateTree(t, fixture.extDir(), before)
	if _, statErr := os.Stat(reportPath); !os.IsNotExist(statErr) {
		t.Fatalf("skipped report was written although the run failed: %v", statErr)
	}
}

func TestGenerateSkipPublishedIdentitiesRequiresIncremental(t *testing.T) {
	fixture := newSkipPublishedFixture(t)
	fixture.write("incus_7.3_13_x86-64.raw", "v1")

	err := fixture.run("--skip-published-identities")
	if err == nil || !strings.Contains(err.Error(), "--incremental") {
		t.Fatalf("--skip-published-identities without --incremental error = %v", err)
	}

	reportPath := filepath.Join(t.TempDir(), "skipped.txt")
	err = fixture.run("--incremental", "--skipped-report", reportPath)
	if err == nil || !strings.Contains(err.Error(), "--skip-published-identities") {
		t.Fatalf("--skipped-report without --skip-published-identities error = %v", err)
	}
	if _, statErr := os.Stat(reportPath); !os.IsNotExist(statErr) {
		t.Fatalf("skipped report was written for a refused run: %v", statErr)
	}
}

func TestGenerateSkipPublishedIdentitiesFirstPublishWritesEmptyReport(t *testing.T) {
	fixture := newSkipPublishedFixture(t)
	fixture.write("incus_7.3_13_x86-64.raw", "v1")
	reportPath := filepath.Join(t.TempDir(), "skipped.txt")

	fixture.mustRun("--incremental", "--skip-published-identities", "--skipped-report", reportPath)

	if _, err := os.Stat(filepath.Join(fixture.extDir(), "incus", "incus_7.3_13_x86-64.raw")); err != nil {
		t.Fatalf("first publish did not publish the payload: %v", err)
	}
	if got, want := fixture.reportContent(reportPath), ""; got != want {
		t.Fatalf("first-publish skipped report = %q, want %q", got, want)
	}
}

func TestGenerateSkipPublishedIdentitiesRefusesOtherPackageTypes(t *testing.T) {
	fixture := newSkipPublishedFixture(t)
	fixture.write("incus_7.3_13_x86-64.raw", "v1")
	fixture.write("foo--1.0.arm64_sonoma.bottle.tar.gz", "bottle")

	err := fixture.run("--incremental", "--skip-published-identities")
	if err == nil || !strings.Contains(err.Error(), "supports only sysext") {
		t.Fatalf("mixed-type error = %v, want a sysext-only refusal", err)
	}
	if _, statErr := os.Stat(fixture.extDir()); !os.IsNotExist(statErr) {
		t.Fatalf("refused run wrote output: %v", statErr)
	}
}

func TestGenerateSkipPublishedIdentitiesFailsOnUnparsableSysext(t *testing.T) {
	fixture := newSkipPublishedFixture(t)
	fixture.write("incus_7.3_13_x86-64.raw", "v1")
	fixture.mustRun()
	before := snapshotGenerateTree(t, fixture.extDir())

	// Detected as a sysext by its suffix, but the name has no OSVersion field.
	fixture.write("incus_7.4_x86-64.raw", "v2")
	report := filepath.Join(t.TempDir(), "skipped.txt")
	err := fixture.run("--incremental", "--skip-published-identities", "--skipped-report", report)
	if err == nil || !strings.Contains(err.Error(), "failed to parse") {
		t.Fatalf("unparsable sysext error = %v, want a parse failure", err)
	}
	assertGenerateTree(t, fixture.extDir(), before)
	if _, statErr := os.Stat(report); !os.IsNotExist(statErr) {
		t.Fatalf("skipped report was written although the run failed: %v", statErr)
	}
}

func TestGenerateSkipPublishedIdentitiesRequiresSigningKey(t *testing.T) {
	fixture := newSkipPublishedFixture(t)
	fixture.write("incus_7.3_13_x86-64.raw", "v1")

	cmd := NewGenerateCmd()
	cmd.SetArgs([]string{
		"--input-dir", fixture.inputDir,
		"--output-dir", fixture.outputDir,
		"--base-url", "https://example.com/repo",
		"--incremental",
		"--skip-published-identities",
	})
	err := cmd.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "requires --gpg-key") {
		t.Fatalf("missing --gpg-key error = %v, want a signing-key requirement", err)
	}
	if _, statErr := os.Stat(fixture.extDir()); !os.IsNotExist(statErr) {
		t.Fatalf("refused run wrote output: %v", statErr)
	}
}

func TestGenerateSkipPublishedIdentitiesRejectsUnsignedPublishedRepo(t *testing.T) {
	fixture := newSkipPublishedFixture(t)
	imagePath := fixture.write("incus_7.3_13_x86-64.raw", "v1")

	// The published repository has no signatures: it was generated without a key.
	unsigned := NewGenerateCmd()
	unsigned.SetArgs([]string{
		"--input-dir", fixture.inputDir,
		"--output-dir", fixture.outputDir,
		"--base-url", "https://example.com/repo",
	})
	if err := unsigned.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("unsigned generation failed: %v", err)
	}
	before := snapshotGenerateTree(t, fixture.extDir())
	reportPath := filepath.Join(t.TempDir(), "skipped.txt")

	if err := os.WriteFile(imagePath, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := fixture.run("--incremental", "--skip-published-identities", "--skipped-report", reportPath)
	if err == nil || !strings.Contains(err.Error(), "signature-verified") {
		t.Fatalf("unsigned published repository error = %v, want a signature-verified read failure", err)
	}
	assertGenerateTree(t, fixture.extDir(), before)
	if _, statErr := os.Stat(reportPath); !os.IsNotExist(statErr) {
		t.Fatalf("skipped report was written although the run failed: %v", statErr)
	}
}

func TestGenerateSkipPublishedIdentitiesRejectsUndetectedCandidate(t *testing.T) {
	fixture := newSkipPublishedFixture(t)
	imagePath := fixture.write("incus_7.3_13_x86-64.raw", "v1")
	fixture.mustRun()
	before := snapshotGenerateTree(t, fixture.extDir())
	reportPath := filepath.Join(t.TempDir(), "skipped.txt")

	if err := os.WriteFile(imagePath, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A zero-byte file ends in .raw, so it is a sysext candidate, but magic byte
	// detection fails with EOF and the scanner drops it.
	fixture.write("incus_7.4_13_x86-64.raw", "")

	err := fixture.run("--incremental", "--skip-published-identities", "--skipped-report", reportPath)
	if err == nil || !strings.Contains(err.Error(), "was not detected as a sysext") {
		t.Fatalf("undetected candidate error = %v, want a detection refusal", err)
	}
	assertGenerateTree(t, fixture.extDir(), before)
	if _, statErr := os.Stat(reportPath); !os.IsNotExist(statErr) {
		t.Fatalf("skipped report was written although the run failed: %v", statErr)
	}
}

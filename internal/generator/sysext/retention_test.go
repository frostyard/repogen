package sysext

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/frostyard/repogen/internal/models"
)

func TestCompareVersionsUsesSystemdOrder(t *testing.T) {
	tests := []struct {
		older, newer string
	}{
		{"9", "10"},
		{"1.9", "1.10"},
		{"1.0", "1.0.1"},
		{"1.0~rc1", "1.0"},
		{"1.0-1", "1.0.1"},
		{"123", "123a"},
		{"26.1027.51404+r1", "26.1027.51404+r2"},
		{"26.1027.51404+r9", "26.1027.51404+r10"},
		{"26.1027.51404+r1", "26.1103.1+r1"},
		{"7.3", "7.10"},
		{"1.0", "1.0^p1"},
		{"a", "b"},
		{"abc", "abcd"},
		{"123.a", "123a"},
	}
	for _, tt := range tests {
		if got := compareVersions(tt.older, tt.newer); got >= 0 {
			t.Errorf("compareVersions(%q, %q) = %d, want < 0", tt.older, tt.newer, got)
		}
		if got := compareVersions(tt.newer, tt.older); got <= 0 {
			t.Errorf("compareVersions(%q, %q) = %d, want > 0", tt.newer, tt.older, got)
		}
	}
	for _, same := range [][2]string{{"1.0", "1.0"}, {"1.02", "1.2"}, {"7.3+r1", "7.3_r1"}} {
		if got := compareVersions(same[0], same[1]); got != 0 {
			t.Errorf("compareVersions(%q, %q) = %d, want 0", same[0], same[1], got)
		}
	}
}

// retentionFixture publishes sysext files of 2 OS versions (trixie as 13,
// forky as 14) into one output tree, run after run, as snosi does.
type retentionFixture struct {
	t         *testing.T
	inputDir  string
	outputDir string
	signer    *testSigner
}

func newRetentionFixture(t *testing.T) *retentionFixture {
	t.Helper()
	return &retentionFixture{
		t:         t,
		inputDir:  t.TempDir(),
		outputDir: filepath.Join(t.TempDir(), "repository"),
		signer:    newTestSigner(t),
	}
}

// publish runs one incremental generation of the named files with the given
// retention and returns the generator's pruned report.
func (f *retentionFixture) publish(keep int, names ...string) []string {
	f.t.Helper()
	packages := make([]models.Package, 0, len(names))
	for _, name := range names {
		path := filepath.Join(f.inputDir, name)
		if err := os.WriteFile(path, []byte("image "+name), 0o644); err != nil {
			f.t.Fatal(err)
		}
		pkg, err := ParsePackage(path)
		if err != nil {
			f.t.Fatal(err)
		}
		packages = append(packages, *pkg)
	}
	gen := NewGenerator("https://example.com/repo", f.signer).(*Generator)
	config := &models.RepositoryConfig{
		OutputDir:          f.outputDir,
		Incremental:        true,
		SysextKeepVersions: keep,
	}
	if err := gen.Generate(context.Background(), config, packages); err != nil {
		f.t.Fatalf("generate %v with keep %d: %v", names, keep, err)
	}
	return gen.PrunedPayloads()
}

func (f *retentionFixture) manifestFiles(name string) []string {
	f.t.Helper()
	content, err := os.ReadFile(filepath.Join(f.outputDir, "ext", name, "SHA256SUMS"))
	if err != nil {
		f.t.Fatal(err)
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(content)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			f.t.Fatalf("bad manifest line %q", line)
		}
		files = append(files, fields[1])
	}
	return files
}

func (f *retentionFixture) payloadExists(name, file string) bool {
	f.t.Helper()
	_, err := os.Stat(filepath.Join(f.outputDir, "ext", name, file))
	if err == nil {
		return true
	}
	if !os.IsNotExist(err) {
		f.t.Fatal(err)
	}
	return false
}

func TestRetentionKeepsNewestVersionPerOSVersion(t *testing.T) {
	fixture := newRetentionFixture(t)

	// Trixie publishes 3 versions; "10" is the newest in systemd order, not
	// in string order.
	pruned := fixture.publish(1,
		"incus_2_13_x86-64.raw",
		"incus_9_13_x86-64.raw",
		"incus_10_13_x86-64.raw",
	)
	if want := []string{"ext/incus/incus_2_13_x86-64.raw", "ext/incus/incus_9_13_x86-64.raw"}; !slices.Equal(pruned, want) {
		t.Fatalf("first run pruned %v, want %v", pruned, want)
	}
	if got, want := fixture.manifestFiles("incus"), []string{"incus_10_13_x86-64.raw"}; !slices.Equal(got, want) {
		t.Fatalf("manifest after first run = %v, want %v", got, want)
	}
	for _, gone := range []string{"incus_2_13_x86-64.raw", "incus_9_13_x86-64.raw"} {
		if fixture.payloadExists("incus", gone) {
			t.Fatalf("pruned payload %s is still in the output tree", gone)
		}
	}

	// Forky publishes an older version than trixie's newest, and 2 arches.
	// Each OS version and arch is its own group, so nothing of trixie goes.
	pruned = fixture.publish(1,
		"incus_8_14_x86-64.raw",
		"incus_8_14_arm64.raw",
	)
	if len(pruned) != 0 {
		t.Fatalf("forky run pruned %v, want nothing", pruned)
	}
	want := []string{"incus_10_13_x86-64.raw", "incus_8_14_arm64.raw", "incus_8_14_x86-64.raw"}
	if got := fixture.manifestFiles("incus"); !slices.Equal(got, want) {
		t.Fatalf("manifest after forky run = %v, want %v", got, want)
	}

	// A newer trixie build prunes only the older trixie entry.
	pruned = fixture.publish(1, "incus_11_13_x86-64.raw")
	if want := []string{"ext/incus/incus_10_13_x86-64.raw"}; !slices.Equal(pruned, want) {
		t.Fatalf("second trixie run pruned %v, want %v", pruned, want)
	}
	want = []string{"incus_11_13_x86-64.raw", "incus_8_14_arm64.raw", "incus_8_14_x86-64.raw"}
	if got := fixture.manifestFiles("incus"); !slices.Equal(got, want) {
		t.Fatalf("manifest after second trixie run = %v, want %v", got, want)
	}

	// The pruned tree is still a valid signed repository for the next run.
	if _, err := os.Stat(filepath.Join(fixture.outputDir, "ext", "incus", "SHA256SUMS.gpg")); err != nil {
		t.Fatalf("signature missing after pruning: %v", err)
	}
	if pruned := fixture.publish(1, "incus_8_14_x86-64.raw"); len(pruned) != 0 {
		t.Fatalf("republishing a kept identity pruned %v", pruned)
	}
}

func TestRetentionDropsIncomingVersionOlderThanKept(t *testing.T) {
	fixture := newRetentionFixture(t)
	fixture.publish(1, "incus_10_13_x86-64.raw")

	pruned := fixture.publish(1, "incus_9_13_x86-64.raw")
	if want := []string{"ext/incus/incus_9_13_x86-64.raw"}; !slices.Equal(pruned, want) {
		t.Fatalf("pruned %v, want %v", pruned, want)
	}
	if got, want := fixture.manifestFiles("incus"), []string{"incus_10_13_x86-64.raw"}; !slices.Equal(got, want) {
		t.Fatalf("manifest = %v, want %v", got, want)
	}
	if fixture.payloadExists("incus", "incus_9_13_x86-64.raw") {
		t.Fatal("an incoming payload that retention dropped was copied into the tree")
	}
}

func TestRetentionKeepsTwoNewest(t *testing.T) {
	fixture := newRetentionFixture(t)
	pruned := fixture.publish(2,
		"docker_1_13_x86-64.raw",
		"docker_2_13_x86-64.raw",
		"docker_3_13_x86-64.raw",
		"docker_1_14_x86-64.raw",
	)
	if want := []string{"ext/docker/docker_1_13_x86-64.raw"}; !slices.Equal(pruned, want) {
		t.Fatalf("pruned %v, want %v", pruned, want)
	}
	want := []string{"docker_1_14_x86-64.raw", "docker_2_13_x86-64.raw", "docker_3_13_x86-64.raw"}
	if got := fixture.manifestFiles("docker"); !slices.Equal(got, want) {
		t.Fatalf("manifest = %v, want %v", got, want)
	}
}

func TestRetentionOffKeepsEveryVersion(t *testing.T) {
	fixture := newRetentionFixture(t)
	names := []string{
		"incus_2_13_x86-64.raw",
		"incus_9_13_x86-64.raw",
		"incus_10_13_x86-64.raw",
		"incus_1_14_x86-64.raw",
		"incus_3_14_x86-64.raw",
	}
	if pruned := fixture.publish(0, names...); len(pruned) != 0 {
		t.Fatalf("keep 0 pruned %v, want nothing", pruned)
	}
	want := slices.Clone(names)
	slices.Sort(want)
	if got := fixture.manifestFiles("incus"); !slices.Equal(got, want) {
		t.Fatalf("manifest = %v, want %v", got, want)
	}
	for _, name := range names {
		if !fixture.payloadExists("incus", name) {
			t.Fatalf("keep 0 removed payload %s", name)
		}
	}
}

// FuzzCompareVersions checks that the comparison ends and is antisymmetric
// for any input, so retention can sort with it.
func FuzzCompareVersions(f *testing.F) {
	for _, seed := range [][2]string{
		{"1.0", "1.0.1"}, {"1~rc1", "1"}, {"1-", "1-."}, {"..", "."}, {"~", "~~"},
		{"^", "-"}, {"26.1027.51404+r1", "26.1027.51404+r2"}, {"", "1"}, {"+", "_"},
	} {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		ab := compareVersions(a, b)
		ba := compareVersions(b, a)
		if (ab < 0) != (ba > 0) || (ab == 0) != (ba == 0) {
			t.Fatalf("compareVersions(%q, %q) = %d but compareVersions(%q, %q) = %d", a, b, ab, b, a, ba)
		}
	})
}

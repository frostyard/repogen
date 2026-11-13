package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateSysextKeepVersionsWritesPrunedReport(t *testing.T) {
	fixture := newSkipPublishedFixture(t)
	fixture.write("incus_9_13_x86-64.raw", "trixie 9")
	fixture.write("incus_10_13_x86-64.raw", "trixie 10")
	fixture.write("incus_7_14_x86-64.raw", "forky 7")
	reportPath := filepath.Join(t.TempDir(), "pruned.txt")

	fixture.mustRun("--incremental", "--sysext-keep-versions", "1", "--pruned-report", reportPath)

	if got, want := fixture.reportContent(reportPath), "ext/incus/incus_9_13_x86-64.raw\n"; got != want {
		t.Fatalf("pruned report = %q, want %q", got, want)
	}
	manifest := fixture.manifest()
	if strings.Contains(manifest, "incus_9_13_x86-64.raw") {
		t.Fatalf("manifest still lists the pruned entry:\n%s", manifest)
	}
	for _, kept := range []string{"incus_10_13_x86-64.raw", "incus_7_14_x86-64.raw"} {
		if !strings.Contains(manifest, kept) {
			t.Fatalf("manifest lost %s:\n%s", kept, manifest)
		}
	}
}

func TestGenerateSysextKeepVersionsDefaultPrunesNothing(t *testing.T) {
	fixture := newSkipPublishedFixture(t)
	fixture.write("incus_9_13_x86-64.raw", "trixie 9")
	fixture.write("incus_10_13_x86-64.raw", "trixie 10")

	fixture.mustRun("--incremental")

	manifest := fixture.manifest()
	for _, kept := range []string{"incus_9_13_x86-64.raw", "incus_10_13_x86-64.raw"} {
		if !strings.Contains(manifest, kept) {
			t.Fatalf("default run dropped %s:\n%s", kept, manifest)
		}
	}
}

func TestGenerateSysextKeepVersionsValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"negative", []string{"--sysext-keep-versions", "-1"}, "--sysext-keep-versions must be 0"},
		{"report without retention", []string{"--pruned-report", "report.txt"}, "--pruned-report requires --sysext-keep-versions"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newSkipPublishedFixture(t)
			fixture.write("incus_9_13_x86-64.raw", "trixie 9")
			err := fixture.run(tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if _, statErr := os.Stat(fixture.extDir()); !os.IsNotExist(statErr) {
				t.Fatalf("a refused configuration wrote output: %v", statErr)
			}
		})
	}
}

func TestGenerateSkipPublishedNoOpWritesEmptyPrunedReport(t *testing.T) {
	fixture := newSkipPublishedFixture(t)
	fixture.write("incus_9_13_x86-64.raw", "trixie 9")
	fixture.mustRun("--incremental")

	fixture.write("incus_9_13_x86-64.raw", "trixie 9 rebuilt")
	skipped := filepath.Join(t.TempDir(), "skipped.txt")
	pruned := filepath.Join(t.TempDir(), "pruned.txt")
	fixture.mustRun("--incremental", "--skip-published-identities", "--skipped-report", skipped,
		"--sysext-keep-versions", "1", "--pruned-report", pruned)

	if got := fixture.reportContent(pruned); got != "" {
		t.Fatalf("pruned report of a run that published nothing = %q, want empty", got)
	}
}

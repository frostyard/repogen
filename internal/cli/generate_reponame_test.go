package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writePacmanPackage writes a minimal Pacman package file to dir. The zstd
// magic bytes are enough for the scanner's type detection (which also accepts
// the .pkg.tar.zst suffix), and generation never parses the file here because
// the missing --repo-name stops the run before any parsing.
func writePacmanPackage(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create package directory %s: %v", dir, err)
	}
	content := append([]byte{0x28, 0xB5, 0x2F, 0xFD}, []byte("not a real pacman package")...)
	if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
		t.Fatalf("write pacman package: %v", err)
	}
}

// TestGeneratePacmanRequiresRepoName pins that --repo-name is required for
// every Pacman package the recursive generation scan finds, not only for one
// in the input root (issue #96).
func TestGeneratePacmanRequiresRepoName(t *testing.T) {
	const pacmanFile = "foo-1.0-1-x86_64.pkg.tar.zst"

	tests := []struct {
		name      string
		packageIn string
	}{
		{name: "input root", packageIn: "."},
		{name: "subdirectory", packageIn: "sub"},
		{name: "nested subdirectory", packageIn: filepath.Join("a", "b")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inputDir := t.TempDir()
			writePacmanPackage(t, filepath.Join(inputDir, tt.packageIn), pacmanFile)
			outputDir := filepath.Join(t.TempDir(), "repository")

			cmd := NewGenerateCmd()
			cmd.SetArgs([]string{"--input-dir", inputDir, "--output-dir", outputDir})
			err := cmd.ExecuteContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), "--repo-name is required for Pacman") {
				t.Fatalf("error = %v, want --repo-name is required for Pacman", err)
			}
			if _, statErr := os.Stat(outputDir); !os.IsNotExist(statErr) {
				t.Fatalf("output dir exists after refused generation: %v", statErr)
			}
		})
	}
}

// TestGenerateSkipPublishedIdentitiesHelpStatesKnownGap pins the known gap of
// the identity-level skip in the flag help (issue #99, ADR-0018).
func TestGenerateSkipPublishedIdentitiesHelpStatesKnownGap(t *testing.T) {
	flag := NewGenerateCmd().Flags().Lookup("skip-published-identities")
	if flag == nil {
		t.Fatal("--skip-published-identities flag is missing")
	}
	for _, want := range []string{"Known gap", "not republished"} {
		if !strings.Contains(flag.Usage, want) {
			t.Fatalf("--skip-published-identities help lacks %q: %s", want, flag.Usage)
		}
	}
}

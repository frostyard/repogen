package releaseasset

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	fixtureVersion = "1.2.3"
	fixtureTag     = "v" + fixtureVersion
	fixtureCommit  = "0123456789abcdef0123456789abcdef01234567"
)

func TestReleaseAssetMappingIsExactAndRejectsMutableVersions(t *testing.T) {
	root := repositoryRoot(t)
	script := filepath.Join(root, "scripts", "install-release.sh")
	for _, test := range []struct {
		name string
		tag  string
		arch string
		want string
	}{
		{name: "amd64", tag: fixtureTag, arch: "x86_64", want: "repogen-linux-amd64\n"},
		{name: "arm64", tag: fixtureTag, arch: "aarch64", want: "repogen-linux-arm64\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command("bash", script, "--asset-name", test.tag, test.arch)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("asset mapping failed: %v\n%s", err, output)
			}
			if got := string(output); got != test.want {
				t.Fatalf("asset mapping = %q, want %q", got, test.want)
			}
		})
	}

	command := exec.Command("bash", script, "--asset-name", "latest", "amd64")
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("mutable version unexpectedly accepted: %s", output)
	}
}

func TestDigestVerifiedReleaseInstallationAndEmbeddedIdentity(t *testing.T) {
	root := repositoryRoot(t)
	script := filepath.Join(root, "scripts", "install-release.sh")
	releaseRoot := filepath.Join(t.TempDir(), "releases", "download")
	tagRoot := filepath.Join(releaseRoot, fixtureTag)
	if err := os.MkdirAll(tagRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	asset := filepath.Join(tagRoot, "repogen-linux-amd64")
	build := exec.Command(
		"go",
		"build",
		"-o",
		asset,
		"-ldflags",
		"-s -w -X github.com/frostyard/repogen/internal/buildinfo.Version="+fixtureVersion+
			" -X github.com/frostyard/repogen/internal/buildinfo.Commit="+fixtureCommit,
		"./cmd/repogen",
	)
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("fixture build failed: %v\n%s", err, output)
	}
	writeChecksums(t, tagRoot, asset)

	destination := filepath.Join(t.TempDir(), "repogen")
	command := releaseInstallCommand(script, releaseRoot, fixtureTag, fixtureCommit, destination)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("verified install failed: %v\n%s", err, output)
	}
	identity := exec.Command(destination, "version", "--short")
	output, err := identity.CombinedOutput()
	if err != nil {
		t.Fatalf("installed binary failed: %v\n%s", err, output)
	}
	if got, want := string(output), fixtureVersion+" "+fixtureCommit+"\n"; got != want {
		t.Fatalf("installed identity = %q, want %q", got, want)
	}

	t.Run("digest mismatch", func(t *testing.T) {
		if err := os.WriteFile(
			filepath.Join(tagRoot, "SHA256SUMS"),
			[]byte(strings.Repeat("0", 64)+"  repogen-linux-amd64\n"),
			0o644,
		); err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(t.TempDir(), "repogen")
		command := releaseInstallCommand(script, releaseRoot, fixtureTag, fixtureCommit, destination)
		if output, err := command.CombinedOutput(); err == nil {
			t.Fatalf("digest mismatch unexpectedly installed binary: %s", output)
		}
		if _, err := os.Stat(destination); !os.IsNotExist(err) {
			t.Fatalf("digest mismatch left destination: %v", err)
		}
	})

	writeChecksums(t, tagRoot, asset)
	t.Run("embedded commit mismatch", func(t *testing.T) {
		destination := filepath.Join(t.TempDir(), "repogen")
		command := releaseInstallCommand(script, releaseRoot, fixtureTag, strings.Repeat("a", 40), destination)
		if output, err := command.CombinedOutput(); err == nil {
			t.Fatalf("identity mismatch unexpectedly installed binary: %s", output)
		}
		if _, err := os.Stat(destination); !os.IsNotExist(err) {
			t.Fatalf("identity mismatch left destination: %v", err)
		}
	})

	t.Run("embedded version mismatch", func(t *testing.T) {
		wrongTag := "v1.2.4"
		wrongTagRoot := filepath.Join(releaseRoot, wrongTag)
		if err := os.MkdirAll(wrongTagRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		wrongAsset := filepath.Join(wrongTagRoot, filepath.Base(asset))
		data, err := os.ReadFile(asset)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(wrongAsset, data, 0o755); err != nil {
			t.Fatal(err)
		}
		writeChecksums(t, wrongTagRoot, wrongAsset)

		destination := filepath.Join(t.TempDir(), "repogen")
		command := releaseInstallCommand(script, releaseRoot, wrongTag, fixtureCommit, destination)
		if output, err := command.CombinedOutput(); err == nil {
			t.Fatalf("version mismatch unexpectedly installed binary: %s", output)
		}
		if _, err := os.Stat(destination); !os.IsNotExist(err) {
			t.Fatalf("version mismatch left destination: %v", err)
		}
	})
}

func TestRepositoryReleaseContractHasOnePinnedPublisherAndVerifiedConsumer(t *testing.T) {
	root := repositoryRoot(t)
	release := readContractFile(t, filepath.Join(root, ".github", "workflows", "release.yml"))
	config := readContractFile(t, filepath.Join(root, ".goreleaser.yml"))
	action := readContractFile(t, filepath.Join(root, ".github", "actions", "publish-to-r2", "action.yml"))
	installer := readContractFile(t, filepath.Join(root, "scripts", "install-release.sh"))

	for _, required := range []string{
		"actions/checkout@11d5960a326750d5838078e36cf38b85af677262",
		"actions/setup-go@40f1582b2485089dde7abd97c1529aa768e1baff",
		"goreleaser/goreleaser-action@e435ccd777264be153ace6237001ef4d979d3a7a",
		"version: v2.18.1",
	} {
		if !strings.Contains(release, required) {
			t.Fatalf("authoritative release workflow lacks %q", required)
		}
	}
	for _, required := range []string{
		"internal/buildinfo.Version={{ .Version }}",
		"internal/buildinfo.Commit={{ .FullCommit }}",
		"name_template: \"repogen-{{ .Os }}-{{ .Arch }}\"",
		"name_template: SHA256SUMS",
		"- binary",
	} {
		if !strings.Contains(config, required) {
			t.Fatalf("GoReleaser contract lacks %q", required)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".github", "workflows", "goreleaser.yml")); !os.IsNotExist(err) {
		t.Fatalf("conflicting GoReleaser workflow still exists: %v", err)
	}
	for _, required := range []string{
		"repogen-version:",
		"repogen-commit:",
		"scripts/install-release.sh",
		"--github-release",
		"REPOGEN_VERSION: ${{ inputs.repogen-version }}",
		"REPOGEN_COMMIT: ${{ inputs.repogen-commit }}",
		"\"$REPOGEN_VERSION\"",
		"\"$REPOGEN_COMMIT\"",
		"legacy sync action is disabled for deb",
	} {
		if !strings.Contains(action, required) {
			t.Fatalf("consumer action lacks %q", required)
		}
	}
	if strings.Contains(action, "releases/latest") || strings.Contains(action, "default: 'latest'") {
		t.Fatal("consumer action retains a mutable release lookup")
	}
	for _, forbidden := range []string{
		"\"${{ inputs.repogen-version }}\"",
		"\"${{ inputs.repogen-commit }}\"",
	} {
		if strings.Contains(action, forbidden) {
			t.Fatalf("consumer action interpolates untrusted input directly into its shell script: %q", forbidden)
		}
	}
	if !strings.Contains(installer, `base_url="https://github.com/frostyard/repogen/releases/download"`) {
		t.Fatal("installer lacks its fixed production release origin")
	}
	for _, forbidden := range []string{"REPOGEN_RELEASE_BASE_URL", "REPOGEN_ALLOW_FILE_FIXTURE"} {
		if strings.Contains(installer, forbidden) {
			t.Fatalf("installer accepts caller-controlled release-origin environment variable %q", forbidden)
		}
	}
}

func releaseInstallCommand(script, releaseRoot, tag, commit, destination string) *exec.Cmd {
	return exec.Command(
		"bash",
		script,
		"--test-release-root",
		releaseRoot,
		tag,
		commit,
		"x86_64",
		destination,
	)
}

func writeChecksums(t *testing.T, directory, asset string) {
	t.Helper()
	data, err := os.ReadFile(asset)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	line := fmt.Sprintf("%s  %s\n", hex.EncodeToString(digest[:]), filepath.Base(asset))
	if err := os.WriteFile(filepath.Join(directory, "SHA256SUMS"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	return root
}

func readContractFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

package releaseasset

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPublishTargetRefusesStable(t *testing.T) {
	root := repositoryRoot(t)
	script := filepath.Join(root, "scripts", "validate-publish-target.sh")
	for _, test := range []struct {
		name        string
		packageType string
		codename    string
		suite       string
		ok          bool
		message     string
	}{
		{name: "sysext without codename", packageType: "sysext", ok: true},
		{name: "sysext trixie", packageType: "sysext", codename: "trixie", ok: true},
		{name: "deb trixie", packageType: "deb", codename: "trixie", ok: true},
		{name: "codename stable", packageType: "sysext", codename: "stable", message: "stable"},
		{name: "codename Stable", packageType: "rpm", codename: "Stable", message: "stable"},
		{name: "codename padded stable", packageType: "sysext", codename: " stable ", message: "stable"},
		{name: "suite stable", packageType: "sysext", codename: "trixie", suite: "stable", message: "stable"},
		{name: "suite STABLE without codename", packageType: "sysext", suite: "STABLE", message: "stable"},
		{name: "deb without codename", packageType: "deb", message: "codename is required"},
		{name: "deb blank codename", packageType: "deb", codename: "  ", message: "codename is required"},
		{name: "codename stable slash", packageType: "sysext", codename: "stable/", message: "not a plain name"},
		{name: "codename dot stable", packageType: "sysext", codename: "./stable", message: "not a plain name"},
		{name: "suite path", packageType: "sysext", suite: "../stable", message: "not a plain name"},
	} {
		t.Run(test.name, func(t *testing.T) {
			output, err := runPublishTarget(script, test.packageType, test.codename, test.suite, "")
			if test.ok {
				if err != nil {
					t.Fatalf("unexpected refusal: %v\n%s", err, output)
				}
				return
			}
			if err == nil {
				t.Fatalf("unexpectedly accepted:\n%s", output)
			}
			if !strings.Contains(output, "::error::") || !strings.Contains(output, test.message) {
				t.Fatalf("output %q lacks ::error:: and %q", output, test.message)
			}
		})
	}
}

func TestPublishTargetRefusesDebianPackagesForAnyType(t *testing.T) {
	script := filepath.Join(repositoryRoot(t), "scripts", "validate-publish-target.sh")
	for _, test := range []struct {
		name     string
		files    map[string]string
		symlinks map[string]string
		ok       bool
	}{
		{name: "sysext only", files: map[string]string{"a.raw": "x", "sub/b.raw.zst": "y"}, ok: true},
		{name: "deb extension", files: map[string]string{"a.raw": "x", "sub/pkg.deb": "x"}},
		{name: "deb magic without extension", files: map[string]string{"a.raw": "x", "pkg.bin": "!<arch>\ndebian-binary   "}},
		{name: "symlink named deb", files: map[string]string{"a.raw": "x", "store/real": "!<arch>\ndebian-binary   "}, symlinks: map[string]string{"pkg.deb": "store/real"}},
		{name: "symlink to deb magic", files: map[string]string{"a.raw": "x", "../outside/real": "!<arch>\ndebian-binary   "}, symlinks: map[string]string{"pkg": "../outside/real"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "packages")
			for name, content := range test.files {
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for name, target := range test.symlinks {
				if err := os.Symlink(filepath.Join(dir, target), filepath.Join(dir, name)); err != nil {
					t.Fatal(err)
				}
			}
			output, err := runPublishTarget(script, "sysext", "", "", dir)
			if test.ok {
				if err != nil {
					t.Fatalf("unexpected refusal: %v\n%s", err, output)
				}
				return
			}
			if err == nil || !strings.Contains(output, "Debian packages found") {
				t.Fatalf("mixed Debian input accepted: %v\n%s", err, output)
			}
		})
	}
}

func runPublishTarget(script, packageType, codename, suite, packagesDir string) (string, error) {
	command := exec.Command("sh", script)
	command.Env = append(os.Environ(),
		"PACKAGE_TYPE="+packageType,
		"CODENAME="+codename,
		"SUITE="+suite,
		"PACKAGES_DIR="+packagesDir,
	)
	output, err := command.CombinedOutput()
	return string(output), err
}

func TestPublishActionValidatesTargetBeforeInstall(t *testing.T) {
	root := repositoryRoot(t)
	data, err := os.ReadFile(filepath.Join(root, ".github", "actions", "publish-to-r2", "action.yml"))
	if err != nil {
		t.Fatal(err)
	}
	action := string(data)
	if regexp.MustCompile(`default:\s*'?"?stable`).MatchString(action) {
		t.Fatal("action.yml still defaults an input to stable")
	}
	validate := strings.Index(action, "- name: Validate inputs")
	install := strings.Index(action, "- name: Install repogen")
	if validate < 0 || install < 0 || validate > install {
		t.Fatalf("Validate inputs (%d) must precede Install repogen (%d)", validate, install)
	}
	step := action[validate:install]
	for _, want := range []string{
		"PACKAGE_TYPE: ${{ inputs.package-type }}",
		"CODENAME: ${{ inputs.codename }}",
		"SUITE: ${{ inputs.suite }}",
		"PACKAGES_DIR: ${{ inputs.packages-dir }}",
		"scripts/validate-publish-target.sh",
	} {
		if !strings.Contains(step, want) {
			t.Fatalf("Validate inputs step lacks %q", want)
		}
	}
	if strings.Contains(step, "${{ inputs.codename }}\"") || strings.Contains(step, "${{ inputs.suite }}\"") {
		t.Fatal("Validate inputs interpolates codename/suite into the shell; pass them through env")
	}
	if strings.Index(step, "validate-publish-target.sh") > strings.Index(step, "case \"${{ inputs.package-type }}\"") {
		t.Fatal("target validation must run before any other input check")
	}
}

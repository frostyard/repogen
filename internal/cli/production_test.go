package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostyard/repogen/internal/models"
)

func TestValidateProductionCommandAcceptsExactDebianRequestWithoutWriting(t *testing.T) {
	t.Parallel()

	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "future-output")
	copyFixture(t, filepath.Join("..", "..", "test", "fixtures", "debs", "repogen-test_1.0.0_amd64.deb"), filepath.Join(inputDir, "repogen-test_1.0.0_amd64.deb"))

	config := &models.RepositoryConfig{
		Origin: "caller supplied",
		Label:  "caller supplied",
	}
	cmd := newValidateProductionCmd(config)
	cmd.SetArgs([]string{
		"--input-dir", inputDir,
		"--output-dir", outputDir,
		"--codename", "trixie",
		"--suite", "trixie",
		"--components", "main",
		"--arch", "amd64,all",
	})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("validate-production returned an error: %v", err)
	}
	if _, err := os.Stat(outputDir); !os.IsNotExist(err) {
		t.Fatalf("validate-production created or changed its output path: %v", err)
	}
	if config.Origin != productionOrigin || config.Label != productionLabel {
		t.Fatalf("production identity = %q/%q, want %q/%q", config.Origin, config.Label, productionOrigin, productionLabel)
	}
	if strings.Join(config.Arches, ",") != "all,amd64" {
		t.Fatalf("canonical architectures = %v, want [all amd64]", config.Arches)
	}
}

func TestValidateProductionCommandRejectsInputBeforeOutputMutation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		prepare   func(t *testing.T, inputDir string)
		arguments func(inputDir, outputDir string) []string
		wantError string
	}{
		{
			name: "implicit suite",
			prepare: func(t *testing.T, inputDir string) {
				copyValidDebFixture(t, inputDir)
			},
			arguments: func(inputDir, outputDir string) []string {
				return productionArgs(inputDir, outputDir, "--suite")
			},
			wantError: "--suite must be provided explicitly",
		},
		{
			name: "stable alias",
			prepare: func(t *testing.T, inputDir string) {
				copyValidDebFixture(t, inputDir)
			},
			arguments: func(inputDir, outputDir string) []string {
				args := productionArgs(inputDir, outputDir)
				replaceArgValue(args, "--codename", "stable")
				replaceArgValue(args, "--suite", "stable")
				return args
			},
			wantError: "mutable or reserved",
		},
		{
			name: "suite mismatch",
			prepare: func(t *testing.T, inputDir string) {
				copyValidDebFixture(t, inputDir)
			},
			arguments: func(inputDir, outputDir string) []string {
				args := productionArgs(inputDir, outputDir)
				replaceArgValue(args, "--suite", "forky")
				return args
			},
			wantError: "must exactly match",
		},
		{
			name: "path traversal identifier",
			prepare: func(t *testing.T, inputDir string) {
				copyValidDebFixture(t, inputDir)
			},
			arguments: func(inputDir, outputDir string) []string {
				args := productionArgs(inputDir, outputDir)
				replaceArgValue(args, "--codename", "../trixie")
				replaceArgValue(args, "--suite", "../trixie")
				return args
			},
			wantError: "not a safe Debian identifier",
		},
		{
			name: "control character",
			prepare: func(t *testing.T, inputDir string) {
				copyValidDebFixture(t, inputDir)
			},
			arguments: func(inputDir, outputDir string) []string {
				args := productionArgs(inputDir, outputDir)
				replaceArgValue(args, "--codename", "trixie\nforged")
				replaceArgValue(args, "--suite", "trixie\nforged")
				return args
			},
			wantError: "control character",
		},
		{
			name: "non main component",
			prepare: func(t *testing.T, inputDir string) {
				copyValidDebFixture(t, inputDir)
			},
			arguments: func(inputDir, outputDir string) []string {
				args := productionArgs(inputDir, outputDir)
				replaceArgValue(args, "--components", "main,contrib")
				return args
			},
			wantError: "exactly main",
		},
		{
			name: "unsupported architecture",
			prepare: func(t *testing.T, inputDir string) {
				copyValidDebFixture(t, inputDir)
			},
			arguments: func(inputDir, outputDir string) []string {
				args := productionArgs(inputDir, outputDir)
				replaceArgValue(args, "--arch", "all,arm64")
				return args
			},
			wantError: "must include amd64",
		},
		{
			name: "overlapping input and output",
			prepare: func(t *testing.T, inputDir string) {
				copyValidDebFixture(t, inputDir)
			},
			arguments: func(inputDir, _ string) []string {
				return productionArgs(inputDir, filepath.Join(inputDir, "repo"))
			},
			wantError: "must not be the same or contain",
		},
		{
			name: "mixed recognized formats",
			prepare: func(t *testing.T, inputDir string) {
				copyValidDebFixture(t, inputDir)
				if err := os.WriteFile(filepath.Join(inputDir, "unexpected.rpm"), []byte("fixture"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			arguments: func(inputDir, outputDir string) []string {
				return productionArgs(inputDir, outputDir)
			},
			wantError: "recognized rpm artifact",
		},
		{
			name: "corrupt deb",
			prepare: func(t *testing.T, inputDir string) {
				if err := os.WriteFile(filepath.Join(inputDir, "broken.deb"), []byte("fixture"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			arguments: func(inputDir, outputDir string) []string {
				return productionArgs(inputDir, outputDir)
			},
			wantError: "production Debian input is invalid",
		},
		{
			name: "symlinked package",
			prepare: func(t *testing.T, inputDir string) {
				target := filepath.Join(t.TempDir(), "outside.deb")
				copyFixture(t, filepath.Join("..", "..", "test", "fixtures", "debs", "repogen-test_1.0.0_amd64.deb"), target)
				if err := os.Symlink(target, filepath.Join(inputDir, "linked.deb")); err != nil {
					t.Fatal(err)
				}
			},
			arguments: func(inputDir, outputDir string) []string {
				return productionArgs(inputDir, outputDir)
			},
			wantError: "is a symlink",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			inputDir := t.TempDir()
			outputDir := t.TempDir()
			sentinel := filepath.Join(outputDir, "sentinel")
			if err := os.WriteFile(sentinel, []byte("unchanged"), 0o644); err != nil {
				t.Fatal(err)
			}
			tt.prepare(t, inputDir)

			cmd := NewValidateProductionCmd()
			cmd.SetArgs(tt.arguments(inputDir, outputDir))
			err := cmd.ExecuteContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantError)
			}

			data, readErr := os.ReadFile(sentinel)
			if readErr != nil {
				t.Fatalf("sentinel was removed: %v", readErr)
			}
			if string(data) != "unchanged" {
				t.Fatalf("sentinel changed to %q", data)
			}
			entries, readErr := os.ReadDir(outputDir)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(entries) != 1 || entries[0].Name() != "sentinel" {
				t.Fatalf("output directory changed: %v", entries)
			}
		})
	}
}

func TestValidateProductionDebianPackage(t *testing.T) {
	t.Parallel()

	base := models.Package{
		Name:         "repogen-test",
		Version:      "1:1.0.0+fy13u1",
		Architecture: "amd64",
		Filename:     "repogen-test_1.0.0_amd64.deb",
		Maintainer:   "Frostyard <maintainer@example.invalid>",
		Description:  "A fixture\nwith details",
		Dependencies: []string{"libc6"},
		Metadata:     map[string]interface{}{"Section": "utils"},
	}
	config := &models.RepositoryConfig{Arches: []string{"all", "amd64"}}

	tests := []struct {
		name      string
		mutate    func(*models.Package)
		wantError string
	}{
		{
			name: "invalid name",
			mutate: func(pkg *models.Package) {
				pkg.Name = "../escape"
			},
			wantError: "not a valid Debian package name",
		},
		{
			name: "invalid version",
			mutate: func(pkg *models.Package) {
				pkg.Version = "1.0\nSHA256: forged"
			},
			wantError: "invalid Debian version",
		},
		{
			name: "architecture outside allowlist",
			mutate: func(pkg *models.Package) {
				pkg.Architecture = "arm64"
			},
			wantError: "outside the requested production allowlist",
		},
		{
			name: "metadata injection",
			mutate: func(pkg *models.Package) {
				pkg.Metadata = map[string]interface{}{"Section": "utils\nFilename: forged"}
			},
			wantError: "contains a control character",
		},
		{
			name: "invalid metadata key",
			mutate: func(pkg *models.Package) {
				pkg.Metadata = map[string]interface{}{"Bad:Field": "value"}
			},
			wantError: "field name",
		},
	}

	if err := validateProductionDebianPackage(config, &base); err != nil {
		t.Fatalf("valid package rejected: %v", err)
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			pkg := base
			pkg.Dependencies = append([]string(nil), base.Dependencies...)
			pkg.Metadata = map[string]interface{}{"Section": "utils"}
			tt.mutate(&pkg)
			err := validateProductionDebianPackage(config, &pkg)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantError)
			}
		})
	}
}

func TestGenericGenerateCommandRemainsSeparate(t *testing.T) {
	t.Parallel()

	cmd := NewGenerateCmd()
	for flag, want := range map[string]string{
		"codename":   "stable",
		"components": "[main]",
		"arch":       "[amd64]",
	} {
		got := cmd.Flag(flag).DefValue
		if got != want {
			t.Errorf("--%s default = %q, want %q", flag, got, want)
		}
	}
	if cmd.Flag("production") != nil {
		t.Fatal("generic generate command unexpectedly gained a production mode")
	}
}

func productionArgs(inputDir, outputDir string, omit ...string) []string {
	omitted := make(map[string]struct{}, len(omit))
	for _, flag := range omit {
		omitted[flag] = struct{}{}
	}

	values := []string{
		"--input-dir", inputDir,
		"--output-dir", outputDir,
		"--codename", "trixie",
		"--suite", "trixie",
		"--components", "main",
		"--arch", "all,amd64",
	}
	var args []string
	for i := 0; i < len(values); i += 2 {
		if _, skip := omitted[values[i]]; skip {
			continue
		}
		args = append(args, values[i], values[i+1])
	}
	return args
}

func replaceArgValue(args []string, flag, value string) {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			args[i+1] = value
			return
		}
	}
}

func copyValidDebFixture(t *testing.T, inputDir string) {
	t.Helper()
	copyFixture(
		t,
		filepath.Join("..", "..", "test", "fixtures", "debs", "repogen-test_1.0.0_amd64.deb"),
		filepath.Join(inputDir, "repogen-test_1.0.0_amd64.deb"),
	)
}

func copyFixture(t *testing.T, source, destination string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProductionErrorsRemainTyped(t *testing.T) {
	t.Parallel()

	err := invalidProductionConfig("bad input")
	var repoErr *models.RepoGenError
	if !errors.As(err, &repoErr) {
		t.Fatalf("error is not RepoGenError: %T", err)
	}
	if repoErr.Type != models.ErrInvalidConfig {
		t.Fatalf("error type = %s, want %s", repoErr.Type, models.ErrInvalidConfig)
	}
}

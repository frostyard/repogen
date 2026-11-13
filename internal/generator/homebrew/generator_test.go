package homebrew

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostyard/repogen/internal/models"
)

func TestParseBottleFilename(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     bottleName
		wantErr  bool
	}{
		{
			name:     "plain gzip bottle",
			filename: "repogen-utils--2.0.0.x86_64_linux.bottle.tar.gz",
			want:     bottleName{Name: "repogen-utils", Version: "2.0.0", Platform: "x86_64_linux"},
		},
		{
			name:     "version with dots",
			filename: "wget--1.21.4.arm64_sonoma.bottle.tar.gz",
			want:     bottleName{Name: "wget", Version: "1.21.4", Platform: "arm64_sonoma"},
		},
		{
			name:     "plain tar bottle",
			filename: "foo--1.0.0.sonoma.bottle.tar",
			want:     bottleName{Name: "foo", Version: "1.0.0", Platform: "sonoma"},
		},
		{
			name:     "name with a directory path",
			filename: "bottles/x--1.0.bottle.tar.gz",
			want:     bottleName{Name: "x", Version: "1", Platform: "0"},
		},
		{
			name:     "full https url",
			filename: "https://h/bottles/x--1.0.0.arm64_sonoma.bottle.tar.gz",
			want:     bottleName{Name: "x", Version: "1.0.0", Platform: "arm64_sonoma"},
		},
		{
			name:     "no double dash",
			filename: "foo-1.0.x86_64_linux.bottle.tar.gz",
			wantErr:  true,
		},
		{
			name:     "no platform separator",
			filename: "foo--1.bottle.tar.gz",
			wantErr:  true,
		},
		{
			name:     "empty name",
			filename: "--1.0.x86_64_linux.bottle.tar.gz",
			wantErr:  true,
		},
		{
			name:     "empty version",
			filename: "foo--.x86_64_linux.bottle.tar.gz",
			wantErr:  true,
		},
		{
			name:     "wrong suffix",
			filename: "foo--1.0.x86_64_linux.tar.gz",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseBottleFilename(tt.filename)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseBottleFilename(%q) = %+v, want an error", tt.filename, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseBottleFilename(%q) returned an unexpected error: %v", tt.filename, err)
			}
			if got != tt.want {
				t.Errorf("parseBottleFilename(%q) = %+v, want %+v", tt.filename, got, tt.want)
			}
		})
	}
}

// TestGenerateUsesBottleFilenameVersion guards the formula version: the bottle
// filename, not the package metadata, holds it. The CLI builds Homebrew
// packages with no Name and no Version, so the generator must read them from
// the filename.
func TestGenerateUsesBottleFilenameVersion(t *testing.T) {
	srcDir := t.TempDir()
	outDir := t.TempDir()

	bottlePath := filepath.Join(srcDir, "repogen-utils--2.0.0.x86_64_linux.bottle.tar.gz")
	content := []byte("not a real bottle, only its name matters")
	if err := os.WriteFile(bottlePath, content, 0o600); err != nil {
		t.Fatalf("failed to write test bottle: %v", err)
	}

	packages := []models.Package{{
		Filename:  bottlePath,
		Size:      int64(len(content)),
		SHA256Sum: "0000000000000000000000000000000000000000000000000000000000000000",
	}}

	g := NewGenerator("https://example.invalid")
	config := &models.RepositoryConfig{OutputDir: outDir}

	if err := g.ValidatePackages(packages); err != nil {
		t.Fatalf("ValidatePackages returned an unexpected error: %v", err)
	}
	if err := g.Generate(context.Background(), config, packages); err != nil {
		t.Fatalf("Generate returned an unexpected error: %v", err)
	}

	formulaPath := filepath.Join(outDir, "Formula", "repogen-utils.rb")
	data, err := os.ReadFile(formulaPath)
	if err != nil {
		t.Fatalf("failed to read formula: %v", err)
	}

	formula := string(data)
	if !strings.Contains(formula, `version "2.0.0"`) {
		t.Errorf("formula does not hold the bottle version %q:\n%s", "2.0.0", formula)
	}
	if strings.Contains(formula, `version "1.0.0"`) {
		t.Errorf("formula falls back to the default version:\n%s", formula)
	}
}

func TestValidatePackagesRejectsBadFilenames(t *testing.T) {
	g := NewGenerator("https://example.invalid")

	tests := []struct {
		name     string
		filename string
	}{
		{name: "not a bottle at all", filename: "foo.tar.gz"},
		{name: "no version or platform", filename: "foo.bottle.tar.gz"},
		{name: "no double dash", filename: "foo-1.0.0.x86_64_linux.bottle.tar.gz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := g.ValidatePackages([]models.Package{{Filename: tt.filename}})
			if err == nil {
				t.Fatalf("ValidatePackages(%q) returned no error", tt.filename)
			}
		})
	}
}

func TestValidatePackagesRejectsMixedVersions(t *testing.T) {
	g := NewGenerator("https://example.invalid")

	packages := []models.Package{
		{Filename: "foo--1.0.0.x86_64_linux.bottle.tar.gz"},
		{Filename: "foo--2.0.0.arm64_sonoma.bottle.tar.gz"},
	}

	err := g.ValidatePackages(packages)
	if err == nil {
		t.Fatal("ValidatePackages returned no error for mixed versions")
	}
	for _, want := range []string{"foo", "different versions", "1.0.0", "2.0.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
}

// TestGenerateRejectsBadFilenames checks that a filename that does not parse
// fails the run and writes no formula.
func TestGenerateRejectsBadFilenames(t *testing.T) {
	srcDir := t.TempDir()
	outDir := t.TempDir()

	bottlePath := filepath.Join(srcDir, "foo.bottle.tar.gz")
	if err := os.WriteFile(bottlePath, []byte("data"), 0o600); err != nil {
		t.Fatalf("failed to write test bottle: %v", err)
	}

	packages := []models.Package{{Filename: bottlePath, Size: 4}}

	g := NewGenerator("https://example.invalid")
	config := &models.RepositoryConfig{OutputDir: outDir}

	if err := g.Generate(context.Background(), config, packages); err == nil {
		t.Fatal("Generate returned no error for an unparsable filename")
	}

	formulas, err := filepath.Glob(filepath.Join(outDir, "Formula", "*.rb"))
	if err != nil {
		t.Fatalf("failed to list formula files: %v", err)
	}
	if len(formulas) != 0 {
		t.Errorf("Generate wrote formulas for an unparsable filename: %v", formulas)
	}
}

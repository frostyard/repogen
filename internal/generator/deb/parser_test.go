package deb

import (
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostyard/repogen/internal/generator"
	"github.com/frostyard/repogen/internal/models"
)

const validAllStanza = "Package: tool-all\nVersion: 1.0\nArchitecture: all\nFilename: pool/main/t/tool-all/tool-all_1.0_all.deb\nSize: 10\nSHA256: aaaa\nDescription: tool\n multi-line continuation\n\n"
const validAmd64Stanza = "Package: tool\nVersion: 2.0\nArchitecture: amd64\nFilename: pool/main/t/tool/tool_2.0_amd64.deb\nSize: 20\nSHA256: bbbb\n\n"

func existingConfig(outputDir string) *models.RepositoryConfig {
	return &models.RepositoryConfig{
		OutputDir:  outputDir,
		Codename:   "testing",
		Components: []string{"main"},
		Arches:     []string{"all", "amd64"},
	}
}

func writeIndex(t *testing.T, outputDir, arch, name string, content []byte) string {
	t.Helper()
	dir := filepath.Join(outputDir, "dists", "testing", "main", "binary-"+arch)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func gzipBytes(t *testing.T, content string) []byte {
	t.Helper()
	var b strings.Builder
	w := gzip.NewWriter(&b)
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return []byte(b.String())
}

func TestParseExistingMetadataAbsentIsSentinel(t *testing.T) {
	_, err := NewGenerator(nil).ParseExistingMetadata(existingConfig(filepath.Join(t.TempDir(), "missing")))
	if !errors.Is(err, generator.ErrNoExistingMetadata) {
		t.Fatalf("error = %v, want ErrNoExistingMetadata", err)
	}
}

func TestParseExistingMetadataReadsAllArches(t *testing.T) {
	out := t.TempDir()
	writeIndex(t, out, "all", "Packages", []byte(validAllStanza))
	writeIndex(t, out, "amd64", "Packages", []byte(validAmd64Stanza))
	pkgs, err := NewGenerator(nil).ParseExistingMetadata(existingConfig(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 2 {
		t.Fatalf("got %d packages, want 2", len(pkgs))
	}
}

func TestParseExistingMetadataCorruptArchFails(t *testing.T) {
	out := t.TempDir()
	writeIndex(t, out, "all", "Packages", []byte(validAllStanza))
	writeIndex(t, out, "amd64", "Packages", []byte("garbage without colon\n\n"))
	_, err := NewGenerator(nil).ParseExistingMetadata(existingConfig(out))
	if err == nil || errors.Is(err, generator.ErrNoExistingMetadata) || !strings.Contains(err.Error(), "binary-amd64") {
		t.Fatalf("error = %v, want non-sentinel error naming binary-amd64", err)
	}
}

func TestParseExistingMetadataGzipOnly(t *testing.T) {
	out := t.TempDir()
	writeIndex(t, out, "amd64", "Packages.gz", gzipBytes(t, validAmd64Stanza))
	pkgs, err := NewGenerator(nil).ParseExistingMetadata(existingConfig(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 || pkgs[0].Name != "tool" {
		t.Fatalf("got %+v, want tool", pkgs)
	}
}

func TestParseExistingMetadataCorruptGzipFails(t *testing.T) {
	out := t.TempDir()
	writeIndex(t, out, "amd64", "Packages.gz", []byte("not gzip"))
	_, err := NewGenerator(nil).ParseExistingMetadata(existingConfig(out))
	if err == nil || errors.Is(err, generator.ErrNoExistingMetadata) {
		t.Fatalf("error = %v, want non-sentinel gzip error", err)
	}
}

func TestParseExistingMetadataRequiresIdentityFields(t *testing.T) {
	for _, field := range []string{"Package", "Version", "Architecture", "Filename", "SHA256"} {
		t.Run(field, func(t *testing.T) {
			out := t.TempDir()
			var lines []string
			for _, line := range strings.Split(validAmd64Stanza, "\n") {
				if !strings.HasPrefix(line, field+": ") {
					lines = append(lines, line)
				}
			}
			writeIndex(t, out, "amd64", "Packages", []byte(strings.Join(lines, "\n")))
			_, err := NewGenerator(nil).ParseExistingMetadata(existingConfig(out))
			if err == nil || !strings.Contains(err.Error(), field) {
				t.Fatalf("error = %v, want missing %s", err, field)
			}
		})
	}
}

func TestParseExistingMetadataRejectsUnselectedExistingIndex(t *testing.T) {
	out := t.TempDir()
	writeIndex(t, out, "all", "Packages", []byte(validAllStanza))
	cfg := existingConfig(out)
	cfg.Arches = []string{"amd64"}
	_, err := NewGenerator(nil).ParseExistingMetadata(cfg)
	if err == nil || errors.Is(err, generator.ErrNoExistingMetadata) || !strings.Contains(err.Error(), "binary-all") {
		t.Fatalf("error = %v, want refusal naming unselected binary-all", err)
	}
}

func TestParseExistingMetadataReleaseWithoutIndexFails(t *testing.T) {
	out := t.TempDir()
	dir := filepath.Join(out, "dists", "testing")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Release"), []byte("Suite: testing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := NewGenerator(nil).ParseExistingMetadata(existingConfig(out))
	if err == nil || errors.Is(err, generator.ErrNoExistingMetadata) {
		t.Fatalf("error = %v, want non-sentinel error for Release without indexes", err)
	}
}

func TestParseExistingMetadataCorruptGzipBesidePlainFails(t *testing.T) {
	out := t.TempDir()
	writeIndex(t, out, "amd64", "Packages", []byte(validAmd64Stanza))
	writeIndex(t, out, "amd64", "Packages.gz", []byte("truncated"))
	cfg := existingConfig(out)
	cfg.Arches = []string{"amd64"}
	_, err := NewGenerator(nil).ParseExistingMetadata(cfg)
	if err == nil || !strings.Contains(err.Error(), "Packages.gz") {
		t.Fatalf("error = %v, want failure naming Packages.gz", err)
	}
}

func TestParseExistingMetadataDivergentRepresentationsFail(t *testing.T) {
	out := t.TempDir()
	writeIndex(t, out, "amd64", "Packages", []byte(validAmd64Stanza))
	writeIndex(t, out, "amd64", "Packages.gz", gzipBytes(t, strings.Replace(validAmd64Stanza, "Version: 2.0", "Version: 3.0", 1)))
	cfg := existingConfig(out)
	cfg.Arches = []string{"amd64"}
	_, err := NewGenerator(nil).ParseExistingMetadata(cfg)
	if err == nil || !strings.Contains(err.Error(), "differ") {
		t.Fatalf("error = %v, want divergence failure", err)
	}
}

func TestParseExistingMetadataMatchingRepresentations(t *testing.T) {
	out := t.TempDir()
	writeIndex(t, out, "amd64", "Packages", []byte(validAmd64Stanza))
	writeIndex(t, out, "amd64", "Packages.gz", gzipBytes(t, validAmd64Stanza))
	cfg := existingConfig(out)
	cfg.Arches = []string{"amd64"}
	pkgs, err := NewGenerator(nil).ParseExistingMetadata(cfg)
	if err != nil || len(pkgs) != 1 {
		t.Fatalf("got %d packages, err %v; want 1, nil", len(pkgs), err)
	}
}

func TestParseExistingMetadataInvalidSizeFails(t *testing.T) {
	out := t.TempDir()
	writeIndex(t, out, "amd64", "Packages", []byte(strings.Replace(validAmd64Stanza, "Size: 20", "Size: garbage", 1)))
	cfg := existingConfig(out)
	cfg.Arches = []string{"amd64"}
	_, err := NewGenerator(nil).ParseExistingMetadata(cfg)
	if err == nil || !strings.Contains(err.Error(), "invalid Size") {
		t.Fatalf("error = %v, want invalid Size", err)
	}
}

func TestParseExistingMetadataDifferingSizeAcrossRepresentationsFails(t *testing.T) {
	out := t.TempDir()
	writeIndex(t, out, "amd64", "Packages", []byte(validAmd64Stanza))
	writeIndex(t, out, "amd64", "Packages.gz", gzipBytes(t, strings.Replace(validAmd64Stanza, "Size: 20", "Size: 0", 1)))
	cfg := existingConfig(out)
	cfg.Arches = []string{"amd64"}
	_, err := NewGenerator(nil).ParseExistingMetadata(cfg)
	if err == nil || !strings.Contains(err.Error(), "differ") {
		t.Fatalf("error = %v, want representations differ", err)
	}
}

func TestParseExistingMetadataDanglingGzipSymlinkFails(t *testing.T) {
	out := t.TempDir()
	plain := writeIndex(t, out, "amd64", "Packages", []byte(validAmd64Stanza))
	if err := os.Symlink(filepath.Join(filepath.Dir(plain), "missing-target"), plain+".gz"); err != nil {
		t.Fatal(err)
	}
	cfg := existingConfig(out)
	cfg.Arches = []string{"amd64"}
	_, err := NewGenerator(nil).ParseExistingMetadata(cfg)
	if err == nil || !strings.Contains(err.Error(), "Packages.gz") {
		t.Fatalf("error = %v, want failure naming dangling Packages.gz", err)
	}
}

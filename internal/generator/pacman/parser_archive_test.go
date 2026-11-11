package pacman

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/frostyard/repogen/internal/models"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// tarFile is one regular file written into a test tar archive.
type tarFile struct {
	name string
	data string
}

// buildTar writes the files into an uncompressed tar archive.
func buildTar(t *testing.T, files []tarFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range files {
		hdr := &tar.Header{Name: f.name, Mode: 0o644, Size: int64(len(f.data)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %q: %v", f.name, err)
		}
		if _, err := tw.Write([]byte(f.data)); err != nil {
			t.Fatalf("write %q: %v", f.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	return buf.Bytes()
}

// encodeTar compresses raw as "gz", "xz" or "zst"; "" leaves it uncompressed.
func encodeTar(t *testing.T, raw []byte, format string) []byte {
	t.Helper()
	var buf bytes.Buffer
	switch format {
	case "":
		return raw
	case "gz":
		w := gzip.NewWriter(&buf)
		if _, err := w.Write(raw); err != nil {
			t.Fatalf("gzip write: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("gzip close: %v", err)
		}
	case "xz":
		w, err := xz.NewWriter(&buf)
		if err != nil {
			t.Fatalf("xz writer: %v", err)
		}
		if _, err := w.Write(raw); err != nil {
			t.Fatalf("xz write: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("xz close: %v", err)
		}
	case "zst":
		w, err := zstd.NewWriter(&buf)
		if err != nil {
			t.Fatalf("zstd writer: %v", err)
		}
		if _, err := w.Write(raw); err != nil {
			t.Fatalf("zstd write: %v", err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("zstd close: %v", err)
		}
	default:
		t.Fatalf("unknown format %q", format)
	}
	return buf.Bytes()
}

// writePacmanFile writes data under dir with the given file name.
func writePacmanFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create %s: %v", dir, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// descFile builds a pacman desc file in the %FIELD% format.
func descFile(name, version, arch string) string {
	return fmt.Sprintf("%%NAME%%\n%s\n\n%%VERSION%%\n%s\n\n%%ARCH%%\n%s\n\n%%CSIZE%%\n1234\n\n", name, version, arch)
}

func TestExtractPKGINFOFormats(t *testing.T) {
	const pkginfo = "pkgname = tool\npkgver = 1.0-1\n"

	cases := []struct {
		name   string
		suffix string
		format string
	}{
		{name: "zstd", suffix: ".pkg.tar.zst", format: "zst"},
		{name: "xz", suffix: ".pkg.tar.xz", format: "xz"},
		{name: "gzip", suffix: ".pkg.tar.gz", format: "gz"},
		{name: "uncompressed", suffix: ".pkg.tar", format: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := encodeTar(t, buildTar(t, []tarFile{
				{name: "usr/bin/tool", data: "binary"},
				{name: ".PKGINFO", data: pkginfo},
			}), tc.format)

			path := writePacmanFile(t, t.TempDir(), "tool-1.0-1-x86_64"+tc.suffix, data)
			got, err := extractPKGINFO(path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != pkginfo {
				t.Errorf("content = %q, want %q", got, pkginfo)
			}
		})
	}
}

func TestExtractPKGINFOUnsupportedFormat(t *testing.T) {
	path := writePacmanFile(t, t.TempDir(), "tool-1.0-1-x86_64.pkg.tar.bz2", []byte("not a tar"))

	_, err := extractPKGINFO(path)
	want := "unsupported package format: tool-1.0-1-x86_64.pkg.tar.bz2"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestExtractPKGINFOAbsent(t *testing.T) {
	data := encodeTar(t, buildTar(t, []tarFile{{name: "usr/bin/tool", data: "binary"}}), "")

	_, err := extractPKGINFO(writePacmanFile(t, t.TempDir(), "tool-1.0-1-x86_64.pkg.tar", data))
	if err == nil || err.Error() != ".PKGINFO not found in package" {
		t.Fatalf("error = %v, want %q", err, ".PKGINFO not found in package")
	}
}

// writeDB writes one pacman database file holding a single package entry.
func writeDB(t *testing.T, archDir, fileName, format, pkgName string) {
	t.Helper()
	raw := buildTar(t, []tarFile{{
		name: pkgName + "-1.0-1/desc",
		data: descFile(pkgName, "1.0-1", "x86_64"),
	}})
	writePacmanFile(t, archDir, fileName, encodeTar(t, raw, format))
}

func parseExisting(t *testing.T, outputDir string, arches []string) ([]models.Package, error) {
	t.Helper()
	g := Generator{}
	return g.ParseExistingMetadata(&models.RepositoryConfig{OutputDir: outputDir, Arches: arches})
}

func TestParseExistingMetadataPrefersZstDatabase(t *testing.T) {
	outputDir := t.TempDir()
	archDir := filepath.Join(outputDir, "x86_64")
	writeDB(t, archDir, "custom.db.tar.zst", "zst", "zst-pkg")
	writeDB(t, archDir, "custom.db.tar.xz", "xz", "xz-pkg")

	packages, err := parseExisting(t, outputDir, []string{"x86_64"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(packages) != 1 || packages[0].Name != "zst-pkg" {
		t.Fatalf("packages = %+v, want the single package zst-pkg", packages)
	}
}

func TestParseExistingMetadataReadsGzipDatabase(t *testing.T) {
	outputDir := t.TempDir()
	writeDB(t, filepath.Join(outputDir, "x86_64"), "custom.db.tar.gz", "gz", "gz-pkg")

	packages, err := parseExisting(t, outputDir, []string{"x86_64"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(packages) != 1 || packages[0].Name != "gz-pkg" {
		t.Fatalf("packages = %+v, want the single package gz-pkg", packages)
	}
}

func TestParseExistingMetadataReadsUncompressedDatabase(t *testing.T) {
	outputDir := t.TempDir()
	writeDB(t, filepath.Join(outputDir, "x86_64"), "custom.db", "", "plain-pkg")

	packages, err := parseExisting(t, outputDir, []string{"x86_64"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(packages) != 1 || packages[0].Name != "plain-pkg" {
		t.Fatalf("packages = %+v, want the single package plain-pkg", packages)
	}
}

func TestParseExistingMetadataSkipsArchWithoutDatabase(t *testing.T) {
	outputDir := t.TempDir()
	writeDB(t, filepath.Join(outputDir, "x86_64"), "custom.db.tar.zst", "zst", "zst-pkg")
	if err := os.MkdirAll(filepath.Join(outputDir, "aarch64"), 0o755); err != nil {
		t.Fatalf("create arch dir: %v", err)
	}

	packages, err := parseExisting(t, outputDir, []string{"x86_64", "aarch64"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(packages) != 1 || packages[0].Name != "zst-pkg" {
		t.Fatalf("packages = %+v, want the single package zst-pkg", packages)
	}
}

func TestParseExistingMetadataNothingFound(t *testing.T) {
	outputDir := t.TempDir()

	_, err := parseExisting(t, outputDir, []string{"x86_64"})
	want := "no existing Pacman metadata found in " + outputDir
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

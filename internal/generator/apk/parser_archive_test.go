package apk

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// apkMember is one regular file written into a test APK tar archive.
type apkMember struct {
	name string
	data string
}

// buildAPKTar writes the members into an uncompressed tar archive.
func buildAPKTar(t *testing.T, members []apkMember) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, m := range members {
		hdr := &tar.Header{Name: m.name, Mode: 0o644, Size: int64(len(m.data)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %q: %v", m.name, err)
		}
		if _, err := tw.Write([]byte(m.data)); err != nil {
			t.Fatalf("write %q: %v", m.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	return buf.Bytes()
}

func gzipAPKTar(t *testing.T, raw []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(raw); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func writeAPKArchive(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestExtractPKGINFOFromAPK(t *testing.T) {
	const pkginfo = "pkgname = tool\npkgver = 1.0-r0\n"

	data := gzipAPKTar(t, buildAPKTar(t, []apkMember{
		{name: "usr/bin/tool", data: "binary"},
		{name: ".PKGINFO", data: pkginfo},
	}))

	got, err := extractPKGINFO(writeAPKArchive(t, t.TempDir(), "tool-1.0-r0.apk", data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != pkginfo {
		t.Errorf("content = %q, want %q", got, pkginfo)
	}
}

func TestExtractPKGINFOAbsent(t *testing.T) {
	data := gzipAPKTar(t, buildAPKTar(t, []apkMember{{name: "usr/bin/tool", data: "binary"}}))

	_, err := extractPKGINFO(writeAPKArchive(t, t.TempDir(), "tool-1.0-r0.apk", data))
	if err == nil || err.Error() != ".PKGINFO not found in APK" {
		t.Fatalf("error = %v, want %q", err, ".PKGINFO not found in APK")
	}
}

func TestParseAPKINDEXFromArchive(t *testing.T) {
	const index = "C:Q1abc\nP:tool\nV:1.0-r0\nA:x86_64\n\n"

	data := gzipAPKTar(t, buildAPKTar(t, []apkMember{
		{name: "DESCRIPTION", data: "index"},
		{name: "APKINDEX", data: index},
	}))

	packages, err := parseAPKINDEX(writeAPKArchive(t, t.TempDir(), "APKINDEX.tar.gz", data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(packages) != 1 {
		t.Fatalf("package count = %d, want 1", len(packages))
	}
	if packages[0].Name != "tool" || packages[0].Version != "1.0-r0" {
		t.Errorf("package = %+v, want tool 1.0-r0", packages[0])
	}
	if packages[0].Filename != "tool-1.0-r0.apk" {
		t.Errorf("filename = %q, want %q", packages[0].Filename, "tool-1.0-r0.apk")
	}
}

func TestParseAPKINDEXAbsent(t *testing.T) {
	data := gzipAPKTar(t, buildAPKTar(t, []apkMember{{name: "DESCRIPTION", data: "index"}}))

	_, err := parseAPKINDEX(writeAPKArchive(t, t.TempDir(), "APKINDEX.tar.gz", data))
	if err == nil || err.Error() != "APKINDEX not found in tar" {
		t.Fatalf("error = %v, want %q", err, "APKINDEX not found in tar")
	}
}

func TestParseAPKINDEXEmptyEntry(t *testing.T) {
	data := gzipAPKTar(t, buildAPKTar(t, []apkMember{{name: "APKINDEX", data: ""}}))

	_, err := parseAPKINDEX(writeAPKArchive(t, t.TempDir(), "APKINDEX.tar.gz", data))
	if err == nil || err.Error() != "APKINDEX not found in tar" {
		t.Fatalf("error = %v, want %q", err, "APKINDEX not found in tar")
	}
}

func TestParseAPKINDEXMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "x86_64", "APKINDEX.tar.gz")

	_, err := parseAPKINDEX(missing)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want a not-exist error", err)
	}
}

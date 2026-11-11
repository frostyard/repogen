package utils

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// tarEntryFile is one regular file written into a test tar archive.
type tarEntryFile struct {
	name string
	data string
}

func buildUtilsTar(t *testing.T, files []tarEntryFile) []byte {
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

// failingReader reports a read error, to exercise the entry-content read path.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("boom")
}

func TestReadTarEntry(t *testing.T) {
	cases := []struct {
		name    string
		files   []tarEntryFile
		lookup  []string
		want    string
		found   bool
		wantErr string
	}{
		{
			name:   "single name matches",
			files:  []tarEntryFile{{name: ".PKGINFO", data: "pkgname = tool\n"}},
			lookup: []string{".PKGINFO"},
			want:   "pkgname = tool\n",
			found:  true,
		},
		{
			name:   "first of two names matches",
			files:  []tarEntryFile{{name: "usr/bin/tool", data: "x"}, {name: "./control", data: "first"}},
			lookup: []string{"./control", "control"},
			want:   "first",
			found:  true,
		},
		{
			name:   "second of two names matches",
			files:  []tarEntryFile{{name: "usr/bin/tool", data: "x"}, {name: "control", data: "second"}},
			lookup: []string{"./control", "control"},
			want:   "second",
			found:  true,
		},
		{
			name:   "first matching entry wins",
			files:  []tarEntryFile{{name: "./control", data: "first"}, {name: "control", data: "second"}},
			lookup: []string{"./control", "control"},
			want:   "first",
			found:  true,
		},
		{
			name:   "empty entry content",
			files:  []tarEntryFile{{name: "APKINDEX", data: ""}},
			lookup: []string{"APKINDEX"},
			want:   "",
			found:  true,
		},
		{
			name:   "no match",
			files:  []tarEntryFile{{name: "usr/bin/tool", data: "x"}},
			lookup: []string{".PKGINFO"},
			found:  false,
		},
		{
			name:   "empty archive",
			lookup: []string{".PKGINFO"},
			found:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := tar.NewReader(bytes.NewReader(buildUtilsTar(t, tc.files)))
			got, found, err := ReadTarEntry(tr, tc.lookup...)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if found != tc.found {
				t.Fatalf("found = %v, want %v", found, tc.found)
			}
			if string(got) != tc.want {
				t.Errorf("data = %q, want %q", got, tc.want)
			}
			if !found && got != nil {
				t.Errorf("data = %q, want nil when not found", got)
			}
		})
	}
}

func TestReadTarEntryReadError(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{Name: ".PKGINFO", Mode: 0o644, Size: 4, Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := tw.Write([]byte("data")); err != nil {
		t.Fatalf("write data: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}

	// Cut the archive right after the 512-byte header block so the content
	// read fails.
	raw := buf.Bytes()
	tr := tar.NewReader(io.MultiReader(bytes.NewReader(raw[:512]), failingReader{}))

	_, found, err := ReadTarEntry(tr, ".PKGINFO")
	if found {
		t.Fatalf("found = true, want false on a read error")
	}
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v, want it to contain %q", err, "boom")
	}
}

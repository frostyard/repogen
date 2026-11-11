package deb

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// tarMember is one regular file written into a test control tar archive.
type tarMember struct {
	name string
	data string
}

// buildControlTar writes the members into an uncompressed tar archive.
func buildControlTar(t *testing.T, members []tarMember) []byte {
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

// compressControlTar compresses raw as "gz", "xz" or "zst"; "" leaves it uncompressed.
func compressControlTar(t *testing.T, raw []byte, format string) []byte {
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

func TestExtractControlFromTar(t *testing.T) {
	const controlData = "Package: tool\nVersion: 1.0\n"

	cases := []struct {
		name     string
		filename string
		format   string
		members  []tarMember
		want     string
		wantErr  string
	}{
		{
			name:     "uncompressed dot slash control",
			filename: "control.tar",
			members:  []tarMember{{name: "./control", data: controlData}},
			want:     controlData,
		},
		{
			name:     "uncompressed bare control",
			filename: "control.tar",
			members:  []tarMember{{name: "other.txt", data: "x"}, {name: "control", data: controlData}},
			want:     controlData,
		},
		{
			name:     "gzip",
			filename: "control.tar.gz",
			format:   "gz",
			members:  []tarMember{{name: "./control", data: controlData}},
			want:     controlData,
		},
		{
			name:     "xz",
			filename: "control.tar.xz",
			format:   "xz",
			members:  []tarMember{{name: "./control", data: controlData}},
			want:     controlData,
		},
		{
			name:     "zstd",
			filename: "control.tar.zst",
			format:   "zst",
			members:  []tarMember{{name: "./control", data: controlData}},
			want:     controlData,
		},
		{
			name:     "first match wins",
			filename: "control.tar",
			members:  []tarMember{{name: "./control", data: "first"}, {name: "control", data: "second"}},
			want:     "first",
		},
		{
			name:     "empty control entry returns empty data",
			filename: "control.tar",
			members:  []tarMember{{name: "./control", data: ""}},
			want:     "",
		},
		{
			name:     "control absent",
			filename: "control.tar",
			members:  []tarMember{{name: "usr/share/doc/copyright", data: "x"}},
			wantErr:  "control file not found in control.tar",
		},
		{
			name:     "empty archive",
			filename: "control.tar",
			wantErr:  "control file not found in control.tar",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := compressControlTar(t, buildControlTar(t, tc.members), tc.format)
			got, err := extractControlFromTar(data, tc.filename)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("content = %q, want %q", got, tc.want)
			}
		})
	}
}

// arMemberBytes builds one ar member: a 60-byte header, the data, and the
// 2-byte alignment pad when the size is odd.
func arMemberBytes(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := fmt.Fprintf(&buf, "%-16s%-12d%-6d%-6d%-8o%-10d`\n", name, 0, 0, 0, 0o100644, len(data)); err != nil {
		t.Fatalf("write ar header %q: %v", name, err)
	}
	buf.Write(data)
	if len(data)%2 != 0 {
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

func arArchive(members ...[]byte) []byte {
	var buf bytes.Buffer
	buf.WriteString("!<arch>\n")
	for _, m := range members {
		buf.Write(m)
	}
	return buf.Bytes()
}

func writeArFile(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tool_1.0_amd64.deb")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write package: %v", err)
	}
	return path
}

func TestExtractControlFromPackage(t *testing.T) {
	const controlData = "Package: tool\nVersion: 1.0\n"

	controlTarGz := compressControlTar(t, buildControlTar(t, []tarMember{{name: "./control", data: controlData}}), "gz")
	// The debian-binary member has an odd size, so it carries a 1-byte pad.
	pkg := arArchive(
		arMemberBytes(t, "debian-binary", []byte("2.0")),
		arMemberBytes(t, "control.tar.gz", controlTarGz),
	)

	got, err := extractControl(writeArFile(t, pkg))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != controlData {
		t.Errorf("content = %q, want %q", got, controlData)
	}
}

func TestExtractControlWithoutControlTar(t *testing.T) {
	pkg := arArchive(arMemberBytes(t, "debian-binary", []byte("2.0\n")))

	_, err := extractControl(writeArFile(t, pkg))
	if err == nil || err.Error() != "control.tar not found in package" {
		t.Fatalf("error = %v, want %q", err, "control.tar not found in package")
	}
}

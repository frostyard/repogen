package dirindex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerate_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	if err := Generate(dir); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	html := readFile(t, filepath.Join(dir, "index.html"))
	// Root: no parent link
	if strings.Contains(html, `href="../"`) {
		t.Error("root index.html should not contain parent link")
	}
	// Title should be "/"
	if !strings.Contains(html, "Index of /") {
		t.Errorf("expected title 'Index of /', got:\n%s", html)
	}
}

func TestGenerate_WithFiles(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "hello.txt"), "hello")
	writeTestFile(t, filepath.Join(dir, "world.bin"), "world!!")

	if err := Generate(dir); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	html := readFile(t, filepath.Join(dir, "index.html"))
	for _, name := range []string{"hello.txt", "world.bin"} {
		if !strings.Contains(html, name) {
			t.Errorf("expected %q in index.html", name)
		}
	}
	// index.html itself must not be listed
	if strings.Contains(html, `href="index.html"`) {
		t.Error("index.html should not list itself")
	}
}

func TestGenerate_NestedDirs(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "sub")
	if err := os.Mkdir(subdir, 0755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(subdir, "pkg.deb"), "fake deb content")

	if err := Generate(root); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Root index should list sub/ as a directory
	rootHTML := readFile(t, filepath.Join(root, "index.html"))
	if !strings.Contains(rootHTML, `href="sub/"`) {
		t.Errorf("root index.html should link to sub/, got:\n%s", rootHTML)
	}

	// Sub index should have parent link and list the file
	subHTML := readFile(t, filepath.Join(subdir, "index.html"))
	if !strings.Contains(subHTML, `href="../"`) {
		t.Errorf("sub index.html should contain parent link, got:\n%s", subHTML)
	}
	if !strings.Contains(subHTML, "pkg.deb") {
		t.Errorf("sub index.html should list pkg.deb, got:\n%s", subHTML)
	}
	// Title should reflect the subdirectory path
	if !strings.Contains(subHTML, "Index of /sub/") {
		t.Errorf("sub index.html title should be 'Index of /sub/', got:\n%s", subHTML)
	}
}

func TestGenerate_IndexHtmlExcluded(t *testing.T) {
	dir := t.TempDir()
	// Pre-create an index.html — it should be overwritten and not self-listed
	writeTestFile(t, filepath.Join(dir, "index.html"), "<old>")
	writeTestFile(t, filepath.Join(dir, "real.rpm"), "rpm data")

	if err := Generate(dir); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	html := readFile(t, filepath.Join(dir, "index.html"))
	if strings.Contains(html, `href="index.html"`) {
		t.Error("index.html should not list itself")
	}
	if !strings.Contains(html, "real.rpm") {
		t.Error("index.html should list real.rpm")
	}
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
	}
	for _, tc := range tests {
		got := humanSize(tc.n)
		if got != tc.want {
			t.Errorf("humanSize(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// helpers

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("readFile %s: %v", path, err)
	}
	return string(b)
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writeTestFile %s: %v", path, err)
	}
}

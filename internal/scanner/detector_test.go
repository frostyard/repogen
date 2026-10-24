package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDetectPackageTypeIgnoresRPM pins ADR-0016: RPM generation was removed,
// so .rpm files and files with the RPM lead magic are not packages.
func TestDetectPackageTypeIgnoresRPM(t *testing.T) {
	dir := t.TempDir()
	rpmLead := []byte{0xED, 0xAB, 0xEE, 0xDB, 0x03, 0x00}

	for _, name := range []string{"pkg-1.0-1.x86_64.rpm", "renamed.bin"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, rpmLead, 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := DetectPackageType(path)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if got != TypeUnknown {
			t.Errorf("%s: got %s, want unknown", name, got)
		}
	}

	scanned, err := NewFileSystemScanner().Scan(t.Context(), dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(scanned) != 0 {
		t.Errorf("scan found %d packages, want 0", len(scanned))
	}
}

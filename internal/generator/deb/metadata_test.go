package deb

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/frostyard/repogen/internal/models"
)

// TestGeneratePackagesFileMultilineDescription guards against the regression
// where a multi-line Description was emitted without indenting its
// continuation lines. That malformed stanza caused apt to drop every field
// after Description (notably Depends), so packages installed with none of
// their dependencies and could not be configured.
func TestGeneratePackagesFileMultilineDescription(t *testing.T) {
	pkgs := []models.Package{{
		Name:         "incus-base",
		Version:      "1:7.2-debian13-202607011055",
		Architecture: "amd64",
		Filename:     "pool/main/i/incus-base/incus-base.deb",
		Description: strings.Join([]string{
			"Incus - Container and virtualization daemon (container-only)",
			"Incus provides the ability to run containers and virtual machines.",
			".",
			"This package contains only what's needed to run containers.",
		}, "\n"),
		Dependencies: []string{"gnutls-bin", "lshw", "libfuse3-4"},
	}}

	data, err := GeneratePackagesFile(pkgs)
	if err != nil {
		t.Fatalf("GeneratePackagesFile: %v", err)
	}

	out := string(data)

	// Every continuation line of the Description must be indented so the
	// RFC822 parser treats it as part of the field rather than a new record.
	for _, line := range strings.Split(out, "\n") {
		if line == "" || strings.HasPrefix(line, " ") {
			continue
		}
		if !strings.Contains(line, ":") {
			t.Fatalf("unindented continuation line breaks stanza parsing: %q\nfull output:\n%s", line, out)
		}
	}

	// The Depends field must survive after the multi-line Description.
	if !strings.Contains(out, "\nDepends: gnutls-bin, lshw, libfuse3-4\n") {
		t.Fatalf("Depends field missing or malformed:\n%s", out)
	}

	// Re-parsing the emitted stanza must recover the dependencies, proving apt
	// would see them too.
	parsed, err := parsePackagesReader(strings.NewReader(out))
	if err != nil {
		t.Fatalf("parsePackagesReader: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected 1 package, got %d", len(parsed))
	}
	if got := len(parsed[0].Dependencies); got != 3 {
		t.Fatalf("round-trip lost dependencies: got %d, want 3 (%v)", got, parsed[0].Dependencies)
	}
}

func TestGeneratePackagesFileCanonicalOrder(t *testing.T) {
	first := models.Package{
		Name:         "same",
		Version:      "2.0",
		Architecture: "amd64",
		Filename:     "pool/main/s/same/same_2.0_amd64.deb",
		Metadata: map[string]interface{}{
			"Section":  "admin",
			"Priority": "optional",
		},
	}
	second := models.Package{
		Name:         "same",
		Version:      "1.0",
		Architecture: "all",
		Filename:     "pool/main/s/same/same_1.0_all.deb",
		Metadata: map[string]interface{}{
			"Priority": "optional",
			"Section":  "admin",
		},
	}

	input := []models.Package{first, second}
	before := append([]models.Package(nil), input...)
	got, err := GeneratePackagesFile(input)
	if err != nil {
		t.Fatal(err)
	}
	shuffled, err := GeneratePackagesFile([]models.Package{second, first})
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, shuffled) {
		t.Fatalf("shuffled packages produced different bytes:\n%s\n---\n%s", got, shuffled)
	}
	if !reflect.DeepEqual(input, before) {
		t.Fatal("GeneratePackagesFile mutated its caller's package order")
	}
	if strings.Index(string(got), "Version: 1.0") > strings.Index(string(got), "Version: 2.0") {
		t.Fatalf("versions are not in canonical order:\n%s", got)
	}
	if strings.Index(string(got), "Priority: optional") > strings.Index(string(got), "Section: admin") {
		t.Fatalf("arbitrary metadata fields are not sorted:\n%s", got)
	}
}

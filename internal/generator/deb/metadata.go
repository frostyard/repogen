package deb

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/frostyard/repogen/internal/models"
)

// GeneratePackagesFile creates a Debian Packages file from package metadata
func GeneratePackagesFile(packages []models.Package) ([]byte, error) {
	var buf bytes.Buffer

	// Sort packages alphabetically by name
	sort.Slice(packages, func(i, j int) bool {
		return packages[i].Name < packages[j].Name
	})

	for _, pkg := range packages {
		// Required fields
		fmt.Fprintf(&buf, "Package: %s\n", pkg.Name)
		fmt.Fprintf(&buf, "Version: %s\n", pkg.Version)
		fmt.Fprintf(&buf, "Architecture: %s\n", pkg.Architecture)

		// File information
		fmt.Fprintf(&buf, "Filename: %s\n", pkg.Filename)
		fmt.Fprintf(&buf, "Size: %d\n", pkg.Size)
		fmt.Fprintf(&buf, "MD5sum: %s\n", pkg.MD5Sum)
		fmt.Fprintf(&buf, "SHA1: %s\n", pkg.SHA1Sum)
		fmt.Fprintf(&buf, "SHA256: %s\n", pkg.SHA256Sum)
		fmt.Fprintf(&buf, "SHA512: %s\n", pkg.SHA512Sum)

		// Optional fields
		if pkg.Maintainer != "" {
			fmt.Fprintf(&buf, "Maintainer: %s\n", pkg.Maintainer)
		}

		if pkg.Homepage != "" {
			fmt.Fprintf(&buf, "Homepage: %s\n", pkg.Homepage)
		}

		if pkg.Description != "" {
			fmt.Fprintf(&buf, "Description: %s\n", formatDescription(pkg.Description))
		}

		if len(pkg.Dependencies) > 0 {
			fmt.Fprintf(&buf, "Depends: %s\n", strings.Join(pkg.Dependencies, ", "))
		}

		// Add other metadata fields
		for key, value := range pkg.Metadata {
			// Skip fields we've already handled
			if key == "Package" || key == "Version" || key == "Architecture" ||
				key == "Maintainer" || key == "Homepage" || key == "Description" ||
				key == "Depends" {
				continue
			}
			fmt.Fprintf(&buf, "%s: %v\n", key, value)
		}

		// Blank line between packages
		buf.WriteString("\n")
	}

	return buf.Bytes(), nil
}

// formatDescription renders a package description as a valid multi-line Debian
// control field. The synopsis stays on the "Description:" line and every
// wrapped line of the extended description is indented by one space (blank
// lines are written as " ."). parseControl strips the leading space from
// continuation lines, so it must be restored here; without it apt fails to
// parse the stanza and silently drops the fields that follow (notably
// Depends), leaving packages installable with none of their dependencies.
func formatDescription(desc string) string {
	lines := strings.Split(desc, "\n")

	var b strings.Builder
	b.WriteString(lines[0])
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			b.WriteString("\n .")
			continue
		}
		b.WriteString("\n ")
		b.WriteString(line)
	}

	return b.String()
}

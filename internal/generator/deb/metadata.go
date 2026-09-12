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

	ordered := append([]models.Package(nil), packages...)
	sort.Slice(ordered, func(i, j int) bool {
		return packageLess(ordered[i], ordered[j])
	})

	for _, pkg := range ordered {
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

		metadataKeys := make([]string, 0, len(pkg.Metadata))
		for key := range pkg.Metadata {
			// Skip fields we've already handled
			if key == "Package" || key == "Version" || key == "Architecture" ||
				key == "Maintainer" || key == "Homepage" || key == "Description" ||
				key == "Depends" {
				continue
			}
			metadataKeys = append(metadataKeys, key)
		}
		sort.Strings(metadataKeys)
		for _, key := range metadataKeys {
			value := pkg.Metadata[key]
			fmt.Fprintf(&buf, "%s: %v\n", key, value)
		}

		// Blank line between packages
		buf.WriteString("\n")
	}

	return buf.Bytes(), nil
}

func packageLess(left, right models.Package) bool {
	leftFields := []string{
		left.Name, left.Version, left.Architecture, left.Filename,
		left.MD5Sum, left.SHA1Sum, left.SHA256Sum, left.SHA512Sum,
		left.Maintainer, left.Homepage, left.Description,
		strings.Join(left.Dependencies, "\x00"),
	}
	rightFields := []string{
		right.Name, right.Version, right.Architecture, right.Filename,
		right.MD5Sum, right.SHA1Sum, right.SHA256Sum, right.SHA512Sum,
		right.Maintainer, right.Homepage, right.Description,
		strings.Join(right.Dependencies, "\x00"),
	}
	for index := range leftFields {
		if leftFields[index] != rightFields[index] {
			return leftFields[index] < rightFields[index]
		}
	}
	if left.Size != right.Size {
		return left.Size < right.Size
	}
	return canonicalMetadata(left.Metadata) < canonicalMetadata(right.Metadata)
}

func canonicalMetadata(metadata map[string]interface{}) string {
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var buf strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&buf, "%s=%v\x00", key, metadata[key])
	}
	return buf.String()
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

// Package sysext provides a generator for systemd-sysext repositories.
//
// # Filename Format
//
// Sysext files must follow a strict naming convention using underscores:
//
//	NAME_VERSION_ARCH.raw[.COMPRESSION]
//
// Where:
//   - NAME: Extension name (must not contain underscores)
//   - VERSION: Version string (must not contain underscores)
//   - ARCH: Architecture (e.g., x86-64, arm64; must not contain underscores)
//   - COMPRESSION: Optional compression suffix (.zst, .xz, or .gz)
//
// Examples:
//   - myext_1.0_x86-64.raw          -> name="myext", version="1.0", arch="x86-64"
//   - docker_24.0.5_x86-64.raw.zst  -> name="docker", version="24.0.5", arch="x86-64"
//   - nvidia_550.54.14_arm64.raw.xz -> name="nvidia", version="550.54.14", arch="arm64"
//
// Invalid filenames (will return an error):
//   - myext.raw                     -> missing underscore separators
//   - myext_1.0.raw                 -> missing architecture
//   - my_ext_1.0_x86-64.raw         -> too many underscores (ambiguous)
package sysext

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/frostyard/repogen/internal/models"
	"github.com/frostyard/repogen/internal/utils"
)

// stripCompressionSuffix removes known compression suffixes from a filename
// and returns the base name without compression extension.
func stripCompressionSuffix(filename string) string {
	for _, suffix := range []string{".zst", ".xz", ".gz"} {
		if strings.HasSuffix(filename, suffix) {
			return strings.TrimSuffix(filename, suffix)
		}
	}
	return filename
}

// ParsePackage parses a systemd-sysext file and extracts metadata from the filename.
//
// The filename must follow the pattern: NAME_VERSION_ARCH.raw[.zst|.xz|.gz]
// where NAME, VERSION, and ARCH are separated by exactly two underscores.
func ParsePackage(path string) (*models.Package, error) {
	// Calculate checksums
	checksums, err := utils.CalculateChecksums(path)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate checksums: %w", err)
	}

	basename := filepath.Base(path)

	// Strip compression suffix first (.raw.zst -> .raw)
	nameWithRaw := stripCompressionSuffix(basename)

	// Must end with .raw
	if !strings.HasSuffix(nameWithRaw, ".raw") {
		return nil, fmt.Errorf("sysext file must have .raw extension: %s", basename)
	}

	// Remove .raw suffix to get NAME_VERSION_ARCH
	nameVersionArch := strings.TrimSuffix(nameWithRaw, ".raw")

	// Split by underscore - must have exactly two underscores (3 parts)
	parts := strings.Split(nameVersionArch, "_")
	if len(parts) != 3 {
		return nil, fmt.Errorf("sysext filename must follow NAME_VERSION_ARCH.raw format with exactly two underscores: %s", basename)
	}

	name := parts[0]
	version := parts[1]
	arch := parts[2]

	if name == "" {
		return nil, fmt.Errorf("sysext extension name cannot be empty: %s", basename)
	}
	if version == "" {
		return nil, fmt.Errorf("sysext version cannot be empty: %s", basename)
	}
	if arch == "" {
		return nil, fmt.Errorf("sysext architecture cannot be empty: %s", basename)
	}

	pkg := &models.Package{
		Name:         name,
		Version:      version,
		Architecture: arch,
		Filename:     path,
		Size:         checksums.Size,
		MD5Sum:       checksums.MD5,
		SHA1Sum:      checksums.SHA1,
		SHA256Sum:    checksums.SHA256,
		SHA512Sum:    checksums.SHA512,
		Metadata:     make(map[string]interface{}),
	}

	return pkg, nil
}

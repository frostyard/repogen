// Package sysext provides a generator for systemd-sysext repositories.
//
// # Filename Format
//
// Sysext files must follow a strict naming convention using underscores:
//
//	NAME_VERSION_OSVERSION_ARCH.raw[.COMPRESSION]
//
// Where:
//   - NAME: Extension name (must not contain underscores)
//   - VERSION: Version string (must not contain underscores)
//   - OSVERSION: OS version (e.g., 13 for Debian Trixie; must not contain underscores)
//   - ARCH: Architecture (e.g., x86-64, arm64; must not contain underscores)
//   - COMPRESSION: Optional compression suffix (.zst, .xz, or .gz)
//
// Examples:
//   - myext_1.0_13_x86-64.raw          -> name="myext", version="1.0", osversion="13", arch="x86-64"
//   - docker_24.0.5_13_x86-64.raw.zst  -> name="docker", version="24.0.5", osversion="13", arch="x86-64"
//   - nvidia_550.54.14_12_arm64.raw.xz -> name="nvidia", version="550.54.14", osversion="12", arch="arm64"
//
// Invalid filenames (will return an error):
//   - myext.raw                             -> missing underscore separators
//   - myext_1.0_x86-64.raw                  -> missing OS version (3 parts instead of 4)
//   - my_ext_1.0_13_x86-64.raw              -> too many underscores (5+ parts)
//   - docker_24.0.5_x86-64.raw              -> missing OS version
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
// The filename must follow the pattern: NAME_VERSION_OSVERSION_ARCH.raw[.zst|.xz|.gz]
// where NAME, VERSION, OSVERSION, and ARCH are separated by exactly three underscores (4 parts).
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

	// Remove .raw suffix to get NAME_VERSION_OSVERSION_ARCH
	nameVersionOSVersionArch := strings.TrimSuffix(nameWithRaw, ".raw")

	// Split by underscore - must have exactly three underscores (4 parts)
	parts := strings.Split(nameVersionOSVersionArch, "_")
	if len(parts) != 4 {
		return nil, fmt.Errorf("sysext filename must follow NAME_VERSION_OSVERSION_ARCH.raw format with exactly three underscores (4 parts): %s", basename)
	}

	name := parts[0]
	version := parts[1]
	osversion := parts[2]
	arch := parts[3]

	if name == "" {
		return nil, fmt.Errorf("sysext extension name cannot be empty: %s", basename)
	}
	if version == "" {
		return nil, fmt.Errorf("sysext version cannot be empty: %s", basename)
	}
	if osversion == "" {
		return nil, fmt.Errorf("sysext OS version cannot be empty: %s", basename)
	}
	if arch == "" {
		return nil, fmt.Errorf("sysext architecture cannot be empty: %s", basename)
	}

	metadata := make(map[string]interface{})
	metadata["OSVersion"] = osversion

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
		Metadata:     metadata,
	}

	return pkg, nil
}

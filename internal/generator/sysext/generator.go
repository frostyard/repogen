package sysext

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/frostyard/repogen/internal/generator"
	"github.com/frostyard/repogen/internal/models"
	"github.com/frostyard/repogen/internal/scanner"
	"github.com/frostyard/repogen/internal/utils"
	"github.com/sirupsen/logrus"
)

// Generator implements the generator.Generator interface for systemd-sysext repositories.
//
// The generated repository structure is:
//
//	<output>/ext/<extension-name>/
//	    SHA256SUMS                    # Standard checksum file for systemd-sysupdate
//	    <extension-name>.transfer     # systemd-sysupdate transfer configuration
//	    <extension>.raw               # Extension files
//	    <extension>.raw.zst           # Compressed variants
//
// Sysext filenames follow the format: NAME_VERSION_OSVERSION_ARCH.raw[.compression]
// Example: docker_24.0.5_13_x86-64.raw.zst
type Generator struct {
	baseURL string
}

// NewGenerator creates a new systemd-sysext generator
func NewGenerator(baseURL string) generator.Generator {
	return &Generator{
		baseURL: baseURL,
	}
}

// Generate creates a systemd-sysext repository structure.
//
// Output structure:
//
//	<output>/ext/<name>/SHA256SUMS
//	<output>/ext/<name>/<name>_<version>_<osversion>_<arch>.raw[.compression]
func (g *Generator) Generate(ctx context.Context, config *models.RepositoryConfig, packages []models.Package) error {
	logrus.Info("Generating systemd-sysext repository...")

	// Group packages by extension name
	extPackages := make(map[string][]models.Package)
	for _, pkg := range packages {
		extPackages[pkg.Name] = append(extPackages[pkg.Name], pkg)
	}

	// Generate repository for each extension
	for extName, pkgs := range extPackages {
		if err := g.generateForExtension(ctx, config, extName, pkgs); err != nil {
			return fmt.Errorf("failed to generate for extension %s: %w", extName, err)
		}
	}

	// Generate index file listing all extensions
	if err := g.generateIndex(config, extPackages); err != nil {
		return fmt.Errorf("failed to generate index: %w", err)
	}

	logrus.Info("systemd-sysext repository generated successfully")
	return nil
}

// generateForExtension generates repository files for a specific extension
func (g *Generator) generateForExtension(ctx context.Context, config *models.RepositoryConfig, extName string, packages []models.Package) error {
	logrus.Infof("Generating for extension: %s", extName)

	// Create extension directory: <output>/ext/<name>/
	extDir := filepath.Join(config.OutputDir, "ext", extName)
	if err := utils.EnsureDir(extDir); err != nil {
		return err
	}

	// Copy files and build SHA256SUMS content
	// Use a map to deduplicate entries by filename (in case existing metadata
	// and new packages overlap)
	sha256Entries := make(map[string]string) // filename -> sha256

	for i := range packages {
		pkg := &packages[i]
		basename := filepath.Base(pkg.Filename)
		dstPath := filepath.Join(extDir, basename)

		// Check if package needs to be copied
		srcPath, finalDstPath, needsCopy, err := utils.ShouldCopyPackage(pkg, dstPath, config.OutputDir)
		if err != nil {
			return fmt.Errorf("package copy check failed for %s: %w", pkg.Name, err)
		}

		if needsCopy {
			logrus.Debugf("Copying extension: %s -> %s", srcPath, finalDstPath)

			if err := utils.CopyFile(srcPath, finalDstPath); err != nil {
				return fmt.Errorf("failed to copy %s: %w", srcPath, err)
			}

			// Recalculate checksums on the copied file
			checksums, err := utils.CalculateChecksums(finalDstPath)
			if err != nil {
				return fmt.Errorf("failed to calculate checksums for %s: %w", basename, err)
			}
			pkg.SHA256Sum = checksums.SHA256
		} else {
			logrus.Debugf("Skipping copy for extension: %s", pkg.Name)
		}

		// Add entry to map (deduplicates by filename)
		sha256Entries[basename] = pkg.SHA256Sum
	}

	// Build SHA256SUMS content from deduplicated entries
	var sha256Lines []string
	for filename, hash := range sha256Entries {
		// Format: "<hash>  <filename>" (two spaces per shasum convention)
		sha256Lines = append(sha256Lines, fmt.Sprintf("%s  %s", hash, filename))
	}

	// Write SHA256SUMS file
	sha256sumsPath := filepath.Join(extDir, "SHA256SUMS")
	sha256Content := strings.Join(sha256Lines, "\n") + "\n"
	if err := utils.WriteFile(sha256sumsPath, []byte(sha256Content), 0644); err != nil {
		return fmt.Errorf("failed to write SHA256SUMS: %w", err)
	}

	// Generate systemd-sysupdate transfer configuration file
	if err := g.generateTransferFile(extDir, extName); err != nil {
		return fmt.Errorf("failed to write transfer file: %w", err)
	}

	logrus.Infof("Generated SHA256SUMS for %s (%d files)", extName, len(sha256Entries))
	return nil
}

// generateTransferFile creates a systemd-sysupdate transfer configuration file
// for the extension. This file can be placed in /etc/sysupdate.d/ to enable
// automatic updates via systemd-sysupdate.
//
// The MatchPattern uses specifiers that are expanded at config-parse time:
// - @v: version placeholder (matched from filename)
// - %w: OS version specifier (expands to VERSION_ID from /etc/os-release)
// - %a: architecture specifier (expands to systemd architecture)
func (g *Generator) generateTransferFile(extDir, extName string) error {
	// Build the source URL path
	sourceURL := strings.TrimSuffix(g.baseURL, "/") + "/ext/" + extName + "/"

	// Generate transfer file content
	// The @v is the version placeholder matched from filenames
	// The %w and %a are systemd-sysupdate specifiers expanded at config-parse time
	transferContent := fmt.Sprintf(`[Transfer]
Verify=false

[Source]
Type=url-file
Path=%s
MatchPattern=%s_@v_%%w_%%a.raw.zst \
             %s_@v_%%w_%%a.raw.xz \
             %s_@v_%%w_%%a.raw.gz \
             %s_@v_%%w_%%a.raw

[Target]
Type=regular-file
Path=/var/lib/extensions.d/
MatchPattern=%s_@v_%%w_%%a.raw.zst \
             %s_@v_%%w_%%a.raw.xz \
             %s_@v_%%w_%%a.raw.gz \
             %s_@v_%%w_%%a.raw
CurrentSymlink=%s.raw
`, sourceURL, extName, extName, extName, extName, extName, extName, extName, extName, extName)

	transferPath := filepath.Join(extDir, extName+".transfer")
	if err := utils.WriteFile(transferPath, []byte(transferContent), 0644); err != nil {
		return err
	}

	logrus.Debugf("Generated transfer file: %s", transferPath)
	return nil
}

// generateIndex creates an index file listing all available extensions.
// The index is a simple newline-separated list of extension names.
func (g *Generator) generateIndex(config *models.RepositoryConfig, extPackages map[string][]models.Package) error {
	extDir := filepath.Join(config.OutputDir, "ext")
	if err := utils.EnsureDir(extDir); err != nil {
		return err
	}

	// Collect extension names and sort them for consistent output
	var names []string
	for name := range extPackages {
		names = append(names, name)
	}
	sort.Strings(names)

	// Write index file (one extension name per line)
	indexPath := filepath.Join(extDir, "index")
	indexContent := strings.Join(names, "\n") + "\n"
	if err := utils.WriteFile(indexPath, []byte(indexContent), 0644); err != nil {
		return err
	}

	logrus.Debugf("Generated index file with %d extensions", len(names))
	return nil
}

// ValidatePackages checks if packages are valid for this generator
func (g *Generator) ValidatePackages(packages []models.Package) error {
	if g.baseURL == "" {
		return fmt.Errorf("--base-url is required for sysext repository generation")
	}
	for _, pkg := range packages {
		if pkg.Name == "" {
			return fmt.Errorf("sysext package missing name: %s", pkg.Filename)
		}
		if pkg.Version == "" {
			return fmt.Errorf("sysext package missing version: %s", pkg.Filename)
		}
	}
	return nil
}

// GetSupportedType returns the package type this generator supports
func (g *Generator) GetSupportedType() scanner.PackageType {
	return scanner.TypeSysext
}

// ParseExistingMetadata reads existing SHA256SUMS files to support incremental mode.
//
// It scans <output>/ext/<name>/SHA256SUMS files and reconstructs package metadata
// from the filenames listed in those files.
func (g *Generator) ParseExistingMetadata(config *models.RepositoryConfig) ([]models.Package, error) {
	var allPackages []models.Package

	extDir := filepath.Join(config.OutputDir, "ext")

	// Check if ext directory exists
	if _, err := os.Stat(extDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("no existing sysext metadata found")
	}

	// List extension directories
	entries, err := os.ReadDir(extDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read ext directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		extName := entry.Name()
		sha256sumsPath := filepath.Join(extDir, extName, "SHA256SUMS")

		packages, err := parseSHA256SUMS(sha256sumsPath, extDir, extName)
		if err != nil {
			logrus.Debugf("Could not parse SHA256SUMS for %s: %v", extName, err)
			continue
		}

		allPackages = append(allPackages, packages...)
	}

	if len(allPackages) == 0 {
		return nil, fmt.Errorf("no existing sysext packages found")
	}

	return allPackages, nil
}

// parseSHA256SUMS parses a SHA256SUMS file and returns package metadata
func parseSHA256SUMS(sha256sumsPath, extDir, extName string) ([]models.Package, error) {
	f, err := os.Open(sha256sumsPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var packages []models.Package
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		// Format: "<sha256>  <filename>" (two spaces)
		// Also handle single space for compatibility
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}

		sha256sum := parts[0]
		filename := parts[1]

		// Reconstruct full path for the file
		filePath := filepath.Join(extDir, extName, filename)

		// Parse metadata from filename
		pkg, err := parseFilenameMetadata(filePath, filename)
		if err != nil {
			logrus.Debugf("Could not parse filename %s: %v", filename, err)
			continue
		}

		pkg.SHA256Sum = sha256sum
		packages = append(packages, *pkg)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return packages, nil
}

// parseFilenameMetadata extracts name, version, osversion, and arch from a sysext filename
func parseFilenameMetadata(filePath, filename string) (*models.Package, error) {
	// Strip compression suffix
	nameWithRaw := stripCompressionSuffix(filename)

	if !strings.HasSuffix(nameWithRaw, ".raw") {
		return nil, fmt.Errorf("not a .raw file: %s", filename)
	}

	nameVersionOSVersionArch := strings.TrimSuffix(nameWithRaw, ".raw")
	parts := strings.Split(nameVersionOSVersionArch, "_")
	if len(parts) != 4 {
		return nil, fmt.Errorf("invalid filename format (expected NAME_VERSION_OSVERSION_ARCH.raw with exactly 3 underscores): %s", filename)
	}

	metadata := make(map[string]interface{})
	metadata["OSVersion"] = parts[2]

	return &models.Package{
		Name:         parts[0],
		Version:      parts[1],
		Architecture: parts[3],
		Filename:     filePath,
		Metadata:     metadata,
	}, nil
}

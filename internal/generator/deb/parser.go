package deb

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/frostyard/repogen/internal/generator"
	"github.com/frostyard/repogen/internal/models"
	"github.com/frostyard/repogen/internal/utils"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// ParsePackage parses a .deb file and extracts metadata
func ParsePackage(path string) (*models.Package, error) {
	// Calculate checksums
	checksums, err := utils.CalculateChecksums(path)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate checksums: %w", err)
	}

	// Extract control file from the .deb
	control, err := extractControl(path)
	if err != nil {
		return nil, fmt.Errorf("failed to extract control: %w", err)
	}

	// Parse control file
	pkg, err := parseControl(control)
	if err != nil {
		return nil, fmt.Errorf("failed to parse control: %w", err)
	}

	// Set file information (keep full path for copying)
	pkg.Filename = path
	pkg.Size = checksums.Size
	pkg.MD5Sum = checksums.MD5
	pkg.SHA1Sum = checksums.SHA1
	pkg.SHA256Sum = checksums.SHA256
	pkg.SHA512Sum = checksums.SHA512

	return pkg, nil
}

// extractControl extracts the control file from a .deb package
func extractControl(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	// .deb files are ar archives
	// Skip the first 8 bytes ("!<arch>\n")
	header := make([]byte, 8)
	if _, err := f.Read(header); err != nil {
		return nil, err
	}

	// Read ar archive entries
	for {
		// Read ar header (60 bytes)
		arHeader := make([]byte, 60)
		n, err := f.Read(arHeader)
		if err == io.EOF {
			break
		}
		if err != nil || n != 60 {
			return nil, fmt.Errorf("failed to read ar header")
		}

		// Parse filename (first 16 bytes, space-padded)
		// Also trim trailing slash that ar format may include
		filename := strings.TrimRight(strings.TrimSpace(string(arHeader[0:16])), "/")

		// Parse file size (bytes 48-58, decimal)
		sizeStr := strings.TrimSpace(string(arHeader[48:58]))
		var size int64
		_, _ = fmt.Sscanf(sizeStr, "%d", &size)

		// Check if this is the control archive
		if strings.HasPrefix(filename, "control.tar") {
			// Read control archive data
			data := make([]byte, size)
			if _, err := io.ReadFull(f, data); err != nil {
				return nil, err
			}

			// Extract control file from control.tar
			return extractControlFromTar(data, filename)
		}

		// Skip this file's data
		if _, err := f.Seek(size, io.SeekCurrent); err != nil {
			return nil, err
		}

		// Align to 2-byte boundary
		if size%2 != 0 {
			_, _ = f.Seek(1, io.SeekCurrent)
		}
	}

	return nil, fmt.Errorf("control.tar not found in package")
}

// extractControlFromTar extracts the control file from control.tar*
func extractControlFromTar(data []byte, filename string) ([]byte, error) {
	var tarReader *tar.Reader

	// Decompress based on extension
	if strings.HasSuffix(filename, ".gz") {
		gr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer func() { _ = gr.Close() }()
		tarReader = tar.NewReader(gr)
	} else if strings.HasSuffix(filename, ".xz") {
		xr, err := xz.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		tarReader = tar.NewReader(xr)
	} else if strings.HasSuffix(filename, ".zst") {
		zr, err := zstd.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		tarReader = tar.NewReader(zr)
	} else {
		tarReader = tar.NewReader(bytes.NewReader(data))
	}

	// Find and read control file
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		if header.Name == "./control" || header.Name == "control" {
			return io.ReadAll(tarReader)
		}
	}

	return nil, fmt.Errorf("control file not found in control.tar")
}

// parseControl parses the Debian control file format
func parseControl(data []byte) (*models.Package, error) {
	pkg := &models.Package{
		Metadata: make(map[string]interface{}),
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	var currentKey string
	var currentValue strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		// Handle continuation lines (start with space)
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			currentValue.WriteString("\n")
			currentValue.WriteString(strings.TrimSpace(line))
			continue
		}

		// Save previous key-value pair
		if currentKey != "" {
			setValue(pkg, currentKey, currentValue.String())
		}

		// Parse new key-value pair
		if strings.Contains(line, ":") {
			parts := strings.SplitN(line, ":", 2)
			currentKey = strings.TrimSpace(parts[0])
			currentValue.Reset()
			if len(parts) > 1 {
				currentValue.WriteString(strings.TrimSpace(parts[1]))
			}
		}
	}

	// Save last key-value pair
	if currentKey != "" {
		setValue(pkg, currentKey, currentValue.String())
	}

	return pkg, scanner.Err()
}

// setValue sets a field in the Package based on the control file key
func setValue(pkg *models.Package, key, value string) {
	switch key {
	case "Package":
		pkg.Name = value
	case "Version":
		pkg.Version = value
	case "Architecture":
		pkg.Architecture = value
	case "Description":
		pkg.Description = value
	case "Maintainer":
		pkg.Maintainer = value
	case "Homepage":
		pkg.Homepage = value
	case "License":
		pkg.License = value
	case "Depends":
		// Parse dependencies (comma-separated)
		deps := strings.Split(value, ",")
		for _, dep := range deps {
			pkg.Dependencies = append(pkg.Dependencies, strings.TrimSpace(dep))
		}
	default:
		// Store other fields in metadata
		pkg.Metadata[key] = value
	}
}

// ParseExistingMetadata reads Packages files and returns existing packages.
//
// For each architecture and component it reads every existing Packages and
// Packages.gz, which must agree. An index that exists but cannot be read or parsed, an
// existing index outside the selected architectures/components, or an
// existing suite with no selected index is an error: incremental generation
// must never drop packages it failed to read. Only when dists/<codename> is
// absent does it return an error wrapping generator.ErrNoExistingMetadata.
func (g *Generator) ParseExistingMetadata(config *models.RepositoryConfig) ([]models.Package, error) {
	suiteDir := filepath.Join(config.OutputDir, "dists", config.Codename)
	if _, err := os.Lstat(suiteDir); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no Debian suite %s: %w", suiteDir, generator.ErrNoExistingMetadata)
		}
		return nil, fmt.Errorf("stat %s: %w", suiteDir, err)
	}

	// Every existing index must be among the selected arch/component set:
	// otherwise regenerating Release would silently drop its packages.
	selected := make(map[string]bool)
	for _, arch := range config.Arches {
		for _, comp := range config.Components {
			selected[filepath.Join(comp, "binary-"+arch)] = true
		}
	}
	existing, err := filepath.Glob(filepath.Join(suiteDir, "*", "binary-*", "Packages*"))
	if err != nil {
		return nil, fmt.Errorf("list indexes in %s: %w", suiteDir, err)
	}
	for _, path := range existing {
		rel, err := filepath.Rel(suiteDir, filepath.Dir(path))
		if err != nil {
			return nil, err
		}
		if !selected[rel] {
			return nil, fmt.Errorf("existing index %s is outside the selected architectures/components; include it to keep its packages", path)
		}
	}

	var allPackages []models.Package
	found := false

	for _, arch := range config.Arches {
		for _, comp := range config.Components {
			packagesPath := filepath.Join(
				config.OutputDir,
				"dists",
				config.Codename,
				comp,
				fmt.Sprintf("binary-%s", arch),
				"Packages",
			)

			packages, present, err := readIndexRepresentations(packagesPath)
			if err != nil {
				return nil, err
			}
			if !present {
				continue
			}
			found = true
			allPackages = append(allPackages, packages...)
		}
	}

	if !found {
		return nil, fmt.Errorf("existing Debian suite %s has no Packages index for the selected architectures", suiteDir)
	}

	return allPackages, nil
}

// readIndexRepresentations reads every existing representation of one index
// (Packages and Packages.gz). Each must be readable and parse, and when both
// exist their decoded bytes must be identical, so a corrupt or stale copy is
// never overwritten unnoticed. present is false when neither exists.
func readIndexRepresentations(packagesPath string) (packages []models.Package, present bool, err error) {
	candidates := []struct {
		path string
		read func(string) ([]byte, error)
	}{
		{packagesPath, os.ReadFile},
		{packagesPath + ".gz", readGzipFile},
	}
	var firstPath string
	var firstContent []byte
	for _, c := range candidates {
		// Lstat so that a dangling symlink counts as an existing, unreadable
		// representation rather than an absent one.
		if _, statErr := os.Lstat(c.path); statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return nil, false, fmt.Errorf("stat %s: %w", c.path, statErr)
		}
		content, readErr := c.read(c.path)
		if readErr != nil {
			return nil, false, fmt.Errorf("read %s: %w", c.path, readErr)
		}
		if !present {
			parsed, parseErr := parsePackagesReader(bytes.NewReader(content))
			if parseErr != nil {
				return nil, false, fmt.Errorf("parse %s: %w", c.path, parseErr)
			}
			packages, present, firstPath, firstContent = parsed, true, c.path, content
			continue
		}
		if !bytes.Equal(firstContent, content) {
			return nil, false, fmt.Errorf("%s and %s differ", firstPath, c.path)
		}
	}
	return packages, present, nil
}

func readGzipFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	return io.ReadAll(gz)
}

func parsePackagesReader(r io.Reader) ([]models.Package, error) {
	var packages []models.Package
	var currentPkg *models.Package
	lineNo := 0

	finish := func() error {
		if currentPkg == nil {
			return nil
		}
		missing := []string{}
		for _, f := range []struct{ name, value string }{
			{"Package", currentPkg.Name},
			{"Version", currentPkg.Version},
			{"Architecture", currentPkg.Architecture},
			{"Filename", currentPkg.Filename},
			{"SHA256", currentPkg.SHA256Sum},
		} {
			if f.value == "" {
				missing = append(missing, f.name)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("stanza ending at line %d is missing %s", lineNo, strings.Join(missing, ", "))
		}
		packages = append(packages, *currentPkg)
		currentPkg = nil
		return nil
	}

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()

		// Empty line = end of package entry
		if line == "" {
			if err := finish(); err != nil {
				return nil, err
			}
			continue
		}

		// Continuation lines (multi-line fields such as Description) belong
		// to the previous field.
		if line[0] == ' ' || line[0] == '\t' {
			if currentPkg == nil {
				return nil, fmt.Errorf("line %d: continuation line outside a stanza", lineNo)
			}
			continue
		}

		// Parse field: value
		parts := strings.SplitN(line, ": ", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("line %d: malformed field %q", lineNo, line)
		}

		field := parts[0]
		value := parts[1]

		if currentPkg == nil {
			currentPkg = &models.Package{
				Metadata: make(map[string]interface{}),
			}
		}

		// Parse known fields
		switch field {
		case "Package":
			currentPkg.Name = value
		case "Version":
			currentPkg.Version = value
		case "Architecture":
			currentPkg.Architecture = value
		case "Filename":
			currentPkg.Filename = value
		case "Size":
			size, err := strconv.ParseInt(value, 10, 64)
			if err != nil || size < 0 {
				return nil, fmt.Errorf("line %d: invalid Size %q", lineNo, value)
			}
			currentPkg.Size = size
		case "MD5sum":
			currentPkg.MD5Sum = value
		case "SHA1":
			currentPkg.SHA1Sum = value
		case "SHA256":
			currentPkg.SHA256Sum = value
		case "SHA512":
			currentPkg.SHA512Sum = value
		case "Description":
			currentPkg.Description = value
		case "Maintainer":
			currentPkg.Maintainer = value
		case "Homepage":
			currentPkg.Homepage = value
		case "Depends":
			currentPkg.Dependencies = strings.Split(value, ", ")
		default:
			currentPkg.Metadata[field] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Don't forget last package
	if err := finish(); err != nil {
		return nil, err
	}

	return packages, nil
}

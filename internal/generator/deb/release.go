package deb

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/frostyard/repogen/internal/models"
	"github.com/frostyard/repogen/internal/utils"
)

// ReleaseFileInfo contains information about a file in the release
type ReleaseFileInfo struct {
	Path     string
	Checksum *utils.Checksum
}

// GenerateReleaseFile creates a Debian Release file
func GenerateReleaseFile(config *models.RepositoryConfig, files []ReleaseFileInfo) ([]byte, error) {
	return GenerateReleaseFileAt(config, files, time.Now())
}

// GenerateReleaseFileAt creates a Debian Release file with an explicit
// publication timestamp.
func GenerateReleaseFileAt(config *models.RepositoryConfig, files []ReleaseFileInfo, publishedAt time.Time) ([]byte, error) {
	var buf bytes.Buffer
	arches := append([]string(nil), config.Arches...)
	components := append([]string(nil), config.Components...)
	orderedFiles := append([]ReleaseFileInfo(nil), files...)
	sort.Strings(arches)
	sort.Strings(components)
	sort.Slice(orderedFiles, func(i, j int) bool {
		left := orderedFiles[i]
		right := orderedFiles[j]
		if left.Path != right.Path {
			return left.Path < right.Path
		}
		if left.Checksum.Size != right.Checksum.Size {
			return left.Checksum.Size < right.Checksum.Size
		}
		leftDigests := left.Checksum.MD5 + left.Checksum.SHA1 + left.Checksum.SHA256 + left.Checksum.SHA512
		rightDigests := right.Checksum.MD5 + right.Checksum.SHA1 + right.Checksum.SHA256 + right.Checksum.SHA512
		return leftDigests < rightDigests
	})

	// Required fields
	fmt.Fprintf(&buf, "Origin: %s\n", config.Origin)
	fmt.Fprintf(&buf, "Label: %s\n", config.Label)
	fmt.Fprintf(&buf, "Suite: %s\n", config.Suite)
	fmt.Fprintf(&buf, "Codename: %s\n", config.Codename)
	fmt.Fprintf(&buf, "Architectures: %s\n", strings.Join(arches, " "))
	fmt.Fprintf(&buf, "Components: %s\n", strings.Join(components, " "))
	fmt.Fprintf(&buf, "Date: %s\n", publishedAt.UTC().Format(time.RFC1123Z))

	// MD5Sum section
	buf.WriteString("MD5Sum:\n")
	for _, file := range orderedFiles {
		fmt.Fprintf(&buf, " %s %d %s\n", file.Checksum.MD5, file.Checksum.Size, file.Path)
	}

	// SHA1 section
	buf.WriteString("SHA1:\n")
	for _, file := range orderedFiles {
		fmt.Fprintf(&buf, " %s %d %s\n", file.Checksum.SHA1, file.Checksum.Size, file.Path)
	}

	// SHA256 section
	buf.WriteString("SHA256:\n")
	for _, file := range orderedFiles {
		fmt.Fprintf(&buf, " %s %d %s\n", file.Checksum.SHA256, file.Checksum.Size, file.Path)
	}

	// SHA512 section (optional but recommended)
	buf.WriteString("SHA512:\n")
	for _, file := range orderedFiles {
		fmt.Fprintf(&buf, " %s %d %s\n", file.Checksum.SHA512, file.Checksum.Size, file.Path)
	}

	return buf.Bytes(), nil
}

// CalculateReleaseFileInfos calculates checksums for all metadata files
func CalculateReleaseFileInfos(basePath string, files []string) ([]ReleaseFileInfo, error) {
	var infos []ReleaseFileInfo

	orderedFiles := append([]string(nil), files...)
	sort.Strings(orderedFiles)
	for _, file := range orderedFiles {
		fullPath := filepath.Join(basePath, file)
		checksum, err := utils.CalculateChecksums(fullPath)
		if err != nil {
			return nil, fmt.Errorf("failed to calculate checksum for %s: %w", file, err)
		}

		infos = append(infos, ReleaseFileInfo{
			Path:     file,
			Checksum: checksum,
		})
	}

	return infos, nil
}

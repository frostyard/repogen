package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
)

// FileSystemScanner implements Scanner interface for filesystem scanning
type FileSystemScanner struct{}

// NewFileSystemScanner creates a new filesystem scanner
func NewFileSystemScanner() *FileSystemScanner {
	return &FileSystemScanner{}
}

// Scan recursively scans a directory for packages
func (s *FileSystemScanner) Scan(ctx context.Context, dir string) ([]ScannedPackage, error) {
	var packages []ScannedPackage

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Try to detect package type
		pkgType, err := s.DetectType(path)
		if err != nil {
			logrus.Warnf("Failed to detect type for %s: %v", path, err)
			return nil
		}

		// Skip unknown types
		if pkgType == TypeUnknown {
			// RPM support was removed (ADR-0016); say so instead of
			// dropping the file silently.
			if strings.EqualFold(filepath.Ext(path), ".rpm") {
				logrus.Warnf("Skipping %s: RPM generation was removed from repogen (ADR-0016)", path)
			}
			return nil
		}

		logrus.Debugf("Found %s package: %s", pkgType, path)

		packages = append(packages, ScannedPackage{
			Path: path,
			Type: pkgType,
			Size: info.Size(),
		})

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to scan directory: %w", err)
	}

	logrus.Infof("Found %d packages in %s", len(packages), dir)
	return packages, nil
}

// DetectType determines the package type of a file
func (s *FileSystemScanner) DetectType(path string) (PackageType, error) {
	return DetectPackageType(path)
}

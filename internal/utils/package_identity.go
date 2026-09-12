package utils

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/frostyard/repogen/internal/models"
	"github.com/frostyard/repogen/internal/scanner"
)

// PackageIdentity returns a unique identifier for a package based on format
func PackageIdentity(pkg models.Package, pkgType scanner.PackageType) string {
	switch pkgType {
	case scanner.TypeDeb, scanner.TypeApk, scanner.TypePacman:
		return fmt.Sprintf("%s:%s:%s", pkg.Name, pkg.Version, pkg.Architecture)
	case scanner.TypeRpm:
		release := "1"
		if r, ok := pkg.Metadata["Release"].(string); ok {
			release = r
		}
		return fmt.Sprintf("%s:%s:%s:%s", pkg.Name, pkg.Version, release, pkg.Architecture)
	case scanner.TypeHomebrewBottle:
		return fmt.Sprintf("%s:%s", pkg.Name, pkg.Version)
	case scanner.TypeSysext:
		osVersion, _ := pkg.Metadata["OSVersion"].(string)
		return fmt.Sprintf("%s:%s:%s:%s", pkg.Name, pkg.Version, osVersion, pkg.Architecture)
	default:
		return fmt.Sprintf("%s:%s", pkg.Name, pkg.Version)
	}
}

// DetectDigestConflicts returns packages whose logical identity already exists
// with another filename or with different or missing SHA-256 evidence.
func DetectDigestConflicts(existing, newPackages []models.Package, pkgType scanner.PackageType) []models.Package {
	type artifact struct {
		filename string
		digest   string
	}
	existingArtifacts := make(map[string]artifact, len(existing))
	for _, pkg := range existing {
		existingArtifacts[PackageIdentity(pkg, pkgType)] = artifact{
			filename: filepath.Base(pkg.Filename),
			digest:   pkg.SHA256Sum,
		}
	}

	var conflicts []models.Package
	for _, pkg := range newPackages {
		existingArtifact, exists := existingArtifacts[PackageIdentity(pkg, pkgType)]
		if exists && (existingArtifact.filename != filepath.Base(pkg.Filename) ||
			existingArtifact.digest == "" ||
			pkg.SHA256Sum == "" ||
			!strings.EqualFold(existingArtifact.digest, pkg.SHA256Sum)) {
			conflicts = append(conflicts, pkg)
		}
	}
	return conflicts
}

// DetectConflicts returns packages from newPackages that conflict with existing
func DetectConflicts(existing, newPackages []models.Package, pkgType scanner.PackageType) []models.Package {
	existingMap := make(map[string]bool)
	for _, pkg := range existing {
		existingMap[PackageIdentity(pkg, pkgType)] = true
	}

	var conflicts []models.Package
	for _, pkg := range newPackages {
		if existingMap[PackageIdentity(pkg, pkgType)] {
			conflicts = append(conflicts, pkg)
		}
	}
	return conflicts
}

// FilterOutConflicts returns packages that are not in the toExclude list
func FilterOutConflicts(packages, toExclude []models.Package, pkgType scanner.PackageType) []models.Package {
	excludeMap := make(map[string]bool)
	for _, pkg := range toExclude {
		excludeMap[PackageIdentity(pkg, pkgType)] = true
	}

	var result []models.Package
	for _, pkg := range packages {
		if !excludeMap[PackageIdentity(pkg, pkgType)] {
			result = append(result, pkg)
		}
	}
	return result
}

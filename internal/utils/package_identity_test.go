package utils

import (
	"testing"

	"github.com/frostyard/repogen/internal/models"
	"github.com/frostyard/repogen/internal/scanner"
)

func TestPackageIdentity(t *testing.T) {
	tests := []struct {
		name     string
		pkg      models.Package
		pkgType  scanner.PackageType
		expected string
	}{
		{
			name: "deb package identity",
			pkg: models.Package{
				Name:         "test-pkg",
				Version:      "1.0.0",
				Architecture: "amd64",
			},
			pkgType:  scanner.TypeDeb,
			expected: "test-pkg:1.0.0:amd64",
		},
		{
			name: "apk package identity",
			pkg: models.Package{
				Name:         "test-pkg",
				Version:      "2.0.0",
				Architecture: "x86_64",
			},
			pkgType:  scanner.TypeApk,
			expected: "test-pkg:2.0.0:x86_64",
		},
		{
			name: "pacman package identity",
			pkg: models.Package{
				Name:         "test-pkg",
				Version:      "3.0.0",
				Architecture: "x86_64",
			},
			pkgType:  scanner.TypePacman,
			expected: "test-pkg:3.0.0:x86_64",
		},
		{
			name: "rpm package identity with release",
			pkg: models.Package{
				Name:         "test-pkg",
				Version:      "1.0.0",
				Architecture: "x86_64",
				Metadata:     map[string]interface{}{"Release": "2"},
			},
			pkgType:  scanner.TypeRpm,
			expected: "test-pkg:1.0.0:2:x86_64",
		},
		{
			name: "rpm package identity without release defaults to 1",
			pkg: models.Package{
				Name:         "test-pkg",
				Version:      "1.0.0",
				Architecture: "x86_64",
			},
			pkgType:  scanner.TypeRpm,
			expected: "test-pkg:1.0.0:1:x86_64",
		},
		{
			name: "homebrew bottle identity",
			pkg: models.Package{
				Name:    "test-bottle",
				Version: "1.2.3",
			},
			pkgType:  scanner.TypeHomebrewBottle,
			expected: "test-bottle:1.2.3",
		},
		{
			name: "sysext package identity",
			pkg: models.Package{
				Name:         "test-ext",
				Version:      "4.0.0",
				Architecture: "x86-64",
			},
			pkgType:  scanner.TypeSysext,
			expected: "test-ext:4.0.0:x86-64",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := PackageIdentity(tt.pkg, tt.pkgType)
			if result != tt.expected {
				t.Errorf("PackageIdentity() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestDetectConflicts(t *testing.T) {
	tests := []struct {
		name          string
		existing      []models.Package
		newPackages   []models.Package
		pkgType       scanner.PackageType
		expectedCount int
	}{
		{
			name: "no conflicts",
			existing: []models.Package{
				{Name: "pkg1", Version: "1.0.0", Architecture: "amd64"},
			},
			newPackages: []models.Package{
				{Name: "pkg2", Version: "1.0.0", Architecture: "amd64"},
			},
			pkgType:       scanner.TypeDeb,
			expectedCount: 0,
		},
		{
			name: "one conflict",
			existing: []models.Package{
				{Name: "pkg1", Version: "1.0.0", Architecture: "amd64"},
			},
			newPackages: []models.Package{
				{Name: "pkg1", Version: "1.0.0", Architecture: "amd64"},
				{Name: "pkg2", Version: "1.0.0", Architecture: "amd64"},
			},
			pkgType:       scanner.TypeDeb,
			expectedCount: 1,
		},
		{
			name: "all conflicts",
			existing: []models.Package{
				{Name: "pkg1", Version: "1.0.0", Architecture: "amd64"},
				{Name: "pkg2", Version: "2.0.0", Architecture: "amd64"},
			},
			newPackages: []models.Package{
				{Name: "pkg1", Version: "1.0.0", Architecture: "amd64"},
				{Name: "pkg2", Version: "2.0.0", Architecture: "amd64"},
			},
			pkgType:       scanner.TypeDeb,
			expectedCount: 2,
		},
		{
			name:     "empty existing",
			existing: []models.Package{},
			newPackages: []models.Package{
				{Name: "pkg1", Version: "1.0.0", Architecture: "amd64"},
			},
			pkgType:       scanner.TypeDeb,
			expectedCount: 0,
		},
		{
			name: "empty new packages",
			existing: []models.Package{
				{Name: "pkg1", Version: "1.0.0", Architecture: "amd64"},
			},
			newPackages:   []models.Package{},
			pkgType:       scanner.TypeDeb,
			expectedCount: 0,
		},
		{
			name: "different version is not a conflict",
			existing: []models.Package{
				{Name: "pkg1", Version: "1.0.0", Architecture: "amd64"},
			},
			newPackages: []models.Package{
				{Name: "pkg1", Version: "2.0.0", Architecture: "amd64"},
			},
			pkgType:       scanner.TypeDeb,
			expectedCount: 0,
		},
		{
			name: "different architecture is not a conflict",
			existing: []models.Package{
				{Name: "pkg1", Version: "1.0.0", Architecture: "amd64"},
			},
			newPackages: []models.Package{
				{Name: "pkg1", Version: "1.0.0", Architecture: "arm64"},
			},
			pkgType:       scanner.TypeDeb,
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conflicts := DetectConflicts(tt.existing, tt.newPackages, tt.pkgType)
			if len(conflicts) != tt.expectedCount {
				t.Errorf("DetectConflicts() returned %d conflicts, want %d", len(conflicts), tt.expectedCount)
			}
		})
	}
}

func TestFilterOutConflicts(t *testing.T) {
	tests := []struct {
		name          string
		packages      []models.Package
		toExclude     []models.Package
		pkgType       scanner.PackageType
		expectedCount int
		expectedNames []string
	}{
		{
			name: "no exclusions",
			packages: []models.Package{
				{Name: "pkg1", Version: "1.0.0", Architecture: "amd64"},
				{Name: "pkg2", Version: "1.0.0", Architecture: "amd64"},
			},
			toExclude:     []models.Package{},
			pkgType:       scanner.TypeDeb,
			expectedCount: 2,
			expectedNames: []string{"pkg1", "pkg2"},
		},
		{
			name: "exclude one package",
			packages: []models.Package{
				{Name: "pkg1", Version: "1.0.0", Architecture: "amd64"},
				{Name: "pkg2", Version: "1.0.0", Architecture: "amd64"},
				{Name: "pkg3", Version: "1.0.0", Architecture: "amd64"},
			},
			toExclude: []models.Package{
				{Name: "pkg2", Version: "1.0.0", Architecture: "amd64"},
			},
			pkgType:       scanner.TypeDeb,
			expectedCount: 2,
			expectedNames: []string{"pkg1", "pkg3"},
		},
		{
			name: "exclude all packages",
			packages: []models.Package{
				{Name: "pkg1", Version: "1.0.0", Architecture: "amd64"},
				{Name: "pkg2", Version: "2.0.0", Architecture: "amd64"},
			},
			toExclude: []models.Package{
				{Name: "pkg1", Version: "1.0.0", Architecture: "amd64"},
				{Name: "pkg2", Version: "2.0.0", Architecture: "amd64"},
			},
			pkgType:       scanner.TypeDeb,
			expectedCount: 0,
			expectedNames: []string{},
		},
		{
			name:          "empty packages list",
			packages:      []models.Package{},
			toExclude:     []models.Package{{Name: "pkg1", Version: "1.0.0", Architecture: "amd64"}},
			pkgType:       scanner.TypeDeb,
			expectedCount: 0,
			expectedNames: []string{},
		},
		{
			name: "exclusion not in packages list",
			packages: []models.Package{
				{Name: "pkg1", Version: "1.0.0", Architecture: "amd64"},
			},
			toExclude: []models.Package{
				{Name: "pkg2", Version: "1.0.0", Architecture: "amd64"},
			},
			pkgType:       scanner.TypeDeb,
			expectedCount: 1,
			expectedNames: []string{"pkg1"},
		},
		{
			name: "sysext package filtering",
			packages: []models.Package{
				{Name: "ext1", Version: "1.0.0", Architecture: "x86-64"},
				{Name: "ext2", Version: "2.0.0", Architecture: "x86-64"},
				{Name: "ext3", Version: "3.0.0", Architecture: "arm64"},
			},
			toExclude: []models.Package{
				{Name: "ext1", Version: "1.0.0", Architecture: "x86-64"},
			},
			pkgType:       scanner.TypeSysext,
			expectedCount: 2,
			expectedNames: []string{"ext2", "ext3"},
		},
		{
			name: "rpm package filtering with release metadata",
			packages: []models.Package{
				{Name: "rpm1", Version: "1.0.0", Architecture: "x86_64", Metadata: map[string]interface{}{"Release": "1"}},
				{Name: "rpm2", Version: "1.0.0", Architecture: "x86_64", Metadata: map[string]interface{}{"Release": "2"}},
			},
			toExclude: []models.Package{
				{Name: "rpm1", Version: "1.0.0", Architecture: "x86_64", Metadata: map[string]interface{}{"Release": "1"}},
			},
			pkgType:       scanner.TypeRpm,
			expectedCount: 1,
			expectedNames: []string{"rpm2"},
		},
		{
			name: "homebrew bottle filtering",
			packages: []models.Package{
				{Name: "bottle1", Version: "1.0.0"},
				{Name: "bottle2", Version: "2.0.0"},
			},
			toExclude: []models.Package{
				{Name: "bottle1", Version: "1.0.0"},
			},
			pkgType:       scanner.TypeHomebrewBottle,
			expectedCount: 1,
			expectedNames: []string{"bottle2"},
		},
		{
			name: "apk package filtering",
			packages: []models.Package{
				{Name: "apk1", Version: "1.0.0", Architecture: "x86_64"},
				{Name: "apk2", Version: "2.0.0", Architecture: "x86_64"},
			},
			toExclude: []models.Package{
				{Name: "apk2", Version: "2.0.0", Architecture: "x86_64"},
			},
			pkgType:       scanner.TypeApk,
			expectedCount: 1,
			expectedNames: []string{"apk1"},
		},
		{
			name: "pacman package filtering",
			packages: []models.Package{
				{Name: "pacman1", Version: "1.0.0", Architecture: "x86_64"},
				{Name: "pacman2", Version: "2.0.0", Architecture: "x86_64"},
			},
			toExclude: []models.Package{
				{Name: "pacman1", Version: "1.0.0", Architecture: "x86_64"},
			},
			pkgType:       scanner.TypePacman,
			expectedCount: 1,
			expectedNames: []string{"pacman2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterOutConflicts(tt.packages, tt.toExclude, tt.pkgType)

			if len(result) != tt.expectedCount {
				t.Errorf("FilterOutConflicts() returned %d packages, want %d", len(result), tt.expectedCount)
			}

			// Verify the expected packages are in the result
			resultNames := make(map[string]bool)
			for _, pkg := range result {
				resultNames[pkg.Name] = true
			}

			for _, expectedName := range tt.expectedNames {
				if !resultNames[expectedName] {
					t.Errorf("FilterOutConflicts() result missing expected package %q", expectedName)
				}
			}
		})
	}
}

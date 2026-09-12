package deb

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/frostyard/repogen/internal/generator"
	"github.com/frostyard/repogen/internal/models"
	"github.com/frostyard/repogen/internal/scanner"
	"github.com/frostyard/repogen/internal/signer"
	"github.com/frostyard/repogen/internal/utils"
	"github.com/sirupsen/logrus"
)

// Generator implements the generator.Generator interface for Debian repositories
type Generator struct {
	signer signer.Signer
	now    func() time.Time
}

// NewGenerator creates a new Debian generator
func NewGenerator(s signer.Signer) generator.Generator {
	return NewGeneratorWithClock(s, time.Now)
}

// NewGeneratorWithClock creates a Debian generator with a controlled
// publication clock.
func NewGeneratorWithClock(s signer.Signer, now func() time.Time) generator.Generator {
	if now == nil {
		now = time.Now
	}
	return &Generator{
		signer: s,
		now:    now,
	}
}

// Generate creates a Debian repository structure
func (g *Generator) Generate(ctx context.Context, config *models.RepositoryConfig, packages []models.Package) error {
	logrus.Info("Generating Debian repository...")
	publishedAt := g.now().UTC()

	// Group packages by architecture
	archPackages := make(map[string][]models.Package)
	for _, pkg := range packages {
		arch := pkg.Architecture
		if arch == "" {
			arch = "amd64"
		}
		archPackages[arch] = append(archPackages[arch], pkg)
	}

	// Generate repository for each architecture
	arches := append([]string(nil), config.Arches...)
	sort.Strings(arches)
	selectedPackages := make([]models.Package, 0, len(packages))
	for _, arch := range arches {
		selectedPackages = append(selectedPackages, archPackages[arch]...)
	}
	if err := validatePoolDestinations(config.OutputDir, selectedPackages); err != nil {
		return fmt.Errorf("failed to validate pool destinations: %w", err)
	}
	for _, arch := range arches {
		if err := g.generateForArch(ctx, config, arch, archPackages[arch]); err != nil {
			return fmt.Errorf("failed to generate for %s: %w", arch, err)
		}
	}

	// Generate Release file at repository root
	if err := g.generateRelease(config, publishedAt); err != nil {
		return fmt.Errorf("failed to generate Release: %w", err)
	}

	logrus.Info("Debian repository generated successfully")
	return nil
}

type poolContentIdentity struct {
	size   int64
	sha256 string
}

func validatePoolDestinations(outputDir string, packages []models.Package) error {
	seen := make(map[string]poolContentIdentity, len(packages))
	for index := range packages {
		pkg := &packages[index]
		dstPath, err := poolDestination(outputDir, pkg)
		if err != nil {
			return err
		}

		srcPath, _, _, err := utils.ShouldCopyPackage(pkg, dstPath, outputDir)
		if err != nil {
			return fmt.Errorf("package copy check failed for %s: %w", pkg.Name, err)
		}

		identity := poolContentIdentity{size: pkg.Size, sha256: pkg.SHA256Sum}
		if _, err := os.Stat(srcPath); err == nil {
			checksums, err := utils.CalculateChecksums(srcPath)
			if err != nil {
				return fmt.Errorf("failed to calculate checksums for %s: %w", filepath.Base(pkg.Filename), err)
			}
			identity = poolContentIdentity{size: checksums.Size, sha256: checksums.SHA256}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("cannot stat package source %s: %w", srcPath, err)
		}

		previous, duplicate := seen[dstPath]
		if !duplicate {
			seen[dstPath] = identity
			continue
		}
		if identity.sha256 == "" || previous.sha256 == "" {
			return fmt.Errorf("cannot verify duplicate pool destination %q without SHA256", dstPath)
		}
		if identity != previous {
			return fmt.Errorf("conflicting package contents for pool destination %q", dstPath)
		}
	}
	return nil
}

func poolDestination(outputDir string, pkg *models.Package) (string, error) {
	if pkg.Name == "" {
		return "", fmt.Errorf("package name cannot be empty")
	}

	firstLetter := string(pkg.Name[0])
	if firstLetter < "a" || firstLetter > "z" {
		firstLetter = "0"
	}

	return filepath.Join(
		outputDir,
		"pool",
		"main",
		firstLetter,
		pkg.Name,
		filepath.Base(pkg.Filename),
	), nil
}

// generateForArch generates repository files for a specific architecture
func (g *Generator) generateForArch(ctx context.Context, config *models.RepositoryConfig, arch string, packages []models.Package) error {
	logrus.Infof("Generating for architecture: %s", arch)

	// Create directory structure
	// dists/{codename}/main/binary-{arch}/
	distsDir := filepath.Join(config.OutputDir, "dists", config.Codename, "main", fmt.Sprintf("binary-%s", arch))
	poolDir := filepath.Join(config.OutputDir, "pool", "main")

	if err := utils.EnsureDir(distsDir); err != nil {
		return err
	}
	if err := utils.EnsureDir(poolDir); err != nil {
		return err
	}

	// Copy packages to pool and update filenames
	for i := range packages {
		pkg := &packages[i]

		dstPath, err := poolDestination(config.OutputDir, pkg)
		if err != nil {
			return err
		}
		pkgDir := filepath.Dir(dstPath)
		if err := utils.EnsureDir(pkgDir); err != nil {
			return err
		}

		// Check if package needs to be copied
		srcPath, finalDstPath, needsCopy, err := utils.ShouldCopyPackage(pkg, dstPath, config.OutputDir)
		if err != nil {
			return fmt.Errorf("package copy check failed for %s: %w", pkg.Name, err)
		}

		if needsCopy {
			logrus.Debugf("Copying package: %s -> %s", srcPath, finalDstPath)

			// Copy package file
			if err := utils.CopyFile(srcPath, finalDstPath); err != nil {
				return fmt.Errorf("failed to copy %s: %w", srcPath, err)
			}

			// Recalculate checksums on the copied file to ensure accuracy
			checksums, err := utils.CalculateChecksums(finalDstPath)
			if err != nil {
				return fmt.Errorf("failed to calculate checksums for %s: %w", filepath.Base(pkg.Filename), err)
			}
			pkg.Size = checksums.Size
			pkg.MD5Sum = checksums.MD5
			pkg.SHA1Sum = checksums.SHA1
			pkg.SHA256Sum = checksums.SHA256
		} else {
			logrus.Debugf("Skipping copy for package: %s", pkg.Name)
		}

		// Update filename to be relative to repository root
		relPath, err := filepath.Rel(config.OutputDir, finalDstPath)
		if err != nil {
			return err
		}
		pkg.Filename = relPath
	}

	// Generate Packages file
	packagesData, err := GeneratePackagesFile(packages)
	if err != nil {
		return fmt.Errorf("failed to generate Packages file: %w", err)
	}

	packagesPath := filepath.Join(distsDir, "Packages")
	if err := utils.WriteFile(packagesPath, packagesData, 0644); err != nil {
		return fmt.Errorf("failed to write Packages: %w", err)
	}

	// Compress Packages file
	packagesGz, err := utils.GzipCompress(packagesData)
	if err != nil {
		return fmt.Errorf("failed to compress Packages: %w", err)
	}

	packagesGzPath := filepath.Join(distsDir, "Packages.gz")
	if err := utils.WriteFile(packagesGzPath, packagesGz, 0644); err != nil {
		return fmt.Errorf("failed to write Packages.gz: %w", err)
	}

	logrus.Infof("Generated Packages files for %s (%d packages)", arch, len(packages))
	return nil
}

// generateRelease generates the Release, InRelease, and Release.gpg files
func (g *Generator) generateRelease(config *models.RepositoryConfig, publishedAt time.Time) error {
	logrus.Info("Generating Release file...")

	distsDir := filepath.Join(config.OutputDir, "dists", config.Codename)

	// Find all Packages files
	var metadataFiles []string
	for _, arch := range config.Arches {
		for _, comp := range config.Components {
			binDir := fmt.Sprintf("%s/binary-%s", comp, arch)

			// Add Packages
			packagesPath := filepath.Join(binDir, "Packages")
			metadataFiles = append(metadataFiles, packagesPath)

			// Add Packages.gz
			packagesGzPath := filepath.Join(binDir, "Packages.gz")
			metadataFiles = append(metadataFiles, packagesGzPath)
		}
	}

	// Calculate checksums for metadata files
	fileInfos, err := CalculateReleaseFileInfos(distsDir, metadataFiles)
	if err != nil {
		return err
	}

	// Generate Release file
	releasePath := filepath.Join(distsDir, "Release")
	if existingRelease, err := os.ReadFile(releasePath); err == nil {
		if previousTime, ok := releaseDate(existingRelease); ok {
			unchangedRelease, err := GenerateReleaseFileAt(config, fileInfos, previousTime)
			if err != nil {
				return fmt.Errorf("failed to generate Release file: %w", err)
			}
			if bytes.Equal(existingRelease, unchangedRelease) && g.hasReusableSignatures(distsDir, unchangedRelease) {
				logrus.Info("Debian metadata unchanged; preserving existing Release signatures")
				return nil
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to read existing Release: %w", err)
	}

	releaseData, err := GenerateReleaseFileAt(config, fileInfos, publishedAt)
	if err != nil {
		return fmt.Errorf("failed to generate Release file: %w", err)
	}

	if err := utils.WriteFile(releasePath, releaseData, 0644); err != nil {
		return fmt.Errorf("failed to write Release: %w", err)
	}

	// Sign if signer is available
	if g.signer != nil {
		// Create InRelease (cleartext signed)
		inReleaseData, err := g.signer.SignCleartext(releaseData)
		if err != nil {
			return fmt.Errorf("failed to sign InRelease: %w", err)
		}

		inReleasePath := filepath.Join(distsDir, "InRelease")
		if err := utils.WriteFile(inReleasePath, inReleaseData, 0644); err != nil {
			return fmt.Errorf("failed to write InRelease: %w", err)
		}

		// Create Release.gpg (detached signature)
		releaseGpg, err := g.signer.SignDetached(releaseData)
		if err != nil {
			return fmt.Errorf("failed to create Release.gpg: %w", err)
		}

		releaseGpgPath := filepath.Join(distsDir, "Release.gpg")
		if err := utils.WriteFile(releaseGpgPath, releaseGpg, 0644); err != nil {
			return fmt.Errorf("failed to write Release.gpg: %w", err)
		}

		logrus.Info("Release file signed successfully")
	} else {
		// For unsigned repositories, create InRelease with Release content
		// This allows modern apt (especially Debian Trixie) to work with [trusted=yes]
		inReleasePath := filepath.Join(distsDir, "InRelease")
		if err := utils.WriteFile(inReleasePath, releaseData, 0644); err != nil {
			return fmt.Errorf("failed to write InRelease: %w", err)
		}

		logrus.Warn("No signer configured, repository will be unsigned")
		logrus.Info("Generated InRelease file for compatibility with modern apt")
	}

	return nil
}

func releaseDate(release []byte) (time.Time, bool) {
	const prefix = "Date: "
	var value string
	for _, line := range strings.Split(string(release), "\n") {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		if value != "" {
			return time.Time{}, false
		}
		value = strings.TrimPrefix(line, prefix)
	}
	if value == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC1123Z, value)
	return parsed, err == nil
}

func (g *Generator) hasReusableSignatures(distsDir string, release []byte) bool {
	inRelease, err := os.ReadFile(filepath.Join(distsDir, "InRelease"))
	if err != nil {
		return false
	}
	if g.signer == nil {
		return bytes.Equal(inRelease, release)
	}
	if len(inRelease) == 0 {
		return false
	}
	releaseGPG, err := os.ReadFile(filepath.Join(distsDir, "Release.gpg"))
	if err != nil || len(releaseGPG) == 0 {
		return false
	}
	publicKey, err := g.signer.GetPublicKey()
	if err != nil {
		return false
	}
	keyring, err := parseProductionKeyRing(publicKey)
	if err != nil {
		return false
	}
	_, err = verifyProductionReleaseSignatures(keyring, release, inRelease, releaseGPG)
	return err == nil
}

// ValidatePackages checks if packages are valid Debian packages
func (g *Generator) ValidatePackages(packages []models.Package) error {
	for _, pkg := range packages {
		if pkg.Name == "" {
			return fmt.Errorf("package missing name: %s", pkg.Filename)
		}
		if pkg.Version == "" {
			return fmt.Errorf("package %s missing version", pkg.Name)
		}
		if pkg.Architecture == "" {
			return fmt.Errorf("package %s missing architecture", pkg.Name)
		}
		if !strings.HasSuffix(pkg.Filename, ".deb") {
			return fmt.Errorf("package %s is not a .deb file", pkg.Name)
		}
	}
	return nil
}

// GetSupportedType returns the package type this generator supports
func (g *Generator) GetSupportedType() scanner.PackageType {
	return scanner.TypeDeb
}

package homebrew

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/frostyard/repogen/internal/generator"
	"github.com/frostyard/repogen/internal/models"
	"github.com/frostyard/repogen/internal/scanner"
	"github.com/frostyard/repogen/internal/utils"
	"github.com/sirupsen/logrus"
)

// platformRe extracts the platform from a Homebrew bottle filename.
var platformRe = regexp.MustCompile(`--(.*)\.(bottle\.tar\.gz|bottle\.tar)`)

// Generator implements the generator.Generator interface for Homebrew taps
type Generator struct {
	baseURL string
}

// NewGenerator creates a new Homebrew generator
func NewGenerator(baseURL string) generator.Generator {
	return &Generator{
		baseURL: baseURL,
	}
}

// Generate creates a Homebrew tap structure
func (g *Generator) Generate(ctx context.Context, config *models.RepositoryConfig, packages []models.Package) error {
	logrus.Info("Generating Homebrew tap...")

	// Create directory structure
	formulaDir := filepath.Join(config.OutputDir, "Formula")
	bottlesDir := filepath.Join(config.OutputDir, "bottles")

	if err := utils.EnsureDir(formulaDir); err != nil {
		return err
	}
	if err := utils.EnsureDir(bottlesDir); err != nil {
		return err
	}

	// Group bottles by the package name that the filename encodes. A filename
	// that does not parse stops the run: the name and the version are needed
	// and must not be guessed.
	bottlesByPkg, err := bottlesByPackageName(packages)
	if err != nil {
		return err
	}

	// Generate formulas
	for pkgName, bottles := range bottlesByPkg {
		// Copy bottles and recalculate checksums
		updatedBottles := make([]models.Package, len(bottles))
		for i, bottle := range bottles {
			dstPath := filepath.Join(bottlesDir, filepath.Base(bottle.Filename))

			// Check if package needs to be copied
			srcPath, finalDstPath, needsCopy, err := utils.ShouldCopyPackage(&bottle, dstPath, config.OutputDir)
			if err != nil {
				return fmt.Errorf("package copy check failed for %s: %w", filepath.Base(bottle.Filename), err)
			}

			updatedBottle := bottle
			if needsCopy {
				logrus.Debugf("Copying bottle: %s -> %s", srcPath, finalDstPath)

				if err := utils.CopyFile(srcPath, finalDstPath); err != nil {
					return fmt.Errorf("failed to copy %s: %w", srcPath, err)
				}

				// Recalculate checksums on the copied file to ensure accuracy
				checksums, err := utils.CalculateChecksums(finalDstPath)
				if err != nil {
					return fmt.Errorf("failed to calculate checksums for %s: %w", filepath.Base(bottle.Filename), err)
				}

				updatedBottle.Size = checksums.Size
				updatedBottle.MD5Sum = checksums.MD5
				updatedBottle.SHA1Sum = checksums.SHA1
				updatedBottle.SHA256Sum = checksums.SHA256
			} else {
				logrus.Debugf("Skipping copy for bottle: %s", filepath.Base(bottle.Filename))
			}
			updatedBottles[i] = updatedBottle
		}

		// Generate formula using updated bottles with correct checksums
		formula, err := g.generateFormula(pkgName, updatedBottles)
		if err != nil {
			return fmt.Errorf("failed to generate formula for %s: %w", pkgName, err)
		}

		className := toClassName(pkgName)
		formulaPath := filepath.Join(formulaDir, fmt.Sprintf("%s.rb", pkgName))
		if err := utils.WriteFile(formulaPath, []byte(formula), 0644); err != nil {
			return fmt.Errorf("failed to write formula: %w", err)
		}

		logrus.Infof("Generated formula for %s (%s.rb)", pkgName, className)
	}

	logrus.Infof("Homebrew tap generated successfully (%d formulas)", len(bottlesByPkg))
	return nil
}

// generateFormula creates a Ruby formula file
func (g *Generator) generateFormula(name string, bottles []models.Package) (string, error) {
	className := toClassName(name)

	// The bottle filename holds the version. Package metadata is no source for
	// it: in incremental mode the version comes from an old formula file, which
	// can hold a wrong value.
	version, err := bottleVersion(name, bottles)
	if err != nil {
		return "", err
	}

	// Get description
	desc := fmt.Sprintf("%s package", name)
	if len(bottles) > 0 && bottles[0].Description != "" {
		desc = bottles[0].Description
	}

	// Get homepage
	homepage := "https://example.com"
	if len(bottles) > 0 && bottles[0].Homepage != "" {
		homepage = bottles[0].Homepage
	}

	// Build formula
	var formula strings.Builder

	fmt.Fprintf(&formula, "class %s < Formula\n", className)
	fmt.Fprintf(&formula, "  desc \"%s\"\n", desc)
	fmt.Fprintf(&formula, "  homepage \"%s\"\n", homepage)
	fmt.Fprintf(&formula, "  version \"%s\"\n", version)
	formula.WriteString("\n")

	// Group bottles by platform
	macosBottles := []models.Package{}
	linuxBottles := []models.Package{}

	for _, bottle := range bottles {
		platform := extractPlatform(bottle.Filename)
		if strings.Contains(platform, "linux") {
			linuxBottles = append(linuxBottles, bottle)
		} else {
			macosBottles = append(macosBottles, bottle)
		}
	}

	// macOS bottles
	if len(macosBottles) > 0 {
		formula.WriteString("  on_macos do\n")

		armBottle := findBottleForArch(macosBottles, "arm64")
		x86Bottle := findBottleForArch(macosBottles, "x86_64")

		if armBottle != nil {
			formula.WriteString("    if Hardware::CPU.arm?\n")
			url := g.getBottleURL(armBottle.Filename)
			fmt.Fprintf(&formula, "      url \"%s\"\n", url)
			fmt.Fprintf(&formula, "      sha256 \"%s\"\n", armBottle.SHA256Sum)
			formula.WriteString("    end\n")
		}

		if x86Bottle != nil {
			if armBottle != nil {
				formula.WriteString("    if Hardware::CPU.intel?\n")
			}
			url := g.getBottleURL(x86Bottle.Filename)
			fmt.Fprintf(&formula, "      url \"%s\"\n", url)
			fmt.Fprintf(&formula, "      sha256 \"%s\"\n", x86Bottle.SHA256Sum)
			if armBottle != nil {
				formula.WriteString("    end\n")
			}
		}

		formula.WriteString("  end\n")
	}

	// Linux bottles
	if len(linuxBottles) > 0 {
		formula.WriteString("\n  on_linux do\n")
		bottle := linuxBottles[0]
		url := g.getBottleURL(bottle.Filename)
		fmt.Fprintf(&formula, "    url \"%s\"\n", url)
		fmt.Fprintf(&formula, "    sha256 \"%s\"\n", bottle.SHA256Sum)
		formula.WriteString("  end\n")
	}

	formula.WriteString("end\n")

	return formula.String(), nil
}

// getBottleURL constructs the URL for a bottle
func (g *Generator) getBottleURL(filename string) string {
	if g.baseURL != "" {
		return fmt.Sprintf("%s/bottles/%s", strings.TrimRight(g.baseURL, "/"), filepath.Base(filename))
	}
	return fmt.Sprintf("bottles/%s", filepath.Base(filename))
}

// bottlesByPackageName groups bottles by the package name that each filename
// encodes. A filename that the pattern does not match gives an error: the name
// must not be guessed.
func bottlesByPackageName(packages []models.Package) (map[string][]models.Package, error) {
	byName := make(map[string][]models.Package, len(packages))
	for _, pkg := range packages {
		parsed, err := parseBottleFilename(pkg.Filename)
		if err != nil {
			return nil, err
		}
		byName[parsed.Name] = append(byName[parsed.Name], pkg)
	}
	return byName, nil
}

// bottleVersion returns the single version that the bottle filenames of one
// package encode. Filenames that do not parse, and filenames with different
// versions, give an error.
func bottleVersion(name string, bottles []models.Package) (string, error) {
	if len(bottles) == 0 {
		return "", fmt.Errorf("no bottles for package %s", name)
	}

	seen := make(map[string]struct{}, len(bottles))
	for _, bottle := range bottles {
		parsed, err := parseBottleFilename(bottle.Filename)
		if err != nil {
			return "", err
		}
		seen[parsed.Version] = struct{}{}
	}

	versions := make([]string, 0, len(seen))
	for version := range seen {
		versions = append(versions, version)
	}
	slices.Sort(versions)

	if len(versions) > 1 {
		return "", fmt.Errorf("bottles for %s have different versions: %s",
			name, strings.Join(versions, ", "))
	}

	return versions[0], nil
}

// extractPlatform extracts platform from bottle filename
func extractPlatform(filename string) string {
	base := filepath.Base(filename)
	matches := platformRe.FindStringSubmatch(base)
	if len(matches) > 1 {
		return matches[1]
	}
	return "unknown"
}

// findBottleForArch finds a bottle for a specific architecture
func findBottleForArch(bottles []models.Package, arch string) *models.Package {
	for _, bottle := range bottles {
		if strings.Contains(bottle.Filename, arch) {
			return &bottle
		}
	}
	return nil
}

// toClassName converts a package name to a Ruby class name
func toClassName(name string) string {
	// Replace hyphens and underscores with spaces
	name = strings.ReplaceAll(name, "-", " ")
	name = strings.ReplaceAll(name, "_", " ")

	// Title case each word
	words := strings.Fields(name)
	for i, word := range words {
		if len(word) > 0 {
			words[i] = strings.ToUpper(word[:1]) + strings.ToLower(word[1:])
		}
	}

	// Join without spaces
	return strings.Join(words, "")
}

// ValidatePackages checks if packages are valid Homebrew bottles. Each
// filename must encode a name, a version and a platform, and all bottles of
// one formula must carry the same version.
func (g *Generator) ValidatePackages(packages []models.Package) error {
	for _, pkg := range packages {
		if !strings.Contains(pkg.Filename, bottleSuffix) {
			return fmt.Errorf("package %s is not a Homebrew bottle", pkg.Filename)
		}
	}

	byName, err := bottlesByPackageName(packages)
	if err != nil {
		return err
	}

	// Sort the names so that a run reports the same error every time.
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	slices.Sort(names)

	for _, name := range names {
		if _, err := bottleVersion(name, byName[name]); err != nil {
			return err
		}
	}

	return nil
}

// GetSupportedType returns the package type this generator supports
func (g *Generator) GetSupportedType() scanner.PackageType {
	return scanner.TypeHomebrewBottle
}

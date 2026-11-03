package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/frostyard/repogen/internal/dirindex"
	"github.com/frostyard/repogen/internal/generator"
	"github.com/frostyard/repogen/internal/generator/apk"
	"github.com/frostyard/repogen/internal/generator/deb"
	"github.com/frostyard/repogen/internal/generator/homebrew"
	"github.com/frostyard/repogen/internal/generator/pacman"
	"github.com/frostyard/repogen/internal/generator/sysext"
	"github.com/frostyard/repogen/internal/models"
	"github.com/frostyard/repogen/internal/scanner"
	"github.com/frostyard/repogen/internal/signer"
	"github.com/frostyard/repogen/internal/utils"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// NewGenerateCmd creates the generate command
func NewGenerateCmd() *cobra.Command {
	var config models.RepositoryConfig

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate repository structure",
		Long: `Scans input directory for packages and generates repository
structures with appropriate metadata files and signatures.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate configuration
			if err := validateConfig(&config); err != nil {
				return err
			}

			logrus.Info("Starting repository generation...")
			logrus.Debugf("Configuration: %+v", config)

			// Run generation
			return runGeneration(cmd.Context(), &config)
		},
	}

	// Input/Output flags
	cmd.Flags().StringVarP(&config.InputDir, "input-dir", "i", ".", "Input directory to scan")
	cmd.Flags().StringVarP(&config.OutputDir, "output-dir", "o", "./repo", "Output directory")

	// GPG signing flags (for Debian, Pacman and sysext)
	cmd.Flags().StringVarP(&config.GPGKeyPath, "gpg-key", "k", "", "Path to GPG private key")
	cmd.Flags().StringVarP(&config.GPGPassphrase, "gpg-passphrase", "p", "", "GPG key passphrase")

	// RSA signing flags (for Alpine)
	cmd.Flags().StringVar(&config.RSAKeyPath, "rsa-key", "", "Path to RSA private key (for Alpine)")
	cmd.Flags().StringVar(&config.RSAPassphrase, "rsa-passphrase", "", "RSA key passphrase")
	cmd.Flags().StringVar(&config.RSAKeyName, "key-name", "repogen", "Key name for Alpine signatures")

	// Repository metadata flags
	cmd.Flags().StringVar(&config.Origin, "origin", "", "Repository origin name")
	cmd.Flags().StringVar(&config.Label, "label", "", "Repository label")
	cmd.Flags().StringVar(&config.RepoName, "repo-name", "", "Repository name for Pacman database files")
	cmd.Flags().StringVar(&config.Codename, "codename", "", "Codename for Debian repos (required when Debian packages are present)")
	cmd.Flags().StringVar(&config.Suite, "suite", "", "Suite for Debian repos (defaults to codename)")
	cmd.Flags().StringSliceVar(&config.Components, "components", []string{"main"}, "Components for Debian repos")
	cmd.Flags().StringSliceVar(&config.Arches, "arch", []string{"amd64"}, "Architectures to support")

	// Type-specific options
	cmd.Flags().StringVar(&config.BaseURL, "base-url", "", "Base URL for Homebrew bottles and sysext transfer files (required for sysext)")

	// Incremental mode
	cmd.Flags().BoolVar(&config.Incremental, "incremental", false, "Add new packages to existing repository without removing existing ones")
	cmd.Flags().BoolVar(&config.SkipDuplicates, "skip-duplicates", false, "In incremental mode, skip packages that already exist instead of failing")
	cmd.Flags().BoolVar(&config.SkipPublishedIdentities, "skip-published-identities", false, "In incremental mode, skip each sysext whose identity (name, version, OSVersion, arch) is already listed in the signature-verified SHA256SUMS, whatever its bytes or compression suffix (sysext only)")
	cmd.Flags().StringVar(&config.SkippedReportPath, "skipped-report", "", "With --skip-published-identities, write the published payload path (ext/NAME/FILE) of each skipped sysext to this file, one per line")

	// Output options
	cmd.Flags().BoolVar(&config.HTMLIndex, "html-index", false, "Generate HTML directory index pages for browser-friendly navigation")

	return cmd
}

func validateConfig(config *models.RepositoryConfig) error {
	if config.InputDir == "" {
		return &models.RepoGenError{
			Type: models.ErrInvalidConfig,
			Err:  fmt.Errorf("input-dir is required"),
		}
	}

	if config.OutputDir == "" {
		return &models.RepoGenError{
			Type: models.ErrInvalidConfig,
			Err:  fmt.Errorf("output-dir is required"),
		}
	}

	if config.SkipPublishedIdentities && !config.Incremental {
		return &models.RepoGenError{
			Type: models.ErrInvalidConfig,
			Err:  fmt.Errorf("--skip-published-identities requires --incremental"),
		}
	}

	// Published identities are read only from signature-verified manifests, so
	// the mode cannot work without a key to verify them.
	if config.SkipPublishedIdentities && config.GPGKeyPath == "" {
		return &models.RepoGenError{
			Type: models.ErrInvalidConfig,
			Err:  fmt.Errorf("--skip-published-identities requires --gpg-key: published identities are read only from signature-verified SHA256SUMS"),
		}
	}

	if config.SkippedReportPath != "" && !config.SkipPublishedIdentities {
		return &models.RepoGenError{
			Type: models.ErrInvalidConfig,
			Err:  fmt.Errorf("--skipped-report requires --skip-published-identities"),
		}
	}

	// Debian output has no implicit codename: a missing --codename must not
	// silently default to a distribution such as "stable".
	hasDeb, err := hasDebianPackages(config.InputDir)
	if err != nil {
		return &models.RepoGenError{
			Type: models.ErrFileOp,
			Err:  fmt.Errorf("failed to scan input directory: %w", err),
		}
	}
	if hasDeb {
		config.Codename = strings.TrimSpace(config.Codename)
		config.Suite = strings.TrimSpace(config.Suite)
		if config.Codename == "" {
			return &models.RepoGenError{
				Type: models.ErrInvalidConfig,
				Err:  fmt.Errorf("--codename is required for Debian repositories"),
			}
		}
		if config.Suite != "" && config.Suite != config.Codename {
			return &models.RepoGenError{
				Type: models.ErrInvalidConfig,
				Err:  fmt.Errorf("--suite must equal --codename for Debian repositories (got suite %q, codename %q)", config.Suite, config.Codename),
			}
		}
	}

	// Set Suite to Codename if not specified
	if config.Suite == "" {
		config.Suite = config.Codename
	}

	// Set default origin/label if not specified
	if config.Origin == "" {
		config.Origin = "Repogen Repository"
	}
	if config.Label == "" {
		config.Label = config.Origin
	}

	// Validate repo-name requirement for Pacman repositories
	if hasPacmanPackages(config.InputDir) && config.RepoName == "" {
		return &models.RepoGenError{
			Type: models.ErrInvalidConfig,
			Err:  fmt.Errorf("--repo-name is required for Pacman (Arch Linux) repository generation"),
		}
	}

	return nil
}

func runGeneration(ctx context.Context, config *models.RepositoryConfig) error {
	// Step 1: Scan for packages
	logrus.Infof("Scanning directory: %s", config.InputDir)
	sc := scanner.NewFileSystemScanner()
	scannedPackages, err := sc.Scan(ctx, config.InputDir)
	if err != nil {
		return &models.RepoGenError{
			Type: models.ErrFileOp,
			Err:  fmt.Errorf("failed to scan directory: %w", err),
		}
	}

	// Scan logs and drops a file whose type detection fails (a zero-byte .raw
	// gives EOF), which would make the identity skip silently miss a rebuilt
	// extension. Checked before the empty-input return, because such a file is
	// the only input here.
	if config.SkipPublishedIdentities {
		if err := validateSysextCandidates(config.InputDir, scannedPackages); err != nil {
			return &models.RepoGenError{
				Type: models.ErrInvalidConfig,
				Err:  err,
			}
		}
	}

	if len(scannedPackages) == 0 {
		logrus.Warn("No packages found in input directory")
		return nil
	}

	logrus.Infof("Found %d packages", len(scannedPackages))

	// --skip-published-identities reads and merges sysext manifests only, so a
	// run that finds any other package type is refused before any parsing.
	if config.SkipPublishedIdentities {
		for _, scanned := range scannedPackages {
			if scanned.Type != scanner.TypeSysext {
				return &models.RepoGenError{
					Type: models.ErrInvalidConfig,
					Err: fmt.Errorf("--skip-published-identities supports only sysext packages (found %s: %s)",
						scanned.Type, scanned.Path),
				}
			}
		}
	}

	// Step 2: Parse packages by type
	packagesByType := make(map[scanner.PackageType][]models.Package)

	for _, scanned := range scannedPackages {
		var pkg *models.Package
		var parseErr error

		logrus.Debugf("Parsing %s package: %s", scanned.Type, scanned.Path)

		switch scanned.Type {
		case scanner.TypeDeb:
			pkg, parseErr = deb.ParsePackage(scanned.Path)
		case scanner.TypeApk:
			pkg, parseErr = apk.ParsePackage(scanned.Path)
		case scanner.TypePacman:
			pkg, parseErr = pacman.ParsePackage(scanned.Path)
		case scanner.TypeHomebrewBottle:
			// Homebrew bottles don't need parsing, use basic info
			pkg = &models.Package{
				Filename: scanned.Path,
				Size:     scanned.Size,
			}
			// Calculate checksums
			checksums, csErr := utils.CalculateChecksums(scanned.Path)
			if csErr == nil {
				pkg.SHA256Sum = checksums.SHA256
			}
		case scanner.TypeSysext:
			pkg, parseErr = sysext.ParsePackage(scanned.Path)
		default:
			logrus.Warnf("Unknown package type: %s", scanned.Type)
			continue
		}

		if parseErr != nil {
			// With --skip-published-identities an unparsable sysext must not
			// vanish into a "nothing to publish" success: fail closed.
			if config.SkipPublishedIdentities {
				return &models.RepoGenError{
					Type: models.ErrPackageParse,
					Err:  fmt.Errorf("--skip-published-identities: failed to parse %s: %w", scanned.Path, parseErr),
				}
			}
			logrus.Warnf("Failed to parse %s: %v", scanned.Path, parseErr)
			continue
		}

		packagesByType[scanned.Type] = append(packagesByType[scanned.Type], *pkg)
	}

	// Step 3: Initialize signers
	var gpgSigner signer.Signer
	var rsaSigner signer.RSASigner

	if config.GPGKeyPath != "" {
		gpgSigner, err = signer.NewGPGSigner(config.GPGKeyPath, config.GPGPassphrase)
		if err != nil {
			return &models.RepoGenError{
				Type: models.ErrSigning,
				Err:  fmt.Errorf("failed to initialize GPG signer: %w", err),
			}
		}
		if closer, ok := gpgSigner.(io.Closer); ok {
			defer func() { _ = closer.Close() }()
		}
		logrus.Info("GPG signer initialized")
	}

	if config.RSAKeyPath != "" {
		rsaSigner, err = signer.NewAlpineRSASigner(config.RSAKeyPath, config.RSAPassphrase)
		if err != nil {
			return &models.RepoGenError{
				Type: models.ErrSigning,
				Err:  fmt.Errorf("failed to initialize RSA signer: %w", err),
			}
		}
		logrus.Info("RSA signer initialized")
	}

	// Step 4: Generate repositories for each type
	generators := make(map[scanner.PackageType]generator.Generator)
	generators[scanner.TypeDeb] = deb.NewGenerator(gpgSigner)
	generators[scanner.TypeApk] = apk.NewGenerator(rsaSigner, config.RSAKeyName)
	generators[scanner.TypePacman] = pacman.NewGenerator(gpgSigner)
	generators[scanner.TypeHomebrewBottle] = homebrew.NewGenerator(config.BaseURL)
	generators[scanner.TypeSysext] = sysext.NewGenerator(config.BaseURL, gpgSigner)

	// Phase 1: restore incremental state, detect conflicts and validate for
	// every format before any format writes output, so a failure in one format
	// cannot leave another format's output half-updated.
	plannedPackages := make(map[scanner.PackageType][]models.Package)
	var skippedReportLines []string
	for pkgType, newPackages := range packagesByType {
		gen, ok := generators[pkgType]
		if !ok {
			logrus.Warnf("No generator for package type: %s", pkgType)
			continue
		}

		var finalPackages []models.Package

		if config.SkipPublishedIdentities && pkgType == scanner.TypeSysext {
			// Skip by identity from the signature-verified manifests, before the
			// conflict checks below: a rebuild with other bytes is intended here.
			publishedGenerator, ok := gen.(interface {
				PublishedPackages(*models.RepositoryConfig) ([]models.Package, error)
			})
			if !ok {
				return &models.RepoGenError{
					Type: models.ErrInvalidConfig,
					Err:  fmt.Errorf("--skip-published-identities: %s generator cannot read published identities", pkgType),
				}
			}

			publishedPackages, err := publishedGenerator.PublishedPackages(config)
			if err != nil {
				return &models.RepoGenError{
					Type: models.ErrInvalidConfig,
					Err:  fmt.Errorf("skip-published-identities: read signature-verified sysext metadata: %w", err),
				}
			}

			incomingCount := len(newPackages)
			keep, skipped := utils.SplitPublishedIdentities(publishedPackages, newPackages, pkgType)
			for _, entry := range skipped {
				logrus.Infof("Skipping %s: identity %s is already published as %s",
					filepath.Base(entry.Package.Filename),
					utils.PackageIdentity(entry.Package, pkgType),
					filepath.Base(entry.Published.Filename))
				skippedReportLines = append(skippedReportLines, path.Join(
					"ext",
					entry.Published.Name,
					filepath.Base(entry.Published.Filename),
				))
			}
			newPackages = keep
			if len(newPackages) == 0 {
				logrus.Infof("All %d sysext package(s) are already published; nothing to publish", incomingCount)
				continue
			}
		}

		if config.Incremental {
			logrus.Infof("Incremental mode: parsing existing %s metadata...", pkgType)

			// Parse existing packages from metadata
			existingPackages, err := gen.ParseExistingMetadata(config)
			if err != nil && pkgType == scanner.TypeDeb && !errors.Is(err, generator.ErrNoExistingMetadata) {
				// An index that exists but cannot be read must stop the run:
				// regenerating from new packages alone would drop published ones.
				return &models.RepoGenError{
					Type: models.ErrPackageParse,
					Err:  fmt.Errorf("incremental deb restore: %w", err),
				}
			}
			if err != nil {
				logrus.Warnf("Could not parse existing metadata for %s: %v. Falling back to normal mode.", pkgType, err)
				finalPackages = newPackages
			} else {
				logrus.Infof("Found %d existing %s packages", len(existingPackages), pkgType)

				// Detect conflicts
				conflicts := utils.DetectConflicts(existingPackages, newPackages, pkgType)
				if pkgType == scanner.TypeSysext {
					digestConflicts := utils.DetectDigestConflicts(existingPackages, newPackages, pkgType)
					if len(digestConflicts) > 0 {
						var conflictNames []string
						for _, pkg := range digestConflicts {
							conflictNames = append(conflictNames, filepath.Base(pkg.Filename))
						}
						return &models.RepoGenError{
							Type: models.ErrInvalidConfig,
							Err: fmt.Errorf(
								"incremental sysext reconciliation: changed or unverifiable bytes for existing logical identity: %s",
								strings.Join(conflictNames, ", "),
							),
						}
					}
				}
				if len(conflicts) > 0 {
					var conflictNames []string
					for _, pkg := range conflicts {
						conflictNames = append(conflictNames, fmt.Sprintf("%s-%s-%s", pkg.Name, pkg.Version, pkg.Architecture))
					}

					if config.SkipDuplicates {
						// Filter out duplicates and continue
						newPackages = utils.FilterOutConflicts(newPackages, conflicts, pkgType)
						logrus.Warnf("Skipping %d duplicate package(s): %s",
							len(conflicts), strings.Join(conflictNames, ", "))
					} else {
						return &models.RepoGenError{
							Type: models.ErrInvalidConfig,
							Err: fmt.Errorf("incremental mode: %d package(s) already exist in repository: %s",
								len(conflicts), strings.Join(conflictNames, ", ")),
						}
					}
				}

				if pkgType == scanner.TypeSysext {
					// The sysext generator verifies and merges retained signed metadata
					// itself so it can distinguish retained entries from incoming files.
					finalPackages = newPackages
					logrus.Infof("Reconciling %d existing + %d new %s packages",
						len(existingPackages), len(newPackages), pkgType)
				} else {
					// Combine existing + new packages
					finalPackages = append(existingPackages, newPackages...)
					logrus.Infof("Combining %d existing + %d new = %d total %s packages",
						len(existingPackages), len(newPackages), len(finalPackages), pkgType)
				}
			}
		} else {
			// Normal mode: only new packages
			finalPackages = newPackages
		}

		if len(finalPackages) == 0 {
			logrus.Warn("No packages to process")
			continue
		}

		if err := gen.ValidatePackages(finalPackages); err != nil {
			return &models.RepoGenError{
				Type: models.ErrInvalidConfig,
				Err:  fmt.Errorf("package validation failed for %s: %w", pkgType, err),
			}
		}
		plannedPackages[pkgType] = finalPackages
	}

	// The identity skip may leave nothing at all. That is a clean no-op: no
	// output is written and no directory index is generated, so a nightly
	// rebuild of unchanged sysexts succeeds without touching the repository.
	if config.SkipPublishedIdentities && len(plannedPackages) == 0 {
		if err := writeSkippedReport(config.SkippedReportPath, skippedReportLines); err != nil {
			return err
		}
		logrus.Info("Nothing to publish: every sysext identity is already published")
		return nil
	}

	// Phase 2: write each format's output.
	for pkgType, finalPackages := range plannedPackages {
		gen := generators[pkgType]
		logrus.Infof("Generating %s repository with %d packages...", pkgType, len(finalPackages))

		if err := gen.Generate(ctx, config, finalPackages); err != nil {
			return &models.RepoGenError{
				Type: models.ErrMetadataGen,
				Err:  fmt.Errorf("failed to generate %s repository: %w", pkgType, err),
			}
		}
	}

	// Generate HTML directory index pages for browsing
	if config.HTMLIndex {
		if err := dirindex.Generate(config.OutputDir); err != nil {
			return &models.RepoGenError{
				Type: models.ErrFileOp,
				Err:  fmt.Errorf("failed to generate directory index: %w", err),
			}
		}
	}

	// The report is a proof of a successful run, so it is written last.
	if config.SkipPublishedIdentities {
		if err := writeSkippedReport(config.SkippedReportPath, skippedReportLines); err != nil {
			return err
		}
	}

	logrus.Info("Repository generation completed successfully!")
	logrus.Infof("Output directory: %s", config.OutputDir)

	return nil
}

// writeSkippedReport writes the sorted skipped-identity lines, one per line, to
// reportPath. An empty path writes nothing; a run that skipped nothing writes
// an empty file.
func writeSkippedReport(reportPath string, lines []string) error {
	if reportPath == "" {
		return nil
	}

	sorted := append([]string(nil), lines...)
	sort.Strings(sorted)

	var builder strings.Builder
	for _, line := range sorted {
		builder.WriteString(line)
		builder.WriteString("\n")
	}

	if err := os.WriteFile(reportPath, []byte(builder.String()), 0o644); err != nil {
		return &models.RepoGenError{
			Type: models.ErrFileOp,
			Err:  fmt.Errorf("failed to write skipped report %s: %w", reportPath, err),
		}
	}

	return nil
}

// validateSysextCandidates reports an error when the scanner dropped a sysext
// candidate. It walks the input directory the same way
// internal/scanner/filesystem.go does and requires every regular file whose
// base name ends in .raw, .raw.zst, .raw.xz or .raw.gz to be present in scanned
// as a sysext, so a file that detection could not classify cannot be skipped
// silently.
func validateSysextCandidates(inputDir string, scanned []scanner.ScannedPackage) error {
	detected := make(map[string]struct{}, len(scanned))
	for _, pkg := range scanned {
		if pkg.Type == scanner.TypeSysext {
			detected[filepath.Clean(pkg.Path)] = struct{}{}
		}
	}

	return filepath.Walk(inputDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk input directory for sysext candidates: %w", walkErr)
		}
		if info.IsDir() || !info.Mode().IsRegular() {
			return nil
		}
		base := filepath.Base(path)
		if !strings.HasSuffix(base, ".raw") &&
			!strings.HasSuffix(base, ".raw.zst") &&
			!strings.HasSuffix(base, ".raw.xz") &&
			!strings.HasSuffix(base, ".raw.gz") {
			return nil
		}
		if _, ok := detected[filepath.Clean(path)]; !ok {
			return fmt.Errorf("--skip-published-identities: sysext candidate %s was not detected as a sysext", path)
		}
		return nil
	})
}

// hasPacmanPackages checks if input directory contains Pacman packages
func hasPacmanPackages(inputDir string) bool {
	matches, _ := filepath.Glob(filepath.Join(inputDir, "*.pkg.tar.*"))
	return len(matches) > 0
}

// hasDebianPackages reports whether the scanner finds any Debian package in inputDir.
func hasDebianPackages(inputDir string) (bool, error) {
	scanned, err := scanner.NewFileSystemScanner().Scan(context.Background(), inputDir)
	if err != nil {
		return false, err
	}
	for _, pkg := range scanned {
		if pkg.Type == scanner.TypeDeb {
			return true, nil
		}
	}
	return false, nil
}

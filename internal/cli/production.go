package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/frostyard/repogen/internal/generator/deb"
	"github.com/frostyard/repogen/internal/models"
	"github.com/frostyard/repogen/internal/scanner"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

const (
	productionOrigin = "Repogen Repository"
	productionLabel  = "Frostyard Repository"
)

var (
	productionIdentifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]*$`)
	productionSHA256Pattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	debianPackageNamePattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]+$`)
	debianVersionPattern        = regexp.MustCompile(`^[0-9][A-Za-z0-9.+:~-]*$`)
	debianFieldNamePattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)
	mutableSuiteNames           = map[string]struct{}{
		"experimental": {},
		"oldoldstable": {},
		"oldstable":    {},
		"sid":          {},
		"stable":       {},
		"testing":      {},
		"unstable":     {},
	}
)

// NewValidateProductionCmd creates the fail-closed Frostyard production
// request preflight. R2 intentionally validates without generating output;
// later phases add strict restore, staging, signing, and publication.
func NewValidateProductionCmd() *cobra.Command {
	return newValidateProductionCmd(&models.RepositoryConfig{
		Origin: productionOrigin,
		Label:  productionLabel,
	})
}

func newValidateProductionCmd(config *models.RepositoryConfig) *cobra.Command {
	var operation string
	var trustedPublicKeyPath string
	var expectedPriorReleaseSHA256 string

	cmd := &cobra.Command{
		Use:   "validate-production",
		Short: "Validate a Frostyard production Debian request and prior state without writing output",
		Long: `Validates one Frostyard production Debian request before any repository
files are written. Initialize proves the target suite is absent. Reconcile
strictly verifies the expected signed prior Release and every architecture
index. This command does not generate, sign, or publish a repository.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateProductionConfig(cmd, config); err != nil {
				return err
			}
			if err := validateProductionOperation(
				cmd,
				operation,
				trustedPublicKeyPath,
				expectedPriorReleaseSHA256,
			); err != nil {
				return err
			}

			packages, err := validateProductionInput(cmd.Context(), config)
			if err != nil {
				return err
			}

			switch operation {
			case "initialize":
				if err := deb.VerifyProductionInitialization(config.OutputDir, config.Codename); err != nil {
					return productionStateError("%v", err)
				}
				logrus.Infof(
					"Validated production initialize for absent suite %s with %d package(s); no output was written",
					config.Codename,
					len(packages),
				)
			case "reconcile":
				state, err := deb.RestoreProductionState(
					config,
					trustedPublicKeyPath,
					expectedPriorReleaseSHA256,
				)
				if err != nil {
					return productionStateError("%v", err)
				}
				logrus.Infof(
					"Validated production reconcile for suite %s at Release %s with %d retained and %d incoming package(s); no output was written",
					config.Codename,
					state.ReleaseSHA256,
					len(state.Packages),
					len(packages),
				)
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&config.InputDir, "input-dir", "i", "", "Explicit directory containing only Debian packages")
	cmd.Flags().StringVarP(&config.OutputDir, "output-dir", "o", "", "Explicit future staging/output directory")
	cmd.Flags().StringVar(&config.Codename, "codename", "", "Explicit immutable Debian codename")
	cmd.Flags().StringVar(&config.Suite, "suite", "", "Explicit Debian suite; must equal codename")
	cmd.Flags().StringSliceVar(&config.Components, "components", nil, "Explicit components; must be exactly main")
	cmd.Flags().StringSliceVar(&config.Arches, "arch", nil, "Explicit architectures; initial allowlist is all,amd64")
	cmd.Flags().StringVar(&operation, "operation", "", "Explicit operation: initialize or reconcile")
	cmd.Flags().StringVar(&trustedPublicKeyPath, "trusted-public-key", "", "Accepted public key for reconcile signature verification")
	cmd.Flags().StringVar(
		&expectedPriorReleaseSHA256,
		"expected-prior-release-sha256",
		"",
		"Expected prior Release SHA-256 for reconcile",
	)

	return cmd
}

func validateProductionOperation(
	cmd *cobra.Command,
	operation string,
	trustedPublicKeyPath string,
	expectedPriorReleaseSHA256 string,
) error {
	if !cmd.Flags().Changed("operation") {
		return invalidProductionConfig("--operation must be provided explicitly")
	}
	switch operation {
	case "initialize":
		if cmd.Flags().Changed("trusted-public-key") || trustedPublicKeyPath != "" {
			return invalidProductionConfig("--trusted-public-key is not valid for initialize")
		}
		if cmd.Flags().Changed("expected-prior-release-sha256") || expectedPriorReleaseSHA256 != "" {
			return invalidProductionConfig("--expected-prior-release-sha256 must be absent for initialize")
		}
	case "reconcile":
		if !cmd.Flags().Changed("trusted-public-key") || trustedPublicKeyPath == "" {
			return invalidProductionConfig("--trusted-public-key must be provided explicitly for reconcile")
		}
		if !cmd.Flags().Changed("expected-prior-release-sha256") {
			return invalidProductionConfig("--expected-prior-release-sha256 must be provided explicitly for reconcile")
		}
		if !productionSHA256Pattern.MatchString(expectedPriorReleaseSHA256) {
			return invalidProductionConfig(
				"--expected-prior-release-sha256 must be 64 lowercase hexadecimal characters",
			)
		}
	default:
		return invalidProductionConfig("--operation must be exactly initialize or reconcile")
	}
	return nil
}

func validateProductionConfig(cmd *cobra.Command, config *models.RepositoryConfig) error {
	for _, flag := range []string{"input-dir", "output-dir", "codename", "suite", "components", "arch"} {
		if !cmd.Flags().Changed(flag) {
			return invalidProductionConfig("--%s must be provided explicitly", flag)
		}
	}

	for name, value := range map[string]string{
		"input-dir":  config.InputDir,
		"output-dir": config.OutputDir,
		"codename":   config.Codename,
		"suite":      config.Suite,
	} {
		if value == "" {
			return invalidProductionConfig("--%s cannot be empty", name)
		}
		if containsControl(value) {
			return invalidProductionConfig("--%s contains a control character", name)
		}
	}

	if err := validateProductionIdentifier("codename", config.Codename); err != nil {
		return err
	}
	if err := validateProductionIdentifier("suite", config.Suite); err != nil {
		return err
	}
	if _, mutable := mutableSuiteNames[config.Codename]; mutable {
		return invalidProductionConfig("codename %q is mutable or reserved; use an immutable Debian release codename", config.Codename)
	}
	if config.Suite != config.Codename {
		return invalidProductionConfig("suite %q must exactly match codename %q", config.Suite, config.Codename)
	}

	if len(config.Components) != 1 || config.Components[0] != "main" {
		return invalidProductionConfig("--components must be exactly main")
	}
	if err := validateExactProductionArchitectures(config.Arches); err != nil {
		return err
	}
	config.Arches = deb.ProductionArchitectures()

	if err := validateProductionPaths(config.InputDir, config.OutputDir); err != nil {
		return err
	}

	config.Origin = productionOrigin
	config.Label = productionLabel
	return nil
}

func validateProductionIdentifier(name, value string) error {
	if value == "." || value == ".." || !productionIdentifierPattern.MatchString(value) {
		return invalidProductionConfig(
			"%s %q is not a safe Debian identifier; use lowercase letters, digits, dots, pluses, or hyphens",
			name,
			value,
		)
	}
	return nil
}

func validateExactProductionArchitectures(architectures []string) error {
	required := deb.ProductionArchitectures()
	if len(architectures) != len(required) {
		return invalidProductionConfig("--arch must contain exactly %s", strings.Join(required, " and "))
	}

	seen := make(map[string]struct{}, len(architectures))
	for _, architecture := range architectures {
		if containsControl(architecture) {
			return invalidProductionConfig("architecture %q contains a control character", architecture)
		}
		if _, duplicate := seen[architecture]; duplicate {
			return invalidProductionConfig("architecture %q is duplicated", architecture)
		}
		seen[architecture] = struct{}{}
	}
	for _, architecture := range required {
		if _, ok := seen[architecture]; !ok {
			return invalidProductionConfig("--arch must include %s", architecture)
		}
	}
	return nil
}

func validateProductionPaths(inputDir, outputDir string) error {
	inputInfo, err := os.Stat(inputDir)
	if err != nil {
		return invalidProductionConfig("cannot inspect input-dir: %v", err)
	}
	if !inputInfo.IsDir() {
		return invalidProductionConfig("input-dir is not a directory")
	}

	if outputInfo, err := os.Stat(outputDir); err == nil {
		if !outputInfo.IsDir() {
			return invalidProductionConfig("output-dir exists and is not a directory")
		}
	} else if !os.IsNotExist(err) {
		return invalidProductionConfig("cannot inspect output-dir: %v", err)
	}

	inputPath, err := canonicalPath(inputDir)
	if err != nil {
		return invalidProductionConfig("cannot resolve input-dir: %v", err)
	}
	outputPath, err := canonicalPath(outputDir)
	if err != nil {
		return invalidProductionConfig("cannot resolve output-dir: %v", err)
	}
	if pathsOverlap(inputPath, outputPath) {
		return invalidProductionConfig("input-dir and output-dir must not be the same or contain one another")
	}

	return nil
}

func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)

	existing := absolute
	var suffix []string
	for {
		if _, err := os.Lstat(existing); err == nil {
			resolved, err := filepath.EvalSymlinks(existing)
			if err != nil {
				return "", err
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}

		parent := filepath.Dir(existing)
		if parent == existing {
			return absolute, nil
		}
		suffix = append(suffix, filepath.Base(existing))
		existing = parent
	}
}

func pathsOverlap(first, second string) bool {
	return pathContains(first, second) || pathContains(second, first)
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." ||
		(relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func validateProductionInput(ctx context.Context, config *models.RepositoryConfig) ([]models.Package, error) {
	scannedPackages, err := scanProductionInput(ctx, config.InputDir)
	if err != nil {
		return nil, err
	}
	if len(scannedPackages) == 0 {
		return nil, invalidProductionConfig("input-dir contains no recognized packages")
	}

	for _, scanned := range scannedPackages {
		if scanned.Type != scanner.TypeDeb {
			return nil, invalidProductionConfig(
				"production Debian input contains recognized %s artifact %q",
				scanned.Type,
				filepath.Base(scanned.Path),
			)
		}
		if err := validateProductionPackagePath(config.InputDir, scanned); err != nil {
			return nil, err
		}
	}

	packages := make([]models.Package, 0, len(scannedPackages))
	for _, scanned := range scannedPackages {
		pkg, err := deb.ParsePackage(scanned.Path)
		if err != nil {
			return nil, &models.RepoGenError{
				Type:    models.ErrPackageParse,
				Package: scanned.Path,
				Err:     fmt.Errorf("production Debian input is invalid: %w", err),
			}
		}
		if err := validateProductionDebianPackage(config, pkg); err != nil {
			return nil, err
		}
		packages = append(packages, *pkg)
	}

	if err := deb.NewGenerator(nil).ValidatePackages(packages); err != nil {
		return nil, invalidProductionConfig("Debian package validation failed: %v", err)
	}
	return packages, nil
}

func scanProductionInput(ctx context.Context, inputDir string) ([]scanner.ScannedPackage, error) {
	var packages []scanner.ScannedPackage
	err := filepath.Walk(inputDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return productionFileError("cannot inspect input path %q: %v", path, walkErr)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if info.IsDir() {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return invalidProductionConfig("input path %q is a symlink", path)
		}
		if !info.Mode().IsRegular() {
			return invalidProductionConfig("input path %q is not a regular file", path)
		}

		packageType, err := scanner.DetectPackageType(path)
		if err != nil {
			return productionFileError("cannot detect package type for %q: %v", path, err)
		}
		if packageType == scanner.TypeUnknown {
			return nil
		}

		packages = append(packages, scanner.ScannedPackage{
			Path: path,
			Type: packageType,
			Size: info.Size(),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	return packages, nil
}

func validateProductionPackagePath(inputDir string, scanned scanner.ScannedPackage) error {
	if containsControl(scanned.Path) {
		return invalidProductionConfig("package path contains a control character")
	}

	inputPath, err := filepath.Abs(inputDir)
	if err != nil {
		return productionFileError("cannot resolve input-dir: %v", err)
	}
	packagePath, err := filepath.Abs(scanned.Path)
	if err != nil {
		return productionFileError("cannot resolve package path: %v", err)
	}
	if !pathContains(filepath.Clean(inputPath), filepath.Clean(packagePath)) {
		return invalidProductionConfig("package path %q escapes input-dir", scanned.Path)
	}

	info, err := os.Lstat(scanned.Path)
	if err != nil {
		return productionFileError("cannot inspect package path %q: %v", scanned.Path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return invalidProductionConfig("package path %q is a symlink", scanned.Path)
	}
	if !info.Mode().IsRegular() {
		return invalidProductionConfig("package path %q is not a regular file", scanned.Path)
	}
	if filepath.Ext(scanned.Path) != ".deb" {
		return invalidProductionConfig("production Debian input %q must use the .deb extension", scanned.Path)
	}
	return nil
}

func validateProductionDebianPackage(config *models.RepositoryConfig, pkg *models.Package) error {
	if !debianPackageNamePattern.MatchString(pkg.Name) {
		return invalidProductionConfig("package name %q is not a valid Debian package name", pkg.Name)
	}
	if !debianVersionPattern.MatchString(pkg.Version) {
		return invalidProductionConfig("package %q has invalid Debian version %q", pkg.Name, pkg.Version)
	}

	allowedArchitecture := false
	for _, architecture := range config.Arches {
		if pkg.Architecture == architecture {
			allowedArchitecture = true
			break
		}
	}
	if !allowedArchitecture {
		return invalidProductionConfig(
			"package %q architecture %q is outside the requested production allowlist",
			pkg.Name,
			pkg.Architecture,
		)
	}

	for field, value := range map[string]string{
		"Maintainer": pkg.Maintainer,
		"Homepage":   pkg.Homepage,
	} {
		if containsControl(value) {
			return invalidProductionConfig("package %q field %s contains a control character", pkg.Name, field)
		}
	}
	if containsDisallowedDescriptionControl(pkg.Description) {
		return invalidProductionConfig("package %q description contains a disallowed control character", pkg.Name)
	}
	for _, dependency := range pkg.Dependencies {
		if containsControl(dependency) {
			return invalidProductionConfig("package %q dependency contains a control character", pkg.Name)
		}
	}
	for key, value := range pkg.Metadata {
		if !debianFieldNamePattern.MatchString(key) {
			return invalidProductionConfig("package %q metadata field name %q is invalid", pkg.Name, key)
		}
		if containsControl(fmt.Sprint(value)) {
			return invalidProductionConfig("package %q metadata field %q contains a control character", pkg.Name, key)
		}
	}

	return nil
}

func containsControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}

func containsDisallowedDescriptionControl(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsControl(r) && r != '\n' && r != '\t'
	}) >= 0
}

func invalidProductionConfig(format string, args ...interface{}) error {
	return &models.RepoGenError{
		Type: models.ErrInvalidConfig,
		Err:  fmt.Errorf(format, args...),
	}
}

func productionFileError(format string, args ...interface{}) error {
	return &models.RepoGenError{
		Type: models.ErrFileOp,
		Err:  fmt.Errorf(format, args...),
	}
}

func productionStateError(format string, args ...interface{}) error {
	return &models.RepoGenError{
		Type: models.ErrMetadataGen,
		Err:  fmt.Errorf(format, args...),
	}
}

package sysext

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/frostyard/repogen/internal/generator"
	"github.com/frostyard/repogen/internal/models"
	"github.com/frostyard/repogen/internal/scanner"
	"github.com/frostyard/repogen/internal/signer"
	"github.com/frostyard/repogen/internal/utils"
	"github.com/sirupsen/logrus"
)

// Generator implements the generator.Generator interface for systemd-sysext repositories.
//
// The generated repository structure is:
//
//	<output>/ext/<extension-name>/
//	    SHA256SUMS                    # Standard checksum file for systemd-sysupdate
//	    SHA256SUMS.gpg                # Detached signature (when a signer is configured)
//	    <extension-name>.transfer     # systemd-sysupdate transfer configuration
//	    <extension>.raw               # Extension files
//	    <extension>.raw.zst           # Compressed variants
//
// Sysext filenames follow the format: NAME_VERSION_OSVERSION_ARCH.raw[.compression]
// Example: docker_24.0.5_13_x86-64.raw.zst
type Generator struct {
	baseURL    string
	signer     signer.Signer
	beforeStep func(string) error
}

// NewGenerator creates a new systemd-sysext generator.
func NewGenerator(baseURL string, metadataSigner signer.Signer) generator.Generator {
	return &Generator{
		baseURL: baseURL,
		signer:  metadataSigner,
	}
}

// Generate creates a systemd-sysext repository structure.
//
// Output structure:
//
//	<output>/ext/<name>/SHA256SUMS
//	<output>/ext/<name>/<name>_<version>_<osversion>_<arch>.raw[.compression]
func (g *Generator) Generate(ctx context.Context, config *models.RepositoryConfig, packages []models.Package) error {
	releaseLock, err := acquireReconcileLock(ctx, config.OutputDir)
	if err != nil {
		return err
	}
	defer func() {
		if err := releaseLock(); err != nil {
			logrus.Errorf("failed to release sysext reconciliation lock: %v", err)
		}
	}()

	incomingFilenames := make(map[string]struct{}, len(packages))
	for _, pkg := range packages {
		incomingFilenames[filepath.Base(pkg.Filename)] = struct{}{}
	}
	if err := validateSysextDestinations(config.OutputDir, packages); err != nil {
		return fmt.Errorf("failed to validate sysext destinations: %w", err)
	}

	currentExt := filepath.Join(config.OutputDir, "ext")
	currentExists := false
	if info, statErr := os.Stat(currentExt); statErr == nil {
		if !info.IsDir() {
			return fmt.Errorf("existing sysext path is not a directory: %s", currentExt)
		}
		currentExists = true
		if err := g.runBeforeStep("restore:read"); err != nil {
			return err
		}
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("inspect existing sysext repository: %w", statErr)
	}

	mergeConfig := *config
	mergeConfig.Incremental = config.Incremental || currentExists
	packages, err = g.normalizeAndMergePackages(&mergeConfig, packages)
	if err != nil {
		return err
	}

	if err := g.runBeforeStep("stage:create"); err != nil {
		return err
	}
	outputParent := filepath.Dir(filepath.Clean(config.OutputDir))
	stageRoot, err := os.MkdirTemp(outputParent, ".repogen-sysext-stage-")
	if err != nil {
		return fmt.Errorf("create sysext staging directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(stageRoot); err != nil {
			logrus.Errorf("failed to remove sysext staging directory: %v", err)
		}
	}()

	stageExt := filepath.Join(stageRoot, "ext")
	if currentExists {
		if err := g.runBeforeStep("stage:copy-existing"); err != nil {
			return err
		}
		if err := copyDirectory(currentExt, stageExt); err != nil {
			return fmt.Errorf("stage existing sysext repository: %w", err)
		}
	}

	stageConfig := mergeConfig
	stageConfig.OutputDir = stageRoot
	if err := g.generateStaged(ctx, &stageConfig, packages); err != nil {
		return err
	}
	if err := g.runBeforeStep("stage:validate"); err != nil {
		return err
	}
	stagedPackages, err := g.readCurrentPackages(&stageConfig, true)
	if err != nil {
		return fmt.Errorf("validate staged sysext repository: %w", err)
	}
	if err := validateStagedPayloads(stagedPackages, incomingFilenames); err != nil {
		return fmt.Errorf("validate staged sysext payloads: %w", err)
	}
	if err := g.runBeforeStep("commit:atomic-switch"); err != nil {
		return err
	}
	if err := os.MkdirAll(config.OutputDir, 0o755); err != nil {
		return fmt.Errorf("create sysext output directory: %w", err)
	}
	if err := atomicReplaceDirectory(stageExt, currentExt, currentExists); err != nil {
		return fmt.Errorf("atomically replace sysext repository: %w", err)
	}
	if currentExists {
		if err := os.RemoveAll(stageExt); err != nil {
			return fmt.Errorf("remove prior sysext generation after commit: %w", err)
		}
	}
	return nil
}

func (g *Generator) generateStaged(ctx context.Context, config *models.RepositoryConfig, packages []models.Package) error {
	logrus.Info("Generating systemd-sysext repository...")

	// Group packages by extension name
	extPackages := make(map[string][]models.Package)
	for _, pkg := range packages {
		extPackages[pkg.Name] = append(extPackages[pkg.Name], pkg)
	}
	if err := validateSysextDestinations(config.OutputDir, packages); err != nil {
		return fmt.Errorf("failed to validate sysext destinations: %w", err)
	}

	// Generate repository for each extension
	extNames := make([]string, 0, len(extPackages))
	for extName := range extPackages {
		extNames = append(extNames, extName)
	}
	sort.Strings(extNames)
	for _, extName := range extNames {
		pkgs := extPackages[extName]
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

func validateSysextDestinations(outputDir string, packages []models.Package) error {
	seen := make(map[string]string, len(packages))
	for index := range packages {
		pkg := &packages[index]
		dstPath := filepath.Join(outputDir, "ext", pkg.Name, filepath.Base(pkg.Filename))

		srcPath, _, _, err := utils.ShouldCopyPackage(pkg, dstPath, outputDir)
		if err != nil {
			return fmt.Errorf("package copy check failed for %s: %w", pkg.Name, err)
		}

		sha256sum := pkg.SHA256Sum
		if _, err := os.Stat(srcPath); err == nil {
			checksums, err := utils.CalculateChecksums(srcPath)
			if err != nil {
				return fmt.Errorf("failed to calculate checksums for %s: %w", filepath.Base(pkg.Filename), err)
			}
			sha256sum = checksums.SHA256
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("cannot stat sysext source %s: %w", srcPath, err)
		}

		previous, duplicate := seen[dstPath]
		if !duplicate {
			seen[dstPath] = sha256sum
			continue
		}
		if sha256sum == "" || previous == "" {
			return fmt.Errorf("cannot verify duplicate sysext destination %q without SHA256", dstPath)
		}
		if sha256sum != previous {
			return fmt.Errorf("conflicting package contents for sysext destination %q", dstPath)
		}
	}
	return nil
}

func (g *Generator) runBeforeStep(step string) error {
	if g.beforeStep == nil {
		return nil
	}
	if err := g.beforeStep(step); err != nil {
		return fmt.Errorf("before sysext %s: %w", step, err)
	}
	return nil
}

func (g *Generator) normalizeAndMergePackages(config *models.RepositoryConfig, packages []models.Package) ([]models.Package, error) {
	merged := make([]models.Package, 0, len(packages))
	if config.Incremental {
		existing, err := g.readCurrentPackages(config, true)
		if err == nil {
			merged = append(merged, existing...)
		} else if _, statErr := os.Stat(filepath.Join(config.OutputDir, "ext")); statErr == nil {
			return nil, fmt.Errorf("restore existing sysext metadata: %w", err)
		} else if !os.IsNotExist(statErr) {
			return nil, fmt.Errorf("inspect existing sysext metadata: %w", statErr)
		}
	}
	merged = append(merged, packages...)

	byIdentity := make(map[string]models.Package, len(merged))
	for _, pkg := range merged {
		parsed, err := parseFilenameMetadata(pkg.Filename, filepath.Base(pkg.Filename))
		if err != nil {
			return nil, err
		}
		osVersion, _ := pkg.Metadata["OSVersion"].(string)
		parsedOSVersion, _ := parsed.Metadata["OSVersion"].(string)
		if osVersion == "" {
			if pkg.Metadata == nil {
				pkg.Metadata = make(map[string]interface{})
			}
			pkg.Metadata["OSVersion"] = parsedOSVersion
			osVersion = parsedOSVersion
		}
		if osVersion != parsedOSVersion {
			return nil, fmt.Errorf(
				"sysext OSVersion metadata %q does not match filename %q",
				osVersion,
				filepath.Base(pkg.Filename),
			)
		}
		identity := utils.PackageIdentity(pkg, scanner.TypeSysext)
		previous, exists := byIdentity[identity]
		if !exists {
			byIdentity[identity] = pkg
			continue
		}
		if filepath.Base(previous.Filename) != filepath.Base(pkg.Filename) ||
			previous.SHA256Sum == "" ||
			pkg.SHA256Sum == "" ||
			previous.SHA256Sum != pkg.SHA256Sum {
			return nil, fmt.Errorf(
				"conflicting sysext artifacts for identity %s: %s and %s",
				identity,
				filepath.Base(previous.Filename),
				filepath.Base(pkg.Filename),
			)
		}
		if _, err := os.Stat(previous.Filename); os.IsNotExist(err) {
			if _, currentErr := os.Stat(pkg.Filename); currentErr == nil {
				byIdentity[identity] = pkg
			}
		}
	}

	result := make([]models.Package, 0, len(byIdentity))
	for _, pkg := range byIdentity {
		result = append(result, pkg)
	}
	return result, nil
}

func (g *Generator) readCurrentPackages(config *models.RepositoryConfig, verifySignatures bool) ([]models.Package, error) {
	extDir := filepath.Join(config.OutputDir, "ext")
	entries, err := os.ReadDir(extDir)
	if err != nil {
		return nil, fmt.Errorf("read ext directory: %w", err)
	}

	var packages []models.Package
	var manifestNames []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifestPath := filepath.Join(extDir, entry.Name(), "SHA256SUMS")
		info, err := os.Lstat(manifestPath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect sysext manifest %s: %w", manifestPath, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("sysext manifest is not a regular file: %s", manifestPath)
		}
		parsed, err := parseSHA256SUMSStrict(manifestPath, extDir, entry.Name())
		if err != nil {
			return nil, fmt.Errorf("parse sysext manifest %s: %w", manifestPath, err)
		}
		if err := validatePresentPayloads(parsed); err != nil {
			return nil, err
		}
		signaturePath := manifestPath + ".gpg"
		_, signatureErr := os.Lstat(signaturePath)
		hasSignature := signatureErr == nil
		if signatureErr != nil && !os.IsNotExist(signatureErr) {
			return nil, fmt.Errorf("inspect sysext manifest signature %s: %w", signaturePath, signatureErr)
		}
		if verifySignatures {
			if hasSignature && g.signer == nil {
				return nil, fmt.Errorf("cannot verify signed sysext manifest without a signing key: %s", manifestPath)
			}
			if !hasSignature && g.signer != nil {
				return nil, fmt.Errorf("signed sysext generation is missing %s", signaturePath)
			}
			if hasSignature {
				if err := g.verifyManifestSignature(manifestPath); err != nil {
					return nil, err
				}
			}
		}
		if err := g.verifyTransferFile(filepath.Join(extDir, entry.Name(), entry.Name()+".transfer"), entry.Name()); err != nil {
			return nil, err
		}
		manifestNames = append(manifestNames, entry.Name())
		packages = append(packages, parsed...)
	}
	if len(packages) == 0 {
		return nil, fmt.Errorf("no existing sysext packages found")
	}
	sort.Strings(manifestNames)
	if err := verifyIndex(filepath.Join(extDir, "index"), manifestNames); err != nil {
		return nil, err
	}
	return packages, nil
}

func validateStagedPayloads(packages []models.Package, incomingFilenames map[string]struct{}) error {
	for _, pkg := range packages {
		_, incoming := incomingFilenames[filepath.Base(pkg.Filename)]
		info, err := os.Stat(pkg.Filename)
		if os.IsNotExist(err) && !incoming {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect %s: %w", pkg.Filename, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("sysext payload is not a regular file: %s", pkg.Filename)
		}
	}
	return validatePresentPayloads(packages)
}

func validatePresentPayloads(packages []models.Package) error {
	for _, pkg := range packages {
		info, err := os.Stat(pkg.Filename)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect %s: %w", pkg.Filename, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("sysext payload is not a regular file: %s", pkg.Filename)
		}
		checksums, err := utils.CalculateChecksums(pkg.Filename)
		if err != nil {
			return fmt.Errorf("hash %s: %w", pkg.Filename, err)
		}
		if checksums.SHA256 != pkg.SHA256Sum {
			return fmt.Errorf(
				"sysext payload %s has SHA-256 %s, want %s",
				pkg.Filename,
				checksums.SHA256,
				pkg.SHA256Sum,
			)
		}
	}
	return nil
}

func parseSHA256SUMSStrict(sha256sumsPath, extDir, extName string) ([]models.Package, error) {
	content, err := os.ReadFile(sha256sumsPath)
	if err != nil {
		return nil, err
	}
	var packages []models.Package
	seen := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return nil, fmt.Errorf("line %d does not contain one digest and filename", lineNumber)
		}
		digest, filename := fields[0], fields[1]
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != 32 {
			return nil, fmt.Errorf("line %d has invalid SHA-256 digest", lineNumber)
		}
		digest = strings.ToLower(digest)
		if filename != filepath.Base(filename) {
			return nil, fmt.Errorf("line %d has non-basename filename %q", lineNumber, filename)
		}
		pkg, err := parseFilenameMetadata(filepath.Join(extDir, extName, filename), filename)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNumber, err)
		}
		if pkg.Name != extName {
			return nil, fmt.Errorf("line %d names extension %q under %q", lineNumber, pkg.Name, extName)
		}
		if prior, exists := seen[filename]; exists {
			return nil, fmt.Errorf("line %d duplicates %q (prior digest %s)", lineNumber, filename, prior)
		}
		seen[filename] = digest
		pkg.SHA256Sum = digest
		packages = append(packages, *pkg)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(packages) == 0 {
		return nil, fmt.Errorf("manifest contains no sysext entries")
	}
	return packages, nil
}

func verifyIndex(indexPath string, manifestNames []string) error {
	content, err := os.ReadFile(indexPath)
	if err != nil {
		return fmt.Errorf("read sysext index: %w", err)
	}
	var names []string
	seen := make(map[string]struct{})
	for lineNumber, line := range strings.Split(strings.TrimSuffix(string(content), "\n"), "\n") {
		if line == "" || line != strings.TrimSpace(line) || filepath.Base(line) != line {
			return fmt.Errorf("sysext index line %d is invalid", lineNumber+1)
		}
		if _, exists := seen[line]; exists {
			return fmt.Errorf("sysext index contains duplicate %q", line)
		}
		seen[line] = struct{}{}
		names = append(names, line)
	}
	if len(names) != len(manifestNames) {
		return fmt.Errorf("sysext index has %d names, want %d manifest-backed names", len(names), len(manifestNames))
	}
	for index := range names {
		if names[index] != manifestNames[index] {
			return fmt.Errorf("sysext index entry %d is %q, want %q", index, names[index], manifestNames[index])
		}
	}
	return nil
}

func (g *Generator) verifyManifestSignature(manifestPath string) error {
	publicKey, err := g.signer.GetPublicKey()
	if err != nil {
		return fmt.Errorf("export sysext signing public key: %w", err)
	}
	keyring, armoredErr := openpgp.ReadArmoredKeyRing(bytes.NewReader(publicKey))
	if armoredErr != nil {
		keyring, err = openpgp.ReadKeyRing(bytes.NewReader(publicKey))
		if err != nil {
			return fmt.Errorf(
				"parse sysext signing public key as armored (%v) or binary: %w",
				armoredErr,
				err,
			)
		}
	}
	if len(keyring) != 1 {
		return fmt.Errorf("sysext signing public key contains %d keys, want 1", len(keyring))
	}
	manifest, err := os.Open(manifestPath)
	if err != nil {
		return fmt.Errorf("open sysext manifest for verification: %w", err)
	}
	defer func() { _ = manifest.Close() }()
	signaturePath := manifestPath + ".gpg"
	signature, err := os.Open(signaturePath)
	if err != nil {
		return fmt.Errorf("open sysext manifest signature %s: %w", signaturePath, err)
	}
	defer func() { _ = signature.Close() }()
	if _, err := openpgp.CheckDetachedSignature(keyring, manifest, signature, nil); err != nil {
		return fmt.Errorf("verify sysext manifest signature %s: %w", signaturePath, err)
	}
	return nil
}

func (g *Generator) verifyTransferFile(path, extName string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect sysext transfer %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("sysext transfer is not a regular file: %s", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read sysext transfer %s: %w", path, err)
	}
	if want := g.transferContent(extName); !bytes.Equal(content, want) {
		return fmt.Errorf("sysext transfer %s does not match the generated contract", path)
	}
	return nil
}

func copyDirectory(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported existing sysext entry %s", path)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		closeOutputErr := output.Close()
		closeInputErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeOutputErr != nil {
			return closeOutputErr
		}
		return closeInputErr
	})
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

	sort.Slice(packages, func(i, j int) bool {
		left := packages[i]
		right := packages[j]
		if left.Version != right.Version {
			return left.Version < right.Version
		}
		if left.Architecture != right.Architecture {
			return left.Architecture < right.Architecture
		}
		return left.Filename < right.Filename
	})
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
			if err := g.runBeforeStep("stage:payload"); err != nil {
				return err
			}
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

		if previous, exists := sha256Entries[basename]; exists && previous != pkg.SHA256Sum {
			return fmt.Errorf("conflicting SHA-256 values for sysext file %s", basename)
		}
		sha256Entries[basename] = pkg.SHA256Sum
	}

	// Build SHA256SUMS content from deduplicated entries
	filenames := make([]string, 0, len(sha256Entries))
	for filename := range sha256Entries {
		filenames = append(filenames, filename)
	}
	sort.Strings(filenames)
	sha256Lines := make([]string, 0, len(filenames))
	for _, filename := range filenames {
		hash := sha256Entries[filename]
		// Format: "<hash>  <filename>" (two spaces per shasum convention)
		sha256Lines = append(sha256Lines, fmt.Sprintf("%s  %s", hash, filename))
	}

	// Write SHA256SUMS file
	if err := g.runBeforeStep("stage:manifest"); err != nil {
		return err
	}
	sha256sumsPath := filepath.Join(extDir, "SHA256SUMS")
	sha256Content := strings.Join(sha256Lines, "\n") + "\n"
	if err := utils.WriteFile(sha256sumsPath, []byte(sha256Content), 0644); err != nil {
		return fmt.Errorf("failed to write SHA256SUMS: %w", err)
	}

	if err := g.runBeforeStep("stage:signature"); err != nil {
		return err
	}
	if g.signer != nil {
		signature, err := g.signer.SignDetachedBinaryFromFile(sha256sumsPath)
		if err != nil {
			return fmt.Errorf("failed to sign SHA256SUMS: %w", err)
		}
		if err := utils.WriteFile(sha256sumsPath+".gpg", signature, 0644); err != nil {
			return fmt.Errorf("failed to write SHA256SUMS.gpg: %w", err)
		}
	}

	// Generate systemd-sysupdate transfer configuration file
	if err := g.runBeforeStep("stage:transfer"); err != nil {
		return err
	}
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
	transferPath := filepath.Join(extDir, extName+".transfer")
	if err := utils.WriteFile(transferPath, g.transferContent(extName), 0644); err != nil {
		return err
	}

	logrus.Debugf("Generated transfer file: %s", transferPath)
	return nil
}

func (g *Generator) transferContent(extName string) []byte {
	sourceURL := strings.TrimSuffix(g.baseURL, "/") + "/ext/" + extName + "/"
	verify := "false"
	if g.signer != nil {
		verify = "true"
	}
	return []byte(fmt.Sprintf(`[Transfer]
Verify=%s

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
`, verify, sourceURL, extName, extName, extName, extName, extName, extName, extName, extName, extName))
}

// generateIndex creates an index file listing all available extensions.
// The index is a simple newline-separated list of extension names.
func (g *Generator) generateIndex(config *models.RepositoryConfig, extPackages map[string][]models.Package) error {
	extDir := filepath.Join(config.OutputDir, "ext")
	if err := utils.EnsureDir(extDir); err != nil {
		return err
	}

	namesSet := make(map[string]struct{}, len(extPackages))
	for name := range extPackages {
		namesSet[name] = struct{}{}
	}

	if config.Incremental {
		entries, err := os.ReadDir(extDir)
		if err != nil {
			return fmt.Errorf("failed to read existing sysext metadata: %w", err)
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			manifestPath := filepath.Join(extDir, entry.Name(), "SHA256SUMS")
			info, err := os.Stat(manifestPath)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return fmt.Errorf("failed to inspect existing sysext manifest %s: %w", manifestPath, err)
			}
			if info.Mode().IsRegular() {
				namesSet[entry.Name()] = struct{}{}
			}
		}
	}

	// Sort extension names for consistent output.
	names := make([]string, 0, len(namesSet))
	for name := range namesSet {
		names = append(names, name)
	}
	sort.Strings(names)

	// Write index file (one extension name per line)
	if err := g.runBeforeStep("stage:index"); err != nil {
		return err
	}
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
		if osVersion, ok := pkg.Metadata["OSVersion"].(string); !ok || osVersion == "" {
			return fmt.Errorf("sysext package missing OSVersion: %s", pkg.Filename)
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

		// Ensure the parsed name matches the extension directory being scanned.
		if pkg.Name != extName {
			logrus.Debugf("Skipping SHA256SUMS entry %s: name %q does not match extension %q", filename, pkg.Name, extName)
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

	// Ensure that NAME, VERSION, OSVERSION, and ARCH are all non-empty.
	for i, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("invalid filename format (empty component at index %d in %q): %s", i, nameVersionOSVersionArch, filename)
		}
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

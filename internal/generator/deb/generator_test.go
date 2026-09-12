package deb

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/frostyard/repogen/internal/models"
	signerpkg "github.com/frostyard/repogen/internal/signer"
	"github.com/frostyard/repogen/internal/utils"
)

type countingSigner struct {
	delegate       signerpkg.Signer
	cleartextCalls int
	detachedCalls  int
}

func (s *countingSigner) SignCleartext(data []byte) ([]byte, error) {
	s.cleartextCalls++
	return s.delegate.SignCleartext(data)
}

func (s *countingSigner) SignDetached(data []byte) ([]byte, error) {
	s.detachedCalls++
	return s.delegate.SignDetached(data)
}

func (s *countingSigner) SignDetachedBinary(data []byte) ([]byte, error) {
	return s.delegate.SignDetachedBinary(data)
}
func (s *countingSigner) SignDetachedBinaryFromFile(path string) ([]byte, error) {
	return s.delegate.SignDetachedBinaryFromFile(path)
}
func (s *countingSigner) GetPublicKey() ([]byte, error) {
	return s.delegate.GetPublicKey()
}

func TestGenerateReleaseUnsigned(t *testing.T) {
	// Setup temp directory
	tmpDir, err := os.MkdirTemp("", "repogen-test-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create generator without signer (unsigned)
	gen := NewGenerator(nil)

	config := &models.RepositoryConfig{
		OutputDir:  tmpDir,
		Codename:   "testing",
		Suite:      "testing",
		Origin:     "Test",
		Label:      "Test",
		Components: []string{"main"},
		Arches:     []string{"amd64"},
	}

	// Create required directory structure
	distsDir := filepath.Join(tmpDir, "dists", "testing", "main", "binary-amd64")
	if err := os.MkdirAll(distsDir, 0755); err != nil {
		t.Fatalf("Failed to create dists dir: %v", err)
	}

	// Create dummy Packages file
	packagesPath := filepath.Join(distsDir, "Packages")
	if err := os.WriteFile(packagesPath, []byte("Package: test\n"), 0644); err != nil {
		t.Fatalf("Failed to write Packages: %v", err)
	}

	packagesGzPath := filepath.Join(distsDir, "Packages.gz")
	if err := os.WriteFile(packagesGzPath, []byte{}, 0644); err != nil {
		t.Fatalf("Failed to write Packages.gz: %v", err)
	}

	// Generate repository files
	err = gen.Generate(context.Background(), config, []models.Package{})
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Verify InRelease exists
	inReleasePath := filepath.Join(tmpDir, "dists", "testing", "InRelease")
	if _, err := os.Stat(inReleasePath); os.IsNotExist(err) {
		t.Errorf("InRelease not created for unsigned repository")
	}

	// Verify Release exists
	releasePath := filepath.Join(tmpDir, "dists", "testing", "Release")
	if _, err := os.Stat(releasePath); os.IsNotExist(err) {
		t.Errorf("Release not created")
	}

	// Verify Release.gpg does NOT exist
	releaseGpgPath := filepath.Join(tmpDir, "dists", "testing", "Release.gpg")
	if _, err := os.Stat(releaseGpgPath); !os.IsNotExist(err) {
		t.Errorf("Release.gpg should not exist for unsigned repository")
	}

	// Verify InRelease and Release have identical content
	inReleaseData, _ := os.ReadFile(inReleasePath)
	releaseData, _ := os.ReadFile(releasePath)

	if !bytes.Equal(inReleaseData, releaseData) {
		t.Errorf("InRelease content doesn't match Release content for unsigned repo")
		t.Logf("InRelease:\n%s", inReleaseData)
		t.Logf("Release:\n%s", releaseData)
	}

	// Verify InRelease does NOT contain PGP signature markers
	if bytes.Contains(inReleaseData, []byte("BEGIN PGP")) {
		t.Errorf("Unsigned InRelease should not contain PGP signature markers")
	}
}

func TestIncrementalModeCopiesNewPackages(t *testing.T) {
	// This test simulates the S3 workflow where:
	// 1. Initial repo is created with package A
	// 2. Only metadata is synced locally (not package files)
	// 3. Incremental mode adds package B
	// 4. New package B should be copied to output
	// 5. Metadata should reference both A and B

	// Setup temp directories
	tmpDir, err := os.MkdirTemp("", "repogen-test-incremental-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	defer func() { _ = os.RemoveAll(tmpDir) }()

	inputDir := filepath.Join(tmpDir, "input")
	outputDir := filepath.Join(tmpDir, "output")
	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatalf("Failed to create input dir: %v", err)
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		t.Fatalf("Failed to create output dir: %v", err)
	}

	gen := NewGenerator(nil)
	config := &models.RepositoryConfig{
		OutputDir:  outputDir,
		Codename:   "testing",
		Suite:      "testing",
		Origin:     "Test",
		Label:      "Test",
		Components: []string{"main"},
		Arches:     []string{"amd64"},
	}

	// Step 1: Create initial repo with package A
	initialPkg := filepath.Join(inputDir, "pkga_1.0_amd64.deb")
	if err := os.WriteFile(initialPkg, []byte("fake deb package A"), 0644); err != nil {
		t.Fatalf("Failed to write initial package: %v", err)
	}

	packagesA := []models.Package{
		{
			Name:         "pkga",
			Version:      "1.0",
			Architecture: "amd64",
			Filename:     initialPkg,
			Size:         18,
			MD5Sum:       "abc123",
			SHA1Sum:      "def456",
			SHA256Sum:    "ghi789",
		},
	}

	// Generate initial repo
	err = gen.Generate(context.Background(), config, packagesA)
	if err != nil {
		t.Fatalf("Initial generation failed: %v", err)
	}

	// Verify package A was copied
	pkgAPath := filepath.Join(outputDir, "pool", "main", "p", "pkga", "pkga_1.0_amd64.deb")
	if _, err := os.Stat(pkgAPath); os.IsNotExist(err) {
		t.Fatalf("Package A was not copied to pool: %v", err)
	}

	// Step 2: Simulate S3 sync - keep only metadata, remove package files
	// Remove pool directory to simulate only having metadata
	poolDir := filepath.Join(outputDir, "pool")
	_ = os.RemoveAll(poolDir)

	// Verify package A is gone (simulating S3 scenario)
	if _, err := os.Stat(pkgAPath); !os.IsNotExist(err) {
		t.Fatalf("Package A should not exist locally after simulated S3 sync")
	}

	// Step 3: Create new package B
	newPkg := filepath.Join(inputDir, "pkgb_1.0_amd64.deb")
	_ = os.WriteFile(newPkg, []byte("fake deb package B"), 0644)

	// Step 4: Parse existing metadata (simulating incremental mode)
	existingPackages, err := gen.ParseExistingMetadata(config)
	if err != nil {
		t.Fatalf("Failed to parse existing metadata: %v", err)
	}

	if len(existingPackages) != 1 {
		t.Fatalf("Expected 1 existing package, got %d", len(existingPackages))
	}

	if existingPackages[0].Name != "pkga" {
		t.Errorf("Expected existing package to be pkga, got %s", existingPackages[0].Name)
	}

	// Step 5: Run incremental generation with package B
	packagesB := []models.Package{
		{
			Name:         "pkgb",
			Version:      "1.0",
			Architecture: "amd64",
			Filename:     newPkg,
			Size:         18,
			MD5Sum:       "xyz123",
			SHA1Sum:      "uvw456",
			SHA256Sum:    "rst789",
		},
	}

	// Combine existing + new packages (simulating incremental mode)
	allPackages := append(existingPackages, packagesB...)

	err = gen.Generate(context.Background(), config, allPackages)
	if err != nil {
		t.Fatalf("Incremental generation failed: %v", err)
	}

	// Step 6: Verify new package B was copied
	pkgBPath := filepath.Join(outputDir, "pool", "main", "p", "pkgb", "pkgb_1.0_amd64.deb")
	if _, err := os.Stat(pkgBPath); os.IsNotExist(err) {
		t.Errorf("NEW package B was NOT copied to pool in incremental mode")
	}

	// Verify package B content
	pkgBContent, err := os.ReadFile(pkgBPath)
	if err != nil {
		t.Errorf("Failed to read package B: %v", err)
	} else if string(pkgBContent) != "fake deb package B" {
		t.Errorf("Package B has wrong content: %s", pkgBContent)
	}

	// Step 7: Verify metadata includes both packages
	packagesFile := filepath.Join(outputDir, "dists", "testing", "main", "binary-amd64", "Packages")
	packagesContent, err := os.ReadFile(packagesFile)
	if err != nil {
		t.Fatalf("Failed to read Packages file: %v", err)
	}

	packagesStr := string(packagesContent)
	if !bytes.Contains(packagesContent, []byte("Package: pkga")) {
		t.Errorf("Packages file should include pkga (existing package)")
	}
	if !bytes.Contains(packagesContent, []byte("Package: pkgb")) {
		t.Errorf("Packages file should include pkgb (new package)")
	}

	t.Logf("Incremental mode test passed!")
	t.Logf("Packages file content:\n%s", packagesStr)
}

func TestGenerateNoOpPreservesSignedRelease(t *testing.T) {
	tmpDir := t.TempDir()
	inputDir := filepath.Join(tmpDir, "input")
	outputDir := filepath.Join(tmpDir, "output")
	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatal(err)
	}

	packagePath := filepath.Join(inputDir, "sample_1.0_amd64.deb")
	if err := os.WriteFile(packagePath, []byte("deterministic package"), 0644); err != nil {
		t.Fatal(err)
	}
	checksums, err := utils.CalculateChecksums(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	packages := []models.Package{{
		Name:         "sample",
		Version:      "1.0",
		Architecture: "amd64",
		Filename:     packagePath,
		Size:         checksums.Size,
		MD5Sum:       checksums.MD5,
		SHA1Sum:      checksums.SHA1,
		SHA256Sum:    checksums.SHA256,
		SHA512Sum:    checksums.SHA512,
	}}
	config := &models.RepositoryConfig{
		OutputDir:  outputDir,
		Codename:   "trixie",
		Suite:      "trixie",
		Origin:     "Test",
		Label:      "Test",
		Components: []string{"main"},
		Arches:     []string{"amd64"},
	}
	times := []time.Time{
		time.Date(2026, time.September, 12, 18, 0, 0, 0, time.UTC),
		time.Date(2026, time.September, 13, 18, 0, 0, 0, time.UTC),
		time.Date(2026, time.September, 14, 18, 0, 0, 0, time.UTC),
		time.Date(2026, time.September, 15, 18, 0, 0, 0, time.UTC),
	}
	next := 0
	gpgSigner, err := signerpkg.NewGPGSigner(productionPrivateKeyFixture(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gpgSigner.Close() }()
	metadataSigner := &countingSigner{delegate: gpgSigner}
	gen := NewGeneratorWithClock(metadataSigner, func() time.Time {
		current := times[next]
		next++
		return current
	})

	if err := gen.Generate(context.Background(), config, packages); err != nil {
		t.Fatalf("initial Generate() failed: %v", err)
	}
	suiteDir := filepath.Join(outputDir, "dists", "trixie")
	paths := []string{"Release", "InRelease", "Release.gpg"}
	initial := make(map[string][]byte, len(paths))
	for _, name := range paths {
		initial[name], err = os.ReadFile(filepath.Join(suiteDir, name))
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := gen.Generate(context.Background(), config, packages); err != nil {
		t.Fatalf("no-op Generate() failed: %v", err)
	}
	if metadataSigner.cleartextCalls != 1 || metadataSigner.detachedCalls != 1 {
		t.Fatalf("no-op re-signed metadata: cleartext=%d detached=%d", metadataSigner.cleartextCalls, metadataSigner.detachedCalls)
	}
	for _, name := range paths {
		got, readErr := os.ReadFile(filepath.Join(suiteDir, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !bytes.Equal(got, initial[name]) {
			t.Errorf("%s changed during no-op generation", name)
		}
	}
	if !strings.Contains(string(initial["Release"]), "Date: Sat, 12 Sep 2026 18:00:00 +0000\n") {
		t.Fatalf("initial Release did not use the controlled publication timestamp:\n%s", initial["Release"])
	}

	if err := os.WriteFile(filepath.Join(suiteDir, "InRelease"), []byte("tampered"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := gen.Generate(context.Background(), config, packages); err != nil {
		t.Fatalf("Generate() with invalid prior signature failed: %v", err)
	}
	if metadataSigner.cleartextCalls != 2 || metadataSigner.detachedCalls != 2 {
		t.Fatalf("invalid prior signatures were reused: cleartext=%d detached=%d", metadataSigner.cleartextCalls, metadataSigner.detachedCalls)
	}

	secondPackagePath := filepath.Join(inputDir, "second_1.0_amd64.deb")
	if err := os.WriteFile(secondPackagePath, []byte("changed package set"), 0644); err != nil {
		t.Fatal(err)
	}
	secondChecksums, err := utils.CalculateChecksums(secondPackagePath)
	if err != nil {
		t.Fatal(err)
	}
	changedPackages := append(packages, models.Package{
		Name:         "second",
		Version:      "1.0",
		Architecture: "amd64",
		Filename:     secondPackagePath,
		Size:         secondChecksums.Size,
		MD5Sum:       secondChecksums.MD5,
		SHA1Sum:      secondChecksums.SHA1,
		SHA256Sum:    secondChecksums.SHA256,
		SHA512Sum:    secondChecksums.SHA512,
	})
	if err := gen.Generate(context.Background(), config, changedPackages); err != nil {
		t.Fatalf("changed Generate() failed: %v", err)
	}
	if metadataSigner.cleartextCalls != 3 || metadataSigner.detachedCalls != 3 {
		t.Fatalf("changed package set was not signed: cleartext=%d detached=%d", metadataSigner.cleartextCalls, metadataSigner.detachedCalls)
	}
	changedRelease, err := os.ReadFile(filepath.Join(suiteDir, "Release"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(changedRelease), "Date: Tue, 15 Sep 2026 18:00:00 +0000\n") {
		t.Fatalf("changed Release did not use its publication timestamp:\n%s", changedRelease)
	}
}

func TestGenerateShuffledInputProducesIdenticalMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	inputDir := filepath.Join(tmpDir, "input")
	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatal(err)
	}
	firstPath := filepath.Join(inputDir, "same_2.0_amd64.deb")
	secondPath := filepath.Join(inputDir, "same_1.0_amd64.deb")
	if err := os.WriteFile(firstPath, []byte("version two"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte("version one"), 0644); err != nil {
		t.Fatal(err)
	}
	buildPackage := func(path, version string) models.Package {
		checksums, err := utils.CalculateChecksums(path)
		if err != nil {
			t.Fatal(err)
		}
		return models.Package{
			Name: "same", Version: version, Architecture: "amd64", Filename: path,
			Size: checksums.Size, MD5Sum: checksums.MD5, SHA1Sum: checksums.SHA1,
			SHA256Sum: checksums.SHA256, SHA512Sum: checksums.SHA512,
		}
	}

	first := buildPackage(firstPath, "2.0")
	second := buildPackage(secondPath, "1.0")
	publishedAt := time.Date(2026, time.September, 12, 18, 0, 0, 0, time.UTC)
	outputs := []string{filepath.Join(tmpDir, "one"), filepath.Join(tmpDir, "two")}
	inputs := [][]models.Package{{first, second}, {second, first}}
	for index := range outputs {
		config := &models.RepositoryConfig{
			OutputDir: outputs[index], Origin: "Test", Label: "Test",
			Codename: "trixie", Suite: "trixie",
			Components: []string{"main"}, Arches: []string{"amd64"},
		}
		if err := NewGeneratorWithClock(nil, func() time.Time { return publishedAt }).Generate(
			context.Background(), config, inputs[index],
		); err != nil {
			t.Fatal(err)
		}
	}

	relativePaths := []string{
		filepath.Join("dists", "trixie", "main", "binary-amd64", "Packages"),
		filepath.Join("dists", "trixie", "main", "binary-amd64", "Packages.gz"),
		filepath.Join("dists", "trixie", "Release"),
		filepath.Join("dists", "trixie", "InRelease"),
	}
	for _, relativePath := range relativePaths {
		firstData, err := os.ReadFile(filepath.Join(outputs[0], relativePath))
		if err != nil {
			t.Fatal(err)
		}
		secondData, err := os.ReadFile(filepath.Join(outputs[1], relativePath))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(firstData, secondData) {
			t.Errorf("shuffled input changed %s", relativePath)
		}
	}
}

func TestGenerateShuffledIdenticalPoolDestinationProducesIdenticalRepository(t *testing.T) {
	tmpDir := t.TempDir()
	firstInputDir := filepath.Join(tmpDir, "input-one")
	secondInputDir := filepath.Join(tmpDir, "input-two")
	for _, dir := range []string{firstInputDir, secondInputDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	firstPath := filepath.Join(firstInputDir, "same.deb")
	secondPath := filepath.Join(secondInputDir, "same.deb")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, []byte("identical package bytes"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	buildPackage := func(path, version string) models.Package {
		checksums, err := utils.CalculateChecksums(path)
		if err != nil {
			t.Fatal(err)
		}
		return models.Package{
			Name: "same", Version: version, Architecture: "amd64", Filename: path,
			Size: checksums.Size, MD5Sum: checksums.MD5, SHA1Sum: checksums.SHA1,
			SHA256Sum: checksums.SHA256, SHA512Sum: checksums.SHA512,
		}
	}
	first := buildPackage(firstPath, "2.0")
	second := buildPackage(secondPath, "1.0")
	publishedAt := time.Date(2026, time.September, 12, 18, 0, 0, 0, time.UTC)
	outputs := []string{filepath.Join(tmpDir, "one"), filepath.Join(tmpDir, "two")}
	inputs := [][]models.Package{{first, second}, {second, first}}
	for index := range outputs {
		config := &models.RepositoryConfig{
			OutputDir: outputs[index], Origin: "Test", Label: "Test",
			Codename: "trixie", Suite: "trixie",
			Components: []string{"main"}, Arches: []string{"amd64"},
		}
		if err := NewGeneratorWithClock(nil, func() time.Time { return publishedAt }).Generate(
			context.Background(), config, inputs[index],
		); err != nil {
			t.Fatal(err)
		}
	}

	relativePaths := []string{
		filepath.Join("pool", "main", "s", "same", "same.deb"),
		filepath.Join("dists", "trixie", "main", "binary-amd64", "Packages"),
		filepath.Join("dists", "trixie", "main", "binary-amd64", "Packages.gz"),
		filepath.Join("dists", "trixie", "Release"),
		filepath.Join("dists", "trixie", "InRelease"),
	}
	for _, relativePath := range relativePaths {
		firstData, err := os.ReadFile(filepath.Join(outputs[0], relativePath))
		if err != nil {
			t.Fatal(err)
		}
		secondData, err := os.ReadFile(filepath.Join(outputs[1], relativePath))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(firstData, secondData) {
			t.Errorf("shuffled identical pool destination changed %s", relativePath)
		}
	}
}

func TestGenerateRejectsConflictingPoolDestinationBeforeWriting(t *testing.T) {
	tmpDir := t.TempDir()
	firstInputDir := filepath.Join(tmpDir, "input-one")
	secondInputDir := filepath.Join(tmpDir, "input-two")
	for _, dir := range []string{firstInputDir, secondInputDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	firstPath := filepath.Join(firstInputDir, "same.deb")
	secondPath := filepath.Join(secondInputDir, "same.deb")
	if err := os.WriteFile(firstPath, []byte("first package bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte("second package bytes"), 0644); err != nil {
		t.Fatal(err)
	}

	buildPackage := func(path, version string) models.Package {
		checksums, err := utils.CalculateChecksums(path)
		if err != nil {
			t.Fatal(err)
		}
		return models.Package{
			Name: "same", Version: version, Architecture: "amd64", Filename: path,
			Size: checksums.Size, MD5Sum: checksums.MD5, SHA1Sum: checksums.SHA1,
			SHA256Sum: checksums.SHA256, SHA512Sum: checksums.SHA512,
		}
	}
	first := buildPackage(firstPath, "2.0")
	second := buildPackage(secondPath, "1.0")
	second.Size = first.Size
	second.SHA256Sum = first.SHA256Sum

	for name, packages := range map[string][]models.Package{
		"forward": {first, second},
		"reverse": {second, first},
	} {
		t.Run(name, func(t *testing.T) {
			outputDir := filepath.Join(tmpDir, name)
			config := &models.RepositoryConfig{
				OutputDir: outputDir, Origin: "Test", Label: "Test",
				Codename: "trixie", Suite: "trixie",
				Components: []string{"main"}, Arches: []string{"amd64"},
			}

			err := NewGenerator(nil).Generate(context.Background(), config, packages)
			if err == nil {
				t.Fatal("Generate() accepted conflicting package bytes for one pool destination")
			}
			if !strings.Contains(err.Error(), "conflicting package contents for pool destination") {
				t.Fatalf("Generate() returned unexpected error: %v", err)
			}
			if _, statErr := os.Stat(outputDir); !os.IsNotExist(statErr) {
				t.Fatalf("Generate() mutated output before rejecting collision: %v", statErr)
			}
		})
	}
}

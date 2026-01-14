package sysext

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/frostyard/repogen/internal/models"
)

func TestParsePackage(t *testing.T) {
	tests := []struct {
		name        string
		filename    string
		wantName    string
		wantVersion string
		wantArch    string
		wantErr     bool
	}{
		{
			name:        "basic .raw file",
			filename:    "myext_1.0_x86-64.raw",
			wantName:    "myext",
			wantVersion: "1.0",
			wantArch:    "x86-64",
			wantErr:     false,
		},
		{
			name:        "zstd compressed",
			filename:    "docker_24.0.5_x86-64.raw.zst",
			wantName:    "docker",
			wantVersion: "24.0.5",
			wantArch:    "x86-64",
			wantErr:     false,
		},
		{
			name:        "xz compressed",
			filename:    "nvidia_550.54.14_arm64.raw.xz",
			wantName:    "nvidia",
			wantVersion: "550.54.14",
			wantArch:    "arm64",
			wantErr:     false,
		},
		{
			name:        "gzip compressed",
			filename:    "podman_5.0_x86-64.raw.gz",
			wantName:    "podman",
			wantVersion: "5.0",
			wantArch:    "x86-64",
			wantErr:     false,
		},
		{
			name:     "missing underscores",
			filename: "myext.raw",
			wantErr:  true,
		},
		{
			name:     "only one underscore (missing arch)",
			filename: "myext_1.0.raw",
			wantErr:  true,
		},
		{
			name:     "too many underscores",
			filename: "my_ext_1.0_x86-64.raw",
			wantErr:  true,
		},
		{
			name:     "empty name",
			filename: "_1.0_x86-64.raw",
			wantErr:  true,
		},
		{
			name:     "empty version",
			filename: "myext__x86-64.raw",
			wantErr:  true,
		},
		{
			name:     "empty arch",
			filename: "myext_1.0_.raw",
			wantErr:  true,
		},
		{
			name:     "no .raw extension",
			filename: "myext_1.0_x86-64.tar.gz",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp file
			tmpDir, err := os.MkdirTemp("", "repogen-test-sysext-")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()

			filePath := filepath.Join(tmpDir, tt.filename)
			err = os.WriteFile(filePath, []byte("fake sysext content"), 0644)
			if err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			pkg, err := ParsePackage(filePath)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ParsePackage() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("ParsePackage() unexpected error: %v", err)
			}

			if pkg.Name != tt.wantName {
				t.Errorf("ParsePackage() Name = %q, want %q", pkg.Name, tt.wantName)
			}

			if pkg.Version != tt.wantVersion {
				t.Errorf("ParsePackage() Version = %q, want %q", pkg.Version, tt.wantVersion)
			}

			if pkg.Architecture != tt.wantArch {
				t.Errorf("ParsePackage() Architecture = %q, want %q", pkg.Architecture, tt.wantArch)
			}

			// Verify checksums were calculated
			if pkg.SHA256Sum == "" {
				t.Error("ParsePackage() SHA256Sum should not be empty")
			}
		})
	}
}

func TestGeneratorGenerate(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "repogen-test-sysext-gen-")
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

	// Create test sysext files
	ext1 := filepath.Join(inputDir, "myext_1.0_x86-64.raw")
	ext2 := filepath.Join(inputDir, "myext_2.0_x86-64.raw.zst")
	ext3 := filepath.Join(inputDir, "other_1.0_x86-64.raw")

	if err := os.WriteFile(ext1, []byte("sysext content v1"), 0644); err != nil {
		t.Fatalf("Failed to write ext1: %v", err)
	}
	if err := os.WriteFile(ext2, []byte("sysext content v2 compressed"), 0644); err != nil {
		t.Fatalf("Failed to write ext2: %v", err)
	}
	if err := os.WriteFile(ext3, []byte("other ext content"), 0644); err != nil {
		t.Fatalf("Failed to write ext3: %v", err)
	}

	gen := NewGenerator("https://example.com/repo")
	config := &models.RepositoryConfig{
		OutputDir: outputDir,
	}

	packages := []models.Package{
		{Name: "myext", Version: "1.0", Architecture: "x86-64", Filename: ext1, SHA256Sum: "abc123"},
		{Name: "myext", Version: "2.0", Architecture: "x86-64", Filename: ext2, SHA256Sum: "def456"},
		{Name: "other", Version: "1.0", Architecture: "x86-64", Filename: ext3, SHA256Sum: "ghi789"},
	}

	err = gen.Generate(context.Background(), config, packages)
	if err != nil {
		t.Fatalf("Generate() failed: %v", err)
	}

	// Verify directory structure
	myextDir := filepath.Join(outputDir, "ext", "myext")
	otherDir := filepath.Join(outputDir, "ext", "other")

	if _, err := os.Stat(myextDir); os.IsNotExist(err) {
		t.Errorf("myext directory not created")
	}
	if _, err := os.Stat(otherDir); os.IsNotExist(err) {
		t.Errorf("other directory not created")
	}

	// Verify files were copied
	if _, err := os.Stat(filepath.Join(myextDir, "myext_1.0_x86-64.raw")); os.IsNotExist(err) {
		t.Errorf("myext_1.0_x86-64.raw not copied")
	}
	if _, err := os.Stat(filepath.Join(myextDir, "myext_2.0_x86-64.raw.zst")); os.IsNotExist(err) {
		t.Errorf("myext_2.0_x86-64.raw.zst not copied")
	}
	if _, err := os.Stat(filepath.Join(otherDir, "other_1.0_x86-64.raw")); os.IsNotExist(err) {
		t.Errorf("other_1.0_x86-64.raw not copied")
	}

	// Verify SHA256SUMS files exist
	myextSums := filepath.Join(myextDir, "SHA256SUMS")
	otherSums := filepath.Join(otherDir, "SHA256SUMS")

	if _, err := os.Stat(myextSums); os.IsNotExist(err) {
		t.Errorf("myext SHA256SUMS not created")
	}
	if _, err := os.Stat(otherSums); os.IsNotExist(err) {
		t.Errorf("other SHA256SUMS not created")
	}

	// Verify SHA256SUMS format (should have two entries for myext)
	myextSumsContent, err := os.ReadFile(myextSums)
	if err != nil {
		t.Fatalf("Failed to read myext SHA256SUMS: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(string(myextSumsContent)), "\n")
	if len(lines) != 2 {
		t.Errorf("myext SHA256SUMS should have 2 lines, got %d", len(lines))
	}

	// Verify format: "<hash>  <filename>"
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			t.Errorf("Invalid SHA256SUMS line format: %q", line)
		}
		// Should have two spaces between hash and filename
		if !strings.Contains(line, "  ") {
			t.Errorf("SHA256SUMS should use two spaces between hash and filename: %q", line)
		}
	}

	// Verify transfer files exist
	myextTransfer := filepath.Join(myextDir, "myext.transfer")
	otherTransfer := filepath.Join(otherDir, "other.transfer")

	if _, err := os.Stat(myextTransfer); os.IsNotExist(err) {
		t.Errorf("myext.transfer not created")
	}
	if _, err := os.Stat(otherTransfer); os.IsNotExist(err) {
		t.Errorf("other.transfer not created")
	}

	// Verify transfer file content
	transferContent, err := os.ReadFile(myextTransfer)
	if err != nil {
		t.Fatalf("Failed to read myext.transfer: %v", err)
	}

	transferStr := string(transferContent)
	if !strings.Contains(transferStr, "[Transfer]") {
		t.Error("Transfer file missing [Transfer] section")
	}
	if !strings.Contains(transferStr, "[Source]") {
		t.Error("Transfer file missing [Source] section")
	}
	if !strings.Contains(transferStr, "[Target]") {
		t.Error("Transfer file missing [Target] section")
	}
	if !strings.Contains(transferStr, "https://example.com/repo/ext/myext/") {
		t.Errorf("Transfer file missing correct source URL, got: %s", transferStr)
	}
	if !strings.Contains(transferStr, "myext_@v_@a.raw") {
		t.Errorf("Transfer file missing pattern with version/arch placeholders, got: %s", transferStr)
	}
	if !strings.Contains(transferStr, "Path=/var/lib/extensions.d/") {
		t.Errorf("Transfer file missing correct target path, got: %s", transferStr)
	}
	if !strings.Contains(transferStr, "CurrentSymlink=myext.raw") {
		t.Errorf("Transfer file missing CurrentSymlink, got: %s", transferStr)
	}

	// Verify index file exists and contains correct extensions
	indexPath := filepath.Join(outputDir, "ext", "index")
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		t.Errorf("index file not created")
	}

	indexContent, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("Failed to read index file: %v", err)
	}

	indexStr := string(indexContent)
	// Index should be sorted alphabetically
	expectedIndex := "myext\nother\n"
	if indexStr != expectedIndex {
		t.Errorf("index file content = %q, want %q", indexStr, expectedIndex)
	}
}

func TestIncrementalMode(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "repogen-test-sysext-incr-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	inputDir := filepath.Join(tmpDir, "input")
	outputDir := filepath.Join(tmpDir, "output")
	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatalf("Failed to create input dir: %v", err)
	}

	gen := NewGenerator("https://example.com/repo")
	config := &models.RepositoryConfig{
		OutputDir: outputDir,
	}

	// Step 1: Create initial repo with v1.0
	ext1 := filepath.Join(inputDir, "myext_1.0_x86-64.raw")
	if err := os.WriteFile(ext1, []byte("sysext content v1"), 0644); err != nil {
		t.Fatalf("Failed to write ext1: %v", err)
	}

	packagesV1 := []models.Package{
		{Name: "myext", Version: "1.0", Architecture: "x86-64", Filename: ext1, SHA256Sum: "abc123"},
	}

	err = gen.Generate(context.Background(), config, packagesV1)
	if err != nil {
		t.Fatalf("Initial generation failed: %v", err)
	}

	// Verify initial SHA256SUMS
	sha256sumsPath := filepath.Join(outputDir, "ext", "myext", "SHA256SUMS")
	initialContent, _ := os.ReadFile(sha256sumsPath)
	if !strings.Contains(string(initialContent), "myext_1.0_x86-64.raw") {
		t.Errorf("Initial SHA256SUMS should contain myext_1.0_x86-64.raw")
	}

	// Step 2: Parse existing metadata
	existingPackages, err := gen.ParseExistingMetadata(config)
	if err != nil {
		t.Fatalf("ParseExistingMetadata() failed: %v", err)
	}

	if len(existingPackages) != 1 {
		t.Fatalf("Expected 1 existing package, got %d", len(existingPackages))
	}

	if existingPackages[0].Name != "myext" || existingPackages[0].Version != "1.0" {
		t.Errorf("Existing package mismatch: %+v", existingPackages[0])
	}

	// Step 3: Add new version
	ext2 := filepath.Join(inputDir, "myext_2.0_x86-64.raw.zst")
	_ = os.WriteFile(ext2, []byte("sysext content v2"), 0644)

	packagesV2 := []models.Package{
		{Name: "myext", Version: "2.0", Architecture: "x86-64", Filename: ext2, SHA256Sum: "def456"},
	}

	// Combine existing + new
	allPackages := append(existingPackages, packagesV2...)

	err = gen.Generate(context.Background(), config, allPackages)
	if err != nil {
		t.Fatalf("Incremental generation failed: %v", err)
	}

	// Verify updated SHA256SUMS contains both versions
	updatedContent, _ := os.ReadFile(sha256sumsPath)
	if !strings.Contains(string(updatedContent), "myext_1.0_x86-64.raw") {
		t.Errorf("Updated SHA256SUMS should still contain myext_1.0_x86-64.raw")
	}
	if !strings.Contains(string(updatedContent), "myext_2.0_x86-64.raw.zst") {
		t.Errorf("Updated SHA256SUMS should contain myext_2.0_x86-64.raw.zst")
	}

	lines := strings.Split(strings.TrimSpace(string(updatedContent)), "\n")
	if len(lines) != 2 {
		t.Errorf("Updated SHA256SUMS should have 2 lines, got %d: %s", len(lines), updatedContent)
	}

	// Verify index file still contains myext
	indexPath := filepath.Join(outputDir, "ext", "index")
	indexContent, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("Failed to read index file: %v", err)
	}
	if string(indexContent) != "myext\n" {
		t.Errorf("Index file content = %q, want %q", string(indexContent), "myext\n")
	}
}

func TestIndexUpdatedWithNewExtension(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "repogen-test-sysext-index-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	inputDir := filepath.Join(tmpDir, "input")
	outputDir := filepath.Join(tmpDir, "output")
	_ = os.MkdirAll(inputDir, 0755)

	gen := NewGenerator("https://example.com/repo")
	config := &models.RepositoryConfig{
		OutputDir: outputDir,
	}

	// Step 1: Create initial repo with one extension
	ext1 := filepath.Join(inputDir, "alpha_1.0_x86-64.raw")
	_ = os.WriteFile(ext1, []byte("alpha content"), 0644)

	packagesAlpha := []models.Package{
		{Name: "alpha", Version: "1.0", Architecture: "x86-64", Filename: ext1, SHA256Sum: "abc123"},
	}

	err = gen.Generate(context.Background(), config, packagesAlpha)
	if err != nil {
		t.Fatalf("Initial generation failed: %v", err)
	}

	// Verify initial index
	indexPath := filepath.Join(outputDir, "ext", "index")
	initialIndex, _ := os.ReadFile(indexPath)
	if string(initialIndex) != "alpha\n" {
		t.Errorf("Initial index = %q, want %q", string(initialIndex), "alpha\n")
	}

	// Step 2: Add a new extension (beta) that comes before alpha alphabetically
	ext2 := filepath.Join(inputDir, "beta_1.0_x86-64.raw")
	_ = os.WriteFile(ext2, []byte("beta content"), 0644)

	// Parse existing and combine with new
	existingPackages, _ := gen.ParseExistingMetadata(config)

	packagesBeta := []models.Package{
		{Name: "beta", Version: "1.0", Architecture: "x86-64", Filename: ext2, SHA256Sum: "def456"},
	}
	allPackages := append(existingPackages, packagesBeta...)

	err = gen.Generate(context.Background(), config, allPackages)
	if err != nil {
		t.Fatalf("Incremental generation failed: %v", err)
	}

	// Verify index is updated and sorted
	updatedIndex, _ := os.ReadFile(indexPath)
	expectedIndex := "alpha\nbeta\n"
	if string(updatedIndex) != expectedIndex {
		t.Errorf("Updated index = %q, want %q", string(updatedIndex), expectedIndex)
	}
}

func TestValidatePackages(t *testing.T) {
	gen := NewGenerator("https://example.com/repo")

	tests := []struct {
		name     string
		packages []models.Package
		wantErr  bool
	}{
		{
			name: "valid packages",
			packages: []models.Package{
				{Name: "ext1", Version: "1.0", Filename: "/path/to/ext1_1.0_x86-64.raw"},
				{Name: "ext2", Version: "2.0", Filename: "/path/to/ext2_2.0_arm64.raw"},
			},
			wantErr: false,
		},
		{
			name: "missing name",
			packages: []models.Package{
				{Name: "", Version: "1.0", Filename: "/path/to/test.raw"},
			},
			wantErr: true,
		},
		{
			name: "missing version",
			packages: []models.Package{
				{Name: "ext1", Version: "", Filename: "/path/to/test.raw"},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := gen.ValidatePackages(tt.packages)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePackages() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidatePackagesMissingBaseURL(t *testing.T) {
	gen := NewGenerator("") // Empty base URL

	packages := []models.Package{
		{Name: "ext1", Version: "1.0", Filename: "/path/to/ext1_1.0_x86-64.raw"},
	}

	err := gen.ValidatePackages(packages)
	if err == nil {
		t.Error("ValidatePackages() should error when base URL is missing")
	}

	if !strings.Contains(err.Error(), "--base-url") {
		t.Errorf("Error should mention --base-url, got: %v", err)
	}
}

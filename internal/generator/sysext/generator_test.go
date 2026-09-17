package sysext

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/frostyard/repogen/internal/models"
	"github.com/frostyard/repogen/internal/utils"
)

type testSigner struct {
	mu         sync.Mutex
	entity     *openpgp.Entity
	signedPath string
}

func newTestSigner(t *testing.T) *testSigner {
	t.Helper()
	entity, err := openpgp.NewEntity("Repogen Test", "", "repogen@example.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	return &testSigner{entity: entity}
}

func (s *testSigner) SignCleartext([]byte) ([]byte, error) { return nil, errors.New("unused") }
func (s *testSigner) SignDetached(data []byte) ([]byte, error) {
	return s.SignDetachedBinary(data)
}
func (s *testSigner) SignDetachedBinary(data []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var signature bytes.Buffer
	if err := openpgp.DetachSign(&signature, s.entity, bytes.NewReader(data), nil); err != nil {
		return nil, err
	}
	return signature.Bytes(), nil
}
func (s *testSigner) GetPublicKey() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var publicKey bytes.Buffer
	if err := s.entity.Serialize(&publicKey); err != nil {
		return nil, err
	}
	return publicKey.Bytes(), nil
}
func (s *testSigner) SignDetachedBinaryFromFile(path string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.signedPath = path
	var signature bytes.Buffer
	if err := openpgp.DetachSign(&signature, s.entity, bytes.NewReader(content), nil); err != nil {
		return nil, err
	}
	return signature.Bytes(), nil
}

func TestParsePackage(t *testing.T) {
	tests := []struct {
		name        string
		filename    string
		wantName    string
		wantVersion string
		wantOSVer   string
		wantArch    string
		wantErr     bool
	}{
		{
			name:        "basic .raw file",
			filename:    "myext_1.0_13_x86-64.raw",
			wantName:    "myext",
			wantVersion: "1.0",
			wantOSVer:   "13",
			wantArch:    "x86-64",
			wantErr:     false,
		},
		{
			name:        "zstd compressed",
			filename:    "docker_24.0.5_13_x86-64.raw.zst",
			wantName:    "docker",
			wantVersion: "24.0.5",
			wantOSVer:   "13",
			wantArch:    "x86-64",
			wantErr:     false,
		},
		{
			name:        "xz compressed",
			filename:    "nvidia_550.54.14_12_arm64.raw.xz",
			wantName:    "nvidia",
			wantVersion: "550.54.14",
			wantOSVer:   "12",
			wantArch:    "arm64",
			wantErr:     false,
		},
		{
			name:        "gzip compressed",
			filename:    "podman_5.0_13_x86-64.raw.gz",
			wantName:    "podman",
			wantVersion: "5.0",
			wantOSVer:   "13",
			wantArch:    "x86-64",
			wantErr:     false,
		},
		{
			name:     "missing underscores",
			filename: "myext.raw",
			wantErr:  true,
		},
		{
			name:     "too few parts and wrong arch (old 3-part format)",
			filename: "myext_1.0_amd64.raw",
			wantErr:  true,
		},
		{
			name:     "too few parts (only 1 underscore)",
			filename: "myext_1.0.raw",
			wantErr:  true,
		},
		{
			name:     "too many underscores (5+ parts)",
			filename: "my_ext_1.0_13_x86-64.raw",
			wantErr:  true,
		},
		{
			name:     "empty name",
			filename: "_1.0_13_x86-64.raw",
			wantErr:  true,
		},
		{
			name:     "empty version",
			filename: "myext__13_x86-64.raw",
			wantErr:  true,
		},
		{
			name:     "empty OS version",
			filename: "myext_1.0__x86-64.raw",
			wantErr:  true,
		},
		{
			name:     "empty arch",
			filename: "myext_1.0_13_.raw",
			wantErr:  true,
		},
		{
			name:     "no .raw extension",
			filename: "myext_1.0_13_x86-64.tar.gz",
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

			// Verify OSVersion is stored in Metadata
			if osver, ok := pkg.Metadata["OSVersion"]; !ok {
				t.Error("ParsePackage() OSVersion not found in Metadata")
			} else if osver != tt.wantOSVer {
				t.Errorf("ParsePackage() Metadata[OSVersion] = %q, want %q", osver, tt.wantOSVer)
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

	// Create test sysext files with 4-part format
	ext1 := filepath.Join(inputDir, "myext_1.0_13_x86-64.raw")
	ext2 := filepath.Join(inputDir, "myext_2.0_13_x86-64.raw.zst")
	ext3 := filepath.Join(inputDir, "other_1.0_13_x86-64.raw")

	if err := os.WriteFile(ext1, []byte("sysext content v1"), 0644); err != nil {
		t.Fatalf("Failed to write ext1: %v", err)
	}
	if err := os.WriteFile(ext2, []byte("sysext content v2 compressed"), 0644); err != nil {
		t.Fatalf("Failed to write ext2: %v", err)
	}
	if err := os.WriteFile(ext3, []byte("other ext content"), 0644); err != nil {
		t.Fatalf("Failed to write ext3: %v", err)
	}

	gen := NewGenerator("https://example.com/repo", nil)
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

	// Verify files were copied with 4-part names
	if _, err := os.Stat(filepath.Join(myextDir, "myext_1.0_13_x86-64.raw")); os.IsNotExist(err) {
		t.Errorf("myext_1.0_13_x86-64.raw not copied")
	}
	if _, err := os.Stat(filepath.Join(myextDir, "myext_2.0_13_x86-64.raw.zst")); os.IsNotExist(err) {
		t.Errorf("myext_2.0_13_x86-64.raw.zst not copied")
	}
	if _, err := os.Stat(filepath.Join(otherDir, "other_1.0_13_x86-64.raw")); os.IsNotExist(err) {
		t.Errorf("other_1.0_13_x86-64.raw not copied")
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
	// Verify new pattern with %w and %a specifiers
	if !strings.Contains(transferStr, "myext_@v_%w_%a.raw") {
		t.Errorf("Transfer file missing pattern with version/osversion/arch placeholders, got: %s", transferStr)
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

func TestGeneratorSignsChecksumManifest(t *testing.T) {
	tmpDir := t.TempDir()
	inputDir := filepath.Join(tmpDir, "input")
	outputDir := filepath.Join(tmpDir, "output")
	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatal(err)
	}

	imagePath := filepath.Join(inputDir, "myext_1.0_13_x86-64.raw")
	if err := os.WriteFile(imagePath, []byte("signed sysext"), 0644); err != nil {
		t.Fatal(err)
	}

	metadataSigner := newTestSigner(t)
	gen := NewGenerator("https://example.com/repo", metadataSigner)
	config := &models.RepositoryConfig{OutputDir: outputDir}
	packages := []models.Package{{
		Name: "myext", Version: "1.0", Architecture: "x86-64",
		Filename: imagePath, SHA256Sum: "placeholder",
	}}

	if err := gen.Generate(context.Background(), config, packages); err != nil {
		t.Fatalf("Generate() failed: %v", err)
	}

	extDir := filepath.Join(outputDir, "ext", "myext")
	manifestPath := filepath.Join(extDir, "SHA256SUMS")
	if !strings.HasSuffix(metadataSigner.signedPath, filepath.Join("ext", "myext", "SHA256SUMS")) {
		t.Errorf("signed path = %q, want staged ext/myext/SHA256SUMS", metadataSigner.signedPath)
	}

	signature, err := os.ReadFile(manifestPath + ".gpg")
	if err != nil {
		t.Fatalf("reading SHA256SUMS.gpg: %v", err)
	}
	if len(signature) == 0 {
		t.Error("signature is empty")
	}

	transfer, err := os.ReadFile(filepath.Join(extDir, "myext.transfer"))
	if err != nil {
		t.Fatalf("reading transfer: %v", err)
	}
	if !strings.Contains(string(transfer), "Verify=true") {
		t.Errorf("signed transfer does not enable verification:\n%s", transfer)
	}

}

func TestGeneratorCanonicalChecksumOrder(t *testing.T) {
	tmpDir := t.TempDir()
	inputDir := filepath.Join(tmpDir, "input")
	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatal(err)
	}
	alpha := filepath.Join(inputDir, "same_1.0_13_x86-64.raw")
	beta := filepath.Join(inputDir, "same_2.0_13_x86-64.raw")
	if err := os.WriteFile(alpha, []byte("alpha"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(beta, []byte("beta"), 0644); err != nil {
		t.Fatal(err)
	}
	first := models.Package{Name: "same", Version: "1.0", Architecture: "x86-64", Filename: alpha}
	second := models.Package{Name: "same", Version: "2.0", Architecture: "x86-64", Filename: beta}

	outputOne := filepath.Join(tmpDir, "one")
	outputTwo := filepath.Join(tmpDir, "two")
	if err := NewGenerator("https://example.com/repo", nil).Generate(
		context.Background(),
		&models.RepositoryConfig{OutputDir: outputOne},
		[]models.Package{second, first},
	); err != nil {
		t.Fatal(err)
	}
	if err := NewGenerator("https://example.com/repo", nil).Generate(
		context.Background(),
		&models.RepositoryConfig{OutputDir: outputTwo},
		[]models.Package{first, second},
	); err != nil {
		t.Fatal(err)
	}

	firstManifest, err := os.ReadFile(filepath.Join(outputOne, "ext", "same", "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	secondManifest, err := os.ReadFile(filepath.Join(outputTwo, "ext", "same", "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if string(firstManifest) != string(secondManifest) {
		t.Fatalf("shuffled sysext input produced different manifests:\n%s\n---\n%s", firstManifest, secondManifest)
	}
	if strings.Index(string(firstManifest), filepath.Base(alpha)) > strings.Index(string(firstManifest), filepath.Base(beta)) {
		t.Fatalf("SHA256SUMS entries are not sorted:\n%s", firstManifest)
	}
}

func TestGenerateRejectsConflictingDestinationBeforeWriting(t *testing.T) {
	tmpDir := t.TempDir()
	firstInputDir := filepath.Join(tmpDir, "input-one")
	secondInputDir := filepath.Join(tmpDir, "input-two")
	for _, dir := range []string{firstInputDir, secondInputDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}

	filename := "same_1.0_13_x86-64.raw"
	firstPath := filepath.Join(firstInputDir, filename)
	secondPath := filepath.Join(secondInputDir, filename)
	if err := os.WriteFile(firstPath, []byte("first sysext bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte("second sysext bytes"), 0644); err != nil {
		t.Fatal(err)
	}
	first, err := ParsePackage(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParsePackage(secondPath)
	if err != nil {
		t.Fatal(err)
	}

	for name, packages := range map[string][]models.Package{
		"forward": {*first, *second},
		"reverse": {*second, *first},
	} {
		t.Run(name, func(t *testing.T) {
			outputDir := filepath.Join(tmpDir, name)
			extDir := filepath.Join(outputDir, "ext", "same")
			if err := os.MkdirAll(extDir, 0755); err != nil {
				t.Fatal(err)
			}

			published := []byte("published sysext bytes")
			publishedPath := filepath.Join(extDir, filename)
			if err := os.WriteFile(publishedPath, published, 0644); err != nil {
				t.Fatal(err)
			}
			checksums, err := utils.CalculateChecksums(publishedPath)
			if err != nil {
				t.Fatal(err)
			}
			manifest := []byte(checksums.SHA256 + "  " + filename + "\n")
			manifestPath := filepath.Join(extDir, "SHA256SUMS")
			if err := os.WriteFile(manifestPath, manifest, 0644); err != nil {
				t.Fatal(err)
			}
			signature := []byte("published signature")
			signaturePath := manifestPath + ".gpg"
			if err := os.WriteFile(signaturePath, signature, 0644); err != nil {
				t.Fatal(err)
			}

			err = NewGenerator("https://example.com/repo", nil).Generate(
				context.Background(),
				&models.RepositoryConfig{OutputDir: outputDir},
				packages,
			)
			if err == nil {
				t.Fatal("Generate() accepted conflicting sysext bytes for one destination")
			}
			if !strings.Contains(err.Error(), "conflicting package contents for sysext destination") &&
				!strings.Contains(err.Error(), "conflicting sysext artifacts for identity") {
				t.Errorf("Generate() returned unexpected error: %v", err)
			}
			for path, want := range map[string][]byte{
				publishedPath: published,
				manifestPath:  manifest,
				signaturePath: signature,
			} {
				got, readErr := os.ReadFile(path)
				if readErr != nil {
					t.Fatalf("reading preserved output %s: %v", path, readErr)
				}
				if string(got) != string(want) {
					t.Fatalf("Generate() changed %s before rejecting collision: got %q, want %q", path, got, want)
				}
			}
			if _, statErr := os.Stat(filepath.Join(outputDir, "ext", "index")); !os.IsNotExist(statErr) {
				t.Fatalf("Generate() wrote index before rejecting collision: %v", statErr)
			}
		})
	}
}

func TestGenerateAllowsIdenticalDuplicateDestination(t *testing.T) {
	tmpDir := t.TempDir()
	var packages []models.Package
	filename := "same_1.0_13_x86-64.raw"
	for _, inputName := range []string{"input-one", "input-two"} {
		inputDir := filepath.Join(tmpDir, inputName)
		if err := os.MkdirAll(inputDir, 0755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(inputDir, filename)
		if err := os.WriteFile(path, []byte("identical sysext bytes"), 0644); err != nil {
			t.Fatal(err)
		}
		pkg, err := ParsePackage(path)
		if err != nil {
			t.Fatal(err)
		}
		packages = append(packages, *pkg)
	}

	outputDir := filepath.Join(tmpDir, "output")
	if err := NewGenerator("https://example.com/repo", nil).Generate(
		context.Background(),
		&models.RepositoryConfig{OutputDir: outputDir},
		packages,
	); err != nil {
		t.Fatalf("Generate() rejected identical duplicate sysext bytes: %v", err)
	}

	manifest, err := os.ReadFile(filepath.Join(outputDir, "ext", "same", "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Split(strings.TrimSpace(string(manifest)), "\n"); len(lines) != 1 {
		t.Fatalf("SHA256SUMS has %d entries for an identical duplicate, want 1:\n%s", len(lines), manifest)
	}
}

func TestGenerateRejectsUnverifiableDuplicateDestination(t *testing.T) {
	outputDir := filepath.Join(t.TempDir(), "output")
	filename := "same_1.0_13_x86-64.raw"
	packages := []models.Package{
		{Name: "same", Version: "1.0", Architecture: "x86-64", Filename: filename},
		{Name: "same", Version: "1.0", Architecture: "x86-64", Filename: filename},
	}

	err := NewGenerator("https://example.com/repo", nil).Generate(
		context.Background(),
		&models.RepositoryConfig{OutputDir: outputDir},
		packages,
	)
	if err == nil {
		t.Fatal("Generate() accepted an unverifiable duplicate sysext destination")
	}
	if !strings.Contains(err.Error(), "cannot verify duplicate sysext destination") &&
		!strings.Contains(err.Error(), "conflicting sysext artifacts for identity") {
		t.Fatalf("Generate() returned unexpected error: %v", err)
	}
	if _, statErr := os.Stat(outputDir); !os.IsNotExist(statErr) {
		t.Fatalf("Generate() mutated output before rejecting unverifiable duplicate: %v", statErr)
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

	gen := NewGenerator("https://example.com/repo", nil)
	config := &models.RepositoryConfig{
		OutputDir: outputDir,
	}

	// Step 1: Create initial repo with v1.0
	ext1 := filepath.Join(inputDir, "myext_1.0_13_x86-64.raw")
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
	if !strings.Contains(string(initialContent), "myext_1.0_13_x86-64.raw") {
		t.Errorf("Initial SHA256SUMS should contain myext_1.0_13_x86-64.raw")
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
	ext2 := filepath.Join(inputDir, "myext_2.0_13_x86-64.raw.zst")
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
	if !strings.Contains(string(updatedContent), "myext_1.0_13_x86-64.raw") {
		t.Errorf("Updated SHA256SUMS should still contain myext_1.0_13_x86-64.raw")
	}
	if !strings.Contains(string(updatedContent), "myext_2.0_13_x86-64.raw.zst") {
		t.Errorf("Updated SHA256SUMS should contain myext_2.0_13_x86-64.raw.zst")
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

	gen := NewGenerator("https://example.com/repo", nil)
	config := &models.RepositoryConfig{
		OutputDir: outputDir,
	}

	// Step 1: Create initial repo with one extension
	ext1 := filepath.Join(inputDir, "alpha_1.0_13_x86-64.raw")
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
	ext2 := filepath.Join(inputDir, "beta_1.0_13_x86-64.raw")
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

func TestIncrementalIndexIncludesExtensionsFromExistingManifests(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "output")
	alphaPath := filepath.Join(tmpDir, "alpha_1.0_13_x86-64.raw")
	if err := os.WriteFile(alphaPath, []byte("alpha content"), 0o644); err != nil {
		t.Fatal(err)
	}
	alpha, err := ParsePackage(alphaPath)
	if err != nil {
		t.Fatal(err)
	}
	gen := NewGenerator("https://example.com/repo", nil)
	if err := gen.Generate(
		context.Background(),
		&models.RepositoryConfig{OutputDir: outputDir},
		[]models.Package{*alpha},
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(outputDir, "ext", "alpha", filepath.Base(alphaPath))); err != nil {
		t.Fatal(err)
	}

	betaPath := filepath.Join(tmpDir, "beta_1.0_13_x86-64.raw")
	if err := os.WriteFile(betaPath, []byte("beta content"), 0644); err != nil {
		t.Fatal(err)
	}

	config := &models.RepositoryConfig{
		OutputDir:   outputDir,
		Incremental: true,
	}
	packages := []models.Package{{
		Name:         "beta",
		Version:      "1.0",
		Architecture: "x86-64",
		Filename:     betaPath,
		SHA256Sum:    "def456",
	}}

	if err := gen.Generate(context.Background(), config, packages); err != nil {
		t.Fatalf("Generate() failed: %v", err)
	}

	index, err := os.ReadFile(filepath.Join(outputDir, "ext", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(index), "alpha\nbeta\n"; got != want {
		t.Errorf("index = %q, want %q", got, want)
	}
}

func TestNonIncrementalGenerationRetainsExistingSignedExtensions(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "repository")
	metadataSigner := newTestSigner(t)
	gen := NewGenerator("https://example.com/repo", metadataSigner)

	incusPath := filepath.Join(inputDir, "incus_7.3_13_x86-64.raw")
	if err := os.WriteFile(incusPath, []byte("incus"), 0o644); err != nil {
		t.Fatal(err)
	}
	incus, err := ParsePackage(incusPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := gen.Generate(
		context.Background(),
		&models.RepositoryConfig{OutputDir: outputDir},
		[]models.Package{*incus},
	); err != nil {
		t.Fatal(err)
	}
	incusManifestPath := filepath.Join(outputDir, "ext", "incus", "SHA256SUMS")
	incusManifest, err := os.ReadFile(incusManifestPath)
	if err != nil {
		t.Fatal(err)
	}

	dockerPath := filepath.Join(inputDir, "docker_27.0_14_x86-64.raw")
	if err := os.WriteFile(dockerPath, []byte("docker"), 0o644); err != nil {
		t.Fatal(err)
	}
	docker, err := ParsePackage(dockerPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := gen.Generate(
		context.Background(),
		&models.RepositoryConfig{OutputDir: outputDir},
		[]models.Package{*docker},
	); err != nil {
		t.Fatalf("non-incremental Generate() failed: %v", err)
	}

	retainedManifest, err := os.ReadFile(incusManifestPath)
	if err != nil {
		t.Fatalf("non-incremental generation removed the retained manifest: %v", err)
	}
	if !bytes.Equal(retainedManifest, incusManifest) {
		t.Fatalf("non-incremental generation changed the retained manifest:\n%s", retainedManifest)
	}
	for _, relative := range []string{
		filepath.Join("incus", "SHA256SUMS.gpg"),
		filepath.Join("incus", "incus.transfer"),
		filepath.Join("incus", filepath.Base(incusPath)),
		filepath.Join("docker", "SHA256SUMS"),
	} {
		if _, err := os.Stat(filepath.Join(outputDir, "ext", relative)); err != nil {
			t.Fatalf("expected retained or generated path %s: %v", relative, err)
		}
	}
	index, err := os.ReadFile(filepath.Join(outputDir, "ext", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(index), "docker\nincus\n"; got != want {
		t.Fatalf("non-incremental index = %q, want %q", got, want)
	}
}

func TestValidatePackages(t *testing.T) {
	gen := NewGenerator("https://example.com/repo", nil)

	tests := []struct {
		name     string
		packages []models.Package
		wantErr  bool
	}{
		{
			name: "valid packages",
			packages: []models.Package{
				{Name: "ext1", Version: "1.0", Filename: "/path/to/ext1_1.0_13_x86-64.raw", Metadata: map[string]interface{}{"OSVersion": "13"}},
				{Name: "ext2", Version: "2.0", Filename: "/path/to/ext2_2.0_13_arm64.raw", Metadata: map[string]interface{}{"OSVersion": "13"}},
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

func TestConcurrentIncrementalReconciliationRetainsBothOSVersionsAndExtensions(t *testing.T) {
	inputDir := t.TempDir()
	outputDir := filepath.Join(t.TempDir(), "repository")
	files := []string{
		"incus_7.3_13_x86-64.raw",
		"incus_7.3_14_x86-64.raw",
		"docker_27.0_13_x86-64.raw",
		"podman_5.4_14_x86-64.raw",
	}
	packages := make([]models.Package, 0, len(files))
	for _, name := range files {
		filename := filepath.Join(inputDir, name)
		if err := os.WriteFile(filename, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		pkg, err := ParsePackage(filename)
		if err != nil {
			t.Fatal(err)
		}
		packages = append(packages, *pkg)
	}

	start := make(chan struct{})
	errs := make(chan error, len(packages))
	var ready sync.WaitGroup
	ready.Add(len(packages))
	metadataSigner := newTestSigner(t)
	for i := range packages {
		pkg := packages[i]
		go func() {
			ready.Done()
			<-start
			errs <- NewGenerator("https://example.com/repo", metadataSigner).Generate(
				context.Background(),
				&models.RepositoryConfig{OutputDir: outputDir, Incremental: true},
				[]models.Package{pkg},
			)
		}()
	}
	ready.Wait()
	close(start)
	for range packages {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent reconciliation failed: %v", err)
		}
	}

	incusManifest, err := os.ReadFile(filepath.Join(outputDir, "ext", "incus", "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	for _, osVersion := range []string{"_13_", "_14_"} {
		if !strings.Contains(string(incusManifest), osVersion) {
			t.Fatalf("incus manifest lost OS version %s:\n%s", osVersion, incusManifest)
		}
	}
	index, err := os.ReadFile(filepath.Join(outputDir, "ext", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(index), "docker\nincus\npodman\n"; got != want {
		t.Fatalf("concurrent reconciliation index = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "ext", "incus", "SHA256SUMS.gpg")); err != nil {
		t.Fatalf("concurrent reconciliation did not retain signed manifest: %v", err)
	}
}

func TestFailedSysextCommitPreservesExistingPath(t *testing.T) {
	steps := []string{
		"restore:read",
		"stage:create",
		"stage:copy-existing",
		"stage:payload",
		"stage:manifest",
		"stage:signature",
		"stage:transfer",
		"stage:index",
		"stage:validate",
		"commit:atomic-switch",
	}
	metadataSigner := newTestSigner(t)
	for _, failAt := range steps {
		t.Run(failAt, func(t *testing.T) {
			inputDir := t.TempDir()
			outputDir := filepath.Join(t.TempDir(), "repository")
			initialPath := filepath.Join(inputDir, "incus_7.3_13_x86-64.raw")
			if err := os.WriteFile(initialPath, []byte("trixie"), 0o644); err != nil {
				t.Fatal(err)
			}
			initial, err := ParsePackage(initialPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := NewGenerator("https://example.com/repo", metadataSigner).Generate(
				context.Background(),
				&models.RepositoryConfig{OutputDir: outputDir},
				[]models.Package{*initial},
			); err != nil {
				t.Fatal(err)
			}
			before := snapshotTree(t, filepath.Join(outputDir, "ext"))

			forkyPath := filepath.Join(inputDir, "incus_7.3_14_x86-64.raw")
			if err := os.WriteFile(forkyPath, []byte("forky"), 0o644); err != nil {
				t.Fatal(err)
			}
			forky, err := ParsePackage(forkyPath)
			if err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected pre-switch failure")
			gen := &Generator{
				baseURL: "https://example.com/repo",
				signer:  metadataSigner,
				beforeStep: func(step string) error {
					if step == failAt {
						return injected
					}
					return nil
				},
			}
			err = gen.Generate(
				context.Background(),
				&models.RepositoryConfig{OutputDir: outputDir, Incremental: true},
				[]models.Package{*forky},
			)
			if !errors.Is(err, injected) {
				t.Fatalf("Generate() error = %v, want injected failure at %s", err, failAt)
			}
			assertTreeSnapshot(t, filepath.Join(outputDir, "ext"), before)

			if err := NewGenerator("https://example.com/repo", metadataSigner).Generate(
				context.Background(),
				&models.RepositoryConfig{OutputDir: outputDir, Incremental: true},
				[]models.Package{*forky},
			); err != nil {
				t.Fatalf("retry after %s did not converge: %v", failAt, err)
			}
			manifest, err := os.ReadFile(filepath.Join(outputDir, "ext", "incus", "SHA256SUMS"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(manifest), "_13_") || !strings.Contains(string(manifest), "_14_") {
				t.Fatalf("retry after %s lost an OS version:\n%s", failAt, manifest)
			}
		})
	}
}

func TestIncrementalReconciliationRejectsCorruptManifestWithoutMutation(t *testing.T) {
	outputDir := filepath.Join(t.TempDir(), "repository")
	extDir := filepath.Join(outputDir, "ext", "incus")
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(extDir, "SHA256SUMS"),
		[]byte("not-a-digest  incus_7.3_13_x86-64.raw\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "ext", "index"), []byte("incus\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, filepath.Join(outputDir, "ext"))

	inputPath := filepath.Join(t.TempDir(), "incus_7.3_14_x86-64.raw")
	if err := os.WriteFile(inputPath, []byte("forky"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkg, err := ParsePackage(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	err = NewGenerator("https://example.com/repo", nil).Generate(
		context.Background(),
		&models.RepositoryConfig{OutputDir: outputDir, Incremental: true},
		[]models.Package{*pkg},
	)
	if err == nil || !strings.Contains(err.Error(), "invalid SHA-256") {
		t.Fatalf("Generate() error = %v, want invalid SHA-256 rejection", err)
	}
	after := snapshotTree(t, filepath.Join(outputDir, "ext"))
	if len(before) != len(after) {
		t.Fatalf("corrupt restore changed path count: before=%d after=%d", len(before), len(after))
	}
	for path, digest := range before {
		if after[path] != digest {
			t.Fatalf("corrupt restore changed %s", path)
		}
	}
}

func TestIncrementalReconciliationRejectsInvalidSignedMetadata(t *testing.T) {
	metadataSigner := newTestSigner(t)
	for _, testCase := range []struct {
		name           string
		relativeTarget string
	}{
		{name: "signature", relativeTarget: filepath.Join("incus", "SHA256SUMS.gpg")},
		{name: "transfer", relativeTarget: filepath.Join("incus", "incus.transfer")},
		{name: "index", relativeTarget: "index"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			inputDir := t.TempDir()
			outputDir := filepath.Join(t.TempDir(), "repository")
			initialPath := filepath.Join(inputDir, "incus_7.3_13_x86-64.raw")
			if err := os.WriteFile(initialPath, []byte("trixie"), 0o644); err != nil {
				t.Fatal(err)
			}
			initial, err := ParsePackage(initialPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := NewGenerator("https://example.com/repo", metadataSigner).Generate(
				context.Background(),
				&models.RepositoryConfig{OutputDir: outputDir},
				[]models.Package{*initial},
			); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(outputDir, "ext", testCase.relativeTarget)
			if err := os.WriteFile(target, []byte("corrupt"), 0o644); err != nil {
				t.Fatal(err)
			}
			before := snapshotTree(t, filepath.Join(outputDir, "ext"))

			forkyPath := filepath.Join(inputDir, "incus_7.3_14_x86-64.raw")
			if err := os.WriteFile(forkyPath, []byte("forky"), 0o644); err != nil {
				t.Fatal(err)
			}
			forky, err := ParsePackage(forkyPath)
			if err != nil {
				t.Fatal(err)
			}
			err = NewGenerator("https://example.com/repo", metadataSigner).Generate(
				context.Background(),
				&models.RepositoryConfig{OutputDir: outputDir, Incremental: true},
				[]models.Package{*forky},
			)
			if err == nil {
				t.Fatalf("Generate() accepted corrupt %s", testCase.relativeTarget)
			}
			assertTreeSnapshot(t, filepath.Join(outputDir, "ext"), before)
		})
	}
}

func TestGenerateRejectsSignedExistingIdentityConflictsWithoutMutation(t *testing.T) {
	tests := []struct {
		name        string
		filename    string
		content     string
		mutate      func(*models.Package)
		wantMessage string
	}{
		{
			name:        "same filename with different digest",
			filename:    "incus_7.3_13_x86-64.raw",
			content:     "changed trixie",
			wantMessage: "conflicting sysext artifacts for identity",
		},
		{
			name:        "alternate filename for same identity",
			filename:    "incus_7.3_13_x86-64.raw.zst",
			content:     "trixie",
			wantMessage: "conflicting sysext artifacts for identity",
		},
		{
			name:     "metadata OSVersion differs from filename",
			filename: "incus_7.3_13_x86-64.raw",
			content:  "trixie",
			mutate: func(pkg *models.Package) {
				pkg.Metadata["OSVersion"] = "14"
			},
			wantMessage: "does not match filename",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			metadataSigner := newTestSigner(t)
			outputDir := filepath.Join(t.TempDir(), "repository")
			initialPath := filepath.Join(t.TempDir(), "incus_7.3_13_x86-64.raw")
			if err := os.WriteFile(initialPath, []byte("trixie"), 0o644); err != nil {
				t.Fatal(err)
			}
			initial, err := ParsePackage(initialPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := NewGenerator("https://example.com/repo", metadataSigner).Generate(
				context.Background(),
				&models.RepositoryConfig{OutputDir: outputDir},
				[]models.Package{*initial},
			); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(
				outputDir,
				"ext",
				"incus",
				"SHA256SUMS.gpg",
			)); err != nil {
				t.Fatalf("initial signed manifest missing: %v", err)
			}
			before := snapshotTree(t, filepath.Join(outputDir, "ext"))

			incomingPath := filepath.Join(t.TempDir(), testCase.filename)
			if err := os.WriteFile(incomingPath, []byte(testCase.content), 0o644); err != nil {
				t.Fatal(err)
			}
			incoming, err := ParsePackage(incomingPath)
			if err != nil {
				t.Fatal(err)
			}
			if testCase.mutate != nil {
				testCase.mutate(incoming)
			}

			err = NewGenerator("https://example.com/repo", metadataSigner).Generate(
				context.Background(),
				&models.RepositoryConfig{
					OutputDir:      outputDir,
					Incremental:    true,
					SkipDuplicates: true,
				},
				[]models.Package{*incoming},
			)
			if err == nil || !strings.Contains(err.Error(), testCase.wantMessage) {
				t.Fatalf("Generate() error = %v, want message %q", err, testCase.wantMessage)
			}
			assertTreeSnapshot(t, filepath.Join(outputDir, "ext"), before)
		})
	}
}

func snapshotTree(t *testing.T, root string) map[string][32]byte {
	t.Helper()
	snapshot := make(map[string][32]byte)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		snapshot[relative] = sha256.Sum256(content)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func assertTreeSnapshot(t *testing.T, root string, before map[string][32]byte) {
	t.Helper()
	after := snapshotTree(t, root)
	if len(before) != len(after) {
		t.Fatalf("failed commit changed path count: before=%d after=%d", len(before), len(after))
	}
	for path, digest := range before {
		if after[path] != digest {
			t.Fatalf("failed commit changed %s", path)
		}
	}
}

func TestValidatePackagesMissingBaseURL(t *testing.T) {
	gen := NewGenerator("", nil) // Empty base URL

	packages := []models.Package{
		{Name: "ext1", Version: "1.0", Filename: "/path/to/ext1_1.0_13_x86-64.raw"},
	}

	err := gen.ValidatePackages(packages)
	if err == nil {
		t.Fatalf("ValidatePackages() should error when base URL is missing")
	}

	if !strings.Contains(err.Error(), "--base-url") {
		t.Errorf("Error should mention --base-url, got: %v", err)
	}
}

func TestParsePackageOSVersionExtraction(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "repogen-test-sysext-osver-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	tests := []struct {
		name      string
		filename  string
		wantOSVer string
		wantArch  string
		wantName  string
		wantVer   string
	}{
		{
			name:      "debian trixie (13)",
			filename:  "docker_24.0.0_13_x86-64.raw",
			wantOSVer: "13",
			wantArch:  "x86-64",
			wantName:  "docker",
			wantVer:   "24.0.0",
		},
		{
			name:      "debian bookworm (12)",
			filename:  "podman_5.0.0_12_arm64.raw.zst",
			wantOSVer: "12",
			wantArch:  "arm64",
			wantName:  "podman",
			wantVer:   "5.0.0",
		},
		{
			name:      "ubuntu 22.04",
			filename:  "nvtop_1.0.0_22.04_arm64.raw.xz",
			wantOSVer: "22.04",
			wantArch:  "arm64",
			wantName:  "nvtop",
			wantVer:   "1.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := filepath.Join(tmpDir, tt.filename)
			err := os.WriteFile(filePath, []byte("test content"), 0644)
			if err != nil {
				t.Fatalf("Failed to write test file: %v", err)
			}

			pkg, err := ParsePackage(filePath)
			if err != nil {
				t.Fatalf("ParsePackage() error: %v", err)
			}

			if pkg.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", pkg.Name, tt.wantName)
			}
			if pkg.Version != tt.wantVer {
				t.Errorf("Version = %q, want %q", pkg.Version, tt.wantVer)
			}
			if pkg.Architecture != tt.wantArch {
				t.Errorf("Architecture = %q, want %q", pkg.Architecture, tt.wantArch)
			}

			// Verify OSVersion is stored correctly in Metadata
			osver, ok := pkg.Metadata["OSVersion"]
			if !ok {
				t.Error("OSVersion not found in Metadata")
			} else if osver != tt.wantOSVer {
				t.Errorf("Metadata[OSVersion] = %q, want %q", osver, tt.wantOSVer)
			}
		})
	}
}

func TestParsePackageRejectThreePartFormat(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "repogen-test-sysext-reject3-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	tests := []struct {
		name     string
		filename string
	}{
		{
			name:     "3-part format (old style)",
			filename: "docker_24.0.0_x86-64.raw",
		},
		{
			name:     "3-part with compression",
			filename: "podman_5.0.0_arm64.raw.zst",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath := filepath.Join(tmpDir, tt.filename)
			err := os.WriteFile(filePath, []byte("test content"), 0644)
			if err != nil {
				t.Fatalf("Failed to write test file: %v", err)
			}

			_, err = ParsePackage(filePath)
			if err == nil {
				t.Fatalf("ParsePackage() should reject 3-part filenames, got nil error")
			}

			if !strings.Contains(err.Error(), "exactly three underscores") &&
				!strings.Contains(err.Error(), "4 parts") {
				t.Errorf("Error message should mention 4 parts or 3 underscores, got: %v", err)
			}
		})
	}
}

func TestGenerateStoresOSVersionInMetadata(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "repogen-test-sysext-meta-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	inputDir := filepath.Join(tmpDir, "input")
	outputDir := filepath.Join(tmpDir, "output")
	if err := os.MkdirAll(inputDir, 0755); err != nil {
		t.Fatalf("Failed to create input dir: %v", err)
	}

	gen := NewGenerator("https://example.com/repo", nil)
	config := &models.RepositoryConfig{
		OutputDir: outputDir,
	}

	// Create test files with different OS versions
	ext1 := filepath.Join(inputDir, "docker_24.0.0_13_x86-64.raw")
	ext2 := filepath.Join(inputDir, "podman_5.0.0_12_arm64.raw.zst")

	if err := os.WriteFile(ext1, []byte("docker content"), 0644); err != nil {
		t.Fatalf("Failed to write ext1: %v", err)
	}
	if err := os.WriteFile(ext2, []byte("podman content"), 0644); err != nil {
		t.Fatalf("Failed to write ext2: %v", err)
	}

	packages := []models.Package{
		{Name: "docker", Version: "24.0.0", Architecture: "x86-64", Filename: ext1, SHA256Sum: "abc123"},
		{Name: "podman", Version: "5.0.0", Architecture: "arm64", Filename: ext2, SHA256Sum: "def456"},
	}

	err = gen.Generate(context.Background(), config, packages)
	if err != nil {
		t.Fatalf("Generate() failed: %v", err)
	}

	// Parse the metadata back from SHA256SUMS
	dockerDir := filepath.Join(outputDir, "ext", "docker")
	podmanDir := filepath.Join(outputDir, "ext", "podman")

	sha256sumDocker := filepath.Join(dockerDir, "SHA256SUMS")
	sha256sumPodman := filepath.Join(podmanDir, "SHA256SUMS")

	dockerPkgs, err := parseSHA256SUMS(sha256sumDocker, filepath.Join(outputDir, "ext"), "docker")
	if err != nil {
		t.Fatalf("parseSHA256SUMS for docker failed: %v", err)
	}

	podmanPkgs, err := parseSHA256SUMS(sha256sumPodman, filepath.Join(outputDir, "ext"), "podman")
	if err != nil {
		t.Fatalf("parseSHA256SUMS for podman failed: %v", err)
	}

	// Verify docker OSVersion
	if len(dockerPkgs) != 1 {
		t.Fatalf("Expected 1 docker package, got %d", len(dockerPkgs))
	}
	if osver, ok := dockerPkgs[0].Metadata["OSVersion"]; !ok {
		t.Error("docker: OSVersion not found in Metadata")
	} else if osver != "13" {
		t.Errorf("docker: Metadata[OSVersion] = %q, want %q", osver, "13")
	}

	// Verify podman OSVersion
	if len(podmanPkgs) != 1 {
		t.Fatalf("Expected 1 podman package, got %d", len(podmanPkgs))
	}
	if osver, ok := podmanPkgs[0].Metadata["OSVersion"]; !ok {
		t.Error("podman: OSVersion not found in Metadata")
	} else if osver != "12" {
		t.Errorf("podman: Metadata[OSVersion] = %q, want %q", osver, "12")
	}
}

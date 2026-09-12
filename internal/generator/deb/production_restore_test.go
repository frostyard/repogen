package deb

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/frostyard/repogen/internal/models"
	"github.com/frostyard/repogen/internal/signer"
	"github.com/frostyard/repogen/internal/utils"
)

func TestRestoreProductionStateAcceptsCompleteSignedPrior(t *testing.T) {
	outputDir := t.TempDir()
	config := productionRestoreConfig(outputDir)
	expectedDigest := writeProductionRestoreFixture(t, config, nil)
	before := snapshotProductionTree(t, outputDir)

	state, err := RestoreProductionState(config, productionPublicKeyFixture(), expectedDigest)
	if err != nil {
		t.Fatalf("RestoreProductionState returned an error: %v", err)
	}
	if state.ReleaseSHA256 != expectedDigest {
		t.Fatalf("Release SHA-256 = %s, want %s", state.ReleaseSHA256, expectedDigest)
	}
	if state.SigningKeyFingerprint == "" {
		t.Fatal("signing-key fingerprint is empty")
	}
	if len(state.Packages) != 1 {
		t.Fatalf("restored package count = %d, want 1", len(state.Packages))
	}
	if state.Packages[0].Name != "repogen-test" || state.Packages[0].Architecture != "amd64" {
		t.Fatalf("restored package = %+v", state.Packages[0])
	}
	if after := snapshotProductionTree(t, outputDir); !reflect.DeepEqual(after, before) {
		t.Fatalf("prior tree changed after successful restore\nbefore: %v\nafter:  %v", before, after)
	}
}

func TestRestoreProductionStateFailsClosedWithoutChangingPriorBytes(t *testing.T) {
	tests := []struct {
		name      string
		prepare   func(t *testing.T, config *models.RepositoryConfig) (string, string)
		wantError string
	}{
		{
			name: "missing metadata",
			prepare: func(t *testing.T, config *models.RepositoryConfig) (string, string) {
				digest := writeProductionRestoreFixture(t, config, nil)
				if err := os.Remove(productionIndexPath(config, "all", "Packages.gz")); err != nil {
					t.Fatal(err)
				}
				return digest, productionPublicKeyFixture()
			},
			wantError: "cannot read prior index",
		},
		{
			name: "missing InRelease",
			prepare: func(t *testing.T, config *models.RepositoryConfig) (string, string) {
				digest := writeProductionRestoreFixture(t, config, nil)
				if err := os.Remove(productionSuitePath(config, "InRelease")); err != nil {
					t.Fatal(err)
				}
				return digest, productionPublicKeyFixture()
			},
			wantError: "no such file or directory",
		},
		{
			name: "symlinked metadata",
			prepare: func(t *testing.T, config *models.RepositoryConfig) (string, string) {
				digest := writeProductionRestoreFixture(t, config, nil)
				indexPath := productionIndexPath(config, "all", "Packages")
				realPath := indexPath + ".real"
				if err := os.Rename(indexPath, realPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Base(realPath), indexPath); err != nil {
					t.Fatal(err)
				}
				return digest, productionPublicKeyFixture()
			},
			wantError: "is a symlink",
		},
		{
			name: "checksum mismatch",
			prepare: func(t *testing.T, config *models.RepositoryConfig) (string, string) {
				digest := writeProductionRestoreFixture(t, config, nil)
				if err := os.WriteFile(productionIndexPath(config, "amd64", "Packages"), []byte("tampered\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				return digest, productionPublicKeyFixture()
			},
			wantError: "verification",
		},
		{
			name: "tampered signed Release",
			prepare: func(t *testing.T, config *models.RepositoryConfig) (string, string) {
				writeProductionRestoreFixture(t, config, nil)
				releasePath := productionSuitePath(config, "Release")
				release, err := os.ReadFile(releasePath)
				if err != nil {
					t.Fatal(err)
				}
				release = bytes.Replace(release, []byte("Origin: Repogen Repository"), []byte("Origin: Drifted Repository"), 1)
				if err := os.WriteFile(releasePath, release, 0o644); err != nil {
					t.Fatal(err)
				}
				return sha256Hex(release), productionPublicKeyFixture()
			},
			wantError: "not byte-identical to Release",
		},
		{
			name: "invalid detached signature",
			prepare: func(t *testing.T, config *models.RepositoryConfig) (string, string) {
				digest := writeProductionRestoreFixture(t, config, nil)
				signaturePath := productionSuitePath(config, "Release.gpg")
				signature, err := os.ReadFile(signaturePath)
				if err != nil {
					t.Fatal(err)
				}
				signature[len(signature)/2] ^= 1
				if err := os.WriteFile(signaturePath, signature, 0o644); err != nil {
					t.Fatal(err)
				}
				return digest, productionPublicKeyFixture()
			},
			wantError: "Release.gpg signature is invalid",
		},
		{
			name: "wrong key",
			prepare: func(t *testing.T, config *models.RepositoryConfig) (string, string) {
				digest := writeProductionRestoreFixture(t, config, nil)
				return digest, writeWrongPublicKey(t)
			},
			wantError: "signature is invalid",
		},
		{
			name: "private key material",
			prepare: func(t *testing.T, config *models.RepositoryConfig) (string, string) {
				digest := writeProductionRestoreFixture(t, config, nil)
				return digest, productionPrivateKeyFixture()
			},
			wantError: "contains private key material",
		},
		{
			name: "identity drift with valid signatures",
			prepare: func(t *testing.T, config *models.RepositoryConfig) (string, string) {
				digest := writeProductionRestoreFixture(t, config, func(fixture *productionRestoreFixture) {
					fixture.origin = "Drifted Repository"
				})
				return digest, productionPublicKeyFixture()
			},
			wantError: "field Origin",
		},
		{
			name: "one of two architectures cannot parse",
			prepare: func(t *testing.T, config *models.RepositoryConfig) (string, string) {
				digest := writeProductionRestoreFixture(t, config, func(fixture *productionRestoreFixture) {
					fixture.indexes["all"] = []byte("not-a-field\n")
				})
				return digest, productionPublicKeyFixture()
			},
			wantError: `prior index "main/binary-all/Packages" cannot be parsed`,
		},
		{
			name: "gzip and plain index disagree",
			prepare: func(t *testing.T, config *models.RepositoryConfig) (string, string) {
				digest := writeProductionRestoreFixture(t, config, func(fixture *productionRestoreFixture) {
					fixture.gzipIndexes["amd64"] = []byte{}
				})
				return digest, productionPublicKeyFixture()
			},
			wantError: "not byte-identical after decompression",
		},
		{
			name: "expected prior digest mismatch",
			prepare: func(t *testing.T, config *models.RepositoryConfig) (string, string) {
				writeProductionRestoreFixture(t, config, nil)
				return strings.Repeat("0", 64), productionPublicKeyFixture()
			},
			wantError: "does not match expected",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			outputDir := t.TempDir()
			config := productionRestoreConfig(outputDir)
			expectedDigest, publicKey := tt.prepare(t, config)
			before := snapshotProductionTree(t, outputDir)

			_, err := RestoreProductionState(config, publicKey, expectedDigest)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantError)
			}

			after := snapshotProductionTree(t, outputDir)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("prior tree changed after failed restore\nbefore: %v\nafter:  %v", before, after)
			}
		})
	}
}

func TestVerifyProductionInitializationRequiresAuthoritativeAbsence(t *testing.T) {
	outputDir := t.TempDir()

	if err := VerifyProductionInitialization(outputDir, "trixie"); err != nil {
		t.Fatalf("absent target rejected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "dists")); !os.IsNotExist(err) {
		t.Fatalf("initialization check created parent state: %v", err)
	}

	target := filepath.Join(outputDir, "dists", "trixie")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := VerifyProductionInitialization(outputDir, "trixie"); err == nil ||
		!strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing target error = %v", err)
	}
}

type productionRestoreFixture struct {
	origin      string
	indexes     map[string][]byte
	gzipIndexes map[string][]byte
}

func writeProductionRestoreFixture(
	t *testing.T,
	config *models.RepositoryConfig,
	mutate func(*productionRestoreFixture),
) string {
	t.Helper()

	amd64Packages, err := GeneratePackagesFile([]models.Package{{
		Name:         "repogen-test",
		Version:      "1.0.0+fy13u1",
		Architecture: "amd64",
		Filename:     "pool/main/r/repogen-test/repogen-test_1.0.0+fy13u1_amd64.deb",
		Size:         7,
		MD5Sum:       strings.Repeat("1", 32),
		SHA1Sum:      strings.Repeat("2", 40),
		SHA256Sum:    strings.Repeat("3", 64),
		SHA512Sum:    strings.Repeat("4", 128),
		Description:  "Production restore fixture",
	}})
	if err != nil {
		t.Fatal(err)
	}

	fixture := &productionRestoreFixture{
		origin: productionReleaseOrigin,
		indexes: map[string][]byte{
			"all":   {},
			"amd64": amd64Packages,
		},
		gzipIndexes: make(map[string][]byte),
	}
	if mutate != nil {
		mutate(fixture)
	}

	var metadataPaths []string
	for _, architecture := range config.Arches {
		plain := fixture.indexes[architecture]
		gzipInput, overridden := fixture.gzipIndexes[architecture]
		if !overridden {
			gzipInput = plain
		}
		compressed, err := utils.GzipCompress(gzipInput)
		if err != nil {
			t.Fatal(err)
		}

		indexDir := filepath.Dir(productionIndexPath(config, architecture, "Packages"))
		if err := os.MkdirAll(indexDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(indexDir, "Packages"), plain, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(indexDir, "Packages.gz"), compressed, 0o644); err != nil {
			t.Fatal(err)
		}
		metadataPaths = append(
			metadataPaths,
			filepath.ToSlash(filepath.Join("main", "binary-"+architecture, "Packages")),
			filepath.ToSlash(filepath.Join("main", "binary-"+architecture, "Packages.gz")),
		)
	}

	releaseConfig := *config
	releaseConfig.Origin = fixture.origin
	fileInfos, err := CalculateReleaseFileInfos(filepath.Dir(productionSuitePath(config, "Release")), metadataPaths)
	if err != nil {
		t.Fatal(err)
	}
	release, err := GenerateReleaseFile(&releaseConfig, fileInfos)
	if err != nil {
		t.Fatal(err)
	}
	release = bytes.Replace(
		release,
		[]byte("Components: main\n"),
		[]byte("Components: main\nAcquire-By-Hash: yes\n"),
		1,
	)

	privateKey := filepath.Join("..", "..", "..", "test", "fixtures", "gpg-keys", "test-key.asc")
	gpgSigner, err := signer.NewGPGSigner(privateKey, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := gpgSigner.Close(); err != nil {
			t.Errorf("close signer: %v", err)
		}
	}()
	inRelease, err := gpgSigner.SignCleartext(release)
	if err != nil {
		t.Fatal(err)
	}
	releaseGPG, err := gpgSigner.SignDetached(release)
	if err != nil {
		t.Fatal(err)
	}

	for name, data := range map[string][]byte{
		"Release":     release,
		"InRelease":   inRelease,
		"Release.gpg": releaseGPG,
	} {
		if err := os.WriteFile(productionSuitePath(config, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return sha256Hex(release)
}

func productionRestoreConfig(outputDir string) *models.RepositoryConfig {
	return &models.RepositoryConfig{
		OutputDir:  outputDir,
		Origin:     productionReleaseOrigin,
		Label:      productionReleaseLabel,
		Codename:   "trixie",
		Suite:      "trixie",
		Components: []string{"main"},
		Arches:     []string{"all", "amd64"},
	}
}

func productionSuitePath(config *models.RepositoryConfig, name string) string {
	return filepath.Join(config.OutputDir, "dists", config.Codename, name)
}

func productionIndexPath(config *models.RepositoryConfig, architecture, name string) string {
	return filepath.Join(
		config.OutputDir,
		"dists",
		config.Codename,
		"main",
		"binary-"+architecture,
		name,
	)
}

func productionPublicKeyFixture() string {
	return filepath.Join("..", "..", "..", "test", "fixtures", "gpg-keys", "test-key-pub.asc")
}

func productionPrivateKeyFixture() string {
	return filepath.Join("..", "..", "..", "test", "fixtures", "gpg-keys", "test-key.asc")
}

func writeWrongPublicKey(t *testing.T) string {
	t.Helper()

	entity, err := openpgp.NewEntity("Wrong key", "", "wrong@example.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	writer, err := armor.Encode(&data, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := entity.Serialize(writer); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "wrong-public-key.asc")
	if err := os.WriteFile(keyPath, data.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return keyPath
}

func snapshotProductionTree(t *testing.T, root string) map[string]string {
	t.Helper()

	snapshot := make(map[string]string)
	err := filepath.Walk(root, func(filePath string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		snapshot[relative] = sha256Hex(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func sha256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

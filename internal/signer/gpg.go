package signer

import (
	"bytes"
	"crypto"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// GPGSigner implements Signer interface using GPG
type GPGSigner struct {
	entity  *openpgp.Entity
	keyPath string // Path to the private key file for GPG command-line operations

	// Cached GPG home directory for CLI operations.
	// Created lazily on first use, cleaned up by Close().
	gpgHome     string
	gpgHomeOnce sync.Once
	gpgHomeErr  error
}

// NewGPGSigner creates a new GPG signer from a private key file
func NewGPGSigner(keyPath, passphrase string) (*GPGSigner, error) {
	if keyPath == "" {
		return nil, fmt.Errorf("key path is empty")
	}

	// Read private key file
	keyFile, err := os.Open(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open key file: %w", err)
	}
	defer func() { _ = keyFile.Close() }()

	// Try to parse as armored key first
	entityList, err := openpgp.ReadArmoredKeyRing(keyFile)
	if err != nil {
		// Try as binary key
		_, _ = keyFile.Seek(0, 0)
		entityList, err = openpgp.ReadKeyRing(keyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read key: %w", err)
		}
	}

	if len(entityList) == 0 {
		return nil, fmt.Errorf("no keys found in key file")
	}

	entity := entityList[0]

	// Decrypt private key if passphrase provided
	if passphrase != "" {
		if entity.PrivateKey != nil && entity.PrivateKey.Encrypted {
			err = entity.PrivateKey.Decrypt([]byte(passphrase))
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt private key: %w", err)
			}
		}

		// Decrypt subkeys as well
		for _, subkey := range entity.Subkeys {
			if subkey.PrivateKey != nil && subkey.PrivateKey.Encrypted {
				err = subkey.PrivateKey.Decrypt([]byte(passphrase))
				if err != nil {
					return nil, fmt.Errorf("failed to decrypt subkey: %w", err)
				}
			}
		}
	}

	return &GPGSigner{
		entity:  entity,
		keyPath: keyPath,
	}, nil
}

// ensureGPGHome lazily creates a temporary GPG home directory and imports the
// private key. The directory is reused for all subsequent CLI signing operations
// and cleaned up by Close().
func (s *GPGSigner) ensureGPGHome() (string, error) {
	s.gpgHomeOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "repogen-gpg-*")
		if err != nil {
			s.gpgHomeErr = fmt.Errorf("failed to create temp dir: %w", err)
			return
		}

		keyPath, err := filepath.Abs(s.keyPath)
		if err != nil {
			_ = os.RemoveAll(tmpDir)
			s.gpgHomeErr = fmt.Errorf("failed to get absolute key path: %w", err)
			return
		}

		cmd := exec.Command("gpg", "--homedir", tmpDir, "--import", keyPath)
		if output, err := cmd.CombinedOutput(); err != nil {
			_ = os.RemoveAll(tmpDir)
			s.gpgHomeErr = fmt.Errorf("failed to import key: %w\nOutput: %s", err, output)
			return
		}

		s.gpgHome = tmpDir
	})
	return s.gpgHome, s.gpgHomeErr
}

// Close removes the cached GPG home directory. It is safe to call multiple
// times or on a signer that never performed CLI signing.
func (s *GPGSigner) Close() error {
	if s.gpgHome != "" {
		return os.RemoveAll(s.gpgHome)
	}
	return nil
}

// SignCleartext creates a cleartext signature (for Debian InRelease)
func (s *GPGSigner) SignCleartext(data []byte) ([]byte, error) {
	// Use GPG command-line for cleartext signing since go-crypto's implementation
	// doesn't produce signatures that APT can verify correctly

	gpgHome, err := s.ensureGPGHome()
	if err != nil {
		return nil, err
	}

	// Create temp file for input data
	inputFile := filepath.Join(gpgHome, "input.txt")
	if err := os.WriteFile(inputFile, data, 0600); err != nil {
		return nil, fmt.Errorf("failed to write input file: %w", err)
	}

	// Sign with GPG
	cmd := exec.Command("gpg", "--homedir", gpgHome, "--clearsign", "--armor",
		"--digest-algo", "SHA512", "--batch", "--yes", inputFile)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to sign with GPG: %w\nOutput: %s", err, output)
	}

	// Read the signed output
	signedFile := inputFile + ".asc"
	signedData, err := os.ReadFile(signedFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read signed file: %w", err)
	}

	return signedData, nil
}

// SignDetached creates a detached ASCII-armored signature (for Debian Release.gpg, RPM repomd.xml.asc)
func (s *GPGSigner) SignDetached(data []byte) ([]byte, error) {
	var buf bytes.Buffer

	err := openpgp.ArmoredDetachSign(&buf, s.entity, bytes.NewReader(data), &packet.Config{
		DefaultHash: crypto.SHA512,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create detached signature: %w", err)
	}

	return buf.Bytes(), nil
}

// SignDetachedBinary creates a detached binary signature (for Pacman .sig files)
// Pacman expects binary OpenPGP signatures in old packet format, not ASCII-armored ones
// We use GPG command-line to ensure compatibility with Pacman's expectations
func (s *GPGSigner) SignDetachedBinary(data []byte) ([]byte, error) {
	gpgHome, err := s.ensureGPGHome()
	if err != nil {
		return nil, err
	}

	// Create temp file for input data
	inputFile := filepath.Join(gpgHome, "input.dat")
	if err := os.WriteFile(inputFile, data, 0600); err != nil {
		return nil, fmt.Errorf("failed to write input file: %w", err)
	}

	// Sign with GPG - use --detach-sign for binary signature
	// --no-armor ensures binary output (old packet format compatible with Pacman)
	outputFile := filepath.Join(gpgHome, "output.sig")
	cmd := exec.Command("gpg", "--homedir", gpgHome, "--detach-sign",
		"--digest-algo", "SHA512", "--batch", "--yes",
		"--output", outputFile, inputFile)
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("failed to sign with GPG: %w\nOutput: %s", err, output)
	}

	// Read the signature
	signature, err := os.ReadFile(outputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read signature file: %w", err)
	}

	return signature, nil
}

// SignDetachedBinaryFromFile creates a detached binary signature directly from a file
// This avoids loading large files into memory
func (s *GPGSigner) SignDetachedBinaryFromFile(filePath string) ([]byte, error) {
	gpgHome, err := s.ensureGPGHome()
	if err != nil {
		return nil, err
	}

	// Get absolute path for input file
	inputFile, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute file path: %w", err)
	}

	// Sign with GPG - use --detach-sign for binary signature
	// --no-armor ensures binary output (old packet format compatible with Pacman)
	outputFile := filepath.Join(gpgHome, "output.sig")
	cmd := exec.Command("gpg", "--homedir", gpgHome, "--detach-sign",
		"--digest-algo", "SHA512", "--batch", "--yes",
		"--output", outputFile, inputFile)
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("failed to sign with GPG: %w\nOutput: %s", err, output)
	}

	// Read the signature
	signature, err := os.ReadFile(outputFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read signature file: %w", err)
	}

	return signature, nil
}

// GetPublicKey returns the public key in armored format
func (s *GPGSigner) GetPublicKey() ([]byte, error) {
	var buf bytes.Buffer

	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		return nil, err
	}

	err = s.entity.Serialize(w)
	if err != nil {
		_ = w.Close()
		return nil, err
	}

	if err := w.Close(); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// NewNilSigner returns a nil signer (for unsigned repositories)
func NewNilSigner() Signer {
	return nil
}

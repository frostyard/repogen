package signer

import (
	"bytes"
	"crypto"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

func runOK(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\nCommand: %s\nOutput:\n%s", name, err, fmt.Sprintf("%q", args), output)
	}
}

func runFails(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("%s unexpectedly succeeded\nCommand: %s\nOutput:\n%s", name, fmt.Sprintf("%q", args), output)
	}
}

func skipIfMissing(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s is not installed", name)
	}
}

func cleartextSection(t *testing.T, signed []byte) []byte {
	t.Helper()
	lines := bytes.Split(signed, []byte("\n"))
	start := -1
	for i, line := range lines {
		if bytes.Equal(line, []byte("-----BEGIN PGP SIGNED MESSAGE-----")) {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatal("cleartext signed message has no start header")
	}
	bodyStart := -1
	for i := start + 1; i < len(lines); i++ {
		if len(lines[i]) == 0 {
			bodyStart = i + 1
			break
		}
	}
	if bodyStart < 0 {
		t.Fatal("cleartext signed message has no header separator")
	}
	var payload []byte
	for i := bodyStart; i < len(lines); i++ {
		line := lines[i]
		if bytes.HasPrefix(line, []byte("-----BEGIN PGP SIGNATURE-----")) {
			break
		}
		if bytes.HasPrefix(line, []byte("- ")) {
			line = line[2:]
		}
		payload = append(payload, line...)
		payload = append(payload, '\n')
	}
	return payload
}

func TestSignaturesVerifyWithGPGAndGo(t *testing.T) {
	privateKey := filepath.Join("..", "..", "test", "fixtures", "gpg-keys", "test-key.asc")
	publicKey := filepath.Join("..", "..", "test", "fixtures", "gpg-keys", "test-key-pub.asc")

	signer, err := NewGPGSigner(privateKey, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = signer.Close() }()

	if _, err := exec.LookPath("gpg"); err != nil {
		t.Fatal("gpg is required for signature verification tests")
	}

	publicKeyArmor, err := signer.GetPublicKey()
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(publicKeyArmor))
	if err != nil {
		t.Fatal(err)
	}

	gpgHome := t.TempDir()
	runOK(t, "import public key", "gpg", "--homedir", gpgHome, "--batch", "--import", publicKey)
	t.Cleanup(func() {
		_ = exec.Command("gpgconf", "--homedir", gpgHome, "--kill", "all").Run()
	})

	publicBlock, err := armor.Decode(bytes.NewReader(publicKeyArmor))
	if err != nil {
		t.Fatal(err)
	}
	publicBinary := filepath.Join(gpgHome, "test-key-pub.gpg")
	body, err := io.ReadAll(publicBlock.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publicBinary, body, 0600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name          string
		data          []byte
		wantPlaintext []byte
	}{
		{name: "LF with final newline", data: []byte("Origin: Test\nSuite: testing\n"), wantPlaintext: []byte("Origin: Test\nSuite: testing")},
		{name: "LF without final newline", data: []byte("Origin: Test\nSuite: testing"), wantPlaintext: []byte("Origin: Test\nSuite: testing")},
		{name: "CRLF with final newline", data: []byte("Origin: Test\r\nSuite: testing\r\n"), wantPlaintext: []byte("Origin: Test\nSuite: testing")},
		{name: "CRLF without final newline", data: []byte("Origin: Test\r\nSuite: testing"), wantPlaintext: []byte("Origin: Test\nSuite: testing")},
		{name: "dash-escaped line", data: []byte("-----BEGIN FAKE-----\n- dash\n"), wantPlaintext: []byte("-----BEGIN FAKE-----\n- dash")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Run("cleartext", func(t *testing.T) {
				dir := t.TempDir()
				signed, err := signer.SignCleartext(tt.data)
				if err != nil {
					t.Fatal(err)
				}
				inRelease := filepath.Join(dir, "InRelease")
				if err := os.WriteFile(inRelease, signed, 0600); err != nil {
					t.Fatal(err)
				}
				runOK(t, "gpg cleartext verify", "gpg", "--homedir", gpgHome, "--batch", "--verify", inRelease)
				t.Run("gpgv", func(t *testing.T) {
					skipIfMissing(t, "gpgv")
					runOK(t, "gpgv cleartext verify", "gpgv", "--keyring", publicBinary, inRelease)
				})
				t.Run("sqv", func(t *testing.T) {
					skipIfMissing(t, "sqv")
					out := filepath.Join(dir, "cleartext-output")
					runOK(t, "sqv cleartext verify", "sqv", "--keyring", publicKey, "--cleartext", "--output", out, inRelease)
				})

				block, rest := clearsign.Decode(signed)
				if block == nil || len(bytes.TrimSpace(rest)) != 0 {
					t.Fatal("clearsign.Decode did not return one complete cleartext message")
				}
				clearSigner, err := block.VerifySignature(keyring, nil)
				if err != nil {
					t.Fatalf("go-crypto cleartext verification failed: %v", err)
				}
				if !bytes.Equal(clearSigner.PrimaryKey.Fingerprint, keyring[0].PrimaryKey.Fingerprint) {
					t.Fatalf("cleartext fingerprint = %X, want %X", clearSigner.PrimaryKey.Fingerprint, keyring[0].PrimaryKey.Fingerprint)
				}
				if !bytes.Equal(block.Plaintext, tt.wantPlaintext) {
					t.Fatalf("Plaintext = %q, want %q", block.Plaintext, tt.wantPlaintext)
				}
				gotPayload := cleartextSection(t, signed)
				wantPayload := tt.data
				if !bytes.HasSuffix(wantPayload, []byte("\n")) {
					wantPayload = append(bytes.Clone(wantPayload), '\n')
				}
				if !bytes.Equal(gotPayload, wantPayload) {
					t.Fatalf("apt cleartext payload = %q, want %q", gotPayload, wantPayload)
				}
			})

			t.Run("detached armored", func(t *testing.T) {
				dir := t.TempDir()
				sig, err := signer.SignDetached(tt.data)
				if err != nil {
					t.Fatal(err)
				}
				dataPath := filepath.Join(dir, "data")
				sigPath := filepath.Join(dir, "data.asc")
				if err := os.WriteFile(dataPath, tt.data, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(sigPath, sig, 0600); err != nil {
					t.Fatal(err)
				}
				runOK(t, "gpg armored verify", "gpg", "--homedir", gpgHome, "--batch", "--verify", sigPath, dataPath)
				t.Run("gpgv", func(t *testing.T) {
					skipIfMissing(t, "gpgv")
					runOK(t, "gpgv armored verify", "gpgv", "--keyring", publicBinary, sigPath, dataPath)
				})
				t.Run("sqv", func(t *testing.T) {
					skipIfMissing(t, "sqv")
					runOK(t, "sqv armored verify", "sqv", "--keyring", publicKey, "--signature-file", sigPath, dataPath)
				})
				if _, err := openpgp.CheckArmoredDetachedSignature(keyring, bytes.NewReader(tt.data), bytes.NewReader(sig), nil); err != nil {
					t.Fatalf("go-crypto armored verification failed: %v", err)
				}

				armored, err := armor.Decode(bytes.NewReader(sig))
				if err != nil {
					t.Fatal(err)
				}
				packetData, err := packet.Read(armored.Body)
				if err != nil {
					t.Fatal(err)
				}
				signature, ok := packetData.(*packet.Signature)
				if !ok {
					t.Fatalf("armored signature packet is %T, want *packet.Signature", packetData)
				}
				if signature.Version != 4 || signature.SigType != packet.SigTypeBinary || signature.Hash != crypto.SHA512 {
					t.Fatalf("signature format = version %d, type %v, hash %v", signature.Version, signature.SigType, signature.Hash)
				}

				if bytes.Contains(tt.data, []byte("\r")) {
					lfOnly := bytes.ReplaceAll(bytes.Clone(tt.data), []byte("\r\n"), []byte("\n"))
					if _, err := openpgp.CheckArmoredDetachedSignature(keyring, bytes.NewReader(lfOnly), bytes.NewReader(sig), nil); err == nil {
						t.Fatal("go-crypto armored verification succeeded for LF-only data")
					}
				}
				withX := append(bytes.Clone(tt.data), 'x')
				if _, err := openpgp.CheckArmoredDetachedSignature(keyring, bytes.NewReader(withX), bytes.NewReader(sig), nil); err == nil {
					t.Fatal("go-crypto armored verification succeeded for data plus x")
				}
				withXPath := filepath.Join(dir, "with-x")
				if err := os.WriteFile(withXPath, withX, 0600); err != nil {
					t.Fatal(err)
				}
				runFails(t, "gpg armored rejects data plus x", "gpg", "--homedir", gpgHome, "--batch", "--verify", sigPath, withXPath)
			})

			t.Run("detached binary", func(t *testing.T) {
				dir := t.TempDir()
				sig, err := signer.SignDetachedBinary(tt.data)
				if err != nil {
					t.Fatal(err)
				}
				dataPath := filepath.Join(dir, "data")
				sigPath := filepath.Join(dir, "data.sig")
				if err := os.WriteFile(dataPath, tt.data, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(sigPath, sig, 0600); err != nil {
					t.Fatal(err)
				}
				runOK(t, "gpg binary verify", "gpg", "--homedir", gpgHome, "--batch", "--verify", sigPath, dataPath)
				t.Run("gpgv", func(t *testing.T) {
					skipIfMissing(t, "gpgv")
					runOK(t, "gpgv binary verify", "gpgv", "--keyring", publicBinary, sigPath, dataPath)
				})
				t.Run("sqv", func(t *testing.T) {
					skipIfMissing(t, "sqv")
					runOK(t, "sqv binary verify", "sqv", "--keyring", publicKey, "--signature-file", sigPath, dataPath)
				})
				if _, err := openpgp.CheckDetachedSignature(keyring, bytes.NewReader(tt.data), bytes.NewReader(sig), nil); err != nil {
					t.Fatalf("go-crypto binary verification failed: %v", err)
				}
				if bytes.Contains(tt.data, []byte("\r")) {
					lfOnly := bytes.ReplaceAll(bytes.Clone(tt.data), []byte("\r\n"), []byte("\n"))
					if _, err := openpgp.CheckDetachedSignature(keyring, bytes.NewReader(lfOnly), bytes.NewReader(sig), nil); err == nil {
						t.Fatal("go-crypto binary verification succeeded for LF-only data")
					}
				}
				withX := append(bytes.Clone(tt.data), 'x')
				if _, err := openpgp.CheckDetachedSignature(keyring, bytes.NewReader(withX), bytes.NewReader(sig), nil); err == nil {
					t.Fatal("go-crypto binary verification succeeded for data plus x")
				}
				withXPath := filepath.Join(dir, "with-x")
				if err := os.WriteFile(withXPath, withX, 0600); err != nil {
					t.Fatal(err)
				}
				runFails(t, "gpg binary rejects data plus x", "gpg", "--homedir", gpgHome, "--batch", "--verify", sigPath, withXPath)
			})

			t.Run("detached binary from file", func(t *testing.T) {
				dir := t.TempDir()
				dataPath := filepath.Join(dir, "data")
				if err := os.WriteFile(dataPath, tt.data, 0600); err != nil {
					t.Fatal(err)
				}
				sig, err := signer.SignDetachedBinaryFromFile(dataPath)
				if err != nil {
					t.Fatal(err)
				}
				sigPath := filepath.Join(dir, "data.sig")
				if err := os.WriteFile(sigPath, sig, 0600); err != nil {
					t.Fatal(err)
				}
				runOK(t, "gpg binary-from-file verify", "gpg", "--homedir", gpgHome, "--batch", "--verify", sigPath, dataPath)
				if _, err := openpgp.CheckDetachedSignature(keyring, bytes.NewReader(tt.data), bytes.NewReader(sig), nil); err != nil {
					t.Fatalf("go-crypto binary-from-file verification failed: %v", err)
				}
			})
		})
	}
}

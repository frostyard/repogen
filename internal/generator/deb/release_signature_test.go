package deb

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	signerpkg "github.com/frostyard/repogen/internal/signer"
)

func publicKeyArmor(t *testing.T, entity *openpgp.Entity) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := entity.Serialize(w); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestParseTrustedKeyRing(t *testing.T) {
	publicKey, err := os.ReadFile("../../../test/fixtures/gpg-keys/test-key-pub.asc")
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := os.ReadFile(testPrivateKeyFixture())
	if err != nil {
		t.Fatal(err)
	}
	// One armor block holding two public keys.
	first, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(publicKey))
	if err != nil {
		t.Fatal(err)
	}
	other, err := openpgp.NewEntity("Other", "", "other@example.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	var two bytes.Buffer
	w, err := armor.Encode(&two, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []*openpgp.Entity{first[0], other} {
		if err := e.Serialize(w); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		data    []byte
		wantErr string
	}{
		{name: "one public key", data: publicKey},
		{name: "private key material", data: privateKey, wantErr: "private key material"},
		{name: "two keys", data: two.Bytes(), wantErr: "exactly one key"},
		{name: "garbage", data: []byte("not a key"), wantErr: "cannot parse"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ring, err := parseTrustedKeyRing(tt.data)
			if tt.wantErr == "" {
				if err != nil || len(ring) != 1 {
					t.Fatalf("parseTrustedKeyRing() = %d keys, %v", len(ring), err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestVerifyReleaseSignatures(t *testing.T) {
	signer, err := signerpkg.NewGPGSigner(testPrivateKeyFixture(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = signer.Close() }()
	publicKey, err := signer.GetPublicKey()
	if err != nil {
		t.Fatal(err)
	}
	ring, err := parseTrustedKeyRing(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	wantFingerprint := fmt.Sprintf("%X", ring[0].PrimaryKey.Fingerprint)

	release := []byte("Origin: Test\nSuite: testing\n")
	inRelease, err := signer.SignCleartext(release)
	if err != nil {
		t.Fatal(err)
	}
	releaseGPG, err := signer.SignDetached(release)
	if err != nil {
		t.Fatal(err)
	}
	otherInRelease, err := signer.SignCleartext([]byte("Origin: Other\n"))
	if err != nil {
		t.Fatal(err)
	}
	otherReleaseGPG, err := signer.SignDetached([]byte("Origin: Other\n"))
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := openpgp.NewEntity("Wrong", "", "wrong@example.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	wrongRing, err := parseTrustedKeyRing(publicKeyArmor(t, wrong))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		ring       openpgp.EntityList
		release    []byte
		inRelease  []byte
		releaseGPG []byte
		wantErr    string
	}{
		{name: "valid", ring: ring, release: release, inRelease: inRelease, releaseGPG: releaseGPG},
		{name: "wrong key", ring: wrongRing, release: release, inRelease: inRelease, releaseGPG: releaseGPG, wantErr: "InRelease signature is invalid"},
		{name: "InRelease payload differs", ring: ring, release: release, inRelease: otherInRelease, releaseGPG: releaseGPG, wantErr: "not byte-identical"},
		{name: "Release.gpg for other data", ring: ring, release: release, inRelease: inRelease, releaseGPG: otherReleaseGPG, wantErr: "detached Release.gpg signature is invalid"},
		{name: "InRelease not clear-signed", ring: ring, release: release, inRelease: release, releaseGPG: releaseGPG, wantErr: "not one complete clear-signed"},
		{name: "Release.gpg invalid", ring: ring, release: release, inRelease: inRelease, releaseGPG: []byte("bad"), wantErr: "Release.gpg signature is invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := verifyReleaseSignatures(tt.ring, tt.release, tt.inRelease, tt.releaseGPG)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("verifyReleaseSignatures() error = %v", err)
				}
				if got != wantFingerprint {
					t.Fatalf("fingerprint = %q, want %q", got, wantFingerprint)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

package deb

import (
	"bytes"
	"fmt"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
)

// parseTrustedKeyRing parses an armored or binary OpenPGP public key and
// returns it as a key ring. The data must hold exactly one key and no private
// key material.
func parseTrustedKeyRing(data []byte) (openpgp.EntityList, error) {
	entities, armoredErr := openpgp.ReadArmoredKeyRing(bytes.NewReader(data))
	if armoredErr != nil {
		var err error
		entities, err = openpgp.ReadKeyRing(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf(
				"cannot parse trusted public key as armored (%v) or binary: %w",
				armoredErr,
				err,
			)
		}
	}
	if len(entities) != 1 {
		return nil, fmt.Errorf("trusted public key file must contain exactly one key, got %d", len(entities))
	}
	if entities[0].PrivateKey != nil {
		return nil, fmt.Errorf("trusted public key file contains private key material")
	}
	for _, subkey := range entities[0].Subkeys {
		if subkey.PrivateKey != nil {
			return nil, fmt.Errorf("trusted public key file contains private subkey material")
		}
	}
	return entities, nil
}

// verifyReleaseSignatures checks that InRelease is one complete clear-signed
// message whose payload is byte-identical to Release, and that Release.gpg is a
// valid armored detached signature of Release. Both signatures must come from
// the same key in keyring. It returns the signing key fingerprint.
func verifyReleaseSignatures(
	keyring openpgp.EntityList,
	releaseData []byte,
	inReleaseData []byte,
	releaseSignature []byte,
) (string, error) {
	block, rest := clearsign.Decode(inReleaseData)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return "", fmt.Errorf("InRelease is not one complete clear-signed message")
	}
	clearSigner, err := block.VerifySignature(keyring, nil)
	if err != nil {
		return "", fmt.Errorf("InRelease signature is invalid: %w", err)
	}
	if !bytes.Equal(block.Plaintext, releaseData) {
		return "", fmt.Errorf("InRelease signed payload is not byte-identical to Release")
	}

	detachedSigner, err := openpgp.CheckArmoredDetachedSignature(
		keyring,
		bytes.NewReader(releaseData),
		bytes.NewReader(releaseSignature),
		nil,
	)
	if err != nil {
		return "", fmt.Errorf("detached Release.gpg signature is invalid: %w", err)
	}

	clearFingerprint := fmt.Sprintf("%X", clearSigner.PrimaryKey.Fingerprint)
	detachedFingerprint := fmt.Sprintf("%X", detachedSigner.PrimaryKey.Fingerprint)
	if clearFingerprint != detachedFingerprint {
		return "", fmt.Errorf(
			"signatures disagree on signing key: InRelease %s, Release.gpg %s",
			clearFingerprint,
			detachedFingerprint,
		)
	}
	return clearFingerprint, nil
}

package production

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/frostyard/repogen/internal/generator/deb"
	"github.com/frostyard/repogen/internal/intake"
	"github.com/frostyard/repogen/internal/signer"
)

type Execution struct {
	Config         Config
	ConfigSHA256   string
	Policy         Policy
	PolicySHA256   string
	RequestSHA256  string
	ProvenanceHash string
	Intake         intake.Store
	Publication    deb.ProductionPublicationStore
	Signer         signer.Signer
	WorkDir        string
	ExecutablePath string
	Version        string
	Commit         string
}

func (e Execution) Run(ctx context.Context) error {
	if err := e.Config.Validate(); err != nil {
		return err
	}
	if err := e.Policy.Validate(e.Config); err != nil {
		return err
	}
	configBytes, err := intake.CanonicalJSON(e.Config)
	if err != nil {
		return fmt.Errorf("encode exact production configuration: %w", err)
	}
	policyBytes, err := intake.CanonicalJSON(e.Policy)
	if err != nil {
		return fmt.Errorf("encode exact authorization policy: %w", err)
	}
	if digest(configBytes) != e.ConfigSHA256 ||
		digest(policyBytes) != e.PolicySHA256 ||
		e.RequestSHA256 != e.Config.RequestSHA256 ||
		e.RequestSHA256 != e.Policy.RequestSHA256 ||
		e.ProvenanceHash != e.Policy.ProvenanceSHA256 {
		return fmt.Errorf("explicit production digest pins disagree")
	}
	if e.Intake == nil || e.Publication == nil || e.Signer == nil {
		return fmt.Errorf("production stores and signer are required")
	}
	publicKey, err := VerifySignerIdentity(
		e.Signer,
		e.Policy.SigningKeyFingerprint,
		e.Policy.SigningPublicKeySHA256,
	)
	if err != nil {
		return err
	}
	if err := VerifyExecutableIdentity(
		e.ExecutablePath,
		e.Version,
		e.Commit,
		e.Policy,
	); err != nil {
		return err
	}

	builder := Builder{
		Intake:          e.Intake,
		Publication:     e.Publication,
		Signer:          e.Signer,
		Policy:          e.Policy,
		WorkDir:         e.WorkDir,
		TrustedKeyBytes: publicKey,
	}
	reconciler := intake.Reconciler{
		Store:      e.Intake,
		Kind:       "debian",
		Writer:     deb.ProductionRecoveryWriter{Store: e.Publication, Builder: builder},
		Authorizer: PolicyAuthorizer{Policy: e.Policy, PolicySHA256: e.PolicySHA256},
	}
	return reconciler.ReconcileRequest(ctx, e.Config.Target, e.RequestSHA256)
}

type PolicyAuthorizer struct {
	Policy       Policy
	PolicySHA256 string
}

func (a PolicyAuthorizer) Authorize(
	_ context.Context,
	receipt intake.Receipt,
	request intake.Request,
) error {
	if receipt.PolicySHA256 != a.PolicySHA256 ||
		receipt.RequestSHA256 != a.Policy.RequestSHA256 ||
		request.ProvenanceDigest != a.Policy.ProvenanceSHA256 ||
		request.Producer != a.Policy.Producer ||
		request.Target != a.Policy.Target ||
		request.Operation != a.Policy.Operation ||
		!request.ProductionEligible {
		return fmt.Errorf("receipt and request are not covered by the exact authorization policy")
	}
	return nil
}

func VerifySignerIdentity(
	value signer.Signer,
	expectedFingerprint string,
	expectedPublicKeySHA256 string,
) ([]byte, error) {
	if value == nil || !validFingerprint(expectedFingerprint) ||
		!validSHA256(expectedPublicKeySHA256) {
		return nil, fmt.Errorf("exact signer identity pins are required")
	}
	publicKey, err := value.GetPublicKey()
	if err != nil {
		return nil, fmt.Errorf("export signing public key: %w", err)
	}
	if digest(publicKey) != expectedPublicKeySHA256 {
		return nil, fmt.Errorf("signing public key digest does not match authorization policy")
	}
	entities, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(publicKey))
	if err != nil || len(entities) != 1 {
		return nil, fmt.Errorf("signing public key must contain exactly one armored key")
	}
	fingerprint := fmt.Sprintf("%X", entities[0].PrimaryKey.Fingerprint)
	if fingerprint != expectedFingerprint {
		return nil, fmt.Errorf("signing fingerprint does not match authorization policy")
	}
	return publicKey, nil
}

func VerifyExecutableIdentity(
	filename string,
	version string,
	commit string,
	policy Policy,
) error {
	if filename == "" || version != policy.RepogenVersion ||
		commit != policy.ActionCommit {
		return fmt.Errorf("running Repogen identity does not match authorization policy")
	}
	info, err := os.Lstat(filename)
	if err != nil {
		return fmt.Errorf("inspect running Repogen executable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("running Repogen executable is not a regular file")
	}
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("open running Repogen executable: %w", err)
	}
	hash := sha256.New()
	_, readErr := io.Copy(hash, file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return fmt.Errorf("hash running Repogen executable")
	}
	if hex.EncodeToString(hash.Sum(nil)) != policy.RepogenSHA256 {
		return fmt.Errorf("running Repogen digest does not match authorization policy")
	}
	return nil
}

var _ intake.Authorizer = PolicyAuthorizer{}

package deb

import (
	"context"
	"fmt"
	"reflect"
	"regexp"

	"github.com/frostyard/repogen/internal/intake"
)

var (
	recoveryActionCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
	recoveryVersionPattern      = regexp.MustCompile(`^[A-Za-z0-9._/:@-]{1,256}$`)
)

// ProductionRecoveryPlan binds a reconstructed transaction to the exact
// action and Repogen executable attested by the request's provenance.
type ProductionRecoveryPlan struct {
	Transaction      *ProductionTransaction
	ProvenanceDigest string
	ActionCommit     string
	RepogenVersion   string
	RepogenSHA256    string
}

// ProductionRecoveryBuilder reconstructs a clean, signed production plan
// exclusively from one verified durable intake request and its provenance.
type ProductionRecoveryBuilder interface {
	Build(ctx context.Context, receipt intake.Receipt, request intake.Request) (*ProductionRecoveryPlan, error)
}

// ProductionRecoveryBuilderFunc adapts a function to ProductionRecoveryBuilder.
type ProductionRecoveryBuilderFunc func(
	context.Context,
	intake.Receipt,
	intake.Request,
) (*ProductionRecoveryPlan, error)

func (f ProductionRecoveryBuilderFunc) Build(
	ctx context.Context,
	receipt intake.Receipt,
	request intake.Request,
) (*ProductionRecoveryPlan, error) {
	return f(ctx, receipt, request)
}

// ProductionRecoveryWriter binds durable receipt recovery to the scoped B5
// Debian publication transaction. It has no submission or credential path.
type ProductionRecoveryWriter struct {
	Store   ProductionPublicationStore
	Builder ProductionRecoveryBuilder
}

func (w ProductionRecoveryWriter) Apply(
	ctx context.Context,
	receipt intake.Receipt,
	request intake.Request,
) (*intake.Result, error) {
	if w.Store == nil || w.Builder == nil {
		return nil, fmt.Errorf("%w: publication store and transaction builder are required", ErrPublicationCandidate)
	}
	plan, err := w.Builder.Build(ctx, receipt, request)
	if err != nil {
		return nil, fmt.Errorf("%w: reconstruct durable transaction: %v", ErrPublicationCandidate, err)
	}
	if plan == nil || plan.Transaction == nil {
		return nil, fmt.Errorf("%w: reconstructed durable plan is empty", ErrPublicationCandidate)
	}
	transaction := plan.Transaction
	if !request.ProductionEligible ||
		plan.ProvenanceDigest != request.ProvenanceDigest ||
		!recoveryActionCommitPattern.MatchString(plan.ActionCommit) ||
		!recoveryVersionPattern.MatchString(plan.RepogenVersion) ||
		!sha256Pattern.MatchString(plan.RepogenSHA256) ||
		transaction.Codename != request.Target ||
		request.Codename != request.Target ||
		transaction.Operation != request.Operation ||
		request.Suite != request.Target ||
		request.Component != "main" ||
		!reflect.DeepEqual(request.Architectures, ProductionArchitectures()) ||
		request.Origin != productionReleaseOrigin ||
		request.Label != productionReleaseLabel ||
		request.ValidUntilPolicy != "omit" ||
		(request.ExpectedPrior == nil && transaction.RequestManifest.ExpectedPriorReleaseSHA256 != "") ||
		(request.ExpectedPrior != nil && transaction.RequestManifest.ExpectedPriorReleaseSHA256 != *request.ExpectedPrior) {
		return nil, fmt.Errorf("%w: reconstructed transaction does not match durable request", ErrPublicationCandidate)
	}
	published, err := RecoverProductionTransaction(ctx, w.Store, transaction)
	if err != nil {
		return nil, err
	}
	result := &intake.Result{
		SigningKeyFingerprint: published.SigningKeyFingerprint,
		StateSHA256:           published.ReleaseSHA256,
		CommitSHA256:          published.InReleaseSHA256,
		ActionCommit:          plan.ActionCommit,
		RepogenVersion:        plan.RepogenVersion,
		RepogenSHA256:         plan.RepogenSHA256,
		Objects:               make([]intake.Object, 0, len(published.Objects)),
	}
	for _, object := range published.Objects {
		result.Objects = append(result.Objects, intake.Object{
			Key:    object.Path,
			SHA256: object.SHA256,
			Size:   object.Size,
		})
	}
	return result, nil
}

func (w ProductionRecoveryWriter) Verify(
	ctx context.Context,
	_ intake.Receipt,
	request intake.Request,
	result intake.Result,
) error {
	if w.Store == nil {
		return fmt.Errorf("%w: publication store is required", ErrPublicationCandidate)
	}
	if result.Target != request.Target {
		return fmt.Errorf("%w: durable result target does not match request", ErrPublicationReadBack)
	}
	var releaseSHA256, inReleaseSHA256 string
	for _, object := range result.Objects {
		expected := ProductionManifestObject{
			Path:   object.Key,
			SHA256: object.SHA256,
			Size:   object.Size,
		}
		if err := verifyPublishedProductionObject(ctx, w.Store, expected); err != nil {
			return err
		}
		switch object.Key {
		case "dists/" + request.Target + "/Release":
			releaseSHA256 = object.SHA256
		case "dists/" + request.Target + "/InRelease":
			inReleaseSHA256 = object.SHA256
		}
	}
	if releaseSHA256 != result.StateSHA256 || inReleaseSHA256 != result.CommitSHA256 {
		return fmt.Errorf("%w: durable result commit digests do not match public objects", ErrPublicationReadBack)
	}
	return nil
}

package production

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/frostyard/repogen/internal/generator/deb"
	"github.com/frostyard/repogen/internal/intake"
	"github.com/frostyard/repogen/internal/models"
	"github.com/frostyard/repogen/internal/signer"
)

const provenanceSchema = "org.frostyard.gchlog.canary-provenance.v1"

type provenanceProducer struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Tree       string `json:"tree"`
	Ref        string `json:"ref"`
}

type provenanceBuilder struct {
	Mode                     string `json:"mode"`
	GoVersion                string `json:"go_version"`
	GoReleaserVersion        string `json:"goreleaser_version"`
	GoReleaserBinarySHA256   string `json:"goreleaser_binary_sha256"`
	Target                   string `json:"target"`
	InstallVerificationImage string `json:"install_verification_image"`
	SourceCommitTimestampUTC string `json:"source_commit_timestamp_utc"`
}

type provenanceArtifact struct {
	Architecture string `json:"architecture"`
	Filename     string `json:"filename"`
	MediaType    string `json:"media_type"`
	PackageName  string `json:"package_name"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	Version      string `json:"version"`
}

type snapshotArtifact struct {
	MediaType         string `json:"media_type"`
	Name              string `json:"name"`
	SelectedForIntake bool   `json:"selected_for_intake"`
	SHA256            string `json:"sha256"`
	Size              int64  `json:"size"`
}

type provenance struct {
	Schema            string             `json:"schema"`
	Evidence          string             `json:"evidence"`
	Producer          provenanceProducer `json:"producer"`
	Builder           provenanceBuilder  `json:"builder"`
	Artifact          provenanceArtifact `json:"artifact"`
	SnapshotArtifacts []snapshotArtifact `json:"snapshot_artifacts"`
}

type Builder struct {
	Intake          intake.Store
	Publication     deb.ProductionPublicationStore
	Signer          signer.Signer
	Policy          Policy
	WorkDir         string
	TrustedKeyBytes []byte
}

func (b Builder) Build(
	ctx context.Context,
	receipt intake.Receipt,
	request intake.Request,
) (*deb.ProductionRecoveryPlan, error) {
	if b.Intake == nil || b.Publication == nil || b.Signer == nil ||
		b.WorkDir == "" || !filepath.IsAbs(b.WorkDir) {
		return nil, fmt.Errorf("production builder is incomplete")
	}
	if receipt.RequestSHA256 != b.Policy.RequestSHA256 ||
		receipt.PolicySHA256 == "" ||
		request.ProvenanceDigest != b.Policy.ProvenanceSHA256 ||
		request.Producer != b.Policy.Producer ||
		request.Operation != b.Policy.Operation ||
		request.Target != b.Policy.Target {
		return nil, fmt.Errorf("durable request does not match exact authorization policy")
	}

	provenanceValue, err := b.loadProvenance(ctx, request)
	if err != nil {
		return nil, err
	}
	packages, err := b.loadPackages(ctx, request, provenanceValue)
	if err != nil {
		return nil, err
	}
	releaseTime, err := time.Parse(time.RFC3339, b.Policy.ReleaseTime)
	if err != nil {
		return nil, fmt.Errorf("parse policy release time: %w", err)
	}

	var prior *deb.ProductionState
	if request.Operation == "reconcile" {
		if request.ExpectedPrior == nil {
			return nil, fmt.Errorf("reconcile request has no expected prior")
		}
		prior, err = b.restorePrior(ctx, request, *request.ExpectedPrior)
		if err != nil {
			return nil, err
		}
	}
	stageDir, err := b.allocateStageDir()
	if err != nil {
		return nil, err
	}
	transaction, err := deb.StageProductionTransaction(stageDir, deb.ProductionStageRequest{
		RequestID:                  receipt.RequestSHA256,
		Operation:                  request.Operation,
		Codename:                   request.Target,
		ReleaseTime:                releaseTime,
		Packages:                   packages,
		Signer:                     b.Signer,
		ExpectedPriorReleaseSHA256: optionalDigest(request.ExpectedPrior),
		PriorState:                 prior,
	})
	if err != nil {
		return nil, err
	}
	return &deb.ProductionRecoveryPlan{
		Transaction:      transaction,
		ProvenanceDigest: request.ProvenanceDigest,
		ActionCommit:     b.Policy.ActionCommit,
		RepogenVersion:   b.Policy.RepogenVersion,
		RepogenSHA256:    b.Policy.RepogenSHA256,
	}, nil
}

func (b Builder) loadProvenance(
	ctx context.Context,
	request intake.Request,
) (provenance, error) {
	key := path.Join(
		"manifests/provenance/v1/sha256",
		request.ProvenanceDigest+".json",
	)
	data, err := readExact(ctx, b.Intake, key, 4<<20, request.ProvenanceDigest)
	if err != nil {
		return provenance{}, fmt.Errorf("read exact provenance: %w", err)
	}
	var value provenance
	if err := intake.DecodeCanonical(data, &value); err != nil {
		return provenance{}, err
	}
	if value.Schema != provenanceSchema || value.Evidence == "" ||
		value.Producer.Repository != b.Policy.Producer ||
		value.Producer.Commit != b.Policy.ProducerCommit ||
		value.Producer.Tree != b.Policy.ProducerTree ||
		!validSHA256(value.Artifact.SHA256) || value.Artifact.Size < 0 ||
		value.Artifact.MediaType != "application/vnd.debian.binary-package" ||
		value.Artifact.Architecture != "amd64" {
		return provenance{}, fmt.Errorf("provenance does not match exact authorization policy")
	}
	if len(request.ArtifactDigests) != 1 ||
		request.ArtifactDigests[0] != value.Artifact.SHA256 {
		return provenance{}, fmt.Errorf("request artifact does not match provenance")
	}
	approved, ok := b.Policy.PoolDigests()[poolPath(value.Artifact)]
	if !ok || approved.SHA256 != value.Artifact.SHA256 ||
		approved.Size != value.Artifact.Size {
		return provenance{}, fmt.Errorf("provenance artifact is not an exact approved pool object")
	}
	return value, nil
}

func (b Builder) loadPackages(
	ctx context.Context,
	request intake.Request,
	value provenance,
) ([]deb.ProductionPackageInput, error) {
	if err := os.MkdirAll(b.WorkDir, 0o700); err != nil {
		return nil, fmt.Errorf("create production work directory: %w", err)
	}
	artifactPath := filepath.Join(b.WorkDir, value.Artifact.Filename)
	if filepath.Base(value.Artifact.Filename) != value.Artifact.Filename ||
		filepath.Ext(value.Artifact.Filename) != ".deb" {
		return nil, fmt.Errorf("provenance artifact filename is unsafe")
	}
	body, err := b.Intake.Open(
		ctx,
		path.Join("blobs/sha256", value.Artifact.SHA256[:2], value.Artifact.SHA256),
	)
	if err != nil {
		return nil, fmt.Errorf("open exact package artifact: %w", err)
	}
	file, err := os.OpenFile(artifactPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = body.Close()
		return nil, fmt.Errorf("create package staging file: %w", err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(
		io.MultiWriter(file, hash),
		io.LimitReader(&contextReader{ctx: ctx, reader: body}, value.Artifact.Size+1),
	)
	bodyCloseErr := body.Close()
	fileCloseErr := file.Close()
	if copyErr != nil || bodyCloseErr != nil || fileCloseErr != nil {
		return nil, fmt.Errorf("copy package artifact failed")
	}
	if written != value.Artifact.Size ||
		hex.EncodeToString(hash.Sum(nil)) != value.Artifact.SHA256 {
		return nil, fmt.Errorf("package artifact bytes do not match provenance")
	}
	pkg, err := deb.ParsePackage(artifactPath)
	if err != nil {
		return nil, fmt.Errorf("parse exact package artifact: %w", err)
	}
	if pkg.Name != value.Artifact.PackageName ||
		pkg.Version != value.Artifact.Version ||
		pkg.Architecture != value.Artifact.Architecture ||
		pkg.SHA256Sum != value.Artifact.SHA256 ||
		pkg.Size != value.Artifact.Size {
		return nil, fmt.Errorf("parsed package identity does not match provenance")
	}
	if len(request.Architectures) != 2 ||
		request.Architectures[0] != "all" ||
		request.Architectures[1] != "amd64" {
		return nil, fmt.Errorf("request architecture order is not the production contract")
	}
	return []deb.ProductionPackageInput{{
		Package:    *pkg,
		SourcePath: artifactPath,
	}}, nil
}

func (b Builder) restorePrior(
	ctx context.Context,
	request intake.Request,
	expectedReleaseSHA256 string,
) (*deb.ProductionState, error) {
	root := filepath.Join(b.WorkDir, "prior")
	prefix := path.Join("dists", request.Target) + "/"
	keys, err := b.Publication.ListPrefix(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("enumerate exact prior suite: %w", err)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("reconcile target is authoritatively absent")
	}
	for _, key := range keys {
		if !strings.HasPrefix(key, prefix) || path.Clean(key) != key {
			return nil, fmt.Errorf("prior suite returned an unsafe object")
		}
		object, err := b.Publication.Open(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("open prior suite object: %w", err)
		}
		destination := filepath.Join(root, filepath.FromSlash(key))
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			_ = object.Body.Close()
			return nil, err
		}
		file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = object.Body.Close()
			return nil, err
		}
		_, copyErr := io.Copy(file, &contextReader{ctx: ctx, reader: object.Body})
		closeErr := errorsJoin(object.Body.Close(), file.Close())
		if copyErr != nil || closeErr != nil {
			return nil, fmt.Errorf("materialize prior suite failed")
		}
	}
	keyPath := filepath.Join(b.WorkDir, "trusted-public.key")
	if err := os.WriteFile(keyPath, b.TrustedKeyBytes, 0o600); err != nil {
		return nil, fmt.Errorf("write temporary trusted public key: %w", err)
	}
	return deb.RestoreProductionState(&models.RepositoryConfig{
		OutputDir:  root,
		Codename:   request.Target,
		Suite:      request.Target,
		Origin:     request.Origin,
		Label:      request.Label,
		Components: []string{request.Component},
		Arches:     append([]string(nil), request.Architectures...),
	}, keyPath, expectedReleaseSHA256)
}

func (b Builder) allocateStageDir() (string, error) {
	parent, err := os.MkdirTemp(b.WorkDir, "transaction-")
	if err != nil {
		return "", fmt.Errorf("allocate transaction staging path: %w", err)
	}
	if err := os.Remove(parent); err != nil {
		return "", fmt.Errorf("prepare transaction staging path: %w", err)
	}
	return parent, nil
}

func readExact(
	ctx context.Context,
	store intake.Store,
	key string,
	maxSize int64,
	expectedSHA256 string,
) ([]byte, error) {
	body, err := store.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(
		io.LimitReader(&contextReader{ctx: ctx, reader: body}, maxSize+1),
	)
	closeErr := body.Close()
	if readErr != nil || closeErr != nil {
		return nil, fmt.Errorf("read immutable object failed")
	}
	if int64(len(data)) > maxSize || digest(data) != expectedSHA256 {
		return nil, fmt.Errorf("immutable object digest or size is invalid")
	}
	return data, nil
}

func poolPath(artifact provenanceArtifact) string {
	shard := "0"
	if artifact.PackageName != "" && artifact.PackageName[0] >= 'a' &&
		artifact.PackageName[0] <= 'z' {
		shard = artifact.PackageName[:1]
	}
	return path.Join(
		"pool",
		"main",
		shard,
		artifact.PackageName,
		artifact.Filename,
	)
}

func optionalDigest(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func errorsJoin(values ...error) error {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(body []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(body)
}

var _ deb.ProductionRecoveryBuilder = Builder{}

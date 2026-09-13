package deb

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/frostyard/repogen/internal/models"
	"github.com/frostyard/repogen/internal/signer"
	"github.com/frostyard/repogen/internal/utils"
)

const productionManifestVersion = 1

var (
	ErrPublicationState     = errors.New("production publication state mismatch")
	ErrPublicationWrite     = errors.New("production publication write failed")
	ErrPublicationReadBack  = errors.New("production publication read-back failed")
	ErrPublicationCandidate = errors.New("invalid production publication candidate")
)

type ProductionObjectDigest struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type ProductionPackageInput struct {
	Package    models.Package
	SourcePath string
}

type ProductionStageRequest struct {
	RequestID                  string
	Operation                  string
	Codename                   string
	ReleaseTime                time.Time
	Packages                   []ProductionPackageInput
	Signer                     signer.Signer
	ExpectedPriorReleaseSHA256 string
	PriorState                 *ProductionState
}

type ProductionManifestObject struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type ProductionRequestManifest struct {
	SchemaVersion              int                        `json:"schema_version"`
	RequestID                  string                     `json:"request_id"`
	Operation                  string                     `json:"operation"`
	Target                     string                     `json:"target"`
	SigningKeyFingerprint      string                     `json:"signing_key_fingerprint"`
	ExpectedPriorReleaseSHA256 string                     `json:"expected_prior_release_sha256,omitempty"`
	Packages                   []ProductionManifestObject `json:"packages"`
	Objects                    []ProductionManifestObject `json:"objects"`
}

type ProductionResultManifest struct {
	SchemaVersion         int                        `json:"schema_version"`
	RequestSHA256         string                     `json:"request_sha256"`
	Target                string                     `json:"target"`
	SigningKeyFingerprint string                     `json:"signing_key_fingerprint"`
	ReleaseSHA256         string                     `json:"release_sha256"`
	InReleaseSHA256       string                     `json:"inrelease_sha256"`
	Objects               []ProductionManifestObject `json:"objects"`
}

type productionObjectKind int

const (
	productionPoolObject productionObjectKind = iota
	productionByHashObject
	productionIndexObject
	productionReleaseObject
	productionReleaseSignatureObject
	productionInReleaseObject
)

type stagedProductionObject struct {
	ProductionManifestObject
	kind      productionObjectKind
	localPath string
}

type ProductionTransaction struct {
	Codename              string
	Operation             string
	StageDir              string
	RequestManifest       ProductionRequestManifest
	RequestSHA256         string
	SigningKeyFingerprint string
	ExpectedPrior         map[string]ProductionObjectDigest
	VerifiedSharedPool    map[string]PoolDigest
	objects               []stagedProductionObject
}

type ProductionPublicationLock interface {
	Release() error
}

type ProductionPublicationStore interface {
	PoolObjectStore
	Replace(
		ctx context.Context,
		key string,
		body io.Reader,
		size int64,
		expectedSHA256 string,
	) error
	PrefixExists(ctx context.Context, prefix string) (bool, error)
	ListPrefix(ctx context.Context, prefix string) ([]string, error)
	Acquire(ctx context.Context, target string) (ProductionPublicationLock, error)
}

func productionPoolDigests(state *ProductionState) map[string]PoolDigest {
	if state == nil || !state.verified {
		return nil
	}
	digests := make(map[string]PoolDigest, len(state.poolDigests))
	for key, digest := range state.poolDigests {
		digests[key] = digest
	}
	return digests
}

func StageProductionTransaction(stageDir string, request ProductionStageRequest) (_ *ProductionTransaction, retErr error) {
	return stageProductionTransaction(context.Background(), stageDir, request, nil)
}

func stageProductionTransaction(
	ctx context.Context,
	stageDir string,
	request ProductionStageRequest,
	hooks *productionLocalHooks,
) (_ *ProductionTransaction, retErr error) {
	if err := validateProductionStageRequest(stageDir, request); err != nil {
		return nil, err
	}
	if err := beforeProductionLocalStep(ctx, hooks, "stage:create"); err != nil {
		return nil, err
	}
	if err := os.Mkdir(stageDir, 0o700); err != nil {
		return nil, fmt.Errorf("%w: create clean staging directory: %v", ErrPublicationCandidate, err)
	}
	defer func() {
		if retErr != nil {
			_ = os.RemoveAll(stageDir)
		}
	}()

	transaction := &ProductionTransaction{
		Codename:  request.Codename,
		Operation: request.Operation,
		StageDir:  stageDir,
	}
	if request.PriorState != nil {
		transaction.ExpectedPrior = copyProductionDigests(request.PriorState.objects)
		transaction.VerifiedSharedPool = productionPoolDigests(request.PriorState)
	} else {
		transaction.ExpectedPrior = make(map[string]ProductionObjectDigest)
		transaction.VerifiedSharedPool = make(map[string]PoolDigest)
	}

	packages, packageManifest, poolObjects, err := stageProductionPackages(
		ctx,
		stageDir,
		request.Packages,
		hooks,
	)
	if err != nil {
		return nil, err
	}
	transaction.objects = append(transaction.objects, poolObjects...)

	indexObjects, releaseInputs, err := stageProductionIndexes(
		ctx,
		stageDir,
		request.Codename,
		packages,
		hooks,
	)
	if err != nil {
		return nil, err
	}
	transaction.objects = append(transaction.objects, indexObjects...)

	if err := beforeProductionLocalStep(ctx, hooks, "stage:generate-release"); err != nil {
		return nil, err
	}
	release := generateProductionRelease(request.Codename, request.ReleaseTime, releaseInputs)
	if err := beforeProductionLocalStep(ctx, hooks, "stage:sign-inrelease"); err != nil {
		return nil, err
	}
	inRelease, err := request.Signer.SignCleartext(release)
	if err != nil {
		return nil, fmt.Errorf("%w: sign InRelease: %v", ErrPublicationCandidate, err)
	}
	if err := beforeProductionLocalStep(ctx, hooks, "stage:sign-release-gpg"); err != nil {
		return nil, err
	}
	releaseSignature, err := request.Signer.SignDetached(release)
	if err != nil {
		return nil, fmt.Errorf("%w: sign Release.gpg: %v", ErrPublicationCandidate, err)
	}
	if err := beforeProductionLocalStep(ctx, hooks, "stage:export-public-key"); err != nil {
		return nil, err
	}
	publicKey, err := request.Signer.GetPublicKey()
	if err != nil {
		return nil, fmt.Errorf("%w: export signing public key: %v", ErrPublicationCandidate, err)
	}
	if err := beforeProductionLocalStep(ctx, hooks, "stage:verify-signatures"); err != nil {
		return nil, err
	}
	if err := verifyStagedProductionSignatures(publicKey, release, inRelease, releaseSignature); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPublicationCandidate, err)
	}
	publicEntities, err := parseProductionKeyRing(publicKey)
	if err != nil {
		return nil, fmt.Errorf("%w: parse signing public key: %v", ErrPublicationCandidate, err)
	}
	transaction.SigningKeyFingerprint = fmt.Sprintf("%X", publicEntities[0].PrimaryKey.Fingerprint)

	suitePrefix := path.Join("dists", request.Codename)
	for _, object := range []struct {
		name string
		data []byte
		kind productionObjectKind
	}{
		{name: "Release", data: release, kind: productionReleaseObject},
		{name: "Release.gpg", data: releaseSignature, kind: productionReleaseSignatureObject},
		{name: "InRelease", data: inRelease, kind: productionInReleaseObject},
	} {
		staged, err := stageProductionBytes(
			ctx,
			stageDir,
			path.Join(suitePrefix, object.name),
			object.data,
			object.kind,
			hooks,
		)
		if err != nil {
			return nil, err
		}
		transaction.objects = append(transaction.objects, staged)
	}

	sort.SliceStable(transaction.objects, func(i, j int) bool {
		if transaction.objects[i].kind != transaction.objects[j].kind {
			return transaction.objects[i].kind < transaction.objects[j].kind
		}
		return transaction.objects[i].Path < transaction.objects[j].Path
	})
	if err := validateProductionWritePlan(transaction, request.ExpectedPriorReleaseSHA256); err != nil {
		return nil, err
	}

	manifestObjects := make([]ProductionManifestObject, 0, len(transaction.objects))
	for _, object := range transaction.objects {
		manifestObjects = append(manifestObjects, object.ProductionManifestObject)
	}
	transaction.RequestManifest = ProductionRequestManifest{
		SchemaVersion:              productionManifestVersion,
		RequestID:                  request.RequestID,
		Operation:                  request.Operation,
		Target:                     suitePrefix,
		SigningKeyFingerprint:      transaction.SigningKeyFingerprint,
		ExpectedPriorReleaseSHA256: request.ExpectedPriorReleaseSHA256,
		Packages:                   packageManifest,
		Objects:                    manifestObjects,
	}
	manifestData, err := json.Marshal(transaction.RequestManifest)
	if err != nil {
		return nil, fmt.Errorf("%w: encode request manifest: %v", ErrPublicationCandidate, err)
	}
	manifestData = append(manifestData, '\n')
	if err := beforeProductionLocalStep(ctx, hooks, "stage:write-request-manifest"); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(stageDir, "request.json"), manifestData, 0o600); err != nil {
		return nil, fmt.Errorf("%w: write request manifest: %v", ErrPublicationCandidate, err)
	}
	transaction.RequestSHA256 = productionSHA256Hex(manifestData)
	return transaction, nil
}

func PublishProductionTransaction(
	ctx context.Context,
	store ProductionPublicationStore,
	transaction *ProductionTransaction,
) (_ *ProductionResultManifest, retErr error) {
	if store == nil || transaction == nil {
		return nil, fmt.Errorf("%w: store and transaction are required", ErrPublicationCandidate)
	}
	if err := validateProductionTransactionIdentity(transaction); err != nil {
		return nil, err
	}
	if err := validateStagedProductionObjects(transaction); err != nil {
		return nil, err
	}

	lock, err := store.Acquire(ctx, transaction.Codename)
	if err != nil {
		return nil, fmt.Errorf("%w: acquire %s serialization: %v", ErrPublicationState, transaction.Codename, err)
	}
	if lock == nil {
		return nil, fmt.Errorf("%w: store returned no serialization lock", ErrPublicationState)
	}
	defer func() {
		if err := lock.Release(); retErr == nil && err != nil {
			retErr = fmt.Errorf("%w: release %s serialization: %v", ErrPublicationState, transaction.Codename, err)
		}
	}()

	if err := verifyProductionPrior(ctx, store, transaction); err != nil {
		return nil, err
	}
	pool, err := NewSharedPool(store, transaction.VerifiedSharedPool)
	if err != nil {
		return nil, err
	}

	for _, object := range transaction.objects {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := os.Open(object.localPath)
		if err != nil {
			return nil, fmt.Errorf("%w: open staged %s: %v", ErrPublicationCandidate, object.Path, err)
		}

		switch object.kind {
		case productionPoolObject:
			_, err = pool.Ensure(ctx, PoolCandidate{
				Path:    object.Path,
				SHA256:  object.SHA256,
				Size:    object.Size,
				Content: file,
			})
		case productionByHashObject:
			err = createImmutableProductionObject(ctx, store, object, file)
		default:
			err = writeMutableProductionObject(ctx, store, transaction, object, file)
		}
		closeErr := file.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, fmt.Errorf("%w: close staged %s: %v", ErrPublicationCandidate, object.Path, closeErr)
		}
		if err := verifyPublishedProductionObject(ctx, store, object.ProductionManifestObject); err != nil {
			return nil, err
		}
	}

	result := &ProductionResultManifest{
		SchemaVersion:         productionManifestVersion,
		RequestSHA256:         transaction.RequestSHA256,
		Target:                path.Join("dists", transaction.Codename),
		SigningKeyFingerprint: transaction.SigningKeyFingerprint,
		Objects:               make([]ProductionManifestObject, 0, len(transaction.objects)),
	}
	for _, object := range transaction.objects {
		result.Objects = append(result.Objects, object.ProductionManifestObject)
		switch object.kind {
		case productionReleaseObject:
			result.ReleaseSHA256 = object.SHA256
		case productionInReleaseObject:
			result.InReleaseSHA256 = object.SHA256
		}
	}
	return result, nil
}

// RecoverProductionTransaction resumes an interrupted publication by
// accepting only exact prior or exact candidate bytes. It never rewrites a
// candidate object and rejects every third state before another write.
func RecoverProductionTransaction(
	ctx context.Context,
	store ProductionPublicationStore,
	transaction *ProductionTransaction,
) (_ *ProductionResultManifest, retErr error) {
	if store == nil || transaction == nil {
		return nil, fmt.Errorf("%w: store and transaction are required", ErrPublicationCandidate)
	}
	if err := validateProductionTransactionIdentity(transaction); err != nil {
		return nil, err
	}
	if err := validateStagedProductionObjects(transaction); err != nil {
		return nil, err
	}

	lock, err := store.Acquire(ctx, transaction.Codename)
	if err != nil {
		return nil, fmt.Errorf("%w: acquire %s recovery serialization: %v", ErrPublicationState, transaction.Codename, err)
	}
	if lock == nil {
		return nil, fmt.Errorf("%w: store returned no recovery serialization lock", ErrPublicationState)
	}
	defer func() {
		if err := lock.Release(); retErr == nil && err != nil {
			retErr = fmt.Errorf("%w: release %s recovery serialization: %v", ErrPublicationState, transaction.Codename, err)
		}
	}()

	candidatePresent, err := classifyRecoverableProductionObjects(ctx, store, transaction)
	if err != nil {
		return nil, err
	}
	pool, err := NewSharedPool(store, transaction.VerifiedSharedPool)
	if err != nil {
		return nil, err
	}
	for _, object := range transaction.objects {
		if candidatePresent[object.Path] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := os.Open(object.localPath)
		if err != nil {
			return nil, fmt.Errorf("%w: open staged %s: %v", ErrPublicationCandidate, object.Path, err)
		}
		switch object.kind {
		case productionPoolObject:
			_, err = pool.Ensure(ctx, PoolCandidate{
				Path:    object.Path,
				SHA256:  object.SHA256,
				Size:    object.Size,
				Content: file,
			})
		case productionByHashObject:
			err = createImmutableProductionObject(ctx, store, object, file)
		default:
			err = writeMutableProductionObject(ctx, store, transaction, object, file)
		}
		closeErr := file.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, fmt.Errorf("%w: close staged %s: %v", ErrPublicationCandidate, object.Path, closeErr)
		}
		if err := verifyPublishedProductionObject(ctx, store, object.ProductionManifestObject); err != nil {
			return nil, err
		}
	}
	for _, object := range transaction.objects {
		if err := verifyPublishedProductionObject(ctx, store, object.ProductionManifestObject); err != nil {
			return nil, err
		}
	}
	return productionResultForTransaction(transaction), nil
}

func (r *ProductionResultManifest) Marshal() ([]byte, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func classifyRecoverableProductionObjects(
	ctx context.Context,
	store ProductionPublicationStore,
	transaction *ProductionTransaction,
) (map[string]bool, error) {
	if transaction.Operation == "initialize" {
		prefix := path.Join("dists", transaction.Codename) + "/"
		keys, err := store.ListPrefix(ctx, prefix)
		if err != nil {
			return nil, fmt.Errorf("%w: enumerate initialize target: %v", ErrPublicationState, err)
		}
		planned := make(map[string]struct{})
		for _, object := range transaction.objects {
			if strings.HasPrefix(object.Path, prefix) {
				planned[object.Path] = struct{}{}
			}
		}
		for _, key := range keys {
			if _, ok := planned[key]; !ok {
				return nil, fmt.Errorf("%w: initialize target contains unexpected object %s", ErrPublicationState, key)
			}
		}
	}

	candidatePresent := make(map[string]bool, len(transaction.objects))
	for _, object := range transaction.objects {
		observed, exists, err := inspectPublishedProductionObject(ctx, store, object.Path)
		if err != nil {
			return nil, err
		}
		if !exists {
			if transaction.Operation == "reconcile" && object.kind >= productionIndexObject {
				return nil, fmt.Errorf("%w: prior mutable object %s is absent", ErrPublicationState, object.Path)
			}
			continue
		}
		if observed.SHA256 == object.SHA256 && observed.Size == object.Size {
			candidatePresent[object.Path] = true
			continue
		}
		if transaction.Operation == "reconcile" && object.kind >= productionIndexObject {
			prior, ok := transaction.ExpectedPrior[object.Path]
			if ok && observed.SHA256 == prior.SHA256 && observed.Size == prior.Size {
				continue
			}
		}
		return nil, fmt.Errorf("%w: object %s is neither prior nor candidate bytes", ErrPublicationState, object.Path)
	}
	commitPath := path.Join("dists", transaction.Codename, "InRelease")
	if candidatePresent[commitPath] {
		for _, object := range transaction.objects {
			if !candidatePresent[object.Path] {
				return nil, fmt.Errorf(
					"%w: candidate InRelease is visible while %s is not candidate bytes",
					ErrPublicationState,
					object.Path,
				)
			}
		}
	}
	return candidatePresent, nil
}

func inspectPublishedProductionObject(
	ctx context.Context,
	store ProductionPublicationStore,
	key string,
) (ProductionObjectDigest, bool, error) {
	object, err := store.Open(ctx, key)
	if errors.Is(err, ErrPoolObjectNotFound) {
		return ProductionObjectDigest{}, false, nil
	}
	if err != nil {
		return ProductionObjectDigest{}, false, fmt.Errorf("%w: inspect %s: %v", ErrPublicationReadBack, key, err)
	}
	if object == nil || object.Body == nil {
		return ProductionObjectDigest{}, false, fmt.Errorf("%w: store returned no body for %s", ErrPublicationReadBack, key)
	}
	observed, err := hashRemote(ctx, object.Body)
	if err != nil {
		return ProductionObjectDigest{}, false, fmt.Errorf("%w: hash %s: %v", ErrPublicationReadBack, key, err)
	}
	return ProductionObjectDigest(observed), true, nil
}

func productionResultForTransaction(transaction *ProductionTransaction) *ProductionResultManifest {
	result := &ProductionResultManifest{
		SchemaVersion:         productionManifestVersion,
		RequestSHA256:         transaction.RequestSHA256,
		Target:                path.Join("dists", transaction.Codename),
		SigningKeyFingerprint: transaction.SigningKeyFingerprint,
		Objects:               make([]ProductionManifestObject, 0, len(transaction.objects)),
	}
	for _, object := range transaction.objects {
		result.Objects = append(result.Objects, object.ProductionManifestObject)
		switch object.kind {
		case productionReleaseObject:
			result.ReleaseSHA256 = object.SHA256
		case productionInReleaseObject:
			result.InReleaseSHA256 = object.SHA256
		}
	}
	return result
}

func validateProductionStageRequest(stageDir string, request ProductionStageRequest) error {
	if stageDir == "" || filepath.Clean(stageDir) != stageDir {
		return fmt.Errorf("%w: staging path must be explicit and clean", ErrPublicationCandidate)
	}
	if _, err := os.Lstat(stageDir); err == nil {
		return fmt.Errorf("%w: staging path %q already exists", ErrPublicationCandidate, stageDir)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("%w: inspect staging path: %v", ErrPublicationCandidate, err)
	}
	if request.Signer == nil {
		return fmt.Errorf("%w: production signing is required", ErrPublicationCandidate)
	}
	if request.RequestID == "" || strings.IndexFunc(request.RequestID, isUnsafeManifestRune) >= 0 {
		return fmt.Errorf("%w: request ID is empty or unsafe", ErrPublicationCandidate)
	}
	if request.Codename == "" ||
		!productionPackageNamePattern.MatchString(request.Codename) ||
		request.Codename == "." ||
		request.Codename == ".." {
		return fmt.Errorf("%w: unsafe codename %q", ErrPublicationCandidate, request.Codename)
	}
	switch request.Codename {
	case "stable", "testing", "unstable", "sid", "experimental", "oldstable", "oldoldstable":
		return fmt.Errorf("%w: mutable or reserved codename %q", ErrPublicationCandidate, request.Codename)
	}
	if request.ReleaseTime.IsZero() {
		return fmt.Errorf("%w: explicit release time is required", ErrPublicationCandidate)
	}
	if len(request.Packages) == 0 {
		return fmt.Errorf("%w: at least one Debian package is required", ErrPublicationCandidate)
	}
	switch request.Operation {
	case "initialize":
		if request.ExpectedPriorReleaseSHA256 != "" || request.PriorState != nil {
			return fmt.Errorf("%w: initialize must not carry expected prior state", ErrPublicationCandidate)
		}
	case "reconcile":
		if !sha256Pattern.MatchString(request.ExpectedPriorReleaseSHA256) {
			return fmt.Errorf("%w: reconcile requires an exact prior Release SHA-256", ErrPublicationCandidate)
		}
		if request.PriorState == nil {
			return fmt.Errorf("%w: reconcile requires verified prior state", ErrPublicationCandidate)
		}
		if !request.PriorState.verified {
			return fmt.Errorf("%w: reconcile prior state did not pass strict signed restore", ErrPublicationCandidate)
		}
		if request.PriorState.ReleaseSHA256 != request.ExpectedPriorReleaseSHA256 {
			return fmt.Errorf("%w: reconcile digest does not match verified prior state", ErrPublicationCandidate)
		}
	default:
		return fmt.Errorf("%w: operation must be initialize or reconcile", ErrPublicationCandidate)
	}
	return nil
}

func stageProductionPackages(
	ctx context.Context,
	stageDir string,
	inputs []ProductionPackageInput,
	hooks *productionLocalHooks,
) ([]models.Package, []ProductionManifestObject, []stagedProductionObject, error) {
	packages := make([]models.Package, 0, len(inputs))
	manifest := make([]ProductionManifestObject, 0, len(inputs))
	objects := make([]stagedProductionObject, 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs))

	for _, input := range inputs {
		if err := validateProductionPackageForStage(input.Package); err != nil {
			return nil, nil, nil, err
		}
		if input.SourcePath == "" {
			return nil, nil, nil, fmt.Errorf("%w: package source path is required", ErrPublicationCandidate)
		}
		base := filepath.Base(input.SourcePath)
		if base != path.Base(base) ||
			!strings.HasPrefix(base, input.Package.Name+"_") ||
			!strings.HasSuffix(base, ".deb") {
			return nil, nil, nil, fmt.Errorf("%w: package filename %q is not canonical for %q", ErrPublicationCandidate, base, input.Package.Name)
		}
		shard := "0"
		if input.Package.Name[0] >= 'a' && input.Package.Name[0] <= 'z' {
			shard = input.Package.Name[:1]
		}
		poolPath := path.Join("pool", "main", shard, input.Package.Name, base)
		if _, duplicate := seen[poolPath]; duplicate {
			return nil, nil, nil, fmt.Errorf("%w: duplicate pool path %q", ErrPublicationCandidate, poolPath)
		}
		seen[poolPath] = struct{}{}

		if err := beforeProductionLocalStep(ctx, hooks, "stage:package:"+poolPath); err != nil {
			return nil, nil, nil, err
		}
		localPath := filepath.Join(stageDir, "objects", filepath.FromSlash(poolPath))
		digest, err := copyAndHashProductionPackage(input.SourcePath, localPath)
		if err != nil {
			return nil, nil, nil, err
		}
		if err := verifyProductionPackageDigest(input.Package, digest); err != nil {
			return nil, nil, nil, err
		}

		pkg := input.Package
		pkg.Filename = poolPath
		packages = append(packages, pkg)
		object := ProductionManifestObject{Path: poolPath, SHA256: digest.SHA256, Size: digest.Size}
		manifest = append(manifest, object)
		objects = append(objects, stagedProductionObject{
			ProductionManifestObject: object,
			kind:                     productionPoolObject,
			localPath:                localPath,
		})
	}
	sort.Slice(packages, func(i, j int) bool {
		if packages[i].Name != packages[j].Name {
			return packages[i].Name < packages[j].Name
		}
		if packages[i].Version != packages[j].Version {
			return packages[i].Version < packages[j].Version
		}
		return packages[i].Architecture < packages[j].Architecture
	})
	sort.Slice(manifest, func(i, j int) bool { return manifest[i].Path < manifest[j].Path })
	return packages, manifest, objects, nil
}

func validateProductionPackageForStage(pkg models.Package) error {
	if !productionPackageNamePattern.MatchString(pkg.Name) {
		return fmt.Errorf("%w: invalid Debian package name %q", ErrPublicationCandidate, pkg.Name)
	}
	if !productionVersionPattern.MatchString(pkg.Version) {
		return fmt.Errorf("%w: package %q has invalid Debian version", ErrPublicationCandidate, pkg.Name)
	}
	if !isProductionArchitecture(pkg.Architecture) {
		return fmt.Errorf("%w: package %q architecture %q is not allowed", ErrPublicationCandidate, pkg.Name, pkg.Architecture)
	}
	for field, value := range map[string]string{
		"Maintainer": pkg.Maintainer,
		"Homepage":   pkg.Homepage,
	} {
		if containsProductionControl(value) {
			return fmt.Errorf("%w: package %q field %s contains a control character", ErrPublicationCandidate, pkg.Name, field)
		}
	}
	if strings.IndexFunc(pkg.Description, func(char rune) bool {
		return char < 0x20 && char != '\n' && char != '\t'
	}) >= 0 {
		return fmt.Errorf("%w: package %q description contains a control character", ErrPublicationCandidate, pkg.Name)
	}
	for _, dependency := range pkg.Dependencies {
		if containsProductionControl(dependency) {
			return fmt.Errorf("%w: package %q dependency contains a control character", ErrPublicationCandidate, pkg.Name)
		}
	}
	reserved := map[string]struct{}{
		"Package": {}, "Version": {}, "Architecture": {}, "Filename": {},
		"Size": {}, "MD5sum": {}, "SHA1": {}, "SHA256": {}, "SHA512": {},
		"Maintainer": {}, "Homepage": {}, "Description": {}, "Depends": {},
	}
	for name, value := range pkg.Metadata {
		if !productionFieldNamePattern.MatchString(name) {
			return fmt.Errorf("%w: package %q has invalid metadata field %q", ErrPublicationCandidate, pkg.Name, name)
		}
		if _, duplicate := reserved[name]; duplicate {
			return fmt.Errorf("%w: package %q repeats reserved metadata field %s", ErrPublicationCandidate, pkg.Name, name)
		}
		if containsProductionControl(fmt.Sprint(value)) {
			return fmt.Errorf("%w: package %q metadata field %s contains a control character", ErrPublicationCandidate, pkg.Name, name)
		}
	}
	return nil
}

func stageProductionIndexes(
	ctx context.Context,
	stageDir string,
	codename string,
	packages []models.Package,
	hooks *productionLocalHooks,
) ([]stagedProductionObject, []ReleaseFileInfo, error) {
	var objects []stagedProductionObject
	var releaseInputs []ReleaseFileInfo
	for _, architecture := range ProductionArchitectures() {
		var architecturePackages []models.Package
		for _, pkg := range packages {
			if pkg.Architecture == architecture {
				architecturePackages = append(architecturePackages, pkg)
			}
		}
		if err := beforeProductionLocalStep(ctx, hooks, "stage:generate-index:"+architecture); err != nil {
			return nil, nil, err
		}
		plain, err := GeneratePackagesFile(architecturePackages)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: generate %s Packages: %v", ErrPublicationCandidate, architecture, err)
		}
		compressed, err := gzipProductionIndex(plain)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: compress %s Packages: %v", ErrPublicationCandidate, architecture, err)
		}
		for _, index := range []struct {
			name string
			data []byte
		}{
			{name: "Packages", data: plain},
			{name: "Packages.gz", data: compressed},
		} {
			relative := path.Join("main", "binary-"+architecture, index.name)
			key := path.Join("dists", codename, relative)
			canonical, err := stageProductionBytes(
				ctx,
				stageDir,
				key,
				index.data,
				productionIndexObject,
				hooks,
			)
			if err != nil {
				return nil, nil, err
			}
			objects = append(objects, canonical)

			checksum := checksumsForBytes(index.data)
			releaseInputs = append(releaseInputs, ReleaseFileInfo{Path: relative, Checksum: checksum})
			byHashKey := path.Join(
				"dists",
				codename,
				"main",
				"binary-"+architecture,
				"by-hash",
				"SHA256",
				checksum.SHA256,
			)
			byHash, err := stageProductionBytes(
				ctx,
				stageDir,
				byHashKey,
				index.data,
				productionByHashObject,
				hooks,
			)
			if err != nil {
				return nil, nil, err
			}
			objects = append(objects, byHash)
		}
	}
	sort.Slice(releaseInputs, func(i, j int) bool { return releaseInputs[i].Path < releaseInputs[j].Path })
	return objects, releaseInputs, nil
}

func stageProductionBytes(
	ctx context.Context,
	stageDir string,
	key string,
	data []byte,
	kind productionObjectKind,
	hooks *productionLocalHooks,
) (stagedProductionObject, error) {
	localPath := filepath.Join(stageDir, "objects", filepath.FromSlash(key))
	if err := beforeProductionLocalStep(ctx, hooks, "stage:write:"+key); err != nil {
		return stagedProductionObject{}, err
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0o700); err != nil {
		return stagedProductionObject{}, fmt.Errorf("%w: create staging parent for %s: %v", ErrPublicationCandidate, key, err)
	}
	if err := os.WriteFile(localPath, data, 0o600); err != nil {
		return stagedProductionObject{}, fmt.Errorf("%w: stage %s: %v", ErrPublicationCandidate, key, err)
	}
	return stagedProductionObject{
		ProductionManifestObject: ProductionManifestObject{
			Path:   key,
			SHA256: productionSHA256Hex(data),
			Size:   int64(len(data)),
		},
		kind:      kind,
		localPath: localPath,
	}, nil
}

func copyAndHashProductionPackage(sourcePath, destinationPath string) (*utils.Checksum, error) {
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("%w: inspect package %q: %v", ErrPublicationCandidate, sourcePath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: package %q is not a regular non-symlink file", ErrPublicationCandidate, sourcePath)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("%w: open package %q: %v", ErrPublicationCandidate, sourcePath, err)
	}
	defer func() { _ = source.Close() }()
	opened, err := source.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("%w: package %q changed while opening", ErrPublicationCandidate, sourcePath)
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o700); err != nil {
		return nil, fmt.Errorf("%w: create package staging parent: %v", ErrPublicationCandidate, err)
	}
	destination, err := os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("%w: create staged package: %v", ErrPublicationCandidate, err)
	}

	md5Hash := md5.New()
	sha1Hash := sha1.New()
	sha256Hash := sha256.New()
	sha512Hash := sha512.New()
	size, copyErr := io.Copy(io.MultiWriter(destination, md5Hash, sha1Hash, sha256Hash, sha512Hash), source)
	closeErr := destination.Close()
	if copyErr != nil {
		return nil, fmt.Errorf("%w: stage package: %v", ErrPublicationCandidate, copyErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("%w: close staged package: %v", ErrPublicationCandidate, closeErr)
	}
	finalInfo, err := os.Lstat(sourcePath)
	if err != nil || !os.SameFile(opened, finalInfo) || finalInfo.Size() != size {
		return nil, fmt.Errorf("%w: package %q changed while staging", ErrPublicationCandidate, sourcePath)
	}
	return &utils.Checksum{
		MD5:    hex.EncodeToString(md5Hash.Sum(nil)),
		SHA1:   hex.EncodeToString(sha1Hash.Sum(nil)),
		SHA256: hex.EncodeToString(sha256Hash.Sum(nil)),
		SHA512: hex.EncodeToString(sha512Hash.Sum(nil)),
		Size:   size,
	}, nil
}

func verifyProductionPackageDigest(pkg models.Package, observed *utils.Checksum) error {
	expected := map[string]string{
		"MD5":    pkg.MD5Sum,
		"SHA1":   pkg.SHA1Sum,
		"SHA256": pkg.SHA256Sum,
		"SHA512": pkg.SHA512Sum,
	}
	actual := map[string]string{
		"MD5":    observed.MD5,
		"SHA1":   observed.SHA1,
		"SHA256": observed.SHA256,
		"SHA512": observed.SHA512,
	}
	if pkg.Size != observed.Size {
		return fmt.Errorf("%w: package %q size is %d, expected %d", ErrPublicationCandidate, pkg.Name, observed.Size, pkg.Size)
	}
	for name, expectedDigest := range expected {
		if expectedDigest == "" || actual[name] != expectedDigest {
			return fmt.Errorf(
				"%w: package %q %s is %s, expected %s",
				ErrPublicationCandidate,
				pkg.Name,
				name,
				actual[name],
				expectedDigest,
			)
		}
	}
	if !isProductionArchitecture(pkg.Architecture) {
		return fmt.Errorf("%w: package %q architecture %q is not allowed", ErrPublicationCandidate, pkg.Name, pkg.Architecture)
	}
	return nil
}

func gzipProductionIndex(data []byte) ([]byte, error) {
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	writer.ModTime = time.Unix(0, 0)
	writer.OS = 255
	if _, err := writer.Write(data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func generateProductionRelease(codename string, releaseTime time.Time, files []ReleaseFileInfo) []byte {
	var output bytes.Buffer
	fmt.Fprintf(&output, "Origin: %s\n", productionReleaseOrigin)
	fmt.Fprintf(&output, "Label: %s\n", productionReleaseLabel)
	fmt.Fprintf(&output, "Suite: %s\n", codename)
	fmt.Fprintf(&output, "Codename: %s\n", codename)
	fmt.Fprintf(&output, "Architectures: %s\n", strings.Join(ProductionArchitectures(), " "))
	output.WriteString("Components: main\n")
	fmt.Fprintf(&output, "Date: %s\n", releaseTime.UTC().Format(time.RFC1123Z))
	output.WriteString("Acquire-By-Hash: yes\n")
	for _, section := range []struct {
		name   string
		digest func(*utils.Checksum) string
	}{
		{name: "MD5Sum", digest: func(value *utils.Checksum) string { return value.MD5 }},
		{name: "SHA1", digest: func(value *utils.Checksum) string { return value.SHA1 }},
		{name: "SHA256", digest: func(value *utils.Checksum) string { return value.SHA256 }},
		{name: "SHA512", digest: func(value *utils.Checksum) string { return value.SHA512 }},
	} {
		fmt.Fprintf(&output, "%s:\n", section.name)
		for _, file := range files {
			fmt.Fprintf(&output, " %s %d %s\n", section.digest(file.Checksum), file.Checksum.Size, file.Path)
		}
	}
	return output.Bytes()
}

func checksumsForBytes(data []byte) *utils.Checksum {
	md5Digest := md5.Sum(data)
	sha1Digest := sha1.Sum(data)
	sha256Digest := sha256.Sum256(data)
	sha512Digest := sha512.Sum512(data)
	return &utils.Checksum{
		MD5:    hex.EncodeToString(md5Digest[:]),
		SHA1:   hex.EncodeToString(sha1Digest[:]),
		SHA256: hex.EncodeToString(sha256Digest[:]),
		SHA512: hex.EncodeToString(sha512Digest[:]),
		Size:   int64(len(data)),
	}
}

func productionSHA256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func verifyStagedProductionSignatures(publicKey, release, inRelease, releaseSignature []byte) error {
	keyring, err := openpgp.ReadArmoredKeyRing(bytes.NewReader(publicKey))
	if err != nil {
		return fmt.Errorf("cannot parse exported signing public key: %w", err)
	}
	if len(keyring) != 1 {
		return fmt.Errorf("exported signing public key contains %d keys, want 1", len(keyring))
	}
	if _, err := verifyProductionReleaseSignatures(keyring, release, inRelease, releaseSignature); err != nil {
		return fmt.Errorf("staged signatures failed verification: %w", err)
	}
	return nil
}

func validateProductionWritePlan(transaction *ProductionTransaction, expectedPriorReleaseSHA256 string) error {
	var inReleaseCount int
	mutable := make(map[string]struct{})
	for index, object := range transaction.objects {
		if object.kind == productionInReleaseObject {
			inReleaseCount++
			if index != len(transaction.objects)-1 {
				return fmt.Errorf("%w: InRelease is not the final write", ErrPublicationCandidate)
			}
		}
		if object.kind >= productionIndexObject {
			mutable[object.Path] = struct{}{}
		}
	}
	if inReleaseCount != 1 {
		return fmt.Errorf("%w: write plan contains %d InRelease objects", ErrPublicationCandidate, inReleaseCount)
	}
	if transaction.Operation == "initialize" {
		return nil
	}
	if len(transaction.ExpectedPrior) != len(mutable) {
		return fmt.Errorf(
			"%w: reconcile expected-prior map has %d objects, want exactly %d",
			ErrPublicationCandidate,
			len(transaction.ExpectedPrior),
			len(mutable),
		)
	}
	for key := range mutable {
		digest, ok := transaction.ExpectedPrior[key]
		if !ok || !sha256Pattern.MatchString(digest.SHA256) || digest.Size < 0 {
			return fmt.Errorf("%w: reconcile lacks a valid expected prior for %s", ErrPublicationCandidate, key)
		}
	}
	releasePath := path.Join("dists", transaction.Codename, "Release")
	if transaction.ExpectedPrior[releasePath].SHA256 != expectedPriorReleaseSHA256 {
		return fmt.Errorf("%w: expected prior Release digest disagrees with prior object map", ErrPublicationCandidate)
	}
	return nil
}

func validateStagedProductionObjects(transaction *ProductionTransaction) error {
	for _, object := range transaction.objects {
		file, err := os.Open(object.localPath)
		if err != nil {
			return fmt.Errorf("%w: open staged %s: %v", ErrPublicationCandidate, object.Path, err)
		}
		observed, hashErr := hashReader(context.Background(), file)
		closeErr := file.Close()
		if hashErr != nil {
			return fmt.Errorf("%w: hash staged %s: %v", ErrPublicationCandidate, object.Path, hashErr)
		}
		if closeErr != nil {
			return fmt.Errorf("%w: close staged %s: %v", ErrPublicationCandidate, object.Path, closeErr)
		}
		if observed.SHA256 != object.SHA256 || observed.Size != object.Size {
			return fmt.Errorf("%w: staged object %s changed after planning", ErrPublicationCandidate, object.Path)
		}
	}
	return nil
}

func validateProductionTransactionIdentity(transaction *ProductionTransaction) error {
	if transaction.Codename == "" ||
		transaction.RequestManifest.Target != path.Join("dists", transaction.Codename) ||
		transaction.RequestManifest.Operation != transaction.Operation ||
		transaction.RequestManifest.SigningKeyFingerprint != transaction.SigningKeyFingerprint ||
		transaction.RequestManifest.SchemaVersion != productionManifestVersion {
		return fmt.Errorf("%w: staged transaction identity changed after planning", ErrPublicationCandidate)
	}
	if len(transaction.RequestManifest.Objects) != len(transaction.objects) {
		return fmt.Errorf("%w: staged transaction object count changed after planning", ErrPublicationCandidate)
	}
	for index, object := range transaction.objects {
		if transaction.RequestManifest.Objects[index] != object.ProductionManifestObject {
			return fmt.Errorf("%w: staged transaction object plan changed after planning", ErrPublicationCandidate)
		}
	}
	requestData, err := os.ReadFile(filepath.Join(transaction.StageDir, "request.json"))
	if err != nil {
		return fmt.Errorf("%w: read staged request manifest: %v", ErrPublicationCandidate, err)
	}
	canonical, err := json.Marshal(transaction.RequestManifest)
	if err != nil {
		return fmt.Errorf("%w: encode staged request manifest: %v", ErrPublicationCandidate, err)
	}
	canonical = append(canonical, '\n')
	if !bytes.Equal(requestData, canonical) ||
		productionSHA256Hex(requestData) != transaction.RequestSHA256 {
		return fmt.Errorf("%w: staged request manifest changed after planning", ErrPublicationCandidate)
	}
	return validateProductionWritePlan(
		transaction,
		transaction.RequestManifest.ExpectedPriorReleaseSHA256,
	)
}

func verifyProductionPrior(
	ctx context.Context,
	store ProductionPublicationStore,
	transaction *ProductionTransaction,
) error {
	prefix := path.Join("dists", transaction.Codename) + "/"
	exists, err := store.PrefixExists(ctx, prefix)
	if err != nil {
		return fmt.Errorf("%w: inspect target prefix: %v", ErrPublicationState, err)
	}
	if transaction.Operation == "initialize" {
		if exists {
			return fmt.Errorf("%w: initialize target %s already exists", ErrPublicationState, prefix)
		}
		return nil
	}
	if !exists {
		return fmt.Errorf("%w: reconcile target %s is absent", ErrPublicationState, prefix)
	}
	keys := make([]string, 0, len(transaction.ExpectedPrior))
	for key := range transaction.ExpectedPrior {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		expected := transaction.ExpectedPrior[key]
		if err := verifyPublishedProductionObject(ctx, store, ProductionManifestObject{
			Path: key, SHA256: expected.SHA256, Size: expected.Size,
		}); err != nil {
			return fmt.Errorf("%w: prior %s: %v", ErrPublicationState, key, err)
		}
	}
	return nil
}

func createImmutableProductionObject(
	ctx context.Context,
	store ProductionPublicationStore,
	object stagedProductionObject,
	body io.Reader,
) error {
	err := store.Create(ctx, object.Path, body, object.Size)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrPoolObjectExists) {
		return fmt.Errorf("%w: create immutable %s: %v", ErrPublicationWrite, object.Path, err)
	}
	if err := verifyPublishedProductionObject(ctx, store, object.ProductionManifestObject); err != nil {
		return fmt.Errorf("%w: existing immutable %s differs: %v", ErrPublicationWrite, object.Path, err)
	}
	return nil
}

func writeMutableProductionObject(
	ctx context.Context,
	store ProductionPublicationStore,
	transaction *ProductionTransaction,
	object stagedProductionObject,
	body io.Reader,
) error {
	var err error
	if transaction.Operation == "initialize" {
		err = store.Create(ctx, object.Path, body, object.Size)
	} else {
		err = store.Replace(
			ctx,
			object.Path,
			body,
			object.Size,
			transaction.ExpectedPrior[object.Path].SHA256,
		)
	}
	if err != nil {
		return fmt.Errorf("%w: write %s: %v", ErrPublicationWrite, object.Path, err)
	}
	return nil
}

func verifyPublishedProductionObject(
	ctx context.Context,
	store ProductionPublicationStore,
	expected ProductionManifestObject,
) error {
	object, err := store.Open(ctx, expected.Path)
	if err != nil {
		return fmt.Errorf("%w: open %s: %v", ErrPublicationReadBack, expected.Path, err)
	}
	if object == nil || object.Body == nil {
		return fmt.Errorf("%w: store returned no body for %s", ErrPublicationReadBack, expected.Path)
	}
	observed, err := hashRemote(ctx, object.Body)
	if err != nil {
		return fmt.Errorf("%w: hash %s: %v", ErrPublicationReadBack, expected.Path, err)
	}
	if observed.SHA256 != expected.SHA256 || observed.Size != expected.Size {
		return fmt.Errorf(
			"%w: %s is sha256 %s size %d, expected sha256 %s size %d",
			ErrPublicationReadBack,
			expected.Path,
			observed.SHA256,
			observed.Size,
			expected.SHA256,
			expected.Size,
		)
	}
	return nil
}

func copyProductionDigests(input map[string]ProductionObjectDigest) map[string]ProductionObjectDigest {
	output := make(map[string]ProductionObjectDigest, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func isUnsafeManifestRune(value rune) bool {
	return value < 0x21 || value > 0x7e
}

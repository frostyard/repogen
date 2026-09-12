package deb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

// ErrLocalGeneration identifies a failed atomic local production generation.
var ErrLocalGeneration = errors.New("local production generation failed")

type productionLocalHooks struct {
	before func(string) error
}

type localTreeEntry struct {
	Mode   fs.FileMode
	Size   int64
	SHA256 string
	IsDir  bool
}

// GenerateLocalProductionRepository stages and atomically commits one signed
// production repository generation. Generic generation remains unchanged.
func GenerateLocalProductionRepository(
	ctx context.Context,
	outputDir string,
	stageDir string,
	request ProductionStageRequest,
) error {
	return generateLocalProductionRepository(ctx, outputDir, stageDir, request, nil)
}

func generateLocalProductionRepository(
	ctx context.Context,
	outputDir string,
	stageDir string,
	request ProductionStageRequest,
	hooks *productionLocalHooks,
) error {
	transaction, err := stageProductionTransaction(ctx, stageDir, request, hooks)
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stageDir) }()

	return commitLocalProductionTransaction(ctx, outputDir, transaction, hooks)
}

// CommitLocalProductionTransaction commits a previously staged production
// transaction through one atomic directory switch.
func CommitLocalProductionTransaction(
	ctx context.Context,
	outputDir string,
	transaction *ProductionTransaction,
) error {
	return commitLocalProductionTransaction(ctx, outputDir, transaction, nil)
}

func commitLocalProductionTransaction(
	ctx context.Context,
	outputDir string,
	transaction *ProductionTransaction,
	hooks *productionLocalHooks,
) error {
	if transaction == nil {
		return fmt.Errorf("%w: transaction is required", ErrLocalGeneration)
	}
	if err := validateLocalGenerationPaths(outputDir, transaction.StageDir); err != nil {
		return err
	}
	if err := beforeProductionLocalStep(ctx, hooks, "commit:validate-stage"); err != nil {
		return err
	}
	if err := validateProductionTransactionIdentity(transaction); err != nil {
		return err
	}
	if err := validateStagedProductionObjects(transaction); err != nil {
		return err
	}

	outputExists, err := validateLocalPrior(ctx, outputDir, transaction, hooks)
	if err != nil {
		return err
	}

	parent := filepath.Dir(outputDir)
	if err := beforeProductionLocalStep(ctx, hooks, "commit:create-generation"); err != nil {
		return err
	}
	generationDir, err := os.MkdirTemp(parent, "."+filepath.Base(outputDir)+".repogen-generation-")
	if err != nil {
		return fmt.Errorf("%w: create sibling generation: %v", ErrLocalGeneration, err)
	}
	if err := os.Chmod(generationDir, 0o755); err != nil {
		_ = os.RemoveAll(generationDir)
		return fmt.Errorf("%w: set generation permissions: %v", ErrLocalGeneration, err)
	}
	committed := false
	defer func() {
		if !committed || outputExists {
			_ = os.RemoveAll(generationDir)
		}
	}()

	var priorSnapshot map[string]localTreeEntry
	if outputExists {
		priorSnapshot, err = snapshotAndCopyLocalTree(
			ctx,
			outputDir,
			generationDir,
			filepath.Join("dists", transaction.Codename),
			hooks,
		)
		if err != nil {
			return err
		}
	}
	if err := installLocalGenerationObjects(ctx, generationDir, transaction, hooks); err != nil {
		return err
	}
	if err := verifyLocalGenerationObjects(ctx, generationDir, transaction, hooks); err != nil {
		return err
	}
	if outputExists {
		currentSnapshot, err := snapshotLocalTree(ctx, outputDir, hooks, "commit:verify-prior")
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(currentSnapshot, priorSnapshot) {
			return fmt.Errorf("%w: prior output changed while staging", ErrLocalGeneration)
		}
	}
	if err := syncLocalGeneration(ctx, generationDir, hooks); err != nil {
		return err
	}
	if err := beforeProductionLocalStep(ctx, hooks, "commit:atomic-switch"); err != nil {
		return err
	}
	if err := atomicReplaceDirectory(generationDir, outputDir, outputExists); err != nil {
		return fmt.Errorf("%w: atomic directory switch: %v", ErrLocalGeneration, err)
	}
	committed = true
	return nil
}

func beforeProductionLocalStep(
	ctx context.Context,
	hooks *productionLocalHooks,
	step string,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if hooks == nil || hooks.before == nil {
		return nil
	}
	if err := hooks.before(step); err != nil {
		return fmt.Errorf("%w before %s: %v", ErrLocalGeneration, step, err)
	}
	return nil
}

func validateLocalGenerationPaths(outputDir, stageDir string) error {
	for name, value := range map[string]string{"output": outputDir, "stage": stageDir} {
		if value == "" || filepath.Clean(value) != value {
			return fmt.Errorf("%w: %s path must be explicit and clean", ErrLocalGeneration, name)
		}
	}
	outputParent, err := filepath.Abs(filepath.Dir(outputDir))
	if err != nil {
		return fmt.Errorf("%w: resolve output parent: %v", ErrLocalGeneration, err)
	}
	outputParent, err = filepath.EvalSymlinks(outputParent)
	if err != nil {
		return fmt.Errorf("%w: resolve output parent links: %v", ErrLocalGeneration, err)
	}
	outputAbsolute := filepath.Join(outputParent, filepath.Base(outputDir))
	if outputAbsolute == outputParent {
		return fmt.Errorf("%w: output path must name a child directory", ErrLocalGeneration)
	}
	stageAbsolute, err := filepath.EvalSymlinks(stageDir)
	if err != nil {
		return fmt.Errorf("%w: resolve staging links: %v", ErrLocalGeneration, err)
	}
	if localPathContains(outputAbsolute, stageAbsolute) ||
		localPathContains(stageAbsolute, outputAbsolute) {
		return fmt.Errorf("%w: output and staging paths must not overlap", ErrLocalGeneration)
	}
	parentInfo, err := os.Lstat(filepath.Dir(outputDir))
	if err != nil {
		return fmt.Errorf("%w: inspect output parent: %v", ErrLocalGeneration, err)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return fmt.Errorf("%w: output parent is not a regular directory", ErrLocalGeneration)
	}
	return nil
}

func localPathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." ||
		(relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func validateLocalPrior(
	ctx context.Context,
	outputDir string,
	transaction *ProductionTransaction,
	hooks *productionLocalHooks,
) (bool, error) {
	if err := beforeProductionLocalStep(ctx, hooks, "commit:inspect-output"); err != nil {
		return false, err
	}
	info, err := os.Lstat(outputDir)
	if os.IsNotExist(err) {
		if transaction.Operation != "initialize" {
			return false, fmt.Errorf("%w: reconcile output is absent", ErrLocalGeneration)
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("%w: inspect output: %v", ErrLocalGeneration, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false, fmt.Errorf("%w: output is not a regular directory", ErrLocalGeneration)
	}

	targetDir := filepath.Join(outputDir, "dists", transaction.Codename)
	targetInfo, targetErr := os.Lstat(targetDir)
	if transaction.Operation == "initialize" {
		if targetErr == nil {
			return false, fmt.Errorf("%w: initialize target already exists", ErrLocalGeneration)
		}
		if !os.IsNotExist(targetErr) {
			return false, fmt.Errorf("%w: inspect initialize target: %v", ErrLocalGeneration, targetErr)
		}
		return true, nil
	}
	if targetErr != nil {
		return false, fmt.Errorf("%w: inspect reconcile target: %v", ErrLocalGeneration, targetErr)
	}
	if targetInfo.Mode()&os.ModeSymlink != 0 || !targetInfo.IsDir() {
		return false, fmt.Errorf("%w: reconcile target is not a regular directory", ErrLocalGeneration)
	}

	keys := make([]string, 0, len(transaction.ExpectedPrior))
	for key := range transaction.ExpectedPrior {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := beforeProductionLocalStep(ctx, hooks, "commit:verify-expected-prior:"+key); err != nil {
			return false, err
		}
		expected := transaction.ExpectedPrior[key]
		observed, err := hashLocalRegularFile(filepath.Join(outputDir, filepath.FromSlash(key)))
		if err != nil {
			return false, fmt.Errorf("%w: verify prior %s: %v", ErrLocalGeneration, key, err)
		}
		if observed.SHA256 != expected.SHA256 || observed.Size != expected.Size {
			return false, fmt.Errorf("%w: prior %s differs from verified state", ErrLocalGeneration, key)
		}
	}
	return true, nil
}

func snapshotAndCopyLocalTree(
	ctx context.Context,
	sourceRoot string,
	destinationRoot string,
	excludedRelative string,
	hooks *productionLocalHooks,
) (map[string]localTreeEntry, error) {
	snapshot := make(map[string]localTreeEntry)
	directoryModes := make(map[string]fs.FileMode)
	err := filepath.WalkDir(sourceRoot, func(sourcePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(sourceRoot, sourcePath)
		if err != nil {
			return err
		}
		if err := beforeProductionLocalStep(ctx, hooks, "commit:copy-prior:"+relative); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: prior output contains symlink %s", ErrLocalGeneration, relative)
		}
		excluded := relative == excludedRelative ||
			strings.HasPrefix(relative, excludedRelative+string(filepath.Separator))
		if entry.IsDir() {
			snapshot[relative] = localTreeEntry{Mode: info.Mode(), IsDir: true}
			if relative == "." {
				directoryModes[destinationRoot] = info.Mode()
			} else if !excluded {
				destination := filepath.Join(destinationRoot, relative)
				if err := os.Mkdir(destination, 0o700); err != nil {
					return err
				}
				directoryModes[destination] = info.Mode()
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: prior output contains non-regular file %s", ErrLocalGeneration, relative)
		}
		destination := ""
		if !excluded {
			destination = filepath.Join(destinationRoot, relative)
		}
		copied, err := copyAndHashLocalFile(sourcePath, destination, info.Mode())
		if err != nil {
			return err
		}
		snapshot[relative] = localTreeEntry{
			Mode:   info.Mode(),
			Size:   copied.Size,
			SHA256: copied.SHA256,
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: copy prior output: %v", ErrLocalGeneration, err)
	}
	directories := make([]string, 0, len(directoryModes))
	for directory := range directoryModes {
		directories = append(directories, directory)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(directories)))
	for _, directory := range directories {
		if err := os.Chmod(directory, directoryModes[directory]); err != nil {
			return nil, fmt.Errorf("%w: preserve prior directory mode: %v", ErrLocalGeneration, err)
		}
	}
	return snapshot, nil
}

func snapshotLocalTree(
	ctx context.Context,
	root string,
	hooks *productionLocalHooks,
	stepPrefix string,
) (map[string]localTreeEntry, error) {
	snapshot := make(map[string]localTreeEntry)
	err := filepath.WalkDir(root, func(currentPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, currentPath)
		if err != nil {
			return err
		}
		if err := beforeProductionLocalStep(ctx, hooks, stepPrefix+":"+relative); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: output contains symlink %s", ErrLocalGeneration, relative)
		}
		if entry.IsDir() {
			snapshot[relative] = localTreeEntry{Mode: info.Mode(), IsDir: true}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: output contains non-regular file %s", ErrLocalGeneration, relative)
		}
		digest, err := hashLocalRegularFile(currentPath)
		if err != nil {
			return err
		}
		snapshot[relative] = localTreeEntry{
			Mode:   info.Mode(),
			Size:   digest.Size,
			SHA256: digest.SHA256,
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: snapshot output: %v", ErrLocalGeneration, err)
	}
	return snapshot, nil
}

func installLocalGenerationObjects(
	ctx context.Context,
	generationDir string,
	transaction *ProductionTransaction,
	hooks *productionLocalHooks,
) error {
	for _, object := range transaction.objects {
		if err := beforeProductionLocalStep(ctx, hooks, "commit:install:"+object.Path); err != nil {
			return err
		}
		destination := filepath.Join(generationDir, filepath.FromSlash(object.Path))
		if info, err := os.Lstat(destination); err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return fmt.Errorf("%w: destination %s is not a regular file", ErrLocalGeneration, object.Path)
			}
			observed, err := hashLocalRegularFile(destination)
			if err != nil {
				return err
			}
			if observed.SHA256 != object.SHA256 || observed.Size != object.Size {
				return fmt.Errorf("%w: immutable object collision at %s", ErrLocalGeneration, object.Path)
			}
			continue
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("%w: inspect destination %s: %v", ErrLocalGeneration, object.Path, err)
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return fmt.Errorf("%w: create destination parent for %s: %v", ErrLocalGeneration, object.Path, err)
		}
		observed, err := copyAndHashLocalFile(object.localPath, destination, 0o644)
		if err != nil {
			return fmt.Errorf("%w: install %s: %v", ErrLocalGeneration, object.Path, err)
		}
		if observed.SHA256 != object.SHA256 || observed.Size != object.Size {
			return fmt.Errorf("%w: installed object %s differs from staging", ErrLocalGeneration, object.Path)
		}
	}
	return nil
}

func verifyLocalGenerationObjects(
	ctx context.Context,
	generationDir string,
	transaction *ProductionTransaction,
	hooks *productionLocalHooks,
) error {
	for _, object := range transaction.objects {
		if err := beforeProductionLocalStep(ctx, hooks, "commit:verify-generation:"+object.Path); err != nil {
			return err
		}
		observed, err := hashLocalRegularFile(
			filepath.Join(generationDir, filepath.FromSlash(object.Path)),
		)
		if err != nil {
			return fmt.Errorf("%w: verify generated %s: %v", ErrLocalGeneration, object.Path, err)
		}
		if observed.SHA256 != object.SHA256 || observed.Size != object.Size {
			return fmt.Errorf("%w: generated object %s differs from transaction", ErrLocalGeneration, object.Path)
		}
	}
	return nil
}

func copyAndHashLocalFile(sourcePath, destinationPath string, mode fs.FileMode) (ProductionObjectDigest, error) {
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return ProductionObjectDigest{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return ProductionObjectDigest{}, fmt.Errorf("%s is not a regular non-symlink file", sourcePath)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return ProductionObjectDigest{}, err
	}
	defer func() { _ = source.Close() }()

	hash := sha256.New()
	writer := io.Writer(hash)
	var destination *os.File
	if destinationPath != "" {
		destination, err = os.OpenFile(destinationPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return ProductionObjectDigest{}, err
		}
		writer = io.MultiWriter(destination, hash)
	}
	size, copyErr := io.Copy(writer, source)
	if destination != nil {
		if syncErr := destination.Sync(); copyErr == nil {
			copyErr = syncErr
		}
		if closeErr := destination.Close(); copyErr == nil {
			copyErr = closeErr
		}
	}
	if copyErr != nil {
		return ProductionObjectDigest{}, copyErr
	}
	if destinationPath != "" {
		if err := os.Chmod(destinationPath, mode); err != nil {
			return ProductionObjectDigest{}, err
		}
	}
	after, err := source.Stat()
	if err != nil || !os.SameFile(info, after) || after.Size() != size {
		return ProductionObjectDigest{}, fmt.Errorf("%s changed while reading", sourcePath)
	}
	return ProductionObjectDigest{
		SHA256: hex.EncodeToString(hash.Sum(nil)),
		Size:   size,
	}, nil
}

func hashLocalRegularFile(path string) (ProductionObjectDigest, error) {
	return copyAndHashLocalFile(path, "", 0)
}

func syncLocalGeneration(
	ctx context.Context,
	generationDir string,
	hooks *productionLocalHooks,
) error {
	var directories []string
	err := filepath.WalkDir(generationDir, func(currentPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			directories = append(directories, currentPath)
			return nil
		}
		if err := beforeProductionLocalStep(ctx, hooks, "commit:sync-file:"+currentPath); err != nil {
			return err
		}
		file, err := os.Open(currentPath)
		if err != nil {
			return err
		}
		syncErr := file.Sync()
		closeErr := file.Close()
		if syncErr != nil {
			return syncErr
		}
		return closeErr
	})
	if err != nil {
		return fmt.Errorf("%w: sync generation files: %v", ErrLocalGeneration, err)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(directories)))
	for _, directory := range directories {
		if err := beforeProductionLocalStep(ctx, hooks, "commit:sync-directory:"+directory); err != nil {
			return err
		}
		handle, err := os.Open(directory)
		if err != nil {
			return fmt.Errorf("%w: open generation directory: %v", ErrLocalGeneration, err)
		}
		syncErr := handle.Sync()
		closeErr := handle.Close()
		if syncErr != nil {
			return fmt.Errorf("%w: sync generation directory: %v", ErrLocalGeneration, syncErr)
		}
		if closeErr != nil {
			return fmt.Errorf("%w: close generation directory: %v", ErrLocalGeneration, closeErr)
		}
	}
	return nil
}

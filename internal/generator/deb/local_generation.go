package deb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

const localRecoveryVersion = 1

// ErrLocalGeneration identifies a failed atomic local production generation.
var ErrLocalGeneration = errors.New("local production generation failed")

type productionLocalHooks struct {
	before         func(string) error
	finishRecovery func(string, localRecoveryJournal) error
}

type localTreeEntry struct {
	Mode   fs.FileMode
	Size   int64
	SHA256 string
	IsDir  bool
}

type localRecoveryJournal struct {
	SchemaVersion    int    `json:"schema_version"`
	Output           string `json:"output"`
	Generation       string `json:"generation"`
	OutputExisted    bool   `json:"output_existed"`
	PriorSHA256      string `json:"prior_sha256,omitempty"`
	GenerationSHA256 string `json:"generation_sha256"`
}

type localTreeDigestEntry struct {
	Path   string `json:"path"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	IsDir  bool   `json:"is_dir"`
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
	if err := recoverLocalProductionRepository(ctx, outputDir); err != nil {
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
	switched := false
	preserveForRecovery := false
	defer func() {
		if !switched && !preserveForRecovery {
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
	generationSHA256, err := digestLocalTree(ctx, generationDir)
	if err != nil {
		return err
	}
	priorSHA256 := ""
	if outputExists {
		priorSHA256, err = digestLocalTreeSnapshot(priorSnapshot)
		if err != nil {
			return err
		}
	}
	journal := localRecoveryJournal{
		SchemaVersion:    localRecoveryVersion,
		Output:           filepath.Base(outputDir),
		Generation:       filepath.Base(generationDir),
		OutputExisted:    outputExists,
		PriorSHA256:      priorSHA256,
		GenerationSHA256: generationSHA256,
	}
	if err := beforeProductionLocalStep(ctx, hooks, "commit:write-recovery"); err != nil {
		return err
	}
	if err := writeLocalRecoveryJournal(filepath.Dir(outputDir), journal); err != nil {
		if cleanupErr := removeLocalRecoveryJournal(filepath.Dir(outputDir), journal.Output); cleanupErr != nil {
			preserveForRecovery = true
			return errors.Join(err, fmt.Errorf("%w: preserve failed recovery journal: %v", ErrLocalGeneration, cleanupErr))
		}
		return err
	}
	if err := beforeProductionLocalStep(ctx, hooks, "commit:atomic-switch"); err != nil {
		if cleanupErr := removeLocalRecoveryJournal(filepath.Dir(outputDir), journal.Output); cleanupErr != nil {
			preserveForRecovery = true
			return errors.Join(err, fmt.Errorf("%w: preserve failed recovery journal: %v", ErrLocalGeneration, cleanupErr))
		}
		return err
	}
	if err := atomicReplaceDirectory(generationDir, outputDir, outputExists); err != nil {
		switchErr := fmt.Errorf("%w: atomic directory switch: %v", ErrLocalGeneration, err)
		if cleanupErr := removeLocalRecoveryJournal(filepath.Dir(outputDir), journal.Output); cleanupErr != nil {
			preserveForRecovery = true
			return errors.Join(switchErr, fmt.Errorf("%w: preserve failed recovery journal: %v", ErrLocalGeneration, cleanupErr))
		}
		return switchErr
	}
	switched = true
	if err := syncLocalDirectory(filepath.Dir(outputDir)); err != nil {
		return fmt.Errorf("%w: persist atomic directory switch: %v", ErrLocalGeneration, err)
	}
	finishRecovery := finishLocalRecovery
	if hooks != nil && hooks.finishRecovery != nil {
		finishRecovery = hooks.finishRecovery
	}
	if err := finishRecovery(filepath.Dir(outputDir), journal); err != nil {
		return fmt.Errorf("%w: finish committed recovery cleanup: %v", ErrLocalGeneration, err)
	}
	return nil
}

// RecoverLocalProductionRepository completes or cleans a journaled local
// production commit without rolling a visible generation backward.
func RecoverLocalProductionRepository(ctx context.Context, outputDir string) error {
	if outputDir == "" || filepath.Clean(outputDir) != outputDir {
		return fmt.Errorf("%w: output path must be explicit and clean", ErrLocalGeneration)
	}
	parent := filepath.Dir(outputDir)
	info, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("%w: inspect output parent: %v", ErrLocalGeneration, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%w: output parent is not a regular directory", ErrLocalGeneration)
	}
	return recoverLocalProductionRepository(ctx, outputDir)
}

func recoverLocalProductionRepository(ctx context.Context, outputDir string) error {
	parent := filepath.Dir(outputDir)
	journalPath := localRecoveryJournalPath(parent, filepath.Base(outputDir))
	data, err := os.ReadFile(journalPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: read recovery journal: %v", ErrLocalGeneration, err)
	}
	var journal localRecoveryJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		return fmt.Errorf("%w: decode recovery journal: %v", ErrLocalGeneration, err)
	}
	canonical, err := json.Marshal(journal)
	if err != nil {
		return fmt.Errorf("%w: encode recovery journal: %v", ErrLocalGeneration, err)
	}
	canonical = append(canonical, '\n')
	if !reflect.DeepEqual(data, canonical) ||
		journal.SchemaVersion != localRecoveryVersion ||
		journal.Output != filepath.Base(outputDir) ||
		filepath.Base(journal.Output) != journal.Output ||
		filepath.Base(journal.Generation) != journal.Generation ||
		!strings.HasPrefix(journal.Generation, "."+journal.Output+".repogen-generation-") ||
		!isLowerHexDigest(journal.GenerationSHA256) ||
		(journal.OutputExisted && !isLowerHexDigest(journal.PriorSHA256)) ||
		(!journal.OutputExisted && journal.PriorSHA256 != "") {
		return fmt.Errorf("%w: invalid recovery journal", ErrLocalGeneration)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	generationDir := filepath.Join(parent, journal.Generation)
	outputDigest, outputExists, err := digestLocalTreeIfExists(ctx, outputDir)
	if err != nil {
		return err
	}
	generationDigest, generationExists, err := digestLocalTreeIfExists(ctx, generationDir)
	if err != nil {
		return err
	}

	switch {
	case outputExists && outputDigest == journal.GenerationSHA256:
		if generationExists && (!journal.OutputExisted || generationDigest != journal.PriorSHA256) {
			return fmt.Errorf("%w: obsolete generation does not match journaled prior state", ErrLocalGeneration)
		}
	case generationExists && generationDigest == journal.GenerationSHA256:
		if journal.OutputExisted {
			if !outputExists || outputDigest != journal.PriorSHA256 {
				return fmt.Errorf("%w: output does not match journaled prior state", ErrLocalGeneration)
			}
		} else if outputExists {
			return fmt.Errorf("%w: initialize output appeared during recovery", ErrLocalGeneration)
		}
		if err := atomicReplaceDirectory(generationDir, outputDir, journal.OutputExisted); err != nil {
			return fmt.Errorf("%w: recover atomic directory switch: %v", ErrLocalGeneration, err)
		}
		if err := syncLocalDirectory(parent); err != nil {
			return fmt.Errorf("%w: persist recovered directory switch: %v", ErrLocalGeneration, err)
		}
	default:
		return fmt.Errorf("%w: neither output nor generation matches recovery journal", ErrLocalGeneration)
	}
	if err := finishLocalRecovery(parent, journal); err != nil {
		return fmt.Errorf("%w: finish recovery: %v", ErrLocalGeneration, err)
	}
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
		if err := createLocalGenerationDirectories(
			generationDir,
			filepath.Dir(destination),
			0o755,
		); err != nil {
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

func createLocalGenerationDirectories(root, path string, mode fs.FileMode) error {
	if !localPathContains(root, path) {
		return fmt.Errorf("destination parent escapes generation root")
	}

	var missing []string
	for current := path; current != root; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("%s is not a regular directory", current)
			}
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		missing = append(missing, current)
	}
	for index := len(missing) - 1; index >= 0; index-- {
		if err := os.Mkdir(missing[index], mode.Perm()); err != nil {
			return err
		}
		if err := os.Chmod(missing[index], mode); err != nil {
			return err
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
		relative, err := filepath.Rel(generationDir, currentPath)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			directories = append(directories, relative)
			return nil
		}
		if err := beforeProductionLocalStep(ctx, hooks, "commit:sync-file:"+relative); err != nil {
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
	for _, relative := range directories {
		if err := beforeProductionLocalStep(ctx, hooks, "commit:sync-directory:"+relative); err != nil {
			return err
		}
		directory := filepath.Join(generationDir, filepath.FromSlash(relative))
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

func digestLocalTree(ctx context.Context, root string) (string, error) {
	snapshot, err := snapshotLocalTree(ctx, root, nil, "recovery:snapshot")
	if err != nil {
		return "", err
	}
	return digestLocalTreeSnapshot(snapshot)
}

func digestLocalTreeSnapshot(snapshot map[string]localTreeEntry) (string, error) {
	paths := make([]string, 0, len(snapshot))
	for relative := range snapshot {
		paths = append(paths, relative)
	}
	sort.Strings(paths)
	entries := make([]localTreeDigestEntry, 0, len(paths))
	for _, relative := range paths {
		entry := snapshot[relative]
		entries = append(entries, localTreeDigestEntry{
			Path:   filepath.ToSlash(relative),
			Mode:   uint32(entry.Mode),
			Size:   entry.Size,
			SHA256: entry.SHA256,
			IsDir:  entry.IsDir,
		})
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("%w: encode tree digest: %v", ErrLocalGeneration, err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func digestLocalTreeIfExists(ctx context.Context, root string) (string, bool, error) {
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("%w: inspect recovery tree: %v", ErrLocalGeneration, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", false, fmt.Errorf("%w: recovery tree is not a regular directory", ErrLocalGeneration)
	}
	digest, err := digestLocalTree(ctx, root)
	return digest, true, err
}

func isLowerHexDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func localRecoveryJournalPath(parent, output string) string {
	return filepath.Join(parent, "."+output+".repogen-recovery.json")
}

func writeLocalRecoveryJournal(parent string, journal localRecoveryJournal) error {
	data, err := json.Marshal(journal)
	if err != nil {
		return fmt.Errorf("%w: encode recovery journal: %v", ErrLocalGeneration, err)
	}
	data = append(data, '\n')
	journalPath := localRecoveryJournalPath(parent, journal.Output)
	file, err := os.OpenFile(journalPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("%w: create recovery journal: %v", ErrLocalGeneration, err)
	}
	writeErr := writeAll(file, data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("%w: write recovery journal: %v", ErrLocalGeneration, writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("%w: close recovery journal: %v", ErrLocalGeneration, closeErr)
	}
	if err := syncLocalDirectory(parent); err != nil {
		return fmt.Errorf("%w: persist recovery journal: %v", ErrLocalGeneration, err)
	}
	return nil
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		written, err := writer.Write(data)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func removeLocalRecoveryJournal(parent, output string) error {
	err := os.Remove(localRecoveryJournalPath(parent, output))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return syncLocalDirectory(parent)
}

func finishLocalRecovery(parent string, journal localRecoveryJournal) error {
	generationDir := filepath.Join(parent, journal.Generation)
	if err := os.RemoveAll(generationDir); err != nil {
		return err
	}
	if err := removeLocalRecoveryJournal(parent, journal.Output); err != nil {
		return err
	}
	return syncLocalDirectory(parent)
}

func syncLocalDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

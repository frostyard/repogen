package intake

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

var (
	ErrNotFound  = errors.New("durable intake object not found")
	ErrConflict  = errors.New("durable intake object already exists")
	ErrIntegrity = errors.New("durable intake integrity failure")
	ErrState     = errors.New("durable intake state mismatch")
)

// Lock serializes one immutable intake target.
type Lock interface {
	Release() error
}

// Store is the retained, enumerable boundary used by intake and recovery.
// Create must never replace an existing key.
type Store interface {
	Create(ctx context.Context, key string, body io.Reader, size int64) error
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	List(ctx context.Context, prefix string) ([]string, error)
	Acquire(ctx context.Context, target string) (Lock, error)
}

// FileStore is a crash-durable local implementation of Store. It is useful
// for protected writer state and fixtures; it is not a remote credential or
// production object-store adapter.
type FileStore struct {
	root string
}

// OpenFileStore opens an existing regular directory as an intake store.
func OpenFileStore(root string) (*FileStore, error) {
	if root == "" || filepath.Clean(root) != root {
		return nil, fmt.Errorf("%w: store root must be explicit and clean", ErrState)
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve store root: %v", ErrState, err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve store root links: %v", ErrState, err)
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return nil, fmt.Errorf("%w: inspect store root: %v", ErrState, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("%w: store root is not a regular directory", ErrState)
	}
	return &FileStore{root: resolved}, nil
}

func (s *FileStore) Create(ctx context.Context, key string, body io.Reader, size int64) error {
	target, err := s.resolveKey(key)
	if err != nil {
		return err
	}
	if body == nil || size < 0 {
		return fmt.Errorf("%w: immutable object body and size are required", ErrState)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	parent := filepath.Dir(target)
	if err := s.mkdirAllDurable(parent); err != nil {
		return err
	}

	temporary, err := os.CreateTemp(parent, ".repogen-intake-")
	if err != nil {
		return fmt.Errorf("%w: create immutable temporary object: %v", ErrState, err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("%w: set immutable object mode: %v", ErrState, err)
	}
	written, err := io.Copy(
		temporary,
		&contextReader{ctx: ctx, reader: io.LimitReader(body, size+1)},
	)
	if err != nil {
		_ = temporary.Close()
		return fmt.Errorf("%w: write immutable object: %v", ErrState, err)
	}
	if written != size {
		_ = temporary.Close()
		return fmt.Errorf("%w: immutable object size %d does not match expected %d", ErrIntegrity, written, size)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("%w: sync immutable object: %v", ErrState, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("%w: close immutable object: %v", ErrState, err)
	}
	if err := os.Link(temporaryPath, target); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrConflict
		}
		return fmt.Errorf("%w: conditionally create immutable object: %v", ErrState, err)
	}
	if err := os.Remove(temporaryPath); err != nil {
		return fmt.Errorf("%w: remove immutable temporary object: %v", ErrState, err)
	}
	if err := syncDirectory(parent); err != nil {
		return fmt.Errorf("%w: persist immutable object: %v", ErrState, err)
	}
	return nil
}

func (s *FileStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	target, err := s.resolveKey(key)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("%w: inspect immutable object: %v", ErrState, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: immutable object is not a regular file", ErrIntegrity)
	}
	file, err := os.Open(target)
	if err != nil {
		return nil, fmt.Errorf("%w: read immutable object: %v", ErrState, err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("%w: inspect opened immutable object: %v", ErrState, err)
	}
	if !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("%w: immutable object changed while opening", ErrIntegrity)
	}
	return file, nil
}

func (s *FileStore) List(ctx context.Context, prefix string) ([]string, error) {
	cleanPrefix := strings.TrimSuffix(prefix, "/")
	root, err := s.resolveKey(cleanPrefix)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: inspect immutable prefix: %v", ErrState, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("%w: immutable prefix is not a regular directory", ErrIntegrity)
	}

	var keys []string
	err = filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if current == root {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: immutable prefix contains a symlink", ErrIntegrity)
		}
		if entry.IsDir() {
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".repogen-intake-") {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: immutable prefix contains an invalid object", ErrIntegrity)
		}
		relative, err := filepath.Rel(s.root, current)
		if err != nil {
			return err
		}
		keys = append(keys, filepath.ToSlash(relative))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: enumerate immutable prefix: %v", ErrState, err)
	}
	sort.Strings(keys)
	return keys, nil
}

func (s *FileStore) Acquire(ctx context.Context, target string) (Lock, error) {
	if !safeSegment(target) {
		return nil, fmt.Errorf("%w: unsafe lock target", ErrState)
	}
	lockDir := filepath.Join(s.root, ".locks")
	if err := s.mkdirAllDurable(lockDir); err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(target))
	lockPath := filepath.Join(lockDir, hex.EncodeToString(digest[:])+".lock")
	file, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("%w: open target lock: %v", ErrState, err)
	}
	for {
		err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return &fileLock{file: file}, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			_ = file.Close()
			return nil, fmt.Errorf("%w: acquire target lock: %v", ErrState, err)
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (s *FileStore) resolveKey(key string) (string, error) {
	if !safeKey(key) {
		return "", fmt.Errorf("%w: unsafe intake key %q", ErrState, key)
	}
	target := filepath.Join(s.root, filepath.FromSlash(key))
	if !pathWithin(s.root, target) {
		return "", fmt.Errorf("%w: intake key escapes store root", ErrState)
	}
	return target, nil
}

func (s *FileStore) mkdirAllDurable(directory string) error {
	if !pathWithin(s.root, directory) {
		return fmt.Errorf("%w: directory escapes store root", ErrState)
	}
	var missing []string
	for current := directory; current != s.root; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("%w: store path is not a regular directory", ErrIntegrity)
			}
			break
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("%w: inspect store directory: %v", ErrState, err)
		}
		missing = append(missing, current)
	}
	for index := len(missing) - 1; index >= 0; index-- {
		if err := os.Mkdir(missing[index], 0o755); err != nil {
			if !errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("%w: create store directory: %v", ErrState, err)
			}
			info, statErr := os.Lstat(missing[index])
			if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return fmt.Errorf("%w: raced store path is not a regular directory", ErrIntegrity)
			}
		}
		if err := syncDirectory(filepath.Dir(missing[index])); err != nil {
			return fmt.Errorf("%w: persist store directory: %v", ErrState, err)
		}
	}
	return nil
}

type fileLock struct {
	mu   sync.Mutex
	file *os.File
}

func (l *fileLock) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	unlockErr := unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

func safeKey(key string) bool {
	if key == "" || strings.HasPrefix(key, "/") || path.Clean(key) != key {
		return false
	}
	for _, segment := range strings.Split(key, "/") {
		if !safeSegment(segment) {
			return false
		}
	}
	return true
}

func safeSegment(segment string) bool {
	if segment == "" || segment == "." || segment == ".." {
		return false
	}
	for _, character := range segment {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil &&
		relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func syncDirectory(directory string) error {
	handle, err := os.Open(directory)
	if err != nil {
		return err
	}
	syncErr := handle.Sync()
	closeErr := handle.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

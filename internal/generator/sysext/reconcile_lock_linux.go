//go:build linux

package sysext

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

func acquireReconcileLock(ctx context.Context, outputDir string) (func() error, error) {
	absoluteOutput, err := filepath.Abs(outputDir)
	if err != nil {
		return nil, fmt.Errorf("resolve sysext output directory: %w", err)
	}
	lockPath := absoluteOutput + ".sysext.lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, fmt.Errorf("create sysext lock parent: %w", err)
	}
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open sysext reconcile lock: %w", err)
	}

	for {
		err = unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() error {
				unlockErr := unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
				closeErr := lockFile.Close()
				return errors.Join(unlockErr, closeErr)
			}, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			_ = lockFile.Close()
			return nil, fmt.Errorf("lock sysext reconciliation: %w", err)
		}
		select {
		case <-ctx.Done():
			_ = lockFile.Close()
			return nil, fmt.Errorf("wait for sysext reconciliation lock: %w", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func atomicReplaceDirectory(source, destination string, destinationExists bool) error {
	flags := uint(unix.RENAME_NOREPLACE)
	if destinationExists {
		flags = unix.RENAME_EXCHANGE
	}
	return unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, destination, flags)
}

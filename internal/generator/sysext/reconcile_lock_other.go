//go:build !linux

package sysext

import (
	"context"
	"errors"
)

func acquireReconcileLock(context.Context, string) (func() error, error) {
	return nil, errors.New("serialized sysext reconciliation requires Linux flock")
}

func atomicReplaceDirectory(string, string, bool) error {
	return errors.New("atomic sysext generation requires Linux renameat2")
}

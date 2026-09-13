//go:build !linux

package deb

import "errors"

func atomicReplaceDirectory(_, _ string, _ bool) error {
	return errors.New("atomic local production generation requires Linux renameat2")
}

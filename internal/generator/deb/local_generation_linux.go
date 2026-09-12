//go:build linux

package deb

import "golang.org/x/sys/unix"

func atomicReplaceDirectory(source, destination string, destinationExists bool) error {
	flags := uint(unix.RENAME_NOREPLACE)
	if destinationExists {
		flags = unix.RENAME_EXCHANGE
	}
	return unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, destination, flags)
}

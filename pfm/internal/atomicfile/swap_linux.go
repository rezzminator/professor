//go:build linux

package atomicfile

import "golang.org/x/sys/unix"

func swapPaths(a, b string) error {
	return unix.Renameat2(unix.AT_FDCWD, a, unix.AT_FDCWD, b, unix.RENAME_EXCHANGE)
}

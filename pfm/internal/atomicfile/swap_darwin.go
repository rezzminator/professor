//go:build darwin

package atomicfile

import "golang.org/x/sys/unix"

func swapPaths(a, b string) error {
	return unix.RenamexNp(a, b, unix.RENAME_SWAP)
}

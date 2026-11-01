//go:build !linux && !darwin

package resolve

import "github.com/rezzminator/professor/pfm/internal/paths"

func newNativeProcFS(root string) ProcFS {
	root = paths.ProcRootOr(root)
	return fileProcFS{root: root}
}

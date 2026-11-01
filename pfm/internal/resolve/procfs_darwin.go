//go:build darwin

package resolve

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

type darwinProcFS struct{}

func newNativeProcFS(root string) ProcFS {
	if root != "" {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return fileProcFS{root: root}
		}
	}
	return darwinProcFS{}
}

// Environ reads kern.procargs2 through gather's decoder, the one door for that layout.
func (darwinProcFS) Environ(pid int) (map[string]string, error) {
	return gather.NewProcFS("").Environ(pid)
}

func (darwinProcFS) Stat(pid int) (ProcStat, error) {
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return ProcStat{}, fmt.Errorf("read kern.proc.pid for %d: %w", pid, err)
	}
	return ProcStat{ParentPID: int(process.Eproc.Ppid)}, nil
}

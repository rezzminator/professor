//go:build darwin

package resolve

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
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

func (darwinProcFS) Environ(pid int) (map[string]string, error) {
	return nil, fmt.Errorf(
		"process environment for %d is not implemented on darwin; KERN_PROCARGS2 is the reachable route",
		pid,
	)
}

func (darwinProcFS) Stat(pid int) (ProcStat, error) {
	process, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return ProcStat{}, fmt.Errorf("read kern.proc.pid for %d: %w", pid, err)
	}
	return ProcStat{ParentPID: int(process.Eproc.Ppid)}, nil
}

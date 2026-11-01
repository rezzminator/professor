package gather

import (
	"io/fs"
	"sort"
)

type fakeProcess struct {
	cmdline []string
	environ map[string]string
	// environErr is the error reading the environment returns — on macOS,
	// where SIP keeps one process's env from another, fs.ErrPermission.
	environErr error
	fdLinks    []FDLink
	stat       ProcStat
	birth      int64
}

type fakeProcFS struct {
	processes map[int]fakeProcess
}

func (proc *fakeProcFS) PIDs() ([]int, error) {
	pids := make([]int, 0, len(proc.processes))
	for pid := range proc.processes {
		pids = append(pids, pid)
	}
	sort.Ints(pids)
	return pids, nil
}

func (proc *fakeProcFS) Cmdline(pid int) ([]string, error) {
	process, found := proc.processes[pid]
	if !found {
		return nil, fs.ErrNotExist
	}
	return append([]string(nil), process.cmdline...), nil
}

func (proc *fakeProcFS) Environ(pid int) (map[string]string, error) {
	process, found := proc.processes[pid]
	if !found {
		return nil, fs.ErrNotExist
	}
	if process.environErr != nil {
		return nil, process.environErr
	}
	environment := make(map[string]string, len(process.environ))
	for key, value := range process.environ {
		environment[key] = value
	}
	return environment, nil
}

func (proc *fakeProcFS) FDLinks(pid int) ([]FDLink, error) {
	process, found := proc.processes[pid]
	if !found {
		return nil, fs.ErrNotExist
	}
	return append([]FDLink(nil), process.fdLinks...), nil
}

func (proc *fakeProcFS) Stat(pid int) (ProcStat, error) {
	process, found := proc.processes[pid]
	if !found {
		return ProcStat{}, fs.ErrNotExist
	}
	return process.stat, nil
}

func (proc *fakeProcFS) Birth(pid int) (int64, error) {
	process, found := proc.processes[pid]
	if !found {
		return 0, fs.ErrNotExist
	}
	return process.birth, nil
}

//go:build linux

package resolve

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type linuxProcFS struct{ root string }

func newNativeProcFS(root string) ProcFS {
	if root == "" {
		root = "/proc"
	}
	return linuxProcFS{root: root}
}

func (proc linuxProcFS) Environ(pid int) (map[string]string, error) {
	content, err := os.ReadFile(proc.path(pid, "environ"))
	if err != nil {
		return nil, proc.withheld(pid, err)
	}
	environment := make(map[string]string)
	for _, entry := range strings.Split(string(content), "\x00") {
		key, value, found := strings.Cut(entry, "=")
		if found {
			environment[key] = value
		}
	}
	return environment, nil
}

func (proc linuxProcFS) Stat(pid int) (ProcStat, error) {
	fields, err := proc.statFields(pid)
	if err != nil {
		return ProcStat{}, err
	}
	if len(fields) < 2 {
		return ProcStat{}, errors.New("short proc stat")
	}
	parent, err := strconv.Atoi(fields[1])
	if err != nil {
		return ProcStat{}, err
	}
	return ProcStat{ParentPID: parent}, nil
}

// statFields are /proc/{pid}/stat's fields after the command name, state first.
func (proc linuxProcFS) statFields(pid int) ([]string, error) {
	content, err := os.ReadFile(proc.path(pid, "stat"))
	if err != nil {
		return nil, err
	}
	closeParen := strings.LastIndex(string(content), ") ")
	if closeParen < 0 {
		return nil, errors.New("malformed proc stat")
	}
	return strings.Fields(string(content[closeParen+2:])), nil
}

// withheld tags a refused environment read of a process that can never be a
// pane of the caller's: another uid's or a non-dumpable one, or one with no
// terminal holding capabilities this process lacks (the systemd user manager a
// herdr-keep holder runs under). Any other refusal stays a read failure.
func (proc linuxProcFS) withheld(pid int, err error) error {
	if errors.Is(err, os.ErrPermission) && (foreignOwned(proc.path(pid, "environ")) || proc.privileged(pid)) {
		return fmt.Errorf("%w: %w", ErrForeignProcess, err)
	}
	return err
}

// privileged reports a process with no controlling terminal whose effective
// capabilities exceed this process's; this process absent from the proc root
// holds none. A pane's process has a terminal, so it is never passed over.
func (proc linuxProcFS) privileged(pid int) bool {
	fields, err := proc.statFields(pid)
	if err != nil || len(fields) < 5 || fields[4] != "0" {
		return false
	}
	theirs, err := proc.effectiveCapabilities(pid)
	if err != nil {
		return false
	}
	ours, err := proc.effectiveCapabilities(os.Getpid())
	if errors.Is(err, os.ErrNotExist) {
		ours = 0
	} else if err != nil {
		return false
	}
	return theirs&^ours != 0
}

func (proc linuxProcFS) effectiveCapabilities(pid int) (uint64, error) {
	path := proc.path(pid, "status")
	content, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(content), "\n") {
		if value, found := strings.CutPrefix(line, "CapEff:"); found {
			return strconv.ParseUint(strings.TrimSpace(value), 16, 64)
		}
	}
	return 0, fmt.Errorf("no CapEff line in %s", path)
}

// foreignOwned reports a proc file another uid owns; a non-dumpable process's files belong to root.
func foreignOwned(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) != os.Geteuid()
}

func (proc linuxProcFS) path(pid int, element string) string {
	return filepath.Join(proc.root, strconv.Itoa(pid), element)
}

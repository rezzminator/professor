package action

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"hostops/pfm/internal/gather"
)

// RealProcesses reads the small /proc subset needed by the stray sweep.
type RealProcesses struct {
	Root string
}

func (processes RealProcesses) Processes(
	ctx context.Context,
) ([]Process, error) {
	root := processes.Root
	if root == "" {
		root = "/proc"
	}
	// A root that exists is always honoured — that is how the jail feeds this a
	// fixture tree. When there is none, the kernel's own table answers instead:
	// macOS has no /proc, and a stray sweep that could not enumerate processes
	// would conclude there are no strays.
	if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
		return nativeProcesses(ctx)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	result := make([]Process, 0)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || !entry.IsDir() {
			continue
		}
		content, err := os.ReadFile(filepath.Join(root, entry.Name(), "cmdline"))
		if err != nil || len(content) == 0 {
			continue
		}
		argv := gather.SplitNUL(content)
		tty := ""
		if target, err := os.Readlink(
			filepath.Join(root, entry.Name(), "fd", "0"),
		); err == nil {
			tty = strings.TrimPrefix(target, "/dev/")
		}
		result = append(result, Process{PID: pid, Argv: argv, TTY: tty})
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].PID < result[right].PID
	})
	return result, nil
}

func (RealProcesses) Terminate(pid int) error {
	return gather.Terminate(pid)
}

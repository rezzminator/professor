package action

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// RealProcesses reads the small /proc subset needed by the stray sweep.
type RealProcesses struct {
	Root string
}

func (processes RealProcesses) Processes(
	ctx context.Context,
) ([]Process, error) {
	root := paths.ProcRootOr(processes.Root)
	// A root that exists is always honoured — that is how the jail feeds this a
	// fixture tree. When there is none, the kernel's own table answers instead:
	// macOS has no /proc, and a stray sweep that could not enumerate processes
	// would conclude there are no strays.
	if info, statErr := os.Stat(root); statErr != nil || !info.IsDir() {
		return nativeProcesses(ctx)
	}
	table := gather.NewProcFS(root)
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
		process, found := scanProcess(table, pid)
		if !found {
			continue
		}
		if target, err := os.Readlink(
			filepath.Join(root, entry.Name(), "fd", "0"),
		); err == nil {
			process.TTY = strings.TrimPrefix(target, "/dev/")
		}
		result = append(result, process)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].PID < result[right].PID
	})
	return result, nil
}

// scanProcess reads pid with the identity the signal door needs. A process
// the table cannot identify keeps its argv for the sweep's match and the reason
// in Unidentified; one that is gone, or whose argv is withheld or empty, is no
// candidate (the sweep matches on argv).
func scanProcess(table gather.ProcFS, pid int) (Process, bool) {
	identity, err := gather.IdentifyProcess(table, pid)
	if err == nil {
		return Process{
			PID: pid, Start: identity.Start, UID: identity.UID,
			Argv: identity.Argv, Executable: identity.Executable,
		}, len(identity.Argv) > 0
	}
	if gather.IsGone(err) {
		return Process{}, false
	}
	argv, argvErr := table.Cmdline(pid)
	if argvErr != nil || len(argv) == 0 {
		return Process{}, false
	}
	return Process{PID: pid, Argv: argv, Unidentified: err}, true
}

// Terminate sends SIGTERM through gather's signal door over the table
// Processes reads.
func (processes RealProcesses) Terminate(target gather.Target) (gather.Outcome, error) {
	return gather.NewDoor(gather.NewProcFS(processes.Root)).Signal(target, syscall.SIGTERM)
}

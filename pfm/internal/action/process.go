package action

import (
	"context"
	"fmt"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

// RealProcesses reads the process subset the stray sweep needs through
// gather's table: Root "" is the kernel's own, an existing directory a jail.
type RealProcesses struct {
	Root string
}

func (processes RealProcesses) Processes(
	ctx context.Context,
) ([]Process, error) {
	// A jail root that does not exist is an error, never the live table: a
	// stray sweep that could not enumerate processes would conclude there are
	// no strays.
	table, err := gather.OpenProcFS(processes.Root)
	if err != nil {
		return nil, fmt.Errorf("read process table: %w", err)
	}
	pids, err := table.PIDs()
	if err != nil {
		return nil, err
	}
	terminals, ok := table.(gather.ProcTerminals)
	if !ok {
		return nil, fmt.Errorf("read process table: %T reports no terminals", table)
	}
	ttys, err := terminals.Terminals()
	if err != nil {
		return nil, err
	}
	result := make([]Process, 0, len(pids))
	for _, pid := range pids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// A process whose argv is withheld is not one of ours: the sweep
		// matches on argv, so it could not have been a target anyway.
		process, found := scanProcess(table, pid)
		if !found {
			continue
		}
		process.TTY = ttys[pid]
		result = append(result, process)
	}
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

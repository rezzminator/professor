//go:build darwin

package action

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// nativeProcesses enumerates the process table on a kernel with no /proc.
//
// It borrows gather's reader rather than decoding sysctl a second time — the
// argv layout kern.procargs2 returns is fiddly enough that a second copy would
// be a second set of bugs.
func nativeProcesses(ctx context.Context) ([]Process, error) {
	table := gather.NewProcFS("")
	pids, err := table.PIDs()
	if err != nil {
		return nil, err
	}
	terminals, err := terminalByPID(ctx)
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
		process.TTY = terminals[pid]
		result = append(result, process)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].PID < result[right].PID
	})
	return result, nil
}

// terminalByPID maps pids to their controlling terminal in ONE ps call. The
// Linux reader gets this from /proc/<pid>/fd/0 and strips the /dev/ prefix; ps
// already prints the short form, and "??" for a process with no terminal. A ps
// that could not be found or run is an error, never a table of no terminals.
func terminalByPID(ctx context.Context) (map[int]string, error) {
	ps, err := deps.Resolve("ps")
	if err != nil {
		return nil, fmt.Errorf("map controlling terminals: resolve ps: %w", err)
	}
	result, err := obs.Runner(deps.RealRunner{}).Run(ctx, []string{ps, "-A", "-o", "pid=,tty="}, deps.RunOptions{})
	if err == nil && result.ExitCode != 0 {
		err = fmt.Errorf("exit status %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	if err != nil {
		return nil, fmt.Errorf("map controlling terminals via %s: %w", ps, err)
	}
	terminals := make(map[int]string)
	for _, line := range strings.Split(string(result.Stdout), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[1] == "??" || fields[1] == "?" {
			continue
		}
		if pid, convErr := strconv.Atoi(fields[0]); convErr == nil {
			terminals[pid] = fields[1]
		}
	}
	return terminals, nil
}

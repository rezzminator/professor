//go:build darwin

package action

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/obs"
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
	terminals := terminalByPID()
	result := make([]Process, 0, len(pids))
	for _, pid := range pids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		argv, err := table.Cmdline(pid)
		if err != nil || len(argv) == 0 {
			// A process whose argv is withheld is not one of ours: the sweep
			// matches on argv, so it could not have been a target anyway.
			continue
		}
		result = append(result, Process{PID: pid, Argv: argv, TTY: terminals[pid]})
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].PID < result[right].PID
	})
	return result, nil
}

// terminalByPID maps pids to their controlling terminal in ONE ps call. The
// Linux reader gets this from /proc/<pid>/fd/0 and strips the /dev/ prefix; ps
// already prints the short form, and "??" for a process with no terminal.
func terminalByPID() map[int]string {
	result, err := obs.Runner(deps.RealRunner{}).Run(
		context.Background(),
		[]string{deps.Executable("ps"), "-A", "-o", "pid=,tty="},
		deps.RunOptions{},
	)
	if err != nil {
		return nil
	}
	if result.ExitCode != 0 {
		return nil
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
	return terminals
}

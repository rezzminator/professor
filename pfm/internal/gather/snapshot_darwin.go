//go:build darwin

package gather

import (
	"context"
	"fmt"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// psSnapshotFormat is the one column list a macOS table reads: every column
// RSS, CPU time and terminal lookups need, so one ps serves them all. comm is
// last because it may hold spaces.
const psSnapshotFormat = "pid=,ppid=,time=,rss=,tty=,comm="

// runPSSnapshot runs the table's one ps; a ps that could not be found or run
// is an error, never an empty table.
func runPSSnapshot() (string, error) {
	return runDarwinCommand("ps", "-A", "-o", psSnapshotFormat)
}

// runVMStat runs vm_stat for the host's page counters.
func runVMStat() (string, error) {
	output, err := runDarwinCommand("vm_stat")
	if err != nil {
		return "", fmt.Errorf("sample Darwin host memory: %w", err)
	}
	return output, nil
}

// darwinCommandTimeout bounds one host counter command: its readers take no
// context, so a hung ps or vm_stat must not hang the sweep or sample that asked.
const darwinCommandTimeout = 30 * time.Second

func runDarwinCommand(name string, args ...string) (string, error) {
	path, err := deps.Resolve(name)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", name, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), darwinCommandTimeout)
	defer cancel()
	result, err := obs.Runner(deps.RealRunner{}).Run(ctx, append([]string{path}, args...), deps.RunOptions{})
	if err == nil && result.ExitCode != 0 {
		err = fmt.Errorf("exit status %d", result.ExitCode)
	}
	if err != nil {
		return "", fmt.Errorf("run %s: %w", name, err)
	}
	return string(result.Stdout), nil
}

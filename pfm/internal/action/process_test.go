package action

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

// A stray that exited between the scan and the kill is gone: Terminate sends
// nothing and reports it, never an error.
func TestRealProcessesTerminateDeadPID(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^$")
	if err := command.Run(); err != nil {
		t.Fatalf("run short-lived process: %v", err)
	}
	target := gather.Target{Identity: gather.Identity{
		PID: command.Process.Pid, Start: 1, UID: uint32(os.Geteuid()),
		Argv: []string{os.Args[0], "-test.run=^$"},
	}}

	outcome, err := (RealProcesses{}).Terminate(target)
	if err != nil || outcome != gather.OutcomeGone {
		t.Fatalf("Terminate(dead pid %d) = %v, %v; want %v, nil", target.PID, outcome, err, gather.OutcomeGone)
	}
}

// The stray sweep reads a jail through gather's table: a pane process's TTY
// is what its fd 0 names, "/dev/" trimmed. A jail root never staged is an
// error, never the live table.
func TestRealProcessesReadsAJailTable(t *testing.T) {
	root := t.TempDir()
	process := filepath.Join(root, "4242")
	if err := os.MkdirAll(filepath.Join(process, "fd"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(process, "cmdline"), []byte("claude\x00--resume\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/pts/7", filepath.Join(process, "fd", "0")); err != nil {
		t.Fatal(err)
	}
	processes, err := (RealProcesses{Root: root}).Processes(context.Background())
	if err != nil || len(processes) != 1 || processes[0].PID != 4242 || processes[0].TTY != "pts/7" ||
		!slices.Equal(processes[0].Argv, []string{"claude", "--resume"}) {
		t.Fatalf("Processes(%s) = %+v, %v; want pid 4242 on pts/7", root, processes, err)
	}
	missing := filepath.Join(root, "missing-proc")
	if processes, err := (RealProcesses{Root: missing}).Processes(context.Background()); processes != nil ||
		!errors.Is(err, gather.ErrNoProcRoot) {
		t.Fatalf("Processes(%s) = %+v, %v; want gather.ErrNoProcRoot", missing, processes, err)
	}
}

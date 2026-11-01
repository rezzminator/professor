package action

import (
	"os"
	"os/exec"
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

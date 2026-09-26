package action

import (
	"os"
	"os/exec"
	"testing"
)

func TestRealProcessesTerminateDeadPID(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^$")
	if err := command.Run(); err != nil {
		t.Fatalf("run short-lived process: %v", err)
	}

	if err := (RealProcesses{}).Terminate(command.Process.Pid); err != nil {
		t.Fatalf("Terminate(dead pid) error = %v, want nil", err)
	}
}

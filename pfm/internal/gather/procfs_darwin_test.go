package gather

import (
	"errors"
	"io/fs"
	"os/exec"
	"testing"
)

// An exited pid reads as absent, as a missing /proc entry does on Linux, so a
// caller walking parents tells "gone" from "unreadable".
func TestDarwinProcFSStatOfAnExitedPIDIsNotExist(t *testing.T) {
	command := exec.Command("/usr/bin/true")
	if err := command.Run(); err != nil {
		t.Fatalf("run /usr/bin/true: %v", err)
	}
	if _, err := NewDarwinProcFS().Stat(command.Process.Pid); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Stat(exited pid %d) = %v, want fs.ErrNotExist", command.Process.Pid, err)
	}
}

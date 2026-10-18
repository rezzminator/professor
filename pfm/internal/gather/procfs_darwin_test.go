package gather

import (
	"errors"
	"io/fs"
	"os/exec"
	"testing"
)

// An exited pid reads as absent, as a missing /proc entry does on Linux, so a
// caller walking parents tells "gone" from "unreadable".
func TestDarwinProcFSReadsOfAnExitedPIDAreNotExist(t *testing.T) {
	command := exec.Command("/usr/bin/true")
	if err := command.Run(); err != nil {
		t.Fatalf("run /usr/bin/true: %v", err)
	}
	proc := NewDarwinProcFS()
	for _, tc := range []struct {
		name string
		read func(int) error
	}{
		{"Stat", func(pid int) error { _, err := proc.Stat(pid); return err }},
		{"Cmdline", func(pid int) error { _, err := proc.Cmdline(pid); return err }},
		{"Environ", func(pid int) error { _, err := proc.Environ(pid); return err }},
		{"ProcessIdentity", func(pid int) error { _, err := proc.ProcessIdentity(pid); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.read(command.Process.Pid); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("%s(exited pid %d)=%v want fs.ErrNotExist", tc.name, command.Process.Pid, err)
			}
		})
	}
}

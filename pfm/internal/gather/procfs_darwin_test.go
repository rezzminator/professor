package gather

import (
	"errors"
	"io/fs"
	"os/exec"
	"syscall"
	"testing"
)

// An exited pid reads as absent, as a missing /proc entry does on Linux, so a
// caller walking parents tells "gone" from "unreadable". The lsof doors once
// returned a bare "exit status 1" here, failing pfm install's Herdr stop.
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
		{"Birth", func(pid int) error { _, err := proc.Birth(pid); return err }},
		{"Executable", func(pid int) error { _, err := proc.Executable(pid); return err }},
		{"Image", func(pid int) error { _, err := proc.Image(pid); return err }},
		{"FDLinks", func(pid int) error { _, err := proc.FDLinks(pid); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.read(command.Process.Pid); !errors.Is(err, fs.ErrNotExist) || !errors.Is(err, syscall.ESRCH) {
				t.Fatalf(
					"%s(exited pid %d)=%v want fs.ErrNotExist and syscall.ESRCH",
					tc.name,
					command.Process.Pid,
					err,
				)
			}
		})
	}
}

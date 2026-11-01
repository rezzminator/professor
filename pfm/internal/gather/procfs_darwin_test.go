package gather

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
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

// With ps and lsof unreachable, every probe built on them says it could not
// look, naming the binary, never an empty answer: no open files would let
// archive evict a file still in use.
func TestDarwinProcFSProbesWithoutTheirBinaryCannotLook(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	pid := os.Getpid()
	proc := NewDarwinProcFS()
	for _, tc := range []struct {
		name string
		read func() error
		want string
	}{
		{
			"RSSKB", func() error { _, err := proc.RSSKB(pid); return err },
			`sample resident memory: resolve ps: exec: "ps": executable file not found in $PATH`,
		},
		{
			"FDLinks", func() error { _, err := proc.FDLinks(pid); return err },
			fmt.Sprintf(`list open files for pid %d: resolve lsof: exec: "lsof": executable file not found in $PATH`, pid),
		},
		{
			"Image", func() error { _, err := proc.Image(pid); return err },
			fmt.Sprintf(`read the executable of pid %d: resolve lsof: exec: "lsof": executable file not found in $PATH`, pid),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.read(); err == nil || err.Error() != tc.want {
				t.Fatalf("%s with an empty PATH = %v, want %q", tc.name, err, tc.want)
			}
		})
	}
}

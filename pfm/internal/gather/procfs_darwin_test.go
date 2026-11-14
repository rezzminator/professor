package gather

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

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
			"sample resident memory: resolve ps: " + testjail.NotOnPath("ps"),
		},
		{
			"FDLinks", func() error { _, err := proc.FDLinks(pid); return err },
			fmt.Sprintf("list open files for pid %d: resolve lsof: %s", pid, testjail.NotOnPath("lsof")),
		},
		{
			"Image", func() error { _, err := proc.Image(pid); return err },
			fmt.Sprintf("read the executable of pid %d: resolve lsof: %s", pid, testjail.NotOnPath("lsof")),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.read(); err == nil || err.Error() != tc.want {
				t.Fatalf("%s with an empty PATH = %v, want %q", tc.name, err, tc.want)
			}
		})
	}
}

// launchd is root's: kern.procargs2 refuses its environment to any other uid
// with a bare EINVAL, which reads as foreign, not as a failed look, so the
// caller lookup and the chat-call guard pass over it.
func TestDarwinEnvironTagsForeignProcess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every environment")
	}
	_, err := NewDarwinProcFS().Environ(1)
	if !errors.Is(err, ErrForeignProcess) || !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("pid 1 (root) was not tagged foreign: %v", err)
	}
}

func TestDarwinDeadReadsAZombieAndAnExitingProcess(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state int8
		flags int32
		want  bool
	}{
		{"running", 2, 0x4004, false},
		{"zombie", 5, 0x4004, true},
		{"exiting, not yet a zombie", 2, 0x6004, true},
	} {
		if got := darwinDead(tc.state, tc.flags); got != tc.want {
			t.Errorf("%s: darwinDead(%d, %#x) = %t, want %t", tc.name, tc.state, tc.flags, got, tc.want)
		}
	}
}

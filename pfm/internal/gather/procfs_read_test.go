package gather

import (
	"errors"
	"io/fs"
	"syscall"
	"testing"
)

// A read the kernel refuses with ESRCH, the process having exited between the
// open and the read, is a gone process: it carries fs.ErrNotExist, which every
// Stat caller tests, and keeps ESRCH. Any other refusal stays as it was read.
func TestProcReadErrorReadsAnExitedProcessAsGone(t *testing.T) {
	for _, tc := range []struct {
		name  string
		errno syscall.Errno
		gone  bool
		want  string
	}{
		{"exited mid-read", syscall.ESRCH, true, "read /proc/4439/stat: no such process: file does not exist"},
		{"missing at open", syscall.ENOENT, true, "read /proc/4439/stat: no such file or directory"},
		{"refused", syscall.EACCES, false, "read /proc/4439/stat: permission denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := procReadError(&fs.PathError{Op: "read", Path: "/proc/4439/stat", Err: tc.errno})
			if errors.Is(err, fs.ErrNotExist) != tc.gone || !errors.Is(err, tc.errno) || err.Error() != tc.want {
				t.Fatalf("procReadError = %q, want %q (gone %t, %v kept)", err, tc.want, tc.gone, tc.errno)
			}
		})
	}
}

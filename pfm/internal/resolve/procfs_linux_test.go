//go:build linux

package resolve

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestLinuxEnvironTagsForeignProcess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every environment")
	}
	if _, err := os.ReadFile(filepath.Join(paths.SystemProcRoot(), "1", "environ")); !errors.Is(err, os.ErrPermission) {
		t.Skipf("pid 1's environment is not refused here: %v", err)
	}
	_, err := newNativeProcFS("").Environ(1)
	if !errors.Is(err, ErrForeignProcess) || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("pid 1 (root) was not tagged foreign: %v", err)
	}
}

func TestLinuxEnvironKeepsOwnRefusedRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "4242"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "4242", "environ"), []byte("A=1\x00"), 0o000); err != nil {
		t.Fatal(err)
	}
	_, err := (linuxProcFS{root: root}).Environ(4242)
	if !errors.Is(err, os.ErrPermission) || errors.Is(err, ErrForeignProcess) {
		t.Fatalf("a refused read of the caller's own file must stay a read failure: %v", err)
	}
}

// A same-uid process with no controlling terminal holding capabilities the
// caller lacks (systemd --user: tty_nr 0, CapEff 0x800000000) refuses its
// environment; it is no pane of the caller's. A process with a terminal, as
// every pane's process has, keeps its read failure however privileged it is.
func TestLinuxEnvironWithheldByPrivilege(t *testing.T) {
	root := t.TempDir()
	proc := linuxProcFS{root: root}
	for _, tc := range []struct {
		name               string
		pid                int
		capabilities, stat string
		foreign            bool
	}{
		{"user manager", 4242, "0000000800000000", "4242 (systemd) S 1 4242 4242 0 -1", true},
		{"unprivileged", 4243, "0000000000000000", "4243 (sh) S 1 4243 4243 0 -1", false},
		{"privileged on a terminal", 4244, "0000000800000000", "4244 (claude) S 1 4244 4244 34816 4244", false},
		{"privileged, terminal unread", 4245, "0000000800000000", "", false},
	} {
		dir := filepath.Join(root, strconv.Itoa(tc.pid))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		status := "Name:\tfixture\nCapEff:\t" + tc.capabilities + "\n"
		if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0o600); err != nil {
			t.Fatal(err)
		}
		if tc.stat != "" {
			if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(tc.stat+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		denied := &os.PathError{Op: "open", Path: proc.path(tc.pid, "environ"), Err: syscall.EACCES}
		err := proc.withheld(tc.pid, denied)
		if !errors.Is(err, os.ErrPermission) || errors.Is(err, ErrForeignProcess) != tc.foreign {
			t.Errorf("%s: pid %d refused environ = %v, want foreign %t (fixture %s)",
				tc.name, tc.pid, err, tc.foreign, dir)
		}
	}
}

//go:build linux

package resolve

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLinuxEnvironTagsForeignProcess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every environment")
	}
	if _, err := os.ReadFile("/proc/1/environ"); !errors.Is(err, os.ErrPermission) {
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

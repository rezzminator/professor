package tmux

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestAcceptsSeesALiveSocketAndRefusesADeadOne(t *testing.T) {
	directory, err := os.MkdirTemp("", "acc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Logf("remove %q: %v", directory, err)
		}
	})
	path := filepath.Join(directory, "s")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Accepts(path); err != nil {
		t.Fatalf("a listening socket must accept: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Accepts(path); err == nil {
		t.Fatal("a socket nobody listens on must be refused")
	}
	if err := Accepts(filepath.Join(directory, "missing")); err == nil {
		t.Fatal("a path with no socket must be refused")
	}
}

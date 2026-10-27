package deps

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestDialUnixReachesAListeningSocketAndNamesAMissingOne(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "s.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	conn, err := DialUnix(context.Background(), path, time.Second)
	if err != nil {
		t.Fatalf("DialUnix(%s): %v", path, err)
	}
	_ = conn.Close()
	if conn, err := DialUnix(context.Background(), filepath.Join(root, "missing.sock"), time.Second); err == nil {
		_ = conn.Close()
		t.Fatal("DialUnix on a missing socket succeeded, want an error")
	}
}

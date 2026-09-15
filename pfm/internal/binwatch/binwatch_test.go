package binwatch

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// lockedBuffer is a stderr the watcher goroutine and the test can share.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (locked *lockedBuffer) Write(data []byte) (int, error) {
	locked.mu.Lock()
	defer locked.mu.Unlock()
	return locked.buffer.Write(data)
}

func (locked *lockedBuffer) String() string {
	locked.mu.Lock()
	defer locked.mu.Unlock()
	return locked.buffer.String()
}

const watchTick = 10 * time.Millisecond

func writeFakeBuild(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func waitReplaced(replaced <-chan struct{}, within time.Duration) bool {
	select {
	case <-replaced:
		return true
	case <-time.After(within):
		return false
	}
}

// TestWatchExecutableSeesAnAtomicInstall is the regression for the stale
// shared daemon: make host-install renames a new build over ~/.local/bin/pfm,
// and the daemon — the one pfm process nobody closes — kept serving the old
// image for days. The watcher must fire on exactly that rename.
func TestWatchExecutableSeesAnAtomicInstall(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "pfm")
	writeFakeBuild(t, binary, "old build")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	replaced, err := watch(ctx, binary, watchTick, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if waitReplaced(replaced, 20*watchTick) {
		t.Fatal("watcher fired with the binary untouched")
	}
	staged := filepath.Join(directory, "pfm.new")
	writeFakeBuild(t, staged, "new build")
	if err := os.Rename(staged, binary); err != nil {
		t.Fatal(err)
	}
	if !waitReplaced(replaced, 2*time.Second) {
		t.Fatal("watcher never saw the binary renamed over by a new build")
	}
}

// TestWatchExecutableSeesACopyOverTheSamePath covers the install that keeps
// the inode: a copy onto the path changes size and mtime instead.
func TestWatchExecutableSeesACopyOverTheSamePath(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "pfm")
	writeFakeBuild(t, binary, "old build")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	replaced, err := watch(ctx, binary, watchTick, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	writeFakeBuild(t, binary, "a longer new build")
	if !waitReplaced(replaced, 2*time.Second) {
		t.Fatal("watcher never saw the binary copied over in place")
	}
}

// TestWatchExecutableNeverRestartsOntoAMissingBinary: a path that vanishes
// mid-run is reported, not treated as a replacement — restarting onto nothing
// would take the daemon down with no build to come back on.
func TestWatchExecutableNeverRestartsOntoAMissingBinary(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "pfm")
	writeFakeBuild(t, binary, "build")
	stderr := &lockedBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	replaced, err := watch(ctx, binary, watchTick, stderr)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(binary); err != nil {
		t.Fatal(err)
	}
	if waitReplaced(replaced, 20*watchTick) {
		t.Fatal("watcher fired on a missing binary")
	}
	cancel()
	time.Sleep(2 * watchTick)
	if got := stderr.String(); strings.Count(got, "cannot stat own executable") != 1 {
		t.Fatalf("stderr = %q, want the unreadable executable said once", got)
	}
}

func TestWatchExecutableRefusesAnUnreadableStart(t *testing.T) {
	if _, err := watch(
		context.Background(),
		filepath.Join(t.TempDir(), "absent"),
		watchTick,
		&bytes.Buffer{},
	); err == nil {
		t.Fatal("watching an absent executable returned no error")
	}
}

func startServe(t *testing.T, replaced <-chan struct{}) (*http.Server, string, <-chan int, *bytes.Buffer) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})}
	var stderr bytes.Buffer
	code := make(chan int, 1)
	go func() { code <- serve(server, listener, replaced, &stderr) }()
	return server, "http://" + listener.Addr().String(), code, &stderr
}

// TestServeUntilReplacedExitsForTheSupervisor: a replacement stops the
// daemon with ExitReplaced, the non-zero status systemd's
// Restart=on-failure and launchd's KeepAlive both restart on.
func TestServeUntilReplacedExitsForTheSupervisor(t *testing.T) {
	replaced := make(chan struct{})
	_, endpoint, code, stderr := startServe(t, replaced)
	response, err := http.Get(endpoint)
	if err != nil {
		t.Fatalf("daemon not serving before the replacement: %v", err)
	}
	response.Body.Close()
	close(replaced)
	select {
	case got := <-code:
		if got != ExitReplaced {
			t.Fatalf("exit code = %d, want %d", got, ExitReplaced)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon kept serving after its binary was replaced")
	}
	if !strings.Contains(stderr.String(), "own executable was replaced") {
		t.Fatalf("stderr = %q, want the restart reason", stderr.String())
	}
}

// TestServeUntilReplacedCloseIsAPlainStop: an ordinary server close is not a
// replacement and must not claim one.
func TestServeUntilReplacedCloseIsAPlainStop(t *testing.T) {
	server, _, code, stderr := startServe(t, nil)
	time.Sleep(20 * time.Millisecond)
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-code:
		if got != 0 {
			t.Fatalf("plain close exit code = %d, want 0 (stderr %q)", got, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after Close")
	}
}

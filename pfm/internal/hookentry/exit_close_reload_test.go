package hookentry

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/kill"
	"github.com/rezzminator/professor/pfm/internal/reload"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// TestRunExitCloseLeavesTheTerminalWhileAReloadIsInFlight: the terminal stays
// open only for the reload's own /exit (pane mutex held, exit mark written); the
// owner's /exit while the reload still waits, and any exit once the reload let
// go, close it.
func TestRunExitCloseLeavesTheTerminalWhileAReloadIsInFlight(t *testing.T) {
	original, originalEnv := exitCloseTerminals, exitCloseIdentify
	t.Cleanup(func() { exitCloseTerminals, exitCloseIdentify = original, originalEnv })
	home := t.TempDir()
	sidDir := filepath.Join(home, "sid")
	t.Setenv("PFM_HOME", home)
	t.Setenv("PFM_SID_DIR", sidDir)
	if err := os.MkdirAll(sidDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const socket, pane = "cc-500-1-1", "%3"
	lockPath := filepath.Join(sidDir, "."+socket+"."+pane+".reloadlock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("hold reload lock: %v", err)
	}
	called := false
	exitCloseTerminals = func(context.Context, string, kill.ViewportDeps) ([]int, []string, error) {
		called = true
		return nil, nil, nil
	}
	exitCloseIdentify = func(context.Context) (resolve.Identity, error) {
		return resolve.Identity{Backend: "tmux", SocketPath: "/tmp/tmux-501/" + socket, Pane: pane}, nil
	}

	payload := `{"hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`
	var stderr bytes.Buffer
	if code := ExitClose(strings.NewReader(payload), &stderr); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if !called {
		t.Fatalf("lock held, no exit mark (the owner's /exit while the reload waits): closer did not run; stderr=%q",
			stderr.String())
	}
	if err := os.WriteFile(reload.ExitMarkPath(sidDir, socket, pane), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	called = false
	if code := ExitClose(strings.NewReader(payload), &stderr); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	want := "pfm internal exit-close: left open — reload in flight for " + socket + " " + pane +
		": the pane is being rebooted, not closed\n"
	if called || stderr.String() != want {
		t.Fatalf("the reload's own /exit: called=%v stderr=%q, want the terminal left open with %q",
			called, stderr.String(), want)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	called = false
	if code := ExitClose(strings.NewReader(payload), &stderr); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if !called {
		t.Fatalf("closer did not run after reload lock release; stderr=%q", stderr.String())
	}
}

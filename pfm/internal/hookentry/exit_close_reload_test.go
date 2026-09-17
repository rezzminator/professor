package hookentry

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"hostops/pfm/internal/kill"
)

func TestRunExitCloseLeavesTheTerminalWhileAReloadIsInFlight(t *testing.T) {
	original, originalEnv := exitCloseTerminals, exitCloseEnv
	t.Cleanup(func() { exitCloseTerminals, exitCloseEnv = original, originalEnv })
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
	exitCloseEnv = func(key string) string {
		switch key {
		case "TMUX":
			return "/tmp/tmux-501/" + socket + ",9999,0"
		case "TMUX_PANE":
			return pane
		default:
			return ""
		}
	}
	payload := `{"hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`
	var stderr bytes.Buffer
	if code := ExitClose(strings.NewReader(payload), &stderr); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	if called || !strings.Contains(stderr.String(), "reload in flight") {
		t.Fatalf("called=%v stderr=%q", called, stderr.String())
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

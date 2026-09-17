package main

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

// TestRunExitCloseLeavesTheTerminalWhileAReloadIsInFlight pins the one /exit
// that must NOT close the tab: `pfm chat reload` ends the old process with
// /exit and reboots the same pane, holding its pane mutex
// (`.<socket>.<pane>.reloadlock` under the sid dir) the whole time. Closing
// the terminal then takes the tab down in the middle of the reboot — the
// reborn chat survives inside tmux with nobody watching it.
func TestRunExitCloseLeavesTheTerminalWhileAReloadIsInFlight(t *testing.T) {
	original := exitCloseTerminals
	originalEnv := exitCloseEnv
	t.Cleanup(func() {
		exitCloseTerminals = original
		exitCloseEnv = originalEnv
	})
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
		t.Fatalf("hold the reload lock: %v", err)
	}

	called := false
	exitCloseTerminals = func(_ context.Context, _ string, _ kill.ViewportDeps) ([]int, []string, error) {
		called = true
		return nil, nil, nil
	}
	exitCloseEnv = func(key string) string {
		switch key {
		case "TMUX":
			return "/tmp/tmux-501/" + socket + ",9999,0"
		case "TMUX_PANE":
			return pane
		}
		return ""
	}
	payload := `{"hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`

	var stderr bytes.Buffer
	if code := runExitClose(strings.NewReader(payload), &stderr); code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%q", code, stderr.String())
	}
	if called {
		t.Fatalf("closer ran during an in-flight reload — the tab would close mid-reboot; stderr=%q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "reload in flight") {
		t.Fatalf("the skip must be named: stderr=%q", stderr.String())
	}

	// Lock released: the same /exit is a real exit again and closes the tab.
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	stderr.Reset()
	called = false
	if code := runExitClose(strings.NewReader(payload), &stderr); code != 0 {
		t.Fatalf("exit=%d, want 0; stderr=%q", code, stderr.String())
	}
	if !called {
		t.Fatalf("closer did not run after the reload lock was released; stderr=%q", stderr.String())
	}
}

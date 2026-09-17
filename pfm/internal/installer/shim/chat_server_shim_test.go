package shim

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// `cx` creates its server through pfm's one chat-server creator, never a zsh
// copy of it. The fixture runs the real `_cx_server` with a RECORDING pfm and
// a RECORDING tmux — no server is created, no socket is touched — and reads
// back who was asked to do what.
func runCxServer(t *testing.T, pfmScript string) (pfmCalls, tmuxCalls, stderr string, failed bool) {
	t.Helper()
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	shimPath := embeddedShimPath(t)
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	fakeBin := filepath.Join(home, "fakebin")
	for _, directory := range []string{binDir, fakeBin} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	pfmLog := filepath.Join(home, "pfm-calls")
	tmuxLog := filepath.Join(home, "tmux-calls")
	recorder := func(log, tail string) []byte {
		return []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + log + "\n" + tail)
	}
	if err := os.WriteFile(filepath.Join(binDir, "pfm"), recorder(pfmLog, pfmScript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fakeBin, "tmux"), recorder(tmuxLog, "exit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	script := "source " + quoteZsh(shimPath) + "\n_cx_server cx-probe-shim-sock /tmp 'sleep 1'\n"
	command := jailedZshCommand(
		zsh, script, home,
		"PATH="+fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	var errorOutput strings.Builder
	command.Stderr = &errorOutput
	runErr := command.Run()
	read := func(log string) string {
		body, err := os.ReadFile(log)
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("read %s: %v", log, err)
		}
		return string(body)
	}
	return read(pfmLog), read(tmuxLog), errorOutput.String(), runErr != nil
}

func TestShimCreatesTheCodexServerThroughPfmsOneCreator(t *testing.T) {
	pfmCalls, tmuxCalls, stderr, failed := runCxServer(t, "exit 0\n")
	if failed {
		t.Fatalf("_cx_server failed on a pfm that succeeded: %s", stderr)
	}
	if want := "internal chat-server cx-probe-shim-sock /tmp sleep 1\n"; pfmCalls != want {
		t.Fatalf("pfm calls = %q, want %q", pfmCalls, want)
	}
	if tmuxCalls != "" {
		t.Fatalf("the shim ran tmux itself — a second creator:\n%s", tmuxCalls)
	}
}

// A creator that failed is a chat that did not open: `cx` must stop before it
// attaches to nothing, and pfm's own reason reaches the user.
func TestShimStopsWhenPfmCouldNotCreateTheServer(t *testing.T) {
	_, _, stderr, failed := runCxServer(
		t, "echo 'pfm internal chat-server: create chat server: boom' >&2\nexit 1\n",
	)
	if !failed {
		t.Fatal("_cx_server succeeded on a pfm that failed")
	}
	if !strings.Contains(stderr, "create chat server: boom") {
		t.Fatalf("the shim swallowed pfm's reason: %q", stderr)
	}
}

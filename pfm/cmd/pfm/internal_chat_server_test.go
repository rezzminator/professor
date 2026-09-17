package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/paths"
)

// chatServerRuntime is a jailed runtime whose tmux directory is SHORT — a
// long socket path is "File name too long", which reads like a tmux bug.
func chatServerRuntime(t *testing.T, enabled bool) (commandRuntime, string) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "pfmcs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv(paths.EnvTmuxConf, "/dev/null")
	machine := pfmconfig.Defaults(root, nil)
	machine.Tmux.Titles.Enabled = enabled
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	return commandRuntime{Config: machine, Paths: paths.Values{Home: root, TmuxDir: tmuxDir}}, tmuxDir
}

func runChatServer(t *testing.T, runtime commandRuntime, args ...string) (string, int) {
	t.Helper()
	var stderr bytes.Buffer
	code := runInternalChatServer(args, &stderr, runtime)
	if code == 0 && len(args) == 3 {
		socketPath := filepath.Join(runtime.Paths.TmuxDir, args[0])
		t.Cleanup(func() {
			kill := exec.Command("tmux", "-S", socketPath, "kill-server")
			kill.Env = append(os.Environ(), "TMUX=")
			_ = kill.Run()
		})
	}
	return stderr.String(), code
}

func readChatServer(t *testing.T, socketPath string, arguments ...string) string {
	t.Helper()
	command := exec.Command("tmux", append([]string{"-S", socketPath}, arguments...)...)
	command.Env = append(os.Environ(), "TMUX=")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("tmux %s: %v", strings.Join(arguments, " "), err)
	}
	return strings.TrimSpace(string(output))
}

// The shim's `cx` creates its server through this door, so a Codex chat opened
// from the shell is born by the same creator as every other door's: the
// machine's title policy, automatic-rename off, and a window named for the
// socket's engine.
func TestChatServerDoorCreatesTheShellsServerThroughTheOneCreator(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	runtime, tmuxDir := chatServerRuntime(t, true)
	socket := "cx-1800000051-1-1"
	if stderr, code := runChatServer(t, runtime, socket, "/tmp", "sleep 120"); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	socketPath := filepath.Join(tmuxDir, socket)
	for _, check := range []struct{ got, want string }{
		{readChatServer(t, socketPath, "display-message", "-p", "-t", socket, "#{window_name}"), "Codex"},
		{readChatServer(t, socketPath, "show-options", "-gv", "set-titles"), "on"},
		{readChatServer(t, socketPath, "show-options", "-gv", "set-titles-string"), pfmconfig.TmuxTitlesString},
		{readChatServer(t, socketPath, "show-window-options", "-gv", "automatic-rename"), "off"},
	} {
		if check.got != check.want {
			t.Fatalf("server reads %q, want %q", check.got, check.want)
		}
	}
}

// Fail-CLOSED on the title only: a config pfm could not read still opens the
// chat, leaves the host's terminal title alone, and says why on stderr.
func TestChatServerDoorLeavesTheTitleToTheHostOnAnUnreadableConfig(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	runtime, tmuxDir := chatServerRuntime(t, true)
	runtime.ConfigError = errors.New("boom")
	socket := "cx-1800000052-1-1"
	stderr, code := runChatServer(t, runtime, socket, "/tmp", "sleep 120")
	if code != 0 || !strings.Contains(stderr, "config unreadable") {
		t.Fatalf("code=%d stderr=%q, want an opened chat and the reason named", code, stderr)
	}
	socketPath := filepath.Join(tmuxDir, socket)
	if got := readChatServer(t, socketPath, "show-options", "-gv", "set-titles"); got != "off" {
		t.Fatalf("set-titles = %q, want the host's untouched off", got)
	}
	if got := readChatServer(t, socketPath, "show-window-options", "-gv", "automatic-rename"); got != "off" {
		t.Fatalf("automatic-rename = %q, want off regardless of the title policy", got)
	}
}

// The socket is the engine marker and a path segment: one that names no
// engine, or that would leave the tmux directory, is refused before tmux runs.
func TestChatServerDoorRefusesWhatItCannotPlace(t *testing.T) {
	runtime, _ := chatServerRuntime(t, true)
	for _, args := range [][]string{
		nil,
		{"cx-1800000053-1-1", "/tmp"},
		{"zz-1800000053-1-1", "/tmp", "sleep 1"},
		{"../cx-1800000053-1-1", "/tmp", "sleep 1"},
		{"cx-1800000053-1-1", "relative", "sleep 1"},
		{"cx-1800000053-1-1", "/tmp", ""},
	} {
		if stderr, code := runChatServer(t, runtime, args...); code != 2 || stderr == "" {
			t.Fatalf("args %q: code=%d stderr=%q, want a named refusal", args, code, stderr)
		}
	}
}

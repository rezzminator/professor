package hookentry

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"hostops/pfm/internal/config"
	"hostops/pfm/internal/paths"
)

func chatServerRuntime(t *testing.T, enabled bool) (config.Runtime, string) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "pfmcs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv(paths.EnvTmuxConf, "/dev/null")
	machine := config.Defaults(root, nil)
	machine.Tmux.Titles.Enabled = enabled
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	return config.Runtime{Config: machine, Paths: paths.Values{Home: root, TmuxDir: tmuxDir}}, tmuxDir
}

func runChatServer(t *testing.T, runtime config.Runtime, args ...string) (string, int) {
	t.Helper()
	var stderr bytes.Buffer
	code := ChatServer(args, &stderr, runtime)
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
		{readChatServer(t, socketPath, "show-options", "-gv", "set-titles-string"), config.TmuxTitlesString},
		{readChatServer(t, socketPath, "show-window-options", "-gv", "automatic-rename"), "off"},
	} {
		if check.got != check.want {
			t.Fatalf("server reads %q, want %q", check.got, check.want)
		}
	}
}

func TestChatServerDoorLeavesTheTitleToTheHostOnAnUnreadableConfig(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	runtime, tmuxDir := chatServerRuntime(t, true)
	runtime.ConfigError = errors.New("boom")
	socket := "cx-1800000052-1-1"
	stderr, code := runChatServer(t, runtime, socket, "/tmp", "sleep 120")
	if code != 0 || !strings.Contains(stderr, "config unreadable") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	socketPath := filepath.Join(tmuxDir, socket)
	if got := readChatServer(t, socketPath, "show-options", "-gv", "set-titles"); got != "off" {
		t.Fatalf("set-titles = %q, want off", got)
	}
	if got := readChatServer(t, socketPath, "show-window-options", "-gv", "automatic-rename"); got != "off" {
		t.Fatalf("automatic-rename = %q, want off", got)
	}
}

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
			t.Fatalf("args %q: code=%d stderr=%q", args, code, stderr)
		}
	}
}

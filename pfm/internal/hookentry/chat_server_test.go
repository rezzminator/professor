package hookentry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func chatServerRuntime(t *testing.T, enabled bool) (config.Runtime, string) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "pfmcs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv(paths.EnvTmuxConf, "/dev/null")
	// The placement's caller lookup scans the tmux directory: only the jail's,
	// never the host's default one, and never an ambient tmux or Herdr pane.
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	t.Setenv(paths.EnvTmuxDir, tmuxDir)
	for _, name := range []string{"TMUX", "TMUX_PANE", "HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID"} {
		t.Setenv(name, "")
	}
	machine := config.Defaults(root, nil)
	machine.Tmux.Titles.Enabled = enabled
	return config.Runtime{Config: machine, Paths: paths.Values{Home: root, TmuxDir: tmuxDir}}, tmuxDir
}

func runChatServer(t *testing.T, runtime config.Runtime, args ...string) (string, int) {
	t.Helper()
	var stderr bytes.Buffer
	code := ChatServer(args, io.Discard, &stderr, runtime, func(context.Context, []string, io.Writer, io.Writer) int {
		t.Fatal("a tmux placement reached the native birth door")
		return 1
	})
	if code == 0 && len(args) >= 3 {
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

// A cx placed in Herdr is born through chat new carrying its one typed prompt;
// a shell holding an older shim passes the prompt only inside its run string,
// which is refused, never born without it.
func TestChatServerNativeBirthCarriesTheTypedPrompt(t *testing.T) {
	const socket = "cx-1800000055-1-1"
	run := `"/h/.local/bin/pfm" internal codex-launch`
	born := []string{"new", "--engine=cx", "--name", socket, "--cwd", "/tmp", "--attach", "--"}
	for _, tc := range []struct {
		name   string
		args   []string
		code   int
		birth  []string
		stderr string
	}{
		{name: "no prompt", args: []string{socket, "/tmp", run + " "}, code: ChatServerBornNative, birth: born},
		{
			name: "typed prompt", args: []string{socket, "/tmp", run + " fix\\ it", "fix it"},
			code: ChatServerBornNative, birth: append(slices.Clone(born), "fix it"),
		},
		{
			name: "older shim's three arguments", args: []string{socket, "/tmp", run + " --resume abc"}, code: 1,
			stderr: "pfm internal chat-server: this shell's cx predates this pfm and passed --resume abc only " +
				"inside its run string; open a new shell (or run: source ~/.zshrc) and run cx again\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			var birth []string
			code := chatServerNative(context.Background(), tc.args, io.Discard, &stderr,
				func(_ context.Context, args []string, _, _ io.Writer) int {
					birth = args
					return 0
				})
			if code != tc.code || !slices.Equal(birth, tc.birth) || stderr.String() != tc.stderr {
				t.Fatalf("code=%d birth=%q stderr=%q\nwant code=%d birth=%q stderr=%q",
					code, birth, stderr.String(), tc.code, tc.birth, tc.stderr)
			}
		})
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

// The cx door asks the one placement once, as "shim.cx": the default births
// its tmux server, and a configured Herdr backend with no Herdr caller is
// refused before any server exists.
func TestChatServerDoorPlacesOnce(t *testing.T) {
	for _, tc := range []struct {
		name, backend string
		code          int
		want          string
	}{
		{name: "default", code: 0, want: "shim.cx=tmux"},
		{name: "configured Herdr outside Herdr", backend: "herdr", code: 1, want: "shim.cx="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath("tmux"); err != nil && tc.code == 0 {
				t.Skip("tmux is not installed")
			}
			for _, name := range []string{"HERDR_ENV", "HERDR_SOCKET_PATH", "HERDR_PANE_ID", "TMUX"} {
				t.Setenv(name, "")
			}
			runtime, tmuxDir := chatServerRuntime(t, true)
			runtime.Config.Chat.Backend = tc.backend
			_, recorder := obs.Test(t)
			socket := "cx-1800000054-1-1"
			stderr, code := runChatServer(t, runtime, socket, "/tmp", "sleep 120")
			var placed []string
			for _, record := range recorder.Records() {
				if record.Message == "chat.placement" {
					door, _ := record.Field("door")
					chosen, _ := record.Field("chosen")
					placed = append(placed, fmt.Sprint(door, "=", chosen))
				}
			}
			_, statErr := os.Stat(filepath.Join(tmuxDir, socket))
			if code != tc.code || !slices.Equal(placed, []string{tc.want}) || (statErr == nil) != (tc.code == 0) {
				t.Fatalf("code=%d placements=%v server=%v stderr=%q; want code %d and [%s]",
					code, placed, statErr, stderr, tc.code, tc.want)
			}
		})
	}
}

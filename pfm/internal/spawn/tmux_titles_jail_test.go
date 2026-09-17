package spawn

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/paths"
)

// pfm takes over a host-level surface when it sets the title options, so the
// takeover has to be a decision the host can make. These fixtures run a real
// tmux server — in a private socket directory, never the fleet's — and read
// the options back.

func newTitlesProbeServer(t *testing.T, socket string, titles *pfmconfig.TmuxTitles) string {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root, err := os.MkdirTemp("/tmp", "pfmtitles")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// /dev/null keeps the HOST's ~/.tmux.conf out of the assertion: it may set
	// set-titles itself, and the question here is what pfm applied.
	t.Setenv(paths.EnvTmuxConf, "/dev/null")

	tmux := TmuxSpawner{TmuxDir: tmuxDir, Titles: titles}
	if err := tmux.NewSession(context.Background(), SessionSpec{
		Socket: socket, Session: socket, Window: "Claude", CWD: root,
		Width: 180, Height: 45, Run: "sleep 120",
	}); err != nil {
		t.Fatalf("create chat server: %v", err)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", filepath.Join(tmuxDir, socket), "kill-server")
		kill.Env = append(os.Environ(), "TMUX=")
		_ = kill.Run()
	})
	return filepath.Join(tmuxDir, socket)
}

func showOption(t *testing.T, socketPath string, arguments ...string) string {
	t.Helper()
	command := exec.Command("tmux", append([]string{"-S", socketPath}, arguments...)...)
	command.Env = append(os.Environ(), "TMUX=")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("tmux %s: %v", strings.Join(arguments, " "), err)
	}
	return strings.TrimSpace(string(output))
}

func TestSpawnAppliesTheTitleOptionsWhenPfmOwnsThem(t *testing.T) {
	titles := pfmconfig.DefaultTmuxTitles()
	socketPath := newTitlesProbeServer(t, "probe-cc-1800000011-1-1", &titles)
	if got := showOption(t, socketPath, "show-options", "-g", "set-titles"); got != "set-titles on" {
		t.Fatalf("set-titles = %q, want on", got)
	}
	want := `set-titles-string "` + pfmconfig.TmuxTitlesString + `"`
	if got := showOption(t, socketPath, "show-options", "-g", "set-titles-string"); got != want {
		t.Fatalf("set-titles-string = %q, want %q", got, want)
	}
}

// The host owns the title: pfm sets NEITHER option, so whatever the host put on
// the outer pty before tmux started still stands.
func TestSpawnLeavesTheHostsTitleAloneWhenTitlesAreDisabled(t *testing.T) {
	socketPath := newTitlesProbeServer(
		t, "probe-cc-1800000012-1-1", &pfmconfig.TmuxTitles{Enabled: false},
	)
	if got := showOption(t, socketPath, "show-options", "-g", "set-titles"); got != "set-titles off" {
		t.Fatalf("set-titles = %q, want tmux's own default off", got)
	}
	got := showOption(t, socketPath, "show-options", "-g", "set-titles-string")
	if strings.Contains(got, pfmconfig.TmuxTitlesString) {
		t.Fatalf("set-titles-string = %q, want the host's own value untouched", got)
	}
	// The window name is the fleet's DNS record either way: automatic-rename
	// is never gated by the title policy.
	if got := showOption(
		t,
		socketPath,
		"show-window-options",
		"-g",
		"automatic-rename",
	); got != "automatic-rename off" {
		t.Fatalf("automatic-rename = %q, want off regardless of the title policy", got)
	}
}

// A client built without a machine config keeps today's behaviour rather than
// silently handing the title to the host.
func TestSpawnWithoutAConfigStillOwnsTheTitle(t *testing.T) {
	socketPath := newTitlesProbeServer(t, "probe-cc-1800000013-1-1", nil)
	if got := showOption(t, socketPath, "show-options", "-g", "set-titles"); got != "set-titles on" {
		t.Fatalf("set-titles = %q, want on for a nil policy", got)
	}
}

// The picker's doors create a server that a client attaches to the moment it
// exists, so they state no size and let that first client size the window.
// A zero size must reach tmux as NO -x/-y — `-x 0` is a size tmux refuses.
func TestNewSessionWithoutASizeLetsTheFirstClientSizeTheWindow(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root, err := os.MkdirTemp("/tmp", "pfmsize")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	t.Setenv(paths.EnvTmuxConf, "/dev/null")
	socket := "probe-cc-1800000014-1-1"
	tmux := TmuxSpawner{TmuxDir: tmuxDir}
	if err := tmux.NewSession(context.Background(), SessionSpec{
		Socket: socket, Session: socket, Window: "Claude", CWD: root, Run: "sleep 120",
	}); err != nil {
		t.Fatalf("create an unsized chat server: %v", err)
	}
	socketPath := filepath.Join(tmuxDir, socket)
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", socketPath, "kill-server")
		kill.Env = append(os.Environ(), "TMUX=")
		_ = kill.Run()
	})
	if got := showOption(t, socketPath, "display-message", "-p", "-t", socket, "#{window_name}"); got != "Claude" {
		t.Fatalf("window name = %q, want the name the door gave it", got)
	}
}

package spawn

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"hostops/pfm/internal/paths"
)

// A chat is a terminal the user LIVES in, so its tmux server has to load their
// ~/.tmux.conf like every other terminal on the machine. Being born with
// `-f /dev/null` is what left engine-spawned chats wearing tmux's default green
// status bar along the bottom while every shell-spawned chat wore the user's
// own bar on top — same fleet, two different-looking chats.

func TestChatServerLoadsTheConfigItIsGiven(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root, err := os.MkdirTemp("/tmp", "pfs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, "tmux.conf")
	if err := os.WriteFile(
		config,
		[]byte("set -g status-position top\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvTmuxConf, config)

	const socket = "cc-1800000009-1-1"
	tmux := TmuxSpawner{TmuxDir: tmuxDir}
	if err := tmux.NewSession(context.Background(), SessionSpec{
		Socket:  socket,
		Session: socket,
		Window:  "Claude",
		CWD:     root,
		Width:   180,
		Height:  45,
		Run:     "sleep 120",
	}); err != nil {
		t.Fatalf("create chat server: %v", err)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", filepath.Join(tmuxDir, socket), "kill-server")
		kill.Env = append(os.Environ(), "TMUX=")
		_ = kill.Run()
	})

	show := exec.Command(
		"tmux", "-S", filepath.Join(tmuxDir, socket),
		"show-options", "-g", "status-position",
	)
	show.Env = append(os.Environ(), "TMUX=")
	output, err := show.Output()
	if err != nil {
		t.Fatalf("read status-position: %v", err)
	}
	if got := string(output); got != "status-position top\n" {
		t.Fatalf("chat server ignored the config it was given: %q", got)
	}
}

func TestChatServerCreatesMissingSocketDirectoryOnFreshMachine(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root, err := os.MkdirTemp("/tmp", "pfs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	tmuxDir := filepath.Join(root, "missing", "tmux-"+strconv.Itoa(os.Getuid()))
	t.Setenv(paths.EnvTmuxConf, "/dev/null")
	const socket = "cx-1800000010-1-1"
	tmux := TmuxSpawner{TmuxDir: tmuxDir}
	if err := tmux.NewSession(context.Background(), SessionSpec{
		Socket: socket, Session: socket, Window: "Codex", CWD: root,
		Width: 180, Height: 45, Run: "sleep 120",
	}); err != nil {
		t.Fatalf("fresh-machine chat launch: %v", err)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", filepath.Join(tmuxDir, socket), "kill-server")
		kill.Env = append(os.Environ(), "TMUX=")
		_ = kill.Run()
	})
	info, err := os.Stat(tmuxDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("socket directory mode = %o, want 700", info.Mode().Perm())
	}
}

// TestTmuxConfigArgumentsDefaultsToTheUsersOwnConfig: unset means "no -f at
// all", which is what makes tmux read ~/.tmux.conf. Returning a path here
// instead would quietly re-pin every chat to one config forever.
func TestTmuxConfigArgumentsDefaultsToTheUsersOwnConfig(t *testing.T) {
	t.Setenv(paths.EnvTmuxConf, "")
	if got := paths.TmuxConfigArguments(); len(got) != 0 {
		t.Fatalf("unset config still passed %q to tmux", got)
	}
	t.Setenv(paths.EnvTmuxConf, "/dev/null")
	got := paths.TmuxConfigArguments()
	if len(got) != 2 || got[0] != "-f" || got[1] != "/dev/null" {
		t.Fatalf("pinned config = %q, want -f /dev/null", got)
	}
}

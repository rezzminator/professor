package picker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"hostops/pfm/internal/compose"
	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/store"
	"hostops/pfm/internal/ui"
)

func jailPaths(t *testing.T) paths.Values {
	t.Helper()
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatalf("resolve jail paths: %v", err)
	}
	return resolved
}

// Hide and deactive are separate contracts, exercised against real tmux
// servers on scratch sockets inside the jail.

// startJailServer brings up a real tmux server on the jail's fleet socket dir,
// addressed the way action.CommandTmux addresses one (-S <dir>/<socket>).
func startJailServer(t *testing.T, tmuxDir, socket string) {
	t.Helper()
	if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
		t.Fatalf("create jail tmux dir: %v", err)
	}
	command := exec.Command(
		"tmux", "-f", "/dev/null",
		"-S", filepath.Join(tmuxDir, socket),
		"new-session", "-d", "-s", "chat", "sleep 120",
	)
	command.Env = append(os.Environ(), "TMUX=")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start jail tmux server %q: %v: %s", socket, err, output)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", filepath.Join(tmuxDir, socket), "kill-server")
		kill.Env = append(os.Environ(), "TMUX=")
		_ = kill.Run()
	})
}

func jailServerAlive(tmuxDir, socket string) bool {
	command := exec.Command(
		"tmux", "-S", filepath.Join(tmuxDir, socket), "list-panes", "-a",
	)
	command.Env = append(os.Environ(), "TMUX=")
	return command.Run() == nil
}

func TestHidingALiveChatKeepsItRunningAndKeepsItsHandles(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	tmuxDir := filepath.Join(root, "tmux")
	sidDir := filepath.Join(root, "sid")
	const socket = "cc-1800000001-42-7"
	startJailServer(t, tmuxDir, socket)
	if !jailServerAlive(tmuxDir, socket) {
		t.Fatal("setup: jail server did not come up")
	}
	if err := os.MkdirAll(sidDir, 0o700); err != nil {
		t.Fatal(err)
	}
	crumb := filepath.Join(sidDir, socket+".%0")
	if err := os.WriteFile(crumb, []byte("/transcript.jsonl\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	resolved := jailPaths(t)
	apply, err := killApplier(context.Background(), database, pfmconfig.Runtime{Paths: resolved})
	if err != nil {
		t.Fatal(err)
	}

	const id = "11111111-1111-4111-8111-111111111111"
	if err := apply(ui.KillChange{
		ID:     id,
		Engine: "cc",
		Killed: true,
		Socket: socket,
		Live:   true,
		Name:   "DOOMED",
	}); err != nil {
		t.Fatalf("kill a live chat: %v", err)
	}

	if !jailServerAlive(tmuxDir, socket) {
		t.Fatal("hiding the chat incorrectly stopped its server")
	}
	if _, err := os.Stat(filepath.Join(tmuxDir, socket)); err != nil {
		t.Fatalf("hiding removed the live socket: %v", err)
	}
	if _, err := os.Stat(crumb); err != nil {
		t.Fatalf("hiding removed the live sid crumb: %v", err)
	}
	killed, found, err := database.Killed(context.Background(), id)
	if err != nil || !found || killed.ID != id {
		t.Fatalf("hidden state=%#v found=%t err=%v", killed, found, err)
	}
}

func TestDeactivatingALiveChatEndsItAndClearsItsHandles(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	tmuxDir := filepath.Join(root, "tmux")
	sidDir := filepath.Join(root, "sid")
	const socket = "cc-1800000004-45-10"
	startJailServer(t, tmuxDir, socket)
	if err := os.MkdirAll(sidDir, 0o700); err != nil {
		t.Fatal(err)
	}
	crumb := filepath.Join(sidDir, socket+".%0")
	if err := os.WriteFile(crumb, []byte("/transcript.jsonl\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	apply := deactivateApplier(context.Background(), jailPaths(t))
	if err := apply(compose.Row{Kind: compose.LiveClaude, Socket: socket, Name: "sleeping"}); err != nil {
		t.Fatalf("deactive live chat: %v", err)
	}
	if jailServerAlive(tmuxDir, socket) {
		t.Fatal("deactive left the chat running")
	}
	if _, err := os.Stat(filepath.Join(tmuxDir, socket)); !os.IsNotExist(err) {
		t.Fatalf("socket file outlived deactive: %v", err)
	}
	if _, err := os.Stat(crumb); !os.IsNotExist(err) {
		t.Fatalf("sid crumb outlived deactive: %v", err)
	}
}

// TestKillingAChatThatIsNotRunningKillsNothing: the kill is scoped to rows the
// picker saw as live. A resumable row is not live, so killing it touches nothing.
func TestKillingAChatThatIsNotRunningKillsNothing(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	tmuxDir := filepath.Join(root, "tmux")
	const bystander = "cc-1800000002-43-8"
	startJailServer(t, tmuxDir, bystander)

	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	apply, err := killApplier(context.Background(), database, pfmconfig.Runtime{Paths: jailPaths(t)})
	if err != nil {
		t.Fatal(err)
	}

	if err := apply(ui.KillChange{
		ID:     "22222222-2222-4222-8222-222222222222",
		Engine: "cc",
		Killed: true,
		Live:   false,
		Name:   "ARCHIVED",
	}); err != nil {
		t.Fatalf("kill a resumable chat: %v", err)
	}
	if !jailServerAlive(tmuxDir, bystander) {
		t.Fatal("killing a resumable row killed a server that was not its own")
	}
}

func TestHidingAChatDoesNotCleanUpADeadSocket(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := jailTest(t)
	tmuxDir := filepath.Join(root, "tmux")
	const socket = "cc-1800000003-44-9"
	if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	createDeadSocket(t, filepath.Join(tmuxDir, socket))

	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	apply, err := killApplier(context.Background(), database, pfmconfig.Runtime{Paths: jailPaths(t)})
	if err != nil {
		t.Fatal(err)
	}

	if err := apply(ui.KillChange{
		ID:     "33333333-3333-4333-8333-333333333333",
		Engine: "cc",
		Killed: true,
		Socket: socket,
		Live:   true,
		Name:   "ALREADY-GONE",
	}); err != nil {
		t.Fatalf("killing an already-dead chat reported a failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmuxDir, socket)); err != nil {
		t.Fatalf("hide changed process-lifecycle state: %v", err)
	}
}

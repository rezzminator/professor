package picker

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/ui"
)

func jailPaths(t *testing.T) paths.Values {
	t.Helper()
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatalf("resolve jail paths: %v", err)
	}
	return resolved
}

func TestUnkillThatClearsNothing(t *testing.T) {
	jailTest(t)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	apply, err := killApplier(context.Background(), database, pfmconfig.Runtime{Paths: jailPaths(t)})
	if err != nil {
		t.Fatal(err)
	}
	change := ui.KillChange{
		ID: "33333333-3333-4333-8333-333333333333", Engine: "cc", Killed: true, Name: "ARCHIVED",
	}
	if err := apply(change); err != nil {
		t.Fatalf("hide the chat: %v", err)
	}
	change.Killed = false
	if err := apply(change); err != nil {
		t.Fatalf("first unkill: %v", err)
	}
	if err := apply(change); err == nil ||
		!strings.Contains(err.Error(), "33333333-3333-4333-8333-333333333333 is not killed; nothing was unkilled") {
		t.Fatalf("second unkill = %v; want an explicit nothing-was-unkilled error", err)
	}
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

// TestEndingALiveChatEndsOnlyItsOwnServer: deactivate and reboot end a
// chat's tmux server only while the chat's pane is the server's sole pane. A
// second pane — another engine's chat, a split the scan never counted, a pane
// born after the scan — keeps the server, its socket and its crumb alive.
func TestEndingALiveChatEndsOnlyItsOwnServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Fatalf("tmux is required: %v", err)
	}
	const refusal = "server %s is still alive after kill-server, which tmux runs only while pane %%0 is the " +
		"server's sole pane; a server holding another pane is never ended: end refused"
	end := map[string]func(resolved paths.Values, row compose.Row) error{
		"deactivate": func(resolved paths.Values, row compose.Row) error {
			return deactivateApplier(context.Background(), resolved)(row)
		},
		"reboot": func(resolved paths.Values, row compose.Row) error {
			_, err := rebootRow(context.Background(), resolved, row, io.Discard)
			return err
		},
	}
	for _, test := range []struct {
		name    string
		door    string
		socket  string
		shared  bool
		wantErr string
	}{
		{"deactivate sole pane", "deactivate", "cc-1800000004-45-10", false, ""},
		{"deactivate shared server", "deactivate", "cc-1800000004-45-11", true, refusal},
		{"reboot sole pane", "reboot", "cc-1800000004-45-12", false, ""},
		{"reboot shared server", "reboot", "cc-1800000004-45-13", true, "reboot seat-id: " + refusal},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := jailTest(t)
			tmuxDir := filepath.Join(root, "tmux")
			sidDir := filepath.Join(root, "sid")
			startJailServer(t, tmuxDir, test.socket)
			wantPanes := "%0\n"
			if test.shared {
				split := exec.Command(
					"tmux", "-S", filepath.Join(tmuxDir, test.socket), "split-window", "-d", "-t", "%0", "sleep 120",
				)
				split.Env = append(os.Environ(), "TMUX=")
				if output, err := split.CombinedOutput(); err != nil {
					t.Fatalf("split a second pane onto %s: %v: %s", test.socket, err, output)
				}
				wantPanes = "%0\n%1\n"
			}
			if got := jailServerPanes(t, tmuxDir, test.socket); got != wantPanes {
				t.Fatalf("setup panes on %s = %q, want %q", test.socket, got, wantPanes)
			}
			if err := os.MkdirAll(sidDir, 0o700); err != nil {
				t.Fatal(err)
			}
			crumb := filepath.Join(sidDir, test.socket+".%0")
			if err := os.WriteFile(crumb, []byte("/transcript.jsonl\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := end[test.door](jailPaths(t), compose.Row{
				Kind: compose.LiveClaude, ID: "seat-id", Name: "sleeping",
				Socket: test.socket, SessionName: "chat", PaneID: "%0",
			})
			want := test.wantErr
			if want != "" {
				want = fmt.Sprintf(want, test.socket)
			}
			if got := fmt.Sprint(err); (err == nil) != (want == "") || (err != nil && got != want) {
				t.Fatalf("%s error = %q, want %q", test.door, got, want)
			}
			_, socketErr := os.Stat(filepath.Join(tmuxDir, test.socket))
			_, crumbErr := os.Stat(crumb)
			if test.shared {
				if got := jailServerPanes(t, tmuxDir, test.socket); got != wantPanes {
					t.Fatalf("panes on refused %s = %q, want %q", test.socket, got, wantPanes)
				}
				if socketErr != nil || crumbErr != nil {
					t.Fatalf("refused %s lost a handle: socket %v, crumb %v", test.door, socketErr, crumbErr)
				}
				return
			}
			if jailServerAlive(tmuxDir, test.socket) {
				t.Fatalf("%s left the chat's sole-pane server %s running", test.door, test.socket)
			}
			if !os.IsNotExist(socketErr) || !os.IsNotExist(crumbErr) {
				t.Fatalf("%s left handles: socket %v, crumb %s: %v", test.door, socketErr, crumb, crumbErr)
			}
		})
	}
}

// jailServerPanes is every pane ID on the jail server, one per line.
func jailServerPanes(t *testing.T, tmuxDir, socket string) string {
	t.Helper()
	command := exec.Command("tmux", "-S", filepath.Join(tmuxDir, socket), "list-panes", "-a", "-F", "#{pane_id}")
	command.Env = append(os.Environ(), "TMUX=")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("list panes on %s: %v", socket, err)
	}
	return string(output)
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

func TestNativeUnkillUsesExistingLedger(t *testing.T) {
	jailTest(t)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	}()
	apply, err := killApplier(context.Background(), database, pfmconfig.Runtime{Paths: jailPaths(t)})
	if err != nil {
		t.Fatal(err)
	}
	change := ui.KillChange{ID: "44444444-4444-4444-8444-444444444444", Engine: "cc", Killed: true}
	if err := apply(change); err != nil {
		t.Fatal(err)
	}
	change.Socket = "/fixture/herdr.sock"
	change.PaneID = "w6:p3"
	change.Killed = false
	if err := apply(change); err != nil {
		t.Fatalf("native unkill: %v", err)
	}
	if _, found, err := database.Killed(context.Background(), change.ID); err != nil || found {
		t.Fatalf("unkilled ledger found=%v err=%v", found, err)
	}
}

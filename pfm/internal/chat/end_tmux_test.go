package chat

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestKillServerRecordsAChatStateTransition: KillServer walks the state door
// (spec § Middleware, `state`) — live to ended, comp=state, kind=chat —
// never the socket name it closed.
func TestKillServerRecordsAChatStateTransition(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := t.TempDir()
	tmuxDir := filepath.Join(root, "tmux")
	sidDir := filepath.Join(root, "sid")
	for _, dir := range []string{tmuxDir, sidDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	socket := "cc-obs-test-1-1"
	socketPath := filepath.Join(tmuxDir, socket)
	start := exec.Command(
		"tmux", "-S", socketPath, "-f", "/dev/null",
		"new-session", "-d", "-s", "bg", "sleep", "120",
	)
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start probe server: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", socketPath, "kill-server").Run()
	})

	resolved := paths.Values{
		TmuxDir: tmuxDir,
		SIDDir:  sidDir,
		StateDB: filepath.Join(root, "pfm.db"),
	}
	ctx, recorder := obs.Test(t)
	if err := KillServer(ctx, resolved, socket, "%0"); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, record := range recorder.Records() {
		if record.Message != "state.transition" {
			continue
		}
		if kind, _ := record.Field("kind"); kind != "chat" {
			continue
		}
		if next, _ := record.Field("next"); next == "ended" {
			found = true
		}
	}
	if !found {
		t.Fatalf("KillServer() wrote no chat->ended transition: %s", recorder.Raw())
	}
}

// TestKillServerEndsOnlyASoleChatServer: KillServer ends a server only while
// the chat's pane is its sole pane; with no pane named, the server's one pane
// stands in, and a server holding a second pane survives with its handles.
func TestKillServerEndsOnlyASoleChatServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Fatalf("tmux is required: %v", err)
	}
	for _, test := range []struct {
		name, pane, wantErr string
		split               bool
	}{
		{name: "sole pane named", pane: "%0"},
		{name: "sole pane unnamed", pane: ""},
		{
			name: "shared server, pane named", pane: "%0", split: true,
			wantErr: "server cc-1800000005-46-1 is still alive after kill-server, which tmux runs only while " +
				"pane %0 is the server's sole pane; a server holding another pane is never ended: end refused",
		},
		{
			name: "shared server, pane unnamed", pane: "", split: true,
			wantErr: "server cc-1800000005-46-1 is alive with 2 panes: " +
				"no sole pane proves the server is one chat's own: end refused",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := testjail.ShortRoot(t)
			resolved := paths.Values{
				TmuxDir: filepath.Join(root, "t"),
				SIDDir:  filepath.Join(root, "s"),
				StateDB: filepath.Join(root, "pfm.db"),
			}
			for _, dir := range []string{resolved.TmuxDir, resolved.SIDDir} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			const socket = "cc-1800000005-46-1"
			socketPath := filepath.Join(resolved.TmuxDir, socket)
			tmux := func(args ...string) (string, error) {
				command := exec.Command("tmux", append([]string{"-f", "/dev/null", "-S", socketPath}, args...)...)
				command.Env = append(os.Environ(), "TMUX=")
				output, err := command.CombinedOutput()
				return string(output), err
			}
			if output, err := tmux("new-session", "-d", "-s", "chat", "sleep 120"); err != nil {
				t.Fatalf("start server %s: %v: %s", socketPath, err, output)
			}
			t.Cleanup(func() { _, _ = tmux("kill-server") })
			if test.split {
				if output, err := tmux("split-window", "-d", "-t", "%0", "sleep 120"); err != nil {
					t.Fatalf("split a second pane onto %s: %v: %s", socketPath, err, output)
				}
			}
			crumb := filepath.Join(resolved.SIDDir, socket+".%0")
			if err := os.WriteFile(crumb, []byte("/transcript.jsonl\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			// The chat's tmux seat, as RecordTmuxSeat writes it at launch.
			state := fleetdb.OpenSharedState(context.Background(), resolved)
			t.Cleanup(func() { _ = state.Close() })
			if err := state.PutSeat(context.Background(), fleetdb.Seat{
				SeatID: socket, SessionID: "sid-sole", Engine: "cc", Backend: spawn.BackendTmux,
				TmuxSocket: socketPath, State: "live",
			}); err != nil {
				t.Fatal(err)
			}

			err := KillServer(context.Background(), resolved, socket, test.pane)

			if got := fmt.Sprint(err); (err == nil) != (test.wantErr == "") || (err != nil && got != test.wantErr) {
				t.Fatalf("KillServer() error = %q, want %q", got, test.wantErr)
			}
			panes, listErr := tmux("list-panes", "-a", "-F", "#{pane_id}")
			_, socketErr := os.Stat(socketPath)
			_, crumbErr := os.Stat(crumb)
			seat, seated, seatErr := state.Seat(context.Background(), socket)
			if seatErr != nil {
				t.Fatalf("read seat %s: %v", socket, seatErr)
			}
			if test.wantErr != "" {
				if listErr != nil || panes != "%0\n%1\n" || socketErr != nil || crumbErr != nil || !seated ||
					seat.State != "live" || seat.SessionID != "sid-sole" {
					t.Fatalf("refused end lost the server: panes %q (%v), socket %v, crumb %v, seat %+v (found %v)",
						panes, listErr, socketErr, crumbErr, seat, seated)
				}
				return
			}
			if listErr == nil || !os.IsNotExist(socketErr) || !os.IsNotExist(crumbErr) || seated {
				t.Fatalf("ended server left: panes %q (%v), socket %v, crumb %s: %v, seat %+v (found %v)",
					panes, listErr, socketErr, crumb, crumbErr, seat, seated)
			}
		})
	}
}

func TestKillServerRefusesNativeSocket(t *testing.T) {
	root := t.TempDir()
	socket := filepath.Join(root, "herdr.sock")
	if err := os.WriteFile(socket, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := KillServer(
		context.Background(),
		paths.Values{TmuxDir: root, StateDB: filepath.Join(root, "state.db")},
		socket,
		"%0",
	)
	body, readErr := os.ReadFile(socket)
	if err == nil || !strings.Contains(err.Error(), "use EndResolved for a native Herdr seat") || readErr != nil ||
		string(body) != "preserve" {
		t.Fatalf("native end err=%v body=%q read=%v", err, body, readErr)
	}
}

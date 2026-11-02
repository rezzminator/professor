package kill

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// TestManagerConfirmExitFailsWhenPaneOutlivesEscalation reproduces the live
// defect: `chat kill` reported "killed ... closing pane" while the detached
// finisher's `setsid -f` wrapper exits almost instantly (it forks and
// returns), so nothing the caller can see ever confirms the pane actually
// closed. A target whose pane survives graceful close AND both escalations
// (kill-pane, kill-server of its OWN socket) must make ConfirmExit report a
// named error instead of letting the caller print success.
func TestManagerConfirmExitFailsWhenPaneOutlivesEscalation(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	tmux := &fakeTmux{existsFor: 1_000_000, panesByTTY: map[string]string{"/dev/pts/9": "%9"}} // never reports gone
	manager, err := New(database, Dependencies{
		Tmux:         tmux,
		Spawner:      &captureSpawner{},
		Now:          func() time.Time { return time.Unix(700, 0) },
		ConfirmEvery: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	target := Target{
		Engine:     pfmengine.Codex,
		ID:         "30303030-3030-4030-8030-303030303030",
		SocketPath: filepath.Join(jail.tmuxDir, "cx-outlives"),
		PaneID:     "%9",
	}
	err = manager.ConfirmExit(context.Background(), target)
	if err == nil {
		t.Fatal("ConfirmExit = nil, want an error: the pane never left tmux's pane list")
	}
	if !strings.Contains(err.Error(), "still alive") {
		t.Fatalf("ConfirmExit error = %q, want it to name the pane as still alive", err.Error())
	}
	if len(tmux.killedPanes) == 0 {
		t.Fatal("ConfirmExit never escalated to kill-pane before giving up")
	}
	if len(tmux.killedServers) == 0 {
		t.Fatal("ConfirmExit never escalated to kill-server of the chat's own socket before giving up")
	}
}

// TestManagerConfirmExitSucceedsOncePaneGone confirms a normal close never
// regresses: once the pane leaves tmux's list (no escalation needed),
// ConfirmExit returns nil and never force-kills anything.
func TestManagerConfirmExitSucceedsOncePaneGone(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	tmux := &fakeTmux{existsFor: 0}
	manager, err := New(database, Dependencies{
		Tmux:         tmux,
		Spawner:      &captureSpawner{},
		Now:          func() time.Time { return time.Unix(700, 0) },
		ConfirmEvery: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	target := Target{
		Engine:     pfmengine.Codex,
		ID:         "40404040-4040-4040-8040-404040404040",
		SocketPath: filepath.Join(jail.tmuxDir, "cx-lands"),
		PaneID:     "%2",
	}
	if err := manager.ConfirmExit(context.Background(), target); err != nil {
		t.Fatalf("ConfirmExit = %v, want nil once the pane is gone", err)
	}
	if len(tmux.killedPanes) != 0 || len(tmux.killedServers) != 0 {
		t.Fatalf("ConfirmExit escalated over an already-gone pane: panes=%v servers=%v",
			tmux.killedPanes, tmux.killedServers)
	}
}

func TestConfirmExitDoesNotKillSiblingPanes(t *testing.T) {
	jail := newKillJail(t)
	tmux := &fakeTmux{
		existsFor:   1_000_000,
		killPaneErr: errors.New("kill-pane failed"),
		panesByTTY:  map[string]string{"/dev/pts/4": "%4", "/dev/pts/5": "%5"},
	}
	manager, err := New(jail.open(t), Dependencies{Tmux: tmux, ConfirmEvery: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	err = manager.ConfirmExit(
		context.Background(),
		Target{SocketPath: filepath.Join(jail.tmuxDir, "shared"), PaneID: "%4"},
	)
	if err == nil || !strings.Contains(err.Error(), "pane %4 still alive") {
		t.Fatalf("ConfirmExit = %v, want named still alive error", err)
	}
	if len(tmux.killedServers) != 0 {
		t.Fatalf("shared server killed: %v; sibling must remain", tmux.killedServers)
	}
	if tmux.panesByTTY["/dev/pts/5"] != "%5" || len(tmux.killedPanes) != 1 ||
		tmux.killedPanes[0] != "shared\t%4" {
		t.Fatalf(
			"sibling changed or target not attempted: panes=%v kills=%v",
			tmux.panesByTTY,
			tmux.killedPanes,
		)
	}
}

func TestNewConfirmEveryTestKnob(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	for _, test := range []struct {
		name      string
		value     string
		explicit  time.Duration
		wantEvery time.Duration
		wantError bool
	}{
		{name: "unset", wantEvery: 500 * time.Millisecond},
		{name: "set", value: "10", wantEvery: 10 * time.Millisecond},
		{name: "explicit wins", value: "10", explicit: 20 * time.Millisecond, wantEvery: 20 * time.Millisecond},
		{name: "not a number", value: "abc", wantError: true},
		{name: "zero", value: "0", wantError: true},
		{name: "negative", value: "-5", wantError: true},
		{name: "fraction", value: "1.5", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("PFM_TEST_KILL_CONFIRM_EVERY_MS", test.value)
			manager, err := New(database, Dependencies{ConfirmEvery: test.explicit})
			if test.wantError {
				if err == nil || !strings.Contains(err.Error(), "PFM_TEST_KILL_CONFIRM_EVERY_MS") ||
					!strings.Contains(err.Error(), test.value) {
					t.Fatalf("New error = %v, want knob name and value %q", err, test.value)
				}
				if manager != nil {
					t.Fatalf("New manager = %v, want nil on invalid knob", manager)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if manager.confirmEvery != test.wantEvery || manager.confirmAttempts != defaultConfirmAttempts {
				t.Fatalf(
					"New confirm pacing = %s, %d attempts; want %s, %d attempts",
					manager.confirmEvery,
					manager.confirmAttempts,
					test.wantEvery,
					defaultConfirmAttempts,
				)
			}
		})
	}
}

func TestConfirmExitMovedSeatLookupFailureKeepsExplicitTmux(t *testing.T) {
	jail := newKillJail(t)
	statePath := filepath.Join(jail.root, "state.db")
	t.Setenv("PFM_STATE_DB", statePath)
	database := jail.open(t)
	ctx := context.Background()
	// A session row the pane read never selects (tmux backend) and the moved
	// read cannot scan: its host pid is text.
	if err := database.Shared().PutSeat(ctx, fleetdb.Seat{
		SeatID: "seat-f9", SessionID: "session-f9", Engine: string(pfmengine.Claude),
		Backend: "tmux", TmuxSocket: "tmux-fixture", TmuxPane: "%9", State: "live",
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := sqlitedb.OpenReadWrite(statePath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(
		ctx,
		"UPDATE seat SET seat_host_pid='unreadable' WHERE seat_id='seat-f9'",
	); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	var warnings bytes.Buffer
	tmux := &fakeTmux{}
	manager, err := New(database, Dependencies{
		Tmux:            tmux,
		Screen:          tmux,
		Spawner:         &captureSpawner{},
		Log:             &warnings,
		Refresher:       refreshFunc(func(context.Context) error { return nil }),
		Delay:           time.Nanosecond,
		PollEvery:       time.Nanosecond,
		PollAttempts:    1,
		IdleAttempts:    2,
		ConfirmEvery:    time.Nanosecond,
		ConfirmAttempts: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(jail.tmuxDir, "tmux-fixture")
	err = manager.ConfirmExit(ctx, Target{
		Engine:     pfmengine.Claude,
		ID:         "session-f9",
		SocketName: "tmux-fixture",
		SocketPath: socket,
		PaneID:     "%9",
	})
	if err != nil {
		t.Fatalf("moved-seat lookup failure broke explicit tmux confirmation: %v", err)
	}
	prefix := "pfm kill: resolve moved kill seat: read seat: "
	want := "; continuing with explicit tmux target " + socket + " %9\n"
	if got := warnings.String(); !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, want) {
		t.Fatalf("warning = %q, want %q…%q", got, prefix, want)
	}
	if tmux.existsCalls == 0 {
		t.Fatal("tmux target was never probed")
	}
}

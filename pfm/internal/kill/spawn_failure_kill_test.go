package kill

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// failingSpawner prepares cleanly, then runs spawn as its launch: the seam
// for a launch that fails after Kill wrote the kill row.
type failingSpawner struct {
	spawn func() error
}

func (failingSpawner) Prepare(ExitArgs) error { return nil }

func (spawner failingSpawner) Spawn(context.Context, ExitArgs) error { return spawner.spawn() }

// TestKillWhoseFinisherCannotStartLeavesTheChatAsItWas is the kill-exit
// path exit and kill share: a finisher that cannot start must not
// leave the chat hidden with its pane still open. A log that cannot be
// opened fails before the kill row is written; a launch that fails after it
// rolls back the row this call created and never a row that was already
// there; and a rollback that fails too names both failures and says the
// chat is hidden with its pane open.
func TestKillWhoseFinisherCannotStartLeavesTheChatAsItWas(t *testing.T) {
	const id = "71717171-7171-4171-8171-717171717171"
	launchFailed := errors.New("setsid: launch failed")
	tests := []struct {
		name       string
		priorKill  bool
		spawner    func(t *testing.T, root string, database *store.Store, cancel context.CancelFunc) ExitSpawner
		wantErr    []string
		wantHidden bool
		// closed: the spawner closed the database, so the error is the
		// whole answer and the row cannot be read back.
		closed bool
	}{
		{
			name: "the finisher log cannot be opened",
			spawner: func(t *testing.T, root string, _ *store.Store, _ context.CancelFunc) ExitSpawner {
				blocker := filepath.Join(root, "blocker")
				if err := os.WriteFile(blocker, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				return CommandSpawner{Executable: "/jail/pfm", Setsid: stubLauncher(t, root, 0), LogDir: blocker}
			},
			wantErr: []string{"create kill finisher log directory"},
		},
		{
			name: "the finisher launch fails",
			spawner: func(t *testing.T, root string, _ *store.Store, _ context.CancelFunc) ExitSpawner {
				return CommandSpawner{Executable: "/jail/pfm", Setsid: stubLauncher(t, root, 1), LogDir: root}
			},
			wantErr: []string{"start detached kill finisher with setsid"},
		},
		{
			name:      "the launch fails over a kill that was already there",
			priorKill: true,
			spawner: func(t *testing.T, root string, _ *store.Store, _ context.CancelFunc) ExitSpawner {
				return CommandSpawner{Executable: "/jail/pfm", Setsid: stubLauncher(t, root, 1), LogDir: root}
			},
			wantErr:    []string{"start detached kill finisher with setsid"},
			wantHidden: true,
		},
		{
			name: "the launch and the rollback both fail",
			spawner: func(_ *testing.T, _ string, database *store.Store, _ context.CancelFunc) ExitSpawner {
				return failingSpawner{spawn: func() error {
					return errors.Join(launchFailed, database.Close())
				}}
			},
			wantErr: []string{launchFailed.Error(), "roll back", "hidden with its pane still open"},
			closed:  true,
		},
		{
			// The MCP client cancels the call while Spawn waits on setsid:
			// the launch fails on the cancelled request, and the rollback
			// must still land.
			name: "the request is cancelled during the launch",
			spawner: func(_ *testing.T, _ string, _ *store.Store, cancel context.CancelFunc) ExitSpawner {
				return failingSpawner{spawn: func() error {
					cancel()
					return context.Canceled
				}}
			},
			wantErr: []string{context.Canceled.Error()},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			jail := newKillJail(t)
			database := jail.open(t)
			if test.priorKill {
				if err := database.Kill(context.Background(), store.Killed{ID: id, KilledAt: 100}); err != nil {
					t.Fatal(err)
				}
			}
			root := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			manager, err := New(database, Dependencies{
				Spawner: test.spawner(t, root, database, cancel),
				Now:     func() time.Time { return time.Unix(900, 0) },
			})
			if err != nil {
				t.Fatal(err)
			}
			_, killErr := manager.Kill(ctx, Request{
				ID: id, Engine: pfmengine.Claude, Exit: true, SocketName: "cc-710-1-1", PaneID: "%4",
			})
			for _, want := range test.wantErr {
				if killErr == nil || !strings.Contains(killErr.Error(), want) {
					t.Fatalf("Kill error = %v, want it to name %q", killErr, want)
				}
			}
			if test.closed {
				return
			}
			_, hidden, err := database.Killed(context.Background(), id)
			if err != nil || hidden != test.wantHidden {
				t.Fatalf("chat hidden = %v (err %v), want %v", hidden, err, test.wantHidden)
			}
		})
	}
}

func TestFailedLaunchRollbackPreservesConcurrentSuccessfulKill(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	const id = "72727272-7272-4272-8272-727272727272"
	ctx := context.Background()
	entered, release := make(chan struct{}), make(chan struct{})
	launchFailed := errors.New("launch A failed")
	first, err := New(database, Dependencies{
		Spawner: failingSpawner{spawn: func() error {
			close(entered)
			<-release
			return launchFailed
		}},
		Now: func() time.Time { return time.Unix(900, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := New(
		database,
		Dependencies{Spawner: &captureSpawner{}, Now: func() time.Time { return time.Unix(900, 0) }},
	)
	if err != nil {
		t.Fatal(err)
	}
	request := Request{ID: id, Engine: pfmengine.Claude, Exit: true, SocketName: "cc-720-1-1", PaneID: "%4"}
	done := make(chan error, 1)
	go func() { _, err := first.Kill(ctx, request); done <- err }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("A did not reach Spawn: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("A did not reach Spawn")
	}
	_, secondErr := second.Kill(ctx, request)
	close(release)
	if err := <-done; !errors.Is(err, launchFailed) {
		t.Fatalf("A Kill = %v, want launch failure", err)
	}
	if secondErr != nil {
		t.Fatalf("B Kill = %v", secondErr)
	}
	if _, hidden, err := database.Killed(ctx, id); err != nil || !hidden {
		t.Fatalf("B's successful kill disappeared after A rollback: hidden=%v err=%v", hidden, err)
	}
	if reasserted, err := database.ReassertKill(ctx, id); err != nil || !reasserted {
		t.Fatalf("B post-exit reassert = %v, %v; want true, nil", reasserted, err)
	}
	if _, hidden, err := database.Killed(ctx, id); err != nil || !hidden {
		t.Fatalf("B's kill disappeared after reassert: hidden=%v err=%v", hidden, err)
	}
}

// stubLauncher is a setsid stand-in exiting with code.
func stubLauncher(t *testing.T, root string, code int) string {
	t.Helper()
	path := filepath.Join(root, "setsid")
	script := "#!/bin/sh\nexit " + string(rune('0'+code)) + "\n"
	if err := testjail.WriteExecutable(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// gateSpawner records what the finisher launch saw: whether its context was
// alive, whether any deadline could still cancel it, and whether the commit
// section was still open.
type gateSpawner struct {
	launches    int
	ctxErr      error
	cancellable bool
	endedSeen   int
	ended       *int
	fail        error
}

func (spawner *gateSpawner) Prepare(ExitArgs) error { return nil }

func (spawner *gateSpawner) Spawn(ctx context.Context, _ ExitArgs) error {
	spawner.launches++
	spawner.ctxErr = ctx.Err()
	spawner.cancellable = ctx.Done() != nil
	spawner.endedSeen = *spawner.ended
	if spawner.ctxErr != nil {
		return spawner.ctxErr
	}
	return spawner.fail
}

// The hook deadline closes the commit gate. Once Kill opens its section the
// kill row is written under the deadline (a deadline before it commits leaves
// no row and starts nothing), then the finisher launch and any rollback run
// to the end on a context the deadline cannot cancel, and the section closes
// after the last of them; a gate that refuses leaves no row and starts nothing.
func TestKillRunsRowAndFinisherInsideOneCommitSection(t *testing.T) {
	const id = "81818181-8181-4181-8181-818181818181"
	for _, test := range []struct {
		name         string
		refuse       bool
		cancelAfter  bool
		launchFails  bool
		wantErr      string
		wantRow      bool
		wantLaunches int
	}{
		{name: "open gate", wantRow: true, wantLaunches: 1},
		{name: "refused gate", refuse: true, wantErr: "deadline", wantLaunches: 0},
		{
			name: "a deadline before the row commits writes nothing", cancelAfter: true,
			wantErr: "context canceled", wantLaunches: 0,
		},
		{name: "a failed launch rolls its row back", launchFails: true, wantErr: "launch failed", wantLaunches: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			jail := newKillJail(t)
			database := jail.open(t)
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			ended := 0
			spawner := &gateSpawner{ended: &ended}
			if test.launchFails {
				spawner.fail = errors.New("launch failed")
			}
			gated := deps.WithCommitGate(ctx, func() (func(), bool) {
				if test.refuse {
					return func() {}, false
				}
				if test.cancelAfter {
					cancel()
				}
				return func() { ended++ }, true
			})
			manager, err := New(
				database,
				Dependencies{Spawner: spawner, Now: func() time.Time { return time.Unix(700, 0) }},
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = manager.Kill(gated, Request{
				ID: id, Engine: pfmengine.Codex, Exit: true, SocketName: "gate-codex-self", PaneID: "%7",
			})
			if test.wantErr == "" && err != nil {
				t.Fatalf("Kill() = %v", err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("Kill() = %v; want an error naming %q", err, test.wantErr)
			}
			if _, found, err := database.Killed(context.Background(), id); err != nil || found != test.wantRow {
				t.Fatalf("kill row found=%t err=%v; want found=%t", found, err, test.wantRow)
			}
			if spawner.launches != test.wantLaunches {
				t.Fatalf("finisher launches = %d, want %d", spawner.launches, test.wantLaunches)
			}
			if test.wantLaunches > 0 && (spawner.ctxErr != nil || spawner.cancellable || spawner.endedSeen != 0) {
				t.Fatalf(
					"finisher launch saw ctx error %v, cancellable=%t and %d ended sections; "+
						"want a context no deadline cancels, inside the open section",
					spawner.ctxErr,
					spawner.cancellable,
					spawner.endedSeen,
				)
			}
			if wantEnded := map[bool]int{true: 0, false: 1}[test.refuse]; ended != wantEnded {
				t.Fatalf("commit sections ended = %d, want %d", ended, wantEnded)
			}
		})
	}
}

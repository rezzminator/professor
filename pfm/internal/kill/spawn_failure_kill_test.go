package kill

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
// path chat_exit and chat_kill share: a finisher that cannot start must not
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

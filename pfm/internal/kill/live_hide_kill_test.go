package kill

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/store"
)

// assertRecordedKill checks only that a kill for id landed and carries no
// prompt baseline. The store DERIVES engine from the index for an id nothing
// has indexed (lookupTarget's own comment on the shared killed store), so
// asserting an exact engine here would pin an unrelated derivation, not this
// change's own contract.
func assertRecordedKill(t *testing.T, database *store.Store, id string, killedAt int64) {
	t.Helper()
	killed, found, err := database.Killed(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("Killed(%q) found=%v err=%v", id, found, err)
	}
	if killed.KilledAt != killedAt || killed.BaselinePrompts != nil {
		t.Fatalf("Killed(%q) = %#v, want killedAt=%d and no baseline", id, killed, killedAt)
	}
}

// TestKillSpawnsTheExitFinisherWheneverTheTargetIsLive pins the defect this
// change fixes: hiding a live chat (⌃X in the picker, `pfm chat kill <id>`
// with no --exit) used to record the kill and leave the pane running,
// because Kill only ever spawned the detached exit finisher on an explicit
// request.Exit. It now spawns on request.Exit OR a resolved live address
// (manager.go's `live := target.SocketPath != "" && target.PaneID != ""`),
// so a caller that resolves a row's socket/pane through the compose pass
// (runKill's fleet.ResolveRow, the UI's toggleKilled) gets the same
// choreography as an explicit --exit — without ever having to ask for it.
func TestKillSpawnsTheExitFinisherWheneverTheTargetIsLive(t *testing.T) {
	const id = "60606060-6060-4060-8060-606060606060"

	tests := []struct {
		name       string
		exit       bool
		socketName string
		paneID     string
		wantErr    bool
		wantSpawns int
	}{
		{
			name:       "live address with no explicit --exit still spawns",
			exit:       false,
			socketName: "cc-live-hide-1-1",
			paneID:     "%9",
			wantSpawns: 1,
		},
		{
			name:       "no address and no --exit only writes the store",
			exit:       false,
			wantSpawns: 0,
		},
		{
			name:    "--exit with no address is refused before any spawn",
			exit:    true,
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			jail := newKillJail(t)
			database := jail.open(t)
			defer func() {
				if err := database.Close(); err != nil {
					t.Errorf("close database: %v", err)
				}
			}()
			spawner := &captureSpawner{}
			manager, err := New(database, Dependencies{
				Spawner: spawner,
				Now:     func() time.Time { return time.Unix(900, 0) },
			})
			if err != nil {
				t.Fatal(err)
			}

			target, err := manager.Kill(context.Background(), Request{
				ID:         id,
				Engine:     pfmengine.Claude,
				Exit:       test.exit,
				SocketName: test.socketName,
				PaneID:     test.paneID,
			})
			if test.wantErr {
				if err == nil {
					t.Fatalf("Kill() = %#v, <nil>, want the --exit-without-a-pane refusal", target)
				}
				if len(spawner.args) != 0 {
					t.Fatalf("refused kill still spawned: %#v", spawner.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("Kill() error = %v", err)
			}
			if len(spawner.args) != test.wantSpawns {
				t.Fatalf("spawns = %#v, want %d", spawner.args, test.wantSpawns)
			}
			if test.wantSpawns == 0 {
				assertRecordedKill(t, database, id, 900)
				return
			}
			got := spawner.args[0]
			wantSocketPath := filepath.Join(jail.tmuxDir, test.socketName)
			if got.ID != id ||
				got.SocketPath != wantSocketPath ||
				got.SocketName != test.socketName ||
				got.PaneID != test.paneID {
				t.Fatalf(
					"spawned ExitArgs = %#v, want id=%q socketPath=%q socketName=%q pane=%q",
					got, id, wantSocketPath, test.socketName, test.paneID,
				)
			}
			assertRecordedKill(t, database, id, 900)
		})
	}
}

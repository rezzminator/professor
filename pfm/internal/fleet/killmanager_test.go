package fleet

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/kill"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestKillTargetProjectionUsesTheProvenPane(t *testing.T) {
	identity := resolve.Identity{Backend: "tmux", SocketPath: "/jail/tmux/cx-seat", SocketName: "cx-seat", Pane: "%2"}
	row := compose.Row{Kind: compose.LiveCodex, ID: "leaf", Path: "/jail/leaf.jsonl", Socket: "cx-seat", PaneID: "%2"}
	for _, tc := range []struct {
		name      string
		rows      []compose.Row
		wantError string
	}{
		{name: "exact", rows: []compose.Row{row}},
		{name: "missing", wantError: "matched 0"},
		{name: "ambiguous", rows: []compose.Row{row, row}, wantError: "matched 2"},
		{name: "another pane", rows: []compose.Row{{Kind: compose.LiveCodex, Socket: row.Socket, PaneID: "%1"}}, wantError: "matched 0"},
		{name: "read failure", rows: []compose.Row{{Kind: compose.LiveCodex, Socket: row.Socket, PaneID: row.PaneID, BackendError: "probe failed"}}, wantError: "probe failed"},
		{name: "killed", rows: []compose.Row{{Kind: compose.LiveCodex, Socket: row.Socket, PaneID: row.PaneID, Killed: true}}, wantError: "matched 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := killTargetFromRows(tc.rows, identity)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("target=%+v err=%v", got, err)
				}
				return
			}
			if err != nil || got.ID != row.ID || got.DataPath != row.Path || got.Engine != pfmengine.Codex {
				t.Fatalf("target=%+v err=%v", got, err)
			}
		})
	}
}

func TestKillTargetProjectionPreservesAnUnindexedLineagePath(t *testing.T) {
	testjail.Fleet(t)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	const root = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const leaf = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	path := filepath.Join(t.TempDir(), "leaf.jsonl")
	if err := os.WriteFile(
		path,
		[]byte(
			`{"type":"session_meta","payload":{"id":"`+leaf+`","session_id":"`+root+`","thread_source":"user"}}`+"\n",
		),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertRollout(
		context.Background(),
		store.Rollout{ID: root, Path: "/jail/root.jsonl", UserThread: true},
	); err != nil {
		t.Fatal(err)
	}
	identity := resolve.Identity{
		Backend:    "tmux",
		Engine:     string(pfmengine.Codex),
		ID:         leaf,
		SocketPath: "/jail/cx-seat",
		SocketName: "cx-seat",
		Pane:       "%2",
	}
	rows := []compose.Row{{Kind: compose.LiveCodex, ID: leaf, Path: path, Socket: "cx-seat", PaneID: "%2"}}
	manager, err := kill.New(database, kill.Dependencies{
		Identifier: projectedKillIdentity{identity: identity}, Spawner: projectionExitSpawner{},
		ResolveTarget: func(_ context.Context, caller resolve.Identity) (kill.Target, error) {
			return killTargetFromRows(rows, caller)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	target, err := manager.Kill(context.Background(), kill.Request{Self: true})
	if err != nil || target.ID != root {
		t.Fatalf("lineage projection=%+v err=%v", target, err)
	}
}

func TestOpenKillManagerReturnsATargetReadFailure(t *testing.T) {
	testjail.Fleet(t)
	var stderr bytes.Buffer
	database, manager, code := OpenKillManager(&stderr)
	if code != 0 {
		t.Fatalf("open=%d: %s", code, &stderr)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := manager.Kill(
		context.Background(),
		kill.Request{
			Self: true,
			Identifier: projectedKillIdentity{
				identity: resolve.Identity{
					Backend:    "tmux",
					Engine:     string(pfmengine.Codex),
					ID:         "leaf",
					SocketPath: "/jail/cx-seat",
					SocketName: "cx-seat",
					Pane:       "%2",
				},
			},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "read caller target rows") {
		t.Fatalf("read failure rendered as absence: %v", err)
	}
}

type projectedKillIdentity struct{ identity resolve.Identity }

func (i projectedKillIdentity) Identify(context.Context) (resolve.Identity, error) {
	return i.identity, nil
}

type projectionExitSpawner struct{}

func (projectionExitSpawner) Prepare(kill.ExitArgs) error                { return nil }
func (projectionExitSpawner) Spawn(context.Context, kill.ExitArgs) error { return nil }

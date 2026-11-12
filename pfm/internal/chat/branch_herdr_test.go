package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/place"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A branch from a Herdr chat is a native seat in a new tab, keyed by the
// fork's own session id, its launch record and its lineage under the seat id.
func TestBranchHerdrBirthsAForkSeat(t *testing.T) {
	testjail.Fleet(t)
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	const parent, fork = "b1111111-1111-4111-8111-111111111111", "f2222222-2222-4222-8222-222222222222"
	var root []string
	socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
		if req.Method == "ping" {
			return map[string]any{"type": "pong", "protocol": 22, "version": "fixture"}, ""
		}
		if req.Method != "layout.apply" {
			return nil, "unexpected_method"
		}
		var params struct {
			Root struct {
				Command []string `json:"command"`
			} `json:"root"`
		}
		raw, _ := json.Marshal(req.Params)
		_ = json.Unmarshal(raw, &params)
		root = params.Root.Command
		return map[string]any{"layout": map[string]any{
			"workspace_id": "w6", "root": map[string]any{"type": "pane", "pane_id": "w6:p9"},
		}}, ""
	})
	choice := spawn.BackendChoice{
		Backend: "herdr", HerdrSocket: socket, HerdrPane: "w6:p3", HerdrWorkspace: "w6",
		Places: func(context.Context) (*place.Fleet, error) { return &place.Fleet{}, nil },
	}
	binary := filepath.Join(values.Home, "native-engine")
	if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	record := &fleetdb.Launch{SessionID: fork, Engine: pfmengine.Claude, Account: 1, Cache1H: true}
	plan := action.HeadlessPlan{
		Engine: pfmengine.Claude, Record: record,
		SeatRun: &fleetdb.SeatRun{Binary: binary, Args: []string{"--session-id", fork}, CWD: values.Home},
	}
	runtime := &config.Runtime{Paths: values}
	var stdout, stderr bytes.Buffer
	if _, err := BranchHerdr(context.Background(), choice, action.HeadlessPlan{Engine: pfmengine.Claude},
		runtime, "fork-one", parent, &stdout, &stderr); err == nil ||
		err.Error() != "a Herdr branch needs a Claude fork with its session id fixed before birth" {
		t.Fatalf("a fork with no fixed id: %v", err)
	}
	seat, err := BranchHerdr(context.Background(), choice, plan, runtime, "fork-one", parent, &stdout, &stderr)
	if err != nil {
		t.Fatalf("BranchHerdr: %v (stderr %q)", err, stderr.String())
	}
	if want := "Branched b1111111… as \"fork-one\" into Herdr pane w6:p9 (socket " + socket +
		") — live in pfm ls; open it with pfm chat open \"fork-one\".\n"; stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
	if seat.SessionID != fork || seat.Name != "fork-one" || seat.HerdrPane != "w6:p9" || seat.HerdrWorkspace != "w6" ||
		seat.HerdrSocket != socket {
		t.Fatalf("seat = %+v, want fork %s named fork-one in w6:p9 on %s", seat, fork, socket)
	}
	if want := []string{"pfm", "internal", "seat-host", seat.SeatID}; !reflect.DeepEqual(root, want) {
		t.Fatalf("tab root command = %q, want %q", root, want)
	}
	state := fleetdb.OpenSharedState(context.Background(), values)
	branches, err := state.BranchSeats(context.Background())
	if closeErr := state.Close(); err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	if got := branches[seat.SeatID]; got.Parent != parent || len(branches) != 1 {
		t.Fatalf("branch seats = %+v, want %s → %s", branches, seat.SeatID, parent)
	}
	launches, err := fleetdb.OpenLaunches(context.Background(), values)
	if err != nil {
		t.Fatal(err)
	}
	launch, err := launches.LaunchFor(context.Background(), fork)
	if closeErr := launches.Close(); err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	if launch.Account != 1 || !launch.Cache1H || launch.Engine != pfmengine.Claude {
		t.Fatalf("launch record = %+v, want account 1 with the 1h cache", launch)
	}
}

// A branch is placed before any host is made: in Herdr while a Herdr server
// runs, with the Claude fork's session id fixed there; a Codex fork, which
// takes no fixed id, is refused there; with no Herdr server it is tmux's.
func TestBranchPlace(t *testing.T) {
	refusal := "host refused at cli.branch: a cx branch is not born in Herdr: only Claude fixes a fork's " +
		"session id before birth; set chat.backend to tmux to branch it in tmux"
	for _, tc := range []struct {
		name, backend, err string
		engine             pfmengine.ID
		up                 error
		fixed              bool
	}{
		{name: "Claude, Herdr running", engine: pfmengine.Claude, backend: "herdr", fixed: true},
		{name: "Codex, Herdr running", engine: pfmengine.Codex, err: refusal},
		{name: "Codex, Herdr down", engine: pfmengine.Codex, up: herdr.ErrServerDown, backend: "tmux"},
		{name: "Claude, Herdr down", engine: pfmengine.Claude, up: herdr.ErrServerDown, backend: "tmux"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := WithCaller(context.Background(), "cli.branch", resolve.Identity{}, resolve.ErrNotInChat)
			ctx = WithHerdrServer(ctx, spawn.HerdrServer{
				Socket: "/env/herdr.sock", Up: func(context.Context, string) error { return tc.up },
			})
			choice, forkID, err := BranchPlace(ctx, "auto", tc.engine)
			if tc.err != "" {
				if err == nil || err.Error() != tc.err || spawn.FailureCode(err) != 10 {
					t.Fatalf("BranchPlace error = %v, want %q with exit 10", err, tc.err)
				}
				return
			}
			if err != nil || choice.Backend != tc.backend || (len(forkID) == 36) != tc.fixed {
				t.Fatalf("BranchPlace = %+v, fork id %q, %v; want %s, fixed id %v",
					choice, forkID, err, tc.backend, tc.fixed)
			}
		})
	}
}

package kill

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// killHerdr is a throwaway socket, never an installed Herdr or a real engine.
type killHerdr struct {
	t           *testing.T
	mu          sync.Mutex
	socket      string
	frame       string
	styledFrame string
	processes   any
	probeError  string
	closeError  string
	gone        bool
	exitGone    bool
	closed      bool
	events      []string
	onRead      func()
	processType string
}

func newKillHerdr(t *testing.T) *killHerdr {
	t.Helper()
	server := &killHerdr{
		t: t, socket: filepath.Join(t.TempDir(), "s"), frame: idleFrame, processType: "pane_process_info",
		processes: []any{map[string]any{"pid": 7, "name": pfmengine.MustLookup(pfmengine.Claude).Binary}},
	}
	listener, err := net.Listen("unix", server.socket)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			server.serve(conn)
		}
	}()
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close fixture: %v", err)
		}
		<-done
	})
	return server
}

func (server *killHerdr) serve(conn net.Conn) {
	defer func() {
		if err := conn.Close(); err != nil {
			server.t.Errorf("close fixture connection: %v", err)
		}
	}()
	var request struct {
		ID     string         `json:"id"`
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&request); err != nil {
		server.t.Errorf("decode fixture request: %v", err)
		return
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	server.events = append(server.events, request.Method+" "+fmt.Sprint(request.Params))
	result := any(map[string]any{"type": "ok"})
	code := ""
	if request.Params["pane_id"] != "w1:p1" {
		code = "wrong_target"
	}
	switch request.Method {
	case "pane.get":
		result = map[string]any{"type": "pane_info", "pane": map[string]any{"pane_id": "w1:p1", "workspace_id": "w1"}}
		if server.gone || server.closed {
			code = "pane_not_found"
		} else if server.probeError != "" {
			code = server.probeError
		}
	case "pane.process_info":
		result = map[string]any{
			"type":         server.processType,
			"process_info": map[string]any{"pane_id": "w1:p1", "foreground_processes": server.processes},
		}
	case "pane.read":
		if server.onRead != nil {
			server.onRead()
			server.onRead = nil
		}
		text, format := strings.NewReplacer("\x1b[2m", "", "\x1b[0m", "").Replace(server.frame), "text"
		if request.Params["format"] == "ansi" {
			text, format = server.frame, "ansi"
			if server.styledFrame != "" {
				text = server.styledFrame
			}
		}
		if request.Params["source"] != "visible" {
			server.t.Errorf("kill composer starts history collection: %+v", request.Params)
		}
		result = map[string]any{
			"type": "pane_read",
			"read": map[string]any{
				"pane_id":      "p1",
				"workspace_id": "w1",
				"text":         text,
				"truncated":    false,
				"format":       format,
			},
		}
	case "pane.send_text":
		if request.Params["text"] != "/exit" {
			code = "wrong_text"
		}
	case "pane.send_keys":
		if server.exitGone {
			server.processes = []any{map[string]any{"pid": 6, "argv0": "pfm"}}
		}
	case "pane.close":
		if server.closeError != "" {
			code = server.closeError
		} else {
			server.closed = true
		}
	default:
		code = "unsupported_method"
	}
	response := map[string]any{"id": request.ID, "ok": code == "", "result": result}
	if code != "" {
		response["error"] = map[string]string{"code": code, "message": "fixture refusal"}
		delete(response, "result")
	}
	if err := json.NewEncoder(conn).Encode(response); err != nil {
		server.t.Errorf("encode fixture response: %v", err)
	}
}

func seedKillHerdr(t *testing.T, database *store.Store, server *killHerdr) fleetdb.Seat {
	t.Helper()
	seat := fleetdb.Seat{
		SeatID: "seat-kill", SessionID: "native-session", Engine: string(pfmengine.Claude),
		Backend: "herdr", HerdrSocket: server.socket, HerdrPane: "w1:p1", State: "live",
	}
	if err := database.Shared().PutSeat(context.Background(), seat); err != nil {
		t.Fatal(err)
	}
	return seat
}

func TestHerdrKillRoutesByDurableSeat(t *testing.T) {
	for _, selector := range []string{"address", "session", "mismatch", "unowned", "partial"} {
		t.Run(selector, func(t *testing.T) {
			jail := newKillJail(t)
			database := jail.open(t)
			server := newKillHerdr(t)
			seat := seedKillHerdr(t, database, server)
			spawner := &captureSpawner{}
			manager, err := New(database, Dependencies{Tmux: &fakeTmux{}, Spawner: spawner})
			if err != nil {
				t.Fatal(err)
			}
			request := Request{ID: seat.SessionID, Engine: pfmengine.ID(seat.Engine), Exit: true}
			if selector != "session" {
				request.SocketName, request.PaneID = seat.HerdrSocket, seat.HerdrPane
			}
			if selector == "mismatch" {
				request.ID = "another-session"
			}
			if selector == "partial" {
				request.SocketName = ""
			}
			if selector == "unowned" {
				request.PaneID = "w1:p2"
			}
			target, err := manager.Kill(context.Background(), request)
			if selector == "mismatch" || selector == "unowned" || selector == "partial" {
				if err == nil || len(spawner.args) != 0 {
					t.Fatalf("unsafe target accepted: target=%+v err=%v spawns=%v", target, err, spawner.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("durable Herdr kill: %v", err)
			}
			if target.SocketPath != seat.HerdrSocket || target.PaneID != seat.HerdrPane ||
				target.ID != seat.SessionID ||
				len(spawner.args) != 1 {
				t.Fatalf("native target/spawn = %+v / %+v", target, spawner.args)
			}
			log, err := (CommandSpawner{LogDir: jail.sidDir}).openLog(ExitArgs(target))
			if err != nil {
				t.Fatalf("native detached log: %v", err)
			}
			if err := log.Close(); err != nil {
				t.Fatal(err)
			}
			if row, found, err := database.Killed(
				context.Background(),
				seat.SessionID,
			); err != nil || !found ||
				row.ID != seat.SessionID {
				t.Fatalf("kill record=%+v found=%t err=%v", row, found, err)
			}
		})
	}
}

func TestHerdrFinisherReusesComposerRatchet(t *testing.T) {
	for _, test := range []struct {
		name, frame, styled string
		exitGone, wantExit  bool
	}{
		{"empty", idleFrame, "", true, true},
		{"dim hint", strings.Replace(idleFrame, "❯ ", "❯ \x1b[2mTry running tests\x1b[0m", 1), "", true, true},
		{"same text draft", strings.Replace(idleFrame, "❯ ", "❯ Try running tests", 1), "", false, false},
		{"draft", strings.Replace(idleFrame, "❯ ", "❯ unsent draft", 1), "", false, false},
		{"busy", busyFrame, "", false, false},
		{"missing composer", "unknown terminal content", "", false, false},
		{"last resort", idleFrame, "", false, true},
		{"styled busy", strings.Replace(idleFrame, "❯ ", "❯ \x1b[2mTry running tests\x1b[0m", 1), strings.Replace(idleFrame, "❯ ", "❯ \x1b[2mTry running tests\x1b[0m", 1) + "\n" + busyFrame, false, false},
		{"styled continuation", strings.Replace(idleFrame, "❯ ", "❯ \x1b[2mTry running tests\x1b[0m", 1), strings.Replace(strings.Replace(idleFrame, "❯ ", "❯ \x1b[2mTry running tests\x1b[0m", 1), "\x1b[0m\n", "\x1b[0m\nprivate draft\n", 1), false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			jail := newKillJail(t)
			database := jail.open(t)
			server := newKillHerdr(t)
			server.frame, server.styledFrame, server.exitGone = test.frame, test.styled, test.exitGone
			seat := seedKillHerdr(t, database, server)
			recipe := fleetdb.SeatRun{Binary: "fixture", Args: []string{"fixture"}}
			if err := database.Shared().WriteSeatRun(seat.SeatID, recipe); err != nil {
				t.Fatal(err)
			}
			tmux := &fakeTmux{}
			finisher, err := NewFinisher(
				database,
				Dependencies{
					Tmux:         tmux,
					Refresher:    refreshFunc(func(context.Context) error { return nil }),
					Delay:        time.Nanosecond,
					PollEvery:    time.Nanosecond,
					PollAttempts: 2,
					IdleAttempts: 2,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			args := ExitArgs{
				Engine:     pfmengine.ID(seat.Engine),
				ID:         seat.SessionID,
				SocketName: seat.SeatID,
				SocketPath: seat.HerdrSocket,
				PaneID:     seat.HerdrPane,
			}
			if err := finisher.Run(context.Background(), args); err != nil {
				t.Fatalf("native finisher: %v", err)
			}
			server.mu.Lock()
			events := append([]string(nil), server.events...)
			closed := server.closed
			server.mu.Unlock()
			joined := strings.Join(events, "\n")
			typed := strings.Contains(joined, "pane.send_text ")
			if typed != test.wantExit {
				t.Fatalf("exit typed=%t want=%t events=%s", typed, test.wantExit, joined)
			}
			if test.wantExit && (!strings.Contains(joined, "text:/exit") || !strings.Contains(joined, "keys:[Enter]")) {
				t.Fatalf("exit not text + Enter: %s", joined)
			}
			if test.exitGone == closed {
				t.Fatalf("close=%t graceful=%t events=%s", closed, test.exitGone, joined)
			}
			if len(tmux.sent)+len(tmux.killedPanes)+len(tmux.killedServers) != 0 {
				t.Fatalf("native kill reached tmux: %+v", tmux)
			}
			got, err := database.Shared().ReadSeatRun(seat.SeatID)
			if !closed {
				if err != nil || !reflect.DeepEqual(got.Args, recipe.Args) {
					t.Fatalf("graceful recipe changed before supervisor cleanup: %+v %v", got, err)
				}
				return
			}
			if _, found, err := database.Shared().Seat(context.Background(), seat.SeatID); err != nil || found {
				t.Fatalf("closed native pane kept a live durable seat: found=%t err=%v", found, err)
			}
			if err == nil {
				t.Fatal("closed native pane kept its run recipe")
			}
		})
	}
}

func TestHerdrConfirmDistinguishesAbsenceFromProbeFailure(t *testing.T) {
	for _, test := range []struct {
		name                 string
		missingType          bool
		processes            any
		gone                 bool
		probeError           string
		wantError, wantClose bool
	}{
		{name: "pane gone", gone: true},
		{name: "supervisor only", processes: []any{map[string]any{"pid": 7, "argv0": "pfm"}}},
		{name: "name only supervisor", processes: []any{map[string]any{"pid": 7, "name": "pfm"}}},
		{name: "empty list", processes: []any{}},
		{name: "engine argv0", processes: []any{map[string]any{"pid": 7, "argv0": "/opt/bin/" + pfmengine.MustLookup(pfmengine.Claude).Binary}}, wantClose: true},
		{name: "engine name", processes: []any{map[string]any{"pid": 7, "name": pfmengine.MustLookup(pfmengine.Claude).Binary}}, wantClose: true},
		{name: "missing process type", missingType: true, processes: []any{}, wantError: true, wantClose: true},
		{name: "null list", wantError: true, wantClose: true},
		{name: "unknown process", processes: []any{map[string]any{"pid": 7}}, wantError: true, wantClose: true},
		{name: "wrong list shape", processes: "unknown", wantError: true, wantClose: true},
		{name: "refused probe", probeError: "permission_denied", wantError: true, wantClose: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			jail := newKillJail(t)
			database := jail.open(t)
			server := newKillHerdr(t)
			server.processes, server.gone, server.probeError = test.processes, test.gone, test.probeError
			if test.missingType {
				server.processType = ""
			}
			if test.wantError {
				server.closeError = "permission_denied"
			}
			seat := seedKillHerdr(t, database, server)
			tmux := &fakeTmux{}
			manager, err := New(database, Dependencies{Tmux: tmux, ConfirmEvery: time.Nanosecond, ConfirmAttempts: 1})
			if err != nil {
				t.Fatal(err)
			}
			err = manager.ConfirmExit(
				context.Background(),
				Target{
					Engine:     pfmengine.ID(seat.Engine),
					ID:         seat.SessionID,
					SocketPath: seat.HerdrSocket,
					PaneID:     seat.HerdrPane,
				},
			)
			if (err != nil) != test.wantError {
				t.Fatalf("ConfirmExit=%v wantError=%t", err, test.wantError)
			}
			server.mu.Lock()
			events := strings.Join(server.events, "\n")
			server.mu.Unlock()
			if strings.Contains(events, "pane.close ") != test.wantClose {
				t.Fatalf("close want=%t events=%s", test.wantClose, events)
			}
			if len(tmux.killedPanes)+len(tmux.killedServers) != 0 {
				t.Fatalf("native escalation reached tmux: %+v", tmux)
			}
		})
	}
}

func TestHerdrConfirmUnreachableIsNotGone(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	server := newKillHerdr(t)
	seat := seedKillHerdr(t, database, server)
	if err := os.Remove(server.socket); err != nil {
		t.Fatal(err)
	}
	manager, err := New(database, Dependencies{Tmux: &fakeTmux{}, ConfirmEvery: time.Nanosecond, ConfirmAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	err = manager.ConfirmExit(
		context.Background(),
		Target{
			Engine:     pfmengine.ID(seat.Engine),
			ID:         seat.SessionID,
			SocketPath: seat.HerdrSocket,
			PaneID:     seat.HerdrPane,
		},
	)
	if err == nil || !strings.Contains(err.Error(), "still alive") {
		t.Fatalf("unreachable native socket treated gone: %v", err)
	}
}

func TestHerdrFinisherProtectsGoneOrReownedPane(t *testing.T) {
	for _, state := range []string{"engine absent", "pane absent", "reowned"} {
		t.Run(state, func(t *testing.T) {
			jail := newKillJail(t)
			database := jail.open(t)
			server := newKillHerdr(t)
			seat := seedKillHerdr(t, database, server)
			switch state {
			case "engine absent":
				server.processes = []any{map[string]any{"pid": 7, "name": "pfm"}}
			case "pane absent":
				server.gone = true
			case "reowned":
				server.onRead = func() {
					replacement := seat
					replacement.SessionID = "another-session"
					if err := database.Shared().PutSeat(context.Background(), replacement); err != nil {
						t.Error(err)
					}
				}
			}
			tmux := &fakeTmux{}
			finisher, err := NewFinisher(
				database,
				Dependencies{
					Tmux:         tmux,
					Refresher:    refreshFunc(func(context.Context) error { return nil }),
					Delay:        time.Nanosecond,
					PollEvery:    time.Nanosecond,
					PollAttempts: 1,
					IdleAttempts: 2,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			err = finisher.Run(
				context.Background(),
				ExitArgs{
					Engine:     pfmengine.ID(seat.Engine),
					ID:         seat.SessionID,
					SocketName: seat.SeatID,
					SocketPath: seat.HerdrSocket,
					PaneID:     seat.HerdrPane,
				},
			)
			if (err != nil) != (state == "reowned") {
				t.Fatalf("finisher=%v state=%s", err, state)
			}
			server.mu.Lock()
			events := strings.Join(server.events, "\n")
			server.mu.Unlock()
			if strings.Contains(events, "pane.close ") || strings.Contains(events, "pane.send_text ") {
				t.Fatalf("mutated gone/reowned pane: %s", events)
			}
		})
	}
}

// F9: the optional native seat lookup cannot strand an explicitly resolved
// tmux target when shared state is degraded; the failure must remain visible.
func TestHerdrDegradedSeatStoreKeepsExplicitTmux(t *testing.T) {
	for _, operation := range []string{"confirm tmux", "finish tmux", "kill tmux", "native", "ambiguous", "outside tmux dir", "tmux probe failure"} {
		t.Run(operation, func(t *testing.T) {
			jail := newKillJail(t)
			statePath := filepath.Join(jail.root, "broken-state.db")
			t.Setenv("PFM_STATE_DB", statePath)
			bootstrap, err := store.Open()
			if err != nil {
				t.Fatal(err)
			}
			if err := bootstrap.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(statePath, []byte("not a sqlite database"), 0o600); err != nil {
				t.Fatal(err)
			}
			var warnings bytes.Buffer
			database, err := store.Open(store.WithWarningWriter(&warnings))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := database.Close(); err != nil {
					t.Errorf("close degraded store: %v", err)
				}
			})
			degraded := database.SharedDegraded()
			if degraded == nil {
				t.Fatal("fixture did not degrade the shared seat database")
			}
			warnings.Reset() // Opening already warns; this assertion checks the kill boundary's warning.
			tmux := &fakeTmux{}
			args := ExitArgs{
				Engine:     pfmengine.Claude,
				ID:         "session-f9",
				SocketName: "tmux-fixture",
				SocketPath: filepath.Join(jail.tmuxDir, "tmux-fixture"),
				PaneID:     "%9",
			}
			switch operation {
			case "native":
				args.SocketPath, args.PaneID = filepath.Join(jail.root, "herdr.sock"), "w1:p1"
			case "ambiguous":
				args.PaneID = "unknown"
			case "outside tmux dir":
				args.SocketPath = filepath.Join(jail.root, "tmux-fixture")
			case "tmux probe failure":
				tmux.existsErrFor = 100
				tmux.killPaneErr = fmt.Errorf("fixture tmux unavailable")
			}
			spawner := &captureSpawner{}
			dependencies := Dependencies{
				Tmux:            tmux,
				Spawner:         spawner,
				Log:             &warnings,
				Refresher:       refreshFunc(func(context.Context) error { return nil }),
				Delay:           time.Nanosecond,
				PollEvery:       time.Nanosecond,
				PollAttempts:    1,
				IdleAttempts:    2,
				ConfirmEvery:    time.Nanosecond,
				ConfirmAttempts: 1,
			}
			if operation == "finish tmux" {
				finisher, constructErr := NewFinisher(database, dependencies)
				if constructErr != nil {
					t.Fatal(constructErr)
				}
				err = finisher.Run(context.Background(), args)
			} else {
				manager, constructErr := New(database, dependencies)
				if constructErr != nil {
					t.Fatal(constructErr)
				}
				if operation == "kill tmux" {
					_, err = manager.Kill(
						context.Background(),
						Request{Engine: args.Engine, ID: args.ID, SocketName: args.SocketName, PaneID: args.PaneID},
					)
				} else {
					err = manager.ConfirmExit(context.Background(), Target(args))
				}
			}
			switch operation {
			case "confirm tmux", "finish tmux", "kill tmux":
				switch operation {
				case "confirm tmux":
					if err != nil {
						t.Fatalf("degraded seat lookup broke explicit tmux confirmation: %v", err)
					}
				case "finish tmux":
					if err == nil || !strings.Contains(err.Error(), "record post-exit kill: reassert shared kill") {
						t.Fatalf("finisher lost baseline durable-write error: %v", err)
					}
				case "kill tmux":
					if err == nil || !strings.Contains(err.Error(), "record shared kill") {
						t.Fatalf("kill lost baseline durable-write error: %v", err)
					}
				}
				if !strings.Contains(warnings.String(), "continuing with explicit tmux target") ||
					!strings.Contains(warnings.String(), degraded.Error()) {
					t.Fatalf("seat lookup failure was not visibly warned with cause: %q", warnings.String())
				}
				if operation == "kill tmux" {
					if len(spawner.args) != 0 {
						t.Fatalf("finisher spawned despite rejected durable kill write: %+v", spawner.args)
					}
				} else if tmux.existsCalls == 0 {
					t.Fatal("tmux target was never probed")
				}
				if operation == "finish tmux" &&
					(len(tmux.sent) != 1 || tmux.sent[0] != "/exit" || len(tmux.killedPanes) != 1) {
					t.Fatalf("tmux ratchet changed: sent=%v closed=%v", tmux.sent, tmux.killedPanes)
				}
			case "tmux probe failure":
				if err == nil || !strings.Contains(err.Error(), "tmux probe failed") {
					t.Fatalf("failed tmux probe treated as absence: %v", err)
				}
				if !strings.Contains(warnings.String(), degraded.Error()) {
					t.Fatalf("degradation warning missing: %q", warnings.String())
				}
			default:
				if err == nil || !strings.Contains(err.Error(), degraded.Error()) {
					t.Fatalf("native/ambiguous target lost the store failure: %v", err)
				}
				if tmux.existsCalls != 0 || len(tmux.killedPanes) != 0 || warnings.Len() != 0 {
					t.Fatalf(
						"native/ambiguous target fell back to tmux: probes=%d kills=%v warnings=%q",
						tmux.existsCalls,
						tmux.killedPanes,
						warnings.String(),
					)
				}
			}
		})
	}
}

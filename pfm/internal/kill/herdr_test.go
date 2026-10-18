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
	"strings"
	"sync"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// killHerdr is a throwaway socket, never an installed Herdr or a real engine.
type killHerdr struct {
	t             *testing.T
	mu            sync.Mutex
	socket        string
	frame         string
	styledFrame   string
	processes     any
	probeError    string
	closeError    string
	gone          bool
	exitGone      bool
	closed        bool
	events        []string
	onRead        func()
	onExit        func()
	processType   string
	omitProcesses bool
	pane          string
	terminal      string
	proc          string
}

func newKillHerdr(t *testing.T) *killHerdr {
	t.Helper()
	proc := t.TempDir()
	t.Setenv(paths.EnvProcRoot, proc)
	for name, body := range map[string]string{"stat": "btime 1000000000\n", "90/stat": "90 (pfm) S 1 " + strings.Repeat("0 ", 17) + "9 0\n", "90/cmdline": "pfm\x00internal\x00seat-host\x00seat-kill\x00"} {
		p := filepath.Join(proc, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	server := &killHerdr{
		t:           t,
		pane:        "w1:p1",
		terminal:    "kill-terminal",
		proc:        proc,
		socket:      filepath.Join(t.TempDir(), "s"),
		frame:       idleFrame,
		processType: "pane_process_info",
		processes:   []any{map[string]any{"pid": 7, "name": pfmengine.MustLookup(pfmengine.Claude).Binary}},
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
	if request.Method != "pane.list" && request.Params["pane_id"] != server.pane &&
		(request.Method != "pane.get" || request.Params["pane_id"] != "w1:p1") {
		code = "wrong_target"
	}
	switch request.Method {
	case "pane.list":
		info := testjail.HerdrPaneInfo(server.pane)["pane"].(map[string]any)
		info["terminal_id"] = server.terminal
		result = map[string]any{"type": "pane_list", "panes": []any{info}}
		if server.gone || server.closed {
			result = map[string]any{"type": "pane_list", "panes": []any{}}
		}
		if server.probeError != "" {
			code = server.probeError
		}
	case "pane.get":
		info := testjail.HerdrPaneInfo(server.pane)
		info["pane"].(map[string]any)["terminal_id"] = server.terminal
		result = info
		if server.gone || server.closed {
			code = "pane_not_found"
		} else if server.probeError != "" {
			code = server.probeError
		}
	case "pane.process_info":
		info := map[string]any{"pane_id": server.pane, "shell_pid": 90}
		if !server.omitProcesses {
			info["foreground_processes"] = server.processes
		}
		result = map[string]any{"type": server.processType, "process_info": info}
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
				"pane_id":      server.pane,
				"workspace_id": strings.Split(server.pane, ":")[0],
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
			// Successful engine exit runs the independent seat closer.
			server.gone = true
			if err := os.RemoveAll(filepath.Join(server.proc, "90")); err != nil {
				server.t.Error(err)
			}
			if server.onExit != nil {
				server.onExit()
			}
		}
	case "pane.close":
		if server.closeError != "" {
			code = server.closeError
		} else {
			server.closed = true
			if err := os.RemoveAll(filepath.Join(server.proc, "90")); err != nil {
				server.t.Error(err)
			}
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
		HerdrWorkspace: "w1", HerdrTerminalID: "kill-terminal", SeatHostPID: 90, SeatHostStart: 9,
	}
	if err := database.Shared().PutSeat(context.Background(), seat); err != nil {
		t.Fatal(err)
	}
	if server.gone {
		if err := os.RemoveAll(filepath.Join(server.proc, "90")); err != nil {
			t.Fatal(err)
		}
	}
	return seat
}

func TestHerdrKillRoutesByDurableSeat(t *testing.T) {
	for _, selector := range []string{"address", "session", "mismatch", "unowned", "partial", "moved-canonical", "moved-alias"} {
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
			if selector == "moved-canonical" {
				server.pane = "w9:p2"
				request.PaneID = server.pane
			}
			if selector == "moved-alias" {
				server.pane = "w9:p2"
				next := seat
				next.HerdrPane = server.pane
				next.HerdrWorkspace = "w9"
				if err := database.Shared().UpdateSeat(context.Background(), seat, next); err != nil {
					t.Fatal(err)
				}
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
			if target.SocketPath != seat.HerdrSocket || target.PaneID != server.pane ||
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
			server.onExit = func() {
				if err := database.Shared().DeleteSeatRun(seat.SeatID); err != nil {
					t.Error(err)
				}
				if err := database.Shared().DeleteSeat(context.Background(), seat.SeatID); err != nil {
					t.Error(err)
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
			_, err = database.Shared().ReadSeatRun(seat.SeatID)
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
		omitProcesses        bool
		processes            any
		gone                 bool
		probeError           string
		wantError, wantClose bool
	}{
		{name: "pane gone", gone: true, wantError: true},
		{name: "supervisor only", wantClose: true, wantError: true, processes: []any{map[string]any{"pid": 7, "argv0": "pfm"}}},
		{name: "name only supervisor", wantClose: true, wantError: true, processes: []any{map[string]any{"pid": 7, "name": "pfm"}}},
		{name: "empty list", wantClose: true, processes: []any{}, wantError: true},
		{name: "omitted list", wantClose: true, omitProcesses: true, wantError: true},
		{name: "engine argv0", processes: []any{map[string]any{"pid": 7, "argv0": "/opt/bin/" + pfmengine.MustLookup(pfmengine.Claude).Binary}}, wantClose: true},
		{name: "engine name", processes: []any{map[string]any{"pid": 7, "name": pfmengine.MustLookup(pfmengine.Claude).Binary}}, wantClose: true},
		{name: "missing process type", missingType: true, processes: []any{}, wantError: true},
		{name: "null list", wantClose: true, wantError: true},
		{name: "unknown process", wantClose: true, processes: []any{map[string]any{"pid": 7}}, wantError: true},
		{name: "wrong list shape", wantClose: true, processes: "unknown", wantError: true},
		{name: "refused probe", probeError: "permission_denied", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			jail := newKillJail(t)
			database := jail.open(t)
			server := newKillHerdr(t)
			server.processes, server.gone, server.probeError = test.processes, test.gone, test.probeError
			server.omitProcesses = test.omitProcesses
			if test.missingType {
				server.processType = ""
			}
			if test.wantError {
				server.closeError = "permission_denied"
			}
			seat := seedKillHerdr(t, database, server)
			if err := database.Shared().WriteSeatRun(
				seat.SeatID, fleetdb.SeatRun{Binary: "/fixture/claude", CWD: jail.root},
			); err != nil {
				t.Fatal(err)
			}
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
			if test.wantError {
				current, found, readErr := database.Shared().Seat(context.Background(), seat.SeatID)
				if readErr != nil || !found || current.SeatID != seat.SeatID {
					t.Fatalf("unknown owner lost: %+v found=%t err=%v", current, found, readErr)
				}
				if _, err := database.Shared().ReadSeatRun(seat.SeatID); err != nil {
					t.Fatalf("unknown recipe lost: %v", err)
				}
				if test.wantClose && !strings.Contains(err.Error(), "permission_denied") {
					t.Fatalf("supervisor-proved close refusal hidden: %v", err)
				}
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
	for _, state := range []string{"engine absent", "pane absent", "reowned", "host died"} {
		t.Run(state, func(t *testing.T) {
			jail := newKillJail(t)
			database := jail.open(t)
			server := newKillHerdr(t)
			seat := seedKillHerdr(t, database, server)
			switch state {
			case "host died":
				server.onRead = func() {
					if err := os.RemoveAll(filepath.Join(server.proc, "90")); err != nil {
						t.Error(err)
					}
				}
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
			if (err != nil) != (state == "reowned" || state == "pane absent" || state == "host died") {
				t.Fatalf("finisher=%v state=%s", err, state)
			}
			server.mu.Lock()
			events := strings.Join(server.events, "\n")
			server.mu.Unlock()
			if strings.Contains(events, "pane.close ") != (state == "engine absent") ||
				strings.Contains(events, "pane.send_text ") {
				t.Fatalf("supervisor close crossed gone/reowned authority: %s", events)
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

func TestHerdrKillRequiresCanonicalSupervisor(t *testing.T) {
	for _, mode := range []string{"recycled", "unknown-host", "moved", "dead-host", "move-before-ack", "retry-prompt"} {
		t.Run(mode, func(t *testing.T) {
			jail := newKillJail(t)
			db := jail.open(t)
			server := newKillHerdr(t)
			seat := seedKillHerdr(t, db, server)
			if err := db.Shared().WriteSeatRun(seat.SeatID, fleetdb.SeatRun{Binary: "/fixture/engine"}); err != nil {
				t.Fatal(err)
			}
			if mode == "retry-prompt" {
				server.processes = []any{}
			}
			if mode == "recycled" {
				server.terminal = "unrelated-terminal"
			}
			if mode == "unknown-host" {
				if err := os.WriteFile(
					filepath.Join(server.proc, "90", "cmdline"),
					[]byte("claude\x00"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "moved" {
				server.pane = "w9:p2"
			}
			if mode == "dead-host" {
				if err := os.RemoveAll(filepath.Join(server.proc, "90")); err != nil {
					t.Fatal(err)
				}
			}
			client := herdrKiller{engine: pfmengine.Claude, database: db, seat: seat}
			if mode == "move-before-ack" {
				server.pane = "w9:p2"
				if err := client.SendKey(context.Background(), seat.HerdrSocket, seat.HerdrPane, "Escape"); err != nil {
					t.Fatalf("moved ack: %v", err)
				}
				return
			}
			err := client.KillPane(context.Background(), seat.HerdrSocket, seat.HerdrPane)
			if mode == "moved" || mode == "retry-prompt" {
				if err != nil || !server.closed {
					t.Fatalf("moved owned close=%t err=%v", server.closed, err)
				}
				return
			}
			if mode == "dead-host" {
				if err == nil || !strings.Contains(err.Error(), "retained") {
					t.Errorf("dead supervisor falsely reported closed: %v", err)
				}
				if _, probeErr := client.PaneExists(
					context.Background(),
					seat.HerdrSocket,
					seat.HerdrPane,
				); probeErr == nil ||
					!strings.Contains(probeErr.Error(), "retained") {
					t.Errorf("dead supervisor falsely reported gone: %v", probeErr)
				}
				current, found, readErr := db.Shared().Seat(context.Background(), seat.SeatID)
				if readErr != nil || !found || current.SeatID != seat.SeatID {
					t.Fatalf("retained seat=%+v found=%t err=%v", current, found, readErr)
				}
				if _, err := db.Shared().ReadSeatRun(seat.SeatID); err != nil {
					t.Fatalf("retained recipe: %v", err)
				}

				if server.closed {
					t.Fatal("dead host closed unrelated occupant")
				}
				return
			}
			if err == nil || server.closed {
				t.Fatalf("unproved %s close=%t err=%v", mode, server.closed, err)
			}
		})
	}
}

package call

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestHerdrDoorsRequireDurableOwnership(t *testing.T) {
	for _, door := range []string{"capture", "raw", "literal", "keys", "wait", "prompt"} {
		t.Run(door, func(t *testing.T) {
			socket := newNativeSocket(t, func(req nativeRequest) (any, string) {
				switch req.Method {
				case "pane.read":
					return nativePaneRead(req, "❯ "), ""
				case "pane.send_text", "pane.send_keys":
					return map[string]any{"type": "ok"}, ""
				case "agent.prompt":
					return nativeAgent("agent_prompted", "working"), ""
				default:
					return nativeAgent("agent_info", "idle"), ""
				}
			})
			engine := newTestEngine(t, "cc-1-2-3", &fakeTmux{})
			target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "w1:p2", Engine: "cc"}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var err error
			switch door {
			case "capture":
				_, err = engine.CaptureTarget(ctx, target, 10)
			case "raw":
				_, err = herdrRawCapture(ctx, target, "")
			case "literal":
				err = engine.SendLiteral(ctx, target, "text")
			case "keys":
				err = engine.SendKey(ctx, target, "Enter")
			case "wait":
				err = engine.waitHerdrTurn(ctx, target, herdrTurn{SawWorking: true})
			case "prompt":
				result, callErr := engine.CallTarget(ctx, target, Request{Message: "hello"})
				err = callErr
				if err == nil && result.Code != 0 {
					err = fmt.Errorf("%s", result.Message)
				}
			}
			if err == nil || !strings.Contains(err.Error(), "ownership unproved") {
				t.Fatalf("%s without owner: %v; want explicit ownership unproved", door, err)
			}
		})
	}
}

func seedNativeOwner(t *testing.T, target Target) {
	t.Helper()
	t.Setenv(paths.EnvStateDB, filepath.Join(t.TempDir(), "state.db"))
	t.Setenv(paths.EnvProcRoot, t.TempDir())
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	seatID := target.SeatID
	if seatID == "" {
		seatID = "native-fixture"
	}
	db := fleetdb.OpenSharedState(context.Background(), values)
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	seat := fleetdb.Seat{
		SeatID:          seatID,
		SessionID:       target.Session,
		Engine:          target.Engine,
		Backend:         "herdr",
		State:           "live",
		HerdrSocket:     target.SocketPath,
		HerdrPane:       "w1:p2",
		HerdrWorkspace:  "w1",
		HerdrTerminalID: "w1:p2",
		SeatHostPID:     42,
		SeatHostStart:   100,
	}
	if err := db.PutSeat(context.Background(), seat); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(values.ProcRoot, "stat"), []byte("btime 1000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for pid, parent := range map[int]int{41: 1, 42: 41} {
		dir := filepath.Join(values.ProcRoot, fmt.Sprint(pid))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields("S 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 100")
		fields[1] = fmt.Sprint(parent)
		argv := "/bin/sh\x00"
		if pid == 42 {
			argv = "/fixture/pfm\x00internal\x00seat-host\x00" + seatID + "\x00"
		}
		for name, body := range map[string]string{"stat": fmt.Sprintf("%d (pfm) %s", pid, strings.Join(fields, " ")), "cmdline": argv} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// A lost seat keeps the pane it last held: a dead supervisor's record never
// makes that pane's live owner ambiguous, a live one still does.
func TestHerdrPaneOwnerIgnoresARetiredSeat(t *testing.T) {
	const socket = "/fixture/herdr.sock"
	for _, tc := range []struct {
		name      string
		pid       int
		ambiguous bool
	}{
		{name: "dead supervisor", pid: 77},
		{name: "live supervisor", pid: 41, ambiguous: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seedNativeOwner(t, Target{SocketPath: socket, Engine: "cc"})
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			db := fleetdb.OpenSharedState(context.Background(), values)
			if err := db.PutSeat(context.Background(), fleetdb.Seat{
				SeatID: "retired-fixture", Engine: "cc", Backend: "herdr", State: "lost",
				HerdrSocket: socket, HerdrPane: "w1:p2", HerdrWorkspace: "w1", HerdrTerminalID: "w1:old",
				SeatHostPID: tc.pid, SeatHostStart: 100,
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			_, err = ProveHerdrTarget(context.Background(), Target{SocketPath: socket, Pane: "w1:p2"})
			if got := err != nil && strings.Contains(err.Error(), "multiple durable owners"); got != tc.ambiguous {
				t.Fatalf("proof = %v; ambiguous=%t, want %t", err, got, tc.ambiguous)
			}
		})
	}
}

// The proofs of one pass read herdr-shield through their one shield: every
// read of a pane herdr-shield holds (a painter tick reads one chat's pane from
// its probe, its view and its status) asks herdr-shield once for the socket,
// where the zero HerdrProofs asks once a read.
func TestHerdrProofsAskHerdrShieldOncePerSocket(t *testing.T) {
	const reads = 3
	for _, tc := range []struct {
		name   string
		shared bool
		runs   int
	}{
		{name: "one shield", shared: true, runs: 1},
		{name: "shield per proof", runs: reads},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Holder 5 runs shell 10, whose terminal's foreground group 41 runs the
			// engine of pane w1:p2; Herdr sees only herdr-shield's attach client 8.
			proc := t.TempDir()
			t.Setenv(paths.EnvProcRoot, proc)
			zeros := strings.Repeat("0 ", 13)
			for name, body := range map[string]string{
				"5/stat":     "5 (herdr-shield) S 1 5 5 0 -1 " + zeros + "2 0\n",
				"10/stat":    "10 (zsh) S 5 10 10 0 41 " + zeros + "3 0\n",
				"41/stat":    "41 (claude) S 10 41 10 0 41 " + zeros + "4 0\n",
				"41/cmdline": "claude\x00",
			} {
				path := filepath.Join(proc, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			socket := newNativeSocketForeground(
				t,
				func(request nativeRequest) (any, string) { return nil, "unexpected " + request.Method },
				func() []any { return []any{map[string]any{"pid": 8, "name": "herdr-shield"}} },
			)
			runs := testjail.StageHerdrShield(t, testjail.HerdrShieldListing(5, 2, 10, 3, "w1:p2", socket.path), 0)
			proofs := HerdrProofs{}
			if tc.shared {
				proofs.Shield = gather.HerdrShield(gather.NewProcFS(proc))
			}
			seat := fleetdb.Seat{HerdrSocket: socket.path, HerdrPane: "w1:p2", Engine: string(pfmengine.Claude)}
			for read := range reads {
				alive, err := proofs.Engine(context.Background(), seat)
				if err != nil || !alive {
					t.Fatalf("read %d of held pane w1:p2: alive=%t err=%v; want alive", read, alive, err)
				}
			}
			if got := runs(); got != tc.runs {
				t.Fatalf("herdr-shield list ran %d times for %d reads of one socket; want %d", got, reads, tc.runs)
			}
		})
	}
}

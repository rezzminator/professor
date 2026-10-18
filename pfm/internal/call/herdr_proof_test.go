package call

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
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

package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestDoctorReportsUnprovedNativeOwnership(t *testing.T) {
	for _, mode := range []string{"healthy", "unknown", "unlearned", "dead", "failed", "lost", "unreachable"} {
		t.Run(mode, func(t *testing.T) {
			runtime := buildCleanDoctorHome(t)
			ctx := context.Background()
			for name, body := range map[string]string{
				"stat": "btime 1000\n", "20/stat": "20 (sh) S 1 " + strings.Repeat("0 ", 17) + "3 0\n",
				"21/stat": "21 (pfm) S 20 " + strings.Repeat("0 ", 17) + "9 0\n", "21/cmdline": "/fixture/pfm\x00internal\x00seat-host\x00stale-seat\x00",
			} {
				path := filepath.Join(runtime.Paths.ProcRoot, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			socket := filepath.Join(testjail.ShortRoot(t), "ownership.sock")
			if mode != "unreachable" {
				listener, err := net.Listen("unix", socket)
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan struct{})
				t.Cleanup(func() { _ = listener.Close(); <-done })
				go func() {
					defer close(done)
					for {
						conn, err := listener.Accept()
						if err != nil {
							return
						}
						func() {
							defer func() { _ = conn.Close() }()
							var req struct {
								ID     string `json:"id"`
								Method string `json:"method"`
							}
							if err := json.NewDecoder(conn).Decode(&req); err != nil {
								t.Error(err)
								return
							}
							var result any
							switch req.Method {
							case "pane.list":
								pane := testjail.HerdrPaneInfo("w1:p1")["pane"].(map[string]any)
								pane["terminal_id"] = "terminal"
								result = map[string]any{"type": "pane_list", "panes": []any{pane}}
							case "pane.process_info":
								result = map[string]any{
									"type":         "pane_process_info",
									"process_info": map[string]any{"pane_id": "w1:p1", "shell_pid": 20},
								}
							default:
								t.Errorf("unexpected ownership method %s", req.Method)
								return
							}
							if err := json.NewEncoder(conn).
								Encode(map[string]any{"id": req.ID, "result": result}); err != nil {
								t.Error(err)
							}
						}()
					}
				}()
			}
			seat := fleetdb.Seat{
				SeatID:          "stale-seat",
				SessionID:       "session",
				Backend:         "herdr",
				Engine:          "cc",
				State:           "live",
				HerdrSocket:     socket,
				HerdrPane:       "w1:p1",
				HerdrWorkspace:  "w1",
				HerdrTerminalID: "terminal",
				SeatHostPID:     21,
				SeatHostStart:   9,
			}
			if mode == "unknown" || mode == "unlearned" {
				seat.HerdrTerminalID = "old-terminal"
			}
			if mode == "unlearned" {
				seat.SessionID = ""
			}
			if mode == "dead" {
				seat.SeatHostPID = 900002
			}
			if mode == "failed" || mode == "lost" {
				seat.State = mode
				seat.HerdrTerminalID = "historical-terminal"
			}
			db := fleetdb.OpenSharedState(ctx, runtime.Paths)
			if err := db.PutSeat(ctx, seat); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			warnings := printHerdrSeatOwnership(ctx, &out, runtime.Paths)
			switch mode {
			case "healthy", "failed", "lost":
				if warnings != 0 || out.Len() != 0 {
					t.Fatalf("healthy/historical row warned: warnings=%d out=%s", warnings, &out)
				}
			case "dead":
				if warnings != 0 || !strings.Contains(out.String(), "native ownership retired seat=stale-seat") ||
					!strings.Contains(out.String(), "--restored session") {
					t.Fatalf("positive death not distinguished: warnings=%d out=%s", warnings, &out)
				}
			default:
				target := seat.SessionID
				if target == "" {
					target = seat.SeatID
				}
				if warnings != 1 || !strings.Contains(out.String(), "native ownership unproved seat=stale-seat") ||
					!strings.Contains(out.String(), fmt.Sprintf("--restored %s;", target)) {
					t.Fatalf("unknown ownership lacks usable repair: warnings=%d out=%s", warnings, &out)
				}
			}
		})
	}
}

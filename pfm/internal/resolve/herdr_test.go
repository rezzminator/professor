package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestWhoamiHerdrRequiresPersistedSeatAndEngineAncestor(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store bool
		pid   int
		want  bool
	}{
		{"history-failed", true, 40, true}, {"history-lost", true, 40, true}, {"history-dead-host", true, 40, true}, {"history-live-host", true, 40, false}, {"live-terminal-ambiguous", true, 40, false}, {"verified", true, 40, true}, {"legacy-live", true, 40, true}, {"legacy-recycled", true, 40, false}, {"saved-host-dead", true, 40, false}, {"saved-host-argv", true, 40, false}, {"saved-host-reused", true, 40, false}, {"moved", true, 40, true}, {"canonical-env", true, 40, true}, {"stale-alias-row", true, 40, true}, {"recycled", true, 40, false}, {"unnamed", true, 40, true}, {"unrelated", true, 88, false}, {"unrecorded", false, 40, false}, {"seatless-unreachable", false, 40, false}, {"seatless-gone", false, 40, false}, {"recorded-probe-failed", true, 40, false}, {"omitted", true, 0, false}, {"empty", true, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testjail.Fleet(t)
			proc := t.TempDir()
			t.Setenv(paths.EnvProcRoot, proc)
			argv := "pfm\x00internal\x00seat-host\x00seat1\x00"
			if tc.name == "legacy-recycled" || tc.name == "saved-host-argv" {
				argv = "claude\x00"
			}
			start := "9"
			if tc.name == "saved-host-reused" {
				start = "10"
			}
			for name, body := range map[string]string{"stat": "btime 1000000000\n", "10/stat": "10 (sh) S 1 " + strings.Repeat("0 ", 17) + "3 0\n", "20/stat": "20 (pfm) S 10 " + strings.Repeat("0 ", 17) + start + " 0\n", "20/cmdline": argv} {
				path := filepath.Join(proc, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "saved-host-dead" {
				if err := os.RemoveAll(filepath.Join(proc, "20")); err != nil {
					t.Fatal(err)
				}
			}
			socket := filepath.Join(testjail.ShortRoot(t), "herdr.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			go func() {
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
						if json.NewDecoder(conn).Decode(&req) != nil {
							return
						}
						if tc.name == "seatless-unreachable" || tc.name == "recorded-probe-failed" {
							return
						}
						if tc.name == "seatless-gone" {
							_ = json.NewEncoder(conn).
								Encode(map[string]any{"id": req.ID, "error": map[string]string{"code": "pane_not_found", "message": "gone"}})
							return
						}
						canonical := "w6:p3"
						if tc.name == "moved" || tc.name == "canonical-env" || tc.name == "stale-alias-row" ||
							strings.HasPrefix(tc.name, "history-") ||
							tc.name == "live-terminal-ambiguous" {
							canonical = "w9:p2"
						}
						if req.Method == "pane.get" || req.Method == "pane.list" {
							body := testjail.HerdrPaneInfo(canonical)
							body["pane"].(map[string]any)["terminal_id"] = "own-terminal"
							if tc.name == "recycled" {
								body["pane"].(map[string]any)["terminal_id"] = "other-terminal"
							}
							if req.Method == "pane.list" {
								body = map[string]any{"type": "pane_list", "panes": []any{body["pane"]}}
							}
							_ = json.NewEncoder(conn).Encode(map[string]any{"id": req.ID, "ok": true, "result": body})
							return
						}
						if tc.name == "omitted" || tc.name == "empty" {
							processes := ""
							if tc.name == "empty" {
								processes = `,"foreground_processes":[]`
							}
							_, _ = fmt.Fprintf(
								conn,
								`{"id":%q,"ok":true,"result":{"type":"pane_process_info","process_info":{"pane_id":"w6:p3"%s}}}`+"\n",
								req.ID,
								processes,
							)
							return
						}
						_, _ = fmt.Fprintf(
							conn,
							`{"id":%q,"ok":true,"result":{"type":"pane_process_info","process_info":{"pane_id":%q,"shell_pid":10,"foreground_processes":[{"pid":%d,"argv0":"claude"}]}}}`+"\n",
							req.ID,
							canonical,
							tc.pid,
						)
					}()
				}
			}()
			t.Setenv("TMUX", "")
			t.Setenv("TMUX_PANE", "")
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_SOCKET_PATH", socket)
			t.Setenv("HERDR_PANE_ID", "w6:p3")
			if tc.name == "canonical-env" || strings.HasPrefix(tc.name, "history-") ||
				tc.name == "live-terminal-ambiguous" {
				t.Setenv("HERDR_PANE_ID", "w9:p2")
			}
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			state := fleetdb.OpenSharedState(context.Background(), values)
			t.Cleanup(func() {
				if err := state.Close(); err != nil {
					t.Error(err)
				}
			})
			name, session := "REVIEW", "REVIEW"
			if tc.name == "unnamed" {
				name, session = "", "seat1"
			}
			if tc.store {
				terminal := "own-terminal"
				if strings.HasPrefix(tc.name, "legacy-") {
					terminal = ""
				}
				host, start := 0, uint64(0)
				if strings.HasPrefix(tc.name, "saved-host-") {
					host, start = 20, 9
				}
				if err := state.PutSeat(
					context.Background(),
					fleetdb.Seat{
						State:           "live",
						SeatID:          "seat1",
						SessionID:       "sid1",
						Engine:          "cc",
						Backend:         "herdr",
						HerdrSocket:     socket,
						HerdrPane:       "w6:p3",
						HerdrWorkspace:  "w6",
						HerdrTerminalID: terminal,
						SeatHostPID:     host, SeatHostStart: start,
						Name: name,
					},
				); err != nil {
					t.Fatal(err)
				}
			}
			if tc.name == "stale-alias-row" {
				own, ok, err := state.Seat(context.Background(), "seat1")
				if err != nil || !ok {
					t.Fatalf("own seat %v", err)
				}
				before := own
				own.HerdrPane = "w9:p2"
				own.HerdrWorkspace = "w9"
				if err := state.UpdateSeat(context.Background(), before, own); err != nil {
					t.Fatal(err)
				}
				other := before
				other.SeatID = "seat2"
				other.SessionID = "sid2"
				other.HerdrTerminalID = "other-terminal"
				if err := state.PutSeat(context.Background(), other); err != nil {
					t.Fatal(err)
				}
			}
			if strings.HasPrefix(tc.name, "history-") || tc.name == "live-terminal-ambiguous" {
				own, ok, err := state.Seat(context.Background(), "seat1")
				if err != nil || !ok {
					t.Fatal(err)
				}
				other := own
				other.SeatID = "historical"
				other.SessionID = "old"
				other.HerdrPane = "w2:p8"
				other.State = "failed"
				if tc.name == "history-lost" {
					other.State = "lost"
				}
				if tc.name == "history-dead-host" {
					other.SeatHostPID = 30
					other.SeatHostStart = 9
				}
				if tc.name == "history-live-host" {
					other.SeatHostPID = 20
					other.SeatHostStart = 9
				}
				if tc.name == "live-terminal-ambiguous" {
					other.State = "live"
				}
				if err := state.PutSeat(context.Background(), other); err != nil {
					t.Fatal(err)
				}
			}
			identifier, err := NewWhoami(
				WhoamiDependencies{
					Processes: fakeProcTree{parents: map[int]int{99: 40, 40: 20, 20: 10, 10: 1}},
					SelfPID:   99,
					TmuxDir:   t.TempDir(),
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := identifier.Identify(context.Background())
			if !tc.want {
				if tc.name == "omitted" {
					if err == nil || !strings.Contains(err.Error(), "foreground_processes missing or null") {
						t.Fatalf("missing process probe rendered as no seat: identity=%+v err=%v", identity, err)
					}
					return
				}
				if tc.name == "legacy-recycled" || strings.HasPrefix(tc.name, "saved-host-") ||
					tc.name == "recorded-probe-failed" || tc.name == "history-live-host" || tc.name == "live-terminal-ambiguous" {
					if err == nil {
						t.Fatalf("unproved supervisor adopted legacy/durable row: identity=%+v", identity)
					}
					row, ok, e := state.Seat(context.Background(), "seat1")
					if e != nil || !ok || tc.name == "legacy-recycled" && row.HerdrTerminalID != "" {
						t.Fatalf("unproved row mutated: %+v %v", row, e)
					}
					return
				}
				if !errors.Is(err, ErrNoTmux) || !strings.Contains(err.Error(), "nor a pfm Herdr seat") {
					t.Fatalf("identity=%+v err=%v", identity, err)
				}
				return
			}
			if err != nil || identity.Backend != "herdr" || identity.SeatID != "seat1" || identity.ID != "sid1" ||
				identity.Session != session ||
				identity.SocketPath != socket ||
				identity.Pane != map[bool]string{true: "w9:p2", false: "w6:p3"}[tc.name == "moved" || tc.name == "canonical-env" || tc.name == "stale-alias-row" || strings.HasPrefix(tc.name, "history-") || tc.name == "live-terminal-ambiguous"] {
				t.Fatalf("identity=%+v err=%v", identity, err)
			}
			row, ok, e := state.Seat(context.Background(), "seat1")
			if e != nil || !ok || row.HerdrPane != identity.Pane {
				t.Fatalf("canonical row=%+v err=%v", row, e)
			}
		})
	}
}

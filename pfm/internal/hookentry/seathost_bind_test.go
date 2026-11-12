package hookentry

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestBindNativeHostRequiresKernelRoleAndCanonicalOwnership(t *testing.T) {
	for _, mode := range []string{"legacy", "moved", "recycled", "old-live", "old-dead-live", "cold-lost", "cold-moved-caller", "unknown-old", "wrong-shell", "shell-negative", "shell-zero", "shell-one", "wrong-argv", "malformed", "malformed-list", "raced", "raced-other", "pane-pending", "pane-never"} {
		t.Run(mode, func(t *testing.T) {
			root := testjail.ShortRoot(t)
			proc := filepath.Join(root, "proc")
			if err := os.MkdirAll(proc, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(proc, "stat"), []byte("btime 1000\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			writeProc := func(pid, parent int, argv string) {
				dir := filepath.Join(proc, strconv.Itoa(pid))
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				fields := make([]string, 24)
				for i := range fields {
					fields[i] = "0"
				}
				fields[0] = "S"
				fields[1] = strconv.Itoa(parent)
				fields[19] = "100"
				if err := os.WriteFile(
					filepath.Join(dir, "stat"),
					[]byte(fmt.Sprintf("%d (pfm) %s", pid, strings.Join(fields, " "))),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(argv), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			argv := "/fixture/pfm\x00internal\x00seat-host\x00host-seat\x00"
			if mode == "wrong-argv" {
				argv = "/fixture/pfm\x00internal\x00launch\x00host-seat\x00"
			}
			writeProc(os.Getpid(), 900002, argv)
			writeProc(900002, 1, "sh\x00")
			pane, terminal := "w6:p3", "stable"
			socket := filepath.Join(root, "h.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			// publish stamps a binding between the bind's read and its write, as a gather proof does
			// without the seat lock: the bind's own supervisor when raced, another process when raced-other.
			publish := func() {
				ctx := context.Background()
				store := fleetdb.OpenSharedState(
					ctx,
					paths.Values{StateDB: filepath.Join(root, "state.db"), ProcRoot: proc},
				)
				defer func() { _ = store.Close() }()
				current, found, err := store.Seat(ctx, "host-seat")
				if err != nil {
					t.Error(err)
					return
				}
				if !found || current.SeatHostPID != 0 {
					return
				}
				next := current
				next.HerdrPane, next.HerdrWorkspace, next.HerdrTerminalID = pane, "w6", terminal
				host := os.Getpid()
				if mode == "raced-other" {
					host = 900002
				}
				if err := gather.StampHerdrHost(gather.NewProcFS(proc), &next, host); err != nil {
					t.Error(err)
					return
				}
				if err := store.UpdateSeat(ctx, current, next); err != nil {
					t.Error(err)
				}
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
							ID     string            `json:"id"`
							Method string            `json:"method"`
							Params map[string]string `json:"params"`
						}
						if err := json.NewDecoder(conn).Decode(&req); err != nil {
							t.Error(err)
							return
						}
						var result any
						if req.Params["pane_id"] == "w9:p9" {
							// The caller's inherited pane id, which a move retired.
							if err := json.NewEncoder(conn).Encode(map[string]any{
								"id": req.ID, "error": map[string]string{"code": "pane_not_found", "message": "gone"},
							}); err != nil {
								t.Error(err)
							}
							return
						}
						switch req.Method {
						case "pane.get":
							info := testjail.HerdrPaneInfo(pane)
							fields := info["pane"].(map[string]any)
							fields["terminal_id"] = terminal
							if mode == "malformed" {
								fields["workspace_id"] = "w99"
							}
							result = info
						case "pane.list":
							fields := testjail.HerdrPaneInfo(pane)["pane"].(map[string]any)
							fields["terminal_id"] = terminal
							result = map[string]any{"type": "pane_list", "panes": []any{fields}}
							if mode == "malformed-list" {
								result = map[string]any{"type": "pane_list"}
							}
						case "pane.process_info":
							if mode == "raced" || mode == "raced-other" {
								publish()
							}
							shell := os.Getpid()
							switch mode {
							case "wrong-shell":
								shell = 900003
							case "shell-negative":
								shell = -1
							case "shell-zero":
								shell = 0
							case "shell-one":
								shell = 1
							}
							foreground := map[string]any{"pid": os.Getpid(), "argv0": "pfm"}
							result = map[string]any{
								"type": "pane_process_info",
								"process_info": map[string]any{
									"pane_id": pane, "shell_pid": shell,
									"foreground_processes": []any{foreground},
								},
							}
						default:
							t.Errorf("unexpected bind mutation %s", req.Method)
							return
						}
						if err := json.NewEncoder(conn).
							Encode(map[string]any{"id": req.ID, "result": result}); err != nil {
							t.Error(err)
						}
					}()
				}
			}()
			values := paths.Values{StateDB: filepath.Join(root, "state.db"), ProcRoot: proc}
			db := fleetdb.OpenSharedState(context.Background(), values)
			defer func() { _ = db.Close() }()
			seat := fleetdb.Seat{
				SeatID:      "host-seat",
				SessionID:   "session",
				Backend:     "herdr",
				State:       "live",
				HerdrSocket: socket,
				HerdrPane:   pane,
			}
			caller := pane
			switch mode {
			case "moved":
				seat.HerdrPane = "w1:p1"
				seat.HerdrTerminalID = terminal
				caller = "w1:p1"
			case "recycled":
				seat.HerdrTerminalID = "old-terminal"
			case "pane-pending", "pane-never":
				// A new tab's reservation: Herdr names the pane only once layout.apply answers.
				seat.HerdrPane = ""
				if mode == "pane-never" {
					saved := reservedPaneWait
					reservedPaneWait = 100 * time.Millisecond
					t.Cleanup(func() { reservedPaneWait = saved })
				}
			case "old-live", "old-dead-live", "cold-lost", "cold-moved-caller", "unknown-old":
				seat.SeatHostPID = 900001
				seat.SeatHostStart = 100
				seat.HerdrTerminalID = "old-terminal"
				if mode == "old-live" {
					writeProc(900001, 1, "/fixture/pfm\x00internal\x00seat-host\x00host-seat\x00")
				}
				if mode == "cold-lost" || mode == "cold-moved-caller" {
					seat.State = "lost"
				}
				if mode == "cold-moved-caller" {
					caller = "w9:p9"
				}
				if mode == "unknown-old" {
					writeProc(900001, 1, argv)
					if err := os.WriteFile(
						filepath.Join(proc, "900001", "stat"),
						[]byte("malformed"),
						0o600,
					); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := db.PutSeat(context.Background(), seat); err != nil {
				t.Fatal(err)
			}
			before, _, err := db.Seat(context.Background(), seat.SeatID)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv(herdr.EnvSocket, socket)
			t.Setenv(herdr.EnvPane, caller)
			addressed := make(chan error, 1)
			if mode == "pane-pending" {
				// The birth records the pane after its supervisor has already started.
				go func() {
					<-time.After(50 * time.Millisecond)
					reserved, _, err := db.Seat(context.Background(), seat.SeatID)
					if err == nil {
						next := reserved
						next.HerdrPane = pane
						err = db.UpdateSeat(context.Background(), reserved, next)
					}
					addressed <- err
				}()
			} else {
				addressed <- nil
			}
			got, bindErr := bindNativeHost(context.Background(), db, before, values)
			if err := <-addressed; err != nil {
				t.Fatalf("record the reserved pane: %v", err)
			}
			if mode == "pane-pending" {
				before.HerdrPane = pane
			}
			allowed := mode == "legacy" || mode == "moved" || mode == "cold-lost" || mode == "cold-moved-caller" ||
				mode == "raced" || mode == "pane-pending"
			current, found, readErr := db.Seat(context.Background(), seat.SeatID)
			if allowed {
				if bindErr != nil || !found || readErr != nil || got.SeatHostPID != os.Getpid() ||
					got.SeatHostStart != 100 ||
					got.SeatHostBirth == 0 ||
					got.HerdrTerminalID != terminal ||
					got.HerdrSocket != socket ||
					got.HerdrPane != pane {
					t.Fatalf("bind %+v err=%v found=%v read=%v", got, bindErr, found, readErr)
				}
			} else if mode == "raced-other" {
				// Another supervisor's binding stands; the bind refuses at its write.
				want := `persist supervisor binding: update seat "host-seat": seat no longer exists or Herdr pane is already owned`
				if bindErr == nil || bindErr.Error() != want || !found || readErr != nil ||
					current.SeatHostPID != 900002 {
					t.Fatalf(
						"raced bind: err=%v want %q; stored %+v found=%v read=%v",
						bindErr,
						want,
						current,
						found,
						readErr,
					)
				}
			} else if bindErr == nil || !found || readErr != nil || !reflect.DeepEqual(current, before) {
				t.Fatalf(
					"unsafe bind changed owner: before=%+v after=%+v err=%v read=%v",
					before,
					current,
					bindErr,
					readErr,
				)
			}
		})
	}
}

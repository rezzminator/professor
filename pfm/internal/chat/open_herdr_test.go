package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

type nativeChatRequest struct {
	ID     string                     `json:"id"`
	Method string                     `json:"method"`
	Params map[string]json.RawMessage `json:"params"`
}

func nativeChatSocket(t *testing.T, handle func(nativeChatRequest) (any, string)) string {
	t.Helper()
	socket := filepath.Join(testjail.ShortRoot(t), "herdr.sock")
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
				var req nativeChatRequest
				if err := json.NewDecoder(conn).Decode(&req); err != nil {
					t.Errorf("decode native request: %v", err)
					return
				}
				result, code := handle(req)
				if code != "" {
					_, _ = fmt.Fprintf(
						conn,
						`{"id":%q,"ok":false,"error":{"code":%q,"message":"fixture refusal"}}`+"\n",
						req.ID,
						code,
					)
					return
				}
				_ = json.NewEncoder(conn).Encode(map[string]any{"id": req.ID, "ok": true, "result": result})
			}()
		}
	}()
	return socket
}

func nativeChatSeat(t *testing.T, socket, state string) (config.Runtime, *fleetdb.Store, fleetdb.Seat) {
	t.Helper()
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	db := fleetdb.OpenSharedState(context.Background(), values)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	seat := fleetdb.Seat{
		SeatID:         "restore-seat",
		SessionID:      "6fafd8a7-5059-4288-bf58-23e60c5fdb88",
		Engine:         string(pfmengine.Claude),
		Backend:        "herdr",
		HerdrSocket:    socket,
		HerdrPane:      "w6:p3",
		HerdrWorkspace: "w6",
		Name:           "INTEGRATION",
		State:          state,
	}
	if err := db.PutSeat(context.Background(), seat); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteSeatRun(
		seat.SeatID,
		fleetdb.SeatRun{Binary: "/fixture/claude", Args: []string{"--resume", seat.SessionID}, CWD: values.Home},
	); err != nil {
		t.Fatal(err)
	}
	return config.Runtime{Paths: values}, db, seat
}

// nativeChatHost seeds the actual ProcFS producer shape, not an engine-name hint.
func nativeChatHost(t *testing.T, db *fleetdb.Store, seat fleetdb.Seat, alive bool) fleetdb.Seat {
	t.Helper()
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	seat.SeatHostPID, seat.SeatHostStart = 42, 100
	seat.HerdrTerminalID = "fixture-w6:p3"
	if err := db.PutSeat(context.Background(), seat); err != nil {
		t.Fatal(err)
	}
	if !alive {
		return seat
	}
	if err := os.MkdirAll(filepath.Join(values.ProcRoot, "42"), 0o700); err != nil {
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
		if err := os.WriteFile(
			filepath.Join(dir, "stat"),
			[]byte(fmt.Sprintf("%d (pfm) %s", pid, strings.Join(fields, " "))),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		argv := "/bin/sh\x00"
		if pid == 42 {
			argv = "/fixture/pfm\x00internal\x00seat-host\x00" + seat.SeatID + "\x00"
		}
		if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(argv), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return seat
}

// nativeChatCallerHostsChat seats another chat in the caller's pane, so a
// live open from it views by focus and moves nothing.
func nativeChatCallerHostsChat(t *testing.T, db *fleetdb.Store, socket, pane string) {
	t.Helper()
	workspace, _, _ := strings.Cut(pane, ":")
	if err := db.PutSeat(context.Background(), fleetdb.Seat{
		SeatID: "caller-seat", SessionID: "11111111-1111-4111-8111-111111111111", Engine: string(pfmengine.Claude),
		Backend: "herdr", HerdrSocket: socket, HerdrPane: pane, HerdrWorkspace: workspace, State: "live",
	}); err != nil {
		t.Fatal(err)
	}
}

func nativeChatProof(req nativeChatRequest, pane string) (any, string, bool) {
	switch req.Method {
	case "pane.list":
		info := testjail.HerdrPaneInfo(pane)["pane"].(map[string]any)
		info["terminal_id"] = "fixture-w6:p3"
		return map[string]any{"type": "pane_list", "panes": []any{info}}, "", true
	case "pane.process_info":
		return map[string]any{
			"type":         "pane_process_info",
			"process_info": map[string]any{"pane_id": pane, "shell_pid": 41},
		}, "", true
	}
	return nil, "", false
}

func TestOpenNativeUsesActualProcessesAndDurableAddress(t *testing.T) {
	for _, state := range []string{"starting", "retrying", "moved", "recycled", "unlearned"} {
		doors := []string{"row", "id", "detached"}
		if state == "unlearned" {
			doors = []string{"row", "detached"}
		} // No conversation id exists yet.
		for _, door := range doors {
			t.Run(state+"/"+door, func(t *testing.T) {
				testjail.Fleet(t)
				pane := "w6:p3"
				if state == "moved" {
					pane = "w9:p2"
				}
				mutations := make(chan string, 8)
				socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
					if req.Method == "agent.focus" || req.Method == "pane.focus" {
						mutations <- req.Method
					}
					if req.Method == "pane.process_info" && state == "recycled" {
						return map[string]any{
							"type": "pane_process_info",
							"process_info": map[string]any{
								"pane_id":   pane,
								"shell_pid": 41,
								"foreground_processes": []any{
									map[string]any{"pid": 42, "name": "claude", "argv0": "claude"},
								},
							},
						}, ""
					}
					if req.Method == "pane.list" && state == "recycled" {
						return map[string]any{
							"type":  "pane_list",
							"panes": []any{testjail.HerdrPaneInfo("w6:p8")["pane"]},
						}, ""
					}
					if value, code, ok := nativeChatProof(req, pane); ok {
						return value, code
					}
					switch req.Method {
					case "pane.get":
						return testjail.HerdrPaneInfo(pane), ""
					case "agent.focus":
						return nil, "agent_not_found"
					case "pane.focus":
						if string(req.Params["pane_id"]) != fmt.Sprintf("%q", pane) {
							t.Errorf("noncanonical focus: %s", req.Params["pane_id"])
						}
						return testjail.HerdrPaneInfo(pane), ""
					default:
						return nil, "unexpected_method"
					}
				})
				runtime, db, seat := nativeChatSeat(t, socket, "live")
				if state == "retrying" {
					seat.State = "failed"
				}
				if state == "unlearned" {
					seat.SessionID = ""
				}
				seat = nativeChatHost(t, db, seat, true)
				t.Setenv("HERDR_ENV", "1")
				t.Setenv("HERDR_SOCKET_PATH", socket)
				t.Setenv("HERDR_PANE_ID", "w6:p8")
				// A caller pane hosting a chat views a live chat by focus.
				nativeChatCallerHostsChat(t, db, socket, "w6:p8")
				var stdout, stderr bytes.Buffer
				code := 0
				switch door {
				case "row":
					code = OpenRow(context.Background(), nativeRow(seat), 1, false, "", &stdout, &stderr, &runtime)
				case "id":
					code = OpenID(context.Background(), seat.SessionID, &stdout, &stderr, &runtime)
				case "detached":
					result, err := OpenDetachedTarget(
						context.Background(),
						headless.Chat{
							Backend: "herdr",
							SeatID:  seat.SeatID,
							ID:      seat.SessionID,
							Socket:  socket,
							Pane:    seat.HerdrPane,
						},
						&stderr,
						&runtime,
					)
					if err != nil {
						code = 1
					} else if result.State != "live" || !strings.Contains(result.Detail, pane) {
						t.Fatalf("result=%+v", result)
					}
				}
				if state == "recycled" {
					if code != 1 || len(mutations) != 0 {
						t.Fatalf("recycled occupant was adopted or focused: code=%d mutations=%d", code, len(mutations))
					}
				} else if code != 0 {
					t.Fatalf("live supervisor rejected: code=%d stderr=%s", code, &stderr)
				}
				if state != "recycled" && door != "detached" {
					if len(mutations) != 2 || <-mutations != "agent.focus" || <-mutations != "pane.focus" {
						t.Fatal("live native open did not focus its canonical pane")
					}
				}
				if state == "moved" && door == "detached" {
					// The first proof persisted w9:p2. A previously resolved caller still
					// carries w6:p3, which is an address hint, never ownership evidence.
					result, err := OpenDetachedTarget(
						context.Background(),
						headless.Chat{
							Backend: "herdr",
							SeatID:  seat.SeatID,
							ID:      seat.SessionID,
							Socket:  socket,
							Pane:    seat.HerdrPane,
						},
						&stderr,
						&runtime,
					)
					if err != nil || result.State != "live" || !strings.Contains(result.Detail, pane) {
						t.Fatalf("stale caller after canonical persistence: result=%+v err=%v", result, err)
					}
				}
				current, found, err := db.Seat(context.Background(), seat.SeatID)
				want := pane
				if state == "recycled" {
					want = seat.HerdrPane
				}
				if err != nil || !found || current.HerdrPane != want {
					t.Fatalf("seat=%+v found=%t err=%v", current, found, err)
				}
				if _, err := db.ReadSeatRun(seat.SeatID); err != nil {
					t.Fatalf("live recipe lost: %v", err)
				}
			})
		}
	}
}

func TestOpenNativeOutsideReportsLocationWhenAttachFails(t *testing.T) {
	testjail.Fleet(t)
	socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
		if value, code, ok := nativeChatProof(req, "w6:p3"); ok {
			return value, code
		}
		return nil, "unexpected_method"
	})
	runtime, db, seat := nativeChatSeat(t, socket, "live")
	seat = nativeChatHost(t, db, seat, true)
	t.Setenv("HERDR_SOCKET_PATH", "")
	t.Setenv("HERDR_ENV", "")
	bin := t.TempDir()
	if err := testjail.WriteExecutable(filepath.Join(bin, "herdr"), []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	var stdout, stderr bytes.Buffer
	code := OpenID(context.Background(), seat.SessionID, &stdout, &stderr, &runtime)
	want := fmt.Sprintf("INTEGRATION lives in Herdr pane w6:p3 (%s)", socket)
	if code != 1 || !strings.Contains(stderr.String(), want) {
		t.Fatalf("external open code=%d out=%q err=%q want=%q", code, stdout.String(), stderr.String(), want)
	}
}

func TestOpenNativeDeadDetachedResumesByCallerBackend(t *testing.T) {
	for _, backend := range []string{"tmux", "herdr"} {
		t.Run(backend, func(t *testing.T) {
			root := testjail.Fleet(t)
			layouts := make(chan struct{}, 4)
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				switch req.Method {
				case "ping":
					return map[string]any{"type": "pong", "protocol": 22, "version": "fixture"}, ""
				case "pane.list":
					return map[string]any{"type": "pane_list", "panes": []any{func() any {
						info := testjail.HerdrPaneInfo("w6:p9")["pane"].(map[string]any)
						info["terminal_id"] = "fixture-w6:p3"
						return info
					}()}}, ""
				case "pane.get":
					var pane string
					_ = json.Unmarshal(req.Params["pane_id"], &pane)
					if pane == "w6:p3" {
						return nil, "pane_not_found"
					}
					return map[string]any{
						"type": "pane_info",
						"pane": map[string]any{"pane_id": pane, "workspace_id": "w6"},
					}, ""
				case "pane.process_info":
					return map[string]any{
						"type": "pane_process_info",
						"process_info": map[string]any{
							"pane_id": "w6:p9", "shell_pid": 41,
							"foreground_processes": []any{map[string]any{"pid": 42, "name": "claude"}},
						},
					}, ""
				case "pane.focus":
					return nil, "permission_denied"
				case "layout.apply":
					layouts <- struct{}{}
					return map[string]any{
						"layout": map[string]any{
							"workspace_id": "w6",
							"root":         map[string]any{"type": "pane", "pane_id": "w6:p9"},
						},
					}, ""
				default:
					return nil, "unexpected_method"
				}
			})
			runtime, db, seat := nativeChatSeat(t, socket, "lost")
			seat = nativeChatHost(t, db, seat, false)
			loaded, err := config.LoadRuntime("")
			if err != nil {
				t.Fatal(err)
			}
			runtime.Config = loaded.Config
			seedClaudeChat(t, root, seat.SessionID)
			// The replaced seat's reload schedule files go with its row: the
			// new seat has a new id, so nothing else would remove them.
			reloadFiles := plantNativeReloadFiles(t, runtime.Paths.SIDDir, seat.SeatID)
			binary := filepath.Join(runtime.Paths.Home, "native-engine")
			if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := db.WriteSeatRun(
				seat.SeatID,
				fleetdb.SeatRun{Binary: binary, CWD: runtime.Paths.Home, Args: []string{"--resume", seat.SessionID}},
			); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HERDR_ENV", "")
			t.Setenv("HERDR_SOCKET_PATH", "")
			t.Setenv("HERDR_PANE_ID", "")
			t.Setenv("TMUX", "")
			ctx := context.Background()
			if strings.HasPrefix(backend, "herdr") {
				ctx = WithResolvedSelf(
					ctx,
					headless.Chat{Backend: "herdr", Socket: socket, Pane: "w6:p8", HerdrWorkspace: "w6"},
				)
			}
			fake := &fakeOpenTmux{alive: map[string]bool{}}
			stubOpenExecutor(t, fake)
			result, err := OpenDetachedTarget(
				ctx,
				headless.Chat{
					ID:      seat.SessionID,
					SeatID:  seat.SeatID,
					Backend: "herdr",
					Engine:  pfmengine.Claude,
					Socket:  seat.HerdrSocket,
					Pane:    seat.HerdrPane,
				},
				io.Discard,
				&runtime,
			)
			if err != nil || result.State != "opened" {
				t.Fatalf("dead native result=%+v err=%v", result, err)
			}
			if backend == "tmux" && len(fake.created) != 1 {
				t.Fatalf("tmux resume=%+v", fake.created)
			}
			if backend == "herdr" && (result.Socket != socket || !strings.Contains(result.Detail, "w6:p9")) {
				t.Fatalf("native resume=%+v", result)
			}
			assertNativeReloadFilesGone(t, reloadFiles)
			if backend != "herdr" {
				return
			}
			// A stale exact row fails closed; the refreshed row finds the
			// completed birth and lays out no second tab.
			stale := headless.Chat{
				ID: seat.SessionID, SeatID: seat.SeatID, Backend: "herdr",
				Socket: seat.HerdrSocket, Pane: seat.HerdrPane,
			}
			if _, err := OpenDetachedTarget(ctx, stale, io.Discard, &runtime); err == nil {
				t.Fatal("stale exact row unexpectedly resolved")
			}
			current, found, err := db.SeatBySession(ctx, seat.SessionID)
			if err != nil || !found || current.HerdrPane != "w6:p9" {
				t.Fatalf("replacement ownership lost: seat=%+v found=%t err=%v", current, found, err)
			}
			current = nativeChatHost(t, db, current, true)
			stale.SeatID = current.SeatID
			stale.Socket, stale.Pane = current.HerdrSocket, current.HerdrPane
			result, err = OpenDetachedTarget(ctx, stale, io.Discard, &runtime)
			if err != nil || result.State != "live" || len(layouts) != 1 {
				t.Fatalf("retry duplicated birth: result=%+v err=%v layouts=%d", result, err, len(layouts))
			}
		})
	}
}

// plantNativeReloadFiles writes seatID's reload schedule record and log into
// sidDir, as a native /reload leaves them, and returns their paths.
func plantNativeReloadFiles(t *testing.T, sidDir, seatID string) []string {
	t.Helper()
	if err := os.MkdirAll(sidDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var planted []string
	for _, suffix := range []string{".pid", ".log"} {
		path := filepath.Join(sidDir, paths.SIDReloadHerdrPrefix+seatID+suffix)
		if err := os.WriteFile(path, []byte("1 1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		planted = append(planted, path)
	}
	return planted
}

func assertNativeReloadFilesGone(t *testing.T, planted []string) {
	t.Helper()
	for _, path := range planted {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("retired seat's reload file %s still there (stat err=%v)", path, err)
		}
	}
}

func TestOpenNativeDeadEndpointUsesBirthRule(t *testing.T) {
	for _, mode := range []string{"missing", "refused", "malformed", "permission"} {
		for _, door := range []string{"terminal", "detached"} {
			t.Run(mode+"/"+door, func(t *testing.T) {
				root := testjail.Fleet(t)
				socket := filepath.Join(testjail.ShortRoot(t), "absent.sock")
				switch mode {
				case "refused":
					listener, err := net.Listen("unix", socket)
					if err != nil {
						t.Fatal(err)
					}
					listener.(*net.UnixListener).SetUnlinkOnClose(false)
					if err := listener.Close(); err != nil {
						t.Fatal(err)
					}
				case "malformed", "permission":
					socket = nativeChatSocket(t, func(nativeChatRequest) (any, string) {
						if mode == "permission" {
							return nil, "permission_denied"
						}
						return map[string]any{"pane": 42}, ""
					})
				}
				runtime, db, seat := nativeChatSeat(t, socket, "lost")
				if mode == "missing" || mode == "refused" {
					seat = nativeChatHost(t, db, seat, false)
				}
				loaded, err := config.LoadRuntime("")
				if err != nil {
					t.Fatal(err)
				}
				runtime.Config = loaded.Config
				seedClaudeChat(t, root, seat.SessionID)
				t.Setenv("HERDR_ENV", "")
				t.Setenv("HERDR_SOCKET_PATH", "")
				t.Setenv("HERDR_PANE_ID", "")
				t.Setenv("TMUX", "")
				fake := &fakeOpenTmux{alive: map[string]bool{}}
				stubOpenExecutor(t, fake)
				var stdout, stderr bytes.Buffer
				code := 0
				if door == "terminal" {
					code = OpenID(context.Background(), seat.SessionID, &stdout, &stderr, &runtime)
				} else {
					_, err = OpenDetachedTarget(
						context.Background(),
						headless.Chat{
							ID:      seat.SessionID,
							SeatID:  seat.SeatID,
							Backend: "herdr",
							Engine:  pfmengine.Claude,
							Socket:  socket,
							Pane:    seat.HerdrPane,
						},
						&stderr,
						&runtime,
					)
					if err != nil {
						code = 1
					}
				}
				want := 0
				if mode == "malformed" || mode == "permission" {
					want = 1
				}
				if code != want || (want == 0 && len(fake.created) != 1) {
					t.Fatalf(
						"endpoint recovery code=%d want=%d created=%v err=%v stderr=%q",
						code,
						want,
						fake.created,
						err,
						stderr.String(),
					)
				}
			})
		}
	}
}

func TestDetachedNativeRereadsAfterConversationLock(t *testing.T) {
	for _, state := range []string{"starting", "retrying"} {
		t.Run(state, func(t *testing.T) {
			testjail.Fleet(t)
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				value, code, _ := nativeChatProof(req, "w9:p2")
				return value, code
			})
			runtime, db, seat := nativeChatSeat(t, socket, "lost")
			seat = nativeChatHost(t, db, seat, false)
			unlock, err := fleetdb.LockSeat(context.Background(), runtime.Paths, seat.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			wg.Add(2)
			results := make(chan error, 2)
			for range 2 {
				go func() {
					defer wg.Done()
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					result, err := detachedNative(ctx, seat, io.Discard, runtime)
					if err == nil && result.State != "live" {
						err = fmt.Errorf("result=%+v", result)
					}
					results <- err
				}()
			}
			current := seat
			current.SeatID = "replacement"
			current.HerdrPane = "w9:p2"
			current.State = "live"
			if state == "retrying" {
				current.State = "failed"
			}
			nativeChatHost(t, db, current, true)
			if err := db.DeleteSeat(context.Background(), seat.SeatID); err != nil {
				t.Fatal(err)
			}
			if err := unlock(); err != nil {
				t.Fatal(err)
			}
			wg.Wait()
			close(results)
			for err := range results {
				if err != nil {
					t.Fatalf("serialized open did not reread current supervisor: %v", err)
				}
			}
		})
	}
}

// A tmux chat attached in a Herdr pane leaves no other Herdr view of it on
// that server: each client its session's record lists in another pane is
// closed only when the bridge proof names that very client of this chat's
// tmux server and session (a pane already gone is no failure); an unproved view, one on
// another server or the caller's own pane is left alone, each unproved one
// named, and one failure never stops the others.
func TestOpenLiveTmuxRetiresOtherHerdrViews(t *testing.T) {
	const herdrSocket, tmuxSocket = "/run/herdr.sock", "/tmp/pfm-tmux/cc-chat"
	record := `{"engine":"claude","clients":{` +
		`"101":{"pane":"w6:p4","socket":"/run/herdr.sock"},` +
		`"102":{"pane":"w6:p5","socket":"/run/herdr.sock"},` +
		`"103":{"pane":"w6:p6","socket":"/run/other.sock"},` +
		`"104":{"pane":"w6:p3","socket":"/run/herdr.sock"},` +
		`"105":{"pane":"w6:p7","socket":"/run/herdr.sock"},` +
		`"106":{"pane":"w6:p8","socket":"/run/herdr.sock"},` +
		`"107":{"pane":"w6:p9","socket":"/run/herdr.sock"}}}`
	proofs := map[string]call.HerdrBridge{
		"w6:p4": {TmuxSocket: tmuxSocket, Session: "chat", ClientPID: 101},
		"w6:p7": {TmuxSocket: tmuxSocket, Session: "chat", ClientPID: 999},
		"w6:p8": {TmuxSocket: tmuxSocket, Session: "chat", ClientPID: 106},
		"w6:p9": {TmuxSocket: tmuxSocket, Session: "other", ClientPID: 107},
	}
	var proved, closed []string
	var stderr bytes.Buffer
	retireHerdrViews(context.Background(), tmuxSocket, herdrSocket, "w6:p3", herdrViewRetirer{
		session: "chat",
		stored:  func(context.Context) (string, error) { return record + "\n", nil },
		prove: func(_ context.Context, socket, pane string) (call.HerdrBridge, error) {
			proved = append(proved, socket+" "+pane)
			if proof, ok := proofs[pane]; ok {
				return proof, nil
			}
			return call.HerdrBridge{}, errors.New("no pfm tmux client in its foreground")
		},
		close: func(_ context.Context, _, pane string) error {
			closed = append(closed, pane)
			if pane == "w6:p8" {
				return &herdr.Refusal{Request: "pane.close", Code: "pane_not_found"}
			}
			return nil
		},
	}, &stderr)
	wantProved := []string{
		herdrSocket + " w6:p4", herdrSocket + " w6:p5", herdrSocket + " w6:p7", herdrSocket + " w6:p8",
		herdrSocket + " w6:p9",
	}
	if !slices.Equal(proved, wantProved) || !slices.Equal(closed, []string{"w6:p4", "w6:p8"}) {
		t.Fatalf("proved=%q closed=%q", proved, closed)
	}
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "w6:p5") || !strings.Contains(lines[0], "no pfm tmux client") ||
		!strings.Contains(lines[1], "w6:p7") || !strings.Contains(lines[1], "999") ||
		!strings.Contains(lines[2], "w6:p9") || !strings.Contains(lines[2], `session "other"`) {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

// A tmux chat opened from a Herdr pane retires the chat's other Herdr views
// only when its attach runs in the caller's own proved pane: a caller inside
// tmux and a caller pane hosting a chat of its own (an agent's pfm call from
// its native pane) retire nothing and ask Herdr nothing, an unproved pane is
// named, and a proved plain pane reads the chat's Herdr record, a failed read
// named.
func TestRetireOtherHerdrViewsSparesAViewFromAnAgentPane(t *testing.T) {
	for _, tc := range []struct {
		name, tmux, wantStderr    string
		callerHostsChat, unproved bool
	}{
		{name: "a plain caller pane", wantStderr: "other Herdr views of this chat stay: read @pfm_herdr"},
		{name: "a caller pane hosting a chat", callerHostsChat: true},
		{name: "a caller inside tmux", tmux: "/tmp/tmux-1/default,1,0"},
		{name: "a pane this process does not front", unproved: true, wantStderr: "ownership unproved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testjail.Fleet(t)
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			var asked []string
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				asked = append(asked, req.Method)
				if req.Method != "pane.process_info" {
					return nil, "unexpected_method"
				}
				pid := os.Getpid()
				if tc.unproved {
					pid = 1
				}
				return map[string]any{"type": "pane_process_info", "process_info": map[string]any{
					"pane_id": "w6:p3", "shell_pid": 1, "foreground_processes": []any{map[string]any{"pid": pid}},
				}}, ""
			})
			if tc.callerHostsChat {
				db := fleetdb.OpenSharedState(context.Background(), values)
				nativeChatCallerHostsChat(t, db, socket, "w6:p3")
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_SOCKET_PATH", socket)
			t.Setenv("HERDR_PANE_ID", "w6:p3")
			t.Setenv("TMUX", tc.tmux)
			var stderr bytes.Buffer
			retireOtherHerdrViews(
				context.Background(),
				compose.Row{Name: "chat", Socket: "cc-chat", PaneID: "%1"},
				config.Runtime{Paths: values},
				&stderr,
			)
			if tc.wantStderr == "" && stderr.Len() != 0 ||
				tc.wantStderr != "" && !strings.Contains(stderr.String(), tc.wantStderr) {
				t.Fatalf("stderr=%q, want %q", stderr.String(), tc.wantStderr)
			}
			if (tc.callerHostsChat || tc.tmux != "") && len(asked) != 0 {
				t.Fatalf("a caller that retires nothing asked Herdr %q", asked)
			}
		})
	}
}

type recordedReboot struct {
	row     compose.Row
	account int
	cache1H bool
}

// A live native chat born on another account than the picker's default gets
// the tmux rows' reload gate; yes reboots it onto the default account, every
// other answer and every unknown birth opens it as today.
func TestOpenNativeOffersAccountChange(t *testing.T) {
	const claudeBirthDir, otherDir = "/fixture/cc/1", "/fixture/cc/2"
	claudeEnv := func(dir string) []string { return []string{"A=b", "CLAUDE_CONFIG_DIR=" + dir} }
	for _, test := range []struct {
		name       string
		engine     pfmengine.ID
		env        []string
		primary    int
		gateAnswer bool
		gateErr    error
		rebootErr  error
		noRecipe   bool
		birthC1H   bool
		wantGate   bool
		wantBirth  int
		wantWant   int
		wantReboot bool
		wantCode   int
		wantAttach bool
		wantStderr string
	}{
		{
			name: "differs and yes reboots onto the default account", engine: pfmengine.Claude,
			env: claudeEnv(claudeBirthDir), primary: 2, gateAnswer: true,
			wantGate: true, wantBirth: 1, wantWant: 2, wantReboot: true, wantAttach: true,
		},
		{
			name: "differs and no opens as today", engine: pfmengine.Claude,
			env: claudeEnv(claudeBirthDir), primary: 2,
			wantGate: true, wantBirth: 1, wantWant: 2, wantAttach: true,
		},
		{
			name: "same account asks nothing", engine: pfmengine.Claude,
			env: claudeEnv(claudeBirthDir), primary: 1, gateAnswer: true, wantAttach: true,
		},
		{
			name: "recipe without an account asks nothing", engine: pfmengine.Claude,
			env: []string{"A=b"}, primary: 2, gateAnswer: true, wantAttach: true,
		},
		{
			name: "recipe naming an unconfigured directory asks nothing", engine: pfmengine.Claude,
			env: claudeEnv("/fixture/cc/9"), primary: 2, gateAnswer: true, wantAttach: true,
		},
		{
			name: "missing recipe asks nothing", engine: pfmengine.Claude,
			noRecipe: true, primary: 2, gateAnswer: true, wantAttach: true,
		},
		{
			name: "failed reboot is shown and the chat attaches as it is", engine: pfmengine.Claude,
			env: claudeEnv(claudeBirthDir), primary: 2, gateAnswer: true, rebootErr: errors.New("reload exploded"),
			wantGate: true, wantBirth: 1, wantWant: 2, wantReboot: true, wantAttach: true,
			wantStderr: "reload exploded",
		},
		{
			name: "failed gate stops the open and says why", engine: pfmengine.Claude,
			env: claudeEnv(claudeBirthDir), primary: 2, gateErr: errors.New("tty gone"),
			wantGate: true, wantBirth: 1, wantWant: 2, wantCode: 1, wantStderr: "tty gone",
		},
		{
			name: "a launch on the 1h cache names the cache change", engine: pfmengine.Claude,
			env: claudeEnv(claudeBirthDir), primary: 2, birthC1H: true,
			wantGate: true, wantBirth: 1, wantWant: 2, wantAttach: true,
		},
		{
			name: "codex seat on another codex account", engine: pfmengine.Codex,
			env: []string{"CODEX_HOME=" + otherDir}, primary: 2, gateAnswer: true,
			wantGate: true, wantBirth: 2, wantWant: 1, wantReboot: true, wantAttach: true,
		},
	} {
		for _, door := range []string{"row", "id"} {
			t.Run(test.name+"/"+door, func(t *testing.T) {
				testjail.Fleet(t)
				socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
					if value, code, ok := nativeChatProof(req, "w6:p3"); ok {
						return value, code
					}
					return nil, "unexpected_method"
				})
				runtime, db, seat := nativeChatSeat(t, socket, "live")
				seat.Engine = string(test.engine)
				seat = nativeChatHost(t, db, seat, true)
				runtime.Config = config.Config{
					Accounts: []config.Account{{ID: 1, ConfigDir: claudeBirthDir}, {ID: 2, ConfigDir: otherDir}},
					CodexAccounts: []config.CodexAccount{
						{ID: 1, Home: claudeBirthDir}, {ID: 2, Home: otherDir},
					},
				}
				if test.noRecipe {
					if err := db.DeleteSeatRun(seat.SeatID); err != nil {
						t.Fatal(err)
					}
				} else if err := db.WriteSeatRun(seat.SeatID, fleetdb.SeatRun{
					Binary: "/fixture/claude", Env: test.env, CWD: runtime.Paths.Home,
				}); err != nil {
					t.Fatal(err)
				}
				if err := fleet.SetPrimaryAccount(runtime.Paths, runtime.Config, test.primary); err != nil {
					t.Fatal(err)
				}
				if test.birthC1H {
					if err := fleetdb.RecordLaunch(context.Background(), runtime.Paths, fleetdb.Launch{
						SessionID: seat.SessionID, Engine: test.engine, Account: 1, Cache1H: true,
					}, 1); err != nil {
						t.Fatal(err)
					}
				}
				marker := filepath.Join(t.TempDir(), "attached")
				bin := t.TempDir()
				if err := testjail.WriteExecutable(
					filepath.Join(bin, "herdr"), []byte("#!/bin/sh\necho \"$@\" > "+marker+"\n"), 0o700,
				); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", bin)
				t.Setenv("HERDR_SOCKET_PATH", "")
				t.Setenv("HERDR_ENV", "")

				var gateRequests []action.GateRequest
				var reboots []recordedReboot
				previousGate, previousReboot := nativeAccountGate, runNativeReboot
				t.Cleanup(func() { nativeAccountGate, runNativeReboot = previousGate, previousReboot })
				nativeAccountGate = gateFunc(func(_ context.Context, request action.GateRequest) (bool, error) {
					gateRequests = append(gateRequests, request)
					return test.gateAnswer, test.gateErr
				})
				runNativeReboot = func(
					_ context.Context, _ paths.Values, row compose.Row, account int, cache1H bool,
					_ io.Writer, _ ...config.Runtime,
				) error {
					reboots = append(reboots, recordedReboot{row: row, account: account, cache1H: cache1H})
					return test.rebootErr
				}

				var stdout, stderr bytes.Buffer
				code := 0
				if door == "row" {
					code = OpenRow(
						context.Background(), nativeRow(seat), test.primary, false, "", &stdout, &stderr, &runtime,
					)
				} else {
					code = OpenID(context.Background(), seat.SessionID, &stdout, &stderr, &runtime)
				}
				if code != test.wantCode {
					t.Fatalf("code=%d want %d stderr=%s", code, test.wantCode, &stderr)
				}
				if (len(gateRequests) != 0) != test.wantGate || len(gateRequests) > 1 {
					t.Fatalf("gate requests=%+v want asked=%t", gateRequests, test.wantGate)
				}
				if test.wantGate {
					want := action.GateRequest{
						Name: seat.Name, BirthAccount: test.wantBirth, PrimaryAccount: test.wantWant,
						BirthCache1H: test.birthC1H,
					}
					if gateRequests[0] != want {
						t.Fatalf("gate request=%+v want %+v", gateRequests[0], want)
					}
				}
				if (len(reboots) == 1) != test.wantReboot || len(reboots) > 1 {
					t.Fatalf("reboots=%+v want one=%t", reboots, test.wantReboot)
				}
				if test.wantReboot {
					got := reboots[0]
					if got.account != test.wantWant || got.row.ID != seat.SessionID ||
						got.row.Socket != socket || got.row.PaneID != seat.HerdrPane ||
						got.row.CWD != runtime.Paths.Home || got.row.Name != seat.Name {
						t.Fatalf("reboot=%+v want account %d on %s %s", got, test.wantWant, socket, seat.HerdrPane)
					}
				}
				if _, err := os.Stat(marker); (err == nil) != test.wantAttach {
					t.Fatalf("attach marker err=%v want attached=%t stderr=%s", err, test.wantAttach, &stderr)
				}
				if test.wantStderr != "" && !strings.Contains(stderr.String(), test.wantStderr) {
					t.Fatalf("stderr=%q want %q", &stderr, test.wantStderr)
				}
			})
		}
	}
}

type gateFunc func(context.Context, action.GateRequest) (bool, error)

func (gate gateFunc) Confirm(ctx context.Context, request action.GateRequest) (bool, error) {
	return gate(ctx, request)
}

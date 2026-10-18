package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
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
	for _, backend := range []string{"tmux", "herdr", "herdr-focus-refused"} {
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
			if backend == "herdr-focus-refused" {
				var stderr bytes.Buffer
				result, err := detachedNative(ctx, seat, &stderr, runtime, true)
				if err != nil || result.State != "opened" || !strings.Contains(stderr.String(), "focus failed") {
					t.Fatalf("completed replacement=%+v err=%v stderr=%q", result, err, stderr.String())
				}
				if _, found, err := db.Seat(ctx, seat.SeatID); err != nil || found {
					t.Fatalf("obsolete seat retained: found=%t err=%v", found, err)
				}
				if _, err := db.ReadSeatRun(seat.SeatID); err == nil {
					t.Fatal("obsolete recipe retained")
				}
				// A stale exact row fails closed; the refreshed row finds the completed birth.
				stale := headless.Chat{
					ID: seat.SessionID, SeatID: seat.SeatID, Backend: "herdr",
					Socket: seat.HerdrSocket, Pane: seat.HerdrPane,
				}
				if _, err := OpenDetachedTarget(ctx, stale, &stderr, &runtime); err == nil {
					t.Fatal("stale exact row unexpectedly resolved")
				}
				current, found, err := db.SeatBySession(ctx, seat.SessionID)
				if err != nil || !found || current.HerdrPane != "w6:p9" {
					t.Fatalf("replacement ownership lost: seat=%+v found=%t err=%v", current, found, err)
				}
				current = nativeChatHost(t, db, current, true)
				stale.SeatID = current.SeatID
				stale.Socket, stale.Pane = current.HerdrSocket, current.HerdrPane
				result, err = OpenDetachedTarget(ctx, stale, &stderr, &runtime)
				if err != nil || result.State != "live" || len(layouts) != 1 {
					t.Fatalf("retry duplicated birth: result=%+v err=%v layouts=%d", result, err, len(layouts))
				}
				return
			}
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
		})
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
					result, err := detachedNative(ctx, seat, io.Discard, runtime, false)
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

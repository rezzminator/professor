package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
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

func TestOpenNativeUsesActualProcessesAndDurableAddress(t *testing.T) {
	for _, door := range []string{"row", "id", "detached"} {
		t.Run(door, func(t *testing.T) {
			testjail.Fleet(t)
			methods := make(chan string, 16)
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				methods <- req.Method
				switch req.Method {
				case "pane.get":
					return map[string]any{"pane": map[string]any{"pane_id": "w6:p3"}}, ""
				case "pane.process_info":
					return map[string]any{
						"type": "pane_process_info",
						"process_info": map[string]any{
							"pane_id": "w6:p3",
							"foreground_processes": []any{
								map[string]any{"pid": 42, "name": "claude", "argv0": "claude"},
							},
						},
					}, ""
				case "agent.focus":
					return map[string]any{"type": "pane_focused", "pane_id": "w6:p3"}, ""
				default:
					return nil, "unexpected_method"
				}
			})
			runtime, _, seat := nativeChatSeat(t, socket, "live")
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_SOCKET_PATH", socket)
			t.Setenv("HERDR_PANE_ID", "w6:p8")
			t.Setenv("TMUX", "")
			var stdout, stderr bytes.Buffer
			target := headless.Chat{
				Backend: "herdr",
				SeatID:  seat.SeatID,
				ID:      seat.SessionID,
				Engine:  pfmengine.Claude,
				Name:    seat.Name,
				Live:    true,
				Socket:  socket,
				Pane:    seat.HerdrPane,
			}
			switch door {
			case "row":
				code := OpenRow(
					context.Background(),
					compose.Row{
						Backend: "herdr",
						SeatID:  seat.SeatID,
						ID:      seat.SessionID,
						Kind:    compose.LiveClaude,
						Name:    seat.Name,
						Socket:  socket,
						PaneID:  seat.HerdrPane,
					},
					1,
					false,
					"",
					&stdout,
					&stderr,
					&runtime,
				)
				if code != 0 {
					t.Fatalf("native row code=%d err=%s", code, stderr.String())
				}
			case "id":
				code := OpenID(context.Background(), seat.SessionID, &stdout, &stderr, &runtime)
				if code != 0 {
					t.Fatalf("native ID code=%d err=%s", code, stderr.String())
				}
			case "detached":
				result, err := OpenDetachedTarget(context.Background(), target, &stderr, &runtime)
				if err != nil || result.Socket != socket || result.State != "live" ||
					!strings.Contains(result.Detail, seat.HerdrPane) {
					t.Fatalf("native detached result=%+v err=%v", result, err)
				}
			}
			if door != "detached" {
				found := false
				for len(methods) > 0 {
					if <-methods == "agent.focus" {
						found = true
					}
				}
				if !found {
					t.Fatal("live native open did not focus its pane")
				}
			}
		})
	}
}

func TestOpenNativeOutsideReportsLocationWhenAttachFails(t *testing.T) {
	testjail.Fleet(t)
	socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
		if req.Method == "pane.get" {
			return map[string]any{"pane": map[string]any{"pane_id": "w6:p3"}}, ""
		}
		return map[string]any{
			"type": "pane_process_info",
			"process_info": map[string]any{
				"pane_id":              "w6:p3",
				"foreground_processes": []any{map[string]any{"pid": 42, "name": "claude", "argv0": "claude"}},
			},
		}, ""
	})
	runtime, _, seat := nativeChatSeat(t, socket, "live")
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
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				switch req.Method {
				case "ping":
					return map[string]any{"type": "pong", "protocol": 22, "version": "fixture"}, ""
				case "pane.get":
					var pane string
					_ = json.Unmarshal(req.Params["pane_id"], &pane)
					if pane == "w6:p3" {
						return nil, "pane_not_found"
					}
					return map[string]any{"pane": map[string]any{"pane_id": pane}}, ""
				case "layout.apply":
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
			if backend == "herdr" {
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
				runtime, _, seat := nativeChatSeat(t, socket, "lost")
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

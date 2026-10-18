package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestOpenNativeFocusAcknowledgement(t *testing.T) {
	// Pinned pane.focus producer envelope with its required identity fields.
	const producer = `{"type":"pane_info","pane":{"terminal_id":"fixture-w6:p3","workspace_id":"w1","tab_id":"w1:t2","pane_id":"w1:p2","focused":true}}`
	const agentProducer = `{"type":"agent_info","agent":{"terminal_id":"fixture-w6:p3","agent_status":"idle","workspace_id":"w1","tab_id":"w1:t2","pane_id":"w1:p2","focused":true,"state_change_seq":0,"revision":1}}`
	for _, tc := range []struct {
		name, body, refusal, diagnostic string
		agent                           bool
	}{
		{name: "agent-producer", body: agentProducer, agent: true},
		{name: "agent-unfocused", body: strings.Replace(agentProducer, `"focused":true`, `"focused":false`, 1), agent: true, diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "agent-mismatched-pane", body: strings.Replace(agentProducer, `"pane_id":"w1:p2"`, `"pane_id":"w1:p9"`, 1), agent: true, diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "producer", body: producer},
		{name: "legacy", body: `{"type":"pane_focused","pane_id":"w1:p2"}`},
		{name: "missing-pane-object", body: `{"type":"pane_info"}`, diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "null-pane", body: `{"type":"pane_info","pane":null}`, diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "missing-pane", body: `{"type":"pane_info","pane":{"focused":true}}`, diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "missing-focused", body: `{"type":"pane_info","pane":{"pane_id":"w1:p2"}}`, diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "unfocused", body: strings.Replace(producer, `"focused":true`, `"focused":false`, 1), diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "mismatched-pane", body: strings.Replace(producer, `"pane_id":"w1:p2"`, `"pane_id":"w1:p9"`, 1), diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "wrong-type", body: strings.Replace(producer, `"type":"pane_info"`, `"type":"agent_info"`, 1), diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "malformed-payload", body: `"malformed"`, diagnostic: "decode Herdr focus"},
		{name: "malformed-focused", body: strings.Replace(producer, `"focused":true`, `"focused":"true"`, 1), diagnostic: "decode Herdr focus"},
		{name: "top-level-spoof", body: `{"type":"pane_info","pane_id":"w1:p2","focused":true}`, diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "refused", refusal: "focus_refused", diagnostic: "focus_refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testjail.Fleet(t)
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				if value, code, ok := nativeChatProof(req, "w1:p2"); ok {
					return value, code
				}
				switch req.Method {
				case "agent.focus":
					if string(req.Params["target"]) != `"w1:p2"` {
						t.Errorf("agent focus target=%s", req.Params["target"])
					}
					if tc.agent {
						return json.RawMessage(tc.body), tc.refusal
					}
					return nil, "agent_not_found"
				case "pane.focus":
					if string(req.Params["pane_id"]) != `"w1:p2"` {
						t.Errorf("focus target=%s", req.Params["pane_id"])
					}
					return json.RawMessage(tc.body), tc.refusal
				default:
					return nil, "unexpected_method"
				}
			})
			runtime, db, seat := nativeChatSeat(t, socket, "live")
			seat.HerdrPane = "w1:p2"
			seat = nativeChatHost(t, db, seat, true)
			if err := db.PutSeat(context.Background(), seat); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_SOCKET_PATH", socket)
			t.Setenv("HERDR_PANE_ID", "w1:p9")
			// A caller pane hosting a chat views a live chat by focus.
			nativeChatCallerHostsChat(t, db, socket, "w1:p9")
			var stdout, stderr bytes.Buffer
			code := OpenID(context.Background(), seat.SessionID, &stdout, &stderr, &runtime)
			if tc.diagnostic == "" {
				if code != 0 || stderr.Len() != 0 {
					t.Fatalf("valid focus acknowledgement: code=%d stderr=%s", code, stderr.String())
				}
			} else if code != 1 || !strings.Contains(stderr.String(), tc.diagnostic) {
				t.Fatalf(
					"invalid focus acknowledgement: code=%d stderr=%s want=%q",
					code,
					stderr.String(),
					tc.diagnostic,
				)
			}
		})
	}
}

// A live native chat opened from a Herdr pane on its server ends where that
// pane was: from another tab of the workspace it moves in beside the caller
// (pane.move), from the caller's own tab it trades places (pane.swap), and
// then the caller's own proved pane closes. Another workspace, a refused move
// or an unproved own pane move and close nothing; a caller pane hosting a chat
// views by focus.
func TestOpenNativeLiveMovesIntoThePickerTab(t *testing.T) {
	for _, tc := range []struct {
		name, caller, callerTab, moveReason, diagnostic string
		tmux                                            string
		swapReason, swapSource, moveTab                 string
		flat, unproved, callerHostsChat                 bool
		want                                            []string
	}{
		{
			name: "another tab", caller: "w6:p5", callerTab: "w6:t2",
			want: []string{
				`pane.move {"destination":{"split":"right","tab_id":"w6:t2","target_pane_id":"w6:p5","type":"tab"},"focus":true,"pane_id":"w6:p3"}`,
				`pane.close {"pane_id":"w6:p5"}`,
			},
		},
		{
			name: "the same tab", caller: "w6:p5", callerTab: "w6:t1",
			want: []string{
				`pane.swap {"source_pane_id":"w6:p3","target_pane_id":"w6:p5"}`,
				`pane.close {"pane_id":"w6:p5"}`,
			},
		},
		{
			name:   "the same tab, answered at the top level as the socket-api prose describes",
			caller: "w6:p5", callerTab: "w6:t1", flat: true,
			want: []string{
				`pane.swap {"source_pane_id":"w6:p3","target_pane_id":"w6:p5"}`,
				`pane.close {"pane_id":"w6:p5"}`,
			},
		},
		{
			name: "a swap Herdr answers cross_tab", caller: "w6:p5", callerTab: "w6:t1",
			swapReason: "cross_tab", diagnostic: `where it is: changed false (reason "cross_tab")`,
			want: []string{`pane.swap {"source_pane_id":"w6:p3","target_pane_id":"w6:p5"}`},
		},
		{
			name: "a swap answered for another pane", caller: "w6:p5", callerTab: "w6:t1",
			swapSource: "w6:p9", diagnostic: `does not confirm where it went: swapped "w6:p9"`,
			want: []string{`pane.swap {"source_pane_id":"w6:p3","target_pane_id":"w6:p5"}`},
		},
		{name: "another workspace", caller: "w7:p1", callerTab: "w7:t1", diagnostic: "workspace"},
		{
			name: "a zoomed tab", caller: "w6:p5", callerTab: "w6:t2", moveReason: "zoomed_tab", diagnostic: `where it is: changed false (reason "zoomed_tab")`,
			want: []string{
				`pane.move {"destination":{"split":"right","tab_id":"w6:t2","target_pane_id":"w6:p5","type":"tab"},"focus":true,"pane_id":"w6:p3"}`,
			},
		},
		{
			name: "a move Herdr answers into another tab", caller: "w6:p5", callerTab: "w6:t2",
			moveTab: "w6:t9", diagnostic: "does not confirm where it went: the chat's pane",
			want: []string{
				`pane.move {"destination":{"split":"right","tab_id":"w6:t2","target_pane_id":"w6:p5","type":"tab"},"focus":true,"pane_id":"w6:p3"}`,
			},
		},
		{name: "an unproved own pane", caller: "w6:p5", callerTab: "w6:t2", unproved: true, diagnostic: "ownership unproved"},
		{
			name: "a caller pane hosting a chat", caller: "w6:p5", callerTab: "w6:t2", callerHostsChat: true,
			want: []string{`agent.focus {"target":"w6:p3"}`},
		},
		{
			name: "a caller inside tmux", caller: "w6:p5", callerTab: "w6:t2", tmux: "/tmp/tmux-1/default,1,0",
			want: []string{`agent.focus {"target":"w6:p3"}`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testjail.Fleet(t)
			var mutations []string
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				var pane string
				_ = json.Unmarshal(req.Params["pane_id"], &pane)
				switch req.Method {
				case "pane.get":
					info := testjail.HerdrPaneInfo(pane)
					if pane == tc.caller {
						info["pane"].(map[string]any)["tab_id"] = tc.callerTab
					}
					return info, ""
				case "pane.process_info":
					if pane == tc.caller {
						foreground := []any{map[string]any{"pid": os.Getpid(), "name": "pfm"}}
						if tc.unproved {
							foreground = []any{map[string]any{"pid": 1, "name": "zsh"}}
						}
						return map[string]any{
							"type": "pane_process_info",
							"process_info": map[string]any{
								"pane_id": pane, "shell_pid": 1, "foreground_processes": foreground,
							},
						}, ""
					}
				}
				if value, code, ok := nativeChatProof(req, "w6:p3"); ok {
					return value, code
				}
				params, err := json.Marshal(req.Params)
				if err != nil {
					t.Errorf("encode %s params: %v", req.Method, err)
				}
				mutations = append(mutations, req.Method+" "+string(params))
				switch req.Method {
				case "pane.move":
					// PaneMoveResult nested under move_result, as Herdr's
					// ResponseResult::PaneMove serializes it (src/api/schema/response.rs L129-L131).
					movedTo := tc.callerTab
					if tc.moveTab != "" {
						movedTo = tc.moveTab
					}
					result := map[string]any{
						"changed": tc.moveReason == "", "previous_pane_id": "w6:p3", "previous_tab_id": "w6:t1",
						"pane": map[string]any{"pane_id": "w6:p3", "tab_id": movedTo},
					}
					if tc.moveReason != "" {
						result["reason"] = tc.moveReason
					}
					return map[string]any{"type": "pane_move", "move_result": result}, ""
				case "pane.swap":
					// PaneSwapResult (src/api/schema/panes.rs L521-L530) nested under swap
					// (response.rs L126-L128), as a live Herdr 0.9.5 answers; the source and
					// target echo the request's ids (src/app/api/panes.rs L902-L925).
					source := "w6:p3"
					if tc.swapSource != "" {
						source = tc.swapSource
					}
					result := map[string]any{
						"changed":         tc.swapReason == "",
						"source_pane_id":  source,
						"target_pane_id":  tc.caller,
						"focused_pane_id": source,
						"layout": map[string]any{
							"workspace_id": "w6",
							"tab_id":       tc.callerTab,
							"zoomed":       false,
						},
					}
					if tc.swapReason != "" {
						result["reason"] = tc.swapReason
					}
					if tc.flat {
						result["type"] = "pane_swap"
						return result, ""
					}
					return map[string]any{"type": "pane_swap", "swap": result}, ""
				case "pane.close":
					return map[string]any{"type": "ok"}, ""
				case "agent.focus":
					agent := map[string]any{"pane_id": "w6:p3", "focused": true}
					return map[string]any{"type": "agent_info", "agent": agent}, ""
				}
				return nil, "unexpected_method"
			})
			runtime, db, seat := nativeChatSeat(t, socket, "live")
			seat = nativeChatHost(t, db, seat, true)
			if tc.callerHostsChat {
				nativeChatCallerHostsChat(t, db, socket, tc.caller)
			}
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_SOCKET_PATH", socket)
			t.Setenv("HERDR_PANE_ID", tc.caller)
			t.Setenv("TMUX", tc.tmux)
			var stdout, stderr bytes.Buffer
			handled, code := openNativeRow(
				context.Background(),
				compose.Row{ID: seat.SessionID, SeatID: seat.SeatID, Backend: "herdr", Kind: compose.LiveClaude},
				0, false, &stdout, &stderr, runtime,
			)
			wantCode := 0
			if tc.diagnostic != "" {
				wantCode = 1
			}
			if !handled || code != wantCode || !strings.Contains(stderr.String(), tc.diagnostic) ||
				stdout.Len() != 0 || strings.Join(mutations, "\n") != strings.Join(tc.want, "\n") {
				t.Fatalf("handled=%v code=%d stderr=%q stdout=%q\nmutations=%q\nwant %q",
					handled, code, stderr.String(), stdout.String(), mutations, tc.want)
			}
		})
	}
}

// A live native chat born on another account than the picker's offers the
// reboot before it moves into the picker's tab, as a view does: the gate is
// asked once with the chat's birth account, and a refused offer moves the chat
// as it is.
func TestOpenNativeMoveHereOffersAccountChange(t *testing.T) {
	testjail.Fleet(t)
	var mutations []string
	socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
		var pane string
		_ = json.Unmarshal(req.Params["pane_id"], &pane)
		switch req.Method {
		case "pane.get":
			info := testjail.HerdrPaneInfo(pane)
			if pane == "w6:p5" {
				info["pane"].(map[string]any)["tab_id"] = "w6:t2"
			}
			return info, ""
		case "pane.process_info":
			if pane == "w6:p5" {
				return map[string]any{"type": "pane_process_info", "process_info": map[string]any{
					"pane_id": pane, "shell_pid": 1,
					"foreground_processes": []any{map[string]any{"pid": os.Getpid(), "name": "pfm"}},
				}}, ""
			}
		}
		if value, code, ok := nativeChatProof(req, "w6:p3"); ok {
			return value, code
		}
		mutations = append(mutations, req.Method)
		switch req.Method {
		case "pane.move":
			return map[string]any{"type": "pane_move", "move_result": map[string]any{
				"changed": true, "previous_pane_id": "w6:p3", "previous_tab_id": "w6:t1",
				"pane": map[string]any{"pane_id": "w6:p3", "tab_id": "w6:t2"},
			}}, ""
		case "pane.close":
			return map[string]any{"type": "ok"}, ""
		}
		return nil, "unexpected_method"
	})
	runtime, db, seat := nativeChatSeat(t, socket, "live")
	seat = nativeChatHost(t, db, seat, true)
	runtime.Config = config.Config{
		Accounts: []config.Account{{ID: 1, ConfigDir: "/fixture/cc/1"}, {ID: 2, ConfigDir: "/fixture/cc/2"}},
	}
	if err := db.WriteSeatRun(seat.SeatID, fleetdb.SeatRun{
		Binary: "/fixture/claude", Env: []string{"CLAUDE_CONFIG_DIR=/fixture/cc/1"}, CWD: runtime.Paths.Home,
	}); err != nil {
		t.Fatal(err)
	}
	var gateRequests []action.GateRequest
	previousGate := nativeAccountGate
	t.Cleanup(func() { nativeAccountGate = previousGate })
	nativeAccountGate = gateFunc(func(_ context.Context, request action.GateRequest) (bool, error) {
		gateRequests = append(gateRequests, request)
		return false, nil
	})
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_SOCKET_PATH", socket)
	t.Setenv("HERDR_PANE_ID", "w6:p5")
	t.Setenv("TMUX", "")
	var stdout, stderr bytes.Buffer
	handled, code := openNativeRow(
		context.Background(),
		compose.Row{ID: seat.SessionID, SeatID: seat.SeatID, Backend: "herdr", Kind: compose.LiveClaude},
		2, false, &stdout, &stderr, runtime,
	)
	want := action.GateRequest{Name: seat.Name, BirthAccount: 1, PrimaryAccount: 2}
	if !handled || code != 0 || len(gateRequests) != 1 || gateRequests[0] != want ||
		strings.Join(mutations, " ") != "pane.move pane.close" {
		t.Fatalf("handled=%v code=%d stderr=%q gate=%+v (want %+v) mutations=%q",
			handled, code, stderr.String(), gateRequests, want, mutations)
	}
}

// A dead native chat opened from a Herdr pane resumes in that pane: the seat is
// rebound to the caller's pane and the line its shell runs execs the seat host,
// with no new tab laid out. A caller pane hosting another live chat (an
// agent's own pfm call) is refused with nothing rebound.
func TestOpenNativeDeadResumesInTheCallerPane(t *testing.T) {
	for _, tc := range []struct {
		name            string
		callerHostsChat bool
	}{
		{name: "a plain caller pane"},
		{name: "a caller pane hosting a chat", callerHostsChat: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := testjail.Fleet(t)
			var methods, unexpected []string
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				methods = append(methods, req.Method)
				var pane string
				_ = json.Unmarshal(req.Params["pane_id"], &pane)
				switch req.Method {
				case "ping":
					return map[string]any{"type": "pong", "protocol": 22, "version": "fixture"}, ""
				case "pane.get":
					if pane == "w6:p3" {
						return nil, "pane_not_found"
					}
					return testjail.HerdrPaneInfo(pane), ""
				case "pane.process_info":
					return map[string]any{
						"type":         "pane_process_info",
						"process_info": map[string]any{"pane_id": pane, "shell_pid": 700001},
					}, ""
				case "pane.rename":
					var label string
					_ = json.Unmarshal(req.Params["label"], &label)
					return map[string]any{"pane": map[string]any{"pane_id": pane, "label": label}}, ""
				case "agent.rename":
					return nil, "agent_not_found"
				case "tab.get", "tab.rename":
					return map[string]any{
						"type": "tab_info",
						"tab":  map[string]any{"tab_id": "w6:t1", "label": "INTEGRATION", "pane_count": 1},
					}, ""
				case "notification.show":
					return map[string]any{"type": "ok"}, ""
				}
				unexpected = append(unexpected, req.Method)
				return nil, "unexpected_method"
			})
			runtime, db, seat := nativeChatSeat(t, socket, "lost")
			seat = nativeChatHost(t, db, seat, false)
			seedClaudeChat(t, root, seat.SessionID)
			if tc.callerHostsChat {
				nativeChatCallerHostsChat(t, db, socket, "w6:p9")
			}
			nativeChatRestoredCaller(t, true)
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_SOCKET_PATH", socket)
			t.Setenv("HERDR_PANE_ID", "w6:p9")
			t.Setenv("TMUX", "")
			var stdout, stderr bytes.Buffer
			handled, code := openNativeRow(
				context.Background(),
				compose.Row{ID: seat.SessionID, SeatID: seat.SeatID, Backend: "herdr", Kind: compose.LiveClaude},
				0, false, &stdout, &stderr, runtime,
			)
			current, found, err := db.Seat(context.Background(), seat.SeatID)
			if err != nil || !found {
				t.Fatalf("seat found=%t err=%v", found, err)
			}
			if len(unexpected) != 0 {
				t.Fatalf("unexpected Herdr methods %q (no new tab is laid out); stderr=%q",
					unexpected, stderr.String())
			}
			if tc.callerHostsChat {
				if !handled || code != 1 || stdout.Len() != 0 || current.HerdrPane != "w6:p3" ||
					!strings.Contains(stderr.String(), "hosts chat") ||
					strings.Contains(strings.Join(methods, " "), "rename") {
					t.Fatalf("handled=%v code=%d stdout=%q stderr=%q seat=%+v methods=%q",
						handled, code, stdout.String(), stderr.String(), current, methods)
				}
				return
			}
			if !handled || code != 0 || stdout.String() != "exec pfm internal seat-host "+seat.SeatID+"\n" ||
				current.HerdrPane != "w6:p9" || current.HerdrTerminalID != "fixture-w6:p9" {
				t.Fatalf("handled=%v code=%d stdout=%q stderr=%q seat=%+v methods=%q",
					handled, code, stdout.String(), stderr.String(), current, methods)
			}
		})
	}
}

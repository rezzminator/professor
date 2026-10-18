package call

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestHerdrCallNative(t *testing.T) {
	for _, test := range []struct {
		name, state, refusal, response, engine, status, detail string
		screen                                                 string
		code                                                   int
		force                                                  bool
	}{
		{name: "newline-draft-preserved", state: "idle", screen: "────────\n❯ \nprivate draft\n────────", status: "undelivered", code: CodeUndelivered, detail: "composer"},
		{name: "draft-preserved", state: "idle", screen: "❯ my unsent draft", status: "undelivered", code: CodeUndelivered, detail: "composer"},
		{name: "menu-preserved", state: "idle", screen: "  ❯ 1. Allow once\n    2. Deny", status: "undelivered", code: CodeUndelivered, detail: "composer"},

		{name: "composer-not-ready", state: "idle", screen: "Starting the engine...", status: "undelivered", code: CodeUndelivered, detail: "nothing was typed"},
		{name: "idle", state: "idle", status: "delivered"},
		{name: "done", state: "done", status: "delivered"},
		{name: "working", state: "working", status: "refused", code: CodeBusy, detail: "ABORT: target pane is busy; nothing was typed"},
		{name: "working-force", state: "working", force: true, status: "refused", code: CodeBusy, detail: "ABORT: target pane is busy; nothing was typed"},
		{name: "blocked", state: "blocked", status: "refused", code: CodeUndelivered, detail: "blocked"},
		{name: "unknown", state: "unknown", status: "refused", code: CodeUndelivered, detail: "unknown"},
		{name: "missing-state", status: "refused", code: CodeUndelivered, detail: "status"},
		{name: "not-ready", state: "idle", refusal: "agent_not_ready", status: "undelivered", code: CodeUndelivered, detail: "agent_not_ready"},
		{name: "blocked-after-idle", state: "idle", refusal: "agent_blocked", status: "undelivered", code: CodeUndelivered, detail: "agent_blocked"},
		{name: "lookup-refusal", state: "idle", refusal: "agent_not_found", status: "undelivered", code: CodeUndelivered, detail: "agent_not_found"},
		{name: "empty-refusal", state: "idle", refusal: "empty_agent_prompt", status: "undelivered", code: CodeUndelivered, detail: "empty_agent_prompt"},
		{name: "submit-failed", state: "idle", refusal: "agent_prompt_failed", status: "typed_unconfirmed", code: CodeUndelivered, detail: "agent_prompt_failed"},
		{name: "stalled", state: "idle", refusal: "agent_prompt_stalled", status: "typed_unconfirmed", code: CodeUndelivered, detail: "agent_prompt_stalled"},
		{name: "unknown-result", state: "idle", response: "surprise", status: "typed_unconfirmed", code: CodeUndelivered, detail: "surprise"},
		{name: "unsupported", state: "idle", engine: "invalid-engine", status: "refused", code: CodeUndelivered, detail: "unsupported"},
	} {
		t.Run(test.name, func(t *testing.T) {
			socket := newNativeSocket(t, func(request nativeRequest) (any, string) {
				if request.Method == "pane.read" {
					if request.Params["source"] != "visible" {
						t.Errorf("composer must be passive visible snapshot: %+v", request)
					}
					screen := test.screen
					if screen == "" {
						screen = "❯ "
					}
					return nativePaneRead(request, screen), ""
				}
				if request.Method == "agent.get" {
					return nativeAgent("agent_info", test.state), ""
				}
				kind := "agent_prompted"
				if test.response != "" {
					kind = test.response
				}
				return nativeAgent(kind, "working"), test.refusal
			})
			target := Target{
				Backend:        "herdr",
				SeatID:         "seat-2",
				HerdrWorkspace: "w1",
				SocketPath:     socket.path,
				Pane:           "w1:p2",
				Engine:         "cc",
				Session:        "recipient",
			}
			if test.engine != "" {
				target.Engine = test.engine
			}
			engine := nativeEngine(t, target)
			result, err := engine.CallTarget(
				context.Background(),
				target,
				Request{Target: "recipient", Message: "hello\nworld", ForceNow: test.force},
			)
			if err != nil {
				t.Fatal(err)
			}
			if test.refusal != "" && (result.Typed != (test.status == "typed_unconfirmed")) {
				t.Fatalf("refusal typed verdict: %+v", result)
			}
			if result.Code != test.code || result.Status != test.status ||
				!strings.Contains(result.Message, test.detail) {
				t.Fatalf(
					"native call = %+v; want status=%s code=%d detail=%q",
					result,
					test.status,
					test.code,
					test.detail,
				)
			}
			if test.code == 0 && (!result.Typed || result.Unsigned) {
				t.Fatalf("delivered result lost signed typed verdict: %+v", result)
			}
			if test.state == "idle" || test.state == "done" {
				if test.engine != "" || test.screen != "" {
					return
				}
				calls := socket.calls()
				if len(calls) != 3 || calls[0].Method != "agent.get" || calls[1].Method != "pane.read" ||
					calls[2].Method != "agent.prompt" ||
					calls[2].Params["target"] != "w1:p2" {
					t.Fatalf("native calls = %+v", calls)
				}
				text, _ := calls[len(calls)-1].Params["text"].(string)
				if !strings.HasPrefix(text, "hello\nworld") || !strings.Contains(text, "caller-name") {
					t.Fatalf("native payload lost bytes/signature: %q", text)
				}
			}
		})
	}
}

func TestHerdrNativePayloadSafety(t *testing.T) {
	for _, test := range []struct {
		name, message string
		unsigned      bool
		wantCode      int
	}{
		{name: "large-multiline", message: strings.Repeat("line\n", 2000)},
		{name: "command", message: "/status"},
		{name: "unsigned", message: "unsafe anonymous request", unsigned: true, wantCode: CodeUndelivered},
		{name: "compact-refused", message: "/compact", wantCode: CodeUndelivered},
	} {
		t.Run(test.name, func(t *testing.T) {
			socket := newNativeSocket(t, func(request nativeRequest) (any, string) {
				if request.Method == "pane.read" {
					return nativePaneRead(request, "❯ "), ""
				}
				if request.Method == "agent.get" {
					return nativeAgent("agent_info", "idle"), ""
				}
				return nativeAgent("agent_prompted", "working"), ""
			})
			target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "w1:p2", Engine: "cc"}
			engine := nativeEngine(t, target)
			if test.unsigned {
				engine.options.Sender = &Sender{}
			}
			result, err := engine.CallTarget(
				context.Background(),
				target,
				Request{Target: "recipient", Message: test.message, FileBacked: true},
			)
			if err != nil || result.Code != test.wantCode {
				t.Fatalf("native safety=%+v err=%v", result, err)
			}
			if test.wantCode != 0 {
				if result.Typed || result.Unsigned {
					t.Fatalf("refusal claims delivered input: %+v", result)
				}
				return
			}
			calls := socket.calls()
			if len(calls) != 3 || calls[2].Method != "agent.prompt" {
				t.Fatalf("native payload did not reach native prompt: %+v", calls)
			}
			text, _ := calls[len(calls)-1].Params["text"].(string)
			if !strings.HasPrefix(text, test.message) || (test.name == "command" && text != test.message) {
				t.Fatalf("native payload bytes changed: %q", text)
			}
		})
	}
}

func TestHerdrCallRaw(t *testing.T) {
	for _, test := range append([]herdrRawCase{
		{name: "multiline", id: pfmengine.Codex, body: "a\nb", wantPayload: "a\nb", wantTyped: true},
		{name: "controls-unframed", id: pfmengine.Codex, body: "one\ntwo\t\x03", wantPayload: "one\ntwo\t\x03", wantTyped: true},
		{name: "opencode", id: pfmengine.OpenCode, body: "a\nb", wantPayload: "a\nb", wantTyped: true},
		{name: "large", id: pfmengine.Codex, body: strings.Repeat("line\n", 2000), wantTyped: true},
		{name: "command", id: pfmengine.Codex, body: "/status", wantTyped: true},
		{name: "draft", id: pfmengine.Codex, body: "a\nb", before: "› unsent", wantCode: CodeUndelivered},
		{name: "busy", id: pfmengine.OpenCode, body: "a\nb", before: "› \nesc interrupt", wantCode: CodeBusy},
		{name: "menu", id: pfmengine.Codex, body: "a\nb", before: "› 1. Allow once\n  2. Deny", wantCode: CodeUndelivered},
		{name: "missing-composer", id: pfmengine.Codex, body: "a\nb", before: "Starting...", wantCode: CodeUndelivered},
		{name: "read-malformed", id: pfmengine.Codex, body: "a\nb", badMethod: "pane.read", wantCode: CodeUndelivered},
		{name: "paste-refused", id: pfmengine.Codex, body: "a\nb", refusal: "pane.send_input", wantCode: CodeUndelivered, wantTyped: true},
		{name: "paste-malformed", id: pfmengine.Codex, body: "a\nb", badMethod: "pane.send_input", wantCode: CodeUndelivered, wantTyped: true},
		{name: "enter-refused", id: pfmengine.Codex, body: "a\nb", refusal: "pane.send_keys", wantCode: CodeUndelivered, wantTyped: true},
		{name: "still-in-composer", id: pfmengine.Codex, body: "a\nb", after: "› [Pasted text #1 +2 lines]", wantCode: CodeUndelivered, wantTyped: true},
		{name: "no-confirmation", id: pfmengine.Codex, body: "a\nb", after: "unrelated screen", wantCode: CodeUndelivered, wantTyped: true},
	}, append(herdrRawStyleCases(), herdrOpenCodeCases()...)...) {
		t.Run(test.name, func(t *testing.T) {
			entered := false
			var payload string
			socket := newNativeSocket(t, func(request nativeRequest) (any, string) {
				if request.Method == test.refusal {
					return nil, "pane_send_failed"
				}
				if request.Method == test.badMethod {
					return map[string]any{"type": "unexpected"}, ""
				}
				switch request.Method {
				case "pane.read":
					if request.Params["format"] == "ansi" {
						if request.Params["strip_ansi"] != false {
							t.Errorf("styled capture must preserve ANSI: %+v", request.Params)
						}
						if test.styleRefused {
							return nil, "styled_read_failed"
						}
						if test.styleMalformed {
							return map[string]any{"type": "unexpected"}, ""
						}
						format := test.styleFormat
						if format == "" {
							format = "ansi"
						}
						screen := test.styled
						if screen == "" {
							screen = test.before
						}
						return map[string]any{"type": "pane_read", "read": map[string]any{
							"pane_id": "w1:p2", "text": screen, "format": format, "truncated": false,
						}}, ""
					}
					screen := test.before
					if screen == "" {
						screen = "› "
					}
					if entered {
						screen = test.after
						if screen == "" {
							screen = strings.TrimSuffix(strings.TrimPrefix(payload, "\x1b[200~"), "\x1b[201~") + "\n› "
						}
					}
					return nativePaneRead(request, screen), ""
				case "pane.send_input":
					payload, _ = request.Params["text"].(string)
				case "pane.send_keys":
					entered = true
				default:
					t.Errorf("raw engine used unexpected API %s", request.Method)
				}
				return map[string]any{"type": "ok"}, ""
			})
			target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "w1:p2", Engine: string(test.id)}
			engine := nativeEngine(t, target)
			// Pin row 9 at the raw wire boundary without adding a sender footer.
			engine.options.DisableSignature = test.wantPayload != ""
			prepared, err := engine.prepareLiveMessage(context.Background(), target, test.body, false)
			if err != nil {
				t.Fatal(err)
			}
			result, err := engine.CallTarget(context.Background(), target, Request{Message: test.body})
			if err != nil || result.Code != test.wantCode || result.Typed != test.wantTyped {
				t.Fatalf("raw call=%+v err=%v; want code=%d typed=%t", result, err, test.wantCode, test.wantTyped)
			}
			if test.styleRefused || test.styleMalformed || test.styleFormat != "" {
				if !strings.Contains(result.Message, "styling") || payload != "" {
					t.Fatalf("styled-read failure lost evidence or typed bytes: %+v payload=%q", result, payload)
				}
			}
			if test.wantCode == 0 {
				if result.Status != statusDelivered {
					t.Fatalf("raw status=%+v", result)
				}
				wantPayload := test.wantPayload
				if wantPayload == "" {
					wantPayload = prepared.Message
				}
				if payload != wantPayload {
					t.Fatalf("paste payload=%q want %q", payload, wantPayload)
				}
				calls := socket.calls()
				wantCalls := 4
				if test.styled != "" {
					wantCalls = 5
				}
				last := len(calls) - 1
				if len(calls) != wantCalls || calls[0].Method != "pane.read" ||
					calls[last-2].Method != "pane.send_input" ||
					calls[last-1].Method != "pane.send_keys" ||
					calls[last].Method != "pane.read" ||
					!reflect.DeepEqual(calls[last-1].Params["keys"], []any{"Enter"}) {
					t.Fatalf("raw call order=%+v", calls)
				}
			}
		})
	}
}

func TestHerdrNativeStyledComposer(t *testing.T) {
	for _, tc := range []struct {
		name, styled, format, refusal string
		delivered                     bool
	}{
		{"hint", "❯ \x1b[2mTry running tests\x1b[0m", "ansi", "", true},
		{"draft", "❯ Try running tests", "ansi", "", false},
		{"coloured-draft", "❯ \x1b[32mTry running tests\x1b[0m", "ansi", "", false},
		{"wrong-format", "❯ \x1b[2mTry running tests\x1b[0m", "text", "", false},
		{"read-error", "", "ansi", "read_failed", false},
		{"changed-continuation", "────────\n❯ \x1b[2mTry running tests\x1b[0m\nprivate draft\n────────", "ansi", "", false},
		{"changed-menu", "  ❯ 1. Allow once\n    2. Deny", "ansi", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := newNativeSocket(t, func(req nativeRequest) (any, string) {
				if req.Method == "agent.get" {
					return nativeAgent("agent_info", "idle"), ""
				}
				if req.Method == "pane.read" {
					if req.Params["source"] != "visible" {
						t.Errorf("active composer snapshot: %+v", req)
					}
					if req.Params["format"] == "ansi" {
						response := nativePaneRead(req, tc.styled)
						response["read"].(map[string]any)["format"] = tc.format
						return response, tc.refusal
					}
					return nativePaneRead(req, "❯ Try running tests"), ""
				}
				return nativeAgent("agent_prompted", "idle"), ""
			})
			target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "w1:p2", Engine: "cc"}
			got, err := nativeEngine(t, target).CallTarget(context.Background(), target, Request{Message: "hello"})
			if err != nil || (got.Code == 0) != tc.delivered || got.Typed != tc.delivered {
				t.Fatalf("styled composer result=%+v err=%v delivered=%t", got, err, tc.delivered)
			}
		})
	}
}

func TestHerdrOversizedBodyNotTyped(t *testing.T) {
	for _, engineID := range []string{"cc", "cx"} {
		for _, body := range []string{strings.Repeat("x", 1024*1024), strings.Repeat("\x00", 200000), strings.Repeat("界", 400000)} {
			t.Run(fmt.Sprintf("%s/%d", engineID, len(body)), func(t *testing.T) {
				socket := newNativeSocket(t, func(req nativeRequest) (any, string) {
					if req.Method == "pane.read" {
						return nativePaneRead(req, "❯ "), ""
					}
					if req.Method == "agent.get" {
						return nativeAgent("agent_info", "idle"), ""
					}
					return nativeAgent("agent_prompted", "idle"), ""
				})
				target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "w1:p2", Engine: engineID}
				got, err := nativeEngine(t, target).CallTarget(context.Background(), target, Request{Message: body})
				if err != nil || got.Typed || got.Code != CodeUndelivered || got.Status != "undelivered" ||
					!strings.Contains(got.Message, "request") {
					t.Fatalf("oversized body result=%+v err=%v", got, err)
				}
				for _, req := range socket.calls() {
					if req.Method == "agent.prompt" || req.Method == "pane.send_input" ||
						req.Method == "pane.send_text" {
						t.Fatalf("oversized body reached %s", req.Method)
					}
				}
			})
		}
	}
}

func TestHerdrTurnSubmissionAnchor(t *testing.T) {
	for _, identity := range []string{"valid-session", "absent-session", "null-session", "missing-terminal", "null-terminal", "empty-terminal"} {
		t.Run(identity, func(t *testing.T) {
			socket := newNativeSocket(t, func(req nativeRequest) (any, string) {
				if req.Method == "pane.read" {
					return nativePaneRead(req, "❯ "), ""
				}
				kind, seq := "agent_info", 4
				if req.Method == "agent.prompt" {
					kind, seq = "agent_prompted", 12
				}
				response := nativeAgent(kind, "idle")
				a := response["agent"].(map[string]any)
				a["state_change_seq"] = seq
				a["completion_seq"] = 3
				a["agent_session"] = map[string]string{
					"source": "hook",
					"agent":  "claude",
					"kind":   "session_id",
					"value":  "turn-session",
				}
				if req.Method == "agent.prompt" {
					switch identity {
					case "absent-session":
						delete(a, "agent_session")
					case "null-session":
						a["agent_session"] = nil
					case "missing-terminal":
						delete(a, "terminal_id")
						delete(a, "agent_session")
					case "null-terminal":
						a["terminal_id"], a["agent_session"] = nil, nil
					case "empty-terminal":
						a["terminal_id"], a["agent_session"] = "", nil
					}
				}
				return response, ""
			})
			target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "w1:p2", Engine: "cc"}
			engine := nativeEngine(t, target)
			got, err := engine.CallTarget(
				context.Background(),
				target,
				Request{Message: "hello", Then: []string{"follow up"}},
			)
			if strings.HasSuffix(identity, "terminal") {
				if err != nil || got.Code != CodeUndelivered || !got.Typed || got.Status != "typed_unconfirmed" ||
					!strings.Contains(
						got.Message,
						"terminal identity",
					) || len(engine.spawner.(*fakeSpawner).spawned()) != 0 {
					t.Fatalf("malformed prompt identity accepted: primary=%+v err=%v", got, err)
				}
				return
			}
			if err != nil || got.Code != 0 {
				t.Fatalf("primary=%+v err=%v", got, err)
			}
			spawned := engine.spawner.(*fakeSpawner).spawned()
			if len(spawned) != 1 {
				t.Fatalf("spawns=%v", spawned)
			}
			data, err := json.Marshal(spawned[0].herdrTurn)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			if fields["state_change_seq"] != float64(12) || fields["saw_working"] != false ||
				fields["terminal_id"] != "w1:p2" ||
				(fields["agent_session"] != nil) != (identity == "valid-session") {
				t.Fatalf("turn not anchored at validation snapshot: %s", data)
			}
		})
	}
}

func TestHerdrTurnPreservesIdentity(t *testing.T) {
	for _, change := range []string{"terminal", "session", "session-disappears", "session-source"} {
		t.Run(change, func(t *testing.T) {
			socket := newNativeSocket(t, func(_ nativeRequest) (any, string) {
				response := nativeAgent("agent_info", "idle")
				a := response["agent"].(map[string]any)
				a["completion_seq"] = 10
				a["state_change_seq"] = 10
				a["agent_session"] = map[string]string{
					"source": "hook",
					"agent":  "claude",
					"kind":   "session_id",
					"value":  "one",
				}
				switch change {
				case "terminal":
					a["terminal_id"] = "replacement"
				case "session":
					a["agent_session"].(map[string]string)["value"] = "two"
				case "session-disappears":
					delete(a, "agent_session")
				case "session-source":
					a["agent_session"].(map[string]string)["source"] = "process"
				}
				return response, ""
			})
			data, err := json.Marshal(
				map[string]any{
					"socket_path":      socket.path,
					"pane":             "w1:p2",
					"state_change_seq": 4,
					"terminal_id":      "w1:p2",
					"agent_session": map[string]string{
						"source": "hook",
						"agent":  "claude",
						"kind":   "session_id",
						"value":  "one",
					},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			var turn herdrTurn
			if err := json.Unmarshal(data, &turn); err != nil {
				t.Fatal(err)
			}
			target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "w1:p2", Engine: "cc"}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err = nativeEngine(t, target).waitHerdrTurn(ctx, target, turn)
			if err == nil || !strings.Contains(err.Error(), "identity") {
				t.Fatalf("replacement accepted: %v", err)
			}
		})
	}
}

func TestHerdrDialFailureNotTyped(t *testing.T) {
	for _, id := range []string{"cc", "cx"} {
		t.Run(id, func(t *testing.T) {
			var socket *nativeSocket
			socket = newNativeSocket(t, func(req nativeRequest) (any, string) {
				if req.Method == "agent.get" {
					return nativeAgent("agent_info", "idle"), ""
				}
				if req.Method == "pane.read" {
					if err := os.Rename(socket.path, socket.path+".parked"); err != nil {
						t.Error(err)
					}
					return nativePaneRead(req, "❯ "), ""
				}
				return map[string]string{"type": "ok"}, ""
			})
			// Keep the fixture's original cleanup and never remove a live socket.
			t.Cleanup(func() {
				if err := os.Rename(socket.path+".parked", socket.path); err != nil {
					t.Error(err)
				}
			})
			target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "w1:p2", Engine: id}
			got, err := nativeEngine(t, target).CallTarget(context.Background(), target, Request{Message: "hello"})
			if err != nil || got.Typed || got.Status != "undelivered" ||
				!strings.Contains(got.Message, "nothing was typed") {
				t.Fatalf("known pre-input failure result=%+v err=%v", got, err)
			}
		})
	}
}

func TestHerdrRawOwnershipRevokedBeforePaste(t *testing.T) {
	socket := newNativeSocket(t, func(request nativeRequest) (any, string) {
		if request.Method == "pane.read" {
			if err := os.Remove(filepath.Join(os.Getenv("PFM_PROC_ROOT"), "42", "cmdline")); err != nil {
				t.Error(err)
			}
			return nativePaneRead(request, "› "), ""
		}
		t.Errorf("input after revoked owner: %s", request.Method)
		return map[string]any{"type": "ok"}, ""
	})
	target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "w1:p2", Engine: "cx"}
	engine := nativeEngine(t, target)
	engine.options.DisableSignature = true
	got, err := engine.CallTarget(context.Background(), target, Request{Message: "hello"})
	if err != nil || got.Typed || got.Status != statusUndelivered || got.Code != CodeUndelivered ||
		!strings.Contains(got.Message, "nothing was typed") || !strings.Contains(got.Message, "ownership unproved") {
		t.Fatalf("pre-input revocation receipt=%+v err=%v", got, err)
	}
}

// A stale and a canonical address of one moved Herdr seat serialize on one
// call lock, and the receipt names the pane the message was delivered to.
func TestHerdrMovedSeatSerializesOnSeatLock(t *testing.T) {
	socket := newNativeSocket(t, func(request nativeRequest) (any, string) {
		switch request.Method {
		case "pane.read":
			return nativePaneRead(request, "❯ "), ""
		case "agent.get":
			return nativeAgent("agent_info", "idle"), ""
		}
		return nativeAgent("agent_prompted", "working"), ""
	})
	canonical := Target{
		Backend: "herdr", SeatID: "seat-2", HerdrWorkspace: "w1",
		SocketPath: socket.path, Pane: "w1:p2", Engine: "cc", Session: "recipient",
	}
	stale := canonical
	stale.Pane = "w1:p1"
	engine := nativeEngine(t, canonical)
	engine.options.LockTimeout, engine.options.LockPoll = 100*time.Millisecond, time.Millisecond
	held, refusal := engine.lockTarget(context.Background(), canonical)
	if refusal != "" {
		t.Fatal(refusal)
	}
	for _, target := range []Target{stale, canonical} {
		got, err := engine.CallTarget(context.Background(), target, Request{Target: "recipient", Message: "hello"})
		if err != nil || got.Typed || got.Code != CodeUndelivered ||
			!strings.Contains(got.Message, "could not acquire call lock") {
			t.Fatalf("%s while the seat's lock is held: %+v err=%v", target.Pane, got, err)
		}
	}
	if calls := socket.calls(); len(calls) != 0 {
		t.Fatalf("a locked seat was driven: %+v", calls)
	}
	held.release()
	got, err := engine.CallTarget(context.Background(), stale, Request{Target: "recipient", Message: "hello"})
	if err != nil || got.Status != statusDelivered || got.Pane != "w1:p2" || got.SocketPath != socket.path {
		t.Fatalf("stale-address receipt=%+v err=%v; want delivered naming w1:p2", got, err)
	}
	calls := socket.calls()
	if len(calls) == 0 || calls[len(calls)-1].Method != "agent.prompt" ||
		calls[len(calls)-1].Params["target"] != "w1:p2" {
		t.Fatalf("native calls=%+v", calls)
	}
}

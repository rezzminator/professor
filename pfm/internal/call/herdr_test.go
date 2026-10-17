package call

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

type nativeRequest struct {
	ID     string         `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}

type nativeSocket struct {
	path     string
	mu       sync.Mutex
	requests []nativeRequest
}

func newNativeSocket(t *testing.T, respond func(nativeRequest) (any, string)) *nativeSocket {
	t.Helper()
	root, err := os.MkdirTemp("", "call-herdr-")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(root, "s"))
	if err != nil {
		_ = os.RemoveAll(root)
		t.Fatal(err)
	}
	socket := &nativeSocket{path: filepath.Join(root, "s")}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() {
					if err := conn.Close(); err != nil {
						t.Errorf("close Herdr fixture connection: %v", err)
					}
				}()
				var request nativeRequest
				if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&request); err != nil {
					t.Errorf("decode Herdr request: %v", err)
					return
				}
				socket.mu.Lock()
				socket.requests = append(socket.requests, request)
				socket.mu.Unlock()
				result, refusal := respond(request)
				envelope := map[string]any{"id": request.ID, "result": result}
				if refusal != "" {
					delete(envelope, "result")
					envelope["error"] = map[string]string{"code": refusal, "message": "native refusal " + refusal}
				}
				if err := json.NewEncoder(conn).Encode(envelope); err != nil {
					t.Errorf("reply to Herdr request: %v", err)
				}
			}()
		}
	}()
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close Herdr listener: %v", err)
		}
		<-done
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove Herdr fixture: %v", err)
		}
	})
	return socket
}

func (socket *nativeSocket) calls() []nativeRequest {
	socket.mu.Lock()
	defer socket.mu.Unlock()
	return append([]nativeRequest(nil), socket.requests...)
}

func nativeAgent(kind, state string) map[string]any {
	return map[string]any{
		"type":  kind,
		"agent": map[string]any{"pane_id": "p2", "terminal_id": "w1:p2", "agent": "claude", "agent_status": state},
	}
}

func nativePaneRead(request nativeRequest, text string) map[string]any {
	return map[string]any{"type": "pane_read", "read": map[string]any{
		"pane_id": request.Params["pane_id"], "text": text, "truncated": false,
	}}
}

func nativeEngine(t *testing.T, target Target) *Engine {
	t.Helper()
	engine := newTestEngine(t, "cc-1-2-3", &fakeTmux{capture: "conversation\n❯ ", submitOnEnter: true})
	engine.names = nativeNames{target: target}
	return engine.WithIdentity(
		resolve.Identity{
			Backend:    "tmux",
			SocketPath: "/caller/tmux",
			Pane:       "%8",
			Engine:     "cc",
			Session:    "caller-session",
			ID:         "caller-uuid",
		},
		"caller-name",
	)
}

type nativeNames struct{ target Target }

func (names nativeNames) ResolveName(context.Context, string, string) (Target, int, string, error) {
	return names.target, 0, "", nil
}

func TestHerdrCallNative(t *testing.T) {
	for _, test := range []struct {
		name, state, refusal, response, engine, status, detail string
		screen                                                 string
		code                                                   int
		force                                                  bool
	}{
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
		{name: "not-ready", state: "idle", refusal: "agent_not_ready", status: "typed_unconfirmed", code: CodeUndelivered, detail: "agent_not_ready"},
		{name: "stalled", state: "idle", refusal: "agent_prompt_stalled", status: "typed_unconfirmed", code: CodeUndelivered, detail: "agent_prompt_stalled"},
		{name: "unknown-result", state: "idle", response: "surprise", status: "typed_unconfirmed", code: CodeUndelivered, detail: "surprise"},
		{name: "unsupported", state: "idle", engine: "invalid-engine", status: "refused", code: CodeUndelivered, detail: "unsupported"},
	} {
		t.Run(test.name, func(t *testing.T) {
			socket := newNativeSocket(t, func(request nativeRequest) (any, string) {
				if request.Method == "pane.read" {
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
				Pane:           "p2",
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
					calls[2].Params["target"] != "p2" {
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

func TestHerdrCaptureNative(t *testing.T) {
	for _, test := range []struct {
		name      string
		lines     int
		text      any
		kind      string
		want      string
		bad       bool
		wantLines int
		pane      string
	}{
		{name: "recent", lines: 2, text: "first\nsecond\nthird\n", kind: "pane_read", want: "second\nthird", wantLines: 2},
		{name: "empty", lines: 4, text: "", kind: "pane_read", wantLines: 4},
		{name: "qualified-pane", lines: 1, text: "tail", kind: "pane_read", want: "tail", wantLines: 1, pane: "w1:p2"},
		{name: "bounded", lines: 2000, text: "tail", kind: "pane_read", want: "tail", wantLines: 1000},
		{name: "default", text: "tail", kind: "pane_read", want: "tail", wantLines: 1000},
		{name: "null", lines: 1, text: nil, kind: "pane_read", bad: true, wantLines: 1},
		{name: "wrong-type", lines: 1, text: 7, kind: "pane_read", bad: true, wantLines: 1},
		{name: "wrong-result", lines: 1, text: "tail", kind: "surprise", bad: true, wantLines: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			socket := newNativeSocket(t, func(_ nativeRequest) (any, string) {
				return map[string]any{
					"type": test.kind,
					"read": map[string]any{
						"pane_id":      "p2",
						"workspace_id": "w1",
						"text":         test.text,
						"truncated":    false,
					},
				}, ""
			})
			target := Target{
				Backend:        "herdr",
				SeatID:         "seat-2",
				HerdrWorkspace: "w1",
				SocketPath:     socket.path,
				Pane:           "p2",
				Engine:         "cc",
			}
			if test.pane != "" {
				target.Pane = test.pane
			}
			got, text, code, detail, err := nativeEngine(
				t,
				target,
			).Capture(context.Background(), "recipient", test.lines)
			if test.bad {
				if err == nil && code == 0 {
					t.Fatalf("malformed native capture read as success: %q", text)
				}
				return
			}
			if err != nil || code != 0 || text != test.want {
				t.Fatalf("capture = %q code=%d detail=%q err=%v", text, code, detail, err)
			}
			if got != target {
				t.Fatalf("capture projection lost seat: %+v want %+v", got, target)
			}
			calls := socket.calls()
			if len(calls) != 1 || calls[0].Method != "pane.read" || calls[0].Params["source"] != "recent" ||
				calls[0].Params["pane_id"] != target.Pane ||
				calls[0].Params["lines"] != float64(test.wantLines) {
				t.Fatalf("capture request=%+v", calls)
			}
			if test.lines > 1000 && !strings.Contains(detail, "1000") {
				t.Fatalf("bounded capture hid limit: %q", detail)
			}
		})
	}
}

func TestHerdrKeysNative(t *testing.T) {
	socket := newNativeSocket(t, func(_ nativeRequest) (any, string) { return map[string]any{"type": "ok"}, "" })
	target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "p2", Engine: "cc"}
	engine := nativeEngine(t, target)
	want := []string{"Enter", "Escape", "C-c", "Down", "Tab"}
	for _, key := range want {
		if err := engine.SendKey(context.Background(), target, key); err != nil {
			t.Fatal(err)
		}
	}
	calls := socket.calls()
	if len(calls) != len(want) {
		t.Fatalf("keys sent %d requests, want %d", len(calls), len(want))
	}
	for i, call := range calls {
		if call.Method != "pane.send_keys" || call.Params["pane_id"] != "p2" ||
			!reflect.DeepEqual(call.Params["keys"], []any{want[i]}) {
			t.Fatalf("key %d request=%+v", i, call)
		}
	}
}

func TestHerdrCallMalformedAndUnreachable(t *testing.T) {
	for _, test := range []struct {
		name    string
		result  any
		refusal string
	}{
		{name: "null"},
		{name: "missing-agent", result: map[string]any{"type": "agent_info"}},
		{name: "wrong-get-result", result: map[string]any{"type": "surprise"}},
		{name: "refused-get", refusal: "pane_not_found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			socket := newNativeSocket(t, func(nativeRequest) (any, string) { return test.result, test.refusal })
			target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "p2", Engine: "cc"}
			result, err := nativeEngine(
				t,
				target,
			).CallTarget(context.Background(), target, Request{Target: "recipient", Message: "hello"})
			if err == nil && result.Code == 0 {
				t.Fatalf("native get error became delivery: %+v", result)
			}
			if result.Typed {
				t.Fatalf("get error claims typed: %+v", result)
			}
		})
	}
	target := Target{Backend: "herdr", SocketPath: filepath.Join(t.TempDir(), "missing"), Pane: "p2", Engine: "cc"}
	result, err := nativeEngine(
		t,
		target,
	).CallTarget(context.Background(), target, Request{Target: "recipient", Message: "hello"})
	if !strings.Contains(fmt.Sprint(err)+result.Message, "herdr") || result.Code == CodeUnknown ||
		result.Code == CodeDead {
		t.Fatalf("unreachable socket lost cause: result=%+v err=%v", result, err)
	}
}

func TestHerdrThenNativeBoundary(t *testing.T) {
	for _, test := range []struct {
		name       string
		sawWorking bool
		states     []string
		seq        []int
		completion []int
		refusal    string
		wantCode   int
		wantDetail string
	}{
		{name: "observed-working", sawWorking: true, states: []string{"idle"}, seq: []int{4}, completion: []int{3}},
		{name: "fast-completed", states: []string{"idle"}, seq: []int{6}, completion: []int{4}},
		{name: "stale-then-working-idle", states: []string{"idle", "working", "idle"}, seq: []int{4, 5, 6}, completion: []int{3, 3, 4}},
		{name: "blocked", states: []string{"blocked"}, seq: []int{5}, completion: []int{3}, wantCode: CodeUndelivered, wantDetail: "blocked"},
		{name: "unknown", states: []string{"unknown"}, seq: []int{5}, completion: []int{3}, wantCode: CodeUndelivered, wantDetail: "unknown"},
		{name: "stale-idle-not-boundary", states: []string{"idle", "idle"}, seq: []int{4, 4}, completion: []int{3, 3}, wantCode: CodeUndelivered, wantDetail: "boundary"},
		{name: "timeout", states: []string{"idle"}, seq: []int{4}, completion: []int{3}, refusal: "timeout", wantCode: CodeUndelivered, wantDetail: "timeout"},
	} {
		t.Run(test.name, func(t *testing.T) {
			i := 0
			socket := newNativeSocket(t, func(request nativeRequest) (any, string) {
				if request.Method == "agent.wait" {
					n := min(i, len(test.states)-1)
					i++
					result := nativeAgent("agent_info", test.states[n])
					a := result["agent"].(map[string]any)
					a["state_change_seq"] = test.seq[n]
					a["completion_seq"] = test.completion[n]
					return result, test.refusal
				}
				if request.Method == "pane.read" {
					return nativePaneRead(request, "❯ "), ""
				}
				if request.Method == "agent.get" {
					return nativeAgent("agent_info", "idle"), ""
				}
				return nativeAgent("agent_prompted", "working"), ""
			})
			target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "p2", Engine: "cc"}
			engine := nativeEngine(t, target)
			turn, _ := json.Marshal(
				map[string]any{
					"socket_path":      socket.path,
					"pane":             "p2",
					"state_change_seq": 4,
					"completion_seq":   3,
					"saw_working":      test.sawWorking,
				},
			)
			engine.env = &paths.MapEnv{Values: map[string]string{"CHAT_THEN_HERDR_TURN": string(turn)}}
			result, err := engine.DeliverThen(
				context.Background(),
				ThenWait{
					Backend:    "herdr",
					SocketPath: socket.path,
					Target:     "p2",
					Engine:     "cc",
					Steers:     []string{"resume the port", "then run the tests"},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.Code != test.wantCode || !strings.Contains(result.Message, test.wantDetail) {
				t.Fatalf("native waiter=%+v; want code=%d detail=%q", result, test.wantCode, test.wantDetail)
			}
			calls := socket.calls()
			if len(calls) == 0 || calls[0].Method != "agent.wait" {
				t.Fatalf("native waiter skipped agent.wait: %+v", calls)
			}
			if test.wantCode == 0 {
				if !result.Typed || result.Steers != 1 || calls[len(calls)-1].Method != "agent.prompt" ||
					calls[len(calls)-1].Params["target"] != "p2" {
					t.Fatalf("native steer target/chain lost: result=%+v calls=%+v", result, calls)
				}
				if text, _ := calls[len(calls)-1].Params["text"].(string); !strings.HasPrefix(text, "resume the port") {
					t.Fatalf("steer payload=%q", text)
				}
			}
		})
	}
}

func TestHerdrAllCallDirections(t *testing.T) {
	for _, senderBackend := range []string{"tmux", "herdr"} {
		for _, targetBackend := range []string{"tmux", "herdr"} {
			t.Run(senderBackend+"-to-"+targetBackend, func(t *testing.T) {
				socket := newNativeSocket(t, func(request nativeRequest) (any, string) {
					if request.Method == "pane.read" {
						return nativePaneRead(request, "❯ "), ""
					}
					if request.Method == "agent.get" {
						return nativeAgent("agent_info", "idle"), ""
					}
					return nativeAgent("agent_prompted", "working"), ""
				})
				target := Target{Backend: targetBackend, SocketPath: socket.path, Pane: "p2", Engine: "cc"}
				caller := resolve.Identity{
					Backend:    senderBackend,
					SocketPath: "/different/caller",
					Pane:       "p8",
					Engine:     "cc",
					Session:    "caller-session",
					ID:         "caller-uuid",
				}
				engine := nativeEngine(t, target).WithIdentity(caller, "caller-name")
				result, err := engine.CallTarget(
					context.Background(),
					target,
					Request{Target: "recipient", Message: "explicit request", Then: []string{"follow up"}},
				)
				if err != nil || result.Code != 0 || !result.Typed {
					t.Fatalf("forward call=%+v err=%v", result, err)
				}
				spawned := engine.spawner.(*fakeSpawner).spawned()
				if len(spawned) != 1 || spawned[0].Backend != targetBackend || spawned[0].SocketPath != socket.path ||
					spawned[0].Target != "p2" {
					t.Fatalf("steering handoff lost target backend: %+v", spawned)
				}
				// A reverse answer is an ordinary second call. Its target backend,
				// not the original caller's or ambient daemon's backend, routes it.
				target.Backend = senderBackend
				reverse := nativeEngine(
					t,
					target,
				).WithIdentity(resolve.Identity{Backend: targetBackend, SocketPath: "/different/recipient", Pane: "p9", Engine: "cc", Session: "recipient-session", ID: "recipient-uuid"}, "recipient-name")
				result, err = reverse.CallTarget(
					context.Background(),
					target,
					Request{Target: "caller", Message: "explicit answer"},
				)
				if err != nil || result.Code != 0 || !result.Typed {
					t.Fatalf("reverse call=%+v err=%v", result, err)
				}
				wantNative := 0
				if targetBackend == "herdr" {
					wantNative += 3
				}
				if senderBackend == "herdr" {
					wantNative += 3
				}
				if len(socket.calls()) != wantNative {
					t.Fatalf("backend directions made %d native requests, want %d", len(socket.calls()), wantNative)
				}
			})
		}
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
			target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "p2", Engine: "cc"}
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

func TestHerdrResolvedAdapterFacades(t *testing.T) {
	t.Run("malformed literal acknowledgement", func(t *testing.T) {
		socket := newNativeSocket(t, func(nativeRequest) (any, string) {
			return map[string]any{"type": "pane_info"}, ""
		})
		target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "w1:p2"}
		engine := nativeEngine(t, target)
		if err := engine.SendLiteral(context.Background(), target, "draft"); err == nil {
			t.Fatal("malformed acknowledgement reported literal delivery")
		}
	})

	t.Run("bounded full capture reports limitation", func(t *testing.T) {
		socket := newNativeSocket(t, func(nativeRequest) (any, string) {
			return map[string]any{"type": "pane_read", "read": map[string]any{
				"pane_id": "p2", "workspace_id": "w1", "truncated": true, "text": "tail",
			}}, ""
		})
		target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "w1:p2"}
		got, err := nativeEngine(t, target).CaptureTarget(context.Background(), target, FullScrollback)
		if err != nil || !strings.Contains(got, "limited to 1000 lines; truncated=true") ||
			!strings.HasSuffix(got, "tail") {
			t.Fatalf("bounded capture hid its limitation: %q %v", got, err)
		}
	})

	socket := newNativeSocket(t, func(request nativeRequest) (any, string) {
		if request.Method == "pane.read" {
			return map[string]any{
				"type": "pane_read",
				"read": map[string]any{
					"pane_id":      "p2",
					"workspace_id": "w1",
					"truncated":    false,
					"text":         "native label\n❯ ",
				},
			}, ""
		}
		return map[string]any{"type": "ok"}, ""
	})
	target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "w1:p2"}
	engine := nativeEngine(t, target)
	if err := engine.SendLiteral(context.Background(), target, "literal\ntext"); err != nil {
		t.Fatal(err)
	}
	text, err := engine.CaptureTarget(context.Background(), target, 17)
	if err != nil || text != "native label\n❯ " {
		t.Fatalf("resolved capture=%q err=%v", text, err)
	}
	calls := socket.calls()
	if len(calls) != 2 || calls[0].Method != "pane.send_text" || calls[0].Params["text"] != "literal\ntext" ||
		calls[0].Params["pane_id"] != target.Pane ||
		calls[1].Method != "pane.read" ||
		calls[1].Params["lines"] != float64(17) {
		t.Fatalf("resolved adapters=%+v", calls)
	}
}

// Herdr matches the current status against until, not an unrequested snapshot
// from a script. A completed turn before the next wait can therefore be missed.
func TestHerdrThenHonorsProducerUntil(t *testing.T) {
	for _, test := range []struct {
		name       string
		states     []string
		seq        []int
		wantCode   int
		wantDetail string
	}{
		{"working-observed", []string{"idle", "working", "idle"}, []int{4, 5, 6}, 0, "agent_prompted"},
		{"quick-working-idle", []string{"idle", "idle"}, []int{4, 6}, CodeUndelivered, "timeout"},
		{"quick-working-idle-no-sequence", []string{"idle", "idle"}, []int{0, 0}, CodeUndelivered, "timeout"},
		{"stale-idle", []string{"idle", "idle"}, []int{4, 4}, CodeUndelivered, "timeout"},
		{"blocked", []string{"idle", "blocked"}, []int{4, 5}, CodeUndelivered, "blocked"},
	} {
		t.Run(test.name, func(t *testing.T) {
			i := 0
			socket := newNativeSocket(t, func(request nativeRequest) (any, string) {
				if request.Method == "pane.read" {
					return nativePaneRead(request, "❯ "), ""
				}
				if request.Method == "agent.get" {
					return nativeAgent("agent_info", "idle"), ""
				}
				if request.Method != "agent.wait" {
					return nativeAgent("agent_prompted", "working"), ""
				}
				n := min(i, len(test.states)-1)
				i++
				until, ok := request.Params["until"].([]any)
				if !ok {
					t.Errorf("wait omitted producer until: %+v", request)
					return nil, "invalid_params"
				}
				if ms, ok := request.Params["timeout_ms"].(float64); !ok || ms < 1 || ms > 200 {
					t.Errorf("wait lost its bounded timeout: %+v", request)
				}
				for _, allowed := range until {
					if allowed == test.states[n] {
						result := nativeAgent("agent_info", test.states[n])
						result["agent"].(map[string]any)["state_change_seq"] = test.seq[n]
						return result, ""
					}
				}
				// The logical producer deadline expires with no matching event.
				// In quick cases the working event preceded this request entirely.
				return nil, "timeout"
			})
			target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "p2", Engine: "cc"}
			engine := nativeEngine(t, target)
			engine.options.ThenMin = 200 * time.Millisecond
			engine.options.ThenBusyTries, engine.options.ThenIdleTries = 0, 0
			engine.options.ThenSettle = 0
			turn, err := json.Marshal(herdrTurn{SocketPath: socket.path, Pane: "p2", StateChangeSeq: 4})
			if err != nil {
				t.Fatal(err)
			}
			engine.env = &paths.MapEnv{Values: map[string]string{herdrTurnEnv: string(turn)}}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			started := time.Now()
			result, err := engine.DeliverThen(ctx, ThenWait{
				Backend: "herdr", SocketPath: socket.path,
				Target: "p2", Engine: "cc", Steers: []string{"resume the port"},
			})
			if err != nil || result.Code != test.wantCode || !strings.Contains(result.Message, test.wantDetail) ||
				time.Since(started) > time.Second {
				t.Fatalf("until-honoring waiter=%+v err=%v elapsed=%v", result, err, time.Since(started))
			}
			if test.wantCode != 0 &&
				(result.Status != statusUndelivered || result.Typed || result.Steers != 0 || !strings.Contains(result.Message, "steer kept in the log")) {
				t.Fatalf("missed turn lost truthful undelivered verdict: %+v", result)
			}
			if test.wantCode == 0 && (result.Status != statusDelivered || !result.Typed) {
				t.Fatalf("observed boundary lost delivered verdict: %+v", result)
			}
			if i > 3 {
				t.Fatalf("wait was not bounded: %d snapshots", i)
			}
		})
	}
}

// The producer may block when a quick working→idle completed before the second
// request. Exercise the actual socket deadline, not just an immediate refusal.
func TestHerdrThenProducerWaitDeadline(t *testing.T) {
	root, err := os.MkdirTemp("", "call-wait-")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(root, "s")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	release, done := make(chan struct{}), make(chan struct{})
	requests := make(chan nativeRequest, 2)
	go func() {
		defer close(done)
		for i := 0; i < 2; i++ {
			conn, err := listener.Accept()
			if err != nil {
				t.Errorf("accept native wait: %v", err)
				return
			}
			func() {
				defer func() {
					if err := conn.Close(); err != nil {
						t.Errorf("close native wait: %v", err)
					}
				}()
				var request nativeRequest
				if err := json.NewDecoder(conn).Decode(&request); err != nil {
					t.Errorf("decode native wait: %v", err)
					return
				}
				requests <- request
				if i == 1 {
					// The working event was before this request; the producer's
					// current idle cannot match until=[working,blocked].
					<-release
					return
				}
				result := nativeAgent("agent_info", "idle")
				result["agent"].(map[string]any)["state_change_seq"] = 4
				if err := json.NewEncoder(conn).Encode(map[string]any{"id": request.ID, "result": result}); err != nil {
					t.Errorf("reply stale idle: %v", err)
				}
			}()
		}
	}()
	t.Cleanup(func() {
		close(release)
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
		<-done
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	target := Target{Backend: "herdr", SocketPath: socket, Pane: "p2", Engine: "cc"}
	engine := nativeEngine(t, target)
	engine.options.ThenMin = 60 * time.Millisecond
	engine.options.ThenBusyTries, engine.options.ThenIdleTries = 0, 0
	engine.options.ThenSettle = 0
	turn, err := json.Marshal(herdrTurn{SocketPath: socket, Pane: "p2", StateChangeSeq: 4})
	if err != nil {
		t.Fatal(err)
	}
	engine.env = &paths.MapEnv{Values: map[string]string{herdrTurnEnv: string(turn)}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	result, err := engine.DeliverThen(
		ctx,
		ThenWait{Backend: "herdr", SocketPath: socket, Target: "p2", Engine: "cc", Steers: []string{"resume the port"}},
	)
	elapsed := time.Since(started)
	if err != nil || result.Status != statusUndelivered || result.Code != CodeUndelivered || result.Typed ||
		result.Steers != 0 ||
		!strings.Contains(result.Message, "herdr agent.wait") ||
		!strings.Contains(result.Message, "steer kept in the log") {
		t.Fatalf("blocking producer lost visible undelivered outcome: result=%+v err=%v", result, err)
	}
	if elapsed < 20*time.Millisecond || elapsed > time.Second {
		t.Fatalf("native wait did not honor its own 60ms bound: %v", elapsed)
	}
	if len(requests) != 2 {
		t.Fatalf("native boundary requests=%d; want stale idle then until-working", len(requests))
	}
	<-requests
	second := <-requests
	if second.Method != "agent.wait" || !reflect.DeepEqual(second.Params["until"], []any{"working", "blocked"}) {
		t.Fatalf("producer was not waiting on a missed transient state: %+v", second)
	}
}

func TestHerdrCallRaw(t *testing.T) {
	for _, test := range []struct {
		name, body, before, after, refusal, badMethod string
		wantPayload                                   string
		id                                            pfmengine.ID
		wantCode                                      int
		wantTyped                                     bool
	}{
		{name: "multiline", id: pfmengine.Codex, body: "a\nb", wantPayload: "\x1b[200~a\nb\x1b[201~", wantTyped: true},
		{name: "opencode", id: pfmengine.OpenCode, body: "a\nb", wantPayload: "\x1b[200~a\nb\x1b[201~", wantTyped: true},
		{name: "large", id: pfmengine.Codex, body: strings.Repeat("line\n", 2000), wantTyped: true},
		{name: "command", id: pfmengine.Codex, body: "/status", wantTyped: true},
		{name: "draft", id: pfmengine.Codex, body: "a\nb", before: "› unsent", wantCode: CodeUndelivered},
		{name: "busy", id: pfmengine.OpenCode, body: "a\nb", before: "› \nesc interrupt", wantCode: CodeBusy},
		{name: "menu", id: pfmengine.Codex, body: "a\nb", before: "› 1. Allow once\n  2. Deny", wantCode: CodeUndelivered},
		{name: "missing-composer", id: pfmengine.Codex, body: "a\nb", before: "Starting...", wantCode: CodeUndelivered},
		{name: "read-malformed", id: pfmengine.Codex, body: "a\nb", badMethod: "pane.read", wantCode: CodeUndelivered},
		{name: "paste-refused", id: pfmengine.Codex, body: "a\nb", refusal: "pane.send_text", wantCode: CodeUndelivered, wantTyped: true},
		{name: "paste-malformed", id: pfmengine.Codex, body: "a\nb", badMethod: "pane.send_text", wantCode: CodeUndelivered, wantTyped: true},
		{name: "enter-refused", id: pfmengine.Codex, body: "a\nb", refusal: "pane.send_keys", wantCode: CodeUndelivered, wantTyped: true},
		{name: "still-in-composer", id: pfmengine.Codex, body: "a\nb", after: "› [Pasted text #1 +2 lines]", wantCode: CodeUndelivered, wantTyped: true},
		{name: "no-confirmation", id: pfmengine.Codex, body: "a\nb", after: "unrelated screen", wantCode: CodeUndelivered, wantTyped: true},
	} {
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
				case "pane.send_text":
					payload, _ = request.Params["text"].(string)
				case "pane.send_keys":
					entered = true
				default:
					t.Errorf("raw engine used unexpected API %s", request.Method)
				}
				return map[string]any{"type": "ok"}, ""
			})
			target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "p2", Engine: string(test.id)}
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
			if test.wantCode == 0 {
				if result.Status != statusDelivered {
					t.Fatalf("raw status=%+v", result)
				}
				wantPayload := test.wantPayload
				if wantPayload == "" {
					wantPayload = "\x1b[200~" + prepared.Message + "\x1b[201~"
				}
				if payload != wantPayload {
					t.Fatalf("paste payload=%q want %q", payload, wantPayload)
				}
				calls := socket.calls()
				if len(calls) != 4 || calls[0].Method != "pane.read" || calls[1].Method != "pane.send_text" ||
					calls[2].Method != "pane.send_keys" ||
					calls[3].Method != "pane.read" ||
					!reflect.DeepEqual(calls[2].Params["keys"], []any{"Enter"}) {
					t.Fatalf("raw call order=%+v", calls)
				}
			}
		})
	}
}

func TestHerdrThenRawBoundary(t *testing.T) {
	for _, test := range []struct {
		name       string
		screens    []string
		sawWorking bool
		wantCode   int
	}{
		{name: "observed-working", screens: []string{"› \nesc interrupt", "› "}},
		{name: "already-observed", screens: []string{"› "}, sawWorking: true},
		{name: "stale-idle", screens: []string{"› "}, wantCode: CodeUndelivered},
		{name: "blocked", screens: []string{"› 1. Allow once\n  2. Deny"}, wantCode: CodeUndelivered},
	} {
		t.Run(test.name, func(t *testing.T) {
			i, entered := 0, false
			socket := newNativeSocket(t, func(request nativeRequest) (any, string) {
				switch request.Method {
				case "pane.read":
					screen := test.screens[min(i, len(test.screens)-1)]
					i++
					if entered {
						screen = "resume the port\n› "
					}
					return nativePaneRead(request, screen), ""
				case "pane.send_keys":
					entered = true
				case "pane.send_text":
				}
				return map[string]any{"type": "ok"}, ""
			})
			target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "p2", Engine: string(pfmengine.OpenCode)}
			engine := nativeEngine(t, target)
			engine.options.ThenBusyTries, engine.options.ThenIdleTries, engine.options.ThenIdlePoll = 3, 2, time.Millisecond
			turn, err := json.Marshal(
				map[string]any{"socket_path": socket.path, "pane": "p2", "raw": true, "saw_working": test.sawWorking},
			)
			if err != nil {
				t.Fatal(err)
			}
			engine.env = &paths.MapEnv{Values: map[string]string{herdrTurnEnv: string(turn)}}
			result, err := engine.DeliverThen(
				context.Background(),
				ThenWait{
					Backend:    "herdr",
					SocketPath: socket.path,
					Target:     "p2",
					Engine:     string(pfmengine.OpenCode),
					Steers:     []string{"resume the port"},
				},
			)
			if err != nil || result.Code != test.wantCode || (test.wantCode == 0 && !result.Typed) {
				t.Fatalf("raw steer=%+v err=%v; want code=%d", result, err, test.wantCode)
			}
			if test.wantCode == 0 {
				calls := socket.calls()
				if calls[len(calls)-2].Method != "pane.send_keys" {
					t.Fatalf("raw steer not submitted: %+v", calls)
				}
			}
		})
	}
}

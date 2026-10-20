package call

import (
	"context"
	"testing"
)

func TestScheduleRetryAdmitsOnlyIdleEmptyComposers(t *testing.T) {
	for _, test := range []struct {
		name, screen string
		allowed      bool
	}{
		{name: "busy empty", screen: "Working (7s · 8 tokens)\n❯ "},
		{name: "idle draft", screen: "conversation\n❯ existing draft"},
		{name: "idle empty", screen: "conversation\n❯ ", allowed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeTmux{capture: test.screen, stashClears: true, submitOnEnter: true}
			engine := newTestEngine(t, "cc-retry", fake)
			result, err := engine.Call(
				context.Background(),
				Request{Target: "chat", Message: "retry the wake", RequireIdle: true},
			)
			if test.allowed {
				if err != nil || result.Code != 0 || !result.Typed {
					t.Fatalf("idle empty retry was refused: %+v err=%v", result, err)
				}
				return
			}
			if err != nil || result.Typed || result.Code == 0 || fake.literal != "" || len(fake.keys) != 0 {
				t.Fatalf(
					"retry touched busy/draft target: result=%+v err=%v literal=%q keys=%v",
					result,
					err,
					fake.literal,
					fake.keys,
				)
			}
		})
	}
}

func TestScheduleRetryReadsNativeAgentBeforeQueueing(t *testing.T) {
	for _, engineName := range []string{"claude", "opencode"} {
		t.Run(engineName, func(t *testing.T) {
			socket := newNativeSocket(t, func(request nativeRequest) (any, string) {
				switch request.Method {
				case "agent.get":
					return nativeAgent("agent_info", "working"), ""
				case "pane.read":
					return nativePaneRead(request, "esc interrupt\n❯ "), ""
				default:
					return map[string]any{"type": "ok"}, ""
				}
			})
			target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "w1:p2", Engine: engineName}
			result, err := nativeEngine(t, target).Call(context.Background(), Request{
				Target: "chat", Message: "retry the wake", RequireIdle: true,
			})
			if err != nil || result.Typed || result.Code == 0 {
				t.Fatalf("retry queued on working native agent: %+v err=%v", result, err)
			}
			for _, request := range socket.calls() {
				if request.Method == "pane.send_input" || request.Method == "agent.prompt" ||
					request.Method == "pane.send_key" {
					t.Fatalf("retry mutated working agent through %s", request.Method)
				}
			}
		})
	}
}

func TestScheduleNeedsNativeStartedTurnProof(t *testing.T) {
	for _, test := range []struct {
		name, state, want    string
		sequence, completion int
	}{
		{name: "working", state: "working", sequence: 5, completion: 3, want: "delivered"},
		{name: "unchanged idle acknowledgement", state: "idle", sequence: 4, completion: 3, want: "delivered_unproven"},
		{name: "only idle state advanced", state: "idle", sequence: 6, completion: 3, want: "delivered_unproven"},
		{name: "fast completed turn", state: "idle", sequence: 6, completion: 6, want: "delivered"},
	} {
		t.Run(test.name, func(t *testing.T) {
			socket := newNativeSocket(t, func(request nativeRequest) (any, string) {
				switch request.Method {
				case "pane.read":
					return nativePaneRead(request, "❯ "), ""
				case "agent.get":
					result := nativeAgent("agent_info", "idle")
					result["agent"].(map[string]any)["state_change_seq"] = 4
					result["agent"].(map[string]any)["completion_seq"] = 3
					return result, ""
				default:
					result := nativeAgent("agent_prompted", test.state)
					result["agent"].(map[string]any)["state_change_seq"] = test.sequence
					result["agent"].(map[string]any)["completion_seq"] = test.completion
					return result, ""
				}
			})
			target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "w1:p2", Engine: "claude"}
			result, err := nativeEngine(
				t,
				target,
			).Call(context.Background(), Request{Target: "chat", Message: "scheduled wake"})
			if err != nil || result.Code != 0 || !result.Typed || result.Status != test.want {
				t.Fatalf("native submission proof: %+v err=%v; want %s", result, err, test.want)
			}
		})
	}
}

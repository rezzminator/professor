package inject

import (
	"context"
	"strings"
	"testing"
)

// The pane Claude Code 2.1.280 draws while background agents run and the
// keyboard sits in their panel: the composer is empty, the cursor ❯ is on the
// main agent row, and the footer is the panel's own hint.
const agentPanelFocusedCapture = "✻ Waiting for 7 background agents to finish\n" +
	"──────────────────────── OPUS ─\n❯ \n────────────────────────\n" +
	"  ◆ Opus 5.5 (1M context) │ 6%\n  ↑/↓ to select · Enter to view\n❯ ⏺ main\n" +
	"  ◯ general-purpose (+3)  T1: port the pieces      12m 59s · ↓ 60.4k tokens"

// The same pane once Escape hands the keyboard back to the composer.
const agentPanelReleasedCapture = "✻ Waiting for 7 background agents to finish\n" +
	"──────────────────────── OPUS ─\n❯ \n────────────────────────\n" +
	"  ◆ Opus 5.5 (1M context) │ 6%\n  ⏵⏵ bypass permissions on · 1 shell · ← for agents\n  ⏺ main\n" +
	"  ◯ general-purpose (+3)  T1: port the pieces      12m 59s · ↓ 60.4k tokens"

// panelTmux is a fakeTmux whose agents panel gives up focus on the first
// Escape when released is set, and keeps it when released is empty.
type panelTmux struct {
	*fakeTmux
	released string
}

func (pane *panelTmux) SendKey(ctx context.Context, socketPath, target, key string) error {
	if key == "Escape" && pane.released != "" {
		pane.mu.Lock()
		pane.capture = pane.released
		pane.mu.Unlock()
		pane.released = ""
	}
	return pane.fakeTmux.SendKey(ctx, socketPath, target, key)
}

func TestAgentPanelFocused(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		capture string
		want    bool
	}{
		{"the panel holds the keyboard", agentPanelFocusedCapture, true},
		{"the composer holds the keyboard", agentPanelReleasedCapture, false},
		{"an agent row with no panel hint is older activity, not focus", "❯ \n❯ ● qa-cortex  Verifying results", false},
		{
			"the hint quoted far up the scrollback is not the footer",
			"  ↑/↓ to select · Enter to view\n" + strings.Repeat("conversation\n", 20) + "❯ \n❯ ⏺ mainly a typed note",
			false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := agentPanelFocused(test.capture); got != test.want {
				t.Fatalf("agentPanelFocused = %t, want %t", got, test.want)
			}
		})
	}
}

func TestInjectReturnsFocusFromTheAgentsPanelBeforeTyping(t *testing.T) {
	fake := &fakeTmux{capture: agentPanelFocusedCapture, submitOnEnter: true}
	engine := newTestEngine(t, "cc-agent-panel", fake)
	engine.tmux = &panelTmux{fakeTmux: fake, released: agentPanelReleasedCapture}
	result, err := engine.Inject(context.Background(), Request{Target: "chat", Message: "steer after the gate"})
	if err != nil || result.Code != 0 {
		t.Fatalf("a focused agents panel blocked delivery: result=%+v err=%v", result, err)
	}
	if len(fake.keys) == 0 || fake.keys[0] != "Escape" {
		t.Fatalf("focus must return to the composer before any other key: keys=%q", fake.keys)
	}
}

func TestInjectRefusesByNameWhenTheAgentsPanelKeepsFocus(t *testing.T) {
	fake := &fakeTmux{capture: agentPanelFocusedCapture, submitOnEnter: true}
	engine := newTestEngine(t, "cc-agent-panel-stuck", fake)
	engine.tmux = &panelTmux{fakeTmux: fake}
	result, err := engine.Inject(context.Background(), Request{Target: "chat", Message: "steer after the gate"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != CodeUndelivered || !strings.Contains(result.Message, "agents panel") {
		t.Fatalf("a panel that keeps focus must be refused by name: result=%+v", result)
	}
	if len(fake.literals) != 0 || contains(fake.keys, "Enter") {
		t.Fatalf("nothing may be typed into a focused panel: literals=%q keys=%q", fake.literals, fake.keys)
	}
}

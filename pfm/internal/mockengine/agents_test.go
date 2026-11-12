package mockengine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestAgentWaitLine(t *testing.T) {
	one := []agentRow{{line: "❯ ● a  running (background)"}}
	two := append([]agentRow{{line: "❯ ● b  running (background)"}}, one...)
	for _, row := range []struct {
		name, rule, footer string
		rows               []agentRow
		want               string
	}{
		{"claude with no agent", claudeComposerRule, "", nil, ""},
		{"claude with one agent", claudeComposerRule, "", one, "\r\n✻ Waiting for 1 background agent to finish"},
		{"claude with two agents", claudeComposerRule, "", two, "\r\n✻ Waiting for 2 background agents to finish"},
		{"claude mid-turn", claudeComposerRule, "✻ Thinking… (1s · esc to interrupt)", one, ""},
		{"codex draws no box", "", "", one, ""},
	} {
		screen := &pane{glyph: "❯", rule: row.rule, footer: row.footer, agents: agentPanel{rows: row.rows}}
		if got := screen.agentWaitLine(); got != row.want {
			t.Errorf("%s: agentWaitLine() = %q, want %q", row.name, got, row.want)
		}
		if frame := screen.render(); row.want != "" && pfmcall.IsFooterBusy(pfmengine.Claude, frame) {
			t.Errorf("%s: the wait row reads as a busy turn:\n%s", row.name, frame)
		}
	}
}

func TestClaudeGatedAgentsEndWithTheirOwnTurn(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	gateA, gateB := filepath.Join(fix.root, "agent-a"), filepath.Join(fix.root, "agent-b")
	for _, gate := range []string{gateA, gateB} {
		if err := os.WriteFile(gate, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	prompts := filepath.Join(fix.root, "prompts.jsonl")
	writeProjectHooks(t, fix.work, `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"cat >> `+
		prompts+`; echo >> `+prompts+`"}]}]}}`)
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepBackgroundAgent, Name: "a", UntilGone: gateA},
		{Type: StepBackgroundAgent, Name: "b", UntilGone: gateB},
		{Type: StepTurn, Reply: "dispatched"},
		{Type: StepMenu, Options: []string{"Yes", "No"}, Selected: 1},
	}})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("start two agents")
	frame := session.waitFrame("both agents waited on", func(frame string) bool {
		return strings.Contains(frame, "⏺ dispatched") &&
			strings.Contains(frame, "✻ Waiting for 2 background agents to finish\r\n"+claudeComposerRule)
	})
	if pfmcall.IsFooterBusy(pfmengine.Claude, frame) {
		t.Fatalf("agents behind an idle composer read as a busy turn:\n%s", frame)
	}
	session.typeLine("ask me")
	const question = "Enter to confirm · Esc to cancel"
	session.waitFrame("the question open", func(frame string) bool { return strings.Contains(frame, question) })
	if err := os.Remove(gateA); err != nil {
		t.Fatal(err)
	}
	frame = session.waitFrame("agent a's row gone under the open question", func(frame string) bool {
		return !strings.Contains(frame, "● a  running") && strings.Contains(frame, "● b  running")
	})
	if !strings.Contains(frame, question) || strings.Contains(frame, `Agent "a" finished`) {
		t.Fatalf("agent a's end closed the question or reported under it:\n%s", frame)
	}
	session.typeRaw("\r")
	session.waitFrame("agent a's result after the answer, b still waited on", func(frame string) bool {
		return strings.Contains(frame, `⏺ Agent "a" finished`) &&
			strings.Contains(frame, "✻ Waiting for 1 background agent to finish\r\n"+claudeComposerRule)
	})
	if err := os.Remove(gateB); err != nil {
		t.Fatal(err)
	}
	frame = session.waitFrame("agent b's result", func(frame string) bool {
		return strings.Contains(frame, `⏺ Agent "b" finished`)
	})
	var submitted []string
	for _, line := range strings.Split(strings.TrimSpace(readFile(t, prompts)), "\n") {
		var payload hookPayload
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			t.Fatalf("UserPromptSubmit payload %q in %s: %v", line, prompts, err)
		}
		submitted = append(submitted, payload.Prompt)
	}
	notified := func(name string) string {
		return "<task-notification>\n<task-id>" + name + "</task-id>\n<status>completed</status>\n" +
			`<summary>Agent "` + name + `" completed</summary>` + "\n</task-notification>"
	}
	if want := []string{"start two agents", "ask me", notified("a"), notified("b")}; !slices.Equal(submitted, want) {
		t.Fatalf("UserPromptSubmit prompts in %s = %q, want %q", prompts, submitted, want)
	}
	if strings.Contains(frame, "Waiting for") || strings.Contains(frame, "(background)") {
		t.Fatalf("an ended agent is still drawn:\n%s", frame)
	}
}

// writeProjectHooks writes the project settings.json a Claude pane in work loads its hooks from.
func writeProjectHooks(t *testing.T, work, settings string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(work, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".claude", "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestClaudeAgentsPanelHoldsTheKeyboard pins Claude Code 2.1.280's agents panel: the left arrow on an empty
// composer over a running agent focuses it (the hint and the cursor on the bare main row that
// internal/call/agentpanel.go reads), typed text goes nowhere while it holds the keyboard, and Escape hands
// it back to the composer; with no agent running the arrow focuses nothing.
func TestClaudeAgentsPanelHoldsTheKeyboard(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	gate := filepath.Join(fix.root, "agent")
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fix.write(Scenario{SessionID: fixtureSession, BusyMS: intPtr(0), Steps: []Step{
		{Type: StepTurn, Reply: "plain"},
		{Type: StepBackgroundAgent, Name: "scout", UntilGone: gate},
		{Type: StepTurn, Reply: "dispatched"},
		{Type: StepTurn, Reply: "over the panel"},
	}})
	session := fix.startTUI("claude", claudeArgs(), nil)
	session.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, "❯") })
	session.typeLine("first")
	session.waitFrame("the plain reply", func(frame string) bool { return strings.Contains(frame, "⏺ plain") })
	session.typeRaw("\x1b[Dz")
	frame := session.waitFrame("the typed z with no agent running", func(frame string) bool {
		return strings.Contains(frame, "❯ z")
	})
	if strings.Contains(frame, agentPanelHint) {
		t.Fatalf("the left arrow focused a panel with no agent running:\n%s", frame)
	}
	session.typeRaw("\x7f")
	session.typeLine("start one")
	session.waitFrame(
		"the agent running",
		func(frame string) bool { return strings.Contains(frame, "● scout  running") },
	)
	session.typeRaw("\x1b[D")
	focused := agentPanelHint + "\r\n❯ ⏺ main\r\n  ● scout  running (background)\r\n"
	session.waitFrame("the focused panel", func(frame string) bool { return strings.Contains(frame, focused) })
	session.typeRaw("typed")
	session.typeRaw("\x1b")
	frame = session.waitFrame("the panel released", func(frame string) bool {
		return !strings.Contains(frame, agentPanelHint) && strings.Contains(frame, "● scout  running")
	})
	if line := composerRow(frame); line != "❯ " {
		t.Fatalf("composer row after the panel let go = %q, want it empty: text typed into the panel reached it", line)
	}
	session.typeLine("after the panel")
	session.waitFrame("the turn typed after the panel let go", func(frame string) bool {
		return strings.Contains(frame, "⏺ over the panel") && strings.Contains(frame, "● scout  running")
	})
	if err := os.Remove(gate); err != nil {
		t.Fatal(err)
	}
	session.waitFrame(
		"the agent's result",
		func(frame string) bool { return strings.Contains(frame, `⏺ Agent "scout" finished`) },
	)
}

// composerRow is the frame's composer row: the line under the input box's first rule.
func composerRow(frame string) string {
	lines := strings.Split(frame, "\r\n")
	for index, line := range lines {
		if line+"\r\n" == claudeComposerRule && index+1 < len(lines) {
			return lines[index+1]
		}
	}
	return ""
}

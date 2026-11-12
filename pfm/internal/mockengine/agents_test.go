package mockengine

import (
	"os"
	"path/filepath"
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
	if strings.Contains(frame, "Waiting for") || strings.Contains(frame, "(background)") {
		t.Fatalf("an ended agent is still drawn:\n%s", frame)
	}
}

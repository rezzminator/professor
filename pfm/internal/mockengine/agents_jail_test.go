//go:build jail

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

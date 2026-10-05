package rowfacts

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

func assistant(model, stop string) string {
	stopJSON := "null"
	if stop != "" {
		stopJSON = `"` + stop + `"`
	}
	return fmt.Sprintf(
		`{"parentUuid":"p","type":"assistant","message":{"model":%q,"role":"assistant","content":[{"type":"text","text":"hi"}],"stop_reason":%s}}`,
		model,
		stopJSON,
	)
}

const (
	userPrompt   = `{"type":"user","message":{"role":"user","content":"please build it"}}`
	toolResult   = `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}`
	interrupted  = `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}`
	slashCommand = `{"type":"user","message":{"role":"user","content":"<command-name>/clear</command-name>"}}`
	noise        = `{"type":"attachment","attachment":{}}`
	titleRecord  = `{"type":"custom-title","customTitle":"x"}`
)

func TestScanClaudeDecidesFromTheNewestAssistantOrUserRecord(t *testing.T) {
	for _, test := range []struct {
		name    string
		lines   []string
		working bool
		model   string
	}{
		{"a finished answer is idle", []string{assistant("claude-opus-5-5", "end_turn")}, false, "claude-opus-5-5"},
		{"a stop sequence is idle", []string{assistant("claude-opus-5-5", "stop_sequence")}, false, "claude-opus-5-5"},
		{"a tool call awaits its result", []string{assistant("claude-opus-5-5", "tool_use")}, true, "claude-opus-5-5"},
		{"a streaming message is open", []string{assistant("claude-opus-5-5", "")}, true, "claude-opus-5-5"},
		{"a fresh prompt owes an answer", []string{assistant("claude-opus-5-5", "end_turn"), userPrompt}, true, "claude-opus-5-5"},
		{"a tool result owes an answer", []string{assistant("claude-opus-5-5", "tool_use"), toolResult}, true, "claude-opus-5-5"},
		{"an interrupt ends the turn", []string{assistant("claude-opus-5-5", "tool_use"), interrupted}, false, "claude-opus-5-5"},
		{"a slash command is not a turn", []string{assistant("claude-opus-5-5", "end_turn"), slashCommand}, false, "claude-opus-5-5"},
		{
			"metadata records are skipped",
			[]string{assistant("claude-sonnet-5-5", "end_turn"), noise, titleRecord, noise},
			false, "claude-sonnet-5-5",
		},
		{
			"a synthetic model never names the chat",
			[]string{assistant("claude-haiku-5", "end_turn"), assistant("<synthetic>", "end_turn")},
			false, "claude-haiku-5",
		},
		{"a line cut by a crash is skipped", []string{assistant("claude-opus-5-5", "end_turn"), `{"type":"assistant","mess`}, false, "claude-opus-5-5"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var tail cachedFile
			decided := false
			scanClaude([]byte(strings.Join(test.lines, "\n")+"\n"), &tail, &decided)
			if tail.working != test.working || tail.model != test.model {
				t.Errorf(
					"working=%v model=%q, want working=%v model=%q",
					tail.working,
					tail.model,
					test.working,
					test.model,
				)
			}
		})
	}
}

func TestWorkingAgentsCountsOnlyFreshOpenTurns(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "session.jsonl")
	writeFile(t, transcript, assistant("claude-opus-5-5", "end_turn")+"\n")
	agents := filepath.Join(dir, "session", "subagents")
	writeFile(t, filepath.Join(agents, "agent-busy.jsonl"), assistant("claude-sonnet-5-5", "tool_use")+"\n")
	writeFile(t, filepath.Join(agents, "agent-busy2.jsonl"), userPrompt+"\n")
	writeFile(t, filepath.Join(agents, "agent-done.jsonl"), assistant("claude-sonnet-5-5", "end_turn")+"\n")
	writeFile(t, filepath.Join(agents, "agent-stale.jsonl"), assistant("claude-sonnet-5-5", "tool_use")+"\n")
	writeFile(t, filepath.Join(agents, "agent-busy.meta.json"), `{}`)
	old := time.Now().Add(-10 * time.Minute)
	if err := osChtimes(filepath.Join(agents, "agent-stale.jsonl"), old); err != nil {
		t.Fatal(err)
	}
	got, err := NewReader(t.TempDir()).workingAgents(transcript, time.Now().UnixNano())
	if err != nil || got != 2 {
		t.Fatalf("two agents are mid-turn and fresh; got %d, err %v", got, err)
	}
	got, err = NewReader(t.TempDir()).workingAgents(filepath.Join(dir, "other.jsonl"), time.Now().UnixNano())
	if err != nil || got != 0 {
		t.Fatalf("a chat that never ran an agent has none working: %d, %v", got, err)
	}
}

func TestClaudeFactsReadsTheRecordFirstAndOnlyALiveSeatWorks(t *testing.T) {
	sid := t.TempDir()
	dir := t.TempDir()
	transcript := filepath.Join(dir, "a0000001-0000-4000-8000-000000000001.jsonl")
	writeFile(t, transcript, assistant("claude-sonnet-5-5", "tool_use")+"\n")
	writeFile(t, filepath.Join(sid, "statusline-effort-a0000001-0000-4000-8000-000000000001"),
		`{"level":"xhigh","model":"claude-opus-5-5"}`)
	reader := NewReader(sid)
	now := time.Now().UnixNano()
	live := compose.Row{Kind: compose.LiveClaude, ID: "a0000001-0000-4000-8000-000000000001", Path: transcript}
	facts, err := reader.claudeFacts(&live, now)
	if err != nil || facts.Model != "claude-opus-5-5" || facts.Effort != "xhigh" || !facts.Working {
		t.Fatalf("live: %+v, %v", facts, err)
	}
	resumable := live
	resumable.Kind = compose.ResumeClaude
	facts, err = reader.claudeFacts(&resumable, now)
	if err != nil || facts.Working || facts.Model != "claude-opus-5-5" {
		t.Fatalf("a resumable chat is never working: %+v, %v", facts, err)
	}
	facts, _ = reader.claudeFacts(&live, now+workingFreshNS+int64(time.Second))
	if facts.Working {
		t.Error("a turn that looks open but has been silent past the bound reads as idle")
	}
}

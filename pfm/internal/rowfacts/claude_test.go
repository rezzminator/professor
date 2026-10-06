package rowfacts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/statusline"
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

// BenchmarkReadClaudeTail is the picker's first refresh for one ordinary
// transcript: a long history whose newest records sit in its last few KiB.
func BenchmarkReadClaudeTail(b *testing.B) {
	path := filepath.Join(b.TempDir(), "session.jsonl")
	var history strings.Builder
	for history.Len() < 600<<10 {
		history.WriteString(
			`{"type":"system","subtype":"turn_duration","content":"` + strings.Repeat("x", 400) + `"}` + "\n",
		)
	}
	history.WriteString(`{"type":"user","message":{"role":"user","content":"ship it"}}` + "\n")
	history.WriteString(
		`{"type":"assistant","message":{"model":"claude-opus-5-5","stop_reason":"end_turn","content":[]}}` + "\n",
	)
	if err := os.WriteFile(path, []byte(history.String()), 0o600); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if tail, err := readClaudeTail(path); err != nil || tail.model != "claude-opus-5-5" || tail.working {
			b.Fatalf("readClaudeTail = %+v, %v", tail, err)
		}
	}
}

func TestClaudeFactsCachesUnchangedSourcesAndRefreshesChanges(t *testing.T) {
	sid, root := t.TempDir(), t.TempDir()
	transcript := filepath.Join(root, "session.jsonl")
	recordPath := filepath.Join(sid, "statusline-effort-session")
	agents := filepath.Join(root, "session", "subagents")
	writeFile(t, transcript, assistant("model-tail", "tool_use")+"\n")
	writeFile(t, recordPath, `{"model":"model-old","level":"high"}`)
	writeFile(t, filepath.Join(agents, "agent-one.jsonl"), assistant("model-agent", "tool_use")+"\n")
	reader := NewReader(sid)
	statusReads, directoryReads := 0, 0
	reader.readSession = func(dir, id string) (statusline.SessionRecord, error) {
		statusReads++
		return statusline.ReadSession(dir, id)
	}
	reader.readDirectory = func(path string) ([]os.DirEntry, error) { directoryReads++; return os.ReadDir(path) }
	row := compose.Row{ID: "session", Path: transcript, Kind: compose.LiveClaude}
	now := time.Now().UnixNano()
	for i := 0; i < 2; i++ {
		facts, err := reader.claudeFacts(&row, now)
		if err != nil || facts.Model != "model-old" || facts.Effort != "high" || facts.AgentsWorking != 1 {
			t.Fatalf("facts = %+v, %v", facts, err)
		}
	}
	if statusReads != 1 || directoryReads != 1 {
		t.Errorf("cache reads = status %d directory %d; want 1 each", statusReads, directoryReads)
	}
	writeFile(t, recordPath, `{"model":"model-newer","level":"xhigh"}`)
	writeFile(t, filepath.Join(agents, "agent-two.jsonl"), assistant("model-agent", "tool_use")+"\n")
	updated := time.Unix(0, now+int64(time.Millisecond))
	if err := os.Chtimes(agents, updated, updated); err != nil {
		t.Fatal(err)
	}
	facts, err := reader.claudeFacts(&row, now)
	if err != nil || facts.Model != "model-newer" || facts.Effort != "xhigh" || facts.AgentsWorking != 2 ||
		statusReads != 2 ||
		directoryReads != 2 {
		t.Errorf("fresh facts = %+v, %v, reads %d/%d", facts, err, statusReads, directoryReads)
	}
	writeFile(t, filepath.Join(agents, "agent-two.jsonl"), assistant("model-agent", "end_turn")+"\n")
	if err := os.Chtimes(filepath.Join(agents, "agent-two.jsonl"), updated, updated); err != nil {
		t.Fatal(err)
	}
	facts, err = reader.claudeFacts(&row, now)
	if err != nil || facts.AgentsWorking != 1 {
		t.Errorf("changed agent content = %+v, %v; want one working", facts, err)
	}
	facts, err = reader.claudeFacts(&row, now+agentFreshNS+int64(time.Second))
	if err != nil || facts.AgentsWorking != 0 {
		t.Errorf("aged facts = %+v, %v; want idle agents", facts, err)
	}
	if err := os.Remove(recordPath); err != nil {
		t.Fatal(err)
	}
	facts, err = reader.claudeFacts(&row, now)
	if err != nil || facts.Model != "model-tail" || facts.Effort != "" {
		t.Errorf("removed record = %+v, %v; want transcript fallback", facts, err)
	}
}

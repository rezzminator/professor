package statusline

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Transcript tails as Claude Code writes them: a turn that ended with text, a
// turn mid tool call, a tool result waiting for the next reply, and a reply
// still streaming (no stop_reason yet).
var (
	turnEnded = `{"type":"assistant","timestamp":"2026-09-24T10:01:00Z","message":{"stop_reason":"end_turn",` +
		`"content":[{"type":"text","text":"DONE"}]}}`
	turnToolCall = `{"type":"assistant","timestamp":"2026-09-24T10:01:00Z","message":{"stop_reason":"tool_use",` +
		`"content":[{"type":"tool_use","id":"x1","name":"Bash"}]}}`
	turnToolResult = `{"type":"user","timestamp":"2026-09-24T10:01:00Z","message":{"content":[{"type":"tool_result",` +
		`"tool_use_id":"x1","content":"ok"}]}}`
	turnStreaming = `{"type":"assistant","timestamp":"2026-09-24T10:01:00Z","message":{"stop_reason":null,` +
		`"content":[{"type":"text","text":"thinking"}]}}`
	trailingSystem = `{"type":"system","subtype":"turn_duration","timestamp":"2026-09-24T10:01:01Z"}`
)

const parentTask = `{"id":"p","type":"local_agent","status":"running","startTime":1790244000000,` +
	`"model":"claude-opus-5-5","contextWindowSize":1000000,"tokenCount":1000,"label":"orchestrate"}`

// nestedSession writes one transcript per agent and a meta file naming each
// agent's parent ("" = spawned by the main loop).
func nestedSession(t *testing.T, transcripts map[string][]string, parents map[string]string) string {
	t.Helper()
	session := subagentSession(t, transcripts, nil)
	dir := filepath.Join(strings.TrimSuffix(session, ".jsonl"), "subagents")
	for id, parent := range parents {
		meta := `{"agentType":"general-purpose","spawnDepth":2`
		if parent != "" {
			meta += `,"parentAgentId":` + jsonText(parent)
		}
		if err := os.WriteFile(filepath.Join(dir, "agent-"+id+".meta.json"), []byte(meta+"}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return session
}

func TestRenderSubagentsNestedAgents(t *testing.T) {
	cases := []struct {
		name        string
		transcripts map[string][]string
		parents     map[string]string
		want        string // the row between the status and the tools segments; "" = no agents segment
		warn        string
	}{
		{
			name: "direct children, deeper descendants, and the running ones",
			transcripts: map[string][]string{
				"p":  agentTranscriptLines,
				"c1": {turnEnded}, "c2": {turnToolCall}, "c3": {turnEnded, trailingSystem},
				"g1": {turnToolResult}, "g2": {turnEnded},
			},
			parents: map[string]string{"p": "", "c1": "p", "c2": "p", "c3": "p", "g1": "c3", "g2": "c1"},
			// c3 ended its own turn but g1 under it still works: c3 is running.
			want: "⤷3 agents +2 nested (2 running)",
		},
		{
			name: "every child finished",
			transcripts: map[string][]string{
				"p": agentTranscriptLines, "c1": {turnToolCall, turnEnded},
				"c2": {turnEnded, `{"type":"assistant","timest`},
			},
			parents: map[string]string{"p": "", "c1": "p", "c2": "p"},
			want:    "⤷2 agents (done)",
		},
		{
			name:        "a reply still streaming is a running child",
			transcripts: map[string][]string{"p": agentTranscriptLines, "c1": {turnStreaming}},
			parents:     map[string]string{"p": "", "c1": "p"},
			want:        "⤷1 agent (1 running)",
		},
		{
			name:        "a child spawned but not yet writing is running",
			transcripts: map[string][]string{"p": agentTranscriptLines, "c1": {}},
			parents:     map[string]string{"p": "", "c1": "p"},
			want:        "⤷1 agent (1 running)",
		},
		{
			name:        "a child whose transcript cannot be read is unknown, never done",
			transcripts: map[string][]string{"p": agentTranscriptLines},
			parents:     map[string]string{"p": "", "c1": "p"},
			want:        "⤷1 agent (1 ?)",
			warn:        "row p: nested agents: open sub-agent transcript",
		},
		{
			name:        "an agent that spawned none shows no segment",
			transcripts: map[string][]string{"p": agentTranscriptLines, "other": {turnToolCall}},
			parents:     map[string]string{"p": "", "other": ""},
			want:        "",
		},
		{
			name: "a grandchild counts as nested, not as a direct child",
			transcripts: map[string][]string{
				"p": agentTranscriptLines, "a": {turnEnded}, "b": {turnEnded},
			},
			parents: map[string]string{"p": "", "a": "p", "b": "a"},
			want:    "⤷1 agent +1 nested (done)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := nestedSession(t, tc.transcripts, tc.parents)
			got, warned := renderOneSubagent(t, session, parentTask)
			wantRow := "running 2m0s │ 2 tools │"
			if tc.want != "" {
				wantRow = "running 2m0s │ " + tc.want + " │ 2 tools │"
			}
			if !strings.Contains(got, wantRow) {
				t.Fatalf("content =\n  %q\nwant it to carry\n  %q", got, wantRow)
			}
			if tc.warn == "" && warned != "" || !strings.Contains(warned, tc.warn) {
				t.Fatalf("warn = %q, want %q", warned, tc.warn)
			}
		})
	}
}

// A real cycle (a → b → a) must not hang the walk or count an agent twice.
func TestRenderSubagentsNestedCycleTerminates(t *testing.T) {
	session := nestedSession(t, map[string][]string{
		"p": agentTranscriptLines, "a": {turnEnded}, "b": {turnEnded},
	}, map[string]string{"p": "", "a": "p", "b": "a"})
	dir := filepath.Join(strings.TrimSuffix(session, ".jsonl"), "subagents")
	// Rewrite a's parent to b after the fact: a → b → a, and p lists no child.
	if err := os.WriteFile(filepath.Join(dir, "agent-a.meta.json"),
		[]byte(`{"agentType":"x","parentAgentId":"b"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ := renderOneSubagent(t, session, parentTask)
	if strings.Contains(got, "⤷") {
		t.Fatalf("p lost its only child to the cycle, yet its row = %q", got)
	}
	aRow, _ := renderOneSubagent(t, session,
		`{"id":"a","type":"local_agent","status":"running","tokenCount":10}`)
	if !strings.Contains(aRow, "⤷1 agent (done)") {
		t.Fatalf("cycle row = %q, want ⤷1 agent (done)", aRow)
	}
}

// A last message beyond the first tail window is still found.
func TestRenderSubagentsNestedLongTail(t *testing.T) {
	long := `{"type":"assistant","timestamp":"2026-09-24T10:01:00Z","message":{"stop_reason":"end_turn",` +
		`"content":[{"type":"text","text":"` + strings.Repeat("x", 3*tailWindow) + `"}]}}`
	session := nestedSession(t, map[string][]string{
		"p": agentTranscriptLines, "c1": {turnToolCall, long, `{"type":"assist`},
	}, map[string]string{"p": "", "c1": "p"})
	got, warned := renderOneSubagent(t, session, parentTask)
	if !strings.Contains(got, "⤷1 agent (done)") || warned != "" {
		t.Fatalf("content = %q warn = %q, want ⤷1 agent (done)", got, warned)
	}
}

// A meta file that cannot be parsed hides which agent spawned it: every row
// says "agents ?", never "no children".
func TestRenderSubagentsNestedTornMetaIsNotAbsence(t *testing.T) {
	session := nestedSession(t, map[string][]string{"p": agentTranscriptLines},
		map[string]string{"p": ""})
	dir := filepath.Join(strings.TrimSuffix(session, ".jsonl"), "subagents")
	if err := os.WriteFile(filepath.Join(dir, "agent-c1.meta.json"), []byte(`{"agentTy`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, warned := renderOneSubagent(t, session, parentTask)
	if !strings.Contains(got, "running 2m0s │ agents ? │ 2 tools") ||
		!strings.Contains(warned, "row p: nested agents: read sub-agent meta") {
		t.Fatalf("content = %q warn = %q, want agents ? and the cause", got, warned)
	}
}

// renderRawSubagent returns one row's content with its colour escapes.
func renderRawSubagent(t *testing.T, session, task string, now time.Time) string {
	t.Helper()
	payload := `{"transcript_path":` + jsonText(session) + `,"cwd":"/work/repo","tasks":[` + task + `]}`
	got, err := RenderSubagents([]byte(payload), now, t.TempDir(), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	var row subagentRow
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &row); err != nil {
		t.Fatalf("row is not JSON: %v: %q", err, got)
	}
	return row.Content
}

// A finished row steps back: muted under Claude Code's faint for its first
// minute, then collapsed to status, age, identity and label. A failure keeps
// its colour for that minute, and a completed row with a running child is not
// finished at all.
func TestRenderSubagentsFinishedRowsStepBack(t *testing.T) {
	session := nestedSession(t, map[string][]string{
		"a1": agentTranscriptLines, "p": agentTranscriptLines, "c1": {turnToolCall},
	}, map[string]string{"a1": "", "p": "", "c1": "p"})
	// agentTranscriptLines ends at 10:01:30.
	ended := subagentStart.Add(90 * time.Second)
	row := func(id, status string) string {
		return `{"id":"` + id + `","name":"scout","type":"local_agent","status":"` + status + `",` +
			`"startTime":` + jsonText(subagentStart.UnixMilli()) + `,"model":"claude-opus-5-5",` +
			`"contextWindowSize":1000,"tokenCount":10,"label":"map it"}`
	}
	const (
		fullRow    = "▱▱▱▱▱▱▱▱ 1% 10/1.0K │ scout·general-purpose │ opus │ "
		fullTail   = " 1m30s │ 2 tools │ 1 error │ cache 94% │ ⟲1 │ map it"
		parentTail = " 1m30s │ ⤷1 agent (1 running) │ 2 tools │ 1 error │ cache 94% │ ⟲1 │ map it"
	)
	mutedWhole := func(raw string) bool {
		return strings.HasPrefix(raw, cMuted) && strings.Count(raw, "\x1b[") == 2 && strings.HasSuffix(raw, reset)
	}
	bright := func(raw string) bool { return strings.HasPrefix(raw, rowOpen) }
	mutedCollapsed := func(raw string) bool { return !strings.HasPrefix(raw, rowOpen) && !strings.Contains(raw, cFailed) }
	redStatus := func(raw string) bool { return strings.HasPrefix(raw, cFailed+"failed") }
	cases := []struct {
		name      string
		task      string
		now       time.Time
		wantPlain string
		check     func(raw string) bool
	}{
		{
			name:      "completed, first minute: the whole line muted, Claude Code's faint left on",
			task:      row("a1", "completed"),
			now:       ended.Add(30 * time.Second),
			wantPlain: fullRow + "completed" + fullTail,
			check:     mutedWhole,
		},
		{
			name:      "completed, after a minute: collapsed",
			task:      row("a1", "completed"),
			now:       ended.Add(2 * time.Minute),
			wantPlain: "completed 2m0s ago │ scout·general-purpose │ map it",
			check:     mutedCollapsed,
		},
		{
			name:      "failed, first minute: full colour, it is an alert",
			task:      row("a1", "failed"),
			now:       ended.Add(30 * time.Second),
			wantPlain: fullRow + "failed" + fullTail,
			check:     bright,
		},
		{
			name:      "failed, after a minute: collapsed, the status still red",
			task:      row("a1", "failed"),
			now:       ended.Add(2 * time.Minute),
			wantPlain: "failed 2m0s ago │ scout·general-purpose │ map it",
			check:     redStatus,
		},
		{
			name:      "completed with a child still running: not finished",
			task:      row("p", "completed"),
			now:       ended.Add(2 * time.Minute),
			wantPlain: fullRow + "completed" + parentTail,
			check:     bright,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := renderRawSubagent(t, session, tc.task, tc.now)
			if plain := stripANSICodes(raw); plain != tc.wantPlain {
				t.Fatalf("content =\n  %q\nwant\n  %q", plain, tc.wantPlain)
			}
			if !tc.check(raw) {
				t.Fatalf("raw row fails its colour check: %q", raw)
			}
		})
	}
}

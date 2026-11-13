package statusline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/transcript"
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
	// A sub-agent's turn ends by the SubagentHandback tool: its result is the
	// transcript's last entry, a user line Claude Code marks toolEndsTurn.
	turnHandbackCall = `{"type":"assistant","timestamp":"2026-09-24T10:00:59Z","message":{"stop_reason":"tool_use",` +
		`"content":[{"type":"tool_use","id":"hb1","name":"SubagentHandback","input":{"message":"done"}}]}}`
	turnHandbackResult = `{"type":"user","timestamp":"2026-09-24T10:01:00Z","message":{"role":"user",` +
		`"content":[{"tool_use_id":"hb1","type":"tool_result","content":[{"type":"text",` +
		`"text":"{\"success\":true,\"message\":\"Report delivered to your caller.\"}"}]}]},` +
		`"toolUseResult":{"success":true,"message":"Report delivered to your caller."},"toolEndsTurn":true}`
	// toolEndsTurn false (or absent, as in an older Claude Code) leaves the result an open turn.
	turnToolResultNotEnding = `{"type":"user","timestamp":"2026-09-24T10:01:00Z","message":{"content":[{"type":"tool_result",` +
		`"tool_use_id":"x1","content":"ok"}]},"toolEndsTurn":false}`
)

// parentTask is completed as far as Claude Code knows: its row says
// delegating while an agent below it works, completed once none does.
const parentTask = `{"id":"p","status":"completed","startTime":1790244000000,` +
	`"model":"claude-opus-5-5","contextWindowSize":1000000,"label":"orchestrate"}`

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

func TestRenderAgentRowsNestedAgents(t *testing.T) {
	cases := []struct {
		name        string
		transcripts map[string][]string
		parents     map[string]string
		running     []string // the agents below p Claude Code still runs
		nested      string   // the row's first segment; "" = none
		warn        string
	}{
		{
			name: "every layer counts; only the working agents below count as working",
			transcripts: map[string][]string{
				"p":  agentTranscriptLines,
				"c1": {turnEnded}, "c2": {turnToolCall}, "c3": {turnEnded, trailingSystem},
				"g1": {turnToolResult}, "g2": {turnEnded},
			},
			parents: map[string]string{"p": "", "c1": "p", "c2": "p", "c3": "p", "g1": "c3", "g2": "c1"},
			running: []string{"c2", "g1"},
			nested:  "2/5",
		},
		{
			name: "only a grandchild works: it counts",
			transcripts: map[string][]string{
				"p": agentTranscriptLines, "c1": {turnEnded}, "g1": {turnToolResult},
			},
			parents: map[string]string{"p": "", "c1": "p", "g1": "c1"},
			running: []string{"g1"},
			nested:  "1/2",
		},
		{
			name: "every agent below finished: none counts",
			transcripts: map[string][]string{
				"p": agentTranscriptLines, "c1": {turnToolCall, turnEnded},
				"c2": {turnEnded, `{"type":"assistant","timest`},
			},
			parents: map[string]string{"p": "", "c1": "p", "c2": "p"},
			running: []string{"c1", "c2"}, // listed a moment longer than their turns: the tails decide
			nested:  "0/2",
		},
		{
			name: "a handback result ends the turn: the agents that handed back are finished",
			transcripts: map[string][]string{
				"p": agentTranscriptLines, "c1": {turnToolCall, turnHandbackCall, turnHandbackResult},
				"c2": {turnHandbackResult}, "g1": {turnHandbackCall, turnHandbackResult},
			},
			parents: map[string]string{"p": "", "c1": "p", "c2": "p", "g1": "c1"},
			running: []string{"c1", "c2", "g1"},
			nested:  "0/3",
		},
		{
			name: "a result Claude Code does not mark as ending the turn is still working",
			transcripts: map[string][]string{
				"p": agentTranscriptLines, "c1": {turnToolResultNotEnding}, "c2": {turnHandbackResult},
			},
			parents: map[string]string{"p": "", "c1": "p", "c2": "p"},
			running: []string{"c1", "c2"},
			nested:  "1/2",
		},
		{
			name:        "a reply still streaming is working",
			transcripts: map[string][]string{"p": agentTranscriptLines, "c1": {turnStreaming}},
			parents:     map[string]string{"p": "", "c1": "p"},
			running:     []string{"c1"},
			nested:      "1/1",
		},
		{
			name:        "a child spawned but not yet writing is working",
			transcripts: map[string][]string{"p": agentTranscriptLines, "c1": {}},
			parents:     map[string]string{"p": "", "c1": "p"},
			running:     []string{"c1"},
			nested:      "1/1",
		},
		{
			name:        "a child whose transcript cannot be read is unread, never finished",
			transcripts: map[string][]string{"p": agentTranscriptLines},
			parents:     map[string]string{"p": "", "c1": "p"},
			running:     []string{"c1"},
			nested:      "0/1 (1 unread)",
			warn:        "row p: nested agents: open sub-agent transcript",
		},
		{
			// Claude Code dropped it from its list: it is over, so its count is
			// known; the lost transcript is still reported, by the spend walk.
			name:        "a child Claude Code no longer lists is finished, even when its transcript cannot be read",
			transcripts: map[string][]string{"p": agentTranscriptLines},
			parents:     map[string]string{"p": "", "c1": "p"},
			nested:      "0/1",
			warn:        "row p: nested agents: open sub-agent transcript",
		},
		{
			name:        "an agent that spawned none has no nesting segment, the row opens on the gauge",
			transcripts: map[string][]string{"p": agentTranscriptLines, "other": {turnToolCall}},
			parents:     map[string]string{"p": "", "other": ""},
			nested:      "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := nestedSession(t, tc.transcripts, tc.parents)
			tasks := []string{parentTask}
			for _, id := range tc.running {
				tasks = append(tasks, agentTask(id, "running"))
			}
			got, warned := renderRowOf(t, session, "p", subagentNow, tasks...)
			wantStart := "▱▱▱▱▱▱▱▱ 0% 1.0K/1.0M Σ$0.01/2.0K/35"
			if tc.nested != "" {
				wantStart = tc.nested + "│" + wantStart
			}
			if !strings.HasPrefix(got, wantStart) {
				t.Fatalf("content =\n  %q\nwant it to start with %q", got, wantStart)
			}
			if tc.warn == "" && warned != "" || !strings.Contains(warned, tc.warn) {
				t.Fatalf("warn = %q, want %q", warned, tc.warn)
			}
		})
	}
}

// A real cycle (a → b → a) must not hang the walk or count an agent twice.
func TestRenderAgentRowsNestedCycleTerminates(t *testing.T) {
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
	if !strings.HasPrefix(got, "▱") {
		t.Fatalf("p lost its only child to the cycle, yet its row = %q", got)
	}
	aRow, _ := renderRowOf(t, session, "a", subagentNow,
		`{"id":"a","status":"running"}`, agentTask("b", "running"))
	if !strings.HasPrefix(aRow, "0/1│") {
		t.Fatalf("cycle row = %q, want 0/1", aRow)
	}
}

// A last message longer than the line reader's buffer is still found.
func TestRenderAgentRowsNestedLongTail(t *testing.T) {
	long := `{"type":"assistant","timestamp":"2026-09-24T10:01:00Z","message":{"stop_reason":"end_turn",` +
		`"content":[{"type":"text","text":"` + strings.Repeat("x", 3*(64<<10)) + `"}]}}`
	session := nestedSession(t, map[string][]string{
		"p": agentTranscriptLines, "c1": {turnToolCall, long, `{"type":"assist`},
	}, map[string]string{"p": "", "c1": "p"})
	got, warned := renderRowOf(t, session, "p", subagentNow, parentTask, agentTask("c1", "running"))
	if !strings.HasPrefix(got, "0/1│") || warned != "" {
		t.Fatalf("content = %q warn = %q, want 0/1", got, warned)
	}
}

// A meta file that cannot be parsed hides which agent spawned that one agent
// only: every other edge stands, the row counts the unreadable agent as unread
// (it may sit below the task), and the cause goes to warn, never "none".
func TestRenderAgentRowsNestedTornMetaCostsOnlyItsOwnParent(t *testing.T) {
	for _, tc := range []struct {
		name        string
		transcripts map[string][]string
		parents     map[string]string
		want        string
	}{
		{"alone", map[string][]string{"p": agentTranscriptLines}, map[string]string{"p": ""}, "0/0 (1 unread)│▱"},
		{
			"beside a readable child",
			map[string][]string{"p": agentTranscriptLines, "c2": {turnEnded}},
			map[string]string{"p": "", "c2": "p"},
			"0/1 (1 unread)│▱",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := nestedSession(t, tc.transcripts, tc.parents)
			dir := filepath.Join(strings.TrimSuffix(session, ".jsonl"), "subagents")
			if err := os.WriteFile(filepath.Join(dir, "agent-c1.meta.json"), []byte(`{"agentTy`), 0o600); err != nil {
				t.Fatal(err)
			}
			got, warned := renderOneSubagent(t, session, parentTask)
			if !strings.HasPrefix(got, tc.want) ||
				!strings.Contains(warned, "row p: nested agents: parse sub-agent meta") {
				t.Fatalf("content = %q warn = %q, want prefix %q and the cause", got, warned, tc.want)
			}
		})
	}
}

// renderRawSubagent returns task's row content with its colour escapes, from
// a payload that also lists the other tasks Claude Code runs.
func renderRawSubagent(t *testing.T, session, task string, now time.Time, listed ...string) string {
	t.Helper()
	var parsed fixtureTask
	if err := json.Unmarshal([]byte(task), &parsed); err != nil {
		t.Fatal(err)
	}
	rows, _ := renderPayload(t, session, now, append([]string{task}, listed...)...)
	content, ok := rows[parsed.ID]
	if !ok {
		t.Fatalf("no row for %q in %v", parsed.ID, rows)
	}
	return content
}

// The working count wears the running colour and the total the tools colour,
// so the two numbers read apart at a glance.
func TestRenderAgentRowsNestedCountColours(t *testing.T) {
	session := nestedSession(t, map[string][]string{
		"p": agentTranscriptLines, "c1": {turnToolCall}, "c2": {turnEnded},
	}, map[string]string{"p": "", "c1": "p", "c2": "p"})
	raw := renderRawSubagent(t, session, parentTask, subagentNow, agentTask("c1", "running"))
	want := cRunning + "1" + reset + cMuted + "/" + reset + cTools + "2" + reset
	if !strings.HasPrefix(raw, want) {
		t.Fatalf("row = %q, want it to open with %q", raw, want)
	}
}

// renderPayload renders one payload of several tasks and returns each row's
// raw body by task id, and the warnings.
func renderPayload(
	t *testing.T,
	session string,
	now time.Time,
	tasks ...string,
) (rows map[string]string, warned string) {
	t.Helper()
	return renderFixture(t, rowsFixture{session: session, now: now}, tasks...)
}

// renderTasks renders one payload of several tasks and returns each row's
// ANSI-stripped body by task id.
func renderTasks(t *testing.T, session string, now time.Time, tasks ...string) map[string]string {
	t.Helper()
	rows, _ := renderPayload(t, session, now, tasks...)
	for id, content := range rows {
		rows[id] = stripANSICodes(content)
	}
	return rows
}

// renderRowOf renders one payload and returns row id's ANSI-stripped body and
// the warnings that name that row.
func renderRowOf(t *testing.T, session, id string, now time.Time, tasks ...string) (content, warned string) {
	t.Helper()
	rows, warnings := renderPayload(t, session, now, tasks...)
	raw, ok := rows[id]
	if !ok {
		t.Fatalf("no row for %q in %v", id, rows)
	}
	var own []string
	for _, line := range strings.Split(warnings, "\n") {
		if strings.Contains(line, "row "+id+": ") {
			own = append(own, line)
		}
	}
	return stripANSICodes(raw), strings.Join(own, "\n")
}

// agentTask is an agent task on opus with a 1M window.
func agentTask(id, status string) string {
	return `{"id":"` + id + `","status":"` + status + `","startTime":1790244000000,` +
		`"model":"claude-opus-5-5","contextWindowSize":1000000,"label":"` + id + `"}`
}

// The count a row shows is the agents below it that work right now, at every
// depth: Claude Code still lists them as running and their own turn is open.
// Claude Code's task list says which agents run, the transcript only whether a
// listed agent's turn is still open; a long silence is no verdict (a live
// session showed haiku workers half an hour quiet on an unanswered request,
// still running, under an orchestrator rendered 0/16 and one rendered
// completed). Finished, working, killed, long-silent and nested agents sit
// below one parent here; only the four Claude Code runs with an open turn count.
func TestRenderAgentRowsNestedCountsOnlyAgentsThatWork(t *testing.T) {
	const (
		longSilent = `{"type":"assistant","timestamp":"2026-09-24T09:30:00Z","message":{"stop_reason":null,` +
			`"content":[{"type":"text","text":"thinking"}],"usage":{"input_tokens":26185,"output_tokens":0}}}`
		silentResult = `{"type":"user","timestamp":"2026-09-24T09:30:05Z","message":{"content":[{"type":"tool_result",` +
			`"tool_use_id":"x1","content":"ok"}]}}`
	)
	// subagentNow is 10:02:00; turnToolCall and turnToolResult are stamped 10:01:00.
	t.Run("finished, working, killed, long-silent and nested agents", func(t *testing.T) {
		session := nestedSession(t, map[string][]string{
			"p":  agentTranscriptLines,
			"c1": {turnToolCall},             // working
			"c2": {longSilent},               // streaming tail, silent for 32 minutes, still run: working
			"c3": {turnEnded},                // finished; its grandchild works
			"c4": {turnToolCall},             // open turn, but Claude Code lists it killed
			"g1": {turnToolResult},           // working, nested under c3
			"g2": {longSilent, silentResult}, // nested under c2, silent 32 minutes on a tool result: working
			"c5": {turnToolResult},           // killed before its first reply: a row Claude Code lists with no counts
		}, map[string]string{"p": "", "c1": "p", "c2": "p", "c3": "p", "c4": "p", "g1": "c3", "g2": "c2", "c5": "p"})
		killedAtSpawn := `{"id":"c5","status":"killed","startTime":1790244000000,"label":"c5"}`
		rows := renderTasks(t, session, subagentNow,
			parentTask, agentTask("c1", "running"), agentTask("c4", "killed"), killedAtSpawn,
			agentTask("c2", "running"), agentTask("g1", "running"), agentTask("g2", "running"))
		got := rows["p"]
		if !strings.HasPrefix(got, "4/7│") {
			t.Fatalf("parent row = %q, want 4/7 (c1, c2, g1 and g2 work; c3, c4, c5 do not)", got)
		}
	})
	// The orchestrator ended its turn after launching background workers, so
	// Claude Code calls it completed; its workers, quiet for half an hour on an
	// unanswered request, are still run, and it waits on them.
	t.Run("a parent whose long-silent workers still run counts them", func(t *testing.T) {
		parents := map[string]string{"p": "", "c1": "p", "g1": "c1"}
		session := nestedSession(t, map[string][]string{
			"p": agentTranscriptLines, "c1": {longSilent}, "g1": {longSilent, silentResult},
		}, parents)
		got := renderTasks(t, session, subagentNow, parentTask, agentTask("c1", "running"), agentTask("g1", "running"))["p"]
		if !strings.HasPrefix(got, "2/2│") {
			t.Fatalf("parent row = %q, want 2/2: c1 and g1 still run", got)
		}
		done := nestedSession(t, map[string][]string{
			"p": agentTranscriptLines, "c1": {longSilent, turnEnded}, "g1": {longSilent, silentResult},
		}, parents)
		got = renderTasks(t, done, subagentNow, parentTask, agentTask("c1", "running"))["p"]
		if !strings.HasPrefix(got, "0/2│") {
			t.Fatalf("parent row = %q, want 0/2: c1 ended its turn, g1 no longer runs", got)
		}
	})
	// The live shape: a finished sub-agent's last line is its final text block
	// as the stream wrote it (no stop_reason), and Claude Code drops a finished
	// task from its list within a minute, so the payload never calls it over.
	t.Run("an agent that no longer runs is finished, whatever its transcript's tail", func(t *testing.T) {
		finalText := func(stamp string) string {
			return `{"type":"assistant","timestamp":"2026-09-24T` + stamp + `Z","message":{"stop_reason":null,` +
				`"content":[{"type":"text","text":"4 done"}],"usage":{"input_tokens":120,"output_tokens":5}}}`
		}
		session := nestedSession(t, map[string][]string{
			"p":  agentTranscriptLines,
			"c1": {turnEnded},                    // finished, its children too
			"g1": {finalText("09:48:20")},        // finished 13m40s ago, no stop_reason on its last line
			"g2": {finalText("09:47:30")},        // finished 14m30s ago, the same
			"c2": {turnToolCall, turnToolResult}, // killed after a tool result nothing answered
		}, map[string]string{"p": "", "c1": "p", "g1": "c1", "g2": "c1", "c2": "p"})
		got := renderTasks(t, session, subagentNow, parentTask)["p"]
		if !strings.HasPrefix(got, "0/4│") {
			t.Fatalf("parent row = %q, want 0/4: none of the agents below it runs", got)
		}
		listed := renderTasks(t, session, subagentNow, parentTask, agentTask("g1", "running"))["p"]
		if !strings.HasPrefix(listed, "1/4│") {
			t.Fatalf("parent row = %q, want 1/4: g1 still runs", listed)
		}
	})
}

// readCounter counts whole-transcript reads per path while installed.
func readCounter(t *testing.T) (counts func() map[string]int) {
	t.Helper()
	var mu sync.Mutex
	seen := map[string]int{}
	restore := transcript.ObserveWholeReads(func(path string) {
		mu.Lock()
		defer mu.Unlock()
		seen[path]++
	})
	t.Cleanup(restore)
	return func() map[string]int {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]int{}
		for path, count := range seen {
			out[path] = count
		}
		return out
	}
}

// TestRenderAgentRowsReadsEachTranscriptOnce pins the read-once law: c1 is a
// task row and also a descendant of the row p, g1 a descendant of both, and
// one render reads each of their transcripts a single time.
func TestRenderAgentRowsReadsEachTranscriptOnce(t *testing.T) {
	session := nestedSession(t, map[string][]string{
		"p": agentTranscriptLines, "c1": {turnToolCall}, "g1": {turnToolCall},
	}, map[string]string{"p": "", "c1": "p", "g1": "c1"})
	counts := readCounter(t)
	rows := renderTasks(t, session, subagentNow, agentTask("p", "running"), agentTask("c1", "running"))
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want p and c1", rows)
	}
	dir := strings.TrimSuffix(session, ".jsonl") + "/subagents"
	for _, id := range []string{"p", "c1", "g1"} {
		path := filepath.Join(dir, "agent-"+id+".jsonl")
		if got := counts()[path]; got != 1 {
			t.Errorf("%s transcript read %d times in one render, want exactly 1", id, got)
		}
	}
}

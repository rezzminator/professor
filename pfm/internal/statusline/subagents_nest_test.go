package statusline

import (
	"bytes"
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
const parentTask = `{"id":"p","type":"local_agent","status":"completed","startTime":1790244000000,` +
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
	const (
		working  = "delegating 2m:0s" // the clock runs on
		finished = "completed 1m:30s" // frozen at p's last entry
	)
	cases := []struct {
		name        string
		transcripts map[string][]string
		parents     map[string]string
		running     []string // the agents below p Claude Code still runs
		nested      string   // the row's first segment; "" = none
		status      string
		warn        string
	}{
		{
			name: "every layer counts; a working child makes the parent delegating",
			transcripts: map[string][]string{
				"p":  agentTranscriptLines,
				"c1": {turnEnded}, "c2": {turnToolCall}, "c3": {turnEnded, trailingSystem},
				"g1": {turnToolResult}, "g2": {turnEnded},
			},
			parents: map[string]string{"p": "", "c1": "p", "c2": "p", "c3": "p", "g1": "c3", "g2": "c1"},
			running: []string{"c2", "g1"},
			nested:  "2/5",
			status:  working,
		},
		{
			name: "only a grandchild works: still delegating",
			transcripts: map[string][]string{
				"p": agentTranscriptLines, "c1": {turnEnded}, "g1": {turnToolResult},
			},
			parents: map[string]string{"p": "", "c1": "p", "g1": "c1"},
			running: []string{"g1"},
			nested:  "1/2",
			status:  working,
		},
		{
			name: "every agent below finished: completed",
			transcripts: map[string][]string{
				"p": agentTranscriptLines, "c1": {turnToolCall, turnEnded},
				"c2": {turnEnded, `{"type":"assistant","timest`},
			},
			parents: map[string]string{"p": "", "c1": "p", "c2": "p"},
			running: []string{"c1", "c2"}, // listed a moment longer than their turns: the tails decide
			nested:  "0/2",
			status:  finished,
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
			status:  finished,
		},
		{
			name: "a result Claude Code does not mark as ending the turn is still working",
			transcripts: map[string][]string{
				"p": agentTranscriptLines, "c1": {turnToolResultNotEnding}, "c2": {turnHandbackResult},
			},
			parents: map[string]string{"p": "", "c1": "p", "c2": "p"},
			running: []string{"c1", "c2"},
			nested:  "1/2",
			status:  working,
		},
		{
			name:        "a reply still streaming is working",
			transcripts: map[string][]string{"p": agentTranscriptLines, "c1": {turnStreaming}},
			parents:     map[string]string{"p": "", "c1": "p"},
			running:     []string{"c1"},
			nested:      "1/1",
			status:      working,
		},
		{
			name:        "a child spawned but not yet writing is working",
			transcripts: map[string][]string{"p": agentTranscriptLines, "c1": {}},
			parents:     map[string]string{"p": "", "c1": "p"},
			running:     []string{"c1"},
			nested:      "1/1",
			status:      working,
		},
		{
			name:        "a child whose transcript cannot be read is unread, never finished",
			transcripts: map[string][]string{"p": agentTranscriptLines},
			parents:     map[string]string{"p": "", "c1": "p"},
			running:     []string{"c1"},
			nested:      "0/1 (1 unread)",
			status:      finished,
			warn:        "row p: nested agents: open sub-agent transcript",
		},
		{
			// Claude Code dropped it from its list: it is over, so its count is
			// known; the lost transcript is still reported, by the spend walk.
			name:        "a child Claude Code no longer lists is finished, even when its transcript cannot be read",
			transcripts: map[string][]string{"p": agentTranscriptLines},
			parents:     map[string]string{"p": "", "c1": "p"},
			nested:      "0/1",
			status:      finished,
			warn:        "row p: nested agents: open sub-agent transcript",
		},
		{
			name:        "an agent that spawned none has no nesting segment, the row opens on the gauge",
			transcripts: map[string][]string{"p": agentTranscriptLines, "other": {turnToolCall}},
			parents:     map[string]string{"p": "", "other": ""},
			nested:      "",
			status:      finished,
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
			if !strings.HasPrefix(got, wantStart) || !strings.Contains(got, "│"+tc.status+"│") {
				t.Fatalf("content =\n  %q\nwant it to start with %q and carry %q", got, wantStart, tc.status)
			}
			if tc.warn == "" && warned != "" || !strings.Contains(warned, tc.warn) {
				t.Fatalf("warn = %q, want %q", warned, tc.warn)
			}
		})
	}
}

// A real cycle (a → b → a) must not hang the walk or count an agent twice.
func TestRenderSubagentsNestedCycleTerminates(t *testing.T) {
	// Each turn of the cycle is ended, or silent 20 minutes on a sub-agent call
	// (so its liveness asks the agents below it, around the cycle).
	staleAgentCall := `{"type":"assistant","timestamp":"2026-09-24T09:40:00Z","message":{"stop_reason":"tool_use",` +
		`"content":[{"type":"tool_use","id":"x7","name":"Agent"}]}}`
	for name, last := range map[string]string{"ended turns": turnEnded, "stale sub-agent calls": staleAgentCall} {
		t.Run(name, func(t *testing.T) {
			session := nestedSession(t, map[string][]string{
				"p": agentTranscriptLines, "a": {last}, "b": {last},
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
				`{"id":"a","type":"local_agent","status":"running","tokenCount":10}`, agentTask("b", "running"))
			if !strings.HasPrefix(aRow, "0/1│") {
				t.Fatalf("cycle row = %q, want 0/1", aRow)
			}
		})
	}
}

// A last message longer than the line reader's buffer is still found.
func TestRenderSubagentsNestedLongTail(t *testing.T) {
	long := `{"type":"assistant","timestamp":"2026-09-24T10:01:00Z","message":{"stop_reason":"end_turn",` +
		`"content":[{"type":"text","text":"` + strings.Repeat("x", 3*(64<<10)) + `"}]}}`
	session := nestedSession(t, map[string][]string{
		"p": agentTranscriptLines, "c1": {turnToolCall, long, `{"type":"assist`},
	}, map[string]string{"p": "", "c1": "p"})
	got, warned := renderRowOf(t, session, "p", subagentNow, parentTask, agentTask("c1", "running"))
	if !strings.HasPrefix(got, "0/1│") || !strings.Contains(got, "│completed 1m:30s│") || warned != "" {
		t.Fatalf("content = %q warn = %q, want 0/1 and completed", got, warned)
	}
}

// A meta file that cannot be parsed hides which agent spawned it: every row
// says "?/?", never "none".
func TestRenderSubagentsNestedTornMetaIsNotAbsence(t *testing.T) {
	session := nestedSession(t, map[string][]string{"p": agentTranscriptLines},
		map[string]string{"p": ""})
	dir := filepath.Join(strings.TrimSuffix(session, ".jsonl"), "subagents")
	if err := os.WriteFile(filepath.Join(dir, "agent-c1.meta.json"), []byte(`{"agentTy`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, warned := renderOneSubagent(t, session, parentTask)
	if !strings.HasPrefix(got, "?/?│▱") ||
		!strings.Contains(warned, "row p: nested agents: parse sub-agent meta") {
		t.Fatalf("content = %q warn = %q, want ?/? and the cause", got, warned)
	}
}

// renderRawSubagent returns task's row content with its colour escapes, from
// a payload that also lists the other tasks Claude Code runs.
func renderRawSubagent(t *testing.T, session, task string, now time.Time, listed ...string) string {
	t.Helper()
	var parsed subagentTask
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
		fullRow    = "▰▰▰▰▰▰▰▰ 100% 1.0K/1.0K Σ$0.01/2.0K/35/2│scout·general-purpose│opus│"
		fullTail   = " 1m:30s│1 error│💾5m✓3m:8s 94%│⟲1│map it"
		parentTail = " 3m:30s│1 error│💾5m✓1m:38s 94%│⟲1│map it"
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
		listed    []string // the other tasks Claude Code runs
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
			wantPlain: "completed 2m0s ago│Σ$0.01/2.0K/35/2│scout·general-purpose│map it",
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
			wantPlain: "failed 2m0s ago│Σ$0.01/2.0K/35/2│scout·general-purpose│map it",
			check:     redStatus,
		},
		{
			name:      "completed with a child still working: delegating, not finished",
			task:      row("p", "completed"),
			listed:    []string{agentTask("c1", "running")},
			now:       ended.Add(2 * time.Minute),
			wantPlain: "1/1│" + strings.Replace(fullRow, "35/2", "35/3", 1) + "delegating" + parentTail,
			check:     bright,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := renderRawSubagent(t, session, tc.task, tc.now, tc.listed...)
			if plain := stripANSICodes(raw); plain != tc.wantPlain {
				t.Fatalf("content =\n  %q\nwant\n  %q", plain, tc.wantPlain)
			}
			if !tc.check(raw) {
				t.Fatalf("raw row fails its colour check: %q", raw)
			}
		})
	}
}

// The working count wears the running colour and the total the tools colour,
// so the two numbers read apart at a glance.
func TestRenderSubagentsNestedCountColours(t *testing.T) {
	session := nestedSession(t, map[string][]string{
		"p": agentTranscriptLines, "c1": {turnToolCall}, "c2": {turnEnded},
	}, map[string]string{"p": "", "c1": "p", "c2": "p"})
	raw := renderRawSubagent(t, session, parentTask, subagentNow, agentTask("c1", "running"))
	want := rowOpen + cRunning + "1" + reset + cMuted + "/" + reset + cTools + "2" + reset
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
	list := strings.Join(tasks, ",")
	payload := `{"transcript_path":` + jsonText(session) + `,"cwd":"/work/repo","tasks":[` + list + `]}`
	var warn bytes.Buffer
	got, err := RenderSubagents([]byte(payload), now, t.TempDir(), testPrices(t), "", noEnv, "", &warn)
	if err != nil {
		t.Fatal(err)
	}
	rows = map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		var row subagentRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("row is not JSON: %v: %q", err, line)
		}
		rows[row.ID] = row.Content
	}
	return rows, warn.String()
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

// agentTask is a local_agent task as Claude Code lists it.
func agentTask(id, status string) string {
	return `{"id":"` + id + `","type":"local_agent","status":"` + status + `","startTime":1790244000000,` +
		`"model":"claude-opus-5-5","contextWindowSize":1000000,"tokenCount":1000,"label":"` + id + `"}`
}

// The count a row shows is the agents below it that work right now, at every
// depth. A transcript whose turn never closed is not enough: an agent killed
// mid-stream, or stopped by its parent, leaves a last line no later line ever
// answers (the live session that showed this had agents silent for hours with
// a streaming tail), and Claude Code's own task list says which tasks are
// over. Finished, working, killed, long-silent and nested agents sit below one
// parent here; only the two that work count, and a parent whose only open
// agents are dead is completed, not delegating.
func TestRenderSubagentsNestedCountsOnlyAgentsThatWork(t *testing.T) {
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
			"c2": {longSilent},               // streaming tail, silent for 32 minutes: dead
			"c3": {turnEnded},                // finished; its grandchild works
			"c4": {turnToolCall},             // open turn, but Claude Code lists it killed
			"g1": {turnToolResult},           // working, nested under c3
			"g2": {longSilent, silentResult}, // nested under the dead c2
			"c5": {turnToolResult},           // killed before its first reply: a row Claude Code lists with no counts
		}, map[string]string{"p": "", "c1": "p", "c2": "p", "c3": "p", "c4": "p", "g1": "c3", "g2": "c2", "c5": "p"})
		killedAtSpawn := `{"id":"c5","type":"local_agent","status":"killed","startTime":1790244000000,"label":"c5"}`
		rows := renderTasks(t, session, subagentNow,
			parentTask, agentTask("c1", "running"), agentTask("c4", "killed"), killedAtSpawn,
			// Listed as running, as a hung agent stays: the 15 minutes judge them.
			agentTask("c2", "running"), agentTask("g1", "running"), agentTask("g2", "running"))
		got := rows["p"]
		if !strings.HasPrefix(got, "2/7│") || !strings.Contains(got, "│delegating 2m:0s│") {
			t.Fatalf("parent row = %q, want 2/7 (c1 and g1 work; c2, c3, c4, c5, g2 do not) and delegating", got)
		}
	})
	t.Run("only dead agents left below: the parent is finished, not delegating", func(t *testing.T) {
		session := nestedSession(t, map[string][]string{
			"p": agentTranscriptLines, "c1": {longSilent}, "g1": {longSilent, silentResult},
		}, map[string]string{"p": "", "c1": "p", "g1": "c1"})
		got := renderTasks(t, session, subagentNow, parentTask, agentTask("c1", "running"), agentTask("g1", "running"))["p"]
		if !strings.HasPrefix(got, "0/2│") || !strings.Contains(got, "│completed 1m:30s│") {
			t.Fatalf("parent row = %q, want 0/2 and completed", got)
		}
	})
	t.Run("a parent silent 20 minutes on a sub-agent call that still works is working", func(t *testing.T) {
		waiting := `{"type":"assistant","timestamp":"2026-09-24T09:40:00Z","message":{"stop_reason":"tool_use",` +
			`"content":[{"type":"tool_use","id":"x8","name":"Agent"}]}}`
		parents := map[string]string{"p": "", "c1": "p", "g1": "c1"}
		session := nestedSession(t, map[string][]string{
			"p": agentTranscriptLines, "c1": {waiting}, "g1": {turnToolCall},
		}, parents)
		running := []string{parentTask, agentTask("c1", "running"), agentTask("g1", "running")}
		got := renderTasks(t, session, subagentNow, running...)["p"]
		if !strings.HasPrefix(got, "2/2│") {
			t.Fatalf("parent row = %q, want 2/2 (the waiting c1 and its working child g1)", got)
		}
		ended := nestedSession(t, map[string][]string{
			"p": agentTranscriptLines, "c1": {waiting}, "g1": {turnEnded},
		}, parents)
		if got := renderTasks(t, ended, subagentNow, running...)["p"]; !strings.HasPrefix(got, "0/2│") {
			t.Fatalf("parent row = %q, want 0/2 (the child is done, so the silent parent is dead)", got)
		}
		// The call's line as the stream left it, no stop_reason written.
		unstopped := strings.Replace(waiting, `"stop_reason":"tool_use"`, `"stop_reason":null`, 1)
		streamed := nestedSession(t, map[string][]string{
			"p": agentTranscriptLines, "c1": {unstopped}, "g1": {turnToolCall},
		}, parents)
		if got := renderTasks(t, streamed, subagentNow, running...)["p"]; !strings.HasPrefix(got, "2/2│") {
			t.Fatalf("parent row = %q, want 2/2 (a sub-agent call without stop_reason still waits on g1)", got)
		}
	})
	t.Run(
		"a parent silent 20 minutes after one of its parallel sub-agents returned waits on the other",
		func(t *testing.T) {
			parallel := `{"type":"assistant","timestamp":"2026-09-24T09:38:00Z","message":{"stop_reason":"tool_use",` +
				`"content":[{"type":"tool_use","id":"x8","name":"Agent"},{"type":"tool_use","id":"x7","name":"Agent"}]}}`
			firstBack := `{"type":"user","timestamp":"2026-09-24T09:40:00Z","message":{"content":[{"type":"tool_result",` +
				`"tool_use_id":"x8","content":"g1 done"}]}}`
			parents := map[string]string{"p": "", "c1": "p", "g1": "c1", "g2": "c1"}
			session := nestedSession(t, map[string][]string{
				"p": agentTranscriptLines, "c1": {parallel, firstBack}, "g1": {turnEnded}, "g2": {turnToolCall},
			}, parents)
			running := []string{
				parentTask, agentTask("c1", "running"), agentTask("g1", "running"), agentTask("g2", "running"),
			}
			got := renderTasks(t, session, subagentNow, running...)["p"]
			if !strings.HasPrefix(got, "2/3│") {
				t.Fatalf("parent row = %q, want 2/3 (c1 still waits on its working child g2)", got)
			}
			allBack := nestedSession(t, map[string][]string{
				"p": agentTranscriptLines, "c1": {parallel, firstBack}, "g1": {turnEnded}, "g2": {turnEnded},
			}, parents)
			if got := renderTasks(t, allBack, subagentNow, running...)["p"]; !strings.HasPrefix(got, "0/3│") {
				t.Fatalf("parent row = %q, want 0/3 (no child works, so the silent parent is dead)", got)
			}
		},
	)
	// The live shape: a finished sub-agent's last line is its final text block
	// as the stream wrote it (no stop_reason), and Claude Code drops a finished
	// task from its list within a minute, so the payload never calls it over.
	t.Run("an agent Claude Code no longer lists is finished, whatever its transcript's tail", func(t *testing.T) {
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
		if !strings.HasPrefix(got, "0/4│") || !strings.Contains(got, "│completed 1m:30s│") {
			t.Fatalf("parent row = %q, want 0/4 and completed: Claude Code runs none of the agents below it", got)
		}
		listed := renderTasks(t, session, subagentNow, parentTask, agentTask("g1", "running"))["p"]
		if !strings.HasPrefix(listed, "1/4│") || !strings.Contains(listed, "│delegating 2m:0s│") {
			t.Fatalf("parent row = %q, want 1/4 and delegating: Claude Code still runs g1", listed)
		}
	})
	t.Run("a silence shorter than any tool call is still working", func(t *testing.T) {
		quiet := `{"type":"assistant","timestamp":"2026-09-24T09:55:00Z","message":{"stop_reason":"tool_use",` +
			`"content":[{"type":"tool_use","id":"x9","name":"Bash"}]}}` // a Bash call 7 minutes in
		session := nestedSession(t, map[string][]string{"p": agentTranscriptLines, "c1": {quiet}},
			map[string]string{"p": "", "c1": "p"})
		got := renderTasks(t, session, subagentNow, parentTask, agentTask("c1", "running"))["p"]
		if !strings.HasPrefix(got, "1/1│") || !strings.Contains(got, "│delegating ") {
			t.Fatalf("parent row = %q, want 1/1 and delegating", got)
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

// TestRenderSubagentsReadsEachTranscriptOnce pins the read-once law: c1 is a
// task row and also a descendant of the row p, g1 a descendant of both, and
// one render reads each of their transcripts a single time.
func TestRenderSubagentsReadsEachTranscriptOnce(t *testing.T) {
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

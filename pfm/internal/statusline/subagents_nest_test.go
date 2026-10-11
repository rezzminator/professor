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
		nested      string // the row's first segment; "" = none
		roster      string // the segment after it; "" = none
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
			nested:  "2/5",
			roster:  "2/5 general-purpose",
			status:  working,
		},
		{
			name: "only a grandchild works: still delegating",
			transcripts: map[string][]string{
				"p": agentTranscriptLines, "c1": {turnEnded}, "g1": {turnToolResult},
			},
			parents: map[string]string{"p": "", "c1": "p", "g1": "c1"},
			nested:  "1/2",
			roster:  "1/2 general-purpose",
			status:  working,
		},
		{
			name: "every agent below finished: completed",
			transcripts: map[string][]string{
				"p": agentTranscriptLines, "c1": {turnToolCall, turnEnded},
				"c2": {turnEnded, `{"type":"assistant","timest`},
			},
			parents: map[string]string{"p": "", "c1": "p", "c2": "p"},
			nested:  "0/2",
			roster:  "2 general-purpose",
			status:  finished,
		},
		{
			name: "a handback result ends the turn: the agents that handed back are finished",
			transcripts: map[string][]string{
				"p": agentTranscriptLines, "c1": {turnToolCall, turnHandbackCall, turnHandbackResult},
				"c2": {turnHandbackResult}, "g1": {turnHandbackCall, turnHandbackResult},
			},
			parents: map[string]string{"p": "", "c1": "p", "c2": "p", "g1": "c1"},
			nested:  "0/3",
			roster:  "3 general-purpose",
			status:  finished,
		},
		{
			name: "a result Claude Code does not mark as ending the turn is still working",
			transcripts: map[string][]string{
				"p": agentTranscriptLines, "c1": {turnToolResultNotEnding}, "c2": {turnHandbackResult},
			},
			parents: map[string]string{"p": "", "c1": "p", "c2": "p"},
			nested:  "1/2",
			roster:  "1/2 general-purpose",
			status:  working,
		},
		{
			name:        "a reply still streaming is working",
			transcripts: map[string][]string{"p": agentTranscriptLines, "c1": {turnStreaming}},
			parents:     map[string]string{"p": "", "c1": "p"},
			nested:      "1/1",
			roster:      "1/1 general-purpose",
			status:      working,
		},
		{
			name:        "a child spawned but not yet writing is working",
			transcripts: map[string][]string{"p": agentTranscriptLines, "c1": {}},
			parents:     map[string]string{"p": "", "c1": "p"},
			nested:      "1/1",
			roster:      "1/1 general-purpose",
			status:      working,
		},
		{
			name:        "a child whose transcript cannot be read is unread, never finished",
			transcripts: map[string][]string{"p": agentTranscriptLines},
			parents:     map[string]string{"p": "", "c1": "p"},
			nested:      "0/1 (1 unread)",
			roster:      "0/1? general-purpose",
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
			got, warned := renderOneSubagent(t, session, parentTask)
			wantStart := "▱▱▱▱▱▱▱▱ 0% 1.0K/1.0M $0.01/2.0K/35"
			if tc.roster != "" {
				wantStart = tc.roster + "│" + wantStart
			}
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
	aRow, _ := renderOneSubagent(t, session,
		`{"id":"a","type":"local_agent","status":"running","tokenCount":10}`)
	if !strings.HasPrefix(aRow, "0/1│") {
		t.Fatalf("cycle row = %q, want 0/1", aRow)
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

// renderRawSubagent returns one row's content with its colour escapes.
func renderRawSubagent(t *testing.T, session, task string, now time.Time) string {
	t.Helper()
	payload := `{"transcript_path":` + jsonText(session) + `,"cwd":"/work/repo","tasks":[` + task + `]}`
	got, err := RenderSubagents([]byte(payload), now, t.TempDir(), testPrices(t), "", "", &bytes.Buffer{})
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
		fullRow    = "▱▱▱▱▱▱▱▱ 1% 10/1.0K $0.01/2.0K/35/2│scout·general-purpose│opus│"
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
			wantPlain: "completed 2m0s ago│$0.01/2.0K/35/2│scout·general-purpose│map it",
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
			wantPlain: "failed 2m0s ago│$0.01/2.0K/35/2│scout·general-purpose│map it",
			check:     redStatus,
		},
		{
			name: "completed with a child still working: delegating, not finished",
			task: row("p", "completed"),
			now:  ended.Add(2 * time.Minute),
			wantPlain: "1/1│1/1 general-purpose│" + strings.Replace(
				fullRow,
				"35/2",
				"35/3",
				1,
			) + "delegating" + parentTail,
			check: bright,
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

// The working count wears the running colour and the total the tools colour,
// so the two numbers read apart at a glance.
func TestRenderSubagentsNestedCountColours(t *testing.T) {
	session := nestedSession(t, map[string][]string{
		"p": agentTranscriptLines, "c1": {turnToolCall}, "c2": {turnEnded},
	}, map[string]string{"p": "", "c1": "p", "c2": "p"})
	raw := renderRawSubagent(t, session, parentTask, subagentNow)
	want := rowOpen + cRunning + "1" + reset + cMuted + "/" + reset + cTools + "2" + reset
	if !strings.HasPrefix(raw, want) {
		t.Fatalf("row = %q, want it to open with %q", raw, want)
	}
}

// renderTasks renders one payload of several tasks and returns each row's
// ANSI-stripped body by task id.
func renderTasks(t *testing.T, session string, now time.Time, tasks ...string) map[string]string {
	t.Helper()
	list := strings.Join(tasks, ",")
	payload := `{"transcript_path":` + jsonText(session) + `,"cwd":"/work/repo","tasks":[` + list + `]}`
	got, err := RenderSubagents([]byte(payload), now, t.TempDir(), testPrices(t), "", "", &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		var row subagentRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("row is not JSON: %v: %q", err, line)
		}
		rows[row.ID] = stripANSICodes(row.Content)
	}
	return rows
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
			parentTask, agentTask("c1", "running"), agentTask("c4", "killed"), killedAtSpawn)
		got := rows["p"]
		if !strings.HasPrefix(got, "2/7│") || !strings.Contains(got, "│delegating 2m:0s│") {
			t.Fatalf("parent row = %q, want 2/7 (c1 and g1 work; c2, c3, c4, c5, g2 do not) and delegating", got)
		}
	})
	t.Run("only dead agents left below: the parent is finished, not delegating", func(t *testing.T) {
		session := nestedSession(t, map[string][]string{
			"p": agentTranscriptLines, "c1": {longSilent}, "g1": {longSilent, silentResult},
		}, map[string]string{"p": "", "c1": "p", "g1": "c1"})
		got := renderTasks(t, session, subagentNow, parentTask)["p"]
		if !strings.HasPrefix(got, "0/2│") || !strings.Contains(got, "│completed 1m:30s│") {
			t.Fatalf("parent row = %q, want 0/2 and completed", got)
		}
	})
	t.Run("a silence shorter than any tool call is still working", func(t *testing.T) {
		quiet := `{"type":"assistant","timestamp":"2026-09-24T09:55:00Z","message":{"stop_reason":"tool_use",` +
			`"content":[{"type":"tool_use","id":"x9","name":"Bash"}]}}` // a Bash call 7 minutes in
		session := nestedSession(t, map[string][]string{"p": agentTranscriptLines, "c1": {quiet}},
			map[string]string{"p": "", "c1": "p"})
		got := renderTasks(t, session, subagentNow, parentTask)["p"]
		if !strings.HasPrefix(got, "1/1│") || !strings.Contains(got, "│delegating ") {
			t.Fatalf("parent row = %q, want 1/1 and delegating", got)
		}
	})
}

// setRoles rewrites the agentType of the named agents' meta files.
func setRoles(t *testing.T, session string, roles map[string]string) {
	t.Helper()
	dir := filepath.Join(strings.TrimSuffix(session, ".jsonl"), "subagents")
	for id, role := range roles {
		path := filepath.Join(dir, "agent-"+id+".meta.json")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var meta map[string]any
		if err := json.Unmarshal(body, &meta); err != nil {
			t.Fatal(err)
		}
		meta["agentType"] = role
		out, err := json.Marshal(meta)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, out, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// rosterSession is an orchestrator p with a lander and a reviewer that handed
// back, four tracers (t1 works, t2 handed back, t3 and t4 ended), and three
// sub-tracers below t1 (s1 and s2 work, s3 ended): 3 of 9 work.
func rosterSession(t *testing.T) string {
	t.Helper()
	session := nestedSession(t, map[string][]string{
		"p":  agentTranscriptLines,
		"l1": {turnHandbackCall, turnHandbackResult}, "r1": {turnEnded},
		"t1": {turnToolCall}, "t2": {turnToolCall, turnHandbackCall, turnHandbackResult},
		"t3": {turnEnded}, "t4": {turnEnded},
		"s1": {turnToolCall}, "s2": {turnToolResult}, "s3": {turnEnded},
	}, map[string]string{
		"p": "", "l1": "p", "r1": "p", "t1": "p", "t2": "p", "t3": "p", "t4": "p",
		"s1": "t1", "s2": "t1", "s3": "t1",
	})
	setRoles(t, session, map[string]string{
		"p": "flights-foreman", "l1": "flights-lander", "r1": "reviewer-pro",
		"t1": "tracer", "t2": "tracer", "t3": "tracer", "t4": "tracer",
		"s1": "sub-tracer", "s2": "sub-tracer", "s3": "sub-tracer",
	})
	return session
}

const rosterFull = "1 flights-lander · 1 reviewer-pro · 1/4 tracer · 2/3 sub-tracer"

// The row of an agent that spawned others names them: the roles below it at
// every depth, nearest first, each with its count — working over all when some
// of its members work, plain and muted when none does.
func TestRenderSubagentsRosterNamesTheAgentsBelow(t *testing.T) {
	session := rosterSession(t)
	got := renderTasks(t, session, subagentNow, parentTask)["p"]
	want := "3/9│" + rosterFull + "│▱▱▱▱▱▱▱▱ 0% 1.0K/1.0M "
	if !strings.HasPrefix(got, want) {
		t.Fatalf("parent row = %q, want it to start with %q", got, want)
	}
	raw := renderRawSubagent(t, session, parentTask, subagentNow)
	working := cRunning + "1" + reset + cMuted + "/" + reset + cTools + "4" + reset + " " + cRole + "tracer" + reset
	finished := cMuted + "1 flights-lander" + reset
	if !strings.Contains(raw, working) || !strings.Contains(raw, finished) {
		t.Fatalf("row = %q, want the working role as %q and the finished one as %q", raw, working, finished)
	}
}

// A role named by a definition's `role:` shows under that name, as in the
// identity segment, and two types that map to one role are one entry. A type
// whose definition cannot be read keeps its own name and warns once per
// render, however many agents carry it; a meta naming no agentType is a
// yellow "?" with its cause on stderr. The row's own padded agentType resolves
// as the roster's does.
func TestRenderSubagentsRosterUsesDefinitionRoles(t *testing.T) {
	session := nestedSession(t, map[string][]string{
		"p": agentTranscriptLines, "c1": {turnToolCall}, "c2": {turnEnded},
		"c3": {turnEnded}, "c4": {turnEnded}, "c5": {turnEnded},
	}, map[string]string{"p": "", "c1": "p", "c2": "p", "c3": "p", "c4": "p", "c5": "p"})
	setRoles(t, session, map[string]string{
		"p": " scout-a ", "c1": "scout-a", "c2": "scout-b", "c3": "broken", "c4": "broken", "c5": "",
	})
	agents := filepath.Join(t.TempDir(), "agents")
	if err := os.MkdirAll(filepath.Join(agents, "broken.md"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"scout-a", "scout-b"} {
		body := "---\nname: " + name + "\nrole: scout\n---\n"
		if err := os.WriteFile(filepath.Join(agents, name+".md"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	payload := `{"transcript_path":` + jsonText(session) + `,"cwd":"/work/repo","tasks":[` + parentTask + `]}`
	var warned bytes.Buffer
	got, err := RenderSubagents([]byte(payload), subagentNow, t.TempDir(), testPrices(t), "", agents, &warned)
	if err != nil {
		t.Fatal(err)
	}
	var row subagentRow
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &row); err != nil {
		t.Fatalf("row is not JSON: %v: %q", err, got)
	}
	plain := stripANSICodes(row.Content)
	if !strings.HasPrefix(plain, "1/5│1 ? · 2 broken · 1/2 scout│") || !strings.Contains(plain, "│scout│") {
		t.Fatalf("row = %q, want 1/5│1 ? · 2 broken · 1/2 scout│ and the row's own role scout", plain)
	}
	if untyped := cMuted + "1 " + reset + cWarn + unknownRole + reset; !strings.Contains(row.Content, untyped) {
		t.Fatalf("row = %q, want the untyped agent as %q", row.Content, untyped)
	}
	if !strings.Contains(warned.String(), "meta of c5 names no agentType") {
		t.Fatalf("warn = %q, want the untyped agent's cause", warned.String())
	}
	if count := strings.Count(warned.String(), "agent definition (rows show broken)"); count != 1 {
		t.Fatalf("warn = %q, want the unreadable definition once, got %d", warned.String(), count)
	}
}

// A narrow panel shortens the roster to the roles that work (what is hidden is
// counted), then drops it, the count staying; a failed scan shows no roster,
// only the "?/?" it already says.
func TestRenderSubagentsRosterFitsColumns(t *testing.T) {
	session := rosterSession(t)
	task := `{"id":"p","type":"local_agent","status":"completed","startTime":1790244000000,"label":"x",` +
		`"cwd":"/work/repo/pfm","model":"claude-opus-5-5","effort":"high","contextWindowSize":1000000,"tokenCount":1000}`
	cases := []struct {
		columns int
		want    string // what the row carries between its count and its gauge
	}{
		{0, "3/9│" + rosterFull + "│▱"},
		{140, "3/9│1/4 tracer · 2/3 sub-tracer +2│▱"},
		{125, "3/9│▱"},
	}
	for _, tc := range cases {
		got, _ := renderOneSubagentColumns(t, session, task, tc.columns)
		if !strings.HasPrefix(got, tc.want) {
			t.Errorf("columns %d: row = %q, want it to start with %q", tc.columns, got, tc.want)
		}
	}
	idle := nestedSession(t, map[string][]string{"p": agentTranscriptLines, "c1": {turnEnded}, "c2": {turnEnded}},
		map[string]string{"p": "", "c1": "p", "c2": "p"})
	setRoles(t, idle, map[string]string{"c1": "tracer", "c2": "scout"})
	narrow, _ := renderOneSubagentColumns(t, idle, task, 120)
	if !strings.HasPrefix(narrow, "0/2│2 roles│▱") {
		t.Errorf("columns 120, nothing working: row = %q, want it to start with 0/2│2 roles│▱", narrow)
	}
	// "1 role" would be wider than "2 qa" and lose its name: the short form keeps the roster.
	single, _ := rosterPart(agentNesting{total: 2, roles: []roleCount{{name: "qa", total: 2, depth: 1}}})
	if got := stripANSICodes(single.short); got != "2 qa" {
		t.Errorf("one idle role: short = %q, want 2 qa", got)
	}
}

func TestRenderSubagentsRosterTornMetaShowsNoRoster(t *testing.T) {
	session := nestedSession(t, map[string][]string{"p": agentTranscriptLines, "c1": {turnEnded}},
		map[string]string{"p": "", "c1": "p"})
	dir := filepath.Join(strings.TrimSuffix(session, ".jsonl"), "subagents")
	if err := os.WriteFile(filepath.Join(dir, "agent-c2.meta.json"), []byte(`{"agentTy`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _ := renderOneSubagent(t, session, parentTask)
	if !strings.HasPrefix(got, "?/?│▱") {
		t.Fatalf("row = %q, want ?/? and no roster after it", got)
	}
}

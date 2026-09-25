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

// agentTranscriptLines is a sub-agent transcript with the traps a real one
// carries: a streamed assistant line repeated with the same tool_use id, a
// tool_result that quotes tool_use text, and a torn final line mid-write —
// plus one errored tool result and one compact boundary.
var agentTranscriptLines = []string{
	`{"type":"user","timestamp":"2026-09-24T10:00:00Z","message":{"role":"user","content":"go"}}`,
	`{"type":"assistant","timestamp":"2026-09-24T10:00:05Z","message":{"content":[{"type":"tool_use","id":"t1","name":"Read"}],` +
		`"usage":{"input_tokens":10,"cache_read_input_tokens":0,"cache_creation_input_tokens":990}}}`,
	`{"type":"assistant","timestamp":"2026-09-24T10:00:06Z","message":{"content":[{"type":"tool_use","id":"t1","name":"Read"}],` +
		`"usage":{"input_tokens":10,"cache_read_input_tokens":0,"cache_creation_input_tokens":990}}}`,
	`{"type":"user","timestamp":"2026-09-24T10:00:07Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1",` +
		`"content":"{\"type\":\"tool_use\",\"id\":\"quoted\"}"}]}}`,
	`{"type":"user","timestamp":"2026-09-24T10:00:08Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1",` +
		`"is_error":true,"content":"exit status 1"}]}}`,
	`{"type":"system","subtype":"compact_boundary","timestamp":"2026-09-24T10:00:50Z",` +
		`"compactMetadata":{"trigger":"auto","preTokens":167189}}`,
	`{"type":"assistant","timestamp":"2026-09-24T10:01:30Z","message":{"content":[{"type":"tool_use","id":"t2","name":"Bash"}],` +
		`"usage":{"input_tokens":20,"cache_read_input_tokens":940,"cache_creation_input_tokens":40}}}`,
	`{"type":"assistant","timest`,
}

var (
	subagentStart = time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	subagentNow   = subagentStart.Add(2 * time.Minute)
)

// subagentSession writes the given agents' transcripts and meta roles beside a
// session transcript path and returns the path the payload names.
func subagentSession(t *testing.T, agents map[string][]string, roles map[string]string) string {
	t.Helper()
	session := filepath.Join(t.TempDir(), "session.jsonl")
	dir := filepath.Join(strings.TrimSuffix(session, ".jsonl"), "subagents")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for id, lines := range agents {
		body := strings.Join(lines, "\n")
		if err := os.WriteFile(filepath.Join(dir, "agent-"+id+".jsonl"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for id, role := range roles {
		meta := `{"agentType":` + jsonText(role) + `,"spawnDepth":1}`
		if err := os.WriteFile(filepath.Join(dir, "agent-"+id+".meta.json"), []byte(meta), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return session
}

func jsonText(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func renderOneSubagent(t *testing.T, session, task string) (content, warned string) {
	t.Helper()
	return renderSubagentAt(t, session, task, subagentNow)
}

func renderSubagentAt(t *testing.T, session, task string, now time.Time) (content, warned string) {
	t.Helper()
	payload := `{"transcript_path":` + jsonText(session) + `,"cwd":"/work/repo","columns":200,"tasks":[` + task + `]}`
	var warn bytes.Buffer
	got, err := RenderSubagents([]byte(payload), now, t.TempDir(), &warn)
	if err != nil {
		t.Fatalf("RenderSubagents: %v", err)
	}
	if got == "" {
		return "", warn.String()
	}
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want exactly one JSON line, got %d: %q", len(lines), got)
	}
	var row subagentRow
	if err := json.Unmarshal([]byte(lines[0]), &row); err != nil {
		t.Fatalf("line is not the {id,content} JSON Claude Code parses: %v: %q", err, lines[0])
	}
	var parsed subagentTask
	if err := json.Unmarshal([]byte(task), &parsed); err != nil {
		t.Fatal(err)
	}
	if row.ID != parsed.ID {
		t.Fatalf("row id = %q, want %q", row.ID, parsed.ID)
	}
	return stripANSICodes(row.Content), warn.String()
}

func TestRenderSubagentsRowBodies(t *testing.T) {
	session := subagentSession(t, map[string][]string{
		"a1":    agentTranscriptLines,
		"fresh": agentTranscriptLines[:1],
	}, map[string]string{"a1": "tracer", "fresh": "general-purpose"})
	task := func(fields string) string {
		return `{"startTime":` + jsonText(subagentStart.UnixMilli()) + `,"cwd":"/work/repo/pfm",` + fields + `}`
	}
	cases := []struct {
		name string
		task string
		want string // ANSI-stripped content; "" = no line for this task
	}{
		{
			name: "running agent: every segment, errors and compactions included",
			task: task(`"id":"a1","name":"scout","type":"local_agent","status":"running","label":"map the resolver",` +
				`"model":"claude-opus-5-5[1m]","effort":"high","contextWindowSize":1000000,"tokenCount":312000,` +
				`"tokenSamples":[0,125000,250000,500000,1000000]`),
			want: "▰▰▱▱▱▱▱▱ 31% 312.0K/1.0M │ scout·tracer │ opus·🏎️ high │ running 2m0s │ 2 tools │ 1 error │ cache 94% │ " +
				"⟲1 │ __⎽⎼¯ │ pfm │ map the resolver",
		},
		{
			name: "a finished agent's time stops at its transcript's last entry",
			task: task(`"id":"a1","type":"local_agent","status":"completed","label":"x","model":"claude-sonnet-5",` +
				`"contextWindowSize":1000000,"tokenCount":90000,"tokenSamples":[900000,90000]`),
			want: "▱▱▱▱▱▱▱▱ 9% 90.0K/1.0M │ tracer │ sonnet │ completed 1m30s │ 2 tools │ 1 error │ cache 94% │ ⟲1 │ ¯_ │ pfm │ x",
		},
		{
			name: "no model turn yet: zero tools, an empty cache, and idle since its prompt",
			task: task(`"id":"fresh","type":"local_agent","status":"running","label":"x","model":"haiku",` +
				`"contextWindowSize":200000,"tokenCount":1`),
			want: "▱▱▱▱▱▱▱▱ 0% 1/200.0K │ general-purpose │ haiku │ running 2m0s │ idle 2m0s │ 0 tools │ cache – │ pfm │ x",
		},
		{
			name: "a non-agent task carries no transcript facts; label falls back to the description",
			task: task(`"id":"sh","type":"local_bash","status":"running","description":"from the description",` +
				`"contextWindowSize":200000,"tokenCount":160000`),
			want: "▰▰▰▰▰▰▱▱ 80% 160.0K/200.0K │ running 2m0s │ pfm │ from the description",
		},
		{
			name: "an effort that is not a string is left out; a name shows without a role",
			task: `{"id":"e","name":"bot","status":"running","model":"claude-haiku-4","effort":{"level":"high"},` +
				`"tokenCount":4200,"tokenSamples":[2100,4200]}`,
			want: "4.2K │ bot │ haiku │ running │ ⎼¯",
		},
		{
			name: "an agent in the session's own directory shows no cwd",
			task: `{"id":"same","status":"running","cwd":"/work/repo/","model":"haiku","tokenCount":10,"label":"x"}`,
			want: "10 │ haiku │ running │ x",
		},
		{
			name: "a task with no tokens and no window keeps Claude Code's own row",
			task: `{"id":"a5","label":"shell","tokenCount":0}`,
			want: "",
		},
		{
			name: "a task with no id is skipped",
			task: `{"label":"orphan","contextWindowSize":1000000,"tokenCount":5}`,
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, warned := renderOneSubagent(t, session, tc.task)
			if got != tc.want {
				t.Fatalf("content =\n  %q\nwant\n  %q", got, tc.want)
			}
			if warned != "" {
				t.Fatalf("readable transcripts warned: %q", warned)
			}
		})
	}
}

// An unreadable transcript is a failure to look: the row says "?" for both
// transcript facts and the cause is reported, never rendered as zero tools.
func TestRenderSubagentsUnreadableTranscriptIsNotZero(t *testing.T) {
	session := subagentSession(t, nil, nil)
	got, warned := renderOneSubagent(t, session,
		`{"id":"gone","type":"local_agent","status":"running","contextWindowSize":1000,"tokenCount":10}`)
	if got != "▱▱▱▱▱▱▱▱ 1% 10/1.0K │ role ? │ running │ tools ? │ cache ?" {
		t.Fatalf("content = %q, want the ? markers", got)
	}
	for _, cause := range []string{"row gone: read sub-agent meta", "row gone: open sub-agent transcript"} {
		if !strings.Contains(warned, cause) {
			t.Fatalf("warn = %q, want %q", warned, cause)
		}
	}
}

// A meta file without agentType is a failure to read the role, not "no role".
func TestRenderSubagentsMetaWithoutRoleIsNotEmpty(t *testing.T) {
	session := subagentSession(t, map[string][]string{"m": agentTranscriptLines[:1]}, map[string]string{"m": ""})
	got, warned := renderOneSubagent(t, session,
		`{"id":"m","type":"local_agent","status":"running","tokenCount":10}`)
	if !strings.HasPrefix(got, "10 │ role ? │ running") || !strings.Contains(warned, "names no agentType") {
		t.Fatalf("content = %q warn = %q, want role ? and the cause", got, warned)
	}
}

// Quiet time turns yellow past a minute and red past five, only while running.
func TestRenderSubagentsIdleOnlyWhileRunning(t *testing.T) {
	session := subagentSession(t, map[string][]string{"a1": agentTranscriptLines}, map[string]string{"a1": "tracer"})
	lastEntry := time.Date(2026, 9, 24, 10, 1, 30, 0, time.UTC)
	for _, tc := range []struct {
		status   string
		now      time.Time
		wantIdle string
	}{
		{"running", lastEntry.Add(59 * time.Second), ""},
		{"running", lastEntry.Add(6 * time.Minute), "idle 6m0s"},
		{"completed", lastEntry.Add(6 * time.Minute), ""},
	} {
		task := `{"id":"a1","type":"local_agent","status":` + jsonText(tc.status) + `,"tokenCount":10}`
		got, _ := renderSubagentAt(t, session, task, tc.now)
		if tc.wantIdle == "" && strings.Contains(got, "idle") {
			t.Fatalf("%s at +%s rendered idle: %q", tc.status, tc.now.Sub(lastEntry), got)
		}
		if tc.wantIdle != "" && !strings.Contains(got, tc.wantIdle) {
			t.Fatalf("%s at +%s = %q, want %q", tc.status, tc.now.Sub(lastEntry), got, tc.wantIdle)
		}
	}
}

func TestRenderSubagentsOneLinePerTaskInOrder(t *testing.T) {
	got, err := RenderSubagents([]byte(`{"tasks":[
		{"id":"first","tokenCount":10,"contextWindowSize":100},
		{"id":"skipped"},
		{"id":"second","tokenCount":20,"contextWindowSize":100}]}`), subagentNow, t.TempDir(), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("RenderSubagents: %v", err)
	}
	var ids []string
	for _, line := range strings.Split(strings.TrimSuffix(got, "\n"), "\n") {
		var row subagentRow
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("line %q: %v", line, err)
		}
		ids = append(ids, row.ID)
	}
	if strings.Join(ids, ",") != "first,second" {
		t.Fatalf("row ids = %v, want [first second]", ids)
	}
}

func TestRenderSubagentsRejectsMalformedInput(t *testing.T) {
	got, err := RenderSubagents([]byte(`{"tasks":`), subagentNow, t.TempDir(), &bytes.Buffer{})
	if err == nil {
		t.Fatalf("malformed payload rendered %q with no error", got)
	}
	if !strings.Contains(err.Error(), "parse subagent row context") {
		t.Fatalf("error does not name what failed: %v", err)
	}
}

// Claude Code draws the row body faint in its muted theme colour; the row
// opens by cancelling the faint and dims nothing but its │ separators.
func TestRenderSubagentsRowIsBrightNotFaint(t *testing.T) {
	session := subagentSession(t, map[string][]string{"a1": agentTranscriptLines}, map[string]string{"a1": "tracer"})
	payload := `{"transcript_path":` + jsonText(session) + `,"tasks":[{"id":"a1","type":"local_agent",` +
		`"status":"running","label":"x","model":"claude-opus-5-5","contextWindowSize":1000,"tokenCount":10,` +
		`"tokenSamples":[5,10]}]}`
	got, err := RenderSubagents([]byte(payload), subagentNow, t.TempDir(), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	var row subagentRow
	if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &row); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(row.Content, rowOpen) {
		t.Fatalf("row does not open by cancelling Claude Code's faint: %q", row.Content)
	}
	withoutSeparators := strings.ReplaceAll(row.Content, sep, " ")
	if strings.Count(withoutSeparators, dim) != 1 { // makeBar's empty cells are the one sanctioned dim run
		t.Fatalf("dim text outside the separators and the gauge's empty cells: %q", withoutSeparators)
	}
}

// A payload without a session transcript cannot name the role either: the row
// says "role ?", never a name trailed by an empty role.
func TestRenderSubagentsNoSessionTranscriptMarksTheRole(t *testing.T) {
	got, warned := renderOneSubagent(t, "",
		`{"id":"x","name":"scout","type":"local_agent","status":"running","contextWindowSize":1000,"tokenCount":10}`)
	if !strings.Contains(got, "│ scout·role ? │") || strings.Count(warned, "names no session transcript") != 1 {
		t.Fatalf("content = %q warn = %q, want scout·role ? and the cause once", got, warned)
	}
}

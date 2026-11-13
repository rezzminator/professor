package statusline

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/pricing"
)

// agentTranscriptLines is a sub-agent transcript with the traps a real one
// carries: a streamed assistant line repeated with the same tool_use id, a
// tool_result that quotes tool_use text, and a torn final line mid-write —
// plus one errored tool result and one compact boundary, run from /work/repo/pfm. Its two responses
// bill $0.01/2.0K/35 on opus-5-5: m1 counts once across its repeated lines.
var agentTranscriptLines = []string{
	`{"type":"user","timestamp":"2026-09-24T10:00:00Z","cwd":"/work/repo/pfm","message":{"role":"user","content":"go"}}`,
	`{"type":"assistant","timestamp":"2026-09-24T10:00:05Z","message":{"id":"m1","model":"claude-opus-5-5",` +
		`"content":[{"type":"tool_use","id":"t1","name":"Read"}],` +
		`"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":990}}}`,
	`{"type":"assistant","timestamp":"2026-09-24T10:00:06Z","message":{"id":"m1","model":"claude-opus-5-5",` +
		`"content":[{"type":"tool_use","id":"t1","name":"Read"}],` +
		`"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":0,"cache_creation_input_tokens":990}}}`,
	`{"type":"user","timestamp":"2026-09-24T10:00:07Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1",` +
		`"content":"{\"type\":\"tool_use\",\"id\":\"quoted\"}"}]}}`,
	`{"type":"user","timestamp":"2026-09-24T10:00:08Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1",` +
		`"is_error":true,"content":"exit status 1"}]}}`,
	`{"type":"system","subtype":"compact_boundary","timestamp":"2026-09-24T10:00:50Z",` +
		`"compactMetadata":{"trigger":"auto","preTokens":167189}}`,
	`{"type":"assistant","timestamp":"2026-09-24T10:01:30Z","message":{"id":"m2","model":"claude-opus-5-5",` +
		`"content":[{"type":"tool_use","id":"t2","name":"Bash"}],` +
		`"usage":{"input_tokens":20,"output_tokens":30,"cache_read_input_tokens":940,"cache_creation_input_tokens":40,` +
		`"cache_creation":{"ephemeral_5m_input_tokens":40,"ephemeral_1h_input_tokens":0}}}}`,
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

// testPrices is a served table holding the rows the fixtures' models resolve
// to; claude-haiku-4 and claude-nonesuch-9 stay unpriced.
func testPrices(t *testing.T) *pricing.Prices {
	t.Helper()
	table, err := pricing.DecodeTable([]byte(`{"version":2,"fetched_at":"2026-10-07T23:02:05Z",
		"sources":[{"provider":"anthropic","url":"https://platform.claude.com/docs/en/about-claude/pricing.md"}],
		"rows":[
		{"key":"claude-opus-4-1","engine":"claude","in":15,"out":75,"hit":1.5,"w5m":18.75,"w1h":30},
		{"key":"claude-opus-5-5","engine":"claude","in":4,"out":20,"hit":0.2,"w5m":5,"w1h":8},
		{"key":"claude-sonnet-5","engine":"claude","in":2,"out":10,"hit":0.2,"w5m":2.5,"w1h":4},
		{"key":"claude-sonnet-5-5","engine":"claude","in":2,"out":10,"hit":0.2,"w5m":2.5,"w1h":4},
		{"key":"gpt-6-sol","engine":"codex","in":2,"out":10,"cached":0.2,
		"long":{"above":272000,"in":4,"out":15,"cached":0.4}}]}`), "status line test prices")
	if err != nil {
		t.Fatalf("test price table: %v", err)
	}
	prices, err := pricing.ServeTable(table, pricing.Origin{From: pricing.FromEmbedded}, "")
	if err != nil {
		t.Fatalf("serve test price table: %v", err)
	}
	return &prices
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
	return renderSubagentWith(t, session, task, now, 200)
}

func renderOneSubagentColumns(t *testing.T, session, task string, columns int) (content, warned string) {
	t.Helper()
	return renderSubagentWith(t, session, task, subagentNow, columns)
}

func renderSubagentWith(t *testing.T, session, task string, now time.Time, columns int) (content, warned string) {
	t.Helper()
	rows, warned := renderFixture(t, rowsFixture{session: session, now: now, columns: columns}, task)
	if len(rows) != 1 {
		t.Fatalf("want exactly one row, got %d: %q", len(rows), rows)
	}
	for _, raw := range rows {
		content = stripANSICodes(raw)
	}
	return content, warned
}

// fixtureTask is a task as a test writes it: what the meta file names, and its
// status — only an agent whose status is not ended counts as running.
type fixtureTask struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Label             string `json:"label"`
	Model             string `json:"model"`
	Effort            string `json:"effort"`
	StartTime         int64  `json:"startTime"`
	ContextWindowSize int64  `json:"contextWindowSize"`
	Status            string `json:"status"`
}

// rowsFixture is what renderFixture renders under: zero fields take the
// defaults (a fresh sid dir, the test prices unless noPrices, no env, 200
// columns, subagentNow, the session cwd /work/repo).
type rowsFixture struct {
	session, sessionID, sidDir, home string
	sessionModel                     string
	sessionWindow                    int64
	noPrices                         bool
	env                              paths.Env
	columns                          int
	now                              time.Time
}

// renderFixture renders every task's row through renderAgentRows and returns
// each raw row by task id, and the warnings.
func renderFixture(t *testing.T, fixture rowsFixture, tasks ...string) (rows map[string]string, warned string) {
	t.Helper()
	if fixture.sidDir == "" {
		fixture.sidDir = t.TempDir()
	}
	if fixture.env == nil {
		fixture.env = noEnv
	}
	if fixture.columns == 0 {
		fixture.columns = 200
	}
	if fixture.now.IsZero() {
		fixture.now = subagentNow
	}
	prices := testPrices(t)
	if fixture.noPrices {
		prices = nil
	}
	data := agentRowsInput{
		SessionID:      fixture.sessionID,
		TranscriptPath: fixture.session,
		Cwd:            "/work/repo",
		Columns:        fixture.columns,
		SessionModel:   fixture.sessionModel,
		SessionWindow:  fixture.sessionWindow,
	}
	live := map[string]bool{}
	for _, raw := range tasks {
		var task fixtureTask
		if err := json.Unmarshal([]byte(raw), &task); err != nil {
			t.Fatalf("fixture task %s: %v", raw, err)
		}
		data.Tasks = append(data.Tasks, subagentTask{
			ID: task.ID, Name: task.Name, Label: task.Label, Model: task.Model, Effort: task.Effort,
			StartTime: task.StartTime, ContextWindowSize: task.ContextWindowSize,
		})
		switch task.Status {
		case "completed", "failed", "killed":
		default:
			live[task.ID] = true
		}
	}
	var warn bytes.Buffer
	got := renderAgentRows(data, live, fixture.now, fixture.sidDir, prices, fixture.home, fixture.env,
		newTranscriptScans("", &warn), &warn)
	if len(got) != len(data.Tasks) {
		t.Fatalf("renderAgentRows gave %d rows for %d tasks: %q", len(got), len(data.Tasks), got)
	}
	rows = map[string]string{}
	for index, row := range got {
		rows[data.Tasks[index].ID] = row
	}
	return rows, warn.String()
}

func TestRenderAgentRowsRowBodies(t *testing.T) {
	quiet := []string{`{"type":"user","timestamp":"2026-09-24T10:02:00Z","message":{"role":"user","content":"go"}}`}
	session := subagentSession(t, map[string][]string{
		"a1":    agentTranscriptLines,
		"fresh": agentTranscriptLines[:1],
		"g1":    quiet, "g2": quiet, "g3": quiet, "g4": quiet, "g5": quiet, "g0": quiet, "gw": quiet,
		"same": {`{"type":"user","timestamp":"2026-09-24T10:02:00Z","cwd":"/work/repo/","message":{"content":"go"}}`},
	}, map[string]string{
		"a1": "tracer", "fresh": "general-purpose", "g1": "general-purpose", "g2": "general-purpose",
		"g3": "general-purpose", "g4": "general-purpose", "g5": "general-purpose", "g0": "general-purpose",
		"gw": "general-purpose", "same": "general-purpose",
	})
	task := func(fields string) string {
		return `{"startTime":` + jsonText(subagentStart.UnixMilli()) + `,` + fields + `}`
	}
	cases := []struct {
		name string
		task string
		want string // ANSI-stripped content
	}{
		{
			name: "running agent: every segment, errors and compactions included",
			task: task(`"id":"a1","name":"scout","label":"map the resolver",` +
				`"model":"claude-opus-5-5[1m]","effort":"high","contextWindowSize":1000000`),
			want: "▱▱▱▱▱▱▱▱ 0% 1.0K/1.0M Σ$0.01/2.0K/35/2│scout·tracer│opus·▰▰▰ high│running 2m:0s│1 error│💾5m✓3m:8s 94%│" +
				"⟲1│pfm│map the resolver",
		},
		{
			name: "the model its calls went to wins over the alias it was spawned with",
			task: task(`"id":"a1","label":"x","model":"haiku","contextWindowSize":1000000`),
			want: "▱▱▱▱▱▱▱▱ 0% 1.0K/1.0M Σ$0.01/2.0K/35/2│tracer│opus│running 2m:0s│1 error│💾5m✓3m:8s 94%│⟲1│pfm│x",
		},
		{
			name: "no model turn yet: unknown context, zero tools, an empty cache, and idle since its prompt",
			task: task(`"id":"fresh","label":"x","model":"haiku","contextWindowSize":200000`),
			want: "?/200.0K Σ$0.00/0/0/0│general-purpose│haiku│running 2m:0s│idle 2m0s│💾–│pfm│x",
		},
		{
			name: "no model and no window: the row still renders, context unknown",
			task: task(`"id":"fresh","label":"x"`),
			want: "?/? Σ$0.00/0/0/0│general-purpose│running 2m:0s│idle 2m0s│💾–│pfm│x",
		},
		{
			name: "a gpt- agent renders its tier then its version",
			task: `{"id":"g1","name":"sol","model":"gpt-6-sol","effort":"low"}`,
			want: "?/? ctx missing Σ$0.00/0/0/0│sol·general-purpose│sol-6·▰ low│running│💾–",
		},
		{
			name: "a gpt- agent's dotted version survives",
			task: `{"id":"g2","name":"bot","model":"gpt-5.6-terra","effort":"high"}`,
			want: "?/? ctx missing Σ$0.00/0/0/0│bot·general-purpose│terra-5.6·▰▰▰ high│running│💾–",
		},
		{
			name: "a bare gpt- version has no tier and renders whole",
			task: `{"id":"g3","name":"bot","model":"gpt-6"}`,
			want: "?/? ctx missing Σ$0.00/0/0/0│bot·general-purpose│gpt-6│running│💾–",
		},
		{
			name: "a gpt- agent on the priority tier renders its tier, not the -fast suffix",
			task: `{"id":"g4","name":"bot","model":"gpt-6-sol-fast[1m]"}`,
			want: "?/? ctx missing Σ$0.00/0/0/0│bot·general-purpose│sol-6│running│💾–",
		},
		{
			name: "a bare gpt- version on the priority tier renders whole, without the -fast suffix",
			task: `{"id":"g5","name":"bot","model":"gpt-5.5-fast"}`,
			want: "?/? ctx missing Σ$0.00/0/0/0│bot·general-purpose│gpt-5.5│running│💾–",
		},
		{
			name: "a gateway agent's harness window is not authoritative",
			task: `{"id":"g0","name":"bot","model":"gpt-6-sol","contextWindowSize":200000}`,
			want: "?/? ctx missing Σ$0.00/0/0/0│bot·general-purpose│sol-6│running│💾–",
		},
		{
			name: "a gateway asset reference names its model's family",
			task: `{"id":"gw","name":"bot","model":"gw:catalog/claude-opus-5-5[1m]"}`,
			want: "?/? Σ$0.00/0/0/0│bot·general-purpose│opus│running│💾–",
		},
		{
			name: "an agent in the session's own directory shows no cwd",
			task: `{"id":"same","model":"haiku","label":"x"}`,
			want: "?/? Σ$0.00/0/0/0│general-purpose│haiku│running│💾–│x",
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

// An agent on the session's model version shares the session's window; one on
// another model has no window to borrow and shows "/?".
func TestRenderAgentRowsSharesTheSessionWindowOnlyOnItsModel(t *testing.T) {
	session := subagentSession(t, map[string][]string{"fresh": agentTranscriptLines[:1]},
		map[string]string{"fresh": "general-purpose"})
	for _, tc := range []struct{ model, want string }{
		{"claude-opus-5-5", "?/1.0M Σ$0.00/0/0/0│general-purpose│opus│running 2m:0s│idle 2m0s│💾–│pfm│x"},
		{"haiku", "?/? Σ$0.00/0/0/0│general-purpose│haiku│running 2m:0s│idle 2m0s│💾–│pfm│x"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			rows, warned := renderFixture(t, rowsFixture{
				session: session, sessionModel: "claude-opus-5-5[1m]", sessionWindow: 1_000_000,
			}, `{"id":"fresh","label":"x","model":"`+tc.model+`","startTime":`+jsonText(subagentStart.UnixMilli())+`}`)
			if got := stripANSICodes(rows["fresh"]); got != tc.want || warned != "" {
				t.Fatalf("row =\n  %q\nwant\n  %q (warned %q)", got, tc.want, warned)
			}
		})
	}
}

// A row names the agent type it was launched as, never the family its
// definition's role key names: rr-pro and rr-pro-max must not both read rr.
func TestRenderAgentRowsIdentityIsTheLaunchedAgentType(t *testing.T) {
	repo := t.TempDir()
	definition := filepath.Join(repo, ".claude", "agents", "rr-pro.md")
	if err := os.MkdirAll(filepath.Dir(definition), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(definition, []byte("---\nname: rr-pro\nrole: rr\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session := subagentSession(
		t,
		map[string][]string{"a1": agentTranscriptLines[:1]},
		map[string]string{"a1": "rr-pro"},
	)
	got, _ := renderOneSubagent(t, session, `{"id":"a1","model":"claude-opus-5-5"}`)
	if content := got; !strings.Contains(content, "│rr-pro│") {
		t.Fatalf("content = %q, want the launched type rr-pro", content)
	}
}

// An unreadable transcript is a failure to look: the row says "?" for both
// transcript facts and the cause is reported, never rendered as zero tools.
func TestRenderAgentRowsUnreadableTranscriptIsNotZero(t *testing.T) {
	session := subagentSession(t, nil, nil)
	got, warned := renderOneSubagent(t, session,
		`{"id":"gone","status":"running","contextWindowSize":1000}`)
	if got != "?/1.0K Σ$?/?/?/?│role ?│running│💾!" {
		t.Fatalf("content = %q, want the ? markers", got)
	}
	for _, cause := range []string{"row gone: read sub-agent meta", "row gone: open sub-agent transcript"} {
		if !strings.Contains(warned, cause) {
			t.Fatalf("warn = %q, want %q", warned, cause)
		}
	}
}

// A meta file without agentType is a failure to read the role, not "no role".
func TestRenderAgentRowsMetaWithoutRoleIsNotEmpty(t *testing.T) {
	session := subagentSession(t, map[string][]string{"m": agentTranscriptLines[:1]}, map[string]string{"m": ""})
	got, warned := renderOneSubagent(t, session,
		`{"id":"m","status":"running"}`)
	if !strings.HasPrefix(got, "?/? Σ$0.00/0/0/0│role ?│running") ||
		!strings.Contains(warned, "names no agentType") {
		t.Fatalf("content = %q warn = %q, want role ? and the cause", got, warned)
	}
}

// Quiet time turns yellow past a minute and red past five.
func TestRenderAgentRowsIdleOnlyWhileRunning(t *testing.T) {
	session := subagentSession(t, map[string][]string{"a1": agentTranscriptLines}, map[string]string{"a1": "tracer"})
	lastEntry := time.Date(2026, 9, 24, 10, 1, 30, 0, time.UTC)
	for _, tc := range []struct {
		status   string
		now      time.Time
		wantIdle string
	}{
		{"running", lastEntry.Add(59 * time.Second), ""},
		{"running", lastEntry.Add(6 * time.Minute), "idle 6m0s"},
	} {
		task := `{"id":"a1","status":` + jsonText(tc.status) + `}`
		got, _ := renderSubagentAt(t, session, task, tc.now)
		if tc.wantIdle == "" && strings.Contains(got, "idle") {
			t.Fatalf("%s at +%s rendered idle: %q", tc.status, tc.now.Sub(lastEntry), got)
		}
		if tc.wantIdle != "" && !strings.Contains(got, tc.wantIdle) {
			t.Fatalf("%s at +%s = %q, want %q", tc.status, tc.now.Sub(lastEntry), got, tc.wantIdle)
		}
	}
}

// Claude Code draws the status line in the terminal's own colours; a row dims
// nothing but its│separators and the gauge's empty cells.
func TestRenderAgentRowsRowDimsOnlyItsSeparators(t *testing.T) {
	session := subagentSession(t, map[string][]string{"a1": agentTranscriptLines}, map[string]string{"a1": "tracer"})
	rows, _ := renderPayload(t, session, subagentNow,
		`{"id":"a1","label":"x","model":"claude-opus-5-5","contextWindowSize":1000}`)
	withoutSeparators := strings.ReplaceAll(rows["a1"], rowSep, " ")
	if strings.Count(withoutSeparators, dim) != 1 { // makeBar's empty cells are the one sanctioned dim run
		t.Fatalf("dim text outside the separators and the gauge's empty cells: %q", withoutSeparators)
	}
}

// A payload without a session transcript cannot name the role either: the row
// says "role ?", never a name trailed by an empty role.
func TestRenderAgentRowsNoSessionTranscriptMarksTheRole(t *testing.T) {
	got, warned := renderOneSubagent(t, "",
		`{"id":"x","name":"scout","status":"running","contextWindowSize":1000}`)
	if !strings.Contains(got, "│scout·role ?│") || strings.Count(warned, "names no session transcript") != 1 {
		t.Fatalf("content = %q warn = %q, want scout·role ? and the cause once", got, warned)
	}
}

// A sub-agent's own transcript is all sidechain records; its cache window is
// read from them, in the main line's shape, with the TTL its write used.
func TestRenderAgentRowsCacheWindowFromItsOwnTranscript(t *testing.T) {
	session := subagentSession(t, map[string][]string{"s1": {
		`{"type":"user","isSidechain":true,"timestamp":"2026-09-24T10:01:00Z","message":{"role":"user","content":"go"}}`,
		`{"type":"assistant","isSidechain":true,"timestamp":"2026-09-24T10:01:40Z","message":{"content":[{"type":"text","text":"ok"}],` +
			`"usage":{"input_tokens":2,"cache_read_input_tokens":9944,"cache_creation_input_tokens":10357,` +
			`"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":10357}}}}`,
	}}, map[string]string{"s1": "gitter"})
	got, _ := renderOneSubagent(t, session,
		`{"id":"s1","status":"running","contextWindowSize":1000}`)
	if !strings.HasSuffix(got, "│💾1h✓59m:0s 48%") {
		t.Fatalf("content = %q, want the agent's own 1h window from its sidechain records and 48%%", got)
	}
}

// A clone prices.json that cannot be used leaves the rows priced from the
// embedded copy; the cause still reaches stderr, never a silent fallback.
func TestLoadServedPricesNamesAnUnusableClonePricesFile(t *testing.T) {
	home, clone := t.TempDir(), t.TempDir()
	file := filepath.Join(clone, filepath.FromSlash(pricing.FileRel))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	file, err := pricing.CloneFile(home)
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if prices := LoadServedPrices(home, "", &stderr, "pfm statusline"); prices == nil {
		t.Fatalf("LoadServedPrices = nil, want the embedded table; stderr %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), file) {
		t.Fatalf("stderr = %q, want the unusable clone prices file %s named", stderr.String(), file)
	}
}

// TestRecordTranscriptLineReadsEachBlockOnItsOwn: the blocks decode through
// transcript.DecodeContent, so one odd element never hides an errored result
// beside it (INTENDED: the list used to fail whole and count nothing).
func TestRecordTranscriptLineReadsEachBlockOnItsOwn(t *testing.T) {
	fold := newActivityFold(nil)
	recordTranscriptLine([]byte(`{"type":"user","message":{"content":["x",{"type":"tool_result","is_error":true}]}}`),
		fold)
	recordTranscriptLine([]byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t9"},7]}}`),
		fold)
	if _, used := fold.Seen["t9"]; fold.Errors != 1 || !used {
		t.Fatalf("errors = %d, seen = %v; want the one errored result and the t9 call", fold.Errors, fold.Seen)
	}
}

// gptAgentLines is a sub-agent transcript the way the gpt-gateway's chatgpt
// route leaves it, taken from a live gpt-6-luna tracer: the translated usage
// counts the uncached remainder in input_tokens (the gateway log for the
// second call reads input=7619 cached=5888, the fourth input=13719
// cached=12032), the cache_creation split is all zeros because OpenAI names
// no cache lifetime, and each call first writes a message_start line whose
// usage is the gateway's estimate of the whole prompt with no cache fields.
// The three completed calls hit 77%, 87% and 87%.
var gptAgentLines = []string{
	`{"type":"user","timestamp":"2026-09-24T10:00:00Z","message":{"role":"user","content":"go"}}`,
	`{"type":"assistant","timestamp":"2026-09-24T10:00:30Z","message":{"id":"msg_a","model":"gpt-6-luna",` +
		`"stop_reason":"tool_use","content":[{"type":"tool_use","id":"call_a","name":"Bash"}],` +
		`"usage":{"input_tokens":1731,"cache_creation_input_tokens":0,"cache_read_input_tokens":5888,"output_tokens":30,` +
		`"output_tokens_details":{"thinking_tokens":0},"service_tier":"standard",` +
		`"cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0},"inference_geo":"",` +
		`"iterations":[],"speed":"standard"}}}`,
	`{"type":"user","timestamp":"2026-09-24T10:00:32Z","message":{"content":[{"type":"tool_result",` +
		`"tool_use_id":"call_a","content":"ok"}]}}`,
	`{"type":"assistant","timestamp":"2026-09-24T10:00:35Z","message":{"id":"msg_b","model":"gpt-6-luna",` +
		`"stop_reason":null,"content":[{"type":"text","text":"checking"}],` +
		`"usage":{"input_tokens":13650,"output_tokens":0}}}`,
	`{"type":"assistant","timestamp":"2026-09-24T10:00:40Z","message":{"id":"msg_b","model":"gpt-6-luna",` +
		`"stop_reason":"end_turn","content":[{"type":"text","text":"checking"}],` +
		`"usage":{"input_tokens":1687,"cache_creation_input_tokens":0,"cache_read_input_tokens":12032,"output_tokens":249,` +
		`"output_tokens_details":{"thinking_tokens":0},"service_tier":"standard",` +
		`"cache_creation":{"ephemeral_1h_input_tokens":0,"ephemeral_5m_input_tokens":0},"inference_geo":"",` +
		`"iterations":[],"speed":"standard"}}}`,
	`{"type":"user","timestamp":"2026-09-24T10:00:50Z","message":{"role":"user","content":"again"}}`,
	`{"type":"assistant","timestamp":"2026-09-24T10:01:00Z","message":{"id":"msg_c","model":"gpt-6-luna",` +
		`"stop_reason":null,"content":[{"type":"text","text":"working"}],` +
		`"usage":{"input_tokens":14200,"output_tokens":0}}}`,
}

// An OpenAI-served row's cache segment carries the hit share of its newest completed
// call, counted from the translated usage like the gateway counts it, and no
// countdown: OpenAI names no cache lifetime, so the window is a deliberate
// "∅" — not the "?" of a transcript with no request to count from, and not the
// "!" of a transcript that could not be read. The message_start line of the
// call still streaming reports no cache fields and must not read as 0%.
func TestRenderAgentRowsOpenAICacheShowsHitWithoutAWindow(t *testing.T) {
	task := func(id string) string {
		return `{"id":"` + id + `","status":"running","model":"gpt-6-luna[1m]",` +
			`"contextWindowSize":1000000}`
	}
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"completed call after a streamed one", gptAgentLines[:6], "│💾∅ 87%"},
		{"a call still streaming keeps the last completed hit", gptAgentLines, "│💾∅ 87%"},
		{"first call still streaming: no hit yet", append(gptAgentLines[:1:1], gptAgentLines[3]), "│💾–"},
		{"first completed call", gptAgentLines[:3], "│💾∅ 77%"},
		// A fresh agent's first call reads nothing from the cache: the gateway
		// still reports both counters, at zero, and that is a 0% call, not a
		// line without counters.
		{"a cold call reporting zero counters is 0%", []string{
			gptAgentLines[0],
			`{"type":"assistant","timestamp":"2026-09-24T10:00:30Z","message":{"id":"msg_z","model":"gpt-6-luna",` +
				`"stop_reason":"end_turn","content":[{"type":"text","text":"done"}],` +
				`"usage":{"input_tokens":7619,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":30}}}`,
		}, "│💾∅ 0%"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session := subagentSession(t, map[string][]string{"g1": tc.lines}, map[string]string{"g1": "tracer"})
			got, _ := renderOneSubagent(t, session, task("g1"))
			if !strings.HasSuffix(got, tc.want) && !strings.Contains(got, tc.want+"│") {
				t.Fatalf("content = %q, want the cache segment %q", got, tc.want)
			}
			if strings.Contains(got, "💾?") || strings.Contains(got, "✓") || strings.Contains(got, "✗") {
				t.Fatalf("content = %q: an OpenAI-served row draws no countdown and no unknown-tier ?", got)
			}
		})
	}
	t.Run("an unreadable transcript is still the failure marker", func(t *testing.T) {
		session := subagentSession(t, map[string][]string{}, map[string]string{"g2": "tracer"})
		got, _ := renderOneSubagent(t, session, task("g2"))
		if !strings.Contains(got, "💾!") || strings.Contains(got, "∅") {
			t.Fatalf("content = %q, want 💾! and no unknown-window marker", got)
		}
	})
	t.Run("the model the transcript records names the provider when the payload names none", func(t *testing.T) {
		session := subagentSession(t,
			map[string][]string{"g3": gptAgentLines[:6]}, map[string]string{"g3": "tracer"})
		got, _ := renderOneSubagent(t, session,
			`{"id":"g3","status":"running","contextWindowSize":1000000}`)
		if !strings.Contains(got, "│💾∅ 87%") {
			t.Fatalf("content = %q, want the unknown window and 87%%", got)
		}
	})
}

// A Claude row keeps its window and its hit exactly: the length from the cache
// write's split, the countdown from its newest request, and a message_start
// line that carries cache fields counts as a real call.
func TestRenderAgentRowsClaudeCacheKeepsItsWindowBesideOpenAI(t *testing.T) {
	session := subagentSession(t, map[string][]string{"c1": {
		`{"type":"user","timestamp":"2026-09-24T10:00:00Z","message":{"role":"user","content":"go"}}`,
		`{"type":"assistant","timestamp":"2026-09-24T10:01:00Z","message":{"id":"m1","model":"claude-opus-5-5",` +
			`"stop_reason":null,"content":[{"type":"text","text":"a"}],` +
			`"usage":{"input_tokens":6,"cache_read_input_tokens":900,"cache_creation_input_tokens":94,` +
			`"cache_creation":{"ephemeral_5m_input_tokens":94,"ephemeral_1h_input_tokens":0},"output_tokens":1}}}`,
	}}, map[string]string{"c1": "tracer"})
	got, _ := renderOneSubagent(t, session,
		`{"id":"c1","status":"running","model":"claude-opus-5-5[1m]",`+
			`"contextWindowSize":1000000}`)
	if !strings.Contains(got, "│💾5m✓3m:0s 90%") {
		t.Fatalf("content = %q, want the Claude row's 5m window and 90%%", got)
	}
}

// The gauge rounds to the nearest percent, so 69.1K of 1.0M reads 7%, not a
// truncated 6%; a window not yet full never reads 100%.

// An override that cannot be used never costs the table: dollars are priced by
// the published table beside the warning naming the override.
func TestLoadServedPricesPricesByThePublishedTableBesideABadOverride(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "pfm.config.json")
	override := pfmconfig.PricesPath(configPath)
	if err := os.WriteFile(override, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	prices := LoadServedPrices(home, configPath, &stderr, "pfm statusline")
	if prices == nil || len(prices.Rows) == 0 {
		t.Fatalf("prices = %+v, want the published table's rows beside the override's error", prices)
	}
	want := "pfm statusline: price override unusable, pricing from the published table: "
	if !strings.HasPrefix(stderr.String(), want) || !strings.Contains(stderr.String(), override) {
		t.Fatalf("stderr = %q, want it to open %q and name %s", stderr.String(), want, override)
	}
}

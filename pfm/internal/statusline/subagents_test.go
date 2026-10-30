package statusline

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/pricing"
)

// agentTranscriptLines is a sub-agent transcript with the traps a real one
// carries: a streamed assistant line repeated with the same tool_use id, a
// tool_result that quotes tool_use text, and a torn final line mid-write —
// plus one errored tool result and one compact boundary. Its two responses
// bill $0.01/2.0K/35 on opus-5-5: m1 counts once across its repeated lines.
var agentTranscriptLines = []string{
	`{"type":"user","timestamp":"2026-09-24T10:00:00Z","message":{"role":"user","content":"go"}}`,
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
	payload := `{"transcript_path":` + jsonText(session) + `,"cwd":"/work/repo","columns":` + jsonText(columns) +
		`,"tasks":[` + task + `]}`
	var warn bytes.Buffer
	got, err := RenderSubagents([]byte(payload), now, t.TempDir(), testPrices(t), "", noEnv, "", &warn)
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
			want: "▰▰▱▱▱▱▱▱ 31% 312.0K/1.0M Σ$0.01/2.0K/35/2│scout·tracer│opus·▰▰▰ high│running 2m:0s│1 error│💾5m✓3m:8s 94%│" +
				"⟲1│pfm│map the resolver",
		},
		{
			name: "a finished agent's time stops at its transcript's last entry",
			task: task(`"id":"a1","type":"local_agent","status":"completed","label":"x","model":"claude-sonnet-5",` +
				`"contextWindowSize":1000000,"tokenCount":90000,"tokenSamples":[900000,90000]`),
			want: "▱▱▱▱▱▱▱▱ 9% 90.0K/1.0M Σ$0.01/2.0K/35/2│tracer│sonnet│completed 1m:30s│1 error│💾5m✓3m:8s 94%│⟲1│pfm│x",
		},
		{
			name: "no model turn yet: zero tools, an empty cache, and idle since its prompt",
			task: task(`"id":"fresh","type":"local_agent","status":"running","label":"x","model":"haiku",` +
				`"contextWindowSize":200000,"tokenCount":1`),
			want: "▱▱▱▱▱▱▱▱ 0% 1/200.0K Σ$0.00/0/0/0│general-purpose│haiku│running 2m:0s│idle 2m0s│💾–│pfm│x",
		},
		{
			name: "a non-agent task carries no transcript facts; label falls back to the description",
			task: task(`"id":"sh","type":"local_bash","status":"running","description":"from the description",` +
				`"contextWindowSize":200000,"tokenCount":160000`),
			want: "▰▰▰▰▰▰▱▱ 80% 160.0K/200.0K│running 2m:0s│pfm│from the description",
		},
		{
			name: "an effort that is not a string is left out; a name shows without a role; no growth line",
			task: `{"id":"e","name":"bot","status":"running","model":"claude-haiku-4","effort":{"level":"high"},` +
				`"tokenCount":4200,"tokenSamples":[2100,4200]}`,
			want: "4.2K/?│bot│haiku│running",
		},
		{
			name: "a gpt- agent renders its tier then its version",
			task: `{"id":"g1","name":"sol","status":"running","model":"gpt-6-sol","effort":"low",` +
				`"tokenCount":4200,"tokenSamples":[2100,4200]}`,
			want: "4.2K/? ctx missing│sol│sol-6·▰ low│running",
		},
		{
			name: "a gpt- agent's dotted version survives",
			task: `{"id":"g2","name":"bot","status":"running","model":"gpt-5.6-terra","effort":"high",` +
				`"tokenCount":4200,"tokenSamples":[2100,4200]}`,
			want: "4.2K/? ctx missing│bot│terra-5.6·▰▰▰ high│running",
		},
		{
			name: "a bare gpt- version has no tier and renders whole",
			task: `{"id":"g3","name":"bot","status":"running","model":"gpt-6","tokenCount":10}`,
			want: "10/? ctx missing│bot│gpt-6│running",
		},
		{
			name: "a gpt- agent on the priority tier renders its tier, not the -fast suffix",
			task: `{"id":"g4","name":"bot","status":"running","model":"gpt-6-sol-fast[1m]","tokenCount":10}`,
			want: "10/? ctx missing│bot│sol-6│running",
		},
		{
			name: "a bare gpt- version on the priority tier renders whole, without the -fast suffix",
			task: `{"id":"g5","name":"bot","status":"running","model":"gpt-5.5-fast","tokenCount":10}`,
			want: "10/? ctx missing│bot│gpt-5.5│running",
		},
		{
			name: "an agent in the session's own directory shows no cwd",
			task: `{"id":"same","status":"running","cwd":"/work/repo/","model":"haiku","tokenCount":10,"label":"x"}`,
			want: "10/?│haiku│running│x",
		},
		{
			name: "a task with no tokens and no window still gets a row with unknown context",
			task: `{"id":"a5","label":"shell","tokenCount":0}`,
			want: "0/?│shell",
		},
		{
			name: "a gateway agent with zero counters keeps its rich row",
			task: task(`"id":"fresh","type":"local_agent","status":"running","label":"x",` +
				`"model":"gpt-6-sol","tokenCount":0,"contextWindowSize":0`),
			want: "0/? ctx missing Σ$0.00/0/0/0│general-purpose│sol-6│running 2m:0s│idle 2m0s│💾–│pfm│x",
		},
		{
			name: "an adopted agent with missing counters and model still gets a rich row",
			task: task(`"id":"fresh","type":"local_agent","status":"running","label":"x"`),
			want: "?/? Σ$0.00/0/0/0│general-purpose│running 2m:0s│idle 2m0s│💾–│pfm│x",
		},
		{
			name: "zero counters remain known but a gateway harness window is not authoritative",
			task: `{"id":"g0","name":"bot","model":"gpt-6-sol","tokenCount":0,"contextWindowSize":200000}`,
			want: "0/? ctx missing│bot│sol-6",
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

// An agent definition names the role it plays: the identity renders that
// role, read from the project's definitions first — the session cwd's and its
// parents' through the git root, never home's own — then the user's.
func TestRenderSubagentsIdentityIsTheDefinitionsRole(t *testing.T) {
	const task = `{"id":"a1","type":"local_agent","status":"running","model":"gpt-6-sol","effort":"high",` +
		`"tokenCount":4200,"tokenSamples":[2100,4200]}`
	definition := func(frontmatter string) string {
		return "---\n" + frontmatter + "\n---\n\nbody\nrole: not-frontmatter\n"
	}
	cases := []struct {
		name    string
		project map[string]string // file name → body under <project>/.claude/agents
		user    map[string]string // file name → body under the user agents dir
		outer   map[string]string // path under home → body, outside the project
		below   string            // the session cwd under <project>; "" = the project itself
		noGit   bool              // the project is no git repository
		meta    string            // the row's own agentType; "" = flights-foreman-sonnet
		torn    bool              // the user's definition is a directory, unreadable
		want    string
		warn    string // what stderr must carry; "" = nothing
	}{
		{
			name: "the user's definition names its role",
			user: map[string]string{
				"flights-foreman-sonnet.md": definition(
					"name: flights-foreman-sonnet\nmodel: gpt-6-sol\nrole: flights-foreman",
				),
			},
			want: "│flights-foreman│sol-6·▰▰▰ high│",
		},
		{
			name:    "the project's definition wins over the user's",
			project: map[string]string{"flights-foreman-sonnet.md": definition("role: project-foreman")},
			user:    map[string]string{"flights-foreman-sonnet.md": definition("role: flights-foreman")},
			want:    "│project-foreman│sol-6·▰▰▰ high│",
		},
		{
			name: "a quoted role loses its quotes",
			user: map[string]string{"flights-foreman-sonnet.md": definition(`role: "flights-foreman"`)},
			want: "│flights-foreman│sol-6·▰▰▰ high│",
		},
		{
			name: "a definition with no role key keeps the agentType",
			user: map[string]string{"flights-foreman-sonnet.md": definition("name: flights-foreman-sonnet")},
			want: "│flights-foreman-sonnet│sol-6·▰▰▰ high│",
		},
		{
			name: "a role in the body is not a role",
			user: map[string]string{"flights-foreman-sonnet.md": "no frontmatter\nrole: flights-foreman\n"},
			want: "│flights-foreman-sonnet│sol-6·▰▰▰ high│",
		},
		{
			name: "no definition file keeps the agentType",
			want: "│flights-foreman-sonnet│sol-6·▰▰▰ high│",
		},
		{
			name:    "a session below the project root reads the root's definition",
			below:   filepath.Join("pfm", "internal"),
			project: map[string]string{"flights-foreman-sonnet.md": definition("role: project-foreman")},
			user:    map[string]string{"flights-foreman-sonnet.md": definition("role: flights-foreman")},
			want:    "│project-foreman│sol-6·▰▰▰ high│",
		},
		{
			name: "a padded agentType in the meta resolves its definition",
			meta: " flights-foreman-sonnet ",
			user: map[string]string{"flights-foreman-sonnet.md": definition("role: flights-foreman")},
			want: "│flights-foreman│sol-6·▰▰▰ high│",
		},
		{
			name: "an unreadable definition keeps the agentType and says why",
			torn: true,
			want: "│flights-foreman-sonnet│sol-6·▰▰▰ high│",
			warn: "agent definition (rows show flights-foreman-sonnet)",
		},
		{
			name: "a definition above the git root is no project's",
			outer: map[string]string{
				filepath.Join("work", ".claude", "agents", "flights-foreman-sonnet.md"): definition(
					"role: outer-foreman",
				),
			},
			want: "│flights-foreman-sonnet│sol-6·▰▰▰ high│",
		},
		{
			name:  "outside a git repository home's own .claude is not a project's",
			noGit: true,
			outer: map[string]string{
				filepath.Join(".claude", "agents", "flights-foreman-sonnet.md"): definition("role: home-foreman"),
			},
			want: "│flights-foreman-sonnet│sol-6·▰▰▰ high│",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agentType := tc.meta
			if agentType == "" {
				agentType = "flights-foreman-sonnet"
			}
			// Two rows of one type: the definition is read, and an unreadable
			// one reported, once per render.
			session := subagentSession(t,
				map[string][]string{"a1": agentTranscriptLines[:1], "a2": agentTranscriptLines[:1]},
				map[string]string{"a1": agentType, "a2": agentType})
			home, userDir := t.TempDir(), t.TempDir()
			if tc.torn {
				if err := os.Mkdir(filepath.Join(userDir, "flights-foreman-sonnet.md"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			project := filepath.Join(home, "work", "proj")
			cwd := filepath.Join(project, tc.below)
			if err := os.MkdirAll(cwd, 0o700); err != nil {
				t.Fatal(err)
			}
			if !tc.noGit {
				if err := os.Mkdir(filepath.Join(project, ".git"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			files := map[string]string{}
			for name, body := range tc.project {
				files[filepath.Join(project, ".claude", "agents", name)] = body
			}
			for name, body := range tc.user {
				files[filepath.Join(userDir, name)] = body
			}
			for rel, body := range tc.outer {
				files[filepath.Join(home, rel)] = body
			}
			for path, body := range files {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			payload := `{"transcript_path":` + jsonText(session) + `,"cwd":` + jsonText(cwd) +
				`,"columns":200,"tasks":[` + task + `,` + strings.Replace(task, `"a1"`, `"a2"`, 1) + `]}`
			var warn bytes.Buffer
			got, err := RenderSubagents(
				[]byte(payload),
				subagentNow,
				t.TempDir(),
				testPrices(t),
				home,
				noEnv,
				userDir,
				&warn,
			)
			if err != nil {
				t.Fatalf("RenderSubagents: %v", err)
			}
			lines := strings.Split(strings.TrimSpace(got), "\n")
			if len(lines) != 2 {
				t.Fatalf("rows = %q, want two", got)
			}
			for _, line := range lines {
				var row subagentRow
				if err := json.Unmarshal([]byte(line), &row); err != nil {
					t.Fatalf("row %q: %v", line, err)
				}
				if content := stripANSICodes(row.Content); !strings.Contains(content, tc.want) {
					t.Fatalf("row %s content =\n  %q\nwant it to hold\n  %q", row.ID, content, tc.want)
				}
			}
			if tc.warn == "" && warn.Len() != 0 || tc.warn != "" && strings.Count(warn.String(), tc.warn) != 1 {
				t.Fatalf("warn = %q, want %q once", warn.String(), tc.warn)
			}
		})
	}
}

// An edited definition shows on the next render: nothing outlives one.
func TestRenderSubagentsRoleIsReadAgainEachRender(t *testing.T) {
	session := subagentSession(t, map[string][]string{"a1": agentTranscriptLines[:1]},
		map[string]string{"a1": "flights-foreman-sonnet"})
	userDir := t.TempDir()
	path := filepath.Join(userDir, "flights-foreman-sonnet.md")
	render := func() string {
		payload := `{"transcript_path":` + jsonText(session) + `,"cwd":"/work/repo","columns":200,"tasks":[` +
			`{"id":"a1","type":"local_agent","status":"running","model":"gpt-6-sol","tokenCount":10}]}`
		var warn bytes.Buffer
		got, err := RenderSubagents([]byte(payload), subagentNow, t.TempDir(), testPrices(t), "", noEnv, userDir, &warn)
		if err != nil {
			t.Fatalf("RenderSubagents: %v", err)
		}
		var row subagentRow
		if err := json.Unmarshal([]byte(strings.TrimSpace(got)), &row); err != nil {
			t.Fatalf("row %q: %v", got, err)
		}
		return stripANSICodes(row.Content)
	}
	for _, step := range []struct{ body, want string }{
		{"---\nrole: flights-foreman\n---\n", "│flights-foreman│sol-6│"},
		{"---\nrole: flights-lander\n---\n", "│flights-lander│sol-6│"},
	} {
		if err := os.WriteFile(path, []byte(step.body), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := render(); !strings.Contains(got, step.want) {
			t.Fatalf("content = %q, want it to hold %q", got, step.want)
		}
	}
}

// An unreadable transcript is a failure to look: the row says "?" for both
// transcript facts and the cause is reported, never rendered as zero tools.
func TestRenderSubagentsUnreadableTranscriptIsNotZero(t *testing.T) {
	session := subagentSession(t, nil, nil)
	got, warned := renderOneSubagent(t, session,
		`{"id":"gone","type":"local_agent","status":"running","contextWindowSize":1000,"tokenCount":10}`)
	if got != "▱▱▱▱▱▱▱▱ 1% 10/1.0K Σ$?/?/?/?│role ?│running│💾!" {
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
	if !strings.HasPrefix(got, "10/? Σ$0.00/0/0/0│role ?│running") ||
		!strings.Contains(warned, "names no agentType") {
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
		{"id":"unknown"},
		{"label":"no id"},
		{"id":"second","tokenCount":20,"contextWindowSize":100}]}`), subagentNow, t.TempDir(), testPrices(t), "", noEnv, "", &bytes.Buffer{})
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
	if strings.Join(ids, ",") != "first,unknown,second" {
		t.Fatalf("row ids = %v, want [first unknown second]", ids)
	}
}

func TestRenderSubagentsRejectsMalformedInput(t *testing.T) {
	got, err := RenderSubagents(
		[]byte(`{"tasks":`),
		subagentNow,
		t.TempDir(),
		testPrices(t),
		"",
		noEnv,
		"",
		&bytes.Buffer{},
	)
	if err == nil {
		t.Fatalf("malformed payload rendered %q with no error", got)
	}
	if !strings.Contains(err.Error(), "parse subagent row context") {
		t.Fatalf("error does not name what failed: %v", err)
	}
}

// Claude Code draws the row body faint in its muted theme colour; the row
// opens by cancelling the faint and dims nothing but its│separators.
func TestRenderSubagentsRowIsBrightNotFaint(t *testing.T) {
	session := subagentSession(t, map[string][]string{"a1": agentTranscriptLines}, map[string]string{"a1": "tracer"})
	payload := `{"transcript_path":` + jsonText(session) + `,"tasks":[{"id":"a1","type":"local_agent",` +
		`"status":"running","label":"x","model":"claude-opus-5-5","contextWindowSize":1000,"tokenCount":10,` +
		`"tokenSamples":[5,10]}]}`
	got, err := RenderSubagents(
		[]byte(payload),
		subagentNow,
		t.TempDir(),
		testPrices(t),
		"",
		noEnv,
		"",
		&bytes.Buffer{},
	)
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
	withoutSeparators := strings.ReplaceAll(row.Content, rowSep, " ")
	if strings.Count(withoutSeparators, dim) != 1 { // makeBar's empty cells are the one sanctioned dim run
		t.Fatalf("dim text outside the separators and the gauge's empty cells: %q", withoutSeparators)
	}
}

// A payload without a session transcript cannot name the role either: the row
// says "role ?", never a name trailed by an empty role.
func TestRenderSubagentsNoSessionTranscriptMarksTheRole(t *testing.T) {
	got, warned := renderOneSubagent(t, "",
		`{"id":"x","name":"scout","type":"local_agent","status":"running","contextWindowSize":1000,"tokenCount":10}`)
	if !strings.Contains(got, "│scout·role ?│") || strings.Count(warned, "names no session transcript") != 1 {
		t.Fatalf("content = %q warn = %q, want scout·role ? and the cause once", got, warned)
	}
}

// A sub-agent's own transcript is all sidechain records; its cache window is
// read from them, in the main line's shape, with the TTL its write used.
func TestRenderSubagentsCacheWindowFromItsOwnTranscript(t *testing.T) {
	session := subagentSession(t, map[string][]string{"s1": {
		`{"type":"user","isSidechain":true,"timestamp":"2026-09-24T10:01:00Z","message":{"role":"user","content":"go"}}`,
		`{"type":"assistant","isSidechain":true,"timestamp":"2026-09-24T10:01:40Z","message":{"content":[{"type":"text","text":"ok"}],` +
			`"usage":{"input_tokens":2,"cache_read_input_tokens":9944,"cache_creation_input_tokens":10357,` +
			`"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":10357}}}}`,
	}}, map[string]string{"s1": "gitter"})
	got, _ := renderOneSubagent(t, session,
		`{"id":"s1","type":"local_agent","status":"running","contextWindowSize":1000,"tokenCount":10}`)
	if !strings.HasSuffix(got, "│💾1h✓59m:0s 48%") {
		t.Fatalf("content = %q, want the agent's own 1h window from its sidechain records and 48%%", got)
	}
}

// A clone prices.json that cannot be used leaves the rows priced from the
// embedded copy; the cause still reaches stderr, never a silent fallback.
func TestServeSubagentsNamesAnUnusableClonePricesFile(t *testing.T) {
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
	var stdout, stderr bytes.Buffer
	machine := paths.Values{Home: home, SIDDir: t.TempDir()}
	if code := ServeSubagents(strings.NewReader(`{"tasks":[]}`), &stdout, &stderr, machine, noEnv, ""); code != 0 {
		t.Fatalf("ServeSubagents exit %d, want the fail-open 0", code)
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
func TestRenderSubagentsOpenAICacheShowsHitWithoutAWindow(t *testing.T) {
	task := func(id string) string {
		return `{"id":"` + id + `","type":"local_agent","status":"running","model":"gpt-6-luna[1m]",` +
			`"contextWindowSize":1000000,"tokenCount":10}`
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
			`{"id":"g3","type":"local_agent","status":"running","contextWindowSize":1000000,"tokenCount":10}`)
		if !strings.Contains(got, "│💾∅ 87%") {
			t.Fatalf("content = %q, want the unknown window and 87%%", got)
		}
	})
}

// A Claude row keeps its window and its hit exactly: the length from the cache
// write's split, the countdown from its newest request, and a message_start
// line that carries cache fields counts as a real call.
func TestRenderSubagentsClaudeCacheKeepsItsWindowBesideOpenAI(t *testing.T) {
	session := subagentSession(t, map[string][]string{"c1": {
		`{"type":"user","timestamp":"2026-09-24T10:00:00Z","message":{"role":"user","content":"go"}}`,
		`{"type":"assistant","timestamp":"2026-09-24T10:01:00Z","message":{"id":"m1","model":"claude-opus-5-5",` +
			`"stop_reason":null,"content":[{"type":"text","text":"a"}],` +
			`"usage":{"input_tokens":6,"cache_read_input_tokens":900,"cache_creation_input_tokens":94,` +
			`"cache_creation":{"ephemeral_5m_input_tokens":94,"ephemeral_1h_input_tokens":0},"output_tokens":1}}}`,
	}}, map[string]string{"c1": "tracer"})
	got, _ := renderOneSubagent(t, session,
		`{"id":"c1","type":"local_agent","status":"running","model":"claude-opus-5-5[1m]",`+
			`"contextWindowSize":1000000,"tokenCount":10}`)
	if !strings.Contains(got, "│💾5m✓3m:0s 90%") {
		t.Fatalf("content = %q, want the Claude row's 5m window and 90%%", got)
	}
}

// TestStampTurnFailureLeavesOnlyTheTurnUnknown pins that a transcript whose
// mtime cannot be read after its lines were (it went in between) makes the
// agent's turn unread, never the row's activity and spend.
func TestStampTurnFailureLeavesOnlyTheTurnUnknown(t *testing.T) {
	activity := agentActivity{turn: agentTurn{open: true}}
	activity.stampTurn(filepath.Join(t.TempDir(), "agent-gone.jsonl"))
	if activity.err != nil || activity.turn.err == nil {
		t.Fatalf("err = %v, turn.err = %v; want the row readable and the turn unknown", activity.err, activity.turn.err)
	}
	tree := &agentTree{
		open: map[string]turnState{}, reads: map[string]*agentActivity{"gone": &activity}, warned: map[string]bool{},
	}
	if got := tree.turn("gone"); got != turnUnread {
		t.Fatalf("turn = %v, want turnUnread", got)
	}
}

// The gauge rounds to the nearest percent, so 69.1K of 1.0M reads 7%, not a
// truncated 6%; a window not yet full never reads 100%.
func TestRenderSubagentsGaugeRoundsToNearest(t *testing.T) {
	session := subagentSession(t, map[string][]string{}, map[string]string{})
	for tokens, want := range map[int]string{
		69_100:    " 7% 69.1K/1.0M",
		999_600:   " 99% 999.6K/1.0M",
		1_000_000: " 100% 1.0M/1.0M",
	} {
		task := `{"id":"sh","type":"local_bash","status":"running","contextWindowSize":1000000,"tokenCount":` +
			jsonText(tokens) + `}`
		if got, _ := renderOneSubagent(t, session, task); !strings.Contains(got, want) {
			t.Fatalf("tokens %d: content = %q, want %q", tokens, got, want)
		}
	}
}

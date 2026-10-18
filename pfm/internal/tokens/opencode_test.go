package tokens

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// openCodeT0 is 2026-09-21T04:00:00Z, two hours before goldenNow.
const openCodeT0 = int64(1789963200000)

// openCodeSchema is OpenCode's session store, the columns pfm's reader reads.
const openCodeSchema = `
CREATE TABLE project (id TEXT PRIMARY KEY, worktree TEXT NOT NULL, time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL, sandboxes TEXT NOT NULL DEFAULT '[]');
CREATE TABLE session (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, parent_id TEXT, slug TEXT NOT NULL,
  directory TEXT NOT NULL, title TEXT NOT NULL, version TEXT NOT NULL, time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL, time_archived INTEGER);
CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL, data TEXT NOT NULL);
CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL,
  time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL);
INSERT INTO project VALUES ('prj', '/work/garden', 0, 0, '[]');`

// openCodeAssistant is one assistant message's data: provider, model, agent,
// input, output, reasoning, cache read, cache write and OpenCode's cost.
func openCodeAssistant(
	provider, model, agent string,
	input, output, reasoning, read, write int64,
	cost float64,
) string {
	return fmt.Sprintf(`{"role":"assistant","agent":%q,"mode":%q,"providerID":%q,"modelID":%q,"cost":%v,`+
		`"tokens":{"input":%d,"output":%d,"reasoning":%d,"cache":{"read":%d,"write":%d}},"finish":"stop"}`,
		agent, agent, provider, model, cost, input, output, reasoning, read, write)
}

// seedOpenCodeUsage writes root/opencode.db: five sessions, one before the
// window, one outside /work/garden, one a child, one on an unpriced model.
func seedOpenCodeUsage(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close the fixture store: %v", err)
		}
	}()
	if _, err := db.Exec(openCodeSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	sessions := []struct {
		id, parent, dir  string
		created, updated int64
	}{
		{"ses_main01", "", "/work/garden", openCodeT0, openCodeT0 + 600000},
		{"ses_child01", "ses_main01", "/work/garden", openCodeT0 + 60000, openCodeT0 + 120000},
		{"ses_local01", "", "/work/garden", openCodeT0 + 300000, openCodeT0 + 360000},
		{"ses_other01", "", "/work/other", openCodeT0 + 900000, openCodeT0 + 960000},
		{"ses_old01", "", "/work/garden", openCodeT0 - 5*86400000, openCodeT0 - 5*86400000},
	}
	for _, s := range sessions {
		var parent any
		if s.parent != "" {
			parent = s.parent
		}
		if _, err := db.Exec(`INSERT INTO session VALUES (?, 'prj', ?, ?, ?, ?, '1.14.30', ?, ?, NULL)`,
			s.id, parent, s.id, s.dir, "title "+s.id, s.created, s.updated); err != nil {
			t.Fatalf("session %s: %v", s.id, err)
		}
	}
	messages := []struct {
		id, session, data string
		at                int64
	}{
		{
			"msg_m0",
			"ses_main01",
			`{"role":"user","agent":"build","model":{"providerID":"openai","modelID":"gpt-5.6-sol"}}`,
			openCodeT0,
		},
		{"msg_m1", "ses_main01", openCodeAssistant("openai", "gpt-5.6-sol", "build", 1000000, 200000, 100000, 400000, 0,
			0.25), openCodeT0 + 1000},
		{
			"msg_m2",
			"ses_main01",
			openCodeAssistant("openai", "gpt-5.6-sol", "build", 300000, 50000, 0, 1500000, 0, 0.05),
			openCodeT0 + 2000,
		},
		{
			"msg_m3", "ses_main01", openCodeAssistant("openai", "gpt-5.6-sol", "build", 0, 0, 0, 0, 0, 0),
			openCodeT0 + 3000,
		},
		{"msg_c1", "ses_child01", openCodeAssistant("anthropic", "claude-sonnet-4-6", "explore", 20000, 30000, 0,
			1000000, 200000, 0.0123), openCodeT0 + 61000},
		{
			"msg_l1", "ses_local01", openCodeAssistant("bonsai", "bonsai2", "build", 500, 100, 0, 0, 0, 0),
			openCodeT0 + 301000,
		},
		{
			"msg_o1", "ses_other01", openCodeAssistant("openai", "gpt-5.6-sol", "build", 10000, 1000, 0, 0, 0, 0),
			openCodeT0 + 901000,
		},
		{
			"msg_x1", "ses_old01", openCodeAssistant("openai", "gpt-5.6-sol", "build", 7, 7, 0, 0, 0, 0),
			openCodeT0 - 5*86400000,
		},
	}
	for _, m := range messages {
		if _, err := db.Exec(`INSERT INTO message VALUES (?, ?, ?, ?, ?)`, m.id, m.session, m.at, m.at,
			m.data); err != nil {
			t.Fatalf("message %s: %v", m.id, err)
		}
	}
}

func TestOpenCodeModeCountsAndPricesEachMessage(t *testing.T) {
	root := t.TempDir()
	seedOpenCodeUsage(t, root)
	code, stdout, stderr := auditWith(t, Options{}, "--opencode", "--opencode-root", root, "--since", "3d",
		"--prices", fixture(t, "model-cost.json"))
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	lines := strings.Split(stdout, "\n")
	if lines[0] != "TOKEN AUDIT · opencode · last 72h · "+root+" · 4 sessions of 5" {
		t.Errorf("head = %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "pricing: saved table ") {
		t.Errorf("pricing line = %q", lines[1])
	}
	want := []string{
		`data gaps: 1 synthetic/zero-usage calls (dropped: the harness billed nothing for them) · 1 UNPRICED calls ` +
			`{"bonsai/bonsai2":1} — tokens counted, dollars "n/a"; check exact IDs and available rates with pfm ` +
			`model-cost --json --all · 1 cache writes with no 5m/1h split (priced as 5m) · 1 sessions older than the window`,
		"",
		"STARTED          SESSION       MODEL        LABEL                           IN      CACHED        OUT   EST USD" +
			"    OC USD",
		"2026-09-21 04:00 ses_main01    gpt-5.6-sol  main                     3,200,000   1,900,000    350,000    $12.96" +
			"     $0.30",
		"2026-09-21 04:01 ses_child01   claude-sonne subagent (explore)       1,220,000   1,000,000     30,000     $1.56" +
			"     $0.01",
		"2026-09-21 04:15 ses_other01   gpt-5.6-sol  main                        10,000           0      1,000     $0.06" +
			"     $0.00",
		"2026-09-21 04:05 ses_local01   bonsai2      main                           500           0        100       n/a" +
			"     $0.00",
		"",
		"TOTAL 4,811,600 tok · uncached in 1,330,500 · cache write 200,000 · cached in 2,900,000 · out 381,100 " +
			"(incl. 100,000 reasoning) · $14.58",
		"OC USD is OpenCode's own recorded cost, shown beside EST USD and never in its place: $0.31 in all",
		`1 session(s) ran an unpriced model and show "n/a" — their TOKENS are in the total, their DOLLARS are not.`,
		"",
	}
	if got := strings.Join(lines[2:], "\n"); got != strings.Join(want, "\n") {
		t.Errorf("report differs:\n got %s\nwant %s", got, strings.Join(want, "\n"))
	}
}

func TestOpenCodeModeFiltersByProject(t *testing.T) {
	root := t.TempDir()
	seedOpenCodeUsage(t, root)
	code, stdout, stderr := auditWith(t, Options{OpenCodeRoots: []string{root}}, "--opencode", "--since", "3d",
		"--project", "garden", "--prices", fixture(t, "model-cost.json"))
	if code != 0 || stderr != "" {
		t.Fatalf("exit %d stderr %q", code, stderr)
	}
	if !strings.HasPrefix(stdout, "TOKEN AUDIT · opencode · last 72h · "+root+" · 3 sessions of 5\n") ||
		!strings.Contains(stdout, " · 1 sessions outside --project garden") || strings.Contains(stdout, "ses_other01") {
		t.Errorf("stdout = %s", stdout)
	}
}

// openCodeOwnMode is the usage error of a flag --opencode cannot honour.
const openCodeOwnMode = "pfm tokens: --opencode is its own mode: it takes no --codex, --tree, " +
	"--root, --family, --family-rx, --session, --out, --view or --briefs\n"

// A store that is missing or cannot be read is a failure naming it, never an
// empty table; a flag the mode cannot honour is refused, never ignored.
func TestOpenCodeModeRefusesWhatItCannotRead(t *testing.T) {
	prices := fixture(t, "model-cost.json")
	missing := t.TempDir()
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "opencode.db"), []byte("not a database\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		opts   Options
		args   []string
		code   int
		stderr string
	}{
		{
			"no root",
			Options{},
			[]string{"--opencode"},
			1,
			"pfm tokens: no OpenCode root is configured; pass --opencode-root DIR\n",
		},
		{
			"missing store",
			Options{},
			[]string{"--opencode", "--opencode-root", missing},
			1,
			"pfm tokens: no OpenCode store could be read: " + filepath.Join(missing, "opencode.db") + " is missing\n",
		},
		{
			"unreadable store",
			Options{OpenCodeRoots: []string{broken}},
			[]string{"--opencode"},
			1,
			"pfm tokens: no OpenCode store could be read: " + filepath.Join(broken, "opencode.db") + ": ",
		},
		{"two modes", Options{}, []string{"--opencode", "--codex"}, 2, openCodeOwnMode},
		{"a Claude report flag", Options{}, []string{"--opencode", "--out", "report.json"}, 2, openCodeOwnMode},
		{"a Claude filter", Options{}, []string{"--opencode", "--family", "garden"}, 2, openCodeOwnMode},
		{
			"a root without the mode",
			Options{},
			[]string{"--opencode-root", missing},
			2,
			"pfm tokens: --opencode-root reads OpenCode only: pass --opencode with it\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, stdout, stderr := auditWith(t, c.opts, append(c.args, "--prices", prices)...)
			if code != c.code || stdout != "" || !strings.HasPrefix(stderr, c.stderr) {
				t.Errorf("exit %d stdout %q stderr %q, want %d, nothing, %q", code, stdout, stderr, c.code, c.stderr)
			}
		})
	}
}

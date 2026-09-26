package reap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// fakeClaudeAgentsBinary writes a `claude` stand-in that plays `agents
// --json`: it ignores its argv (BusySessions' own shape is covered by
// action.ClaudeSpawn's own tests) and answers with body on stdout, or exits
// non-zero with body on stderr when fail is set.
func fakeClaudeAgentsBinary(t *testing.T, body string, fail bool) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\n"
	if fail {
		script += "printf '%s' " + shQuote(body) + " >&2\nexit 1\n"
	} else {
		script += "printf '%s' " + shQuote(body) + "\n"
	}
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary
}

// shQuote wraps a literal in single quotes for a POSIX shell fixture; the
// bodies this file writes never contain a single quote of their own.
func shQuote(value string) string {
	return "'" + value + "'"
}

// TestClaudeAgentsBusySessionsParsesBusyRows (L1-T5): the production probe
// that decides which chats are too busy for the sweep to touch — read
// straight from `claude agents --json`, not substituted by a test double —
// keeps only the rows the engine itself marked busy.
func TestClaudeAgentsBusySessionsParsesBusyRows(t *testing.T) {
	body := `[{"sessionId":"sess-busy","status":"busy"},{"sessionId":"sess-idle","status":"idle"}]`
	directory := t.TempDir()
	agents := ClaudeAgents{
		Binary:     fakeClaudeAgentsBinary(t, body, false),
		ConfigDirs: []string{directory},
	}
	busy, err := agents.BusySessions(context.Background())
	if err != nil {
		t.Fatalf("BusySessions() = %v", err)
	}
	if _, found := busy["sess-busy"]; !found || len(busy) != 1 {
		t.Fatalf("busy = %v, want exactly {sess-busy}", busy)
	}
}

// TestClaudeAgentsBusySessionsFailsClosedOnQueryError: a query the engine
// itself refused (non-zero exit) must fail the whole probe rather than read
// as "nobody is busy" — the sweep would otherwise reap a chat the engine
// could not vouch for.
func TestClaudeAgentsBusySessionsFailsClosedOnQueryError(t *testing.T) {
	directory := t.TempDir()
	agents := ClaudeAgents{
		Binary:     fakeClaudeAgentsBinary(t, "boom", true),
		ConfigDirs: []string{directory},
	}
	busy, err := agents.BusySessions(context.Background())
	if err == nil {
		t.Fatal("BusySessions() succeeded against a query that exited non-zero")
	}
	if busy != nil {
		t.Fatalf("busy = %v, want nil alongside the error", busy)
	}
}

// TestClaudeAgentsBusySessionsFailsClosedOnMalformedJSON: output the probe
// cannot parse must not read as an empty (safe-to-reap) busy set.
func TestClaudeAgentsBusySessionsFailsClosedOnMalformedJSON(t *testing.T) {
	directory := t.TempDir()
	agents := ClaudeAgents{
		Binary:     fakeClaudeAgentsBinary(t, "not json", false),
		ConfigDirs: []string{directory},
	}
	busy, err := agents.BusySessions(context.Background())
	if err == nil {
		t.Fatal("BusySessions() succeeded against malformed JSON")
	}
	if busy != nil {
		t.Fatalf("busy = %v, want nil alongside the error", busy)
	}
}

// TestClaudeAgentsBusySessionsRefusesWithNoConfigDirs: an empty roster is an
// error, never a silent "nobody is busy".
func TestClaudeAgentsBusySessionsRefusesWithNoConfigDirs(t *testing.T) {
	agents := ClaudeAgents{Binary: "claude"}
	busy, err := agents.BusySessions(context.Background())
	if err == nil {
		t.Fatal("BusySessions() succeeded with no configured account directories")
	}
	if busy != nil {
		t.Fatalf("busy = %v, want nil alongside the error", busy)
	}
}

func TestClaudeAgentsQueriesConfiguredDirsAndUnionsBusyRows(t *testing.T) {
	home := t.TempDir()
	dirs := []string{filepath.Join(home, ".claude"), filepath.Join(home, ".cc", "2"), filepath.Join(home, ".cc", "3")}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	logPath := filepath.Join(home, "queries.log")
	binary := filepath.Join(home, "claude")
	script := "#!/bin/sh\nprintf '%s|%s\\n' \"$CLAUDE_CONFIG_DIR\" \"$*\" >> \"$PFM_TEST_QUERY_LOG\"\n" +
		"case \"$CLAUDE_CONFIG_DIR\" in\n" +
		"  */2) printf '[{\"sessionId\":\"second\",\"status\":\"busy\"}]' ;;\n" +
		"  */3) printf '[{\"sessionId\":\"third\",\"status\":\"busy\"}]' ;;\n" +
		"  *) printf '[]' ;;\n" +
		"esac\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_TEST_QUERY_LOG", logPath)
	machine := pfmconfig.Config{
		Claude: pfmconfig.Claude{Binary: binary},
		Accounts: []pfmconfig.Account{
			{ID: 1, ConfigDir: filepath.Join(home, "wrong-implicit"), Implicit: true},
			{ID: 2, ConfigDir: dirs[1]},
			{ID: 3, ConfigDir: dirs[2]},
			{ID: 4, ConfigDir: dirs[2]},
		},
	}
	agents := NewClaudeAgents(paths.Values{Home: home, Roots: map[pfmengine.ID][]string{
		pfmengine.Claude: {filepath.Join(home, "transcripts", "projects")},
	}}, machine)
	busy, err := agents.BusySessions(context.Background())
	if err != nil || len(busy) != 2 {
		t.Fatalf("busy=%v err=%v", busy, err)
	}
	for _, id := range []string{"second", "third"} {
		if _, found := busy[id]; !found {
			t.Fatalf("busy=%v lacks %q", busy, id)
		}
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 {
		t.Fatalf("queries=%q, want one per distinct account config dir", raw)
	}
	for index, line := range lines {
		if !strings.HasPrefix(line, dirs[index]+"|") {
			t.Fatalf("query[%d]=%q, want dir %q", index, line, dirs[index])
		}
	}
}

func TestClaudeAgentsQueryUsesRegistryReadShape(t *testing.T) {
	home := t.TempDir()
	logPath := filepath.Join(home, "query.log")
	binary := filepath.Join(home, "claude")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$PFM_TEST_QUERY_LOG\"\nprintf '[]'\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_TEST_QUERY_LOG", logPath)
	agents := ClaudeAgents{Binary: binary, ConfigDirs: []string{home}}
	if _, err := agents.BusySessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	query := string(raw)
	if !strings.Contains(query, "agents --json --settings ") ||
		strings.Contains(query, "--system-prompt-file") || strings.Contains(query, "--mcp-config") ||
		strings.Contains(query, "--dangerously-skip-permissions") ||
		strings.Contains(query, "\"hooks\"") || strings.Contains(query, "\"statusLine\"") {
		t.Fatalf("agent query argv=%q", query)
	}
}

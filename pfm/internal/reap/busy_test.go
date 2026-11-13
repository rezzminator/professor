package reap

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// fakeClaudeAgents plays `claude agents --json`: it ignores its argv (BusySessions' own shape is covered
// by action.ClaudeSpawn's own tests) and answers with body on stdout, or exits non-zero with body on stderr
// when fail is set. The binary is a file the query resolves and never runs.
func fakeClaudeAgents(t *testing.T, body string, fail bool) ClaudeAgents {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "claude")
	if err := testjail.WriteExecutableNeverRun(binary, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	output := deps.StartOutput{Stdout: body}
	if fail {
		output = deps.StartOutput{Stderr: body, ExitCode: 1}
	}
	runner := &deps.FakeRunner{}
	runner.ScriptStartOutput(nil, output)
	return ClaudeAgents{Binary: binary, ConfigDirs: []string{t.TempDir()}, Runner: runner}
}

// TestClaudeAgentsBusySessionsParsesBusyRows (L1-T5): the production probe
// that decides which chats are too busy for the sweep to touch — read
// straight from `claude agents --json`, not substituted by a test double —
// keeps only the rows the engine itself marked busy.
func TestClaudeAgentsBusySessionsParsesBusyRows(t *testing.T) {
	body := `[{"sessionId":"sess-busy","status":"busy"},{"sessionId":"sess-idle","status":"idle"}]`
	agents := fakeClaudeAgents(t, body, false)
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
	agents := fakeClaudeAgents(t, "boom", true)
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
	agents := fakeClaudeAgents(t, "not json", false)
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
	dirs := []string{
		pfmconfig.DefaultAccountDir(home, 1),
		filepath.Join(home, ".cc", "2"),
		filepath.Join(home, ".cc", "3"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	binary := filepath.Join(home, "claude")
	if err := testjail.WriteExecutableNeverRun(binary, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var queries []string
	runner := &deps.FakeRunner{}
	runner.ScriptStartOutput(nil, deps.StartOutput{AtStart: func(argv []string, opts deps.StartOptions) {
		configDir := lastEnv(opts.Env, "CLAUDE_CONFIG_DIR")
		queries = append(queries, configDir+"|"+strings.Join(argv[1:], " "))
		answer := "[]"
		switch {
		case strings.HasSuffix(configDir, "/2"):
			answer = `[{"sessionId":"second","status":"busy"}]`
		case strings.HasSuffix(configDir, "/3"):
			answer = `[{"sessionId":"third","status":"busy"}]`
		}
		_, _ = io.WriteString(opts.Stdout, answer)
	}})
	machine := pfmconfig.Config{
		Claude: pfmconfig.Claude{Binary: binary},
		Accounts: []pfmconfig.Account{
			{ID: 1, ConfigDir: dirs[0]},
			{ID: 2, ConfigDir: dirs[1]},
			{ID: 3, ConfigDir: dirs[2]},
			{ID: 4, ConfigDir: dirs[2]},
		},
	}
	agents := NewClaudeAgents(paths.Values{Home: home, Roots: map[pfmengine.ID][]string{
		pfmengine.Claude: {filepath.Join(home, "transcripts", "projects")},
	}}, machine)
	agents.Runner = runner
	busy, err := agents.BusySessions(context.Background())
	if err != nil || len(busy) != 2 {
		t.Fatalf("busy=%v err=%v", busy, err)
	}
	for _, id := range []string{"second", "third"} {
		if _, found := busy[id]; !found {
			t.Fatalf("busy=%v lacks %q", busy, id)
		}
	}
	lines := queries
	if len(lines) != 3 {
		t.Fatalf("queries=%q, want one per distinct account config dir", queries)
	}
	for index, line := range lines {
		if !strings.HasPrefix(line, dirs[index]+"|") {
			t.Fatalf("query[%d]=%q, want dir %q", index, line, dirs[index])
		}
	}
}

func TestClaudeAgentsQueryUsesRegistryReadShape(t *testing.T) {
	home := t.TempDir()
	binary := filepath.Join(home, "claude")
	if err := testjail.WriteExecutableNeverRun(binary, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &deps.FakeRunner{}
	runner.ScriptStartOutput(nil, deps.StartOutput{Stdout: "[]"})
	agents := ClaudeAgents{Binary: binary, ConfigDirs: []string{home}, Runner: runner}
	if _, err := agents.BusySessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	starts := runner.Starts()
	if len(starts) != 1 {
		t.Fatalf("agent queries = %v, want exactly 1", starts)
	}
	query := strings.Join(starts[0].Argv[1:], " ") + "\n"
	if !strings.Contains(query, "agents --json --settings ") ||
		strings.Contains(query, "--system-prompt-file") || strings.Contains(query, "--mcp-config") ||
		strings.Contains(query, "--dangerously-skip-permissions") ||
		strings.Contains(query, "\"hooks\"") || strings.Contains(query, "\"statusLine\"") {
		t.Fatalf("agent query argv=%q", query)
	}
}

// lastEnv is the last value an environment gives name: the one a child sees.
func lastEnv(environment []string, name string) string {
	value := ""
	for _, entry := range environment {
		if found, ok := strings.CutPrefix(entry, name+"="); ok {
			value = found
		}
	}
	return value
}

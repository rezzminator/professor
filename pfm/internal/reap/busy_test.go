package reap

import (
	"context"
	"fmt"
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
	runner.ScriptLookPath(binary, binary, nil)
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
	runner.ScriptLookPath(binary, binary, nil)
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
	if got, want := strings.ReplaceAll(
		fmt.Sprintf("%v %v\n%s", busy, err, strings.Join(queries, "\n")),
		home,
		"{home}",
	),
		"map[second:{} third:{}] <nil>\n{home}/.cc/1|agents --json --settings {\"cleanupPeriodDays\":0,\"env\":{\"ANTHROPIC_SMALL_FAST_MODEL\":\"claude-haiku-5-5\",\"CLAUDE_CODE_AUTO_COMPACT_WINDOW\":\"0\",\"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS\":\"1\",\"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS\":\"0\",\"CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH\":\"8\",\"CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION\":\"0\"},\"outputStyle\":\"default\"}\n{home}/.cc/2|agents --json --settings {\"cleanupPeriodDays\":0,\"env\":{\"ANTHROPIC_SMALL_FAST_MODEL\":\"claude-haiku-5-5\",\"CLAUDE_CODE_AUTO_COMPACT_WINDOW\":\"0\",\"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS\":\"1\",\"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS\":\"0\",\"CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH\":\"8\",\"CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION\":\"0\"},\"outputStyle\":\"default\"}\n{home}/.cc/3|agents --json --settings {\"cleanupPeriodDays\":0,\"env\":{\"ANTHROPIC_SMALL_FAST_MODEL\":\"claude-haiku-5-5\",\"CLAUDE_CODE_AUTO_COMPACT_WINDOW\":\"0\",\"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS\":\"1\",\"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS\":\"0\",\"CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH\":\"8\",\"CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION\":\"0\"},\"outputStyle\":\"default\"}"; got != want {
		t.Fatalf("busy and queries = %q, want %q", got, want)
	}
}

func TestClaudeAgentsQueryUsesRegistryReadShape(t *testing.T) {
	home := t.TempDir()
	binary := filepath.Join(home, "claude")
	if err := testjail.WriteExecutableNeverRun(binary, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &deps.FakeRunner{}
	runner.ScriptLookPath(binary, binary, nil)
	runner.ScriptStartOutput(nil, deps.StartOutput{Stdout: "[]"})
	agents := ClaudeAgents{Binary: binary, ConfigDirs: []string{home}, Runner: runner}
	if _, err := agents.BusySessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	starts := runner.Starts()
	if len(starts) != 1 {
		t.Fatalf("agent queries = %v, want exactly 1", starts)
	}
	if got, want := strings.ReplaceAll(
		strings.Join(starts[0].Argv[1:], " "),
		home,
		"{home}",
	), "agents --json --settings {\"cleanupPeriodDays\":0,\"env\":{\"ANTHROPIC_SMALL_FAST_MODEL\":\"claude-haiku-5-5\",\"CLAUDE_CODE_AUTO_COMPACT_WINDOW\":\"0\",\"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS\":\"1\",\"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS\":\"0\",\"CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH\":\"8\",\"CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION\":\"0\"},\"outputStyle\":\"default\"}"; got != want {
		t.Fatalf("agent query argv = %q, want %q", got, want)
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

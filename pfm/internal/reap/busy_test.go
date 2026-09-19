package reap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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

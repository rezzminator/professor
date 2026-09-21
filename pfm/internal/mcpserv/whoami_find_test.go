package mcpserv

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// TestChatWhoamiReportsIdentityOrStatesItsAbsence covers chat.sh:482-484 over
// MCP: inside tmux the answer is this chat's own session name; outside it, the
// absence is stated rather than guessed.
func TestChatWhoamiReportsIdentityOrStatesItsAbsence(t *testing.T) {
	setupBackendFixture(t)
	t.Setenv("TMUX", "")
	t.Setenv(resolve.ClaudeSessionEnv, "")
	t.Setenv(resolve.CodexThreadEnv, "")
	service := newFixtureService(t)
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	client := connectInMemory(t, service.Server())
	output := callTool[WhoamiOutput](
		t,
		client.clientSession,
		"chat_whoami",
		WhoamiInput{},
	)
	if output.Status != "not_found" ||
		!strings.Contains(output.Message, "not inside tmux") {
		t.Fatalf("chat_whoami outside tmux = %+v", output)
	}
	if output.Session != "" {
		t.Fatalf("chat_whoami invented a session: %+v", output)
	}
}

// TestChatFindRanksByNeedleVotesAndExcludesSelf covers chat.sh:440-478: an
// excerpt becomes up to five needles of 20+ characters, each transcript is
// ranked by how many it hits, and the ASKING session never matches itself.
func TestChatFindRanksByNeedleVotesAndExcludesSelf(t *testing.T) {
	root := setupBackendFixture(t)
	project := filepath.Join(root, "claude", "project-alpha")
	// three needles, one transcript hits all three, one hits a single one.
	writeJSONL(t, filepath.Join(project, "three.jsonl"), []any{
		map[string]any{
			"type": "user", "cwd": "/work/alpha",
			"message": map[string]any{
				"content": "the migration plan must survive verbatim",
			},
		},
		map[string]any{
			"type": "assistant",
			"message": map[string]any{
				"content": "lock namespace unification is the hard part\n" +
					"the waiter rides out the compaction to idle",
			},
		},
	})
	writeJSONL(t, filepath.Join(project, "one.jsonl"), []any{
		map[string]any{
			"type": "user", "cwd": "/work/alpha",
			"message": map[string]any{
				"content": "lock namespace unification is the hard part",
			},
		},
	})
	writeJSONL(t, filepath.Join(project, "selfchat.jsonl"), []any{
		map[string]any{
			"type": "user", "cwd": "/work/alpha",
			"message": map[string]any{
				"content": "the migration plan must survive verbatim\n" +
					"lock namespace unification is the hard part\n" +
					"the waiter rides out the compaction to idle",
			},
		},
	})
	t.Setenv(resolve.ClaudeSessionEnv, "selfchat")
	t.Setenv(resolve.CodexThreadEnv, "")
	service := newFixtureService(t)
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	client := connectInMemory(t, service.Server())

	excerpt := strings.Join([]string{
		"> the migration plan must survive verbatim",
		"- lock namespace unification is the hard part",
		"  the waiter rides out the compaction to idle",
		"too short",
	}, "\n")
	output := callTool[FindOutput](t, client.clientSession, "chat_find", FindInput{
		Excerpt: excerpt,
	})
	if len(output.Needles) != 3 {
		t.Fatalf("needles = %+v, want the three 20+ character lines", output.Needles)
	}
	if output.SelfID != "selfchat" {
		t.Fatalf("self id = %q, want the asking session", output.SelfID)
	}
	for _, candidate := range output.Candidates {
		if candidate.ID == "selfchat" {
			t.Fatalf("the asking session matched itself: %+v", output.Candidates)
		}
	}
	if output.Count < 2 {
		t.Fatalf("find = %+v, want both other transcripts", output)
	}
	if output.Candidates[0].ID != "three" || output.Candidates[0].Hits != 3 {
		t.Fatalf("top candidate = %+v, want three with 3 hits", output.Candidates[0])
	}
	ranked := make([]int, 0, len(output.Candidates))
	for _, candidate := range output.Candidates {
		ranked = append(ranked, candidate.Hits)
	}
	for index := 1; index < len(ranked); index++ {
		if ranked[index] > ranked[index-1] {
			t.Fatalf("candidates are not ordered by hits: %v", ranked)
		}
	}

	// include_self is the escape hatch, and it brings the excluded row back.
	withSelf := callTool[FindOutput](t, client.clientSession, "chat_find", FindInput{
		Excerpt:     excerpt,
		IncludeSelf: true,
	})
	found := false
	for _, candidate := range withSelf.Candidates {
		if candidate.ID == "selfchat" {
			found = true
		}
	}
	if !found {
		t.Fatalf("include_self did not restore the asking session: %+v", withSelf)
	}
}

// TestChatInjectCarriesTheThenArgument proves the steer chain reaches the
// engine through the MCP schema, that a /compact PRIMARY is refused outright
// on chat_inject regardless of a then steer (Task C: compaction is
// chat_self_compact / `pfm chat self-compact` only, never a live chat_inject
// /compact), and that a /compact STEER is still refused by checkSteerChain
// even when the primary is ordinary. Renamed cases from
// steerless/recursive/unresolved "/compact hold…" primaries: the steerless
// and legal-steer cases used to differ only in whether `then` carried a
// steer, which no longer distinguishes anything once chat_inject bans every
// /compact primary outright — see engine.go's Inject().
func TestChatInjectCarriesTheThenArgument(t *testing.T) {
	setupBackendFixture(t)
	service := newFixtureService(t)
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	client := connectInMemory(t, service.Server())

	compactPrimary := callTool[InjectOutput](t, client.clientSession, "chat_inject", InjectInput{
		Target:  "no-such-chat",
		Message: "/compact hold: read /tmp/hold.md",
	})
	if compactPrimary.Code != 6 ||
		compactPrimary.Status != "refused" ||
		compactPrimary.Typed ||
		!strings.Contains(compactPrimary.Message, "/compact is never injected") {
		t.Fatalf("compact primary via chat_inject = %+v", compactPrimary)
	}

	// A /compact PRIMARY with a then steer is refused the same way — the ban
	// is unconditional, not steer-gated.
	compactPrimaryWithSteer := callTool[InjectOutput](t, client.clientSession, "chat_inject", InjectInput{
		Target:  "no-such-chat",
		Message: "/compact hold: read /tmp/hold.md",
		Then:    []string{"resume the port"},
	})
	if compactPrimaryWithSteer.Code != 6 ||
		compactPrimaryWithSteer.Typed ||
		!strings.Contains(compactPrimaryWithSteer.Message, "/compact is never injected") {
		t.Fatalf("compact primary with steer via chat_inject = %+v", compactPrimaryWithSteer)
	}

	recursive := callTool[InjectOutput](t, client.clientSession, "chat_inject", InjectInput{
		Target:  "no-such-chat",
		Message: "hold: read /tmp/hold.md",
		Then:    []string{"/compact again"},
	})
	if recursive.Code != 6 ||
		!strings.Contains(recursive.Message, "must not itself start with /compact") {
		t.Fatalf("recursive steer = %+v", recursive)
	}

	// With an ordinary primary and a legal steer the chain passes every guard
	// and dies at resolution instead — proof the Then argument travelled
	// through the MCP schema into the engine, and that nothing was typed.
	unresolved := callTool[InjectOutput](t, client.clientSession, "chat_inject", InjectInput{
		Target:  "no-such-chat",
		Message: "hold: read /tmp/hold.md",
		Then:    []string{"resume the port"},
	})
	if unresolved.Code != 4 ||
		unresolved.Typed ||
		!strings.Contains(unresolved.Message, "matched no live chat") {
		t.Fatalf("legal steer chain = %+v", unresolved)
	}
}

// TestChatCaptureBoundsAreAppliedAfterTheCapture keeps the whole-scrollback
// capture from returning an unbounded payload.
func TestChatCaptureBoundsAreAppliedAfterTheCapture(t *testing.T) {
	if _, err := os.Stat("/proc/self"); err != nil {
		t.Skip("no /proc")
	}
	setupBackendFixture(t)
	service := newFixtureService(t)
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	client := connectInMemory(t, service.Server())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, input := range []CaptureInput{
		{Target: "missing-target", MaxBytes: captureInt(maxCaptureBytes + 1)},
		{Target: "missing-target", TailLines: captureInt(5000)},
		{Target: "missing-target", MaxBytes: captureInt(0)},
		{Target: "missing-target", TailLines: captureInt(0)},
	} {
		result, err := client.clientSession.CallTool(ctx, &mcp.CallToolParams{
			Name:      "chat_capture",
			Arguments: input,
		})
		if err == nil && (result == nil || !result.IsError) {
			t.Fatalf("out-of-range bound accepted: %+v", input)
		}
	}
	if got := tailBytes("héllo world", 6); got != " world" {
		t.Fatalf("tailBytes() = %q, want the last six bytes", got)
	}
	// A cut that lands inside a rune advances to the next whole one.
	if got := tailBytes("héllo", 4); got != "llo" {
		t.Fatalf("tailBytes() = %q, want a whole-rune tail", got)
	}
}

// TestChatFindOnTheSharedDaemonExcludesNoAmbientSelf pins the daemon half of
// self-exclusion: a shared HTTP daemon's environment names whoever launched
// it, not the MCP caller, so it must neither drop that launcher's transcript
// nor report it as self_id — it reports that it excluded nobody.
func TestChatFindOnTheSharedDaemonExcludesNoAmbientSelf(t *testing.T) {
	root := setupBackendFixture(t)
	line := "the daemon launcher is not the asking chat"
	writeJSONL(t, filepath.Join(root, "claude", "project-alpha", "launcher.jsonl"), []any{
		map[string]any{"type": "user", "cwd": "/work/alpha", "message": map[string]any{"content": line}},
	})
	t.Setenv(resolve.ClaudeSessionEnv, "launcher")
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewConfigured("test", io.Discard, Runtime{
		Paths: resolved, Chat: chat.Verbs{Warnings: io.Discard}, AllowAmbientIdentity: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	client := connectInMemory(t, service.Server())
	output := callTool[FindOutput](t, client.clientSession, "chat_find", FindInput{Excerpt: line})
	if output.SelfID != "" || output.Count != 1 || output.Candidates[0].ID != "launcher" {
		t.Fatalf("daemon chat_find = %+v; want the launcher's transcript found and no self_id", output)
	}
}

// TestChatFindReportsNoMatchAsAnEmptyAnswer pins that a search which ran and
// matched nothing answers count 0 — an answer, not a tool failure a caller
// would read as "the search could not run".
func TestChatFindReportsNoMatchAsAnEmptyAnswer(t *testing.T) {
	root := setupBackendFixture(t)
	writeJSONL(t, filepath.Join(root, "claude", "project-alpha", "other.jsonl"), []any{
		map[string]any{
			"type":    "user",
			"cwd":     "/work/alpha",
			"message": map[string]any{"content": "an unrelated conversation entirely"},
		},
	})
	t.Setenv(resolve.ClaudeSessionEnv, "")
	service := newFixtureService(t)
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	client := connectInMemory(t, service.Server())
	output := callTool[FindOutput](
		t,
		client.clientSession,
		"chat_find",
		FindInput{Excerpt: "a sentence no transcript here holds"},
	)
	if output.Count != 0 || len(output.Candidates) != 0 || len(output.Needles) != 1 {
		t.Fatalf("chat_find with no match = %+v; want count 0 with the needle it searched", output)
	}
}

package mcpserv

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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
		"whoami",
		WhoamiInput{},
	)
	if output.Status != "not_found" ||
		!strings.Contains(output.Message, "not inside tmux") {
		t.Fatalf("whoami outside tmux = %+v", output)
	}
	if output.Session != "" {
		t.Fatalf("whoami invented a session: %+v", output)
	}
}

// TestChatCallCarriesTheThenArgument proves the steer chain reaches the
// engine through the MCP schema, that a /compact PRIMARY is refused outright
// on call regardless of a then steer, and that a /compact STEER is still refused by checkSteerChain
// even when the primary is ordinary. Renamed cases from
// steerless/recursive/unresolved "/compact hold…" primaries: the steerless
// and legal-steer cases used to differ only in whether `then` carried a
// steer, which no longer distinguishes anything once call bans every
// /compact primary outright — see engine.go's Call().
func TestChatCallCarriesTheThenArgument(t *testing.T) {
	setupBackendFixture(t)
	service := newFixtureService(t)
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	client := connectInMemory(t, service.Server())

	compactPrimary := callTool[CallOutput](t, client.clientSession, "call", CallInput{
		Target:  "no-such-chat",
		Message: "/compact hold: read /tmp/hold.md",
	})
	if compactPrimary.Code != 6 ||
		compactPrimary.Status != "refused" ||
		compactPrimary.Typed ||
		!strings.Contains(compactPrimary.Message, "/compact is never called") {
		t.Fatalf("compact primary via call = %+v", compactPrimary)
	}

	// A /compact PRIMARY with a then steer is refused the same way — the ban
	// is unconditional, not steer-gated.
	compactPrimaryWithSteer := callTool[CallOutput](t, client.clientSession, "call", CallInput{
		Target:  "no-such-chat",
		Message: "/compact hold: read /tmp/hold.md",
		Then:    []string{"resume the port"},
	})
	if compactPrimaryWithSteer.Code != 6 ||
		compactPrimaryWithSteer.Typed ||
		!strings.Contains(compactPrimaryWithSteer.Message, "/compact is never called") {
		t.Fatalf("compact primary with steer via call = %+v", compactPrimaryWithSteer)
	}

	recursive := callTool[CallOutput](t, client.clientSession, "call", CallInput{
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
	unresolved := callTool[CallOutput](t, client.clientSession, "call", CallInput{
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
			Name:      "capture",
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

package binwatch

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The stdio MCP server is the OTHER pfm process nobody closes: a chat launches
// it once and it serves that chat for the life of the session — days, across
// every install in between. It has no listener, so binwatch.Serve cannot guard
// it, and it kept answering chat_kill / chat_status out of a fleet composer
// that predated the build the caller believed it was talking to. The guard is
// that server’s half of the same contract: the replacement ends its context,
// says so on stderr, and is reported as the reason it ended.
func TestGuardEndsTheContextWhenTheBinaryIsReplaced(t *testing.T) {
	replaced := make(chan struct{})
	stderr := &lockedBuffer{}
	ctx, wasReplaced, stop := guardReplacement(context.Background(), replaced, stderr)
	defer stop()

	if wasReplaced() {
		t.Fatal("guard reported a replacement before one happened")
	}
	close(replaced)
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("guard did not end its context after the binary was replaced")
	}
	// The predicate is read after the served call returns, so it must be true
	// by then — the goroutine cancels only after recording the reason.
	if !wasReplaced() {
		t.Fatal("guard ended its context without naming the replacement as the reason")
	}
	if got := stderr.String(); !strings.Contains(got, "replaced by a new build") {
		t.Fatalf("stderr = %q, want the replacement said out loud", got)
	}
}

// A server that ends for its own reasons never claims it was replaced: the
// caller's exit code has to stay its own.
func TestGuardWithoutAReplacementReportsNone(t *testing.T) {
	stderr := &lockedBuffer{}
	_, wasReplaced, stop := guardReplacement(context.Background(), make(chan struct{}), stderr)
	stop()
	if wasReplaced() {
		t.Fatal("guard reported a replacement that never happened")
	}
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want silence", stderr.String())
	}
}

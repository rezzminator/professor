package reload

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// proseTokensTmux is the 2026-09-25 self-reload incident: the caller's turn
// has ended and the pane is idle, but an earlier answer still on screen says
// "read 27,615 tokens from cache". The prose sits well above the footer — the
// live spinner never renders there — yet it matches the busy test's
// "\d+ tokens" arm, and a whole-pane test read it as a turn that never ends.
type proseTokensTmux struct {
	fakeReloadTmux
}

func (tmux *proseTokensTmux) Capture(context.Context, string, string) (string, error) {
	if tmux.literal == "/exit" {
		return tmux.screen("❯ /exit"), nil
	}
	return tmux.screen("❯ "), nil
}

func (tmux *proseTokensTmux) screen(composer string) string {
	var pane strings.Builder
	pane.WriteString("⏺ My latest call read 27,615 tokens from cache and re-wrote 207,108.\n")
	for line := range 24 {
		fmt.Fprintf(&pane, "  later transcript line %d\n", line+1)
	}
	pane.WriteString("⏺ Verdict: reloading now.\n\n")
	pane.WriteString("────────────────────────────────\n" + composer + "\n────────────────────────────────\n")
	pane.WriteString("  opus · ctx 41% · 1 background task\n")
	return pane.String()
}

func TestRunDoesNotReadTranscriptProseAsARunningTurn(t *testing.T) {
	tmux := &proseTokensTmux{}
	var stderr strings.Builder
	_, err := Run(
		context.Background(),
		reloadIdleWaitRequest("/tmp/tmux-1000/probe-reload-prose"),
		Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2, IdleTries: 5},
		tmux,
		nil,
		&stderr,
	)
	if err != nil {
		t.Fatalf("an idle chat with token prose on screen was refused: %v (stderr %q)", err, stderr.String())
	}
	if tmux.respawn == "" {
		t.Fatalf("an idle chat was never rebooted; stderr %q", stderr.String())
	}
}

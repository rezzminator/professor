package reload

import (
	"context"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/clock"
)

// blindExitTmux never shows the typed /exit rendered — no context deadline
// cuts it short, so waitExitRendered runs out its own retries and refuses to
// press Enter blind. The composer it typed into (already stashed empty by
// C-s) must come back out clean.
type blindExitTmux struct {
	fakeReloadTmux
	backspaces int
}

func (tmux *blindExitTmux) Capture(context.Context, string, string) (string, error) {
	return "Claude\n❯ ", nil
}

func (tmux *blindExitTmux) SendKey(ctx context.Context, socket, pane, key string) error {
	if key == "BSpace" {
		tmux.backspaces++
	}
	return tmux.fakeReloadTmux.SendKey(ctx, socket, pane, key)
}

// A /exit that never confirms rendering was still TYPED — the worker sent it
// via SendLiteral before the wait began. Refusing to press Enter on it must
// not leave that stray "/exit" sitting in the composer for a human to find.
func TestRunClearsTypedExitWhenItNeverRenders(t *testing.T) {
	tmux := &blindExitTmux{}
	// The render wait sleeps fixed real intervals; the fake clock resolves them.
	fakeClock := clock.NewFake(time.Unix(0, 0))
	sidDir := t.TempDir()
	var err error
	driveFakeClock(t, fakeClock, func() {
		_, err = Run(
			context.Background(),
			reloadIdleWaitRequest("/tmp/tmux-1000/probe-reload-blind-render"),
			Options{SIDDir: sidDir, Delay: -1, Poll: -1, ExitTries: 2, Clock: fakeClock},
			tmux,
			nil,
			nil,
		)
	})
	if err == nil || !strings.Contains(err.Error(), "refusing blind Enter") {
		t.Fatalf("never-rendered /exit error=%v, want 'refusing blind Enter'", err)
	}
	if tmux.backspaces != len("/exit") {
		t.Fatalf("backspaces=%d, want %d to clear the typed /exit before returning", tmux.backspaces, len("/exit"))
	}
	if tmux.respawn != "" {
		t.Fatalf("blind exit mutation: respawn=%q", tmux.respawn)
	}
}

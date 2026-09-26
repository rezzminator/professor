package inject

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/resolve"
)

func TestReviewRawPaneKeepsProxySocket(t *testing.T) {
	base := newTestEngine(t, "cx-daemon", &fakeTmux{})
	callerSocket := filepath.Join(string(filepath.Separator), "tmp", "tmux-jail", "cx-caller")
	scoped := base.WithIdentity(resolve.Identity{
		Session:    "cx-caller",
		SocketPath: callerSocket,
		Pane:       "%1",
		Engine:     "codex",
		ID:         "caller-thread",
	}, "Caller")

	target, code, detail, err := scoped.Resolve(context.Background(), "%2")
	if err != nil || code != 0 || detail != "" {
		t.Fatalf("Resolve(%%2) = target=%+v code=%d detail=%q err=%v", target, code, detail, err)
	}
	if target.SocketPath != callerSocket || target.Pane != "%2" {
		t.Fatalf("Resolve(%%2) = %+v, want caller socket %q pane %%2", target, callerSocket)
	}
	if target.ID != "" || target.Session != "" {
		t.Fatalf("raw destination inherited caller identity: %+v", target)
	}
}

func TestWithIdentityRawPaneIsolationAndAmbientControl(t *testing.T) {
	base := newTestEngine(t, "cx-daemon", &fakeTmux{})
	t.Setenv("CHAT_INJECT_SOCKET", "/tmp/tmux-jail/cx-daemon-inject")
	t.Setenv("TMUX", "/tmp/tmux-jail/cx-daemon-tmux,3,0")

	firstIdentity := resolve.Identity{
		Session: "cx-first", SocketPath: "/tmp/tmux-jail/cx-first", Pane: "%1", ID: "thread-a",
	}
	first := base.WithIdentity(firstIdentity, "First")
	second := base.WithIdentity(resolve.Identity{
		Session: "cx-second", SocketPath: "/tmp/tmux-jail/cx-second", Pane: "%1", ID: "thread-b",
	}, "Second")
	firstIdentity.SocketPath = "/tmp/tmux-jail/cx-mutated-after-scope"

	for _, testCase := range []struct {
		name       string
		engine     *Engine
		wantSocket string
	}{
		{name: "first caller", engine: first, wantSocket: "/tmp/tmux-jail/cx-first"},
		{name: "second caller", engine: second, wantSocket: "/tmp/tmux-jail/cx-second"},
		{name: "ambient base", engine: base, wantSocket: "/tmp/tmux-jail/cx-daemon-inject"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			target, code, detail, err := testCase.engine.Resolve(context.Background(), "%2")
			if err != nil || code != 0 || detail != "" || target.SocketPath != testCase.wantSocket ||
				target.Pane != "%2" {
				t.Fatalf("Resolve(%%2) = target=%+v code=%d detail=%q err=%v", target, code, detail, err)
			}
		})
	}
}

func TestWithIdentityRawPaneMissingSocketRefusesAmbientFallback(t *testing.T) {
	base := newTestEngine(t, "cx-daemon", &fakeTmux{})
	t.Setenv("CHAT_INJECT_SOCKET", "/tmp/tmux-jail/cx-daemon-inject")
	scoped := base.WithIdentity(resolve.Identity{Session: "cx-caller", Pane: "%1", ID: "thread-a"}, "Caller")

	target, code, detail, err := scoped.Resolve(context.Background(), "%2")
	if err != nil || code != CodeUnknown || detail != "raw pane target request identity has no socket" ||
		target != (Target{}) {
		t.Fatalf("Resolve(%%2) = target=%+v code=%d detail=%q err=%v", target, code, detail, err)
	}
}

package kill

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	pfmengine "hostops/pfm/internal/engine"
)

// A live OpenCode chat gets the same graceful-close-then-fallback-kill
// choreography the other two engines do. Its TUI command is /exit (the binary
// accepts /exit, /quit and :q; /exit is the one its own command palette
// lists). Without an arm here the finisher refused the engine outright, so a
// kill of a running OpenCode chat closed nothing at all.
func TestFinisherOpenCodeUsesExit(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	tmux := &fakeTmux{}
	finisher, err := NewFinisher(database, Dependencies{
		Tmux:         tmux,
		Refresher:    refreshFunc(func(context.Context) error { return nil }),
		Delay:        time.Millisecond,
		PollEvery:    time.Millisecond,
		PollAttempts: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := finisher.Run(context.Background(), ExitArgs{
		Engine:     pfmengine.OpenCode,
		ID:         "ses_live",
		SocketPath: filepath.Join(jail.tmuxDir, "ox-500-1-1"),
		SocketName: "ox-500-1-1",
		PaneID:     "%0",
	}); err != nil {
		t.Fatalf("finisher.Run for OpenCode: %v", err)
	}
	if !reflect.DeepEqual(tmux.sent, []string{"/exit"}) {
		t.Fatalf("sent = %q, want /exit", tmux.sent)
	}
}

// An ox- socket must never be identified AS Claude. Self-kill has no session
// environment to work from for OpenCode, so the answer is a NAMED refusal —
// the Claude crumb path would otherwise hand back somebody else's identity or
// a bare "no valid session id" about the wrong engine entirely.
func TestIdentifySelfRefusesAnOpenCodeSocketByName(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	manager, err := New(database, Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.IdentifySelf(context.Background(), SelfEnvironment{
		TMUX:     filepath.Join(jail.tmuxDir, "ox-500-1-1") + ",123,0",
		TMUXPane: "%0",
	})
	if err == nil {
		t.Fatal("IdentifySelf on an ox- socket succeeded; want a named refusal")
	}
	if !strings.Contains(err.Error(), "self-kill is not supported for OpenCode chats") {
		t.Fatalf("IdentifySelf error = %v, want the OpenCode refusal by name", err)
	}
	if strings.Contains(err.Error(), "Claude") {
		t.Fatalf("IdentifySelf error = %v, want no mention of Claude", err)
	}
}

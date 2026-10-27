package kill

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// An ox- socket must never be identified AS Claude. Self-kill has no session
// environment to work from for OpenCode, so the answer is a NAMED refusal —
// the Claude crumb path would otherwise hand back somebody else's identity or
// a bare "no valid session id" about the wrong engine entirely.
func TestKillSelfRefusesAnOpenCodeSocketByName(t *testing.T) {
	jail := newKillJail(t)
	database := jail.open(t)
	manager, err := New(database, Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	manager.identifier = killTestIdentity{
		identity: resolve.Identity{
			Backend:    "tmux",
			Engine:     "ox",
			SocketPath: filepath.Join(jail.tmuxDir, "ox-500-1-1"),
			SocketName: "ox-500-1-1",
			Pane:       "%0",
		},
	}
	_, err = manager.Kill(context.Background(), Request{Self: true})
	if err == nil {
		t.Fatal("Kill self on an ox- socket succeeded; want a named refusal")
	}
	if !strings.Contains(err.Error(), "self-kill is not supported for OpenCode chats") {
		t.Fatalf("Kill self error = %v, want the OpenCode refusal by name", err)
	}
	if strings.Contains(err.Error(), "Claude") {
		t.Fatalf("Kill self error = %v, want no mention of Claude", err)
	}
}

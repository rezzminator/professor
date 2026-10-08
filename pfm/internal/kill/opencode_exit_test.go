package kill

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

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

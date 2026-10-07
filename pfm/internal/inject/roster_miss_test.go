package inject

import (
	"context"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// deadRosterNames is a roster rung whose only match for the name is dead.
type deadRosterNames struct{ detail string }

func (names deadRosterNames) ResolveName(context.Context, string, string) (Target, int, string, error) {
	return Target{}, CodeUnknown, names.detail, nil
}

func (deadRosterNames) SenderName(context.Context, resolve.Identity) (string, bool, error) {
	return "", false, nil
}

// TestInjectToADeadOnlyNameSaysWhichChatIsDead pins rule 2 where MCP
// chat_inject and `pfm chat inject` meet the engine: a name only a dead chat
// answers to is refused with the roster's account of that dead chat, never
// the bare "matched no live chat" that hides it.
func TestInjectToADeadOnlyNameSaysWhichChatIsDead(t *testing.T) {
	const detail = `"probe" matched no live chat; its newest match is dead: thread id t-2 (socket none, name "probe")`
	engine := newTestEngine(t, "cc-1-2-3", &fakeTmux{})
	engine.resolver = fakeResolver{code: CodeUnknown}
	engine.names = deadRosterNames{detail: detail}
	result, err := engine.Inject(context.Background(), Request{Target: "probe", Message: "hello"})
	if err != nil || result.Code != CodeUnknown || result.Message != detail {
		t.Fatalf("Inject()=(%+v,%v), want CodeUnknown carrying the dead chat", result, err)
	}
}

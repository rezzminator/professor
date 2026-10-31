package call

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestCallRefusesWhenTheCopyModeReadFails: a pane whose copy mode cannot be
// read may be in it, where typed keys move a cursor instead of filling the
// composer, so the delivery is refused, nothing typed — never read as "not in
// copy mode".
func TestCallRefusesWhenTheCopyModeReadFails(t *testing.T) {
	fake := &fakeTmux{
		capture:       "❯ ",
		modeErr:       errors.New("tmux: server not answering"),
		submitOnEnter: true,
	}
	engine := newTestEngine(t, "cc-copy-mode-unread", fake)
	result, err := engine.Call(context.Background(), Request{Target: "chat", Message: "never typed"})
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if result.Code != CodeUndelivered || !strings.Contains(result.Message, "server not answering") {
		t.Fatalf("Call() over an unreadable copy mode = %+v, want the read failure reported undelivered", result)
	}
	if len(fake.literals) != 0 || len(fake.keys) != 0 {
		t.Fatalf("typed %q keys %q into a pane whose mode was never read", fake.literals, fake.keys)
	}
}

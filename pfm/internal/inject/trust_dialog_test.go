package inject

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// trustDialogCapture is Claude Code's folder-trust dialog; its default row is
// "No, exit", so any key pressed on it can end the chat.
const trustDialogCapture = " Accessing workspace:\n\n /work/alpha\n\n" +
	" Quick safety check: Is this a project you created or one you trust?\n\n" +
	" ❯ 1. Yes, I trust this folder\n   2. No, exit\n\n Enter to confirm · Esc to cancel\n"

func TestInjectRefusesByNameAtTheClaudeFolderTrustDialog(t *testing.T) {
	fake := &fakeTmux{capture: trustDialogCapture, submitOnEnter: true}
	engine := newTestEngine(t, "cc-trust-dialog", fake)
	result, err := engine.Inject(context.Background(), Request{Target: "chat", Message: "steer after the gate"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != CodeUndelivered || !strings.Contains(result.Message, "folder-trust dialog") {
		t.Fatalf("the trust dialog must be refused by name: result=%+v", result)
	}
	if len(fake.literals) != 0 || len(fake.keys) != 0 {
		t.Fatalf("nothing may be typed or pressed at the dialog: literals=%q keys=%q", fake.literals, fake.keys)
	}
}

func TestRescueLaunchPromptPressesNothingAtTheClaudeFolderTrustDialog(t *testing.T) {
	fake := &fakeTmux{capture: trustDialogCapture}
	outcome, err := RescueLaunchPrompt(
		context.Background(), fake, "/tmp/jail/cc-trust", "%1", clock.Real, time.Millisecond,
	)
	if err != nil || outcome != RescueTrustHeld {
		t.Fatalf("outcome=%v err=%v, want RescueTrustHeld", outcome, err)
	}
	if len(fake.keys) != 0 || len(fake.literals) != 0 {
		t.Fatalf("the dialog was sent keys=%q literals=%q", fake.keys, fake.literals)
	}
}

func TestRescueLaunchPromptDismissesThenSubmitsOnAnOrdinaryPane(t *testing.T) {
	fake := &fakeTmux{capture: "claude\n❯ the unsent prompt"}
	outcome, err := RescueLaunchPrompt(
		context.Background(), fake, "/tmp/jail/cc-rescue", "%1", clock.Real, time.Millisecond,
	)
	if err != nil || outcome != RescueKeysPressed {
		t.Fatalf("outcome=%v err=%v, want RescueKeysPressed", outcome, err)
	}
	if got := strings.Join(fake.keys, ","); got != "Escape,Enter" {
		t.Fatalf("keys = %q, want Escape,Enter", got)
	}
}

func TestRescueLaunchPromptPressesNothingOnAPaneItCannotRead(t *testing.T) {
	fake := &fakeTmux{dead: true}
	outcome, err := RescueLaunchPrompt(
		context.Background(), fake, "/tmp/jail/cc-dead", "%1", clock.Real, time.Millisecond,
	)
	if err == nil || outcome != RescueNotSent {
		t.Fatalf("outcome=%v err=%v, want RescueNotSent and the read error", outcome, err)
	}
	if len(fake.keys) != 0 {
		t.Fatalf("keys sent to an unreadable pane: %q", fake.keys)
	}
}

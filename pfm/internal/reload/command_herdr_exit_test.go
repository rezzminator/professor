package reload

import (
	"context"
	"io"
	"strings"
	"testing"
)

// TestRunHerdrClearsAnUnrenderedExit: a typed /exit the Herdr composer never
// shows is backspaced back out — the clean-up the tmux worker runs, through the
// same step — and no Enter is pressed on it.
func TestRunHerdrClearsAnUnrenderedExit(t *testing.T) {
	f := newReloadHerdrFixture(t)
	f.mode = "exit-unrendered"
	_, err := RunHerdr(context.Background(), f.request, f.options, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "refusing blind Enter") {
		t.Fatalf("RunHerdr error = %v, want the blind-Enter refusal", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	backspaces := 0
	for _, key := range f.keys {
		switch key {
		case "backspace":
			backspaces++
		case "Enter":
			t.Fatalf("Enter pressed on a /exit that never rendered: keys=%v", f.keys)
		}
	}
	if backspaces != len("/exit") {
		t.Fatalf("keys = %v, want %d backspaces taking the typed /exit back out", f.keys, len("/exit"))
	}
}

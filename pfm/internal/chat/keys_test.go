package chat

import "testing"

// TestKeyValidAcceptsTheKeysTmuxPresses keeps the tmux key-name gate from
// rejecting a key a caller can use or accepting a token tmux would type.
func TestKeyValidAcceptsTheKeysTmuxPresses(t *testing.T) {
	for _, key := range []string{
		"Enter", "Escape", "Tab", "BTab", "BSpace", "Space", "Up", "Down",
		"Left", "Right", "Home", "End", "PageUp", "PageDown", "F1", "F12",
		"C-c", "C-o", "M-x", "S-Tab", "C-M-a",
	} {
		if !KeyValid(key) {
			t.Fatalf("%q rejected, want it accepted", key)
		}
	}
	for _, key := range []string{"F0", "F13", "Esc", "Ctrl-C", "C-", ""} {
		if KeyValid(key) {
			t.Fatalf("%q accepted, want it rejected", key)
		}
	}
}

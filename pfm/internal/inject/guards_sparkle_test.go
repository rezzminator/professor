package inject

import "testing"

// Codex paints an idle "sparkle" of braille glyphs around its dim composer
// placeholder, in 24-bit greys rather than SGR dim once the terminal advertises
// truecolor — so the glyphs survive the dim-span strip and used to read as an
// unsent draft, refusing every inject into an idle Codex chat. The line below
// is a real tmux capture of that composer (Codex 0.154 under tmux-256color+Tc).
func TestIdleCodexSparkleIsNotADraft(t *testing.T) {
	t.Parallel()
	styled := "\x1b[1m\x1b[39m›\x1b[0m\x1b[38;2;63;77;80m\x1b[48;2;42;55;58m⠁\x1b[2m\x1b[39mAsk Codex to do anything\x1b[0m\x1b[48;2;42;55;58m         \x1b[38;2;96;110;113m⠈\x1b[39m      \x1b[38;2;126;140;144m⠁"
	if !isDimPlaceholder(styled) {
		t.Fatalf("idle Codex composer with sparkle glyphs read as a draft: %q", stripTerminalControl(styled))
	}
	if hasDraft("› ⠁                ⠈      ⠁") {
		t.Fatal("braille ornament alone counted as a draft")
	}
	// The same animation's ASCII frames — a lone spinner character is ornament too.
	for _, frame := range []string{"› /", "› -", "› \\", "› |"} {
		if hasDraft(frame) {
			t.Fatalf("lone spinner frame %q counted as a draft", frame)
		}
	}
	// Real text — even one letter, or a slash command in progress — is still a draft.
	for _, draft := range []string{"› a", "› /model", "› ⠁ hello", "› 7"} {
		if !hasDraft(draft) {
			t.Fatalf("genuine draft %q was not recognised", draft)
		}
	}
}

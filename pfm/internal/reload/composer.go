package reload

import (
	"fmt"
	"regexp"
	"strings"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
)

// This file is reload's READER of the pane: every question the choreography
// asks about what a chat currently shows — is its input box up, is our text in
// it, is a menu or the exit dialog in front of it — is answered here and
// nowhere else, so the exit path and the --then path can never disagree about
// what the same screen means.

// composerDrawn reports whether the chat has drawn its input box — the one
// proof that the pane can take a keystroke at all. deliverThen waits on it for
// the reborn pane; waitCallerIdle waits on it for the one being rebooted.
// (internal/spawn's composerReady answers a narrower, Codex-only question:
// that a BOOTING Codex TUI reached its idle composer and footer.)
func composerDrawn(capture string) bool {
	return lastReloadComposerLine(capture) != ""
}

// reloadComposerEmpty also checks continuation rows in a bounded input box:
// a newline-led draft leaves the marker row empty. Unbounded rows may be
// status text, so only a closing box rule establishes continuation text.
func reloadComposerEmpty(capture string, styled func() (string, error)) (bool, string, error) {
	empty, draft, err := pfmcall.ComposerEmpty(capture, styled)
	if err != nil || !empty {
		return empty, draft, err
	}
	below, _ := pfmcall.ComposerBoxText(capture)
	return pfmcall.ComposerEmpty("❯ "+strings.ReplaceAll(below, "\n", " "), func() (string, error) {
		frame, err := styled()
		if err != nil {
			return "", err
		}
		below, _ := pfmcall.ComposerBoxText(frame)
		return "❯ " + strings.ReplaceAll(below, "\n", " "), nil
	})
}

func composerDraftError(draft string) error {
	return fmt.Errorf(
		"composer holds a draft: %q; nothing was typed and the draft is preserved (dim placeholder text is exempt)",
		fmt.Sprintf("%.80s", draft),
	)
}

// composerShowsExit reads the composer's own DRAFT, never the whole line: a
// pane that echoed the keystrokes before its TUI came up draws the marker
// AFTER them ("/exit❯ "), and a whole-line read took that for a rendered
// draft and pressed Enter on a chat that had received nothing. The draft must
// BE /exit, not contain it: "old draft/exit" is a prompt, and Enter on it
// submits the old draft to the model instead of ending the chat.
func composerShowsExit(capture string) bool {
	return pfmcall.ComposerDraftText(capture) == "/exit"
}

func selectorOpen(capture string) bool {
	selector := regexp.MustCompile(`❯[\s\v]*\d+\.`)
	for _, line := range strings.Split(capture, "\n") {
		if selector.MatchString(line) {
			return true
		}
	}
	return false
}

// composerText returns the ACTIVE composer's WHOLE draft: the marker line plus
// every wrapped continuation line beneath it, up to the box's closing rule.
//
// A one-line read was the bug this replaces. Claude and Codex both wrap a long
// draft inside the input box and print the ❯/› marker on the FIRST line only,
// so a check that scanned the marker line alone saw the draft's head and never
// its tail — and deliverThen proves delivery by the TAIL, which is the half
// that proves nothing was truncated in transit. Every steer worth sending after
// a reload is long enough to wrap, so the proof could never be satisfied and
// the follow-up sat in the composer waiting for a human finger.
//
// The block ends at the box's horizontal rule. When a render carries no closing
// rule the block runs to the end of the capture: the callers only ever ask
// whether their OWN text is present, so trailing status rows cost nothing,
// while a missing continuation line costs the whole delivery.
func composerText(capture string) string {
	lines := strings.Split(capture, "\n")
	start := -1
	for index := len(lines) - 1; index >= 0; index-- {
		if strings.Contains(lines[index], "❯") || strings.Contains(lines[index], "›") {
			start = index
			break
		}
	}
	if start < 0 {
		return ""
	}
	block := lines[start : start+1]
	for index := start + 1; index < len(lines); index++ {
		if pfmcall.ComposerBoxRule(lines[index]) {
			break
		}
		block = lines[start : index+1]
	}
	return strings.Join(block, "\n")
}

// squashSpace drops every space so a comparison survives the composer's line
// wrapping. Collapsing to single spaces survives a wrap at a word boundary and
// NOT one inside a word, and a token wider than the box — a long path or URL,
// the substance of most steers — is wrapped mid-word.
func squashSpace(value string) string {
	return strings.Join(strings.Fields(value), "")
}

func lastReloadComposerLine(capture string) string {
	return pfmcall.LastComposerLine(capture)
}

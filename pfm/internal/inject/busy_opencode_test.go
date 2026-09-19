package inject

import (
	"strings"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
)

// The exact footers an OpenCode 1.18.18 pane renders. The sidebar's token
// counter sits ABOVE the footer in both, and `\d+ tokens` is one of IsBusy's
// own patterns — so the Claude/Codex rule reads EVERY OpenCode pane as busy,
// idle or not, and every inject into one would queue forever.
const (
	openCodeIdleCapture = "  Build · claude-sonnet-4-5\n" +
		"  0 tokens\n" +
		"┃\n" +
		"\n" +
		"                                   tab agents  ctrl+p commands    • OpenCode 1.18.18\n"
	openCodeBusyCapture = "  Build · claude-sonnet-4-5\n" +
		"  0 tokens\n" +
		"┃\n" +
		"\n" +
		"   ⬝■■■■■■⬝  esc interrupt                  tab agents  ctrl+p commands    • OpenCode 1.18.18\n"
)

func TestIsBusyForOpenCodeReadsItsOwnFooter(t *testing.T) {
	if IsBusyFor(pfmengine.OpenCode, openCodeIdleCapture) {
		t.Fatalf("idle OpenCode pane read as busy:\n%s", openCodeIdleCapture)
	}
	if !IsBusyFor(pfmengine.OpenCode, openCodeBusyCapture) {
		t.Fatalf("busy OpenCode pane read as idle:\n%s", openCodeBusyCapture)
	}
}

// The footer is a LIVE property of the bottom of the pane. An "esc interrupt"
// retained in scrollback from a turn that ended long ago must not read as busy
// forever — the same scope rule paneBusyTailLines states for the other two.
func TestIsBusyForOpenCodeIgnoresScrollback(t *testing.T) {
	stale := "   ⬝■■■■■■⬝  esc interrupt   \n" +
		strings.Repeat("some transcript line\n", 10) +
		openCodeIdleCapture
	if IsBusyFor(pfmengine.OpenCode, stale) {
		t.Fatal("a stale esc-interrupt line in scrollback read as busy")
	}
}

func TestIsBusyForKeepsTheClaudeAndCodexRule(t *testing.T) {
	claudeBusy := "some output\n· 12s · esc to interrupt\n"
	for _, id := range []pfmengine.ID{pfmengine.Claude, pfmengine.Codex} {
		if !IsBusyFor(id, claudeBusy) {
			t.Fatalf("%s: busy capture read as idle", id)
		}
		if IsBusyFor(id, "nothing happening here\n") {
			t.Fatalf("%s: quiet capture read as busy", id)
		}
	}
	// An unknown engine keeps the historical rule rather than silently
	// answering "idle" about a pane nobody could classify.
	if !IsBusyFor("", claudeBusy) {
		t.Fatal("unknown engine lost the default busy rule")
	}
}

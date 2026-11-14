package mockengine

import (
	"bytes"
	"strings"
	"testing"
	"time"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
)

func TestPaneRendersWhatCallMatches(t *testing.T) {
	screen := &pane{glyph: "❯"}
	screen.say("> hello")
	idle := screen.render()
	if pfmcall.IsBusy(idle) || pfmcall.SelectorLine(idle) != "" || idle != "\x1b[2J\x1b[H> hello\x0d\n\x0d\n❯ \x0d\n" {
		t.Fatalf("idle frame reads busy or as a menu:\n%s", idle)
	}
	screen.footer = claudeBusyLine(3*time.Second, Tokens{Input: 10, Output: 2})
	if frame := screen.render(); !pfmcall.IsBusy(frame) {
		t.Fatalf("busy footer is not what pfmcall.IsBusy matches:\n%s", frame)
	}
	screen.footer = ""
	screen.menu = &Step{Type: StepMenu, Options: []string{"Yes", "No"}}
	screen.selected = 2
	frame := screen.render()
	if selector := pfmcall.SelectorLine(frame); selector != "❯ 2. No" {
		t.Fatalf("selector = %q for frame:\n%s", selector, frame)
	}
	screen.menu = nil
	screen.queued = []string{"later"}
	if frame := screen.render(); frame != "\x1b[2J\x1b[H> hello\x0d\n\x0d\n❯ \x0d\n  Press up to edit queued messages\x0d\n" {
		t.Fatalf("queued hint (inject/guards.go:26 queueProofPattern) missing:\n%s", frame)
	}
	screen.queued = nil
	screen.agents.rows = []agentRow{{line: "❯ ● tracer  mapping (background)"}}
	frame = screen.render()
	if pfmcall.SelectorLine(frame) != "" || pfmcall.IsBusy(frame) {
		t.Fatalf("agent row read as a menu or a busy turn:\n%s", frame)
	}
	for index := 0; index < historyKeep*2; index++ {
		screen.say("line")
	}
	if len(screen.history) != historyKeep {
		t.Fatalf("history kept %d lines, want %d", len(screen.history), historyKeep)
	}
}

// codexComposerFrame applies internal/spawn/spawn.go's two-part idle proof.
func codexComposerFrame(frame string) bool {
	frame = ansiEscape.ReplaceAllString(frame, "")
	lines := strings.Split(frame, "\n")
	for index, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "› Ask Codex to do anything") {
			continue
		}
		for next := index + 1; next < len(lines) && next <= index+4; next++ {
			footer := strings.TrimSpace(lines[next])
			if strings.Contains(footer, " · ") && !strings.HasPrefix(footer, "›") {
				return true
			}
		}
	}
	return false
}

func TestPaneCodexPlaceholderStyling(t *testing.T) {
	screen := &pane{glyph: "›", placeholder: "Ask Codex to do anything", status: "model · 100%"}
	idle := screen.render()
	if idle != "\x1b[2J\x1b[H\x0d\n› \x1b[2mAsk Codex to do anything\x1b[0m\x0d\nmodel · 100%\x0d\n" ||
		!pfmcall.ComposerIsDimPlaceholder(idle) || !codexComposerFrame(idle) ||
		!codexComposerFrame(ansiEscape.ReplaceAllString(idle, "")) {
		t.Fatalf("idle Codex frame lacks dim placeholder or spawn proof: %q", idle)
	}
	screen.draft.apply(keyEvent{kind: keyRune, r: 'x'})
	typed := screen.render()
	if typed != "\x1b[2J\x1b[H\x0d\n› x\x0d\nmodel · 100%\x0d\n" || pfmcall.ComposerIsDimPlaceholder(typed) ||
		typed != "\x1b[2J\x1b[H\x0d\n› x\x0d\nmodel · 100%\x0d\n" {
		t.Fatalf("typed Codex draft has placeholder styling: %q", typed)
	}
	for _, glyph := range []string{"❯", ">"} {
		other := (&pane{glyph: glyph}).render()
		if strings.Contains(other, "\x1b[2m") {
			t.Fatalf("%s composer gained SGR: %q", glyph, other)
		}
	}
}

func TestPaintWritesAFrameOnlyWhenItChanged(t *testing.T) {
	var out, errs bytes.Buffer
	screen := &pane{glyph: "❯"}
	loop := &paneLoop{proc: &process{stdout: &out, stderr: &errs}, screen: screen}
	screen.say("> hello")
	first := screen.render()
	loop.paint()
	loop.paint()
	if got := out.String(); got != first {
		t.Fatalf("two paints of one frame wrote %q, want the frame once %q", got, first)
	}
	screen.footer = claudeBusyLine(time.Second, Tokens{})
	second := screen.render()
	loop.paint()
	loop.paint()
	if got := out.String(); got != first+second {
		t.Fatalf("a changed frame painted twice wrote %q, want %q", got, first+second)
	}
	// A typed burst is drawn once, after its last key.
	keys := make(chan keyEvent, 2)
	loop.keys = keys
	keys <- keyEvent{kind: keyRune, r: 'b'}
	loop.idleKey(keyEvent{kind: keyRune, r: 'a'})
	if got := out.String(); got != first+second {
		t.Fatalf("a typed key with another waiting wrote %q, want nothing past %q", got, first+second)
	}
	loop.idleKey(<-keys)
	if got, want := out.String(), first+second+screen.render(); screen.draft.text() != "ab" || got != want {
		t.Fatalf("the burst's last key left draft %q and wrote %q, want draft \"ab\" and %q",
			screen.draft.text(), got, want)
	}
	// A warning written on the pane's terminal is covered by the next paint of the same frame.
	before := out.String()
	loop.warn("statusline: %s", "exit status 1")
	loop.paint()
	if got, want := out.String(), before+screen.render(); got != want ||
		errs.String() != "mock-engine: statusline: exit status 1\n" {
		t.Fatalf("a paint after a warning wrote %q and warned %q, want %q and the warning",
			got, errs.String(), want)
	}
}

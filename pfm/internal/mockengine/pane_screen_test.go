package mockengine

import (
	"strings"
	"testing"
	"time"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
)

func TestPaneRendersWhatCallMatches(t *testing.T) {
	screen := &pane{glyph: "❯"}
	screen.say("> hello")
	idle := screen.render()
	if pfmcall.IsBusy(idle) || pfmcall.SelectorLine(idle) != "" || !strings.Contains(idle, "\x1b[2J\x1b[H") {
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
	if selector := pfmcall.SelectorLine(frame); !strings.Contains(selector, "2. No") {
		t.Fatalf("selector = %q for frame:\n%s", selector, frame)
	}
	screen.menu = nil
	screen.queued = []string{"later"}
	if frame := screen.render(); !strings.Contains(frame, "Press up to edit queued messages") {
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
	if !strings.Contains(idle, "› \x1b[2mAsk Codex to do anything\x1b[0m") ||
		!pfmcall.ComposerIsDimPlaceholder(idle) || !codexComposerFrame(idle) ||
		!codexComposerFrame(ansiEscape.ReplaceAllString(idle, "")) {
		t.Fatalf("idle Codex frame lacks dim placeholder or spawn proof: %q", idle)
	}
	screen.draft.apply(keyEvent{kind: keyRune, r: 'x'})
	typed := screen.render()
	if !strings.Contains(typed, "› x\r\n") || pfmcall.ComposerIsDimPlaceholder(typed) ||
		strings.Contains(typed, "\x1b[2mx") {
		t.Fatalf("typed Codex draft has placeholder styling: %q", typed)
	}
	for _, glyph := range []string{"❯", ">"} {
		other := (&pane{glyph: glyph}).render()
		if strings.Contains(other, "\x1b[2m") {
			t.Fatalf("%s composer gained SGR: %q", glyph, other)
		}
	}
}

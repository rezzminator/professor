package mockengine

import (
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/inject"
)

func TestPaneRendersWhatInjectMatches(t *testing.T) {
	screen := &pane{glyph: "❯"}
	screen.say("> hello")
	idle := screen.render()
	if inject.IsBusy(idle) || inject.SelectorLine(idle) != "" || !strings.Contains(idle, "\x1b[2J\x1b[H") {
		t.Fatalf("idle frame reads busy or as a menu:\n%s", idle)
	}
	screen.footer = claudeBusyLine(3*time.Second, Tokens{Input: 10, Output: 2})
	if frame := screen.render(); !inject.IsBusy(frame) {
		t.Fatalf("busy footer is not what inject.IsBusy matches:\n%s", frame)
	}
	screen.footer = ""
	screen.menu = &Step{Type: StepMenu, Options: []string{"Yes", "No"}}
	screen.selected = 2
	frame := screen.render()
	if selector := inject.SelectorLine(frame); !strings.Contains(selector, "2. No") {
		t.Fatalf("selector = %q for frame:\n%s", selector, frame)
	}
	screen.menu = nil
	screen.queued = []string{"later"}
	if frame := screen.render(); !strings.Contains(frame, "Press up to edit queued messages") {
		t.Fatalf("queued hint (inject/guards.go:26 queueProofPattern) missing:\n%s", frame)
	}
	screen.queued = nil
	screen.agentRows = []string{"❯ ● tracer  mapping (background)"}
	frame = screen.render()
	if inject.SelectorLine(frame) != "" || inject.IsBusy(frame) {
		t.Fatalf("agent row read as a menu or a busy turn:\n%s", frame)
	}
	for index := 0; index < historyKeep*2; index++ {
		screen.say("line")
	}
	if len(screen.history) != historyKeep {
		t.Fatalf("history kept %d lines, want %d", len(screen.history), historyKeep)
	}
}

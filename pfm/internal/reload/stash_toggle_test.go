package reload

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// stashToggleTmux models Claude Code's composer the way 2.1.283 actually
// behaves: Ctrl+S is a TOGGLE — a draft goes into the stash, and an empty
// composer (a dim suggestion is empty underneath) gets a pending stash
// restored into it. Enter submits whatever the composer holds; only a bare
// /exit ends the chat.
type stashToggleTmux struct {
	fakeReloadTmux
	composer    string
	suggestion  string
	stash       string
	refuseStash bool
	// statusMarker draws Claude Code 2.1.257's "› stashed" status-row marker
	// beneath the input box while a stash is pending.
	statusMarker bool
	// hintAfterStash is the dim suggestion drawn into the box a stash emptied.
	hintAfterStash string
	stashKeys      int
	submitted      []string
}

func (tmux *stashToggleTmux) Capture(context.Context, string, string) (string, error) {
	return tmux.screen(tmux.suggestion), nil
}

func (tmux *stashToggleTmux) CaptureStyled(context.Context, string, string) (string, error) {
	return tmux.screen("\x1b[2m" + tmux.suggestion + "\x1b[0m"), nil
}

func (tmux *stashToggleTmux) screen(hint string) string {
	line := "❯ " + tmux.composer
	if tmux.composer == "" && tmux.suggestion != "" {
		line = "❯ " + hint
	}
	status := "  Opus │ pfm"
	if tmux.statusMarker && tmux.stash != "" {
		status += " › stashed"
	}
	return "Claude\n" + line + "\n────\n" + status
}

func (tmux *stashToggleTmux) SendKey(_ context.Context, _, _, key string) error {
	switch key {
	case "C-s":
		tmux.stashKeys++
		switch {
		case tmux.refuseStash:
		case tmux.composer != "":
			tmux.stash, tmux.composer = tmux.composer, ""
			tmux.suggestion = tmux.hintAfterStash
		case tmux.stash != "":
			tmux.composer, tmux.stash = tmux.stash, ""
		}
	case "BSpace":
		if tmux.composer != "" {
			runes := []rune(tmux.composer)
			tmux.composer = string(runes[:len(runes)-1])
		}
	case "Enter":
		tmux.submitted = append(tmux.submitted, tmux.composer)
		tmux.dead = tmux.composer == "/exit"
		tmux.composer = ""
	}
	return nil
}

func (tmux *stashToggleTmux) SendLiteral(_ context.Context, _, _, value string) error {
	tmux.literal = value
	tmux.composer += value
	return nil
}

func runStashToggle(t *testing.T, tmux *stashToggleTmux) error {
	t.Helper()
	fakeClock := clock.NewFake(time.Unix(0, 0))
	sidDir := t.TempDir()
	var err error
	driveFakeClock(t, fakeClock, func() {
		_, err = Run(
			context.Background(),
			reloadIdleWaitRequest("/tmp/tmux-1000/probe-reload-stash-toggle"),
			Options{SIDDir: sidDir, Delay: -1, Poll: -1, ExitTries: 2, Clock: fakeClock},
			tmux,
			nil,
			nil,
		)
	})
	return err
}

func wantOnlyExitSubmitted(t *testing.T, tmux *stashToggleTmux) {
	t.Helper()
	if len(tmux.submitted) != 1 || tmux.submitted[0] != "/exit" {
		t.Fatalf("submitted=%q, want exactly one bare /exit", tmux.submitted)
	}
	if tmux.respawn == "" {
		t.Fatal("pane was never respawned after a clean /exit")
	}
}

// The live failure: the chat's own turn ended with an empty composer and a
// stash pending. A blind Ctrl+S restored that stash, /exit landed on its end,
// and the reload either refused ("never rendered") or submitted the old draft
// plus /exit as a prompt. An empty composer needs no stash key at all.
func TestRunTypesExitOverAnEmptyComposerWithoutRestoringAPendingStash(t *testing.T) {
	tmux := &stashToggleTmux{stash: "an old draft the human stashed earlier"}
	if err := runStashToggle(t, tmux); err != nil {
		t.Fatalf("Run: %v", err)
	}
	wantOnlyExitSubmitted(t, tmux)
	if tmux.stashKeys != 0 {
		t.Fatalf("stash key pressed %d times on an empty composer, want 0", tmux.stashKeys)
	}
	if tmux.stash != "an old draft the human stashed earlier" {
		t.Fatalf("pending stash=%q, want it left untouched", tmux.stash)
	}
}

// After a model turn Claude Code shows a dim prompt suggestion; the plain
// capture reads it as text, but the composer is empty underneath, so Ctrl+S
// there is the same stash-restoring toggle.
func TestRunDoesNotStashADimSuggestion(t *testing.T) {
	tmux := &stashToggleTmux{suggestion: "run the integration suite", stash: "an old draft"}
	if err := runStashToggle(t, tmux); err != nil {
		t.Fatalf("Run: %v", err)
	}
	wantOnlyExitSubmitted(t, tmux)
	if tmux.stashKeys != 0 || tmux.stash != "an old draft" {
		t.Fatalf("stashKeys=%d stash=%q, want no key and the stash untouched", tmux.stashKeys, tmux.stash)
	}
}

// A real draft the human left is stashed, never mashed into the /exit.
func TestRunStashesARealDraftBeforeTypingExit(t *testing.T) {
	tmux := &stashToggleTmux{composer: "half-typed question"}
	if err := runStashToggle(t, tmux); err != nil {
		t.Fatalf("Run: %v", err)
	}
	wantOnlyExitSubmitted(t, tmux)
	if tmux.stashKeys != 1 || tmux.stash != "half-typed question" {
		t.Fatalf("stashKeys=%d stash=%q, want one key and the draft stashed", tmux.stashKeys, tmux.stash)
	}
}

// A draft Ctrl+S did not clear is refused by name before anything is typed:
// /exit appended to it would be submitted as a prompt, or never render.
func TestRunRefusesADraftThatWillNotStash(t *testing.T) {
	tmux := &stashToggleTmux{composer: "half-typed question", refuseStash: true}
	err := runStashToggle(t, tmux)
	if err == nil || !strings.Contains(err.Error(), "half-typed question") {
		t.Fatalf("Run error=%v, want a refusal naming the draft", err)
	}
	if len(tmux.submitted) != 0 || tmux.literal != "" {
		t.Fatalf("submitted=%q literal=%q, want nothing typed or submitted", tmux.submitted, tmux.literal)
	}
	if tmux.composer != "half-typed question" || tmux.respawn != "" {
		t.Fatalf("composer=%q respawn=%q, want the draft intact and no respawn", tmux.composer, tmux.respawn)
	}
}

// Claude Code 2.1.257 marks a pending stash on the status row beneath the
// input box ("› stashed"). That row is not the composer: an empty box with a
// stash pending still needs no key, and a draft just stashed reads empty.
func TestRunReadsTheInputBoxNotTheStashMarkerBelowIt(t *testing.T) {
	pending := &stashToggleTmux{stash: "an old draft", statusMarker: true}
	if err := runStashToggle(t, pending); err != nil {
		t.Fatalf("Run with a pending stash: %v", err)
	}
	wantOnlyExitSubmitted(t, pending)
	if pending.stashKeys != 0 || pending.stash != "an old draft" {
		t.Fatalf("stashKeys=%d stash=%q, want no key and the stash untouched", pending.stashKeys, pending.stash)
	}
	fresh := &stashToggleTmux{composer: "half-typed question", statusMarker: true}
	if err := runStashToggle(t, fresh); err != nil {
		t.Fatalf("Run stashing a draft: %v", err)
	}
	wantOnlyExitSubmitted(t, fresh)
}

// The box a stash just emptied may show a dim suggestion at once; that is an
// empty composer, not a draft that refused to stash.
func TestRunTreatsADimHintAfterTheStashAsEmpty(t *testing.T) {
	tmux := &stashToggleTmux{composer: "half-typed question", hintAfterStash: "run the integration suite"}
	if err := runStashToggle(t, tmux); err != nil {
		t.Fatalf("Run: %v", err)
	}
	wantOnlyExitSubmitted(t, tmux)
}

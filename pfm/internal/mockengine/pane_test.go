package mockengine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
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
		!inject.ComposerIsDimPlaceholder(idle) || !codexComposerFrame(idle) ||
		!codexComposerFrame(ansiEscape.ReplaceAllString(idle, "")) {
		t.Fatalf("idle Codex frame lacks dim placeholder or spawn proof: %q", idle)
	}
	screen.draft.apply(keyEvent{kind: keyRune, r: 'x'})
	typed := screen.render()
	if !strings.Contains(typed, "› x\r\n") || inject.ComposerIsDimPlaceholder(typed) ||
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

func codexPaneFixture(t *testing.T) (*fixture, *tui) {
	t.Helper()
	fix := newFixture(t)
	fix.write(Scenario{SessionID: fixtureThread, Pane: codexShapes})
	session := fix.startTUI("codex", codexArgs(), nil)
	session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "›") })
	return fix, session
}

func TestPaneCodexIdleComposer(t *testing.T) {
	_, session := codexPaneFixture(t)
	session.waitFrame("idle Codex composer with nearby footer", codexComposerFrame)
	session.typeRaw("typed draft")
	session.waitFrame("typed draft", func(frame string) bool {
		return strings.Contains(frame, "› typed draft") && !strings.Contains(frame, "Ask Codex to do anything")
	})
	session.typeRaw(strings.Repeat("\x7f", len("typed draft")))
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("Codex exit = %d, want 0", code)
	}
	session.out.mu.Lock()
	output := session.out.buf.String()
	session.out.mu.Unlock()
	if !strings.Contains(output, "\x1b[?1049h") || !strings.HasSuffix(output, "\x1b[?1049l") {
		t.Fatalf("Codex did not enter and leave tmux's alternate screen: %q", output)
	}
}

func TestPaneCodexRenameOffer(t *testing.T) {
	_, session := codexPaneFixture(t)
	draft := ""
	for _, letter := range "/rename" {
		draft += string(letter)
		session.typeRaw(string(letter))
		session.waitFrame("rename offer", func(frame string) bool {
			return strings.Contains(frame, "› "+draft+"\r\n") && strings.Contains(frame, "rename the current thread")
		})
	}
	session.typeRaw(" extra")
	session.waitFrame("ordinary typed draft", func(frame string) bool {
		return strings.Contains(frame, "› /rename extra") && !strings.Contains(frame, "rename the current thread")
	})
}

func openCodexRename(t *testing.T, session *tui) {
	t.Helper()
	session.typeLine("/rename")
	session.waitFrame("rename dialog", func(frame string) bool {
		return strings.Contains(frame, "Rename thread") && strings.Contains(frame, "Type a name and press Enter") &&
			strings.Contains(frame, "Press enter to confirm or esc to go back") &&
			!strings.Contains(
				frame,
				"Ask Codex to do anything",
			) && !strings.Contains(frame, "rename the current thread")
	})
}

func TestPaneCodexRenameDialog(t *testing.T) {
	_, session := codexPaneFixture(t)
	openCodexRename(t, session)
	session.typeRaw("draft name")
	session.waitFrame("rename field", func(frame string) bool { return strings.Contains(frame, "› draft name") })
}

func TestPaneCodexRenameConfirm(t *testing.T) {
	fix, session := codexPaneFixture(t)
	openCodexRename(t, session)
	session.typeRaw("discard" + strings.Repeat("\x7f", 80) + "  orbit worker  \r")
	session.waitFrame("named composer", func(frame string) bool {
		return codexComposerFrame(frame) && strings.Contains(frame, "orbit worker · ") &&
			!strings.Contains(frame, "Rename thread")
	})
	rows := strings.Split(
		strings.TrimSpace(readFile(t, filepath.Join(fix.codexHome, codexmeta.SessionIndexFile))),
		"\n",
	)
	if len(rows) != 1 {
		t.Fatalf("rename wrote %d session index rows, want one", len(rows))
	}
	var entry codexmeta.SessionIndexEntry
	if err := json.Unmarshal([]byte(rows[0]), &entry); err != nil {
		t.Fatal(err)
	}
	if _, err := time.Parse(time.RFC3339Nano, entry.UpdatedAt); err != nil ||
		entry.ID != fixtureThread || entry.ThreadName != "orbit worker" {
		t.Fatalf("rename entry = %+v, timestamp error=%v", entry, err)
	}
}

func TestPaneCodexRenameEmptyName(t *testing.T) {
	fix, session := codexPaneFixture(t)
	openCodexRename(t, session)
	session.typeRaw("  \r")
	session.waitFrame("empty-name refusal in the dialog", func(frame string) bool {
		return strings.Contains(frame, "Thread name cannot be empty.") && strings.Contains(frame, "Rename thread") &&
			!codexComposerFrame(frame)
	})
	if _, err := os.Stat(filepath.Join(fix.codexHome, codexmeta.SessionIndexFile)); !os.IsNotExist(err) {
		t.Fatalf("empty name wrote a rename row: %v", err)
	}
}

func TestPaneCodexRenameEscape(t *testing.T) {
	fix, session := codexPaneFixture(t)
	openCodexRename(t, session)
	session.typeRaw("unused\x1b")
	session.waitFrame("composer after Escape", func(frame string) bool {
		return codexComposerFrame(frame) && !strings.Contains(frame, "Rename thread")
	})
	if _, err := os.Stat(filepath.Join(fix.codexHome, codexmeta.SessionIndexFile)); !os.IsNotExist(err) {
		t.Fatalf("Escape wrote a rename row: %v", err)
	}
}

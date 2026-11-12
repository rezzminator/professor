package mockengine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

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

// TestPaneClaudeTrustDialog pins the mock's folder-trust dialog, asked for by
// its scenario wire key: it is the frame internal/engine reads as the dialog,
// 1 boots on to the composer, and every key that answers "No, exit" ends the
// engine with status 1.
func TestPaneClaudeTrustDialog(t *testing.T) {
	for _, test := range []struct {
		name     string
		key      string
		wantExit int
		boots    bool
	}{
		{name: "1 trusts the folder", key: "1", boots: true},
		{name: "Enter takes the default No, exit", key: "\r", wantExit: 1},
		{name: "Escape cancels", key: "\x1b", wantExit: 1},
		{name: "2 is No, exit", key: "2", wantExit: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			fix.write(Scenario{BusyMS: intPtr(0)})
			var wire map[string]any
			if err := json.Unmarshal([]byte(readFile(t, fix.scenario)), &wire); err != nil {
				t.Fatal(err)
			}
			wire["trust_dialog"] = true
			content, err := json.Marshal(wire)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fix.scenario, content, 0o600); err != nil {
				t.Fatal(err)
			}
			session := fix.startTUI("claude", claudeArgs("--session-id", fixtureSession), nil)
			session.waitFrame("folder-trust dialog", func(frame string) bool {
				return pfmengine.ClaudeTrustDialog(frame) && strings.Contains(frame, fix.work)
			})
			session.typeRaw(test.key)
			if test.boots {
				session.waitFrame("composer after trusting", func(frame string) bool {
					return strings.Contains(frame, "❯") && !pfmengine.ClaudeTrustDialog(frame)
				})
				session.typeLine("/exit")
			}
			if code := session.waitExit(); code != test.wantExit {
				t.Fatalf("exit = %d, want %d; frame:\n%s", code, test.wantExit, session.out.frame())
			}
		})
	}
}

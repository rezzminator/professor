package installer

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReadStatusLineCommandDistinguishesAbsentFromUnreadable pins the
// distinction doctor's row needs and round 1 collapsed: a settings.json that
// does not exist yet is a genuine "not configured" (empty command, nil
// error), while one that exists but cannot be read or parsed is a DIFFERENT
// state — its actual wiring is unknown, not absent — and must come back as
// an error, never silently folded into the same empty string.
func TestReadStatusLineCommandDistinguishesAbsentFromUnreadable(t *testing.T) {
	home := t.TempDir()

	t.Run("a settings file that does not exist is not configured, not an error", func(t *testing.T) {
		command, err := ReadStatusLineCommand(filepath.Join(home, "never-written", "settings.json"))
		if err != nil || command != "" {
			t.Fatalf("ReadStatusLineCommand() = %q, %v, want empty/nil for an absent file", command, err)
		}
	})

	t.Run("a settings file that is a directory is unreadable, not absent", func(t *testing.T) {
		path := filepath.Join(home, "is-a-dir", "settings.json")
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		command, err := ReadStatusLineCommand(path)
		if err == nil || command != "" {
			t.Fatalf("ReadStatusLineCommand() = %q, %v, want an error naming the unreadable file", command, err)
		}
	})

	t.Run("malformed JSON is unreadable, not absent", func(t *testing.T) {
		path := filepath.Join(home, "malformed", "settings.json")
		writeFixture(t, path, "{not json")
		command, err := ReadStatusLineCommand(path)
		if err == nil || command != "" {
			t.Fatalf("ReadStatusLineCommand() = %q, %v, want an error naming the malformed file", command, err)
		}
	})

	t.Run("a valid settings file with no statusLine key is still not configured", func(t *testing.T) {
		path := filepath.Join(home, "no-statusline", "settings.json")
		writeFixture(t, path, `{}`)
		command, err := ReadStatusLineCommand(path)
		if err != nil || command != "" {
			t.Fatalf("ReadStatusLineCommand() = %q, %v, want empty/nil with no statusLine key", command, err)
		}
	})

	t.Run("a configured command reads back", func(t *testing.T) {
		path := filepath.Join(home, "configured", "settings.json")
		writeFixture(t, path, `{"statusLine":{"type":"command","command":"pfm-statusline"}}`)
		command, err := ReadStatusLineCommand(path)
		if err != nil || command != "pfm-statusline" {
			t.Fatalf("ReadStatusLineCommand() = %q, %v, want %q/nil", command, err, "pfm-statusline")
		}
	})
}

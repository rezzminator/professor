package spawn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeSessionIndex(t *testing.T, root string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(
		filepath.Join(root, "session_index.jsonl"),
		[]byte(strings.Join(lines, "\n")),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
}

func indexLine(id, name string, at time.Time) string {
	return `{"id":"` + id + `","thread_name":"` + name + `","updated_at":"` + at.UTC().Format(time.RFC3339Nano) + `"}`
}

// TestCodexIndexProofFindsOnlyThisRename: the ledger proves a rename only
// when it carries the name at or after the rename began. An older rename to
// the same name (a chat reusing a label) never counts, and a torn last line —
// Codex appending while pfm reads — is skipped, not fatal.
func TestCodexIndexProofFindsOnlyThisRename(t *testing.T) {
	since := time.Date(2026, 9, 13, 0, 25, 58, 0, time.UTC)
	stale := t.TempDir()
	writeSessionIndex(t, stale, indexLine("old", "PING_PROBE", since.Add(-time.Hour)), `{"id":"torn","thread_na`)
	fresh := t.TempDir()
	writeSessionIndex(
		t,
		fresh,
		indexLine("other", "SOMETHING_ELSE", since.Add(time.Second)),
		indexLine("new", "PING_PROBE", since.Add(3*time.Second)),
	)

	if landed, err := codexIndexProof([]string{stale})("PING_PROBE", since); err != nil || landed {
		t.Fatalf("stale-only ledger = %v, %v; want false, nil", landed, err)
	}
	if landed, err := codexIndexProof([]string{stale, fresh})("PING_PROBE", since); err != nil || !landed {
		t.Fatalf("fresh rename in the second home = %v, %v; want true, nil", landed, err)
	}
	if landed, err := codexIndexProof([]string{fresh})("NOT_RENAMED", since); err != nil || landed {
		t.Fatalf("an unrecorded name = %v, %v; want false, nil", landed, err)
	}
}

// TestCodexIndexProofSeparatesNothingRecordedFromCouldNotRead: a home with no
// ledger yet is "nothing recorded"; a ledger that cannot be read, or no home
// to read at all, is an error — the "could not verify" verdict, never "no".
func TestCodexIndexProofSeparatesNothingRecordedFromCouldNotRead(t *testing.T) {
	since := time.Now()
	if landed, err := codexIndexProof([]string{t.TempDir()})("x", since); err != nil || landed {
		t.Fatalf("home without a ledger = %v, %v; want false, nil", landed, err)
	}
	unreadable := t.TempDir()
	if err := os.Mkdir(filepath.Join(unreadable, "session_index.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := codexIndexProof([]string{unreadable})("x", since); err == nil {
		t.Fatal("an unreadable ledger returned no error")
	}
	if _, err := codexIndexProof(nil)("x", since); err == nil {
		t.Fatal("no Codex home returned no error")
	}
}

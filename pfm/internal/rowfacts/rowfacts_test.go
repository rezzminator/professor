package rowfacts

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

func osChtimes(path string, when time.Time) error { return os.Chtimes(path, when, when) }

func TestEnrichFillsFactsClonesOnlyWhenARowChangedAndNeverWritesTheCallersRows(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "c.jsonl")
	writeFile(t, transcript, assistant("claude-opus-5-5", "tool_use")+"\n")
	rows := []compose.Row{
		{Kind: compose.NewClaude, Name: "New Claude chat"},
		{Kind: compose.LiveClaude, ID: "c", Name: "worker", Path: transcript},
	}
	reader := NewReader(t.TempDir())
	now := time.Now().UnixNano()
	out, failures := reader.Enrich(rows, now)
	if len(failures) != 0 {
		t.Fatalf("failures: %v", failures)
	}
	if out[1].Model != "claude-opus-5-5" || !out[1].Working {
		t.Errorf("the live row lacks its facts: %+v", out[1])
	}
	if rows[1].Model != "" || rows[1].Working {
		t.Error("the caller's rows were written")
	}
	untouched := []compose.Row{{Kind: compose.NewClaude, Name: "New"}}
	again, _ := reader.Enrich(untouched, now)
	if &again[0] != &untouched[0] {
		t.Error("rows that gained no facts are returned as they came, not cloned")
	}
}

func TestEnrichServesAnUnchangedFileFromItsCache(t *testing.T) {
	transcript := filepath.Join(t.TempDir(), "c.jsonl")
	writeFile(t, transcript, assistant("claude-opus-5-5", "end_turn")+"\n")
	stamp := time.Now().Add(-time.Minute)
	if err := osChtimes(transcript, stamp); err != nil {
		t.Fatal(err)
	}
	reader := NewReader(t.TempDir())
	rows := []compose.Row{{Kind: compose.LiveClaude, ID: "c", Path: transcript}}
	reader.Enrich(rows, time.Now().UnixNano())
	// Same size, same modified time, different bytes: only a cache hit can answer
	// with the old model.
	writeFile(t, transcript, assistant("claude-opus-5-6", "end_turn")+"\n")
	if err := osChtimes(transcript, stamp); err != nil {
		t.Fatal(err)
	}
	out, _ := reader.Enrich(rows, time.Now().UnixNano())
	if out[0].Model != "claude-opus-5-5" {
		t.Errorf("an unchanged file must not be read again, got %q", out[0].Model)
	}
	if err := osChtimes(transcript, time.Now()); err != nil {
		t.Fatal(err)
	}
	out, _ = reader.Enrich(rows, time.Now().UnixNano())
	if out[0].Model != "claude-opus-5-6" {
		t.Errorf("a file that moved is read again, got %q", out[0].Model)
	}
}

func TestEnrichNamesAFailureAndKeepsTheRowOtherwiseWhole(t *testing.T) {
	sid := t.TempDir()
	transcript := filepath.Join(t.TempDir(), "c.jsonl")
	writeFile(t, transcript, assistant("claude-opus-5-5", "end_turn")+"\n")
	writeFile(t, filepath.Join(sid, "statusline-effort-c"), "{broken")
	rows := []compose.Row{
		{Kind: compose.LiveClaude, ID: "c", Name: "chat", Path: transcript},
		{Kind: compose.LiveClaude, ID: "gone", Name: "swept", Path: filepath.Join(t.TempDir(), "missing.jsonl")},
	}
	out, failures := NewReader(sid).Enrich(rows, time.Now().UnixNano())
	if len(failures) != 1 {
		t.Fatalf("the unreadable session record is one named failure, got %v", failures)
	}
	if out[0].Model != "claude-opus-5-5" {
		t.Errorf("a failed record must not cost the row its transcript's model: %+v", out[0])
	}
	if out[1].Model != "" || out[1].Working {
		t.Errorf("a transcript that is gone is no facts, not invented ones: %+v", out[1])
	}
}

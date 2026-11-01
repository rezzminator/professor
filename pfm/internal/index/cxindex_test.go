package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestCodexNamesSurviveAnUnreadableSessionIndex: an undecodable session index
// line costs that line only, warned by count; a session index that cannot be
// read at all keeps every prior name and the Codex pass goes on, warned.
func TestCodexNamesSurviveAnUnreadableSessionIndex(t *testing.T) {
	for _, test := range []struct {
		name   string
		legacy bool
	}{{"every home", false}, {"single legacy home", true}} {
		t.Run(test.name, func(t *testing.T) {
			codexHome := t.TempDir()
			indexPath := filepath.Join(codexHome, "session_index.jsonl")
			writeLines(t, indexPath, `{"id":"named","thread_name":"KEPT"}`, `{"thread_name":"no id"}`, `not json`)
			database := openIndexStore(t)
			t.Cleanup(func() { _ = database.Close() })
			ctx, recorder := obs.Test(t)
			pass := func() error {
				return SyncCodex(ctx, database, []string{codexHome}, &Counters{legacySingleCodexHome: test.legacy})
			}
			if err := pass(); err != nil {
				t.Fatalf("first SyncCodex() error = %v", err)
			}
			if got := unparsedLineCounts(recorder, indexPath); len(got) != 1 || got[0] != float64(2) {
				t.Fatalf("unparsed line warnings for %s = %v, want [2]", indexPath, got)
			}

			if err := os.Remove(indexPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(indexPath, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := pass(); err != nil {
				t.Fatalf("SyncCodex() over an unreadable session index error = %v, want the pass to go on", err)
			}
			names, err := database.CxNames(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(names) != 1 || names["named"] != "KEPT" {
				t.Fatalf("Codex names = %v, want named=KEPT kept", names)
			}
			want := `codex session index|read "` + indexPath + `": read ` + indexPath + ": is a directory"
			if got := unreadableWarnings(recorder, indexPath); len(got) != 1 || got[0] != want {
				t.Fatalf("warnings for %s = %q, want [%q]", indexPath, got, want)
			}
		})
	}
}

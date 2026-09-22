package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// TestContinuedInColumnIsEnsuredOnAnOlderSchema8Database opens a database an
// older pfm left at schema 8 — no continued_in column — and proves the column
// is added without a version bump (an older pfm on the machine can still open
// it) and round-trips, with Superseded derived from it.
func TestContinuedInColumnIsEnsuredOnAnOlderSchema8Database(t *testing.T) {
	dbPath := setStoreTestJail(t)
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range migrations {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.ExecContext(ctx, "PRAGMA user_version=8"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	for pass := 0; pass < 2; pass++ { // the second open must not ALTER again
		opened := openTestStore(t)
		assertSchemaVersion(t, opened, SchemaVersion)
		if pass == 0 {
			for _, transcript := range []Transcript{
				{UUID: "old", Path: "/p/old.jsonl", Size: 1, PromptCount: 1, ContinuedIn: "new"},
				{UUID: "new", Path: "/p/new.jsonl", Size: 1, PromptCount: 1},
				{UUID: "orphan", Path: "/p/orphan.jsonl", Size: 1, PromptCount: 1, ContinuedIn: "gone"},
			} {
				if err := opened.UpsertTranscript(ctx, transcript); err != nil {
					t.Fatal(err)
				}
			}
		}
		want := map[string]struct {
			continuedIn string
			superseded  bool
		}{
			"old":    {"new", true},
			"new":    {"", false},
			"orphan": {"gone", false},
		}
		transcripts, err := opened.Transcripts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, transcript := range transcripts {
			expected := want[transcript.UUID]
			if transcript.ContinuedIn != expected.continuedIn || transcript.Superseded != expected.superseded {
				t.Fatalf("pass %d %s = {continuedIn=%q superseded=%t}, want %+v",
					pass, transcript.UUID, transcript.ContinuedIn, transcript.Superseded, expected)
			}
		}

		// The cached first frame agrees with compose: a superseded segment
		// is neither a candidate nor counted as killed or suppressed.
		candidates, _, counts, err := opened.DefaultCandidates(ctx, 30, 15)
		if err != nil {
			t.Fatal(err)
		}
		for _, candidate := range candidates {
			if candidate.UUID == "old" {
				t.Fatalf("pass %d: DefaultCandidates offered the superseded segment", pass)
			}
		}
		if len(candidates) != 2 || counts.Killed != 0 || counts.Suppressed != 0 {
			t.Fatalf(
				"pass %d: candidates=%d counts=%+v, want 2 candidates and nothing counted",
				pass, len(candidates), counts,
			)
		}
		if err := opened.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

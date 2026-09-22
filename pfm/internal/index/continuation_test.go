package index

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/store"
)

const (
	continuedPredecessor = "11111111-2222-4333-8444-555555555555"
	continuedSuccessor   = "66666666-7777-4888-8999-aaaaaaaaaaaa"
	// preContinuationParserVersion is the claude parser version every pfm
	// wrote before continued-in records were read. A database it indexed
	// holds rows whose files will never change again, so only a version bump
	// makes the new parser read them.
	preContinuationParserVersion = "3"
)

func continuationJail(t *testing.T) (string, *store.Store, *Indexer) {
	t.Helper()
	root := t.TempDir()
	claudeRoot := filepath.Join(root, "claude")
	project := filepath.Join(claudeRoot, "-work-app")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_TMPDIR", filepath.Join(root, "t"))
	t.Setenv(paths.EnvDB, filepath.Join(root, "state", "fleet.db"))
	t.Setenv(paths.EnvSIDDir, filepath.Join(root, "sid"))
	t.Setenv(paths.EnvClaudeRoots, claudeRoot)
	t.Setenv(paths.EnvCodexHome, filepath.Join(root, "codex"))
	t.Setenv(paths.EnvTmuxDir, filepath.Join(root, "tmux"))
	t.Setenv(paths.EnvHome, filepath.Join(root, "home"))
	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	indexer, err := New(database)
	if err != nil {
		t.Fatal(err)
	}
	return project, database, indexer
}

func continuedPrompt(text string) map[string]any {
	return map[string]any{
		"type":         "user",
		"cwd":          "/work/app",
		"promptSource": "typed",
		"entrypoint":   "cli",
		"message":      map[string]any{"role": "user", "content": text},
	}
}

func continuedIn(session, successor string) map[string]any {
	return map[string]any{
		"type":                 "continued-in",
		"timestamp":            "2026-09-18T08:50:25.188Z",
		"sessionId":            session,
		"continuedInSessionId": successor,
	}
}

func mustTranscript(t *testing.T, database *store.Store, id string) store.Transcript {
	t.Helper()
	transcript, found, err := database.Transcript(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("Transcript(%s) found=%t err=%v", id, found, err)
	}
	return transcript
}

// TestClaudeContinuedInRecordSupersedesThePredecessor pins how a chat Claude
// moved into a background job is indexed: the old session records where the
// conversation went, is superseded only while that successor is indexed, and
// stops being superseded the moment it is resumed under its own id again.
func TestClaudeContinuedInRecordSupersedesThePredecessor(t *testing.T) {
	project, database, indexer := continuationJail(t)
	ctx := context.Background()
	predecessorPath := filepath.Join(project, continuedPredecessor+".jsonl")
	successorPath := filepath.Join(project, continuedSuccessor+".jsonl")
	rewriteJSONLines(t, predecessorPath, []any{
		continuedPrompt("design the lanes"),
		map[string]any{"type": "custom-title", "customTitle": "OLD"},
		continuedIn(continuedPredecessor, continuedSuccessor),
	})

	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	orphan := mustTranscript(t, database, continuedPredecessor)
	if orphan.ContinuedIn != continuedSuccessor || orphan.Superseded {
		t.Fatalf("predecessor before its successor is indexed = {continuedIn=%q superseded=%t}, "+
			"want the handoff recorded but NOT superseded — a successor pfm cannot show must not hide the chat",
			orphan.ContinuedIn, orphan.Superseded)
	}

	rewriteJSONLines(t, successorPath, []any{
		map[string]any{"type": "custom-title", "customTitle": "NEW"},
		continuedPrompt("design the lanes"),
	})
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if parked := mustTranscript(t, database, continuedPredecessor); !parked.Superseded {
		t.Fatalf("predecessor = %+v, want superseded once its successor is indexed", parked)
	}
	successor := mustTranscript(t, database, continuedSuccessor)
	if successor.ContinuedIn != "" || successor.Superseded {
		t.Fatalf("successor = %+v, want a chat of its own", successor)
	}

	// Resumed under its own id: a real prompt lands after the handoff.
	rewriteJSONLines(t, predecessorPath, []any{
		continuedPrompt("design the lanes"),
		map[string]any{"type": "custom-title", "customTitle": "OLD"},
		continuedIn(continuedPredecessor, continuedSuccessor),
		continuedPrompt("back in the original session"),
	})
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if resumed := mustTranscript(t, database, continuedPredecessor); resumed.ContinuedIn != "" || resumed.Superseded {
		t.Fatalf("predecessor resumed after the handoff = %+v, want a live chat again", resumed)
	}
}

// TestClaudeContinuedInIgnoresIDsThatNameNoSession: only a well-formed id
// other than the transcript's own is a handoff.
func TestClaudeContinuedInIgnoresIDsThatNameNoSession(t *testing.T) {
	project, database, indexer := continuationJail(t)
	for _, test := range []struct{ file, target string }{
		{"aaaaaaaa-0000-4000-8000-000000000001", "../../etc/passwd"},
		{"aaaaaaaa-0000-4000-8000-000000000002", "aaaaaaaa-0000-4000-8000-000000000002"},
		{"aaaaaaaa-0000-4000-8000-000000000003", ""},
	} {
		rewriteJSONLines(t, filepath.Join(project, test.file+".jsonl"), []any{
			continuedPrompt("hello"),
			continuedIn(test.file, test.target),
		})
	}
	if _, err := indexer.Run(context.Background(), Options{}); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	for _, id := range []string{
		"aaaaaaaa-0000-4000-8000-000000000001",
		"aaaaaaaa-0000-4000-8000-000000000002",
		"aaaaaaaa-0000-4000-8000-000000000003",
	} {
		if transcript := mustTranscript(t, database, id); transcript.ContinuedIn != "" {
			t.Fatalf("%s continuedIn = %q, want no handoff", id, transcript.ContinuedIn)
		}
	}
}

// TestClaudeContinuationBackfillsRowsAPreviousParserIndexed pins the upgrade
// path: a database an older pfm indexed already holds the parked chat's rows
// with no continuation, their files unchanged since. The parser version bump
// is the only thing that makes this pfm read them again.
func TestClaudeContinuationBackfillsRowsAPreviousParserIndexed(t *testing.T) {
	project, database, indexer := continuationJail(t)
	ctx := context.Background()
	rewriteJSONLines(t, filepath.Join(project, continuedPredecessor+".jsonl"), []any{
		continuedPrompt("design the lanes"),
		continuedIn(continuedPredecessor, continuedSuccessor),
	})
	rewriteJSONLines(t, filepath.Join(project, continuedSuccessor+".jsonl"), []any{
		continuedPrompt("design the lanes"),
	})
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("Run() = %v", err)
	}

	// Rewind to what the previous parser left: the row without its handoff,
	// and that parser's version stamp.
	stale := mustTranscript(t, database, continuedPredecessor)
	stale.ContinuedIn = ""
	if err := database.Batch(ctx, 1, func(tx *store.ImmediateTx, _, _ int) error {
		return tx.UpsertTranscript(ctx, stale)
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.SetMeta(ctx, claudeParserVersionKey, preContinuationParserVersion); err != nil {
		t.Fatal(err)
	}

	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("upgrade Run() = %v", err)
	}
	backfilled := mustTranscript(t, database, continuedPredecessor)
	if backfilled.ContinuedIn != continuedSuccessor || !backfilled.Superseded {
		t.Fatalf("predecessor after upgrade = {continuedIn=%q superseded=%t}, want the handoff backfilled "+
			"— claudeParserVersion must move past %q so rows indexed before continued-in was read are reparsed",
			backfilled.ContinuedIn, backfilled.Superseded, preContinuationParserVersion)
	}
}

package index

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
	"github.com/rezzminator/professor/pfm/internal/store"
)

type builtinTestSource struct{ id pfmengine.ID }

func (source builtinTestSource) Sync(
	ctx context.Context,
	database *store.Store,
	roots []string,
	counters *Counters,
) error {
	switch source.id {
	case pfmengine.Claude:
		return SyncClaude(ctx, database, roots, counters)
	case pfmengine.Codex:
		return SyncCodex(ctx, database, roots, counters)
	case pfmengine.OpenCode:
		return SyncOpenCode(ctx, database, roots, counters)
	default:
		return nil
	}
}

func init() {
	RegisterSource(pfmengine.Claude, builtinTestSource{id: pfmengine.Claude})
	RegisterSource(pfmengine.Codex, builtinTestSource{id: pfmengine.Codex})
	RegisterSource(pfmengine.OpenCode, builtinTestSource{id: pfmengine.OpenCode})
}

func TestUnknownEngineIsANamedError(t *testing.T) {
	_, err := SourceFor(pfmengine.ID("zz"))
	if err == nil || err.Error() != "engine zz: no index source registered" {
		t.Fatalf("SourceFor(zz) error = %v", err)
	}
}

// TestRunIndexesPastAFailedSource: a source whose pass fails costs that engine
// only: the engines after it still index, and the error names the failure.
func TestRunIndexesPastAFailedSource(t *testing.T) {
	root := t.TempDir()
	claudeRoot := filepath.Join(root, "claude")
	codexHome := filepath.Join(root, "codex")
	writeLines(t, filepath.Join(claudeRoot, "p", "chat.jsonl"),
		`{"type":"user","cwd":"/work","message":{"content":"hello"}}`)
	writeLines(
		t,
		filepath.Join(codexHome, "sessions", "rollout-2026-09-23T12-07-24-kept.jsonl"),
		`{"type":"session_meta","payload":{"source":"cli","thread_source":"user","cwd":"/work/app"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"indexed"}]}}`,
	)
	t.Setenv(paths.EnvCacheDB, filepath.Join(root, "pfm-cache.db")) // its table is dropped below
	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	raw, err := sqlitedb.OpenReadWrite(database.Path(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("DROP TABLE transcripts"); err != nil { // the Claude pass's own table
		t.Fatal(errors.Join(err, raw.Close()))
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	indexer, err := NewWithRoots(database, paths.Values{}, map[pfmengine.ID][]string{
		pfmengine.Claude: {claudeRoot},
		pfmengine.Codex:  {codexHome},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = indexer.Run(context.Background(), Options{})
	var partial *PartialError
	want := "index engine cc: query transcripts: SQL logic error: no such table: transcripts (1)"
	if !errors.As(err, &partial) || err.Error() != want {
		t.Errorf("Run() error = %v, want the partial failure %q", err, want)
	}
	rollout, found, err := database.Rollout(context.Background(), "kept")
	if err != nil || !found || rollout.FirstPrompt != "indexed" {
		t.Fatalf("Rollout(kept) = %#v found=%t err=%v, want the Codex pass run after the Claude failure",
			rollout, found, err)
	}
}

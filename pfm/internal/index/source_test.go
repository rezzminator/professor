package index

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
	"github.com/rezzminator/professor/pfm/internal/store"
)

const missingSourceHelper = "PFM_INDEX_MISSING_SOURCE_HELPER"

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

// TestRunRefusesAnEngineWithNoIndexSource pins the scan's honesty: an engine
// the registry knows but no index source serves is a wiring failure, never an
// empty index. Run refuses before any source syncs — a half-indexed store would
// answer "no chat named X" for every chat of the unserved engine, reporting a
// failure to look as absence.
func TestRunRefusesAnEngineWithNoIndexSource(t *testing.T) {
	if os.Getenv(missingSourceHelper) != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestRunRefusesAnEngineWithNoIndexSource$")
		command.Env = append(os.Environ(), missingSourceHelper+"=1")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("missing-source proof failed: %v\n%s", err, output)
		}
		return
	}
	id := pfmengine.ID("zz")
	pfmengine.Register(pfmengine.Descriptor{
		ID: id, Name: "Zed", Short: "Zed", LongName: "zed", Binary: "zed",
		SocketPrefix: "zz-", RootEnv: "PFM_ZZ_ROOT",
		DefaultRoots: func(home string) []string { return []string{home} },
	})
	fixture := setupIndexFixture(t)
	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	indexer, err := NewWithRoots(database, paths.Values{}, map[pfmengine.ID][]string{
		pfmengine.Claude: {fixture.claudeRoot},
		pfmengine.Codex:  {fixture.codexHome},
	})
	if err != nil {
		t.Fatal(err)
	}
	counters, err := indexer.Run(context.Background(), Options{})
	_, sourceErr := SourceFor(id)
	if err == nil || sourceErr == nil || !strings.Contains(err.Error(), sourceErr.Error()) {
		t.Fatalf("Run() error = %v, want the registry's %v", err, sourceErr)
	}
	if counters.FilesSeen != 0 || counters.RowsTouched != 0 {
		t.Fatalf("Run() counters = %+v, want the refusal before any source synced", counters)
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

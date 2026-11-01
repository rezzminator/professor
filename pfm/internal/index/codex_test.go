package index

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// No Codex state DB exists here: old exec rollouts must classify themselves.
func setupCodexRolloutSourceFixture(t *testing.T) (*store.Store, *Indexer) {
	t.Helper()
	codexHome := t.TempDir()
	for _, fixture := range []struct {
		id   string
		meta string
	}{
		{"exec", `{"type":"session_meta","payload":{"source":"exec","originator":"codex_exec","thread_source":"user","cwd":"/work/app"}}`},
		{"exec-top-level", `{"type":"session_meta","source":"exec","originator":"codex_exec","thread_source":"user","cwd":"/work/app"}`},
		{"cli", `{"type":"session_meta","payload":{"source":"cli","originator":"codex_exec","thread_source":"user","cwd":"/work/app"}}`},
	} {
		writeLines(
			t,
			filepath.Join(codexHome, "sessions", "rollout-"+fixture.id+".jsonl"),
			fixture.meta,
			`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"review the changes"}]}}`,
		)
	}
	database := openIndexStore(t)
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	indexer, err := NewWithRoots(database, paths.Values{}, map[pfmengine.ID][]string{pfmengine.Codex: {codexHome}})
	if err != nil {
		t.Fatalf("NewWithRoots() error = %v", err)
	}
	return database, indexer
}

func TestCodexExecRolloutsWithoutStateAreBackground(t *testing.T) {
	database, indexer := setupCodexRolloutSourceFixture(t)
	ctx := context.Background()
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, id := range []string{"exec", "exec-top-level", "cli"} {
		rollout, found, err := database.Rollout(ctx, id)
		if err != nil || !found {
			t.Fatalf("Rollout(%q) found=%t err=%v", id, found, err)
		}
		if wantBG := id != "cli"; rollout.IsBG != wantBG {
			t.Errorf("Rollout(%q).IsBG = %t, want %t", id, rollout.IsBG, wantBG)
		}
		if !rollout.UserThread || rollout.PromptCount != 1 {
			t.Errorf("Rollout(%q) = %#v, want a user thread with one prompt", id, rollout)
		}
	}
	if listed := defaultCodexIDs(t, database); !reflect.DeepEqual(listed, []string{"cli"}) {
		t.Errorf("default Codex listing = %v, want [cli]", listed)
	}
}

func TestCodexCachedExecRolloutsReclassifyOnUpgrade(t *testing.T) {
	database, indexer := setupCodexRolloutSourceFixture(t)
	ctx := context.Background()
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("initial Run() error = %v", err)
	}
	stale, found, err := database.Rollout(ctx, "exec")
	if err != nil || !found {
		t.Fatalf("Rollout(exec) found=%t err=%v", found, err)
	}
	// Recreate the previous parser's cached row, including its EOF offset.
	stale.IsBG = false
	if err := database.UpsertRollout(ctx, stale); err != nil {
		t.Fatalf("UpsertRollout() error = %v", err)
	}
	if err := database.SetMeta(ctx, codexParserVersionKey, "4"); err != nil {
		t.Fatalf("set previous parser version: %v", err)
	}
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("upgrade Run() error = %v", err)
	}
	repaired, found, err := database.Rollout(ctx, "exec")
	if err != nil || !found || !repaired.IsBG {
		t.Fatalf("cached exec after upgrade: IsBG=%t found=%t err=%v, want background", repaired.IsBG, found, err)
	}
	if repaired.PromptCount != stale.PromptCount || repaired.ParsedOffset != stale.ParsedOffset {
		t.Fatalf("upgrade changed prompt count/offset: got %d/%d, want %d/%d",
			repaired.PromptCount, repaired.ParsedOffset, stale.PromptCount, stale.ParsedOffset)
	}
	if listed := defaultCodexIDs(t, database); !reflect.DeepEqual(listed, []string{"cli"}) {
		t.Fatalf("default Codex listing after upgrade = %v, want [cli]", listed)
	}
	warm, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("warm Run() error = %v", err)
	}
	if warm.FullParsed != 0 || warm.DeltaParsed != 0 || warm.FilesSkipped != 3 {
		t.Fatalf("warm counters = %+v, want unchanged files skipped", warm)
	}
}

// A rollout Codex compressed is indexed from its decompressed lines, by the id
// its name carries; beside a plain twin the plain file is the row.
func TestACompressedRolloutIsIndexedFromItsLines(t *testing.T) {
	codexHome := t.TempDir()
	day := filepath.Join(codexHome, "sessions", "2026", "09", "23")
	meta := `{"type":"session_meta","payload":{"source":"cli","thread_source":"user","cwd":"/work/app"}}`
	prompt := func(text string) string {
		return `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` +
			text + `"}]}}`
	}
	compress := func(path string) string {
		t.Helper()
		compressed, err := codexmeta.CompressRollout(path)
		if err != nil {
			t.Fatal(err)
		}
		return compressed
	}
	finished := filepath.Join(day, "rollout-2026-09-23T12-07-24-finished.jsonl")
	writeLines(t, finished, meta, prompt("review the changes"))
	compressedPath := compress(finished)
	twin := filepath.Join(day, "rollout-2026-09-23T12-07-24-twin.jsonl")
	writeLines(t, twin, meta, prompt("stale"))
	compress(twin)
	writeLines(t, twin, meta, prompt("current"))

	database := openIndexStore(t)
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	indexer, err := NewWithRoots(database, paths.Values{}, map[pfmengine.ID][]string{pfmengine.Codex: {codexHome}})
	if err != nil {
		t.Fatalf("NewWithRoots() error = %v", err)
	}
	ctx := context.Background()
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, want := range []struct{ id, path, prompt string }{
		{"finished", compressedPath, "review the changes"},
		{"twin", twin, "current"},
	} {
		rollout, found, err := database.Rollout(ctx, want.id)
		if err != nil || !found || rollout.Path != want.path || rollout.FirstPrompt != want.prompt ||
			rollout.CWD != "/work/app" || !rollout.UserThread || rollout.PromptCount != 1 {
			t.Errorf("Rollout(%q) = %#v, found=%t, err=%v; want path %s, first prompt %q", want.id, rollout, found, err,
				want.path, want.prompt)
		}
	}
}

// TestCodexParseCountsLinesItCannotDecode: an undecodable line costs that
// line only; the rollout keeps its other prompts and the parse warns the
// count.
func TestCodexParseCountsLinesItCannotDecode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions", "rollout-2026-09-23T12-07-24-torn.jsonl")
	meta := `{"type":"session_meta","payload":{"source":"cli","thread_source":"user","cwd":"/work/app"}}`
	torn := `{"type":"response_item","payload":{"type":"message","role":"user"`
	kept := `{"type":"response_item","payload":{"type":"message","role":"user",` +
		`"content":[{"type":"input_text","text":"kept"}]}}`
	writeLines(t, path, meta, torn, kept)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, recorder := obs.Test(t)
	file := diskFile{ID: "torn", Path: path, Size: info.Size(), MTimeNS: info.ModTime().UnixNano()}
	rollout, _, err := parseCodexRolloutFile(ctx, file, 0, store.Rollout{})
	if err != nil {
		t.Fatal(err)
	}
	if rollout.FirstPrompt != "kept" || rollout.PromptCount != 1 || rollout.CWD != "/work/app" {
		t.Fatalf("rollout = %#v, want the decodable lines kept", rollout)
	}
	if got := unparsedLineCounts(recorder, path); len(got) != 1 || got[0] != float64(1) {
		t.Fatalf("unparsed line warnings for %s = %v, want [1]", path, got)
	}
}

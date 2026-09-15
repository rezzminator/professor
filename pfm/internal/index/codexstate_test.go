package index

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"hostops/pfm/internal/compose"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/store"
)

type codexStateThread struct {
	ID               string
	RolloutPath      string
	CWD              string
	Title            string
	FirstUserMessage string
	Name             string
	// Source is threads.source, the entry point Codex was started through. It
	// defaults to "cli", the interactive terminal front end.
	Source       string
	ThreadSource string
	HistoryMode  string
	CreatedAt    int64
	UpdatedAt    int64
	TokensUsed   int64
	Archived     bool
}

// buildCodexState writes a scratch Codex state store using the real schema
// captured from a live 0.146.1 store.
func buildCodexState(t *testing.T, path string, threads ...codexStateThread) {
	t.Helper()

	schema, err := os.ReadFile(testdataPath(t, "codex-state-schema.sql"))
	if err != nil {
		t.Fatalf("read Codex state schema: %v", err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("create scratch Codex state store: %v", err)
	}
	defer database.Close()
	if _, err := database.Exec(string(schema)); err != nil {
		t.Fatalf("apply Codex state schema: %v", err)
	}
	for _, thread := range threads {
		historyMode := thread.HistoryMode
		if historyMode == "" {
			historyMode = "legacy"
		}
		archived := 0
		if thread.Archived {
			archived = 1
		}
		var name any
		if thread.Name != "" {
			name = thread.Name
		}
		source := thread.Source
		if source == "" {
			source = "cli"
		}
		if _, err := database.Exec(`
INSERT INTO threads (
  id, rollout_path, created_at, updated_at, source, model_provider, cwd,
  title, sandbox_policy, approval_mode, tokens_used, archived,
  first_user_message, preview, thread_source, recency_at, history_mode, name
) VALUES (?, ?, ?, ?, ?, 'openai', ?, ?, 'workspace-write', 'on-request',
  ?, ?, ?, '', ?, ?, ?, ?)`,
			thread.ID,
			thread.RolloutPath,
			thread.CreatedAt,
			thread.UpdatedAt,
			source,
			thread.CWD,
			thread.Title,
			thread.TokensUsed,
			archived,
			thread.FirstUserMessage,
			thread.ThreadSource,
			thread.UpdatedAt,
			historyMode,
			name,
		); err != nil {
			t.Fatalf("insert scratch Codex thread %q: %v", thread.ID, err)
		}
	}
}

func execCodexState(t *testing.T, path, statement string, args ...any) {
	t.Helper()

	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open scratch Codex state store: %v", err)
	}
	defer database.Close()
	if _, err := database.Exec(statement, args...); err != nil {
		t.Fatalf("update scratch Codex state store: %v", err)
	}
}

type codexStateFixture struct {
	codexRoot       string
	statePath       string
	fileRolloutPath string
}

func setupCodexStateFixture(t *testing.T) codexStateFixture {
	t.Helper()

	root := t.TempDir()
	codexRoot := filepath.Join(root, "codex")
	sessions := filepath.Join(codexRoot, "sessions", "2026", "01", "01")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	fileRollout := filepath.Join(
		sessions,
		"rollout-2026-01-01T00-00-00-file-thread.jsonl",
	)
	writeLines(
		t,
		fileRollout,
		`{"type":"session_meta","payload":{"id":"file-thread","thread_source":"user","cwd":"/work/kept"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"kept first prompt"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"kept second prompt"}]}}`,
	)
	// The rollout file alone looks like a top-level chat; only the state store
	// knows Codex spawned it as a subagent.
	writeLines(
		t,
		filepath.Join(
			sessions,
			"rollout-2026-01-01T00-00-02-killed-subagent.jsonl",
		),
		`{"type":"session_meta","payload":{"id":"killed-subagent","cwd":"/work/kept"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"delegated work"}]}}`,
	)
	writeLines(t, filepath.Join(codexRoot, "session_index.jsonl"),
		`{"id":"file-thread","thread_name":"SESSION INDEX NAME"}`,
		`{"id":"legacy-named","thread_name":"ONLY IN SESSION INDEX"}`,
	)

	statePath := filepath.Join(codexRoot, "state_5.sqlite")
	buildCodexState(t, statePath,
		codexStateThread{
			ID:               "file-thread",
			RolloutPath:      fileRollout,
			CWD:              "/work/kept",
			Title:            "kept first prompt",
			FirstUserMessage: "kept first prompt",
			Name:             "STORE NAME",
			ThreadSource:     "user",
			CreatedAt:        100,
			UpdatedAt:        200,
			TokensUsed:       10,
		},
		codexStateThread{
			ID: "store-only",
			RolloutPath: filepath.Join(
				sessions,
				"rollout-2026-01-01T00-00-01-store-only.jsonl",
			),
			CWD:              "/work/paginated",
			FirstUserMessage: "paginated first prompt",
			Name:             "PAGINATED NAME",
			ThreadSource:     "user",
			HistoryMode:      "paginated",
			CreatedAt:        300,
			UpdatedAt:        400,
			TokensUsed:       99,
		},
		codexStateThread{
			ID: "killed-subagent",
			RolloutPath: filepath.Join(
				sessions,
				"rollout-2026-01-01T00-00-02-killed-subagent.jsonl",
			),
			CWD:          "/work/kept",
			Title:        "delegated work",
			ThreadSource: "subagent",
			CreatedAt:    150,
			UpdatedAt:    150,
		},
	)

	t.Setenv("TMUX_TMPDIR", filepath.Join(root, "t"))
	t.Setenv(paths.EnvDB, filepath.Join(root, "state", "fleet.db"))
	t.Setenv(paths.EnvSIDDir, filepath.Join(root, "sid"))
	t.Setenv(paths.EnvClaudeRoots, filepath.Join(root, "claude"))
	t.Setenv(paths.EnvCodexRoot, codexRoot)
	t.Setenv(paths.EnvTmuxDir, filepath.Join(root, "tmux"))
	t.Setenv(paths.EnvHome, filepath.Join(root, "home"))

	return codexStateFixture{
		codexRoot:       codexRoot,
		statePath:       statePath,
		fileRolloutPath: fileRollout,
	}
}

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		path,
		[]byte(strings.Join(lines, "\n")+"\n"),
		0o600,
	); err != nil {
		t.Fatalf("write fixture %q: %v", path, err)
	}
}

func codexRows(t *testing.T, database *store.Store) []compose.Row {
	t.Helper()

	ctx := context.Background()
	rollouts, err := database.Rollouts(ctx)
	if err != nil {
		t.Fatalf("Rollouts() error = %v", err)
	}
	names, err := database.CxNames(ctx)
	if err != nil {
		t.Fatalf("CxNames() error = %v", err)
	}
	killed, err := database.KilledChats(ctx)
	if err != nil {
		t.Fatalf("KilledChats() error = %v", err)
	}
	output := compose.Compose(compose.Input{
		Rollouts: rollouts,
		CxNames:  names,
		Killed:   killed,
		Options:  compose.Options{View: compose.AllView},
	})
	codex := make([]compose.Row, 0, len(output.Rows))
	for _, row := range output.Rows {
		if row.Kind == compose.ResumeCodex {
			codex = append(codex, row)
		}
	}
	return codex
}

// The Codex state store decides which conversations exist: one that never
// wrote a rollout file is listed exactly once under its store name, one that
// did keeps its file-derived size while the store still names it, and a
// subagent the rollout file cannot self-identify stays out of the list.
func TestCodexStateStoreDrivesListingAndNames(t *testing.T) {
	setupCodexStateFixture(t)
	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	indexer, err := New(database)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()

	counters, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("initial Run() error = %v", err)
	}
	if counters.CodexThreads != 2 || counters.CodexRowsCreated != 1 {
		t.Fatalf(
			"initial counters = %+v, want two listed threads and one store-only row",
			counters,
		)
	}

	storeOnly, found, err := database.Rollout(ctx, "store-only")
	if err != nil || !found {
		t.Fatalf("store-only Rollout() found = %v, error = %v", found, err)
	}
	if storeOnly.Size != 0 ||
		storeOnly.PromptCount != 1 ||
		!storeOnly.UserThread ||
		storeOnly.CWD != "/work/paginated" ||
		storeOnly.LineageRoot != "store-only" ||
		storeOnly.FirstPrompt != "paginated first prompt" ||
		storeOnly.MTimeNS != 400*1_000_000_000 {
		t.Fatalf("store-only row = %#v", storeOnly)
	}
	withFile, found, err := database.Rollout(ctx, "file-thread")
	if err != nil || !found {
		t.Fatalf("file-thread Rollout() found = %v, error = %v", found, err)
	}
	if withFile.Size <= 0 || withFile.PromptCount != 2 || !withFile.UserThread {
		t.Fatalf("file-thread row = %#v, want file-derived size and prompts", withFile)
	}
	subagent, found, err := database.Rollout(ctx, "killed-subagent")
	if err != nil || !found {
		t.Fatalf("killed-subagent Rollout() found = %v, error = %v", found, err)
	}
	if subagent.UserThread {
		t.Fatal("the state store classified killed-subagent as a subagent; it is still listed")
	}

	names, err := database.CxNames(ctx)
	if err != nil {
		t.Fatalf("CxNames() error = %v", err)
	}
	if names["file-thread"] != "STORE NAME" {
		t.Fatalf(
			"file-thread name = %q, want the state store to outrank session_index",
			names["file-thread"],
		)
	}
	if names["store-only"] != "PAGINATED NAME" {
		t.Fatalf("store-only name = %q, want the state store name", names["store-only"])
	}
	if names["legacy-named"] != "ONLY IN SESSION INDEX" {
		t.Fatalf(
			"legacy-named name = %q, want session_index kept as the fallback",
			names["legacy-named"],
		)
	}

	rows := codexRows(t, database)
	if len(rows) != 2 {
		t.Fatalf("composed Codex rows = %#v, want exactly two", rows)
	}
	byID := map[string]compose.Row{rows[0].ID: rows[0], rows[1].ID: rows[1]}
	if row := byID["store-only"]; row.Name != "PAGINATED NAME" ||
		row.Project != "paginated" ||
		row.PromptCount != 1 {
		t.Fatalf("store-only row = %#v", row)
	}
	if row := byID["file-thread"]; row.Name != "STORE NAME" ||
		row.PromptCount != 2 {
		t.Fatalf("file-thread row = %#v", row)
	}

	warm, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("warm Run() error = %v", err)
	}
	if warm.RowsTouched != 0 || warm.Deleted != 0 {
		t.Fatalf("warm counters = %+v, want an idempotent pass", warm)
	}
}

// The 2026-08-11 shape: a thread resumed through the picker and renamed twice
// in Codex 0.147, which writes the name to session_index.jsonl ALONE and
// leaves threads.name empty. The index is append-only, so the LAST entry for
// an id is the current name, and a nameless store row must not blank it.
func TestCodexSessionIndexRenamesKeepTheLastWordOverANamelessStoreRow(t *testing.T) {
	fixture := setupCodexStateFixture(t)
	execCodexState(t, fixture.statePath, `
INSERT INTO threads (
  id, rollout_path, created_at, updated_at, source, model_provider, cwd,
  title, sandbox_policy, approval_mode, tokens_used, archived,
  first_user_message, preview, thread_source, recency_at, history_mode, name
) VALUES ('resumed-thread', '', 500, 600, 'cli', 'openai', '/work/resumed',
  '', 'workspace-write', 'on-request', 7, 0, 'verify the dispatch claim', '',
  'user', 600, 'paginated', NULL)`)
	indexPath := filepath.Join(fixture.codexRoot, "session_index.jsonl")
	appendJSONLine(t, indexPath, map[string]any{
		"id":          "resumed-thread",
		"thread_name": "AWCX",
		"updated_at":  "2026-08-10T23:19:20.801437897Z",
	})
	appendJSONLine(t, indexPath, map[string]any{
		"id":          "resumed-thread",
		"thread_name": "AWD",
		"updated_at":  "2026-08-10T23:27:52.914145697Z",
	})

	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	indexer, err := New(database)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("initial Run() error = %v", err)
	}

	names, err := database.CxNames(ctx)
	if err != nil {
		t.Fatalf("CxNames() error = %v", err)
	}
	if names["resumed-thread"] != "AWD" {
		t.Fatalf(
			"resumed-thread name = %q, want the LAST session_index rename",
			names["resumed-thread"],
		)
	}
	rows := codexRows(t, database)
	byID := make(map[string]compose.Row, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	if row := byID["resumed-thread"]; row.Name != "AWD" {
		t.Fatalf("resumed-thread row = %#v, want the session_index name", row)
	}
}

// THE BUG. file-thread already carries a non-empty, STALE store name ("STORE
// NAME" from setupCodexStateFixture) — the store keeps no rename clock, so a
// non-empty threads.name only proves it is A name, never the CURRENT one. A
// second, DATED session_index.jsonl entry for the same id is the only
// evidence that proves which of the two is newer, and it must win over the
// undated store name.
func TestCodexSessionIndexRenameBeatsStaleStoreName(t *testing.T) {
	fixture := setupCodexStateFixture(t)
	indexPath := filepath.Join(fixture.codexRoot, "session_index.jsonl")
	appendJSONLine(t, indexPath, map[string]any{
		"id":          "file-thread",
		"thread_name": "FRESH RENAME",
		"updated_at":  "2026-08-10T23:27:52.914145697Z",
	})

	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	indexer, err := New(database)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	names, err := database.CxNames(ctx)
	if err != nil {
		t.Fatalf("CxNames() error = %v", err)
	}
	if names["file-thread"] != "FRESH RENAME" {
		t.Fatalf(
			"file-thread name = %q, want the dated session_index rename to beat "+
				"the stale store name %q",
			names["file-thread"],
			"STORE NAME",
		)
	}
	rows := codexRows(t, database)
	byID := make(map[string]compose.Row, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	if row := byID["file-thread"]; row.Name != "FRESH RENAME" {
		t.Fatalf("file-thread row = %#v, want the dated session_index rename", row)
	}
}

// A store name with no session_index entry at all — store-only never wrote
// one, since it has no rollout file to seed session_index.jsonl — still
// wins, and is tagged with its provenance so a later dated rename could
// still outrank it.
func TestCodexStoreNameSurvivesWithNoSessionIndexEntry(t *testing.T) {
	setupCodexStateFixture(t)
	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	indexer, err := New(database)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	records, err := database.CxNameRecords(ctx)
	if err != nil {
		t.Fatalf("CxNameRecords() error = %v", err)
	}
	record, found := records["store-only"]
	if !found ||
		record.ThreadName != "PAGINATED NAME" ||
		record.Source != store.CxNameSourceStore {
		t.Fatalf(
			"store-only record = %#v, found = %t, want the store name tagged %q",
			record,
			found,
			store.CxNameSourceStore,
		)
	}
}

// A rename inside Codex reaches the picker, and archiving a conversation
// there retires the row the store alone was holding up.
func TestCodexStateRenameAndArchivePropagate(t *testing.T) {
	fixture := setupCodexStateFixture(t)
	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	indexer, err := New(database)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("initial Run() error = %v", err)
	}

	execCodexState(
		t,
		fixture.statePath,
		"UPDATE threads SET name = ? WHERE id = ?",
		"RENAMED IN CODEX",
		"store-only",
	)
	renamed, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("rename Run() error = %v", err)
	}
	if renamed.RowsTouched != 1 {
		t.Fatalf("rename counters = %+v, want one name row touched", renamed)
	}
	names, err := database.CxNames(ctx)
	if err != nil {
		t.Fatalf("CxNames() error = %v", err)
	}
	if names["store-only"] != "RENAMED IN CODEX" {
		t.Fatalf("store-only name = %q, want the rename", names["store-only"])
	}

	execCodexState(
		t,
		fixture.statePath,
		"UPDATE threads SET archived = 1 WHERE id = ?",
		"store-only",
	)
	archived, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("archive Run() error = %v", err)
	}
	if archived.Deleted != 1 || archived.CodexThreads != 1 {
		t.Fatalf("archive counters = %+v, want the store-only row retired", archived)
	}
	if _, found, err := database.Rollout(ctx, "store-only"); err != nil || found {
		t.Fatalf("archived Rollout() found = %v, error = %v; want false, nil", found, err)
	}
	rows := codexRows(t, database)
	if len(rows) != 1 || rows[0].ID != "file-thread" {
		t.Fatalf("composed Codex rows after archive = %#v", rows)
	}
}

// The store's row is a placeholder for a file that may still arrive: when the
// rollout file finally appears the parsed file replaces the derived values
// instead of stacking on top of them.
func TestCodexStateRowYieldsToTheRolloutFileWhenItArrives(t *testing.T) {
	setupCodexStateFixture(t)
	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	indexer, err := New(database)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("initial Run() error = %v", err)
	}
	declared, found, err := database.Rollout(ctx, "store-only")
	if err != nil || !found {
		t.Fatalf("store-only Rollout() found = %v, error = %v", found, err)
	}

	writeLines(
		t,
		declared.Path,
		`{"type":"session_meta","payload":{"id":"store-only","thread_source":"user","cwd":"/work/paginated"}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"paginated first prompt"}]}}`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"paginated second prompt"}]}}`,
	)
	arrived, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("post-arrival Run() error = %v", err)
	}
	if arrived.FullParsed != 1 || arrived.DeltaParsed != 0 {
		t.Fatalf("post-arrival counters = %+v, want a full parse of the new file", arrived)
	}
	parsed, found, err := database.Rollout(ctx, "store-only")
	if err != nil || !found {
		t.Fatalf("parsed Rollout() found = %v, error = %v", found, err)
	}
	if parsed.PromptCount != 2 || parsed.Size <= 0 || !parsed.UserThread {
		t.Fatalf("store-only row after its file arrived = %#v", parsed)
	}
	rows := codexRows(t, database)
	if len(rows) != 2 {
		t.Fatalf("composed Codex rows = %#v, want the conversation still listed once", rows)
	}
}

// A conversation the state store still vouches for keeps its row when its
// rollout file is deleted, instead of vanishing with the file.
func TestCodexStateKeepsThreadWhoseRolloutFileIsDeleted(t *testing.T) {
	fixture := setupCodexStateFixture(t)
	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	indexer, err := New(database)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("initial Run() error = %v", err)
	}
	if err := os.Remove(fixture.fileRolloutPath); err != nil {
		t.Fatalf("remove rollout file: %v", err)
	}
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("post-delete Run() error = %v", err)
	}

	kept, found, err := database.Rollout(ctx, "file-thread")
	if err != nil || !found {
		t.Fatalf("file-thread Rollout() found = %v, error = %v; want the store's row", found, err)
	}
	if !kept.UserThread || kept.PromptCount != 2 {
		t.Fatalf("file-thread row after file deletion = %#v", kept)
	}
	rows := codexRows(t, database)
	if len(rows) != 2 {
		t.Fatalf("composed Codex rows = %#v, want both conversations", rows)
	}
}

// setupMachineSpawnedFixture builds a Codex root holding four conversations
// that are IDENTICAL on every column the fleet used to classify by — all four
// are thread_source='user', unarchived, with a rollout file carrying one
// prompt — so nothing but the entry point and the owner's rename can tell them
// apart:
//
//   - verify-twin-a / verify-twin-b: two headless verify twins. Two
//     `codex exec` threads created a second apart with the SAME first prompt,
//     each its own lineage root. Hiding one used to leave the other listed.
//   - agent-worktree: an exec thread whose cwd is a workflow worktree.
//   - real-chat: an interactive terminal chat.
//   - adopted-exec: an exec thread the owner adopted by renaming it, which is
//     the one thing that makes title differ from the first prompt.
func setupMachineSpawnedFixture(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	codexRoot := filepath.Join(root, "codex")
	sessions := filepath.Join(codexRoot, "sessions", "2026", "08", "10")
	const twinPrompt = "Trace one planned API field through directly evidenced repos"

	threads := []codexStateThread{
		{
			ID:               "verify-twin-a",
			CWD:              "/work/cross-workflow",
			Source:           "exec",
			Title:            twinPrompt,
			FirstUserMessage: twinPrompt,
			ThreadSource:     "user",
			CreatedAt:        100,
			UpdatedAt:        100,
		},
		{
			ID:               "verify-twin-b",
			CWD:              "/work/cross-workflow",
			Source:           "exec",
			Title:            twinPrompt,
			FirstUserMessage: twinPrompt,
			ThreadSource:     "user",
			CreatedAt:        101,
			UpdatedAt:        101,
		},
		{
			ID:               "agent-worktree",
			CWD:              "/tmp/cross-workflow-agent-vhxIa3",
			Source:           "exec",
			Title:            "Return the fixed portable-effect plan",
			FirstUserMessage: "Return the fixed portable-effect plan",
			ThreadSource:     "user",
			CreatedAt:        102,
			UpdatedAt:        102,
		},
		{
			ID:               "real-chat",
			CWD:              "/work/proja",
			Source:           "cli",
			Title:            "help me with the picker",
			FirstUserMessage: "help me with the picker",
			ThreadSource:     "user",
			CreatedAt:        103,
			UpdatedAt:        103,
		},
		{
			ID:               "adopted-exec",
			CWD:              "/work/proja",
			Source:           "exec",
			Title:            "AWCX",
			FirstUserMessage: "[verify] you are an INDEPENDENT VERIFIER",
			ThreadSource:     "user",
			CreatedAt:        104,
			UpdatedAt:        104,
		},
	}
	for index := range threads {
		path := filepath.Join(
			sessions,
			fmt.Sprintf(
				"rollout-2026-08-10T00-00-0%d-%s.jsonl",
				index,
				threads[index].ID,
			),
		)
		threads[index].RolloutPath = path
		writeLines(t, path,
			`{"type":"session_meta","payload":{"id":"`+threads[index].ID+
				`","thread_source":"user","cwd":"`+threads[index].CWD+`"}}`,
			`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":`+
				strconv.Quote(threads[index].FirstUserMessage)+`}]}}`,
		)
	}
	buildCodexState(t, filepath.Join(codexRoot, "state_5.sqlite"), threads...)

	t.Setenv("TMUX_TMPDIR", filepath.Join(root, "t"))
	t.Setenv(paths.EnvDB, filepath.Join(root, "state", "fleet.db"))
	t.Setenv(paths.EnvSharedDB, filepath.Join(root, "cc", "fleet.db"))
	t.Setenv(paths.EnvSIDDir, filepath.Join(root, "sid"))
	t.Setenv(paths.EnvClaudeRoots, filepath.Join(root, "claude"))
	t.Setenv(paths.EnvCodexRoot, codexRoot)
	t.Setenv(paths.EnvTmuxDir, filepath.Join(root, "tmux"))
	t.Setenv(paths.EnvHome, filepath.Join(root, "home"))
	return codexRoot
}

// THE TWIN REGRESSION. A workflow's verify twins used to list as two of the
// owner's own Codex chats, so hiding one left its identical sibling sitting in
// the picker and the kill looked like it had "come back". They are background
// work and the entry point says so; the chats around them stay listed.
func TestMachineSpawnedCodexThreadsAreBackground(t *testing.T) {
	setupMachineSpawnedFixture(t)
	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	indexer, err := New(database)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("initial Run() error = %v", err)
	}

	wantBG := map[string]bool{
		"verify-twin-a":  true,
		"verify-twin-b":  true,
		"agent-worktree": true,
		"real-chat":      false,
		"adopted-exec":   false,
	}
	for id, want := range wantBG {
		rollout, found, err := database.Rollout(ctx, id)
		if err != nil || !found {
			t.Fatalf("Rollout(%q) found = %v, error = %v", id, found, err)
		}
		if rollout.IsBG != want {
			t.Fatalf("Rollout(%q).IsBG = %t, want %t", id, rollout.IsBG, want)
		}
		if !rollout.UserThread || rollout.Size <= 0 || rollout.PromptCount <= 0 {
			t.Fatalf(
				"Rollout(%q) = %#v; the fixture must differ ONLY in is_bg",
				id,
				rollout,
			)
		}
	}

	listed := defaultCodexIDs(t, database)
	if !reflect.DeepEqual(listed, []string{"adopted-exec", "real-chat"}) {
		t.Fatalf(
			"default Codex listing = %v, want only the owner's two chats",
			listed,
		)
	}
	if all := len(codexRows(t, database)); all != 5 {
		t.Fatalf("all-view Codex rows = %d, want all five kept and merely suppressed", all)
	}
}

// A row indexed before the classification existed carries is_bg=0 while the
// state store says otherwise. The next ORDINARY pass repairs it: the reconcile
// re-derives is_bg from the store on every run, so no parser-version bump and
// no full reparse is needed to clear the twins out.
func TestCodexBackgroundRepairsWithoutAFullReparse(t *testing.T) {
	setupMachineSpawnedFixture(t)
	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	indexer, err := New(database)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("initial Run() error = %v", err)
	}

	stale, found, err := database.Rollout(ctx, "verify-twin-a")
	if err != nil || !found {
		t.Fatalf("verify-twin-a Rollout() found = %v, error = %v", found, err)
	}
	stale.IsBG = false
	if err := database.UpsertRollout(ctx, stale); err != nil {
		t.Fatalf("UpsertRollout() error = %v", err)
	}

	counters, err := indexer.Run(ctx, Options{})
	if err != nil {
		t.Fatalf("repair Run() error = %v", err)
	}
	if counters.FullParsed != 0 || counters.DeltaParsed != 0 {
		t.Fatalf("repair counters = %+v, want no file reparsed", counters)
	}
	repaired, found, err := database.Rollout(ctx, "verify-twin-a")
	if err != nil || !found || !repaired.IsBG {
		t.Fatalf("repaired row = %#v found=%t err=%v, want is_bg set", repaired, found, err)
	}
}

// setupPaginatedContentFixture builds a Codex root with three conversations
// that are IDENTICAL in shape on disk — a header-only rollout file, a single
// session_meta line and NO user_message events, exactly what Codex >=0.146.1
// leaves behind for a paginated thread whose content lives in its own sqlite
// state store instead of the file — and differ only in what the state store
// says about them:
//
//   - real-paginated: an interactive chat the store has real content
//     evidence for (tokens_used=500). The file alone parses to prompt_count=0.
//   - exec-paginated: the SAME evidence, but source='exec' and never
//     renamed — background work, not a chat.
//   - empty-spawn: no content evidence at all (tokens_used=0, no first user
//     message, no title) — a genuinely empty spawn, not merely a paginated
//     one whose content the file cannot show.
func setupPaginatedContentFixture(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	codexRoot := filepath.Join(root, "codex")
	sessions := filepath.Join(codexRoot, "sessions", "2026", "08", "12")

	threads := []codexStateThread{
		{
			ID:           "real-paginated",
			CWD:          "/work/paginated",
			Source:       "cli",
			ThreadSource: "user",
			TokensUsed:   500,
			CreatedAt:    100,
			UpdatedAt:    100,
		},
		{
			ID:           "exec-paginated",
			CWD:          "/work/paginated",
			Source:       "exec",
			ThreadSource: "user",
			TokensUsed:   500,
			CreatedAt:    101,
			UpdatedAt:    101,
		},
		{
			ID:           "empty-spawn",
			CWD:          "/work/paginated",
			Source:       "cli",
			ThreadSource: "user",
			CreatedAt:    102,
			UpdatedAt:    102,
		},
	}
	for index := range threads {
		path := filepath.Join(
			sessions,
			fmt.Sprintf(
				"rollout-2026-08-12T00-00-0%d-%s.jsonl",
				index,
				threads[index].ID,
			),
		)
		threads[index].RolloutPath = path
		writeLines(t, path,
			`{"type":"session_meta","payload":{"id":"`+threads[index].ID+
				`","thread_source":"user","cwd":"`+threads[index].CWD+`"}}`,
		)
	}
	buildCodexState(t, filepath.Join(codexRoot, "state_5.sqlite"), threads...)

	t.Setenv("TMUX_TMPDIR", filepath.Join(root, "t"))
	t.Setenv(paths.EnvDB, filepath.Join(root, "state", "fleet.db"))
	t.Setenv(paths.EnvSharedDB, filepath.Join(root, "cc", "fleet.db"))
	t.Setenv(paths.EnvSIDDir, filepath.Join(root, "sid"))
	t.Setenv(paths.EnvClaudeRoots, filepath.Join(root, "claude"))
	t.Setenv(paths.EnvCodexRoot, codexRoot)
	t.Setenv(paths.EnvTmuxDir, filepath.Join(root, "tmux"))
	t.Setenv(paths.EnvHome, filepath.Join(root, "home"))
	return codexRoot
}

// THE BUG. A header-only rollout file has nonzero Size once parsed — bytes on
// disk, zero prompts among them — so applyCodexThread's old `if rollout.Size
// == 0` gate skipped it entirely: the state store's own content evidence
// never reached the row, and prompt_count stayed at the file's own zero
// forever, right alongside compose's emptiness test that then suppressed it
// from the default listing (497 such rows on the live fleet, 19 of them
// carrying a name and alive in tmux).
func TestHeaderOnlyRolloutIsEnrichedFromTheStateStore(t *testing.T) {
	setupPaginatedContentFixture(t)
	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	indexer, err := New(database)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	real, found, err := database.Rollout(ctx, "real-paginated")
	if err != nil || !found {
		t.Fatalf("real-paginated Rollout() found = %v, error = %v", found, err)
	}
	if real.Size <= 0 {
		t.Fatalf(
			"real-paginated row = %#v, want a header-only file's nonzero size",
			real,
		)
	}
	if real.PromptCount == 0 {
		t.Fatalf(
			"real-paginated row = %#v, want the state store's content evidence "+
				"(tokens_used=500) to fill prompt_count",
			real,
		)
	}

	listed := defaultCodexIDs(t, database)
	found = false
	for _, id := range listed {
		if id == "real-paginated" {
			found = true
		}
	}
	if !found {
		t.Fatalf("default Codex listing = %v, want real-paginated listed", listed)
	}
}

// A machine-spawned thread carrying the SAME content evidence must stay
// suppressed by is_bg, whatever prompt_count the state store fills in — the
// enrichment fix must never smuggle background work into the owner's chats.
func TestHeaderOnlyMachineSpawnedStaysSuppressed(t *testing.T) {
	setupPaginatedContentFixture(t)
	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	indexer, err := New(database)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	execRow, found, err := database.Rollout(ctx, "exec-paginated")
	if err != nil || !found {
		t.Fatalf("exec-paginated Rollout() found = %v, error = %v", found, err)
	}
	if !execRow.IsBG {
		t.Fatalf("exec-paginated row = %#v, want is_bg", execRow)
	}

	listed := defaultCodexIDs(t, database)
	for _, id := range listed {
		if id == "exec-paginated" {
			t.Fatalf(
				"default Codex listing = %v, want exec-paginated suppressed",
				listed,
			)
		}
	}
	if all := len(codexRows(t, database)); all != 3 {
		t.Fatalf(
			"all-view Codex rows = %d, want all three kept and merely suppressed",
			all,
		)
	}
}

// A genuinely empty spawn — no content evidence anywhere in the state store —
// must stay suppressed: the enrichment fix must never manufacture a prompt
// out of nothing.
func TestGenuinelyEmptySpawnStaysSuppressed(t *testing.T) {
	setupPaginatedContentFixture(t)
	database := openIndexStore(t)
	t.Cleanup(func() { _ = database.Close() })
	indexer, err := New(database)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx := context.Background()
	if _, err := indexer.Run(ctx, Options{}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	empty, found, err := database.Rollout(ctx, "empty-spawn")
	if err != nil || !found {
		t.Fatalf("empty-spawn Rollout() found = %v, error = %v", found, err)
	}
	if empty.PromptCount != 0 {
		t.Fatalf("empty-spawn row = %#v, want prompt_count to stay 0", empty)
	}

	listed := defaultCodexIDs(t, database)
	for _, id := range listed {
		if id == "empty-spawn" {
			t.Fatalf(
				"default Codex listing = %v, want empty-spawn suppressed",
				listed,
			)
		}
	}
}

// defaultCodexIDs answers the visibility question twice — through the cached
// SQL candidate query and through compose — and fails if they disagree, which
// is how a chat comes back on the rescan after the cached first frame dropped
// it.
func defaultCodexIDs(t *testing.T, database *store.Store) []string {
	t.Helper()

	ctx := context.Background()
	_, candidates, _, err := database.DefaultCandidates(ctx, 50, 50)
	if err != nil {
		t.Fatalf("DefaultCandidates() error = %v", err)
	}
	cached := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		cached = append(cached, candidate.LineageRoot)
	}
	sort.Strings(cached)

	rollouts, err := database.Rollouts(ctx)
	if err != nil {
		t.Fatalf("Rollouts() error = %v", err)
	}
	output := compose.Compose(compose.Input{
		Rollouts: rollouts,
		Options:  compose.Options{View: compose.DefaultView},
	})
	composed := make([]string, 0, len(output.Rows))
	for _, row := range output.Rows {
		if row.Kind == compose.ResumeCodex {
			composed = append(composed, row.ID)
		}
	}
	sort.Strings(composed)
	if !reflect.DeepEqual(cached, composed) {
		t.Fatalf(
			"cached candidates %v and compose %v disagree about the default listing",
			cached,
			composed,
		)
	}
	return composed
}

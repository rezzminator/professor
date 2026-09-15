package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type codexStateThread struct {
	ID               string
	RolloutPath      string
	CWD              string
	Title            string
	FirstUserMessage string
	Preview          string
	Name             string
	ThreadSource     string
	HistoryMode      string
	CreatedAt        int64
	UpdatedAt        int64
	RecencyAt        int64
	TokensUsed       int64
	Archived         bool
}

// buildCodexState writes a scratch Codex state store using the real schema
// captured from a live 0.146.1 store.
func buildCodexState(t *testing.T, path string, threads ...codexStateThread) {
	t.Helper()

	schema, err := os.ReadFile(
		filepath.Join("..", "..", "testdata", "codex-state-schema.sql"),
	)
	if err != nil {
		t.Fatalf("read Codex state schema: %v", err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("create scratch Codex state store: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, string(schema)); err != nil {
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
		if _, err := database.ExecContext(ctx, `
INSERT INTO threads (
  id, rollout_path, created_at, updated_at, source, model_provider, cwd,
  title, sandbox_policy, approval_mode, tokens_used, archived,
  first_user_message, preview, thread_source, recency_at, history_mode, name
) VALUES (?, ?, ?, ?, 'cli', 'openai', ?, ?, 'workspace-write', 'on-request',
  ?, ?, ?, ?, ?, ?, ?, ?)`,
			thread.ID,
			thread.RolloutPath,
			thread.CreatedAt,
			thread.UpdatedAt,
			thread.CWD,
			thread.Title,
			thread.TokensUsed,
			archived,
			thread.FirstUserMessage,
			thread.Preview,
			nullableText(thread.ThreadSource),
			thread.RecencyAt,
			historyMode,
			nullableText(thread.Name),
		); err != nil {
			t.Fatalf("insert scratch Codex thread %q: %v", thread.ID, err)
		}
	}
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func TestCodexStateFilesOrderNewestGenerationFirst(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{
		"state_2.sqlite",
		"state_10.sqlite",
		"state_9.sqlite",
		"logs_2.sqlite",
		"state_9.sqlite-wal",
		"state_x.sqlite",
	} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := CodexStateFiles(root)
	if err != nil {
		t.Fatalf("CodexStateFiles() error = %v", err)
	}
	want := []string{
		filepath.Join(root, "state_10.sqlite"),
		filepath.Join(root, "state_9.sqlite"),
		filepath.Join(root, "state_2.sqlite"),
	}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("CodexStateFiles() = %#v, want %#v", files, want)
	}

	missing, err := CodexStateFiles(filepath.Join(root, "absent"))
	if err != nil || len(missing) != 0 {
		t.Fatalf("CodexStateFiles(absent) = %#v, %v, want none", missing, err)
	}
}

func TestReadCodexThreadsHighestGenerationWinsAndClassifies(t *testing.T) {
	root := t.TempDir()
	buildCodexState(t, filepath.Join(root, "state_4.sqlite"),
		codexStateThread{
			ID:               "shared-thread",
			RolloutPath:      "/codex/sessions/rollout-old.jsonl",
			CWD:              "/work/old",
			Name:             "STALE NAME",
			FirstUserMessage: "stale first message",
			ThreadSource:     "user",
			CreatedAt:        100,
			UpdatedAt:        100,
		},
		codexStateThread{
			ID:           "only-in-old-store",
			RolloutPath:  "/codex/sessions/rollout-only-old.jsonl",
			CWD:          "/work/old",
			Title:        "kept from the retired generation",
			ThreadSource: "user",
			CreatedAt:    90,
			UpdatedAt:    90,
		},
	)
	buildCodexState(t, filepath.Join(root, "state_5.sqlite"),
		codexStateThread{
			ID:               "shared-thread",
			RolloutPath:      "/codex/sessions/rollout-new.jsonl",
			CWD:              "/work/new",
			Name:             "LIVE NAME",
			FirstUserMessage: "live first message",
			ThreadSource:     "user",
			CreatedAt:        100,
			UpdatedAt:        700,
			RecencyAt:        900,
			TokensUsed:       42,
		},
		codexStateThread{
			ID:           "subagent-thread",
			RolloutPath:  "/codex/sessions/rollout-sub.jsonl",
			CWD:          "/work/new",
			Title:        "delegated",
			ThreadSource: "subagent",
			CreatedAt:    200,
			UpdatedAt:    200,
		},
		codexStateThread{
			ID:           "archived-thread",
			RolloutPath:  "/codex/sessions/rollout-archived.jsonl",
			CWD:          "/work/new",
			Title:        "put away",
			ThreadSource: "user",
			Archived:     true,
			CreatedAt:    300,
			UpdatedAt:    300,
		},
		codexStateThread{
			ID:           "empty-thread",
			RolloutPath:  "/codex/sessions/rollout-empty.jsonl",
			CWD:          "/work/new",
			Name:         "NAMED BUT UNUSED",
			ThreadSource: "user",
			HistoryMode:  "paginated",
			CreatedAt:    400,
			UpdatedAt:    400,
		},
	)

	files, err := CodexStateFiles(root)
	if err != nil {
		t.Fatalf("CodexStateFiles() error = %v", err)
	}
	threads, err := ReadCodexThreads(context.Background(), files)
	if err != nil {
		t.Fatalf("ReadCodexThreads() error = %v", err)
	}
	byID := make(map[string]CodexThread, len(threads))
	for _, thread := range threads {
		byID[thread.ID] = thread
	}
	if len(threads) != 5 {
		t.Fatalf("ReadCodexThreads() = %#v, want five rows", threads)
	}

	shared := byID["shared-thread"]
	if shared.Name != "LIVE NAME" ||
		shared.CWD != "/work/new" ||
		shared.RolloutPath != "/codex/sessions/rollout-new.jsonl" ||
		shared.FirstPrompt != "live first message" ||
		shared.ActivityAt != 900 ||
		!shared.Listed() ||
		!shared.Prompted ||
		filepath.Base(shared.StateFile) != "state_5.sqlite" {
		t.Fatalf("shared thread = %#v, want the state_5 row", shared)
	}
	if retired := byID["only-in-old-store"]; !retired.Listed() ||
		retired.FirstPrompt != "kept from the retired generation" {
		t.Fatalf("older-generation-only thread = %#v, want kept and listed", retired)
	}
	if subagent := byID["subagent-thread"]; subagent.Listed() ||
		subagent.UserThread {
		t.Fatalf("subagent thread = %#v, want unlisted", subagent)
	}
	if archived := byID["archived-thread"]; archived.Listed() ||
		!archived.Archived {
		t.Fatalf("archived thread = %#v, want unlisted", archived)
	}
	empty := byID["empty-thread"]
	if !empty.Listed() || empty.Prompted || empty.FirstPrompt != "" {
		t.Fatalf("empty thread = %#v, want listed with nothing derivable", empty)
	}
}

func TestReadCodexThreadsSkipsUnusableGenerations(t *testing.T) {
	root := t.TempDir()
	ancient, err := sql.Open("sqlite", filepath.Join(root, "state_1.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ancient.Exec(`
CREATE TABLE threads (id TEXT PRIMARY KEY, cwd TEXT, created_at INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := ancient.Exec(
		"INSERT INTO threads (id, cwd, created_at) VALUES ('ancient', '/work', 1)",
	); err != nil {
		t.Fatal(err)
	}
	if err := ancient.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "state_2.sqlite"),
		[]byte("not a database"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	buildCodexState(t, filepath.Join(root, "state_3.sqlite"), codexStateThread{
		ID:           "live",
		RolloutPath:  "/codex/sessions/rollout-live.jsonl",
		CWD:          "/work",
		Title:        "still readable",
		ThreadSource: "user",
		CreatedAt:    5,
		UpdatedAt:    5,
	})

	files, err := CodexStateFiles(root)
	if err != nil {
		t.Fatalf("CodexStateFiles() error = %v", err)
	}
	threads, err := ReadCodexThreads(context.Background(), files)
	if err != nil {
		t.Fatalf("ReadCodexThreads() error = %v", err)
	}
	if len(threads) != 1 || threads[0].ID != "live" {
		t.Fatalf("ReadCodexThreads() = %#v, want only the readable generation", threads)
	}
}

// T1 — the store exposes threads.title, trimmed and untouched otherwise: it
// is a separate channel from Name (an entirely separate rename mechanism)
// and from FirstPrompt, which is what a name-only status line resolves
// against once a status-line title move (T2/T3) can act on it at all.
func TestReadCodexThreadsExposesTitleTrimmed(t *testing.T) {
	root := t.TempDir()
	buildCodexState(t, filepath.Join(root, "state_1.sqlite"),
		codexStateThread{
			ID:               "titled",
			RolloutPath:      "/codex/sessions/rollout-titled.jsonl",
			CWD:              "/work/titled",
			Title:            "  Reply with SECOND  ",
			FirstUserMessage: "first",
			ThreadSource:     "user",
			CreatedAt:        10,
			UpdatedAt:        10,
		},
		codexStateThread{
			ID:               "untitled",
			RolloutPath:      "/codex/sessions/rollout-untitled.jsonl",
			CWD:              "/work/untitled",
			FirstUserMessage: "first",
			ThreadSource:     "user",
			CreatedAt:        20,
			UpdatedAt:        20,
		},
	)

	files, err := CodexStateFiles(root)
	if err != nil {
		t.Fatalf("CodexStateFiles() error = %v", err)
	}
	threads, err := ReadCodexThreads(context.Background(), files)
	if err != nil {
		t.Fatalf("ReadCodexThreads() error = %v", err)
	}
	byID := make(map[string]CodexThread, len(threads))
	for _, thread := range threads {
		byID[thread.ID] = thread
	}
	if titled := byID["titled"]; titled.Title != "Reply with SECOND" {
		t.Fatalf("titled thread Title = %q, want the trimmed %q", titled.Title, "Reply with SECOND")
	}
	if untitled := byID["untitled"]; untitled.Title != "" {
		t.Fatalf("untitled thread Title = %q, want empty", untitled.Title)
	}
}

// A store-only conversation is killed by its lineage root like any other, and
// the kill is permanent: new prompts and a rollout file that finally appears
// never lift it, and nothing writes a baseline to lift it with.
func TestKillStoreOnlyCodexLineageIsPermanent(t *testing.T) {
	setStoreTestJail(t)
	database := openTestStore(t)
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()

	storeOnly := Rollout{
		ID:          "store-only",
		Path:        "/codex/sessions/rollout-store-only.jsonl",
		MTimeNS:     900 * 1_000_000_000,
		CWD:         "/work/paginated",
		UserThread:  true,
		LineageRoot: "store-only",
		FirstPrompt: "paginated conversation",
		PromptCount: 1,
	}
	neighbor := Rollout{
		ID:          "with-file",
		Path:        "/codex/sessions/rollout-with-file.jsonl",
		Size:        4096,
		MTimeNS:     800 * 1_000_000_000,
		CWD:         "/work/paginated",
		UserThread:  true,
		LineageRoot: "with-file",
		FirstPrompt: "ordinary conversation",
		PromptCount: 2,
	}
	for _, rollout := range []Rollout{storeOnly, neighbor} {
		if err := database.UpsertRollout(ctx, rollout); err != nil {
			t.Fatalf("UpsertRollout(%q) error = %v", rollout.ID, err)
		}
	}
	if err := database.Kill(ctx, Killed{
		ID:       storeOnly.ID,
		Engine:   "cx",
		KilledAt: 1,
	}); err != nil {
		t.Fatalf("Kill() error = %v", err)
	}

	assertStoreOnlyKilled := func(stage string) {
		t.Helper()
		_, rollouts, counts, err := database.DefaultCandidates(ctx, 30, 15)
		if err != nil {
			t.Fatalf("%s DefaultCandidates() error = %v", stage, err)
		}
		for _, rollout := range rollouts {
			if rollout.ID == storeOnly.ID {
				t.Fatalf("%s: killed store-only lineage reappeared", stage)
			}
		}
		if counts.Killed != 1 {
			t.Fatalf("%s: killed count = %d, want 1", stage, counts.Killed)
		}
		killed, found, err := database.Killed(ctx, storeOnly.ID)
		if err != nil || !found {
			t.Fatalf("%s Killed() found = %v, error = %v", stage, found, err)
		}
		if killed.BaselinePrompts != nil {
			t.Fatalf(
				"%s: kill carries baseline %d; kills are permanent and write none",
				stage,
				*killed.BaselinePrompts,
			)
		}
	}
	assertStoreOnlyKilled("after kill")

	// The conversation gains a prompt and finally writes its rollout file.
	grown := storeOnly
	grown.PromptCount = 9
	grown.Size = 2048
	grown.MTimeNS = 1000 * 1_000_000_000
	if err := database.UpsertRollout(ctx, grown); err != nil {
		t.Fatalf("UpsertRollout(grown) error = %v", err)
	}
	assertStoreOnlyKilled("after a new prompt")
}

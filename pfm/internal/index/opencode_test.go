package index

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// seedOpenCodeStore builds the OpenCode v1.14.30 store shape from its published
// Drizzle schema. Session usage lived only in assistant-message JSON in that
// release; later OpenCode versions added denormalized session summary columns.
func seedOpenCodeStore(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir opencode root: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "opencode.db"))
	if err != nil {
		t.Fatalf("open fixture store: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	}()
	script := `
CREATE TABLE project (
  id TEXT PRIMARY KEY,
  worktree TEXT NOT NULL,
  vcs TEXT,
  name TEXT,
  icon_url TEXT,
  icon_url_override TEXT,
  icon_color TEXT,
  time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL,
  time_initialized INTEGER,
  sandboxes TEXT NOT NULL,
  commands TEXT
);
CREATE TABLE session (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  parent_id TEXT,
  slug TEXT NOT NULL,
  directory TEXT NOT NULL,
  title TEXT NOT NULL,
  version TEXT NOT NULL,
  share_url TEXT,
  summary_additions INTEGER,
  summary_deletions INTEGER,
  summary_files INTEGER,
  summary_diffs TEXT,
  revert TEXT,
  permission TEXT,
  time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL,
  time_compacting INTEGER,
  time_archived INTEGER,
  workspace_id TEXT,
  path TEXT
);
CREATE TABLE message (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL,
  data TEXT NOT NULL
);
CREATE TABLE part (
  id TEXT PRIMARY KEY,
  message_id TEXT NOT NULL,
  session_id TEXT NOT NULL,
  time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL,
  data TEXT NOT NULL
);
CREATE INDEX message_session_time_created_id_idx ON message (session_id, time_created, id);
CREATE INDEX part_message_id_id_idx ON part (message_id, id);
CREATE INDEX part_session_idx ON part (session_id);`
	if _, err := db.Exec(script); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO project (id, worktree, time_created, time_updated, sandboxes) VALUES ('p1', '/work/nuts', 1, 1, '[]')",
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	sessions := []struct {
		id     string
		parent any
		title  string
		arch   any
	}{
		{"ses_root", nil, "Ramsey attack", nil},
		{"ses_child", "ses_root", "subagent", nil},
		{"ses_gone", nil, "old", 500},
	}
	for _, session := range sessions {
		if _, err := db.Exec(
			"INSERT INTO session (id, project_id, parent_id, slug, directory, title, version, time_created, time_updated, time_archived) VALUES (?, 'p1', ?, ?, '/work/nuts/03-ramsey', ?, '1.14.30', 10, 20, ?)",
			session.id,
			session.parent,
			session.id,
			session.title,
			session.arch,
		); err != nil {
			t.Fatalf("seed session %s: %v", session.id, err)
		}
	}
	prompts := [][8]any{
		{"m1", "i1", "ses_root", "prove the bound", 100, "build", "prov", "m1"},
		{"m2", "i2", "ses_root", "now generalize", 200, "plan", "openai", "m2"},
		{"m3", "i3", "ses_child", "child probe", 150, "explore", "anthropic", "claude"},
	}
	for i := range prompts {
		p := &prompts[i]
		if _, err := db.Exec(
			`INSERT INTO message (id, session_id, time_created, time_updated, data)
			 VALUES (?, ?, ?, ?, json_object(
			   'role','user', 'agent',?,
			   'model',json_object('providerID',?,'modelID',?)
			 ))`,
			p[0], p[2], p[4], p[4], p[5], p[6], p[7],
		); err != nil {
			t.Fatalf("seed message %v: %v", p[0], err)
		}
		if _, err := db.Exec(
			`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data)
			 VALUES (?, ?, ?, ?, ?, json_object('type','text','text',?))`,
			p[1], p[0], p[2], p[4], p[4], p[3],
		); err != nil {
			t.Fatalf("seed part %v: %v", p[1], err)
		}
	}
	assistants := [][6]any{
		{"a1", "ses_root", 110, 100, 25, 1.234567},
		{"a2", "ses_root", 210, 50, 10, 0.265433},
	}
	for _, assistant := range assistants {
		if _, err := db.Exec(
			`INSERT INTO message (id, session_id, time_created, time_updated, data)
			 VALUES (?, ?, ?, ?, json_object(
			   'role','assistant', 'agent','build',
			   'providerID','prov', 'modelID','m1',
			   'tokens',json_object('input',?,'output',?),
			   'cost',?
			 ))`,
			assistant[0], assistant[1], assistant[2], assistant[2],
			assistant[3], assistant[4], assistant[5],
		); err != nil {
			t.Fatalf("seed assistant %v: %v", assistant[0], err)
		}
	}
}

func TestReadOpenCodeSessionsReadsTheFixtureStore(t *testing.T) {
	root := t.TempDir()
	seedOpenCodeStore(t, root)

	sessions, err := ReadOpenCodeSessions(context.Background(), root)
	if err != nil {
		t.Fatalf("read sessions: %v", err)
	}
	byID := make(map[string]int, len(sessions))
	for index, session := range sessions {
		byID[session.ID] = index
	}
	rootSession, found := byID["ses_root"]
	if !found {
		t.Fatalf("root session missing from %#v", sessions)
	}
	got := sessions[rootSession]
	want := struct {
		title          string
		projectDir     string
		agent          string
		model          string
		firstPrompt    string
		promptCount    int64
		assistantCount int64
		tokensInput    int64
		tokensOutput   int64
		costMilli      int64
	}{"Ramsey attack", "/work/nuts", "plan", "openai/m2", "prove the bound", 2, 2, 150, 35, 150000}
	switch {
	case got.Title != want.title:
		t.Errorf("title = %q, want %q", got.Title, want.title)
	case got.ProjectDir != want.projectDir:
		t.Errorf("project dir = %q, want %q", got.ProjectDir, want.projectDir)
	case got.Agent != want.agent:
		t.Errorf("agent = %q, want %q", got.Agent, want.agent)
	case got.Model != want.model:
		t.Errorf("model = %q, want %q", got.Model, want.model)
	case got.FirstPrompt != want.firstPrompt:
		t.Errorf("first prompt = %q, want %q", got.FirstPrompt, want.firstPrompt)
	case got.PromptCount != want.promptCount:
		t.Errorf("prompt count = %d, want %d", got.PromptCount, want.promptCount)
	case got.AssistantCount != want.assistantCount:
		t.Errorf("assistant count = %d, want %d", got.AssistantCount, want.assistantCount)
	case got.TokensInput != want.tokensInput:
		t.Errorf("tokens input = %d, want %d", got.TokensInput, want.tokensInput)
	case got.TokensOutput != want.tokensOutput:
		t.Errorf("tokens output = %d, want %d", got.TokensOutput, want.tokensOutput)
	case got.CostMillicents != want.costMilli:
		t.Errorf("cost millicents = %d, want %d", got.CostMillicents, want.costMilli)
	}
	// ses_child has one user prompt (m3) and zero assistant messages — a
	// session opened and never answered, the exact shape defaultEligible
	// must treat as empty.
	child, found := byID["ses_child"]
	if !found || sessions[child].ParentID != "ses_root" {
		t.Errorf("child session lost or unparented: %#v", sessions)
	} else if sessions[child].PromptCount != 1 || sessions[child].AssistantCount != 0 {
		t.Errorf("unanswered child session = %#v, want prompt_count=1 assistant_count=0", sessions[child])
	}
	empty, found := byID["ses_gone"]
	if !found {
		t.Fatalf("empty session missing from %#v", sessions)
	}
	if got := sessions[empty]; got.Agent != "" || got.Model != "" || got.PromptCount != 0 ||
		got.TokensInput != 0 || got.TokensOutput != 0 || got.CostMillicents != 0 {
		t.Errorf("empty session acquired message-derived values: %#v", got)
	}
}

func TestReadOpenCodeSessionsCompactsNativeModelID(t *testing.T) {
	root := t.TempDir()
	seedOpenCodeStore(t, root)

	db, err := sql.Open("sqlite", filepath.Join(root, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
UPDATE message
   SET data = json_object(
       'role','user', 'agent','build',
       'model',json_object('providerID','openai','modelID','gpt-5.6-sol')
   )
	 WHERE id = 'm2'`); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatalf("update model fixture: %v; close database: %v", err, closeErr)
		}
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	sessions, err := ReadOpenCodeSessions(context.Background(), root)
	if err != nil {
		t.Fatalf("read sessions: %v", err)
	}
	for _, session := range sessions {
		if session.ID == "ses_root" {
			if session.Model != "openai/gpt-5.6-sol" {
				t.Fatalf("model = %q, want native provider/modelID", session.Model)
			}
			if session.Agent != "build" {
				t.Fatalf("agent = %q, want latest user-message agent", session.Agent)
			}
			return
		}
	}
	t.Fatal("native model session missing")
}

func TestReadOpenCodeSessionsMissingStoreIsQuietlyEmpty(t *testing.T) {
	sessions, err := ReadOpenCodeSessions(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("missing store must not error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions = %#v, want empty", sessions)
	}
}

// mutateOpenCodeStore runs statement against root's fixture store.
func mutateOpenCodeStore(t *testing.T, root, statement string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(statement); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatalf("mutate fixture: %v; close database: %v", err, closeErr)
		}
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestReadOpenCodeSessionsKeepsSessionsPastMalformedRows: a malformed message
// or part row costs that row only. Every session stays mirrored, the reader
// warns naming the session and the kind (never the stored content), and the
// doctor probe still reports the store unhealthy.
func TestReadOpenCodeSessionsKeepsSessionsPastMalformedRows(t *testing.T) {
	tests := []struct {
		name      string
		statement string
		wantIDs   []string
		wantWarn  string
	}{
		{
			name:      "message JSON",
			statement: `UPDATE message SET data = 'not-json' WHERE id = 'm1'`,
			wantIDs:   []string{"ses_child", "ses_gone", "ses_root"},
			wantWarn:  "opencode session ses_root kept with malformed rows (message JSON 1)",
		},
		{
			name: "message shape",
			statement: `UPDATE message SET data = json_object(
			  'role','user', 'agent','build',
			  'model',json_object('providerID',7,'modelID','m1')
			) WHERE id = 'm1'`,
			wantIDs:  []string{"ses_child", "ses_gone", "ses_root"},
			wantWarn: "opencode session ses_root kept with malformed rows (message shape 1)",
		},
		{
			name:      "assistant shape",
			statement: `UPDATE message SET data = json_remove(data, '$.agent') WHERE id = 'a1'`,
			wantIDs:   []string{"ses_child", "ses_gone", "ses_root"},
			wantWarn:  "opencode session ses_root kept with malformed rows (message shape 1)",
		},
		{
			name:      "part JSON",
			statement: `UPDATE part SET data = 'not-json' WHERE id = 'i3'`,
			wantIDs:   []string{"ses_child", "ses_gone", "ses_root"},
			wantWarn:  "opencode session ses_child kept with malformed rows (part JSON 1)",
		},
		{
			name:      "part shape",
			statement: `UPDATE part SET data = json_object('type','text','text',7) WHERE id = 'i1'`,
			wantIDs:   []string{"ses_child", "ses_gone", "ses_root"},
			wantWarn:  "opencode session ses_root kept with malformed rows (part shape 1)",
		},
		{
			name: "rows of no session",
			statement: `DELETE FROM session;
UPDATE message SET data = 'not-json' WHERE id = 'm1';
UPDATE part SET data = 'not-json' WHERE id = 'i1';`,
			wantWarn: "opencode rows of no session hold malformed rows (message JSON 1, part JSON 1)",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			seedOpenCodeStore(t, root)
			mutateOpenCodeStore(t, root, test.statement)

			ctx, recorder := obs.Test(t)
			sessions, err := ReadOpenCodeSessions(ctx, root)
			if err != nil {
				t.Fatalf("ReadOpenCodeSessions() error = %v, want the sessions kept", err)
			}
			ids := make([]string, 0, len(sessions))
			for _, session := range sessions {
				ids = append(ids, session.ID)
			}
			sort.Strings(ids)
			if strings.Join(ids, " ") != strings.Join(test.wantIDs, " ") {
				t.Fatalf("session ids = %v, want %v", ids, test.wantIDs)
			}
			storePath := OpenCodeStorePath(root)
			want := "opencode rows|" + test.wantWarn
			if got := unreadableWarnings(recorder, storePath); len(got) != 1 || got[0] != want {
				t.Fatalf("warnings for %s = %q, want [%q]", storePath, got, want)
			}
			wantProbe := "validate opencode rows: " + test.wantWarn
			if err := ProbeOpenCodeStore(context.Background(), root); err == nil || err.Error() != wantProbe {
				t.Fatalf("ProbeOpenCodeStore() error = %v, want %q", err, wantProbe)
			}
		})
	}
}

func TestReadOpenCodeSessionsAcceptsACompletelyEmptyNativeStore(t *testing.T) {
	root := t.TempDir()
	seedOpenCodeStore(t, root)
	db, err := sql.Open("sqlite", filepath.Join(root, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM part; DELETE FROM message; DELETE FROM session;`); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatalf("empty fixture: %v; close database: %v", err, closeErr)
		}
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	sessions, err := ReadOpenCodeSessions(context.Background(), root)
	if err != nil {
		t.Fatalf("empty native store must remain valid: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("sessions = %#v, want empty", sessions)
	}
}

func openMirrorStore(t *testing.T) *store.Store {
	t.Helper()
	root := t.TempDir()
	t.Setenv(paths.EnvCacheDB, filepath.Join(root, "pfm-cache.db"))
	t.Setenv(paths.EnvStateDB, filepath.Join(root, "shared.db"))
	database, err := store.Open()
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	return database
}

// TestSyncOpenCodeMirrorsChangedStoresOnly pins the mirror pass a fleet scan
// runs on every refresh: a pass over an unchanged store neither reads it nor
// replaces the mirror, and a commit a live OpenCode left in its WAL (the
// database file untouched) is read, dropping the session it deleted.
func TestSyncOpenCodeMirrorsChangedStoresOnly(t *testing.T) {
	database := openMirrorStore(t)
	ctx, recorder := obs.Test(t)
	root := t.TempDir()
	seedOpenCodeStore(t, root)
	reads := func() int {
		count := 0
		for _, record := range recorder.Records() {
			if kind, _ := record.Field("kind"); record.Message == "db.statement" && kind == "opencode" {
				count++
			}
		}
		return count
	}
	roots := []string{root}
	pass := func(step string, wantReads, wantMirrored int, wantIDs ...string) {
		t.Helper()
		var counters Counters
		if err := SyncOpenCode(ctx, database, roots, &counters); err != nil {
			t.Fatalf("%s: sync: %v", step, err)
		}
		stored, err := database.OpenCodeSessions(ctx)
		if err != nil {
			t.Fatalf("%s: reread mirror: %v", step, err)
		}
		ids := make([]string, 0, len(stored))
		for _, row := range stored {
			ids = append(ids, row.ID)
		}
		sort.Strings(ids)
		if got := reads(); got != wantReads || counters.OpenCodeSessions != wantMirrored ||
			strings.Join(ids, ",") != strings.Join(wantIDs, ",") {
			t.Fatalf("%s: store reads %d, mirrored %d, mirror %v; want %d, %d, %v",
				step, got, counters.OpenCodeSessions, ids, wantReads, wantMirrored, wantIDs)
		}
	}
	// A live OpenCode holds its store open in WAL mode, its WAL created by its
	// first transaction, so its commits stay in the WAL until a checkpoint.
	live, err := sql.Open("sqlite", filepath.Join(root, "opencode.db"))
	if err != nil {
		t.Fatalf("open the live store: %v", err)
	}
	live.SetMaxOpenConns(1)
	defer func() {
		if err := live.Close(); err != nil {
			t.Errorf("close the live store: %v", err)
		}
	}()
	if _, err := live.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatalf("enable WAL: %v", err)
	}
	if _, err := live.Exec("SELECT count(*) FROM session"); err != nil {
		t.Fatalf("open the live WAL: %v", err)
	}

	pass("first pass", 2, 3, "ses_child", "ses_gone", "ses_root")
	pass("unchanged store", 2, 0, "ses_child", "ses_gone", "ses_root")
	if _, err := live.Exec("DELETE FROM session WHERE id = 'ses_child'"); err != nil {
		t.Fatalf("delete child: %v", err)
	}
	pass("a commit only the WAL holds", 4, 2, "ses_gone", "ses_root")

	// A second OpenCode data home (PFM_OPENCODE_ROOT lists several) adds its
	// sessions to the mirror beside the first root's, never replacing them.
	second := t.TempDir()
	seedOpenCodeStore(t, second)
	mutateOpenCodeStore(t, second, "DELETE FROM session WHERE id <> 'ses_root'")
	mutateOpenCodeStore(t, second, "UPDATE session SET id = 'ses_second' WHERE id = 'ses_root'")
	roots = append(roots, second)
	pass("a second root", 8, 3, "ses_gone", "ses_root", "ses_second")
}

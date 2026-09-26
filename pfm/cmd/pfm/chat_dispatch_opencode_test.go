package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// seedOpenCodeSessionWithAReply builds the smallest OpenCode v1.14.30 store
// (schema mirrored from internal/index/opencode_test.go's seedOpenCodeStore)
// holding one session with a user prompt and an assistant reply, both stored
// as SQLite message/part rows — OpenCode has no transcript FILE at all.
func seedOpenCodeSessionWithAReply(t *testing.T, root string) {
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
  id TEXT PRIMARY KEY, worktree TEXT NOT NULL, vcs TEXT, name TEXT,
  icon_url TEXT, icon_url_override TEXT, icon_color TEXT,
  time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, time_initialized INTEGER,
  sandboxes TEXT NOT NULL, commands TEXT
);
CREATE TABLE session (
  id TEXT PRIMARY KEY, project_id TEXT NOT NULL, parent_id TEXT, slug TEXT NOT NULL,
  directory TEXT NOT NULL, title TEXT NOT NULL, version TEXT NOT NULL, share_url TEXT,
  summary_additions INTEGER, summary_deletions INTEGER, summary_files INTEGER, summary_diffs TEXT,
  revert TEXT, permission TEXT, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL,
  time_compacting INTEGER, time_archived INTEGER, workspace_id TEXT, path TEXT
);
CREATE TABLE message (
  id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL, data TEXT NOT NULL
);
CREATE TABLE part (
  id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL,
  time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL
);`
	if _, err := db.Exec(script); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO project (id, worktree, time_created, time_updated, sandboxes) VALUES ('p1', '/work/nuts', 1, 1, '[]')",
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO session (id, project_id, slug, directory, title, version, time_created, time_updated) " +
			"VALUES ('ses_root', 'p1', 'ses_root', '/work/nuts', 'prove the bound', '1.14.30', 10, 20)",
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO message (id, session_id, time_created, time_updated, data)
		 VALUES ('m1', 'ses_root', 100, 100, json_object(
		   'role','user', 'agent','build', 'model', json_object('providerID','prov','modelID','m1')
		 ))`,
	); err != nil {
		t.Fatalf("seed user message: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data)
		 VALUES ('i1', 'm1', 'ses_root', 100, 100, json_object('type','text','text','prove the bound'))`,
	); err != nil {
		t.Fatalf("seed user part: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO message (id, session_id, time_created, time_updated, data)
		 VALUES ('a1', 'ses_root', 200, 200, json_object(
		   'role','assistant', 'agent','build', 'providerID','prov', 'modelID','m1',
		   'tokens', json_object('input',10,'output',5), 'cost', 0.1
		 ))`,
	); err != nil {
		t.Fatalf("seed assistant message: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data)
		 VALUES ('i2', 'a1', 'ses_root', 200, 200, json_object('type','text','text','the bound holds'))`,
	); err != nil {
		t.Fatalf("seed assistant part: %v", err)
	}
}

// TestChatReadAndLastNameOpenCodeAsUnsupportedNotAbsent pins D4: an OpenCode
// session that the fleet listing already knows about (its SQLite store) has
// no transcript FILE for `pfm chat read` / `pfm chat last` to walk — pfm has
// no OpenCode message reader (grepped: no reader beyond count/first-prompt
// SQL in internal/index/opencode.go). The CLI-visible answer must name that
// as unsupported, never repeat the generic "has not written a transcript
// yet" line a truly silent chat gets — that line is an absence claim, and an
// OpenCode session with a real recorded reply is not absent.
func TestChatReadAndLastNameOpenCodeAsUnsupportedNotAbsent(t *testing.T) {
	jailTest(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	seedOpenCodeSessionWithAReply(t, resolved.Roots[pfmengine.OpenCode][0])

	for _, args := range [][]string{{"chat", "read", "ses_root"}, {"chat", "last", "ses_root"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(args, &stdout, &stderr)
			if code == 0 {
				t.Fatalf("%v: code=0, want a non-zero unsupported-format exit; stdout=%q", args, stdout.String())
			}
			if strings.Contains(stderr.String(), "has not written a transcript yet") {
				t.Fatalf(
					"%v: stderr=%q renders OpenCode's unread SQLite content as absence",
					args, stderr.String(),
				)
			}
			if !strings.Contains(stderr.String(), "not supported") {
				t.Fatalf("%v: stderr=%q does not name OpenCode content reading as unsupported", args, stderr.String())
			}
		})
	}
}

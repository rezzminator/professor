package main

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
)

func TestDoctorRejectsAnUnreadableOpenCodeSchema(t *testing.T) {
	jailTest(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	opencodeRoot := resolved.Roots[pfmengine.Opencode][0]
	if err := os.MkdirAll(opencodeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(opencodeRoot, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE session (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"doctor", "--skip-harvest"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stdout.String(), "doctor: opencode store=unhealthy") ||
		!strings.Contains(stdout.String(), "query opencode sessions") {
		t.Fatalf("doctor code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestDoctorRejectsMalformedOpenCodeRowsInsteadOfReportingHealthy(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE project (id TEXT PRIMARY KEY, worktree TEXT NOT NULL);
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
INSERT INTO project VALUES ('project', '/fixture');
INSERT INTO session (id, project_id, parent_id, slug, directory, title, version, time_created, time_updated)
VALUES ('session', 'project', NULL, 'session', '/fixture', 'fixture', '1.14.30', 1, 1);
INSERT INTO message VALUES ('message', 'session', 1, 1, 'not-json');
INSERT INTO part VALUES ('part', 'message', 'session', 1, 1, '{"type":"text","text":"prompt"}');
`)
	if err != nil {
		if closeErr := db.Close(); closeErr != nil {
			t.Fatalf("seed malformed OpenCode store: %v; close database: %v", err, closeErr)
		}
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	warnings := printOpencodeStoreDoctor(context.Background(), &stdout, pfmconfig.Config{
		OpencodeAccounts: []pfmconfig.OpenCodeAccount{{ID: 1, Home: root}},
	})
	if warnings == 0 || !strings.Contains(stdout.String(), "doctor: opencode store=unhealthy") {
		t.Fatalf("doctor warnings=%d output=%q, want malformed row reported unhealthy", warnings, stdout.String())
	}
}

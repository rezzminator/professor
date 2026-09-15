package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// A duplicate resume-boundary rollout (openai/codex#38792) in a SCRATCH codex
// home — never the real one. Same shape as internal/heal's
// TestDuplicateOrdinalIsNoncanonicalNotWedged, reproduced inline here because
// this package's own jail helper (healJail, heal_jail_test.go) builds its
// state/history stores by hand rather than sharing a fixture type with
// internal/heal.
func healNoncanonicalJail(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	codexRoot := filepath.Join(root, "codex")
	if err := os.MkdirAll(filepath.Join(codexRoot, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_HOME", root)
	t.Setenv("PFM_CODEX_ROOT", codexRoot)

	const id = "66666666-6666-4666-8666-666666666666"
	rollout := filepath.Join(codexRoot, "sessions", "rollout-"+id+".jsonl")
	lines := []string{
		`{"ordinal":0,"type":"event_msg","payload":{"type":"user_message"}}`,
		`{"ordinal":1,"type":"event_msg","payload":{"type":"user_message"}}`,
		`{"ordinal":2,"type":"event_msg","payload":{"type":"token_count"}}`,
		`{"ordinal":2,"type":"event_msg","payload":{"type":"thread_settings_applied"}}`,
		`{"ordinal":3,"type":"event_msg","payload":{"type":"user_message"}}`,
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(rollout, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	// The cursor sits at the SECOND ordinal-2 record expecting ordinal 3 —
	// the resumed writer reused the ordinal, not the 0.146.1 offset/ordinal
	// desync this package was originally built for.
	offset := len(lines[0]+"\n") + len(lines[1]+"\n") + len(lines[2]+"\n")

	state, err := sql.Open("sqlite", "file:"+filepath.Join(codexRoot, "state_1.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := state.Close(); err != nil {
			t.Errorf("close state: %v", err)
		}
	}()
	if _, err := state.Exec(
		"CREATE TABLE threads (id TEXT PRIMARY KEY, rollout_path TEXT)",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := state.Exec(
		"INSERT INTO threads VALUES (?, ?)", id, rollout,
	); err != nil {
		t.Fatal(err)
	}

	history, err := sql.Open(
		"sqlite",
		"file:"+filepath.Join(codexRoot, "thread_history_1.sqlite"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := history.Close(); err != nil {
			t.Errorf("close history: %v", err)
		}
	}()
	if _, err := history.Exec(`
		CREATE TABLE thread_history_projection_state (
			thread_id TEXT PRIMARY KEY,
			next_rollout_byte_offset INTEGER,
			next_rollout_ordinal INTEGER);
		CREATE TABLE thread_items (thread_id TEXT);
		CREATE TABLE thread_turns (thread_id TEXT);`); err != nil {
		t.Fatal(err)
	}
	if _, err := history.Exec(
		"INSERT INTO thread_history_projection_state VALUES (?, ?, 3)",
		id,
		offset,
	); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"thread_items", "thread_turns"} {
		if _, err := history.Exec("INSERT INTO "+table+" VALUES (?)", id); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

// The report must never fold a NONCANONICAL thread into the Broken() lines
// (it must still be NAMED, per the printer spec) and must print the upgrade
// advice once; --apply must count it in left_alone and delete none of its
// rows.
func TestHealCommandPrintsNoncanonicalAdvice(t *testing.T) {
	id := healNoncanonicalJail(t)
	codexRoot := os.Getenv("PFM_CODEX_ROOT")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"heal"}, &stdout, &stderr); code != 0 {
		t.Fatalf("heal report rc = %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "NONCANONICAL\t"+id) {
		t.Fatalf("the report did not name the noncanonical thread:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "0.154.0") {
		t.Fatalf("the report did not carry the Codex version boundary:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "never rebuilt") {
		t.Fatalf("the report did not carry the never-rebuilt advice:\n%s", stdout.String())
	}
	if healProjectionRows(t, codexRoot, id) != 1 {
		t.Fatal("the report deleted a noncanonical projection row")
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"heal", "--apply"}, &stdout, &stderr); code != 0 {
		t.Fatalf("heal --apply rc = %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "left_alone=1") {
		t.Fatalf("--apply did not count the left-alone thread:\n%s", stdout.String())
	}
	if healProjectionRows(t, codexRoot, id) != 1 {
		t.Fatal("--apply deleted a noncanonical projection")
	}
}

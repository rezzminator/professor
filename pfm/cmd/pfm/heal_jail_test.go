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

// A wedged Codex projection in a SCRATCH codex home — never the real one: a
// heal deletes projection rows, and a fixture that could reach ~/.codex is a
// fixture that can cost a chat its history.
func healJail(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	codexHome := filepath.Join(root, "codex")
	if err := os.MkdirAll(filepath.Join(codexHome, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_HOME", root)
	t.Setenv("PFM_CODEX_ROOT", codexHome)

	const id = "33333333-3333-4333-8333-333333333333"
	rollout := filepath.Join(codexHome, "sessions", "rollout-"+id+".jsonl")
	// Three records; the cursor will point at the third while claiming the
	// first's ordinal — the 0.146.1 desync.
	content := `{"ordinal":0,"type":"event_msg"}` + "\n" +
		`{"ordinal":1,"type":"event_msg"}` + "\n" +
		`{"ordinal":2,"type":"event_msg"}` + "\n"
	if err := os.WriteFile(rollout, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	offset := len(`{"ordinal":0,"type":"event_msg"}`+"\n") +
		len(`{"ordinal":1,"type":"event_msg"}`+"\n")

	state, err := sql.Open("sqlite", "file:"+filepath.Join(codexHome, "state_1.sqlite"))
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
		"file:"+filepath.Join(codexHome, "thread_history_1.sqlite"),
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
		"INSERT INTO thread_history_projection_state VALUES (?, ?, 1)",
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

func healProjectionRows(t *testing.T, codexHome, id string) int {
	t.Helper()
	history, err := sql.Open(
		"sqlite",
		"file:"+filepath.Join(codexHome, "thread_history_1.sqlite")+"?mode=ro",
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := history.Close(); err != nil {
			t.Errorf("close history: %v", err)
		}
	}()
	var count int
	if err := history.QueryRow(
		"SELECT count(*) FROM thread_history_projection_state WHERE thread_id = ?",
		id,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// The report is read-only and names the wedged thread; --thread repairs that
// one thread and exits 0; a healthy thread is a silent exit 0, which is what
// makes the pre-resume call free.
func TestHealCommandReportsThenRepairs(t *testing.T) {
	id := healJail(t)
	codexHome := os.Getenv("PFM_CODEX_ROOT")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"heal"}, &stdout, &stderr); code != 0 {
		t.Fatalf("heal report rc = %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "WEDGED\t"+id) {
		t.Fatalf("the report did not name the wedged thread:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "totals: WEDGED=1") {
		t.Fatalf("totals line missing:\n%s", stdout.String())
	}
	if healProjectionRows(t, codexHome, id) != 1 {
		t.Fatal("the report deleted a projection row")
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"heal", "--thread", id}, &stdout, &stderr); code != 0 {
		t.Fatalf("heal --thread rc = %d: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), id) {
		t.Fatalf("the repair said nothing about what it repaired: %q", stderr.String())
	}
	if healProjectionRows(t, codexHome, id) != 0 {
		t.Fatal("--thread reported a repair but left the projection in place")
	}

	// Second call: nothing broken, nothing said, still exit 0.
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"heal", "--thread", id}, &stdout, &stderr); code != 0 {
		t.Fatalf("a no-op heal exited %d", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("a no-op heal wrote %q", stderr.String())
	}
}

// A Codex home with no stores is not a failure for --thread: the resume it
// guards matters more than the repair.
func TestHealThreadIsSilentWithoutACodexHome(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PFM_HOME", root)
	t.Setenv("PFM_CODEX_ROOT", filepath.Join(root, "nowhere"))
	var stdout, stderr bytes.Buffer
	if code := run(
		[]string{"heal", "--thread", "33333333-3333-4333-8333-333333333333"},
		&stdout,
		&stderr,
	); code != 0 {
		t.Fatalf("rc = %d, want 0: %s", code, stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("output = %q / %q, want silence", stdout.String(), stderr.String())
	}
	// The reporting form DOES fail loudly — an operator asking for a report
	// deserves to hear that there is nothing to report on.
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"heal"}, &stdout, &stderr); code != 1 {
		t.Fatalf("heal report rc = %d, want 1 for a missing Codex home", code)
	}
}

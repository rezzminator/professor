package callmeter

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenDB(context.Background(), filepath.Join(t.TempDir(), "state", "callmeter.db"))
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store
}

// row reads one row of table as column name -> value (nil for NULL).
func row(t *testing.T, store *Store, table, where string, args ...any) map[string]any {
	t.Helper()
	rows, err := store.DB().Query(fmt.Sprintf("SELECT * FROM %s WHERE %s", table, where), args...)
	if err != nil {
		t.Fatalf("query %s: %v", table, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close %s rows: %v", table, err)
		}
	}()
	names, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns of %s: %v", table, err)
	}
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			t.Fatalf("read %s: %v", table, err)
		}
		return nil
	}
	values := make([]any, len(names))
	pointers := make([]any, len(names))
	for i := range values {
		pointers[i] = &values[i]
	}
	if err := rows.Scan(pointers...); err != nil {
		t.Fatalf("scan %s: %v", table, err)
	}
	out := map[string]any{}
	for i, name := range names {
		out[name] = values[i]
	}
	return out
}

func count(t *testing.T, store *Store, table string) int {
	t.Helper()
	var n int
	if err := store.DB().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestDefaultPath(t *testing.T) {
	if got, want := DefaultPath("/tmp/demo-home"), "/tmp/demo-home/.local/state/pfm/callmeter.db"; got != want {
		t.Fatalf("DefaultPath = %q, want %q", got, want)
	}
}

func TestOpenSetsPragmasAndVersion(t *testing.T) {
	store := openTestStore(t)
	for pragma, want := range map[string]string{
		"journal_mode": "wal",
		"busy_timeout": "5000",
		"foreign_keys": "0",
		"user_version": "3",
	} {
		var got string
		if err := store.DB().QueryRow("PRAGMA " + pragma).Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s: %v", pragma, err)
		}
		if got != want {
			t.Errorf("PRAGMA %s = %q, want %q", pragma, got, want)
		}
	}
}

func TestOpenRefusesNewerSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "callmeter.db")
	store, err := OpenDB(ctx, path)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	if _, err := store.DB().Exec("PRAGMA user_version=4"); err != nil {
		t.Fatalf("raise version: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened, err := OpenDB(ctx, path)
	if err == nil {
		_ = reopened.Close()
		t.Fatal("OpenDB of a version-4 store succeeded, want a refusal")
	}
	for _, want := range []string{"version 4", "version 3"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
	// The refusal left the store as it was: a newer binary still reads it.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer func() {
		if err := raw.Close(); err != nil {
			t.Errorf("close raw handle: %v", err)
		}
	}()
	var version int
	if err := raw.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 4 {
		t.Fatalf("user_version after refusal = %d, want 4", version)
	}
}

// rawStore opens path with the bare driver, bypassing OpenDB's migration.
func rawStore(t *testing.T, path string) *sql.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	t.Cleanup(func() {
		if err := raw.Close(); err != nil {
			t.Errorf("close raw handle: %v", err)
		}
	})
	return raw
}

func rawExec(t *testing.T, raw *sql.DB, statements ...string) {
	t.Helper()
	for _, statement := range statements {
		if _, err := raw.Exec(statement); err != nil {
			t.Fatalf("exec %q: %v", statement, err)
		}
	}
}

func rawVersion(t *testing.T, raw *sql.DB) int {
	t.Helper()
	var version int
	if err := raw.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	return version
}

// writeV1Store writes a version-1 store holding one hook row and one
// transcript row in calls, requests and agents, the command parts of both
// calls and, unless brokenFaults, the faults a transcript replay left: backfill,
// store and parse on its call, parse on the hook call. brokenFaults gives
// faults no stage column, so the purge's fault statement fails.
func writeV1Store(t *testing.T, brokenFaults bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "callmeter.db")
	raw := rawStore(t, path)
	rawExec(t, raw, schema,
		`INSERT INTO calls (tool_use_id, tool, source) VALUES ('toolu_hook', 'Bash', 'hook'),
			('toolu_replay', 'Bash', 'transcript')`,
		`INSERT INTO requests (request_id, source) VALUES ('msg_hook', 'hook'), ('msg_replay', 'transcript')`,
		`INSERT INTO agents (agent_id, source) VALUES ('agent_hook', 'hook'), ('agent_replay', 'transcript')`,
		`INSERT INTO command_parts (tool_use_id, seq, program) VALUES ('toolu_hook', 0, 'wc'),
			('toolu_replay', 0, 'head')`,
	)
	if brokenFaults {
		rawExec(t, raw, "DROP TABLE faults",
			"CREATE TABLE faults (ts INTEGER, session_id TEXT, tool_use_id TEXT, error TEXT)")
	} else {
		rawExec(t, raw, `INSERT INTO faults (tool_use_id, stage, error) VALUES
			('toolu_replay', 'backfill', 'replay'), ('toolu_replay', 'store', 'disk full'),
			('toolu_replay', 'parse', 'bad quote'), ('toolu_hook', 'parse', 'bad quote')`)
	}
	rawExec(t, raw, "PRAGMA user_version=1")
	return path
}

// keys lists one column of a table, sorted, as "a,b".
func keys(t *testing.T, db *sql.DB, query string) string {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close rows: %v", err)
		}
	}()
	var out []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatalf("scan %q: %v", query, err)
		}
		out = append(out, key)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read %q: %v", query, err)
	}
	return strings.Join(out, ",")
}

func TestOpenMigratesV1StoreToHookRowsOnly(t *testing.T) {
	path := writeV1Store(t, false)
	store, err := OpenDB(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenDB of a v1 store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	for query, want := range map[string]string{
		"SELECT tool_use_id FROM calls ORDER BY 1":                   "toolu_hook",
		"SELECT request_id FROM requests ORDER BY 1":                 "msg_hook",
		"SELECT agent_id FROM agents ORDER BY 1":                     "agent_hook",
		"SELECT tool_use_id FROM command_parts ORDER BY 1":           "toolu_hook",
		"SELECT stage || ':' || tool_use_id FROM faults ORDER BY 1":  "parse:toolu_hook,store:toolu_replay",
		"SELECT CAST(user_version AS TEXT) FROM pragma_user_version": "3",
	} {
		if got := keys(t, store.DB(), query); got != want {
			t.Errorf("%s = %q, want %q", query, got, want)
		}
	}
}

// TestOpenMigrationNullsReferencesToPurgedRows: a transcript replay filled a
// hook call's empty request_id with a request it wrote itself, so a v1 store
// holds hook rows pointing at transcript calls, requests and agents. The purge
// deletes every transcript row all the same and nulls only a kept call's
// request_id that names a deleted request; a request_id that named no row, or
// was NULL, stays, and agent_id and parent_tool_use_id, the hook's own payload
// values, stay even when the row they name is deleted.
func TestOpenMigrationNullsReferencesToPurgedRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "callmeter.db")
	raw := rawStore(t, path)
	rawExec(t, raw, schema,
		`INSERT INTO calls (tool_use_id, session_id, tool, request_id, agent_id, source) VALUES
			('toolu_hook', 'sess_demo', 'Bash', 'msg_filled', 'agent_filled', 'hook'),
			('toolu_bare', 'sess_demo', 'Read', NULL, NULL, 'hook'),
			('toolu_unresolved', 'sess_demo', 'Grep', 'msg_never_held', NULL, 'hook'),
			('toolu_replay', 'sess_demo', 'Bash', 'msg_replay', 'agent_replay', 'transcript')`,
		`INSERT INTO requests (request_id, agent_id, context_tokens, source) VALUES
			('msg_filled', 'agent_filled', 900, 'transcript'), ('msg_replay', 'agent_replay', 5, 'transcript'),
			('msg_hook', 'agent_of_request', 7, 'hook')`,
		`INSERT INTO agents (agent_id, parent_tool_use_id, source) VALUES ('agent_filled', NULL, 'transcript'),
			('agent_replay', NULL, 'transcript'), ('agent_of_request', NULL, 'transcript'),
			('agent_child', 'toolu_replay', 'hook')`,
		"PRAGMA user_version=1",
	)
	before := row(t, &Store{db: raw, path: path}, "calls", "tool_use_id = ?", "toolu_hook")
	store, err := OpenDB(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenDB of a v1 store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	for query, want := range map[string]string{
		"SELECT tool_use_id FROM calls ORDER BY 1":                      "toolu_bare,toolu_hook,toolu_unresolved",
		"SELECT request_id FROM requests ORDER BY 1":                    "msg_hook",
		"SELECT agent_id FROM agents ORDER BY 1":                        "agent_child",
		"SELECT tool_use_id FROM calls WHERE source IS NOT 'hook'":      "",
		"SELECT request_id FROM requests WHERE source IS NOT 'hook'":    "",
		"SELECT agent_id FROM agents WHERE source IS NOT 'hook'":        "",
		"SELECT request_id FROM calls WHERE request_id NOT NULL":        "msg_never_held",
		"SELECT agent_id FROM calls WHERE agent_id NOT NULL":            "agent_filled",
		"SELECT agent_id FROM requests WHERE agent_id NOT NULL":         "agent_of_request",
		"SELECT agent_id FROM agents WHERE parent_tool_use_id NOT NULL": "agent_child",
	} {
		if got := keys(t, store.DB(), query); got != want {
			t.Errorf("%s = %q, want %q", query, got, want)
		}
	}
	after := row(t, store, "calls", "tool_use_id = ?", "toolu_hook")
	if after == nil {
		t.Fatal("toolu_hook is gone, want the hook call kept")
	}
	for column, value := range before {
		want := value
		if column == "request_id" {
			want = nil
		}
		if !reflect.DeepEqual(after[column], want) {
			t.Errorf("toolu_hook %s = %v, want %v", column, after[column], want)
		}
	}
	got := row(t, store, "requests", "request_id = ?", "msg_hook")
	if got == nil || got["agent_id"] != "agent_of_request" {
		t.Errorf("msg_hook = %v, want kept with agent_id agent_of_request", got)
	}
	agent := row(t, store, "agents", "agent_id = ?", "agent_child")
	if agent == nil || agent["parent_tool_use_id"] != "toolu_replay" {
		t.Errorf("agent_child = %v, want kept with parent_tool_use_id toolu_replay", agent)
	}
}

// TestOpenMigrationPurgesRowsWithNoSource: the replay's task-notice path wrote an
// agents row holding only agent_id and stopped, with no source at all, and
// the hook always sets its source, so a v1 row whose source is NULL is a
// replay's. The purge deletes it like a transcript row and nulls a kept call's
// request_id to a deleted row, while a kept agent_id naming it and a kept
// agent's parent_tool_use_id naming a NULL-source call stay; a NULL source in
// calls or requests goes the same way.
func TestOpenMigrationPurgesRowsWithNoSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "callmeter.db")
	raw := rawStore(t, path)
	rawExec(t, raw, schema,
		`INSERT INTO calls (tool_use_id, tool, request_id, agent_id, source) VALUES
			('toolu_hook', 'Bash', 'msg_unsourced', 'agent_notice', 'hook'),
			('toolu_unsourced', 'Read', NULL, NULL, NULL)`,
		`INSERT INTO requests (request_id, agent_id, source) VALUES
			('msg_hook', 'agent_notice', 'hook'), ('msg_unsourced', NULL, NULL)`,
		`INSERT INTO agents (agent_id, parent_tool_use_id, stopped, source) VALUES
			('agent_notice', NULL, 1700000000000, NULL), ('agent_child', 'toolu_unsourced', NULL, 'hook')`,
		"PRAGMA user_version=1",
	)
	store, err := OpenDB(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenDB of a v1 store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	for query, want := range map[string]string{
		"SELECT tool_use_id FROM calls ORDER BY 1":                      "toolu_hook",
		"SELECT request_id FROM requests ORDER BY 1":                    "msg_hook",
		"SELECT agent_id FROM agents ORDER BY 1":                        "agent_child",
		"SELECT tool_use_id FROM calls WHERE request_id NOT NULL":       "",
		"SELECT agent_id FROM calls WHERE agent_id NOT NULL":            "agent_notice",
		"SELECT agent_id FROM requests WHERE agent_id NOT NULL":         "agent_notice",
		"SELECT agent_id FROM agents WHERE parent_tool_use_id NOT NULL": "agent_child",
		"SELECT tool_use_id FROM calls WHERE source IS NOT 'hook'":      "",
		"SELECT request_id FROM requests WHERE source IS NOT 'hook'":    "",
		"SELECT agent_id FROM agents WHERE source IS NOT 'hook'":        "",
	} {
		if got := keys(t, store.DB(), query); got != want {
			t.Errorf("%s = %q, want %q", query, got, want)
		}
	}
	agent := row(t, store, "agents", "agent_id = ?", "agent_child")
	if agent == nil || agent["parent_tool_use_id"] != "toolu_unsourced" {
		t.Errorf("agent_child = %v, want kept with parent_tool_use_id toolu_unsourced", agent)
	}
}

// TestOpenMigrationKeepsHookPayloadIDs: the hook takes a call's and a
// request's agent_id from its own payload, beside agent_type, and a sub-agent's
// parent_tool_use_id from the Agent or Task call whose result carries it, so
// the purge leaves both as the hook wrote them even when the row they name is
// deleted, whether that row came from a transcript replay or carried no source
// at all.
func TestOpenMigrationKeepsHookPayloadIDs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "callmeter.db")
	raw := rawStore(t, path)
	rawExec(t, raw, schema,
		`INSERT INTO calls (tool_use_id, session_id, tool, request_id, agent_id, agent_type, source) VALUES
			('toolu_sub', 'sess_demo', 'Bash', 'msg_replayed', 'agent_replayed', 'Explore', 'hook'),
			('toolu_spawn', 'sess_demo', 'Agent', NULL, NULL, NULL, 'transcript')`,
		`INSERT INTO requests (request_id, agent_id, source) VALUES
			('msg_replayed', NULL, 'transcript'), ('msg_sub', 'agent_unsourced', 'hook')`,
		`INSERT INTO agents (agent_id, parent_tool_use_id, source) VALUES ('agent_replayed', NULL, 'transcript'),
			('agent_unsourced', NULL, NULL), ('agent_spawned', 'toolu_spawn', 'hook')`,
		"PRAGMA user_version=1",
	)
	store, err := OpenDB(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenDB of a v1 store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	call := row(t, store, "calls", "tool_use_id = ?", "toolu_sub")
	if call == nil {
		t.Fatal("toolu_sub is gone, want the hook call kept")
	}
	if call["agent_id"] != "agent_replayed" || call["agent_type"] != "Explore" || call["request_id"] != nil {
		t.Errorf("toolu_sub = %v, want agent_id agent_replayed, agent_type Explore, request_id NULL", call)
	}
	request := row(t, store, "requests", "request_id = ?", "msg_sub")
	if request == nil || request["agent_id"] != "agent_unsourced" {
		t.Errorf("msg_sub = %v, want kept with agent_id agent_unsourced", request)
	}
	for query, want := range map[string]string{
		"SELECT agent_id FROM agents ORDER BY 1":                     "agent_spawned",
		"SELECT request_id FROM requests ORDER BY 1":                 "msg_sub",
		"SELECT tool_use_id FROM calls ORDER BY 1":                   "toolu_sub",
		"SELECT tool_use_id FROM calls WHERE source IS NOT 'hook'":   "",
		"SELECT request_id FROM requests WHERE source IS NOT 'hook'": "",
		"SELECT agent_id FROM agents WHERE source IS NOT 'hook'":     "",
	} {
		if got := keys(t, store.DB(), query); got != want {
			t.Errorf("%s = %q, want %q", query, got, want)
		}
	}
	agent := row(t, store, "agents", "agent_id = ?", "agent_spawned")
	if agent == nil || agent["parent_tool_use_id"] != "toolu_spawn" {
		t.Errorf("agent_spawned = %v, want kept with parent_tool_use_id toolu_spawn", agent)
	}
}

func TestOpenPurgesV1StoreOnlyOnce(t *testing.T) {
	ctx := context.Background()
	path := writeV1Store(t, false)
	store, err := OpenDB(ctx, path)
	if err != nil {
		t.Fatalf("OpenDB of a v1 store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	rawExec(t, rawStore(t, path), "INSERT INTO calls (tool_use_id, source) VALUES ('toolu_later', 'transcript')")
	reopened, err := OpenDB(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	if got := row(t, reopened, "calls", "tool_use_id = ?", "toolu_later"); got == nil {
		t.Fatal("reopening a version-2 store purged a transcript row, want the purge to run only at version 1")
	}
}

func TestOpenFreshStoreIsEmptyAtCurrentVersion(t *testing.T) {
	store := openTestStore(t)
	if got := keys(t, store.DB(), "SELECT CAST(user_version AS TEXT) FROM pragma_user_version"); got != "3" {
		t.Errorf("user_version = %s, want 3", got)
	}
	for _, table := range []string{"calls", "requests", "agents", "command_parts", "faults"} {
		if n := count(t, store, table); n != 0 {
			t.Errorf("fresh %s holds %d rows, want 0", table, n)
		}
	}
}

func TestOpenFailedPurgeRollsBackAtV1(t *testing.T) {
	path := writeV1Store(t, true)
	store, err := OpenDB(context.Background(), path)
	if err == nil {
		_ = store.Close()
		t.Fatal("OpenDB over a failing purge succeeded, want an error")
	}
	for _, want := range []string{path, "stage = 'backfill'"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	raw := rawStore(t, path)
	if version := rawVersion(t, raw); version != 1 {
		t.Errorf("user_version after a failed purge = %d, want 1", version)
	}
	if got := keys(t, raw, "SELECT tool_use_id FROM calls ORDER BY 1"); got != "toolu_hook,toolu_replay" {
		t.Errorf("calls after a failed purge = %q, want both rows back: the purge rolls back whole", got)
	}
}

func TestPruneByAge(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	cutoff := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	old, recent := cutoff.Add(-time.Hour).UnixMilli(), cutoff.Add(time.Hour).UnixMilli()
	for id, ts := range map[string]int64{"toolu_old": old, "toolu_new": recent} {
		if err := store.UpsertCall(ctx, Call{ToolUseID: id, TS: Ptr(ts)}, Overwrite); err != nil {
			t.Fatal(err)
		}
		if err := store.ReplaceCommandParts(ctx, id, []CommandPart{{Seq: 0, Lang: "sh", Program: "cat"}}); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertRequest(ctx, Request{RequestID: "msg_" + id, TS: Ptr(ts)}, Overwrite); err != nil {
			t.Fatal(err)
		}
		if err := store.UpsertAgent(ctx, Agent{AgentID: "agent_" + id, Started: Ptr(ts)}, Overwrite); err != nil {
			t.Fatal(err)
		}
		if err := store.AddFault(ctx, Fault{TS: ts, ToolUseID: id, Stage: StageParse, Error: "boom"}); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := store.Prune(ctx, cutoff)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if removed != 5 {
		t.Errorf("Prune removed %d rows, want 5 (one per table)", removed)
	}
	for _, table := range []string{"calls", "requests", "agents", "faults", "command_parts"} {
		if n := count(t, store, table); n != 1 {
			t.Errorf("%s holds %d rows after prune, want 1", table, n)
		}
	}
	if row(t, store, "calls", "tool_use_id = ?", "toolu_new") == nil {
		t.Error("the recent call was pruned")
	}
	if row(t, store, "command_parts", "tool_use_id = ?", "toolu_new") == nil {
		t.Error("the recent call's command parts were pruned")
	}
}

func TestAddFaultStoresEmptyAsNull(t *testing.T) {
	store := openTestStore(t)
	if err := store.AddFault(context.Background(), Fault{TS: 1, Stage: StagePayload, Error: "bad json"}); err != nil {
		t.Fatal(err)
	}
	got := row(t, store, "faults", "stage = ?", StagePayload)
	if got["session_id"] != nil || got["tool_use_id"] != nil {
		t.Fatalf("empty ids stored as %v / %v, want NULL", got["session_id"], got["tool_use_id"])
	}
}

// v2Schema is the store's DDL at schema version 2: calls, requests and agents
// without the account and seat_dir columns.
const v2Schema = `
CREATE TABLE calls (tool_use_id TEXT PRIMARY KEY, session_id TEXT, agent_id TEXT, agent_type TEXT,
	request_id TEXT, ts INTEGER, tool TEXT, input TEXT, cwd TEXT, duration_ms INTEGER, failed INTEGER,
	error TEXT, bytes_real INTEGER, bytes_delivered INTEGER, persisted_path TEXT, file_path TEXT,
	file_bytes INTEGER, file_bytes_before INTEGER, read_start INTEGER, read_lines INTEGER,
	read_total_lines INTEGER, source TEXT, config_dir TEXT);
CREATE INDEX calls_session_agent_ts ON calls(session_id, agent_id, ts);
CREATE INDEX calls_ts ON calls(ts);
CREATE INDEX calls_file_path ON calls(file_path);
CREATE TABLE requests (request_id TEXT PRIMARY KEY, session_id TEXT, agent_id TEXT, ts INTEGER,
	context_tokens INTEGER, output_tokens INTEGER, calls INTEGER, pending INTEGER, source TEXT, config_dir TEXT);
CREATE INDEX requests_session_agent_pending ON requests(session_id, agent_id, pending);
CREATE TABLE agents (agent_id TEXT PRIMARY KEY, session_id TEXT, agent_type TEXT, parent_tool_use_id TEXT,
	started INTEGER, stopped INTEGER, transcript_path TEXT, total_tokens INTEGER, tool_uses INTEGER,
	model TEXT, source TEXT, config_dir TEXT);
CREATE TABLE command_parts (tool_use_id TEXT NOT NULL, seq INTEGER NOT NULL, lang TEXT, program TEXT,
	args TEXT, files TEXT, parse_status TEXT, conditional INTEGER NOT NULL DEFAULT 0,
	parser INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (tool_use_id, seq));
CREATE TABLE faults (ts INTEGER, session_id TEXT, tool_use_id TEXT, stage TEXT, error TEXT);
`

// TestOpenMigratesV2StoreAddingAccountColumns: a version-2 store gains account
// and seat_dir on calls, requests and agents in place; every row it held
// keeps them NULL (no backfill) and the store reads version 3.
func TestOpenMigratesV2StoreAddingAccountColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "callmeter.db")
	raw := rawStore(t, path)
	rawExec(t, raw, v2Schema,
		`INSERT INTO calls (tool_use_id, tool, source, config_dir) VALUES ('toolu_old', 'Bash', 'hook', '/h/.claude')`,
		`INSERT INTO requests (request_id, source, config_dir) VALUES ('msg_old', 'hook', '/h/.claude')`,
		`INSERT INTO agents (agent_id, source, config_dir) VALUES ('agent_old', 'hook', '/h/.claude')`,
		"PRAGMA user_version=2",
	)
	store, err := OpenDB(context.Background(), path)
	if err != nil {
		t.Fatalf("OpenDB of a v2 store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	for query, want := range map[string]string{
		"SELECT CAST(user_version AS TEXT) FROM pragma_user_version":                                    "3",
		"SELECT tool_use_id || ':' || config_dir FROM calls WHERE account IS NULL AND seat_dir IS NULL": "toolu_old:/h/.claude",
		"SELECT request_id FROM requests WHERE account IS NULL AND seat_dir IS NULL":                    "msg_old",
		"SELECT agent_id FROM agents WHERE account IS NULL AND seat_dir IS NULL":                        "agent_old",
	} {
		if got := keys(t, store.DB(), query); got != want {
			t.Errorf("%s = %q, want %q", query, got, want)
		}
	}
	assertAccountColumns(t, store.DB())
}

// assertAccountColumns fails unless calls, requests and agents each carry
// account INTEGER and seat_dir TEXT.
func assertAccountColumns(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"calls", "requests", "agents"} {
		query := "SELECT name || ' ' || type FROM pragma_table_info('" + table +
			"') WHERE name IN ('account', 'seat_dir') ORDER BY name"
		if got := keys(t, db, query); got != "account INTEGER,seat_dir TEXT" {
			t.Errorf("%s account columns = %q, want account INTEGER and seat_dir TEXT", table, got)
		}
	}
}

func TestOpenFreshStoreHasAccountColumns(t *testing.T) {
	assertAccountColumns(t, openTestStore(t).DB())
}

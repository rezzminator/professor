package fleetdb

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// The schema is a compatibility surface for existing fleet databases, so its
// columns and constraints stay exact.
func TestSharedSchemaIsComplete(t *testing.T) {
	t.Parallel()
	// The file: journal_mode=wal is a property of the file, never of the in-memory state.
	state, _ := openFileTestStore(t)
	ctx := context.Background()

	tables := queryColumn(t, state, `
SELECT name FROM sqlite_master
WHERE type='table' AND name NOT LIKE 'sqlite_%'
ORDER BY name`)
	want := []string{"chat", "children", "comms", "hidden", "launch", "meta", "schedules", "seat"}
	if !reflect.DeepEqual(tables, want) {
		t.Fatalf("shared tables = %v, want %v", tables, want)
	}

	// Kills are uuid-keyed, with no engine column.
	columns := queryColumn(t, state, "SELECT name FROM pragma_table_info('hidden')")
	if !reflect.DeepEqual(columns, []string{"uuid", "hidden_at", "at_payload", "revision"}) {
		t.Fatalf("killed columns = %v", columns)
	}
	columns = queryColumn(t, state, "SELECT name FROM pragma_table_info('children')")
	if !reflect.DeepEqual(columns, []string{"kind", "key", "val", "created_at"}) {
		t.Fatalf("children columns = %v", columns)
	}
	columns = queryColumn(t, state, "SELECT name FROM pragma_table_info('meta')")
	if !reflect.DeepEqual(columns, []string{"key", "val", "updated_at"}) {
		t.Fatalf("meta columns = %v", columns)
	}
	columns = queryColumn(t, state, "SELECT name FROM pragma_table_info('comms')")
	if !reflect.DeepEqual(columns, []string{
		"id", "at_ns", "kind", "sender_session", "sender_label", "sender_uuid",
		"target", "receiver_socket", "receiver_pane", "group_name", "members", "message",
	}) {
		t.Fatalf("comms columns = %v", columns)
	}
	columns = queryColumn(t, state, "SELECT name FROM pragma_table_info('launch')")
	if !reflect.DeepEqual(
		columns,
		[]string{"session_id", "engine", "account", "cache1h", "launched_at", "updated_at"},
	) {
		t.Fatalf("launch columns = %v", columns)
	}
	var version int
	if err := state.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatalf("user_version = %d, %v; want %d", version, err, SchemaVersion)
	}
	if _, err := os.Stat(state.path + ".bak-before-v2"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fresh database backup = %v", err)
	}

	// Initialization stamps the schema version.
	if version, found, err := state.Meta(ctx, "schema_version"); err != nil ||
		!found || version != "1" {
		t.Fatalf("schema_version = %q, %v, %v", version, found, err)
	}

	var journalMode string
	if err := state.db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}
	var busyTimeout int
	if err := state.db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if busyTimeout == 0 {
		t.Fatal("busy_timeout = 0: a concurrent sqlite3 CLI writer would fail instead of queue")
	}
}

// openFileTestStore opens the shared state file under a fresh temp root, for a test of the file
// itself: its path, a second connection, a reopen, a file beside it.
func openFileTestStore(t *testing.T) (*Store, paths.Values) {
	t.Helper()

	root := t.TempDir()
	testjail.DatabaseFiles(t, root)
	values := paths.Values{
		StateDB: filepath.Join(root, "cc", "pfm.db"),
		Home:    filepath.Join(root, "home"),
	}
	state := OpenSharedState(context.Background(), values)
	if err := state.Degraded(); err != nil {
		t.Fatalf("Open() degraded: %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	return state, values
}

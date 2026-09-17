package fleetdb

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"hostops/pfm/internal/paths"
)

// The schema is a compatibility surface for existing fleet databases, so its
// columns and constraints stay exact.
func TestSharedSchemaIsComplete(t *testing.T) {
	state, _ := openTestStore(t)
	ctx := context.Background()

	tables := queryColumn(t, state, `
SELECT name FROM sqlite_master
WHERE type='table' AND name NOT LIKE 'sqlite_%'
ORDER BY name`)
	want := []string{"chat", "children", "comms", "hidden", "issues", "meta"}
	if !reflect.DeepEqual(tables, want) {
		t.Fatalf("shared tables = %v, want %v", tables, want)
	}

	// Kills are uuid-keyed, with no engine column.
	columns := queryColumn(t, state, "SELECT name FROM pragma_table_info('hidden')")
	if !reflect.DeepEqual(columns, []string{"uuid", "hidden_at", "at_payload"}) {
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
	columns = queryColumn(t, state, "SELECT name FROM pragma_table_info('issues')")
	if !reflect.DeepEqual(columns, []string{
		"id", "at_ns", "title", "detail", "severity", "area",
		"reporter_session", "reporter_label", "reporter_uuid", "reporter_cwd", "reporter_engine",
		"status",
	}) {
		t.Fatalf("issues columns = %v", columns)
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

func TestKillDoesNotRecreateTheRetiredCarrier(t *testing.T) {
	state, values := openTestStore(t)
	legacyCarrier := filepath.Join(
		values.Home,
		".claude",
		".cc-ls-hidden",
	)
	if err := state.Kill(context.Background(), "database-only", 42); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacyCarrier); !os.IsNotExist(err) {
		t.Fatalf("Kill recreated retired carrier %s: %v", legacyCarrier, err)
	}
}

func TestClearKillBaselineIsMonotonicAndRaceSafe(t *testing.T) {
	state, _ := openTestStore(t)
	ctx := context.Background()
	const id = "11111111-1111-4111-8111-111111111111"

	if err := state.KillUntilPrompt(ctx, id, 10, 2); err != nil {
		t.Fatal(err)
	}
	if err := state.KillUntilPrompt(ctx, id, 11, 3); err != nil {
		t.Fatal(err)
	}
	records, err := state.KilledRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := records[id]; got.KilledAt != 11 || got.AtPayload == nil || *got.AtPayload != 3 {
		t.Fatalf("repeated clear kill = %#v, want hidden_at=11 baseline=3", got)
	}
	if removed, err := state.UnkillIfPayload(ctx, id, 2); err != nil || removed {
		t.Fatalf("stale conditional unkill = %v, %v; want false, nil", removed, err)
	}

	if err := state.Kill(ctx, id, 12); err != nil {
		t.Fatal(err)
	}
	if err := state.KillUntilPrompt(ctx, id, 13, 4); err != nil {
		t.Fatal(err)
	}
	records, err = state.KilledRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := records[id]; got.KilledAt != 12 || got.AtPayload != nil {
		t.Fatalf("clear weakened permanent kill = %#v", got)
	}

	const expiring = "22222222-2222-4222-8222-222222222222"
	if err := state.KillUntilPrompt(ctx, expiring, 20, 5); err != nil {
		t.Fatal(err)
	}
	if removed, err := state.UnkillIfPayload(ctx, expiring, 5); err != nil || !removed {
		t.Fatalf("matching conditional unkill = %v, %v; want true, nil", removed, err)
	}
}

func TestDegradedStoreRejectsOperatorStateChanges(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	values := paths.Values{
		FleetDB: filepath.Join(blocker, "fleet.db"),
		Home:    filepath.Join(root, "home"),
	}
	ctx := context.Background()
	state := OpenSharedState(ctx, values)
	t.Cleanup(func() { _ = state.Close() })

	if state.Degraded() == nil {
		t.Fatal("Degraded() = nil, want the reason the database is unusable")
	}
	if err := state.Kill(ctx, "not-written", 7); err == nil {
		t.Fatal("degraded Kill() reported success")
	}
	if _, err := state.KilledAt(ctx); err == nil {
		t.Fatal("degraded KilledAt() reported an empty set instead of its lookup failure")
	}
}

// child-add / child-list / child-clear, in the shapes chat.sh writes them:
// a bare socket for a detached teammate (chat.sh:1362) and "<socket>\t<pane>"
// for one sharing this chat's server (chat.sh:435).
func TestChildrenRoundTripInCCDBShShapes(t *testing.T) {
	state, _ := openTestStore(t)
	ctx := context.Background()

	if err := state.AddChild(ctx, KindNew, "chat-1", "cc-new-worker", 10); err != nil {
		t.Fatal(err)
	}
	if err := state.AddChild(ctx, KindPane, "chat-1", "cc-1-1-1\t%3", 11); err != nil {
		t.Fatal(err)
	}
	// Repeat registration remains one row.
	if err := state.AddChild(ctx, KindNew, "chat-1", "cc-new-worker", 12); err != nil {
		t.Fatal(err)
	}

	detached, fromTable, err := state.Children(ctx, KindNew, "chat-1")
	if err != nil || !fromTable {
		t.Fatalf("Children(new) fromTable=%v err=%v", fromTable, err)
	}
	if !reflect.DeepEqual(detached, []string{"cc-new-worker"}) {
		t.Fatalf("Children(new) = %v", detached)
	}
	panes, _, err := state.Children(ctx, KindPane, "chat-1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(panes, []string{"cc-1-1-1\t%3"}) {
		t.Fatalf("Children(pane) = %v", panes)
	}

	if err := state.ClearChildren(ctx, KindNew, "chat-1"); err != nil {
		t.Fatal(err)
	}
	detached, _, err = state.Children(ctx, KindNew, "chat-1")
	if err != nil || len(detached) != 0 {
		t.Fatalf("Children(new) after clear = %v, %v", detached, err)
	}
	if panes, _, err = state.Children(ctx, KindPane, "chat-1"); err != nil ||
		len(panes) != 1 {
		t.Fatalf("clearing one kind cleared the other: %v, %v", panes, err)
	}
}

// PrimaryAccount reads the database first and the ~/.claude-primary mirror
// only when the database has no row.
func TestPrimaryAccountPrefersTheDatabaseOverTheMirror(t *testing.T) {
	state, values := openTestStore(t)
	ctx := context.Background()

	if account, found := ClaudePrimaryAccount(ctx, values); found {
		t.Fatalf("PrimaryAccount() with nothing recorded = %d, %v", account, found)
	}

	mirror := filepath.Join(values.Home, ".claude-primary")
	if err := os.MkdirAll(filepath.Dir(mirror), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mirror, []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if account, found := ClaudePrimaryAccount(ctx, values); !found || account != 1 {
		t.Fatalf("PrimaryAccount() from the mirror = %d, %v", account, found)
	}

	if err := state.SetMeta(ctx, PrimaryAccountKey, "2", 99); err != nil {
		t.Fatal(err)
	}
	if account, found := ClaudePrimaryAccount(ctx, values); !found || account != 2 {
		t.Fatalf(
			"PrimaryAccount() = %d, %v; want 2 — the database outranks a stale mirror",
			account,
			found,
		)
	}
}

func TestBranchSeatMarkersRoundTripWithoutChangingTheSchema(t *testing.T) {
	state, _ := openTestStore(t)
	ctx := context.Background()
	const socket = "cc-1800000000-42-7"
	if err := state.RecordBranchSeat(ctx, socket, "parent-id", 123); err != nil {
		t.Fatal(err)
	}
	seats, err := state.BranchSeats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := seats[socket]; got.Socket != socket || got.Parent != "parent-id" || got.CreatedAt != 123 {
		t.Fatalf("branch seat = %#v", got)
	}
	if err := state.ClearBranchSeat(ctx, socket); err != nil {
		t.Fatal(err)
	}
	seats, err = state.BranchSeats(ctx)
	if err != nil || len(seats) != 0 {
		t.Fatalf("branch seats after clear = %#v err=%v", seats, err)
	}
}

// The reader must not be what creates the state store: a missing database is a
// missing database, not an empty one.
func TestPrimaryAccountNeverCreatesTheDatabase(t *testing.T) {
	root := t.TempDir()
	values := paths.Values{
		FleetDB: filepath.Join(root, "state", "fleet.db"),
		Home:    filepath.Join(root, "home"),
	}
	if account, found := ClaudePrimaryAccount(context.Background(), values); found {
		t.Fatalf("PrimaryAccount() = %d, %v, want not found", account, found)
	}
	if _, err := os.Stat(values.FleetDB); !os.IsNotExist(err) {
		t.Fatalf("reading the primary account created %s: %v", values.FleetDB, err)
	}
}

func TestSetPrimaryAccountKeepsDatabaseAndMirrorInLockstep(t *testing.T) {
	root := t.TempDir()
	values := paths.Values{
		Home:    root,
		FleetDB: filepath.Join(root, ".cc", "fleet.db"),
	}
	if err := SetClaudePrimaryAccount(context.Background(), values, 2, 123); err != nil {
		t.Fatal(err)
	}
	if account, found := ClaudePrimaryAccount(context.Background(), values); !found || account != 2 {
		t.Fatalf("PrimaryAccount()=%d,%v, want 2,true", account, found)
	}
	content, err := os.ReadFile(filepath.Join(root, ".claude-primary"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "2\n" {
		t.Fatalf("mirror=%q, want 2\\n", content)
	}
}

func openTestStore(t *testing.T) (*Store, paths.Values) {
	t.Helper()

	root := t.TempDir()
	values := paths.Values{
		FleetDB: filepath.Join(root, "cc", "fleet.db"),
		Home:    filepath.Join(root, "home"),
	}
	state := OpenSharedState(context.Background(), values)
	if err := state.Degraded(); err != nil {
		t.Fatalf("Open() degraded: %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	return state, values
}

func queryColumn(t *testing.T, state *Store, query string) []string {
	t.Helper()

	rows, err := state.db.Query(query)
	if err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("close rows: %v", err)
		}
	}()
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// The v3->v4 migration adds cx_names.source and cx_names.renamed_at without
// losing a pre-migration row, and running it — through the ordinary
// open-time migration gate — is a no-op whether the database starts at v3
// or is already at v4: PRAGMA user_version guards migration_v4.sql from
// ever running twice, which an ALTER TABLE ADD COLUMN cannot survive.
func TestV4MigrationAddsCxNameProvenanceIdempotently(t *testing.T) {
	dbPath := setStoreTestJail(t)
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{schemaV1, schemaV2, schemaV3} {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.ExecContext(ctx, "PRAGMA user_version=3"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(
		ctx,
		"INSERT INTO cx_names (id, thread_name) VALUES (?, ?)",
		"pre-v4",
		"OLD NAME",
	); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	migrated := openFileTestStore(t)
	// The whole chain runs from v3, not just migration_v4.sql in isolation.
	assertSchemaVersion(t, migrated, SchemaVersion)
	records, err := migrated.CxNameRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	record, found := records["pre-v4"]
	if !found ||
		record.ThreadName != "OLD NAME" ||
		record.Source != CxNameSourceSessionIndex ||
		record.RenamedAt != 0 {
		t.Fatalf(
			"pre-v4 row after migration = %#v, found = %t, want the column defaults",
			record,
			found,
		)
	}
	if err := migrated.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening an already-v4 database is the other half of "idempotent":
	// the migration loop must not attempt migration_v4.sql again.
	reopened := openFileTestStore(t)
	t.Cleanup(func() { _ = reopened.Close() })
	assertSchemaVersion(t, reopened, SchemaVersion)
	records, err = reopened.CxNameRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if record := records["pre-v4"]; record.ThreadName != "OLD NAME" {
		t.Fatalf("pre-v4 row after reopen = %#v", record)
	}
}

// TestEnsureOpenCodeSessionsAssistantCountAddsColumnIdempotently builds a database
// at schema 8 the way an older pfm (before assistant_count existed) would
// have left it — the v1..v8 migrations only, no assistant_count — and proves
// the ensure step adds the column on open, round-trips it through
// ReplaceOpenCodeSessions/OpenCodeSessions, and never fails when run twice.
func TestEnsureOpenCodeSessionsAssistantCountAddsColumnIdempotently(t *testing.T) {
	dbPath := setStoreTestJail(t)
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range migrations {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.ExecContext(ctx, "PRAGMA user_version=8"); err != nil {
		t.Fatal(err)
	}
	if hasOpenCodeSessionsAssistantCount(t, database) {
		t.Fatal("fixture already carries assistant_count — it must start without the column")
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	opened := openFileTestStore(t)
	assertSchemaVersion(t, opened, SchemaVersion)
	if !hasOpenCodeSessionsAssistantCount(t, opened.db) {
		t.Fatal("opening a pre-assistant_count schema-8 database did not add the column")
	}
	if err := opened.ReplaceOpenCodeSessions(
		ctx,
		[]OpenCodeSession{{ID: "ses-1", Title: "fixture", AssistantCount: 3}},
	); err != nil {
		t.Fatal(err)
	}
	sessions, err := opened.OpenCodeSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].AssistantCount != 3 {
		t.Fatalf("round-tripped sessions = %#v, want one session with AssistantCount 3", sessions)
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening must not attempt ALTER TABLE a second time — that fails with
	// "duplicate column name" — and the earlier row must survive untouched.
	reopened := openFileTestStore(t)
	t.Cleanup(func() { _ = reopened.Close() })
	assertSchemaVersion(t, reopened, SchemaVersion)
	sessions, err = reopened.OpenCodeSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].AssistantCount != 3 {
		t.Fatalf("round-tripped sessions after reopen = %#v", sessions)
	}
}

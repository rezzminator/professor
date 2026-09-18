package obs

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
)

// TestSQLRecordsShapeNeverText: one comp=db record per statement carrying
// the verb, the table, the kind, rows, retries and dur_ms — while the SQL
// text and every literal in it stay out of the file.
func TestSQLRecordsShapeNeverText(t *testing.T) {
	ctx, recorder := Test(t)
	op := SQL(
		ctx,
		"fleet",
		"INSERT INTO hidden(uuid,hidden_at) VALUES(?, 'sk-PLANTED-literal') ON CONFLICT(uuid) DO UPDATE SET hidden_at=excluded.hidden_at",
	)
	op.Retries = 2
	op.End(1, nil)
	records := recorder.Records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1: %s", len(records), recorder.Raw())
	}
	record := records[0]
	if record.Message != "db.statement" || record.Level != slog.LevelInfo.String() {
		t.Fatalf("record = %s at %s", record.Message, record.Level)
	}
	requireField(t, record, FieldComp, "db")
	requireField(t, record, "op", "insert")
	requireField(t, record, "table", "hidden")
	requireField(t, record, "kind", "fleet")
	requireField(t, record, "rows", float64(1))
	requireField(t, record, "retries", float64(2))
	requireDur(t, record)
	if raw := recorder.Raw(); strings.Contains(raw, "PLANTED") || strings.Contains(raw, "VALUES") ||
		strings.Contains(raw, "excluded") {
		t.Fatalf("SQL text reached the file: %s", raw)
	}
}

// TestSQLShapeOfEveryStatementKind pins the verb/table reading for the
// statement shapes the access layers write.
func TestSQLShapeOfEveryStatementKind(t *testing.T) {
	for _, tc := range []struct{ query, op, table string }{
		{"SELECT value FROM meta WHERE key=?", "select", "meta"},
		{"\n\tSELECT t.uuid, t.name\n\tFROM transcripts t LEFT JOIN hidden h ON h.uuid=t.uuid", "select", "transcripts"},
		{"DELETE FROM transcripts WHERE uuid=?", "delete", "transcripts"},
		{"UPDATE meta SET val=? WHERE key=?", "update", "meta"},
		{"INSERT OR IGNORE INTO cx_names(id) VALUES(?)", "insert", "cx_names"},
		{"CREATE TABLE IF NOT EXISTS issues (id INTEGER)", "create", "issues"},
		{"CREATE INDEX IF NOT EXISTS issues_status ON issues(status)", "create", "issues"},
		{"PRAGMA journal_mode=WAL", "pragma", "journal_mode"},
		{"PRAGMA table_info(threads)", "pragma", "table_info"},
		{"BEGIN IMMEDIATE", "begin", ""},
		{"COMMIT", "commit", ""},
		{"WITH recent AS (SELECT id FROM rollouts) SELECT * FROM recent", "with", "rollouts"},
		{"", "", ""},
	} {
		op, table := statementShape(tc.query)
		if op != tc.op || table != tc.table {
			t.Errorf("%q -> %s %s, want %s %s", tc.query, op, table, tc.op, tc.table)
		}
	}
}

// TestSQLFailureIsAnErrorRecordWithoutRows: a failed statement is ERROR with
// err; unknown rows (a read) omit the field rather than claiming zero.
func TestSQLFailureIsAnErrorRecordWithoutRows(t *testing.T) {
	ctx, recorder := Test(t)
	SQL(ctx, "store", "SELECT 1 FROM transcripts").End(-1, errors.New("database is locked"))
	records := recorder.Records()
	if len(records) != 1 || records[0].Level != slog.LevelError.String() {
		t.Fatalf("want one ERROR record: %s", recorder.Raw())
	}
	if _, found := records[0].Field("rows"); found {
		t.Fatalf("unknown rows were recorded: %v", records[0].Fields)
	}
	if _, found := records[0].Field("retries"); found {
		t.Fatalf("zero retries were recorded as a field: %v", records[0].Fields)
	}
	requireField(t, records[0], FieldErr, "database is locked")
}

// TestSQLOpenRecordsKindPathAndResult: the sqlitedb openers write db.open
// with the database kind and its path, ERROR when the open failed.
func TestSQLOpenRecordsKindPathAndResult(t *testing.T) {
	ctx, recorder := Test(t)
	SQLOpen(ctx, "store", "/home/x/.pfm/pfm.db")(nil)
	SQLOpen(ctx, "readonly", "/state.sqlite")(errors.New("unable to open"))
	records := recorder.Records()
	if len(records) != 2 || records[0].Message != "db.open" {
		t.Fatalf("want two db.open records: %s", recorder.Raw())
	}
	requireField(t, records[0], FieldComp, "db")
	requireField(t, records[0], "kind", "store")
	requireField(t, records[0], "path", "/home/x/.pfm/pfm.db")
	requireDur(t, records[0])
	if records[1].Level != slog.LevelError.String() {
		t.Fatalf("failed open at %s, want ERROR", records[1].Level)
	}
}

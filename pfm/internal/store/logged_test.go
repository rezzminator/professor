package store

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestStoreStatementsRecordUnderTheDBComponent: the transcript store's
// direct statements (SetMeta/Meta/DeleteMeta through the logged *sql.DB) and
// its transactions (WithImmediateTx, plus every statement inside one) each
// write comp=db records with verb, table and rows; the bound value never
// reaches the file.
func TestStoreStatementsRecordUnderTheDBComponent(t *testing.T) {
	ctx, recorder := obs.Test(t)
	store := openTestStore(t)
	const planted = "PLANTED-BOUND-VALUE"
	if err := store.SetMeta(ctx, "lane", planted); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Meta(ctx, "lane"); err != nil {
		t.Fatal(err)
	}
	if err := store.WithImmediateTx(ctx, func(tx *ImmediateTx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM meta WHERE key=?", "lane")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, record := range recorder.Records() {
		if record.Message != "db.statement" {
			continue
		}
		if comp, _ := record.Field(obs.FieldComp); comp != "db" {
			t.Fatalf("comp = %v, want db", comp)
		}
		// The store also opens the shared fleet database; its records are the
		// fleetdb package's proof, not this one.
		if kind, _ := record.Field("kind"); kind != "store" {
			continue
		}
		op, _ := record.Field("op")
		table, _ := record.Field("table") // absent on a transaction record
		counts[fmt.Sprintf("%v %v", op, table)]++
		if _, found := record.Field(obs.FieldDur); !found {
			t.Fatalf("no dur_ms: %v", record.Fields)
		}
	}
	for _, want := range []string{"insert meta", "select meta", "delete meta", "begin <nil>"} {
		if counts[want] == 0 {
			t.Fatalf("no record for %q; saw %v in %s", want, counts, recorder.Raw())
		}
	}
	if strings.Contains(recorder.Raw(), planted) || strings.Contains(recorder.Raw(), "WHERE") {
		t.Fatalf("a bound value or SQL text reached the file: %s", recorder.Raw())
	}
	_ = context.Background
}

// countUserVersionPragmaRecords counts comp=db, kind=store records for
// "PRAGMA user_version" statementShape reads as op=pragma, table=user_version.
func countUserVersionPragmaRecords(recorder *obs.Recorder) int {
	count := 0
	for _, record := range recorder.Records() {
		if record.Message != "db.statement" {
			continue
		}
		if kind, _ := record.Field("kind"); kind != "store" {
			continue
		}
		op, _ := record.Field("op")
		table, _ := record.Field("table")
		if op == "pragma" && table == "user_version" {
			count++
		}
	}
	return count
}

// TestUserVersionRecordsUnderTheDBComponent: UserVersion reads PRAGMA
// user_version through the same logged door as its sibling reads
// (killed.go's queries) — a comp=db record naming the pragma op and
// user_version table, never the raw handle unlogged. Open()'s own migration
// check already writes one such record before this ever runs, so the proof
// is a NEW record appearing, not merely one existing.
func TestUserVersionRecordsUnderTheDBComponent(t *testing.T) {
	ctx, recorder := obs.Test(t)
	store := openTestStore(t)
	before := countUserVersionPragmaRecords(recorder)
	if _, err := store.UserVersion(ctx); err != nil {
		t.Fatal(err)
	}
	if after := countUserVersionPragmaRecords(recorder); after <= before {
		t.Fatalf(
			"UserVersion() wrote no NEW comp=db pragma record: before=%d after=%d: %s",
			before, after, recorder.Raw(),
		)
	}
}

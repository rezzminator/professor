package store

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"hostops/pfm/internal/obs"
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

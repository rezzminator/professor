package fleetdb

import (
	"context"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestStoreOperationsRecordUnderTheDBComponent: every public fleetdb
// operation reaches SQLite through the store's logged statement helpers —
// one comp=db record per statement with the verb, table and rows — and a
// bound value (the chat id) never reaches the file.
// It stays serial: obs.Test swaps the process logger, so it never overlaps
// the package's parallel tests.
func TestStoreOperationsRecordUnderTheDBComponent(t *testing.T) {
	ctx, recorder := obs.Test(t)
	state, _ := openTestStore(t)
	const id = "cccccccc-cccc-4ccc-8ccc-PLANTEDBOUND"
	if err := state.Kill(ctx, id, 1700000000); err != nil {
		t.Fatal(err)
	}
	if _, err := state.KilledAt(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := state.Meta(ctx, "schema_version"); err != nil {
		t.Fatal(err)
	}
	var kill, killed, meta obs.Record
	for _, record := range recorder.Records() {
		if record.Message != "db.statement" {
			continue
		}
		if comp, _ := record.Field(obs.FieldComp); comp != "db" {
			t.Fatalf("comp = %v, want db", comp)
		}
		if kind, _ := record.Field("kind"); kind != "fleet" {
			t.Fatalf("kind = %v, want fleet", kind)
		}
		op, _ := record.Field("op")
		table, _ := record.Field("table")
		switch {
		case op == "insert" && table == "hidden":
			kill = record
		case op == "select" && table == "hidden":
			killed = record
		case op == "select" && table == "meta":
			meta = record
		}
	}
	if kill.Message == "" || killed.Message == "" || meta.Message == "" {
		t.Fatalf(
			"missing statement records (kill=%v killed=%v meta=%v): %s",
			kill.Message != "",
			killed.Message != "",
			meta.Message != "",
			recorder.Raw(),
		)
	}
	if rows, _ := kill.Field("rows"); rows != float64(1) {
		t.Fatalf("kill rows = %v, want 1", rows)
	}
	if _, found := kill.Field(obs.FieldDur); !found {
		t.Fatalf("no dur_ms: %v", kill.Fields)
	}
	if strings.Contains(recorder.Raw(), "PLANTEDBOUND") || strings.Contains(recorder.Raw(), "INSERT") {
		t.Fatalf("a bound value or SQL text reached the file: %s", recorder.Raw())
	}
	_ = context.Background
}

func TestPrimaryAccountAbsenceIsNotAnErrorRecord(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "query failure"}[broken], func(t *testing.T) {
			ctx, recorder := obs.Test(t)
			state, values := openTestStore(t)
			if broken {
				if _, err := state.db.ExecContext(ctx, "DROP TABLE meta"); err != nil {
					t.Fatal(err)
				}
			}
			account, found, err := ClaudePrimaryAccount(ctx, values)
			if found || account != 0 {
				t.Fatalf("account=%d found=%v", account, found)
			}
			if broken {
				if err == nil || !strings.Contains(err.Error(), "query primary account:") {
					t.Fatalf("err=%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			var records []obs.Record
			for _, record := range recorder.Records() {
				op, _ := record.Field("op")
				table, _ := record.Field("table")
				if record.Message == "db.statement" && op == "select" && table == "meta" {
					records = append(records, record)
				}
			}
			if len(records) != 1 {
				t.Fatalf("records=%v: %s", records, recorder.Raw())
			}
			want := "INFO"
			if broken {
				want = "ERROR"
			}
			if records[0].Level != want {
				t.Fatalf("level=%s want=%s", records[0].Level, want)
			}
			value, hasErr := records[0].Field(obs.FieldErr)
			if broken && (!hasErr || value == "") {
				t.Fatalf("missing err: %+v", records[0])
			}
		})
	}
}

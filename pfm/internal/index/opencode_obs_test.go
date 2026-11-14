package index

import (
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestReadOpenCodeSessionsRecordsTheForeignRead: OpenCode's store is read
// through the db door — a kind=opencode select over its tables, then one over sessions.
func TestReadOpenCodeSessionsRecordsTheForeignRead(t *testing.T) {
	ctx, recorder := obs.Test(t)
	root := t.TempDir()
	seedOpenCodeStore(t, root)
	if _, err := ReadOpenCodeSessions(ctx, root); err != nil {
		t.Fatal(err)
	}
	var ops []any
	for _, record := range recorder.Records() {
		kind, _ := record.Field("kind")
		if record.Message != "db.statement" || kind != "opencode" {
			continue
		}
		if comp, _ := record.Field(obs.FieldComp); comp != "db" {
			t.Fatalf("comp = %v", comp)
		}
		op, _ := record.Field("op")
		ops = append(ops, op)
		if _, ok := record.Field(obs.FieldDur); !ok {
			t.Fatalf("no dur_ms: %v", record.Fields)
		}
	}
	// The table check selects from sqlite_master; openCodeSessionsQuery is a CTE: its verb is `with`.
	if want := []any{"select", "with"}; !slices.Equal(ops, want) {
		t.Fatalf("opencode record ops = %v, want %v: %s", ops, want, recorder.Raw())
	}
}

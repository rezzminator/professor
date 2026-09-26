package index

import (
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestReadOpenCodeSessionsRecordsTheForeignRead: OpenCode's store is read
// through the db door — one kind=opencode select record over sessions.
func TestReadOpenCodeSessionsRecordsTheForeignRead(t *testing.T) {
	ctx, recorder := obs.Test(t)
	root := t.TempDir()
	seedOpenCodeStore(t, root)
	if _, err := ReadOpenCodeSessions(ctx, root); err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, record := range recorder.Records() {
		kind, _ := record.Field("kind")
		if record.Message != "db.statement" || kind != "opencode" {
			continue
		}
		if comp, _ := record.Field(obs.FieldComp); comp != "db" {
			t.Fatalf("comp = %v", comp)
		}
		// openCodeSessionsQuery is a CTE: its verb is `with`.
		if op, _ := record.Field("op"); op != "with" {
			t.Fatalf("op = %v, want with", op)
		}
		if _, ok := record.Field(obs.FieldDur); !ok {
			t.Fatalf("no dur_ms: %v", record.Fields)
		}
		found++
	}
	if found != 1 {
		t.Fatalf("opencode records = %d, want 1: %s", found, recorder.Raw())
	}
}

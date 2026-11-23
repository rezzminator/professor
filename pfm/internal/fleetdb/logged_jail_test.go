//go:build jail

package fleetdb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

func TestPrimaryAccountAbsenceIsNotAnErrorRecord(t *testing.T) {
	for _, mode := range []string{"absent", "uncreated", "query failure"} {
		broken := mode == "query failure"
		t.Run(mode, func(t *testing.T) {
			ctx, recorder := obs.Test(t)
			state, values := openFileTestStore(t)
			if broken {
				if _, err := state.db.ExecContext(ctx, "DROP TABLE meta"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "uncreated" {
				// Another process has created the file and not yet its schema.
				values.StateDB = filepath.Join(t.TempDir(), "pfm.db")
				if err := os.WriteFile(values.StateDB, nil, 0o600); err != nil {
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
			// A database not yet given its schema is read by the schema look
			// alone: it holds no meta table to select from.
			want := map[bool]string{false: "meta", true: "sqlite_master"}[mode == "uncreated"]
			var records []obs.Record
			for _, record := range recorder.Records() {
				op, _ := record.Field("op")
				table, _ := record.Field("table")
				if record.Message == "db.statement" && op == "select" && table == want {
					records = append(records, record)
				}
			}
			if len(records) != 1 {
				t.Fatalf("records=%v: %s", records, recorder.Raw())
			}
			level := "DEBUG" // a fast first-try success is routine; only a failure is louder
			if broken {
				level = "ERROR"
			}
			if records[0].Level != level {
				t.Fatalf("level=%s want=%s", records[0].Level, level)
			}
			value, hasErr := records[0].Field(obs.FieldErr)
			if broken && (!hasErr || value == "") {
				t.Fatalf("missing err: %+v", records[0])
			}
		})
	}
}

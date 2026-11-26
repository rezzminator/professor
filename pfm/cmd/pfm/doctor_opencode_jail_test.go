//go:build jail

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestDoctorReadsTheOpenCodeStoreSchema: doctor names a store holding neither
// schema's tables unhealthy, naming what each lacks, and reads a 2.x store healthy.
func TestDoctorReadsTheOpenCodeStoreSchema(t *testing.T) {
	for _, tc := range []struct {
		name, schema, row string
		code              int
	}{
		{
			name:   "neither schema",
			schema: `CREATE TABLE session (id TEXT PRIMARY KEY)`,
			row: "doctor: opencode store=unhealthy account=1 error=opencode store holds tables [session] but lacks " +
				"1.x tables [message part project] and 2.x tables [project session_message session_part session_v2]\n",
			code: 1,
		},
		{
			name: "2.x store",
			schema: `CREATE TABLE project (id TEXT PRIMARY KEY, worktree TEXT NOT NULL);
CREATE TABLE session_v2 (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, parent_id TEXT, title TEXT,
  directory TEXT NOT NULL, agent TEXT, model TEXT, time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL, time_archived INTEGER);
CREATE TABLE session_message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, type TEXT NOT NULL,
  seq INTEGER NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL);
CREATE TABLE session_part (message_id TEXT NOT NULL, session_id TEXT NOT NULL, ordinal INTEGER NOT NULL,
  type TEXT NOT NULL, call_id TEXT, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL,
  data TEXT NOT NULL);`,
			row:  "doctor: opencode store=healthy account=1\n",
			code: -1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testjail.PinClaudeAsk(t, filepath.Join(jailTest(t), "home"))
			resolved, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			openCodeRoot := resolved.Roots[pfmengine.OpenCode][0]
			if err := os.MkdirAll(openCodeRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			db, err := sqlitedb.OpenReadWrite(filepath.Join(openCodeRoot, "opencode.db"), 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(tc.schema); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}

			var stdout, stderr bytes.Buffer
			code := run([]string{"doctor"}, &stdout, &stderr)
			rows := strings.SplitAfter(stdout.String(), "\n")
			if (tc.code >= 0 && code != tc.code) || !slices.Contains(rows, tc.row) {
				t.Fatalf("doctor code=%d stdout=%q stderr=%q, want row %q",
					code, stdout.String(), stderr.String(), tc.row)
			}
		})
	}
}

package main

import (
	"bytes"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestDoctorRejectsAnUnreadableOpenCodeSchema(t *testing.T) {
	jailTest(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	openCodeRoot := resolved.Roots[pfmengine.OpenCode][0]
	if err := os.MkdirAll(openCodeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(openCodeRoot, "opencode.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE session (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"doctor", "--skip-harvest"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stdout.String(), "doctor: opencode store=unhealthy") ||
		!strings.Contains(stdout.String(), "query opencode sessions") {
		t.Fatalf("doctor code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

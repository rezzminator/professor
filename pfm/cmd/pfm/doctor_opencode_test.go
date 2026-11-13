package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestDoctorRejectsAnUnreadableOpenCodeSchema(t *testing.T) {
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
	if _, err := db.Exec(`CREATE TABLE session (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"doctor"}, &stdout, &stderr)
	row := "doctor: opencode store=unhealthy account=1 " +
		"error=opencode store holds tables [session] but not [message part project]\n"
	if code != 1 || !strings.Contains(stdout.String(), row) {
		t.Fatalf("doctor code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

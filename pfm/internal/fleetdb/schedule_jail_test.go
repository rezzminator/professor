package fleetdb

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestSharedMigrationFromV2AddsSchedulesAndBacksUp(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	testjail.DatabaseFiles(t, root)
	path := filepath.Join(root, "pfm.db")
	ctx := context.Background()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL;"+schemaDDL+migrationV2+
		`INSERT INTO launch VALUES('keep','claude',1,0,5,5); PRAGMA user_version=2;`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	state := OpenSharedState(ctx, paths.Values{StateDB: path})
	if err := state.Degraded(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	var version, count int
	if err := state.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatalf("version=%d err=%v", version, err)
	}
	keepRow := state.db.QueryRow("SELECT count(*) FROM launch WHERE session_id='keep'")
	if err := keepRow.Scan(&count); err != nil || count != 1 {
		t.Fatalf("launch row=%d err=%v", count, err)
	}
	if _, err := state.CreateSchedule(ctx, newTestSchedule("s", time.Unix(1, 0), time.Hour)); err != nil {
		t.Fatalf("CreateSchedule() after migration = %v", err)
	}
	if _, err := os.Stat(path + ".bak-before-v3"); err != nil {
		t.Fatalf("v2 database was not backed up before v3: %v", err)
	}
}

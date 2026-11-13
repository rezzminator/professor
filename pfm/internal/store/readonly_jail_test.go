package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

func setReadOnlyTestVersion(t *testing.T, path string, version int) {
	t.Helper()
	db, err := sqlitedb.OpenReadWrite(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version=%d", version)); err != nil {
		t.Fatal(errors.Join(err, db.Close()))
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenReadOnlyNeverCreatesOrMigratesTheCache(t *testing.T) {
	path := setStoreTestJail(t)
	ctx := context.Background()
	if database, err := OpenWithoutMigrating(ctx); database != nil || !errors.Is(err, fleetdb.ErrAbsent) {
		t.Fatalf("absent cache: store=%v err=%v, want ErrAbsent", database, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a read-only open created %s (stat err=%v)", path, err)
	}

	database, err := Open(WithWarningWriter(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Kill(ctx, Killed{ID: "read-only-kill", KilledAt: 3}); err != nil {
		t.Fatal(errors.Join(err, database.Close()))
	}
	statePath := database.SharedPath()
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	// Current schema: every doctor read works on the read-only handles,
	// including the kill census that fills its temp mirror.
	database, err = OpenWithoutMigrating(ctx)
	if err != nil {
		t.Fatal(err)
	}
	counts, err := database.Counts(ctx)
	if err != nil || counts.Killed != 1 {
		t.Fatalf("read-only counts=%+v err=%v", counts, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	setReadOnlyTestVersion(t, path, SchemaVersion-1)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	database, err = OpenWithoutMigrating(ctx)
	var mismatch *fleetdb.SchemaMismatchError
	if database != nil || !errors.As(err, &mismatch) || mismatch.Name != "cache" ||
		mismatch.Found != SchemaVersion-1 || mismatch.Expected != SchemaVersion {
		t.Fatalf("older cache: store=%v err=%v, want a cache schema mismatch", database, err)
	}
	if after, err := os.ReadFile(path); err != nil || !bytes.Equal(after, before) {
		t.Fatalf("a read-only open changed the older cache (err=%v)", err)
	}

	// The shared half's mismatch rides SharedDegraded beside a readable cache.
	setReadOnlyTestVersion(t, path, SchemaVersion)
	setReadOnlyTestVersion(t, statePath, fleetdb.SchemaVersion-1)
	database, err = OpenWithoutMigrating(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.As(database.SharedDegraded(), &mismatch) || mismatch.Name != "state" {
		t.Fatalf("older state: SharedDegraded=%v, want a state schema mismatch", database.SharedDegraded())
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}

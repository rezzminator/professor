package fleetdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"

	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// ErrAbsent is a database a read-only open found missing or empty: a
// diagnostic never creates the file it inspects.
var ErrAbsent = errors.New("database absent")

// SchemaMismatchError is a database a read-only open found at a schema
// version other than this build's: an older file the first read-write open of
// this build would migrate, or a newer one a newer pfm wrote. A diagnostic
// reads neither, so its rows say they could not look.
type SchemaMismatchError struct {
	Name     string // "state" or "cache"
	Path     string
	Found    int
	Expected int
}

func (e *SchemaMismatchError) Error() string {
	return fmt.Sprintf(
		"could not look: %s database %s schema v%d, this build expects v%d", e.Name, e.Path, e.Found, e.Expected,
	)
}

// Newer reports whether a newer pfm wrote the database.
func (e *SchemaMismatchError) Newer() bool { return e.Found > e.Expected }

// OpenReadOnlyAtSchema opens an existing pfm database read-only (mode=ro) for
// a diagnostic: it never creates, initializes or migrates the file, so a newer
// build's doctor leaves an older installed pfm the database it can still
// read. A missing or empty file is ErrAbsent; a file at another schema version
// is a *SchemaMismatchError, its handle closed.
func OpenReadOnlyAtSchema(ctx context.Context, name, path string, expected int) (*sql.DB, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) || err == nil && info.Size() == 0 {
		return nil, fmt.Errorf("%s database %s: %w", name, path, ErrAbsent)
	}
	if err != nil {
		return nil, fmt.Errorf("inspect %s database %s: %w", name, path, err)
	}
	db, err := sqlitedb.OpenReadOnly(path, sqlitedb.StoreBusyTimeout)
	if err != nil {
		return nil, fmt.Errorf("open %s database %s read-only: %w", name, path, err)
	}
	const versionQuery = "PRAGMA user_version"
	var version int
	read := obs.SQL(ctx, kind, versionQuery)
	err = db.QueryRowContext(ctx, versionQuery).Scan(&version)
	read.End(-1, err)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("read %s database %s schema version: %w", name, path, err), db.Close())
	}
	if version != expected {
		mismatch := &SchemaMismatchError{Name: name, Path: path, Found: version, Expected: expected}
		if err := db.Close(); err != nil {
			return nil, errors.Join(mismatch, fmt.Errorf("close %s database %s: %w", name, path, err))
		}
		return nil, mismatch
	}
	return db, nil
}

// OpenSharedStateReadOnly is OpenSharedState for a diagnostic (pfm doctor):
// it opens an existing state file read-only and never creates, initializes or
// migrates it. Degraded carries ErrAbsent for a missing file — whose
// KilledRecords read empty, as a file never written holds no kills — and a
// *SchemaMismatchError for a file at another schema version.
func OpenSharedStateReadOnly(ctx context.Context, values paths.Values) *Store {
	store := &Store{path: values.StateDB}
	if err := CheckLegacyState(values); err != nil {
		store.degraded = err
		return store
	}
	db, err := OpenReadOnlyAtSchema(ctx, "state", values.StateDB, SchemaVersion)
	if err != nil {
		store.degraded = err
		return store
	}
	store.db = db
	return store
}

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

// OpenSharedState records a database initialization failure in Degraded. Operations then
// return that failure instead of pretending an operator decision was stored.
func OpenSharedState(ctx context.Context, values paths.Values) *Store {
	store := &Store{
		path: values.StateDB,
	}
	db, err := openDatabase(ctx, values.StateDB)
	if err != nil {
		store.degraded = err
		return store
	}
	store.db = db
	return store
}

func openDatabase(ctx context.Context, path string) (*sql.DB, error) {
	info, statErr := os.Stat(path)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect shared state database %s: %w", path, statErr)
	}
	existed := statErr == nil && info.Size() > 0
	db, err := sqlitedb.OpenStore(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("open shared state database: %w", err)
	}
	return initDatabase(ctx, db, path, existed)
}

// OpenSharedStateMemory is OpenSharedState over a new in-memory database
// (sqlitedb.OpenStoreMemory), initialized and migrated by the same steps: the shared
// half of store.OpenMemory. Its seat run recipes, which are files, are refused.
func OpenSharedStateMemory(ctx context.Context) *Store {
	store := &Store{}
	db, path, err := sqlitedb.OpenStoreMemory(ctx)
	if err != nil {
		store.degraded = fmt.Errorf("open shared state database: %w", err)
		return store
	}
	store.path = path
	if store.db, err = initDatabase(ctx, db, path, false); err != nil {
		store.degraded = err
	}
	return store
}

// initDatabase brings an open shared database to this binary's schema; a
// failure closes db.
func initDatabase(ctx context.Context, db *sql.DB, path string, existed bool) (*sql.DB, error) {
	schema := obs.SQL(ctx, kind, schemaDDL)
	_, err := db.ExecContext(ctx, schemaDDL)
	schema.End(-1, err)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize shared state schema: %w", err)
	}
	if err := migrate(ctx, db, path, existed); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate shared state database: %w", err)
	}
	if err := addHiddenRevision(ctx, db, path); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate shared state database: %w", err)
	}
	if err := renameRemindersTable(ctx, db, path); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate shared state database: %w", err)
	}
	// The two cleanups below keep their rows when they fail and retry at the
	// next open: retired or stale rows never cost the store.
	if err := dropRetiredIssuesTable(ctx, db, path); err != nil {
		obs.Logger(ctx).Warn("fleet.issues.kept", "path", path, obs.FieldErr, err.Error())
	}
	if err := addScheduleOnce(ctx, db, path); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate shared state database: %w", err)
	}
	if err := addSeatOwnership(ctx, db, path); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate seat ownership: %w", err)
	}
	if err := retireMovedTmuxSeats(ctx, db, path); err != nil {
		obs.Logger(ctx).Warn("fleet.seats.repair_deferred", "path", path, obs.FieldErr, err.Error())
	}
	return db, nil
}

// UserVersion reports the shared database's PRAGMA user_version, the schema
// version its last migration stamped.
func (s *Store) UserVersion(ctx context.Context) (int, error) {
	if s.db == nil {
		return 0, fmt.Errorf("read shared database schema version: %w", s.degraded)
	}
	var version int
	if err := s.queryRow(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("read shared database schema version: %w", err)
	}
	return version, nil
}

package fleetdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"

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

// schemasBrought holds the schemaFingerprint of every shared database this
// process brought to schema, read after its steps and holding no retired
// issues table. The steps of
// bringToSchema decide by user_version and the schema alone, so a database
// whose fingerprint is held here needs none of them; the one write skipped
// with them is schemaDDL's meta row, present since the database was created.
var schemasBrought sync.Map

// schemaFingerprintQuery reads user_version and every schema object as one
// text, each field quoted and the fields and rows separated, so two different
// schemas never read alike; in the same read, whether the retired issues table
// is there.
const schemaFingerprintQuery = `SELECT (SELECT user_version FROM pragma_user_version)||
 COALESCE((SELECT group_concat(quote(type)||','||quote(name)||','||quote(tbl_name)||','||quote(sql),';') FROM sqlite_master),''),
 EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='issues')`

// schemaFingerprint reads the database's fingerprint and whether it still
// holds the retired issues table, from one snapshot.
func schemaFingerprint(ctx context.Context, db *sql.DB) (fingerprint string, retiredIssues bool, err error) {
	op := obs.SQL(ctx, kind, schemaFingerprintQuery)
	err = db.QueryRowContext(ctx, schemaFingerprintQuery).Scan(&fingerprint, &retiredIssues)
	op.End(-1, err)
	if err != nil {
		return "", false, fmt.Errorf("read shared database schema: %w", err)
	}
	return fingerprint, retiredIssues, nil
}

// initDatabase brings an open shared database to this binary's schema, once
// per process for each schema it meets: a later open of a database already
// brought here reads its fingerprint and runs no schema step. A failure
// closes db.
func initDatabase(ctx context.Context, db *sql.DB, path string, existed bool) (*sql.DB, error) {
	fingerprint, _, err := schemaFingerprint(ctx, db)
	if err != nil {
		// No match: the steps below meet the same database and name its failure.
		obs.Logger(ctx).Warn("fleet.schema.unread", "path", path, obs.FieldErr, err.Error())
	}
	if _, brought := schemasBrought.Load(fingerprint); err != nil || !brought {
		if err := bringToSchema(ctx, db, path, existed); err != nil {
			_ = db.Close()
			return nil, err
		}
		// Read again, the fingerprint describes the schema the steps left. One
		// still holding the issues table is never noted: its drop failed, or an
		// older pfm's schema script made the table again since, and the drop
		// retries at the next open.
		if fingerprint, retiredIssues, err := schemaFingerprint(ctx, db); err != nil {
			obs.Logger(ctx).Warn("fleet.schema.unread", "path", path, obs.FieldErr, err.Error())
		} else if !retiredIssues {
			schemasBrought.Store(fingerprint, struct{}{})
		}
	}
	// The repair is of rows, not schema: every open runs it. It keeps its rows
	// when it fails and retries at the next open; stale rows never cost the store.
	if err := retireMovedTmuxSeats(ctx, db, path); err != nil {
		obs.Logger(ctx).Warn("fleet.seats.repair_deferred", "path", path, obs.FieldErr, err.Error())
	}
	return db, nil
}

// bringToSchema runs every step that takes a shared database to this binary's
// schema. The retired issues table's drop keeps its rows when it fails and
// retries at the next open, never costing the store.
func bringToSchema(ctx context.Context, db *sql.DB, path string, existed bool) error {
	schema := obs.SQL(ctx, kind, schemaDDL)
	_, err := db.ExecContext(ctx, schemaDDL)
	schema.End(-1, err)
	if err != nil {
		return fmt.Errorf("initialize shared state schema: %w", err)
	}
	if err := migrate(ctx, db, path, existed); err != nil {
		return fmt.Errorf("migrate shared state database: %w", err)
	}
	if err := addHiddenRevision(ctx, db, path); err != nil {
		return fmt.Errorf("migrate shared state database: %w", err)
	}
	if err := renameRemindersTable(ctx, db, path); err != nil {
		return fmt.Errorf("migrate shared state database: %w", err)
	}
	if err := dropRetiredIssuesTable(ctx, db, path); err != nil {
		obs.Logger(ctx).Warn("fleet.issues.kept", "path", path, obs.FieldErr, err.Error())
	}
	if err := addScheduleOnce(ctx, db, path); err != nil {
		return fmt.Errorf("migrate shared state database: %w", err)
	}
	if err := addSeatOwnership(ctx, db, path); err != nil {
		return fmt.Errorf("migrate seat ownership: %w", err)
	}
	return nil
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

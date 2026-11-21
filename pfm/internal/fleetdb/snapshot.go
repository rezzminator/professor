package fleetdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"

	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// SharedStateRestore is what RestoreSharedState did: Restored false left the
// database as it was; true took it from schema From back to To, the database
// it replaced kept at Kept.
type SharedStateRestore struct {
	Restored bool
	From, To int
	Kept     string
}

// SnapshotSharedState copies the shared database at path to target, so a later
// RestoreSharedState can undo a migration another build makes. No database
// at path (or no path) copies nothing and returns "".
func SnapshotSharedState(ctx context.Context, path, target string) (written string, returnErr error) {
	if path == "" {
		return "", nil
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) || (err == nil && info.Size() == 0) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("snapshot shared database %s: %w", path, err)
	}
	db, err := openSharedStateRaw(ctx, path)
	if err != nil {
		return "", err
	}
	defer func() { returnErr = errors.Join(returnErr, closeSharedStateRaw(db, path)) }()
	return backupBeside(ctx, db, path, target)
}

// RestoreSharedState returns the shared database at path to snapshot when a
// newer build migrated it past SchemaVersion, which this binary's opens refuse;
// the migrated database is first kept at {path}.rolled-back-v{its version}.
// A database at or below SchemaVersion, or none at path, is left as it is.
func RestoreSharedState(ctx context.Context, path, snapshot string) (result SharedStateRestore, returnErr error) {
	if path == "" {
		return result, nil
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return result, nil
	} else if err != nil {
		return result, fmt.Errorf("inspect shared database %s: %w", path, err)
	}
	db, err := openSharedStateRaw(ctx, path)
	if err != nil {
		return result, err
	}
	defer func() { returnErr = errors.Join(returnErr, closeSharedStateRaw(db, path)) }()
	// An opener of this binary migrating under the lock meanwhile would read the half-restored version.
	unlock, err := LockMigration(ctx, path)
	if err != nil {
		return result, err
	}
	defer func() { returnErr = errors.Join(returnErr, unlock()) }()
	version, err := sharedStateVersion(ctx, db, path)
	if err != nil || version <= SchemaVersion {
		return result, err
	}
	if snapshot == "" {
		return result, fmt.Errorf(
			"shared database %s is at schema version %d, newer than this binary's %d, "+
				"and has no copy from before the update: move it aside and pfm creates a new one",
			path, version, SchemaVersion,
		)
	}
	kept, err := backupBeside(ctx, db, path, fmt.Sprintf("%s.rolled-back-v%d", path, version))
	if err != nil {
		return result, err
	}
	if err := sqlitedb.RestoreFrom(ctx, db, snapshot); err != nil {
		return result, fmt.Errorf("restore shared database %s (its migrated copy is kept at %s): %w", path, kept, err)
	}
	restored, err := sharedStateVersion(ctx, db, path)
	if err != nil {
		return result, err
	}
	if restored > SchemaVersion {
		return result, fmt.Errorf(
			"shared database %s restored from %s is at schema version %d, still newer than this binary's %d",
			path, snapshot, restored, SchemaVersion,
		)
	}
	return SharedStateRestore{Restored: true, From: version, To: restored, Kept: kept}, nil
}

// openSharedStateRaw opens the shared database without bringing it to schema,
// so a database a newer build stamped opens to be copied or restored.
func openSharedStateRaw(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sqlitedb.OpenStore(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("open shared database %s: %w", path, err)
	}
	return db, nil
}

func closeSharedStateRaw(db *sql.DB, path string) error {
	if err := db.Close(); err != nil {
		return fmt.Errorf("close shared database %s: %w", path, err)
	}
	return nil
}

func sharedStateVersion(ctx context.Context, db *sql.DB, path string) (int, error) {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("read shared database %s schema version: %w", path, err)
	}
	return version, nil
}

package fleetdb

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
)

const SchemaVersion = 3

//go:embed migration_v2.sql
var migrationV2 string

//go:embed migration_v3.sql
var migrationV3 string

// sharedMigrations is indexed by the version each step produces; version 1 is
// schemaDDL itself, so the first two slots stay empty.
var sharedMigrations = [...]string{2: migrationV2, 3: migrationV3}

func migrate(ctx context.Context, db *sql.DB, path string, existed bool) (returnErr error) {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read shared database schema version: %w", err)
	}
	if version < SchemaVersion {
		unlock, err := LockMigration(ctx, path)
		if err != nil {
			return err
		}
		defer func() { returnErr = errors.Join(returnErr, unlock()) }()
		if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
			return fmt.Errorf("re-read shared database schema version: %w", err)
		}
	}
	if version > SchemaVersion {
		return fmt.Errorf(
			"shared database %s schema version %d is newer than binary version %d",
			path,
			version,
			SchemaVersion,
		)
	}
	if version == SchemaVersion {
		return nil
	}
	first := max(version+1, 2)
	if err := BackupBeforeMigration(ctx, db, path, first, existed); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin shared database migration: %w", err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			returnErr = errors.Join(returnErr, fmt.Errorf("roll back shared database migration: %w", err))
		}
	}()
	for next := first; next <= SchemaVersion; next++ {
		if _, err := tx.ExecContext(ctx, sharedMigrations[next]); err != nil {
			return fmt.Errorf("apply shared database migration %d: %w", next, err)
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", SchemaVersion)); err != nil {
		return fmt.Errorf("set shared database schema version %d: %w", SchemaVersion, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit shared database migration %d: %w", SchemaVersion, err)
	}
	return nil
}

// LockMigration serializes one database file's schema upgrade across
// processes. After an upgrade every hook, statusline and picker opens the
// store at once; each must re-read user_version under this lock, or a second
// opener re-applies migrations the first one already committed. The returned
// func releases the lock.
func LockMigration(ctx context.Context, path string) (func() error, error) {
	lockPath := path + ".migrate.lock"
	for {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, fmt.Errorf("open migration lock %s: %w", lockPath, err)
		}
		if err := flockWait(ctx, file, lockPath); err != nil {
			return nil, errors.Join(err, file.Close())
		}
		// The holder removes the file on release, so a lock taken on an
		// already-unlinked inode guards nothing: reopen and try again.
		held, heldErr := file.Stat()
		named, namedErr := os.Stat(lockPath)
		if heldErr == nil && namedErr == nil && os.SameFile(held, named) {
			return func() error {
				removeErr := os.Remove(lockPath)
				if removeErr != nil {
					removeErr = fmt.Errorf("remove migration lock %s: %w", lockPath, removeErr)
				}
				unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				if unlockErr != nil {
					unlockErr = fmt.Errorf("unlock migration %s: %w", lockPath, unlockErr)
				}
				return errors.Join(removeErr, unlockErr, file.Close())
			}, nil
		}
		if heldErr != nil || (namedErr != nil && !errors.Is(namedErr, os.ErrNotExist)) {
			return nil, errors.Join(
				fmt.Errorf("inspect migration lock %s: %w", lockPath, errors.Join(heldErr, namedErr)),
				file.Close(),
			)
		}
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("close stale migration lock %s: %w", lockPath, err)
		}
	}
}

func flockWait(ctx context.Context, file *os.File, lockPath string) error {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("lock migration %s: %w", lockPath, err)
		}
		if err := clock.Real.Sleep(ctx, 10*time.Millisecond); err != nil {
			return fmt.Errorf("wait for migration lock %s: %w", lockPath, err)
		}
	}
}

// BackupBeforeMigration checkpoints WAL and copies the database before its
// first pending migration. An existing backup is preserved with a suffix.
func BackupBeforeMigration(
	ctx context.Context, db *sql.DB, path string, next int, existed bool,
) (returnErr error) {
	if !existed {
		return nil
	}
	var busy, log, checkpointed int
	if err := db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &checkpointed); err != nil {
		return fmt.Errorf("backup %s: checkpoint WAL: %w", path, err)
	}
	if busy != 0 {
		return fmt.Errorf("backup %s: WAL checkpoint busy", path)
	}
	// Keep other writers from checkpointing into the main file while it is
	// copied. The checkpoint precedes this lock so it can finish normally.
	if _, err := db.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("backup %s: lock database: %w", path, err)
	}
	defer func() {
		if _, err := db.ExecContext(context.Background(), "ROLLBACK"); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("backup %s: release lock: %w", path, err))
		}
	}()
	base := fmt.Sprintf("%s.bak-before-v%d", path, next)
	target := base
	for suffix := 1; ; suffix++ {
		info, err := os.Stat(target)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return fmt.Errorf("backup %s: inspect %s: %w", path, target, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("backup %s: %s is not a regular file", path, target)
		}
		target = fmt.Sprintf("%s.%d", base, suffix)
	}
	source, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("backup %s: open source: %w", path, err)
	}
	defer func() {
		if err := source.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("backup %s: close source: %w", path, err))
		}
	}()
	// The source is stable after the checkpoint while this migration owns the
	// store connection; WriteFrom publishes an atomic copy beside it.
	if _, err := atomicfile.WriteFrom(target, source, 0o600, 0); err != nil {
		return fmt.Errorf("backup %s to %s: %w", path, target, err)
	}
	return nil
}

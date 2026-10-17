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

// SchemaVersion is the user_version this build stamps on the shared database.
// Every pfm refuses a file stamped newer than its own, so a column an older
// pfm's statements survive (they name their columns, and its default fills
// their inserts) is added at open with no bump: addColumnAtOpen; a table
// rename likewise: renameRemindersTable.
const SchemaVersion = 5

// RetiredSchemaVersion reserves the version stamped by a develop build that
// added hidden.revision. The column is now added at open; migration 5 follows
// both version 3 and those version-4 files.
const RetiredSchemaVersion = 4

//go:embed migration_v2.sql
var migrationV2 string

//go:embed migration_v3.sql
var migrationV3 string

//go:embed migration_v5.sql
var migrationV5 string

// sharedMigrations is indexed by the version each step produces; version 1 is
// schemaDDL itself, so the first two slots stay empty.
var sharedMigrations = [...]string{2: migrationV2, 3: migrationV3, 5: migrationV5}

// hiddenRevisionDDL adds the column KillWithRevision decides rollback
// ownership by. A version-3 pfm's inserts take its default.
const hiddenRevisionDDL = "ALTER TABLE hidden ADD COLUMN revision TEXT NOT NULL DEFAULT ''"

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
	if first == RetiredSchemaVersion {
		first++
	}
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
		if next == RetiredSchemaVersion {
			continue
		}
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

// addHiddenRevision adds hidden.revision to a file that lacks it: one created
// or migrated here, or one a version-3 pfm left.
func addHiddenRevision(ctx context.Context, db *sql.DB, path string) error {
	return addColumnAtOpen(ctx, db, path, "hidden", "revision", hiddenRevisionDDL)
}

// scheduleOnceDDL adds the column that marks a one-time schedule: it fires once
// and is then kept, fired, until its chat is opened. A row written before it
// existed takes its default and stays recurring.
const scheduleOnceDDL = "ALTER TABLE schedules ADD COLUMN once INTEGER NOT NULL DEFAULT 0 CHECK(once IN (0,1))"

// addScheduleOnce adds schedules.once to a file that lacks it.
func addScheduleOnce(ctx context.Context, db *sql.DB, path string) error {
	return addColumnAtOpen(ctx, db, path, "schedules", "once", scheduleOnceDDL)
}

// addColumnAtOpen runs ddl, which adds column to table, on a file that lacks
// the column. It re-reads the columns under the migration lock, or two first
// openers would both add it; a file that has it opens without taking the lock.
func addColumnAtOpen(ctx context.Context, db *sql.DB, path, table, column, ddl string) (returnErr error) {
	present, err := hasColumn(ctx, db, table, column)
	if err != nil || present {
		return err
	}
	unlock, err := LockMigration(ctx, path)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, unlock()) }()
	if present, err := hasColumn(ctx, db, table, column); err != nil || present {
		return err
	}
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("add %s.%s to shared database %s: %w", table, column, path, err)
	}
	return nil
}

func hasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	var count int
	if err := db.QueryRowContext(
		ctx, "SELECT count(*) FROM pragma_table_info(?) WHERE name=?", table, column,
	).Scan(&count); err != nil {
		return false, fmt.Errorf("read shared database %s columns: %w", table, err)
	}
	return count == 1, nil
}

// schedulesRenameDDL renames the table migration 3 creates as reminders, with
// its indexes, to the schedules name every statement here uses. It runs at
// open with no version bump, like hidden.revision, so a version-3 pfm still
// opens the file; only its own reminder statements stop finding their table.
const schedulesRenameDDL = `
ALTER TABLE reminders RENAME TO schedules;
DROP INDEX IF EXISTS reminders_next_fire;
DROP INDEX IF EXISTS reminders_session;
CREATE INDEX IF NOT EXISTS schedules_next_fire ON schedules(next_fire_ns);
CREATE INDEX IF NOT EXISTS schedules_session ON schedules(session_id);`

// renameRemindersTable moves a file's reminders table to schedules. It re-reads
// the tables under the migration lock, or two first openers would both rename;
// a file already holding schedules opens without taking the lock.
func renameRemindersTable(ctx context.Context, db *sql.DB, path string) (returnErr error) {
	pending, err := hasRemindersTable(ctx, db)
	if err != nil || !pending {
		return err
	}
	unlock, err := LockMigration(ctx, path)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, unlock()) }()
	if pending, err := hasRemindersTable(ctx, db); err != nil || !pending {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reminders-to-schedules rename on shared database %s: %w", path, err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			returnErr = errors.Join(returnErr, fmt.Errorf("roll back reminders-to-schedules rename: %w", err))
		}
	}()
	if _, err := tx.ExecContext(ctx, schedulesRenameDDL); err != nil {
		return fmt.Errorf("rename reminders to schedules on shared database %s: %w", path, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reminders-to-schedules rename on shared database %s: %w", path, err)
	}
	return nil
}

// hasRemindersTable reports a reminders table still waiting for its rename.
func hasRemindersTable(ctx context.Context, db *sql.DB) (bool, error) {
	var count int
	if err := db.QueryRowContext(
		ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='reminders'",
	).Scan(&count); err != nil {
		return false, fmt.Errorf("read shared database tables: %w", err)
	}
	return count == 1, nil
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

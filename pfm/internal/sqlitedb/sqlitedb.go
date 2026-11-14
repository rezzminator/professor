// Package sqlitedb is the one SQLite opener. pfm's own databases open through
// OpenStore with one pragma set, or through OpenStoreMemory, its in-memory double
// for unit tests; a database another program owns — Codex's state and history
// stores, OpenCode's store — opens through OpenReadOnly or OpenReadWrite, which
// never change its settings.
//
// memory.go: the test-process memory mode — ServeTestMemory (testjail.Run's
// switch), AllowDatabaseFiles, ReleaseTestMemory and AttachName: in a test
// process every path is served from a memory database its marker file names.
package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// driverName is the pure-Go SQLite driver the whole engine uses.
const driverName = "sqlite"

// StoreBusyTimeout is how long a statement on a pfm store waits for a
// concurrent writer before it fails.
const StoreBusyTimeout = 10 * time.Second

// OpenStore opens one of pfm's own databases, creating it and its directory
// (0700): one connection, StoreBusyTimeout, WAL — verified, since a store
// that silently stayed in rollback mode would make concurrent chats erase one
// another's writes — synchronous=NORMAL and foreign keys on.
func OpenStore(ctx context.Context, path string) (*sql.DB, error) {
	finish := obs.SQLOpen(ctx, "store", path)
	open := openStoreFile
	if served(path) {
		open = openStoreServed
	}
	database, err := open(ctx, path)
	finish(err)
	return database, err
}

func openStoreFile(ctx context.Context, path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory for %s: %w", path, err)
	}
	database, err := sql.Open(driverName, fileURI(path, ""))
	if err != nil {
		return nil, fmt.Errorf("open sqlite database %s: %w", path, err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	if err := storePragmas(ctx, database); err != nil {
		return nil, errors.Join(fmt.Errorf("configure sqlite database %s: %w", path, err), database.Close())
	}
	return database, nil
}

func storePragmas(ctx context.Context, database *sql.DB) error {
	if err := connectionPragmas(ctx, database); err != nil {
		return err
	}
	journalMode, err := enableWAL(ctx, database)
	if err != nil {
		return fmt.Errorf("enable WAL: %w", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		return fmt.Errorf("enable WAL: journal_mode is %q", journalMode)
	}
	if _, err := database.ExecContext(ctx, "PRAGMA synchronous=NORMAL"); err != nil {
		return fmt.Errorf("set synchronous mode: %w", err)
	}
	return nil
}

// connectionPragmas sets what every pfm store connection runs with, a file or
// memory: the store busy timeout and foreign keys on.
func connectionPragmas(ctx context.Context, database *sql.DB) error {
	if _, err := database.ExecContext(
		ctx,
		fmt.Sprintf("PRAGMA busy_timeout=%d", StoreBusyTimeout.Milliseconds()),
	); err != nil {
		return fmt.Errorf("set busy_timeout: %w", err)
	}
	if _, err := database.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}
	return nil
}

// memoryNames holds every database name OpenStoreMemory created in this process, so
// InMemory answers for those alone and no resolved path can pass for one.
var (
	memoryNames  sync.Map
	memoryNumber atomic.Uint64
)

// OpenStoreMemory opens a new, empty pfm database in this process's memory, the
// unit-test double of OpenStore: one connection, StoreBusyTimeout and foreign
// keys on, its journal in memory, since WAL needs a file. It creates no file and
// reads no environment. name is the database's URI: another handle in this
// process opens or ATTACHes it to reach the same database, which lives until
// its last handle closes.
func OpenStoreMemory(ctx context.Context) (database *sql.DB, name string, err error) {
	name = fmt.Sprintf("file:/pfm-memory-%d?vfs=memdb", memoryNumber.Add(1))
	finish := obs.SQLOpen(ctx, "memory", name)
	defer func() { finish(err) }()
	database, err = sql.Open(driverName, name)
	if err != nil {
		return nil, "", fmt.Errorf("open sqlite database %s: %w", name, err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	if err := connectionPragmas(ctx, database); err != nil {
		return nil, "", errors.Join(fmt.Errorf("configure sqlite database %s: %w", name, err), database.Close())
	}
	memoryNames.Store(name, struct{}{})
	return database, name, nil
}

// InMemory reports whether name is a database OpenStoreMemory created in this
// process: one with no directory beside it for a lock, a backup or a file.
func InMemory(name string) bool {
	_, found := memoryNames.Load(name)
	return found
}

// walRetryDelay spaces enableWAL's attempts.
const walRetryDelay = 25 * time.Millisecond

// enableWAL switches the file to WAL and returns the journal mode SQLite reports. The switch needs the
// file to itself, and SQLite answers it SQLITE_BUSY at once, never through the busy handler, while another
// connection holds the file: the first commands of a fresh machine open a new store together, so a busy
// switch is retried until StoreBusyTimeout.
func enableWAL(ctx context.Context, database *sql.DB) (string, error) {
	deadline := clock.Real.Now().Add(StoreBusyTimeout)
	for {
		var mode string
		err := database.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode)
		if err == nil || !Busy(err) || clock.Real.Now().After(deadline) {
			return mode, err
		}
		if sleepErr := clock.Real.Sleep(ctx, walRetryDelay); sleepErr != nil {
			return "", errors.Join(err, sleepErr)
		}
	}
}

// Busy reports whether err is SQLite's SQLITE_BUSY, extended codes included: the one door every
// package asks, so a busy store reads the same everywhere.
func Busy(err error) bool {
	var sqliteError *sqlite.Error
	return errors.As(err, &sqliteError) && sqliteError.Code()&0xff == sqlite3.SQLITE_BUSY
}

// OpenReadOnly opens a database another program is writing, read-only, on one
// connection. The handle uses the mode=ro URI and never immutable=1, which
// would ignore the live -wal and serve a stale snapshot. busy bounds how long
// a statement waits on the writer, so a hot moment is an error, never a hang.
func OpenReadOnly(path string, busy time.Duration) (*sql.DB, error) {
	return openForeign(path, "readonly", "mode=ro&", busy)
}

// OpenReadWrite opens a database another program owns for a write, on one
// connection, without changing its journal or sync settings. busy bounds how
// long a statement waits on the owner's writer.
func OpenReadWrite(path string, busy time.Duration) (*sql.DB, error) {
	return openForeign(path, "readwrite", "", busy)
}

// openForeign records the open under the db component as kind (readonly,
// readwrite); the foreign openers take no context, so the process logger is
// the destination.
func openForeign(path, kind, mode string, busy time.Duration) (*sql.DB, error) {
	finish := obs.SQLOpen(context.Background(), kind, path)
	if served(path) {
		database, found, err := openForeignServed(path, mode, busy)
		if err != nil || found {
			finish(err)
			return database, err
		}
	}
	database, err := sql.Open(
		driverName,
		fileURI(path, fmt.Sprintf("%s_pragma=busy_timeout(%d)", mode, busy.Milliseconds())),
	)
	if err != nil {
		finish(err)
		return nil, fmt.Errorf("open sqlite database %s: %w", path, err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	finish(nil)
	return database, nil
}

// uriPath escapes the characters SQLite's URI parser (and the driver's own
// split at the first "?") would read as syntax, so a directory holding "?",
// "#" or "%" opens the file it names.
var uriPath = strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23")

// fileURI is the one DSN shape: a file: URI over the escaped path, with the
// query appended when there is one.
func fileURI(path, query string) string {
	uri := "file:" + uriPath.Replace(path)
	if query != "" {
		uri += "?" + query
	}
	return uri
}

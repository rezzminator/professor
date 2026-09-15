// Package sqlitedb is the one SQLite opener. pfm's own databases open through
// OpenStore with one pragma set; a database another program owns — Codex's
// state and history stores, OpenCode's store — opens through OpenReadOnly or
// OpenReadWrite, which never change its settings.
package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
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
	if _, err := database.ExecContext(
		ctx,
		fmt.Sprintf("PRAGMA busy_timeout=%d", StoreBusyTimeout.Milliseconds()),
	); err != nil {
		return fmt.Errorf("set busy_timeout: %w", err)
	}
	if _, err := database.ExecContext(ctx, "PRAGMA foreign_keys=ON"); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}
	var journalMode string
	if err := database.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&journalMode); err != nil {
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

// OpenReadOnly opens a database another program is writing, read-only, on one
// connection. The handle uses the mode=ro URI and never immutable=1, which
// would ignore the live -wal and serve a stale snapshot. busy bounds how long
// a statement waits on the writer, so a hot moment is an error, never a hang.
func OpenReadOnly(path string, busy time.Duration) (*sql.DB, error) {
	return openForeign(path, "mode=ro&", busy)
}

// OpenReadWrite opens a database another program owns for a write, on one
// connection, without changing its journal or sync settings. busy bounds how
// long a statement waits on the owner's writer.
func OpenReadWrite(path string, busy time.Duration) (*sql.DB, error) {
	return openForeign(path, "", busy)
}

func openForeign(path, mode string, busy time.Duration) (*sql.DB, error) {
	database, err := sql.Open(
		driverName,
		fileURI(path, fmt.Sprintf("%s_pragma=busy_timeout(%d)", mode, busy.Milliseconds())),
	)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database %s: %w", path, err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
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

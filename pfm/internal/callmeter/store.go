// Package callmeter owns the callmeter store: one SQLite file recording every
// tool call a Claude chat or sub-agent makes, the model request that grouped
// it and the sub-agent that made it (docs/design/hooks/callmeter.md § The
// store), plus the input sanitizer and the transcript readers the hook entry,
// backfill and reports share.
package callmeter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	modernsqlite "modernc.org/sqlite"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// SchemaVersion is the store schema this binary writes and reads, kept in
// PRAGMA user_version.
const SchemaVersion = 1

// BusyTimeout is how long a statement waits on a concurrent async writer.
const BusyTimeout = 5 * time.Second

// Source values: which path wrote (or last touched) a row.
const (
	SourceHook       = "hook"
	SourceTranscript = "transcript"
)

// Fault stages: where a failure to record or parse happened.
const (
	StagePayload    = "payload"
	StageStore      = "store"
	StageTranscript = "transcript"
	StageParse      = "parse"
	StageBackfill   = "backfill"
)

const schema = `
CREATE TABLE IF NOT EXISTS calls (
	tool_use_id TEXT PRIMARY KEY,
	session_id TEXT,
	agent_id TEXT,
	agent_type TEXT,
	request_id TEXT,
	ts INTEGER,
	tool TEXT,
	input TEXT,
	cwd TEXT,
	duration_ms INTEGER,
	failed INTEGER,
	error TEXT,
	bytes_real INTEGER,
	bytes_delivered INTEGER,
	persisted_path TEXT,
	file_path TEXT,
	file_bytes INTEGER,
	file_bytes_before INTEGER,
	read_start INTEGER,
	read_lines INTEGER,
	read_total_lines INTEGER,
	source TEXT,
	config_dir TEXT
);
CREATE INDEX IF NOT EXISTS calls_session_agent_ts ON calls(session_id, agent_id, ts);
CREATE INDEX IF NOT EXISTS calls_ts ON calls(ts);
CREATE INDEX IF NOT EXISTS calls_file_path ON calls(file_path);
CREATE TABLE IF NOT EXISTS requests (
	request_id TEXT PRIMARY KEY,
	session_id TEXT,
	agent_id TEXT,
	ts INTEGER,
	context_tokens INTEGER,
	output_tokens INTEGER,
	calls INTEGER,
	pending INTEGER,
	source TEXT,
	config_dir TEXT
);
CREATE INDEX IF NOT EXISTS requests_session_agent_pending ON requests(session_id, agent_id, pending);
CREATE TABLE IF NOT EXISTS agents (
	agent_id TEXT PRIMARY KEY,
	session_id TEXT,
	agent_type TEXT,
	parent_tool_use_id TEXT,
	started INTEGER,
	stopped INTEGER,
	transcript_path TEXT,
	total_tokens INTEGER,
	tool_uses INTEGER,
	model TEXT,
	source TEXT,
	config_dir TEXT
);
CREATE TABLE IF NOT EXISTS command_parts (
	tool_use_id TEXT NOT NULL,
	seq INTEGER NOT NULL,
	lang TEXT,
	program TEXT,
	args TEXT,
	files TEXT,
	parse_status TEXT,
	conditional INTEGER NOT NULL DEFAULT 0,
	parser INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (tool_use_id, seq)
);
CREATE TABLE IF NOT EXISTS faults (
	ts INTEGER,
	session_id TEXT,
	tool_use_id TEXT,
	stage TEXT,
	error TEXT
);
`

// Store is an open callmeter database.
type Store struct {
	db   *sql.DB
	path string
}

// DefaultPath is the store's file under home.
func DefaultPath(home string) string {
	return filepath.Join(home, ".local", "state", "pfm", "callmeter.db")
}

// OpenDB opens (creating it and its directory) the store at path: WAL,
// BusyTimeout, foreign keys off, schema SchemaVersion. A store written by a
// newer schema is refused, never opened and misread.
//
// A store another process is creating at this instant answers SQLITE_BUSY at
// once — its switch to WAL takes no busy wait — so a busy open is retried
// until BusyTimeout has passed: the first async hooks of a session all race
// to create the store, and the loser must not lose its record.
func OpenDB(ctx context.Context, path string) (*Store, error) {
	deadline := clock.Real.Now().Add(BusyTimeout)
	for {
		store, err := openAndPrepare(ctx, path)
		if err == nil || !isBusySQLite(err) || clock.Real.Now().After(deadline) {
			return store, err
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(err, fmt.Errorf("callmeter store %s: retry a busy open: %w", path, ctx.Err()))
		case <-clock.Real.After(openRetryDelay):
		}
	}
}

// openRetryDelay spaces the retries of a busy open.
const openRetryDelay = 20 * time.Millisecond

// sqliteBusy is SQLite's primary result code SQLITE_BUSY.
const sqliteBusy = 5

// isBusySQLite reports whether err is SQLite answering SQLITE_BUSY.
func isBusySQLite(err error) bool {
	var sqliteError *modernsqlite.Error
	return errors.As(err, &sqliteError) && sqliteError.Code()&0xff == sqliteBusy
}

func openAndPrepare(ctx context.Context, path string) (*Store, error) {
	db, err := sqlitedb.OpenStore(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("open callmeter store: %w", err)
	}
	if err := prepare(ctx, db, path); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return &Store{db: db, path: path}, nil
}

func prepare(ctx context.Context, db *sql.DB, path string) error {
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout=%d", BusyTimeout.Milliseconds())); err != nil {
		return fmt.Errorf("callmeter store %s: set busy_timeout: %w", path, err)
	}
	if _, err := db.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return fmt.Errorf("callmeter store %s: disable foreign keys: %w", path, err)
	}
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("callmeter store %s: read schema version: %w", path, err)
	}
	if version > SchemaVersion {
		return fmt.Errorf(
			"callmeter store %s has schema version %d, newer than this pfm's version %d: refusing to open it",
			path,
			version,
			SchemaVersion,
		)
	}
	if version == SchemaVersion {
		return nil // current: an open writes nothing, so concurrent hooks never collide here
	}
	return createSchema(ctx, db, path)
}

// createSchema writes the schema and its version in one transaction that takes
// the write lock at BEGIN IMMEDIATE, so a concurrent writer is waited out by
// the busy timeout. A deferred transaction would read first and then fail with
// SQLITE_BUSY at its first write, no busy wait, whenever another async hook
// wrote in between — the store open that lost a hook's whole record.
func createSchema(ctx context.Context, db *sql.DB, path string) (err error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("callmeter store %s: take a connection for the schema: %w", path, err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("callmeter store %s: release the schema connection: %w", path, closeErr))
		}
	}()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("callmeter store %s: begin schema transaction: %w", path, err)
	}
	rollback := func(cause error) error {
		if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
			return errors.Join(cause, fmt.Errorf("callmeter store %s: roll back schema: %w", path, err))
		}
		return cause
	}
	if _, err := conn.ExecContext(ctx, schema); err != nil {
		return rollback(fmt.Errorf("callmeter store %s: create schema: %w", path, err))
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", SchemaVersion)); err != nil {
		return rollback(fmt.Errorf("callmeter store %s: set schema version: %w", path, err))
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return rollback(fmt.Errorf("callmeter store %s: commit schema: %w", path, err))
	}
	return nil
}

// DB is the handle for read-only report queries.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the store.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close callmeter store %s: %w", s.path, err)
	}
	return nil
}

// Fault is one failure to record or parse.
type Fault struct {
	TS        int64 // Unix ms UTC
	SessionID string
	ToolUseID string
	Stage     string // one of the Stage* constants
	Error     string
}

// AddFault records f; an empty SessionID or ToolUseID is stored as NULL.
func (s *Store) AddFault(ctx context.Context, f Fault) error {
	if _, err := s.db.ExecContext(ctx,
		"INSERT INTO faults (ts, session_id, tool_use_id, stage, error) VALUES (?, ?, ?, ?, ?)",
		f.TS, nullString(f.SessionID), nullString(f.ToolUseID), f.Stage, f.Error,
	); err != nil {
		return fmt.Errorf("callmeter store %s: add %s fault for call %q: %w", s.path, f.Stage, f.ToolUseID, err)
	}
	return nil
}

// Prune deletes the calls, requests, agents and faults older than before, then
// every command_parts row whose call is gone, in one transaction. A row with
// no timestamp has no age and is kept.
func (s *Store) Prune(ctx context.Context, before time.Time) (removed int64, err error) {
	cutoff := before.UnixMilli()
	statements := []struct {
		sql  string
		args []any
	}{
		{"DELETE FROM calls WHERE ts < ?", []any{cutoff}},
		{"DELETE FROM requests WHERE ts < ?", []any{cutoff}},
		{"DELETE FROM agents WHERE COALESCE(stopped, started) < ?", []any{cutoff}},
		{"DELETE FROM faults WHERE ts < ?", []any{cutoff}},
		{"DELETE FROM command_parts WHERE tool_use_id NOT IN (SELECT tool_use_id FROM calls)", nil},
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("callmeter store %s: begin prune: %w", s.path, err)
	}
	for _, statement := range statements {
		result, err := tx.ExecContext(ctx, statement.sql, statement.args...)
		if err != nil {
			return 0, errors.Join(
				fmt.Errorf("callmeter store %s: prune (%s): %w", s.path, statement.sql, err),
				tx.Rollback(),
			)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return 0, errors.Join(
				fmt.Errorf("callmeter store %s: prune count (%s): %w", s.path, statement.sql, err),
				tx.Rollback(),
			)
		}
		removed += n
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("callmeter store %s: commit prune: %w", s.path, err)
	}
	return removed, nil
}

// RetireRequests deletes the requests and calls rows of requestIDs, in one
// transaction (backfill's orphan retirement, docs/design/hooks/callmeter.md §
// Backfill): an untyped internal sub-agent's pending request the hook can
// never resolve and backfill can never find in a transcript. The count
// returned is the requests rows removed; a requestID absent from either
// table is simply not touched.
func (s *Store) RetireRequests(ctx context.Context, requestIDs []string) (removed int64, err error) {
	if len(requestIDs) == 0 {
		return 0, nil
	}
	marks := make([]string, len(requestIDs))
	args := make([]any, len(requestIDs))
	for i, id := range requestIDs {
		marks[i], args[i] = "?", id
	}
	inClause := "(" + strings.Join(marks, ", ") + ")"
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("callmeter store %s: begin retire: %w", s.path, err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM calls WHERE request_id IN "+inClause, args...); err != nil {
		return 0, errors.Join(fmt.Errorf("callmeter store %s: retire calls: %w", s.path, err), tx.Rollback())
	}
	result, err := tx.ExecContext(ctx, "DELETE FROM requests WHERE request_id IN "+inClause, args...)
	if err != nil {
		return 0, errors.Join(fmt.Errorf("callmeter store %s: retire requests: %w", s.path, err), tx.Rollback())
	}
	removed, err = result.RowsAffected()
	if err != nil {
		return 0, errors.Join(fmt.Errorf("callmeter store %s: retire count: %w", s.path, err), tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("callmeter store %s: commit retire: %w", s.path, err)
	}
	return removed, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

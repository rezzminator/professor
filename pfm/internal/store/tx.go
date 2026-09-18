package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"hostops/pfm/internal/obs"
)

const (
	// BatchSize is the maximum number of records handled by one index batch.
	BatchSize = 500

	rollbackTimeout = time.Second
)

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type queryExecer interface {
	rowQueryer
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// ImmediateTx is a transaction begun with SQLite's BEGIN IMMEDIATE.
// Commit and rollback are owned by Store.WithImmediateTx.
type ImmediateTx struct {
	conn       *sql.Conn
	statements map[string]*sql.Stmt
}

// ExecContext executes a statement within the immediate transaction. Every
// ImmediateTx statement writes one comp=db record (obs.SQL) around the
// driver call — the transaction is the store's other statement door.
func (tx *ImmediateTx) ExecContext(
	ctx context.Context,
	query string,
	args ...any,
) (sql.Result, error) {
	op := obs.SQL(ctx, storeKind, query)
	result, err := tx.conn.ExecContext(ctx, query, args...)
	op.End(affectedRows(result, err), err)
	return result, err
}

// QueryContext queries rows within the immediate transaction.
func (tx *ImmediateTx) QueryContext(
	ctx context.Context,
	query string,
	args ...any,
) (*sql.Rows, error) {
	op := obs.SQL(ctx, storeKind, query)
	rows, err := tx.conn.QueryContext(ctx, query, args...)
	op.End(-1, err)
	return rows, err
}

// QueryRowContext queries one row within the immediate transaction.
func (tx *ImmediateTx) QueryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) *sql.Row {
	op := obs.SQL(ctx, storeKind, query)
	row := tx.conn.QueryRowContext(ctx, query, args...)
	op.End(-1, row.Err())
	return row
}

func (tx *ImmediateTx) execCachedContext(
	ctx context.Context,
	query string,
	args ...any,
) (sql.Result, error) {
	op := obs.SQL(ctx, storeKind, query)
	statement := tx.statements[query]
	if statement == nil {
		var err error
		statement, err = tx.conn.PrepareContext(ctx, query)
		if err != nil {
			op.End(-1, err)
			return nil, err
		}
		tx.statements[query] = statement
	}
	result, err := statement.ExecContext(ctx, args...)
	op.End(affectedRows(result, err), err)
	return result, err
}

func (tx *ImmediateTx) closeStatements() error {
	var closeError error
	for query, statement := range tx.statements {
		if err := statement.Close(); err != nil && closeError == nil {
			closeError = fmt.Errorf("close prepared statement %q: %w", query, err)
		}
	}
	clear(tx.statements)
	return closeError
}

func execWrite(
	ctx context.Context,
	db queryExecer,
	query string,
	args ...any,
) (sql.Result, error) {
	if tx, ok := db.(*ImmediateTx); ok {
		return tx.execCachedContext(ctx, query, args...)
	}
	return db.ExecContext(ctx, query, args...)
}

// WithImmediateTx runs fn in a BEGIN IMMEDIATE transaction.
func (s *Store) WithImmediateTx(
	ctx context.Context,
	fn func(*ImmediateTx) error,
) (err error) {
	if fn == nil {
		return errors.New("immediate transaction callback is nil")
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire sqlite connection: %w", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close sqlite connection: %w", closeErr))
		}
	}()

	// One record spans the transaction: op=begin, ended with the commit's
	// result or the failure that rolled it back.
	transaction := obs.SQL(ctx, storeKind, "BEGIN IMMEDIATE")
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		transaction.End(-1, err)
		return fmt.Errorf("begin immediate transaction: %w", err)
	}

	committed := false
	defer func() {
		transaction.End(-1, err)
		if committed {
			return
		}
		rollbackCtx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
		defer cancel()
		if _, rollbackErr := conn.ExecContext(rollbackCtx, "ROLLBACK"); err == nil && rollbackErr != nil {
			err = fmt.Errorf("rollback immediate transaction: %w", rollbackErr)
		}
	}()

	tx := &ImmediateTx{
		conn:       conn,
		statements: make(map[string]*sql.Stmt),
	}
	if err := fn(tx); err != nil {
		_ = tx.closeStatements()
		return err
	}
	if err := tx.closeStatements(); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit immediate transaction: %w", err)
	}
	committed = true
	return nil
}

// Batch invokes fn for ranges of at most BatchSize records. Every range gets
// its own BEGIN IMMEDIATE transaction so a cold index does not monopolize the
// writer lock.
func (s *Store) Batch(
	ctx context.Context,
	count int,
	fn func(tx *ImmediateTx, start, end int) error,
) error {
	if count < 0 {
		return fmt.Errorf("batch record count cannot be negative: %d", count)
	}
	if fn == nil {
		return errors.New("batch callback is nil")
	}

	for start := 0; start < count; start += BatchSize {
		end := min(start+BatchSize, count)
		if err := s.WithImmediateTx(ctx, func(tx *ImmediateTx) error {
			return fn(tx, start, end)
		}); err != nil {
			return fmt.Errorf("batch records %d:%d: %w", start, end, err)
		}
	}
	return nil
}

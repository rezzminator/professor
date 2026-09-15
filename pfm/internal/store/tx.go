package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
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

// ExecContext executes a statement within the immediate transaction.
func (tx *ImmediateTx) ExecContext(
	ctx context.Context,
	query string,
	args ...any,
) (sql.Result, error) {
	return tx.conn.ExecContext(ctx, query, args...)
}

// QueryContext queries rows within the immediate transaction.
func (tx *ImmediateTx) QueryContext(
	ctx context.Context,
	query string,
	args ...any,
) (*sql.Rows, error) {
	return tx.conn.QueryContext(ctx, query, args...)
}

// QueryRowContext queries one row within the immediate transaction.
func (tx *ImmediateTx) QueryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) *sql.Row {
	return tx.conn.QueryRowContext(ctx, query, args...)
}

func (tx *ImmediateTx) execCachedContext(
	ctx context.Context,
	query string,
	args ...any,
) (sql.Result, error) {
	statement := tx.statements[query]
	if statement == nil {
		var err error
		statement, err = tx.conn.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}
		tx.statements[query] = statement
	}
	return statement.ExecContext(ctx, args...)
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

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin immediate transaction: %w", err)
	}

	committed := false
	defer func() {
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

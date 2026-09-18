package store

import (
	"context"
	"database/sql"

	"hostops/pfm/internal/obs"
)

// storeKind names the transcript store to the db component's records.
const storeKind = "store"

// loggedDB is Store.db seen through the db door: the same three statement
// methods *sql.DB has, each writing one comp=db record (verb, table, rows,
// dur_ms) around the driver call. Every clean access path reaches the
// database through Store.logged() or an ImmediateTx, so the record is
// written exactly once per statement and the SQL text and its arguments
// stay with the driver.
type loggedDB struct {
	db *sql.DB
}

// logged is the store's *sql.DB for statements; tx.go's WithImmediateTx
// takes the raw handle for its connection.
func (s *Store) logged() loggedDB { return loggedDB{db: s.db} }

func (db loggedDB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	op := obs.SQL(ctx, storeKind, query)
	result, err := db.db.ExecContext(ctx, query, args...)
	op.End(affectedRows(result, err), err)
	return result, err
}

func (db loggedDB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	op := obs.SQL(ctx, storeKind, query)
	rows, err := db.db.QueryContext(ctx, query, args...)
	op.End(-1, err)
	return rows, err
}

func (db loggedDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	op := obs.SQL(ctx, storeKind, query)
	row := db.db.QueryRowContext(ctx, query, args...)
	op.End(-1, row.Err())
	return row
}

// affectedRows reads a result's count, or -1 (unknown) when there is none
// or the driver cannot say.
func affectedRows(result sql.Result, err error) int64 {
	if err != nil || result == nil {
		return -1
	}
	affected, countErr := result.RowsAffected()
	if countErr != nil {
		return -1
	}
	return affected
}

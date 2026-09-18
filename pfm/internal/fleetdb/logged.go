package fleetdb

import (
	"context"
	"database/sql"

	"hostops/pfm/internal/obs"
)

// kind names this database to the db component's records.
const kind = "fleet"

// exec, query and queryRow are the only way a Store operation reaches its
// *sql.DB: each writes one comp=db record (verb, table, rows, dur_ms) and
// hands the statement through untouched. The SQL text and its arguments are
// passed to the driver only — obs.SQL reads the shape and keeps nothing else.
func (s *Store) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	op := obs.SQL(ctx, kind, query)
	result, err := s.db.ExecContext(ctx, query, args...)
	op.End(rowsAffected(result, err), err)
	return result, err
}

func (s *Store) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	op := obs.SQL(ctx, kind, query)
	rows, err := s.db.QueryContext(ctx, query, args...)
	op.End(-1, err)
	return rows, err
}

func (s *Store) queryRow(ctx context.Context, query string, args ...any) *sql.Row {
	op := obs.SQL(ctx, kind, query)
	row := s.db.QueryRowContext(ctx, query, args...)
	op.End(-1, row.Err())
	return row
}

// rowsAffected reads a result's count, or -1 (unknown) when there is none
// or the driver cannot say.
func rowsAffected(result sql.Result, err error) int64 {
	if err != nil || result == nil {
		return -1
	}
	affected, countErr := result.RowsAffected()
	if countErr != nil {
		return -1
	}
	return affected
}

package fleetdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// immediateLabels name a withImmediate caller's work in the three errors withImmediate itself returns.
type immediateLabels struct {
	conn, lock, release string
}

// withImmediate runs fn on one connection inside a BEGIN IMMEDIATE transaction. The write lock is taken up
// front, through the busy handler: a deferred transaction whose first statement only reads fails
// SQLITE_BUSY at once when it upgrades after another connection wrote. fn commits and reports committed;
// a transaction it leaves open is rolled back.
func withImmediate(
	ctx context.Context,
	db *sql.DB,
	labels immediateLabels,
	fn func(*sql.Conn) (committed bool, err error),
) (returnErr error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("%s: %w", labels.conn, err)
	}
	defer func() { returnErr = errors.Join(returnErr, conn.Close()) }()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("%s: %w", labels.lock, err)
	}
	committed, err := fn(conn)
	if committed {
		return err
	}
	if _, rollbackErr := conn.ExecContext(context.Background(), "ROLLBACK"); rollbackErr != nil {
		err = errors.Join(err, fmt.Errorf("%s: %w", labels.release, rollbackErr))
	}
	return err
}

package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"modernc.org/sqlite"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// RestoreFrom overwrites the database behind database with the SQLite database at
// source through SQLite's online backup: the pages are copied under the
// destination's own locks and through its WAL, so every other connection to it
// reads the restored database next and none sees a half-replaced file.
func RestoreFrom(ctx context.Context, database *sql.DB, source string) (returnErr error) {
	conn, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("restore from %s: acquire connection: %w", source, err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("restore from %s: release connection: %w", source, err))
		}
	}()
	return conn.Raw(func(driverConn any) error {
		restorer, ok := driverConn.(interface {
			NewRestore(srcURI string) (*sqlite.Backup, error)
		})
		if !ok {
			return fmt.Errorf("restore from %s: driver connection %T has no online backup", source, driverConn)
		}
		finish := obs.SQLOpen(ctx, "restore", source)
		backup, err := restorer.NewRestore(fileURI(source, "mode=ro"))
		finish(err)
		if err != nil {
			return fmt.Errorf("restore from %s: open source: %w", source, err)
		}
		_, stepErr := backup.Step(-1)
		if stepErr != nil {
			stepErr = fmt.Errorf("restore from %s: copy pages: %w", source, stepErr)
		}
		finishErr := backup.Finish()
		if finishErr != nil {
			finishErr = fmt.Errorf("restore from %s: finish: %w", source, finishErr)
		}
		return errors.Join(stepErr, finishErr)
	})
}

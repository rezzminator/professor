package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// openCodeTables are the tables the session mirror reads, sorted.
var openCodeTables = []string{"message", "part", "project", "session"}

const openCodeTablesQuery = `SELECT name FROM sqlite_master
 WHERE type = 'table' AND name IN ('message', 'part', 'project', 'session') ORDER BY name`

// openCodeSchemaReady reads whether a store holds the mirror's tables yet. OpenCode
// opens its store file before its migration commits the schema in one transaction,
// so a store holding none of them is still being created: no sessions, not an error.
// A store holding some but not all is not that moment, and its error names both sides.
func openCodeSchemaReady(ctx context.Context, db *sql.DB) (ready bool, returnErr error) {
	read := obs.SQL(ctx, pfmengine.MustLookup(pfmengine.OpenCode).LongName, openCodeTablesQuery)
	rows, err := db.QueryContext(ctx, openCodeTablesQuery)
	read.End(-1, err)
	if err != nil {
		return false, fmt.Errorf("read opencode store tables: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close opencode table rows: %w", closeErr))
		}
	}()
	var held []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, fmt.Errorf("scan opencode store table: %w", err)
		}
		held = append(held, name)
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("read opencode store tables: %w", err)
	}
	switch len(held) {
	case 0:
		return false, nil
	case len(openCodeTables):
		return true, nil
	}
	missing := slices.DeleteFunc(
		slices.Clone(openCodeTables),
		func(name string) bool { return slices.Contains(held, name) },
	)
	return false, fmt.Errorf("opencode store holds tables %v but not %v", held, missing)
}

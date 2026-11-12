package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

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
// A store holding some but not all is not ready either: its error is an
// *openCodeStorePartialError, which the listing warns of and reads no sessions past
// and doctor reports as an unhealthy store.
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
	return false, &openCodeStorePartialError{held: held, missing: missing}
}

// openCodeStorePartialError is a store holding some, not all, of the mirror's tables.
type openCodeStorePartialError struct{ held, missing []string }

func (e *openCodeStorePartialError) Error() string {
	return fmt.Sprintf("opencode store holds tables %v but not %v", e.held, e.missing)
}

// ProbeOpenCodeStore runs the production reader and discards its rows. Doctor
// therefore proves that both the native schema and the stored JSON are readable;
// a schema-only prepare could report healthy while every real index pass fails.
// Malformed rows the index reads past are unhealthy here, and so is a store holding
// only some of the mirror's tables, which the index lists no sessions from.
func ProbeOpenCodeStore(ctx context.Context, root string) error {
	_, malformed, err := readOpenCodeStore(ctx, root)
	if err != nil {
		return err
	}
	if len(malformed) != 0 {
		texts := make([]string, 0, len(malformed))
		for _, problem := range malformed {
			texts = append(texts, problem.Error())
		}
		return fmt.Errorf("validate opencode rows: %s", strings.Join(texts, "; "))
	}
	return nil
}

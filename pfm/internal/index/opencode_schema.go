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

// openCodeSchema is one OpenCode store layout: the tables it holds and the
// relations presenting them to every reader as 1.x's `session`, `message` and
// `part`, the rows and JSON the readers' statements and decoders read.
type openCodeSchema struct {
	version string
	// session is the table holding its sessions.
	session string
	// tables are the tables the readers read, sorted.
	tables []string
	// relations is a WITH clause naming session, message and part; "" reads the tables themselves.
	relations string
}

// openCodeSchemas are the store layouts the readers know: 1.x, and 2.x, which
// renamed `session` to `session_v2` and moved messages and parts into
// `session_message` and `session_part` (openCodeV2Relations).
var openCodeSchemas = []openCodeSchema{
	{version: "1.x", session: "session", tables: []string{"message", "part", "project", "session"}},
	{
		version:   "2.x",
		session:   "session_v2",
		tables:    []string{"project", "session_message", "session_part", "session_v2"},
		relations: openCodeV2Relations,
	},
}

// openCodeTablesQuery lists which of every schema's tables a store holds.
var openCodeTablesQuery = func() string {
	var names []string
	for _, schema := range openCodeSchemas {
		names = append(names, schema.tables...)
	}
	slices.Sort(names)
	return "SELECT name FROM sqlite_master WHERE type = 'table' AND name IN ('" +
		strings.Join(slices.Compact(names), "', '") + "') ORDER BY name"
}()

// statement is body, a statement over `session`, `message`, `part` and
// `project`, run against this schema's tables.
func (schema *openCodeSchema) statement(body string) string {
	if schema.relations == "" {
		return body
	}
	body = strings.TrimSpace(body)
	if rest, found := strings.CutPrefix(body, "WITH "); found {
		return schema.relations + ",\n" + rest
	}
	return schema.relations + "\n" + body
}

// openCodeSchemaRead is the one door every reader learns a store's layout through:
// the schema whose tables it holds. OpenCode opens its store file before its
// migration commits the schema in one transaction, so a store holding none of any
// schema's tables is still being created: nil, not an error. A store holding some
// but no schema's every table is not ready either: its error is an
// *openCodeStorePartialError, which the listing warns of and reads no sessions past
// and doctor reports as an unhealthy store.
func openCodeSchemaRead(ctx context.Context, db *sql.DB) (schema *openCodeSchema, returnErr error) {
	read := obs.SQL(ctx, pfmengine.MustLookup(pfmengine.OpenCode).LongName, openCodeTablesQuery)
	rows, err := db.QueryContext(ctx, openCodeTablesQuery)
	read.End(-1, err)
	if err != nil {
		return nil, fmt.Errorf("read opencode store tables: %w", err)
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
			return nil, fmt.Errorf("scan opencode store table: %w", err)
		}
		held = append(held, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read opencode store tables: %w", err)
	}
	if len(held) == 0 {
		return nil, nil
	}
	// The newest schema wins: a store an upgrade migrated may still hold the older
	// schema's tables, frozen, beside the ones the engine now writes.
	partial := &openCodeStorePartialError{held: held}
	for index := range openCodeSchemas {
		missing := slices.DeleteFunc(
			slices.Clone(openCodeSchemas[index].tables),
			func(name string) bool { return slices.Contains(held, name) },
		)
		if len(missing) == 0 {
			schema = &openCodeSchemas[index]
		}
		partial.missing = append(partial.missing, missing)
		if slices.Contains(held, openCodeSchemas[index].session) {
			partial.sessions = &openCodeSchemas[index]
		}
	}
	if schema != nil {
		return schema, nil
	}
	return nil, partial
}

// openCodeStorePartialError is a store holding some, not all, of every schema's
// tables: missing holds each schema's lacking tables, in openCodeSchemas order,
// sessions the newest schema whose session table it holds, nil for none.
type openCodeStorePartialError struct {
	held     []string
	missing  [][]string
	sessions *openCodeSchema
}

func (e *openCodeStorePartialError) Error() string {
	return fmt.Sprintf("opencode store holds tables %v but lacks %s", e.held, e.lacks())
}

// lacks names each schema's missing tables: `1.x tables [part] and 2.x tables [session_v2]`.
func (e *openCodeStorePartialError) lacks() string {
	texts := make([]string, 0, len(e.missing))
	for index, missing := range e.missing {
		texts = append(texts, fmt.Sprintf("%s tables %v", openCodeSchemas[index].version, missing))
	}
	return strings.Join(texts, " and ")
}

// queryOpenCodeStore runs one read-only statement over root's store through
// its schema, handing each row to scan; every failure names the store, and a
// store holding no schema's tables yet is an error that says so. A statement
// reads only the tables it names, so a store lacking some of a schema's tables
// is read through the schema whose session table it holds, a table the
// statement needs and the store lacks failing the query by name.
func queryOpenCodeStore(
	ctx context.Context,
	root, query string,
	args []any,
	scan func(*sql.Rows) error,
) (returnErr error) {
	db, err := openOpenCodeStore(root)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close opencode store read-only: %w", closeErr))
		}
	}()
	schema, err := openCodeSchemaRead(ctx, db)
	var partial *openCodeStorePartialError
	if errors.As(err, &partial) && partial.sessions != nil {
		schema, err = partial.sessions, nil
	}
	if err != nil {
		return fmt.Errorf("read opencode store %s: %w", OpenCodeStorePath(root), err)
	}
	if schema == nil {
		return fmt.Errorf("opencode store %s holds no session tables yet", OpenCodeStorePath(root))
	}
	statement := schema.statement(query)
	read := obs.SQL(ctx, pfmengine.MustLookup(pfmengine.OpenCode).LongName, statement)
	rows, err := db.QueryContext(ctx, statement, args...)
	read.End(-1, err)
	if err != nil {
		return fmt.Errorf("query opencode store %s: %w", OpenCodeStorePath(root), err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close opencode rows: %w", closeErr))
		}
	}()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate opencode store %s: %w", OpenCodeStorePath(root), err)
	}
	return nil
}

// ProbeOpenCodeStore runs the production reader and discards its rows. Doctor
// therefore proves that both the native schema and the stored JSON are readable;
// a schema-only prepare could report healthy while every real index pass fails.
// Malformed rows the index reads past are unhealthy here, and so is a store holding
// only some of a schema's tables, which the index lists no sessions from.
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

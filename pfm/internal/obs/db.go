package obs

import (
	"context"
	"log/slog"
	"strings"
	"time"
)

// compDB is the component every database door records under.
const compDB = "db"

// SQLOp is one statement in flight through the db door (spec § Middleware,
// `db`): SQL starts it, the access layer counts busy retries on Retries, and
// End writes the one record. What is recorded is the statement's SHAPE —
// verb, table, kind — read from the text once here and thrown away; the text
// itself and every bound value never leave the access layer.
type SQLOp struct {
	Retries int
	ctx     context.Context
	kind    string
	op      string
	table   string
	started time.Time
}

// SQL begins the record for one statement against a database of kind
// (fleet, store, codex, opencode). query is read for its verb and table only.
func SQL(ctx context.Context, kind, query string) *SQLOp {
	op, table := statementShape(query)
	return &SQLOp{ctx: ctx, kind: kind, op: op, table: table, started: current(ctx).timing.Now()}
}

// End writes db.statement: INFO with rows (omitted when the layer does not
// know them, rows < 0) and retries when any happened; ERROR with err.
func (op *SQLOp) End(rows int64, err error) {
	attrs := []slog.Attr{slog.String("op", op.op), slog.String("kind", op.kind)}
	if op.table != "" {
		attrs = append(attrs, slog.String("table", op.table))
	}
	if rows >= 0 {
		attrs = append(attrs, slog.Int64("rows", rows))
	}
	if op.Retries > 0 {
		attrs = append(attrs, slog.Int("retries", op.Retries))
	}
	record(op.ctx, compDB, "db.statement", errorLevel(err), op.started, err, attrs...)
}

// SQLOpen is the record of one sqlitedb open: call it before the open with
// the database kind (store, readonly, readwrite) and the path, and the
// returned function once with the open's error.
func SQLOpen(ctx context.Context, kind, path string) func(err error) {
	started := current(ctx).timing.Now()
	return func(err error) {
		record(ctx, compDB, "db.open", errorLevel(err), started, err,
			slog.String("kind", kind), slog.String("path", path))
	}
}

// statementShape reads the verb (the first word, lowered) and the table the
// statement is about — after INTO, FROM, UPDATE, TABLE, or INDEX … ON; a
// PRAGMA's name — out of query. Only the first statement of a batch is
// read. Nothing else in the text is kept.
func statementShape(query string) (op, table string) {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '(' || r == ')' || r == ',' || r == ';' ||
			r == '='
	})
	if len(fields) == 0 {
		return "", ""
	}
	op = strings.ToLower(fields[0])
	if op == "pragma" && len(fields) > 1 {
		return op, unquote(fields[1])
	}
	for index := 0; index+1 < len(fields); index++ {
		switch strings.ToUpper(fields[index]) {
		case "INTO", "UPDATE", "FROM":
			return op, unquote(fields[index+1])
		case "TABLE":
			return op, unquote(afterExistsClause(fields[index+1:]))
		case "INDEX":
			for rest := index + 1; rest+1 < len(fields); rest++ {
				if strings.EqualFold(fields[rest], "ON") {
					return op, unquote(fields[rest+1])
				}
			}
			return op, ""
		}
	}
	return op, ""
}

// afterExistsClause skips an `IF NOT EXISTS` and returns the name that follows.
func afterExistsClause(fields []string) string {
	if len(fields) >= 4 && strings.EqualFold(fields[0], "IF") && strings.EqualFold(fields[1], "NOT") &&
		strings.EqualFold(fields[2], "EXISTS") {
		return fields[3]
	}
	if len(fields) != 0 {
		return fields[0]
	}
	return ""
}

func unquote(name string) string {
	return strings.Trim(name, "`\"'[]")
}

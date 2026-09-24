// Package report answers the callmeter report topics over the store
// (docs/design/hooks/callmeter.md § Reports): the shared filter, the prune
// that runs before every report, the command-parse cache fill, the
// fixed-width table and one query per topic.
package report

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
)

// Retention is the store's window: rows older than this are pruned before
// every report, and it is the default --since.
const Retention = 30 * 24 * time.Hour

// DefaultLimit is the rows per table when Filter.Limit is not positive.
const DefaultLimit = 25

// EmptyLine is the only body line of a table with no rows.
const EmptyLine = "callmeter: no calls recorded in window"

// Filter narrows every topic. A zero Since covers the whole retention window
// (PruneExpired has pruned everything older); an empty field does not filter.
type Filter struct {
	Since      time.Time
	Project    string   // calls whose cwd is Project or under it
	AgentType  string   // calls made by that agent type
	Session    string   // calls in that session
	ConfigDirs []string // empty = every config dir
	Account    *int     // calls and requests that configured account ran; nil = every row
	Limit      int      // rows per table; <= 0 is DefaultLimit
}

// NameOf resolves a session id to its chat name; the CLI reads pfm's transcript
// index read-only (store.OpenTranscriptNames), never fleet.db.
type NameOf func(sessionID string) (string, error)

var durationSince = regexp.MustCompile(`^(\d+)([dh])$`)

// ParseSince reads a --since value: a duration in days or hours ("7d",
// "24h") counted back from now, or a UTC date ("2026-09-01"). An empty value
// is the whole retention window, now - Retention.
func ParseSince(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return now.Add(-Retention), nil
	}
	if m := durationSince.FindStringSubmatch(s); m != nil {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return time.Time{}, fmt.Errorf("callmeter report: --since %q: %w", s, err)
		}
		unit := time.Hour
		if m[2] == "d" {
			unit = 24 * time.Hour
		}
		return now.Add(-time.Duration(n) * unit), nil
	}
	day, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, fmt.Errorf(
			"callmeter report: --since %q is neither a duration (7d, 24h) nor a date (2026-09-01)",
			s,
		)
	}
	return day.UTC(), nil
}

// PruneExpired prunes every row older than the retention window before a report
// runs, and returns how many rows went.
func PruneExpired(ctx context.Context, store *callmeter.Store, now time.Time) (int64, error) {
	removed, err := store.Prune(ctx, now.Add(-Retention))
	if err != nil {
		return 0, fmt.Errorf("callmeter report: prune before report: %w", err)
	}
	return removed, nil
}

// Table is one topic's answer.
type Table struct {
	Title  string // the heading: topic, filters, window
	Header []string
	Rows   [][]string
	Notes  []string // one line per named gap
}

// Render writes the heading, the fixed-width rows (or EmptyLine) and the
// note lines.
func (t *Table) Render(w io.Writer) error {
	if _, err := fmt.Fprintln(w, t.Title); err != nil {
		return fmt.Errorf("callmeter report: write heading: %w", err)
	}
	if len(t.Rows) == 0 {
		if _, err := fmt.Fprintln(w, EmptyLine); err != nil {
			return fmt.Errorf("callmeter report: write empty line: %w", err)
		}
	} else {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		lines := append([][]string{t.Header}, t.Rows...)
		for _, cells := range lines {
			if _, err := fmt.Fprintln(tw, strings.Join(cells, "\t")); err != nil {
				return fmt.Errorf("callmeter report: write row: %w", err)
			}
		}
		if err := tw.Flush(); err != nil {
			return fmt.Errorf("callmeter report: flush table: %w", err)
		}
	}
	for _, note := range t.Notes {
		if _, err := fmt.Fprintln(w, "note: "+note); err != nil {
			return fmt.Errorf("callmeter report: write note: %w", err)
		}
	}
	return nil
}

func (f Filter) limit() int {
	if f.Limit <= 0 {
		return DefaultLimit
	}
	return f.Limit
}

// title names the topic, the active filters and the window.
func (f Filter) title(topic string, names *names) string {
	parts := []string{"callmeter " + topic}
	if f.Since.IsZero() {
		parts = append(parts, "window: last 30 days")
	} else {
		parts = append(parts, "window: since "+f.Since.UTC().Format("2006-01-02 15:04")+" UTC")
	}
	if f.Project != "" {
		parts = append(parts, "project="+f.Project)
	}
	if f.AgentType != "" {
		parts = append(parts, "agent-type="+f.AgentType)
	}
	if f.Session != "" {
		parts = append(parts, fmt.Sprintf("session=%s (%s)", f.Session, names.of(f.Session)))
	}
	if len(f.ConfigDirs) > 0 {
		parts = append(parts, "config-dir="+strings.Join(f.ConfigDirs, ","))
	}
	if f.Account != nil {
		parts = append(parts, fmt.Sprintf("account=%d", *f.Account))
	}
	parts = append(parts, fmt.Sprintf("limit=%d", f.limit()))
	return strings.Join(parts, " · ")
}

// where is the calls filter over alias c, as an SQL condition and its args.
func (f Filter) where() (string, []any) {
	conds := []string{"1=1"}
	var args []any
	col := func(name string) string { return "c." + name }
	if !f.Since.IsZero() {
		conds = append(conds, col("ts")+" >= ?")
		args = append(args, f.Since.UnixMilli())
	}
	if f.Project != "" {
		project := filepath.Clean(f.Project)
		under := strings.TrimSuffix(project, "/") + "/"
		conds = append(conds, fmt.Sprintf("(%s = ? OR substr(%s, 1, ?) = ?)", col("cwd"), col("cwd")))
		args = append(args, project, len(under), under)
	}
	if f.AgentType != "" {
		conds = append(conds, col("agent_type")+" = ?")
		args = append(args, f.AgentType)
	}
	if f.Session != "" {
		conds = append(conds, col("session_id")+" = ?")
		args = append(args, f.Session)
	}
	if len(f.ConfigDirs) > 0 {
		marks := make([]string, len(f.ConfigDirs))
		for i, dir := range f.ConfigDirs {
			marks[i] = "?"
			args = append(args, dir)
		}
		conds = append(conds, col("config_dir")+" IN ("+strings.Join(marks, ", ")+")")
	}
	if f.Account != nil {
		conds = append(conds, col("account")+" = ?")
		args = append(args, *f.Account)
	}
	return strings.Join(conds, " AND "), args
}

// names caches chat names; the first lookup error becomes one note line.
type names struct {
	fn    NameOf
	cache map[string]string
	err   error
}

func newNames(fn NameOf) *names { return &names{fn: fn, cache: map[string]string{}} }

func (n *names) of(session string) string {
	if session == "" {
		return "?"
	}
	if name, ok := n.cache[session]; ok {
		return name
	}
	name := "?"
	if n.fn == nil {
		if n.err == nil {
			n.err = fmt.Errorf("no chat-name source was given")
		}
	} else if got, err := n.fn(session); err != nil {
		if n.err == nil {
			n.err = fmt.Errorf("session %s: %w", session, err)
		}
	} else if got != "" {
		name = got
	}
	n.cache[session] = name
	return name
}

func (n *names) notes() []string {
	if n.err == nil {
		return nil
	}
	return []string{"chat names could not be read: " + n.err.Error()}
}

// row scanning without naming database/sql: the store's rows satisfy this.
type rowSource interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
	Close() error
}

// query runs statement and hands each row to scan.
func query(
	ctx context.Context,
	store *callmeter.Store,
	what, statement string,
	args []any,
	scan func(rowSource) error,
) error {
	rows, err := store.DB().QueryContext(ctx, statement, args...)
	if err != nil {
		return fmt.Errorf("callmeter report: query %s: %w", what, err)
	}
	return readRows(rows, what, scan)
}

func readRows(rows rowSource, what string, scan func(rowSource) error) error {
	for rows.Next() {
		if err := scan(rows); err != nil {
			closeErr := rows.Close()
			if closeErr != nil {
				return fmt.Errorf("callmeter report: read %s: %w (close: %v)", what, err, closeErr)
			}
			return fmt.Errorf("callmeter report: read %s: %w", what, err)
		}
	}
	if err := rows.Err(); err != nil {
		closeErr := rows.Close()
		if closeErr != nil {
			return fmt.Errorf("callmeter report: read %s: %w (close: %v)", what, err, closeErr)
		}
		return fmt.Errorf("callmeter report: read %s: %w", what, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("callmeter report: close %s rows: %w", what, err)
	}
	return nil
}

// gapNotes names every gap the window holds: calls not recorded, requests
// still pending, calls with no delivered size, snippets unparsed per status and
// Bash calls never parsed.
func gapNotes(ctx context.Context, store *callmeter.Store, f Filter) ([]string, error) {
	var notes []string
	faultWhere, faultArgs := faultFilter(f, "faults")
	var unrecorded int64
	if err := store.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM faults WHERE stage IN (?, ?, ?) AND "+faultWhere,
		append([]any{callmeter.StagePayload, callmeter.StageStore, callmeter.StageTranscript}, faultArgs...)...,
	).Scan(&unrecorded); err != nil {
		return nil, fmt.Errorf("callmeter report: count unrecorded calls: %w", err)
	}
	if unrecorded > 0 {
		notes = append(
			notes,
			fmt.Sprintf(
				"%d calls not recorded (payload, store or transcript faults; see the faults topic)",
				unrecorded,
			),
		)
	}
	reqWhere, reqArgs := requestFilter(f)
	var pending int64
	if err := store.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM requests r WHERE r.pending = 1 AND "+reqWhere, reqArgs...,
	).Scan(&pending); err != nil {
		return nil, fmt.Errorf("callmeter report: count pending requests: %w", err)
	}
	if pending > 0 {
		notes = append(
			notes,
			fmt.Sprintf("%d requests still pending (context size not yet read from the transcript)", pending),
		)
	}
	where, args := f.where()
	var undelivered int64
	if err := store.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM calls c WHERE c.bytes_delivered IS NULL AND "+where, args...,
	).Scan(&undelivered); err != nil {
		return nil, fmt.Errorf("callmeter report: count calls without delivered bytes: %w", err)
	}
	if undelivered > 0 {
		notes = append(
			notes,
			fmt.Sprintf("%d calls have no delivered size (no PostToolBatch recorded): counted as 0 bytes", undelivered),
		)
	}
	err := query(ctx, store, "unparsed snippets",
		`SELECT p.parse_status, COUNT(*) FROM command_parts p JOIN calls c ON c.tool_use_id = p.tool_use_id
		WHERE COALESCE(p.parse_status, '') != ? AND `+where+` GROUP BY p.parse_status ORDER BY p.parse_status`,
		append([]any{statusOK}, args...),
		func(r rowSource) error {
			var status *string
			var n int64
			if err := r.Scan(&status, &n); err != nil {
				return err
			}
			label := "(none)"
			if status != nil {
				label = *status
			}
			notes = append(notes, fmt.Sprintf("%d snippets unparsed: %s", n, label))
			return nil
		})
	if err != nil {
		return nil, err
	}
	var unparsedCalls int64
	if err := store.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM calls c WHERE c.tool = 'Bash'
		AND NOT EXISTS (SELECT 1 FROM command_parts p WHERE p.tool_use_id = c.tool_use_id) AND `+where, args...,
	).Scan(&unparsedCalls); err != nil {
		return nil, fmt.Errorf("callmeter report: count unparsed Bash calls: %w", err)
	}
	if unparsedCalls > 0 {
		notes = append(
			notes,
			fmt.Sprintf("%d Bash calls skipped by the parser (relative or missing cwd, or no command)", unparsedCalls),
		)
	}
	return notes, nil
}

// faultFilter narrows faults by window and session only: a fault that failed
// to record its call has no calls row to carry the other filters.
func faultFilter(f Filter, table string) (string, []any) {
	conds := []string{"1=1"}
	var args []any
	if !f.Since.IsZero() {
		conds = append(conds, table+".ts >= ?")
		args = append(args, f.Since.UnixMilli())
	}
	if f.Session != "" {
		conds = append(conds, table+".session_id = ?")
		args = append(args, f.Session)
	}
	return strings.Join(conds, " AND "), args
}

// requestFilter narrows requests by window, session, config dir and account.
func requestFilter(f Filter) (string, []any) {
	where, args := faultFilter(f, "r")
	if len(f.ConfigDirs) > 0 {
		marks := make([]string, len(f.ConfigDirs))
		for i, dir := range f.ConfigDirs {
			marks[i] = "?"
			args = append(args, dir)
		}
		where += " AND r.config_dir IN (" + strings.Join(marks, ", ") + ")"
	}
	if f.Account != nil {
		where += " AND r.account = ?"
		args = append(args, *f.Account)
	}
	return where, args
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

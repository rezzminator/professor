package fleetdb

import (
	"context"
	"fmt"
)

const (
	IssueSeverityLow    = "low"
	IssueSeverityMedium = "medium"
	IssueSeverityHigh   = "high"

	IssueStatusOpen = "open"

	// UnidentifiedSender marks an issue whose reporter identity could not be
	// derived. It is stored in place of an empty string on purpose: an empty
	// reporter_session reads as "not yet populated," which is indistinguishable
	// from a genuine bug in the capture path. UNIDENTIFIED is unmistakable —
	// the reporter WAS looked for and none could be proven.
	UnidentifiedSender = "UNIDENTIFIED"
)

// Issue is one durable operator-triaged complaint filed by an agent through
// issue_servicedesk. Reporter fields are captured automatically by the
// caller, exactly as CommsEvent captures a sender — never accepted as tool
// input, so a model can complain but never forge who is complaining.
type Issue struct {
	ID              int64
	AtNS            int64
	Title           string
	Detail          string
	Severity        string
	Area            string
	ReporterSession string
	ReporterLabel   string
	ReporterUUID    string
	ReporterCWD     string
	ReporterEngine  string
	Status          string
}

// RecordIssue appends one issue to the shared operator ledger and returns its
// assigned id.
func (s *Store) RecordIssue(ctx context.Context, issue Issue) (int64, error) {
	if s.db == nil {
		return 0, fmt.Errorf("record issue: %w", s.degraded)
	}
	status := issue.Status
	if status == "" {
		status = IssueStatusOpen
	}
	result, err := s.db.ExecContext(ctx, `
INSERT INTO issues(
  at_ns,title,detail,severity,area,
  reporter_session,reporter_label,reporter_uuid,reporter_cwd,reporter_engine,
  status
) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		issue.AtNS,
		issue.Title,
		issue.Detail,
		issue.Severity,
		issue.Area,
		issue.ReporterSession,
		issue.ReporterLabel,
		issue.ReporterUUID,
		issue.ReporterCWD,
		issue.ReporterEngine,
		status,
	)
	if err != nil {
		return 0, fmt.Errorf("record issue: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("record issue: read inserted id: %w", err)
	}
	return id, nil
}

// Issues returns every issue newest first. includeClosed also returns issues
// whose status is not "open"; the default view keeps a growing ledger from
// burying what still needs a human.
func (s *Store) Issues(ctx context.Context, includeClosed bool) ([]Issue, error) {
	if s.db == nil {
		return nil, fmt.Errorf("query issues: %w", s.degraded)
	}
	query := `
SELECT id,at_ns,title,detail,severity,area,
       reporter_session,reporter_label,reporter_uuid,reporter_cwd,reporter_engine,
       status
FROM issues`
	args := []any{}
	if !includeClosed {
		query += " WHERE status = ?"
		args = append(args, IssueStatusOpen)
	}
	query += " ORDER BY at_ns DESC, id DESC"
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query issues: %w", err)
	}
	return collectRows(rows, func(rows rowSet) (Issue, error) {
		var issue Issue
		err := rows.Scan(
			&issue.ID,
			&issue.AtNS,
			&issue.Title,
			&issue.Detail,
			&issue.Severity,
			&issue.Area,
			&issue.ReporterSession,
			&issue.ReporterLabel,
			&issue.ReporterUUID,
			&issue.ReporterCWD,
			&issue.ReporterEngine,
			&issue.Status,
		)
		return issue, err
	}, "scan issue", "iterate issues", "close issue rows")
}

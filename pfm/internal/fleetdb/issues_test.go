package fleetdb

import (
	"context"
	"testing"
)

// TestRecordIssueRoundTripsThroughIssues pins the write half of the
// servicedesk ledger: RecordIssue returns a real assigned id, and every
// field it wrote — including the five reporter-identity columns — comes
// back unchanged through Issues().
func TestRecordIssueRoundTripsThroughIssues(t *testing.T) {
	state, _ := openTestStore(t)
	ctx := context.Background()

	id, err := state.RecordIssue(ctx, Issue{
		AtNS:            1000,
		Title:           "leak-check false green",
		Detail:          "zsh no-word-split hid a nonzero exit",
		Severity:        IssueSeverityHigh,
		Area:            "scripts/leak-check.sh",
		ReporterSession: "cc-1234",
		ReporterLabel:   "Gate Runner",
		ReporterUUID:    "uuid-1",
		ReporterCWD:     "/work/repo",
		ReporterEngine:  "claude",
	})
	if err != nil {
		t.Fatalf("RecordIssue: %v", err)
	}
	if id == 0 {
		t.Fatalf("RecordIssue returned id=0, want an assigned row id")
	}

	issues, err := state.Issues(ctx, false)
	if err != nil {
		t.Fatalf("Issues: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("Issues() = %d rows, want 1: %+v", len(issues), issues)
	}
	got := issues[0]
	if got.ID != id || got.Title != "leak-check false green" ||
		got.Detail != "zsh no-word-split hid a nonzero exit" ||
		got.Severity != IssueSeverityHigh || got.Area != "scripts/leak-check.sh" ||
		got.ReporterSession != "cc-1234" || got.ReporterLabel != "Gate Runner" ||
		got.ReporterUUID != "uuid-1" || got.ReporterCWD != "/work/repo" ||
		got.ReporterEngine != "claude" || got.Status != IssueStatusOpen {
		t.Fatalf("round-tripped issue = %+v, want the recorded fields back verbatim with id %d", got, id)
	}
}

// TestIssuesFiltersToOpenUnlessIncludeClosed pins the read half: the
// default view (includeClosed=false) hides a closed issue entirely, and
// includeClosed=true is the only way to see both.
func TestIssuesFiltersToOpenUnlessIncludeClosed(t *testing.T) {
	state, _ := openTestStore(t)
	ctx := context.Background()

	openID, err := state.RecordIssue(ctx, Issue{
		AtNS: 1, Title: "open one", Detail: "d", Severity: IssueSeverityMedium,
	})
	if err != nil {
		t.Fatalf("RecordIssue open: %v", err)
	}
	closedID, err := state.RecordIssue(ctx, Issue{
		AtNS: 2, Title: "closed one", Detail: "d", Severity: IssueSeverityMedium,
		Status: "closed",
	})
	if err != nil {
		t.Fatalf("RecordIssue closed: %v", err)
	}

	openOnly, err := state.Issues(ctx, false)
	if err != nil {
		t.Fatalf("Issues(false): %v", err)
	}
	if len(openOnly) != 1 || openOnly[0].ID != openID {
		t.Fatalf("Issues(false) = %+v, want only the open issue %d", openOnly, openID)
	}

	all, err := state.Issues(ctx, true)
	if err != nil {
		t.Fatalf("Issues(true): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("Issues(true) = %d rows, want 2: %+v", len(all), all)
	}
	seen := map[int64]bool{}
	for _, issue := range all {
		seen[issue.ID] = true
	}
	if !seen[openID] || !seen[closedID] {
		t.Fatalf("Issues(true) ids = %v, want %d and %d present", seen, openID, closedID)
	}
}

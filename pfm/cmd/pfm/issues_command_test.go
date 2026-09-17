package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"hostops/pfm/internal/fleetdb"
	"hostops/pfm/internal/paths"
)

// TestRunIssuesDefaultsToOpenOnlyAndJSONAllReturnsEverything pins the read
// surface's two visibly distinct states: the default view lists only
// status="open" rows in the operator-facing columns, and --json --all is
// the only combination that returns every row, including closed ones, as a
// real JSON array.
func TestRunIssuesDefaultsToOpenOnlyAndJSONAllReturnsEverything(t *testing.T) {
	jailTest(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	state := fleetdb.Open(ctx, resolved)
	if _, err := state.RecordIssue(ctx, fleetdb.Issue{
		AtNS: 1, Title: "open issue", Detail: "still needs a human",
		Severity: fleetdb.IssueSeverityLow, Area: "areaA",
		ReporterLabel: "Gate Runner",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordIssue(ctx, fleetdb.Issue{
		AtNS: 2, Title: "closed issue", Detail: "already handled",
		Severity: fleetdb.IssueSeverityHigh, Area: "areaB",
		ReporterSession: fleetdb.UnidentifiedSender, Status: "closed",
	}); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"issues"}, &stdout, &stderr); code != 0 {
		t.Fatalf("pfm issues code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "open issue") {
		t.Fatalf("pfm issues stdout=%q, want the open issue listed", stdout.String())
	}
	if strings.Contains(stdout.String(), "closed issue") {
		t.Fatalf("pfm issues stdout=%q, want the closed issue hidden by default", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"issues", "--all", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("pfm issues --all --json code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var decoded []fleetdb.Issue
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("decode --all --json output %q: %v", stdout.String(), err)
	}
	if len(decoded) != 2 {
		t.Fatalf("pfm issues --all --json = %d rows, want 2: %+v", len(decoded), decoded)
	}
}

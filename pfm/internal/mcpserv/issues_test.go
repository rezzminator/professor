package mcpserv

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/shared"
)

// newIssuesTestService builds a Service with an isolated shared database and
// AllowAmbientIdentity left at its zero value (false) — the shared HTTP
// daemon's real posture, and the one issueServicedesk falls back to when a
// caller carries no _meta.threadId.
func newIssuesTestService(t *testing.T) *Service {
	t.Helper()
	setupBackendFixture(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewConfigured("test", nil, Runtime{Paths: resolved})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service
}

func TestIssueServicedeskRejectsEmptyTitle(t *testing.T) {
	service := newIssuesTestService(t)
	_, _, err := service.issueServicedesk(context.Background(), nil, IssueInput{
		Title: "   ", Detail: "something broke",
	})
	if err == nil || !strings.Contains(err.Error(), "title is required") {
		t.Fatalf("issueServicedesk empty title error = %v, want a title-is-required refusal", err)
	}
}

func TestIssueServicedeskRejectsEmptyDetail(t *testing.T) {
	service := newIssuesTestService(t)
	_, _, err := service.issueServicedesk(context.Background(), nil, IssueInput{
		Title: "a real title", Detail: "  ",
	})
	if err == nil || !strings.Contains(err.Error(), "detail is required") {
		t.Fatalf("issueServicedesk empty detail error = %v, want a detail-is-required refusal", err)
	}
}

func TestIssueServicedeskRejectsSeverityOutsideTheThreeValues(t *testing.T) {
	service := newIssuesTestService(t)
	_, _, err := service.issueServicedesk(context.Background(), nil, IssueInput{
		Title: "a real title", Detail: "a real detail", Severity: "urgent",
	})
	if err == nil || !strings.Contains(err.Error(), "severity must be") {
		t.Fatalf("issueServicedesk bad severity error = %v, want a severity-must-be refusal", err)
	}
}

func TestIssueServicedeskDefaultsSeverityToMedium(t *testing.T) {
	service := newIssuesTestService(t)
	_, output, err := service.issueServicedesk(context.Background(), nil, IssueInput{
		Title: "no severity given", Detail: "defaults matter",
	})
	if err != nil {
		t.Fatalf("issueServicedesk: %v", err)
	}
	if output.Status != "ok" || output.ID == 0 {
		t.Fatalf("issueServicedesk output = %+v, want ok with an assigned id", output)
	}
	stored, err := service.backend.sharedState.Issues(context.Background(), true)
	if err != nil {
		t.Fatalf("Issues: %v", err)
	}
	if len(stored) != 1 || stored[0].Severity != shared.IssueSeverityMedium {
		t.Fatalf("stored severity = %+v, want exactly one row with severity %q", stored, shared.IssueSeverityMedium)
	}
}

// TestIssueServicedeskRecordsUnidentifiedSenderWhenNoCallerIdentity is the
// load-bearing test for the whole ReporterSession field: a filer whose
// identity cannot be derived (no MCP _meta.threadId, ambient identity not
// permitted — the shared HTTP daemon's ordinary case) must still be
// recorded, and recorded under the literal shared.UnidentifiedSender
// sentinel, never an empty string. An empty reporter_session is
// indistinguishable from "column not populated yet"; only the sentinel says
// "looked, found nobody."
func TestIssueServicedeskRecordsUnidentifiedSenderWhenNoCallerIdentity(t *testing.T) {
	service := newIssuesTestService(t)
	if service.backend.allowAmbientIdentity {
		t.Fatal("fixture must start with ambient identity disallowed to exercise the no-identity path")
	}
	_, output, err := service.issueServicedesk(context.Background(), nil, IssueInput{
		Title: "filed with no caller identity", Detail: "the daemon cannot see who called",
	})
	if err != nil {
		t.Fatalf("issueServicedesk: %v", err)
	}
	stored, err := service.backend.sharedState.Issues(context.Background(), true)
	if err != nil {
		t.Fatalf("Issues: %v", err)
	}
	var row *shared.Issue
	for index := range stored {
		if stored[index].ID == output.ID {
			row = &stored[index]
		}
	}
	if row == nil {
		t.Fatalf("filed issue id %d not found in %+v", output.ID, stored)
	}
	if row.ReporterSession == "" {
		t.Fatalf(
			"reporter_session is empty, want the %q sentinel — an empty column is indistinguishable from a capture-path bug",
			shared.UnidentifiedSender,
		)
	}
	if row.ReporterSession != shared.UnidentifiedSender {
		t.Fatalf("reporter_session = %q, want the literal sentinel %q", row.ReporterSession, shared.UnidentifiedSender)
	}
}

// TestIssueInputCarriesNoReporterIdentityField is a contract test, not a
// behavioral one: IssueInput must never grow a field a caller can use to
// state its own reporter identity — that is captured automatically, the
// same way chat_inject captures a sender, so a model can complain but never
// forge who is complaining. This fails the moment IssueInput's field set
// changes at all, which is deliberate: any addition must be reviewed against
// exactly this invariant before the list below is updated.
func TestIssueInputCarriesNoReporterIdentityField(t *testing.T) {
	fieldType := reflect.TypeOf(IssueInput{})
	names := make([]string, 0, fieldType.NumField())
	for index := 0; index < fieldType.NumField(); index++ {
		tag := fieldType.Field(index).Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "" {
			name = fieldType.Field(index).Name
		}
		names = append(names, name)
	}
	want := []string{"title", "detail", "severity", "area"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf(
			"IssueInput json fields = %v, want exactly %v — reporter identity (session/label/uuid/cwd/engine) must come only from the capture path, never from caller input",
			names,
			want,
		)
	}
}

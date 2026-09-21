package mcpserv

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
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
	if len(stored) != 1 || stored[0].Severity != fleetdb.IssueSeverityMedium {
		t.Fatalf("stored severity = %+v, want exactly one row with severity %q", stored, fleetdb.IssueSeverityMedium)
	}
}

// TestIssueServicedeskRecordsUnidentifiedSenderWhenNoCallerIdentity is the
// load-bearing test for the whole ReporterSession field: a filer whose
// identity cannot be derived (no MCP _meta.threadId, ambient identity not
// permitted — the shared HTTP daemon's ordinary case) must still be
// recorded, and recorded under the literal fleetdb.UnidentifiedSender
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
	var row *fleetdb.Issue
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
			fleetdb.UnidentifiedSender,
		)
	}
	if row.ReporterSession != fleetdb.UnidentifiedSender {
		t.Fatalf("reporter_session = %q, want the literal sentinel %q", row.ReporterSession, fleetdb.UnidentifiedSender)
	}
}

// TestIssueReporterSeparatesAFailedIdentityLookupFromNoIdentity pins the
// root law on the reporter capture path: a caller that PRESENTED a thread id
// whose lookup could not run (here the fleet scan itself fails, because the
// chat verb layer is not configured) must not be filed under the same
// sentinel as a caller that presented no identity at all — "we failed to
// look" is not "nobody was there" — and the failure must leave a record
// naming it, never fall through silently.
func TestIssueReporterSeparatesAFailedIdentityLookupFromNoIdentity(t *testing.T) {
	service := newIssuesTestService(t)
	if service.backend.chat != nil {
		t.Fatal("fixture must leave the chat verb layer unconfigured so the caller scan fails")
	}
	ctx, recorder := obs.Test(t)
	request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{"threadId": "thread-a"}}}
	_, output, err := service.issueServicedesk(ctx, request, IssueInput{
		Title: "filed while the fleet could not be read", Detail: "the scan itself failed",
	})
	if err != nil {
		t.Fatalf("issueServicedesk: %v", err)
	}
	stored, err := service.backend.sharedState.Issues(ctx, true)
	if err != nil {
		t.Fatalf("Issues: %v", err)
	}
	var row *fleetdb.Issue
	for index := range stored {
		if stored[index].ID == output.ID {
			row = &stored[index]
		}
	}
	if row == nil {
		t.Fatalf("filed issue id %d not found in %+v", output.ID, stored)
	}
	if row.ReporterSession == fleetdb.UnidentifiedSender {
		t.Fatalf(
			"reporter_session = %q for a lookup that FAILED, the same sentinel a caller with no identity gets — the two states are indistinguishable",
			row.ReporterSession,
		)
	}
	if row.ReporterSession == "" {
		t.Fatalf("reporter_session is empty; a failed lookup must be marked, never blank")
	}
	var named bool
	for _, record := range recorder.Records() {
		if strings.Contains(record.Message, "caller") && record.Level != "INFO" {
			named = true
		}
	}
	if !named {
		t.Fatalf("a failed MCP caller lookup left no record naming it: %s", recorder.Raw())
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

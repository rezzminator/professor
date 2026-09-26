package mcpserv

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

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

// TestChatInstructionsRouteComplaintsToServicedesk pins the chat server's
// initialize Instructions to the contracts' chat part, byte for byte,
// ending in the shell-only sentence after the servicedesk routing clause
// (never issue_servicedesk).
func TestChatInstructionsRouteComplaintsToServicedesk(t *testing.T) {
	service := newIssuesTestService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := service.Server().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "pfm-test", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	want := "Message another running chat → chat_inject; list running chats → chat_ls; who am I → chat_whoami; start a chat → chat_new; is a chat idle, what is it doing → chat_status; its last answer → chat_last; find, then read an old transcript → chat_find, chat_read; dump my transcript to a file → chat_save; compact myself at a milestone → chat_self_compact; complain about Professor itself → servicedesk. Chats are independent running sessions, never sub-agents. end, modal, watch, stream, recover, and history stay shell-only pfm chat commands."
	got := clientSession.InitializeResult().Instructions
	if got != want {
		t.Fatalf("chat server Instructions =\n%q\nwant\n%q", got, want)
	}
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
			tool, ok := record.Field("tool")
			if !ok || tool != "servicedesk" {
				t.Fatalf(
					"mcp.caller record tool = %v (present=%v), want %q: %s",
					tool,
					ok,
					"servicedesk",
					recorder.Raw(),
				)
			}
		}
	}
	if !named {
		t.Fatalf("a failed MCP caller lookup left no record naming it: %s", recorder.Raw())
	}
}

// TestToolNamesHoldsServicedesk pins the roster entry directly, independent
// of the jailed stdio protocol test.
func TestToolNamesHoldsServicedesk(t *testing.T) {
	names := ToolNames()
	if !slices.Contains(names, "servicedesk") {
		t.Fatalf("ToolNames() = %v, want it to hold %q", names, "servicedesk")
	}
}

// TestServicedeskCallableByNameOverInMemorySession exercises the tool the
// way a real client does: connected in-memory to the service's own server,
// calling it by its registered name rather than the Go method directly.
func TestServicedeskCallableByNameOverInMemorySession(t *testing.T) {
	service := newIssuesTestService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := service.Server().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "pfm-test", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name: "servicedesk",
		Arguments: IssueInput{
			Title:  "filed by name over an in-memory session",
			Detail: "the roster rename must not break the call path",
		},
	})
	if err != nil {
		t.Fatalf("CallTool servicedesk: %v", err)
	}
	if result.IsError {
		t.Fatalf("servicedesk call reported an error: %+v", result)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var output IssueOutput
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatalf("unmarshal IssueOutput: %v (content: %s)", err, raw)
	}
	if output.Status != "ok" || output.ID == 0 {
		t.Fatalf("servicedesk output = %+v, want ok with an assigned id", output)
	}
}

// TestServicedeskCallMissingTitleOrDetailIsToolError pins the "missing
// title or detail is a tool error" contract row over the same in-memory
// call path as the successful call above.
func TestServicedeskCallMissingTitleOrDetailIsToolError(t *testing.T) {
	service := newIssuesTestService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := service.Server().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "pfm-test", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{
		Name:      "servicedesk",
		Arguments: IssueInput{Detail: "title is missing"},
	})
	if err != nil {
		t.Fatalf("CallTool servicedesk: %v", err)
	}
	if !result.IsError {
		t.Fatalf("servicedesk call with no title = %+v, want a tool error", result)
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

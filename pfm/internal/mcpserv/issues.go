package mcpserv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// compMCP is the component mcpserv's own records belong to, the same one
// obs.Tool scopes every tool call with.
const compMCP = "mcp"

// reporterLookupFailed marks an issue whose reporter lookup could not RUN —
// the fleet scan behind a presented _meta.threadId failed. It is distinct
// from fleetdb.UnidentifiedSender on purpose: that sentinel promises the
// reporter was looked for and none could be proven, which is a different
// fact from "the search itself broke."
const reporterLookupFailed = fleetdb.UnidentifiedSender + "-LOOKUP-FAILED"

// issueServicedesk files one agent complaint into the shared operator ledger.
// It never refuses for want of identity: a complaint from a process whose
// sender cannot be derived is still worth keeping, but it is recorded under
// the UNIDENTIFIED sentinel so that fact is visible, never mistaken for an
// unpopulated column. Writes that fail return a real error so the caller sees
// "not filed" instead of a false "filed."
func (service *Service) issueServicedesk(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input IssueInput,
) (*mcp.CallToolResult, IssueOutput, error) {
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return nil, IssueOutput{}, fmt.Errorf("title is required")
	}
	detail := strings.TrimSpace(input.Detail)
	if detail == "" {
		return nil, IssueOutput{}, fmt.Errorf("detail is required")
	}
	severity := strings.TrimSpace(input.Severity)
	if severity == "" {
		severity = fleetdb.IssueSeverityMedium
	}
	if severity != fleetdb.IssueSeverityLow &&
		severity != fleetdb.IssueSeverityMedium &&
		severity != fleetdb.IssueSeverityHigh {
		return nil, IssueOutput{}, fmt.Errorf(
			"severity must be %q, %q, or %q, got %q",
			fleetdb.IssueSeverityLow, fleetdb.IssueSeverityMedium, fleetdb.IssueSeverityHigh,
			input.Severity,
		)
	}
	reporter := service.issueReporter(ctx, request)
	id, err := service.backend.sharedState.RecordIssue(ctx, fleetdb.Issue{
		AtNS:            service.backend.clock.Now().UnixNano(),
		Title:           title,
		Detail:          detail,
		Severity:        severity,
		Area:            strings.TrimSpace(input.Area),
		ReporterSession: reporter.Session,
		ReporterLabel:   reporter.Label,
		ReporterUUID:    reporter.UUID,
		ReporterCWD:     reporter.CWD,
		ReporterEngine:  reporter.Engine,
	})
	if err != nil {
		return nil, IssueOutput{}, fmt.Errorf("file issue: %w", err)
	}
	return nil, IssueOutput{Status: "ok", ID: id}, nil
}

// issueReporter is the five identity fields an issue records about whoever
// filed it.
type issueReporter struct {
	Session string
	Label   string
	UUID    string
	CWD     string
	Engine  string
}

// issueReporter derives the calling chat's identity with the same precedence
// call signs with: a validated Codex _meta.threadId first, then
// ambient environment/ancestry recovery ONLY where that is meaningful (the
// per-chat stdio transport), and the UNIDENTIFIED sentinel when neither
// applies — which is exactly the shared HTTP daemon's normal case, since its
// one process serves every chat on the machine and has no ambient caller of
// its own to mistake for one.
func (service *Service) issueReporter(
	ctx context.Context,
	request *mcp.CallToolRequest,
) issueReporter {
	caller, err := service.backend.callerForRequest(ctx, requestMeta(request))
	if err == nil && caller.valid {
		return issueReporter{
			Session: caller.identity.Session,
			Label:   caller.row.Name,
			UUID:    caller.identity.ID,
			CWD:     caller.row.Dir,
			Engine:  caller.identity.Engine,
		}
	}
	if err == nil && caller.err != nil && !errors.Is(caller.err, resolve.ErrNotInChat) &&
		!errors.Is(caller.err, resolve.ErrNotRegistered) {
		err = caller.err
	}
	if err != nil {
		return issueReporterFailure(ctx, err)
	}

	if !service.backend.allowAmbientIdentity {
		return issueReporter{Session: fleetdb.UnidentifiedSender}
	}
	identifier, identifierErr := resolve.NewWhoami(resolve.WhoamiDependencies{CodexSeats: service.backend.codexSeats})
	if identifierErr != nil {
		return issueReporterFailure(ctx, fmt.Errorf("build ambient issue reporter identity: %w", identifierErr))
	}
	identity, identifyErr := identifier.Identify(ctx)
	if identifyErr != nil {
		if errors.Is(identifyErr, resolve.ErrNotInChat) || errors.Is(identifyErr, resolve.ErrNotRegistered) {
			return issueReporter{Session: fleetdb.UnidentifiedSender}
		}
		return issueReporterFailure(ctx, fmt.Errorf("resolve ambient issue reporter: %w", identifyErr))
	}
	if identity.Session == "" {
		return issueReporterFailure(ctx, fmt.Errorf("resolve ambient issue reporter: caller has no session"))
	}
	reporter := issueReporter{
		Session: identity.Session,
		UUID:    identity.ID,
		Engine:  identity.Engine,
	}
	listed, listErr := service.backend.list(ctx, LSInput{All: true})
	if listErr != nil {
		return reporter
	}
	for index := range listed.Rows {
		row := &listed.Rows[index]
		if row.Session == identity.Session {
			reporter.Label = row.Name
			reporter.CWD = row.Dir
			break
		}
	}
	return reporter
}

func issueReporterFailure(ctx context.Context, err error) issueReporter {
	scoped := obs.Component(ctx, compMCP)
	obs.Logger(scoped).
		LogAttrs(scoped, slog.LevelError, "mcp.caller", slog.String("op", "resolve"), slog.String("tool", "servicedesk"), slog.String(obs.FieldErr, err.Error()))
	return issueReporter{Session: reporterLookupFailed}
}

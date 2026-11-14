package mcpserv

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// callToolWithMeta is the protocol-level counterpart of callTool. The
// caller identity is deliberately carried in MCP's reserved _meta envelope,
// never in the tool arguments where a model could spoof another seat.
func callToolWithMeta[T any](
	t *testing.T,
	session *mcp.ClientSession,
	name string,
	meta mcp.Meta,
	arguments any,
) T {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Meta:      meta,
		Name:      name,
		Arguments: arguments,
	})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if result.IsError {
		content, marshalErr := json.Marshal(result.Content)
		if marshalErr != nil {
			t.Fatalf("%s returned tool error: %#v", name, result.Content)
		}
		t.Fatalf("%s returned tool error: %s", name, content)
	}
	content, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var output T
	if err := json.Unmarshal(content, &output); err != nil {
		t.Fatalf("%s structured output %s: %v", name, content, err)
	}
	return output
}

type metadataNamedResolver struct {
	fallback pfmcall.Resolver
	name     string
	socket   string
	pane     string
}

func (resolver metadataNamedResolver) Resolve(
	ctx context.Context,
	kind resolve.Kind,
	query string,
) (resolve.Outcome, error) {
	if query == resolver.name {
		return resolve.Outcome{Stdout: resolver.socket + "\t" + resolver.pane + "\n"}, nil
	}
	return resolver.fallback.Resolve(ctx, kind, query)
}

func TestMCPMetadataIdentityRejectsAmbiguousRowsAndNamesListFailures(t *testing.T) {
	setupBackendPaths(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	live := compose.Row{
		SessionName: "cx-seat", ID: "thread-a", Kind: compose.LiveCodex, Socket: "cx-seat", PaneID: "%0",
	}
	duplicateBackend := &backend{
		paths: resolved,
		chat:  &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{live, live}}},
	}
	caller, err := duplicateBackend.callerForRequest(
		context.Background(), mcp.Meta{"threadId": "thread-a"},
	)
	if err != nil || caller.valid || !caller.present ||
		!strings.Contains(caller.detail, "multiple live Codex seats hold thread thread-a") {
		t.Fatalf("duplicate caller resolution = %+v err=%v", caller, err)
	}

	listBackend := &backend{
		paths: resolved,
		chat:  &fakeChatVerbs{err: errors.New("fleet database busy")},
	}
	caller, err = listBackend.callerForRequest(
		context.Background(), mcp.Meta{"threadId": "thread-a"},
	)
	const wantDetail = `resolve MCP caller thread "thread-a": list live chats: fleet database busy`
	if err != nil || !caller.present || caller.valid || caller.detail != wantDetail ||
		identityFailureStatus(caller.err) != statusError {
		t.Fatalf("list failure caller = %+v err=%v, want present-invalid %q, never absent", caller, err, wantDetail)
	}

	for _, malformed := range []compose.Row{
		{SessionName: "cx-seat", ID: "thread-a", Kind: compose.ResumeCodex, Socket: "cx-seat", PaneID: "%0"},
		{SessionName: "cx-seat", ID: "thread-a", Kind: compose.LiveCodex, Socket: "../cx-seat", PaneID: "%0"},
	} {
		malformedBackend := &backend{
			paths: resolved,
			chat:  &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{malformed}}},
		}
		caller, err := malformedBackend.callerForRequest(
			context.Background(), mcp.Meta{"threadId": "thread-a"},
		)
		if err != nil || caller.valid {
			t.Fatalf("malformed row resolved: row=%+v caller=%+v err=%v", malformed, caller, err)
		}
	}
}

func TestBirthMixedProxyIdentityRefused(t *testing.T) {
	current := &backend{
		chat: &fakeChatVerbs{
			listed: chat.ListResult{
				Rows: []compose.Row{
					{
						Backend:        "herdr",
						SeatID:         "87caf6bf",
						SessionName:    "87caf6bf",
						ID:             "thread-a",
						Kind:           compose.LiveCodex,
						Socket:         "/h/herdr.sock",
						PaneID:         "wC:p24",
						HerdrWorkspace: "wC",
					},
				},
			},
		},
	}
	caller, err := current.callerForRequest(
		context.Background(),
		map[string]any{
			"pfmProxy": map[string]any{
				"v":          1,
				"session":    "87caf6bf",
				"backend":    "tmux",
				"socketPath": "/tmp/tmux-1000/cc-1",
				"pane":       "%3",
			},
		},
	)
	want := "MCP caller location disagrees with its live chat: payload tmux /tmp/tmux-1000/cc-1 %3, row herdr /h/herdr.sock wC:p24"
	if err != nil || caller.valid || caller.detail != want {
		t.Fatalf("caller=%+v err=%v, want %q", caller, err, want)
	}
}

func TestBirthProxyIdentityComesWhollyFromRow(t *testing.T) {
	row := compose.Row{
		Backend:        "herdr",
		SeatID:         "87caf6bf",
		SessionName:    "87caf6bf",
		ID:             "old-transcript",
		Kind:           compose.LiveClaude,
		Socket:         "/h/herdr.sock",
		PaneID:         "wC:p24",
		HerdrWorkspace: "wC",
	}
	current := &backend{chat: &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}}}}
	caller, err := current.callerForRequest(
		context.Background(),
		map[string]any{
			"pfmProxy": map[string]any{
				"v":          1,
				"session":    "87caf6bf",
				"id":         "new-transcript",
				"socketPath": "/h/herdr.sock",
				"pane":       "wC:p24",
			},
		},
	)
	if err != nil || !caller.valid {
		t.Fatalf("caller=%+v err=%v", caller, err)
	}
	want, err := current.identityFromRow(caller.row)
	if err != nil {
		t.Fatal(err)
	}
	want.ID = "new-transcript"
	want.Source = "mcp-proxy"
	if !reflect.DeepEqual(caller.identity, want) {
		t.Fatalf("identity=%+v, row identity=%+v", caller.identity, want)
	}
}

func TestBirthProxyBackendDisagreementRefused(t *testing.T) {
	current := &backend{
		chat: &fakeChatVerbs{
			listed: chat.ListResult{
				Rows: []compose.Row{
					{
						Backend:        "herdr",
						SeatID:         "87caf6bf",
						SessionName:    "87caf6bf",
						ID:             "thread-a",
						Kind:           compose.LiveCodex,
						Socket:         "/h/herdr.sock",
						PaneID:         "wC:p24",
						HerdrWorkspace: "wC",
					},
				},
			},
		},
	}
	caller, err := current.callerForRequest(
		context.Background(),
		map[string]any{
			"pfmProxy": map[string]any{
				"v":          1,
				"session":    "87caf6bf",
				"backend":    "tmux",
				"socketPath": "/h/herdr.sock",
				"pane":       "wC:p24",
			},
		},
	)
	if err != nil || caller.valid {
		t.Fatalf("backend disagreement caller=%+v err=%v, want refused", caller, err)
	}
}

func TestCallEngineForRequestFindsThreadIDBeyondPublicListLimit(t *testing.T) {
	setupBackendPaths(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]compose.Row, defaultChatLSLimit+1)
	for index := range defaultChatLSLimit {
		rows[index] = compose.Row{ID: "historical", Kind: compose.ResumeClaude}
	}
	rows[defaultChatLSLimit] = compose.Row{
		SessionName: "cx-seat", ID: "thread-a", Kind: compose.LiveCodex, Socket: "cx-seat", PaneID: "%7",
	}
	verbs := &cappedProxyChat{fakeChatVerbs: &fakeChatVerbs{
		listed: chat.ListResult{Rows: rows, Matched: len(rows)},
	}}
	caller, err := (&backend{paths: resolved, chat: verbs}).callerForRequest(context.Background(), mcp.Meta{
		"threadId": "thread-a",
	})
	if err != nil || !caller.valid || caller.row.Session != "cx-seat" {
		t.Fatalf("threadId caller beyond public list limit = %+v err=%v", caller, err)
	}
}

func TestCallEngineForRequestKeepsThreadIDPrecedenceAndEmptyMetadata(t *testing.T) {
	setupBackendPaths(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	live := compose.Row{
		SessionName: "cx-seat", ID: "thread-a", Kind: compose.LiveCodex, Socket: "cx-seat", PaneID: "%0",
	}
	current := &backend{
		paths: resolved,
		chat:  &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{live}, Matched: 1}},
	}
	caller, err := current.callerForRequest(context.Background(), mcp.Meta{
		"threadId": "thread-a",
		"pfmProxy": map[string]any{"v": 0, "session": "must-not-be-read"},
	})
	if err != nil || !caller.valid || caller.identity.Source != "mcp-thread-meta" || caller.identity.ID != "thread-a" {
		t.Fatalf("threadId did not retain precedence: caller=%+v err=%v", caller, err)
	}

	empty, err := current.callerForRequest(context.Background(), nil)
	if err != nil || !reflect.DeepEqual(empty, callerIdentity{}) {
		t.Fatalf("metadata-free caller = %+v err=%v, want zero caller", empty, err)
	}
}

package mcpserv

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

type cappedProxyChat struct {
	*fakeChatVerbs
}

func (fake *cappedProxyChat) List(_ context.Context, request chat.ListRequest) (chat.ListResult, error) {
	result := fake.listed
	if request.Limit > 0 && len(result.Rows) > request.Limit {
		result.Rows = result.Rows[:request.Limit]
		result.Truncated = true
	}
	return result, fake.err
}

// TestChatRowStateNamesTheKilledButLiveContradiction is the honesty half of the
// kill regression. A kill that closed nothing still wrote its tombstone row, so
// chat_ls handed back a row asserting BOTH things at once — killed:true beside
// state "idle" over a live kind — and a caller had no way to tell a real kill
// from a de-listing. The state names the contradiction instead of collapsing
// it into either half.
func TestChatRowStateNamesTheKilledButLiveContradiction(t *testing.T) {
	for _, kind := range []compose.Kind{
		compose.LiveClaude,
		compose.LiveCodex,
		compose.LiveOpenCode,
		compose.LiveSplit,
		compose.Agent,
		compose.Booting,
	} {
		row := compose.Row{Kind: kind, Killed: true}
		if state := chatRowState(row); state != "killed-but-live" {
			t.Errorf(
				"killed %s row reports state %q, want %q",
				kind, state, "killed-but-live",
			)
		}
	}
}

// TestChatRowStateKeepsEveryOtherVerdict pins the arms the contradiction check
// must not have swallowed: an unkilled live row is idle, a booting row says so,
// a resumable row stays resumable, and a killed row that is NOT live is an
// ordinary de-listed row with nothing to contradict.
func TestChatRowStateKeepsEveryOtherVerdict(t *testing.T) {
	cases := []struct {
		name string
		row  compose.Row
		want string
	}{
		{"live claude", compose.Row{Kind: compose.LiveClaude}, "idle"},
		{"live codex", compose.Row{Kind: compose.LiveCodex}, "idle"},
		{"live opencode", compose.Row{Kind: compose.LiveOpenCode}, "idle"},
		{"booting", compose.Row{Kind: compose.Booting}, "booting"},
		{"resumable", compose.Row{Kind: compose.ResumeClaude}, "resumable"},
		{
			"killed resumable",
			compose.Row{Kind: compose.ResumeClaude, Killed: true},
			"resumable",
		},
	}
	for _, testCase := range cases {
		if state := chatRowState(testCase.row); state != testCase.want {
			t.Errorf(
				"%s: state = %q, want %q",
				testCase.name, state, testCase.want,
			)
		}
	}
}

func TestListProjectedKeepsDistinctPrivateTranscriptPaths(t *testing.T) {
	rows := []compose.Row{
		{ID: "first", Path: "/transcripts/first.jsonl", Kind: compose.LiveClaude},
		{ID: "second", Path: "/transcripts/second.jsonl", Kind: compose.LiveClaude},
	}
	current := &backend{chat: &fakeChatVerbs{listed: chat.ListResult{Rows: rows, Matched: len(rows)}}}
	listed, err := current.list(context.Background(), LSInput{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Rows) != 2 || listed.Rows[0].transcriptPath != rows[0].Path ||
		listed.Rows[1].transcriptPath != rows[1].Path {
		t.Fatalf("projected private paths = %+v, want each source row path", listed.Rows)
	}
}

func TestCallerForRequestResolvesProxyIdentity(t *testing.T) {
	row := compose.Row{
		SessionName: "cc-seat", ID: "session-id", CWD: "/work/proxy", Project: "proxy",
		Name: "Proxy Claude", Kind: compose.LiveClaude, Socket: "cc-seat", PaneID: "%7",
	}
	current := &backend{chat: &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}}}
	meta := mcp.Meta{"pfmProxy": map[string]any{
		"v": ProxyWireVersion, "session": "cc-seat", "socketPath": "/tmp/tmux-1000/cc-seat",
		"socketName": "cc-seat", "pane": "%7", "engine": "claude", "id": "session-id", "source": "tmux",
	}}
	caller, err := current.callerForRequest(context.Background(), meta)
	if err != nil {
		t.Fatal(err)
	}
	if !caller.present || !caller.valid || caller.row.Session != "cc-seat" || caller.row.Dir != "/work/proxy" {
		t.Fatalf("caller = %+v, want the unique live row", caller)
	}
	if caller.identity.Session != "cc-seat" || caller.identity.SocketPath != "/tmp/tmux-1000/cc-seat" ||
		caller.identity.SocketName != "cc-seat" || caller.identity.Pane != "%7" ||
		caller.identity.Engine != "claude" || caller.identity.ID != "session-id" ||
		caller.identity.Source != "mcp-proxy" {
		t.Fatalf("identity = %+v, want the proxy-supplied seat with mcp-proxy source", caller.identity)
	}
}

func TestCallerForRequestRejectsConflictingSingleProxySeat(t *testing.T) {
	row := compose.Row{
		SessionName: "cc-seat", ID: "session-id", CWD: "/work/proxy",
		Kind: compose.LiveClaude, Socket: "cc-seat", PaneID: "%7",
	}
	tests := []struct {
		name       string
		field      string
		value      string
		wantDetail string
	}{
		{name: "id", field: "id", value: "other-id", wantDetail: "id"},
		{name: "pane", field: "pane", value: "%9", wantDetail: "pane"},
		{name: "socket name", field: "socketName", value: "cc-other", wantDetail: "socketName"},
		{
			name: "socket path basename", field: "socketPath", value: "/tmp/tmux-1000/cc-other",
			wantDetail: "socketPath",
		},
		{name: "engine", field: "engine", value: "codex", wantDetail: "engine"},
		{name: "malformed engine", field: "engine", value: "not-an-engine", wantDetail: "engine"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			proxy := map[string]any{
				"v": ProxyWireVersion, "session": "cc-seat", "socketPath": "/tmp/tmux-1000/cc-seat",
				"socketName": "cc-seat", "pane": "%7", "engine": "claude", "id": "session-id",
			}
			proxy[test.field] = test.value
			current := &backend{chat: &fakeChatVerbs{
				listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1},
			}}
			caller, err := current.callerForRequest(context.Background(), mcp.Meta{"pfmProxy": proxy})
			if err != nil {
				t.Fatalf("conflicting proxy returned error: %v", err)
			}
			if !caller.present || caller.valid || caller.row.ID != "" || caller.identity.ID != "" ||
				!strings.Contains(caller.detail, test.wantDetail) {
				t.Fatalf(
					"conflicting proxy caller = %+v, want present invalid refusal naming %q",
					caller,
					test.wantDetail,
				)
			}
		})
	}
}

func TestCallerForRequestRejectsConflictingProxySocketPair(t *testing.T) {
	row := compose.Row{
		SessionName: "cc-seat", ID: "session-id", Kind: compose.LiveClaude,
		Socket: "cc-seat", PaneID: "%7",
	}
	tests := []struct {
		name       string
		socketName string
		socketPath string
		wantDetail string
	}{
		{
			name: "name matches but path differs", socketName: "cc-seat",
			socketPath: "/tmp/tmux-1000/cc-other", wantDetail: "socketPath",
		},
		{
			name: "path matches but name differs", socketName: "cc-other",
			socketPath: "/tmp/tmux-1000/cc-seat", wantDetail: "socketName",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := &backend{chat: &fakeChatVerbs{
				listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1},
			}}
			caller, err := current.callerForRequest(context.Background(), mcp.Meta{
				"pfmProxy": map[string]any{
					"v": ProxyWireVersion, "session": "cc-seat", "socketName": test.socketName,
					"socketPath": test.socketPath, "pane": "%7", "engine": "claude", "id": "session-id",
				},
			})
			if err != nil {
				t.Fatalf("conflicting socket pair returned error: %v", err)
			}
			if !caller.present || caller.valid || !strings.Contains(caller.detail, test.wantDetail) {
				t.Fatalf(
					"conflicting socket caller = %+v, want present invalid refusal naming %q",
					caller,
					test.wantDetail,
				)
			}
		})
	}
}

func TestReviewProxySocketPathRejectsForeignNamespace(t *testing.T) {
	const socket = "ox-shared"
	row := compose.Row{
		SessionName: "shared-session", ID: "opencode-id", Kind: compose.LiveOpenCode,
		Socket: socket, PaneID: "%2",
	}
	current := &backend{
		chat:  &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}},
		paths: paths.Values{TmuxDir: "/jail/tmux-a"},
	}
	caller, err := current.callerForRequest(context.Background(), mcp.Meta{
		"pfmProxy": map[string]any{
			"v": ProxyWireVersion, "session": "shared-session", "socketName": socket,
			"socketPath": "/jail/tmux-b/" + socket, "pane": "%2", "engine": "opencode",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !caller.present || caller.valid || !strings.Contains(caller.detail, "socketPath") {
		t.Fatalf("foreign namespace caller = %+v, want present invalid socketPath refusal", caller)
	}
}

func TestCallerForRequestKeepsSessionFallbackAndEngineAliases(t *testing.T) {
	row := compose.Row{
		SessionName: "oc-seat", ID: "opencode-id", CWD: "/work/opencode",
		Kind: compose.LiveOpenCode, Socket: "ox-seat", PaneID: "%7",
	}
	tests := []struct {
		name  string
		proxy map[string]any
	}{
		{
			name:  "session only",
			proxy: map[string]any{"v": ProxyWireVersion, "session": "oc-seat"},
		},
		{
			name: "long engine alias",
			proxy: map[string]any{
				"v": ProxyWireVersion, "session": "oc-seat", "engine": "OpenCode",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := &backend{chat: &fakeChatVerbs{
				listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1},
			}}
			caller, err := current.callerForRequest(context.Background(), mcp.Meta{"pfmProxy": test.proxy})
			if err != nil || !caller.present || !caller.valid || caller.row.ID != "opencode-id" {
				t.Fatalf("fallback caller = %+v err=%v, want the unique session row", caller, err)
			}
		})
	}
}

func TestCallerForRequestDisambiguatesSharedSessionByPane(t *testing.T) {
	rows := []compose.Row{
		{
			SessionName: "shared-session", Socket: "ox-seat", PaneID: "%1",
			ID: "first", CWD: "/work/first", Kind: compose.LiveOpenCode,
		},
		{
			SessionName: "shared-session", Socket: "ox-seat", PaneID: "%2",
			ID: "second", CWD: "/work/second", Kind: compose.LiveOpenCode,
		},
	}
	current := &backend{chat: &fakeChatVerbs{listed: chat.ListResult{Rows: rows, Matched: len(rows)}}}
	caller, err := current.callerForRequest(context.Background(), mcp.Meta{
		"pfmProxy": map[string]any{
			"v": ProxyWireVersion, "session": "shared-session", "socketName": "ox-seat",
			"socketPath": "/tmp/tmux-1000/ox-seat", "pane": "%2", "engine": "opencode",
		},
	})
	if err != nil || !caller.valid || caller.row.ID != "second" || caller.row.Dir != "/work/second" {
		t.Fatalf("shared-session caller = %+v err=%v, want only the addressed second pane", caller, err)
	}
}

func TestCallerForRequestRejectsSplitProxyConflicts(t *testing.T) {
	row := compose.Row{Kind: compose.LiveSplit, Socket: "cc-split"}
	tests := []struct {
		name       string
		proxy      map[string]any
		wantDetail string
	}{
		{
			name: "socket",
			proxy: map[string]any{
				"v": ProxyWireVersion, "session": "renamed-split", "socketName": "cc-other",
				"socketPath": "/tmp/tmux-1000/cc-other", "engine": "claude", "id": "second",
			},
			wantDetail: "socketName",
		},
		{
			name: "socket pair",
			proxy: map[string]any{
				"v": ProxyWireVersion, "session": "renamed-split", "socketName": "cc-split",
				"socketPath": "/tmp/tmux-1000/cc-other", "engine": "claude", "id": "second",
			},
			wantDetail: "socketPath",
		},
		{
			name: "engine",
			proxy: map[string]any{
				"v": ProxyWireVersion, "session": "renamed-split", "socketName": "cc-split",
				"socketPath": "/tmp/tmux-1000/cc-split", "engine": "codex", "id": "second",
			},
			wantDetail: "engine",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := &backend{chat: &fakeChatVerbs{
				listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1},
			}}
			caller, err := current.callerForRequest(context.Background(), mcp.Meta{"pfmProxy": test.proxy})
			if err != nil || !caller.present || caller.valid || !strings.Contains(caller.detail, test.wantDetail) {
				t.Fatalf(
					"conflicting split caller = %+v err=%v, want refusal naming %q",
					caller,
					err,
					test.wantDetail,
				)
			}
		})
	}
}

func TestCallerForRequestNamesSplitTranscriptFailures(t *testing.T) {
	setupBackendFixture(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertTranscript(context.Background(), store.Transcript{
		UUID: "empty-cwd", Path: "/work/empty.jsonl",
	}); err != nil {
		t.Fatal(err)
	}
	const socket = "cc-123-456-789"
	row := compose.Row{Kind: compose.LiveSplit, Socket: socket}
	current := &backend{
		chat:     &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}},
		database: database,
		paths:    resolved,
	}
	for _, transcriptID := range []string{"missing", "empty-cwd"} {
		t.Run(transcriptID, func(t *testing.T) {
			if err := os.WriteFile(
				filepath.Join(resolved.SIDDir, socket+".%1"),
				[]byte("/work/"+transcriptID+".jsonl\n"),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			caller, err := current.callerForRequest(context.Background(), mcp.Meta{
				"pfmProxy": map[string]any{
					"v": ProxyWireVersion, "session": "renamed-split", "socketName": socket,
					"socketPath": filepath.Join(resolved.TmuxDir, socket), "pane": "%1",
					"engine": "claude", "id": transcriptID,
				},
			})
			if err != nil || !caller.present || caller.valid ||
				!strings.Contains(caller.detail, "has no indexed working directory") {
				t.Fatalf("split transcript caller = %+v err=%v, want named indexed-CWD refusal", caller, err)
			}
		})
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(resolved.SIDDir, socket+".%1"),
		[]byte("/work/closed-store.jsonl\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	_, err = current.callerForRequest(context.Background(), mcp.Meta{
		"pfmProxy": map[string]any{
			"v": ProxyWireVersion, "session": "renamed-split", "socketName": socket,
			"socketPath": filepath.Join(resolved.TmuxDir, socket), "pane": "%1",
			"engine": "claude", "id": "closed-store",
		},
	})
	if err == nil || !strings.Contains(err.Error(), `resolve MCP proxy split transcript "closed-store"`) {
		t.Fatalf("split transcript lookup failure collapsed into absence: %v", err)
	}
}

func TestCallerForRequestDisambiguatesSplitPaneProxyIdentity(t *testing.T) {
	setupBackendFixture(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := database.UpsertTranscript(context.Background(), store.Transcript{
		UUID: "second", Path: "/work/second.jsonl", CWD: "/work/second", CustomTitle: "Second pane",
	}); err != nil {
		t.Fatal(err)
	}
	const socket = "cc-123-456-789"
	if err := os.WriteFile(
		filepath.Join(resolved.SIDDir, socket+".%2"),
		[]byte("/work/second.jsonl\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	composed := compose.Compose(compose.Input{
		Transcripts: []store.Transcript{
			{UUID: "first", Path: "/work/first.jsonl", CWD: "/work/first", Size: 100, PromptCount: 1},
			{UUID: "second", Path: "/work/second.jsonl", CWD: "/work/second", Size: 100, PromptCount: 1},
		},
		Snapshot: gather.Snapshot{
			Panes: []gather.ProbePane{
				{Socket: socket, PaneID: "%1", SessionName: "renamed-split"},
				{Socket: socket, PaneID: "%2", SessionName: "renamed-split"},
			},
			Crumbs: []gather.Crumb{
				{Filename: socket + ".%1", Socket: socket, PaneID: "%1", TranscriptPath: "/work/first.jsonl"},
				{Filename: socket + ".%2", Socket: socket, PaneID: "%2", TranscriptPath: "/work/second.jsonl"},
			},
		},
		Options: compose.Options{View: compose.AllView},
	})
	if len(composed.Rows) != 1 || composed.Rows[0].Kind != compose.LiveSplit ||
		composed.Rows[0].SessionName != "" || composed.Rows[0].PaneID != "" {
		t.Fatalf("split fixture did not produce the aggregate production shape: %+v", composed.Rows)
	}
	composed.Rows = append(composed.Rows, compose.Row{
		SessionName: "renamed-split", Socket: "oc-other", PaneID: "%9",
		ID: "other", CWD: "/work/other", Kind: compose.LiveOpenCode,
	})
	verbs := &fakeChatVerbs{listed: chat.ListResult{Rows: composed.Rows, Matched: len(composed.Rows)}}
	current := &backend{chat: verbs, database: database, paths: resolved}
	caller, err := current.callerForRequest(context.Background(), mcp.Meta{
		"pfmProxy": map[string]any{
			"v": ProxyWireVersion, "session": "renamed-split",
			"socketPath": filepath.Join(resolved.TmuxDir, socket), "socketName": socket, "pane": "%2",
			"engine": "claude", "id": "second",
		},
	})
	if err != nil || !caller.valid || caller.row.Session != "renamed-split" ||
		caller.row.Kind != compose.LiveSplit.String() ||
		caller.row.Dir != "/work/second" || caller.row.Name != "Second pane" ||
		caller.identity.Pane != "%2" || caller.identity.ID != "second" {
		t.Fatalf("split-pane proxy caller = %+v err=%v, want the supplied pane's exact context", caller, err)
	}
	if len(verbs.statuses) != 0 {
		t.Fatalf("split-pane lookup duplicated chat status resolution: %+v", verbs.statuses)
	}
}

func TestReviewSplitCallerRejectsUnrelatedTranscript(t *testing.T) {
	root := setupBackendFixture(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for _, item := range []struct {
		id  string
		cwd string
	}{
		{id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", cwd: "/work/a"},
		{id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", cwd: "/work/b"},
		{id: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", cwd: "/work/c"},
	} {
		if err := database.UpsertTranscript(context.Background(), store.Transcript{
			UUID: item.id, Path: filepath.Join(root, item.id+".jsonl"), CWD: item.cwd,
		}); err != nil {
			t.Fatal(err)
		}
	}
	const socket = "cc-123-456-789"
	if err := os.WriteFile(
		filepath.Join(resolved.SIDDir, socket+".%1"),
		[]byte(filepath.Join(root, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa.jsonl")+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(resolved.SIDDir, socket+".%2"),
		[]byte(filepath.Join(root, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb.jsonl")+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	current := &backend{
		paths: resolved, database: database,
		chat: &fakeChatVerbs{listed: chat.ListResult{
			Rows: []compose.Row{{Kind: compose.LiveSplit, Socket: socket}}, Matched: 1,
		}},
	}
	for _, transcriptID := range []string{
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		"cccccccc-cccc-4ccc-8ccc-cccccccccccc",
	} {
		caller, err := current.callerForRequest(context.Background(), mcp.Meta{
			"pfmProxy": map[string]any{
				"v": ProxyWireVersion, "session": "renamed-split", "socketName": socket,
				"pane": "%2", "engine": "claude", "id": transcriptID,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if caller.valid || !caller.present || !strings.Contains(caller.detail, "pane") {
			t.Fatalf("unrelated split caller = %+v, want present-invalid exact-pane refusal", caller)
		}
	}
}

func TestSplitCallerRequiresReadableExactPaneBinding(t *testing.T) {
	setupBackendFixture(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	const socket = "cc-123-456-789"
	current := &backend{
		paths: resolved,
		chat: &fakeChatVerbs{listed: chat.ListResult{
			Rows: []compose.Row{{Kind: compose.LiveSplit, Socket: socket}}, Matched: 1,
		}},
	}
	request := func(pane string) mcp.Meta {
		return mcp.Meta{"pfmProxy": map[string]any{
			"v": ProxyWireVersion, "session": "split", "socketName": socket,
			"pane": pane, "engine": "claude", "id": "bound-id",
		}}
	}
	if err := os.WriteFile(
		filepath.Join(resolved.SIDDir, socket), []byte("/transcripts/bound-id.jsonl\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	caller, err := current.callerForRequest(context.Background(), request("%3"))
	if err != nil || caller.valid || !strings.Contains(caller.detail, "no exact transcript binding") {
		t.Fatalf("socket-only binding caller=%+v err=%v", caller, err)
	}
	if err := os.WriteFile(filepath.Join(resolved.SIDDir, socket+".%3"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	caller, err = current.callerForRequest(context.Background(), request("%3"))
	if err != nil || caller.valid || !strings.Contains(caller.detail, "no exact transcript binding") {
		t.Fatalf("empty pane binding caller=%+v err=%v", caller, err)
	}
	caller, err = current.callerForRequest(context.Background(), request("bad"))
	if err != nil || caller.valid || !strings.Contains(caller.detail, "invalid exact breadcrumb") {
		t.Fatalf("invalid pane binding caller=%+v err=%v", caller, err)
	}
	if err := os.Mkdir(filepath.Join(resolved.SIDDir, socket+".%4"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := current.callerForRequest(context.Background(), request("%4")); err == nil ||
		!strings.Contains(err.Error(), "split pane \"%4\" breadcrumb") {
		t.Fatalf("pane binding read failure = %v", err)
	}
}

func TestCallerForRequestUsesProxyPaneToDisambiguateSession(t *testing.T) {
	rows := []compose.Row{
		{
			SessionName: "stale-session", Socket: "oc-seat", PaneID: "%1",
			ID: "first", CWD: "/work/first", Kind: compose.LiveOpenCode,
		},
		{
			SessionName: "stale-session", Socket: "oc-seat", PaneID: "%2",
			ID: "second", CWD: "/work/second", Kind: compose.LiveOpenCode,
		},
	}
	current := &backend{chat: &fakeChatVerbs{listed: chat.ListResult{Rows: rows, Matched: len(rows)}}}
	caller, err := current.callerForRequest(context.Background(), mcp.Meta{
		"pfmProxy": map[string]any{
			"v": ProxyWireVersion, "session": "renamed-session", "socketName": "oc-seat",
			"socketPath": "/tmp/tmux-1000/oc-seat", "pane": "%2", "engine": "opencode",
		},
	})
	if err != nil || !caller.valid || caller.row.ID != "second" || caller.row.Dir != "/work/second" ||
		caller.identity.ID != "second" {
		t.Fatalf("disambiguated proxy caller = %+v err=%v, want the uniquely addressed second pane", caller, err)
	}
}

func TestCallerForRequestFindsProxyBeyondPublicListLimit(t *testing.T) {
	rows := make([]compose.Row, defaultChatLSLimit+1)
	for index := range defaultChatLSLimit {
		rows[index] = compose.Row{ID: "historical", Kind: compose.ResumeClaude}
	}
	rows[defaultChatLSLimit] = compose.Row{
		SessionName: "cc-seat", ID: "session-id", Kind: compose.LiveClaude,
	}
	verbs := &cappedProxyChat{fakeChatVerbs: &fakeChatVerbs{
		listed: chat.ListResult{Rows: rows, Matched: len(rows)},
	}}
	caller, err := (&backend{chat: verbs}).callerForRequest(context.Background(), mcp.Meta{
		"pfmProxy": map[string]any{"v": ProxyWireVersion, "session": "cc-seat", "id": "session-id"},
	})
	if err != nil || !caller.valid || caller.row.Session != "cc-seat" {
		t.Fatalf("caller beyond public list limit = %+v err=%v", caller, err)
	}
}

func TestCallerForRequestFallsBackToMatchedIDWhenProxyOmitsIt(t *testing.T) {
	row := compose.Row{SessionName: "oc-seat", ID: "opencode-id", Kind: compose.LiveOpenCode}
	current := &backend{chat: &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}}}
	meta := mcp.Meta{"pfmProxy": map[string]any{
		"v": ProxyWireVersion, "session": "oc-seat", "engine": "opencode",
	}}
	caller, err := current.callerForRequest(context.Background(), meta)
	if err != nil || !caller.valid || caller.identity.ID != "opencode-id" {
		t.Fatalf("caller with omitted proxy id = %+v err=%v", caller, err)
	}
	service := newService("test", current)
	ctx, target, err := service.cliTargetForRequest(
		context.Background(),
		&mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: meta}},
		"self",
	)
	if err != nil || target != "self" {
		t.Fatalf("proxy self target = %q err=%v, want self with scoped identity", target, err)
	}
	resolved, err := chat.Target(ctx, target, nil)
	if err != nil || resolved.ID != "opencode-id" || resolved.Session != "oc-seat" {
		t.Fatalf("proxy self context resolved %+v err=%v, want matched OpenCode row", resolved, err)
	}
}

func TestCallerForRequestNamesProxyNoMatchAndAmbiguity(t *testing.T) {
	tests := []struct {
		name   string
		rows   []compose.Row
		detail string
	}{
		{
			name: "no live match",
			rows: []compose.Row{
				{SessionName: "missing-seat", Kind: compose.ResumeClaude},
				{SessionName: "missing-seat", Kind: compose.LiveClaude, Killed: true},
			},
			detail: `session "missing-seat" matched no live chat`,
		},
		{
			name: "several matches",
			rows: []compose.Row{
				{SessionName: "missing-seat", Kind: compose.LiveClaude},
				{SessionName: "missing-seat", Kind: compose.LiveOpenCode},
			},
			detail: `session "missing-seat" matched 2 live chats`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := &backend{chat: &fakeChatVerbs{listed: chat.ListResult{Rows: test.rows, Matched: len(test.rows)}}}
			caller, err := current.callerForRequest(context.Background(), mcp.Meta{
				"pfmProxy": map[string]any{"v": ProxyWireVersion, "session": "missing-seat"},
			})
			if err != nil || !caller.present || caller.valid || !strings.Contains(caller.detail, test.detail) {
				t.Fatalf("caller = %+v err=%v, want present invalid detail containing %q", caller, err, test.detail)
			}
		})
	}
}

func TestCallerForRequestReturnsProxyVersionAndListingErrors(t *testing.T) {
	current := &backend{chat: &fakeChatVerbs{err: errors.New("fleet database busy")}}
	_, err := current.callerForRequest(context.Background(), mcp.Meta{
		"pfmProxy": map[string]any{"v": ProxyWireVersion, "session": "cc-seat"},
	})
	if err == nil ||
		!strings.Contains(err.Error(), `resolve MCP proxy session "cc-seat": list live chats: fleet database busy`) {
		t.Fatalf("listing failure collapsed or lost its session: %v", err)
	}

	caller, err := current.callerForRequest(context.Background(), mcp.Meta{
		"pfmProxy": map[string]any{"v": ProxyWireVersion + 1, "session": "cc-seat"},
	})
	if err == nil || !strings.Contains(err.Error(), "version 2") ||
		!strings.Contains(err.Error(), "requires version 1") || !strings.Contains(err.Error(), "restart the chat") {
		t.Fatalf("unsupported version error = %v", err)
	}
	if !reflect.DeepEqual(caller, callerIdentity{}) {
		t.Fatalf("unsupported version returned an invalid caller instead of only an error: %+v", caller)
	}
}

func TestCallerForRequestMarksMalformedProxyPresent(t *testing.T) {
	tests := []struct {
		name string
		raw  any
	}{
		{name: "not object", raw: "cc-seat"},
		{name: "empty session", raw: map[string]any{"v": ProxyWireVersion, "session": ""}},
		{name: "whitespace session", raw: map[string]any{"v": ProxyWireVersion, "session": " cc-seat "}},
		{name: "control session", raw: map[string]any{"v": ProxyWireVersion, "session": "cc-seat\n"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caller, err := (&backend{}).callerForRequest(context.Background(), mcp.Meta{"pfmProxy": test.raw})
			if err != nil || !caller.present || caller.valid || caller.detail == "" {
				t.Fatalf("malformed proxy caller = %+v err=%v, want present invalid detail", caller, err)
			}
		})
	}
}

func TestCallerForRequestKeepsThreadIDPrecedenceAndEmptyMetadata(t *testing.T) {
	setupBackendFixture(t)
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

func TestProxyIdentityReachesCallerBoundTools(t *testing.T) {
	row := compose.Row{
		SessionName: "cc-seat", ID: "session-id", CWD: "/work/proxy", Project: "proxy",
		Name: "Proxy Claude", Kind: compose.LiveClaude, Socket: "cc-seat", PaneID: "%7",
	}
	verbs := &fakeChatVerbs{
		listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1},
		last:   chat.LastResult{Text: "proxy self answer\n"}, resolveScopedSelf: true,
	}
	var calls [][]string
	current := &backend{
		chat: verbs,
		dispatch: func(_ context.Context, args []string, stdout, _ io.Writer) int {
			calls = append(calls, append([]string(nil), args...))
			_, _ = io.WriteString(stdout, "launched\n")
			return 0
		},
		allowAmbientIdentity: false,
	}
	service := newService("test", current)
	protocol := connectInMemory(t, service.Server())
	meta := mcp.Meta{"pfmProxy": map[string]any{
		"v": ProxyWireVersion, "session": "cc-seat", "socketPath": "/tmp/tmux-1000/cc-seat",
		"socketName": "cc-seat", "pane": "%7", "engine": "claude", "id": "session-id", "source": "tmux",
	}}

	whoami := callToolWithMeta[WhoamiOutput](t, protocol.clientSession, "chat_whoami", meta, WhoamiInput{})
	if whoami.Status != "ok" || whoami.Session != "cc-seat" || whoami.ID != "session-id" ||
		whoami.Source != "mcp-proxy" {
		t.Fatalf("proxied whoami = %+v", whoami)
	}
	last := callToolWithMeta[LastOutput](t, protocol.clientSession, "chat_last", meta, LastInput{Target: "self"})
	if last.Text != "proxy self answer" || !reflect.DeepEqual(verbs.lasts, []chat.LastRequest{{Target: "self"}}) ||
		!reflect.DeepEqual(verbs.resolvedSelfIDs, []string{"session-id"}) {
		t.Fatalf("proxied chat_last = %+v, verb calls = %+v", last, verbs.lasts)
	}
	created := callToolWithMeta[ActionOutput](t, protocol.clientSession, "chat_new", meta, NewInput{Name: "child"})
	wantCalls := [][]string{{"chat", "new", "--name", "child", "--engine", "cc", "--cwd", "/work/proxy"}}
	if created.Status != "ok" || !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("proxied chat_new = %+v, dispatch calls = %q", created, calls)
	}

	request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: meta}}
	reporter := service.issueReporter(context.Background(), request)
	if reporter.Session != "cc-seat" || reporter.Label != "Proxy Claude" || reporter.UUID != "session-id" ||
		reporter.CWD != "/work/proxy" || reporter.Engine != "claude" {
		t.Fatalf("proxied issue reporter = %+v", reporter)
	}
}

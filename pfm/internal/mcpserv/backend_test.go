package mcpserv

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
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

func TestListProjectedKeepsDistinctPrivateTranscriptPaths(t *testing.T) {
	t.Parallel()
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

func TestCallEngineForRequestResolvesProxyIdentity(t *testing.T) {
	t.Parallel()
	row := compose.Row{
		SessionName: "cc-seat", ID: "session-id", CWD: testjail.Place("/work/proxy"), Project: "proxy",
		Name: "Proxy Claude", Kind: compose.LiveClaude, Socket: "cc-seat", PaneID: "%7",
	}
	current := &backend{
		paths: paths.Values{TmuxDir: "/tmp/tmux-1000"},
		chat:  &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}},
	}
	meta := mcp.Meta{"pfmProxy": map[string]any{
		"v": ProxyWireVersion, "session": "cc-seat", "socketPath": "/tmp/tmux-1000/cc-seat",
		"socketName": "cc-seat", "pane": "%7", "engine": "claude", "id": "session-id",
	}}
	caller, err := current.callerForRequest(context.Background(), meta)
	if err != nil {
		t.Fatal(err)
	}
	if !caller.present || !caller.valid || caller.row.Session != "cc-seat" ||
		caller.row.Dir != testjail.Place("/work/proxy") {
		t.Fatalf("caller = %+v, want the unique live row", caller)
	}
	if caller.identity.Session != "cc-seat" || caller.identity.SocketPath != "/tmp/tmux-1000/cc-seat" ||
		caller.identity.SocketName != "cc-seat" || caller.identity.Pane != "%7" ||
		caller.identity.Engine != string(pfmengine.Claude) || caller.identity.ID != "session-id" ||
		caller.identity.Source != "mcp-proxy" {
		t.Fatalf("identity = %+v, want the proxy-supplied seat with mcp-proxy source", caller.identity)
	}
}

// TestCallerForRequestRefusesConflictingProxies: a proxy payload whose seat,
// socket, namespace or engine disagrees with the live row, or that names no or
// several live chats, is a present but invalid caller naming the conflict.
func TestCallerForRequestRefusesConflictingProxies(t *testing.T) {
	t.Parallel()
	seat := []compose.Row{{
		SessionName: "cc-seat", ID: "session-id", CWD: testjail.Place("/work/proxy"),
		Kind: compose.LiveClaude, Socket: "cc-seat", PaneID: "%7",
	}}
	split := []compose.Row{{Kind: compose.LiveSplit, Socket: "cc-split"}}
	shared := []compose.Row{{
		SessionName: "shared-session", ID: "opencode-id", Kind: compose.LiveOpenCode, Socket: "ox-shared", PaneID: "%2",
	}}
	// proxy is base with the pairs' fields replaced
	proxy := func(base map[string]any, pairs ...string) map[string]any {
		out := map[string]any{"v": ProxyWireVersion}
		for key, value := range base {
			out[key] = value
		}
		for i := 0; i < len(pairs); i += 2 {
			out[pairs[i]] = pairs[i+1]
		}
		return out
	}
	seatProxy := map[string]any{
		"session": "cc-seat", "socketPath": "/tmp/tmux-1000/cc-seat", "socketName": "cc-seat",
		"pane": "%7", "engine": "claude", "id": "session-id",
	}
	splitProxy := map[string]any{
		"session": "renamed-split", "socketName": "cc-split", "socketPath": "/tmp/tmux-1000/cc-split",
		"engine": "claude", "id": "second",
	}
	missing := map[string]any{"session": "missing-seat"}
	for _, test := range []struct {
		name, tmux string
		rows       []compose.Row
		proxy      map[string]any
		want       string
	}{
		{"seat pane", "/tmp/tmux-1000", seat, proxy(seatProxy, "pane", "%9"), "<nil> present=true valid=false row=\"\" identity=\"\" MCP caller location disagrees with its live chat: payload  /tmp/tmux-1000/cc-seat %9, row tmux /tmp/tmux-1000/cc-seat %7"},
		{"seat socket name", "/tmp/tmux-1000", seat, proxy(seatProxy, "socketName", "cc-other"), "<nil> present=true valid=false row=\"\" identity=\"\" MCP _meta.pfmProxy conflicts with live chat: socketName \"cc-other\" does not match live socket \"cc-seat\""},
		{"seat socket path", "/tmp/tmux-1000", seat, proxy(seatProxy, "socketPath", "/tmp/tmux-1000/cc-other"), "<nil> present=true valid=false row=\"\" identity=\"\" MCP caller location disagrees with its live chat: payload  /tmp/tmux-1000/cc-other %7, row tmux /tmp/tmux-1000/cc-seat %7"},
		{"seat engine", "/tmp/tmux-1000", seat, proxy(seatProxy, "engine", "codex"), "<nil> present=true valid=false row=\"\" identity=\"\" MCP _meta.pfmProxy conflicts with live chat: engine \"codex\" does not match live engine \"cc\""},
		{"foreign namespace", "/jail/tmux-a", shared, proxy(map[string]any{
			"session": "shared-session", "socketName": "ox-shared", "socketPath": "/jail/tmux-b/ox-shared",
			"pane": "%2", "engine": "opencode",
		}), "<nil> present=true valid=false row=\"\" identity=\"\" MCP caller location disagrees with its live chat: payload  /jail/tmux-b/ox-shared %2, row tmux /jail/tmux-a/ox-shared %2"},
		{"split socket", "/tmp/tmux-1000", split, proxy(
			splitProxy, "socketName", "cc-other", "socketPath", "/tmp/tmux-1000/cc-other",
		), "<nil> present=true valid=false row=\"\" identity=\"\" MCP _meta.pfmProxy conflicts with live chat: socketName \"cc-other\" does not match live socket \"cc-split\"; socketPath \"/tmp/tmux-1000/cc-other\" does not match live socket path \"/tmp/tmux-1000/cc-split\""},
		{"split socket pair", "/tmp/tmux-1000", split, proxy(splitProxy, "socketPath", "/tmp/tmux-1000/cc-other"), "<nil> present=true valid=false row=\"\" identity=\"\" MCP _meta.pfmProxy conflicts with live chat: socketPath \"/tmp/tmux-1000/cc-other\" does not match live socket path \"/tmp/tmux-1000/cc-split\""},
		{"split engine", "/tmp/tmux-1000", split, proxy(splitProxy, "engine", "codex"), "<nil> present=true valid=false row=\"\" identity=\"\" MCP _meta.pfmProxy conflicts with live chat: engine \"codex\" does not match live engine \"cc\""},
		{"no live match", "/tmp/tmux-1000", []compose.Row{
			{SessionName: "missing-seat", Kind: compose.ResumeClaude},
			{SessionName: "missing-seat", Kind: compose.LiveClaude, Killed: true},
		}, proxy(missing), "<nil> present=true valid=false row=\"\" identity=\"\" MCP _meta.pfmProxy session \"missing-seat\" matched no live chat"},
		{"several matches", "/tmp/tmux-1000", []compose.Row{
			{SessionName: "missing-seat", Kind: compose.LiveClaude},
			{SessionName: "missing-seat", Kind: compose.LiveOpenCode},
		}, proxy(missing), "<nil> present=true valid=false row=\"\" identity=\"\" MCP _meta.pfmProxy session \"missing-seat\" matched 2 live chats"},
	} {
		current := &backend{paths: paths.Values{TmuxDir: test.tmux}, chat: &fakeChatVerbs{
			listed: chat.ListResult{Rows: test.rows, Matched: len(test.rows)},
		}}
		caller, err := current.callerForRequest(context.Background(), mcp.Meta{"pfmProxy": test.proxy})
		got := fmt.Sprintf("%v present=%t valid=%t row=%q identity=%q %s",
			err, caller.present, caller.valid, caller.row.ID, caller.identity.ID, caller.detail)
		if got != test.want {
			t.Errorf("%s: caller = %q, want %q", test.name, got, test.want)
		}
	}
}

func TestCallEngineForRequestKeepsSessionFallbackAndEngineAliases(t *testing.T) {
	setupBackendPaths(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	row := compose.Row{
		SessionName: "oc-seat", ID: "opencode-id", CWD: testjail.Place("/work/opencode"),
		Kind: compose.LiveOpenCode, Socket: "ox-seat", PaneID: "%7",
	}
	tests := []struct {
		name              string
		proxy             map[string]any
		wantSocketName    string
		wantSocketPath    string
		wantPane          string
		wantEngineWarning []string
	}{
		{
			name: "session only", proxy: map[string]any{"v": ProxyWireVersion, "session": "oc-seat"},
			wantSocketName: "ox-seat", wantSocketPath: filepath.Join(resolved.TmuxDir, "ox-seat"), wantPane: "%7",
		},
		{
			name: "long engine alias",
			proxy: map[string]any{
				"v": ProxyWireVersion, "session": "oc-seat", "engine": "OpenCode",
			},
			wantSocketName: "ox-seat", wantSocketPath: filepath.Join(resolved.TmuxDir, "ox-seat"), wantPane: "%7",
		},
		{
			// The engine only feeds the conflict check: unreadable, it is skipped and named.
			name: "unreadable engine",
			proxy: map[string]any{
				"v": ProxyWireVersion, "session": "oc-seat", "engine": "gpt-x",
			},
			wantSocketName: "ox-seat", wantSocketPath: filepath.Join(resolved.TmuxDir, "ox-seat"), wantPane: "%7",
			wantEngineWarning: []string{`gpt-x|unknown engine "gpt-x" (want cc/claude, cx/codex, ox/opencode)`},
		},
		{
			name: "keeps supplied socket name",
			proxy: map[string]any{
				"v": ProxyWireVersion, "session": "oc-seat", "socketName": "ox-seat",
			},
			wantSocketName: "ox-seat", wantSocketPath: filepath.Join(resolved.TmuxDir, "ox-seat"), wantPane: "%7",
		},
		{
			name: "keeps supplied socket path and pane",
			proxy: map[string]any{
				"v": ProxyWireVersion, "session": "oc-seat",
				"socketPath": filepath.Join(resolved.TmuxDir, "ox-seat"), "pane": "%7",
			},
			wantSocketName: "ox-seat", wantSocketPath: filepath.Join(resolved.TmuxDir, "ox-seat"), wantPane: "%7",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := &backend{paths: resolved, chat: &fakeChatVerbs{
				listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1},
			}}
			ctx, recorder := obs.Test(t)
			caller, err := current.callerForRequest(ctx, mcp.Meta{"pfmProxy": test.proxy})
			if err != nil || !caller.present || !caller.valid || caller.row.ID != "opencode-id" {
				t.Fatalf("fallback caller = %+v err=%v, want the unique session row", caller, err)
			}
			var engineWarnings []string
			for _, record := range recorder.Records() {
				if record.Message == "mcp.caller_engine_invalid" && record.Level == "WARN" {
					engine, _ := record.Field(obs.FieldEngine)
					text, _ := record.Field(obs.FieldErr)
					engineWarnings = append(engineWarnings, engine.(string)+"|"+text.(string))
				}
			}
			if !slices.Equal(engineWarnings, test.wantEngineWarning) {
				t.Fatalf("engine warnings = %q, want %q", engineWarnings, test.wantEngineWarning)
			}
			if caller.identity.SocketName != test.wantSocketName ||
				caller.identity.SocketPath != test.wantSocketPath || caller.identity.Pane != test.wantPane {
				t.Fatalf("fallback identity = %+v, want socket %q path %q pane %q",
					caller.identity, test.wantSocketName, test.wantSocketPath, test.wantPane)
			}
		})
	}
}

func TestCallEngineForRequestSessionFallbackReachesResolvedSelf(t *testing.T) {
	setupBackendPaths(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	row := compose.Row{
		SessionName: "oc-seat", ID: "opencode-id", CWD: testjail.Place("/work/opencode"),
		Kind: compose.LiveOpenCode, Socket: "ox-seat", PaneID: "%7",
	}
	current := &backend{
		paths: resolved,
		chat:  &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}},
	}
	meta := mcp.Meta{"pfmProxy": map[string]any{"v": ProxyWireVersion, "session": "oc-seat"}}
	caller, err := current.callerForRequest(context.Background(), meta)
	if err != nil || !caller.valid {
		t.Fatalf("session-only caller = %+v err=%v, want valid caller", caller, err)
	}
	self, err := newService("test", current).resolvedSelf(context.Background(), caller)
	if err != nil || self.Socket != "ox-seat" || self.Pane != "%7" {
		t.Fatalf("resolved self = %+v err=%v, want row socket and pane", self, err)
	}
	ctx, target, err := newService("test", current).cliTargetForRequest(
		context.Background(), &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: meta}}, "self",
	)
	requestSelf, resolveErr := chat.Target(ctx, target, nil)
	if err != nil || resolveErr != nil || requestSelf.Socket != "ox-seat" || requestSelf.Pane != "%7" {
		t.Fatalf("request self = %+v target=%q err=%v resolveErr=%v, want row socket and pane",
			requestSelf, target, err, resolveErr)
	}
}

func TestCallEngineForRequestRejectsInvalidFallbackSocket(t *testing.T) {
	t.Parallel()
	row := compose.Row{
		SessionName: "oc-seat", ID: "opencode-id", Kind: compose.LiveOpenCode,
		Socket: "../outside", PaneID: "%7",
	}
	current := &backend{
		paths: paths.Values{TmuxDir: "/jail/tmux"},
		chat:  &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}},
	}
	caller, err := current.callerForRequest(context.Background(), mcp.Meta{
		"pfmProxy": map[string]any{"v": ProxyWireVersion, "session": "oc-seat"},
	})
	want := `resolve MCP proxy session "oc-seat": live chat "oc-seat" socket: ` +
		`socket "../outside" must be one relative tmux socket name`
	if err != nil || !caller.present || caller.valid || caller.detail != want || caller.identity.SocketName != "" {
		t.Fatalf("invalid row socket caller = %+v err=%v, want present invalid caller naming %q", caller, err, want)
	}
}

func TestCallEngineForRequestDisambiguatesSharedSessionByPane(t *testing.T) {
	t.Parallel()
	rows := []compose.Row{
		{
			SessionName: "shared-session", Socket: "ox-seat", PaneID: "%1",
			ID: "first", CWD: testjail.Place("/work/first"), Kind: compose.LiveOpenCode,
		},
		{
			SessionName: "shared-session", Socket: "ox-seat", PaneID: "%2",
			ID: "second", CWD: testjail.Place("/work/second"), Kind: compose.LiveOpenCode,
		},
	}
	current := &backend{
		paths: paths.Values{TmuxDir: "/tmp/tmux-1000"},
		chat:  &fakeChatVerbs{listed: chat.ListResult{Rows: rows, Matched: len(rows)}},
	}
	caller, err := current.callerForRequest(context.Background(), mcp.Meta{
		"pfmProxy": map[string]any{
			"v": ProxyWireVersion, "session": "shared-session", "socketName": "ox-seat",
			"socketPath": "/tmp/tmux-1000/ox-seat", "pane": "%2", "engine": "opencode",
		},
	})
	if err != nil || !caller.valid || caller.row.ID != "second" || caller.row.Dir != testjail.Place("/work/second") {
		t.Fatalf("shared-session caller = %+v err=%v, want only the addressed second pane", caller, err)
	}
}

// A split pane the breadcrumb bound exactly keeps its identity while its
// transcript is unindexed, lacks a directory or cannot be read: directory and
// transcript path stay unknown (never the split row's, another pane's), and
// an unreadable store is a warning naming the transcript.
func TestCallEngineForRequestNamesSplitTranscriptFailures(t *testing.T) {
	setupBackendPaths(t)
	ctx, recorder := obs.Test(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	database, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertTranscript(context.Background(), store.Transcript{
		UUID: "empty-cwd", Path: testjail.Place("/work/empty.jsonl"),
	}); err != nil {
		t.Fatal(err)
	}
	const socket = "cc-123-456-789"
	row := compose.Row{
		Kind:   compose.LiveSplit,
		Socket: socket,
		CWD:    testjail.Place("/work/other-pane"),
		Path:   testjail.Place("/work/other-pane.jsonl"),
	}
	current := &backend{
		chat:     &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}},
		database: database,
		paths:    resolved,
	}
	bind := func(t *testing.T, transcriptID string) callerIdentity {
		t.Helper()
		if err := os.WriteFile(
			filepath.Join(resolved.SIDDir, socket+".%1"),
			[]byte(testjail.Place("/work/")+transcriptID+".jsonl\n"),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
		caller, err := current.callerForRequest(ctx, mcp.Meta{
			"pfmProxy": map[string]any{
				"v": ProxyWireVersion, "session": "renamed-split", "socketName": socket,
				"socketPath": filepath.Join(resolved.TmuxDir, socket), "pane": "%1",
				"engine": "claude", "id": transcriptID,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return caller
	}
	transcriptWarnings := func(id string) []string {
		var found []string
		for _, record := range recorder.Records() {
			if named, _ := record.Field(
				obs.FieldChat,
			); record.Message == "mcp.caller_transcript_unread" &&
				named == id {
				text, _ := record.Field(obs.FieldErr)
				found = append(found, record.Level+"|"+text.(string))
			}
		}
		return found
	}
	for _, test := range []struct {
		id, wantPath string
	}{
		{id: "missing"},
		{id: "empty-cwd", wantPath: testjail.Place("/work/empty.jsonl")},
	} {
		t.Run(test.id, func(t *testing.T) {
			caller := bind(t, test.id)
			if !caller.valid || caller.row.ID != test.id || caller.identity.ID != test.id ||
				caller.row.Dir != "" || caller.row.transcriptPath != test.wantPath || caller.row.Pane != "%1" {
				t.Fatalf("split transcript caller = %+v, want valid %q with no directory and path %q",
					caller, test.id, test.wantPath)
			}
		})
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	caller := bind(t, "closed-store")
	if !caller.valid || caller.identity.ID != "closed-store" || caller.row.Dir != "" ||
		caller.row.transcriptPath != "" {
		t.Fatalf("closed-store split caller = %+v, want the bound identity with no directory or path", caller)
	}
	if got, want := transcriptWarnings(
		"closed-store",
	), []string{
		`WARN|query transcript "closed-store": sql: database is closed`,
	}; !slices.Equal(
		got,
		want,
	) {
		t.Fatalf("closed-store warnings = %q, want %q", got, want)
	}
	if got := transcriptWarnings("missing"); len(got) != 0 {
		t.Fatalf("an unindexed transcript warned %q, want no warning", got)
	}
}

func TestCallEngineForRequestDisambiguatesSplitPaneProxyIdentity(t *testing.T) {
	setupBackendPaths(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	database, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := database.UpsertTranscript(context.Background(), store.Transcript{
		UUID:        "second",
		Path:        testjail.Place("/work/second.jsonl"),
		CWD:         testjail.Place("/work/second"),
		CustomTitle: "Second pane",
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
			{
				UUID:        "first",
				Path:        testjail.Place("/work/first.jsonl"),
				CWD:         testjail.Place("/work/first"),
				Size:        100,
				PromptCount: 1,
			},
			{
				UUID:        "second",
				Path:        testjail.Place("/work/second.jsonl"),
				CWD:         testjail.Place("/work/second"),
				Size:        100,
				PromptCount: 1,
			},
		},
		Snapshot: gather.Snapshot{
			Panes: []gather.ProbePane{
				{Socket: socket, PaneID: "%1", SessionName: "renamed-split"},
				{Socket: socket, PaneID: "%2", SessionName: "renamed-split"},
			},
			Crumbs: []gather.Crumb{
				{
					Filename:       socket + ".%1",
					Socket:         socket,
					PaneID:         "%1",
					TranscriptPath: testjail.Place("/work/first.jsonl"),
				},
				{
					Filename:       socket + ".%2",
					Socket:         socket,
					PaneID:         "%2",
					TranscriptPath: testjail.Place("/work/second.jsonl"),
				},
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
		ID: "other", CWD: testjail.Place("/work/other"), Kind: compose.LiveOpenCode,
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
		caller.row.Dir != testjail.Place("/work/second") || caller.row.Name != "Second pane" ||
		caller.identity.Pane != "%2" || caller.identity.ID != "second" {
		t.Fatalf("split-pane proxy caller = %+v err=%v, want the supplied pane's exact context", caller, err)
	}
	if len(verbs.statuses) != 0 {
		t.Fatalf("split-pane lookup duplicated chat status resolution: %+v", verbs.statuses)
	}
}

func TestReviewSplitCallerRejectsUnrelatedTranscript(t *testing.T) {
	root := setupBackendPaths(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	database, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for _, item := range []struct {
		id  string
		cwd string
	}{
		{id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", cwd: testjail.Place("/work/a")},
		{id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", cwd: testjail.Place("/work/b")},
		{id: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", cwd: testjail.Place("/work/c")},
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
	got := ""
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
		got += fmt.Sprintf("present=%t valid=%t %s\n", caller.present, caller.valid, caller.detail)
	}
	if got, want := strings.ReplaceAll(
		got,
		root,
		"{root}",
	), "present=true valid=false MCP _meta.pfmProxy split pane \"%2\" is bound to transcript \"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb\", not \"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa\"\npresent=true valid=false MCP _meta.pfmProxy split pane \"%2\" is bound to transcript \"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb\", not \"cccccccc-cccc-4ccc-8ccc-cccccccccccc\"\n"; got != want {
		t.Fatalf("unrelated split callers = %q, want %q", got, want)
	}
}

func TestSplitCallerRequiresReadableExactPaneBinding(t *testing.T) {
	setupBackendPaths(t)
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
	caller, err = current.callerForRequest(context.Background(), request("%4"))
	crumb := filepath.Join(resolved.SIDDir, socket+".%4")
	wantDetail := `resolve MCP proxy split pane "%4" breadcrumb: read reload breadcrumb "` + crumb +
		`": read ` + crumb + ": is a directory"
	if err != nil || !caller.present || caller.valid || caller.detail != wantDetail {
		t.Fatalf("pane binding read failure caller=%+v err=%v, want present-invalid %q", caller, err, wantDetail)
	}
}

func TestCallEngineForRequestRefusesStaleProxySession(t *testing.T) {
	t.Parallel()
	rows := []compose.Row{
		{
			SessionName: "stale-session", Socket: "oc-seat", PaneID: "%1",
			ID: "first", CWD: testjail.Place("/work/first"), Kind: compose.LiveOpenCode,
		},
		{
			SessionName: "stale-session", Socket: "oc-seat", PaneID: "%2",
			ID: "second", CWD: testjail.Place("/work/second"), Kind: compose.LiveOpenCode,
		},
	}
	current := &backend{
		paths: paths.Values{TmuxDir: "/tmp/tmux-1000"},
		chat:  &fakeChatVerbs{listed: chat.ListResult{Rows: rows, Matched: len(rows)}},
	}
	caller, err := current.callerForRequest(context.Background(), mcp.Meta{
		"pfmProxy": map[string]any{
			"v": ProxyWireVersion, "session": "renamed-session", "socketName": "oc-seat",
			"socketPath": "/tmp/tmux-1000/oc-seat", "pane": "%2", "engine": "opencode",
		},
	})
	if err != nil || caller.valid || !errors.Is(caller.err, resolve.ErrNotInChat) {
		t.Fatalf("stale proxy caller=%+v err=%v, want unmatched live session", caller, err)
	}
}

func TestCallEngineForRequestFindsProxyBeyondPublicListLimit(t *testing.T) {
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
		SessionName: "cc-seat", ID: "session-id", Kind: compose.LiveClaude, Socket: "cc-seat", PaneID: "%7",
	}
	verbs := &cappedProxyChat{fakeChatVerbs: &fakeChatVerbs{
		listed: chat.ListResult{Rows: rows, Matched: len(rows)},
	}}
	caller, err := (&backend{paths: resolved, chat: verbs}).callerForRequest(context.Background(), mcp.Meta{
		"pfmProxy": map[string]any{"v": ProxyWireVersion, "session": "cc-seat", "id": "session-id"},
	})
	if err != nil || !caller.valid || caller.row.Session != "cc-seat" {
		t.Fatalf("caller beyond public list limit = %+v err=%v", caller, err)
	}
}

func TestCallEngineForRequestFallsBackToMatchedIDWhenProxyOmitsIt(t *testing.T) {
	setupBackendPaths(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	row := compose.Row{
		SessionName: "oc-seat", ID: "opencode-id", Kind: compose.LiveOpenCode, Socket: "ox-seat", PaneID: "%7",
	}
	current := &backend{
		paths: resolved,
		chat:  &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}},
	}
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
	resolvedSelf, err := chat.Target(ctx, target, nil)
	if err != nil || resolvedSelf.ID != "opencode-id" || resolvedSelf.Session != "oc-seat" {
		t.Fatalf("proxy self context resolved %+v err=%v, want matched OpenCode row", resolvedSelf, err)
	}
}

// A listing failure is a present caller that did not bind, named and never
// absent: self targets refuse with it and explicit targets go on. Only an
// unsupported wire version fails the request.
func TestCallEngineForRequestReturnsProxyVersionAndListingErrors(t *testing.T) {
	t.Parallel()
	current := &backend{chat: &fakeChatVerbs{err: errors.New("fleet database busy")}}
	caller, err := current.callerForRequest(context.Background(), mcp.Meta{
		"pfmProxy": map[string]any{"v": ProxyWireVersion, "session": "cc-seat"},
	})
	const wantDetail = `resolve MCP proxy session "cc-seat": list live chats: fleet database busy`
	if err != nil || !caller.present || caller.valid || caller.detail != wantDetail ||
		identityFailureStatus(caller.err) != statusError {
		t.Fatalf("listing failure caller = %+v err=%v, want present-invalid %q with status %q",
			caller, err, wantDetail, statusError)
	}

	caller, err = current.callerForRequest(context.Background(), mcp.Meta{
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

func TestCallEngineForRequestMarksMalformedProxyPresent(t *testing.T) {
	t.Parallel()
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

func TestProxyIdentityReachesCallerBoundTools(t *testing.T) {
	t.Parallel()
	row := compose.Row{
		SessionName: "cc-seat", ID: "session-id", CWD: testjail.Place("/work/proxy"), Project: "proxy",
		Name: "Proxy Claude", Kind: compose.LiveClaude, Socket: "cc-seat", PaneID: "%7",
	}
	verbs := &fakeChatVerbs{
		listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1},
		last:   chat.LastResult{Text: "proxy self answer\n"}, resolveScopedSelf: true,
	}
	var calls [][]string
	current := &backend{
		paths: paths.Values{TmuxDir: "/tmp/tmux-1000"},
		chat:  verbs,
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
		"socketName": "cc-seat", "pane": "%7", "engine": "claude", "id": "session-id",
	}}

	whoami := callToolWithMeta[WhoamiOutput](t, protocol.clientSession, "whoami", meta, WhoamiInput{})
	if whoami.Status != "ok" || whoami.Session != "cc-seat" || whoami.ID != "session-id" ||
		whoami.Source != "mcp-proxy" {
		t.Fatalf("proxied whoami = %+v", whoami)
	}
	last := callToolWithMeta[LastOutput](t, protocol.clientSession, "last", meta, LastInput{Target: "self"})
	if last.Text != "proxy self answer" || !reflect.DeepEqual(verbs.lasts, []chat.LastRequest{{Target: "self"}}) ||
		!reflect.DeepEqual(verbs.resolvedSelfIDs, []string{"session-id"}) {
		t.Fatalf("proxied last = %+v, verb calls = %+v", last, verbs.lasts)
	}
	created := callToolWithMeta[ActionOutput](t, protocol.clientSession, "new", meta, NewInput{Name: "child"})
	wantCalls := [][]string{
		{"chat", "new", "--name", "child", "--engine", "cc", "--cwd", testjail.Place("/work/proxy")},
	}
	if created.Status != "ok" || !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("proxied new = %+v, dispatch calls = %q", created, calls)
	}
}

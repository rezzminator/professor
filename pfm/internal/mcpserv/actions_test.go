package mcpserv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// fakeChatVerbs is the MCP tests' stand-in for chat.Verbs: it records every
// typed call and answers from its fields.
type fakeChatVerbs struct {
	lasts             []chat.LastRequest
	statuses          []chat.StatusRequest
	lists             []chat.ListRequest
	finds             []chat.FindRequest
	reads             []string
	last              chat.LastResult
	status            headless.Status
	listed            chat.ListResult
	found             []chat.TranscriptMatch
	read              []transcript.Entry
	err               error
	resolveScopedSelf bool
	resolvedSelfIDs   []string
	resolvedSelfPaths []string
	resolvedStatusIDs []string
}

func (fake *fakeChatVerbs) Last(ctx context.Context, request chat.LastRequest) (chat.LastResult, error) {
	fake.lasts = append(fake.lasts, request)
	if fake.resolveScopedSelf && (request.Target == "self" || request.Target == "me") {
		resolved, err := chat.Target(ctx, request.Target, nil)
		if err != nil {
			return chat.LastResult{}, err
		}
		fake.resolvedSelfIDs = append(fake.resolvedSelfIDs, resolved.ID)
		fake.resolvedSelfPaths = append(fake.resolvedSelfPaths, resolved.Path)
	}
	return fake.last, fake.err
}

func (fake *fakeChatVerbs) Status(ctx context.Context, request chat.StatusRequest) (headless.Status, error) {
	fake.statuses = append(fake.statuses, request)
	if fake.resolveScopedSelf && (request.Target == "self" || request.Target == "me") {
		resolved, err := chat.Target(ctx, request.Target, nil)
		if err != nil {
			return headless.Status{}, err
		}
		fake.resolvedStatusIDs = append(fake.resolvedStatusIDs, resolved.ID)
	}
	return fake.status, fake.err
}

func (fake *fakeChatVerbs) List(_ context.Context, request chat.ListRequest) (chat.ListResult, error) {
	fake.lists = append(fake.lists, request)
	return fake.listed, fake.err
}

func (fake *fakeChatVerbs) Find(_ context.Context, request chat.FindRequest) ([]chat.TranscriptMatch, error) {
	fake.finds = append(fake.finds, request)
	return fake.found, fake.err
}

func (fake *fakeChatVerbs) Read(
	_ context.Context,
	target string,
	tail int,
) (headless.Chat, []transcript.Entry, bool, error) {
	fake.reads = append(fake.reads, fmt.Sprintf("%s/%d", target, tail))
	return headless.Chat{
		ID:     target,
		Engine: pfmengine.Claude,
		Path:   "/transcripts/" + target + ".jsonl",
	}, fake.read, false, fake.err
}

// TestChatLastAndStatusReachTheTypedVerbs pins the seam: chat_last and
// chat_status call the chat verb layer with the target as given — no argv, no
// JSON round trip — and a dead chat comes back as a status, not an error.
func TestChatLastAndStatusReachTheTypedVerbs(t *testing.T) {
	setupBackendFixture(t)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	verbs := &fakeChatVerbs{
		last: chat.LastResult{Text: "live answer\n"},
		status: headless.Status{
			Name: "MCP_HAMMER_A", State: headless.StateDead,
			Engine: pfmengine.Codex, SessionID: "thread-a",
		},
	}
	service, err := NewConfigured("test", nil, Runtime{Paths: resolved, Chat: verbs})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()

	_, last, err := service.chatLast(context.Background(), nil, LastInput{Target: "MCP_HAMMER_A"})
	if err != nil || last.Text != "live answer" || last.Target != "MCP_HAMMER_A" {
		t.Fatalf("chat_last = %+v, err=%v", last, err)
	}
	_, status, err := service.chatStatus(context.Background(), nil, StatusInput{Target: "MCP_HAMMER_A"})
	if err != nil || status.State != headless.StateDead || status.SessionID != "thread-a" ||
		status.Engine != pfmengine.Codex {
		t.Fatalf("chat_status = %+v, err=%v; want the dead chat as a status", status, err)
	}
	if want := []chat.LastRequest{{Target: "MCP_HAMMER_A"}}; !reflect.DeepEqual(verbs.lasts, want) {
		t.Fatalf("Last calls = %+v, want %+v", verbs.lasts, want)
	}
	if want := []chat.StatusRequest{{Target: "MCP_HAMMER_A"}}; !reflect.DeepEqual(verbs.statuses, want) {
		t.Fatalf("Status calls = %+v, want %+v", verbs.statuses, want)
	}
}

func TestCallerScopedSelfIsolatedAcrossRequests(t *testing.T) {
	rows := []compose.Row{
		{Kind: compose.LiveCodex, ID: "thread-a", Name: "a", Socket: "cx-a", SessionName: "renamed-a", PaneID: "%1"},
		{Kind: compose.LiveCodex, ID: "thread-b", Name: "b", Socket: "cx-b", SessionName: "renamed-b", PaneID: "%2"},
	}
	verbs := &fakeChatVerbs{
		listed: chat.ListResult{Rows: rows, Matched: len(rows)},
		last:   chat.LastResult{Text: "answer"}, resolveScopedSelf: true,
	}
	service := newService("test", &backend{
		chat: verbs, paths: paths.Values{TmuxDir: t.TempDir()},
	})
	request := func(id string) *mcp.CallToolRequest {
		return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{"threadId": id}}}
	}
	for _, id := range []string{"thread-a", "thread-b", "thread-a"} {
		if _, _, err := service.chatLast(context.Background(), request(id), LastInput{Target: "self"}); err != nil {
			t.Fatalf("chatLast(self, %s): %v", id, err)
		}
	}
	if want := []string{"thread-a", "thread-b", "thread-a"}; !reflect.DeepEqual(verbs.resolvedSelfIDs, want) {
		t.Fatalf("resolved self ids = %v, want isolated sequence %v", verbs.resolvedSelfIDs, want)
	}
	for _, call := range verbs.lasts {
		if call.Target != "self" {
			t.Fatalf("valid scoped self rewritten to %q", call.Target)
		}
	}
}

func TestCallerScopedSelfReachesMutationDispatchWithoutRedirectingExplicitTargets(t *testing.T) {
	rows := []compose.Row{
		{
			Kind:        compose.LiveClaude,
			ID:          "first",
			Name:        "first",
			Socket:      "cc-shared",
			SessionName: "renamed",
			PaneID:      "%1",
		},
		{
			Kind:        compose.LiveClaude,
			ID:          "second",
			Name:        "second",
			Socket:      "cc-shared",
			SessionName: "renamed",
			PaneID:      "%2",
		},
	}
	var calls [][]string
	var scopedIDs []string
	service := newService("test", &backend{
		chat: &fakeChatVerbs{listed: chat.ListResult{Rows: rows, Matched: len(rows)}},
		dispatch: func(ctx context.Context, args []string, _, _ io.Writer) int {
			calls = append(calls, append([]string(nil), args...))
			if scoped, ok := chat.ScopedSelf(ctx); ok {
				scopedIDs = append(scopedIDs, scoped.ID)
			} else {
				scopedIDs = append(scopedIDs, "")
			}
			return 0
		},
	})
	request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{
		"pfmProxy": map[string]any{
			"v": ProxyWireVersion, "session": "renamed", "socketName": "cc-shared",
			"socketPath": "/tmp/tmux/cc-shared", "pane": "%2", "engine": "claude", "id": "second",
		},
	}}}
	if _, _, err := service.chatName(
		context.Background(),
		request,
		NameInput{Target: "self", Name: "chosen"},
	); err != nil {
		t.Fatalf("chatName(self): %v", err)
	}
	if _, _, err := service.chatKill(context.Background(), request, KillInput{Target: "me", Exit: true}); err != nil {
		t.Fatalf("chatKill(me): %v", err)
	}
	if _, _, err := service.chatUnkill(context.Background(), request, TargetInput{Target: "self"}); err != nil {
		t.Fatalf("chatUnkill(self): %v", err)
	}
	if _, _, err := service.chatKill(context.Background(), request, KillInput{Target: "first"}); err != nil {
		t.Fatalf("chatKill(first): %v", err)
	}
	wantCalls := [][]string{
		{"chat", "name", "self", "chosen"},
		{"chat", "kill", "me", "--exit"},
		{"chat", "unkill", "self"},
		{"chat", "kill", "first"},
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("dispatch calls = %q, want %q", calls, wantCalls)
	}
	if want := []string{"second", "second", "second", ""}; !reflect.DeepEqual(scopedIDs, want) {
		t.Fatalf("dispatch scoped ids = %v, want %v", scopedIDs, want)
	}
}

func TestCallerScopedRawPaneReachesMutationDispatchOnTheCallerSocket(t *testing.T) {
	setupBackendFixture(t)
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	callerSocket := filepath.Join(values.TmuxDir, "cx-caller")
	daemonSocket := filepath.Join(values.TmuxDir, "cc-daemon")
	t.Setenv("CHAT_INJECT_SOCKET", "")
	t.Setenv("TMUX", daemonSocket+",123,0")

	row := compose.Row{
		Kind: compose.LiveCodex, ID: "thread-caller", Name: "caller",
		Socket: "cx-caller", SessionName: "caller-session", PaneID: "%1",
	}
	var resolved headless.Chat
	service := newService("test", &backend{
		paths: values,
		chat:  &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}},
		dispatch: func(ctx context.Context, args []string, _, _ io.Writer) int {
			resolved, err = chat.Target(ctx, args[2], nil)
			if err != nil {
				return 1
			}
			return 0
		},
	})
	request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{
		"pfmProxy": map[string]any{
			"v": ProxyWireVersion, "session": row.SessionName, "socketName": row.Socket,
			"socketPath": callerSocket, "pane": row.PaneID, "engine": "codex", "id": row.ID,
		},
	}}}
	for _, target := range []string{"%0", " %0 ", `"%0"`} {
		resolved = headless.Chat{}
		if _, _, err := service.chatName(
			context.Background(), request, NameInput{Target: target, Name: "chosen"},
		); err != nil {
			t.Fatalf("chatName(%q): %v", target, err)
		}
		if resolved.Socket != filepath.Base(callerSocket) || resolved.Pane != "%0" {
			t.Fatalf(
				"raw action %q resolved to socket %q pane %q, want caller socket %q pane %%0 (daemon socket %q)",
				target,
				resolved.Socket,
				resolved.Pane,
				filepath.Base(callerSocket),
				filepath.Base(daemonSocket),
			)
		}
		if resolved.ID != "" || resolved.Session != "" {
			t.Fatalf("raw action %q inherited caller identity: %+v", target, resolved)
		}
	}
}

func TestResolvedCodexSelfUsesTheNewestLineageRollout(t *testing.T) {
	setupBackendFixture(t)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	root := store.Rollout{ID: "root", Path: "/root.jsonl", UserThread: true, SessionID: "root", MTimeNS: 100}
	child := store.Rollout{ID: "child", Path: "/child.jsonl", UserThread: true, SessionID: "root", MTimeNS: 200}
	for _, rollout := range []store.Rollout{root, child} {
		if err := database.UpsertRollout(context.Background(), rollout); err != nil {
			t.Fatal(err)
		}
	}
	service := newService("test", &backend{database: database})
	self, err := service.resolvedSelf(context.Background(), callerIdentity{
		valid: true,
		identity: resolve.Identity{
			ID: "root", Engine: string(pfmengine.Codex), SocketName: "cx-seat", Session: "cx-seat", Pane: "%0",
		},
	})
	if err != nil || self.Path != child.Path {
		t.Fatalf("resolved self path = %q err=%v, want newest lineage path %q", self.Path, err, child.Path)
	}
}

func TestReviewScopedSelfKeepsUnindexedTranscript(t *testing.T) {
	root := setupBackendFixture(t)
	transcriptPath := filepath.Join(root, "claude", "project-alpha", "unindexed.jsonl")
	if err := os.WriteFile(
		transcriptPath,
		[]byte(`{"type":"assistant","message":{"content":"unindexed answer"}}`+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	row := compose.Row{
		Kind: compose.LiveClaude, ID: "unindexed", Path: transcriptPath, CWD: "/work/alpha",
		SessionName: "cc-unindexed", Socket: "cc-unindexed", PaneID: "%1",
	}
	verbs := &fakeChatVerbs{
		listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1},
		last:   chat.LastResult{Text: "unindexed answer"}, resolveScopedSelf: true,
	}
	service := newService("test", &backend{
		paths: paths.Values{TmuxDir: t.TempDir()},
		chat:  verbs,
	})
	request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{
		"pfmProxy": map[string]any{
			"v": ProxyWireVersion, "session": row.SessionName, "socketName": row.Socket,
			"pane": row.PaneID, "engine": "claude", "id": row.ID,
		},
	}}}
	if _, _, err := service.chatLast(context.Background(), request, LastInput{Target: "self"}); err != nil {
		t.Fatalf("chatLast(self): %v", err)
	}
	if want := []string{transcriptPath}; !reflect.DeepEqual(verbs.resolvedSelfPaths, want) {
		t.Fatalf("scoped self paths = %q, want composed transcript %q", verbs.resolvedSelfPaths, want)
	}
	encoded, err := json.Marshal(ChatRow{ID: row.ID, transcriptPath: transcriptPath})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), transcriptPath) || strings.Contains(string(encoded), "transcriptPath") {
		t.Fatalf("private transcript path leaked onto chat row wire: %s", encoded)
	}
}

func TestChatNewDefaultsEngineFromValidatedCaller(t *testing.T) {
	tests := []struct {
		name   string
		kind   compose.Kind
		engine string
	}{
		{name: "claude", kind: compose.LiveClaude, engine: "cc"},
		{name: "codex", kind: compose.LiveCodex, engine: "cx"},
		{name: "opencode", kind: compose.LiveOpenCode, engine: "ox"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			row := compose.Row{
				Kind: test.kind, ID: test.name + "-id", CWD: "/caller/" + test.name,
				SessionName: test.name + "-seat", Socket: test.name + "-socket", PaneID: "%1",
			}
			var calls [][]string
			service := newService("test", &backend{
				paths: paths.Values{TmuxDir: t.TempDir()},
				chat:  &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}},
				dispatch: func(_ context.Context, args []string, _, _ io.Writer) int {
					calls = append(calls, append([]string(nil), args...))
					return 0
				},
			})
			request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{
				"pfmProxy": map[string]any{
					"v": ProxyWireVersion, "session": row.SessionName, "socketName": row.Socket,
					"pane": row.PaneID, "engine": test.name, "id": row.ID,
				},
			}}}
			if _, _, err := service.chatNew(
				context.Background(), request, NewInput{Name: "child", CWD: "/explicit"},
			); err != nil {
				t.Fatal(err)
			}
			want := [][]string{{
				"chat", "new", "--name", "child", "--engine", test.engine, "--cwd", "/explicit",
			}}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("chat_new calls = %q, want %q", calls, want)
			}
		})
	}
}

func TestChatNewCarriesValidatedCallerContextAndPaths(t *testing.T) {
	row := compose.Row{
		Kind: compose.LiveClaude, ID: "caller-id", CWD: "/caller",
		SessionName: "caller-seat", Socket: "caller-socket", PaneID: "%1",
	}
	meta := mcp.Meta{"pfmProxy": map[string]any{
		"v": ProxyWireVersion, "session": row.SessionName, "socketName": row.Socket,
		"pane": row.PaneID, "engine": "claude", "id": row.ID,
	}}
	var calls [][]string
	var scopedIDs []string
	service := newService("test", &backend{
		paths: paths.Values{TmuxDir: t.TempDir()},
		chat:  &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}},
		dispatch: func(ctx context.Context, args []string, _, _ io.Writer) int {
			calls = append(calls, append([]string(nil), args...))
			if self, ok := chat.ScopedSelf(ctx); ok {
				scopedIDs = append(scopedIDs, self.ID)
			} else {
				scopedIDs = append(scopedIDs, "")
			}
			return 0
		},
	})
	request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: meta}}
	if _, _, err := service.chatNew(
		context.Background(), request, NewInput{Name: "override", Engine: "opencode"},
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.chatNew(
		context.Background(), request, NewInput{Name: "relative", Engine: "codex", CWD: "child"},
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.chatNew(
		context.Background(), request, NewInput{Name: "explicit", Engine: "codex", CWD: "/chosen"},
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.chatNew(context.Background(), nil, NewInput{Name: "ambient"}); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"chat", "new", "--name", "override", "--engine", "opencode", "--cwd", "/caller"},
		{"chat", "new", "--name", "relative", "--engine", "codex", "--cwd", "/caller/child"},
		{"chat", "new", "--name", "explicit", "--engine", "codex", "--cwd", "/chosen"},
		{"chat", "new", "--name", "ambient"},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("chat_new calls = %q, want %q", calls, want)
	}
	if wantIDs := []string{"caller-id", "caller-id", "caller-id", ""}; !reflect.DeepEqual(scopedIDs, wantIDs) {
		t.Fatalf("chat_new scoped caller ids = %q, want %q", scopedIDs, wantIDs)
	}

	failing := newService("test", &backend{
		paths: paths.Values{TmuxDir: t.TempDir()},
		chat:  &fakeChatVerbs{err: errors.New("must not list")},
	})
	if _, _, err := failing.chatNew(
		context.Background(), request,
		NewInput{Name: "explicit", Engine: "codex", CWD: "/chosen"},
	); err == nil || !strings.Contains(err.Error(), "must not list") {
		t.Fatalf("fully explicit chat_new caller lookup error = %v", err)
	}
	for _, test := range []struct {
		name string
		cwd  string
	}{
		{name: "missing caller directory"},
		{name: "relative caller directory", cwd: "caller"},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalidRow := row
			invalidRow.CWD = test.cwd
			refusing := newService("test", &backend{
				paths: paths.Values{TmuxDir: t.TempDir()},
				chat:  &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{invalidRow}, Matched: 1}},
			})
			_, _, err := refusing.chatNew(
				context.Background(), request, NewInput{Name: "child", CWD: "relative"},
			)
			if err == nil || !strings.Contains(err.Error(), "caller working directory") {
				t.Fatalf("chat_new relative cwd error = %v", err)
			}
		})
	}
}

func TestChatNewRefusesPresentInvalidCallerMetadata(t *testing.T) {
	var calls int
	service := newService("test", &backend{
		chat: &fakeChatVerbs{listed: chat.ListResult{}},
		dispatch: func(_ context.Context, _ []string, _, _ io.Writer) int {
			calls++
			return 0
		},
	})
	request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{"threadId": "missing"}}}
	_, _, err := service.chatNew(
		context.Background(), request, NewInput{Name: "must-not-launch", Engine: "codex", CWD: "/chosen"},
	)
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("chat_new invalid metadata error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("chat_new invalid metadata dispatched %d calls", calls)
	}
}

func TestResolvedSelfPathFallbacksAndIndexedPrecedence(t *testing.T) {
	setupBackendFixture(t)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.UpsertTranscript(context.Background(), store.Transcript{
		UUID: "indexed", Path: "/indexed.jsonl", CWD: "/work/indexed",
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertTranscript(context.Background(), store.Transcript{
		UUID: "indexed-empty", CWD: "/work/indexed-empty",
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertRollout(context.Background(), store.Rollout{
		ID: "codex-indexed", SessionID: "codex-indexed", Path: "/indexed-rollout.jsonl",
		UserThread: true, MTimeNS: 1,
	}); err != nil {
		t.Fatal(err)
	}
	service := newService("test", &backend{database: database})
	tests := []struct {
		name   string
		engine pfmengine.ID
		id     string
		row    string
		want   string
	}{
		{
			name: "unindexed claude", engine: pfmengine.Claude, id: "missing",
			row: "/composed.jsonl", want: "/composed.jsonl",
		},
		{name: "indexed claude", engine: pfmengine.Claude, id: "indexed", row: "/stale.jsonl", want: "/indexed.jsonl"},
		{
			name: "empty indexed claude", engine: pfmengine.Claude, id: "indexed-empty",
			row: "/composed-empty.jsonl", want: "/composed-empty.jsonl",
		},
		{
			name: "unindexed codex", engine: pfmengine.Codex, id: "codex-missing",
			row: "/rollout.jsonl", want: "/rollout.jsonl",
		},
		{
			name: "indexed codex", engine: pfmengine.Codex, id: "codex-indexed",
			row: "/stale-rollout.jsonl", want: "/indexed-rollout.jsonl",
		},
		{name: "no path", engine: pfmengine.Claude, id: "none", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			self, resolveErr := service.resolvedSelf(context.Background(), callerIdentity{
				valid:    true,
				identity: resolve.Identity{ID: test.id, Engine: string(test.engine)},
				row:      ChatRow{Engine: test.engine, transcriptPath: test.row},
			})
			if resolveErr != nil || self.Path != test.want {
				t.Fatalf("resolved path = %q err=%v, want %q", self.Path, resolveErr, test.want)
			}
		})
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = service.resolvedSelf(context.Background(), callerIdentity{
		valid:    true,
		identity: resolve.Identity{ID: "lookup-error", Engine: string(pfmengine.Claude)},
		row:      ChatRow{Engine: pfmengine.Claude, transcriptPath: "/composed.jsonl"},
	})
	if err == nil || !strings.Contains(err.Error(), `resolve MCP self transcript "lookup-error"`) {
		t.Fatalf("lookup error collapsed into composed fallback: %v", err)
	}
}

// TestChatStatusSummaryReachesTheVerbAndReturnsField pins summary=true end to
// end over the protocol: the engine name parses to its ID, the model rides
// along, and the verb's summary comes back in the structured output.
func TestChatStatusSummaryReachesTheVerbAndReturnsField(t *testing.T) {
	verbs := &fakeChatVerbs{status: headless.Status{
		Name: "seat", State: headless.StateIdle, IdleSeconds: 2, Engine: pfmengine.Claude,
		Summary: "done", SummaryCached: true,
	}}
	protocol := connectInMemory(t, newService("test", &backend{chat: verbs}).Server())
	output := callTool[StatusOutput](t, protocol.clientSession, "chat_status", StatusInput{
		Target: "seat", Summary: true, Engine: "codex", Model: "test-model",
	})
	want := []chat.StatusRequest{{Target: "seat", Summary: true, Engine: pfmengine.Codex, Model: "test-model"}}
	if !reflect.DeepEqual(verbs.statuses, want) {
		t.Fatalf("Status calls = %+v, want %+v", verbs.statuses, want)
	}
	if output.Summary != "done" || !output.SummaryCached || output.Engine != pfmengine.Claude {
		t.Fatalf("status output=%+v", output)
	}
}

// TestChatStatusAskReachesTheVerbAndReturnsField is the same pin for ask=true.
func TestChatStatusAskReachesTheVerbAndReturnsField(t *testing.T) {
	const answer = "TRANSCRIPT-ONLY (chat is not live: there is no pane to capture): steady state"
	verbs := &fakeChatVerbs{status: headless.Status{
		Name: "seat", State: headless.StateIdle, IdleSeconds: 2, Engine: pfmengine.Claude, Ask: answer,
	}}
	protocol := connectInMemory(t, newService("test", &backend{chat: verbs}).Server())
	output := callTool[StatusOutput](t, protocol.clientSession, "chat_status", StatusInput{
		Target: "seat", Ask: true, Engine: "codex", Model: "test-model",
	})
	want := []chat.StatusRequest{{Target: "seat", Ask: true, Engine: pfmengine.Codex, Model: "test-model"}}
	if !reflect.DeepEqual(verbs.statuses, want) {
		t.Fatalf("Status calls = %+v, want %+v", verbs.statuses, want)
	}
	if output.Ask != answer {
		t.Fatalf("status output=%+v", output)
	}
}

// TestChatLastAndStatusReportTheVerbsFailureWithItsKind pins the error seam:
// the verb's own error reaches the MCP caller with its kind intact — no rc,
// no decode noise — so a caller can still tell a target failure apart.
func TestChatLastAndStatusReportTheVerbsFailureWithItsKind(t *testing.T) {
	verbs := &fakeChatVerbs{err: &chat.TargetError{
		Name: "duplicate", Err: errors.New(`"duplicate" matches 2 chats`),
	}}
	service := newService("test", &backend{chat: verbs})
	_, _, statusErr := service.chatStatus(context.Background(), nil, StatusInput{Target: "duplicate"})
	_, _, lastErr := service.chatLast(context.Background(), nil, LastInput{Target: "duplicate"})
	for name, err := range map[string]error{"chat_status": statusErr, "chat_last": lastErr} {
		var target *chat.TargetError
		if !errors.As(err, &target) || !strings.Contains(err.Error(), "matches 2 chats") ||
			strings.Contains(err.Error(), "decode") {
			t.Fatalf("%s error = %v, want the verb's *TargetError verbatim", name, err)
		}
	}
}

// TestChatLastAndStatusRefuseWithoutAVerbLayer pins the broken wiring's own
// answer: a server built without its verb layer says so, and an engine name
// the registry does not know is refused before any verb runs.
func TestChatLastAndStatusRefuseWithoutAVerbLayer(t *testing.T) {
	unwired := newService("test", &backend{})
	if _, _, err := unwired.chatLast(context.Background(), nil, LastInput{Target: "seat"}); err == nil ||
		!strings.Contains(err.Error(), "chat_last verb is not configured") {
		t.Fatalf("chat_last without verbs error = %v", err)
	}
	if _, _, err := unwired.chatStatus(context.Background(), nil, StatusInput{Target: "seat"}); err == nil ||
		!strings.Contains(err.Error(), "chat_status verb is not configured") {
		t.Fatalf("chat_status without verbs error = %v", err)
	}
	verbs := &fakeChatVerbs{}
	_, _, err := newService("test", &backend{chat: verbs}).chatStatus(context.Background(), nil, StatusInput{
		Target: "seat", Summary: true, Engine: "gpt",
	})
	if err == nil || !strings.Contains(err.Error(), `unknown engine "gpt"`) || len(verbs.statuses) != 0 {
		t.Fatalf("chat_status engine=gpt error = %v, calls = %+v; want a refusal before the verb", err, verbs.statuses)
	}
}

// TestChatOpenUnindexedTargetIsATooErrorNeverEmptySuccess pins chat_open's
// failure shape now that it opens through action.OpenDetached instead of the
// argv Dispatch seam: a target that is not indexed comes back as a tool
// error naming the target, never an "ok" ActionOutput with nothing behind
// it.
func TestChatOpenUnindexedTargetIsATooErrorNeverEmptySuccess(t *testing.T) {
	setupBackendFixture(t)
	service := newFixtureService(t)
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	_, output, err := service.chatOpen(context.Background(), nil, TargetInput{Target: "no-such-chat"})
	if err == nil || !strings.Contains(err.Error(), "no-such-chat") {
		t.Fatalf("chat_open unindexed target error = %v, want it to name the target", err)
	}
	if output.Status == "ok" {
		t.Fatalf("chat_open unindexed target reported ok: %+v", output)
	}
}

// TestChatOpenResolvesATargetByName pins the resolution the CLI has and the
// MCP door lost: `pfm chat open` reaches its row through chat.Target (name,
// id prefix or socket), so addressing a chat by its NAME is the ordinary
// case. A door matching row.ID == target answers a name with "is not
// indexed" — an absence for a chat that is right there.
func TestChatOpenResolvesATargetByName(t *testing.T) {
	root := setupBackendFixture(t)
	// The fixture's own Claude chat, addressed the way a caller does.
	rows, err := chat.Rows(context.Background(), io.Discard, nil)
	if err != nil {
		t.Fatalf("fleet scan failed, so the case below would pass vacuously: %v", err)
	}
	var name string
	for _, row := range rows {
		if row.ID == "alpha" {
			name = row.Name
		}
	}
	if name == "" || name == "alpha" {
		t.Fatalf("fixture row alpha has no name distinct from its id: %+v", rows)
	}
	// The spawn must not reach a real tmux server: with the socket directory
	// gone, the detached door fails loudly at its OWN step. What is pinned
	// here is that the name got it that far at all.
	t.Setenv(paths.EnvTmuxDir, filepath.Join(root, "no-such-tmux-dir"))
	service := newFixtureService(t)
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	_, output, err := service.chatOpen(context.Background(), nil, TargetInput{Target: name})
	if errors.Is(err, chat.ErrUnknownChat) || (err != nil && strings.Contains(err.Error(), "not indexed")) {
		t.Fatalf("chat_open %q = %v; the name never reached the open door", name, err)
	}
	if err != nil && !strings.Contains(err.Error(), "open detached") {
		t.Fatalf("chat_open %q failed before the open door: %v (output %+v)", name, err, output)
	}
}

// TestListProjectedScopeNamesTheRepoItWasGiven pins pfm-update-6#F4: a
// repository-scoped listing reports that repository as its scope, and only
// an unscoped one reports every repository.
func TestListProjectedScopeNamesTheRepoItWasGiven(t *testing.T) {
	current := &backend{chat: &fakeChatVerbs{}}
	for repo, want := range map[string]string{"/work/one": "/work/one", "": "all repos"} {
		output, err := current.listProjected(context.Background(), LSInput{}, true, repo)
		if err != nil {
			t.Fatalf("listProjected(%q): %v", repo, err)
		}
		if output.Scope != want {
			t.Errorf("listProjected(%q).Scope = %q, want %q", repo, output.Scope, want)
		}
	}
}

package mcpserv

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
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

// metadataIdentityService installs two deterministic live Codex rows through
// the same list callback production uses. The rows are the authority for the
// thread-id -> tmux-seat binding; no caller environment is populated, so the
// test catches servers that accidentally read their own process instead of
// the requesting MCP session.
func metadataIdentityService(t *testing.T) *Service {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not installed")
	}
	jail := newStdioJail(t)
	t.Setenv("HOME", jail.home)
	t.Setenv("TMUX", "")
	t.Setenv("CHAT_CALL_SOCKET", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TMUX_TMPDIR", jail.tmuxBase)
	t.Setenv(resolve.ClaudeSessionEnv, "")
	t.Setenv(resolve.CodexThreadEnv, "")
	t.Setenv(pfmcall.SenderSessionEnv, "")
	t.Setenv(pfmcall.SenderLabelEnv, "")
	t.Setenv(pfmcall.SenderIDEnv, "")
	t.Setenv(paths.EnvHome, jail.home)
	t.Setenv(paths.EnvCacheDB, jail.database)
	t.Setenv(paths.EnvSIDDir, jail.sid)
	t.Setenv(paths.EnvClaudeRoots, jail.claude)
	t.Setenv(paths.EnvCodexHome, jail.codex)
	t.Setenv(paths.EnvTmuxDir, jail.tmuxDir)
	t.Setenv(paths.EnvProcRoot, jail.proc)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	rows := []compose.Row{
		{
			SessionName: jail.session, ID: "thread-a", CWD: "/work/alpha", Project: "alpha",
			Name: "Codex A", Kind: compose.LiveCodex, Socket: jail.socket, PaneID: jail.pane,
		},
		{
			SessionName: jail.busySession, ID: "thread-b", CWD: "/work/beta", Project: "beta",
			Name: "Codex B", Kind: compose.LiveCodex, Socket: jail.busySocket, PaneID: "%0",
		},
	}
	service, err := NewConfigured("test", io.Discard, Runtime{
		Paths: resolved,
		Chat:  &fakeChatVerbs{listed: chat.ListResult{Rows: rows, Matched: len(rows)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	testCaller, err := pfmcall.New(pfmcall.Dependencies{
		Resolver: metadataNamedResolver{
			fallback: &service.backend.resolver,
			name:     "Codex B",
			socket:   filepath.Join(jail.tmuxDir, jail.busySocket),
			pane:     "%0",
		},
		Tmux:           pfmcall.TmuxCaller{},
		Spawner:        metadataThenSpawner{},
		ClaudeBinary:   "claude",
		CodexBinary:    "codex",
		OpenCodeBinary: "opencode",
		Recorder:       service.backend.sharedState.RecordComms,
		WarningWriter:  service.backend.warnings,
	})
	if err != nil {
		t.Fatal(err)
	}
	service.backend.callEngine = testCaller
	t.Cleanup(func() { _ = service.Close() })
	return service
}

// TestMetadataIdentityNormalizesSelfBeforeTheChatVerbs pins that "self" on
// last and status is the REQUEST's caller — resolved from the call's
// metadata before the verb runs, while the literal self target remains intact.
func TestMetadataIdentityNormalizesSelfBeforeTheChatVerbs(t *testing.T) {
	service := metadataIdentityService(t)
	// The service's verb fake already lists the jailed Codex seats the caller
	// resolves against; the verbs under test answer beside them.
	verbs := service.backend.chat.(*fakeChatVerbs)
	verbs.resolveScopedSelf = true
	verbs.last = chat.LastResult{Text: "self answer\n"}
	verbs.status = headless.Status{
		Name: "Codex A", State: headless.StateIdle, Engine: pfmengine.Codex, SessionID: "thread-a",
	}
	protocol := connectInMemory(t, service.Server())
	meta := mcp.Meta{"threadId": "thread-a"}
	last := callToolWithMeta[LastOutput](t, protocol.clientSession, "last", meta, LastInput{Target: "self"})
	if last.Target != "self" || last.Text != "self answer" {
		t.Fatalf("last(self) = %+v", last)
	}
	status := callToolWithMeta[StatusOutput](
		t,
		protocol.clientSession,
		"status",
		meta,
		StatusInput{Target: "self"},
	)
	if status.Name != "Codex A" || status.SessionID != "thread-a" {
		t.Fatalf("status(self) = %+v", status)
	}
	if want := []chat.LastRequest{{Target: "self"}}; !reflect.DeepEqual(verbs.lasts, want) {
		t.Fatalf("self Last calls = %+v, want %+v", verbs.lasts, want)
	}
	if want := []chat.StatusRequest{{Target: "self"}}; !reflect.DeepEqual(verbs.statuses, want) {
		t.Fatalf("self Status calls = %+v, want %+v", verbs.statuses, want)
	}
	if !reflect.DeepEqual(verbs.resolvedSelfIDs, []string{"thread-a"}) ||
		!reflect.DeepEqual(verbs.resolvedStatusIDs, []string{"thread-a"}) {
		t.Fatalf("scoped identities: last=%v status=%v, want thread-a", verbs.resolvedSelfIDs, verbs.resolvedStatusIDs)
	}
}

func TestChatNewDefaultsToRequestScopedCallerCWD(t *testing.T) {
	service := metadataIdentityService(t)
	var calls [][]string
	service.backend.dispatch = func(_ context.Context, args []string, stdout, _ io.Writer) int {
		calls = append(calls, append([]string(nil), args...))
		_, _ = io.WriteString(stdout, "launched\n")
		return 0
	}
	protocol := connectInMemory(t, service.Server())
	created := callToolWithMeta[ActionOutput](
		t, protocol.clientSession, "new", mcp.Meta{"threadId": "thread-a"},
		NewInput{Name: "child"},
	)
	if created.Status != "ok" || created.Code != 0 {
		t.Fatalf("request-scoped new = %+v", created)
	}
	want := [][]string{{"chat", "new", "--name", "child", "--engine", "cx", "--cwd", "/work/alpha"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("new dispatch calls = %q, want %q", calls, want)
	}
}

// metadataThenSpawner keeps detached waiter tests inside the test process.
// CommandThenSpawner deliberately uses os.Executable; under go test that is
// the test binary, so launching it as `internal then` would recursively rerun
// the package instead of exercising the waiter command.
type metadataThenSpawner struct{}

func (metadataThenSpawner) Spawn(context.Context, pfmcall.SteerSpawn) error { return nil }

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

// TestMCPMetadataThreadIdentityRoutesDistinctCodexSeats pins the real
// protocol boundary for a long-lived MCP server: each call's _meta.threadId
// selects its own live row for whoami, self capture, and self call. The
// calls are repeated concurrently so a cached sender or cached caller
// identity cannot leak seat A into seat B.
func TestMCPMetadataThreadIdentityRoutesDistinctCodexSeats(t *testing.T) {
	service := metadataIdentityService(t)
	clients := []protocolClient{
		connectInMemory(t, service.Server()),
		connectInMemory(t, service.Server()),
	}
	metas := []mcp.Meta{
		{"threadId": "thread-a"},
		{"threadId": "thread-b"},
	}
	wantSessions := []string{"fixture-session", "busy-session"}

	for index := range clients {
		whoami := callToolWithMeta[WhoamiOutput](
			t, clients[index].clientSession, "whoami", metas[index], WhoamiInput{},
		)
		if whoami.Status != "ok" || whoami.ID != metas[index]["threadId"] ||
			whoami.Session != wantSessions[index] || whoami.Engine != string(pfmengine.Codex) {
			t.Errorf(
				"whoami[%d] = %+v, want thread %q on %s",
				index,
				whoami,
				metas[index]["threadId"],
				wantSessions[index],
			)
		}

		captured := callToolWithMeta[CaptureOutput](
			t, clients[index].clientSession, "capture", metas[index], CaptureInput{Target: "self"},
		)
		if captured.Status != "ok" || captured.Code != 0 || captured.SocketPath == "" {
			t.Errorf("capture[%d] = %+v, want the caller's live seat", index, captured)
		}

		keyed := callToolWithMeta[KeysOutput](
			t, clients[index].clientSession, "keys", metas[index], KeysInput{
				Target: "self", Keys: []string{"Escape"}, Capture: true,
			},
		)
		if keyed.Status != "ok" || keyed.Code != 0 || keyed.Count != 1 ||
			keyed.SocketPath != captured.SocketPath || keyed.Pane != captured.Pane {
			t.Errorf("keys[%d] = %+v, want the same request-scoped live seat as capture %+v", index, keyed, captured)
		}

		called := callToolWithMeta[CallOutput](
			t, clients[index].clientSession, "call", metas[index], CallInput{
				Target: "self", Message: "signed from " + metas[index]["threadId"].(string),
			},
		)
		if called.Code != 0 || !called.Typed || called.Unsigned {
			t.Errorf("call[%d] = %+v, want typed signed delivery", index, called)
		}
		for _, signaturePart := range []string{
			"sid " + metas[index]["threadId"].(string),
			`to reply: call "Codex ` + string(rune('A'+index)) + `" <message>`,
		} {
			if !strings.Contains(called.Proof, signaturePart) {
				t.Errorf(
					"call[%d] proof %q lacks request-scoped signature part %q",
					index,
					called.Proof,
					signaturePart,
				)
			}
		}
	}

	var wait sync.WaitGroup
	errs := make(chan string, len(clients))
	for index := range clients {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 10; iteration++ {
				whoami := callToolWithMeta[WhoamiOutput](
					t, clients[index].clientSession, "whoami", metas[index], WhoamiInput{},
				)
				if whoami.ID != metas[index]["threadId"] || whoami.Session != wantSessions[index] {
					errs <- "cross-talk"
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	// A metadata-free call after valid calls must not inherit either caller.
	missing := callToolWithMeta[WhoamiOutput](
		t, clients[0].clientSession, "whoami", nil, WhoamiInput{},
	)
	if missing.Status != "not_found" || missing.Session != "" || missing.ID != "" {
		t.Fatalf("metadata-free whoami inherited a prior callEngine: %+v", missing)
	}
}

func TestMCPRawPaneUsesRequestCallerSocketAcrossTools(t *testing.T) {
	service := metadataIdentityService(t)
	client := connectInMemory(t, service.Server())
	meta := mcp.Meta{"threadId": "thread-a"}
	want := callToolWithMeta[WhoamiOutput](
		t, client.clientSession, "whoami", meta, WhoamiInput{},
	)
	if want.SocketPath == "" || want.Pane == "" {
		t.Fatalf("request caller identity lacks a raw tmux destination: %+v", want)
	}

	captured := callToolWithMeta[CaptureOutput](
		t, client.clientSession, "capture", meta, CaptureInput{Target: want.Pane},
	)
	if captured.Status != "ok" || captured.Code != 0 ||
		captured.SocketPath != want.SocketPath || captured.Pane != want.Pane {
		t.Fatalf("raw capture = %+v, want caller socket %q pane %q", captured, want.SocketPath, want.Pane)
	}

	keyed := callToolWithMeta[KeysOutput](
		t, client.clientSession, "keys", meta,
		KeysInput{Target: want.Pane, Keys: []string{"Escape"}, Capture: true},
	)
	if keyed.Status != "ok" || keyed.Code != 0 || keyed.Count != 1 ||
		keyed.SocketPath != want.SocketPath || keyed.Pane != want.Pane {
		t.Fatalf("raw keys = %+v, want caller socket %q pane %q", keyed, want.SocketPath, want.Pane)
	}

	called := callToolWithMeta[CallOutput](
		t, client.clientSession, "call", meta,
		CallInput{Target: want.Pane, Message: "raw pane request scope"},
	)
	if called.Code != 0 || !called.Typed || called.Unsigned ||
		called.SocketPath != want.SocketPath || called.Pane != want.Pane {
		t.Fatalf("raw call = %+v, want caller socket %q pane %q", called, want.SocketPath, want.Pane)
	}
}

func TestMCPResolveBindsRelativeTargetsToEachRequestCaller(t *testing.T) {
	service := metadataIdentityService(t)
	clients := []protocolClient{
		connectInMemory(t, service.Server()),
		connectInMemory(t, service.Server()),
	}
	metas := []mcp.Meta{{"threadId": "thread-a"}, {"threadId": "thread-b"}}

	for index, name := range []string{"self", "me"} {
		want := callToolWithMeta[WhoamiOutput](
			t, clients[index].clientSession, "whoami", metas[index], WhoamiInput{},
		)
		for _, kind := range []resolve.Kind{resolve.Label, resolve.Session, resolve.CxWindow} {
			resolved := callToolWithMeta[ResolveOutput](
				t, clients[index].clientSession, "resolve", metas[index],
				ResolveInput{Kind: string(kind), Name: name},
			)
			if resolved.Status != "ok" || resolved.Code != 0 ||
				resolved.SocketPath != want.SocketPath || resolved.Pane != want.Pane {
				t.Fatalf("resolve(%s, %s) = %+v, want caller %+v", kind, name, resolved, want)
			}
		}
	}

	caller := callToolWithMeta[WhoamiOutput](
		t, clients[0].clientSession, "whoami", metas[0], WhoamiInput{},
	)
	t.Setenv("CHAT_CALL_SOCKET", filepath.Join(service.backend.paths.TmuxDir, "cx-ambient-other"))
	for _, kind := range []resolve.Kind{resolve.Label, resolve.Session, resolve.CxWindow} {
		raw := callToolWithMeta[ResolveOutput](
			t, clients[0].clientSession, "resolve", metas[0],
			ResolveInput{Kind: string(kind), Name: " %2 "},
		)
		if raw.Status != "ok" || raw.Code != 0 ||
			raw.SocketPath != caller.SocketPath || raw.Pane != "%2" {
			t.Fatalf("resolve(%s, %%2) = %+v, want caller socket %q", kind, raw, caller.SocketPath)
		}
	}

	type result struct {
		index  int
		output ResolveOutput
	}
	results := make(chan result, len(clients))
	var wait sync.WaitGroup
	for index := range clients {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- result{index: index, output: callToolWithMeta[ResolveOutput](
				t, clients[index].clientSession, "resolve", metas[index],
				ResolveInput{Kind: string(resolve.Session), Name: "self"},
			)}
		}()
	}
	wait.Wait()
	close(results)
	for got := range results {
		want := callToolWithMeta[WhoamiOutput](
			t, clients[got.index].clientSession, "whoami", metas[got.index], WhoamiInput{},
		)
		if got.output.SocketPath != want.SocketPath || got.output.Pane != want.Pane {
			t.Errorf("overlapping resolve[%d] = %+v, want caller %+v", got.index, got.output, want)
		}
	}
}

func TestMCPResolveRefusesRelativeTargetsWithoutAValidCaller(t *testing.T) {
	service := metadataIdentityService(t)
	client := connectInMemory(t, service.Server())
	t.Setenv("CHAT_CALL_SOCKET", filepath.Join(service.backend.paths.TmuxDir, "cx-ambient-other"))
	badMetas := []mcp.Meta{nil, {"threadId": "not-a-live-thread"}}
	for _, meta := range badMetas {
		for _, kind := range []resolve.Kind{resolve.Label, resolve.Session, resolve.CxWindow} {
			for _, name := range []string{"self", "me", "%2", ` "self" `, ` "%2" `} {
				resolved := callToolWithMeta[ResolveOutput](
					t, client.clientSession, "resolve", meta,
					ResolveInput{Kind: string(kind), Name: name},
				)
				if resolved.Status != statusNotFound || resolved.Code != 1 ||
					resolved.SocketPath != "" || resolved.Pane != "" {
					t.Fatalf("resolve(%s, %q, meta=%v) = %+v, want refusal", kind, name, meta, resolved)
				}
			}
		}

		explicit := callToolWithMeta[ResolveOutput](
			t, client.clientSession, "resolve", meta,
			ResolveInput{Kind: string(resolve.Label), Name: "Codex B"},
		)
		if explicit.Status != "ok" || explicit.Code != 0 || explicit.Pane != "%0" ||
			!strings.HasSuffix(explicit.SocketPath, "cc-1700000002-1-3") {
			t.Fatalf("explicit resolve(meta=%v) = %+v", meta, explicit)
		}
	}
}

// A shared HTTP daemon can be launched by a developer from inside Claude. A
// metadata-free request belongs to no chat and must not inherit that launcher's
// session id, even when the daemon process has a real ambient Claude identity.
func TestMetadataFreeDaemonIdentityDoesNotInheritAmbientClaude(t *testing.T) {
	service := metadataIdentityService(t)
	client := connectInMemory(t, service.Server())
	t.Setenv(resolve.ClaudeSessionEnv, "ambient-launcher-session")

	whoami := callToolWithMeta[WhoamiOutput](
		t, client.clientSession, "whoami", nil, WhoamiInput{},
	)
	if whoami.Status != "not_found" || whoami.Session != "" ||
		whoami.ID != "" || whoami.Engine != "" || whoami.Source != "" {
		t.Fatalf("metadata-free daemon request inherited ambient Claude identity: %+v", whoami)
	}
}

// TestMCPMetadataIdentityRefusesMissingUnknownAndMalformedValues pins the
// fail-closed edge: no metadata, an unknown thread, and a non-string value
// cannot impersonate a live row or make self resolve to an arbitrary pane.
func TestMCPMetadataIdentityRefusesMissingUnknownAndMalformedValues(t *testing.T) {
	service := metadataIdentityService(t)
	client := connectInMemory(t, service.Server())
	tests := []struct {
		name string
		meta mcp.Meta
	}{
		{name: "missing", meta: nil},
		{name: "unknown", meta: mcp.Meta{"threadId": "not-a-live-thread"}},
		{name: "non-string", meta: mcp.Meta{"threadId": 7}},
		{name: "empty", meta: mcp.Meta{"threadId": ""}},
		{name: "outer whitespace", meta: mcp.Meta{"threadId": " thread-a "}},
		{name: "control character", meta: mcp.Meta{"threadId": "thread-a\n"}},
		{name: "wrong key spelling", meta: mcp.Meta{"thread_id": "thread-a"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			whoami := callToolWithMeta[WhoamiOutput](
				t, client.clientSession, "whoami", test.meta, WhoamiInput{},
			)
			if whoami.Status != "not_found" || whoami.Session != "" {
				t.Fatalf("whoami = %+v, want honest not-found", whoami)
			}
			captured := callToolWithMeta[CaptureOutput](
				t, client.clientSession, "capture", test.meta, CaptureInput{Target: "self"},
			)
			if captured.Status != "not_found" || captured.Code == 0 || captured.Text != "" {
				t.Fatalf("capture = %+v, want honest not-found", captured)
			}
			called := callToolWithMeta[CallOutput](
				t, client.clientSession, "call", test.meta, CallInput{
					Target: "self", Message: "must not impersonate",
				},
			)
			if called.Code == 0 || called.Typed || called.Unsigned {
				t.Fatalf("call = %+v, want refusal without a signed/typed impersonation", called)
			}
		})
	}
}

func TestMCPMetadataIdentityRejectsAmbiguousRowsAndNamesListFailures(t *testing.T) {
	setupBackendFixture(t)
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
		!strings.Contains(caller.detail, "matched 2 live Codex tmux seats") {
		t.Fatalf("duplicate caller resolution = %+v err=%v", caller, err)
	}

	listBackend := &backend{
		paths: resolved,
		chat:  &fakeChatVerbs{err: errors.New("fleet database busy")},
	}
	_, err = listBackend.callerForRequest(
		context.Background(), mcp.Meta{"threadId": "thread-a"},
	)
	if err == nil || !strings.Contains(err.Error(), "list live chats: fleet database busy") {
		t.Fatalf("list failure collapsed into absence: %v", err)
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

func TestMCPMetadataSignsExplicitTargetWithoutRedirectingIt(t *testing.T) {
	service := metadataIdentityService(t)
	client := connectInMemory(t, service.Server())
	output := callToolWithMeta[CallOutput](
		t, client.clientSession, "call", mcp.Meta{"threadId": "thread-a"},
		CallInput{Target: "busy-session", Message: "explicit target provenance"},
	)
	if output.Code != 0 || !output.Typed || output.Unsigned ||
		!strings.Contains(output.SocketPath, "cc-1700000002-1-3") ||
		!strings.Contains(output.Proof, "sid thread-a") ||
		!strings.Contains(output.Proof, `to reply: call "Codex A" <message>`) {
		t.Fatalf("explicit target call lost target or caller provenance: %+v", output)
	}
}

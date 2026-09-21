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

	"github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/inject"
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
		t.Fatalf("%s returned tool error: %#v", name, result.Content)
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
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TMUX_TMPDIR", jail.tmuxBase)
	t.Setenv(resolve.ClaudeSessionEnv, "")
	t.Setenv(resolve.CodexThreadEnv, "")
	t.Setenv(inject.SenderSessionEnv, "")
	t.Setenv(inject.SenderLabelEnv, "")
	t.Setenv(inject.SenderIDEnv, "")
	t.Setenv(paths.EnvHome, jail.home)
	t.Setenv(paths.EnvDB, jail.database)
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
	testInjector, err := inject.New(inject.Dependencies{
		Resolver: metadataNamedResolver{
			fallback: &service.backend.resolver,
			name:     "Codex B",
			socket:   filepath.Join(jail.tmuxDir, jail.busySocket),
			pane:     "%0",
		},
		Tmux:           inject.TmuxInjector{},
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
	service.backend.injector = testInjector
	t.Cleanup(func() { _ = service.Close() })
	return service
}

// TestMetadataIdentityNormalizesSelfBeforeTheChatVerbs pins that "self" on
// chat_last and chat_status is the REQUEST's caller — resolved from the call's
// metadata before the verb runs, so the verb only ever sees a concrete id.
func TestMetadataIdentityNormalizesSelfBeforeTheChatVerbs(t *testing.T) {
	service := metadataIdentityService(t)
	// The service's verb fake already lists the jailed Codex seats the caller
	// resolves against; the verbs under test answer beside them.
	verbs := service.backend.chat.(*fakeChatVerbs)
	verbs.last = chat.LastResult{Text: "self answer\n"}
	verbs.status = headless.Status{
		Name: "Codex A", State: headless.StateIdle, Engine: pfmengine.Codex, SessionID: "thread-a",
	}
	protocol := connectInMemory(t, service.Server())
	meta := mcp.Meta{"threadId": "thread-a"}
	last := callToolWithMeta[LastOutput](t, protocol.clientSession, "chat_last", meta, LastInput{Target: "self"})
	if last.Target != "self" || last.Text != "self answer" {
		t.Fatalf("chat_last(self) = %+v", last)
	}
	status := callToolWithMeta[StatusOutput](
		t,
		protocol.clientSession,
		"chat_status",
		meta,
		StatusInput{Target: "self"},
	)
	if status.Name != "Codex A" || status.SessionID != "thread-a" {
		t.Fatalf("chat_status(self) = %+v", status)
	}
	if want := []chat.LastRequest{{Target: "thread-a"}}; !reflect.DeepEqual(verbs.lasts, want) {
		t.Fatalf("self Last calls = %+v, want %+v", verbs.lasts, want)
	}
	if want := []chat.StatusRequest{{Target: "thread-a"}}; !reflect.DeepEqual(verbs.statuses, want) {
		t.Fatalf("self Status calls = %+v, want %+v", verbs.statuses, want)
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
		t, protocol.clientSession, "chat_new", mcp.Meta{"threadId": "thread-a"},
		NewInput{Name: "child"},
	)
	if created.Status != "ok" || created.Code != 0 {
		t.Fatalf("request-scoped chat_new = %+v", created)
	}
	want := [][]string{{"chat", "new", "--name", "child", "--cwd", "/work/alpha"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("chat_new dispatch calls = %q, want %q", calls, want)
	}
}

// metadataThenSpawner keeps a valid self-compact call inside the test process.
// CommandThenSpawner deliberately uses os.Executable; under go test that is
// the test binary, so launching it as `internal then` would recursively rerun
// the package instead of exercising the waiter command.
type metadataThenSpawner struct{}

func (metadataThenSpawner) Spawn(context.Context, inject.SteerSpawn) error { return nil }

type metadataNamedResolver struct {
	fallback inject.Resolver
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

type recordingCompactInjector struct {
	scheduled      inject.Request
	scheduledFocus string
	scheduledThen  []string
}

func (recorder *recordingCompactInjector) Resolve(context.Context, string) (inject.Target, int, string, error) {
	return inject.Target{}, 0, "", nil
}

func (recorder *recordingCompactInjector) ResolveEngine(
	context.Context,
	string,
	string,
) (inject.Target, int, string, error) {
	return inject.Target{}, 0, "", nil
}

func (recorder *recordingCompactInjector) Capture(
	context.Context,
	string,
	int,
) (inject.Target, string, int, string, error) {
	return inject.Target{}, "", 0, "", nil
}

func (recorder *recordingCompactInjector) Inject(context.Context, inject.Request) (inject.Result, error) {
	return inject.Result{}, nil
}

func (recorder *recordingCompactInjector) ScheduleAfterCurrentTurn(
	_ context.Context,
	request inject.Request,
) (inject.Result, error) {
	recorder.scheduled = request
	return inject.Result{Status: "scheduled", Code: 0}, nil
}

func (recorder *recordingCompactInjector) ScheduleSelfCompact(
	_ context.Context,
	focus string,
	then []string,
) (inject.Result, error) {
	recorder.scheduledFocus = focus
	recorder.scheduledThen = then
	return inject.Result{Status: "scheduled", Code: 0}, nil
}

// TestMCPMetadataThreadIdentityRoutesDistinctCodexSeats pins the real
// protocol boundary for a long-lived MCP server: each call's _meta.threadId
// selects its own live row for whoami, self capture, and self inject. The
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
			t, clients[index].clientSession, "chat_whoami", metas[index], WhoamiInput{},
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
			t, clients[index].clientSession, "chat_capture", metas[index], CaptureInput{Target: "self"},
		)
		if captured.Status != "ok" || captured.Code != 0 || captured.SocketPath == "" {
			t.Errorf("capture[%d] = %+v, want the caller's live seat", index, captured)
		}

		keyed := callToolWithMeta[KeysOutput](
			t, clients[index].clientSession, "chat_keys", metas[index], KeysInput{
				Target: "self", Keys: []string{"Escape"}, Capture: true,
			},
		)
		if keyed.Status != "ok" || keyed.Code != 0 || keyed.Count != 1 ||
			keyed.SocketPath != captured.SocketPath || keyed.Pane != captured.Pane {
			t.Errorf("keys[%d] = %+v, want the same request-scoped live seat as capture %+v", index, keyed, captured)
		}

		injected := callToolWithMeta[InjectOutput](
			t, clients[index].clientSession, "chat_inject", metas[index], InjectInput{
				Target: "self", Message: "signed from " + metas[index]["threadId"].(string),
			},
		)
		if injected.Code != 0 || !injected.Typed || injected.Unsigned {
			t.Errorf("inject[%d] = %+v, want typed signed delivery", index, injected)
		}
		for _, signaturePart := range []string{
			"sid " + metas[index]["threadId"].(string),
			`to reply: chat_inject "Codex ` + string(rune('A'+index)) + `" <message>`,
		} {
			if !strings.Contains(injected.Proof, signaturePart) {
				t.Errorf(
					"inject[%d] proof %q lacks request-scoped signature part %q",
					index,
					injected.Proof,
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
					t, clients[index].clientSession, "chat_whoami", metas[index], WhoamiInput{},
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
		t, clients[0].clientSession, "chat_whoami", nil, WhoamiInput{},
	)
	if missing.Status != "not_found" || missing.Session != "" || missing.ID != "" {
		t.Fatalf("metadata-free whoami inherited a prior caller: %+v", missing)
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
		t, client.clientSession, "chat_whoami", nil, WhoamiInput{},
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
				t, client.clientSession, "chat_whoami", test.meta, WhoamiInput{},
			)
			if whoami.Status != "not_found" || whoami.Session != "" {
				t.Fatalf("whoami = %+v, want honest not-found", whoami)
			}
			captured := callToolWithMeta[CaptureOutput](
				t, client.clientSession, "chat_capture", test.meta, CaptureInput{Target: "self"},
			)
			if captured.Status != "not_found" || captured.Code == 0 || captured.Text != "" {
				t.Fatalf("capture = %+v, want honest not-found", captured)
			}
			injected := callToolWithMeta[InjectOutput](
				t, client.clientSession, "chat_inject", test.meta, InjectInput{
					Target: "self", Message: "must not impersonate",
				},
			)
			if injected.Code == 0 || injected.Typed || injected.Unsigned {
				t.Fatalf("inject = %+v, want refusal without a signed/typed impersonation", injected)
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
	output := callToolWithMeta[InjectOutput](
		t, client.clientSession, "chat_inject", mcp.Meta{"threadId": "thread-a"},
		InjectInput{Target: "busy-session", Message: "explicit target provenance"},
	)
	if output.Code != 0 || !output.Typed || output.Unsigned ||
		!strings.Contains(output.SocketPath, "cc-1700000002-1-3") ||
		!strings.Contains(output.Proof, "sid thread-a") ||
		!strings.Contains(output.Proof, `to reply: chat_inject "Codex A" <message>`) {
		t.Fatalf("explicit target injection lost target or caller provenance: %+v", output)
	}
}

func TestChatSelfCompactRequiresSteerAndTargetsRequestingSeat(t *testing.T) {
	service := metadataIdentityService(t)
	client := connectInMemory(t, service.Server())
	meta := mcp.Meta{"threadId": "thread-a"}

	steerless := callToolWithMeta[InjectOutput](
		t, client.clientSession, "chat_self_compact", meta,
		SelfCompactInput{Focus: "preserve the active MCP investigation"},
	)
	if steerless.Code != inject.CodeUndelivered || steerless.Typed ||
		!strings.Contains(steerless.Message, "requires exactly one then steer") {
		t.Fatalf("steerless self compact = %+v", steerless)
	}

	recursive := callToolWithMeta[InjectOutput](
		t, client.clientSession, "chat_self_compact", meta,
		SelfCompactInput{
			Focus: "preserve the active MCP investigation",
			Then:  "/compact again",
		},
	)
	if recursive.Code != inject.CodeUndelivered || recursive.Typed ||
		!strings.Contains(recursive.Message, "must not itself start with /compact") {
		t.Fatalf("recursive self compact = %+v", recursive)
	}

	scheduled := callToolWithMeta[InjectOutput](
		t, client.clientSession, "chat_self_compact", meta,
		SelfCompactInput{
			Focus: "preserve the active MCP investigation; drop resolved setup noise",
			Then:  "resume the MCP stress test",
		},
	)
	if scheduled.Code != 0 || scheduled.Status != "scheduled" || scheduled.Typed ||
		scheduled.Steers != 1 || scheduled.Unsigned ||
		scheduled.SocketPath == "" || scheduled.Pane == "" ||
		!strings.Contains(scheduled.Message, "after the current turn settles") {
		t.Fatalf("request-scoped self compact = %+v", scheduled)
	}
}

// TestChatSelfCompactForwardsFocusAndThenToScheduleSelfCompact is the Task D
// regression test, re-pointed: composing "/compact " + focus onto the
// delivered command is now Engine.ScheduleSelfCompact's job
// (internal/inject/engine.go), the one implementation `pfm chat
// self-compact` shares — not chatSelfCompact's. What chatSelfCompact itself
// must still get right is forwarding the validated focus and the ONE steer
// to the injector unmodified, never discarding or recomposing them. Renamed
// from ...ComposesFocusIntoScheduledCommand, which pinned the composition
// here before Task D moved it.
func TestChatSelfCompactForwardsFocusAndThenToScheduleSelfCompact(t *testing.T) {
	recorder := &recordingCompactInjector{}
	service := newService("test", &backend{
		injector:             recorder,
		allowAmbientIdentity: true,
	})
	_, _, err := service.chatSelfCompact(
		context.Background(),
		nil,
		SelfCompactInput{
			Focus: "preserve the signed MCP acceptance verdict",
			Then:  "resume the acceptance test",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := "preserve the signed MCP acceptance verdict"
	if recorder.scheduledFocus != want {
		t.Fatalf("forwarded focus = %q, want %q", recorder.scheduledFocus, want)
	}
	if !reflect.DeepEqual(recorder.scheduledThen, []string{"resume the acceptance test"}) {
		t.Fatalf("forwarded continuation = %q", recorder.scheduledThen)
	}
}

// TestChatSelfCompactValidationRefusesBadFocus pins the validation
// chatSelfCompact keeps as its own tool-call error even though
// Engine.ScheduleSelfCompact validates focus again on the way in — this
// handler must still return a Go error (not an InjectOutput refusal) for a
// bad focus, so the check stays here too. None of these cases may reach the
// injector.
func TestChatSelfCompactValidationRefusesBadFocus(t *testing.T) {
	tests := []struct {
		name  string
		focus string
	}{
		{name: "empty", focus: ""},
		{name: "blank", focus: "   "},
		{name: "multi-line", focus: "line one\nline two"},
		{name: "carriage-return", focus: "line one\rline two"},
		{name: "nul byte", focus: "wave three\x00closeout"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := &recordingCompactInjector{}
			service := newService("test", &backend{
				injector:             recorder,
				allowAmbientIdentity: true,
			})
			_, _, err := service.chatSelfCompact(
				context.Background(),
				nil,
				SelfCompactInput{
					Focus: test.focus,
					Then:  "resume the acceptance test",
				},
			)
			if err == nil || !strings.Contains(err.Error(), "focus must be one non-empty line") {
				t.Fatalf("focus %q error = %v, want the one-non-empty-line refusal", test.focus, err)
			}
			if recorder.scheduledFocus != "" || len(recorder.scheduledThen) != 0 {
				t.Fatalf(
					"invalid focus %q reached the injector: focus=%q then=%q",
					test.focus, recorder.scheduledFocus, recorder.scheduledThen,
				)
			}
		})
	}
}

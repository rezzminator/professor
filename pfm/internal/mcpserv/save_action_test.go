package mcpserv

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// TestChatSaveRefusesATargetWithNoPathSeparator is the F7 regression:
// chat_save's target is a FILE PATH to append a transcript snapshot to, not a
// chat — but nothing stopped a caller from passing a bare chat name out of
// habit, which created a file of that name beside wherever the MCP server
// process happened to be running and appended a whole transcript to it (this
// is exactly how "mcpaudit-b" became a stray 142KB file in the operator's
// home directory). The fix refuses any target without a path separator
// before it ever reaches the dispatcher.
func TestChatSaveRefusesATargetWithNoPathSeparator(t *testing.T) {
	dispatched := false
	service := &Service{backend: &backend{
		dispatch: func(context.Context, []string, io.Writer, io.Writer) int {
			dispatched = true
			return 0
		},
	}}

	if _, _, err := service.chatSave(
		context.Background(), nil, SaveInput{Target: "mcpaudit-b"},
	); err == nil || !strings.Contains(err.Error(), "is not a file path") {
		t.Fatalf("chatSave error = %v, want a not-a-file-path refusal", err)
	}
	if dispatched {
		t.Fatal("chatSave dispatched a bare chat name instead of refusing it")
	}
}

// TestChatSaveDispatchesAPathShapedTarget is the companion positive case: a
// target that DOES contain a path separator is still accepted and reaches
// the dispatcher unchanged, transcript argument included when supplied.
func TestChatSaveDispatchesAPathShapedTarget(t *testing.T) {
	var calls [][]string
	service := &Service{backend: &backend{
		allowAmbientIdentity: true,
		dispatch: func(_ context.Context, args []string, _, _ io.Writer) int {
			calls = append(calls, append([]string(nil), args...))
			return 0
		},
	}}

	if _, _, err := service.chatSave(
		context.Background(), nil,
		SaveInput{Target: "./notes/mcpaudit-b.md", Transcript: "/jailed/transcript.jsonl"},
	); err != nil {
		t.Fatalf("chatSave: %v", err)
	}
	want := [][]string{{"chat", "save", "./notes/mcpaudit-b.md", "/jailed/transcript.jsonl"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("dispatch calls = %q, want %q", calls, want)
	}
}

func TestChatSaveResolvesCallerRelativePathsAndScopesRepository(t *testing.T) {
	callerCWD := filepath.Join(t.TempDir(), "caller")
	row := compose.Row{
		Kind: compose.LiveClaude, ID: "caller-id", CWD: callerCWD,
		SessionName: "cc-caller", Socket: "cc-caller", PaneID: "%1",
	}
	var calls [][]string
	var scoped []string
	service := newService("test", &backend{
		chat: &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}},
		dispatch: func(ctx context.Context, args []string, _, _ io.Writer) int {
			calls = append(calls, append([]string(nil), args...))
			self, ok := chat.ScopedSelf(ctx)
			if !ok {
				t.Fatal("dispatch context has no request-scoped caller")
			}
			scoped = append(scoped, self.CWD)
			return 0
		},
	})
	request := saveProxyRequest("cc-caller", "caller-id")

	if _, _, err := service.chatSave(context.Background(), request, SaveInput{
		Target: "./notes/chat.md", Transcript: "./transcripts/chat.jsonl",
	}); err != nil {
		t.Fatalf("chatSave: %v", err)
	}
	wantCalls := [][]string{{
		"chat", "save",
		filepath.Join(callerCWD, "notes", "chat.md"),
		filepath.Join(callerCWD, "transcripts", "chat.jsonl"),
	}}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("dispatch calls = %q, want caller-relative paths %q", calls, wantCalls)
	}
	if want := []string{callerCWD}; !reflect.DeepEqual(scoped, want) {
		t.Fatalf("scoped caller CWDs = %q, want %q", scoped, want)
	}
}

func TestChatSaveDefaultsToExactIndexedCallerTranscript(t *testing.T) {
	setupBackendFixture(t)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	})
	callerCWD := filepath.Join(t.TempDir(), "caller")
	transcriptPath := filepath.Join(t.TempDir(), "indexed", "caller.jsonl")
	if err := database.UpsertTranscript(context.Background(), store.Transcript{
		UUID: "caller-id", Path: transcriptPath, CWD: callerCWD,
	}); err != nil {
		t.Fatal(err)
	}
	row := compose.Row{
		Kind: compose.LiveClaude, ID: "caller-id", CWD: callerCWD,
		SessionName: "cc-caller", Socket: "cc-caller", PaneID: "%1",
	}
	var calls [][]string
	service := newService("test", &backend{
		database: database,
		chat:     &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}},
		dispatch: func(_ context.Context, args []string, _, _ io.Writer) int {
			calls = append(calls, append([]string(nil), args...))
			return 0
		},
	})

	if _, _, err := service.chatSave(
		context.Background(), saveProxyRequest("cc-caller", "caller-id"), SaveInput{Target: "./notes/chat.md"},
	); err != nil {
		t.Fatalf("chatSave: %v", err)
	}
	want := [][]string{{"chat", "save", filepath.Join(callerCWD, "notes", "chat.md"), transcriptPath}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("dispatch calls = %q, want exact indexed transcript %q", calls, want)
	}
}

func TestChatSaveKeepsExplicitAbsolutePathsWithInvalidCaller(t *testing.T) {
	target := filepath.Join(t.TempDir(), "notes", "chat.md")
	transcriptPath := filepath.Join(t.TempDir(), "transcripts", "chat.jsonl")
	var calls [][]string
	service := newService("test", &backend{
		chat: &fakeChatVerbs{},
		dispatch: func(_ context.Context, args []string, _, _ io.Writer) int {
			calls = append(calls, append([]string(nil), args...))
			return 0
		},
	})
	request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{"threadId": "missing"}}}

	if _, _, err := service.chatSave(context.Background(), request, SaveInput{
		Target: target, Transcript: transcriptPath,
	}); err != nil {
		t.Fatalf("chatSave: %v", err)
	}
	want := [][]string{{"chat", "save", target, transcriptPath}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("dispatch calls = %q, want byte-preserved absolute paths %q", calls, want)
	}
}

func TestChatSaveKeepsExplicitAbsolutePathSpellingForValidCaller(t *testing.T) {
	root := t.TempDir()
	target := root + "/notes/../chat.md"
	transcriptPath := root + "/transcripts/../chat.jsonl"
	row := compose.Row{
		Kind: compose.LiveClaude, ID: "caller-id", CWD: "/caller",
		SessionName: "cc-caller", Socket: "cc-caller", PaneID: "%1",
	}
	var calls [][]string
	service := newService("test", &backend{
		chat: &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}},
		dispatch: func(_ context.Context, args []string, _, _ io.Writer) int {
			calls = append(calls, append([]string(nil), args...))
			return 0
		},
	})

	if _, _, err := service.chatSave(
		context.Background(), saveProxyRequest("cc-caller", "caller-id"),
		SaveInput{Target: target, Transcript: transcriptPath},
	); err != nil {
		t.Fatalf("chatSave: %v", err)
	}
	want := [][]string{{"chat", "save", target, transcriptPath}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("dispatch calls = %q, want byte-preserved absolute paths %q", calls, want)
	}
}

func TestChatSaveKeepsAmbientRelativeArgumentsAndDefaultInference(t *testing.T) {
	var calls [][]string
	service := newService("test", &backend{
		allowAmbientIdentity: true,
		dispatch: func(_ context.Context, args []string, _, _ io.Writer) int {
			calls = append(calls, append([]string(nil), args...))
			return 0
		},
	})

	if _, _, err := service.chatSave(
		context.Background(), nil, SaveInput{Target: "./notes/chat.md"},
	); err != nil {
		t.Fatalf("chatSave: %v", err)
	}
	want := [][]string{{"chat", "save", "./notes/chat.md"}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("dispatch calls = %q, want ambient argv %q", calls, want)
	}
}

func TestChatSaveRefusesCallerDependentPathsWithoutUsableCallerContext(t *testing.T) {
	tests := []struct {
		name        string
		row         compose.Row
		request     *mcp.CallToolRequest
		input       SaveInput
		wantMessage string
	}{
		{
			name:        "invalid caller metadata",
			request:     &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{"threadId": "missing"}}},
			input:       SaveInput{Target: "./notes/chat.md", Transcript: "/transcripts/chat.jsonl"},
			wantMessage: "has no live Codex tmux seat",
		},
		{
			name: "missing caller cwd",
			row: compose.Row{
				Kind: compose.LiveClaude, ID: "caller-id", SessionName: "cc-caller",
				Socket: "cc-caller", PaneID: "%1",
			},
			request:     saveProxyRequest("cc-caller", "caller-id"),
			input:       SaveInput{Target: "./notes/chat.md", Transcript: "/transcripts/chat.jsonl"},
			wantMessage: "working directory is required",
		},
		{
			name: "nonabsolute caller cwd",
			row: compose.Row{
				Kind: compose.LiveClaude, ID: "caller-id", CWD: "relative/caller", SessionName: "cc-caller",
				Socket: "cc-caller", PaneID: "%1",
			},
			request:     saveProxyRequest("cc-caller", "caller-id"),
			input:       SaveInput{Target: "./notes/chat.md", Transcript: "/transcripts/chat.jsonl"},
			wantMessage: "is not absolute",
		},
		{
			name: "missing indexed transcript",
			row: compose.Row{
				Kind: compose.LiveClaude, ID: "caller-id", CWD: "/caller", SessionName: "cc-caller",
				Socket: "cc-caller", PaneID: "%1",
			},
			request:     saveProxyRequest("cc-caller", "caller-id"),
			input:       SaveInput{Target: "/notes/chat.md"},
			wantMessage: "transcript path is required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dispatched := false
			rows := []compose.Row(nil)
			if test.row.Kind != 0 {
				rows = append(rows, test.row)
			}
			service := newService("test", &backend{
				chat: &fakeChatVerbs{listed: chat.ListResult{Rows: rows, Matched: len(rows)}},
				dispatch: func(context.Context, []string, io.Writer, io.Writer) int {
					dispatched = true
					return 0
				},
			})
			_, _, err := service.chatSave(context.Background(), test.request, test.input)
			if err == nil || !strings.Contains(err.Error(), test.wantMessage) {
				t.Fatalf("chatSave error = %v, want %q", err, test.wantMessage)
			}
			if dispatched {
				t.Fatal("chatSave dispatched an invalid caller-dependent request")
			}
		})
	}
}

func TestChatSaveKeepsInterleavedCallerContextsIsolated(t *testing.T) {
	rows := []compose.Row{
		{
			Kind: compose.LiveClaude, ID: "alpha", CWD: "/work/alpha",
			SessionName: "cc-alpha", Socket: "cc-alpha", PaneID: "%1",
		},
		{
			Kind: compose.LiveClaude, ID: "beta", CWD: "/work/beta",
			SessionName: "cc-beta", Socket: "cc-beta", PaneID: "%1",
		},
	}
	var calls [][]string
	var scopedCWDs []string
	service := newService("test", &backend{
		chat: &fakeChatVerbs{listed: chat.ListResult{Rows: rows, Matched: len(rows)}},
		dispatch: func(ctx context.Context, args []string, _, _ io.Writer) int {
			calls = append(calls, append([]string(nil), args...))
			self, ok := chat.ScopedSelf(ctx)
			if !ok {
				t.Fatal("dispatch context has no request-scoped caller")
			}
			scopedCWDs = append(scopedCWDs, self.CWD)
			return 0
		},
	})
	for _, caller := range []string{"alpha", "beta", "alpha"} {
		if _, _, err := service.chatSave(
			context.Background(), saveProxyRequest("cc-"+caller, caller),
			SaveInput{Target: "./notes/chat.md", Transcript: "./transcripts/chat.jsonl"},
		); err != nil {
			t.Fatalf("chatSave(%s): %v", caller, err)
		}
	}
	wantCalls := [][]string{
		{"chat", "save", "/work/alpha/notes/chat.md", "/work/alpha/transcripts/chat.jsonl"},
		{"chat", "save", "/work/beta/notes/chat.md", "/work/beta/transcripts/chat.jsonl"},
		{"chat", "save", "/work/alpha/notes/chat.md", "/work/alpha/transcripts/chat.jsonl"},
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("dispatch calls = %q, want isolated calls %q", calls, wantCalls)
	}
	wantCWDs := []string{"/work/alpha", "/work/beta", "/work/alpha"}
	if !reflect.DeepEqual(scopedCWDs, wantCWDs) {
		t.Fatalf("scoped caller CWDs = %q, want %q", scopedCWDs, wantCWDs)
	}
}

func TestChatSaveNamesCallerListingAndTranscriptLookupFailures(t *testing.T) {
	t.Run("listing", func(t *testing.T) {
		service := newService("test", &backend{chat: &fakeChatVerbs{err: errors.New("list failed")}})
		request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{"threadId": "caller-id"}}}
		_, _, err := service.chatSave(context.Background(), request, SaveInput{
			Target: "./notes/chat.md", Transcript: "/transcripts/chat.jsonl",
		})
		if err == nil || !strings.Contains(err.Error(), "chat_save: resolve caller") ||
			!strings.Contains(err.Error(), "list live chats") {
			t.Fatalf("chatSave listing error = %v, want wrapped caller listing failure", err)
		}
	})

	t.Run("transcript", func(t *testing.T) {
		setupBackendFixture(t)
		database, err := store.Open()
		if err != nil {
			t.Fatal(err)
		}
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
		row := compose.Row{
			Kind: compose.LiveClaude, ID: "caller-id", CWD: "/caller",
			SessionName: "cc-caller", Socket: "cc-caller", PaneID: "%1",
		}
		service := newService("test", &backend{
			database: database,
			chat:     &fakeChatVerbs{listed: chat.ListResult{Rows: []compose.Row{row}, Matched: 1}},
		})
		_, _, err = service.chatSave(
			context.Background(), saveProxyRequest("cc-caller", "caller-id"),
			SaveInput{Target: "/notes/chat.md", Transcript: "/transcripts/chat.jsonl"},
		)
		if err == nil || !strings.Contains(err.Error(), "chat_save: resolve caller") ||
			!strings.Contains(err.Error(), "resolve MCP self transcript") {
			t.Fatalf("chatSave transcript error = %v, want wrapped transcript lookup failure", err)
		}
	})
}

func saveProxyRequest(session, id string) *mcp.CallToolRequest {
	return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{
		"pfmProxy": map[string]any{
			"v": ProxyWireVersion, "session": session, "socketName": session,
			"socketPath": "/tmp/tmux/" + session, "pane": "%1", "engine": string(pfmengine.Claude), "id": id,
		},
	}}}
}

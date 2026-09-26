package obs

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolInput is the shape the chat tools share: a target plus a body the log
// must never carry.
type toolInput struct {
	Target  string   `json:"target"`
	Message string   `json:"message"`
	Then    []string `json:"then,omitempty"`
}

type toolOutput struct {
	Status string `json:"status"`
}

// connectInProcess serves server over an in-memory transport and returns a
// connected client session; both close with t.
func connectInProcess(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(context.Background(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := serverSession.Close(); err != nil {
			t.Errorf("close server session: %v", err)
		}
	})
	client := mcp.NewClient(&mcp.Implementation{Name: "fixture", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Errorf("close client session: %v", err)
		}
	})
	return session
}

func TestToolRecordsNameTargetArgumentShapeResultShapeAndDurationNeverAValue(t *testing.T) {
	ctx, recorder := Test(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "test"}, nil)
	var seen toolInput
	mcp.AddTool(server, &mcp.Tool{Name: "chat_inject"}, Tool("chat_inject",
		func(_ context.Context, _ *mcp.CallToolRequest, input toolInput) (*mcp.CallToolResult, toolOutput, error) {
			seen = input
			return nil, toolOutput{Status: "delivered"}, nil
		},
	))
	session := connectInProcess(t, server)
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "chat_inject",
		Arguments: map[string]any{
			"target": "cc-3", "message": "Bearer sk-PROMPTSECRET body", "then": []string{"/next"},
		},
	})
	if err != nil || result.IsError {
		t.Fatalf("call = %v, %v", result, err)
	}
	if seen.Message != "Bearer sk-PROMPTSECRET body" {
		t.Fatalf("the wrapper changed the input: %+v", seen)
	}

	record := onlyRecord(t, recorder)
	if record.Message != "mcp.call" || record.Level != slog.LevelInfo.String() {
		t.Fatalf("record = %s %s, want INFO mcp.call", record.Level, record.Message)
	}
	wantField(t, record, FieldComp, "mcp")
	wantField(t, record, "op", "call")
	wantField(t, record, "kind", "tool")
	wantField(t, record, "tool", "chat_inject")
	wantField(t, record, "target", "cc-3")
	wantField(t, record, "args", "target:4,message:27,then:9")
	wantField(t, record, "bytes", float64(len(`{"status":"delivered"}`)))
	if _, found := record.Field(FieldDur); !found {
		t.Fatalf("record carries no %s: %v", FieldDur, record.Fields)
	}
	for _, secret := range []string{"PROMPTSECRET", "delivered", "/next"} {
		if strings.Contains(recorder.Raw(), secret) {
			t.Fatalf("a value (%q) reached the activity log: %s", secret, recorder.Raw())
		}
	}
}

func TestToolRecordsAHandlerErrorAtErrorLevelCappedAtTheFieldLimit(t *testing.T) {
	ctx, recorder := Test(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "test"}, nil)
	long := strings.Repeat("e", MaxValueBytes+100)
	mcp.AddTool(server, &mcp.Tool{Name: "chat_last"}, Tool("chat_last",
		func(context.Context, *mcp.CallToolRequest, toolInput) (*mcp.CallToolResult, any, error) {
			return nil, nil, errors.New(long)
		},
	))
	session := connectInProcess(t, server)
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "chat_last", Arguments: map[string]any{"target": "x", "message": ""},
	})
	if err != nil || !result.IsError {
		t.Fatalf("a handler error did not become a tool error: %v, %v", result, err)
	}
	record := onlyRecord(t, recorder)
	if record.Level != slog.LevelError.String() {
		t.Fatalf("a failed tool logged at %s, want ERROR", record.Level)
	}
	wantField(t, record, "args", "target:1,message:0,then:4")
	got, _ := record.Field(FieldErr)
	text, _ := got.(string)
	if !strings.HasSuffix(text, "…(truncated 100 bytes)") {
		t.Fatalf("err was not cut at the cap: len=%d", len(text))
	}
	if _, found := record.Field("bytes"); found {
		t.Fatalf("a failed call carries a result size: %v", record.Fields)
	}
}

func TestToolWarnsWhenTheResultItselfIsAnError(t *testing.T) {
	ctx, recorder := Test(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "test"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "chat_status"}, Tool("chat_status",
		func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "dead"}}}, nil, nil
		},
	))
	session := connectInProcess(t, server)
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "chat_status", Arguments: map[string]any{"target": "cc-9", "ask": true},
	}); err != nil {
		t.Fatal(err)
	}
	record := onlyRecord(t, recorder)
	if record.Level != slog.LevelWarn.String() {
		t.Fatalf("an IsError result logged at %s, want WARN", record.Level)
	}
	wantField(t, record, "args", "ask:4,target:4")
	wantField(t, record, "target", "cc-9")
	wantField(t, record, FieldErr, "tool result IsError")
}

func TestPromptRecordsKindPromptWithTheArgumentShape(t *testing.T) {
	ctx, recorder := Test(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "test"}, nil)
	server.AddPrompt(
		&mcp.Prompt{Name: "fetch", Arguments: []*mcp.PromptArgument{{Name: "url", Required: true}}},
		Prompt("fetch", func(_ context.Context, request *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{
				Role: "user", Content: &mcp.TextContent{Text: "fetched " + request.Params.Arguments["url"]},
			}}}, nil
		}),
	)
	session := connectInProcess(t, server)
	if _, err := session.GetPrompt(ctx, &mcp.GetPromptParams{
		Name: "fetch", Arguments: map[string]string{"url": "https://h.example.test/?key=PROMPTKEY"},
	}); err != nil {
		t.Fatal(err)
	}
	record := onlyRecord(t, recorder)
	if record.Message != "mcp.call" || record.Level != slog.LevelInfo.String() {
		t.Fatalf("record = %s %s, want INFO mcp.call", record.Level, record.Message)
	}
	wantField(t, record, FieldComp, "mcp")
	wantField(t, record, "kind", "prompt")
	wantField(t, record, "tool", "fetch")
	wantField(t, record, "args", "url:37")
	if got, _ := record.Field("bytes"); got == nil || got.(float64) <= 0 {
		t.Fatalf("a prompt result has no size: %v", record.Fields)
	}
	if strings.Contains(recorder.Raw(), "PROMPTKEY") {
		t.Fatalf("the prompt argument reached the activity log: %s", recorder.Raw())
	}
}

func TestPromptRecordsAFailureAtErrorLevel(t *testing.T) {
	ctx, recorder := Test(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "test"}, nil)
	server.AddPrompt(&mcp.Prompt{Name: "fetch"}, Prompt("fetch",
		func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return nil, errors.New("URL is required")
		},
	))
	session := connectInProcess(t, server)
	if _, err := session.GetPrompt(ctx, &mcp.GetPromptParams{Name: "fetch"}); err == nil {
		t.Fatal("the prompt error vanished")
	}
	record := onlyRecord(t, recorder)
	if record.Level != slog.LevelError.String() {
		t.Fatalf("a failed prompt logged at %s, want ERROR", record.Level)
	}
	wantField(t, record, FieldErr, "URL is required")
	wantField(t, record, "args", "")
}

// TestToolRecoversAPanicIntoAnErrorRecordAndSurvives is L2-F1: a panicking
// handler must not kill the process that serves every chat, the mcp.call
// record must still be written (naming the tool), and the SDK must see an
// error rather than a dropped connection.
func TestToolRecoversAPanicIntoAnErrorRecordAndSurvives(t *testing.T) {
	ctx, recorder := Test(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "test"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "chat_boom"}, Tool("chat_boom",
		func(context.Context, *mcp.CallToolRequest, toolInput) (*mcp.CallToolResult, toolOutput, error) {
			var nilInput *toolInput
			return nil, toolOutput{}, errors.New(nilInput.Target) // nil pointer deref panic
		},
	))
	session := connectInProcess(t, server)
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "chat_boom", Arguments: map[string]any{"target": "cc-3", "message": "hi"},
	})
	if err != nil {
		t.Fatalf("the panic reached the client as a transport error instead of a tool error: %v", err)
	}
	if result == nil || !result.IsError {
		t.Fatalf("a panicking handler did not become a tool error: %+v", result)
	}
	record := onlyRecord(t, recorder)
	if record.Level != slog.LevelError.String() || record.Message != "mcp.call" {
		t.Fatalf("record = %s %s, want ERROR mcp.call", record.Level, record.Message)
	}
	wantField(t, record, "tool", "chat_boom")
	got, _ := record.Field(FieldErr)
	text, _ := got.(string)
	if !strings.Contains(text, "chat_boom") || !strings.Contains(text, "nil pointer") {
		t.Fatalf("panic record err = %q, want it to name the tool and the panic", text)
	}
	// A second, ordinary call on the SAME server proves the process — and the
	// server — survived the panic rather than being torn down with it.
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "chat_boom", Arguments: map[string]any{"target": "cc-4", "message": "still alive"},
	}); err != nil {
		t.Fatalf("the server did not survive the panic: %v", err)
	}
}

// TestPromptRecoversAPanicIntoAnErrorRecord is Tool's sibling test for
// Prompt's own recover.
func TestPromptRecoversAPanicIntoAnErrorRecord(t *testing.T) {
	ctx, recorder := Test(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "test"}, nil)
	server.AddPrompt(&mcp.Prompt{Name: "boom"}, Prompt("boom",
		func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			var messages []*mcp.PromptMessage
			return &mcp.GetPromptResult{Messages: messages}, errors.New(string(messages[0].Role)) // index out of range
		},
	))
	session := connectInProcess(t, server)
	if _, err := session.GetPrompt(ctx, &mcp.GetPromptParams{Name: "boom"}); err == nil {
		t.Fatal("the panic vanished instead of becoming a prompt error")
	}
	record := onlyRecord(t, recorder)
	if record.Level != slog.LevelError.String() {
		t.Fatalf("record level = %s, want ERROR", record.Level)
	}
	wantField(t, record, "tool", "boom")
	got, _ := record.Field(FieldErr)
	text, _ := got.(string)
	if !strings.Contains(text, "boom") {
		t.Fatalf("panic record err = %q, want it to name the prompt", text)
	}
}

// TestToolNeverRecordsChatSaveTargetAsAChat is L2-F26: chat_save's Target is
// a FILE PATH, not a chat — it must never land in the record's target field
// (the field `pfm log --chat` filters on), only its SIZE through args' shape.
func TestToolNeverRecordsChatSaveTargetAsAChat(t *testing.T) {
	ctx, recorder := Test(t)
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "test"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "chat_save"}, Tool("chat_save",
		func(_ context.Context, _ *mcp.CallToolRequest, _ toolInput) (*mcp.CallToolResult, toolOutput, error) {
			return nil, toolOutput{Status: "saved"}, nil
		},
	))
	session := connectInProcess(t, server)
	const path = "./notes/private-session-2026.md"
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "chat_save", Arguments: map[string]any{"target": path, "message": ""},
	}); err != nil {
		t.Fatal(err)
	}
	record := onlyRecord(t, recorder)
	if _, found := record.Field("target"); found {
		t.Fatalf("chat_save's file path was recorded as a target: %v", record.Fields)
	}
	wantField(t, record, "args", "target:"+strconv.Itoa(len(path))+",message:0,then:4")
	if strings.Contains(recorder.Raw(), "private-session") {
		t.Fatalf("chat_save's file path leaked into the activity log: %s", recorder.Raw())
	}
}

func TestArgumentShapeCoversStructsPointersMapsAndScalars(t *testing.T) {
	type nested struct {
		Chat  string `json:"chat"`
		Count int    `json:"count,omitempty"`
		skip  bool
	}
	for _, tc := range []struct {
		name  string
		value any
		want  string
	}{
		{"struct with tags", nested{Chat: "cc-1", Count: 12, skip: true}, "chat:4,count:2"},
		{"pointer to struct", &nested{Chat: "x"}, "chat:1,count:1"},
		{"nil pointer", (*nested)(nil), ""},
		{"map sorted by key", map[string]any{"b": "two", "a": 1}, "a:1,b:3"},
		{"scalar", "just a string", "bytes:13"},
		{"nil", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := argumentShape(tc.value); got != tc.want {
				t.Fatalf("argumentShape(%v) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
	if got := targetOf(nested{Chat: "cc-1"}); got != "cc-1" {
		t.Fatalf("targetOf found %q through Chat, want cc-1", got)
	}
	if got := targetOf(map[string]any{"target": "m"}); got != "m" {
		t.Fatalf("targetOf on a map = %q, want m", got)
	}
	if got := targetOf(42); got != "" {
		t.Fatalf("targetOf on a scalar = %q, want empty", got)
	}
}

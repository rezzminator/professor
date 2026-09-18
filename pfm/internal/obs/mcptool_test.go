package obs

import (
	"context"
	"errors"
	"log/slog"
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

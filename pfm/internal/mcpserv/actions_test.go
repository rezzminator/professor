package mcpserv

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"hostops/pfm/internal/chat"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/headless"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/transcript"
)

// fakeChatVerbs is the MCP tests' stand-in for chat.Verbs: it records every
// typed call and answers from its fields.
type fakeChatVerbs struct {
	lasts    []chat.LastRequest
	statuses []chat.StatusRequest
	lists    []chat.ListRequest
	finds    []chat.FindRequest
	reads    []string
	last     chat.LastResult
	status   headless.Status
	listed   chat.ListResult
	found    []chat.TranscriptMatch
	read     []transcript.Entry
	err      error
}

func (fake *fakeChatVerbs) Last(_ context.Context, request chat.LastRequest) (chat.LastResult, error) {
	fake.lasts = append(fake.lasts, request)
	return fake.last, fake.err
}

func (fake *fakeChatVerbs) Status(_ context.Context, request chat.StatusRequest) (headless.Status, error) {
	fake.statuses = append(fake.statuses, request)
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

package chat

import (
	"context"
	"errors"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestLastReadsTheNewestAnswerPastLaterToolCalls pins what `last` means: the
// newest thing the chat SAID, however many tool calls came after it.
func TestLastReadsTheNewestAnswerPastLaterToolCalls(t *testing.T) {
	root := testjail.Fleet(t)
	const id = "a1111111-1111-4111-8111-111111111111"
	seedClaudeChat(
		t,
		root,
		id,
		assistantSaid("first answer"),
		assistantSaid("tests are green"),
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}`,
	)
	result, err := LastAnswer(context.Background(), nil, LastRequest{Target: id})
	if err != nil {
		t.Fatalf("Last() error = %v", err)
	}
	if result.Text != "tests are green" || result.Chat.ID != id {
		t.Fatalf("Last() = %+v, want the newest assistant text of %s", result, id)
	}
}

// TestLastNamesAChatThatHasNotAnswered pins the distinct answer for a chat
// that exists but never spoke: ErrNoAnswer carrying the resolved chat — not a
// target failure, and never an empty success.
func TestLastNamesAChatThatHasNotAnswered(t *testing.T) {
	root := testjail.Fleet(t)
	const id = "b2222222-2222-4222-8222-222222222222"
	seedClaudeChat(t, root, id)
	result, err := LastAnswer(context.Background(), nil, LastRequest{Target: id})
	var target *TargetError
	if !errors.Is(err, ErrNoAnswer) || errors.As(err, &target) {
		t.Fatalf("Last() error = %v, want ErrNoAnswer and no *TargetError", err)
	}
	if result.Chat.ID != id || result.Text != "" {
		t.Fatalf("Last() = %+v, want the resolved chat and no text", result)
	}
}

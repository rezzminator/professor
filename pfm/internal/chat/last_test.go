package chat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// seedCodexRollout writes a resumable Codex rollout (seedCodexThread) and
// appends records to it.
func seedCodexRollout(t *testing.T, root, id string, records ...string) {
	t.Helper()
	seedCodexThread(t, root, id)
	rollout := filepath.Join(root, "codex", "sessions", "2030", "01", "02",
		"rollout-2030-01-02T03-04-05-"+id+".jsonl")
	file, err := os.OpenFile(rollout, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(strings.Join(records, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

const (
	codexTaskStarted  = `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-0002"}}`
	codexTaskComplete = `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-0002","last_agent_message":null}}`
	codexToolCall     = `{"type":"response_item","payload":{"type":"function_call","name":"shell","arguments":"{}"}}`
)

func codexSaid(text string) string {
	return `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` +
		text + `"}]}}`
}

// Codex writes commentary between the tool calls of one turn, so while its
// rollout's newest turn is open what it said last is not an answer — and an
// older turn's answer is not this turn's: an orchestrator would read round
// 1's DONE as round 2's. Only a task_complete or turn_aborted ends the turn.
func TestLastHoldsAnOpenCodexTurnAsInProgress(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		id      string
		records []string
		want    string
		wantErr error
	}{
		{
			name: "commentary inside an open turn",
			id:   "019f0000-0000-7000-8000-0000000000b1",
			records: []string{
				codexSaid("DONE round one"), codexTaskStarted,
				codexToolCall, codexSaid("checking the next file"),
			},
			wantErr: ErrTurnInProgress,
		},
		{
			name: "a turn ended by task_complete",
			id:   "019f0000-0000-7000-8000-0000000000b2",
			records: []string{
				codexTaskStarted, codexToolCall, codexSaid("DONE round two"), codexTaskComplete,
			},
			want: "DONE round two",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := testjail.Fleet(t)
			seedCodexRollout(t, root, testCase.id, testCase.records...)
			result, err := LastAnswer(context.Background(), nil, LastRequest{Target: testCase.id})
			if testCase.wantErr != nil {
				if !errors.Is(err, testCase.wantErr) || errors.Is(err, ErrNoAnswer) || result.Text != "" {
					t.Fatalf("Last() = (%q, %v), want %v and no text", result.Text, err, testCase.wantErr)
				}
				if want := `chat "` + result.Chat.Name + `" is mid-turn: no final answer yet`; err.Error() != want {
					t.Fatalf("Last() error = %q, want %q", err, want)
				}
				return
			}
			if err != nil || result.Text != testCase.want {
				t.Fatalf("Last() = (%q, %v), want %q", result.Text, err, testCase.want)
			}
		})
	}
}

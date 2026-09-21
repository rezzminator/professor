package chat

import (
	"context"
	"fmt"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// lastWindow is how many trailing entries Last searches for the newest
// assistant turn: wide enough to reach past a long run of tool calls.
const lastWindow = 200

// LastRequest names the chat whose newest answer to read.
type LastRequest struct {
	Target string
}

// LastResult is the newest assistant turn — the last thing the chat SAID,
// however many tool calls have happened since.
type LastResult struct {
	Chat headless.Chat
	Text string
}

// Last reads the target's newest assistant turn. A chat with no transcript
// yet is ErrNoTranscript and one that has not answered is ErrNoAnswer; both
// carry the resolved chat in the result.
func LastAnswer(ctx context.Context, runtime *pfmconfig.Runtime, request LastRequest) (LastResult, error) {
	target, err := Target(ctx, request.Target, runtime)
	if err != nil {
		return LastResult{}, err
	}
	if target.Path == "" {
		return LastResult{Chat: target}, fmt.Errorf("%w: %q", ErrNoTranscript, target.Name)
	}
	entries, _, err := transcript.Tail(ctx, target.Path, string(target.Engine), lastWindow, 0)
	if err != nil {
		return LastResult{Chat: target}, err
	}
	entry, found := transcript.Last(entries, transcript.RoleAssistant)
	if !found {
		return LastResult{Chat: target}, fmt.Errorf("%w: %q", ErrNoAnswer, target.Name)
	}
	return LastResult{Chat: target, Text: entry.Text}, nil
}

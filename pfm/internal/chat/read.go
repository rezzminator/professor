package chat

import (
	"context"
	"fmt"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// ReadEntries is the transcript tail of one chat, newest last: the shared
// extraction behind `pfm chat read` and MCP chat_read, so their defaults and
// failures are identical. truncated reports a tail cut by transcript.Tail's
// own window.
func ReadEntries(
	ctx context.Context,
	target string,
	tail int,
	runtime *pfmconfig.Runtime,
) (headless.Chat, []transcript.Entry, bool, error) {
	found, err := Target(ctx, target, runtime)
	if err != nil {
		return headless.Chat{}, nil, false, err
	}
	if found.Path == "" {
		return found, nil, false, fmt.Errorf("%w: %q", ErrNoTranscript, found.Name)
	}
	entries, truncated, err := transcript.Tail(ctx, found.Path, string(found.Engine), tail, 0)
	if err != nil {
		return found, nil, false, err
	}
	return found, entries, truncated, nil
}

package headless

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

type exchangeSnapshot struct {
	prompt   []transcript.Entry
	response []transcript.Entry
	offset   int64
	complete bool
}

func readLatestExchange(ctx context.Context, chat Chat) (exchangeSnapshot, bool, error) {
	entries, offset, err := transcript.From(ctx, chat.Path, string(chat.Engine), 0)
	if err != nil {
		return exchangeSnapshot{}, false, err
	}
	prompt, response, ok := transcript.LastExchange(entries)
	if !ok {
		return exchangeSnapshot{}, false, nil
	}
	return exchangeSnapshot{
		prompt:   prompt,
		response: response,
		offset:   offset,
		complete: len(entries) != 0 && assistantAnswered(entries[len(entries)-1].Role),
	}, true, nil
}

func writePreparedExchange(directory string, prompt, response []transcript.Entry, complete bool) (string, error) {
	var content strings.Builder
	content.WriteString("PROMPT\n")
	for _, entry := range prompt {
		content.WriteString(transcript.Condensed(entry))
		content.WriteByte('\n')
	}
	content.WriteString("RESPONSE\n")
	for _, entry := range response {
		content.WriteString(transcript.Condensed(entry))
		content.WriteByte('\n')
	}
	if complete {
		content.WriteString("STATE: COMPLETE\n")
	} else {
		content.WriteString("STATE: PARTIAL\n")
	}
	return writePreparedFile(directory, "exchange", paths.SIDExchangeScratchPattern, content.String())
}

// preparedScratchDir resolves the directory writePreparedExchange and
// writePreparedCapture use for their disposable per-run files (a prepared
// exchange, a live pane capture) when the caller supplies none — as
// options.TempDir is in every real chat-status command, only tests inject a
// directory directly.
//
// It goes through internal/paths — the same package cmd/pfm's launch command
// uses for its own disposable status file (SIDDir, via os.CreateTemp) — so
// the PFM_SID_DIR jail override the rest of the fleet honors keeps working
// here too. A bare filepath.Join("tmp", "chat-status") would instead resolve
// CWD-relative: whatever directory the operator happens to be standing in
// when they run a read-only status command gets a tmp/ dropped into it.
//
// A path that fails to resolve is an error, never a second-choice guess —
// silently falling back to something else is how the CWD bug came to exist.
func preparedScratchDir(directory string) (string, error) {
	if directory != "" {
		return directory, nil
	}
	resolved, err := paths.Resolve()
	if err != nil {
		return "", fmt.Errorf("resolve scratch directory: %w", err)
	}
	return resolved.SIDDir, nil
}

func writePreparedFile(directory, description, pattern, content string) (string, error) {
	directory, err := preparedScratchDir(directory)
	if err != nil {
		return "", err
	}
	path, _, err := atomicfile.WriteScratch(directory, pattern, []byte(content))
	if err != nil {
		return "", fmt.Errorf("write prepared %s: %w", description, err)
	}
	return path, nil
}

func removePreparedFiles(files []string) error {
	var removeErr error
	for _, path := range files {
		if err := os.Remove(path); err != nil {
			removeErr = errors.Join(removeErr, fmt.Errorf("remove prepared file %s: %w", path, err))
		}
	}
	return removeErr
}

func failedResultText(err error) string {
	detail := "unknown error"
	if err != nil {
		detail = flattenErrorText(err)
	}
	return "failed (" + detail + ")"
}

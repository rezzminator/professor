package mcpserv

import (
	"context"
	"fmt"

	"hostops/pfm/internal/transcript"
)

// read is chat_read: chat.ReadEntries under the tool's turn and byte bounds.
func (current *backend) read(ctx context.Context, input ReadInput) (ReadOutput, error) {
	if current.chat == nil {
		return ReadOutput{}, fmt.Errorf("chat_read verb is not configured")
	}
	lastN := input.LastN
	if lastN == 0 {
		lastN = 20
	}
	if lastN < 1 || lastN > 200 {
		return ReadOutput{}, fmt.Errorf("last_n must be between 1 and 200")
	}
	maxBytes := input.MaxBytes
	if maxBytes == 0 {
		maxBytes = 64 << 10
	}
	if maxBytes < 1 || maxBytes > 1<<20 {
		return ReadOutput{}, fmt.Errorf("max_bytes must be between 1 and 1048576")
	}
	found, entries, truncated, err := current.chat.Read(ctx, input.Source, lastN)
	if err != nil {
		return ReadOutput{}, fmt.Errorf("chat_read: %w", err)
	}
	turns, bytes, budgetTruncated := boundTurns(entries, maxBytes)
	return ReadOutput{
		ID: found.ID, Path: found.Path, Engine: string(found.Engine),
		Turns: turns, Count: len(turns), Truncated: truncated || budgetTruncated, Bytes: bytes,
	}, nil
}

// boundTurns keeps the newest entries whose text fits maxBytes, newest last;
// the oldest kept entry is cut to the remaining budget rather than dropped.
// A tool call records no prose, so its condensed input is the turn's text and
// the tool's name rides along in Tool — otherwise a stretch of tool work
// returns blank turns and reads as a chat that said nothing.
func boundTurns(entries []transcript.Entry, maxBytes int) ([]Turn, int, bool) {
	kept := make([]Turn, 0, len(entries))
	used := 0
	truncated := false
	for index := len(entries) - 1; index >= 0; index-- {
		available := maxBytes - used
		if available <= 0 {
			truncated = true
			break
		}
		entry := entries[index]
		text := entry.Text
		if text == "" && entry.Input != "" {
			text = entry.Input
		}
		if len(text) > available {
			text = transcript.Truncate(text, available)
			truncated = true
		}
		used += len(text)
		kept = append(kept, Turn{Role: entry.Role, Text: text, Tool: entry.Tool, Timestamp: entry.Timestamp})
	}
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	return kept, used, truncated
}

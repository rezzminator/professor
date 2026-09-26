package chat

import (
	"context"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// TestReadEntriesTailsNewestLastAndSaysWhenItCut pins chat read's shared
// extraction: the newest N entries, newest last, and truncated exactly when
// entries were left out.
func TestReadEntriesTailsNewestLastAndSaysWhenItCut(t *testing.T) {
	root := testjail.Fleet(t)
	const id = "d4444444-4444-4444-8444-444444444444"
	seedClaudeChat(t, root, id, assistantSaid("tests are green"))
	ctx := context.Background()

	found, entries, truncated, err := ReadEntries(ctx, id, 1, nil)
	if err != nil || found.ID != id {
		t.Fatalf("ReadEntries(tail 1) = %+v, %v", found, err)
	}
	if len(entries) != 1 || entries[0].Role != transcript.RoleAssistant || !truncated {
		t.Fatalf("ReadEntries(tail 1) = %+v truncated=%t, want the answer alone and truncated", entries, truncated)
	}

	_, entries, truncated, err = ReadEntries(ctx, id, 10, nil)
	if err != nil || truncated || len(entries) != 2 ||
		entries[0].Role != transcript.RoleUser || entries[1].Role != transcript.RoleAssistant {
		t.Fatalf("ReadEntries(tail 10) = %+v truncated=%t err=%v, want the whole exchange", entries, truncated, err)
	}
}

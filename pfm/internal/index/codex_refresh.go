package index

import (
	"context"
	"fmt"
	"os"

	"hostops/pfm/internal/store"
)

// RefreshCodexLineage catches up only the known rollout files about to be
// retired. A cached prompt count is not a safe /clear baseline: indexing the
// already-written tail later would make it look like a new prompt and unhide
// the chat. Missing/unreadable files must leave retirement pending for retry.
func RefreshCodexLineage(ctx context.Context, database *store.Store, id string) error {
	family, err := database.RolloutLineage(ctx, id)
	if err != nil {
		return fmt.Errorf("read Codex clear lineage %q: %w", id, err)
	}
	if len(family) == 0 {
		return fmt.Errorf("clear lineage for Codex %q is not indexed yet", id)
	}
	version, found, err := database.Meta(ctx, codexParserVersionKey)
	if err != nil {
		return fmt.Errorf("read Codex clear parser version: %w", err)
	}
	full := !found || version != codexParserVersion
	updates := make([]store.Rollout, 0, len(family))
	for i := range family {
		previous := &family[i]
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("refresh Codex clear lineage %q: %w", id, err)
		}
		info, err := os.Stat(previous.Path)
		if err != nil {
			return fmt.Errorf("stat Codex clear rollout %q: %w", previous.Path, err)
		}
		file := diskFile{ID: previous.ID, Path: previous.Path, Size: info.Size(), MTimeNS: info.ModTime().UnixNano()}
		if shouldSkip(file, true, previous.Size, previous.MTimeNS, full) {
			continue
		}
		start := int64(0)
		// Parent metadata can also come from the Codex state store.
		base := store.Rollout{
			ParentThread: previous.ParentThread,
			SessionID:    previous.SessionID,
			CWD:          previous.CWD,
			UserThread:   previous.UserThread,
		}
		if shouldDelta(file, true, previous.Size, previous.ParsedOffset, full) {
			start, base = previous.ParsedOffset, *previous
		}
		rollout, _, err := parseCodexRolloutFile(file, start, base)
		if err != nil {
			return fmt.Errorf("refresh Codex clear rollout %q: %w", previous.Path, err)
		}
		updates = append(updates, rollout)
	}
	return writeRolloutUpdates(ctx, database, updates)
}

package main

import (
	"context"
	"fmt"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/transcript"
)

// transcriptEntriesForSave tries transcript.All under both engines an
// explicit `pfm chat save` path may name — the one parser package owns —
// and keeps whichever actually reads entries, instead of the hardcoded
// Claude engine this replaced, which silently read zero entries from a
// Codex rollout and still exited 0.
func transcriptEntriesForSave(ctx context.Context, path string) ([]transcript.Entry, error) {
	for _, engine := range []string{string(pfmengine.Claude), string(pfmengine.Codex)} {
		if entries, err := transcript.All(ctx, path, engine); err == nil && len(entries) > 0 {
			return entries, nil
		}
	}
	return nil, fmt.Errorf("%s: no recognizable Claude or Codex transcript record", path)
}

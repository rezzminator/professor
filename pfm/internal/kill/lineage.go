package kill

import (
	"context"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// resolveUnindexedCodexParent reads rolloutPath's own session_meta header —
// the shared shape the identity resolver and the CLI's own row lookup both hit —
// for the conversation it resumes from: the ONLY place that link exists
// before the indexer has parsed the file into the fleet database. It
// answers found=true only when THAT parent id itself resolves to a real,
// already-indexed lineage; a parent that is ALSO unindexed (a deeper resume
// chain the indexer has not reached at all) falls through to the caller's
// own fallback rather than guessing further.
func (manager *Manager) resolveUnindexedCodexParent(
	ctx context.Context,
	rolloutPath string,
) (Target, bool) {
	if rolloutPath == "" {
		return Target{}, false
	}
	// Could not look is said, never read as "no lineage".
	unresolved := func(err error) (Target, bool) {
		fmt.Fprintf(
			manager.log,
			"pfm kill: resolve the lineage of %s: %v; the kill falls back to the chat's own id\n",
			rolloutPath,
			err,
		)
		return Target{}, false
	}
	// Unindexed lineage uses the same bounded metadata reader as live identity.
	header, err := codexmeta.ReadHeader(rolloutPath)
	if err != nil {
		return unresolved(err)
	}
	if header.LineageParent == "" {
		return Target{}, false
	}
	lineage, found, err := manager.database.CodexLineage(ctx, header.LineageParent)
	if err != nil {
		return unresolved(fmt.Errorf("look up parent %s: %w", header.LineageParent, err))
	}
	if !found {
		return Target{}, false
	}
	return Target{
		Engine:   pfmengine.Codex,
		ID:       lineage.RootID,
		DataPath: lineage.Newest.Path,
	}, true
}

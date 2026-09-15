package gather

import (
	"context"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/index"
	"hostops/pfm/internal/store"
)

type gatherIndexSource struct{ id pfmengine.ID }

func (source gatherIndexSource) Sync(
	ctx context.Context,
	database *store.Store,
	roots []string,
	counters *index.Counters,
) error {
	switch source.id {
	case pfmengine.Claude:
		return index.SyncClaude(ctx, database, roots, counters)
	case pfmengine.Codex:
		return index.SyncCodex(ctx, database, roots, counters)
	case pfmengine.Opencode:
		return index.SyncOpencode(ctx, database, roots, counters)
	default:
		return nil
	}
}

func init() {
	for _, id := range []pfmengine.ID{pfmengine.Claude, pfmengine.Codex, pfmengine.Opencode} {
		index.RegisterSource(id, gatherIndexSource{id: id})
	}
}

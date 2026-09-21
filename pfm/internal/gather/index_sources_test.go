package gather

import (
	"context"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/store"
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
	case pfmengine.OpenCode:
		return index.SyncOpenCode(ctx, database, roots, counters)
	default:
		return nil
	}
}

func init() {
	for _, id := range []pfmengine.ID{pfmengine.Claude, pfmengine.Codex, pfmengine.OpenCode} {
		index.RegisterSource(id, gatherIndexSource{id: id})
	}
}

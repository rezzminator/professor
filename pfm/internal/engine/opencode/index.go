package opencode

import (
	"context"

	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/store"
)

type Source struct{}

func (Source) Sync(ctx context.Context, database *store.Store, roots []string, counters *index.Counters) error {
	return index.SyncOpenCode(ctx, database, roots, counters)
}

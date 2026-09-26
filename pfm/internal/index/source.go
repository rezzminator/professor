package index

import (
	"context"
	"fmt"
	"sort"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/store"
)

type Source interface {
	Sync(ctx context.Context, db *store.Store, roots []string, counters *Counters) error
}

var sources = map[pfmengine.ID]Source{}

func RegisterSource(id pfmengine.ID, source Source) {
	if _, duplicate := sources[id]; duplicate {
		panic(fmt.Sprintf("index: source for engine %q registered twice", id))
	}
	sources[id] = source
}

func SourceFor(id pfmengine.ID) (Source, error) {
	source, ok := sources[id]
	if !ok {
		return nil, fmt.Errorf("engine %s: no index source registered", id)
	}
	return source, nil
}

func RegisteredSources() []pfmengine.ID { return sortedSourceIDs(sources) }

func sortedSourceIDs[T any](values map[pfmengine.ID]T) []pfmengine.ID {
	ids := make([]pfmengine.ID, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	return ids
}

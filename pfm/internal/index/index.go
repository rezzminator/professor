// Package index incrementally indexes every registered engine's sessions.
package index

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/store"
)

const (
	codexParserVersionKey = "codex_parser_version"
	codexParserVersion    = "4"

	claudeParserVersionKey = "claude_parser_version"
	claudeParserVersion    = "3"
	messageRoleUser        = "user"
)

// Options controls one indexing pass.
type Options struct {
	Full         bool
	PriorityCWD  string
	PriorityOnly bool
}

// Counters describes the work performed by one indexing pass.
type Counters struct {
	FilesSeen       int
	FilesSkipped    int
	DeltaParsed     int
	FullParsed      int
	Deleted         int
	RowsTouched     int
	BytesRead       int64
	CxNamesReloaded bool
	// CodexThreads counts the listed conversations the Codex state store
	// vouched for, and CodexRowsCreated the rows this pass had to create for
	// conversations the rollout directory never showed.
	CodexThreads     int
	CodexRowsCreated int

	// OpenCodeSessions counts the OpenCode sessions mirrored this pass. The mirror
	// is a full replace, so this is the population, not a delta.
	OpenCodeSessions      int
	options               Options
	legacySingleCodexHome bool
}

// Indexer incrementally mirrors session stores into SQLite.
type Indexer struct {
	database              *store.Store
	roots                 map[pfmengine.ID][]string
	legacySingleCodexHome bool
}

// New resolves the jailed or default host paths used by an Indexer.
func New(database *store.Store) (*Indexer, error) {
	if database == nil {
		return nil, fmt.Errorf("index store is nil")
	}
	resolved, err := paths.Resolve()
	if err != nil {
		return nil, fmt.Errorf("resolve index paths: %w", err)
	}
	return NewWithPaths(database, resolved)
}

// NewWithPaths constructs an Indexer with already-resolved host paths.
// Callers that have loaded command policy must pass those paths here so the
// indexer reads the same account roots as the rest of the command.
func NewWithPaths(database *store.Store, resolved paths.Values) (*Indexer, error) {
	indexer, err := newWithRoots(database, resolved.Roots)
	if indexer != nil {
		indexer.legacySingleCodexHome = true
	}
	return indexer, err
}

// NewWithRoots constructs an Indexer over config-owned engine roots.
func NewWithRoots(database *store.Store, _ paths.Values, roots map[pfmengine.ID][]string) (*Indexer, error) {
	return newWithRoots(database, roots)
}

func newWithRoots(database *store.Store, roots map[pfmengine.ID][]string) (*Indexer, error) {
	if database == nil {
		return nil, fmt.Errorf("index store is nil")
	}
	cleanRoots := make(map[pfmengine.ID][]string, len(roots))
	for id, values := range roots {
		seen := make(map[string]bool, len(values))
		for _, root := range values {
			clean := filepath.Clean(strings.TrimSpace(root))
			if clean == "." || seen[clean] {
				continue
			}
			seen[clean] = true
			cleanRoots[id] = append(cleanRoots[id], clean)
		}
	}
	return &Indexer{database: database, roots: cleanRoots}, nil
}

// Run asks every registered engine's index source to perform its indexing
// pass. An engine with no source is a wiring failure, refused before any source
// syncs: skipping it would leave its chats out of the index, and every lookup
// would report a failure to look as "no chat named X".
func (indexer *Indexer) Run(ctx context.Context, options Options) (Counters, error) {
	counters := Counters{
		options:               options,
		legacySingleCodexHome: indexer.legacySingleCodexHome,
	}
	ids := pfmengine.All()
	engineSources := make([]Source, 0, len(ids))
	for _, id := range ids {
		source, err := SourceFor(id)
		if err != nil {
			return counters, fmt.Errorf("index: %w", err)
		}
		engineSources = append(engineSources, source)
	}
	for position, source := range engineSources {
		if err := source.Sync(ctx, indexer.database, indexer.roots[ids[position]], &counters); err != nil {
			return counters, fmt.Errorf("index engine %s: %w", ids[position], err)
		}
	}
	return counters, nil
}

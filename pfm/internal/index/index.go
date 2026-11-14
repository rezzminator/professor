// Package index incrementally indexes every registered engine's sessions.
//
//   - claude.go: parses a Claude transcript file into session rows.
//   - codex.go: parses a Codex rollout file into session rows.
//   - codex_refresh.go: RefreshCodexLineage, the catch-up of one Codex lineage.
//   - codexstate.go: reconciles the index with Codex's own state store.
//   - cxindex.go: Codex session-name renames and file stamps.
//   - incremental.go: which files a pass parses, skips or continues.
//   - opencode.go: ReadOpenCodeSessions, the OpenCode session mirror.
//   - opencode_rows.go: OpenCode's message and part rows, and the read-only store open.
//   - opencode_schema.go: whether an OpenCode store's tables exist yet; ProbeOpenCodeStore, doctor's read of it.
//   - source.go: the per-engine Source registry.
//   - sync_sources.go: each engine's complete sync pass.
//   - walk.go: walks the engines' transcript roots for files.
package index

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

const (
	codexParserVersionKey = "codex_parser_version"
	codexParserVersion    = "5"

	claudeParserVersionKey = "claude_parser_version"
	claudeParserVersion    = "5"

	// openCodeStoreStampKey holds the stamp of the OpenCode stores the
	// oc_sessions mirror was last replaced from; a change to the mirror's
	// columns or to how a session row is read bumps openCodeMirrorVersion.
	openCodeStoreStampKey = "opencode_store_stamp"
	openCodeMirrorVersion = "1"
	messageRoleUser       = "user"
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
	// is a full replace, so this is the population, not a delta; a pass that
	// found the stores unchanged mirrors none.
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

// PartialError is a Run in which some sources failed and the rest indexed:
// each failed engine keeps its prior rows, so a caller may list from the index
// and report the failures. Its text is the failures' own.
type PartialError struct{ Failures []error }

func (partial *PartialError) Error() string { return errors.Join(partial.Failures...).Error() }

func (partial *PartialError) Unwrap() []error { return partial.Failures }

// Run asks every registered engine's index source to perform its indexing
// pass. An engine with no source is a wiring failure, refused before any source
// syncs: skipping it would leave its chats out of the index, and every lookup
// would report a failure to look as "no chat named X". A source that fails
// costs its own pass only: the others still run, and the error is a
// *PartialError unless every source failed or ctx was cancelled.
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
	failures := make([]error, 0)
	for position, source := range engineSources {
		if err := source.Sync(ctx, indexer.database, indexer.roots[ids[position]], &counters); err != nil {
			err = fmt.Errorf("index engine %s: %w", ids[position], err)
			if ctx.Err() != nil {
				return counters, err
			}
			failures = append(failures, err)
		}
	}
	switch len(failures) {
	case 0:
		return counters, nil
	case len(engineSources):
		return counters, errors.Join(failures...)
	default:
		return counters, &PartialError{Failures: failures}
	}
}

// warnUnreadable records one item a pass could not read and went on without:
// the pass keeps its siblings and the item's prior row, and the activity log
// is where the loss shows.
func warnUnreadable(ctx context.Context, kind, path string, err error) {
	obs.Logger(ctx).Warn("index.unreadable", "kind", kind, "path", path, obs.FieldErr, err.Error())
}

// warnUnparsedLines records how many complete lines of one file a parse
// skipped as undecodable; the file's other lines are kept.
func warnUnparsedLines(ctx context.Context, path string, lines int) {
	if lines == 0 {
		return
	}
	obs.Logger(ctx).Warn("index.unparsed_lines", "path", path, "count", lines)
}

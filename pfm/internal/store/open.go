package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

type openOptions struct {
	warn     io.Writer
	clock    clock.Clock
	rowCache bool
}

// OpenOption customizes process-local Store behavior.
type OpenOption func(*openOptions)

// WithWarningWriter directs non-fatal busy warnings to w. The default is
// os.Stderr.
func WithWarningWriter(w io.Writer) OpenOption {
	return func(options *openOptions) {
		if w != nil {
			options.warn = w
		}
	}
}

// withRowCache keeps the index tables in memory between reads (rowcache.go):
// only a Held Store, read again for a process's whole life, pays its memory.
func withRowCache() OpenOption {
	return func(options *openOptions) { options.rowCache = true }
}

// WithClock injects the store clock for deterministic busy retry and audit timestamps.
func WithClock(value clock.Clock) OpenOption {
	return func(options *openOptions) {
		if value != nil {
			options.clock = value
		}
	}
}

// Open opens the database selected by internal/paths and applies migrations
// and connection policy.
func Open(options ...OpenOption) (*Store, error) {
	return OpenContext(context.Background(), options...)
}

// OpenContext is Open with caller-controlled cancellation.
func OpenContext(ctx context.Context, options ...OpenOption) (*Store, error) {
	resolved, settings, err := resolveOpen(options)
	if err != nil {
		return nil, err
	}

	info, statErr := os.Stat(resolved.CacheDB)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect cache database %s: %w", resolved.CacheDB, statErr)
	}
	existed := statErr == nil && info.Size() > 0
	db, err := sqlitedb.OpenStore(ctx, resolved.CacheDB)
	if err != nil {
		return nil, err
	}
	return openMigrated(ctx, db, fleetdb.OpenSharedState(ctx, resolved), resolved.CacheDB, existed, settings)
}

// OpenMemory opens a Store whose cache and shared state are new in-memory
// databases (sqlitedb.OpenStoreMemory), migrated by the same steps as Open: the door
// a unit test opens its store through. It creates no file and reads no
// environment; both databases end at Close. Unlike Open, a shared half that
// fails to open is an error: no test runs over a degraded in-memory store.
func OpenMemory(options ...OpenOption) (*Store, error) {
	ctx := context.Background()
	db, path, err := sqlitedb.OpenStoreMemory(ctx)
	if err != nil {
		return nil, err
	}
	state := fleetdb.OpenSharedStateMemory(ctx)
	if degraded := state.Degraded(); degraded != nil {
		return nil, errors.Join(fmt.Errorf("open in-memory shared state: %w", degraded), db.Close())
	}
	return openMigrated(ctx, db, state, path, false, openSettings(options))
}

// openMigrated wraps an open cache and its shared half in a Store and migrates
// the cache; existed says the cache file held data before this open.
func openMigrated(
	ctx context.Context, db *sql.DB, state *fleetdb.Store, path string, existed bool, settings openOptions,
) (*Store, error) {
	store := &Store{
		db:    db,
		state: state,
		path:  path,
		warn:  settings.warn,
		clock: settings.clock,
	}
	if settings.rowCache {
		store.transcripts, store.rollouts = newTranscriptCache(), newRolloutCache()
	}
	if err := store.migrate(ctx, existed); err != nil {
		return nil, errors.Join(err, store.Close())
	}
	if degraded := store.state.Degraded(); degraded != nil {
		store.warningf(
			"WARNING: pfm could not open the shared state store at %s: %v; "+
				"operator-state operations will fail\n",
			store.state.Path(),
			degraded,
		)
	}
	return store, nil
}

// resolveOpen resolves both database paths and the open settings.
func resolveOpen(options []OpenOption) (paths.Values, openOptions, error) {
	resolved, err := pfmconfig.ResolvePaths()
	if err != nil {
		return paths.Values{}, openOptions{}, fmt.Errorf("resolve store paths: %w", err)
	}
	return resolved, openSettings(options), nil
}

// openSettings applies options over the defaults: warnings to os.Stderr, the
// real clock.
func openSettings(options []OpenOption) openOptions {
	settings := openOptions{warn: os.Stderr, clock: clock.Real}
	for _, option := range options {
		option(&settings)
	}
	return settings
}

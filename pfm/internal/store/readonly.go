package store

import (
	"context"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

// OpenWithoutMigrating opens both databases for a diagnostic — pfm doctor —
// read-only (mode=ro): it never creates, initializes or migrates either file,
// so a newer build's doctor leaves an older installed pfm the databases it can
// still read; only a read-write Open migrates. The cache half must sit at
// this build's schema: a missing or empty file returns fleetdb.ErrAbsent and
// another version a *fleetdb.SchemaMismatchError, each with a nil Store. The
// shared half's absence or mismatch rides SharedDegraded, as an unopenable
// shared store does for Open.
func OpenWithoutMigrating(ctx context.Context, options ...OpenOption) (*Store, error) {
	resolved, settings, err := resolveOpen(options)
	if err != nil {
		return nil, err
	}
	db, err := fleetdb.OpenReadOnlyAtSchema(ctx, "cache", resolved.CacheDB, SchemaVersion)
	if err != nil {
		return nil, err
	}
	return &Store{
		db:    db,
		state: fleetdb.OpenSharedStateReadOnly(ctx, resolved),
		path:  resolved.CacheDB,
		warn:  settings.warn,
		clock: settings.clock,
	}, nil
}

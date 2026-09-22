package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

const (
	// SchemaVersion is the newest database schema understood by this binary.
	SchemaVersion = 8
)

//go:embed schema.sql
var schemaV1 string

//go:embed migration_v2.sql
var schemaV2 string

//go:embed migration_v3.sql
var schemaV3 string

//go:embed migration_v4.sql
var schemaV4 string

//go:embed migration_v5.sql
var schemaV5 string

//go:embed migration_v6.sql
var schemaV6 string

//go:embed migration_v7.sql
var schemaV7 string

//go:embed migration_v8.sql
var schemaV8 string

var migrations = [...]string{
	schemaV1,
	schemaV2,
	schemaV3,
	schemaV4,
	schemaV5,
	schemaV6,
	schemaV7,
	schemaV8,
}

// Store is a single-connection handle to the pfm SQLite database.
//
// It holds TWO databases, and the split is the whole point. `db` is this
// binary's private cache — transcripts, rollouts, Codex names — every row of it
// derived from files on disk and rebuildable by a rescan. `state` is the
// fleet's shared store at ~/.cc/fleet.db. It holds
// operator decisions such as kills, teammates, and the primary account.
type Store struct {
	db    *sql.DB
	state *fleetdb.Store
	path  string

	warnMu sync.Mutex
	warn   io.Writer
	clock  clock.Clock
}

type openOptions struct {
	warn  io.Writer
	clock clock.Clock
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
	resolved, err := paths.Resolve()
	if err != nil {
		return nil, fmt.Errorf("resolve store paths: %w", err)
	}

	settings := openOptions{warn: os.Stderr, clock: clock.Real}
	for _, option := range options {
		option(&settings)
	}

	db, err := sqlitedb.OpenStore(ctx, resolved.DB)
	if err != nil {
		return nil, err
	}

	store := &Store{
		db:    db,
		state: fleetdb.OpenSharedState(ctx, resolved),
		path:  resolved.DB,
		warn:  settings.warn,
		clock: settings.clock,
	}
	if err := store.migrate(ctx); err != nil {
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
	if err := store.adoptLocalKills(ctx); err != nil {
		return nil, errors.Join(err, store.Close())
	}
	return store, nil
}

func (s *Store) clockNow() time.Time {
	if s.clock == nil {
		return clock.Real.Now()
	}
	return s.clock.Now()
}

func (s *Store) clockTimer(duration time.Duration) clock.Timer {
	if s.clock == nil {
		return clock.Real.NewTimer(duration)
	}
	return s.clock.NewTimer(duration)
}

// SharedPath reports the shared state database this Store writes kills to.
func (s *Store) SharedPath() string { return s.state.Path() }

// SharedDegraded reports why the shared database half is unavailable, or nil.
func (s *Store) SharedDegraded() error { return s.state.Degraded() }

// Shared exposes the shared state store for the few callers that need it
// directly, including the teammate reaper.
func (s *Store) Shared() *fleetdb.Store { return s.state }

func (s *Store) migrate(ctx context.Context) error {
	return s.WithImmediateTx(ctx, func(tx *ImmediateTx) error {
		version, err := userVersion(ctx, tx)
		if err != nil {
			return err
		}
		if version > SchemaVersion {
			// Migrations are ONE-WAY. A newer binary migrated this database;
			// every older binary on the machine is now locked out until it is
			// upgraded — there is no automatic downgrade, and hand-editing
			// user_version is only safe when the skipped migrations happen to
			// be purely additive, which nothing here guarantees. Say so where
			// the failure lands, with the recovery that always works.
			return fmt.Errorf(
				"database schema version %d is newer than supported version %d: "+
					"this pfm binary is OLDER than the one that migrated it — "+
					"upgrade every pfm on this machine, or restore a backup "+
					"(sqlite3 <db> \".backup <backup-before-upgrade>\") taken before the newer binary first ran; "+
					"migrations are one-way and no downgrade path exists",
				version,
				SchemaVersion,
			)
		}

		startingVersion := version
		for next := version + 1; next <= SchemaVersion; next++ {
			if _, err := tx.ExecContext(ctx, migrations[next-1]); err != nil {
				return fmt.Errorf("apply database migration %d: %w", next, err)
			}
			if _, err := tx.ExecContext(
				ctx,
				fmt.Sprintf("PRAGMA user_version=%d", next),
			); err != nil {
				return fmt.Errorf("set database schema version %d: %w", next, err)
			}
		}
		if startingVersion < 2 {
			if err := migrateCodexLineageKills(ctx, tx); err != nil {
				return fmt.Errorf("migrate Codex lineage kills: %w", err)
			}
		}
		// assistant_count is ensured here, never versioned: it is a single
		// additive column an older binary's explicit column list already
		// ignores, so ADD COLUMN is idempotent and ignorable both ways.
		// Bumping user_version for it would instead lock every older pfm on
		// this machine out of the whole store the moment one binary ran it —
		// including the very binary pfm update's rollback restores.
		if err := ensureColumn(
			ctx, tx, "oc_sessions", "assistant_count", "INTEGER NOT NULL DEFAULT 0",
		); err != nil {
			return err
		}
		// continued_in is ensured the same way and for the same reason: an
		// older binary's explicit transcript column list never names it, and
		// its upsert leaves it alone. The claude parser version bump that ships
		// with it is what backfills the rows indexed before it existed.
		return ensureColumn(ctx, tx, "transcripts", "continued_in", "TEXT NOT NULL DEFAULT ''")
	})
}

// ensureColumn adds one additive column when the table lacks it. table,
// column and definition are compile-time constants, never user input.
func ensureColumn(ctx context.Context, tx *ImmediateTx, table, column, definition string) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA table_info("+table+")")
	if err != nil {
		return fmt.Errorf("inspect %s columns: %w", table, err)
	}
	present := false
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return errors.Join(fmt.Errorf("scan %s column: %w", table, err), rows.Close())
		}
		if name == column {
			present = true
		}
	}
	if err := rows.Err(); err != nil {
		return errors.Join(fmt.Errorf("iterate %s columns: %w", table, err), rows.Close())
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close %s columns: %w", table, err)
	}
	if present {
		return nil
	}
	if _, err := tx.ExecContext(
		ctx,
		"ALTER TABLE "+table+" ADD COLUMN "+column+" "+definition,
	); err != nil {
		return fmt.Errorf("ensure %s.%s: %w", table, column, err)
	}
	return nil
}

// adoptedKillsMeta marks the one-time move of kills out of this binary's
// private cache and into the fleet's shared store.
const adoptedKillsMeta = "shared_hidden_adopted"

// adoptLocalKills merges, once, the kills this binary used to keep to itself
// into the shared store, then stops consulting the local table for good.
//
// Local rows become shared rows. Nothing is deleted: a kill is
// permanent and an unkill is the only removal, so a startup that could drop a
// row is a startup that could lose a decision.
//
// The v1 `hidden` table is left in place, populated, and unread. It costs a few
// kilobytes and it is the rollback: an older binary still finds its kills
// there. `pfm doctor` reports it; nothing else looks at it.
func (s *Store) adoptLocalKills(ctx context.Context) error {
	done, found, err := s.Meta(ctx, adoptedKillsMeta)
	if err != nil {
		return err
	}
	if found && done == "1" {
		return nil
	}

	rows, err := s.logged().QueryContext(ctx, "SELECT id, hidden_at FROM hidden")
	if err != nil {
		return fmt.Errorf("read kills awaiting adoption: %w", err)
	}
	adopted := make(map[string]int64)
	for rows.Next() {
		var id string
		var killedAt int64
		if err := rows.Scan(&id, &killedAt); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan kill awaiting adoption: %w", err)
		}
		adopted[id] = killedAt
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("read kills awaiting adoption: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read kills awaiting adoption: %w", err)
	}

	existing, err := s.state.KilledAt(ctx)
	if err != nil {
		return err
	}
	for _, id := range fleetdb.SortedIDs(adopted) {
		if _, alreadyShared := existing[id]; alreadyShared {
			continue
		}
		if err := s.state.Kill(ctx, id, adopted[id]); err != nil {
			return err
		}
	}
	return s.SetMeta(ctx, adoptedKillsMeta, "1")
}

func userVersion(ctx context.Context, query rowQueryer) (int, error) {
	var version int
	if err := query.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return 0, fmt.Errorf("read database schema version: %w", err)
	}
	return version, nil
}

// UserVersion reports PRAGMA user_version.
func (s *Store) UserVersion(ctx context.Context) (int, error) {
	return userVersion(ctx, s.logged())
}

// Path reports the database pathname resolved when the Store was opened.
func (s *Store) Path() string {
	return s.path
}

// Stats reports database/sql pool statistics.
func (s *Store) Stats() sql.DBStats {
	return s.db.Stats()
}

// Close closes both SQLite handles.
func (s *Store) Close() error {
	return errors.Join(s.db.Close(), s.state.Close())
}

func (s *Store) warningf(format string, args ...any) {
	s.warnMu.Lock()
	defer s.warnMu.Unlock()
	fmt.Fprintf(s.warn, format, args...)
}

// Package fleetdb is the fleet's authoritative state store — operator decisions: kills, comms.
//
// The SQLite database at ~/.local/state/pfm/pfm.db holds every operator decision: killed
// chats, spawned teammates, and the primary account. The transcript database
// (paths.Values.CacheDB) remains a derived cache that a rescan can rebuild.
//
// open.go: OpenSharedState and OpenSharedStateMemory, the shared database opened, created or in memory, and brought to schema; Store.UserVersion, its stamp.
// immediate.go: withImmediate, the BEGIN IMMEDIATE transaction a write that reads first runs in.
// seatrun.go: SeatRun and its recipe file beside the shared database.
// primary.go: ClaudePrimaryAccount and SetClaudePrimaryAccount, the primary Claude account row and its mirror file.
package fleetdb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
)

const (
	// PrimaryAccountKey is the meta row for the primary Claude account.
	PrimaryAccountKey = "primary_account"

	// KindNew and KindPane are the two children kinds the schema admits. `new`
	// is a detached teammate on its own tmux socket; `pane` preserves cleanup
	// records for a teammate sharing a server as "<socket>\t<pane>".
	KindNew  = "new"
	KindPane = "pane"

	branchSeatPrefix = "branch-seat:"
)

// BranchSeat is a detached /chat:branch process that has not necessarily
// written a transcript yet. The socket remains the immutable address; Parent
// is provenance only, never an alias.
type BranchSeat struct {
	Socket    string
	Parent    string
	CreatedAt int64
}

// schemaDDL initializes the complete operator-state schema atomically.
const schemaDDL = `
CREATE TABLE IF NOT EXISTS meta(key TEXT PRIMARY KEY, val TEXT NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS hidden(
  uuid TEXT PRIMARY KEY,
  hidden_at INTEGER NOT NULL,
  at_payload INTEGER
);
CREATE TABLE IF NOT EXISTS children(
  kind TEXT NOT NULL CHECK(kind IN ('new','pane')),
  key  TEXT NOT NULL,
  val  TEXT,
  created_at INTEGER NOT NULL,
  PRIMARY KEY(kind, key, val)
);
CREATE TABLE IF NOT EXISTS chat(
  uuid    TEXT PRIMARY KEY,
  mtime   INTEGER NOT NULL DEFAULT 0,
  bytes   INTEGER NOT NULL DEFAULT 0,
  cwd     TEXT,
  label   TEXT,
  prompts INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS chat_mtime ON chat(mtime);
CREATE TABLE IF NOT EXISTS comms(
  id              INTEGER PRIMARY KEY,
  at_ns           INTEGER NOT NULL,
  kind            TEXT NOT NULL CHECK(kind IN ('call','group','spawn')),
  sender_session  TEXT NOT NULL DEFAULT '',
  sender_label    TEXT NOT NULL DEFAULT '',
  sender_uuid     TEXT NOT NULL DEFAULT '',
  target          TEXT NOT NULL DEFAULT '',
  receiver_socket TEXT NOT NULL DEFAULT '',
  receiver_pane   TEXT NOT NULL DEFAULT '',
  group_name      TEXT NOT NULL DEFAULT '',
  members         TEXT NOT NULL DEFAULT '',
  message         TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS comms_at ON comms(at_ns);
INSERT OR IGNORE INTO meta(key,val,updated_at) VALUES('schema_version','1',strftime('%s','now'));
`

// Store is a handle on the shared database.
type Store struct {
	db       *sql.DB
	path     string
	degraded error
	// commsWrites counts this Store's own committed comms rows: SQLite's
	// data_version does not move for the asking connection's own commits.
	commsWrites atomic.Uint64
}

// SetBusyTimeout changes how long a statement waits for a concurrent writer
// before giving up. The default is sqlitedb.StoreBusyTimeout; the
// busy-policy fixture shortens it so it can reach the give-up path without
// stalling a test for ten seconds.
func (s *Store) SetBusyTimeout(ctx context.Context, milliseconds int) error {
	if s.db == nil {
		return nil
	}
	if _, err := s.exec(
		ctx,
		fmt.Sprintf("PRAGMA busy_timeout=%d", milliseconds),
	); err != nil {
		return fmt.Errorf("set shared busy_timeout: %w", err)
	}
	return nil
}

// Path reports the shared database pathname.
func (s *Store) Path() string { return s.path }

// Degraded reports why the database half is unavailable, or nil.
func (s *Store) Degraded() error { return s.degraded }

// Close releases the database handle.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// KilledRecord is one operator kill. A nil AtPayload is permanent; a prompt
// baseline is the clear-kill ratchet and expires when that transcript grows.
type KilledRecord struct {
	KilledAt  int64
	AtPayload *int64
}

// Kill records a permanent kill in SQLite. An explicit kill always wins over
// an earlier clear-kill ratchet by clearing its prompt baseline.
func (s *Store) Kill(ctx context.Context, id string, killedAt int64) error {
	if s.db == nil {
		return fmt.Errorf("record shared kill %q: %w", id, s.degraded)
	}
	if _, err := s.exec(ctx, permanentKillSQL, id, killedAt, rand.Text()); err != nil {
		return fmt.Errorf("record shared kill %q: %w", id, err)
	}
	return nil
}

const permanentKillSQL = `
INSERT INTO hidden(uuid,hidden_at,at_payload,revision) VALUES(?,?,NULL,?)
ON CONFLICT(uuid) DO UPDATE SET
  hidden_at=excluded.hidden_at,
  at_payload=NULL,
  revision=excluded.revision || '-updated'
RETURNING revision`

// KillWithRevision returns a rollback identity only when this statement inserts
// the row. An upsert of an existing kill gets a fresh revision but no rollback.
func (s *Store) KillWithRevision(ctx context.Context, id string, killedAt int64) (string, error) {
	if s.db == nil {
		return "", fmt.Errorf("record shared kill %q: %w", id, s.degraded)
	}
	revision := rand.Text()
	var written string
	if err := s.queryRow(ctx, permanentKillSQL, id, killedAt, revision).Scan(&written); err != nil {
		return "", fmt.Errorf("record shared kill %q: %w", id, err)
	}
	if written != revision {
		return "", nil
	}
	return revision, nil
}

// UnkillIfRevision rolls back only the exact permanent kill write observed.
func (s *Store) UnkillIfRevision(ctx context.Context, id, revision string) (bool, error) {
	if s.db == nil {
		return false, fmt.Errorf("roll back shared kill %q: %w", id, s.degraded)
	}
	result, err := s.exec(ctx, "DELETE FROM hidden WHERE uuid=? AND revision=? AND at_payload IS NULL", id, revision)
	if err != nil {
		return false, fmt.Errorf("roll back shared kill %q: %w", id, err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count rolled back shared kill %q: %w", id, err)
	}
	return removed == 1, nil
}

// KillUntilPrompt records the current prompt count for a /clear kill. A
// permanent kill is never weakened; repeated clear events retain the greatest
// observed baseline so delivery/index races cannot unkill too early.
func (s *Store) KillUntilPrompt(
	ctx context.Context,
	id string,
	killedAt, baseline int64,
) error {
	if s.db == nil {
		return fmt.Errorf("record shared clear kill %q: %w", id, s.degraded)
	}
	if _, err := s.exec(ctx, `
INSERT INTO hidden(uuid,hidden_at,at_payload,revision) VALUES(?,?,?,lower(hex(randomblob(16))))
ON CONFLICT(uuid) DO UPDATE SET
  revision=CASE WHEN hidden.at_payload IS NULL THEN hidden.revision ELSE excluded.revision END,
  hidden_at=CASE WHEN hidden.at_payload IS NULL THEN hidden.hidden_at ELSE excluded.hidden_at END,
  at_payload=CASE
    WHEN hidden.at_payload IS NULL THEN NULL
    WHEN excluded.at_payload > hidden.at_payload THEN excluded.at_payload
    ELSE hidden.at_payload
  END`,
		id,
		killedAt,
		baseline,
	); err != nil {
		return fmt.Errorf("record shared clear kill %q: %w", id, err)
	}
	return nil
}

// Unkill deletes one kill row and reports whether it existed.
func (s *Store) Unkill(ctx context.Context, id string) (bool, error) {
	if s.db == nil {
		return false, fmt.Errorf("remove shared kill %q: %w", id, s.degraded)
	}
	result, err := s.exec(
		ctx,
		"DELETE FROM hidden WHERE uuid=?",
		id,
	)
	if err != nil {
		return false, fmt.Errorf("remove shared kill %q: %w", id, err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count removed shared kill %q: %w", id, err)
	}
	return removed == 1, nil
}

// ReassertKill makes an existing kill permanent without changing its time.
func (s *Store) ReassertKill(ctx context.Context, id string) (bool, error) {
	if s.db == nil {
		return false, fmt.Errorf("reassert shared kill %q: %w", id, s.degraded)
	}
	result, err := s.exec(
		ctx,
		"UPDATE hidden SET at_payload=NULL, revision=lower(hex(randomblob(16))) WHERE uuid=?",
		id,
	)
	if err != nil {
		return false, fmt.Errorf("reassert shared kill %q: %w", id, err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count reasserted shared kill %q: %w", id, err)
	}
	return updated == 1, nil
}

// UnkillIfPayload expires only the clear-kill version the caller observed.
// A concurrent explicit kill or later /clear survives the conditional delete.
func (s *Store) UnkillIfPayload(
	ctx context.Context,
	id string,
	payload int64,
) (bool, error) {
	if s.db == nil {
		return false, fmt.Errorf("expire shared clear kill %q: %w", id, s.degraded)
	}
	result, err := s.exec(
		ctx,
		"DELETE FROM hidden WHERE uuid=? AND at_payload=?",
		id,
		payload,
	)
	if err != nil {
		return false, fmt.Errorf("expire shared clear kill %q: %w", id, err)
	}
	removed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count expired shared clear kill %q: %w", id, err)
	}
	return removed == 1, nil
}

// KilledRecords returns the complete shared kill state keyed by chat id.
func (s *Store) KilledRecords(ctx context.Context) (records map[string]KilledRecord, returnErr error) {
	if errors.Is(s.degraded, ErrAbsent) {
		// Only a read-only open leaves a file absent: one never written holds no kills.
		return map[string]KilledRecord{}, nil
	}
	if s.db == nil {
		return nil, fmt.Errorf("query shared kills: %w", s.degraded)
	}
	records = make(map[string]KilledRecord)
	rows, err := s.query(
		ctx,
		"SELECT uuid, hidden_at, at_payload FROM hidden",
	)
	if err != nil {
		return nil, fmt.Errorf("query shared kills: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close shared kill rows: %w", err))
		}
	}()
	for rows.Next() {
		var id string
		var killedAt int64
		var payload sql.NullInt64
		if err := rows.Scan(&id, &killedAt, &payload); err != nil {
			skipRow(ctx, "hidden", fmt.Errorf("scan shared kill: %w", err))
			// The kill is the operator's decision: a row whose id still reads
			// stays killed, its time unknown and its clear baseline dropped, so it
			// never auto-unkills on a guess.
			var at, baseline any
			if rows.Scan(&id, &at, &baseline) == nil && id != "" {
				records[id] = KilledRecord{}
			}
			continue
		}
		record := KilledRecord{KilledAt: killedAt}
		if payload.Valid {
			value := payload.Int64
			record.AtPayload = &value
		}
		records[id] = record
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate shared kills: %w", err)
	}
	return records, nil
}

// KilledAt returns every killed row keyed by chat id.
func (s *Store) KilledAt(ctx context.Context) (map[string]int64, error) {
	records, err := s.KilledRecords(ctx)
	if err != nil {
		return nil, err
	}
	killed := make(map[string]int64, len(records))
	for id, record := range records {
		killed[id] = record.KilledAt
	}
	return killed, nil
}

// Children returns the teammates a chat spawned. The second result is false
// when the shared children table is unreachable, which is the caller's cue to
// fall back to flat files from an older install.
func (s *Store) Children(
	ctx context.Context,
	kind, key string,
) (values []string, found bool, returnErr error) {
	if s.db == nil {
		return nil, false, nil
	}
	rows, err := s.query(
		ctx,
		"SELECT val FROM children WHERE kind=? AND key=?",
		kind,
		key,
	)
	if err != nil {
		return nil, false, fmt.Errorf("query shared children: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close shared child rows: %w", err))
		}
	}()
	for rows.Next() {
		var value sql.NullString
		if err := rows.Scan(&value); err != nil {
			return nil, false, fmt.Errorf("scan shared child: %w", err)
		}
		if value.Valid && value.String != "" {
			values = append(values, value.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate shared children: %w", err)
	}
	return values, true, nil
}

// AddChild records one spawned teammate.
func (s *Store) AddChild(
	ctx context.Context,
	kind, key, value string,
	createdAt int64,
) error {
	if s.db == nil {
		return nil
	}
	if _, err := s.exec(ctx, `
INSERT OR IGNORE INTO children(kind,key,val,created_at) VALUES(?,?,?,?)`,
		kind,
		key,
		value,
		createdAt,
	); err != nil {
		return fmt.Errorf("record shared child: %w", err)
	}
	return nil
}

// ClearChild removes only the teammate that was actually reaped.
func (s *Store) ClearChild(ctx context.Context, kind, key, value string) error {
	if s.db == nil {
		return fmt.Errorf("clear shared child %q: %w", value, s.degraded)
	}
	if _, err := s.exec(ctx, "DELETE FROM children WHERE kind=? AND key=? AND val=?", kind, key, value); err != nil {
		return fmt.Errorf("clear shared %s child %q of %s: %w", kind, value, key, err)
	}
	return nil
}

// ClearChildren removes a chat's teammate rows once every teammate is down.
func (s *Store) ClearChildren(ctx context.Context, kind, key string) error {
	if s.db == nil {
		return nil
	}
	if _, err := s.exec(
		ctx,
		"DELETE FROM children WHERE kind=? AND key=?",
		kind,
		key,
	); err != nil {
		return fmt.Errorf("clear shared children: %w", err)
	}
	return nil
}

// Meta reads one meta row.
func (s *Store) Meta(ctx context.Context, key string) (string, bool, error) {
	if s.db == nil {
		return "", false, nil
	}
	var value string
	err := s.queryRow(
		ctx,
		"SELECT val FROM meta WHERE key=?",
		key,
	).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read shared meta %q: %w", key, err)
	}
	return value, true, nil
}

// SetMeta writes one timestamped meta row.
func (s *Store) SetMeta(
	ctx context.Context,
	key, value string,
	updatedAt int64,
) error {
	if s.db == nil {
		return nil
	}
	if _, err := s.exec(ctx, `
INSERT INTO meta(key,val,updated_at) VALUES(?,?,?)
ON CONFLICT(key) DO UPDATE SET val=excluded.val, updated_at=excluded.updated_at`,
		key,
		value,
		updatedAt,
	); err != nil {
		return fmt.Errorf("write shared meta %q: %w", key, err)
	}
	return nil
}

// RecordBranchSeat durably marks a detached fork before it is reported to the
// operator. That marker is how the reaper distinguishes an untouched fork
// from an unknown crumbless Claude process.
func (s *Store) RecordBranchSeat(
	ctx context.Context,
	socket, parent string,
	createdAt int64,
) error {
	if socket == "" || parent == "" {
		return errors.New("record branch seat requires socket and parent")
	}
	if s.degraded != nil {
		return s.degraded
	}
	return s.SetMeta(ctx, branchSeatPrefix+socket, parent, createdAt)
}

// BranchSeats returns every detached-fork marker keyed by immutable socket.
func (s *Store) BranchSeats(ctx context.Context) (result map[string]BranchSeat, returnErr error) {
	if s.degraded != nil {
		return nil, s.degraded
	}
	result = make(map[string]BranchSeat)
	if s.db == nil {
		return result, nil
	}
	rows, err := s.query(
		ctx,
		"SELECT key,val,updated_at FROM meta WHERE key GLOB ? ORDER BY key",
		branchSeatPrefix+"*",
	)
	if err != nil {
		return nil, fmt.Errorf("read shared branch seats: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close shared branch seat rows: %w", err))
		}
	}()
	for rows.Next() {
		var key, parent string
		var createdAt int64
		if err := rows.Scan(&key, &parent, &createdAt); err != nil {
			skipRow(ctx, "meta", fmt.Errorf("scan shared branch seat: %w", err))
			continue
		}
		socket := strings.TrimPrefix(key, branchSeatPrefix)
		if socket == "" {
			skipRow(ctx, "meta", fmt.Errorf("branch seat key %q names no socket", key))
			continue
		}
		result[socket] = BranchSeat{
			Socket: socket, Parent: parent, CreatedAt: createdAt,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate shared branch seats: %w", err)
	}
	return result, nil
}

// ClearBranchSeat removes the provenance marker once the fork's server is
// deliberately ended. It never touches the socket itself.
func (s *Store) ClearBranchSeat(ctx context.Context, socket string) error {
	if socket == "" {
		return nil
	}
	if s.degraded != nil {
		return s.degraded
	}
	if s.db == nil {
		return nil
	}
	if _, err := s.exec(
		ctx,
		"DELETE FROM meta WHERE key=?",
		branchSeatPrefix+socket,
	); err != nil {
		return fmt.Errorf("clear shared branch seat %q: %w", socket, err)
	}
	return nil
}

// SortedIDs returns the keys of a killed set in stable order.
func SortedIDs(killed map[string]int64) []string {
	ids := make([]string, 0, len(killed))
	for id := range killed {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

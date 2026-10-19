package fleetdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

const clearNamePrefix = "clear-name:"

// ClearName reads a successor's naming decision, not a cached engine title.
func (s *Store) ClearName(ctx context.Context, id string) (naming.ClearName, bool, error) {
	if s.Degraded() != nil {
		return naming.ClearName{}, false, fmt.Errorf("read clear name %s: %w", id, s.Degraded())
	}
	raw, found, err := s.Meta(ctx, clearNamePrefix+id)
	if err != nil || !found {
		return naming.ClearName{}, false, err
	}
	name, err := decodeClearName(id, raw)
	return name, err == nil, err
}

// ClearNames reads the durable overlays in one pass for a cached transcript list.
func (s *Store) ClearNames(ctx context.Context) (names map[string]naming.ClearName, returnErr error) {
	if s.Degraded() != nil {
		return nil, fmt.Errorf("read clear names: %w", s.Degraded())
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire clear names connection: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, conn.Close()) }()
	return clearNameDecisions(ctx, conn)
}

func decodeClearName(id, raw string) (naming.ClearName, error) {
	var name naming.ClearName
	if err := json.Unmarshal([]byte(raw), &name); err != nil {
		return name, fmt.Errorf("decode clear name %s: %w", id, err)
	}
	return name, name.Validate(id)
}

// PutClearName persists title observations independently of the rebuildable cache.
// New decisions are allocated by ReserveClearName, never by a read-then-write pair.
func (s *Store) PutClearName(ctx context.Context, id string, name naming.ClearName, at int64) error {
	if s.Degraded() != nil {
		return fmt.Errorf("write clear name %s: %w", id, s.Degraded())
	}
	if err := name.Validate(id); err != nil {
		return err
	}
	raw, err := json.Marshal(name)
	if err != nil {
		return fmt.Errorf("encode clear name %s: %w", id, err)
	}
	// The prompt hook and title observers read independently. Both flags are
	// monotonic: a stale snapshot must never undo another writer's observation.
	if _, err := s.exec(ctx, `
INSERT INTO meta(key,val,updated_at) VALUES(?,?,?)
ON CONFLICT(key) DO UPDATE SET
  val=json_set(excluded.val,
    '$.emitted', json(CASE WHEN json_extract(meta.val,'$.emitted') OR
      json_extract(excluded.val,'$.emitted') THEN 'true' ELSE 'false' END),
    '$.confirmed', json(CASE WHEN json_extract(meta.val,'$.confirmed') OR
      json_extract(excluded.val,'$.confirmed') THEN 'true' ELSE 'false' END)),
  updated_at=MAX(meta.updated_at,excluded.updated_at)`, clearNamePrefix+id, string(raw), at); err != nil {
		return fmt.Errorf("write clear name %s: %w", id, err)
	}
	return nil
}

// ReserveClearName reads all stored names and commits the smallest free choice
// in one immediate transaction. Attaching the initialized cache locks both
// databases before looking, including allocations from other pfm cache homes.
func (s *Store) ReserveClearName(
	ctx context.Context, id, original, cachePath string, at int64,
) (name naming.ClearName, returnErr error) {
	fresh, err := naming.ClearSuccessorName(original, id)
	if err != nil || fresh == "" {
		return name, err
	}
	if s.Degraded() != nil {
		return name, fmt.Errorf("reserve clear name %s: %w", id, s.Degraded())
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return name, fmt.Errorf("acquire clear name connection: %w", err)
	}
	defer func() { returnErr = errors.Join(returnErr, conn.Close()) }()
	if err := clearNameExec(ctx, conn, "ATTACH DATABASE ? AS clear_cache", cachePath); err != nil {
		return name, fmt.Errorf("attach clear name cache: %w", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		returnErr = errors.Join(returnErr, clearNameExec(cleanup, conn, "DETACH DATABASE clear_cache"))
	}()
	if err := clearNameExec(ctx, conn, "BEGIN IMMEDIATE"); err != nil {
		return name, fmt.Errorf("begin clear name reservation: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			returnErr = errors.Join(returnErr, clearNameExec(cleanup, conn, "ROLLBACK"))
		}
	}()
	decisions, err := clearNameDecisions(ctx, conn)
	if err != nil {
		return name, err
	}
	if existing, found := decisions[id]; found {
		return existing, nil // Deferred rollback releases an idempotent read.
	}
	occupied, err := clearNameStoredNames(ctx, conn, id, decisions)
	if err != nil {
		return name, fmt.Errorf("look up occupied clear names: %w", err)
	}
	fresh, err = naming.ClearSuccessorName(original, id, occupied...)
	if err != nil {
		return name, err
	}
	name = naming.ClearName{Inherited: original, Name: fresh}
	raw, err := json.Marshal(name)
	if err != nil {
		return name, fmt.Errorf("encode reserved clear name: %w", err)
	}
	if err := clearNameExec(ctx, conn, "INSERT INTO meta(key,val,updated_at) VALUES(?,?,?)",
		clearNamePrefix+id, string(raw), at); err != nil {
		return name, fmt.Errorf("write clear name reservation: %w", err)
	}
	if err := clearNameExec(ctx, conn, "COMMIT"); err != nil {
		return name, fmt.Errorf("commit clear name reservation: %w", err)
	}
	committed = true
	return name, nil
}

func clearNameDecisions(ctx context.Context, conn *sql.Conn) (decisions map[string]naming.ClearName, returnErr error) {
	rows, err := clearNameQuery(ctx, conn, "SELECT key,val FROM meta WHERE key LIKE ?", clearNamePrefix+"%")
	if err != nil {
		return nil, fmt.Errorf("look up clear name reservations: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close clear name reservations: %w", err))
		}
	}()
	decisions = make(map[string]naming.ClearName)
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, fmt.Errorf("scan clear name reservation: %w", err)
		}
		id := strings.TrimPrefix(key, clearNamePrefix)
		name, err := decodeClearName(id, raw)
		if err != nil {
			return nil, err
		}
		decisions[id] = name
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate clear name reservations: %w", err)
	}
	return decisions, nil
}

func clearNameStoredNames(
	ctx context.Context, conn *sql.Conn, id string, decisions map[string]naming.ClearName,
) (occupied []string, returnErr error) {
	rows, err := clearNameQuery(ctx, conn,
		"SELECT uuid,custom_title,ai_title,first_prompt FROM clear_cache.transcripts WHERE uuid<>?", id)
	if err != nil {
		return nil, fmt.Errorf("query transcript names: %w", err)
	}
	defer func(rows *sql.Rows) {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close transcript names: %w", err))
		}
	}(rows)
	for rows.Next() {
		var otherID, custom, ai, prompt string
		if err := rows.Scan(&otherID, &custom, &ai, &prompt); err != nil {
			return nil, fmt.Errorf("scan transcript name: %w", err)
		}
		if decision, found := decisions[otherID]; found {
			custom = decision.Title(custom)
			delete(decisions, otherID)
		}
		occupied = append(occupied, naming.DisplayName(custom, ai, prompt))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transcript names: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close transcript names: %w", err)
	}
	for otherID, decision := range decisions {
		if otherID != id {
			occupied = append(occupied, decision.Name)
		}
	}
	rows, err = clearNameQuery(ctx, conn, `
SELECT name FROM seat WHERE session_id<>?
UNION ALL SELECT label FROM chat WHERE uuid<>?
UNION ALL SELECT thread_name FROM clear_cache.cx_names WHERE id<>?`, id, id, id)
	if err != nil {
		return nil, fmt.Errorf("query seat and thread names: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close seat and thread names: %w", err))
		}
	}()
	for rows.Next() {
		var value sql.NullString
		if err := rows.Scan(&value); err != nil {
			return nil, fmt.Errorf("scan seat or thread name: %w", err)
		}
		occupied = append(occupied, value.String)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate seat and thread names: %w", err)
	}
	return occupied, nil
}

func clearNameExec(ctx context.Context, conn *sql.Conn, query string, args ...any) error {
	op := obs.SQL(ctx, kind, query)
	result, err := conn.ExecContext(ctx, query, args...)
	op.End(rowsAffected(result, err), err)
	return err
}

func clearNameQuery(ctx context.Context, conn *sql.Conn, query string, args ...any) (*sql.Rows, error) {
	op := obs.SQL(ctx, kind, query)
	rows, err := conn.QueryContext(ctx, query, args...)
	op.End(-1, err)
	return rows, err
}

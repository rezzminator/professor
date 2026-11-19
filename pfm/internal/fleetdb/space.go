package fleetdb

import (
	"context"
	"errors"
	"fmt"
)

// SetSpace records the space a chat was moved into, keyed by its session id so
// the choice survives /clear, resume, restore and reload; an empty space
// clears it, returning the chat to its folder's place.
func (s *Store) SetSpace(ctx context.Context, id, space string, at int64) error {
	if id == "" {
		return fmt.Errorf("record chat space %q: empty chat id", space)
	}
	if s.db == nil {
		return fmt.Errorf("record chat space %q for %q: %w", space, id, s.degraded)
	}
	query, args := "DELETE FROM chat_space WHERE session_id=?", []any{id}
	if space != "" {
		query = `INSERT INTO chat_space(session_id,space,updated_at) VALUES(?,?,?)
ON CONFLICT(session_id) DO UPDATE SET space=excluded.space, updated_at=excluded.updated_at`
		args = []any{id, space, at}
	}
	if _, err := s.exec(ctx, query, args...); err != nil {
		return fmt.Errorf("record chat space %q for %q: %w", space, id, err)
	}
	return nil
}

// Spaces returns every chat's recorded space keyed by chat id.
func (s *Store) Spaces(ctx context.Context) (spaces map[string]string, returnErr error) {
	if errors.Is(s.degraded, ErrAbsent) {
		// Only a read-only open leaves a file absent: one never written holds no spaces.
		return map[string]string{}, nil
	}
	if s.db == nil {
		return nil, fmt.Errorf("query chat spaces: %w", s.degraded)
	}
	rows, err := s.query(ctx, "SELECT session_id, space FROM chat_space")
	if err != nil {
		return nil, fmt.Errorf("query chat spaces: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close chat space rows: %w", err))
		}
	}()
	spaces = make(map[string]string)
	for rows.Next() {
		var id, space string
		if err := rows.Scan(&id, &space); err != nil {
			return nil, fmt.Errorf("scan chat space: %w", err)
		}
		spaces[id] = space
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate chat spaces: %w", err)
	}
	return spaces, nil
}

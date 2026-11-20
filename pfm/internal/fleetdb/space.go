package fleetdb

import (
	"context"
	"errors"
	"fmt"
)

// ChatSpace is one chat's recorded space and when it was recorded; an empty
// Space is a cleared one, kept so it outranks an older record the chat's other
// ids carry (store.Store.ChatSpaces).
type ChatSpace struct {
	Space     string
	UpdatedAt int64
}

// SetSpace records the space a chat was moved into, keyed by its session id so
// the choice survives reload, resume and restore; an empty space records the
// chat cleared, returning it to its folder's place.
func (s *Store) SetSpace(ctx context.Context, id, space string, at int64) error {
	if id == "" {
		return fmt.Errorf("record chat space %q: empty chat id", space)
	}
	if s.db == nil {
		return fmt.Errorf("record chat space %q for %q: %w", space, id, s.degraded)
	}
	query := `INSERT INTO chat_space(session_id,space,updated_at) VALUES(?,?,?)
ON CONFLICT(session_id) DO UPDATE SET space=excluded.space, updated_at=excluded.updated_at`
	if _, err := s.exec(ctx, query, id, space, at); err != nil {
		return fmt.Errorf("record chat space %q for %q: %w", space, id, err)
	}
	return nil
}

// Spaces returns every chat's recorded space keyed by chat id, cleared ones
// included, whether or not the chat still exists: store.Store.ChatSpaces is
// the read placement files by.
func (s *Store) Spaces(ctx context.Context) (spaces map[string]ChatSpace, returnErr error) {
	if errors.Is(s.degraded, ErrAbsent) {
		// Only a read-only open leaves a file absent: one never written holds no spaces.
		return map[string]ChatSpace{}, nil
	}
	if s.db == nil {
		return nil, fmt.Errorf("query chat spaces: %w", s.degraded)
	}
	rows, err := s.query(ctx, "SELECT session_id, space, updated_at FROM chat_space")
	if err != nil {
		return nil, fmt.Errorf("query chat spaces: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close chat space rows: %w", err))
		}
	}()
	spaces = make(map[string]ChatSpace)
	for rows.Next() {
		var id string
		var record ChatSpace
		if err := rows.Scan(&id, &record.Space, &record.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan chat space: %w", err)
		}
		spaces[id] = record
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate chat spaces: %w", err)
	}
	return spaces, nil
}

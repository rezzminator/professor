package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// EpicInjected reports whether one chat session has already received one
// epic manifest. The pair, not manifest content, is the durable dedupe key.
func (s *Store) EpicInjected(ctx context.Context, sessionID, slug string) (bool, error) {
	var found int
	err := s.db.QueryRowContext(ctx,
		"SELECT 1 FROM epic_injections WHERE session_id=? AND slug=?",
		sessionID, slug,
	).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check epic injection %q/%q: %w", sessionID, slug, err)
	}
	return found == 1, nil
}

// RecordEpicInjection durably records a successful manifest injection. A
// repeated pair is an idempotent no-op.
func (s *Store) RecordEpicInjection(ctx context.Context, sessionID, slug string) error {
	if _, err := s.db.ExecContext(ctx, `
INSERT OR IGNORE INTO epic_injections(session_id, slug, injected_at)
	VALUES (?, ?, ?)`, sessionID, slug, s.clockNow().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("record epic injection %q/%q: %w", sessionID, slug, err)
	}
	return nil
}

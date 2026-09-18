package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ChatSummary returns the paid summary for one exact transcript frontier.
func (s *Store) ChatSummary(ctx context.Context, path string, offset int64) (string, bool, error) {
	var summary string
	err := s.logged().QueryRowContext(ctx, `
SELECT summary
FROM chat_summaries
WHERE transcript_path=? AND last_offset=?`, path, offset).Scan(&summary)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read chat summary cache for %q at %d: %w", path, offset, err)
	}
	return summary, true, nil
}

// PutChatSummary stores one completed exchange. Callers never invoke it for a
// working tail, so a partial answer cannot become a cache hit.
func (s *Store) PutChatSummary(ctx context.Context, path string, offset int64, summary string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("chat summary transcript path is empty")
	}
	if offset < 0 {
		return fmt.Errorf("chat summary offset %d is negative", offset)
	}
	if strings.TrimSpace(summary) == "" {
		return fmt.Errorf("chat summary is empty")
	}
	_, err := execWrite(ctx, s.logged(), `
INSERT INTO chat_summaries (transcript_path, last_offset, summary)
VALUES (?, ?, ?)
ON CONFLICT(transcript_path, last_offset) DO UPDATE SET
  summary=excluded.summary`, path, offset, summary)
	if err != nil {
		return fmt.Errorf("write chat summary cache for %q at %d: %w", path, offset, err)
	}
	return nil
}

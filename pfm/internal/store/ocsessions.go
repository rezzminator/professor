package store

import (
	"context"
	"errors"
	"fmt"
)

// The oc_sessions mirror: OpenCode sessions read from opencode.db, the same
// rebuildable derived data as transcripts and rollouts. One writer —
// internal/index — and these queries.

const openCodeSessionColumns = `
  id, title, directory, project_dir, parent_id, agent, model,
  first_prompt, prompt_count, assistant_count, tokens_input, tokens_output,
  cost_millicents, time_created_ms, time_updated_ms, time_archived_ms`

func scanOpenCodeSession(row interface{ Scan(...any) error }) (OpenCodeSession, error) {
	var session OpenCodeSession
	err := row.Scan(
		&session.ID,
		&session.Title,
		&session.Directory,
		&session.ProjectDir,
		&session.ParentID,
		&session.Agent,
		&session.Model,
		&session.FirstPrompt,
		&session.PromptCount,
		&session.AssistantCount,
		&session.TokensInput,
		&session.TokensOutput,
		&session.CostMillicents,
		&session.TimeCreatedMS,
		&session.TimeUpdatedMS,
		&session.TimeArchivedMS,
	)
	if err != nil {
		return OpenCodeSession{}, err
	}
	return session, nil
}

// ReplaceOpenCodeSessions atomically mirrors one indexing pass's full view of
// OpenCode's session store: every session seen on disk is upserted, every row
// no longer present is deleted. A full replace (rather than delta upserts)
// matches the source: OpenCode's database is a single file whose mtime is the
// only change signal pfm gets, so per-row offsets do not exist.
func (s *Store) ReplaceOpenCodeSessions(ctx context.Context, sessions []OpenCodeSession) (err error) {
	return s.WithImmediateTx(ctx, func(tx *ImmediateTx) error {
		rows, err := tx.QueryContext(ctx, "SELECT id FROM oc_sessions")
		if err != nil {
			return fmt.Errorf("query existing oc sessions: %w", err)
		}
		existing := make(map[string]bool)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return errors.Join(fmt.Errorf("scan existing oc session id: %w", err), rows.Close())
			}
			existing[id] = true
		}
		if err := rows.Err(); err != nil {
			return errors.Join(fmt.Errorf("iterate existing oc sessions: %w", err), rows.Close())
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close existing oc session rows: %w", err)
		}

		for index := range sessions {
			session := &sessions[index]
			_, err := tx.ExecContext(ctx, `
INSERT INTO oc_sessions (`+openCodeSessionColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  title=excluded.title,
  directory=excluded.directory,
  project_dir=excluded.project_dir,
  parent_id=excluded.parent_id,
  agent=excluded.agent,
  model=excluded.model,
  first_prompt=excluded.first_prompt,
  prompt_count=excluded.prompt_count,
  assistant_count=excluded.assistant_count,
  tokens_input=excluded.tokens_input,
  tokens_output=excluded.tokens_output,
  cost_millicents=excluded.cost_millicents,
  time_created_ms=excluded.time_created_ms,
  time_updated_ms=excluded.time_updated_ms,
  time_archived_ms=excluded.time_archived_ms`,
				session.ID,
				session.Title,
				session.Directory,
				session.ProjectDir,
				session.ParentID,
				session.Agent,
				session.Model,
				session.FirstPrompt,
				session.PromptCount,
				session.AssistantCount,
				session.TokensInput,
				session.TokensOutput,
				session.CostMillicents,
				session.TimeCreatedMS,
				session.TimeUpdatedMS,
				session.TimeArchivedMS,
			)
			if err != nil {
				return fmt.Errorf("upsert oc session %q: %w", session.ID, err)
			}
			delete(existing, session.ID)
		}

		for id := range existing {
			if _, err := tx.ExecContext(ctx, "DELETE FROM oc_sessions WHERE id = ?", id); err != nil {
				return fmt.Errorf("delete vanished oc session %q: %w", id, err)
			}
		}
		return nil
	})
}

// OpenCodeSessions returns every indexed OpenCode session, newest activity first.
func (s *Store) OpenCodeSessions(ctx context.Context) (sessions []OpenCodeSession, returnErr error) {
	rows, err := s.logged().QueryContext(ctx, `
SELECT `+openCodeSessionColumns+` FROM oc_sessions ORDER BY time_updated_ms DESC`)
	if err != nil {
		return nil, fmt.Errorf("query oc sessions: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close oc session rows: %w", err))
		}
	}()

	sessions = make([]OpenCodeSession, 0)
	for rows.Next() {
		session, err := scanOpenCodeSession(rows)
		if err != nil {
			return nil, fmt.Errorf("scan oc session: %w", err)
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate oc sessions: %w", err)
	}
	return sessions, nil
}

// CountOpenCodeSessions returns the number of indexed OpenCode sessions. A count of
// zero means "no sessions indexed"; whether that is emptiness or a store that
// was never scanned is meta-key business, not this query's.
func (s *Store) CountOpenCodeSessions(ctx context.Context) (int, error) {
	var count int
	err := s.logged().QueryRowContext(ctx, "SELECT COUNT(*) FROM oc_sessions").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count oc sessions: %w", err)
	}
	return count, nil
}

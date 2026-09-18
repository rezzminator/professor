package store

import (
	"context"
	"errors"
	"fmt"
)

// RowCounts is the stable database summary used by doctor and jailed
// idempotence checks.
type RowCounts struct {
	Transcripts   int
	Rollouts      int
	CxNames       int
	Killed        int
	OrphanedKills int
}

// orphanedKilledSource is the single definition of an orphaned kill: a chat in
// the shared store's killed set that resolves to no transcript, rollout, or
// OpenCode session, directly or through a Codex lineage root. Counts, the
// listing, and the prune all read it, so doctor and `killed --prune-orphans`
// can never disagree.
//
// It reads the effective mirror, so a concurrent external kill counts here too;
// every caller refills that mirror first. The engine test the v1 query carried
// is gone with the column: an id matching a lineage root is a live Codex kill
// whatever engine anyone once recorded for it.
const orphanedKilledSource = `
FROM ` + effectiveKilled + ` h
WHERE NOT EXISTS (SELECT 1 FROM transcripts t WHERE t.uuid=h.uuid)
  AND NOT EXISTS (
    SELECT 1 FROM rollouts r
    WHERE r.id=h.uuid OR r.lineage_root=h.uuid
  )
  AND NOT EXISTS (SELECT 1 FROM oc_sessions o WHERE o.id=h.uuid)`

// QuickCheck runs SQLite's bounded integrity probe.
func (s *Store) QuickCheck(ctx context.Context) (string, error) {
	var result string
	if err := s.logged().QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		return "", fmt.Errorf("SQLite quick_check: %w", err)
	}
	return result, nil
}

// Counts reports table sizes and kills whose target row vanished. Killed and
// OrphanedKills come from the shared store; the rest are this binary's cache.
func (s *Store) Counts(ctx context.Context) (RowCounts, error) {
	if err := s.syncEffectiveKilled(ctx); err != nil {
		return RowCounts{}, err
	}
	var counts RowCounts
	queries := []struct {
		query string
		value *int
	}{
		{"SELECT count(*) FROM transcripts", &counts.Transcripts},
		{"SELECT count(*) FROM rollouts", &counts.Rollouts},
		{"SELECT count(*) FROM cx_names", &counts.CxNames},
		{"SELECT count(*) FROM " + effectiveKilled, &counts.Killed},
		{"SELECT count(*) " + orphanedKilledSource, &counts.OrphanedKills},
	}
	for _, query := range queries {
		if err := s.logged().QueryRowContext(ctx, query.query).Scan(query.value); err != nil {
			return RowCounts{}, fmt.Errorf("count fleet rows: %w", err)
		}
	}
	return counts, nil
}

// OrphanedKills lists the kills Counts reports as OrphanedKills, ordered by
// chat ID. The engine is derived, and for an orphan it derives empty by
// definition: no index row is left to name it.
func (s *Store) OrphanedKills(ctx context.Context) ([]Killed, error) {
	if err := s.syncEffectiveKilled(ctx); err != nil {
		return nil, err
	}
	ids, err := s.orphanedKillIDs(ctx)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	killedAt, err := s.state.KilledAt(ctx)
	if err != nil {
		return nil, err
	}
	orphans := make([]Killed, 0, len(ids))
	for _, id := range ids {
		orphans = append(orphans, Killed{ID: id, KilledAt: killedAt[id]})
	}
	return orphans, nil
}

func (s *Store) orphanedKillIDs(ctx context.Context) (ids []string, returnErr error) {
	rows, err := s.logged().QueryContext(
		ctx,
		"SELECT h.uuid "+orphanedKilledSource+" ORDER BY h.uuid",
	)
	if err != nil {
		return nil, fmt.Errorf("query orphaned killed chats: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close orphaned killed rows: %w", err))
		}
	}()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan orphaned killed chat: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate orphaned killed chats: %w", err)
	}
	return ids, nil
}

// DeleteOrphanedKills removes exactly the kills OrphanedKills lists and reports
// how many were removed.
//
// This is the one path besides an unkill that takes a row out of the shared
// store, and it is gated behind `killed --prune-orphans --confirm`. Each removal
// goes through the ordinary unkill, so the carrier file loses the id too: half a
// removal is the drift this store exists to end. There is no transaction around
// the set: holding its write lock across hundreds of rows would stall every
// other chat.
func (s *Store) DeleteOrphanedKills(ctx context.Context) (int, error) {
	if err := s.syncEffectiveKilled(ctx); err != nil {
		return 0, err
	}
	ids, err := s.orphanedKillIDs(ctx)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, id := range ids {
		if err := s.state.Unkill(ctx, id); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

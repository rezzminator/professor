package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// CachedCounts separates explicit kills from rows omitted because
// they are empty, background, or beyond the default resume caps; compose adds
// them to the counts of the rows it builds (compose.Input.CachedCounts).
type CachedCounts struct {
	Killed     int
	Suppressed int
}

// LiveClaims names the chats compose shows as live rows
// (compose.Output.LiveClaims), which it never draws as resumable. A claimed
// chat never takes one of the default view's resume slots, else every live
// chat among the newest pushes the oldest resumable chat out of the capped
// frame the full frame shows; nor is it counted, since compose counts its
// live row itself. Every claimed Codex conversation and each claimed Claude
// chat inside the read window is still returned for compose to match.
type LiveClaims struct {
	// Transcripts holds Claude session ids.
	Transcripts map[string]struct{}
	// Rollouts holds Codex thread ids; a lineage is claimed when its root or
	// any member is.
	Rollouts map[string]struct{}
}

// DefaultCandidates reads only the rows capable of appearing in the default
// cached first frame: up to transcriptLimit resumable Claude chats and
// rolloutLimit resumable Codex conversations beside the claimed ones read,
// and the killed and suppressed totals of every unclaimed chat of the two.
// SQL applies the same kill and empty/background predicates as compose,
// avoiding a full corpus decode before the TUI opens.
func (s *Store) DefaultCandidates(
	ctx context.Context,
	transcriptLimit, rolloutLimit int,
	claims LiveClaims,
) ([]Transcript, []Rollout, CachedCounts, error) {
	// One refill for all three queries below: they run back to back on the
	// same connection, and re-reading the shared store between them could only
	// make the frame disagree with itself.
	if err := s.syncEffectiveKilled(ctx); err != nil {
		return nil, nil, CachedCounts{}, err
	}
	transcripts, err := s.defaultTranscripts(ctx, transcriptLimit, claims.Transcripts)
	if err != nil {
		return nil, nil, CachedCounts{}, err
	}
	rollouts, err := s.defaultRollouts(ctx, rolloutLimit, claims.Rollouts)
	if err != nil {
		return nil, nil, CachedCounts{}, err
	}
	counts, err := s.defaultCounts(ctx, transcriptLimit, rolloutLimit, claims)
	if err != nil {
		return nil, nil, CachedCounts{}, err
	}
	return transcripts, rollouts, counts, nil
}

func (s *Store) defaultTranscripts(
	ctx context.Context,
	limit int,
	claimed map[string]struct{},
) (transcripts []Transcript, returnErr error) {
	rows, err := s.logged().QueryContext(ctx, `
SELECT `+transcriptColumns+`
FROM transcripts AS t
LEFT JOIN `+effectiveKilled+` AS h ON h.uuid=t.uuid
WHERE t.is_bg=0 AND t.size>0 AND t.prompt_count>0 AND h.uuid IS NULL
  AND NOT `+labelKilledSQL+`
  AND NOT `+transcriptSupersededSQL+`
ORDER BY CASE WHEN t.activity_ns>0 THEN t.activity_ns ELSE t.mtime_ns END DESC, t.uuid
LIMIT ?`, limit+len(claimed))
	if err != nil {
		return nil, fmt.Errorf("query cached transcript candidates: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close cached transcript rows: %w", err))
		}
	}()
	// The window above holds limit resumable rows however many of its newest
	// rows are claimed; a claimed row past it is one the live load reads.
	transcripts = make([]Transcript, 0, limit+len(claimed))
	resumable := 0
	for rows.Next() {
		transcript, err := scanTranscript(rows)
		if err != nil {
			return nil, fmt.Errorf("scan cached transcript: %w", err)
		}
		if _, live := claimed[transcript.UUID]; !live {
			if resumable == limit {
				continue
			}
			resumable++
		}
		transcripts = append(transcripts, transcript)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cached transcripts: %w", err)
	}
	return s.clearNames(ctx, transcripts), nil
}

func (s *Store) defaultRollouts(
	ctx context.Context,
	limit int,
	claimed map[string]struct{},
) ([]Rollout, error) {
	lineages, killedByID, cxNames, err := s.codexLineageRows(ctx)
	if err != nil {
		return nil, err
	}
	rollouts := make([]Rollout, 0, min(limit+len(claimed), len(lineages)))
	resumable := 0
	for index := range lineages {
		lineage := &lineages[index]
		if codexLineageKilled(*lineage, killedByID) ||
			codexLineageLabelKilled(*lineage, cxNames) {
			continue
		}
		if codexLineageSuppressed(*lineage) {
			continue
		}
		if !codexLineageClaimed(*lineage, claimed) {
			if resumable == limit {
				continue
			}
			resumable++
		}
		rollouts = append(rollouts, lineage.Newest)
	}
	return rollouts, nil
}

// codexLineageClaimed reports whether a live chat shows any thread of lineage.
func codexLineageClaimed(lineage CodexLineage, claimed map[string]struct{}) bool {
	if _, live := claimed[lineage.RootID]; live {
		return true
	}
	for _, member := range lineage.MemberIDs {
		if _, live := claimed[member]; live {
			return true
		}
	}
	return false
}

// transcriptClaimedSQL is the predicate "t.uuid is claimed" with its
// arguments, false outright when nothing is.
func transcriptClaimedSQL(claimed map[string]struct{}) (string, []any) {
	if len(claimed) == 0 {
		return "0", nil
	}
	ids := make([]string, 0, len(claimed))
	for id := range claimed {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	args := make([]any, len(ids))
	for index, id := range ids {
		args[index] = id
	}
	return "t.uuid IN (?" + strings.Repeat(",?", len(ids)-1) + ")", args
}

func (s *Store) defaultCounts(
	ctx context.Context,
	transcriptLimit, rolloutLimit int,
	claims LiveClaims,
) (CachedCounts, error) {
	var counts CachedCounts
	var claudeEligible, codexEligible int
	claimedSQL, claimedArgs := transcriptClaimedSQL(claims.Transcripts)
	// A label-killed row counts as HIDDEN, not suppressed, and never as
	// eligible — compose's countOmitted tests row.Killed first for the same
	// reason: killed is dead, empty or not. A claimed row counts nowhere:
	// compose counts the live row that claims it.
	err := s.logged().QueryRowContext(ctx, `
SELECT
  COALESCE(SUM(CASE
    WHEN `+transcriptSupersededSQL+` THEN 0
    WHEN h.uuid IS NOT NULL OR `+labelKilledSQL+`
    THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE
    WHEN h.uuid IS NULL AND NOT `+labelKilledSQL+` AND NOT `+transcriptSupersededSQL+`
      AND (t.is_bg!=0 OR t.size<=0 OR t.prompt_count<=0)
    THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE
    WHEN h.uuid IS NULL AND NOT `+labelKilledSQL+` AND NOT `+transcriptSupersededSQL+`
      AND t.is_bg=0 AND t.size>0 AND t.prompt_count>0
    THEN 1 ELSE 0 END), 0)
FROM transcripts AS t
LEFT JOIN `+effectiveKilled+` AS h ON h.uuid=t.uuid
WHERE NOT `+claimedSQL,
		claimedArgs...,
	).Scan(&counts.Killed, &counts.Suppressed, &claudeEligible)
	if err != nil {
		return CachedCounts{}, fmt.Errorf("count cached transcripts: %w", err)
	}
	lineages, killedByID, cxNames, err := s.codexLineageRows(ctx)
	if err != nil {
		return CachedCounts{}, err
	}
	var codexKilled, codexSuppressed int
	for index := range lineages {
		lineage := &lineages[index]
		if codexLineageClaimed(*lineage, claims.Rollouts) {
			continue
		}
		if codexLineageKilled(*lineage, killedByID) ||
			codexLineageLabelKilled(*lineage, cxNames) {
			codexKilled++
			continue
		}
		if codexLineageSuppressed(*lineage) {
			codexSuppressed++
			continue
		}
		codexEligible++
	}
	counts.Killed += codexKilled
	counts.Suppressed += codexSuppressed
	if claudeEligible > transcriptLimit {
		counts.Suppressed += claudeEligible - transcriptLimit
	}
	if codexEligible > rolloutLimit {
		counts.Suppressed += codexEligible - rolloutLimit
	}
	return counts, nil
}

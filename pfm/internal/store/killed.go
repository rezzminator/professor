package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	modernsqlite "modernc.org/sqlite"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/fleetdb"
	"hostops/pfm/internal/obs"
)

const (
	sqliteBusyCode = 5
	busyRetryDelay = 25 * time.Millisecond

	// effectiveKilled is a per-connection mirror of the shared store's killed
	// set, so the cached
	// first-frame queries can still express "not killed" as a join. It is
	// refilled from the shared store on every read that consults it, which is
	// what lets a concurrent kill show up with no sync step in between.
	effectiveKilled = "temp.effective_killed"

	effectiveKilledDDL = `
CREATE TABLE IF NOT EXISTS ` + effectiveKilled + `(
  uuid TEXT PRIMARY KEY,
  killed_at INTEGER NOT NULL
)`
)

// Kill records a permanent kill or a /clear prompt-baseline kill in the shared
// store. A persistent SQLITE_BUSY is retried, reported, and returned so a
// caller never reports an unrecorded decision as durable.
func (s *Store) Kill(ctx context.Context, killed Killed) error {
	if killed.BaselinePrompts != nil {
		baseline := *killed.BaselinePrompts
		return s.killedWrite(ctx, "kill", killed.ID, func() error {
			return s.state.KillUntilPrompt(ctx, killed.ID, killed.KilledAt, baseline)
		})
	}
	return s.killedWrite(ctx, "kill", killed.ID, func() error {
		return s.state.Kill(ctx, killed.ID, killed.KilledAt)
	})
}

// Unkill removes a kill from the shared store under the same busy policy.
func (s *Store) Unkill(ctx context.Context, id string) error {
	return s.killedWrite(ctx, "unkill", id, func() error {
		return s.state.Unkill(ctx, id)
	})
}

func (s *Store) killedWrite(
	ctx context.Context,
	action string,
	id string,
	write func() error,
) error {
	// The ONLY busy-retry loop in the package, so Retries lives here: the
	// fleetdb statement underneath write() already records its own verb and
	// table, and this record adds the retry count around the whole attempt.
	op := obs.SQL(ctx, storeKind, "UPDATE hidden")
	err := write()
	if !isSQLiteBusy(err) {
		op.End(1, err)
		return err
	}

	timer := s.clockTimer(busyRetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		op.End(1, ctx.Err())
		return ctx.Err()
	case <-timer.C():
	}

	op.Retries++
	err = write()
	if !isSQLiteBusy(err) {
		op.End(1, err)
		return err
	}

	op.End(1, err)
	s.warningf(
		"WARNING: pfm could not %s %q in %s: SQLite remained busy after "+
			"retry; the change was NOT written\n",
		action,
		id,
		s.state.Path(),
	)
	counterCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if counterErr := s.IncrementMeta(counterCtx, "busy_"+action+"_warnings"); counterErr != nil {
		s.warningf(
			"WARNING: pfm could not record the busy-warning counter for %s %q in %s: %v\n",
			action,
			id,
			s.state.Path(),
			counterErr,
		)
	}
	return err
}

func isSQLiteBusy(err error) bool {
	var sqliteError *modernsqlite.Error
	return errors.As(err, &sqliteError) &&
		sqliteError.Code()&0xff == sqliteBusyCode
}

// UpsertKilled inserts or replaces a row in the RETIRED local killed table.
// Only the v1→v2 Codex lineage migration calls it, and only to tidy rows that
// the shared store adopts during migration; no live kill reaches this table.
func (tx *ImmediateTx) UpsertKilled(ctx context.Context, killed Killed) error {
	return upsertKilled(ctx, tx, killed)
}

func upsertKilled(ctx context.Context, db queryExecer, killed Killed) error {
	_, err := execWrite(ctx, db, `
INSERT INTO hidden (id, engine, hidden_at, baseline_prompts)
VALUES (?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  engine=excluded.engine,
  hidden_at=excluded.hidden_at,
  baseline_prompts=excluded.baseline_prompts`,
		killed.ID,
		string(killed.Engine),
		killed.KilledAt,
		killed.BaselinePrompts,
	)
	if err != nil {
		return fmt.Errorf("upsert killed chat %q: %w", killed.ID, err)
	}
	return nil
}

// Killed reports whether one chat is killed, by exact chat ID.
func (s *Store) Killed(ctx context.Context, id string) (Killed, bool, error) {
	records, err := s.activeKilledRecords(ctx)
	if err != nil {
		return Killed{}, false, err
	}
	record, found := records[id]
	if !found {
		return Killed{}, false, nil
	}
	engines, err := s.deriveEngines(ctx, []string{id})
	if err != nil {
		return Killed{}, false, err
	}
	return Killed{
		ID: id, Engine: engines[id], KilledAt: record.KilledAt,
		BaselinePrompts: record.AtPayload,
	}, true, nil
}

// KilledChats returns the shared database's killed rows ordered by chat ID.
func (s *Store) KilledChats(ctx context.Context) ([]Killed, error) {
	records, err := s.activeKilledRecords(ctx)
	if err != nil {
		return nil, err
	}
	killedAt := make(map[string]int64, len(records))
	for id, record := range records {
		killedAt[id] = record.KilledAt
	}
	ids := fleetdb.SortedIDs(killedAt)
	engines, err := s.deriveEngines(ctx, ids)
	if err != nil {
		return nil, err
	}
	killedChats := make([]Killed, 0, len(ids))
	for _, id := range ids {
		record := records[id]
		killedChats = append(killedChats, Killed{
			ID:              id,
			Engine:          engines[id],
			KilledAt:        record.KilledAt,
			BaselinePrompts: record.AtPayload,
		})
	}
	if len(killedChats) == 0 {
		return nil, nil
	}
	return killedChats, nil
}

// activeKilledRecords expires prompt-baseline kills whose indexed transcript
// has grown. The conditional shared-store delete protects a concurrent newer
// /clear or explicit permanent kill from the stale read/delete race.
func (s *Store) activeKilledRecords(
	ctx context.Context,
) (map[string]fleetdb.KilledRecord, error) {
	for attempt := 0; attempt < 3; attempt++ {
		records, err := s.state.KilledRecords(ctx)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(records))
		for id, record := range records {
			if record.AtPayload != nil {
				ids = append(ids, id)
			}
		}
		counts, err := s.transcriptPromptCounts(ctx, ids)
		if err != nil {
			return nil, err
		}
		raced := false
		for id, record := range records {
			if record.AtPayload == nil || counts[id] <= *record.AtPayload {
				continue
			}
			removed := false
			err := s.killedWrite(ctx, "auto-unkill", id, func() error {
				var deleteErr error
				removed, deleteErr = s.state.UnkillIfPayload(ctx, id, *record.AtPayload)
				return deleteErr
			})
			if err != nil {
				return nil, err
			}
			if removed {
				delete(records, id)
			} else {
				raced = true
			}
		}
		if !raced {
			return records, nil
		}
	}
	return nil, errors.New("clear-kill state changed repeatedly during auto-unkill")
}

func (s *Store) transcriptPromptCounts(
	ctx context.Context,
	ids []string,
) (map[string]int64, error) {
	counts := make(map[string]int64, len(ids))
	for start := 0; start < len(ids); start += BatchSize {
		end := min(start+BatchSize, len(ids))
		chunk := ids[start:end]
		arguments := make([]any, len(chunk))
		for index, id := range chunk {
			arguments[index] = id
		}
		rows, err := s.logged().QueryContext(
			ctx,
			"SELECT uuid,prompt_count FROM transcripts WHERE uuid IN ("+placeholders(len(chunk))+")",
			arguments...,
		)
		if err != nil {
			return nil, fmt.Errorf("query clear-kill prompt counts: %w", err)
		}
		for rows.Next() {
			var id string
			var count int64
			if err := rows.Scan(&id, &count); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan clear-kill prompt count: %w", err)
			}
			counts[id] = count
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close clear-kill prompt counts: %w", err)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate clear-kill prompt counts: %w", err)
		}
	}
	unresolved := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, found := counts[id]; !found {
			unresolved[id] = struct{}{}
		}
	}
	if len(unresolved) == 0 {
		return counts, nil
	}
	rollouts, err := s.Rollouts(ctx)
	if err != nil {
		return nil, fmt.Errorf("query clear-kill Codex prompt counts: %w", err)
	}
	lineages, _ := ResolveCodexLineages(rollouts)
	for index := range lineages {
		lineage := &lineages[index]
		if _, found := unresolved[lineage.RootID]; found {
			counts[lineage.RootID] = lineage.PromptCount
		}
		for _, member := range lineage.MemberIDs {
			if _, found := unresolved[member]; found {
				counts[member] = lineage.PromptCount
			}
		}
	}
	return counts, nil
}

// deriveEngines answers which engine owns an id from the index rather than from a
// stored column. The shared killed table is keyed by uuid alone, so the engine
// is read back out of whichever index table claims the id:
// a transcript uses engine.Claude, a rollout or lineage root uses engine.Codex,
// and an OpenCode mirror row uses engine.OpenCode. An id no table knows is an
// orphaned kill and keeps an empty engine, which
// compose reads as "killed whatever the engine".
func (s *Store) deriveEngines(
	ctx context.Context,
	ids []string,
) (map[string]pfmengine.ID, error) {
	engines := make(map[string]pfmengine.ID, len(ids))
	if len(ids) == 0 {
		return engines, nil
	}
	for start := 0; start < len(ids); start += BatchSize {
		end := min(start+BatchSize, len(ids))
		chunk := ids[start:end]
		if err := s.deriveEngineChunk(ctx, chunk, engines); err != nil {
			return nil, err
		}
	}
	return engines, nil
}

func (s *Store) deriveEngineChunk(
	ctx context.Context,
	ids []string,
	engines map[string]pfmengine.ID,
) (returnErr error) {
	marks := placeholders(len(ids))
	query := `
SELECT uuid, ? FROM transcripts WHERE uuid IN (` + marks + `)
UNION ALL
SELECT id, ? FROM rollouts WHERE id IN (` + marks + `)
UNION ALL
SELECT lineage_root, ? FROM rollouts WHERE lineage_root IN (` + marks + `)
UNION ALL
SELECT id, ? FROM cx_names WHERE id IN (` + marks + `)
UNION ALL
SELECT id, ? FROM oc_sessions WHERE id IN (` + marks + `)`
	// One bound id list per IN clause: five clauses, five copies.
	arguments := make([]any, 0, (len(ids)+1)*5)
	for _, id := range []pfmengine.ID{pfmengine.Claude, pfmengine.Codex, pfmengine.Codex, pfmengine.Codex, pfmengine.OpenCode} {
		arguments = append(arguments, string(id))
		for _, id := range ids {
			arguments = append(arguments, id)
		}
	}

	rows, err := s.logged().QueryContext(ctx, query, arguments...)
	if err != nil {
		return fmt.Errorf("derive killed chat engines: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close killed chat engine rows: %w", err))
		}
	}()
	for rows.Next() {
		var id, engine string
		if err := rows.Scan(&id, &engine); err != nil {
			return fmt.Errorf("scan killed chat engine: %w", err)
		}
		engineID, err := pfmengine.Parse(engine)
		if err != nil {
			return fmt.Errorf("fleet.db row %s: %w", id, err)
		}
		// A transcript wins a collision, whatever order the union arms come
		// back in: SQL does not promise that order, and an id that resolved
		// one way this run and the other way next run would flicker a chat in
		// and out of the listing.
		_, alreadyDerived := engines[id]
		if engineID == pfmengine.Claude || !alreadyDerived {
			engines[id] = engineID
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate killed chat engines: %w", err)
	}
	return nil
}

// scanKilled reads a row of the RETIRED local killed table, which still has the
// engine and baseline_prompts columns. The v1→v2 Codex lineage migration and
// the orphan report are its only readers.
func scanKilled(row rowScanner) (Killed, error) {
	var killed Killed
	var engine string
	var baseline sql.NullInt64
	err := row.Scan(
		&killed.ID,
		&engine,
		&killed.KilledAt,
		&baseline,
	)
	if baseline.Valid {
		killed.BaselinePrompts = &baseline.Int64
	}
	if err != nil {
		return killed, err
	}
	id, err := pfmengine.Parse(engine)
	if err != nil {
		return Killed{}, fmt.Errorf("fleet.db row %s: %w", killed.ID, err)
	}
	killed.Engine = id
	return killed, nil
}

func placeholders(count int) string {
	if count <= 0 {
		return "NULL"
	}
	marks := make([]byte, 0, count*2-1)
	for index := 0; index < count; index++ {
		if index > 0 {
			marks = append(marks, ',')
		}
		marks = append(marks, '?')
	}
	return string(marks)
}

// syncEffectiveKilled refills the per-connection mirror of the shared killed
// set. Every query that joins against effectiveKilled calls this first: the
// mirror is a query convenience, never a second copy of the truth.
func (s *Store) syncEffectiveKilled(ctx context.Context) error {
	records, err := s.activeKilledRecords(ctx)
	if err != nil {
		return err
	}
	if _, err := s.logged().ExecContext(ctx, effectiveKilledDDL); err != nil {
		return fmt.Errorf("create effective killed mirror: %w", err)
	}
	if _, err := s.logged().ExecContext(
		ctx,
		"DELETE FROM "+effectiveKilled,
	); err != nil {
		return fmt.Errorf("clear effective killed mirror: %w", err)
	}
	ids := make([]string, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for start := 0; start < len(ids); start += BatchSize {
		end := min(start+BatchSize, len(ids))
		arguments := make([]any, 0, (end-start)*2)
		values := make([]byte, 0, (end-start)*8)
		for index := start; index < end; index++ {
			if index > start {
				values = append(values, ',')
			}
			values = append(values, "(?,?)"...)
			arguments = append(arguments, ids[index], records[ids[index]].KilledAt)
		}
		if _, err := s.logged().ExecContext(
			ctx,
			"INSERT INTO "+effectiveKilled+"(uuid,killed_at) VALUES "+string(values),
			arguments...,
		); err != nil {
			return fmt.Errorf("fill effective killed mirror: %w", err)
		}
	}
	return nil
}

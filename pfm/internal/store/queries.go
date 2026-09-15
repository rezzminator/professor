package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/naming"
)

// transcriptColumns is alias-qualified because every transcript query joins
// the effective-killed mirror, whose key column is also named uuid.
const transcriptColumns = `
t.uuid, t.path, t.size, t.mtime_ns, t.activity_ns, t.parsed_offset, t.cwd, t.custom_title,
t.ai_title, t.first_prompt, t.last_prompt, t.prompt_count, t.is_bg`

const rolloutColumns = `
id, path, size, mtime_ns, parsed_offset, cwd, user_thread, session_id,
parent_thread, lineage_root, first_prompt, prompt_count, is_bg`

// transcriptLabelSQL is naming.DisplayName's precedence in SQL, and
// labelKilledSQL is naming.LabelKilled over it. Both are built from the
// naming constants rather than spelled out, so the cached frame cannot drift
// from compose's applyKill when the prefix changes. upper() is ASCII-only in
// SQLite, which is exactly the case-folding LabelKilled does.
const transcriptLabelSQL = `COALESCE(NULLIF(t.custom_title,''), NULLIF(t.ai_title,''), t.first_prompt)`

var labelKilledSQL = fmt.Sprintf(
	`upper(substr(%s, 1, %d))=%s`,
	transcriptLabelSQL,
	len(naming.KillPrefix),
	"'"+strings.ToUpper(naming.KillPrefix)+"'",
)

type rowScanner interface {
	Scan(...any) error
}

// CachedCounts separates explicit kills from rows omitted because
// they are empty, background, or beyond the default resume caps.
type CachedCounts struct {
	Killed     int
	Suppressed int
}

// DefaultCandidates reads only the rows capable of appearing in the default
// cached first frame. SQL applies the same kill and empty/background
// predicates as compose, avoiding a full corpus decode before the TUI opens.
func (s *Store) DefaultCandidates(
	ctx context.Context,
	transcriptLimit, rolloutLimit int,
) ([]Transcript, []Rollout, CachedCounts, error) {
	// One refill for all three queries below: they run back to back on the
	// same connection, and re-reading the shared store between them could only
	// make the frame disagree with itself.
	if err := s.syncEffectiveKilled(ctx); err != nil {
		return nil, nil, CachedCounts{}, err
	}
	transcripts, err := s.defaultTranscripts(ctx, transcriptLimit)
	if err != nil {
		return nil, nil, CachedCounts{}, err
	}
	rollouts, err := s.defaultRollouts(ctx, rolloutLimit)
	if err != nil {
		return nil, nil, CachedCounts{}, err
	}
	counts, err := s.defaultCounts(ctx, transcriptLimit, rolloutLimit)
	if err != nil {
		return nil, nil, CachedCounts{}, err
	}
	return transcripts, rollouts, counts, nil
}

func (s *Store) defaultTranscripts(
	ctx context.Context,
	limit int,
) (transcripts []Transcript, returnErr error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT `+transcriptColumns+`
FROM transcripts AS t
LEFT JOIN `+effectiveKilled+` AS h ON h.uuid=t.uuid
WHERE t.is_bg=0 AND t.size>0 AND t.prompt_count>0 AND h.uuid IS NULL
  AND NOT `+labelKilledSQL+`
ORDER BY CASE WHEN t.activity_ns>0 THEN t.activity_ns ELSE t.mtime_ns END DESC, t.uuid
LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("query cached transcript candidates: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close cached transcript rows: %w", err))
		}
	}()
	transcripts = make([]Transcript, 0, limit)
	for rows.Next() {
		transcript, err := scanTranscript(rows)
		if err != nil {
			return nil, fmt.Errorf("scan cached transcript: %w", err)
		}
		transcripts = append(transcripts, transcript)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cached transcripts: %w", err)
	}
	return transcripts, nil
}

func (s *Store) defaultRollouts(
	ctx context.Context,
	limit int,
) ([]Rollout, error) {
	lineages, killedByID, cxNames, err := s.codexLineageRows(ctx)
	if err != nil {
		return nil, err
	}
	rollouts := make([]Rollout, 0, min(limit, len(lineages)))
	for _, lineage := range lineages {
		if codexLineageKilled(lineage, killedByID) ||
			codexLineageLabelKilled(lineage, cxNames) {
			continue
		}
		if codexLineageSuppressed(lineage) {
			continue
		}
		rollouts = append(rollouts, lineage.Newest)
		if len(rollouts) == limit {
			break
		}
	}
	return rollouts, nil
}

func (s *Store) defaultCounts(
	ctx context.Context,
	transcriptLimit, rolloutLimit int,
) (CachedCounts, error) {
	var counts CachedCounts
	var claudeEligible, codexEligible int
	// A label-killed row counts as HIDDEN, not suppressed, and never as
	// eligible — compose's countOmitted tests row.Killed first for the same
	// reason: killed is dead, empty or not.
	err := s.db.QueryRowContext(ctx, `
SELECT
  COALESCE(SUM(CASE
    WHEN h.uuid IS NOT NULL OR `+labelKilledSQL+`
    THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE
    WHEN h.uuid IS NULL AND NOT `+labelKilledSQL+`
      AND (t.is_bg!=0 OR t.size<=0 OR t.prompt_count<=0)
    THEN 1 ELSE 0 END), 0),
  COALESCE(SUM(CASE
    WHEN h.uuid IS NULL AND NOT `+labelKilledSQL+`
      AND t.is_bg=0 AND t.size>0 AND t.prompt_count>0
    THEN 1 ELSE 0 END), 0)
FROM transcripts AS t
LEFT JOIN `+effectiveKilled+` AS h ON h.uuid=t.uuid`,
	).Scan(&counts.Killed, &counts.Suppressed, &claudeEligible)
	if err != nil {
		return CachedCounts{}, fmt.Errorf("count cached transcripts: %w", err)
	}
	lineages, killedByID, cxNames, err := s.codexLineageRows(ctx)
	if err != nil {
		return CachedCounts{}, err
	}
	var codexKilled, codexSuppressed int
	for _, lineage := range lineages {
		if codexLineageKilled(lineage, killedByID) ||
			codexLineageLabelKilled(lineage, cxNames) {
			codexKilled++
			continue
		}
		if codexLineageSuppressed(lineage) {
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

// codexLineageSuppressed is the cached frame's copy of compose's
// default-eligibility test for a Codex conversation, and the two must stay
// identical: a row the SQL half omits and compose keeps (or the reverse) is a
// chat that flickers between the cached first frame and the rescan.
func codexLineageSuppressed(lineage CodexLineage) bool {
	return lineage.Newest.IsBG ||
		lineage.Newest.Size <= 0 ||
		lineage.PromptCount <= 0
}

// codexLineageKilled is the cached frame's copy of compose's own kill test
// for a Codex conversation (composer.killedMatch, compose/compose.go), and
// the two must stay identical for the same reason codexLineageSuppressed
// does: a lineage the SQL half counts killed and compose does not (or the
// reverse) is a chat that flickers between the cached first frame and the
// rescan.
//
// A live Codex process can expose the raw id of its current rollout — the
// resumed CHILD's id on a
// multi-file lineage, not the root this cache keys kills on — so the root
// alone is not enough; every member id must be checked too. A kill the
// `kill` manager writes is already normalized onto the root
// (internal/kill/manager.go), so this is only ever a WIDER match, never a
// narrower one.
func codexLineageKilled(lineage CodexLineage, killedByID map[string]Killed) bool {
	if killedApplies(killedByID[lineage.RootID], lineage.PromptCount) {
		return true
	}
	for _, member := range lineage.MemberIDs {
		if killedApplies(killedByID[member], lineage.PromptCount) {
			return true
		}
	}
	return false
}

func killedApplies(killed Killed, promptCount int64) bool {
	return killed.ID != "" &&
		(killed.BaselinePrompts == nil || promptCount <= *killed.BaselinePrompts)
}

// codexLineageLabelKilled is the cached frame's copy of the "_KILL…" label
// test compose's applyKill runs on the row it composes, and the two must stay
// identical for the same reason codexLineageKilled does. Both name the lineage
// through naming.CodexRowName, so only the test itself lives twice, never the
// naming walk.
func codexLineageLabelKilled(
	lineage CodexLineage,
	cxNames map[string]string,
) bool {
	return naming.LabelKilled(naming.CodexRowName(
		lineage.Newest.ID,
		lineage.Newest.SessionID,
		lineage.Newest.ParentThread,
		lineage.RootID,
		lineage.Newest.FirstPrompt,
		cxNames,
	))
}

func (s *Store) codexLineageRows(
	ctx context.Context,
) ([]CodexLineage, map[string]Killed, map[string]string, error) {
	rollouts, err := s.Rollouts(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("query cached rollout lineages: %w", err)
	}
	lineages, _ := ResolveCodexLineages(rollouts)
	sort.Slice(lineages, func(left, right int) bool {
		if lineages[left].Newest.MTimeNS != lineages[right].Newest.MTimeNS {
			return lineages[left].Newest.MTimeNS > lineages[right].Newest.MTimeNS
		}
		return lineages[left].RootID < lineages[right].RootID
	})
	killedRows, err := s.KilledChats(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("query cached lineage kills: %w", err)
	}
	cxNames, err := s.CxNames(ctx)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("query cached lineage names: %w", err)
	}
	// Anything but a derived engine.Claude counts. An engine derives empty when neither
	// index table claims the id, and an empty engine means "killed whatever the
	// engine" everywhere else in the tree (compose.go:665) — a kill must not
	// quietly lapse because the row that named its engine was pruned.
	killedByID := make(map[string]Killed, len(killedRows))
	for _, killed := range killedRows {
		if killed.Engine != pfmengine.Claude {
			killedByID[killed.ID] = killed
		}
	}
	return lineages, killedByID, cxNames, nil
}

// CodexThreads projects rollout rows onto the naming package's own thread
// shape, so the Codex name index can be built without naming depending on
// this package.
func CodexThreads(rollouts []Rollout) []naming.CodexThread {
	threads := make([]naming.CodexThread, 0, len(rollouts))
	for _, rollout := range rollouts {
		threads = append(threads, naming.CodexThread{
			ID:           rollout.ID,
			SessionID:    rollout.SessionID,
			ParentThread: rollout.ParentThread,
			Path:         rollout.Path,
			FirstPrompt:  rollout.FirstPrompt,
		})
	}
	return threads
}

// UpsertTranscript inserts or replaces all indexed fields for a transcript.
func (s *Store) UpsertTranscript(ctx context.Context, transcript Transcript) error {
	return upsertTranscript(ctx, s.db, transcript)
}

// UpsertTranscript inserts or replaces all indexed fields within tx.
func (tx *ImmediateTx) UpsertTranscript(ctx context.Context, transcript Transcript) error {
	return upsertTranscript(ctx, tx, transcript)
}

func upsertTranscript(ctx context.Context, db queryExecer, transcript Transcript) error {
	_, err := execWrite(ctx, db, `
INSERT INTO transcripts (
  uuid, path, size, mtime_ns, activity_ns, parsed_offset, cwd, custom_title, ai_title,
  first_prompt, last_prompt, prompt_count, is_bg
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(uuid) DO UPDATE SET
  path=excluded.path,
  size=excluded.size,
  mtime_ns=excluded.mtime_ns,
  activity_ns=excluded.activity_ns,
  parsed_offset=excluded.parsed_offset,
  cwd=excluded.cwd,
  custom_title=excluded.custom_title,
  ai_title=excluded.ai_title,
  first_prompt=excluded.first_prompt,
  last_prompt=excluded.last_prompt,
  prompt_count=excluded.prompt_count,
  is_bg=excluded.is_bg`,
		transcript.UUID,
		transcript.Path,
		transcript.Size,
		transcript.MTimeNS,
		transcript.ActivityNS,
		transcript.ParsedOffset,
		transcript.CWD,
		transcript.CustomTitle,
		transcript.AITitle,
		transcript.FirstPrompt,
		transcript.LastPrompt,
		transcript.PromptCount,
		boolInteger(transcript.IsBG),
	)
	if err != nil {
		return fmt.Errorf("upsert transcript %q: %w", transcript.UUID, err)
	}
	return nil
}

// Transcript returns a transcript by UUID.
func (s *Store) Transcript(
	ctx context.Context,
	uuid string,
) (Transcript, bool, error) {
	transcript, err := scanTranscript(s.db.QueryRowContext(
		ctx,
		"SELECT "+transcriptColumns+" FROM transcripts AS t WHERE t.uuid=?",
		uuid,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Transcript{}, false, nil
	}
	if err != nil {
		return Transcript{}, false, fmt.Errorf("query transcript %q: %w", uuid, err)
	}
	return transcript, true, nil
}

// Transcripts returns all transcripts ordered by UUID.
func (s *Store) Transcripts(ctx context.Context) (transcripts []Transcript, returnErr error) {
	rows, err := s.db.QueryContext(
		ctx,
		"SELECT "+transcriptColumns+" FROM transcripts AS t ORDER BY t.uuid",
	)
	if err != nil {
		return nil, fmt.Errorf("query transcripts: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close transcript rows: %w", err))
		}
	}()

	for rows.Next() {
		transcript, err := scanTranscript(rows)
		if err != nil {
			return nil, fmt.Errorf("scan transcript: %w", err)
		}
		transcripts = append(transcripts, transcript)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate transcripts: %w", err)
	}
	return transcripts, nil
}

func scanTranscript(row rowScanner) (Transcript, error) {
	var transcript Transcript
	var isBG int
	err := row.Scan(
		&transcript.UUID,
		&transcript.Path,
		&transcript.Size,
		&transcript.MTimeNS,
		&transcript.ActivityNS,
		&transcript.ParsedOffset,
		&transcript.CWD,
		&transcript.CustomTitle,
		&transcript.AITitle,
		&transcript.FirstPrompt,
		&transcript.LastPrompt,
		&transcript.PromptCount,
		&isBG,
	)
	transcript.IsBG = isBG != 0
	return transcript, err
}

// DeleteTranscript deletes a transcript by UUID.
func (s *Store) DeleteTranscript(ctx context.Context, uuid string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM transcripts WHERE uuid=?", uuid); err != nil {
		return fmt.Errorf("delete transcript %q: %w", uuid, err)
	}
	return nil
}

// DeleteTranscript deletes a transcript by UUID within tx.
func (tx *ImmediateTx) DeleteTranscript(ctx context.Context, uuid string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM transcripts WHERE uuid=?", uuid); err != nil {
		return fmt.Errorf("delete transcript %q: %w", uuid, err)
	}
	return nil
}

// UpsertRollout inserts or replaces all indexed fields for a rollout.
func (s *Store) UpsertRollout(ctx context.Context, rollout Rollout) error {
	return upsertRollout(ctx, s.db, rollout)
}

// UpsertRollout inserts or replaces all indexed fields within tx.
func (tx *ImmediateTx) UpsertRollout(ctx context.Context, rollout Rollout) error {
	return upsertRollout(ctx, tx, rollout)
}

func upsertRollout(ctx context.Context, db queryExecer, rollout Rollout) error {
	_, err := execWrite(ctx, db, `
INSERT OR REPLACE INTO rollouts (
  id, path, size, mtime_ns, parsed_offset, cwd, user_thread, session_id,
  parent_thread, lineage_root, first_prompt, prompt_count, is_bg
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rollout.ID,
		rollout.Path,
		rollout.Size,
		rollout.MTimeNS,
		rollout.ParsedOffset,
		rollout.CWD,
		boolInteger(rollout.UserThread),
		rollout.SessionID,
		rollout.ParentThread,
		initialLineageRoot(rollout),
		rollout.FirstPrompt,
		rollout.PromptCount,
		boolInteger(rollout.IsBG),
	)
	if err != nil {
		return fmt.Errorf("upsert rollout %q: %w", rollout.ID, err)
	}
	return nil
}

// Rollout returns a rollout by ID.
func (s *Store) Rollout(ctx context.Context, id string) (Rollout, bool, error) {
	rollout, err := scanRollout(s.db.QueryRowContext(
		ctx,
		"SELECT "+rolloutColumns+" FROM rollouts WHERE id=?",
		id,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Rollout{}, false, nil
	}
	if err != nil {
		return Rollout{}, false, fmt.Errorf("query rollout %q: %w", id, err)
	}
	return rollout, true, nil
}

// Rollouts returns all rollouts ordered by ID.
func (s *Store) Rollouts(ctx context.Context) (rollouts []Rollout, returnErr error) {
	rows, err := s.db.QueryContext(
		ctx,
		"SELECT "+rolloutColumns+" FROM rollouts ORDER BY id",
	)
	if err != nil {
		return nil, fmt.Errorf("query rollouts: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close rollout rows: %w", err))
		}
	}()

	for rows.Next() {
		rollout, err := scanRollout(rows)
		if err != nil {
			return nil, fmt.Errorf("scan rollout: %w", err)
		}
		rollouts = append(rollouts, rollout)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate rollouts: %w", err)
	}
	return rollouts, nil
}

func scanRollout(row rowScanner) (Rollout, error) {
	var rollout Rollout
	var userThread, isBG int
	err := row.Scan(
		&rollout.ID,
		&rollout.Path,
		&rollout.Size,
		&rollout.MTimeNS,
		&rollout.ParsedOffset,
		&rollout.CWD,
		&userThread,
		&rollout.SessionID,
		&rollout.ParentThread,
		&rollout.LineageRoot,
		&rollout.FirstPrompt,
		&rollout.PromptCount,
		&isBG,
	)
	rollout.UserThread = userThread != 0
	rollout.IsBG = isBG != 0
	return rollout, err
}

// DeleteRollout deletes a rollout by ID.
func (s *Store) DeleteRollout(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM rollouts WHERE id=?", id); err != nil {
		return fmt.Errorf("delete rollout %q: %w", id, err)
	}
	return nil
}

// DeleteRollout deletes a rollout by ID within tx.
func (tx *ImmediateTx) DeleteRollout(ctx context.Context, id string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM rollouts WHERE id=?", id); err != nil {
		return fmt.Errorf("delete rollout %q: %w", id, err)
	}
	return nil
}

// UpsertCxName inserts or replaces a Codex thread name.
func (s *Store) UpsertCxName(ctx context.Context, name CxName) error {
	return upsertCxName(ctx, s.db, name)
}

// UpsertCxName inserts or replaces a Codex thread name within tx.
func (tx *ImmediateTx) UpsertCxName(ctx context.Context, name CxName) error {
	return upsertCxName(ctx, tx, name)
}

func upsertCxName(ctx context.Context, db queryExecer, name CxName) error {
	// An empty Source is a caller that predates this provenance (or a test
	// fixture that never set it); it reads back as the column default,
	// CxNameSourceSessionIndex, which is also the safer of the two guesses.
	source := name.Source
	if source == "" {
		source = CxNameSourceSessionIndex
	}
	_, err := execWrite(ctx, db, `
INSERT INTO cx_names (id, thread_name, source, renamed_at) VALUES (?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  thread_name=excluded.thread_name,
  source=excluded.source,
  renamed_at=excluded.renamed_at`,
		name.ID,
		name.ThreadName,
		source,
		name.RenamedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert cx name %q: %w", name.ID, err)
	}
	return nil
}

// ReplaceCxNames atomically replaces the session_index name mirror.
func (s *Store) ReplaceCxNames(ctx context.Context, names []CxName) error {
	return s.WithImmediateTx(ctx, func(tx *ImmediateTx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM cx_names"); err != nil {
			return fmt.Errorf("clear cx names: %w", err)
		}
		for _, name := range names {
			if err := tx.UpsertCxName(ctx, name); err != nil {
				return err
			}
		}
		return nil
	})
}

// CxNames returns the session_index name mirror keyed by rollout ID. Most
// callers only ever display the name; CxNameRecords is for the one caller
// that must also see where a name came from.
func (s *Store) CxNames(ctx context.Context) (map[string]string, error) {
	records, err := s.CxNameRecords(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(records))
	for id, record := range records {
		names[id] = record.ThreadName
	}
	return names, nil
}

// CxNameRecords returns the full Codex name mirror, keyed by rollout ID,
// including the provenance reconcileCodexNames needs to arbitrate a store
// name against a session_index rename.
func (s *Store) CxNameRecords(ctx context.Context) (records map[string]CxName, returnErr error) {
	rows, err := s.db.QueryContext(
		ctx,
		"SELECT id, thread_name, source, renamed_at FROM cx_names ORDER BY id",
	)
	if err != nil {
		return nil, fmt.Errorf("query cx name records: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close cx name rows: %w", err))
		}
	}()

	records = make(map[string]CxName)
	for rows.Next() {
		var record CxName
		if err := rows.Scan(
			&record.ID,
			&record.ThreadName,
			&record.Source,
			&record.RenamedAt,
		); err != nil {
			return nil, fmt.Errorf("scan cx name record: %w", err)
		}
		records[record.ID] = record
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cx name records: %w", err)
	}
	return records, nil
}

// SetMeta inserts or replaces a metadata value.
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	return setMeta(ctx, s.db, key, value)
}

// SetMeta inserts or replaces a metadata value within tx.
func (tx *ImmediateTx) SetMeta(ctx context.Context, key, value string) error {
	return setMeta(ctx, tx, key, value)
}

func setMeta(ctx context.Context, db queryExecer, key, value string) error {
	_, err := execWrite(ctx, db, `
INSERT INTO meta (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key,
		value,
	)
	if err != nil {
		return fmt.Errorf("set meta %q: %w", key, err)
	}
	return nil
}

// Meta returns a metadata value by key.
func (s *Store) Meta(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key=?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("query meta %q: %w", key, err)
	}
	return value, true, nil
}

// MetaPrefix returns every metadata key/value whose key starts with prefix.
// A caller that wants to audit a whole key FAMILY — the per-pane Codex
// bindings, say — cannot do it one Meta() at a time, because the thing worth
// auditing is what two keys say about each other.
func (s *Store) MetaPrefix(ctx context.Context, prefix string) (values map[string]string, returnErr error) {
	if prefix == "" {
		return nil, errors.New("meta prefix scan needs a prefix")
	}
	rows, err := s.db.QueryContext(
		ctx, "SELECT key, value FROM meta WHERE key LIKE ? ESCAPE '\\' ORDER BY key",
		escapeLikePrefix(prefix)+"%",
	)
	if err != nil {
		return nil, fmt.Errorf("query meta prefix %q: %w", prefix, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close meta prefix rows %q: %w", prefix, err))
		}
	}()
	values = make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan meta prefix %q: %w", prefix, err)
		}
		values[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read meta prefix %q: %w", prefix, err)
	}
	return values, nil
}

// escapeLikePrefix neutralises LIKE's own wildcards so a prefix containing
// "%" or "_" matches literally rather than sweeping the whole table.
func escapeLikePrefix(prefix string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")
	return replacer.Replace(prefix)
}

// DeleteMeta deletes a metadata value by key.
func (s *Store) DeleteMeta(ctx context.Context, key string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM meta WHERE key=?", key); err != nil {
		return fmt.Errorf("delete meta %q: %w", key, err)
	}
	return nil
}

// IncrementMeta increments one decimal counter, creating it at one.
func (s *Store) IncrementMeta(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO meta (key, value) VALUES (?, '1')
ON CONFLICT(key) DO UPDATE SET
  value=CAST(meta.value AS INTEGER)+1`,
		key,
	)
	if err != nil {
		return fmt.Errorf("increment meta %q: %w", key, err)
	}
	return nil
}

func boolInteger(value bool) int {
	if value {
		return 1
	}
	return 0
}

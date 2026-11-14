package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// rowCache is one index table (transcripts or rollouts) as this Store last
// read it. Every write to either table moves row_seq and stamps the written
// row's seq (row_seq.sql), so a repeated read asks only for the rows
// written since the last one, and an unchanged row_seq asks for nothing more:
// a long-lived reader (the Herdr indicator painter's held Store) rereads what
// changed instead of the whole table each scan.
type rowCache[T any] struct {
	mu      sync.Mutex
	table   string
	key     string
	columns string
	id      func(*T) string
	scan    func(rowScanner) (T, error)

	loaded bool
	seq    int64
	byID   map[string]T
	sorted []T
}

func newTranscriptCache() *rowCache[Transcript] {
	return &rowCache[Transcript]{
		table: "transcripts", key: "uuid", columns: transcriptColumns,
		id: func(row *Transcript) string { return row.UUID }, scan: scanTranscript,
	}
}

func newRolloutCache() *rowCache[Rollout] {
	return &rowCache[Rollout]{
		table: "rollouts", key: "id", columns: rolloutColumns,
		id: func(row *Rollout) string { return row.ID }, scan: scanRollout,
	}
}

// Transcripts returns all transcripts ordered by UUID.
func (s *Store) Transcripts(ctx context.Context) ([]Transcript, error) {
	var transcripts []Transcript
	var err error
	if s.readOnly || s.transcripts == nil {
		// A read-only open never migrates, so it may predate row_seq; a Store
		// opened without withRowCache reads whole and keeps nothing.
		transcripts, err = queryRows(ctx, s.logged(),
			"SELECT "+transcriptColumns+" FROM transcripts AS t ORDER BY t.uuid", "transcript", scanTranscript)
	} else {
		transcripts, err = s.transcripts.rows(ctx, s.db, func(present map[string]Transcript) {
			// Superseded names another row, which may have come or gone
			// without this one being written: it is taken from the table as
			// it is now, as transcriptSupersededSQL takes it.
			for uuid := range present {
				transcript := present[uuid]
				_, successor := present[transcript.ContinuedIn]
				transcript.Superseded = transcript.ContinuedIn != "" && transcript.ContinuedIn != uuid && successor
				present[uuid] = transcript
			}
		})
	}
	if err != nil {
		return nil, err
	}
	return s.clearNames(ctx, transcripts), nil
}

// Rollouts returns all rollouts ordered by ID.
func (s *Store) Rollouts(ctx context.Context) ([]Rollout, error) {
	if s.readOnly || s.rollouts == nil {
		return queryRows(ctx, s.logged(), "SELECT "+rolloutColumns+" FROM rollouts ORDER BY id", "rollout", scanRollout)
	}
	return s.rollouts.rows(ctx, s.db, nil)
}

// rows brings the cache up to the table's present state in one read
// transaction and returns a copy of it ordered by key. derive, when set,
// recomputes fields that depend on other rows after any change.
func (cache *rowCache[T]) rows(ctx context.Context, db *sql.DB, derive func(map[string]T)) (_ []T, returnErr error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin %s read: %w", cache.table, err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			returnErr = errors.Join(returnErr, fmt.Errorf("end %s read: %w", cache.table, err))
		}
	}()
	changed, err := cache.refresh(ctx, tx)
	if err != nil {
		// A half-applied refresh is never trusted: the next read starts over.
		cache.loaded, cache.byID, cache.sorted = false, nil, nil
		return nil, err
	}
	if changed {
		if derive != nil {
			derive(cache.byID)
		}
		cache.sorted = cache.sorted[:0]
		for _, row := range cache.byID {
			cache.sorted = append(cache.sorted, row)
		}
		sort.Slice(cache.sorted, func(left, right int) bool {
			return cache.id(&cache.sorted[left]) < cache.id(&cache.sorted[right])
		})
	}
	return append(make([]T, 0, len(cache.sorted)), cache.sorted...), nil
}

// refresh applies every write since the last read and reports whether any
// happened. Every row written after the last read carries a seq above it, and
// every row at or below it was read then, so once the written rows are in the
// cache it holds every row the table does; a count above the table's means
// rows were deleted, and only then are the keys read to drop them.
func (cache *rowCache[T]) refresh(ctx context.Context, tx *sql.Tx) (bool, error) {
	var seq int64
	if err := queryLogged(ctx, tx, "SELECT value FROM row_seq WHERE id=1", 1, func(row rowScanner) error {
		return row.Scan(&seq)
	}); err != nil {
		return false, fmt.Errorf("read the index write count: %w", err)
	}
	if cache.loaded && seq == cache.seq {
		return false, nil
	}
	if seq < cache.seq {
		// The count went back: the file was restored or rebuilt in place, so
		// a row at or below the last count may be new. Read it whole again.
		cache.loaded = false
	}
	query, arguments := "SELECT "+cache.columns+" FROM "+cache.table+" AS t", []any{}
	if !cache.loaded {
		cache.byID = make(map[string]T)
	} else {
		query, arguments = query+" WHERE t.seq>?", []any{cache.seq}
	}
	if err := queryLogged(ctx, tx, query, -1, func(row rowScanner) error {
		item, err := cache.scan(row)
		if err != nil {
			return err
		}
		cache.byID[cache.id(&item)] = item
		return nil
	}, arguments...); err != nil {
		return false, fmt.Errorf("query %s: %w", cache.table, err)
	}
	if cache.loaded {
		if err := cache.dropDeleted(ctx, tx); err != nil {
			return false, err
		}
	}
	cache.loaded, cache.seq = true, seq
	return true, nil
}

func (cache *rowCache[T]) dropDeleted(ctx context.Context, tx *sql.Tx) error {
	var count int
	if err := queryLogged(ctx, tx, "SELECT count(*) FROM "+cache.table, 1, func(row rowScanner) error {
		return row.Scan(&count)
	}); err != nil {
		return fmt.Errorf("count %s: %w", cache.table, err)
	}
	if count == len(cache.byID) {
		return nil
	}
	present := make(map[string]struct{}, count)
	if err := queryLogged(ctx, tx, "SELECT "+cache.key+" FROM "+cache.table, -1, func(row rowScanner) error {
		var id string
		if err := row.Scan(&id); err != nil {
			return err
		}
		present[id] = struct{}{}
		return nil
	}); err != nil {
		return fmt.Errorf("read %s keys: %w", cache.table, err)
	}
	for id := range cache.byID {
		if _, found := present[id]; !found {
			delete(cache.byID, id)
		}
	}
	return nil
}

// queryLogged runs query in tx, hands each row to visit and writes its
// db.statement record with the rows it returned; want, when not -1, is the
// exact row count the query must return.
func queryLogged(
	ctx context.Context, tx *sql.Tx, query string, want int, visit func(rowScanner) error, arguments ...any,
) (returnErr error) {
	op := obs.SQL(ctx, storeKind, query)
	count := 0
	defer func() { op.End(int64(count), returnErr) }()
	rows, err := tx.QueryContext(ctx, query, arguments...)
	if err != nil {
		return err
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close rows: %w", err))
		}
	}()
	for rows.Next() {
		if err := visit(rows); err != nil {
			return fmt.Errorf("scan row: %w", err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if want != -1 && count != want {
		return fmt.Errorf("query returned %d rows, want %d", count, want)
	}
	return nil
}

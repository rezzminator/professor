package index

import (
	"context"
	"path/filepath"
	"time"

	"hostops/pfm/internal/store"
)

// readCodexThreads loads the Codex CLI's own view of its conversations, newest
// state generation first.
func readCodexThreads(
	ctx context.Context,
	codexRoot string,
) ([]store.CodexThread, error) {
	files, err := store.CodexStateFiles(codexRoot)
	if err != nil {
		return nil, err
	}
	threads, err := store.ReadCodexThreads(ctx, files)
	if err != nil {
		return nil, err
	}
	return threads, nil
}

// reconcileCodexState folds the Codex state store into this pass's rollout
// rows. The store decides which conversations exist and how they are
// classified; a conversation that never wrote a rollout file still gets a row,
// so it can be listed, named, resumed, and killed like any other. Rows whose
// rollout file was parsed keep their file-derived size and prompt count.
func reconcileCodexState(
	threads []store.CodexThread,
	codexRoot string,
	updates []store.Rollout,
	existing map[string]store.Rollout,
	presentIDs map[string]struct{},
	counters *Counters,
) []store.Rollout {
	positionByID := make(map[string]int, len(updates))
	for position := range updates {
		positionByID[updates[position].ID] = position
	}
	for i := range threads {
		thread := &threads[i]
		if thread.Listed() {
			counters.CodexThreads++
			// The rollout file may be absent or already deleted; the store
			// still vouches for the conversation, so the row must survive the
			// pass that prunes rows without files.
			presentIDs[thread.ID] = struct{}{}
		}
		if position, found := positionByID[thread.ID]; found {
			updates[position] = applyCodexThread(updates[position], *thread)
			continue
		}
		if row, found := existing[thread.ID]; found {
			adjusted := applyCodexThread(row, *thread)
			if adjusted != row {
				positionByID[thread.ID] = len(updates)
				updates = append(updates, adjusted)
			}
			continue
		}
		if !thread.Listed() {
			continue
		}
		counters.CodexRowsCreated++
		positionByID[thread.ID] = len(updates)
		updates = append(updates, codexStoreRollout(*thread, codexRoot))
	}
	return updates
}

// applyCodexThread lets the store correct what only it knows: whether the
// conversation is a listed user thread, whether a machine or a person started
// it, and the directory a truncated rollout file never named. Content stays
// the parsed file's whenever the file actually found any, field by field, so
// the store's evidence only ever fills what the parse left empty.
//
// Each field is gated on ITS OWN emptiness rather than on the whole file's
// Size: a header-only rollout — a session_meta line and no user_message
// events, exactly what Codex >=0.146.1 leaves behind for a paginated thread
// whose content lives in the state store instead — has nonzero Size from
// those header bytes alone, so gating on Size left prompt_count at the file's
// own zero forever. Gating per field lets a real header-only chat's
// tokens_used/first_user_message evidence reach the row exactly as it would
// for a thread with no rollout file at all (codexStoreRollout, below).
//
// is_bg is re-derived on every pass rather than only on a reparse, so rows
// indexed before this classification existed repair themselves the next time
// the state store is read — no parser-version bump and no full rescan.
func applyCodexThread(
	rollout store.Rollout,
	thread store.CodexThread,
) store.Rollout {
	rollout.UserThread = thread.Listed()
	rollout.IsBG = thread.MachineSpawned()
	if rollout.CWD == "" {
		rollout.CWD = thread.CWD
	}
	if rollout.FirstPrompt == "" {
		rollout.FirstPrompt = thread.FirstPrompt
	}
	if rollout.PromptCount == 0 && thread.Prompted {
		rollout.PromptCount = 1
	}
	return rollout
}

// codexStoreRollout builds the row for a conversation with no rollout file.
// Size stays zero because no bytes exist to measure; activity comes from the
// store's own recency, which is the only timestamp such a thread has.
func codexStoreRollout(
	thread store.CodexThread,
	codexRoot string,
) store.Rollout {
	path := thread.RolloutPath
	if path == "" {
		// The rollout row's path is a unique key as well as a location. A
		// placeholder keeps the key without claiming a file that Codex never
		// wrote; walkCodexRollouts only ever collects rollout-*.jsonl names.
		path = filepath.Join(codexRoot, "sessions", thread.ID+".jsonl")
	}
	activityNS := int64(0)
	if thread.ActivityAt > 0 {
		activityNS = thread.ActivityAt * int64(time.Second)
	}
	promptCount := int64(0)
	if thread.Prompted {
		promptCount = 1
	}
	return store.Rollout{
		ID:          thread.ID,
		Path:        path,
		MTimeNS:     activityNS,
		CWD:         thread.CWD,
		UserThread:  true,
		LineageRoot: thread.ID,
		FirstPrompt: thread.FirstPrompt,
		PromptCount: promptCount,
		IsBG:        thread.MachineSpawned(),
	}
}

// reconcileCodexNames makes a rename performed inside Codex reach the picker.
// The state store owns a thread's explicit name; session_index.jsonl remains
// the cache and the fallback for threads the store never named.
//
// The store keeps NO rename clock — threads.name changes in place with no
// timestamp of its own, so a non-empty store name only proves it is A name,
// never that it is the CURRENT one. Codex 0.147 writes every rename to
// session_index.jsonl and lets threads.name go stale the moment a thread is
// renamed a second time, so a dated session_index entry for the same id
// outranks the store name: it is the only evidence of the two that can prove
// it is newer. A store name loses only to a DATED session_index entry —
// against an undated one, or no entry at all, it still wins, which is what
// keeps the store the authority for a thread session_index has never heard
// of.
func reconcileCodexNames(
	ctx context.Context,
	database *store.Store,
	threads []store.CodexThread,
	counters *Counters,
) error {
	if len(threads) == 0 {
		return nil
	}
	names, err := database.CxNameRecords(ctx)
	if err != nil {
		return err
	}
	updates := make([]store.CxName, 0)
	for i := range threads {
		thread := &threads[i]
		if !thread.Listed() || thread.Name == "" {
			continue
		}
		existing, found := names[thread.ID]
		if found &&
			existing.Source == store.CxNameSourceSessionIndex &&
			existing.RenamedAt > 0 {
			continue
		}
		if found &&
			existing.Source == store.CxNameSourceStore &&
			existing.ThreadName == thread.Name {
			continue
		}
		updates = append(updates, store.CxName{
			ID:         thread.ID,
			ThreadName: thread.Name,
			Source:     store.CxNameSourceStore,
		})
	}
	if len(updates) == 0 {
		return nil
	}
	if err := database.Batch(ctx, len(updates), func(
		tx *store.ImmediateTx,
		start, end int,
	) error {
		for _, name := range updates[start:end] {
			if err := tx.UpsertCxName(ctx, name); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	counters.RowsTouched += len(updates)
	return nil
}

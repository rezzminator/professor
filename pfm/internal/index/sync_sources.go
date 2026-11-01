package index

import (
	"context"
	"fmt"
	"sort"

	"github.com/rezzminator/professor/pfm/internal/store"
)

// SyncClaude runs Claude's complete incremental transcript pass. It is
// exported only so the Claude capability package can implement Source
// without creating an import cycle back into cmd/pfm's composition root.
func SyncClaude(ctx context.Context, database *store.Store, roots []string, counters *Counters) error {
	files, walkComplete, err := walkClaudeRoots(ctx, roots)
	if err != nil {
		return err
	}
	existing, err := database.Transcripts(ctx)
	if err != nil {
		return err
	}
	files = prioritizeClaudeFiles(files, counters.options.PriorityCWD, existing, counters.options.PriorityOnly)
	counters.FilesSeen += len(files)

	storedVersion, versionFound, err := database.Meta(ctx, claudeParserVersionKey)
	if err != nil {
		return err
	}
	forceFull := counters.options.Full || !versionFound || storedVersion != claudeParserVersion
	byPath := make(map[string]store.Transcript, len(existing))
	for i := range existing {
		transcript := &existing[i]
		byPath[transcript.Path] = *transcript
	}

	updates := make([]store.Transcript, 0)
	presentPaths := make(map[string]struct{}, len(files))
	presentIDs := make(map[string]struct{}, len(files))
	unreadable := 0
	for _, file := range files {
		presentPaths[file.Path] = struct{}{}
		presentIDs[file.ID] = struct{}{}
		previous, found := byPath[file.Path]
		if shouldSkip(file, found, previous.Size, previous.MTimeNS, forceFull) {
			counters.FilesSkipped++
			continue
		}
		start := int64(0)
		base := store.Transcript{}
		if shouldDelta(file, found, previous.Size, previous.ParsedOffset, forceFull) {
			start = previous.ParsedOffset
			base = previous
			counters.DeltaParsed++
		} else {
			counters.FullParsed++
		}
		transcript, bytesRead, parseErr := parseClaudeTranscriptFile(ctx, file, start, base)
		counters.BytesRead += bytesRead
		if parseErr != nil {
			// Its prior row, if any, stays: the path is present, so no prune.
			warnUnreadable(ctx, "claude transcript", file.Path, parseErr)
			unreadable++
			continue
		}
		updates = append(updates, transcript)
	}

	deletes := make([]string, 0)
	if !counters.options.PriorityOnly && walkComplete {
		for i := range existing {
			transcript := &existing[i]
			_, pathPresent := presentPaths[transcript.Path]
			_, idPresent := presentIDs[transcript.UUID]
			if !pathPresent && !idPresent {
				deletes = append(deletes, transcript.UUID)
			}
		}
	}
	if err := writeTranscriptUpdates(ctx, database, updates); err != nil {
		return err
	}
	if err := deleteTranscripts(ctx, database, deletes); err != nil {
		return err
	}
	counters.Deleted += len(deletes)
	counters.RowsTouched += len(updates) + len(deletes)
	// A file this pass could not read keeps its old-parser row, so the version
	// is stamped only once every file was read under it.
	if !counters.options.PriorityOnly && walkComplete && unreadable == 0 &&
		(!versionFound || storedVersion != claudeParserVersion) {
		if err := database.SetMeta(ctx, claudeParserVersionKey, claudeParserVersion); err != nil {
			return err
		}
	}
	// This pass is where a continued-in handoff is recorded, so it is where
	// the chat's schedules follow it into the new session.
	database.CarryContinuedSchedules(ctx)
	return nil
}

// SyncCodex runs Codex's complete rollout, state-store, and rename pass.
func SyncCodex(ctx context.Context, database *store.Store, roots []string, counters *Counters) error {
	if counters.options.PriorityOnly {
		return nil
	}
	files := make([]diskFile, 0)
	walkComplete := true
	for _, root := range roots {
		found, complete, err := walkCodexRollouts(ctx, root)
		if err != nil {
			return err
		}
		walkComplete = walkComplete && complete
		files = append(files, found...)
	}
	sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
	counters.FilesSeen += len(files)

	existing, err := database.Rollouts(ctx)
	if err != nil {
		return err
	}
	storedVersion, versionFound, err := database.Meta(ctx, codexParserVersionKey)
	if err != nil {
		return err
	}
	forceFull := counters.options.Full || !versionFound || storedVersion != codexParserVersion
	byPath := make(map[string]store.Rollout, len(existing))
	byID := make(map[string]store.Rollout, len(existing))
	for i := range existing {
		rollout := &existing[i]
		byPath[rollout.Path] = *rollout
		byID[rollout.ID] = *rollout
	}

	updates := make([]store.Rollout, 0)
	presentPaths := make(map[string]struct{}, len(files))
	presentIDs := make(map[string]struct{}, len(files))
	unreadable := 0
	for _, file := range files {
		presentPaths[file.Path] = struct{}{}
		previous, found := byPath[file.Path]
		if shouldSkip(file, found, previous.Size, previous.MTimeNS, forceFull) {
			counters.FilesSkipped++
			presentIDs[previous.ID] = struct{}{}
			continue
		}
		start := int64(0)
		base := store.Rollout{}
		if shouldDelta(file, found, previous.Size, previous.ParsedOffset, forceFull) {
			start = previous.ParsedOffset
			base = previous
			counters.DeltaParsed++
		} else {
			counters.FullParsed++
		}
		rollout, bytesRead, parseErr := parseCodexRolloutFile(ctx, file, start, base)
		counters.BytesRead += bytesRead
		if parseErr != nil {
			// Its prior row, if any, stays present and unpruned.
			warnUnreadable(ctx, "codex rollout", file.Path, parseErr)
			unreadable++
			if found {
				presentIDs[previous.ID] = struct{}{}
			}
			continue
		}
		updates = append(updates, rollout)
		presentIDs[rollout.ID] = struct{}{}
	}

	threads := make([]store.CodexThread, 0)
	for _, root := range roots {
		found, readErr := loadIndexedCodexThreads(ctx, root)
		if readErr != nil {
			return readErr
		}
		threads = append(threads, found...)
		updates = reconcileCodexState(found, root, updates, byID, presentIDs, counters)
	}
	deletes := make([]string, 0)
	for i := range existing {
		rollout := &existing[i]
		_, pathPresent := presentPaths[rollout.Path]
		_, idPresent := presentIDs[rollout.ID]
		if walkComplete && !pathPresent && !idPresent {
			deletes = append(deletes, rollout.ID)
		}
	}
	if err := writeRolloutUpdates(ctx, database, updates); err != nil {
		return err
	}
	if err := deleteRollouts(ctx, database, deletes); err != nil {
		return err
	}
	if err := database.ReconcileCodexLineageRoots(ctx); err != nil {
		return fmt.Errorf("reconcile Codex lineage roots: %w", err)
	}
	counters.Deleted += len(deletes)
	counters.RowsTouched += len(updates) + len(deletes)

	if counters.legacySingleCodexHome && len(roots) == 1 {
		err = reloadCxNames(ctx, database, roots[0], counters)
	} else {
		err = reloadCxNamesFromRoots(ctx, database, roots, counters)
	}
	if err != nil {
		return err
	}
	if err := reconcileCodexNames(ctx, database, threads, counters); err != nil {
		return err
	}
	if walkComplete && unreadable == 0 && (!versionFound || storedVersion != codexParserVersion) {
		if err := database.SetMeta(ctx, codexParserVersionKey, codexParserVersion); err != nil {
			return err
		}
	}
	return nil
}

// SyncOpenCode runs OpenCode's session-mirror pass. A pass whose stores are
// unchanged since the last mirror (openCodeStoreStamp) reads and writes
// nothing: the store's whole-table read is the costly part of a fleet scan.
func SyncOpenCode(ctx context.Context, database *store.Store, roots []string, counters *Counters) error {
	if counters.options.PriorityOnly {
		return nil
	}
	stamp, err := openCodeStoreStamp(roots)
	if err != nil {
		return err
	}
	if !counters.options.Full {
		previous, found, err := database.Meta(ctx, openCodeStoreStampKey)
		if err != nil {
			return err
		}
		if found && previous == stamp {
			return nil
		}
	}
	for i, root := range roots {
		rootStamp := "" // the last root's replace is the mirror a pass leaves
		if i == len(roots)-1 {
			rootStamp = stamp
		}
		if err := syncOpenCodeMirror(ctx, database, root, rootStamp, counters); err != nil {
			return err
		}
	}
	return nil
}

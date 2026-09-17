package index

import (
	"context"
	"fmt"
	"sort"

	"hostops/pfm/internal/store"
)

// SyncClaude runs Claude's complete incremental transcript pass. It is
// exported only so the Claude capability package can implement Source
// without creating an import cycle back into cmd/pfm's composition root.
func SyncClaude(ctx context.Context, database *store.Store, roots []string, counters *Counters) error {
	files, err := walkClaudeRoots(ctx, roots)
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
		transcript, bytesRead, parseErr := parseClaude(file, start, base)
		if parseErr != nil {
			return parseErr
		}
		counters.BytesRead += bytesRead
		updates = append(updates, transcript)
	}

	deletes := make([]string, 0)
	if !counters.options.PriorityOnly {
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
	if !counters.options.PriorityOnly && (!versionFound || storedVersion != claudeParserVersion) {
		if err := database.SetMeta(ctx, claudeParserVersionKey, claudeParserVersion); err != nil {
			return err
		}
	}
	return nil
}

// SyncCodex runs Codex's complete rollout, state-store, and rename pass.
func SyncCodex(ctx context.Context, database *store.Store, roots []string, counters *Counters) error {
	if counters.options.PriorityOnly {
		return nil
	}
	files := make([]diskFile, 0)
	for _, root := range roots {
		found, err := walkCodexRollouts(ctx, root)
		if err != nil {
			return err
		}
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
		rollout, bytesRead, parseErr := parseCodex(file, start, base)
		if parseErr != nil {
			return parseErr
		}
		counters.BytesRead += bytesRead
		updates = append(updates, rollout)
		presentIDs[rollout.ID] = struct{}{}
	}

	threads := make([]store.CodexThread, 0)
	for _, root := range roots {
		found, readErr := readCodexThreads(ctx, root)
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
		if !pathPresent && !idPresent {
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
	if !versionFound || storedVersion != codexParserVersion {
		if err := database.SetMeta(ctx, codexParserVersionKey, codexParserVersion); err != nil {
			return err
		}
	}
	return nil
}

// SyncOpenCode runs OpenCode's session-mirror pass.
func SyncOpenCode(ctx context.Context, database *store.Store, roots []string, counters *Counters) error {
	if counters.options.PriorityOnly {
		return nil
	}
	for _, root := range roots {
		if err := syncOpenCodeMirror(ctx, database, root, counters); err != nil {
			return err
		}
	}
	return nil
}

package index

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/store"
)

func prioritizeClaudeFiles(
	files []diskFile,
	cwd string,
	existing []store.Transcript,
	only bool,
) []diskFile {
	cwd = filepath.Clean(cwd)
	if cwd == "." || cwd == "" {
		if only {
			return nil
		}
		return files
	}
	knownPaths := make(map[string]struct{})
	for i := range existing {
		transcript := &existing[i]
		if filepath.Clean(transcript.CWD) == cwd {
			knownPaths[filepath.Clean(transcript.Path)] = struct{}{}
		}
	}
	encoded := strings.ReplaceAll(cwd, string(filepath.Separator), "-")
	if filepath.VolumeName(cwd) != "" {
		encoded = strings.ReplaceAll(encoded, ":", "-")
	}
	isPriority := func(file diskFile) bool {
		if _, found := knownPaths[filepath.Clean(file.Path)]; found {
			return true
		}
		return filepath.Base(filepath.Dir(file.Path)) == encoded
	}
	if only {
		selected := make([]diskFile, 0)
		for _, file := range files {
			if isPriority(file) {
				selected = append(selected, file)
			}
		}
		return selected
	}
	sort.SliceStable(files, func(left, right int) bool {
		leftPriority := isPriority(files[left])
		rightPriority := isPriority(files[right])
		if leftPriority != rightPriority {
			return leftPriority
		}
		return files[left].Path < files[right].Path
	})
	return files
}

func shouldSkip(file diskFile, found bool, oldSize, oldMTimeNS int64, full bool) bool {
	return !full && found && file.Size == oldSize && file.MTimeNS == oldMTimeNS
}

// shouldDelta reports whether the previous parse of this file can be resumed.
// A row with no parsed bytes has no parse to continue, so it is parsed in full.
func shouldDelta(file diskFile, found bool, oldSize, parsedOffset int64, full bool) bool {
	return !full && found && oldSize > 0 && file.Size > oldSize && file.Size >= parsedOffset
}

func writeTranscriptUpdates(ctx context.Context, database *store.Store, updates []store.Transcript) error {
	return database.Batch(ctx, len(updates), func(tx *store.ImmediateTx, start, end int) error {
		for i := range updates[start:end] {
			if err := tx.UpsertTranscript(ctx, updates[start+i]); err != nil {
				return err
			}
		}
		return nil
	})
}

func writeRolloutUpdates(ctx context.Context, database *store.Store, updates []store.Rollout) error {
	return database.Batch(ctx, len(updates), func(tx *store.ImmediateTx, start, end int) error {
		for i := range updates[start:end] {
			if err := tx.UpsertRollout(ctx, updates[start+i]); err != nil {
				return err
			}
		}
		return nil
	})
}

func deleteTranscripts(ctx context.Context, database *store.Store, ids []string) error {
	return database.Batch(ctx, len(ids), func(tx *store.ImmediateTx, start, end int) error {
		for _, id := range ids[start:end] {
			if err := tx.DeleteTranscript(ctx, id); err != nil {
				return err
			}
		}
		return nil
	})
}

func deleteRollouts(ctx context.Context, database *store.Store, ids []string) error {
	return database.Batch(ctx, len(ids), func(tx *store.ImmediateTx, start, end int) error {
		for _, id := range ids[start:end] {
			if err := tx.DeleteRollout(ctx, id); err != nil {
				return err
			}
		}
		return nil
	})
}

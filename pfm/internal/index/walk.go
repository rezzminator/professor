package index

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
)

type diskFile struct {
	ID      string
	Path    string
	Size    int64
	MTimeNS int64
}

// walkClaudeRoots lists every transcript under roots. A root, directory or
// file it cannot read is warned and skipped, its siblings still listed;
// complete is false then, so the caller never prunes a row it could not look
// for. Only a cancelled ctx or an unresolvable root path is an error.
func walkClaudeRoots(ctx context.Context, roots []string) ([]diskFile, bool, error) {
	seenRoots := make(map[string]struct{})
	filesByPath := make(map[string]diskFile)
	complete := true

	for _, root := range roots {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return nil, false, fmt.Errorf("make Claude root absolute %q: %w", root, err)
		}
		_, err = os.Stat(absolute)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			warnUnreadable(ctx, "claude root", absolute, err)
			complete = false
			continue
		}
		if _, duplicate := seenRoots[absolute]; duplicate {
			continue
		}
		seenRoots[absolute] = struct{}{}

		// The trailing "/." makes WalkDir descend a symlinked root; filepath.Join would clean it away.
		walkRoot := fmt.Sprintf("%s%c.", absolute, os.PathSeparator)
		err = filepath.WalkDir(walkRoot, func(path string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil // removed mid-walk: gone, not unread
			}
			if walkErr != nil {
				warnUnreadable(ctx, "claude directory", path, walkErr)
				complete = false
				if entry != nil && entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			relative, err := filepath.Rel(walkRoot, path)
			if err != nil {
				return err
			}
			depth := 0
			if relative != "." {
				depth = strings.Count(relative, string(os.PathSeparator)) + 1
			}
			if entry.IsDir() {
				if depth >= 2 {
					return filepath.SkipDir
				}
				return nil
			}
			if depth > 2 ||
				entry.Type()&os.ModeSymlink != 0 ||
				filepath.Ext(entry.Name()) != ".jsonl" {
				return nil
			}

			info, err := entry.Info()
			if errors.Is(err, fs.ErrNotExist) {
				return nil // removed mid-walk: gone, not unread
			}
			if err != nil {
				warnUnreadable(ctx, "claude transcript", path, err)
				complete = false
				return nil
			}
			path = filepath.Clean(path)
			filesByPath[path] = diskFile{
				ID:      strings.TrimSuffix(entry.Name(), ".jsonl"),
				Path:    path,
				Size:    info.Size(),
				MTimeNS: info.ModTime().UnixNano(),
			}
			return nil
		})
		if err != nil {
			return nil, false, fmt.Errorf("walk Claude root %q: %w", absolute, err)
		}
	}

	return sortedDiskFiles(filesByPath), complete, nil
}

// walkCodexRollouts lists codexHome's live rollouts. The index reads sessions/
// only: an archived thread is no live or resumable seat. What the walk could
// not read is warned and complete is false, the rollouts it did list kept;
// only a cancelled ctx is an error.
func walkCodexRollouts(ctx context.Context, codexHome string) ([]diskFile, bool, error) {
	rollouts, errs := codexmeta.WalkRollouts(ctx, codexHome, codexmeta.SessionsDir)
	if err := ctx.Err(); err != nil {
		return nil, false, fmt.Errorf("walk Codex rollouts under %q: %w", codexHome, err)
	}
	for _, walkErr := range errs {
		warnUnreadable(ctx, "codex rollouts", codexHome, walkErr)
	}
	files := make([]diskFile, 0, len(rollouts))
	for _, rollout := range rollouts {
		files = append(files, diskFile{
			ID:      codexmeta.RolloutID(filepath.Base(rollout.Path)),
			Path:    rollout.Path,
			Size:    rollout.Size,
			MTimeNS: rollout.ModTime.UnixNano(),
		})
	}
	return files, len(errs) == 0, nil
}

func sortedDiskFiles(filesByPath map[string]diskFile) []diskFile {
	files := make([]diskFile, 0, len(filesByPath))
	for _, file := range filesByPath {
		files = append(files, file)
	}
	sort.Slice(files, func(left, right int) bool {
		return files[left].Path < files[right].Path
	})
	return files
}

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

func walkClaudeRoots(ctx context.Context, roots []string) ([]diskFile, error) {
	seenRoots := make(map[string]struct{})
	filesByPath := make(map[string]diskFile)

	for _, root := range roots {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("make Claude root absolute %q: %w", root, err)
		}
		_, err = os.Stat(absolute)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("stat Claude root %q: %w", root, err)
		}
		if _, duplicate := seenRoots[absolute]; duplicate {
			continue
		}
		seenRoots[absolute] = struct{}{}

		// The trailing "/." makes WalkDir descend a symlinked root; filepath.Join would clean it away.
		walkRoot := fmt.Sprintf("%s%c.", absolute, os.PathSeparator)
		err = filepath.WalkDir(walkRoot, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
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
			if err != nil {
				return err
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
			return nil, fmt.Errorf("walk Claude root %q: %w", absolute, err)
		}
	}

	return sortedDiskFiles(filesByPath), nil
}

// walkCodexRollouts lists codexHome's live rollouts. The index reads sessions/
// only: an archived thread is no live or resumable seat.
func walkCodexRollouts(ctx context.Context, codexHome string) ([]diskFile, error) {
	rollouts, errs := codexmeta.WalkRollouts(ctx, codexHome, codexmeta.SessionsDir)
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("walk Codex rollouts under %q: %w", codexHome, err)
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
	return files, nil
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

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
		resolved, err := filepath.EvalSymlinks(absolute)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("resolve Claude root %q: %w", root, err)
		}
		if _, duplicate := seenRoots[resolved]; duplicate {
			continue
		}
		seenRoots[resolved] = struct{}{}

		err = filepath.WalkDir(resolved, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}

			relative, err := filepath.Rel(resolved, path)
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
			filesByPath[path] = diskFile{
				ID:      strings.TrimSuffix(entry.Name(), ".jsonl"),
				Path:    path,
				Size:    info.Size(),
				MTimeNS: info.ModTime().UnixNano(),
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk Claude root %q: %w", resolved, err)
		}
	}

	return sortedDiskFiles(filesByPath), nil
}

func walkCodexRollouts(ctx context.Context, codexHome string) ([]diskFile, error) {
	root := filepath.Join(codexHome, "sessions")
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("make Codex rollout root absolute: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("resolve Codex rollout root %q: %w", root, err)
	}

	filesByPath := make(map[string]diskFile)
	err = filepath.WalkDir(resolved, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 ||
			filepath.Ext(entry.Name()) != ".jsonl" ||
			!strings.HasPrefix(entry.Name(), "rollout-") {
			return nil
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		filesByPath[path] = diskFile{
			ID:      rolloutIDFromFilename(entry.Name()),
			Path:    path,
			Size:    info.Size(),
			MTimeNS: info.ModTime().UnixNano(),
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk Codex rollouts %q: %w", resolved, err)
	}
	return sortedDiskFiles(filesByPath), nil
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

func rolloutIDFromFilename(name string) string {
	stem := strings.TrimSuffix(name, ".jsonl")
	rest := strings.TrimPrefix(stem, "rollout-")
	if len(rest) > 20 &&
		rest[4] == '-' &&
		rest[7] == '-' &&
		rest[10] == 'T' &&
		rest[13] == '-' &&
		rest[16] == '-' &&
		rest[19] == '-' {
		return rest[20:]
	}
	return rest
}

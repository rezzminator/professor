package opencodegen

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"hostops/pfm/internal/atomicfile"
)

type reconcileResult struct {
	Wrote, Unchanged, Deleted int
	Warnings, Problems        []string
	Actions                   []Action
}

func reconcileOpenCode(outputs []generatedFile, mode Mode, root, home string) reconcileResult {
	result := reconcileResult{}
	managed := []string{
		filepath.Join(root, ".opencode", "agent"),
		filepath.Join(root, ".opencode", "command"),
		filepath.Join(root, ".opencode", "skills"),
		filepath.Join(home, ".config", openCodeName(), "command"),
	}
	wanted := map[string]bool{}
	for _, output := range outputs {
		wanted[managedOpenCodeEntry(output.Path, managed)] = true
		if output.Link != "" {
			reconcileOpenCodeLink(&result, output, mode)
		} else {
			reconcileOpenCodeFile(&result, output, mode)
		}
	}
	for _, dir := range managed {
		reconcileOpenCodeOrphans(&result, dir, wanted, mode)
	}
	return result
}

func managedOpenCodeEntry(path string, managed []string) string {
	for _, root := range managed {
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return filepath.Join(root, strings.Split(rel, string(filepath.Separator))[0])
	}
	return path
}

func reconcileOpenCodeLink(result *reconcileResult, output generatedFile, mode Mode) {
	info, err := os.Lstat(output.Path)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		if link, readErr := os.Readlink(output.Path); readErr == nil && link == output.Link {
			result.Unchanged++
			return
		}
	}
	if mode != ModeBuild {
		if err == nil && !isClaimable(output.Path, false) {
			result.Problems = append(
				result.Problems,
				fmt.Sprintf("CONFLICT %s — exists without a generated marker; not touching it", output.Path),
			)
			return
		}
		state := "STALE"
		if errors.Is(err, fs.ErrNotExist) {
			state = "MISSING"
		}
		result.Problems = append(
			result.Problems,
			fmt.Sprintf("%s %s (want symlink → %s)", state, output.Path, output.Link),
		)
		result.Actions = append(result.Actions, Action{Kind: actionLink, Path: output.Path, Target: output.Link})
		return
	}
	if err == nil && !isClaimable(output.Path, false) {
		result.Problems = append(
			result.Problems,
			fmt.Sprintf("CONFLICT %s — exists without a generated marker; not touching it", output.Path),
		)
		return
	}
	result.Actions = append(result.Actions, Action{Kind: actionLink, Path: output.Path, Target: output.Link})
	if removeErr := os.RemoveAll(output.Path); removeErr != nil {
		result.Problems = append(result.Problems, fmt.Sprintf("remove %s: %v", output.Path, removeErr))
		return
	}
	if err := os.MkdirAll(filepath.Dir(output.Path), 0o755); err != nil {
		result.Problems = append(result.Problems, fmt.Sprintf("mkdir %s: %v", output.Path, err))
		return
	}
	if err := os.Symlink(output.Link, output.Path); err != nil {
		result.Problems = append(result.Problems, fmt.Sprintf("symlink %s: %v", output.Path, err))
		return
	}
	result.Wrote++
}

func reconcileOpenCodeFile(result *reconcileResult, output generatedFile, mode Mode) {
	info, err := os.Lstat(output.Path)
	current := ""
	if err == nil && info.Mode().IsRegular() {
		if raw, readErr := os.ReadFile(output.Path); readErr == nil {
			current = string(raw)
		} else {
			result.Problems = append(result.Problems, fmt.Sprintf("read %s: %v", output.Path, readErr))
			return
		}
	}
	if current == output.Content {
		result.Unchanged++
		return
	}
	if mode != ModeBuild {
		if err == nil && !isClaimable(output.Path, output.MirrorCopy) {
			result.Problems = append(
				result.Problems,
				fmt.Sprintf("CONFLICT %s — exists without a generated marker; not touching it", output.Path),
			)
			return
		}
		state := "STALE"
		if errors.Is(err, fs.ErrNotExist) || current == "" {
			state = "MISSING"
		}
		result.Problems = append(result.Problems, fmt.Sprintf("%s %s", state, output.Path))
		result.Actions = append(result.Actions, Action{Kind: actionWrite, Path: output.Path})
		return
	}
	if err == nil && !isClaimable(output.Path, output.MirrorCopy) {
		result.Problems = append(
			result.Problems,
			fmt.Sprintf("CONFLICT %s — exists without a generated marker; not touching it", output.Path),
		)
		return
	}
	result.Actions = append(result.Actions, Action{Kind: actionWrite, Path: output.Path})
	if err := atomicfile.Write(output.Path, []byte(output.Content), 0o644); err != nil {
		result.Problems = append(result.Problems, fmt.Sprintf("write %s: %v", output.Path, err))
		return
	}
	result.Wrote++
}

func reconcileOpenCodeOrphans(result *reconcileResult, dir string, wanted map[string]bool, mode Mode) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err != nil {
		result.Problems = append(result.Problems, fmt.Sprintf("read managed directory %s: %v", dir, err))
		return
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if wanted[path] || !isClaimable(path, false) {
			continue
		}
		if mode != ModeBuild {
			result.Problems = append(result.Problems, "ORPHAN "+path)
			result.Actions = append(result.Actions, Action{Kind: actionDelete, Path: path})
			continue
		}
		result.Actions = append(result.Actions, Action{Kind: actionDelete, Path: path})
		if err := os.RemoveAll(path); err != nil {
			result.Problems = append(result.Problems, fmt.Sprintf("remove orphan %s: %v", path, err))
		} else {
			result.Deleted++
		}
	}
}

func isClaimable(path string, mirrorCopy bool) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	if mirrorCopy {
		return true
	}
	if info.Mode().IsRegular() {
		raw, err := os.ReadFile(path)
		return err == nil && hasMarker(string(raw))
	}
	if info.IsDir() {
		raw, err := os.ReadFile(filepath.Join(path, "SKILL.md"))
		return err == nil && hasMarker(string(raw))
	}
	return false
}

package opencodegen

import (
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
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
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		result.Problems = append(result.Problems, unreadableProblem(output.Path, err))
		return
	}
	exists := err == nil
	if exists && info.Mode()&os.ModeSymlink != 0 {
		if link, readErr := os.Readlink(output.Path); readErr == nil && link == output.Link {
			result.Unchanged++
			return
		}
	}
	if exists {
		if refusal := claimProblem(output); refusal != "" {
			result.Problems = append(result.Problems, refusal)
			return
		}
	}
	if mode != ModeBuild {
		result.Problems = append(
			result.Problems,
			fmt.Sprintf("%s %s (want symlink → %s)", outputState(exists), output.Path, output.Link),
		)
		result.Actions = append(result.Actions, Action{Kind: actionLink, Path: output.Path, Target: output.Link})
		return
	}
	result.Actions = append(result.Actions, Action{Kind: actionLink, Path: output.Path, Target: output.Link})
	if exists && info.IsDir() {
		if removeErr := os.RemoveAll(output.Path); removeErr != nil {
			result.Problems = append(result.Problems, fmt.Sprintf("remove %s: %v", output.Path, removeErr))
			return
		}
	}
	if err := os.MkdirAll(filepath.Dir(output.Path), 0o755); err != nil {
		result.Problems = append(result.Problems, fmt.Sprintf("mkdir %s: %v", output.Path, err))
		return
	}
	if err := replaceSymlink(output.Link, output.Path); err != nil {
		result.Problems = append(result.Problems, fmt.Sprintf("symlink %s: %v", output.Path, err))
		return
	}
	result.Wrote++
}

// replaceSymlink points path at target in one step: the link is made at a
// unique name in the same directory, then renamed over path, so two builds
// replacing the same link never race into EEXIST.
func replaceSymlink(target, path string) error {
	scratch := filepath.Join(
		filepath.Dir(path),
		fmt.Sprintf(".%s.pfm-link-%d-%016x", filepath.Base(path), os.Getpid(), rand.Uint64()),
	)
	if err := os.Symlink(target, scratch); err != nil {
		return err
	}
	if err := os.Rename(scratch, path); err != nil {
		if removeErr := os.Remove(scratch); removeErr != nil {
			return fmt.Errorf("%w; remove scratch link %s: %v", err, scratch, removeErr)
		}
		return err
	}
	return nil
}

// outputState names an output check would rewrite: only an absent path is
// MISSING; anything present (empty or not) is STALE.
func outputState(exists bool) string {
	if exists {
		return "STALE"
	}
	return "MISSING"
}

func unreadableProblem(path string, err error) string {
	return fmt.Sprintf("UNREADABLE %s: %v", path, err)
}

// claimProblem is the problem that forbids touching an existing output, or
// "" when pfm provably owns it.
func claimProblem(output generatedFile) string {
	claimable, err := isClaimable(output.Path)
	if err != nil {
		return unreadableProblem(output.Path, err)
	}
	if claimable {
		return ""
	}
	if output.Source != "" {
		return fmt.Sprintf(
			"CONFLICT %s — differs from %s and pfm cannot prove it wrote it; delete it and rebuild",
			output.Path,
			output.Source,
		)
	}
	return fmt.Sprintf("CONFLICT %s — exists without a generated marker; not touching it", output.Path)
}

func reconcileOpenCodeFile(result *reconcileResult, output generatedFile, mode Mode) {
	wantMode := output.Mode
	if wantMode == 0 {
		wantMode = defaultGeneratedFileMode
	}
	info, err := os.Lstat(output.Path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		result.Problems = append(result.Problems, unreadableProblem(output.Path, err))
		return
	}
	exists := err == nil
	regular := exists && info.Mode().IsRegular()
	current := ""
	haveMode := os.FileMode(0)
	if regular {
		haveMode = info.Mode().Perm()
		raw, readErr := os.ReadFile(output.Path)
		if readErr != nil {
			result.Problems = append(result.Problems, unreadableProblem(output.Path, readErr))
			return
		}
		current = string(raw)
	}
	// A generated file whose content is already right but whose mode drifted
	// (an operator's chmod, a restore from a permission-lossy archive) is not
	// "Unchanged" (L3-F14) — check names it distinctly from STALE/MISSING
	// content, and build fixes it with a chmod rather than rewriting content
	// that was already correct.
	modeOnlyDrift := current == output.Content && regular && haveMode != wantMode
	if current == output.Content && !modeOnlyDrift {
		result.Unchanged++
		return
	}
	if exists {
		if refusal := claimProblem(output); refusal != "" {
			result.Problems = append(result.Problems, refusal)
			return
		}
	}
	if mode != ModeBuild {
		if modeOnlyDrift {
			result.Problems = append(
				result.Problems,
				fmt.Sprintf("MODE %s (want %04o, have %04o)", output.Path, wantMode, haveMode),
			)
			result.Actions = append(
				result.Actions,
				Action{Kind: actionChmod, Path: output.Path, Target: fmt.Sprintf("%04o", wantMode)},
			)
			return
		}
		result.Problems = append(result.Problems, fmt.Sprintf("%s %s", outputState(exists), output.Path))
		result.Actions = append(result.Actions, Action{Kind: actionWrite, Path: output.Path})
		return
	}
	if modeOnlyDrift {
		result.Actions = append(
			result.Actions,
			Action{Kind: actionChmod, Path: output.Path, Target: fmt.Sprintf("%04o", wantMode)},
		)
		if err := os.Chmod(output.Path, wantMode); err != nil {
			result.Problems = append(result.Problems, fmt.Sprintf("chmod %s: %v", output.Path, err))
			return
		}
		result.Wrote++
		return
	}
	result.Actions = append(result.Actions, Action{Kind: actionWrite, Path: output.Path})
	if err := atomicfile.Write(output.Path, []byte(output.Content), wantMode); err != nil {
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
		if wanted[path] {
			continue
		}
		claimable, claimErr := isClaimable(path)
		if claimErr != nil {
			result.Warnings = append(
				result.Warnings,
				fmt.Sprintf("unreadable %s — cannot tell whether pfm owns it; left in place: %v", path, claimErr),
			)
			continue
		}
		if !claimable {
			continue
		}
		if mode != ModeBuild {
			result.Problems = append(result.Problems, "ORPHAN "+path)
			result.Actions = append(result.Actions, Action{Kind: actionDelete, Path: path})
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			result.Problems = append(result.Problems, fmt.Sprintf("remove orphan %s: %v", path, err))
			continue
		}
		result.Actions = append(result.Actions, Action{Kind: actionDelete, Path: path})
		result.Deleted++
	}
}

// isClaimable reports whether pfm-generated content already owns path: a
// symlink into the Claude source tree, or a regular file/skill whose content
// carries the compiler's own marker. A byte-for-byte MirrorCopy output
// (.opencode/LICENSE, .opencode/SECURITY.md) can never carry that marker
// without corrupting the copy, and there is no persisted manifest of what a
// PRIOR pfm run wrote here — content equal to a past source revision is
// unknowable without one (L3-F19). So MirrorCopy gets no ownership
// shortcut: a pre-existing, content-differing file at its path is the same
// CONFLICT an unrelated hand-placed file (a real `.opencode/LICENSE`) would
// be — the smallest honest rule available without a manifest to consult.
// A path it cannot read returns the error: "cannot tell" is never "not ours".
func isClaimable(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return false, err
		}
		return isClaudeSourceTarget(target), nil
	case info.Mode().IsRegular():
		raw, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		return hasMarker(string(raw)), nil
	case info.IsDir():
		raw, err := os.ReadFile(filepath.Join(path, "SKILL.md"))
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return hasMarker(string(raw)), nil
	}
	return false, nil
}

func isClaudeSourceTarget(target string) bool {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(target)), "/")
	for i, part := range parts[:len(parts)-1] {
		if part != ".claude" {
			continue
		}
		switch parts[i+1] {
		case "agents", "commands", "skills":
			return true
		}
	}
	return false
}

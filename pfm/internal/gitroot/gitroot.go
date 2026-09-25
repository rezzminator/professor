// Package gitroot resolves the repository a directory belongs to by reading
// .git files only: a subdirectory or a linked worktree maps to its main
// checkout. It never runs the git binary, and an unreadable file is a
// fallback to the next rule, never an error.
package gitroot

import (
	"os"
	"path/filepath"
	"strings"
)

// worktreesSegment is the conventional home of linked worktrees inside a
// repository; a removed worktree's path still names its repo through it.
const worktreesSegment = string(filepath.Separator) + ".worktrees" + string(filepath.Separator)

// RepoRoot is the main checkout of the repository holding dir: the first
// ancestor with a .git directory, or, for a .git file (a linked worktree), the
// checkout its commondir names. A directory outside any repository returns
// unchanged. A dir that no longer exists (a removed worktree, an old
// transcript's cwd) maps to the path before its /.worktrees/ segment, or
// returns unchanged. A relative dir returns unchanged: it names no place.
func RepoRoot(dir string) string {
	if !filepath.IsAbs(dir) {
		return dir
	}
	dir = filepath.Clean(dir)
	if _, err := os.Stat(dir); err != nil {
		if index := strings.Index(dir+string(filepath.Separator), worktreesSegment); index > 0 {
			return dir[:index]
		}
		return dir
	}
	for current := dir; ; {
		info, err := os.Lstat(filepath.Join(current, ".git"))
		switch {
		case err == nil && info.IsDir():
			return current
		case err == nil && info.Mode().IsRegular():
			if main, ok := MainCheckout(current); ok {
				return main
			}
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return dir
		}
		current = parent
	}
}

// MainCheckout maps a linked worktree root (a directory whose .git is a file)
// to its repository's main checkout: the .git file's gitdir, that gitdir's
// commondir, and the common dir's parent. ok is false when root is not a
// linked worktree or any of those files cannot be read.
func MainCheckout(root string) (main string, ok bool) {
	raw, err := os.ReadFile(filepath.Join(root, ".git"))
	if err != nil {
		return "", false
	}
	gitdir, found := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir: ")
	if !found {
		return "", false
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}
	common, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return "", false
	}
	commonDir := strings.TrimSpace(string(common))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(gitdir, commonDir)
	}
	return filepath.Dir(filepath.Clean(commonDir)), true
}

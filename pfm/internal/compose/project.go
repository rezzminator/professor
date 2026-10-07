package compose

import (
	"github.com/rezzminator/professor/pfm/internal/gitroot"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

// projectName is the project label a chat lists under: the basename of the
// repository its cwd belongs to (gitroot.RepoRoot), so a chat in a linked
// worktree or a repo subdirectory files under the repo. "?" names a cwd with
// no usable basename. A row's CWD stays the literal cwd; only the label moves.
func projectName(cwd string) string {
	return resolveProject(cwd).name
}

// projectRef is one cwd's repository root and the label derived from it.
type projectRef struct {
	root, name string
}

func resolveProject(cwd string) projectRef {
	root, name := gitroot.Project(cwd)
	return projectRef{root: root, name: name}
}

// projectNames memoizes resolveProject per cwd for one compose run: hundreds
// of rows share a handful of cwds, and each resolution reads the filesystem.
// A nil map resolves without caching.
type projectNames struct {
	resolved map[string]projectRef
	benches  []workbench.Bench
}

func (names projectNames) resolve(cwd string) projectRef {
	if ref, found := names.resolved[cwd]; found {
		return ref
	}
	ref := resolveProject(cwd)
	if cwd != "" && len(names.benches) != 0 {
		if bench, found := workbench.Owner(names.benches, cwd); found {
			ref = projectRef{root: bench.Dir, name: bench.Key}
		}
	}
	if names.resolved != nil {
		names.resolved[cwd] = ref
	}
	return ref
}

func (names projectNames) of(cwd string) string {
	return names.resolve(cwd).name
}

package compose

import (
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/gitroot"
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
	if cwd == "" {
		return projectRef{name: "?"}
	}
	root := gitroot.RepoRoot(cwd)
	trimmed := strings.TrimRight(root, string(filepath.Separator))
	project := trimmed[strings.LastIndexByte(trimmed, byte(filepath.Separator))+1:]
	if project == "." || project == ".." || project == "" {
		project = "?"
	}
	return projectRef{root: root, name: project}
}

// projectNames memoizes resolveProject per cwd for one compose run: hundreds
// of rows share a handful of cwds, and each resolution reads the filesystem.
// A nil map resolves without caching.
type projectNames map[string]projectRef

func (names projectNames) resolve(cwd string) projectRef {
	if ref, found := names[cwd]; found {
		return ref
	}
	ref := resolveProject(cwd)
	if names != nil {
		names[cwd] = ref
	}
	return ref
}

func (names projectNames) of(cwd string) string {
	return names.resolve(cwd).name
}

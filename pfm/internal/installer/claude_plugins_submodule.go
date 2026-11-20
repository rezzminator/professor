package installer

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// uninitialisedSubmodule reports whether the clone's .gitmodules registers
// plugins/{name} while that directory holds no .git: a submodule git never
// initialised, whose directory a clone without --recurse-submodules leaves
// empty. An unreadable .gitmodules reports false, leaving the plain no-checkout
// reason. The `path = ` lines are matched by hand, a quoted value unquoted:
// one key of git's config format needs no git process.
func uninitialisedSubmodule(clone, name string) bool {
	content, err := os.ReadFile(filepath.Join(clone, ".gitmodules"))
	if err != nil {
		return false
	}
	registered := false
	for line := range strings.SplitSeq(string(content), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && strings.TrimSpace(key) == "path" && strings.Trim(strings.TrimSpace(value), `"`) == "plugins/"+name {
			registered = true
		}
	}
	if !registered {
		return false
	}
	_, gitErr := os.Lstat(filepath.Join(clone, "plugins", name, ".git"))
	return errors.Is(gitErr, fs.ErrNotExist)
}

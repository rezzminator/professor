package professor

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/gitroot"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// DiscoverSourceRepo finds the source clone without invoking git and maps a linked worktree to its main checkout.
func DiscoverSourceRepo() string {
	if value := strings.TrimSpace((paths.OSEnv{}).Get("PFM_SOURCE_REPO")); value != "" {
		if absolute, err := filepath.Abs(value); err == nil {
			return absolute
		}
	}
	current, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if isSourceRepo(current) {
			return mainWorktreeOf(current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

// mainWorktreeOf maps a linked worktree of the source clone to its main
// checkout; anything else, or a main checkout that is not the clone, is root.
func mainWorktreeOf(root string) string {
	main, ok := gitroot.MainCheckout(root)
	if !ok || !isSourceRepo(main) {
		return root
	}
	return main
}

// isSourceRepo names the blueprint clone by files a FRESH clone carries.
// AGENTS.md is a generated engine mirror, gitignored and absent until the first
// compile, so it can never be one of them. pfm/go.mod is the engine source:
// present in every clone and worktree, absent from a scaffolded adopter
// project, which must never be mistaken for the blueprint.
func isSourceRepo(root string) bool {
	for _, relative := range []string{ClaudeInstructionsFile, "pfm/go.mod", ".claude/settings.json"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
			return false
		}
	}
	return true
}

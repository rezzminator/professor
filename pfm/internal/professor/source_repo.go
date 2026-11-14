package professor

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/gitroot"
	"github.com/rezzminator/professor/pfm/internal/obs"
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
		// Could not look: logged, so a missing source clone is never silent.
		obs.Logger(context.Background()).Warn("professor.source_repo.unreadable", obs.FieldErr, err.Error())
		return ""
	}
	for {
		if IsSourceRepo(current) {
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
	if !ok || !IsSourceRepo(main) {
		return root
	}
	return main
}

// IsSourceRepo names the blueprint clone by files a FRESH clone carries.
// AGENTS.md is a generated engine mirror, gitignored and absent until the first
// compile, so it can never be one of them. pfm/go.mod is the engine source:
// present in every clone and worktree, absent from a scaffolded adopter
// project, which must never be mistaken for the blueprint.
func IsSourceRepo(root string) bool {
	for _, relative := range []string{ClaudeInstructionsFile, "pfm/go.mod", ".claude/settings.json"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
			if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
				obs.Logger(context.Background()).Warn("professor.source_repo.unreadable", obs.FieldErr, err.Error())
			}
			return false
		}
	}
	return true
}

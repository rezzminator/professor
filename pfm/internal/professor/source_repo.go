package professor

import (
	"os"
	"path/filepath"
	"strings"
)

// DiscoverSourceRepo finds the source clone without invoking git and maps a linked worktree to its main checkout.
func DiscoverSourceRepo() string {
	if value := strings.TrimSpace(os.Getenv("PFM_SOURCE_REPO")); value != "" {
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

func mainWorktreeOf(root string) string {
	raw, err := os.ReadFile(filepath.Join(root, ".git"))
	if err != nil {
		return root
	}
	gitdir, found := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir: ")
	if !found {
		return root
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}
	common, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return root
	}
	commonDir := strings.TrimSpace(string(common))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(gitdir, commonDir)
	}
	main := filepath.Dir(filepath.Clean(commonDir))
	if !isSourceRepo(main) {
		return root
	}
	return main
}

func isSourceRepo(root string) bool {
	for _, relative := range []string{ClaudeInstructionsFile, "AGENTS.md", ".claude/settings.json"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
			return false
		}
	}
	return true
}

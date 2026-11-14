package gather

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestMain gives this package a short, canonical TMPDIR before any test builds
// a path from it. See internal/testjail for why both properties matter.
func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }

func setGatherTestEnv(t *testing.T, root, tmuxDir string) {
	t.Helper()

	home := filepath.Join(root, "home")
	sidDir := filepath.Join(root, "sid")
	codexHome := filepath.Join(root, "codex")
	for _, directory := range []string{home, sidDir, codexHome, tmuxDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create gather jail directory %q: %v", directory, err)
		}
	}

	t.Setenv("HOME", home)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", root)
	t.Setenv(paths.EnvHome, home)
	t.Setenv(paths.EnvCacheDB, filepath.Join(root, "pfm-cache.db"))
	t.Setenv(paths.EnvSIDDir, sidDir)
	t.Setenv(paths.EnvClaudeRoots, filepath.Join(root, "claude-projects"))
	t.Setenv(paths.EnvCodexHome, codexHome)
	t.Setenv(paths.EnvTmuxDir, tmuxDir)
	// A chat server loads the user's ~/.tmux.conf in real life; a fixture must
	// not, or the machine it runs on steers the test.
	t.Setenv(paths.EnvTmuxConf, "/dev/null")
}

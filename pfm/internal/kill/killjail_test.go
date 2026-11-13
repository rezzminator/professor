package kill

import (
	"os"
	"path/filepath"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

type killJail struct {
	root       string
	home       string
	sidDir     string
	tmuxDir    string
	claudeRoot string
	codexHome  string
}

func newKillJail(t *testing.T) killJail {
	t.Helper()
	root := t.TempDir()
	jail := killJail{
		root:       root,
		home:       filepath.Join(root, "home"),
		sidDir:     filepath.Join(root, "sid"),
		tmuxDir:    filepath.Join(root, "tmux"),
		claudeRoot: filepath.Join(root, "claude"),
		codexHome:  filepath.Join(root, "codex"),
	}
	for _, directory := range []string{
		jail.home,
		jail.sidDir,
		jail.tmuxDir,
		jail.claudeRoot,
		jail.codexHome,
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMUX_TMPDIR", filepath.Join(root, "tmp"))
	t.Setenv("PFM_SID_DIR", jail.sidDir)
	t.Setenv("PFM_CLAUDE_ROOTS", jail.claudeRoot)
	t.Setenv("PFM_CODEX_ROOT", jail.codexHome)
	t.Setenv("PFM_TMUX_DIR", jail.tmuxDir)
	t.Setenv(paths.EnvHome, jail.home)
	t.Setenv(paths.EnvCacheDB, filepath.Join(root, "pfm-cache.db"))
	t.Setenv(paths.EnvStateDB, paths.DefaultStateDB(jail.home))
	return jail
}

// resolved is the jail's paths, handed to New and NewFinisher through
// Dependencies.Paths, so no manager resolves them from the environment.
func (jail killJail) resolved() paths.Values {
	return paths.Values{
		Home:    jail.home,
		SIDDir:  jail.sidDir,
		Roots:   map[pfmengine.ID][]string{pfmengine.Claude: {jail.claudeRoot}, pfmengine.Codex: {jail.codexHome}},
		TmuxDir: jail.tmuxDir,
	}
}

// open is the jail's store, opened by the paths newKillJail set: the SQLite
// door serves both from memory, so code under test that opens the shared state
// again by its path (the Herdr ownership proof in internal/gather) reads the
// same database; a test handing it to another process keeps the files
// (testjail.DatabaseFiles).
func (killJail) open(t *testing.T) *store.Store {
	t.Helper()
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return database
}

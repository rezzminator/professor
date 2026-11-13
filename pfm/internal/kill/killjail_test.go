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
	// The databases sit under a regular file until openFile names the jail's own,
	// so a code path that opens the shared state or cache by its resolved path
	// (the Herdr ownership proof) fails over the in-memory store instead of
	// reading a database the test never seeded.
	noDatabase := filepath.Join(root, "no-database")
	if err := os.WriteFile(noDatabase, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvStateDB, filepath.Join(noDatabase, "state"))
	t.Setenv(paths.EnvCacheDB, filepath.Join(noDatabase, "cache"))
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

// open is the jail's store: both databases in memory (store.OpenMemory).
func (jail killJail) open(t *testing.T) *store.Store {
	t.Helper()
	database, err := store.OpenMemory()
	return jail.opened(t, database, err)
}

// openFile is the jail's store on disk under its private PFM_HOME, for the
// tests whose code under test opens the shared state again by its path (the
// Herdr ownership proof in internal/gather) or from another process.
func (jail killJail) openFile(t *testing.T) *store.Store {
	t.Helper()
	t.Setenv(paths.EnvCacheDB, filepath.Join(jail.root, "pfm-cache.db"))
	t.Setenv(paths.EnvStateDB, paths.DefaultStateDB(jail.home))
	database, err := store.Open()
	return jail.opened(t, database, err)
}

func (killJail) opened(t *testing.T, database *store.Store, err error) *store.Store {
	t.Helper()
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

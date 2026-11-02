package hostcheck

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
)

func retiredPathEnv(t *testing.T) Env {
	t.Helper()
	home := t.TempDir()
	return Env{
		Home:       home,
		Store:      filepath.Join(home, ".claude"),
		ConfigPath: filepath.Join(home, ".professor", config.FileName),
		StateDB:    filepath.Join(home, ".local", "state", "pfm", "pfm.db"),
		CacheDB:    filepath.Join(home, ".local", "state", "pfm", "pfm-cache.db"),
		Accounts:   []config.Account{{ID: 1, ConfigDir: config.DefaultAccountDir(home, 1)}},
	}
}

func writeRetired(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func rowFor(rows []Row, path string) (Row, bool) {
	for _, row := range rows {
		if row.Path == path {
			return row, true
		}
	}
	return Row{}, false
}

// TestRetiredPathsKeepLivePaths pins what is never stale: the account dir the
// config names, a backup without pfm's marker, doctor's own move-aside dirs,
// an unstamped file in an archive dir.
func TestRetiredPathsKeepLivePaths(t *testing.T) {
	env := retiredPathEnv(t)
	writeRetired(t, filepath.Join(env.Accounts[0].ConfigDir, "settings.json"))
	writeRetired(t, filepath.Join(env.Store, "settings.json.bak-20300101"))
	state := filepath.Dir(env.StateDB)
	writeRetired(t, filepath.Join(state, "stale-backup", "20300101-000000", ".zshrc"))
	writeRetired(t, filepath.Join(state, "stray-claude-state", "20300101-000000", ".claude.json"))
	writeRetired(t, filepath.Join(state, "retired-store-entries", "notes.txt"))
	env.Accounts = append(env.Accounts, config.Account{ID: 2, ConfigDir: config.DefaultAccountDir(env.Home, 2)})
	if err := os.MkdirAll(env.Accounts[1].ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}

	if rows := RetiredPaths(env); len(rows) != 0 {
		t.Fatalf("live paths reported stale: %+v", rows)
	}
}

// TestRetiredPathsWithoutAccountsCannotJudgeAccountDirs pins the error door:
// no configured account means no live set, a BLOCK, never "no dead dirs".
func TestRetiredPathsWithoutAccountsCannotJudgeAccountDirs(t *testing.T) {
	env := retiredPathEnv(t)
	env.Accounts = nil
	if err := os.MkdirAll(config.DefaultAccountDir(env.Home, 3), 0o700); err != nil {
		t.Fatal(err)
	}
	rows := RetiredPaths(env)
	if len(rows) != 1 || rows[0].Severity != Block || rows[0].Check != "dead-account-dir" {
		t.Fatalf("rows=%+v, want one dead-account-dir BLOCK", rows)
	}
}

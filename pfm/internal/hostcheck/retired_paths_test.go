package hostcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func retiredPathEnv(t *testing.T) Env {
	t.Helper()
	home := t.TempDir()
	return Env{
		Home:            home,
		Store:           filepath.Join(home, ".claude"),
		ConfigPath:      filepath.Join(home, ".professor", config.FileName),
		LegacyConfigDir: filepath.Join(home, ".config", "pfm"),
		StateDB:         filepath.Join(home, ".local", "state", "pfm", "pfm.db"),
		CacheDB:         filepath.Join(home, ".local", "state", "pfm", "pfm-cache.db"),
		ManagedRoot:     filepath.Join(home, ".local", "share", "pfm", "install"),
		Accounts:        []config.Account{{ID: 1, ConfigDir: config.DefaultAccountDir(home, 1)}},
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

// TestRetiredPathsHoldAPathAwaitingItsMove pins the held door: a retired path
// whose successor is absent still carries the operator's data, so it is a
// BLOCK naming the move, never a WARN the purge would carry off.
func TestRetiredPathsHoldAPathAwaitingItsMove(t *testing.T) {
	env := retiredPathEnv(t)
	legacyConfig := filepath.Join(env.LegacyConfigDir, config.FileName)
	legacyState := paths.LegacyStateDB(env.Home)
	writeRetired(t, legacyConfig)
	writeRetired(t, legacyState)

	rows := RetiredPaths(env)
	for path, want := range map[string]string{env.LegacyConfigDir: "legacy-config-dir", legacyState: "legacy-state-db"} {
		row, ok := rowFor(rows, path)
		if !ok || row.Severity != Block || row.Check != want || !strings.Contains(row.Problem, "awaits its move") {
			t.Fatalf("held %s: row=%+v ok=%v rows=%+v", path, row, ok, rows)
		}
	}

	writeRetired(t, env.ConfigPath)
	writeRetired(t, env.StateDB)
	rows = RetiredPaths(env)
	for _, path := range []string{env.LegacyConfigDir, legacyState} {
		if row, ok := rowFor(rows, path); !ok || row.Severity != Warn {
			t.Fatalf("successor present %s: row=%+v ok=%v rows=%+v", path, row, ok, rows)
		}
	}
}

// TestRetiredPathsKeepLivePaths pins what is never stale: the account dir the
// config names, a backup without pfm's marker, the config dir in use.
func TestRetiredPathsKeepLivePaths(t *testing.T) {
	env := retiredPathEnv(t)
	env.ConfigPath = filepath.Join(env.LegacyConfigDir, config.FileName)
	writeRetired(t, env.ConfigPath)
	writeRetired(t, filepath.Join(env.Accounts[0].ConfigDir, "settings.json"))
	writeRetired(t, filepath.Join(env.Store, "settings.json.bak-20300101"))
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

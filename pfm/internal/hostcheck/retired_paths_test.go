package hostcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
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
// config names, a backup without pfm's marker, the config dir in use, doctor's
// own move-aside dirs, an unstamped file in an archive dir.
func TestRetiredPathsKeepLivePaths(t *testing.T) {
	env := retiredPathEnv(t)
	env.ConfigPath = filepath.Join(env.LegacyConfigDir, config.FileName)
	writeRetired(t, env.ConfigPath)
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

// TestRetiredPathsNameTheOldEngineSyncConfig pins the engine-sync detector:
// engine-sync exits 2 beside {config}/engine-sync/config.json or targets.json
// while ~/.claude/engine-sync.json is absent, in the default directory and in an
// absolute XDG_CONFIG_HOME's; a BLOCK naming pfm install and the move then, a
// WARN once it exists that claims no exit, nothing for a clean home or a
// relative XDG_CONFIG_HOME.
func TestRetiredPathsNameTheOldEngineSyncConfig(t *testing.T) {
	env := retiredPathEnv(t)
	successor := filepath.Join(env.Home, ".claude", "engine-sync.json")
	xdg := filepath.Join(env.Home, "xdg")
	env.Environ = []string{"XDG_CONFIG_HOME=" + xdg}
	if row, ok := rowFor(RetiredPaths(env), successor); ok {
		t.Fatalf("a clean home raised %+v", row)
	}
	olds := []string{
		filepath.Join(env.Home, ".config", "engine-sync", "config.json"),
		filepath.Join(env.Home, ".config", "engine-sync", "targets.json"),
		filepath.Join(xdg, "engine-sync", "config.json"),
		filepath.Join(xdg, "engine-sync", "targets.json"),
	}
	for _, path := range olds {
		writeRetired(t, path)
	}
	rows := RetiredPaths(env)
	for _, path := range olds {
		row, ok := rowFor(rows, path)
		if !ok || row.Severity != Block || row.Check != "engine-sync-old-config" ||
			!strings.Contains(row.Problem, "awaits its move to "+successor) ||
			!strings.Contains(row.Fix, "run pfm install --yes") ||
			!strings.Contains(row.Fix, "move its keys into "+successor) {
			t.Fatalf("held %s: row=%+v ok=%v", path, row, ok)
		}
	}
	writeRetired(t, successor)
	rows = RetiredPaths(env)
	for _, path := range olds {
		if row, ok := rowFor(rows, path); !ok || row.Severity != Warn || row.Check != "engine-sync-old-config" ||
			strings.Contains(row.Problem, "exits 2") {
			t.Fatalf("successor present %s: row=%+v ok=%v", path, row, ok)
		}
	}
	env.Environ = []string{"XDG_CONFIG_HOME=relative"}
	rows = RetiredPaths(env)
	if _, ok := rowFor(rows, olds[2]); ok {
		t.Fatalf("a relative XDG_CONFIG_HOME was read: %+v", rows)
	}
	if _, ok := rowFor(rows, olds[0]); !ok {
		t.Fatalf("the default old config dir was not checked: %+v", rows)
	}
}

// TestRetiredPathsNameTheRetiredGeneratedStore pins the home stores engine-sync
// once rendered agents into: each that still exists is a WARN row of kind
// retired-generated-store naming the directory (the purge moves it); an absent one
// is no row.
func TestRetiredPathsNameTheRetiredGeneratedStore(t *testing.T) {
	env := retiredPathEnv(t)
	stores := installer.RetiredGeneratedStores(env.Home)
	writeRetired(t, filepath.Join(stores[1], "rr.toml"))
	rows := RetiredPaths(env)
	row, ok := rowFor(rows, stores[1])
	if !ok || row.Severity != Warn || row.Check != "retired-generated-store" {
		t.Fatalf("codex-agents store: row=%+v ok=%v rows=%+v", row, ok, rows)
	}
	if absent, found := rowFor(rows, stores[0]); found {
		t.Fatalf("absent claude-agents store reported: %+v", absent)
	}
	writeRetired(t, filepath.Join(stores[0], "astra.md"))
	if row, ok := rowFor(RetiredPaths(env), stores[0]); !ok || row.Check != "retired-generated-store" {
		t.Fatalf("claude-agents store: row=%+v ok=%v", row, ok)
	}
}

package hostcheck

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	goRuntime "runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func fixtureEnv(t *testing.T) Env {
	t.Helper()
	home := t.TempDir()
	return Env{
		Home: home, Store: installer.ClaudeStore(home),
		ConfigPath:      filepath.Join(home, "clone", config.FileName),
		LegacyConfigDir: filepath.Join(home, ".config", "pfm"),
		StateDB:         paths.DefaultStateDB(home), CacheDB: paths.DefaultCacheDB(home),
		ManagedRoot: installer.ManagedRoot(home), MCPPort: config.DefaultMCPPort,
		Accounts: []config.Account{{ID: 1, ConfigDir: config.DefaultAccountDir(home, 1)}},
		Now:      time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
	}
}

func TestLoadedStoreAccountReachesHostGate(t *testing.T) {
	env := fixtureEnv(t)
	makeDir(t, env.Store)
	writeFile(t, env.ConfigPath, `{"version":2,"accounts":[{"id":1,"configDir":"~/.claude"}]}`)
	loaded, err := config.Load(env.ConfigPath, env.Home, nil)
	if err != nil {
		t.Fatalf("Load(store account) = %v, want nil", err)
	}
	env.Accounts = loaded.Accounts
	assertRows(t, detect(t, "account-is-store", env), Row{
		Block,
		"account-is-store",
		env.Store,
		"account 1's config dir resolves to the store " + env.Store,
		"point accounts[1].configDir in " + env.ConfigPath + " at " + config.DefaultAccountDir(env.Home, 1),
	})
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()
	makeDir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func makeDir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func detect(t *testing.T, check string, env Env) []Row {
	t.Helper()
	for _, detector := range Detectors() {
		if detector.Check == check {
			rows, err := detector.Detect(env)
			if err != nil {
				t.Fatalf("detect %s: %v", check, err)
			}
			return rows
		}
	}
	t.Fatalf("missing detector %s", check)
	return nil
}

func assertRows(t *testing.T, got []Row, want ...Row) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("rows=%+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("row[%d]=%+v, want %+v", i, got[i], want[i])
		}
	}
}

func assertUnreadable(t *testing.T, got []Row, check, path string, cause error) {
	t.Helper()
	assertRows(
		t,
		got,
		Row{
			Block,
			check,
			path,
			fmt.Sprintf("UNREADABLE %s: %v", path, cause),
			"make " + path + " readable to you, then rerun",
		},
	)
}

func TestHostcheckAPI(t *testing.T) {
	row := Row{Block, "a", "/invented/path", "a problem", "a fix"}
	if got := row.Line(); got != "BLOCK a /invented/path — a problem" {
		t.Fatalf("line=%q", got)
	}
	row.Severity = Warn
	if got := row.Line(); got != "WARN a /invented/path — a problem" {
		t.Fatalf("line=%q", got)
	}
	rows := []Row{{Severity: Block}, {Severity: Warn}, {Severity: Block}}
	if Count(rows, Block) != 2 || Count(rows, Warn) != 1 || Count(nil, Block) != 0 {
		t.Fatal("severity count")
	}
	want := strings.Fields(
		"legacy-config legacy-harvester-config pre-split-config legacy-state-db legacy-cache-db legacy-harvester-cache pfm-settings pfm-mcp memory-helpers staged-shim staged-prompts shared-db stray-dir account-is-store store-identity home-state-file account-entry-real retired-store-entry unclassified third-party-mcp stale-state-tmp beside-backup",
	)
	var got []string
	for _, detector := range Detectors() {
		got = append(got, detector.Check)
		if detector.Detect == nil {
			t.Fatal("nil detector")
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("detectors=%v", got)
	}
}

func TestRunErrorConversion(t *testing.T) {
	env := fixtureEnv(t)
	path := filepath.Join(env.Home, "unreadable")
	rows := runDetectors(env, []Detector{
		{"path", func(Env) ([]Row, error) {
			err := fmt.Errorf("wrapped: %w", &fs.PathError{Op: "read", Path: path, Err: syscall.ENOTDIR})
			return []Row{{Severity: Warn, Check: "found"}}, err
		}},
		{"generic", func(Env) ([]Row, error) { return nil, errors.New("failed") }},
		{"absent", func(Env) ([]Row, error) { return nil, fmt.Errorf("absent: %w", fs.ErrNotExist) }},
		{"later", func(Env) ([]Row, error) { return []Row{{Severity: Warn, Check: "later"}}, nil }},
	})
	assertRows(
		t,
		rows,
		Row{Severity: Warn, Check: "found"},
		Row{
			Block,
			"path",
			path,
			"UNREADABLE " + path + ": not a directory",
			"make " + path + " readable to you, then rerun",
		},
		Row{
			Block,
			"generic",
			"generic",
			"UNREADABLE generic: failed",
			"make generic readable to you, then rerun",
		},
		Row{Severity: Warn, Check: "later"},
	)
}

func TestEnvFor(t *testing.T) {
	env := fixtureEnv(t)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(env.Home, "xdg"))
	runtime := config.Runtime{
		Config:         config.Config{Path: env.ConfigPath, Accounts: env.Accounts},
		Paths:          paths.Values{Home: env.Home, StateDB: env.StateDB, CacheDB: env.CacheDB},
		ConfigExplicit: true,
	}
	runtime.Config.MCP.HTTP.Port = 19200
	runtime.Config.Harvester.Cache.Dir = filepath.Join(env.Home, "custom-cache")
	got := EnvFor(runtime, env.Now)
	env.ConfigExplicit = true
	env.LegacyConfigDir = filepath.Join(env.Home, "xdg", "pfm")
	env.MCPPort = 19200
	env.HarvesterCacheDir = runtime.Config.Harvester.Cache.Dir
	if !reflect.DeepEqual(got, env) {
		t.Fatalf("env=%+v want=%+v", got, env)
	}
	clone := filepath.Dir(env.ConfigPath)
	makeDir(t, clone)
	if err := paths.WriteSourceRepoMarker(env.Home, clone); err != nil {
		t.Fatal(err)
	}
	if got := EnvFor(runtime, env.Now); got.CloneConfigPath != env.ConfigPath {
		t.Fatalf("recorded clone config=%q, want %q", got.CloneConfigPath, env.ConfigPath)
	}
	if err := os.Rename(clone, clone+".moved"); err != nil {
		t.Fatal(err)
	}
	if got := EnvFor(runtime, env.Now); got.CloneConfigPath != "" {
		t.Fatalf("unusable marker clone config=%q, want empty", got.CloneConfigPath)
	}
}

func TestRunHealthyAndAbsent(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		assertRows(t, RunAll(fixtureEnv(t)))
	})
	healthy := func(t *testing.T, content string) Env {
		env := fixtureEnv(t)
		makeDir(t, env.Store)
		makeDir(t, env.Accounts[0].ConfigDir)
		writeFile(t, env.ConfigPath, content)
		for _, entry := range installer.StoreEntries {
			path := filepath.Join(env.Store, entry.Name)
			if entry.Dir {
				makeDir(t, path)
			} else {
				writeFile(t, path, entry.Seed)
			}
			if err := os.Symlink(path, filepath.Join(env.Accounts[0].ConfigDir, entry.Name)); err != nil {
				t.Fatal(err)
			}
		}
		return env
	}
	t.Run("healthy", func(t *testing.T) {
		assertRows(t, RunAll(healthy(t, "{}")))
	})
	// A first pfm install seeds the clone's tracked example.pfm.config.json
	// verbatim (installer.InstallConfig); the next install, update or doctor
	// must pass the gate on it.
	t.Run("seeded example", func(t *testing.T) {
		_, source, _, ok := goRuntime.Caller(0)
		if !ok {
			t.Fatal("find test source")
		}
		example, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "..", "example.pfm.config.json"))
		if err != nil {
			t.Fatal(err)
		}
		assertRows(t, RunAll(healthy(t, string(example))))
	})
}

func oneFile(remove, keep string) string {
	return remove + " and " + keep + " are one file through a link: delete neither; " +
		"replace the link with a real copy, then rerun pfm doctor"
}

// TestRemoveKeeping pins the one guard on a printed fix that deletes one path
// while keeping another: one file under two names never prints the delete, and
// a sameness stat that fails prints the UNREADABLE row in place of any fix.
func TestRemoveKeeping(t *testing.T) {
	const fix = "keep it; after checking, rm -r it"
	for _, test := range []struct {
		name  string
		shape func(t *testing.T, dir, remove string) string
		want  string
	}{
		{"different-files", func(t *testing.T, dir, _ string) string {
			keep := filepath.Join(dir, "account", "entry")
			writeFile(t, keep, "keep")
			return keep
		}, "fix"},
		{"keep-absent", func(_ *testing.T, dir, _ string) string {
			return filepath.Join(dir, "account", "entry")
		}, "fix"},
		{"symlink", func(t *testing.T, dir, remove string) string {
			return linkEntry(t, dir, remove, os.Symlink)
		}, "same"},
		{"hard-link", func(t *testing.T, dir, remove string) string {
			return linkEntry(t, dir, remove, os.Link)
		}, "same"},
		{"linked-dir", func(t *testing.T, dir, remove string) string {
			if err := os.Symlink(filepath.Dir(remove), filepath.Join(dir, "account")); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(dir, "account", "entry")
		}, "same"},
		{"stat-error", func(t *testing.T, dir, _ string) string {
			return linkEntry(t, dir, filepath.Join(dir, "gone"), os.Symlink)
		}, "unreadable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			remove := filepath.Join(dir, "store", "entry")
			writeFile(t, remove, "data")
			keep := test.shape(t, dir, remove)
			var rows []Row
			got, ok := removeKeeping(&rows, "guard", keep, fix, filepath.Join(dir, "store", "other"), remove)
			switch test.want {
			case "unreadable":
				assertUnreadable(t, rows, "guard", keep, syscall.ENOENT)
				if ok || got != "" {
					t.Fatalf("got %q %v, want no fix", got, ok)
				}
			case "same":
				assertRows(t, rows)
				if !ok || got != oneFile(remove, keep) || strings.Contains(got, "rm ") {
					t.Fatalf("got %q %v, want %q", got, ok, oneFile(remove, keep))
				}
			default:
				assertRows(t, rows)
				if !ok || got != fix {
					t.Fatalf("got %q %v, want %q", got, ok, fix)
				}
			}
		})
	}
}

func linkEntry(t *testing.T, dir, target string, link func(string, string) error) string {
	t.Helper()
	keep := filepath.Join(dir, "account", "entry")
	makeDir(t, filepath.Dir(keep))
	if err := link(target, keep); err != nil {
		t.Fatal(err)
	}
	return keep
}

// TestRemoveKeepingDoors routes every printed fix that deletes one path while
// keeping another through removeKeeping: each door, shown one file under two
// names, prints the guard's fix and no rm.
func TestRemoveKeepingDoors(t *testing.T) {
	for _, door := range []struct {
		check string
		shape func(t *testing.T, env Env) (remove, keep string)
	}{
		{"legacy-config", func(t *testing.T, env Env) (string, string) {
			remove := filepath.Join(env.LegacyConfigDir, config.FileName)
			return remove, hardLink(t, env.ConfigPath, remove)
		}},
		{"legacy-harvester-config", func(t *testing.T, env Env) (string, string) {
			remove := filepath.Join(env.LegacyConfigDir, "harvester.config.json")
			return remove, hardLink(t, filepath.Join(filepath.Dir(env.ConfigPath), "harvester.config.json"), remove)
		}},
		{"legacy-state-db", func(t *testing.T, env Env) (string, string) {
			remove := paths.LegacyStateDB(env.Home)
			writeFile(t, remove+"-wal", "wal")
			return remove, hardLink(t, env.StateDB, remove)
		}},
		{"legacy-harvester-cache", func(t *testing.T, env Env) (string, string) {
			remove, keep := paths.LegacyHarvesterCacheDir(env.Home), paths.HarvesterCacheDir(env.Home)
			makeDir(t, remove)
			symlink(t, remove, keep)
			return remove, keep
		}},
		{"pre-split-config", func(t *testing.T, env Env) (string, string) {
			remove := filepath.Join(filepath.Dir(env.ConfigPath), "config.json")
			writeFile(t, remove, "{}")
			symlink(t, remove, env.ConfigPath)
			return remove, env.ConfigPath
		}},
		{"home-state-file", func(t *testing.T, env Env) (string, string) {
			remove, keep := filepath.Join(env.Home, ".claude.json"), filepath.Join(env.Accounts[0].ConfigDir, ".claude.json")
			writeFile(t, remove, "{}")
			symlink(t, remove, keep)
			return remove, keep
		}},
		{"account-entry-real", func(t *testing.T, env Env) (string, string) {
			remove, keep := filepath.Join(env.Accounts[0].ConfigDir, "settings.json"), filepath.Join(env.Store, "settings.json")
			writeFile(t, remove, "{}")
			symlink(t, remove, keep)
			return remove, keep
		}},
		{"beside-backup", func(t *testing.T, env Env) (string, string) {
			remove, keep := filepath.Join(env.Store, "settings.json.bak-1"), filepath.Join(env.Store, "settings.json")
			writeFile(t, remove, "{}")
			symlink(t, remove, keep)
			return remove, keep
		}},
		{"store-identity", func(t *testing.T, env Env) (string, string) {
			remove := filepath.Join(env.Store, ".credentials.json")
			keep := filepath.Join(env.Accounts[0].ConfigDir, ".credentials.json")
			writeFile(t, remove, "{}")
			symlink(t, remove, keep)
			return remove, keep
		}},
	} {
		t.Run(door.check, func(t *testing.T) {
			env := fixtureEnv(t)
			remove, keep := door.shape(t, env)
			rows := detect(t, door.check, env)
			if len(rows) != 1 || rows[0].Fix != oneFile(remove, keep) || strings.Contains(rows[0].Fix, "rm ") {
				t.Fatalf("rows=%+v, want one fix %q", rows, oneFile(remove, keep))
			}
		})
	}
}

func hardLink(t *testing.T, keep, remove string) string {
	t.Helper()
	writeFile(t, keep, "{}")
	makeDir(t, filepath.Dir(remove))
	if err := os.Link(keep, remove); err != nil {
		t.Fatal(err)
	}
	return keep
}

func symlink(t *testing.T, target, path string) {
	t.Helper()
	makeDir(t, filepath.Dir(path))
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

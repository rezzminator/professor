package hostcheck

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	goRuntime "runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/retired"
)

func fixtureEnv(t *testing.T) Env {
	t.Helper()
	home := t.TempDir()
	return Env{
		Home: home, Store: installer.ClaudeStore(home),
		ConfigPath: filepath.Join(home, "clone", config.FileName),
		StateDB:    paths.DefaultStateDB(home), CacheDB: paths.DefaultCacheDB(home),
		Accounts: []config.Account{{ID: 1, ConfigDir: config.DefaultAccountDir(home, 1)}},
		Now:      time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		Retired:  fixtureRetired,
	}
}

// fixtureRetired are the retired lists the detectors are tested on; the
// shipped file's lists are empty.
var fixtureRetired = retired.Lists{
	StoreEntries: []string{".last-update-result.json", "stats-cache.json"},
	HookCommands: []retired.HookCommand{{Name: "clear-hide", Subcommand: "internal clear-hide"}},
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
		"pfm-settings stray-dir account-is-store store-identity home-state-file account-entry-real retired-store-entry unclassified third-party-mcp stale-state-tmp beside-backup function-hook-modules shell-claude-env tmux-servers",
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
	writeFile(t, filepath.Join(env.Home, ".professor", "templates", "global", "retired.json"),
		`{"storeEntries":[".last-update-result.json","stats-cache.json"],`+
			`"hookCommands":[{"name":"clear-hide","subcommand":"internal clear-hide"}]}`)
	runtime := config.Runtime{
		Config: config.Config{Path: env.ConfigPath, Accounts: env.Accounts},
		Paths:  paths.Values{Home: env.Home, StateDB: env.StateDB, CacheDB: env.CacheDB},
	}
	got := EnvFor(runtime, env.Now)
	env.Environ = os.Environ()
	if !slices.Contains(got.Environ, "XDG_CONFIG_HOME="+filepath.Join(env.Home, "xdg")) {
		t.Fatalf("env.Environ=%v lacks the process environment", got.Environ)
	}
	if !reflect.DeepEqual(got, env) {
		t.Fatalf("env=%+v want=%+v", got, env)
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

func jsonDecodeError(raw string) error { var value any; return json.Unmarshal([]byte(raw), &value) }

func symlink(t *testing.T, target, path string) {
	t.Helper()
	makeDir(t, filepath.Dir(path))
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

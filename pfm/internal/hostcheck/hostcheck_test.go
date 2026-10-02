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
		"legacy-config legacy-harvester-config pre-split-config legacy-state-db legacy-cache-db legacy-harvester-cache pfm-settings pfm-mcp memory-helpers staged-shim staged-prompts shared-db stray-dir account-is-store store-identity home-state-file account-entry-real unclassified third-party-mcp stale-state-tmp beside-backup",
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

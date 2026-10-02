package hostcheck

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

func TestAccountIsStore(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(fmt.Sprint(symlink), func(t *testing.T) {
			env := fixtureEnv(t)
			makeDir(t, env.Store)
			path := env.Store
			fix := "point accounts[1].configDir in " + env.ConfigPath + " at " + config.DefaultAccountDir(env.Home, 1)
			if symlink {
				path = env.Accounts[0].ConfigDir
				makeDir(t, filepath.Dir(path))
				if err := os.Symlink(env.Store, path); err != nil {
					t.Fatal(err)
				}
				fix = "rm " + path + " && mkdir -m 700 " + path
			}
			env.Accounts[0].ConfigDir = path
			writeFile(t, filepath.Join(env.Store, "settings.json"), "{}")
			assertRows(
				t,
				detect(t, "account-is-store", env),
				Row{Block, "account-is-store", path, "account 1's config dir resolves to the store " + env.Store, fix},
			)
			assertRows(t, detect(t, "account-entry-real", env))
		})
	}
	t.Run("unreadable-continues", func(t *testing.T) {
		env := fixtureEnv(t)
		parent := filepath.Join(env.Home, "file")
		writeFile(t, parent, "file")
		path := filepath.Join(parent, "account")
		env.Accounts[0].ConfigDir = path
		makeDir(t, env.Store)
		env.Accounts = append(env.Accounts, config.Account{ID: 2, ConfigDir: env.Store})
		rows := detect(t, "account-is-store", env)
		assertUnreadable(t, rows[:1], "account-is-store", path, syscall.ENOTDIR)
		assertRows(
			t,
			rows[1:],
			Row{
				Block,
				"account-is-store",
				env.Store,
				"account 2's config dir resolves to the store " + env.Store,
				"point accounts[2].configDir in " + env.ConfigPath + " at " + config.DefaultAccountDir(env.Home, 2),
			},
		)
	})
}

// TestAccountDirSymlink pins the host-check doors of the account-dir rule.
func TestAccountDirSymlink(t *testing.T) {
	for _, scenario := range []string{"outside", "store", "inside-store", "dangling"} {
		t.Run(scenario, func(t *testing.T) {
			env := fixtureEnv(t)
			makeDir(t, filepath.Join(env.Store, "projects"))
			path := env.Accounts[0].ConfigDir
			makeDir(t, filepath.Dir(path))
			target := map[string]string{
				"outside":      filepath.Join(env.Home, "data", "cc1"),
				"store":        env.Store,
				"inside-store": filepath.Join(env.Store, "projects"),
				"dangling":     filepath.Join(env.Home, "gone"),
			}[scenario]
			if scenario == "outside" {
				makeDir(t, filepath.Join(target, "plugins"))
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			rows := detect(t, "account-is-store", env)
			entryRows := detect(t, "account-entry-real", env)
			switch scenario {
			case "outside":
				assertRows(t, rows)
				plugins := filepath.Join(path, "plugins")
				assertRows(
					t,
					entryRows,
					Row{
						Block,
						"account-entry-real",
						plugins,
						"plugins is a real dir; it belongs in the store",
						"the store keeps its copy (reinstallable): rm -r " + plugins,
					},
				)
			case "dangling":
				assertUnreadable(t, rows, "account-is-store", path, syscall.ENOENT)
				assertRows(t, entryRows)
			default:
				assertRows(
					t,
					rows,
					Row{
						Block,
						"account-is-store",
						path,
						"account 1's config dir resolves to the store " + env.Store,
						"rm " + path + " && mkdir -m 700 " + path,
					},
				)
				assertRows(t, entryRows)
			}
		})
	}
}

func TestStoreIdentity(t *testing.T) {
	for _, present := range []bool{false, true} {
		t.Run(fmt.Sprint(present), func(t *testing.T) {
			env := fixtureEnv(t)
			first := env.Accounts[0].ConfigDir
			env.Accounts = append(
				[]config.Account{{ID: 9, ConfigDir: config.DefaultAccountDir(env.Home, 9)}},
				env.Accounts...)
			var want []Row
			for _, entry := range installer.AccountEntries {
				if entry == "state" {
					entry = "state/mcp-discover-verdicts.json"
				}
				path, target := filepath.Join(env.Store, entry), filepath.Join(first, entry)
				writeFile(t, path, "data")
				dirs := first
				if filepath.Dir(target) != first {
					dirs += " " + filepath.Dir(target)
				}
				fix := "mkdir -m 700 -p " + dirs + " && mv " + path + " " + target
				if present {
					writeFile(t, target, "data")
					fix = "keep " + target + "; after checking, rm -r " + path
				}
				want = append(
					want,
					Row{Block, "store-identity", path, entry + " is account identity inside the store", fix},
				)
			}
			assertRows(t, detect(t, "store-identity", env), want...)
		})
	}
	t.Run("state-directory-alone", func(t *testing.T) {
		env := fixtureEnv(t)
		writeFile(t, filepath.Join(env.Store, "state", "other-state"), "data")
		assertRows(t, detect(t, "store-identity", env))
	})
	t.Run("no-account", func(t *testing.T) {
		env := fixtureEnv(t)
		env.Accounts = nil
		path := filepath.Join(env.Store, ".credentials.json")
		writeFile(t, path, "{}")
		assertRows(
			t,
			detect(t, "store-identity", env),
			Row{
				Block,
				"store-identity",
				path,
				".credentials.json is account identity inside the store",
				"mkdir -m 700 -p " + config.DefaultAccountDir(env.Home, 1) + " && mv " + path + " " +
					filepath.Join(config.DefaultAccountDir(env.Home, 1), ".credentials.json"),
			},
		)
	})
	t.Run("unreadable-and-later-paths", func(t *testing.T) {
		env := fixtureEnv(t)
		writeFile(t, filepath.Join(env.Store, "state"), "file")
		other := filepath.Join(env.Store, "telemetry")
		makeDir(t, other)
		rows := detect(t, "store-identity", env)
		assertUnreadable(
			t,
			rows[:1],
			"store-identity",
			filepath.Join(env.Store, "state", "mcp-discover-verdicts.json"),
			syscall.ENOTDIR,
		)
		assertRows(
			t,
			rows[1:],
			Row{
				Block,
				"store-identity",
				other,
				"telemetry is account identity inside the store",
				"mkdir -m 700 -p " + env.Accounts[0].ConfigDir + " && mv " + other + " " +
					filepath.Join(env.Accounts[0].ConfigDir, "telemetry"),
			},
		)
	})
}

func TestHomeStateFile(t *testing.T) {
	env := fixtureEnv(t)
	path := filepath.Join(env.Home, ".claude.json")
	writeFile(t, path, "{}")
	assertRows(
		t,
		detect(t, "home-state-file", env),
		Row{
			Warn,
			"home-state-file",
			path,
			"a Claude launched without CLAUDE_CONFIG_DIR wrote this state file",
			"check it names the same oauthAccount as " + filepath.Join(
				env.Accounts[0].ConfigDir,
				".claude.json",
			) + ", then rm " + path,
		},
	)
	t.Run("unreadable", func(t *testing.T) {
		env := fixtureEnv(t)
		path := filepath.Join(env.Home, ".claude.json")
		makeDir(t, path)
		assertUnreadable(t, detect(t, "home-state-file", env), "home-state-file", path, syscall.EISDIR)
	})
}

func TestAccountEntryReal(t *testing.T) {
	env := fixtureEnv(t)
	var want []Row
	fixes := map[string]string{}
	for _, name := range strings.Fields("projects file-history tasks session-env paste-cache shell-snapshots plans uploads downloads teams") {
		fixes[name] = "union into the store: cp -an {path}/. {store}/{entry}/ ; diff -rq {path} {store}/{entry} | grep -v '^Only in {store}/{entry}' (empty: nothing differs) ; rm -r {path}"
	}
	for _, name := range strings.Fields("agents commands skills rules themes") {
		fixes[name] = "move what the store lacks: mv -n {path}/* {store}/{entry}/ ; compare what is left, then rm -r {path}"
	}
	for _, name := range strings.Fields("stats-cache.json .last-cleanup .last-update-result.json gh-pr-status-cache.json") {
		fixes[name] = "a cache: rm {path}"
	}
	fixes["history.jsonl"] = "interleave by timestamp: jq -c -s 'sort_by(.timestamp)[]' {store}/history.jsonl {path} > {store}/history.jsonl.new && mv {store}/history.jsonl.new {store}/history.jsonl && rm {path}"
	fixes["plugins"] = "the store keeps its copy (reinstallable): rm -r {path}"
	fixes["settings.json"] = "copy any key you keep into {store}/settings.json, then rm {path}"
	fixes["CLAUDE.md"] = "append what you keep to {store}/CLAUDE.md, then rm {path}"
	for _, entry := range installer.StoreEntries {
		path := filepath.Join(env.Accounts[0].ConfigDir, entry.Name)
		kind := "file"
		if entry.Dir {
			kind = "dir"
			makeDir(t, path)
		} else {
			writeFile(t, path, entry.Seed)
		}
		fix, present := fixes[entry.Name]
		if !present {
			t.Fatalf("no contract fixture for %s", entry.Name)
		}
		fix = strings.NewReplacer("{path}", path, "{store}", env.Store, "{entry}", entry.Name).Replace(fix)
		want = append(
			want,
			Row{
				Block,
				"account-entry-real",
				path,
				entry.Name + " is a real " + kind + "; it belongs in the store",
				fix,
			},
		)
	}
	assertRows(t, detect(t, "account-entry-real", env), want...)
	t.Run("unreadable-continues", func(t *testing.T) {
		env := fixtureEnv(t)
		writeFile(t, env.Accounts[0].ConfigDir, "file")
		other := config.DefaultAccountDir(env.Home, 2)
		env.Accounts = append(env.Accounts, config.Account{ID: 2, ConfigDir: other})
		path := filepath.Join(other, "plugins")
		makeDir(t, path)
		rows := detect(t, "account-entry-real", env)
		if len(rows) != len(installer.StoreEntries)+1 {
			t.Fatalf("rows=%v", rows)
		}
		for i, entry := range installer.StoreEntries {
			assertUnreadable(
				t,
				rows[i:i+1],
				"account-entry-real",
				filepath.Join(env.Accounts[0].ConfigDir, entry.Name),
				syscall.ENOTDIR,
			)
		}
		assertRows(
			t,
			rows[len(rows)-1:],
			Row{
				Block,
				"account-entry-real",
				path,
				"plugins is a real dir; it belongs in the store",
				"the store keeps its copy (reinstallable): rm -r " + path,
			},
		)
	})
	t.Run("account-order", func(t *testing.T) {
		env := fixtureEnv(t)
		first := env.Accounts[0]
		second := config.Account{ID: 3, ConfigDir: config.DefaultAccountDir(env.Home, 3)}
		env.Accounts = []config.Account{second, first}
		for _, account := range env.Accounts {
			makeDir(t, filepath.Join(account.ConfigDir, "plugins"))
		}
		rows := detect(t, "account-entry-real", env)
		if len(rows) != 2 || rows[0].Path != filepath.Join(first.ConfigDir, "plugins") ||
			rows[1].Path != filepath.Join(second.ConfigDir, "plugins") {
			t.Fatalf("rows=%v", rows)
		}
		if env.Accounts[0].ID != second.ID {
			t.Fatal("environment roster reordered")
		}
	})
}

func TestStoreLeftovers(t *testing.T) {
	for _, check := range []string{"unclassified", "stale-state-tmp", "beside-backup"} {
		t.Run(check, func(t *testing.T) {
			env := fixtureEnv(t)
			name, problem, fix := "future-entry", "UNCLASSIFIED — on neither the shared nor the per-account list", "keep it; pfm doctor names it until a pfm release classifies it"
			if check == "stale-state-tmp" {
				name, problem = ".claude.json.tmp.1", "Claude's stale state temp file"
			}
			if check == "beside-backup" {
				name, problem = "settings.json.pre-professor-fixture", "a backup beside the file"
			}
			var want []Row
			dirs := []string{env.Store, env.Accounts[0].ConfigDir}
			if check == "stale-state-tmp" {
				dirs = append(dirs, env.Home)
			}
			for _, dir := range dirs {
				path := filepath.Join(dir, name)
				writeFile(t, path, "data")
				switch check {
				case "stale-state-tmp":
					old := env.Now.Add(-25 * time.Hour)
					if err := os.Chtimes(path, old, old); err != nil {
						t.Fatal(err)
					}
					fix = "rm " + path
				case "beside-backup":
					fix = "rm -r " + path + " once you no longer need it"
				}
				want = append(want, Row{Warn, check, path, problem, fix})
			}
			assertRows(t, detect(t, check, env), want...)
		})
		t.Run(check+"-unreadable-continues", func(t *testing.T) {
			env := fixtureEnv(t)
			writeFile(t, env.Store, "file")
			name, problem, fix := "future-entry", "UNCLASSIFIED — on neither the shared nor the per-account list", "keep it; pfm doctor names it until a pfm release classifies it"
			switch check {
			case "stale-state-tmp":
				name, problem = ".claude.json.tmp.1", "Claude's stale state temp file"
			case "beside-backup":
				name, problem = "settings.json.bak-fixture", "a backup beside the file"
			}
			other := filepath.Join(env.Accounts[0].ConfigDir, name)
			writeFile(t, other, "data")
			switch check {
			case "stale-state-tmp":
				stamp := env.Now.Add(-25 * time.Hour)
				if err := os.Chtimes(other, stamp, stamp); err != nil {
					t.Fatal(err)
				}
				fix = "rm " + other
			case "beside-backup":
				fix = "rm -r " + other + " once you no longer need it"
			}
			rows := detect(t, check, env)
			assertUnreadable(t, rows[:1], check, env.Store, syscall.ENOTDIR)
			assertRows(t, rows[1:], Row{Warn, check, other, problem, fix})
		})
	}
}

func TestStaleStateTmpAge(t *testing.T) {
	env := fixtureEnv(t)
	for _, age := range []time.Duration{25, 24, 23} {
		path := filepath.Join(env.Store, fmt.Sprintf(".claude.json.tmp.%d", age))
		writeFile(t, path, "{}")
		stamp := env.Now.Add(-age * time.Hour)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(env.Store, ".claude.json.tmp.25")
	assertRows(
		t,
		detect(t, "stale-state-tmp", env),
		Row{Warn, "stale-state-tmp", path, "Claude's stale state temp file", "rm " + path},
	)
	assertRows(t, detect(t, "unclassified", env))
}

func TestUnclassifiedExcludesKnownAndBackupNames(t *testing.T) {
	env := fixtureEnv(t)
	for _, name := range []string{"ide", ".cc-new-children", ".cc-pane-children", "settings.local.json", "settings.json", ".claude.json", "settings.bak-fixture", "settings.before-fixture", "settings.pre-professor-fixture"} {
		writeFile(t, filepath.Join(env.Store, name), "{}")
	}
	assertRows(t, detect(t, "unclassified", env))
	var want []Row
	for _, name := range []string{"settings.bak-fixture", "settings.before-fixture", "settings.pre-professor-fixture"} {
		path := filepath.Join(env.Store, name)
		want = append(
			want,
			Row{
				Warn,
				"beside-backup",
				path,
				"a backup beside the file",
				"rm -r " + path + " once you no longer need it",
			},
		)
	}
	assertRows(t, detect(t, "beside-backup", env), want...)
}

// TestStoreIdentityFixRunsOnAFirstMigration runs the printed fixes on the
// shape every pre-account host has: identity in the store and no account 1
// directory yet. pfm install, which would create it, is refused by these
// same rows, so each fix must run as printed.
func TestStoreIdentityFixRunsOnAFirstMigration(t *testing.T) {
	env := fixtureEnv(t)
	acct := env.Accounts[0].ConfigDir
	for _, entry := range []string{".credentials.json", "state/mcp-discover-verdicts.json"} {
		writeFile(t, filepath.Join(env.Store, entry), entry)
	}
	rows := detect(t, "store-identity", env)
	if len(rows) != 2 {
		t.Fatalf("rows=%+v", rows)
	}
	for _, row := range rows {
		if output, err := exec.Command("sh", "-c", row.Fix).CombinedOutput(); err != nil {
			t.Fatalf("fix %q: %v: %s", row.Fix, err, output)
		}
	}
	for _, entry := range []string{".credentials.json", "state/mcp-discover-verdicts.json"} {
		if raw, err := os.ReadFile(filepath.Join(acct, entry)); err != nil || string(raw) != entry {
			t.Fatalf("%s not moved: %q %v", entry, raw, err)
		}
	}
	for _, dir := range []string{acct, filepath.Join(acct, "state")} {
		if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s: %v %v", dir, info, err)
		}
	}
	assertRows(t, detect(t, "store-identity", env))
}

package hostcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

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
						"rm -r " + plugins + "  # the store keeps its copy (reinstallable)",
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

// A foreign hook writes cache/write-state-sync/x.json under the store: Claude
// rebuilds a cache, so the store holding only that is no identity and no row,
// while a real stray (.claude.json) beside it still blocks.
func TestStoreIdentityIgnoresStoreCache(t *testing.T) {
	env := fixtureEnv(t)
	writeFile(t, filepath.Join(env.Store, "cache", "write-state-sync", "x.json"), "{}")
	assertRows(t, detect(t, "store-identity", env))
	for _, row := range RunAll(env) {
		if strings.Contains(row.Path, "cache") {
			t.Errorf("row names the store cache: %s", row.Render(""))
		}
	}
	cache := filepath.Join(env.Store, "cache")
	if err := os.RemoveAll(cache); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), cache); err != nil {
		t.Fatal(err)
	}
	if rows := detect(t, "store-identity", env); len(rows) == 0 {
		t.Errorf("a store cache that is a symbolic link was never inspected")
	}
	if err := os.Remove(cache); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(env.Store, ".claude.json")
	writeBorn(t, stray, "data", env.Now)
	rows := detect(t, "store-identity", env)
	if len(rows) != 1 || rows[0].Path != stray || rows[0].Severity != Block {
		t.Fatalf("rows=%+v", rows)
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
				if slices.Contains(env.Retired.StoreEntries, entry) {
					continue // retired-store-entry's, a warning
				}
				if entry == "cache" {
					continue // rebuildable, never identity: TestStoreIdentityIgnoresStoreCache
				}
				if entry == "state" {
					entry = "state/mcp-discover-verdicts.json"
				}
				path, target := filepath.Join(env.Store, entry), filepath.Join(first, entry)
				writeBorn(t, path, "data", env.Now)
				dirs := first
				if filepath.Dir(target) != first {
					dirs += " " + filepath.Dir(target)
				}
				fix := "mkdir -m 700 -p " + dirs + " && mv " + path + " " + target
				if present {
					writeFile(t, target, "data")
					fix = asideFix(env, path)
				}
				problem := entry + storeIdentityText
				switch entry {
				case "backups":
					problem += "; a Claude launched without CLAUDE_CONFIG_DIR writes backups here too"
					fallthrough
				case ".claude.json", ".claude.json.backup":
					problem += " (mtime 2026-08-01T12:00:00Z, firstStartTime unreadable: not JSON (invalid character 'd' looking for beginning of value))"
				}
				want = append(want, Row{Block, "store-identity", path, problem, fix})
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
				".credentials.json is account identity inside the store, written by a Claude launched with CLAUDE_CONFIG_DIR set to the store (a loop over config dirs that still lists it)",
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
				"telemetry is account identity inside the store, written by a Claude launched with CLAUDE_CONFIG_DIR set to the store (a loop over config dirs that still lists it)",
				"mkdir -m 700 -p " + env.Accounts[0].ConfigDir + " && mv " + other + " " +
					filepath.Join(env.Accounts[0].ConfigDir, "telemetry"),
			},
		)
	})
	t.Run("config-dir-is-store", func(t *testing.T) {
		env := fixtureEnv(t)
		env.Accounts[0].ConfigDir = env.Store
		path := filepath.Join(env.Store, ".credentials.json")
		writeFile(t, path, "{}")
		assertRows(
			t,
			detect(t, "store-identity", env),
			Row{
				Block,
				"store-identity",
				path,
				".credentials.json is account identity inside the store, written by a Claude launched with CLAUDE_CONFIG_DIR set to the store (a loop over config dirs that still lists it)",
				"apply account-is-store's fix for " + env.Store + " first; pfm doctor then names this entry's move",
			},
		)
	})
	t.Run("dangling-account-entry", func(t *testing.T) {
		env := fixtureEnv(t)
		writeFile(t, filepath.Join(env.Store, ".credentials.json"), "{}")
		target := filepath.Join(env.Accounts[0].ConfigDir, ".credentials.json")
		symlink(t, filepath.Join(env.Home, "gone"), target)
		assertUnreadable(t, detect(t, "store-identity", env), "store-identity", target, syscall.ENOENT)
	})
}

// strayAside is where a fixture env's move-aside fixes put a stray file:
// its path under home, mirrored under the run's timestamped directory.
func strayAside(env Env, path string) string {
	relative, err := filepath.Rel(env.Home, path)
	if err != nil {
		panic(err)
	}
	return filepath.Join(env.Home, ".local", "state", "pfm", "stray-claude-state", "20260801-120000", relative)
}

// writeBorn writes text at path with a fixed mtime, so a row naming it is exact.
func writeBorn(t *testing.T, path, text string, born time.Time) {
	t.Helper()
	writeFile(t, path, text)
	if err := os.Chtimes(path, born, born); err != nil {
		t.Fatal(err)
	}
}

func asideFix(env Env, path string) string {
	target := strayAside(env, path)
	return "mkdir -m 700 -p " + filepath.Dir(target) + " && [ ! -e " + target + " ] && mv " + path + " " + target +
		"  # moved aside, never deleted"
}

const storeIdentityText = " is account identity inside the store, written by a Claude launched with CLAUDE_CONFIG_DIR set to the store (a loop over config dirs that still lists it)"

func TestAccountEntryReal(t *testing.T) {
	env := fixtureEnv(t)
	var want []Row
	fixes := map[string]string{}
	for _, name := range strings.Fields(
		"projects file-history tasks session-env paste-cache shell-snapshots plans uploads downloads teams " +
			"agents commands skills rules themes",
	) {
		fixes[name] = "mkdir -p {store}/{entry} && { cp -an {path}/. {store}/{entry}/ || :; } && " +
			"! diff -rq {path} {store}/{entry} 2>&1 | grep -v '^Only in {store}/{entry}' && " +
			"rm -r {path}  # union into the store; stops while a file differs"
	}
	for _, name := range strings.Fields(".last-cleanup gh-pr-status-cache.json") {
		fixes[name] = "rm {path}  # a cache"
	}
	fixes["history.jsonl"] = "jq -c -s 'sort_by(.timestamp)[]' {store}/history.jsonl {path} > " +
		"{store}/history.jsonl.new && mv {store}/history.jsonl.new {store}/history.jsonl && " +
		"rm {path}  # interleaved by timestamp"
	fixes["plugins"] = "rm -r {path}  # the store keeps its copy (reinstallable)"
	fixes["settings.json"] = "jq -e -s '.[0] as $s | .[1] | to_entries | all(.key as $k | ($s | has($k) | not) or " +
		"$s[$k] == .value)' {store}/{entry} {path} > /dev/null && " +
		"jq -s '.[0] * .[1]' {store}/{entry} {path} > " +
		"{store}/{entry}.new && mv {store}/{entry}.new {store}/{entry} && rm {path}  " +
		"# adds the keys the store lacks; stops while a key differs"
	fixes["tool-visibility-controller.json"] = "mkdir -p {store} && { [ -e {store}/{entry} ] || [ -L {store}/{entry} ] || " +
		"printf '{}\\n' > {store}/{entry}; } && " + fixes["settings.json"]
	fixes["CLAUDE.md"] = "cat {path} >> {store}/CLAUDE.md && rm {path}  " +
		"# appended whole; prune {store}/CLAUDE.md as you like"
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
		dir := env.Accounts[0].ConfigDir
		assertRows(
			t,
			rows,
			Row{
				Block,
				"account-entry-real",
				dir,
				"account config dir is a file, not a directory",
				"mv " + dir + " " + dir + ".bak  # pfm install then creates the account dir",
			},
			Row{
				Block,
				"account-entry-real",
				path,
				"plugins is a real dir; it belongs in the store",
				"rm -r " + path + "  # the store keeps its copy (reinstallable)",
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
	for _, scenario := range []string{"foreign", "foreign-dangling", "dangling", "store-link", "unreadable-link"} {
		t.Run(scenario, func(t *testing.T) {
			env := fixtureEnv(t)
			account := env.Accounts[0].ConfigDir
			name, target := "CLAUDE.md", filepath.Join(env.Home, "dotfiles", "CLAUDE.md")
			if scenario != "foreign" && scenario != "foreign-dangling" {
				name, target = "agents", filepath.Join(env.Store, "gone")
			}
			path := filepath.Join(account, name)
			makeDir(t, account)
			switch scenario {
			case "foreign":
				writeFile(t, target, "mine\n")
			case "store-link":
				target = filepath.Join(env.Store, name)
				makeDir(t, target)
			case "unreadable-link":
				target = path
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			rows := detect(t, "account-entry-real", env)
			switch scenario {
			case "foreign", "foreign-dangling":
				fix := "cat " + path + " >> " + env.Store + "/CLAUDE.md && rm " + path + "  # appended whole; prune " + env.Store + "/CLAUDE.md as you like"
				assertRows(
					t,
					rows,
					Row{
						Block,
						"account-entry-real",
						path,
						"CLAUDE.md links to " + target + " outside the store; its data belongs in the store",
						fix,
					},
				)
			case "unreadable-link":
				assertUnreadable(t, rows, "account-entry-real", path, syscall.ELOOP)
			default:
				assertRows(t, rows)
			}
		})
	}
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
	for _, name := range []string{"ide", ".cc-new-children", ".cc-pane-children", "settings.local.json", "settings.json", ".claude.json", "settings.bak-fixture", "settings.before-fixture", "settings.pre-professor-fixture", "debug", "daemon.lock", "daemon.status.json", ".claude.json.lock", "tmp"} {
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

// TestUnclassifiedSparesStoreMarkdown: a regular *.md file at the store's top
// level is the user's own material, shared by construction; the same name in an
// account dir, a *.md dir and an unknown non-md store entry still report.
func TestUnclassifiedSparesStoreMarkdown(t *testing.T) {
	env := fixtureEnv(t)
	account := filepath.Join(env.Accounts[0].ConfigDir, "notes.md")
	drafts, text := filepath.Join(env.Store, "drafts.md"), filepath.Join(env.Store, "notes.txt")
	for _, path := range []string{filepath.Join(env.Store, "notes.md"), account, text} {
		writeFile(t, path, "# notes")
	}
	makeDir(t, drafts)
	var want []Row
	for _, path := range []string{drafts, text, account} {
		problem := "UNCLASSIFIED — on neither the shared nor the per-account list"
		fix := "keep it; pfm doctor names it until a pfm release classifies it"
		want = append(want, Row{Warn, "unclassified", path, problem, fix})
	}
	assertRows(t, detect(t, "unclassified", env), want...)
}

// TestUnclassifiedKnowsPluginDirs: a top-level dir named after an installed
// plugin is that plugin's (its logs), in the store and in every account dir; a
// plugin registry that cannot be read is named, and the dirs it would have
// classified stay unclassified.
func TestUnclassifiedKnowsPluginDirs(t *testing.T) {
	env := fixtureEnv(t)
	registry := filepath.Join(env.Store, "plugins", "installed_plugins.json")
	writeFile(t, registry, `{"version":2,"plugins":{"logbook@fixture-market":[],"other@fixture-market":[]}}`)
	for _, dir := range []string{env.Store, env.Accounts[0].ConfigDir} {
		writeFile(t, filepath.Join(dir, "logbook", "logbook.log"), "line")
	}
	assertRows(t, detect(t, "unclassified", env))
	t.Run("registry-unreadable", func(t *testing.T) {
		env := fixtureEnv(t)
		registry := filepath.Join(env.Store, "plugins", "installed_plugins.json")
		writeFile(t, registry, "{")
		path := filepath.Join(env.Store, "logbook")
		makeDir(t, path)
		assertRows(
			t,
			detect(t, "unclassified", env),
			Row{
				Warn,
				"unclassified",
				registry,
				"UNREADABLE plugin registry: unexpected end of JSON input — a dir named after a plugin reports unclassified",
				"make " + registry + " readable JSON, then rerun pfm doctor",
			},
			Row{
				Warn,
				"unclassified",
				path,
				"UNCLASSIFIED — on neither the shared nor the per-account list",
				"keep it; pfm doctor names it until a pfm release classifies it",
			},
		)
	})
}

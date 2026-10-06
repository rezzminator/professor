package hostcheck

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
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
	t.Run("parent-link", func(t *testing.T) {
		env := fixtureEnv(t)
		acct := env.Accounts[0].ConfigDir
		parent, resolved := filepath.Dir(acct), filepath.Join(env.Store, filepath.Base(acct))
		writeFile(t, filepath.Join(resolved, ".credentials.json"), "token")
		writeFile(t, filepath.Join(env.Store, "settings.json"), "{}")
		symlink(t, env.Store, parent)
		fix := "[ ! -L " + parent + " ] || { rm " + parent + " && mkdir -m 700 " + parent + "; } && [ ! -e " + acct +
			" ] && mv " + resolved + " " + acct
		assertRows(
			t,
			detect(t, "account-is-store", env),
			Row{Block, "account-is-store", acct, "account 1's config dir resolves to the store " + env.Store, fix},
		)
		if output, err := exec.Command("sh", "-c", fix).CombinedOutput(); err != nil {
			t.Fatalf("fix %q: %v: %s", fix, err, output)
		}
		if info, err := os.Lstat(parent); err != nil || !info.IsDir() {
			t.Fatalf("%s is not a real dir: %v %v", parent, info, err)
		}
		if raw, err := os.ReadFile(filepath.Join(acct, ".credentials.json")); err != nil || string(raw) != "token" {
			t.Fatalf("credentials not in %s: %q %v", acct, raw, err)
		}
		if _, err := os.Lstat(resolved); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s left in the store: %v", resolved, err)
		}
		if _, err := os.Stat(filepath.Join(env.Store, "settings.json")); err != nil {
			t.Fatalf("store lost its settings: %v", err)
		}
		assertRows(t, detect(t, "account-is-store", env))
	})
	t.Run("default-dir-resolves-to-store", func(t *testing.T) {
		env := fixtureEnv(t)
		env.Accounts[0].ConfigDir = env.Store
		makeDir(t, env.Store)
		symlink(t, env.Store, filepath.Dir(config.DefaultAccountDir(env.Home, 1)))
		assertRows(
			t,
			detect(t, "account-is-store", env),
			Row{
				Block,
				"account-is-store",
				env.Store,
				"account 1's config dir resolves to the store " + env.Store,
				"point accounts[1].configDir in " + env.ConfigPath + " at a real dir outside the store " + env.Store +
					"; " + config.DefaultAccountDir(env.Home, 1) + " resolves into it",
			},
		)
	})
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
				if slices.Contains(installer.RetiredStoreEntries, entry) {
					continue // retired-store-entry's, a warning
				}
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
					Row{
						Block,
						"store-identity",
						path,
						entry + " is account identity inside the store, written by a Claude launched with CLAUDE_CONFIG_DIR set to the store (a loop over config dirs that still lists it)",
						fix,
					},
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

// TestStoreIdentityAccountLinkedToStore runs the printed fixes on the
// account-is-store shape ~/.cc/1 -> ~/.claude, where each identity entry's
// store and account paths are one file: no store-identity fix deletes either
// side, and the fixes, run as printed in either order, leave every entry once,
// in account 1's real dir.
func TestStoreIdentityAccountLinkedToStore(t *testing.T) {
	entries := []string{".credentials.json", ".claude.json", "sessions/1.json", "state/mcp-discover-verdicts.json"}
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint("reverse=", reverse), func(t *testing.T) {
			env := fixtureEnv(t)
			acct := env.Accounts[0].ConfigDir
			for _, entry := range entries {
				writeFile(t, filepath.Join(env.Store, entry), entry)
			}
			symlink(t, env.Store, acct)
			identity := detect(t, "store-identity", env)
			if len(identity) != len(entries) {
				t.Fatalf("store-identity rows=%+v", identity)
			}
			for _, row := range identity {
				side := filepath.Join(acct, strings.TrimPrefix(row.Path, env.Store+string(filepath.Separator)))
				for _, remove := range []string{"rm " + row.Path, "rm -r " + row.Path, "rm " + side, "rm -r " + side} {
					if strings.Contains(row.Fix, remove) {
						t.Errorf("fix deletes one side of %s: %q", row.Path, row.Fix)
					}
				}
			}
			fixes := append(detect(t, "account-is-store", env), identity...)
			for i := range fixes {
				row := fixes[i]
				if reverse {
					row = fixes[len(fixes)-1-i]
				}
				output, err := exec.Command("sh", "-c", row.Fix).CombinedOutput()
				// Run last, account-is-store's own fix finds the real dir its
				// link became and refuses, deleting nothing.
				if err != nil && (!reverse || row.Check != "account-is-store") {
					t.Fatalf("fix %q: %v: %s", row.Fix, err, output)
				}
			}
			if info, err := os.Lstat(acct); err != nil || !info.IsDir() {
				t.Fatalf("%s is not a real dir: %v %v", acct, info, err)
			}
			for _, entry := range entries {
				if raw, err := os.ReadFile(filepath.Join(acct, entry)); err != nil || string(raw) != entry {
					t.Fatalf("%s not in account 1's dir: %q %v", entry, raw, err)
				}
				if _, err := os.Lstat(filepath.Join(env.Store, entry)); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("%s left in the store: %v", entry, err)
				}
			}
			assertRows(t, detect(t, "account-is-store", env))
			assertRows(t, detect(t, "store-identity", env))
		})
	}
}

// TestAccountEntryRealFixesRun runs every account-entry-real fix as printed:
// each shape is one shell line whose delete runs only after its merge into the
// store succeeded, and a merge that finds a difference deletes nothing.
func TestAccountEntryRealFixesRun(t *testing.T) {
	env := fixtureEnv(t)
	acct := env.Accounts[0].ConfigDir
	for _, entry := range installer.StoreEntries {
		path, target := filepath.Join(acct, entry.Name), filepath.Join(env.Store, entry.Name)
		switch {
		case entry.Dir:
			writeFile(t, filepath.Join(path, "sub", "account"), "account")
			writeFile(t, filepath.Join(path, "both"), "same")
			writeFile(t, filepath.Join(target, "both"), "same")
			writeFile(t, filepath.Join(target, "store"), "store")
		case entry.Name == "settings.json":
			writeFile(t, path, `{"account":1,"both":{"k":2}}`)
			writeFile(t, target, `{"both":{"k":2},"store":3}`)
		case entry.Name == "history.jsonl":
			writeFile(t, path, `{"timestamp":1}`+"\n")
			writeFile(t, target, `{"timestamp":2}`+"\n")
		default:
			writeFile(t, path, "account\n")
			writeFile(t, target, "store\n")
		}
	}
	rows := detect(t, "account-entry-real", env)
	if len(rows) != len(installer.StoreEntries) {
		t.Fatalf("rows=%+v", rows)
	}
	for _, row := range rows {
		if output, err := exec.Command("sh", "-c", row.Fix).CombinedOutput(); err != nil {
			t.Fatalf("fix %q: %v: %s", row.Fix, err, output)
		}
	}
	assertRows(t, detect(t, "account-entry-real", env))
	for _, entry := range installer.StoreEntries {
		path, target := filepath.Join(acct, entry.Name), filepath.Join(env.Store, entry.Name)
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s left in the account: %v", path, err)
		}
		want := map[string]string{target: "store\n"}
		switch {
		case entry.Name == "plugins":
			want = map[string]string{filepath.Join(target, "store"): "store", filepath.Join(target, "both"): "same"}
		case entry.Dir:
			want = map[string]string{
				filepath.Join(target, "store"):          "store",
				filepath.Join(target, "both"):           "same",
				filepath.Join(target, "sub", "account"): "account",
			}
		case entry.Name == "settings.json":
			raw, err := os.ReadFile(target)
			var got map[string]any
			merged := map[string]any{"account": 1.0, "both": map[string]any{"k": 2.0}, "store": 3.0}
			if err != nil || json.Unmarshal(raw, &got) != nil || !reflect.DeepEqual(got, merged) {
				t.Errorf("%s=%q %v", target, raw, err)
			}
			continue
		case entry.Name == "history.jsonl":
			want[target] = `{"timestamp":1}` + "\n" + `{"timestamp":2}` + "\n"
		case entry.Name == "CLAUDE.md":
			want[target] = "store\naccount\n"
		}
		for file, text := range want {
			if raw, err := os.ReadFile(file); err != nil || string(raw) != text {
				t.Errorf("%s=%q %v, want %q", file, raw, err, text)
			}
		}
	}
	for _, name := range []string{"projects", "settings.json"} {
		t.Run(name+"-stops-on-a-difference", func(t *testing.T) {
			env := fixtureEnv(t)
			path, target := filepath.Join(env.Accounts[0].ConfigDir, name), filepath.Join(env.Store, name)
			if name == "projects" {
				path, target = filepath.Join(path, "f"), filepath.Join(target, "f")
			}
			writeFile(t, path, `{"k":1}`)
			writeFile(t, target, `{"k":2}`)
			rows := detect(t, "account-entry-real", env)
			if len(rows) != 1 {
				t.Fatalf("rows=%+v", rows)
			}
			if err := exec.Command("sh", "-c", rows[0].Fix).Run(); err == nil {
				t.Fatalf("fix %q ran through a difference", rows[0].Fix)
			}
			for file, text := range map[string]string{path: `{"k":1}`, target: `{"k":2}`} {
				if raw, err := os.ReadFile(file); err != nil || string(raw) != text {
					t.Fatalf("%s=%q %v, want %q", file, raw, err, text)
				}
			}
		})
	}
	t.Run("foreign-link", func(t *testing.T) {
		env := fixtureEnv(t)
		path := filepath.Join(env.Accounts[0].ConfigDir, "CLAUDE.md")
		outside, store := filepath.Join(env.Home, "dotfiles", "CLAUDE.md"), filepath.Join(env.Store, "CLAUDE.md")
		writeFile(t, outside, "mine\n")
		writeFile(t, store, "store\n")
		makeDir(t, filepath.Dir(path))
		if err := os.Symlink(outside, path); err != nil {
			t.Fatal(err)
		}
		rows := detect(t, "account-entry-real", env)
		if len(rows) != 1 {
			t.Fatalf("rows=%+v", rows)
		}
		if output, err := exec.Command("sh", "-c", rows[0].Fix).CombinedOutput(); err != nil {
			t.Fatalf("fix %q: %v: %s", rows[0].Fix, err, output)
		}
		for file, want := range map[string]string{store: "store\nmine\n", outside: "mine\n"} {
			if got, err := os.ReadFile(file); err != nil || string(got) != want {
				t.Fatalf("%s=%q error=%v want=%q", file, got, err, want)
			}
		}
		if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("account link still exists: %v", err)
		}
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
	for _, name := range strings.Fields(
		"projects file-history tasks session-env paste-cache shell-snapshots plans uploads downloads teams " +
			"agents commands skills rules themes",
	) {
		fixes[name] = "mkdir -p {store}/{entry} && cp -an {path}/. {store}/{entry}/ && " +
			"! diff -rq {path} {store}/{entry} 2>&1 | grep -v '^Only in {store}/{entry}' && " +
			"rm -r {path}  # union into the store; stops while a file differs"
	}
	for _, name := range strings.Fields("stats-cache.json .last-cleanup gh-pr-status-cache.json") {
		fixes[name] = "rm {path}  # a cache"
	}
	fixes["history.jsonl"] = "jq -c -s 'sort_by(.timestamp)[]' {store}/history.jsonl {path} > " +
		"{store}/history.jsonl.new && mv {store}/history.jsonl.new {store}/history.jsonl && " +
		"rm {path}  # interleaved by timestamp"
	fixes["plugins"] = "rm -r {path}  # the store keeps its copy (reinstallable)"
	fixes["settings.json"] = "jq -e -s '.[0] as $s | .[1] | to_entries | all(.key as $k | ($s | has($k) | not) or " +
		"$s[$k] == .value)' {store}/settings.json {path} > /dev/null && " +
		"jq -s '.[0] * .[1]' {store}/settings.json {path} > " +
		"{store}/settings.json.new && mv {store}/settings.json.new {store}/settings.json && rm {path}  " +
		"# adds the keys the store lacks; stops while a key differs"
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
	for _, scenario := range []string{"foreign", "dangling", "store-link", "unreadable-link"} {
		t.Run(scenario, func(t *testing.T) {
			env := fixtureEnv(t)
			account := env.Accounts[0].ConfigDir
			name, target := "CLAUDE.md", filepath.Join(env.Home, "dotfiles", "CLAUDE.md")
			if scenario != "foreign" {
				name, target = "agents", filepath.Join(env.Home, "gone")
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
			case "foreign":
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

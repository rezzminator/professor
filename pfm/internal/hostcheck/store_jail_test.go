//go:build jail

// The tests below run the shell fix a report row carries (sh -c, cp, mv, ln) and read the files it leaves: the real shell is the subject.

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
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/testjail"
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
		case entry.Name == "settings.json" || entry.Name == "tool-visibility-controller.json":
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
	// BSD cp -n exits 1 for every file it skips, and each union here skips
	// "both"; this cp copies as the host's does, then exits as macOS's does.
	realCp, err := exec.LookPath("cp")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := testjail.WriteExecutable(
		filepath.Join(bin, "cp"), []byte("#!/bin/sh\n"+realCp+" \"$@\"\nexit 1\n"), 0o700,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
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
		case entry.Name == "settings.json" || entry.Name == "tool-visibility-controller.json":
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
	for _, name := range []string{"projects", "settings.json", "tool-visibility-controller.json"} {
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
	// A plugin user's rules live in each account dir (CLAUDE_CONFIG_DIR) and the store has no copy yet:
	// the printed fixes, run in turn, move them into the store whole, so install is unblocked.
	t.Run("tool-visibility-controller.json-adopts-into-a-missing-store", func(t *testing.T) {
		env := fixtureEnv(t)
		const name, rules = "tool-visibility-controller.json", `{"loops":{"main":{"tools":{"exclude":["WebSearch"]}}}}`
		env.Accounts = append(env.Accounts, config.Account{ID: 2, ConfigDir: config.DefaultAccountDir(env.Home, 2)})
		for _, account := range env.Accounts {
			writeFile(t, filepath.Join(account.ConfigDir, name), rules)
		}
		rows := detect(t, "account-entry-real", env)
		if len(rows) != len(env.Accounts) {
			t.Fatalf("rows=%+v", rows)
		}
		for _, row := range rows {
			if output, err := exec.Command("sh", "-c", row.Fix).CombinedOutput(); err != nil {
				t.Fatalf("fix %q: %v: %s", row.Fix, err, output)
			}
		}
		assertRows(t, detect(t, "account-entry-real", env))
		raw, err := os.ReadFile(filepath.Join(env.Store, name))
		var got, want any
		if err != nil || json.Unmarshal(raw, &got) != nil || json.Unmarshal([]byte(rules), &want) != nil ||
			!reflect.DeepEqual(got, want) {
			t.Fatalf("store copy=%q %v, want the account rules", raw, err)
		}
	})
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
	born := time.Date(2026, 7, 30, 8, 15, 0, 0, time.UTC)
	for _, test := range []struct{ name, text, birth string }{
		{"stamped", `{"firstStartTime":"2026-07-30T08:14:59.120Z"}`, "mtime 2026-07-30T08:15:00Z, firstStartTime 2026-07-30T08:14:59.120Z"},
		{"absent", `{"numStartups":1}`, "mtime 2026-07-30T08:15:00Z, firstStartTime absent"},
		{"not-json", "{", "mtime 2026-07-30T08:15:00Z, firstStartTime unreadable: not JSON (unexpected end of JSON input)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := fixtureEnv(t)
			path := filepath.Join(env.Home, ".claude.json")
			writeBorn(t, path, test.text, born)
			rows := detect(t, "home-state-file", env)
			assertRows(
				t,
				rows,
				Row{
					Warn,
					"home-state-file",
					path,
					"a Claude launched without CLAUDE_CONFIG_DIR wrote this state file (" + test.birth + ")",
					asideFix(env, path),
				},
			)
			if output, err := exec.Command("sh", "-c", rows[0].Fix).CombinedOutput(); err != nil {
				t.Fatalf("fix %q: %v: %s", rows[0].Fix, err, output)
			}
			if raw, err := os.ReadFile(strayAside(env, path)); err != nil || string(raw) != test.text {
				t.Fatalf("aside copy %q %v", raw, err)
			}
			assertRows(t, detect(t, "home-state-file", env))
		})
	}
	t.Run("unreadable", func(t *testing.T) {
		env := fixtureEnv(t)
		path := filepath.Join(env.Home, ".claude.json")
		makeDir(t, path)
		assertUnreadable(t, detect(t, "home-state-file", env), "home-state-file", path, syscall.EISDIR)
	})
}

// TestStrayBackupsReportBirthAndMoveAside is the issue's shape: an unscoped
// Claude recreated the store's backups beside account 1's own. The BLOCK names
// the newest backup's birth, and its fix moves the dir aside, deleting nothing.
func TestStrayBackupsReportBirthAndMoveAside(t *testing.T) {
	env := fixtureEnv(t)
	backups := filepath.Join(env.Store, "backups")
	writeBorn(t, filepath.Join(backups, ".claude.json.backup.1"), "{}", time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC))
	writeBorn(
		t,
		filepath.Join(backups, ".claude.json.backup.2"),
		`{"firstStartTime":"2026-07-31T23:59:58.000Z"}`,
		time.Date(2026, 7, 31, 23, 59, 59, 0, time.UTC),
	)
	makeDir(t, filepath.Join(env.Accounts[0].ConfigDir, "backups"))
	rows := detect(t, "store-identity", env)
	assertRows(
		t,
		rows,
		Row{
			Block,
			"store-identity",
			backups,
			"backups" + storeIdentityText + "; a Claude launched without CLAUDE_CONFIG_DIR writes backups here too " +
				"(newest .claude.json.backup.2: mtime 2026-07-31T23:59:59Z, firstStartTime 2026-07-31T23:59:58.000Z)",
			asideFix(env, backups),
		},
	)
	if output, err := exec.Command("sh", "-c", rows[0].Fix).CombinedOutput(); err != nil {
		t.Fatalf("fix %q: %v: %s", rows[0].Fix, err, output)
	}
	for _, name := range []string{".claude.json.backup.1", ".claude.json.backup.2"} {
		if _, err := os.Stat(filepath.Join(strayAside(env, backups), name)); err != nil {
			t.Fatalf("%s not moved aside: %v", name, err)
		}
	}
	assertRows(t, detect(t, "store-identity", env))
	t.Run("empty", func(t *testing.T) {
		env := fixtureEnv(t)
		backups := filepath.Join(env.Store, "backups")
		makeDir(t, backups)
		makeDir(t, filepath.Join(env.Accounts[0].ConfigDir, "backups"))
		rows := detect(t, "store-identity", env)
		if len(rows) != 1 || !strings.HasSuffix(rows[0].Problem, "(holds no file)") {
			t.Fatalf("rows=%+v", rows)
		}
	})
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

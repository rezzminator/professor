package hostcheck

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

// linkedStoreEnv is a healthy host with ids accounts: every shared entry in
// the store and linked from each account.
func linkedStoreEnv(t *testing.T, ids ...int) Env {
	t.Helper()
	env := fixtureEnv(t)
	env.Accounts = nil
	for _, id := range ids {
		env.Accounts = append(env.Accounts, config.Account{ID: id, ConfigDir: config.DefaultAccountDir(env.Home, id)})
	}
	makeDir(t, env.Store)
	writeFile(t, env.ConfigPath, "{}")
	for _, entry := range installer.StoreEntries {
		path := filepath.Join(env.Store, entry.Name)
		if entry.Dir {
			makeDir(t, path)
		} else {
			writeFile(t, path, entry.Seed)
		}
		for _, account := range env.Accounts {
			makeDir(t, account.ConfigDir)
			if err := os.Symlink(path, filepath.Join(account.ConfigDir, entry.Name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return env
}

func replaceWithFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	writeFile(t, path, text)
}

func assertNoBlock(t *testing.T, rows []Row) {
	t.Helper()
	for _, row := range rows {
		if row.Severity == Block {
			t.Errorf("BLOCK row: %s", row.Render(""))
		}
	}
}

// Claude writes a per-account entry in each account's own dir; a real file
// there is the healthy shape, never a finding that refuses pfm install.
func TestPerAccountEntriesRealYieldNoBlock(t *testing.T) {
	env := linkedStoreEnv(t, 1, 2)
	names := slices.Clone(installer.AccountEntries)
	for _, name := range installer.RetiredStoreEntries {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	for _, account := range env.Accounts {
		for _, name := range names {
			replaceWithFile(t, filepath.Join(account.ConfigDir, name), "{}\n")
		}
	}
	assertNoBlock(t, RunAll(env))
}

// The host a retired entry leaves behind (Claude replaced the link with a real
// file in accounts 1 and 3, account 2 still links into the store, the store
// holds its copy) must not block: pfm install is the step that migrates it.
func TestRetiredStoreEntryPreFixShapeDoesNotBlock(t *testing.T) {
	env := linkedStoreEnv(t, 1, 2, 3)
	var want []Row
	for _, name := range installer.RetiredStoreEntries {
		storePath, linked := filepath.Join(env.Store, name), filepath.Join(env.Accounts[1].ConfigDir, name)
		replaceWithFile(t, storePath, "store copy")
		replaceWithFile(t, filepath.Join(env.Accounts[0].ConfigDir, name), "account 1's own")
		replaceWithFile(t, filepath.Join(env.Accounts[2].ConfigDir, name), "account 3's own")
		if err := os.Remove(linked); err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
		if err := os.Symlink(storePath, linked); err != nil {
			t.Fatal(err)
		}
		want = append(
			want,
			Row{
				Warn,
				checkRetiredStoreEntry,
				linked,
				name + " links into the store; it is a per-account file now",
				"run pfm install; it removes the link, and Claude writes this account's own file",
			},
			Row{
				Warn,
				checkRetiredStoreEntry,
				storePath,
				name + " is a per-account file now; the store copy is retired",
				"run pfm install; it archives the copy under " +
					filepath.Join(env.Home, ".local", "state", "pfm", "retired-store-entries"),
			},
		)
	}
	rows := RunAll(env)
	assertNoBlock(t, rows)
	assertRows(t, detect(t, checkRetiredStoreEntry, env), want...)
}

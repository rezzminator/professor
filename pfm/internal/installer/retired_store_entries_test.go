package installer

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// TestRetireStoreEntries runs install's migration on the shape a retired entry
// leaves behind: Claude replaced the store link with a real file in accounts 1
// and 3, account 2 still links into the store, and the store holds its copy.
// Account 4's link points outside the store and is the operator's own.
func TestRetireStoreEntries(t *testing.T) {
	for _, scenario := range []string{"apply", "preview", "archive-error"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			store := ClaudeStore(home)
			var accounts []pfmconfig.Account
			for id := 1; id <= 4; id++ {
				accounts = append(accounts, pfmconfig.Account{ID: id, ConfigDir: pfmconfig.DefaultAccountDir(home, id)})
			}
			var output bytes.Buffer
			runner := &engine{
				options: Options{Home: home, ConfigDir: store, ClaudeAccounts: accounts, Stdout: &output},
				apply:   scenario != "preview",
				stamp:   "20261004-000000",
			}
			if err := (&engine{options: runner.options, apply: true}).wireClaudeStore(); err != nil {
				t.Fatal(err)
			}
			write := func(path, content string) {
				t.Helper()
				if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			link := func(target, path string) {
				t.Helper()
				if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			outside := filepath.Join(home, "elsewhere", "kept")
			write(outside, "operator's own")
			archiveDir := filepath.Join(home, ".local", "state", "pfm", "retired-store-entries")
			if scenario == "archive-error" {
				// A file where the archive dir belongs fails the mkdir for root too.
				write(archiveDir, "blocked")
			}
			for _, name := range RetiredStoreEntries {
				write(filepath.Join(store, name), "store copy of "+name)
				write(filepath.Join(accounts[0].ConfigDir, name), "account 1's own")
				link(filepath.Join(store, name), filepath.Join(accounts[1].ConfigDir, name))
				write(filepath.Join(accounts[2].ConfigDir, name), "account 3's own")
				link(outside, filepath.Join(accounts[3].ConfigDir, name))
			}
			output.Reset()
			err := runner.retireStoreEntries()
			if scenario == "archive-error" {
				name := RetiredStoreEntries[0]
				if err == nil || !strings.Contains(err.Error(), filepath.Join(store, name)) ||
					!strings.Contains(err.Error(), archiveDir) {
					t.Fatalf("contextual failure=%v", err)
				}
				assertContent(t, filepath.Join(store, name), "store copy of "+name)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range RetiredStoreEntries {
				storePath, linked := filepath.Join(store, name), filepath.Join(accounts[1].ConfigDir, name)
				archive := filepath.Join(archiveDir, name+".pre-professor-"+runner.stamp)
				unlink := "  change  unlink " + linked
				archived := "  change  archive " + storePath + " -> " + archive
				lines := output.String()
				if !strings.Contains(lines, unlink) || !strings.Contains(lines, archived) ||
					strings.Index(lines, unlink) > strings.Index(lines, archived) {
					t.Fatalf("want %q before %q in:\n%s", unlink, archived, lines)
				}
				assertContent(t, filepath.Join(accounts[0].ConfigDir, name), "account 1's own")
				assertContent(t, filepath.Join(accounts[2].ConfigDir, name), "account 3's own")
				if target, err := os.Readlink(filepath.Join(accounts[3].ConfigDir, name)); err != nil ||
					target != outside {
					t.Fatalf("operator's link changed: %q %v", target, err)
				}
				if scenario == "preview" {
					assertContent(t, storePath, "store copy of "+name)
					assertContent(t, linked, "store copy of "+name)
					assertAbsent(t, archive)
					continue
				}
				assertAbsent(t, storePath)
				assertAbsent(t, linked)
				assertContent(t, archive, "store copy of "+name)
			}
			if scenario == "preview" {
				return
			}
			output.Reset()
			if err := runner.retireStoreEntries(); err != nil {
				t.Fatal(err)
			}
			if output.Len() != 0 {
				t.Fatalf("second run acted:\n%s", output.String())
			}
			for _, name := range RetiredStoreEntries {
				assertAbsent(t, filepath.Join(archiveDir, name+".pre-professor-"+runner.stamp+".1"))
			}
		})
	}
	t.Run("no-roster", func(t *testing.T) {
		home := t.TempDir()
		store := ClaudeStore(home)
		path := filepath.Join(store, ".last-update-result.json")
		writeFixture(t, path, "store copy")
		var output bytes.Buffer
		runner := &engine{options: Options{Home: home, ConfigDir: store, Stdout: &output}, apply: true}
		if err := runner.retireStoreEntries(); err != nil {
			t.Fatal(err)
		}
		want := "  skip    keep " + path + ": no account roster to check its links — pfm doctor names the fix\n"
		if output.String() != want {
			t.Fatalf("transcript=%q want=%q", output.String(), want)
		}
		assertContent(t, path, "store copy")
		assertAbsent(t, RetiredStoreArchive(home))
	})
}

func assertContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("%s=%q (%v), want %q", path, got, err, want)
	}
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s still present: %v", path, err)
	}
}

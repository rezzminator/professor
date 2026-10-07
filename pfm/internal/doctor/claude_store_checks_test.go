package doctor

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestClaudeStoreChecks(t *testing.T) {
	for _, scenario := range []string{"ok", "store-missing", "account-missing", "link-missing", "elsewhere", "foreign", "broken", "unreadable-store", "unreadable-account", "account-not-dir", "real", "account-store"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			store := installer.ClaudeStore(home)
			account := config.DefaultAccountDir(home, 1)
			if err := os.MkdirAll(account, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, entry := range installer.StoreEntries {
				path := filepath.Join(store, entry.Name)
				if entry.Dir {
					if err := os.MkdirAll(path, 0o700); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.WriteFile(path, []byte(entry.Seed), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(path, filepath.Join(account, entry.Name)); err != nil {
					t.Fatal(err)
				}
			}
			link := filepath.Join(account, "agents")
			want, failures := "account-links: ok (1 accounts × 22 entries)\n", 0
			switch scenario {
			case "ok":
			case "broken":
				path := filepath.Join(store, "agents")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("file"), 0o600); err != nil {
					t.Fatal(err)
				}
				want, failures = fmt.Sprintf(
					"store: %s broken: a file where a directory belongs — remove %s, then run pfm install --yes\n",
					path,
					path,
				), 1
			case "store-missing":
				path := filepath.Join(store, "CLAUDE.md")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				want, failures = "store: "+path+" missing — run pfm install\n", 1
			case "account-missing", "account-store", "unreadable-account":
				if err := os.RemoveAll(account); err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "account-missing":
					want, failures = fmt.Sprintf("account: 1 %s missing — run pfm install\n", account), 1
				case "account-store":
					if err := os.Symlink(store, account); err != nil {
						t.Fatal(err)
					}
					want = ""
				case "unreadable-account":
					if err := os.Symlink(account, account); err != nil {
						t.Fatal(err)
					}
					want, failures = fmt.Sprintf(
						"account: 1 %s UNREADABLE error=stat %s: %s\n",
						account,
						account,
						syscall.ELOOP,
					), 1
				}
			case "unreadable-store":
				if err := os.RemoveAll(store); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(store, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				want, failures = "", len(installer.StoreEntries)
				for _, entry := range installer.StoreEntries {
					path := filepath.Join(store, entry.Name)
					want += fmt.Sprintf("store: %s UNREADABLE error=lstat %s: %s\n", path, path, syscall.ENOTDIR)
				}
			case "account-not-dir":
				if err := os.RemoveAll(account); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(account, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				want, failures = fmt.Sprintf(
					"account: 1 %s is a file, not a directory — mv %s %s.bak, then run pfm install\n",
					account,
					account,
					account,
				), 0 // host-check account-entry-real owns this failure count
			default:
				if err := os.Remove(link); err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "link-missing":
					want, failures = "account-link: "+link+" missing — run pfm install\n", 1
				case "elsewhere", "foreign":
					target := filepath.Join(home, "old", "agents")
					if scenario == "elsewhere" {
						target = filepath.Join(store, "old", "agents")
					}
					if scenario == "foreign" {
						if err := os.MkdirAll(target, 0o700); err != nil {
							t.Fatal(err)
						}
					}
					if err := os.Symlink(target, link); err != nil {
						t.Fatal(err)
					}
					want, failures = fmt.Sprintf(
						"account-link: %s points at %s, want %s — run pfm install\n",
						link,
						target,
						filepath.Join(store, "agents"),
					), 1
					if scenario == "foreign" {
						want, failures = fmt.Sprintf(
							"account-link: %s points at %s outside the store — run pfm install --yes; its host check names the merge\n",
							link,
							target,
						), 0
					}
				case "real":
					if err := os.Mkdir(link, 0o700); err != nil {
						t.Fatal(err)
					}
					want = ""
				}
			}
			runtime := config.Runtime{
				Paths:  paths.Values{Home: home},
				Config: config.Config{Accounts: []config.Account{{ID: 1, ConfigDir: account}}},
			}
			var out bytes.Buffer
			got := printClaudeStoreChecks(&out, runtime)
			if got != failures || out.String() != want {
				t.Fatalf("failures=%d want=%d output=%q want=%q", got, failures, out.String(), want)
			}
		})
	}
}

// TestClaudeStoreChecksSymlinkedAccount pins doctor's account rows of the
// account-dir rule, the ones a launch refusal sends the operator to.
func TestClaudeStoreChecksSymlinkedAccount(t *testing.T) {
	for _, scenario := range []string{"outside", "inside-store", "dangling"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			store := installer.ClaudeStore(home)
			account := config.DefaultAccountDir(home, 1)
			target := map[string]string{
				"outside":      filepath.Join(home, "data", "cc1"),
				"inside-store": filepath.Join(store, "projects"),
				"dangling":     filepath.Join(home, "gone"),
			}[scenario]
			for _, dir := range []string{filepath.Dir(account), filepath.Join(store, "projects")} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			for _, entry := range installer.StoreEntries {
				path := filepath.Join(store, entry.Name)
				if entry.Dir {
					if err := os.MkdirAll(path, 0o700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(path, []byte(entry.Seed), 0o600); err != nil {
					t.Fatal(err)
				}
				if scenario == "outside" {
					if err := os.MkdirAll(target, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(path, filepath.Join(target, entry.Name)); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := os.Symlink(target, account); err != nil {
				t.Fatal(err)
			}
			want, failures := map[string]string{
				"outside":      "account-links: ok (1 accounts × 22 entries)\n",
				"inside-store": "",
				"dangling":     fmt.Sprintf("account: 1 %s UNREADABLE error=stat %s: %s\n", account, account, syscall.ENOENT),
			}[scenario], 0
			if scenario == "dangling" {
				failures = 1
			}
			runtime := config.Runtime{
				Paths:  paths.Values{Home: home},
				Config: config.Config{Accounts: []config.Account{{ID: 1, ConfigDir: account}}},
			}
			var out bytes.Buffer
			got := printClaudeStoreChecks(&out, runtime)
			if got != failures || out.String() != want {
				t.Fatalf("failures=%d want=%d output=%q want=%q", got, failures, out.String(), want)
			}
		})
	}
}

func TestDoctorInstalledHomeAccountLinks(t *testing.T) {
	root := jailTest(t)
	runtime, err := config.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	wantDir := config.DefaultAccountDir(filepath.Join(root, "home"), 1)
	if len(runtime.Config.Accounts) != 1 || runtime.Config.Accounts[0].ConfigDir != wantDir {
		t.Fatalf("accounts=%+v want=%s", runtime.Config.Accounts, wantDir)
	}
	var out bytes.Buffer
	if warnings, failures := printHostChecks(&out, runtime, time.Now()); warnings != 0 || failures != 0 {
		t.Fatalf("host warnings=%d failures=%d output=%q", warnings, failures, out.String())
	}
	if got := printClaudeStoreChecks(&out, runtime); got != 0 {
		t.Fatalf("store failures=%d output=%q", got, out.String())
	}
	if want := "host-check: ok (22 checks)\naccount-links: ok (1 accounts × 22 entries)\n"; out.String() != want {
		t.Fatalf("output=%q want=%q", out.String(), want)
	}
	for _, account := range runtime.Config.Accounts {
		for _, entry := range installer.StoreEntries {
			link := filepath.Join(account.ConfigDir, entry.Name)
			target, err := os.Readlink(link)
			if err != nil ||
				!strings.HasPrefix(target, installer.ClaudeStore(runtime.Paths.Home)+string(os.PathSeparator)) {
				t.Fatalf("link=%s target=%s error=%v", link, target, err)
			}
		}
	}
}

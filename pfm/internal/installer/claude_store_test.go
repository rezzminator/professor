package installer

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestClaudeStoreEntries(t *testing.T) {
	home := t.TempDir()
	if got := ClaudeStore(home); got != filepath.Join(home, ".claude") {
		t.Fatalf("store=%q", got)
	}
	dirs := []string{
		"agents",
		"commands",
		"skills",
		"rules",
		"plugins",
		"themes",
		"projects",
		"file-history",
		"tasks",
		"session-env",
		"plans",
		"paste-cache",
		"shell-snapshots",
		"uploads",
		"downloads",
		"teams",
	}
	files := []string{
		"settings.json",
		"CLAUDE.md",
		"history.jsonl",
		"stats-cache.json",
		".last-cleanup",
		"gh-pr-status-cache.json",
	}
	var want []StoreEntry
	for _, name := range dirs {
		want = append(want, StoreEntry{Name: name, Dir: true})
	}
	for _, name := range files {
		seed := ""
		if name == "settings.json" {
			seed = "{}\n"
		}
		want = append(want, StoreEntry{Name: name, Seed: seed})
	}
	if !reflect.DeepEqual(StoreEntries, want) {
		t.Fatalf("entries=%v, want %v", StoreEntries, want)
	}
	accounts := []string{
		".credentials.json",
		".claude.json",
		".claude.json.backup",
		"backups",
		"sessions",
		"daemon",
		"daemon.log",
		"daemon-auth-status.json",
		"daemon-auth-cooldown",
		"jobs",
		"cache",
		"state",
		"mcp-needs-auth-cache.json",
		"telemetry",
		"feedback",
		".last-update-result.json",
	}
	if !reflect.DeepEqual(AccountEntries, accounts) {
		t.Fatalf("accounts=%v", AccountEntries)
	}
	if retired := []string{".last-update-result.json"}; !reflect.DeepEqual(RetiredStoreEntries, retired) {
		t.Fatalf("retired=%v, want %v", RetiredStoreEntries, retired)
	}
	ignored := []string{"ide", ".cc-new-children", ".cc-pane-children", "settings.local.json"}
	if !reflect.DeepEqual(IgnoredEntries, ignored) {
		t.Fatalf("ignored=%v", IgnoredEntries)
	}
	for _, test := range []struct {
		names []string
		class string
	}{
		{append(dirs, files...), "shared"}, {accounts, "account"}, {ignored, "ignored"}, {[]string{"future-entry"}, "unclassified"},
	} {
		for _, name := range test.names {
			t.Run(name, func(t *testing.T) {
				if got := EntryClass(name); got != test.class {
					t.Fatalf("class=%q want=%q", got, test.class)
				}
			})
		}
	}
}

func TestWireClaudeStore(t *testing.T) {
	for _, scenario := range []string{"fresh", "preview", "second", "elsewhere", "real-dir", "real-file", "existing", "account-store", "config-dir", "create-error", "link-error"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			store := ClaudeStore(home)
			account := pfmconfig.DefaultAccountDir(home, 1)
			accounts := []pfmconfig.Account{{ID: 1, ConfigDir: account}}
			if scenario == "config-dir" {
				store = filepath.Join(home, "custom")
				accounts = nil
			}
			old := filepath.Join(home, "old", "agents")
			write := func(path, content string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "existing" {
				write(filepath.Join(store, "history.jsonl"), "keep history")
			}
			if scenario == "create-error" {
				write(store, "blocked")
			}
			if scenario == "link-error" {
				write(filepath.Dir(account), "blocked")
			}
			if scenario == "elsewhere" || strings.HasPrefix(scenario, "real-") {
				if err := os.MkdirAll(account, 0o700); err != nil {
					t.Fatal(err)
				}
				if scenario == "elsewhere" {
					write(filepath.Join(old, "kept"), "old data")
					if err := os.Symlink(old, filepath.Join(account, "agents")); err != nil {
						t.Fatal(err)
					}
				}
				if scenario == "real-dir" {
					write(filepath.Join(account, "projects", "kept"), "real data")
				}
				if scenario == "real-file" {
					write(filepath.Join(account, "projects"), "real data")
				}
			}
			if scenario == "account-store" {
				if err := os.MkdirAll(store, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Dir(account), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(store, account); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			runner := &engine{
				options: Options{Home: home, ConfigDir: store, ClaudeAccounts: accounts, Stdout: &output},
				apply:   scenario != "preview",
			}
			if scenario == "second" {
				if err := runner.wireClaudeStore(); err != nil {
					t.Fatal(err)
				}
				output.Reset()
			}
			err := runner.wireClaudeStore()
			if strings.HasSuffix(scenario, "error") {
				if err == nil || !strings.Contains(err.Error(), home) {
					t.Fatalf("contextual failure=%v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var want strings.Builder
			for _, entry := range StoreEntries {
				path := filepath.Join(store, entry.Name)
				state := "  change  create "
				if scenario == "second" || (scenario == "existing" && entry.Name == "history.jsonl") {
					state = "  ok      "
				}
				fmt.Fprintln(&want, state+path)
			}
			if scenario == "account-store" {
				fmt.Fprintf(&want, "  skip    account 1 %s resolves to the store — pfm doctor names the fix\n", account)
			} else if len(accounts) > 0 {
				if scenario == "second" || scenario == "elsewhere" || strings.HasPrefix(scenario, "real-") {
					fmt.Fprintln(&want, "  ok      "+account)
				} else {
					fmt.Fprintln(&want, "  change  create "+account)
				}
				for _, entry := range StoreEntries {
					path, target := filepath.Join(account, entry.Name), filepath.Join(store, entry.Name)
					switch {
					case scenario == "second":
						fmt.Fprintln(&want, "  ok      "+path)
					case scenario == "elsewhere" && entry.Name == "agents":
						fmt.Fprintf(
							&want,
							"  skip    %s links to %s outside the store — pfm doctor names its merge\n",
							path,
							old,
						)
					case strings.HasPrefix(scenario, "real-") && entry.Name == "projects":
						fmt.Fprintf(
							&want,
							"  skip    %s is a real %s — pfm doctor names its merge\n",
							path,
							strings.TrimPrefix(scenario, "real-"),
						)
					default:
						fmt.Fprintf(&want, "  change  link %s -> %s\n", path, target)
					}
				}
			}
			if output.String() != want.String() {
				t.Fatalf("transcript=%q want=%q", output.String(), want.String())
			}
			if scenario == "preview" {
				if _, err := os.Lstat(store); !os.IsNotExist(err) {
					t.Fatalf("preview store=%v", err)
				}
				if _, err := os.Lstat(account); !os.IsNotExist(err) {
					t.Fatalf("preview account=%v", err)
				}
				return
			}
			for _, entry := range StoreEntries {
				path := filepath.Join(store, entry.Name)
				info, err := os.Lstat(path)
				if err != nil {
					t.Fatal(err)
				}
				mode := os.FileMode(0o600)
				if entry.Dir {
					mode = 0o700
				}
				if info.Mode().Perm() != mode || info.IsDir() != entry.Dir {
					t.Fatalf("entry %s mode=%v", entry.Name, info.Mode())
				}
				if !entry.Dir {
					seed := entry.Seed
					if scenario == "existing" && entry.Name == "history.jsonl" {
						seed = "keep history"
					}
					if raw, err := os.ReadFile(path); err != nil || string(raw) != seed {
						t.Fatalf("entry %s seed=%q error=%v", entry.Name, raw, err)
					}
				}
				if scenario == "account-store" || len(accounts) == 0 ||
					(strings.HasPrefix(scenario, "real-") && entry.Name == "projects") {
					continue
				}
				target := path
				if scenario == "elsewhere" && entry.Name == "agents" {
					target = old
				}
				if got, err := os.Readlink(filepath.Join(account, entry.Name)); err != nil || got != target {
					t.Fatalf("link %s=%q error=%v", entry.Name, got, err)
				}
			}
			if scenario == "elsewhere" {
				if raw, err := os.ReadFile(filepath.Join(old, "kept")); err != nil || string(raw) != "old data" {
					t.Fatalf("old data=%q error=%v", raw, err)
				}
			}
			if strings.HasPrefix(scenario, "real-") {
				path := filepath.Join(account, "projects")
				if scenario == "real-dir" {
					path = filepath.Join(path, "kept")
				}
				if raw, err := os.ReadFile(path); err != nil || string(raw) != "real data" {
					t.Fatalf("real data=%q error=%v", raw, err)
				}
			}
		})
	}
	for _, scenario := range []string{"dangling", "wrong-entry", "live", "live-once", "live-read-error"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			store, account := ClaudeStore(home), pfmconfig.DefaultAccountDir(home, 1)
			var output bytes.Buffer
			runner := &engine{options: Options{Home: home, ConfigDir: store, Stdout: &output}, apply: true}
			if err := runner.wireClaudeStore(); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(account, 0o700); err != nil {
				t.Fatal(err)
			}
			old := filepath.Join(home, "gone")
			if scenario == "wrong-entry" {
				old = filepath.Join(store, "commands")
			}
			path := filepath.Join(account, "agents")
			if err := os.Symlink(old, path); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "live", "live-once":
				writeFixture(t, filepath.Join(account, "sessions", "4242.json"), "{}")
				runner.options.ProcRoot = filepath.Join(home, "proc")
				if err := os.MkdirAll(filepath.Join(runner.options.ProcRoot, "4242"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(old, filepath.Join(account, "commands")); err != nil {
					t.Fatal(err)
				}
			case "live-read-error":
				writeFixture(t, filepath.Join(account, "sessions"), "file")
			}
			runner.options.ClaudeAccounts = []pfmconfig.Account{{ID: 1, ConfigDir: account}}
			var writer *storeMutationWriter
			if scenario == "live-once" {
				writer = &storeMutationWriter{match: "  skip    repoint " + path, mutate: func() {
					sessions := filepath.Join(account, "sessions")
					if err := os.RemoveAll(sessions); err != nil {
						t.Fatal(err)
					}
					writeFixture(t, sessions, "file")
				}}
				runner.options.Stdout = writer
			}
			output.Reset()
			err := runner.wireClaudeStore()
			if scenario == "live-read-error" {
				if err == nil || !strings.Contains(err.Error(), "read live chats in "+account) {
					t.Fatalf("live-chat read error=%v", err)
				}
				if got, err := os.Readlink(path); err != nil || got != old {
					t.Fatalf("link=%q error=%v want=%q", got, err, old)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var want strings.Builder
			for _, entry := range StoreEntries {
				fmt.Fprintln(&want, "  ok      "+filepath.Join(store, entry.Name))
			}
			fmt.Fprintln(&want, "  ok      "+account)
			for _, entry := range StoreEntries {
				path, target := filepath.Join(account, entry.Name), filepath.Join(store, entry.Name)
				switch {
				case (scenario == "live" || scenario == "live-once") && (entry.Name == "agents" || entry.Name == "commands"):
					fmt.Fprintf(
						&want,
						"  skip    repoint %s: live chats 4242 on %s — close them and rerun pfm install --yes\n",
						path,
						account,
					)
					target = old
				case entry.Name == "agents":
					fmt.Fprintf(&want, "  change  repoint %s -> %s (was %s)\n", path, target, old)
				default:
					fmt.Fprintf(&want, "  change  link %s -> %s\n", path, target)
				}
				if got, err := os.Readlink(path); err != nil || got != target {
					t.Errorf("link=%q error=%v want=%q", got, err, target)
				}
			}
			if writer != nil {
				output = writer.output
			}
			if output.String() != want.String() {
				t.Fatalf("transcript=%q want=%q", output.String(), want.String())
			}
			if scratch, err := filepath.Glob(
				filepath.Join(account, ".agents.pfm-link-*"),
			); err != nil ||
				len(scratch) != 0 {
				t.Fatalf("scratch=%v error=%v", scratch, err)
			}
		})
	}
	for _, scenario := range []string{"dangling-store", "file-in-dir", "dir-in-file"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			store, account := ClaudeStore(home), pfmconfig.DefaultAccountDir(home, 1)
			path, reason := filepath.Join(store, "agents"), "a file where a directory belongs"
			switch scenario {
			case "dangling-store":
				path, reason = filepath.Join(store, "projects"), "a dangling link"
				if err := os.MkdirAll(store, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(home, "gone"), path); err != nil {
					t.Fatal(err)
				}
			case "file-in-dir":
				writeFixture(t, path, "file")
			case "dir-in-file":
				path, reason = filepath.Join(store, "settings.json"), "a directory where a file belongs"
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			runner := &engine{
				options: Options{
					Home:           home,
					ConfigDir:      store,
					ClaudeAccounts: []pfmconfig.Account{{ID: 1, ConfigDir: account}},
					Stdout:         &bytes.Buffer{},
				},
				apply: true,
			}
			want := fmt.Sprintf("store entry %s broken: %s — remove %s, then run pfm install --yes", path, reason, path)
			if err := runner.wireClaudeStore(); err == nil || err.Error() != want {
				t.Fatalf("error=%v want=%s", err, want)
			}
			assertAbsent(t, account)
		})
	}
}

func TestInspectClaudeStore(t *testing.T) {
	for _, scenario := range []string{"missing-store", "missing-account", "missing-link", "elsewhere", "relative-link", "real", "account-store", "unreadable-store", "unreadable-account", "account-not-dir"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			store := ClaudeStore(home)
			account := pfmconfig.DefaultAccountDir(home, 1)
			if err := os.MkdirAll(account, 0o700); err != nil {
				t.Fatal(err)
			}
			for _, entry := range StoreEntries {
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
			switch scenario {
			case "missing-store":
				if err := os.Remove(filepath.Join(store, "CLAUDE.md")); err != nil {
					t.Fatal(err)
				}
			case "missing-account", "account-store", "unreadable-account":
				if err := os.RemoveAll(account); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.Remove(link); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "elsewhere":
				if err := os.Symlink("../old/agents", link); err != nil {
					t.Fatal(err)
				}
			case "relative-link":
				target, err := filepath.Rel(account, filepath.Join(store, "agents"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
			case "real":
				if err := os.Mkdir(link, 0o700); err != nil {
					t.Fatal(err)
				}
			case "account-store":
				if err := os.Symlink(store, account); err != nil {
					t.Fatal(err)
				}
			case "unreadable-store":
				if err := os.RemoveAll(store); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(store, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			case "unreadable-account":
				if err := os.Symlink(account, account); err != nil {
					t.Fatal(err)
				}
			case "account-not-dir":
				if err := os.RemoveAll(account); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(account, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			report := InspectClaudeStore(
				store,
				[]pfmconfig.Account{{ID: 2, ConfigDir: filepath.Join(home, "missing")}, {ID: 1, ConfigDir: account}},
			)
			if len(report.Entries) != len(StoreEntries) || len(report.Accounts) != 2 || report.Accounts[0].ID != 1 {
				t.Fatalf("report=%+v", report)
			}
			acct := report.Accounts[0]
			switch scenario {
			case "missing-store":
				if report.Entries[17].State != "missing" {
					t.Fatalf("entry=%+v", report.Entries[17])
				}
			case "missing-account":
				if acct.State != "missing" {
					t.Fatalf("account=%+v", acct)
				}
			case "account-store":
				if acct.State != "store" || len(acct.Links) != 0 {
					t.Fatalf("account=%+v", acct)
				}
			case "unreadable-store":
				if report.Entries[0].State != "unreadable" || !errors.Is(report.Entries[0].Err, syscall.ENOTDIR) {
					t.Fatalf("entry=%+v", report.Entries[0])
				}
			case "unreadable-account":
				if acct.State != "unreadable" || !errors.Is(acct.Err, syscall.ELOOP) {
					t.Fatalf("account=%+v", acct)
				}
			case "account-not-dir":
				if acct.ID != 1 || acct.Dir != account || acct.State != "not-dir" || acct.Links != nil ||
					acct.Err != nil {
					t.Fatalf("account=%+v", acct)
				}
			default:
				want := map[string]string{"missing-link": "missing", "elsewhere": "elsewhere", "relative-link": "ok", "real": "real"}[scenario]
				if len(acct.Links) != len(StoreEntries) || acct.Links[0].State != want {
					t.Fatalf("links=%+v want=%s", acct.Links, want)
				}
			}
		})
	}
	for _, scenario := range []string{"foreign", "dangling", "wrong-entry", "dangling-store", "file-in-dir", "dir-in-file", "linked-store", "unreadable-target"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			store, account := ClaudeStore(home), pfmconfig.DefaultAccountDir(home, 1)
			if err := (&engine{options: Options{Home: home, ConfigDir: store, Stdout: &bytes.Buffer{}}, apply: true}).wireClaudeStore(); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(account, 0o700); err != nil {
				t.Fatal(err)
			}
			path, target, want, reason := filepath.Join(account, "agents"), filepath.Join(home, "gone"), "elsewhere", ""
			index := 0
			switch scenario {
			case "foreign":
				target, want = filepath.Join(home, "old", "agents"), "foreign"
				writeFixture(t, filepath.Join(target, "kept"), "kept")
			case "wrong-entry":
				target = filepath.Join(store, "commands")
			case "dangling-store", "linked-store":
				index, path = 6, filepath.Join(store, "projects")
				want, reason = "broken", "a dangling link"
				if scenario == "linked-store" {
					target, want, reason = filepath.Join(home, "data", "projects"), "ok", ""
					if err := os.MkdirAll(target, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			case "file-in-dir":
				path, want, reason = filepath.Join(store, "agents"), "broken", "a file where a directory belongs"
			case "dir-in-file":
				index, path, want, reason = 16, filepath.Join(
					store,
					"settings.json",
				), "broken", "a directory where a file belongs"
			case "unreadable-target":
				target, want = path, "unreadable"
			}
			if strings.Contains(scenario, "store") || scenario == "file-in-dir" || scenario == "dir-in-file" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "file-in-dir":
				writeFixture(t, path, "file")
			case "dir-in-file":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			report := InspectClaudeStore(store, []pfmconfig.Account{{ID: 1, ConfigDir: account}})
			if strings.Contains(scenario, "store") || scenario == "file-in-dir" || scenario == "dir-in-file" {
				entry := report.Entries[index]
				if entry.State != want || (reason != "" && (entry.Err == nil || entry.Err.Error() != reason)) {
					t.Fatalf("entry=%+v want=%s reason=%q", entry, want, reason)
				}
			} else {
				link := report.Accounts[0].Links[0]
				if link.State != want || link.Target != target ||
					(want == "unreadable" && !errors.Is(link.Err, syscall.ELOOP)) {
					t.Fatalf("link=%+v want=%s target=%q", link, want, target)
				}
			}
		})
	}
}

// TestClaudeStoreSymlinkedAccount pins install's door of the account-dir rule:
// links land inside the resolved dir, a dir in the store is skipped, a
// dangling link is an inspection error, never "missing".
func TestClaudeStoreSymlinkedAccount(t *testing.T) {
	for _, scenario := range []string{"outside", "inside-store", "dangling"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			store := ClaudeStore(home)
			account := pfmconfig.DefaultAccountDir(home, 1)
			target := map[string]string{
				"outside":      filepath.Join(home, "data", "deep", "cc1"),
				"inside-store": filepath.Join(store, "projects"),
				"dangling":     filepath.Join(home, "gone"),
			}[scenario]
			for _, dir := range []string{filepath.Dir(account), filepath.Join(store, "projects")} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "outside" {
				if err := os.MkdirAll(target, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(target, account); err != nil {
				t.Fatal(err)
			}
			accounts := []pfmconfig.Account{{ID: 1, ConfigDir: account}}
			var output bytes.Buffer
			runner := &engine{
				options: Options{Home: home, ConfigDir: store, ClaudeAccounts: accounts, Stdout: &output},
				apply:   true,
			}
			err := runner.wireClaudeStore()
			acct := InspectClaudeStore(store, accounts).Accounts[0]
			switch scenario {
			case "outside":
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range StoreEntries {
					got, err := os.Readlink(filepath.Join(target, entry.Name))
					if err != nil || got != filepath.Join(store, entry.Name) {
						t.Fatalf("link %s = %q error=%v", entry.Name, got, err)
					}
				}
				relative, err := filepath.Rel(target, filepath.Join(store, "agents"))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(target, "agents")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(relative, filepath.Join(target, "agents")); err != nil {
					t.Fatal(err)
				}
				acct = InspectClaudeStore(store, accounts).Accounts[0]
				if acct.State != "ok" || len(acct.Links) != len(StoreEntries) {
					t.Fatalf("account=%+v", acct)
				}
				for _, link := range acct.Links {
					if link.State != "ok" {
						t.Fatalf("link=%+v", link)
					}
				}
				if info, err := os.Lstat(account); err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("account link replaced: %v %v", info, err)
				}
			case "inside-store":
				if err != nil || acct.State != "store" || len(acct.Links) != 0 {
					t.Fatalf("account=%+v error=%v", acct, err)
				}
				if !strings.Contains(output.String(), "account 1 "+account+" resolves to the store") {
					t.Fatalf("output=%q", output.String())
				}
				if _, err := os.Lstat(filepath.Join(target, "agents")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("store subdir written: %v", err)
				}
			case "dangling":
				if acct.State != "unreadable" || !errors.Is(acct.Err, os.ErrNotExist) {
					t.Fatalf("account=%+v", acct)
				}
				if err == nil || !strings.Contains(err.Error(), "inspect account 1 "+account) {
					t.Fatalf("wire error=%v", err)
				}
			}
		})
	}
}

// storeMutationWriter inserts an I/O failure after inspection and before the change.
type storeMutationWriter struct {
	output bytes.Buffer
	match  string
	mutate func()
}

func (writer *storeMutationWriter) Write(data []byte) (int, error) {
	if writer.mutate != nil && strings.Contains(string(data), writer.match) {
		mutate := writer.mutate
		writer.mutate = nil
		mutate()
	}
	return writer.output.Write(data)
}

func TestWireClaudeStoreWriteErrors(t *testing.T) {
	for _, scenario := range []string{"create-dir", "create-file", "account-dir", "link", "repoint"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			store := ClaudeStore(home)
			account := pfmconfig.DefaultAccountDir(home, 1)
			path := filepath.Join(store, "agents")
			match := "  change  create " + path
			want := "create store entry " + path
			if scenario == "create-file" {
				path = filepath.Join(store, "settings.json")
				match = "  change  create " + path
				want = "create store entry " + path
			}
			if scenario == "account-dir" {
				path = account
				match = "  change  create " + path
				want = "create account " + path
			}
			if scenario == "link" || scenario == "repoint" {
				path = filepath.Join(account, "agents")
				match = "  change  link " + path
				want = "link account entry " + path
				if scenario == "repoint" {
					if err := os.MkdirAll(account, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(filepath.Join(home, "old", "agents"), path); err != nil {
						t.Fatal(err)
					}
					match = "  change  repoint " + path
				}
			}
			writer := &storeMutationWriter{match: match, mutate: func() {
				switch scenario {
				case "create-dir":
					if err := os.WriteFile(store, nil, 0o600); err != nil {
						t.Fatal(err)
					}
				case "create-file":
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				case "account-dir":
					if err := os.WriteFile(filepath.Dir(account), nil, 0o600); err != nil {
						t.Fatal(err)
					}
				case "link":
					if err := os.WriteFile(path, nil, 0o600); err != nil {
						t.Fatal(err)
					}
				case "repoint":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(path, "kept"), nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}
			}}
			runner := &engine{
				options: Options{
					Home:           home,
					ConfigDir:      store,
					ClaudeAccounts: []pfmconfig.Account{{ID: 1, ConfigDir: account}},
					Stdout:         writer,
				},
				apply: true,
			}
			if err := runner.wireClaudeStore(); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("error=%v want=%s", err, want)
			}
			if scenario == "repoint" {
				assertContent(t, filepath.Join(path, "kept"), "")
				if scratch, err := filepath.Glob(
					filepath.Join(account, ".agents.pfm-link-*"),
				); err != nil ||
					len(scratch) != 0 {
					t.Fatalf("scratch=%v error=%v", scratch, err)
				}
			}
		})
	}
}

func TestWireClaudeStoreAccountOrder(t *testing.T) {
	home := t.TempDir()
	store := ClaudeStore(home)
	accounts := []pfmconfig.Account{
		{ID: 2, ConfigDir: pfmconfig.DefaultAccountDir(home, 2)},
		{ID: 1, ConfigDir: pfmconfig.DefaultAccountDir(home, 1)},
	}
	var output bytes.Buffer
	runner := &engine{options: Options{Home: home, ConfigDir: store, ClaudeAccounts: accounts, Stdout: &output}}
	if err := runner.wireClaudeStore(); err != nil {
		t.Fatal(err)
	}
	var want strings.Builder
	for _, entry := range StoreEntries {
		fmt.Fprintln(&want, "  change  create "+filepath.Join(store, entry.Name))
	}
	for _, index := range []int{1, 0} {
		fmt.Fprintln(&want, "  change  create "+accounts[index].ConfigDir)
	}
	for _, index := range []int{1, 0} {
		for _, entry := range StoreEntries {
			fmt.Fprintf(
				&want,
				"  change  link %s -> %s\n",
				filepath.Join(accounts[index].ConfigDir, entry.Name),
				filepath.Join(store, entry.Name),
			)
		}
	}
	if output.String() != want.String() {
		t.Fatalf("account order=%q want=%q", output.String(), want.String())
	}

	t.Run("alias", func(t *testing.T) {
		home := t.TempDir()
		store, account := ClaudeStore(home), pfmconfig.DefaultAccountDir(home, 1)
		alias := filepath.Join(home, "alias")
		if err := os.MkdirAll(account, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(account, alias); err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		accounts := []pfmconfig.Account{{ID: 2, ConfigDir: alias}, {ID: 1, ConfigDir: account}}
		runner := &engine{
			options: Options{Home: home, ConfigDir: store, ClaudeAccounts: accounts, Stdout: &output},
			apply:   true,
		}
		if err := runner.wireClaudeStore(); err != nil {
			t.Fatal(err)
		}
		var want strings.Builder
		for _, entry := range StoreEntries {
			fmt.Fprintln(&want, "  change  create "+filepath.Join(store, entry.Name))
		}
		fmt.Fprintf(&want, "  ok      %s\n  skip    account 2 %s is the same directory as account 1\n", account, alias)
		for _, entry := range StoreEntries {
			path, target := filepath.Join(account, entry.Name), filepath.Join(store, entry.Name)
			fmt.Fprintf(&want, "  change  link %s -> %s\n", path, target)
			if got, err := os.Readlink(path); err != nil || got != target {
				t.Fatalf("link=%q error=%v want=%q", got, err, target)
			}
		}
		if output.String() != want.String() {
			t.Fatalf("transcript=%q want=%q", output.String(), want.String())
		}
		if report := InspectClaudeStore(store, accounts); len(report.Accounts) != 2 {
			t.Fatalf("report=%+v", report)
		}
	})
}

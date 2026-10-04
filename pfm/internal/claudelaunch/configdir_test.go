package claudelaunch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestCheckConfigDirSymlinkedAccount pins the launch door of the account-dir
// rule: a link resolving outside the store launches with its literal path, one
// resolving to or into the store is refused, a dangling one names its error.
func TestCheckConfigDirSymlinkedAccount(t *testing.T) {
	store := jailStore(t)
	home := filepath.Dir(store)
	root := t.TempDir()
	outside := filepath.Join(root, "data", "cc2")
	for _, dir := range []string{filepath.Join(store, "projects"), outside} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for index, tc := range []struct {
		name, target string
		into         bool
	}{
		{name: "outside the store", target: outside},
		{name: "the store", target: store, into: true},
		{name: "inside the store", target: filepath.Join(store, "projects"), into: true},
		{name: "dangling", target: filepath.Join(root, "gone")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(root, fmt.Sprint(index))
			if err := os.Symlink(tc.target, dir); err != nil {
				t.Fatal(err)
			}
			err := CheckConfigDir(2, dir)
			switch {
			case tc.into:
				want := "account 2: " + dir + " resolves to the Claude store — run pfm doctor"
				if err == nil || err.Error() != want {
					t.Fatalf("CheckConfigDir = %v, want %q", err, want)
				}
			case tc.target == outside:
				if err != nil {
					t.Fatalf("CheckConfigDir = %v, want accepted", err)
				}
				_, machine := renderMachine(t)
				launch, err := Render(Request{Purpose: PurposeInteractive, Home: home, ConfigDir: dir}, machine)
				if err != nil {
					t.Fatalf("Render = %v", err)
				}
				if !slices.Contains(launch.Env, configDirEnv+"="+dir) {
					t.Fatalf("env = %v, want the literal %s", launch.Env, dir)
				}
			default:
				want := fmt.Sprintf("account 2: inspect %s: stat %s: %v", dir, dir, syscall.ENOENT)
				if err == nil || err.Error() != want || !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("CheckConfigDir = %v, want %q", err, want)
				}
			}
		})
	}
}

// jailStore is the store CheckConfigDir compares against: the jail home's.
func jailStore(t *testing.T) string {
	t.Helper()
	home, err := paths.Home()
	if err != nil {
		t.Fatal(err)
	}
	return ClaudeStore(home)
}

// TestInheritedConfigDirTellsTheLoginDefaultFromAnExplicitValue pins the one
// rule every reader asks: the login default is CLAUDE_CONFIG_DIR with the
// sentinel naming the same dir; anything else is an explicit value.
func TestInheritedConfigDirTellsTheLoginDefaultFromAnExplicitValue(t *testing.T) {
	for _, test := range []struct {
		name, value, sentinel string
		want                  bool
	}{
		{"neither", "", "", false},
		{"login default", "/home/test/.cc/1", "/home/test/.cc/1", true},
		{"login default, spelled with a trailing slash", "/home/test/.cc/1/", "/home/test/.cc/1", true},
		{"explicit, no sentinel", "/home/test/.cc/1", "", false},
		{"explicit over a stale sentinel", "/home/test/.cc/2", "/home/test/.cc/1", false},
		{"sentinel alone", "", "/home/test/.cc/1", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := map[string]string{"CLAUDE_CONFIG_DIR": test.value, ConfigDirDefaultEnv: test.sentinel}
			if got := InheritedConfigDir(func(name string) string { return env[name] }); got != test.want {
				t.Fatalf("InheritedConfigDir(%q, sentinel %q) = %t, want %t", test.value, test.sentinel, got, test.want)
			}
		})
	}
}

// TestEveryLaunchStripsTheLoginDefaultSentinel pins the sentinel's meaning:
// a pfm launch sets CLAUDE_CONFIG_DIR itself, so the sentinel never reaches a
// child whose dir pfm chose, in either hygiene list.
func TestEveryLaunchStripsTheLoginDefaultSentinel(t *testing.T) {
	for name, list := range map[string][]string{"Hygiene": Hygiene(), "IdentityHygiene": IdentityHygiene()} {
		found := false
		for _, entry := range list {
			found = found || entry == ConfigDirDefaultEnv
		}
		if !found {
			t.Errorf("%s() = %q, want it to strip %s", name, list, ConfigDirDefaultEnv)
		}
	}
}

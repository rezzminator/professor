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

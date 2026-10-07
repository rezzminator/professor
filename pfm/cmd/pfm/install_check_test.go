package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/doctor"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
	"github.com/rezzminator/professor/pfm/internal/store"
)

func TestInstallCheckRefusesYes(t *testing.T) {
	for _, args := range [][]string{{"--check", "--yes"}, {"--yes", "--check"}} {
		var out, errOut bytes.Buffer
		if code := runInstall(args, &out, &errOut); code != 2 {
			t.Fatalf("code=%d stderr=%q", code, errOut.String())
		}
	}
}

// installCheckTree maps every path under root to its content, so a test can
// prove `pfm install --check` wrote nothing; a symlink is recorded as its target.
func installCheckTree(t *testing.T, root string) map[string]string {
	t.Helper()
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			tree[path] = "dir"
			return nil
		}
		// A link is its target, so a dangling one the apply leaves reads as a change.
		if entry.Type()&os.ModeSymlink != 0 {
			target, linkErr := os.Readlink(path)
			tree[path] = "link:" + target
			return linkErr
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		tree[path] = string(raw)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return tree
}

// installCheckHome stages a host with one Claude account under {home}/.cc/1
// and its config in the clone (migrated) or in ~/.config/pfm (legacy, the
// host the install gate refuses until the operator moves it).
func installCheckHome(t *testing.T, legacy bool) (home, clone, account string) {
	t.Helper()
	home = t.TempDir()
	clone, account = filepath.Join(home, "clone"), pfmconfig.DefaultAccountDir(home, 1)
	for _, dir := range []string{clone, account, filepath.Join(home, "proc")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for key, value := range map[string]string{
		"HOME": home, paths.EnvHome: home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"),
		paths.EnvProcRoot: filepath.Join(home, "proc"), paths.EnvStateDB: "", paths.EnvCacheDB: "",
		paths.EnvConfig: "",
	} {
		t.Setenv(key, value)
	}
	if err := os.Unsetenv(paths.EnvConfig); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(clone, pfmconfig.FileName)
	if legacy {
		configPath = filepath.Join(home, ".config", "pfm", pfmconfig.FileName)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf(`{"version":2,"accounts":[{"id":1,"configDir":%q}]}`+"\n", account)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, clone, account
}

func TestInstallCheckPreChangeChecks(t *testing.T) {
	for _, scenario := range []string{"quiet", "running", "short reminder", "unprobed", "dependency", "explicit missing"} {
		t.Run(scenario, func(t *testing.T) {
			home, _, _ := installCheckHome(t, false)
			bin, _ := writeManagerFakes(
				t,
				"case \"$*\" in *ActiveState*) echo inactive;; esac\nexit 0",
				"echo 'state = not running'",
			)
			wantCode, wantErr := 0, ""
			if scenario == "dependency" {
				wantCode = 1
			}
			if scenario == "running" {
				bin, _ = writeManagerFakes(
					t,
					"case \"$*\" in *ActiveState*) echo activating;; esac\nexit 0",
					"echo 'state = running'",
				)
				schedulerErr := installer.ErrNameSyncRunning
				if runtime.GOOS == "darwin" {
					schedulerErr = installer.ErrLaunchAgentRunning
				}
				wantCode, wantErr = 4, installer.SchedulerRefusal(installCommand, schedulerErr)+"\n"
			}
			if scenario == "short reminder" {
				bin, _ = writeManagerFakes(
					t,
					`case "$*" in *pfm-reminder.service*) if [ -e "$0.once" ]; then echo inactive; else : > "$0.once"; echo activating; fi;; *ActiveState*) echo inactive;; esac`+"\nexit 0",
					`case "$*" in *com.professor.pfm.reminder*) if [ -e "$0.once" ]; then echo 'state = not running'; else : > "$0.once"; echo 'state = running'; fi;; *) echo 'state = not running';; esac`,
				)
			}
			if scenario == "unprobed" {
				bin, _ = writeManagerFakes(t,
					"case \"$*\" in *show*) exit 1;; esac\nexit 0",
					"exit 1",
				)
				// launchctl's positive exit answers "unknown label", which is
				// idle. A missing tool models a probe that could not run.
				if err := os.Remove(filepath.Join(bin, "launchctl")); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			savedProbe, savedInstaller := doctor.DependencyProbeOverride, runInstaller
			t.Cleanup(func() { doctor.DependencyProbeOverride, runInstaller = savedProbe, savedInstaller })
			doctor.DependencyProbeOverride = func(_ context.Context, entries []deps.Entry, _ deps.ProbeOptions) []deps.Result {
				if scenario != "dependency" {
					return nil
				}
				for _, entry := range entries {
					if entry.Name == "tmux" {
						return []deps.Result{{Entry: entry, State: deps.StateMissing}}
					}
				}
				t.Fatal("tmux registry entry missing")
				return nil
			}
			runInstaller = func(context.Context, installer.Options) (installer.Report, error) {
				t.Fatal("check ran installer")
				return installer.Report{}, nil
			}
			flag := ""
			if scenario == "explicit missing" {
				flag = filepath.Join(home, "missing.json")
				wantCode = 1
			}
			loaded, err := pfmconfig.LoadInstallRuntime(flag)
			if err != nil {
				t.Fatal(err)
			}
			before := installCheckTree(t, home)
			var out, errOut bytes.Buffer
			code := runInstall([]string{"--check"}, &out, &errOut, loaded)
			if code != wantCode {
				t.Fatalf("code=%d want=%d stdout=%q stderr=%q", code, wantCode, out.String(), errOut.String())
			}
			if !reflect.DeepEqual(before, installCheckTree(t, home)) {
				t.Fatal("check changed home")
			}
			switch scenario {
			case "quiet", "short reminder":
				if out.String() != "install check: ok — pfm install --yes would pass its pre-change checks\n" ||
					errOut.Len() != 0 {
					t.Fatalf("stdout=%q stderr=%q", out.String(), errOut.String())
				}
			case "running":
				if errOut.String() != wantErr {
					t.Fatalf("stderr=%q want=%q", errOut.String(), wantErr)
				}
			case "unprobed":
				note := "name-sync gate NOT probed (systemctl show could not read the unit state); an apply during a name-sync or reminder run is not refused"
				if runtime.GOOS == "darwin" {
					note = "launch-agent gate NOT probed (launchctl print could not run or its output could not be read); an apply during a name-sync or reminder run is not refused"
				}
				want := "install check: ok — pfm install --yes would pass its pre-change checks\n  skip    " + note + "\n"
				if out.String() != want || errOut.Len() != 0 {
					t.Fatalf("stdout=%q stderr=%q, want %q", out.String(), errOut.String(), want)
				}
			case "dependency":
				if !strings.Contains(out.String(), "doctor: dep tmux path=(none) MISSING required") ||
					errOut.String() != "pfm install: required dependency preflight failed\n" {
					t.Fatalf("stdout=%q stderr=%q", out.String(), errOut.String())
				}
			case "explicit missing":
				if !strings.Contains(errOut.String(), "--config "+flag+" does not exist") {
					t.Fatal(errOut.String())
				}
			}
		})
	}
}

// installDatabaseFile is one database as a check must leave it: the main
// file's bytes and the schema version a reader sees through its WAL.
func installDatabaseFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlitedb.OpenReadOnly(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(errors.Join(err, db.Close()))
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("v%d sha256=%x", version, sha256.Sum256(raw))
}

// TestInstallCheckAndPreviewLeaveOlderDatabasesUnmigrated: make host-install
// runs the NEW binary's `pfm install --check` before the swap, and a refusal
// keeps the old binary — so a check, or a preview, that migrated a database
// would lock the binary it kept out of its own state. Neither migrates.
func TestInstallCheckAndPreviewLeaveOlderDatabasesUnmigrated(t *testing.T) {
	installCheckHome(t, false)
	bin, _ := writeManagerFakes(
		t,
		"case \"$*\" in *ActiveState*) echo inactive;; esac\nexit 0",
		"echo 'state = not running'",
	)
	t.Setenv("PATH", bin)
	savedProbe, savedInstaller := doctor.DependencyProbeOverride, runInstaller
	t.Cleanup(func() { doctor.DependencyProbeOverride, runInstaller = savedProbe, savedInstaller })
	doctor.DependencyProbeOverride = func(context.Context, []deps.Entry, deps.ProbeOptions) []deps.Result {
		return nil
	}
	runInstaller = func(context.Context, installer.Options) (installer.Report, error) {
		return installer.Report{}, nil
	}
	loaded, err := pfmconfig.LoadInstallRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	database, err := store.Open(store.WithWarningWriter(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	older := map[string]int{
		loaded.Paths.StateDB: fleetdb.SchemaVersion - 1,
		loaded.Paths.CacheDB: store.SchemaVersion - 1,
	}
	for path, version := range older {
		db, err := sqlitedb.OpenReadWrite(path, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version=%d", version)); err != nil {
			t.Fatal(errors.Join(err, db.Close()))
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"--check"}, {"--skip-harvest"}} {
		before := map[string]string{}
		for path := range older {
			before[path] = installDatabaseFile(t, path)
		}
		var out, errOut bytes.Buffer
		if code := runInstall(args, &out, &errOut, loaded); code != 0 {
			t.Fatalf("pfm install %v code=%d stdout=%q stderr=%q", args, code, out.String(), errOut.String())
		}
		for path, version := range older {
			if after := installDatabaseFile(t, path); after != before[path] {
				t.Fatalf("pfm install %v changed %s (older v%d): %s -> %s", args, path, version, before[path], after)
			}
			backups, err := filepath.Glob(path + ".bak-before-v*")
			if err != nil {
				t.Fatal(err)
			}
			if len(backups) != 0 {
				t.Fatalf("pfm install %v wrote a migration backup %v", args, backups)
			}
		}
	}
}

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestInstallYesRefusesLiveSessionMergeBeforeAnyChange(t *testing.T) {
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	runInstaller = func(_ context.Context, options installer.Options) (installer.Report, error) {
		if options.Mode != installer.ModeDryRun {
			t.Fatal("applying installer ran after layout refusal")
		}
		return installer.Report{}, nil
	}
	home := t.TempDir()
	proc := filepath.Join(home, "proc")
	account := filepath.Join(home, ".cc", "2")
	for _, dir := range []string{proc, account, filepath.Join(home, ".claude"), filepath.Join(home, ".config", "pfm")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	config := pfmconfig.Defaults(home, nil, "")
	config.Path = filepath.Join(home, "pfm.config.json")
	config.Exists = true
	config.Accounts = append(config.Accounts, pfmconfig.Account{ID: 2, ConfigDir: account})
	legacy := filepath.Join(home, ".config", "pfm", "pfm.config.json")
	if err := os.WriteFile(legacy, []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sessionStore := filepath.Join(account, "file-history")
	if err := os.MkdirAll(filepath.Join(sessionStore, "session"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionStore, "session", "checkpoint"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(account, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(account, "sessions", "123.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(proc, "123"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtime := commandRuntime{Config: config, Paths: paths.Values{
		Home: home, ProcRoot: proc,
		ManagedSettingsDir: filepath.Join(home, "managed"), StateDB: filepath.Join(home, "state.db"),
		CacheDB: filepath.Join(home, "cache.db"),
	}}
	snapshot := func() map[string]string {
		t.Helper()
		state := map[string]string{}
		err := filepath.Walk(home, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(home, path)
			if err != nil {
				return err
			}
			switch {
			case info.Mode().IsRegular():
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				state[relative] = "file:" + string(data)
			case info.Mode()&os.ModeSymlink != 0:
				target, err := os.Readlink(path)
				if err != nil {
					return err
				}
				state[relative] = "link:" + target
			default:
				state[relative] = info.Mode().String()
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	before := snapshot()
	var stdout, stderr bytes.Buffer
	code := runInstall([]string{"--yes", "--skip-harvest"}, &stdout, &stderr, runtime)
	if code != 1 || !strings.HasPrefix(stderr.String(), "pfm install: refused before any change:\n") ||
		!strings.Contains(stderr.String(), "  refuse  layout session-store "+sessionStore+" — live chats: 123") ||
		strings.Contains(stdout.String(), "install journal:") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got, err := os.ReadFile(legacy); err != nil || string(got) != `{"version":2}` {
		t.Fatalf("legacy config changed: %q %v", got, err)
	}
	if got, err := os.ReadFile(
		filepath.Join(sessionStore, "session", "checkpoint"),
	); err != nil ||
		string(got) != "keep" {
		t.Fatalf("account store changed: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "state", "pfm", "migrations")); !os.IsNotExist(err) {
		t.Fatalf("install created journal directory: %v", err)
	}
	if after := snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatalf("refused install changed HOME: before=%v after=%v", before, after)
	}
}

// An identical config.json.pre-split beside the legacy config.json (a
// rollback restoring the pre-update config is one producer, issue #24 #7)
// must not abort the install with the "already exists" refusal.
func TestInstallApplyContinuesPastAnIdenticalPreSplitBackup(t *testing.T) {
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	runInstaller = func(_ context.Context, _ installer.Options) (installer.Report, error) {
		return installer.Report{}, nil
	}
	runtime := preSplitInstallRuntime(t)
	var stdout, stderr bytes.Buffer
	if code := runInstall([]string{"--yes", "--skip-harvest"}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf(
			"runInstall() code=%d stdout=%q stderr=%q, want 0 for an identical pre-split backup",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	if strings.Contains(stderr.String(), "apply config migration") {
		t.Fatalf("stderr=%q, want no apply config migration failure", stderr.String())
	}
}

// preSplitInstallRuntime is a legacy home whose pre-split config.json the
// layout moves (layout work) beside an identical pre-split backup; the
// managed drop-in goes to a temp dir, never the package directory.
func preSplitInstallRuntime(t *testing.T) commandRuntime {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "pfm")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, pfmconfig.LegacyFileName)
	content := `{"version":2,"theme":"tokyo-night","mcp":{"http":{"port":8377}}}`
	if err := os.WriteFile(legacy, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json.pre-split"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	// The jail's own config and clone would conflict with the legacy file:
	// the layout moves config.json to a free target in a clone of its own.
	t.Setenv(paths.EnvConfig, "")
	t.Setenv("PFM_SOURCE_REPO", t.TempDir())
	loaded, err := pfmconfig.Load("", home, nil)
	if err != nil {
		t.Fatal(err)
	}
	return commandRuntime{Paths: paths.Values{Home: home, ManagedSettingsDir: t.TempDir()}, Config: loaded}
}

// TestInstallApplyRestartsTheStoppedSchedulerAfterAFailedRun proves the
// name-sync scheduler units a migrating apply stops start again once
// installer.Run returned, a failed Run included: the service managers are
// fakes on PATH, stateful per unit.
func TestInstallApplyRestartsTheStoppedSchedulerAfterAFailedRun(t *testing.T) {
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	runInstaller = func(_ context.Context, options installer.Options) (installer.Report, error) {
		if options.Mode == installer.ModeApply {
			return installer.Report{}, errors.New("installer run failed")
		}
		return installer.Report{}, nil
	}
	fake := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
d=%q
echo "${0##*/} $*" >>"$d/calls"
case "${0##*/} $1" in
"systemctl --version") echo "systemd 255" ;;
"systemctl --user")
  shift
  case "$1" in
  show) eval "u=\${$#}"; if [ "$2" = --property=MainPID ]; then echo 0; elif [ -f "$d/up.$u" ]; then echo active; else echo inactive; fi ;;
  stop) shift; for u; do rm -f "$d/up.$u"; done ;;
  start) shift; for u; do : >"$d/up.$u"; done ;;
  esac ;;
"launchctl print") [ -f "$d/up.${2##*/}" ] || exit 113; echo "state = not running" ;;
"launchctl bootout") rm -f "$d/up.${2##*/}" ;;
"launchctl bootstrap") b=${3##*/}; : >"$d/up.${b%%.plist}" ;;
esac
exit 0
`, fake)
	scheduler := []string{"pfm-name-sync.path", "pfm-name-sync.timer"}
	if goruntime.GOOS == "darwin" {
		scheduler = []string{"com.professor.pfm.name-sync"}
	}
	for _, name := range []string{"systemctl", "launchctl"} {
		if err := testjail.WriteExecutable(filepath.Join(fake, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, unit := range scheduler {
		if err := os.WriteFile(filepath.Join(fake, "up."+unit), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	runtime := preSplitInstallRuntime(t)
	var stdout, stderr bytes.Buffer
	if code := runInstall([]string{"--yes", "--skip-harvest"}, &stdout, &stderr, runtime); code == 0 {
		t.Fatalf("runInstall() = 0 after a failed Run; stderr=%q", stderr.String())
	}
	calls, err := os.ReadFile(filepath.Join(fake, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), " stop ") && !strings.Contains(string(calls), "bootout ") {
		t.Fatalf("the apply stopped no scheduler unit: calls %q stderr=%q", calls, stderr.String())
	}
	for _, unit := range scheduler {
		if _, err := os.Stat(filepath.Join(fake, "up."+unit)); err != nil {
			t.Fatalf("%s left stopped after the failed Run: calls %q stderr=%q", unit, calls, stderr.String())
		}
	}
}

// TestInstallApplyExits97ForANameSyncJobRunningAfterTheStop proves the
// apply's post-stop name-sync refusal exits 97 like the pre-change ask, with
// its stop command, before any write, and the stopped scheduler starts again.
func TestInstallApplyExits97ForANameSyncJobRunningAfterTheStop(t *testing.T) {
	if goruntime.GOOS == "darwin" {
		t.Skip("launchd's bootout kills a running job: the post-stop ask cannot see one on macOS")
	}
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	runInstaller = func(_ context.Context, options installer.Options) (installer.Report, error) {
		if options.Mode == installer.ModeApply {
			t.Error("installer.Run ran after the post-stop refusal")
		}
		return installer.Report{}, nil
	}
	// The job reads idle until the stop, then running: its schedule started it
	// between the pre-change ask and the stop.
	fake, scheduler := fakeSchedulerSystemctl(t, `: >"$d/job"`)
	runtime := preSplitInstallRuntime(t)
	var stdout, stderr bytes.Buffer
	if code := runInstall([]string{"--yes", "--skip-harvest"}, &stdout, &stderr, runtime); code != 97 ||
		!strings.Contains(stderr.String(), "systemctl --user stop pfm-name-sync.service") {
		t.Fatalf("runInstall() = %d, want 97 with the stop command; stderr=%q", code, stderr.String())
	}
	if journals, err := installer.InstallJournals(runtime.Paths.Home); err != nil || len(journals) != 0 {
		t.Fatalf("journal written before the refusal: %+v err=%v", journals, err)
	}
	for _, unit := range scheduler {
		if _, err := os.Stat(filepath.Join(fake, "up."+unit)); err != nil {
			t.Fatalf("%s left stopped after the refusal: stderr=%q", unit, stderr.String())
		}
	}
}

// fakeSchedulerSystemctl puts on PATH a systemctl whose name-sync scheduler
// units are up; its stop runs afterStop, and the name-sync job reads running
// once "$d/job" exists. It returns the fake's directory and the units.
func fakeSchedulerSystemctl(t *testing.T, afterStop string) (string, []string) {
	t.Helper()
	fake := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
d=%q
echo "${0##*/} $*" >>"$d/calls"
case "${0##*/} $1" in
"systemctl --version") echo "systemd 255" ;;
"systemctl --user")
  shift
  case "$1" in
  show) eval "u=\${$#}"; if [ "$2" = --property=MainPID ]; then echo 0
    elif [ "$u" = pfm-name-sync.service ] && [ -f "$d/job" ]; then echo activating
    elif [ -f "$d/up.$u" ]; then echo active; else echo inactive; fi ;;
  stop) shift; for u; do rm -f "$d/up.$u"; done; %s ;;
  start) shift; for u; do : >"$d/up.$u"; done ;;
  esac ;;
esac
exit 0
`, fake, afterStop)
	if err := testjail.WriteExecutable(filepath.Join(fake, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	scheduler := []string{"pfm-name-sync.path", "pfm-name-sync.timer"}
	for _, unit := range scheduler {
		if err := os.WriteFile(filepath.Join(fake, "up."+unit), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	return fake, scheduler
}

// TestInstallApplyExits128PlusTheSignalForAnInterruptedLayout delivers a
// SIGTERM to pfm install from inside its scheduler stop: the layout runs no
// row, installer.Run never runs, the stopped scheduler starts again and the
// install exits 128 + SIGTERM, its notice on the command's stderr.
func TestInstallApplyExits128PlusTheSignalForAnInterruptedLayout(t *testing.T) {
	if goruntime.GOOS == "darwin" {
		t.Skip("the fake scheduler is a systemctl on PATH; macOS reaches launchctl")
	}
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	runInstaller = func(_ context.Context, options installer.Options) (installer.Report, error) {
		if options.Mode == installer.ModeApply {
			t.Error("installer.Run ran after the interrupt")
		}
		return installer.Report{}, nil
	}
	fake, scheduler := fakeSchedulerSystemctl(t, "kill -TERM $PPID; sleep 1")
	runtime := preSplitInstallRuntime(t)
	var stdout, stderr bytes.Buffer
	if code := runInstall([]string{"--yes", "--skip-harvest"}, &stdout, &stderr, runtime); code != 128+15 ||
		!strings.Contains(stderr.String(), "pfm install: interrupt received (terminated)") {
		t.Fatalf("runInstall() = %d, want 143 with the notice; stderr=%q", code, stderr.String())
	}
	if journals, err := installer.InstallJournals(runtime.Paths.Home); err != nil || len(journals) != 0 {
		t.Fatalf("journal written after the interrupt: %+v err=%v", journals, err)
	}
	for _, unit := range scheduler {
		if _, err := os.Stat(filepath.Join(fake, "up."+unit)); err != nil {
			t.Fatalf("%s left stopped after the interrupt: stderr=%q", unit, stderr.String())
		}
	}
}

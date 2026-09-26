package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	goRuntime "runtime"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/doctor"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
	"github.com/rezzminator/professor/pfm/internal/store"
)

func TestInstallerOptionsCarryEachEngineRosterIndependently(t *testing.T) {
	home := t.TempDir()
	runtime := commandRuntime{
		Paths: paths.Values{Home: home},
		Config: pfmconfig.Config{
			Accounts: []pfmconfig.Account{{ID: 4, ConfigDir: filepath.Join(home, ".cc", "4")}},
			CodexAccounts: []pfmconfig.CodexAccount{
				{ID: 2, Home: filepath.Join(home, ".codex-2")},
				{ID: 7, Home: filepath.Join(home, ".codex-7")},
			},
		},
	}
	options := newInstallerOptions(installer.ModeDryRun, "", true, io.Discard, io.Discard, runtime)
	if !reflect.DeepEqual(options.ConfigDirs, []string{runtime.Config.Accounts[0].ConfigDir}) ||
		!reflect.DeepEqual(
			options.CodexHomes,
			[]string{runtime.Config.CodexAccounts[0].Home, runtime.Config.CodexAccounts[1].Home},
		) {
		t.Fatalf("installer rosters ConfigDirs=%q CodexHomes=%q", options.ConfigDirs, options.CodexHomes)
	}
	if options.OpenCodeConfigPath != installer.OpenCodeConfigPath(home) {
		t.Fatalf("OpenCodeConfigPath=%q, want %q", options.OpenCodeConfigPath, installer.OpenCodeConfigPath(home))
	}
}

// TestInstallOptionsSourceRepoFallsBackToTheRecordedClone is a REGRESSION
// test for the 2026-09-14 retro finding: `pfm install --yes` run outside the
// source checkout (cwd discovery finds nothing) must still resolve
// options.SourceRepo from the recorded source-repo marker rather than
// leaving it empty and falling through to the release manifest URL. FAILS on
// unfixed code because newInstallerOptions sets SourceRepo from
// discoverSourceRepo() alone, which returns "" outside a clone.
func TestInstallOptionsSourceRepoFallsBackToTheRecordedClone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("PFM_SOURCE_REPO", "")
	t.Chdir(t.TempDir()) // no repo markers here — discoverSourceRepo() finds nothing
	home := t.TempDir()
	clone := t.TempDir()
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	runtime := commandRuntime{Paths: paths.Values{Home: home}}
	options := newInstallerOptions(installer.ModeDryRun, "", true, io.Discard, io.Discard, runtime)
	got, err := filepath.EvalSymlinks(options.SourceRepo)
	if err != nil {
		t.Fatalf("options.SourceRepo = %q: %v", options.SourceRepo, err)
	}
	want, err := filepath.EvalSymlinks(clone)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("options.SourceRepo = %q, want the recorded clone %q", got, want)
	}
}

// TestInstallOptionsNameAnUnusableRecordedCloneBeforeFallingBack pins the
// difference between "no clone was ever recorded" and "the recorded clone is
// gone": both leave SourceRepo empty and send install to the release
// manifest, but only one of them is normal. The vanished-clone case must say
// so on stderr — silently fetching from GitHub while a marker points at a
// directory that no longer exists is an error rendered as absence. `pfm init`
// (init_command.go) already names the same failure.
func TestInstallOptionsNameAnUnusableRecordedCloneBeforeFallingBack(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("PFM_SOURCE_REPO", "")
	t.Chdir(t.TempDir()) // no repo markers here — discoverSourceRepo() finds nothing
	home := t.TempDir()
	vanished := filepath.Join(t.TempDir(), "clone")
	if err := os.MkdirAll(vanished, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, vanished); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(vanished); err != nil {
		t.Fatal(err)
	}
	runtime := commandRuntime{Paths: paths.Values{Home: home}}
	var stderr bytes.Buffer
	options := newInstallerOptions(installer.ModeDryRun, "", true, io.Discard, &stderr, runtime)
	if options.SourceRepo != "" {
		t.Fatalf("options.SourceRepo = %q, want empty for a clone that is gone", options.SourceRepo)
	}
	if !strings.Contains(stderr.String(), "source repository") || !strings.Contains(stderr.String(), vanished) {
		t.Fatalf("stderr = %q, want the unusable recorded clone %q named", stderr.String(), vanished)
	}

	// The control arm: a home that never recorded a clone is the ordinary
	// first install, and must stay silent.
	var quiet bytes.Buffer
	fresh := commandRuntime{Paths: paths.Values{Home: t.TempDir()}}
	if options := newInstallerOptions(
		installer.ModeDryRun, "", true, io.Discard, &quiet, fresh,
	); options.SourceRepo != "" || quiet.Len() != 0 {
		t.Fatalf("a home with no marker reported %q on stderr (SourceRepo=%q)", quiet.String(), options.SourceRepo)
	}
}

// writeManagerFakes stages fake systemctl and launchctl binaries in one PATH
// directory, each appending its own invocation to a shared log so a subtest
// can assert WHICH manager the sandbox actually reached instead of assuming
// a PATH miss silently degraded interception into "hopefully not found."
func writeManagerFakes(t *testing.T, systemctlBody, launchctlBody string) (binDir, logPath string) {
	t.Helper()
	binDir = t.TempDir()
	logPath = filepath.Join(t.TempDir(), "manager-calls.log")
	write := func(name, body string) {
		script := "#!/bin/sh\necho \"" + name + " $*\" >> " + logPath + "\n" + body
		if err := os.WriteFile(filepath.Join(binDir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write("systemctl", systemctlBody)
	write("launchctl", launchctlBody)
	return binDir, logPath
}

// assertManagerConsulted proves the sandbox intercepted the platform's real
// scheduler manager by design (schedulerIsLaunchd, scheduler_darwin.go /
// scheduler_other.go) rather than by a PATH lookup happening not to find the
// real binary: on Linux, only systemd may ever be consulted; on Darwin, only
// launchd may ever be consulted. wantDarwinConsulted covers the one case
// that differs across the two platforms — a dry-run preview reaches systemd
// for plan messaging on Linux (wireUnits' unconditional probe) but reaches
// no manager at all on Darwin (wireLaunchAgent never dials out unless
// applying).
func assertManagerConsulted(t *testing.T, logPath string, wantDarwinConsulted bool) {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read manager call log: %v", err)
	}
	log := string(data)
	if goRuntime.GOOS == "darwin" {
		if strings.Contains(log, "systemctl ") {
			t.Fatalf("manager log=%q, systemd must never be consulted on darwin", log)
		}
		if got := strings.Contains(log, "launchctl "); got != wantDarwinConsulted {
			t.Fatalf("manager log=%q, launchctl consulted=%v want=%v", log, got, wantDarwinConsulted)
		}
		return
	}
	if strings.Contains(log, "launchctl ") {
		t.Fatalf("manager log=%q, launchd must never be consulted off darwin", log)
	}
	if !strings.Contains(log, "systemctl ") {
		t.Fatalf("manager log=%q, want systemd consulted", log)
	}
}

func TestInstallGateScopesDryRunIdleAndRunningService(t *testing.T) {
	// The launchctl fake's "print" reply models launchAgentRunning's state
	// line (launchd.go:177-196); its exit code always succeeds like the
	// existing systemctl fakes below, so a subtest's only lever is the
	// reported state, never a manager it forgot to answer.
	const launchctlIdle = "case \"$1\" in\n  print) echo \"state = not running\" ;;\nesac\nexit 0\n"
	const launchctlRunning = "case \"$1\" in\n  print) echo \"state = running\" ;;\nesac\nexit 0\n"

	t.Run("bare preview ignores reachable manager", func(t *testing.T) {
		home := t.TempDir()
		binDir, logPath := writeManagerFakes(t, "exit 0\n", launchctlIdle)
		t.Setenv("HOME", home)
		t.Setenv("PATH", binDir)
		var stdout, stderr bytes.Buffer
		if code := runInstall(nil, &stdout, &stderr); code != 0 {
			t.Fatalf("preview code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
			t.Fatalf("preview wrote files: entries=%v err=%v", entries, err)
		}
		const confirmation = "if you agree, run again: pfm install --yes\n"
		if !strings.HasSuffix(stdout.String(), confirmation) || strings.Count(stdout.String(), confirmation) != 1 {
			t.Fatalf("preview confirmation=%q, want one final line %q", stdout.String(), confirmation)
		}
		assertManagerConsulted(t, logPath, false)
	})

	t.Run("idle reachable manager applies with yes", func(t *testing.T) {
		home := t.TempDir()
		script := "case \"$*\" in *ActiveState*) echo inactive;; esac\nexit 0\n"
		binDir, logPath := writeManagerFakes(t, script, launchctlIdle)
		t.Setenv("HOME", home)
		t.Setenv("PATH", binDir)
		var stdout, stderr bytes.Buffer
		if code := runInstall([]string{"--yes"}, &stdout, &stderr); code != 0 {
			t.Fatalf("idle yes code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		assertManagerConsulted(t, logPath, true)
	})

	t.Run("running service refuses actionably", func(t *testing.T) {
		home := t.TempDir()
		// A oneshot mid-run: `show` names it activating (is-active would exit 3).
		script := "case \"$*\" in *ActiveState*) echo activating;; esac\nexit 0\n"
		binDir, logPath := writeManagerFakes(t, script, launchctlRunning)
		t.Setenv("HOME", home)
		t.Setenv("PATH", binDir)

		var stdout, stderr bytes.Buffer
		if code := runInstall([]string{"--yes"}, &stdout, &stderr); code != 97 {
			t.Fatalf("runInstall() code=%d, want 97; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		remediation := "systemctl --user stop pfm-name-sync.service"
		if goRuntime.GOOS == "darwin" {
			remediation = "launchctl bootout"
		}
		if !strings.Contains(stderr.String(), remediation) {
			t.Fatalf("stderr=%q, want actionable running-service refusal", stderr.String())
		}
		entries, err := os.ReadDir(home)
		if err != nil || len(entries) != 0 {
			t.Fatalf("rc 97 refusal wrote files: entries=%v err=%v", entries, err)
		}
		assertManagerConsulted(t, logPath, true)
	})
}

func TestInstallUsesOnlyTheNewSurface(t *testing.T) {
	for _, retired := range []string{"-" + "-apply", "-" + "-uninstall", "-" + "-dry-run"} {
		t.Run(retired, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			var stdout, stderr bytes.Buffer
			if code := runInstall([]string{retired}, &stdout, &stderr); code != 2 {
				t.Fatalf(
					"runInstall(%q) code=%d stdout=%q stderr=%q, want unknown-flag usage",
					retired,
					code,
					stdout.String(),
					stderr.String(),
				)
			}
			if !strings.Contains(
				stderr.String(),
				"usage: pfm install [--yes] [--rollback ID] [--vscode] [--skip-harvest] [--skip-engine codex] [--skip-themes] [--config-dir DIR]",
			) {
				t.Fatalf("runInstall(%q) stderr=%q, want new usage", retired, stderr.String())
			}
		})
	}
}

func TestInstallRollbackUsesJournalAndRejectsMixedFlags(t *testing.T) {
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	runInstaller = func(_ context.Context, _ installer.Options) (installer.Report, error) {
		t.Fatal("rollback entered ordinary installer")
		return installer.Report{}, nil
	}
	home := t.TempDir()
	runtime := commandRuntime{
		Paths:  paths.Values{Home: home},
		Config: pfmconfig.Config{Path: filepath.Join(home, "pfm.config.json")},
	}
	id := "20260102T030405Z"
	dir := filepath.Join(home, ".local", "state", "pfm", "migrations", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "journal.json"), []byte("[]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--rollback", id, "--yes"}, {"--rollback", ""}} {
		var stdout, stderr bytes.Buffer
		if code := runInstall(args, &stdout, &stderr, runtime); code != 2 {
			t.Errorf("args=%v code=%d stderr=%s, want usage 2", args, code, stderr.String())
		}
	}
	var stdout, stderr bytes.Buffer
	if code := runInstall([]string{"--rollback", id}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("rollback code=%d stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("journal directory survived rollback: %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := runInstall([]string{"--rollback", id}, &stdout, &stderr, runtime); code != 2 ||
		!strings.Contains(stderr.String(), filepath.Join(home, ".local", "state", "pfm", "migrations")) {
		t.Fatalf("unknown rollback code=%d stderr=%s", code, stderr.String())
	}
}

func TestInstallRejectsRetiredForceFlag(t *testing.T) {
	runtime := commandRuntime{Paths: paths.Values{Home: t.TempDir()}}
	var stdout, stderr bytes.Buffer
	if code := runInstall([]string{"--force", "--skip-harvest"}, &stdout, &stderr, runtime); code != 2 {
		t.Fatalf(
			"retired --force code=%d stdout=%q stderr=%q, want unknown-flag usage",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	if strings.Contains(stderr.String(), "[--force]") {
		t.Fatalf("install usage still advertises retired --force: %q", stderr.String())
	}
}

func TestInstallCarriesExplicitVSCodeTerminalOptIn(t *testing.T) {
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	var captured installer.Options
	runInstaller = func(_ context.Context, options installer.Options) (installer.Report, error) {
		captured = options
		return installer.Report{}, nil
	}
	runtime := commandRuntime{Paths: paths.Values{Home: t.TempDir()}}
	var stdout, stderr bytes.Buffer
	if code := runInstall([]string{"--vscode", "--skip-harvest"}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("pfm install --vscode code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !captured.VSCode {
		t.Fatal("pfm install --vscode did not reach installer.Options")
	}
	if !strings.HasSuffix(stdout.String(), "if you agree, run again: pfm install --yes --vscode --skip-harvest\n") {
		t.Fatalf("VS Code preview dropped its apply flag: %q", stdout.String())
	}
}

func TestInstallPreviewAndYesUseTheSameInstallerClassification(t *testing.T) {
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	var calls []installer.Options
	runInstaller = func(_ context.Context, options installer.Options) (installer.Report, error) {
		calls = append(calls, options)
		return installer.Report{}, nil
	}
	runtime := commandRuntime{Paths: paths.Values{Home: t.TempDir()}}
	configDir := filepath.Join(t.TempDir(), "config")
	var previewOut, previewErr bytes.Buffer
	if code := runInstall([]string{"--config-dir", configDir}, &previewOut, &previewErr, runtime); code != 0 {
		t.Fatalf("preview code=%d stdout=%q stderr=%q", code, previewOut.String(), previewErr.String())
	}
	var applyOut, applyErr bytes.Buffer
	if code := runInstall([]string{"--yes", "--config-dir", configDir}, &applyOut, &applyErr, runtime); code != 0 {
		t.Fatalf("yes code=%d stdout=%q stderr=%q", code, applyOut.String(), applyErr.String())
	}
	if len(calls) != 2 {
		t.Fatalf("installer calls=%d, want preview and yes", len(calls))
	}
	if calls[0].Mode != installer.ModeDryRun || calls[1].Mode != installer.ModeApply {
		t.Fatalf("installer modes=%v/%v, want dry-run/apply", calls[0].Mode, calls[1].Mode)
	}
	preview, apply := calls[0], calls[1]
	preview.Mode = installer.ModeApply
	preview.Stdout = nil
	apply.Stdout = nil
	if !reflect.DeepEqual(preview, apply) {
		t.Fatalf("preview and yes options classify differently:\npreview=%#v\nyes=%#v", preview, apply)
	}
	if got := previewOut.String(); !strings.HasSuffix(got, "if you agree, run again: pfm install --yes\n") {
		t.Fatalf("preview output=%q, want exact confirmation suffix", got)
	}
	if strings.Contains(applyOut.String(), "if you agree, run again:") {
		t.Fatalf("yes output unexpectedly contains preview confirmation: %q", applyOut.String())
	}
}

func TestInstallSkipHarvestDisablesProvisioningButDefaultFailureStaysLoud(t *testing.T) {
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	runInstaller = func(_ context.Context, options installer.Options) (installer.Report, error) {
		if options.ProvisionHarvest {
			return installer.Report{}, errors.New("harvest fixture failed")
		}
		return installer.Report{}, nil
	}
	runtime := commandRuntime{Paths: paths.Values{Home: t.TempDir()}}

	var stdout, stderr bytes.Buffer
	if code := runInstall([]string{"--yes"}, &stdout, &stderr, runtime); code != 1 {
		t.Fatalf("default apply code=%d stdout=%q stderr=%q, want loud failure", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "harvest fixture failed") {
		t.Fatalf("default apply stderr=%q, want provisioning failure", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runInstall([]string{"--yes", "--skip-harvest"}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("skip apply code=%d stdout=%q stderr=%q, want success", code, stdout.String(), stderr.String())
	}
}

func TestInstallSkipHarvestPreviewPreservesTheConfirmationFlag(t *testing.T) {
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	runInstaller = func(_ context.Context, options installer.Options) (installer.Report, error) {
		if options.ProvisionHarvest {
			t.Fatal("skip preview enabled harvest provisioning")
		}
		return installer.Report{}, nil
	}
	runtime := commandRuntime{Paths: paths.Values{Home: t.TempDir()}}
	var stdout, stderr bytes.Buffer
	if code := runInstall([]string{"--skip-harvest"}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("skip preview code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.HasSuffix(stdout.String(), "if you agree, run again: pfm install --yes --skip-harvest\n") {
		t.Fatalf("skip preview output=%q, want preserved skip confirmation", stdout.String())
	}
}

func TestInstallSkipEngineCodexReachesProbeInstallerAndConfirmation(t *testing.T) {
	savedProbe, savedInstaller := doctor.DependencyProbeOverride, runInstaller
	t.Cleanup(func() {
		doctor.DependencyProbeOverride = savedProbe
		runInstaller = savedInstaller
	})
	doctor.DependencyProbeOverride = func(_ context.Context, entries []deps.Entry, options deps.ProbeOptions) []deps.Result {
		if !options.SkipEngines[pfmengine.Codex] {
			t.Fatal("--skip-engine codex did not reach dependency probe options")
		}
		for _, entry := range entries {
			if entry.Name == "codex" {
				return []deps.Result{{Entry: entry, State: deps.StateSkipped, Error: "--skip-engine codex"}}
			}
		}
		t.Fatal("codex registry entry missing")
		return nil
	}
	var captured installer.Options
	runInstaller = func(_ context.Context, options installer.Options) (installer.Report, error) {
		captured = options
		return installer.Report{}, nil
	}
	home := t.TempDir()
	runtime := commandRuntime{
		Paths:  paths.Values{Home: home},
		Config: pfmconfig.Config{CodexAccounts: []pfmconfig.CodexAccount{{ID: 1, Home: filepath.Join(home, ".codex")}}},
	}
	var stdout, stderr bytes.Buffer
	if code := runInstall([]string{"--skip-harvest", "--skip-engine", "codex"}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("skip-engine preview code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if captured.CodexHomes == nil || len(captured.CodexHomes) != 0 {
		t.Fatalf("CodexHomes=%q, want an explicit empty roster", captured.CodexHomes)
	}
	if !strings.Contains(stdout.String(), "skipped (--skip-engine codex)") ||
		!strings.HasSuffix(
			stdout.String(),
			"if you agree, run again: pfm install --yes --skip-harvest --skip-engine codex\n",
		) {
		t.Fatalf("skip-engine output=%q", stdout.String())
	}
}

func TestInstallSkipThemesDisablesFetchAndPreservesConfirmation(t *testing.T) {
	saved := runInstaller
	t.Cleanup(func() { runInstaller = saved })
	var captured installer.Options
	runInstaller = func(_ context.Context, options installer.Options) (installer.Report, error) {
		captured = options
		return installer.Report{}, nil
	}
	runtime := commandRuntime{Paths: paths.Values{Home: t.TempDir()}}
	var stdout, stderr bytes.Buffer
	if code := runInstall([]string{"--skip-harvest", "--skip-themes"}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("skip-themes preview code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if captured.InstallThemes {
		t.Fatal("--skip-themes left theme installation enabled")
	}
	if !strings.HasSuffix(
		stdout.String(),
		"if you agree, run again: pfm install --yes --skip-harvest --skip-themes\n",
	) {
		t.Fatalf("skip-themes confirmation=%q", stdout.String())
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
	loaded, err := pfmconfig.Load("", home, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime := commandRuntime{Paths: paths.Values{Home: home}, Config: loaded}
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

// TestInstallApplyRefusesAnExplicitConfigThatDoesNotExist is a REGRESSION
// test for issue #24 findings 3/4's guard (M3 change B): an explicit
// --config path that does not exist loads as silent defaults (config.go
// Load), and converging host wiring on those defaults would boot out and
// delete every MCP service the missing file actually enabled — exactly what
// stranded the launch agent after a rollback across the v0.74.0 config
// migration. Apply refuses outright; preview names the skip and continues.
// Unfixed: apply proceeds on defaults with no refusal.
func TestInstallApplyRefusesAnExplicitConfigThatDoesNotExist(t *testing.T) {
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	installerRan := false
	runInstaller = func(_ context.Context, _ installer.Options) (installer.Report, error) {
		installerRan = true
		return installer.Report{}, nil
	}
	home := t.TempDir()
	absent := filepath.Join(home, "missing-config.json")
	explicitRuntime := commandRuntime{
		Paths:          paths.Values{Home: home},
		Config:         pfmconfig.Config{Path: absent, Exists: false},
		ConfigExplicit: true,
	}

	var applyStdout, applyStderr bytes.Buffer
	if code := runInstall([]string{"--yes", "--skip-harvest"}, &applyStdout, &applyStderr, explicitRuntime); code != 1 {
		t.Fatalf(
			"runInstall(apply, missing --config) code=%d stdout=%q stderr=%q, want refusal",
			code,
			applyStdout.String(),
			applyStderr.String(),
		)
	}
	if !strings.Contains(applyStderr.String(), absent+" does not exist; refusing to converge host wiring on defaults") {
		t.Fatalf("runInstall(apply) stderr=%q, want the refusal naming the missing path", applyStderr.String())
	}
	if installerRan {
		t.Fatal("runInstall(apply, missing --config) ran the installer despite the refusal")
	}

	var previewStdout, previewStderr bytes.Buffer
	if code := runInstall([]string{"--skip-harvest"}, &previewStdout, &previewStderr, explicitRuntime); code != 0 {
		t.Fatalf(
			"runInstall(preview, missing --config) code=%d stdout=%q stderr=%q, want 0",
			code,
			previewStdout.String(),
			previewStderr.String(),
		)
	}
	if !strings.Contains(previewStdout.String(), "skip") ||
		!strings.Contains(previewStdout.String(), absent+" does not exist") {
		t.Fatalf("runInstall(preview) stdout=%q, want a skip line naming the missing path", previewStdout.String())
	}
}

func TestInstallApplyAcceptsMissingDefaultConfigNamedByFlag(t *testing.T) {
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	installerRan := false
	runInstaller = func(_ context.Context, _ installer.Options) (installer.Report, error) {
		installerRan = true
		return installer.Report{}, nil
	}
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	t.Setenv(paths.EnvConfig, "")
	clone := filepath.Join(home, "clone")
	if err := os.MkdirAll(clone, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	defaultPath, err := pfmconfig.ResolvePath(home)
	if err != nil {
		t.Fatalf("ResolvePath(%q) = %v", home, err)
	}
	if _, err := os.Stat(defaultPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("default config %q must be absent, stat error = %v", defaultPath, err)
	}

	var stdout, stderr bytes.Buffer
	if code := run(
		[]string{"--config", defaultPath, "install", "--yes", "--skip-harvest"},
		&stdout,
		&stderr,
	); code != 0 {
		t.Fatalf(
			"pfm --config %q install --yes code=%d stdout=%q stderr=%q, want install on defaults",
			defaultPath,
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	if !installerRan {
		t.Fatal("pfm install --yes did not run the installer for the absent default config")
	}
}

// TestOlderUpdaterInstallArgvOnTheRealBinaryDoesNotRefuse pins a4d89776
// across the process boundary (pfm-update-1#NEW-F3): an older `pfm update`
// ran its candidate as `pfm --config <default path> install --yes` whether or
// not that file existed, and a candidate that refused the missing default
// exited non-zero, which that updater reads as a failed install and rolls
// back. The older updater is a released binary, not this tree's code, so the
// closest real test runs its exact argv against this tree's real built
// binary (TestMain's testPFMBinary): a real process, real config load, real
// installer, in a jailed HOME with no config file. The updater half — a
// non-zero candidate exit is what triggers rollback — is update.Run's own
// rollback tests.
func TestOlderUpdaterInstallArgvOnTheRealBinaryDoesNotRefuse(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	env := replaceAttachEnv(os.Environ(), map[string]string{
		"HOME":             home,
		paths.EnvHome:      home,
		"XDG_CONFIG_HOME":  filepath.Join(home, ".config"),
		"XDG_DATA_HOME":    filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME":   filepath.Join(home, ".local", "state"),
		"XDG_CACHE_HOME":   filepath.Join(home, ".cache"),
		paths.EnvConfig:    "",
		paths.EnvStateDB:   filepath.Join(root, "pfm.db"),
		paths.EnvCacheDB:   filepath.Join(root, "pfm-cache.db"),
		"PFM_CLAUDE_ROOTS": filepath.Join(home, ".claude", "projects"),
		"PFM_CODEX_ROOT":   filepath.Join(home, ".codex"),
		"TMUX":             "",
	})
	defaultPath := filepath.Join(home, ".config", "pfm", pfmconfig.FileName)
	if _, err := os.Stat(defaultPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("default config %q must be absent, stat error = %v", defaultPath, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(
		ctx, testPFMBinary, "--config", defaultPath, "install", "--yes", "--skip-harvest",
	)
	command.Env = env
	command.Dir = home
	command.Stdin = nil
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("older updater argv `pfm --config %s install --yes --skip-harvest` on the real binary: %v\n%s",
			defaultPath, err, output)
	}
	if strings.Contains(string(output), "refusing to converge host wiring on defaults") {
		t.Fatalf("the real install refused the absent default config:\n%s", output)
	}
}

func TestInstallMigratesMovedDatabaseSchemas(t *testing.T) {
	home := t.TempDir()
	statePath := filepath.Join(home, ".local", "state", "pfm", "pfm.db")
	cachePath := filepath.Join(home, ".local", "state", "pfm", "pfm-cache.db")
	t.Setenv("HOME", home)
	t.Setenv("PFM_HOME", home)
	t.Setenv("PFM_STATE_DB", statePath)
	t.Setenv("PFM_CACHE_DB", cachePath)
	fresh, err := store.OpenContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := fresh.Close(); err != nil {
		t.Fatal(err)
	}
	state, err := sqlitedb.OpenStore(context.Background(), statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.Exec(
		`PRAGMA user_version=1; CREATE TABLE swap_event(id INTEGER PRIMARY KEY); INSERT INTO swap_event VALUES(1)`,
	); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	cache, err := sqlitedb.OpenStore(context.Background(), cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cache.Exec(
		`CREATE TABLE hidden(id TEXT PRIMARY KEY, engine TEXT NOT NULL, hidden_at INTEGER NOT NULL, baseline_prompts INTEGER); INSERT INTO hidden(id,engine,hidden_at) VALUES('cached','cc',88); UPDATE meta SET value='0' WHERE key='shared_hidden_adopted'; PRAGMA user_version=8`,
	); err != nil {
		t.Fatal(err)
	}
	if err := cache.Close(); err != nil {
		t.Fatal(err)
	}
	if err := migrateInstalledLayoutDatabases(context.Background(), statePath, cachePath); err != nil {
		t.Fatal(err)
	}
	state, err = sqlitedb.OpenReadOnly(statePath, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := state.Close(); err != nil {
			t.Errorf("close shared database: %v", err)
		}
	}()
	cache, err = sqlitedb.OpenReadOnly(cachePath, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := cache.Close(); err != nil {
			t.Errorf("close cache database: %v", err)
		}
	}()
	for _, check := range []struct {
		db    interface{ QueryRow(string, ...any) *sql.Row }
		query string
		want  int
	}{
		{state, "PRAGMA user_version", 2},
		{cache, "PRAGMA user_version", 9},
		{state, "SELECT count(*) FROM sqlite_master WHERE name='swap_event'", 0},
		{cache, "SELECT count(*) FROM sqlite_master WHERE name='hidden'", 0},
		{state, "SELECT count(*) FROM hidden WHERE uuid='cached'", 1},
	} {
		var got int
		if err := check.db.QueryRow(check.query).Scan(&got); err != nil || got != check.want {
			t.Fatalf("%s = %d, %v; want %d", check.query, got, err, check.want)
		}
	}
	wrongCache := filepath.Join(home, "wrong-cache.db")
	t.Setenv("PFM_CACHE_DB", wrongCache)
	if err := migrateInstalledLayoutDatabases(context.Background(), statePath, cachePath); err == nil ||
		!strings.Contains(err.Error(), "moved database paths differ") {
		t.Fatalf("mismatched paths error=%v", err)
	}
	if _, err := os.Lstat(wrongCache); !os.IsNotExist(err) {
		t.Fatalf("mismatched cache was touched: %v", err)
	}
}

func TestUninstallVerbAcceptsConfigDirAndUsesUninstallMode(t *testing.T) {
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	var got installer.Options
	runInstaller = func(_ context.Context, options installer.Options) (installer.Report, error) {
		got = options
		return installer.Report{}, nil
	}
	configDir := filepath.Join(t.TempDir(), "config")
	runtime := commandRuntime{Paths: paths.Values{Home: t.TempDir()}}
	var stdout, stderr bytes.Buffer
	if code := runUninstall([]string{"--config-dir", configDir}, &stdout, &stderr, runtime); code != 0 {
		t.Fatalf("uninstall code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got.Mode != installer.ModeUninstall || got.ConfigDir != configDir || got.Home != runtime.Paths.Home {
		t.Fatalf("uninstall options=%#v, want mode uninstall, config %q, home %q", got, configDir, runtime.Paths.Home)
	}
}

func TestRootHelpListsUninstall(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("help code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "  uninstall") {
		t.Fatalf("help=%q, want top-level uninstall", stdout.String())
	}
}

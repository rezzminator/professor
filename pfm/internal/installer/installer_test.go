package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/fleetdb"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/reload"
)

type fakeRunner struct {
	manager        bool
	nameSyncActive bool
	// nameSyncIdle makes the is-active probe for pfm-name-sync.service run to
	// completion and report the service inactive via a genuine
	// *exec.ExitError — the only shape nameSyncServiceRunning's classifier
	// accepts as "probed, and idle" rather than "the probe never answered".
	// Leaving both this and nameSyncActive false models a probe that could
	// not run at all (systemctl missing, dead bus, permission denied), via a
	// plain error that does NOT satisfy errors.As(*exec.ExitError).
	nameSyncIdle bool
	calls        []string
}

func (runner *fakeRunner) Run(_ context.Context, name string, args ...string) error {
	call := name + " " + strings.Join(args, " ")
	runner.calls = append(runner.calls, call)
	if call == "systemctl --user is-active --quiet pfm-name-sync.service" {
		if runner.nameSyncActive {
			return nil
		}
		if runner.nameSyncIdle {
			return genuineExitError()
		}
	}
	if call == "systemctl --user show-environment" && runner.manager {
		return nil
	}
	if strings.Contains(call, "is-active") || strings.Contains(call, "is-enabled") ||
		strings.Contains(call, "is-failed") {
		return errors.New("not loaded")
	}
	if runner.manager || name == "launchctl" {
		return nil
	}
	return errors.New("dead user bus")
}

// genuineExitError returns a real *exec.ExitError from a command that
// actually ran and exited non-zero — the shape `systemctl is-active` takes
// when it genuinely reports "inactive", as opposed to never having run at
// all. A hand-rolled type that merely satisfies errors.As(*exec.ExitError)
// would defeat the point of the distinction nameSyncServiceRunning draws, so
// fakeRunner earns one for real instead of faking the type.
func genuineExitError() error {
	err := exec.Command("sh", "-c", "exit 3").Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		panic(fmt.Sprintf("fake idle probe did not produce a genuine *exec.ExitError: %v (%T)", err, err))
	}
	return exitErr
}

// TestInstallPreviewListsPrunableVersionsAndApplyRemovesOnlyThem is C: the
// prune sibling to wireClaudeLauncher, destructive-defaults-to-preview per
// pfm/CLAUDE.md, and the preview IS the apply's own preview — same
// classification with and without --apply, only the action differs. Four
// versions: the newest two are protected by the keep window, a third is
// protected because a live pid is executing it despite being outside that
// window, and the fourth is the only one either run may remove.
func TestInstallPreviewListsPrunableVersionsAndApplyRemovesOnlyThem(t *testing.T) {
	home := t.TempDir()
	versions := filepath.Join(home, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(versions, 0o700); err != nil {
		t.Fatal(err)
	}
	newest := filepath.Join(versions, "2.1.270")
	second := filepath.Join(versions, "2.1.269")
	live := filepath.Join(versions, "2.1.260")
	prunable := filepath.Join(versions, "2.1.250")
	for _, path := range []string{newest, second, live, prunable} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	procRoot := filepath.Join(t.TempDir(), "proc")
	if err := os.MkdirAll(filepath.Join(procRoot, "4242"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(live, filepath.Join(procRoot, "4242", "exe")); err != nil {
		t.Fatal(err)
	}
	// Candidate scoping (ProbeLiveClaudeVersions) reads argv[0] before ever
	// calling Image, so the fixture needs a cmdline record naming the live
	// build — the same file a real /proc/<pid>/cmdline is.
	if err := os.WriteFile(filepath.Join(procRoot, "4242", "cmdline"), []byte(live+"\x00"), 0o600); err != nil {
		t.Fatal(err)
	}

	var preview bytes.Buffer
	if _, err := Run(context.Background(), Options{
		Mode: ModeDryRun, Home: home, Runner: &fakeRunner{}, ProcRoot: procRoot, Stdout: &preview,
	}); err != nil {
		t.Fatal(err)
	}
	previewOutput := preview.String()
	if !strings.Contains(previewOutput, "remove "+prunable) {
		t.Fatalf("preview did not list the prunable version:\n%s", previewOutput)
	}
	if !strings.Contains(previewOutput, "keep "+live+" (live (pids 4242))") {
		t.Fatalf("preview did not name the live version kept, with its pids:\n%s", previewOutput)
	}
	// issue #24 F7: only the actual newest build is labelled "newest" — the
	// second-newest kept build carries a distinct, honest label.
	if !strings.Contains(previewOutput, "keep "+newest+" (newest)") ||
		!strings.Contains(previewOutput, "keep "+second+" (within keep window)") {
		t.Fatalf("preview did not name the two newest versions kept, one honestly labelled second:\n%s", previewOutput)
	}
	for _, path := range []string{newest, second, live, prunable} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("preview removed %s: %v", path, err)
		}
	}

	var apply bytes.Buffer
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, ProcRoot: procRoot, Stdout: &apply,
	}); err != nil {
		t.Fatal(err)
	}
	applyOutput := apply.String()
	if !strings.Contains(applyOutput, "remove "+prunable) {
		t.Fatalf("apply did not report the removal:\n%s", applyOutput)
	}
	if !strings.Contains(applyOutput, "keep "+live+" (live (pids 4242))") {
		t.Fatalf("apply did not name the live version kept:\n%s", applyOutput)
	}
	if _, err := os.Stat(prunable); !os.IsNotExist(err) {
		t.Fatalf("apply left the prunable version in place: err=%v", err)
	}
	for _, path := range []string{newest, second, live} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("apply removed a protected version %s: %v", path, err)
		}
	}
}

func TestDryRunNeverGatesOnAReachableUserManager(t *testing.T) {
	home := t.TempDir()
	runner := &fakeRunner{manager: true, nameSyncActive: true}
	report, err := Run(context.Background(), Options{
		Mode: ModeDryRun, Home: home, Runner: runner,
	})
	if err != nil || report.Changed == 0 {
		t.Fatalf("dry run report=%#v err=%v, want an ungated preview", report, err)
	}
	for _, call := range runner.calls {
		if call == "systemctl --user is-active --quiet pfm-name-sync.service" {
			t.Fatalf("dry run called the running-service gate: %v", runner.calls)
		}
	}
	if entries, readErr := os.ReadDir(home); readErr != nil || len(entries) != 0 {
		t.Fatalf("dry run wrote files: entries=%v err=%v", entries, readErr)
	}
}

func TestDryRunNamesUpdateMetadataWithoutWritingIt(t *testing.T) {
	home := t.TempDir()
	source := t.TempDir()
	var output bytes.Buffer
	report, err := Run(context.Background(), Options{
		Mode: ModeDryRun, Home: home, SourceRepo: source,
		Stdout: &output, Runner: &fakeRunner{},
	})
	if err != nil || report.Changed == 0 {
		t.Fatalf("dry run report=%#v err=%v\n%s", report, err, output.String())
	}
	for _, path := range []string{SourceRepoPath(home), binaryOwnershipPath(home)} {
		if !strings.Contains(output.String(), "write "+path) {
			t.Errorf("dry run omitted update-metadata path %s:\n%s", path, output.String())
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("dry run wrote update metadata %s: %v", path, err)
		}
	}
}

// TestReachableIdleUserManagerAllowsMutatingModes is behavior (b): the
// systemctl is-active probe ran to completion and genuinely reported the
// service inactive (a real *exec.ExitError, via fakeRunner's nameSyncIdle).
// That is the proceed-silently case: install must not refuse, and — unlike
// the probe-could-not-run case — must never claim the gate was unprobed.
func TestReachableIdleUserManagerAllowsMutatingModes(t *testing.T) {
	for _, mode := range []Mode{ModeApply, ModeUninstall} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			home := t.TempDir()
			var output bytes.Buffer
			_, err := Run(context.Background(), Options{
				Mode:   mode,
				Home:   home,
				Stdout: &output,
				Runner: &outputRunner{
					fakeRunner:  fakeRunner{manager: true, nameSyncIdle: true},
					printOutput: "state = not running\n",
				},
			})
			if err != nil {
				t.Fatalf("mode %d refused an idle reachable manager: %v\n%s", mode, err, output.String())
			}
			if strings.Contains(output.String(), "gate NOT probed") {
				t.Fatalf(
					"mode %d claimed the gate was unprobed for a genuinely idle service:\n%s",
					mode,
					output.String(),
				)
			}
		})
	}
}

// TestUnprobedNameSyncGateProceedsButSaysSo is behavior (c): the systemctl
// is-active probe never got an answer at all — modeled by fakeRunner's
// default is-active response, a plain error that is NOT an *exec.ExitError
// (as would come from systemctl missing, a dead user bus, or permission
// denied). Install must still proceed (this gate only ever refuses a
// CONFIRMED running service), but it must say the gate was not probed rather
// than silently reading that ambiguity as safe — the entire point of the fix.
func TestUnprobedNameSyncGateProceedsButSaysSo(t *testing.T) {
	if schedulerIsLaunchd {
		t.Skip("the systemd name-sync gate is Linux-only")
	}
	home := t.TempDir()
	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Stdout: &output, Runner: &fakeRunner{},
	})
	if err != nil {
		t.Fatalf("an unprobed gate refused the install: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "name-sync gate NOT probed") {
		t.Fatalf("an unprobed name-sync gate was silent:\n%s", output.String())
	}
}

func TestRunningNameSyncRefusesMutatingModesBeforeWriting(t *testing.T) {
	for _, mode := range []Mode{ModeApply, ModeUninstall} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			home := t.TempDir()
			var runner CommandRunner = &fakeRunner{nameSyncActive: true}
			expected := ErrNameSyncRunning
			if schedulerIsLaunchd {
				runner = &outputRunner{printOutput: "state = running\n"}
				expected = ErrLaunchAgentRunning
			}
			_, err := Run(context.Background(), Options{
				Mode: mode, Home: home, Runner: runner,
			})
			if !errors.Is(err, expected) {
				t.Fatalf("Run() error = %v, want %v", err, expected)
			}
			if entries, readErr := os.ReadDir(home); readErr != nil || len(entries) != 0 {
				t.Fatalf("running-service refusal wrote files: entries=%v err=%v", entries, readErr)
			}
		})
	}
}

// TestChangeDescriptionNamesCreateVsBackedUpRewrite is a direct pin on the
// #9 helper: installer.change must report "create" for a target that had
// nothing to back up and "rewrite ... (backup preserved)" only when a
// backup is actually written. Callers pass the SAME existed value that
// gates the backup, so this contract is the whole guarantee.
func TestChangeDescriptionNamesCreateVsBackedUpRewrite(t *testing.T) {
	for _, test := range []struct {
		name    string
		existed bool
		want    string
	}{
		{"nothing existed to back up", false, "create /fixture/path"},
		{"a prior file is backed up", true, "rewrite /fixture/path (backup preserved)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := changeDescription("/fixture/path", test.existed); got != test.want {
				t.Fatalf("changeDescription(%q, %v) = %q, want %q", "/fixture/path", test.existed, got, test.want)
			}
		})
	}
}

func TestZshrcCreateOnFreshHomeNamesItselfHonestly(t *testing.T) {
	home := t.TempDir()
	var applied bytes.Buffer
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: &applied,
	}); err != nil {
		t.Fatalf("apply on a fresh home: %v\n%s", err, applied.String())
	}
	out := applied.String()
	zshrc := filepath.Join(home, ".zshrc")
	if !strings.Contains(out, "create "+zshrc) {
		t.Fatalf("apply output never says it created %s:\n%s", zshrc, out)
	}
	if strings.Contains(out, "rewrite "+zshrc+" (backup preserved)") {
		t.Fatalf("claimed a backed-up rewrite for an absent zshrc:\n%s", out)
	}
	if matches, _ := filepath.Glob(zshrc + ".pre-professor-*"); len(matches) != 0 {
		t.Fatalf("backup written for a file that did not exist: %v", matches)
	}
	if content := readFixture(t, zshrc); !strings.Contains(content, "pfm.zsh") {
		t.Fatalf("zshrc was not created with the source line:\n%s", content)
	}
}

func TestApplyIsSelfContainedIdempotentAndReversible(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	writeFixture(t, filepath.Join(home, ".codex", "hooks.json"), `{
  "hooks": {
    "SubagentStart": [{"matcher":"","hooks":[
      {"type":"command","command":"`+home+`/.local/bin/cc-fleet dream hook codex-subagent-inject"},
      {"type":"command","command":"fixture-codex-keep"}
    ]}]
  }
}`)
	writeFixture(t, filepath.Join(config, "settings.json"), `{
  "statusLine":{"type":"command","command":"bash ~/.claude/statusline-command.sh"},
  "hooks":{
    "PreToolUse":[{"matcher":"Agent","hooks":[{"type":"command","command":"`+home+`/.local/bin/cc-fleet dream hook agent-inject"}]}],
    "UserPromptSubmit":[
      {"matcher":"","hooks":[{"type":"command","command":"bash ~/.claude/bin/bb-hook.sh"}]},
      {"matcher":"","hooks":[{"type":"command","command":"bash /fixture/cc-usage-hook.sh"}]}
    ]
  }
}`)
	secondarySettings := filepath.Join(home, ".cc", "2", "settings.json")
	writeFixture(
		t,
		secondarySettings,
		`{"hooks":{"UserPromptSubmit":[{"matcher":"","hooks":[{"type":"command","command":"pfm chat bb"},{"type":"command","command":"secondary-keep"}]}]}}`,
	)
	writeFixture(t, filepath.Join(config, ".cc-ls-hidden"), "killed-b\nkilled-a\n")
	writeFixture(t, filepath.Join(config, "bin", "cc-kill.sh"), "retired\n")
	writeFixture(t, filepath.Join(home, ".zshrc"), "alias keep=yes\nsource /old/cc-fleet.zsh\n")
	unitDir := filepath.Join(home, ".config", "systemd", "user")
	legacyPathWant := filepath.Join(unitDir, "default.target.wants", "cc-name-sync.path")
	legacyTimerWant := filepath.Join(unitDir, "timers.target.wants", "cc-name-sync.timer")
	for _, target := range []string{legacyPathWant, legacyTimerWant} {
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join("..", filepath.Base(target)), target); err != nil {
			t.Fatal(err)
		}
	}
	bbTarget := filepath.Join(config, "commands", "bb.md")
	writeFixture(t, bbTarget, "operator copy\n")
	seed := fleetdb.OpenSharedState(context.Background(), paths.Values{
		Home: home, FleetDB: filepath.Join(home, ".cc", "fleet.db"),
	})
	if err := seed.Kill(context.Background(), "killed-a", 99); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{}
	now := func() time.Time { return time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC) }
	var preview bytes.Buffer
	previewReport, err := Run(context.Background(), Options{
		Mode: ModeDryRun, Home: home, Now: now, Stdout: &preview, Runner: runner,
		ConfigDirs: []string{config, filepath.Join(home, ".cc", "2")},
	})
	if err != nil || previewReport.Changed == 0 {
		t.Fatalf("dry run report=%#v err=%v", previewReport, err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".local", "share", "pfm", "install")); !os.IsNotExist(err) {
		t.Fatalf("dry run staged assets: %v", err)
	}
	if content := readFixture(t, bbTarget); content != "operator copy\n" {
		t.Fatalf("dry run changed bb.md: %q", content)
	}

	var applied bytes.Buffer
	report, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Now: now, Stdout: &applied, Runner: runner,
		ConfigDirs: []string{config, filepath.Join(home, ".cc", "2")},
	})
	if err != nil || report.Changed == 0 {
		t.Fatalf("apply report=%#v err=%v\n%s", report, err, applied.String())
	}
	managed := filepath.Join(home, ".local", "share", "pfm", "install")
	if content := readFixture(t, bbTarget); content != "operator copy\n" {
		t.Fatalf("install changed operator bb.md: %q", content)
	}
	assertLink(t, filepath.Join(config, "commands", "reload.md"), filepath.Join(managed, "reload.command.md"))
	// T3: the staged /reload command's frontmatter description opens with
	// the USER-ONLY law and then carries reload.Usage itself, folded to one
	// line — never the unrendered {{RELOAD_USAGE}} token — so the picker
	// shows the human exactly the flags reload.Run accepts.
	reloadMD := readFixture(t, filepath.Join(config, "commands", "reload.md"))
	if strings.Contains(reloadMD, "{{RELOAD_USAGE}}") {
		t.Fatalf("reload command asset kept its unrendered token:\n%s", reloadMD)
	}
	descriptionLine := ""
	for _, line := range strings.Split(reloadMD, "\n") {
		if strings.HasPrefix(line, "description: '") {
			descriptionLine = line
			break
		}
	}
	if descriptionLine == "" {
		t.Fatalf("reload command asset has no frontmatter description line:\n%s", reloadMD)
	}
	gotDescription := strings.ReplaceAll(
		strings.TrimSuffix(strings.TrimPrefix(descriptionLine, "description: '"), "'"),
		"''", "'",
	)
	wantDescription := "USER-ONLY — the user types /reload; never run this without the user's permission. " +
		foldReloadUsage(reload.Usage)
	if gotDescription != wantDescription {
		t.Fatalf(
			"reload description = %q, want the USER-ONLY law plus the folded usage line %q",
			gotDescription,
			wantDescription,
		)
	}
	// T4: /handoff is a global skill, linked the same way /reload is a
	// global command.
	assertLink(t, filepath.Join(config, "skills", "handoff", "SKILL.md"), filepath.Join(managed, "handoff.skill.md"))
	if _, err := os.Lstat(filepath.Join(config, "commands", "chat", "group", "send.md")); !os.IsNotExist(err) {
		t.Fatalf("install wired a retired /chat: command link: %v", err)
	}
	// One job, two schedulers: assert the one this platform actually installs,
	// and that it did NOT leave the other platform's files behind.
	if schedulerIsLaunchd {
		agent := filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
		info, err := os.Lstat(agent)
		if err != nil {
			t.Fatalf("launch agent was not installed: %v", err)
		}
		// launchd silently ignores a symlinked agent, so this must be a real file.
		if info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("launch agent is a symlink; launchd will never load it")
		}
		plist := readFixture(t, agent)
		if strings.Contains(plist, "__PFM_HOME__") {
			t.Fatalf("launch agent kept its placeholder:\n%s", plist)
		}
		if !strings.Contains(plist, home+"/.local/bin/pfm") ||
			!strings.Contains(plist, home+"/.codex/session_index.jsonl") {
			t.Fatalf("launch agent does not point at this home:\n%s", plist)
		}
		if _, err := os.Lstat(filepath.Join(managed, "systemd")); !os.IsNotExist(err) {
			t.Fatalf("systemd units were staged on a launchd host: %v", err)
		}
	} else {
		assertLink(
			t,
			filepath.Join(home, ".config", "systemd", "user", "pfm-name-sync.service"),
			filepath.Join(managed, "systemd", "pfm-name-sync.service"),
		)
		assertLink(
			t,
			filepath.Join(unitDir, "default.target.wants", "pfm-name-sync.path"),
			filepath.Join(unitDir, "pfm-name-sync.path"),
		)
		assertLink(
			t,
			filepath.Join(unitDir, "timers.target.wants", nameSyncTimerUnit),
			filepath.Join(unitDir, nameSyncTimerUnit),
		)
	}
	// The predecessor's enablement links are a systemd concept; a launchd host
	// never wires systemd at all, so it has none to retire.
	if !schedulerIsLaunchd {
		for _, retired := range []string{legacyPathWant, legacyTimerWant} {
			if _, err := os.Lstat(retired); !os.IsNotExist(err) {
				t.Fatalf("retired enablement link remains at %s: %v", retired, err)
			}
		}
	}
	if _, err := os.Lstat(bbTarget + ".pre-professor-20300102-030405"); !os.IsNotExist(err) {
		t.Fatalf("install backed up an unowned bb.md: %v", err)
	}
	for _, retired := range []string{
		filepath.Join(config, ".cc-ls-hidden"),
		filepath.Join(config, "bin", "cc-kill.sh"),
	} {
		if _, err := os.Lstat(retired); !os.IsNotExist(err) {
			t.Fatalf("retired file remains at %s: %v", retired, err)
		}
	}
	// F1: the pfm-statusline and tmux-title-renudge host overlays are
	// installer-owned end to end — materialized under the managed root and
	// symlinked at their contracted ~/.local/bin names, same as every other
	// embedded asset and the Claude launcher.
	for name, command := range map[string]string{"pfm-statusline": "statusline", "tmux-title-renudge": "tmux-title-renudge"} {
		managedShim := filepath.Join(managed, "bin", name)
		assertLink(t, filepath.Join(home, ".local", "bin", name), managedShim)
		if info, err := os.Stat(managedShim); err != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("managed %s mode=%v err=%v, want 0755", name, info, err)
		}
		shim := readFixture(t, managedShim)
		want := "#!/usr/bin/env bash\n" + `exec "$HOME/.local/bin/pfm" internal ` + command + ` "$@"` + "\n"
		if shim != want {
			t.Fatalf("managed %s=%q, want native exec shim %q", name, shim, want)
		}
	}
	settings := readFixture(t, filepath.Join(config, "settings.json"))
	for _, wanted := range []string{
		home + "/.local/bin/pfm-statusline",
		home + "/.local/bin/pfm usage-hook",
		home + "/.local/bin/pfm internal clear-kill",
		home + "/.local/bin/pfm internal launcher-repair",
	} {
		if !strings.Contains(settings, wanted) {
			t.Fatalf("settings missing %q:\n%s", wanted, settings)
		}
	}
	if strings.Contains(settings, home+"/.local/bin/pfm statusline\"") {
		t.Fatalf("settings still point statusLine at the raw command, not the overlay:\n%s", settings)
	}
	for _, retired := range []string{"chat bb", "pfm bb", "bb-hook.sh"} {
		if strings.Contains(settings, retired) {
			t.Fatalf("settings retained retired /bb wiring %q:\n%s", retired, settings)
		}
	}
	codexHooks := readFixture(t, filepath.Join(home, ".codex", "hooks.json"))
	for _, wanted := range []string{"fixture-codex-keep"} {
		if !strings.Contains(codexHooks, wanted) {
			t.Fatalf("Codex hooks missing %q:\n%s", wanted, codexHooks)
		}
	}
	if strings.Contains(codexHooks, "/cc-fleet ") {
		t.Fatalf("Codex hooks retained predecessor command:\n%s", codexHooks)
	}
	if strings.Contains(codexHooks, "dream hook codex-subagent-inject") {
		t.Fatalf("Codex hooks retained paused Dream injection:\n%s", codexHooks)
	}
	// The Codex SessionStart clear-kill hook is retired: SessionStart(source=
	// clear) fires on the new session's first turn, by which point every
	// Codex chat on the host shares one app-server daemon pid, so it could
	// never say which pane cleared. Install strips a leftover one and never
	// writes it back.
	if strings.Contains(codexHooks, "internal clear-kill") {
		t.Fatalf("install wrote the retired Codex SessionStart clear-kill hook:\n%s", codexHooks)
	}
	secondary := readFixture(t, secondarySettings)
	if strings.Contains(secondary, "chat bb") ||
		!strings.Contains(secondary, home+"/.local/bin/pfm internal clear-kill") ||
		!strings.Contains(secondary, "secondary-keep") {
		t.Fatalf("secondary settings did not receive the complete hook wiring:\n%s", secondary)
	}
	if zshrc := readFixture(
		t,
		filepath.Join(home, ".zshrc"),
	); !strings.Contains(
		zshrc,
		sourceLine(filepath.Join(managed, "shim", "pfm.zsh")),
	) ||
		strings.Contains(zshrc, "cc-fleet.zsh") {
		t.Fatalf("zshrc was not converged:\n%s", zshrc)
	}

	state := fleetdb.OpenSharedState(context.Background(), paths.Values{
		Home: home, FleetDB: filepath.Join(home, ".cc", "fleet.db"),
	})
	killed, err := state.KilledAt(context.Background())
	closeErr := state.Close()
	if err != nil || closeErr != nil || len(killed) != 2 || killed["killed-a"] != 99 || killed["killed-b"] != 0 {
		t.Fatalf("migrated killed=%v err=%v close=%v", killed, err, closeErr)
	}

	var second bytes.Buffer
	secondReport, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Now: now, Stdout: &second, Runner: runner,
		ConfigDirs: []string{config, filepath.Join(home, ".cc", "2")},
	})
	if err != nil || secondReport.Changed != 0 {
		t.Fatalf("second apply report=%#v err=%v\n%s", secondReport, err, second.String())
	}

	var removed bytes.Buffer
	if _, err := Run(context.Background(), Options{
		Mode: ModeUninstall, Home: home, Now: now, Stdout: &removed, Runner: runner,
		ConfigDirs: []string{config, filepath.Join(home, ".cc", "2")},
	}); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, removed.String())
	}
	if content := readFixture(t, bbTarget); content != "operator copy\n" {
		t.Fatalf("uninstall changed operator bb.md = %q", content)
	}
	if settings := readFixture(
		t,
		filepath.Join(config, "settings.json"),
	); strings.Contains(
		settings,
		"internal clear-kill",
	) {
		t.Fatalf("uninstall retained clear-kill hook:\n%s", settings)
	}
	if codexHooks := readFixture(
		t,
		filepath.Join(home, ".codex", "hooks.json"),
	); strings.Contains(
		codexHooks,
		"internal clear-kill",
	) ||
		!strings.Contains(codexHooks, "fixture-codex-keep") ||
		strings.Contains(codexHooks, "dream hook codex-subagent-inject") {
		t.Fatalf("uninstall did not remove only the owned Codex clear hook:\n%s", codexHooks)
	}
	if _, err := os.Lstat(managed); !os.IsNotExist(err) {
		t.Fatalf("uninstall left managed asset root: %v", err)
	}
	for _, overlay := range []string{"pfm-statusline", "tmux-title-renudge"} {
		if _, err := os.Lstat(filepath.Join(home, ".local", "bin", overlay)); !os.IsNotExist(err) {
			t.Fatalf("uninstall left the %s host overlay link: %v", overlay, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(config, "skills", "handoff", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("uninstall left the /handoff skill link: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(config, "skills", "handoff")); !os.IsNotExist(err) {
		t.Fatalf("uninstall left the empty handoff skill directory behind: %v", err)
	}
	if schedulerIsLaunchd {
		agent := filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist")
		if _, err := os.Lstat(agent); !os.IsNotExist(err) {
			t.Fatalf("uninstall left the launch agent at %s: %v", agent, err)
		}
	}
	for _, removed := range []string{
		filepath.Join(unitDir, "default.target.wants", "pfm-name-sync.path"),
		filepath.Join(unitDir, "timers.target.wants", nameSyncTimerUnit),
	} {
		if _, err := os.Lstat(removed); !os.IsNotExist(err) {
			t.Fatalf("uninstall left enablement link at %s: %v", removed, err)
		}
	}
}

func TestThemeInstallIsIdempotentVisibleOnDriftAndReversible(t *testing.T) {
	home := t.TempDir()
	sourceRepo := t.TempDir()
	themeBody := []byte(`{"name":"Tokyo Night","fixture":true}` + "\n")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/tokyo-night.json" {
			http.NotFound(response, request)
			return
		}
		if _, err := response.Write(themeBody); err != nil {
			t.Errorf("write theme fixture response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	manifest := fmt.Sprintf(`{
  "_comment": "fixture",
  "source_fetched": {
    "tokyo-night": {
      "repo": %q,
      "raw": %q,
      "target": "~/.claude/themes/tokyo-night.json",
      "activate": "/theme",
      "requires": "fixture"
    }
  }
}`, server.URL, server.URL+"/tokyo-night.json")
	writeFixture(t, filepath.Join(sourceRepo, "templates", "themes", "sources.json"), manifest)

	run := func(mode Mode) (Report, string, error) {
		var output bytes.Buffer
		report, err := Run(context.Background(), Options{
			Mode: mode, Home: home, SourceRepo: sourceRepo, Stdout: &output,
			Runner: &fakeRunner{nameSyncIdle: true}, CodexHomes: []string{}, InstallThemes: true,
		})
		return report, output.String(), err
	}
	target := filepath.Join(home, ".claude", "themes", "tokyo-night.json")
	first, firstOutput, err := run(ModeApply)
	if err != nil {
		t.Fatalf("first theme install: %v\n%s", err, firstOutput)
	}
	if got := []byte(readFixture(t, target)); !bytes.Equal(got, themeBody) {
		t.Fatalf("installed theme=%q, want %q", got, themeBody)
	}
	second, secondOutput, err := run(ModeApply)
	if err != nil {
		t.Fatalf("second theme install: %v\n%s", err, secondOutput)
	}
	if second.Changed != 0 || !strings.Contains(secondOutput, "theme tokyo-night") {
		t.Fatalf("second report=%#v, want changed=0 with a named theme row\n%s", second, secondOutput)
	}
	if first.Changed == 0 {
		t.Fatalf("first report=%#v, want a theme write\n%s", first, firstOutput)
	}

	writeFixture(t, target, "operator modification\n")
	_, driftOutput, err := run(ModeApply)
	if err != nil {
		t.Fatalf("locally modified theme aborted install: %v\n%s", err, driftOutput)
	}
	if got := readFixture(t, target); got != "operator modification\n" {
		t.Fatalf("locally modified theme was overwritten: %q", got)
	}
	if !strings.Contains(driftOutput, "theme tokyo-night locally modified; left in place") {
		t.Fatalf("local drift was silent:\n%s", driftOutput)
	}

	writeFixture(t, target, string(themeBody))
	_, uninstallOutput, err := run(ModeUninstall)
	if err != nil {
		t.Fatalf("theme uninstall: %v\n%s", err, uninstallOutput)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("uninstall retained installer-owned theme: %v", err)
	}
}

func TestThemeFetchFailureIsLoudAndNonFatal(t *testing.T) {
	home := t.TempDir()
	sourceRepo := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "blocked by fixture", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	manifest := fmt.Sprintf(
		`{"source_fetched":{"tokyo-night":{"repo":%q,"raw":%q,"target":"~/.claude/themes/tokyo-night.json","activate":"/theme","requires":"fixture"}}}`,
		server.URL,
		server.URL+"/tokyo-night.json",
	)
	writeFixture(t, filepath.Join(sourceRepo, "templates", "themes", "sources.json"), manifest)
	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, SourceRepo: sourceRepo, Stdout: &output,
		Runner: &fakeRunner{nameSyncIdle: true}, CodexHomes: []string{}, InstallThemes: true,
	})
	if err != nil {
		t.Fatalf("cosmetic theme fetch aborted host install: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "theme tokyo-night fetch failed") ||
		!strings.Contains(output.String(), "503") {
		t.Fatalf("theme fetch failure was silent or vague:\n%s", output.String())
	}
	if _, statErr := os.Stat(filepath.Join(home, ".zshrc")); statErr != nil {
		t.Fatalf("host install did not continue after theme failure: %v", statErr)
	}
}

func TestEmptyCodexRosterSkipsCommandAndAgentMirrors(t *testing.T) {
	home := t.TempDir()
	writeFixture(t, filepath.Join(home, ".claude", "commands", "fixture.md"), "# Fixture command\n")
	writeFixture(t, filepath.Join(home, ".professor", "templates", "global", "agents", "fixture.md"), `---
name: fixture
description: fixture agent
---

# Fixture agent
`)
	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		Mode: ModeDryRun, Home: home, Stdout: &output, Runner: &fakeRunner{}, CodexHomes: []string{},
	})
	if err != nil {
		t.Fatalf("zero-Codex preview: %v\n%s", err, output.String())
	}
	for _, wanted := range []string{
		"no Codex accounts configured — command mirror has nothing to write",
		"no Codex accounts configured — agent mirror has nothing to write",
	} {
		if !strings.Contains(output.String(), wanted) {
			t.Errorf("zero-account mirror skip omitted %q:\n%s", wanted, output.String())
		}
	}
	for _, forbidden := range []string{
		filepath.Join(home, ".codex", "prompts"),
		filepath.Join(home, ".codex", "skills"),
		filepath.Join(home, ".codex", "agents"),
	} {
		if strings.Contains(output.String(), forbidden) {
			t.Errorf("zero-account preview still plans Codex mirror %s:\n%s", forbidden, output.String())
		}
	}
}

func TestThemeManifestResolvesRegisteredOwnerPlaceholder(t *testing.T) {
	sourceRepo := t.TempDir()
	writeFixture(
		t,
		filepath.Join(sourceRepo, ".professor", "manifest.json"),
		`{"installed_from":{"repo":"fixture-owner/professor"}}`,
	)
	writeFixture(t, filepath.Join(sourceRepo, "templates", "themes", "sources.json"), `{
  "source_fetched": {
    "tokyo-night": {
      "repo": "https://github.com/{GH_USER}/claude-code-tokyo-night",
      "raw": "https://raw.githubusercontent.com/{GH_USER}/claude-code-tokyo-night/main/tokyo-night.json",
      "target": "~/.claude/themes/tokyo-night.json",
      "activate": "/theme",
      "requires": "fixture"
    }
  }
}`)
	sources, err := loadThemeSources(context.Background(), Options{SourceRepo: sourceRepo})
	if err != nil {
		t.Fatalf("loadThemeSources: %v", err)
	}
	if got := sources["tokyo-night"].Raw; got != "https://raw.githubusercontent.com/fixture-owner/claude-code-tokyo-night/main/tokyo-night.json" {
		t.Fatalf("resolved raw URL=%q", got)
	}
}

func TestThemeManifestFallsBackToReleaseWhenDiscoveredSourceLacksManifest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(
			response,
			`{"source_fetched":{"tokyo-night":{"repo":"https://example.test/theme","raw":"https://example.test/theme.json","target":"~/.claude/themes/tokyo-night.json","activate":"/theme","requires":"fixture"}}}`,
		); err != nil {
			t.Errorf("write release manifest fixture: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	sources, err := loadThemeSources(context.Background(), Options{
		SourceRepo:       t.TempDir(),
		ThemeManifestURL: server.URL,
	})
	if err != nil {
		t.Fatalf("loadThemeSources() did not fall back to release manifest: %v", err)
	}
	if _, found := sources["tokyo-night"]; !found {
		t.Fatalf("release manifest sources=%#v, want tokyo-night", sources)
	}
}

func TestMCPEnablementSurvivesAnUnavailableSystemdUserManagerAndDisableRemovesIt(t *testing.T) {
	if schedulerIsLaunchd {
		t.Skip("systemd enablement is not installed on launchd hosts")
	}
	home := t.TempDir()
	wants := filepath.Join(home, ".config", "systemd", "user", "default.target.wants", mcpUnitName)
	options := Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{},
		MCPEnabled: map[string]bool{"chat": true},
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	assertLink(t, wants, filepath.Join(home, ".config", "systemd", "user", mcpUnitName))

	options.MCPEnabled = map[string]bool{"chat": false}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(wants); !os.IsNotExist(err) {
		t.Fatalf("MCP disable retained wants link %s: %v", wants, err)
	}
}

func TestMCPDisableRemovesEveryStagedSchedulerAsset(t *testing.T) {
	home := t.TempDir()
	managed := filepath.Join(home, ".local", "share", "pfm", "install")
	staleLaunchd := filepath.Join(managed, "launchd", "com.professor.pfm.mcp.plist")
	writeFixture(t, staleLaunchd, "stale staged plist\n")
	installer := engine{
		options: Options{Mode: ModeApply, Home: home, MCPEnabled: map[string]bool{"chat": false}, Stdout: io.Discard},
		apply:   true, managedRoot: managed,
	}
	assets, err := assetFiles()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installer.stageAssets(assets); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(staleLaunchd); !os.IsNotExist(err) {
		t.Fatalf("MCP disable retained stale staged launchd plist: %v", err)
	}
}

func TestUninstallCodexConflictRefusesBeforeRemovingGlobalCommands(t *testing.T) {
	home := t.TempDir()
	options := Options{Mode: ModeApply, Home: home, Runner: &fakeRunner{}}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(home, ".claude", "commands", "reload.md")
	if _, err := os.Lstat(command); err != nil {
		t.Fatalf("install did not wire command fixture: %v", err)
	}
	operatorSource := filepath.Join(home, ".claude", "commands", "audit-fixture.md")
	writeFixture(t, operatorSource, "# Operator command\n")
	conflict := filepath.Join(home, ".codex", "prompts", "audit-fixture.md")
	writeFixture(t, conflict, "operator-owned conflict\n")

	options.Mode = ModeUninstall
	_, err := Run(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), "CONFLICT "+conflict) {
		t.Fatalf("uninstall error=%v, want Codex conflict", err)
	}
	if _, err := os.Lstat(command); err != nil {
		t.Fatalf("uninstall conflict removed global command before aborting: %v", err)
	}
	if got := readFixture(t, conflict); got != "operator-owned conflict\n" {
		t.Fatalf("uninstall conflict changed operator file: %q", got)
	}
}

// TestInstallReconcilesGlobalCodexCommands is the issue-6 regression: install
// wired the global Claude command sources but never rebuilt their Codex prompt
// and skill mirrors, leaving pfm codex check to report stale/missing/orphans.
func TestInstallReconcilesGlobalCodexCommands(t *testing.T) {
	home := t.TempDir()
	repository := t.TempDir()
	repositorySentinel := filepath.Join(repository, "AGENTS.md")
	writeFixture(t, repositorySentinel, "repository sentinel\n")
	t.Chdir(repository)
	if _, err := Run(context.Background(), Options{
		Mode: ModeDryRun, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); !os.IsNotExist(err) {
		t.Fatalf("dry run created Codex registry: %v", err)
	}
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	for _, path := range []string{
		filepath.Join(home, ".codex", "prompts", "reload.md"),
		filepath.Join(home, ".codex", "skills", "reload", "SKILL.md"),
	} {
		content := readFixture(t, path)
		if !strings.Contains(content, "Generated by pfm codex build") {
			t.Fatalf("global Codex command mirror %s is not marker-owned:\n%s", path, content)
		}
	}
	// The retired /chat: commands never reach ~/.claude/commands/chat/ at all,
	// so their former Codex mirrors (chat-inject, chat-interrogate) must not
	// exist either.
	for _, path := range []string{
		filepath.Join(home, ".codex", "prompts", "chat-inject.md"),
		filepath.Join(home, ".codex", "prompts", "chat-interrogate.md"),
		filepath.Join(home, ".codex", "skills", "chat-inject"),
		filepath.Join(home, ".codex", "skills", "chat-interrogate"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("retired /chat: command left a Codex mirror at %s: %v", path, err)
		}
	}

	foreign := filepath.Join(home, ".codex", "prompts", "operator.md")
	writeFixture(t, foreign, "operator-owned prompt\n")
	if _, err := Run(context.Background(), Options{
		Mode: ModeUninstall, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	for _, path := range []string{
		filepath.Join(home, ".codex", "prompts", "reload.md"),
		filepath.Join(home, ".codex", "skills", "reload"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("uninstall left marker-owned command mirror %s: %v", path, err)
		}
	}
	if got := readFixture(t, foreign); got != "operator-owned prompt\n" {
		t.Fatalf("uninstall changed foreign prompt: %q", got)
	}
	if got := readFixture(t, repositorySentinel); got != "repository sentinel\n" {
		t.Fatalf("global install path changed repository file: %q", got)
	}
}

// TestWireCodexAgentsInstallsSymlinksNotCopies is the RED-then-GREEN pin on
// the copy-to-symlink conversion at the pfm install call site: both
// {Home}/.claude/agents and {Home}/.codex/agents must resolve to the exact
// source-repo originals, never a byte-for-byte copy install used to leave.
func TestWireCodexAgentsInstallsSymlinksNotCopies(t *testing.T) {
	home := t.TempDir()
	writeFixture(t, filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md"),
		"---\nname: alpha\ndescription: Alpha role for testing.\n---\n\nbody\n")

	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}

	assertLink(t,
		filepath.Join(home, ".claude", "agents", "alpha.md"),
		filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md"))
	assertLink(t,
		filepath.Join(home, ".codex", "agents", "alpha.toml"),
		filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.toml"))
}

// TestWireCodexAgentsReportsAndPreservesAForeignConflict is the conflict-law
// pin at the installer boundary: a symlink pointing entirely outside the
// source repository is reported by the exact "CONFLICT ...: not ours"
// wording and is never overwritten or deleted — and, critically, a conflict
// never aborts the rest of the install.
func TestWireCodexAgentsReportsAndPreservesAForeignConflict(t *testing.T) {
	home := t.TempDir()
	writeFixture(t, filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md"),
		"---\nname: alpha\ndescription: Alpha role for testing.\n---\n\nbody\n")
	elsewhere := filepath.Join(home, "elsewhere.md")
	writeFixture(t, elsewhere, "operator file\n")
	foreignLink := filepath.Join(home, ".claude", "agents", "alpha.md")
	if err := os.MkdirAll(filepath.Dir(foreignLink), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, foreignLink); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Stdout: &output, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatalf("apply refused on a conflict it must only report: %v\n%s", err, output.String())
	}
	want := "CONFLICT " + foreignLink + ": not ours"
	if !strings.Contains(output.String(), want) {
		t.Fatalf("apply output omitted %q:\n%s", want, output.String())
	}
	resolved, err := os.Readlink(foreignLink)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != elsewhere {
		t.Fatalf("conflict link was rewritten: now -> %s", resolved)
	}
}

// TestWireGlobalCommandsLinksFilesAndDirectories covers both shapes bullet 2
// of the global-commands behavior spec names: a file entry links as a single
// file symlink, a directory entry (wave/) links as ONE whole-directory
// symlink — never a copy of its contents.
func TestWireGlobalCommandsLinksFilesAndDirectories(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, ".professor", "templates", "global", "commands")
	writeFixture(t, filepath.Join(source, "git.md"), "# git command\n")
	writeFixture(t, filepath.Join(source, "wave", "refine.md"), "# wave refine\n")

	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}

	assertLink(t, filepath.Join(home, ".claude", "commands", "git.md"), filepath.Join(source, "git.md"))
	assertLink(t, filepath.Join(home, ".claude", "commands", "wave"), filepath.Join(source, "wave"))
}

// TestWireGlobalCommandsSkipsAnAbsentOrEmptySource pins the spec's explicit
// carve-out: a parallel lane may not have populated templates/global/commands
// yet, and that is a reported skip (0 entries), never an error.
func TestWireGlobalCommandsSkipsAnAbsentOrEmptySource(t *testing.T) {
	for _, name := range []string{"absent", "empty"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if name == "empty" {
				if err := os.MkdirAll(
					filepath.Join(home, ".professor", "templates", "global", "commands"),
					0o700,
				); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			if _, err := Run(context.Background(), Options{
				Mode: ModeApply, Home: home, Stdout: &output, Runner: &fakeRunner{},
			}); err != nil {
				t.Fatalf("apply: %v\n%s", err, output.String())
			}
			if !strings.Contains(output.String(), "(0 entries)") {
				t.Fatalf("%s global commands source was not reported as 0 entries:\n%s", name, output.String())
			}
			entries, err := os.ReadDir(filepath.Join(home, ".claude", "commands"))
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() != "reload.md" {
					t.Fatalf("%s global commands source still wrote %s", name, entry.Name())
				}
			}
		})
	}
}

// TestWireGlobalSkillsLinksDeepRR pins the third registry: a whole-directory
// symlink at {Home}/.claude/skills/deep-rr resolving to the in-tree
// workflows/deep-rr skill.
func TestWireGlobalSkillsLinksDeepRR(t *testing.T) {
	home := t.TempDir()
	writeFixture(t, filepath.Join(home, ".professor", "workflows", "deep-rr", "SKILL.md"), "# deep-rr skill\n")

	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	assertLink(t,
		filepath.Join(home, ".claude", "skills", "deep-rr"),
		filepath.Join(home, ".professor", "workflows", "deep-rr"))
}

// TestWireGlobalSkillsReportsMissingSkillSource pins the exact
// SKILL-SOURCE-MISSING wording bullet 3 requires when workflows/deep-rr/
// SKILL.md is absent at link time — reported, no link ever created.
func TestWireGlobalSkillsReportsMissingSkillSource(t *testing.T) {
	home := t.TempDir()
	var output bytes.Buffer
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Stdout: &output, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatalf("apply: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "SKILL-SOURCE-MISSING deep-rr") {
		t.Fatalf("apply output omitted the missing-skill-source report:\n%s", output.String())
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "deep-rr")); !os.IsNotExist(err) {
		t.Fatalf("a missing skill source still produced a link: %v", err)
	}
}

// TestWireGlobalSkillsLinksTemplateSkillDirectories pins the second half of
// the skills registry: every directory shipped under templates/global/skills/
// becomes ONE whole-directory link in {Home}/.claude/skills, while the
// sources.json registry beside them — a file, not a skill — is never linked.
func TestWireGlobalSkillsLinksTemplateSkillDirectories(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, ".professor", "templates", "global", "skills")
	writeFixture(t, filepath.Join(source, "sources.json"), "{}\n")
	writeFixture(t, filepath.Join(source, "architecture-design", "SKILL.md"), "# architecture-design skill\n")

	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}

	assertLink(t,
		filepath.Join(home, ".claude", "skills", "architecture-design"),
		filepath.Join(source, "architecture-design"))
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "sources.json")); !os.IsNotExist(err) {
		t.Fatalf("the skills registry file was linked as if it were a skill: %v", err)
	}
}

// TestWireGlobalSkillsReportsATemplateSkillWithoutSKILLMd pins that the
// SKILL-SOURCE-MISSING report covers template skills too: a directory with no
// SKILL.md is named and left unlinked rather than linked as a loadable skill.
func TestWireGlobalSkillsReportsATemplateSkillWithoutSKILLMd(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, ".professor", "templates", "global", "skills")
	if err := os.MkdirAll(filepath.Join(source, "half-built"), 0o700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Stdout: &output, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatalf("apply: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "SKILL-SOURCE-MISSING half-built") {
		t.Fatalf("apply output omitted the missing-skill-source report:\n%s", output.String())
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude", "skills", "half-built")); !os.IsNotExist(err) {
		t.Fatalf("a skill source without SKILL.md still produced a link: %v", err)
	}
}

// TestRetireOrphanCodexAgentsDeletesExactlyTheKnownStrays pins bullet 4's
// narrow, hardcoded sweep: the retired explorer agent's compiled TOML and
// any timestamped backup are deleted; a genuinely unrelated agent file next
// to them is never touched.
func TestRetireOrphanCodexAgentsDeletesExactlyTheKnownStrays(t *testing.T) {
	home := t.TempDir()
	explorer := filepath.Join(home, ".codex", "agents", "explorer.toml")
	writeFixture(t, explorer, "stale explorer agent\n")
	backup := explorer + ".bak-20300101"
	writeFixture(t, backup, "stale backup\n")
	keeper := filepath.Join(home, ".codex", "agents", "tracer.toml")
	writeFixture(t, keeper, "keeper\n")

	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	for _, retired := range []string{explorer, backup} {
		if _, err := os.Lstat(retired); !os.IsNotExist(err) {
			t.Fatalf("orphan sweep left %s: %v", retired, err)
		}
	}
	if got := readFixture(t, keeper); got != "keeper\n" {
		t.Fatalf("orphan sweep touched an unrelated agent file: %q", got)
	}
}

// TestRetireRenamedGlobalAgentsDeletesOnlyTheInstallersOwnFrrLeftover pins
// issue #14 F5: the frr->rr global-agent rename (3976b53) left a stale
// {config}/agents/frr.md RunGlobalAgents no longer visits. A symlink at that
// path is always the installer's own — nothing else in this registry ever
// creates one — so it retires unconditionally, dangling target or not; a
// regular file retires only when its YAML frontmatter `name:` still reads
// "frr", the same field RunGlobalAgents keys identity on, so a user's own
// same-named agent (different frontmatter, or none at all) is never touched.
func TestRetireRenamedGlobalAgentsDeletesOnlyTheInstallersOwnFrrLeftover(t *testing.T) {
	t.Run("a symlink at the retired path retires unconditionally, even dangling", func(t *testing.T) {
		home := t.TempDir()
		link := filepath.Join(home, ".claude", "agents", "frr.md")
		if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(
			filepath.Join(home, ".professor", "templates", "global", "agents", "frr.md"),
			link,
		); err != nil {
			t.Fatal(err)
		}
		if _, err := Run(
			context.Background(),
			Options{Mode: ModeApply, Home: home, Runner: &fakeRunner{}},
		); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(link); !os.IsNotExist(err) {
			t.Fatalf("retired frr.md symlink survived: %v", err)
		}
	})

	t.Run("a regular file whose frontmatter name is frr retires", func(t *testing.T) {
		home := t.TempDir()
		frr := filepath.Join(home, ".claude", "agents", "frr.md")
		writeFixture(t, frr, "---\nname: frr\ndescription: pre-rename research agent\n---\nbody\n")
		if _, err := Run(
			context.Background(),
			Options{Mode: ModeApply, Home: home, Runner: &fakeRunner{}},
		); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(frr); !os.IsNotExist(err) {
			t.Fatalf("retired frr.md regular file survived: %v", err)
		}
	})

	t.Run("a same-named user-authored agent with different frontmatter survives", func(t *testing.T) {
		home := t.TempDir()
		frr := filepath.Join(home, ".claude", "agents", "frr.md")
		writeFixture(
			t,
			frr,
			"---\nname: my-own-frr\ndescription: unrelated agent I happen to have named frr\n---\nbody\n",
		)
		if _, err := Run(
			context.Background(),
			Options{Mode: ModeApply, Home: home, Runner: &fakeRunner{}},
		); err != nil {
			t.Fatal(err)
		}
		if got := readFixture(t, frr); !strings.Contains(got, "my-own-frr") {
			t.Fatalf("installer touched a user-authored agent that shares the retired filename: %q", got)
		}
	})

	t.Run("a regular file with no frontmatter at all survives", func(t *testing.T) {
		home := t.TempDir()
		frr := filepath.Join(home, ".claude", "agents", "frr.md")
		writeFixture(t, frr, "just prose, no frontmatter\n")
		if _, err := Run(
			context.Background(),
			Options{Mode: ModeApply, Home: home, Runner: &fakeRunner{}},
		); err != nil {
			t.Fatal(err)
		}
		if got := readFixture(t, frr); got != "just prose, no frontmatter\n" {
			t.Fatalf("installer touched a frontmatter-less file: %q", got)
		}
	})

	t.Run("absent frr.md is a silent no-op", func(t *testing.T) {
		home := t.TempDir()
		if _, err := Run(
			context.Background(),
			Options{Mode: ModeApply, Home: home, Runner: &fakeRunner{}},
		); err != nil {
			t.Fatal(err)
		}
	})
}

// TestGlobalSourceRepoRootPrefersExplicitOptionOverDefault pins the
// resolution order globalSourceRepoRoot promises: an explicit --source-repo
// wins over the documented {Home}/.professor default, even when a same-named
// fixture also exists at that default location.
func TestGlobalSourceRepoRootPrefersExplicitOptionOverDefault(t *testing.T) {
	home := t.TempDir()
	elsewhere := t.TempDir()
	writeFixture(t, filepath.Join(elsewhere, "workflows", "deep-rr", "SKILL.md"), "# deep-rr skill\n")
	writeFixture(t, filepath.Join(home, ".professor", "workflows", "deep-rr", "SKILL.md"), "# wrong deep-rr skill\n")

	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, SourceRepo: elsewhere, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	assertLink(t,
		filepath.Join(home, ".claude", "skills", "deep-rr"),
		filepath.Join(elsewhere, "workflows", "deep-rr"))
}

// TestGlobalSourceRepoRootFallsBackToTheRecordedMarker pins the second rung:
// a later install that omits --source-repo entirely must still resolve
// through the marker the first install recorded, not silently reset to the
// {Home}/.professor default.
func TestGlobalSourceRepoRootFallsBackToTheRecordedMarker(t *testing.T) {
	home := t.TempDir()
	elsewhere := t.TempDir()
	writeFixture(t, filepath.Join(elsewhere, "workflows", "deep-rr", "SKILL.md"), "# deep-rr skill\n")

	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, SourceRepo: elsewhere, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(home, ".claude", "skills", "deep-rr")); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	assertLink(t,
		filepath.Join(home, ".claude", "skills", "deep-rr"),
		filepath.Join(elsewhere, "workflows", "deep-rr"))
}

func TestApplyRetiresInstalledBBCardsAndHook(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	managed := filepath.Join(home, ".local", "share", "pfm", "install")
	commandTarget := filepath.Join(config, "commands", "bb.md")
	skillTarget := filepath.Join(home, ".agents", "skills", "bb")
	writeFixture(t, filepath.Join(managed, "bb.command.md"), "old managed command\n")
	writeFixture(t, filepath.Join(managed, "codex-skills", "bb", "SKILL.md"), "old managed skill\n")
	writeFixture(t, filepath.Join(managed, "codex-skills", "bb", "agents", "openai.yaml"), "old managed metadata\n")
	if err := os.MkdirAll(filepath.Dir(commandTarget), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(managed, "bb.command.md"), commandTarget); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, commandTarget+".pre-professor-20300102-030405", "operator command\n")
	if err := os.MkdirAll(filepath.Dir(skillTarget), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(managed, "codex-skills", "bb"), skillTarget); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(config, "settings.json"), `{
  "hooks": {
    "UserPromptSubmit": [
      {"matcher":"","hooks":[
        {"type":"command","command":"`+home+`/.local/bin/pfm chat bb"},
        {"type":"command","command":"fixture-keep"}
      ]}
    ]
  }
}`)

	now := func() time.Time { return time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC) }
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Now: now, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	if content := readFixture(t, commandTarget); content != "operator command\n" {
		t.Fatalf("retirement did not restore operator card: %q", content)
	}
	for _, retired := range []string{
		skillTarget,
		filepath.Join(managed, "bb.command.md"),
		filepath.Join(managed, "codex-skills"),
	} {
		if _, err := os.Lstat(retired); !os.IsNotExist(err) {
			t.Fatalf("retired /bb surface remains at %s: %v", retired, err)
		}
	}
	settings := readFixture(t, filepath.Join(config, "settings.json"))
	if strings.Contains(settings, "chat bb") || !strings.Contains(settings, "fixture-keep") ||
		!strings.Contains(settings, home+"/.local/bin/pfm internal clear-kill") {
		t.Fatalf("settings did not retire /bb and wire clear-kill:\n%s", settings)
	}

	var second bytes.Buffer
	report, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Now: now, Stdout: &second, Runner: &fakeRunner{},
	})
	if err != nil || report.Changed != 0 {
		t.Fatalf("second apply report=%#v err=%v\n%s", report, err, second.String())
	}
}

func TestApplyLeavesUnrelatedBBSymlinkAlone(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, ".claude", "commands", "bb.md")
	operatorSource := filepath.Join(home, "operator", "bb.md")
	writeFixture(t, operatorSource, "operator command\n")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(operatorSource, target); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(home, ".claude", "settings.json"), `{}`)

	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	assertLink(t, target, operatorSource)
}

func TestApplyRetiresDanglingBBLinksFromTheRecordedProfessorClone(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "professor-clone")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteSourceRepoMarker(home, repo); err != nil {
		t.Fatal(err)
	}
	commandTarget := filepath.Join(home, ".claude", "commands", "bb.md")
	skillTarget := filepath.Join(home, ".agents", "skills", "bb")
	for _, target := range []string{commandTarget, skillTarget} {
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	legacyRoot := filepath.Join(repo, "blueprint", "templates", "host-swap")
	if err := os.Symlink(filepath.Join(legacyRoot, "bb.command.md"), commandTarget); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(legacyRoot, "codex-skills", "bb"), skillTarget); err != nil {
		t.Fatal(err)
	}

	installer := &engine{
		options: Options{Mode: ModeApply, Home: home, ConfigDir: filepath.Join(home, ".claude"), Stdout: io.Discard},
		apply:   true, managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
	}
	if err := installer.retireBBInstall(); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{commandTarget, skillTarget} {
		if _, err := os.Lstat(target); !os.IsNotExist(err) {
			t.Fatalf("recorded legacy /bb link remains at %s: %v", target, err)
		}
	}
}

func TestDryRunNamesFutureCodexWritesAndRefusesTheirConflicts(t *testing.T) {
	home := t.TempDir()
	writeFixture(t, filepath.Join(home, ".professor", "templates", "global", "agents", "tracer.md"), `---
name: tracer
description: Trace a target.
---
Read only.
`)
	conflict := filepath.Join(home, ".codex", "prompts", "reload.md")
	writeFixture(t, conflict, "operator-owned\n")

	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		Mode: ModeDryRun, Home: home, Stdout: &output, Runner: &fakeRunner{},
	})
	if err == nil || !strings.Contains(err.Error(), "CONFLICT "+conflict) {
		t.Fatalf("dry-run error=%v, want future Codex conflict; output:\n%s", err, output.String())
	}
	for _, path := range []string{
		conflict,
		filepath.Join(home, ".professor", "templates", "global", "agents", "tracer.toml"),
		filepath.Join(home, ".claude", "agents", "tracer.md"),
		filepath.Join(home, ".codex", "agents", "tracer.toml"),
	} {
		if !strings.Contains(output.String(), path) {
			t.Errorf("dry-run omitted planned path %s:\n%s", path, output.String())
		}
	}
	if strings.Contains(output.String(), "would reconcile") ||
		strings.Contains(output.String(), "would run pfm codex agents") {
		t.Fatalf("dry-run retained vague placeholders:\n%s", output.String())
	}
	if got := readFixture(t, conflict); got != "operator-owned\n" {
		t.Fatalf("dry-run mutated conflict = %q", got)
	}
	for _, path := range []string{
		filepath.Join(home, ".professor", "templates", "global", "agents", "tracer.toml"),
		filepath.Join(home, ".claude", "agents", "tracer.md"),
		filepath.Join(home, ".codex", "agents", "tracer.toml"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("dry-run wrote agent artifact %s: %v", path, err)
		}
	}

	output.Reset()
	_, err = Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Stdout: &output, Runner: &fakeRunner{},
	})
	if err == nil || !strings.Contains(err.Error(), "preflight apply plan") ||
		!strings.Contains(err.Error(), "CONFLICT "+conflict) {
		t.Fatalf("apply error=%v, want pre-mutation conflict refusal; output:\n%s", err, output.String())
	}
	for _, path := range []string{
		filepath.Join(home, ".local", "share", "pfm", "install"),
		filepath.Join(home, ".claude", "agents", "tracer.md"),
		binaryOwnershipPath(home),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Errorf("apply preflight wrote %s before refusing conflict: %v", path, statErr)
		}
	}
	if got := readFixture(t, conflict); got != "operator-owned\n" {
		t.Fatalf("apply preflight mutated conflict = %q", got)
	}
}

func TestUninstallAlsoRemovesRetiredDreamHooks(t *testing.T) {
	home := t.TempDir()
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	oldCommand := home + "/.local/bin/cc-fleet dream hook agent-inject"
	writeFixture(t, settingsPath, `{"hooks":{"PreToolUse":[{"hooks":[{"command":"`+oldCommand+`"}]}]}}`)

	if _, err := Run(context.Background(), Options{
		Mode: ModeUninstall, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	if settings := readFixture(t, settingsPath); strings.Contains(settings, oldCommand) {
		t.Fatalf("uninstall retained retired Dream hook:\n%s", settings)
	}
}

func TestUnitTransitionsUseOnlyTheInjectedManager(t *testing.T) {
	home := t.TempDir()
	runner := &fakeRunner{manager: true}
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: runner,
	}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.calls, "\n")
	wantCalls := []string{
		"systemctl --user is-active --quiet pfm-name-sync.service",
		"systemctl --user daemon-reload",
		"systemctl --user enable --now pfm-name-sync.path " + nameSyncTimerUnit,
	}
	if schedulerIsLaunchd {
		// launchd has no manager probe to fail: the agent is bootstrapped into
		// the caller's own gui domain, and bootout first so an edited plist
		// replaces the loaded job instead of being rejected as a duplicate.
		wantCalls = []string{
			"launchctl bootout gui/",
			"launchctl bootstrap gui/",
		}
		for _, unwanted := range []string{"daemon-reload", "enable --now"} {
			if strings.Contains(joined, unwanted) {
				t.Fatalf("systemd was driven on a launchd host:\n%s", joined)
			}
		}
	}
	for _, wanted := range wantCalls {
		if !strings.Contains(joined, wanted) {
			t.Fatalf("systemctl calls missing %q:\n%s", wanted, joined)
		}
	}
}

// TestEarlyFleetCallsNamesWhatRunsBeforeTheLaunchersExist fixtures the class the
// installer cannot let pass silently: a fleet command CALLED above the source
// line. `cc` is also the POSIX C compiler and `cx` is a name anything may claim,
// so instead of "command not found" the shell runs a stranger — which is how a
// terminal profile calling `cc` from ~/.zshrc greeted every new terminal with
// "clang: error: no input files" while the fleet loaded fine a few lines later.
func TestEarlyFleetCallsNamesWhatRunsBeforeTheLaunchersExist(t *testing.T) {
	const sourced = `[[ -r "/opt/fixture/pfm/install/shim/pfm.zsh" ]] && source "/opt/fixture/pfm/install/shim/pfm.zsh"`

	cases := []struct {
		name    string
		content string
		want    []string
	}{{
		name:    "the canonical bug: a profile hook above the source line",
		content: "export EDITOR=vim\n[[ -n \"$VSCODE_AUTO_CC\" ]] && cc\n" + sourced + "\n",
		want:    []string{"line 2: [[ -n \"$VSCODE_AUTO_CC\" ]] && cc"},
	}, {
		name:    "the same call below the source line is fine",
		content: sourced + "\n[[ -n \"$VSCODE_AUTO_CC\" ]] && cc\n",
		want:    nil,
	}, {
		name:    "no source line yet — the whole file is above the bottom",
		content: "cc-ls\n",
		want:    []string{"line 1: cc-ls"},
	}, {
		name:    "a commented-out call is not a call",
		content: "# cc-ls here would break\ncc  # but this one is real\n" + sourced + "\n",
		want:    []string{"line 2: cc  # but this one is real"},
	}, {
		name:    "definitions and paths are not calls",
		content: "alias cc='echo no'\nexport CCDIR=$HOME/.cc/2\nPATH=$PATH:/opt/cc\n" + sourced + "\n",
		want:    nil,
	}, {
		name: "every launcher, after every separator that starts a command",
		content: "cc1\nfoo; cc2\nfoo && cx\nfoo || cc-ls\n(cc-open)\n" +
			"cc-swap 1\n" + sourced + "\n",
		want: []string{
			"line 1: cc1", "line 2: foo; cc2", "line 3: foo && cx", "line 4: foo || cc-ls",
			"line 5: (cc-open)", "line 6: cc-swap 1",
		},
	}, {
		name:    "an empty rc file reports nothing",
		content: "",
		want:    nil,
	}}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := earlyFleetCalls(testCase.content)
			if len(got) != len(testCase.want) {
				t.Fatalf("earlyFleetCalls() = %q, want %q", got, testCase.want)
			}
			for index, wanted := range testCase.want {
				if got[index] != wanted {
					t.Fatalf("earlyFleetCalls()[%d] = %q, want %q", index, got[index], wanted)
				}
			}
		})
	}
}

// The rewriter and the early-call scan must never disagree about where the
// source line is: one decides where the launchers start existing, the other
// reports what runs before they do.
func TestEarlyCallScanStopsWhereTheRewriterFindsTheSourceLine(t *testing.T) {
	home := t.TempDir()
	zshrc := filepath.Join(home, ".zshrc")
	writeFixture(t, zshrc, "cc\nsource /old/cc-fleet.zsh\ncc-ls\n")

	var output bytes.Buffer
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Stdout: &output, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatalf("apply: %v\n%s", err, output.String())
	}
	report := output.String()
	// Line 1 runs before the launchers exist; line 3 does not.
	if !strings.Contains(report, "a fleet command runs ABOVE the source line") ||
		!strings.Contains(report, "line 1: cc\n") {
		t.Fatalf("installer did not report the early call:\n%s", report)
	}
	if strings.Contains(report, "line 3: cc-ls") {
		t.Fatalf("installer reported a call below the source line:\n%s", report)
	}
	// It reports; it never rewrites a line of the operator's shell that is not ours.
	if zshrcContent := readFixture(t, zshrc); !strings.HasPrefix(zshrcContent, "cc\n") {
		t.Fatalf("installer rewrote the operator's own line:\n%s", zshrcContent)
	}
}

// outputRunner is a fakeRunner that can also answer a probe, which is what the
// launch-agent gate needs: launchctl reports a job's state in its output and
// exits zero either way.
type outputRunner struct {
	fakeRunner
	printOutput string
	printErr    error
}

func (runner *outputRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	runner.calls = append(runner.calls, name+" "+strings.Join(args, " "))
	if runner.printErr != nil {
		return nil, runner.printErr
	}
	return []byte(runner.printOutput), nil
}

// TestLaunchAgentGateRefusesOnlyMidExecution pins the macOS half of the rc 97
// refusal. It is deliberately NOT the dead-bus gate: launchd is always live for
// a logged-in user, so the only window worth refusing is an apply that would
// rewrite the agent and its binary while that agent is running.
func TestLaunchAgentGateRefusesOnlyMidExecution(t *testing.T) {
	if !schedulerIsLaunchd {
		t.Skip("launch-agent gate is macOS-only")
	}
	cases := []struct {
		name       string
		output     string
		outputErr  error
		wantRefuse bool
	}{
		{name: "mid-execution refuses", output: "\tstate = running\n", wantRefuse: true},
		// "not running" CONTAINS "running": a substring match here would refuse
		// every install on a perfectly idle agent.
		{name: "idle proceeds", output: "\tstate = not running\n"},
		{name: "unknown label proceeds", outputErr: errors.New("could not find service")},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &outputRunner{printOutput: testCase.output, printErr: testCase.outputErr}
			_, err := Run(context.Background(), Options{
				Mode: ModeApply, Home: t.TempDir(), Stdout: io.Discard, Runner: runner,
			})
			if testCase.wantRefuse {
				if !errors.Is(err, ErrLaunchAgentRunning) {
					t.Fatalf("Run() error = %v, want ErrLaunchAgentRunning", err)
				}
				return
			}
			if errors.Is(err, ErrLaunchAgentRunning) {
				t.Fatalf("Run() refused an agent that was not mid-execution: %v", err)
			}
		})
	}
}

// A runner that cannot be probed must not be read as "safe": the installer says
// the gate did not run rather than implying it passed.
func TestLaunchAgentGateAnnouncesWhenItCannotProbe(t *testing.T) {
	if !schedulerIsLaunchd {
		t.Skip("launch-agent gate is macOS-only")
	}
	var output bytes.Buffer
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: t.TempDir(), Stdout: &output, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "launch-agent gate NOT probed") {
		t.Fatalf("an unprobed gate was silent:\n%s", output.String())
	}
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFixture(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func assertLink(t *testing.T, target, wanted string) {
	t.Helper()
	got, linked := resolvedLink(target)
	if !linked || got != wanted {
		t.Fatalf("link %s -> %q,%v, want %q,true", target, got, linked, wanted)
	}
}

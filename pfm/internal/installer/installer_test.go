package installer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/reload"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestInstallPreviewListsPrunableVersionsAndApplyRemovesOnlyThem is C: the
// prune sibling to wireClaudeLauncher, destructive-defaults-to-preview per
// pfm/CLAUDE.md, and the preview IS the apply's own preview — same
// classification with and without --apply, only the action differs. Four
// versions: the newest two are protected by the keep window, a third is
// protected because a live pid is executing it despite being outside that
// window, and the fourth is the only one either run may remove.
func TestInstallPreviewListsPrunableVersionsAndApplyRemovesOnlyThem(t *testing.T) {
	t.Parallel()
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
		if err := testjail.WriteExecutable(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
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
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeDryRun, Home: home, Runner: &fakeRunner{}, ProcRoot: procRoot, Stdout: &preview,
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
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Runner: &fakeRunner{}, ProcRoot: procRoot, Stdout: &apply,
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
	t.Parallel()
	home := t.TempDir()
	runner := &fakeRunner{manager: true, nameSyncActive: true}
	report, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeDryRun, Home: home, Runner: runner,
	})
	if err != nil || report.Changed == 0 {
		t.Fatalf("dry run report=%#v err=%v, want an ungated preview", report, err)
	}
	for _, call := range runner.calls {
		if call == nameSyncStateProbe {
			t.Fatalf("dry run called the running-service gate: %v", runner.calls)
		}
	}
	if entries, readErr := os.ReadDir(home); readErr != nil || len(entries) != 0 {
		t.Fatalf("dry run wrote files: entries=%v err=%v", entries, readErr)
	}
}

func TestDryRunNamesUpdateMetadataWithoutWritingIt(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	source := t.TempDir()
	var output bytes.Buffer
	report, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeDryRun, Home: home, SourceRepo: source,
		Stdout: &output, Runner: &fakeRunner{},
	})
	if err != nil || report.Changed == 0 {
		t.Fatalf("dry run report=%#v err=%v\n%s", report, err, output.String())
	}
	for _, path := range []string{paths.SourceRepoPath(home), binaryOwnershipPath(home)} {
		if !strings.Contains(output.String(), "write "+path) {
			t.Errorf("dry run omitted update-metadata path %s:\n%s", path, output.String())
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("dry run wrote update metadata %s: %v", path, err)
		}
	}
}

// TestChangeDescriptionNamesCreateVsBackedUpRewrite is a direct pin on the
// #9 helper: installer.change must report "create" for a target that had
// nothing to back up and "rewrite ... (backup preserved)" only when a
// backup is actually written. Callers pass the SAME existed value that
// gates the backup, so this contract is the whole guarantee.
func TestChangeDescriptionNamesCreateVsBackedUpRewrite(t *testing.T) {
	t.Parallel()
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

// TestZshrcWiredOnFirstInstallBeforeAnyMarker is a fresh machine's first
// `pfm install --yes` from the clone: the marker is recorded by this very run,
// so the shell line must come from the clone being installed, not from a
// marker a prior install would have left.
func TestZshrcWiredOnFirstInstallBeforeAnyMarker(t *testing.T) {
	home := t.TempDir()
	clone := t.TempDir()
	var applied bytes.Buffer
	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t), SourceRepo: clone,
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: &applied,
	}); err != nil {
		t.Fatalf("first install: %v\n%s", err, applied.String())
	}
	want := sourceLine(filepath.Join(clone, "pfm", "internal", "installer", "assets", "shim", "pfm.zsh"))
	if content := readFixture(t, filepath.Join(home, ".zshrc")); !strings.Contains(content, want) {
		t.Fatalf("first install left .zshrc without %q:\n%s\n%s", want, content, applied.String())
	}
}

func TestZshrcCreateOnFreshHomeNamesItselfHonestly(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	recordFixtureSourceRepo(t, home, t.TempDir())
	var applied bytes.Buffer
	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: &applied,
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
	t.Parallel()
	home := t.TempDir()
	clone := t.TempDir()
	recordFixtureSourceRepo(t, home, clone)
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
		Home: home, StateDB: filepath.Join(home, ".local", "state", "pfm", "pfm.db"),
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
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeDryRun, Home: home, Now: now, Stdout: &preview, Runner: runner,
	})
	if err != nil || previewReport.Changed == 0 {
		t.Fatalf("dry run report=%#v err=%v", previewReport, err)
	}
	stagedClaude := filepath.Join(home, ".local", "share", "pfm", "install", "bin", "claude")
	if _, err := os.Lstat(stagedClaude); !os.IsNotExist(err) {
		t.Fatalf("dry run staged assets: %v", err)
	}
	if content := readFixture(t, bbTarget); content != "operator copy\n" {
		t.Fatalf("dry run changed bb.md: %q", content)
	}

	var applied bytes.Buffer
	report, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Now: now, Stdout: &applied, Runner: runner,
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
		for _, unit := range []string{reminderServiceUnit, reminderTimerUnit} {
			assertLink(t, filepath.Join(unitDir, unit), filepath.Join(managed, "systemd", unit))
		}
		assertLink(
			t,
			filepath.Join(unitDir, "timers.target.wants", reminderTimerUnit),
			filepath.Join(unitDir, reminderTimerUnit),
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
	// F1: host overlays are materialized under the managed root and symlinked
	// at their contracted ~/.local/bin names, including the Claude launcher.
	managedClaude := filepath.Join(managed, "bin", "claude")
	assertLink(t, filepath.Join(home, ".local", "bin", "claude"), managedClaude)
	// The shim's own behaviour (the login-default CLAUDE_CONFIG_DIR, then the
	// native exec) is pinned in launcher_test.go; install materializes it verbatim.
	wantClaude, err := readAsset("bin/claude")
	if err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, managedClaude); got != string(wantClaude) {
		t.Fatalf("managed claude=%q, want the shipped shim %q", got, wantClaude)
	}
	for name, command := range map[string]string{"pfm-statusline": "statusline", "tmux-title-renudge": "tmux-title-renudge"} {
		managedShim := filepath.Join(managed, "bin", name)
		assertLink(t, filepath.Join(home, ".local", "bin", name), managedShim)
		if info, err := os.Stat(managedShim); err != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("managed %s mode=%v err=%v, want 0o755", name, info, err)
		}
		shim := readFixture(t, managedShim)
		want := "#!/usr/bin/env bash\n" + `exec "$HOME/.local/bin/pfm" internal ` + command + ` "$@"` + "\n"
		if shim != want {
			t.Fatalf("managed %s=%q, want native exec shim %q", name, shim, want)
		}
	}
	if settings := readFixture(t, filepath.Join(config, "settings.json")); !strings.Contains(settings, "bb-hook.sh") {
		t.Fatalf("install changed account settings: %s", settings)
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
	if secondary := readFixture(t, secondarySettings); !strings.Contains(secondary, "secondary-keep") {
		t.Fatalf("install changed secondary settings: %s", secondary)
	}
	if zshrc := readFixture(
		t,
		filepath.Join(home, ".zshrc"),
	); !strings.Contains(
		zshrc,
		sourceLine(filepath.Join(clone, "pfm", "internal", "installer", "assets", "shim", "pfm.zsh")),
	) ||
		strings.Contains(zshrc, "cc-fleet.zsh") {
		t.Fatalf("zshrc was not converged:\n%s", zshrc)
	}

	state := fleetdb.OpenSharedState(context.Background(), paths.Values{
		Home: home, StateDB: filepath.Join(home, ".local", "state", "pfm", "pfm.db"),
	})
	killed, err := state.KilledAt(context.Background())
	closeErr := state.Close()
	if err != nil || closeErr != nil || len(killed) != 2 || killed["killed-a"] != 99 || killed["killed-b"] != 0 {
		t.Fatalf("migrated killed=%v err=%v close=%v", killed, err, closeErr)
	}

	var second bytes.Buffer
	secondReport, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Now: now, Stdout: &second, Runner: runner,
	})
	if err != nil || secondReport.Changed != 0 {
		t.Fatalf("second apply report=%#v err=%v\n%s", secondReport, err, second.String())
	}

	var removed bytes.Buffer
	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeUninstall, Home: home, Now: now, Stdout: &removed, Runner: runner,
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
		filepath.Join(unitDir, "timers.target.wants", reminderTimerUnit),
		filepath.Join(unitDir, reminderServiceUnit),
		filepath.Join(unitDir, reminderTimerUnit),
	} {
		if _, err := os.Lstat(removed); !os.IsNotExist(err) {
			t.Fatalf("uninstall left enablement link at %s: %v", removed, err)
		}
	}
}

func TestEmptyCodexRosterSkipsCommandAndAgentMirrors(t *testing.T) {
	t.Parallel()
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
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeDryRun, Home: home, Stdout: &output, Runner: &fakeRunner{}, CodexHomes: []string{},
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

func TestMCPEnablementSurvivesAnUnavailableSystemdUserManagerAndDisableRemovesIt(t *testing.T) {
	t.Parallel()
	if schedulerIsLaunchd {
		t.Skip("systemd enablement is not installed on launchd hosts")
	}
	home := t.TempDir()
	wants := filepath.Join(home, ".config", "systemd", "user", "default.target.wants", mcpUnitName)
	options := Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Runner: &fakeRunner{},
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
	t.Parallel()
	home := t.TempDir()
	managed := filepath.Join(home, ".local", "share", "pfm", "install")
	staleLaunchd := filepath.Join(managed, "launchd", "com.professor.pfm.mcp.plist")
	writeFixture(t, staleLaunchd, "stale staged plist\n")
	installer := engine{
		options: Options{
			MCPConfigPath: testConfigPath(t),
			Mode:          ModeApply,
			Home:          home,
			MCPEnabled:    map[string]bool{"chat": false},
			Stdout:        io.Discard,
		},
		apply:       true,
		managedRoot: managed,
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
	t.Parallel()
	home := t.TempDir()
	options := Options{MCPConfigPath: testConfigPath(t), Mode: ModeApply, Home: home, Runner: &fakeRunner{}}
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

// TestWireCodexAgentsInstallsTheTwoShapesEachEngineLoads pins the install call
// site's two promises apart: Claude's agent is a symlink to the clone, while
// Codex — whose loader opens a role with O_NOFOLLOW — gets a REGULAR FILE
// carrying the generated marker that proves pfm owns it.
func TestWireCodexAgentsInstallsTheTwoShapesEachEngineLoads(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeFixture(t, filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md"),
		"---\nname: alpha\ndescription: Alpha role for testing.\n---\n\nbody\n")

	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}

	assertLink(t,
		filepath.Join(home, ".claude", "agents", "alpha.md"),
		filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.md"))
	assertOwnedCodexRole(t, filepath.Join(home, ".codex", "agents", "alpha.toml"))
	twin := filepath.Join(home, ".professor", "templates", "global", "agents", "alpha.toml")
	if _, err := os.Lstat(twin); !os.IsNotExist(err) {
		t.Fatalf("install wrote a .toml twin inside the source clone, lstat err=%v", err)
	}
}

// TestWireCodexAgentsReportsAndPreservesAForeignConflict is the conflict-law
// pin at the installer boundary: a symlink pointing entirely outside the
// source repository is reported by the exact "CONFLICT ...: not ours"
// wording and is never overwritten or deleted — and, critically, a conflict
// never aborts the rest of the install.
func TestWireCodexAgentsReportsAndPreservesAForeignConflict(t *testing.T) {
	t.Parallel()
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
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Stdout: &output, Runner: &fakeRunner{},
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
// file symlink, a directory entry (tools/) links as ONE whole-directory
// symlink — never a copy of its contents.
func TestWireGlobalCommandsLinksFilesAndDirectories(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	source := filepath.Join(home, ".professor", "templates", "global", "commands")
	writeFixture(t, filepath.Join(source, "git.md"), "# git command\n")
	writeFixture(t, filepath.Join(source, "tools", "refine.md"), "# tools refine\n")

	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}

	assertLink(t, filepath.Join(home, ".claude", "commands", "git.md"), filepath.Join(source, "git.md"))
	assertLink(t, filepath.Join(home, ".claude", "commands", "tools"), filepath.Join(source, "tools"))
}

// TestWireGlobalCommandsSkipsAnAbsentOrEmptySource pins the spec's explicit
// carve-out: a parallel lane may not have populated templates/global/commands
// yet, and that is a reported skip (0 entries), never an error.
func TestWireGlobalCommandsSkipsAnAbsentOrEmptySource(t *testing.T) {
	t.Parallel()
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
				MCPConfigPath: testConfigPath(t),
				Mode:          ModeApply, Home: home, Stdout: &output, Runner: &fakeRunner{},
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

// TestWireGlobalSkillsLinksTemplateSkillDirectories pins the second half of
// the skills registry: every directory shipped under templates/global/skills/
// becomes ONE whole-directory link in {Home}/.claude/skills, while the
// sources.json registry beside them — a file, not a skill — is never linked.
func TestWireGlobalSkillsLinksTemplateSkillDirectories(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	source := filepath.Join(home, ".professor", "templates", "global", "skills")
	writeFixture(t, filepath.Join(source, "sources.json"), "{}\n")
	writeFixture(t, filepath.Join(source, "architecture-design", "SKILL.md"), "# architecture-design skill\n")

	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Runner: &fakeRunner{},
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
	t.Parallel()
	home := t.TempDir()
	source := filepath.Join(home, ".professor", "templates", "global", "skills")
	if err := os.MkdirAll(filepath.Join(source, "half-built"), 0o700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Stdout: &output, Runner: &fakeRunner{},
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
	t.Parallel()
	home := t.TempDir()
	explorer := filepath.Join(home, ".codex", "agents", "explorer.toml")
	writeFixture(t, explorer, "stale explorer agent\n")
	backup := explorer + ".bak-20300101"
	writeFixture(t, backup, "stale backup\n")
	keeper := filepath.Join(home, ".codex", "agents", "tracer.toml")
	writeFixture(t, keeper, "keeper\n")

	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Runner: &fakeRunner{},
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
	t.Parallel()
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
			Options{MCPConfigPath: testConfigPath(t), Mode: ModeApply, Home: home, Runner: &fakeRunner{}},
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
			Options{MCPConfigPath: testConfigPath(t), Mode: ModeApply, Home: home, Runner: &fakeRunner{}},
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
			Options{MCPConfigPath: testConfigPath(t), Mode: ModeApply, Home: home, Runner: &fakeRunner{}},
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
			Options{MCPConfigPath: testConfigPath(t), Mode: ModeApply, Home: home, Runner: &fakeRunner{}},
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
			Options{MCPConfigPath: testConfigPath(t), Mode: ModeApply, Home: home, Runner: &fakeRunner{}},
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
	t.Parallel()
	home := t.TempDir()
	elsewhere := t.TempDir()
	writeFixture(t, filepath.Join(elsewhere, "templates", "global", "skills", "pcm", "SKILL.md"), "# pcm skill\n")
	writeFixture(
		t,
		filepath.Join(home, ".professor", "templates", "global", "skills", "pcm", "SKILL.md"),
		"# wrong pcm skill\n",
	)

	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, SourceRepo: elsewhere, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	assertLink(t,
		filepath.Join(home, ".claude", "skills", "pcm"),
		filepath.Join(elsewhere, "templates", "global", "skills", "pcm"))
}

// TestGlobalSourceRepoRootFallsBackToTheRecordedMarker pins the second rung:
// a later install that omits --source-repo entirely must still resolve
// through the marker the first install recorded, not silently reset to the
// {Home}/.professor default.
func TestGlobalSourceRepoRootFallsBackToTheRecordedMarker(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	elsewhere := t.TempDir()
	writeFixture(t, filepath.Join(elsewhere, "templates", "global", "skills", "pcm", "SKILL.md"), "# pcm skill\n")

	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, SourceRepo: elsewhere, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(home, ".claude", "skills", "pcm")); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	assertLink(t,
		filepath.Join(home, ".claude", "skills", "pcm"),
		filepath.Join(elsewhere, "templates", "global", "skills", "pcm"))
}

func TestDryRunNamesFutureCodexWritesAndRefusesTheirConflicts(t *testing.T) {
	t.Parallel()
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
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeDryRun, Home: home, Stdout: &output, Runner: &fakeRunner{},
	})
	if err == nil || !strings.Contains(err.Error(), "CONFLICT "+conflict) {
		t.Fatalf("dry-run error=%v, want future Codex conflict; output:\n%s", err, output.String())
	}
	for _, path := range []string{
		conflict,
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
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Stdout: &output, Runner: &fakeRunner{},
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

func TestUnitTransitionsUseOnlyTheInjectedManager(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	runner := &fakeRunner{manager: true}
	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Runner: runner,
	}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.calls, "\n")
	wantCalls := []string{
		nameSyncStateProbe,
		"systemctl --user daemon-reload",
		"systemctl --user enable --now pfm-name-sync.path " + nameSyncTimerUnit + " " + reminderTimerUnit,
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
	t.Parallel()
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

func TestShimSourceUsesRecordedCloneAndKeepsLegacyPosition(t *testing.T) {
	home := t.TempDir()
	clone := t.TempDir()
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	legacy := sourceLine(filepath.Join(home, ".local", "share", "pfm", "install", "shim", "pfm.zsh"))
	zshrc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(zshrc, []byte("before\n"+legacy+"\nafter\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	installer := &engine{
		options: Options{Home: home, Mode: ModeApply, Stdout: io.Discard},
		apply:   true, stamp: "test", managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
	}
	if err := installer.wireShell(false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(zshrc)
	if err != nil {
		t.Fatal(err)
	}
	want := sourceLine(filepath.Join(clone, "pfm", "internal", "installer", "assets", "shim", "pfm.zsh"))
	if !strings.Contains(string(raw), "before\n# The shell launchers delegate to the pfm engine.\n"+want+"\nafter") {
		t.Fatalf("zshrc=%q", raw)
	}
}

func TestShimSourceSkipsWithoutRecordedClone(t *testing.T) {
	home := t.TempDir()
	zshrc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(zshrc, []byte("untouched\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	installer := &engine{
		options: Options{Home: home, Mode: ModeApply, Stdout: io.Discard},
		apply:   true, stamp: "test", managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
	}
	if err := installer.wireShell(false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(zshrc)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "untouched\n" {
		t.Fatalf("zshrc=%q", raw)
	}
	if installer.report.Skipped == 0 {
		t.Fatal("missing skip report")
	}
}

func TestShimUnwireRemovesLegacyAndCloneLinesWithoutMarker(t *testing.T) {
	for _, source := range []string{
		"/fixture/home/.local/share/pfm/install/shim/pfm.zsh",
		"/fixture/clone/pfm/internal/installer/assets/shim/pfm.zsh",
	} {
		t.Run(source, func(t *testing.T) {
			home := t.TempDir()
			writeFixture(t, paths.SourceRepoPath(home), filepath.Join(home, "missing-clone")+"\n")
			zshrc := filepath.Join(home, ".zshrc")
			writeFixture(t, zshrc, "export EDITOR=vim\n"+sourceLine(source)+"\n")
			installer := &engine{options: Options{Home: home, Stdout: io.Discard}, apply: true, stamp: "test"}
			if err := installer.wireShell(true); err != nil {
				t.Fatal(err)
			}
			if got := readFixture(t, zshrc); got != "export EDITOR=vim\n" {
				t.Fatalf("zshrc=%q", got)
			}
		})
	}
}

// The rewriter and the early-call scan must never disagree about where the
// source line is: one decides where the launchers start existing, the other
// reports what runs before they do.
func TestEarlyCallScanStopsWhereTheRewriterFindsTheSourceLine(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	recordFixtureSourceRepo(t, home, t.TempDir())
	zshrc := filepath.Join(home, ".zshrc")
	writeFixture(t, zshrc, "cc\nsource /old/cc-fleet.zsh\ncc-ls\n")

	var output bytes.Buffer
	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Stdout: &output, Runner: &fakeRunner{},
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

func (runner *outputRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "systemctl" {
		return runner.fakeRunner.Output(ctx, name, args...)
	}
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
	t.Parallel()
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
				MCPConfigPath: testConfigPath(t),
				Mode:          ModeApply, Home: t.TempDir(), Stdout: io.Discard, Runner: runner,
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
	t.Parallel()
	if !schedulerIsLaunchd {
		t.Skip("launch-agent gate is macOS-only")
	}
	var output bytes.Buffer
	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: t.TempDir(), Stdout: &output, Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "launch-agent gate NOT probed") {
		t.Fatalf("an unprobed gate was silent:\n%s", output.String())
	}
}

func recordFixtureSourceRepo(t *testing.T, home, repo string) {
	t.Helper()
	if err := paths.WriteSourceRepoMarker(home, repo); err != nil {
		t.Fatal(err)
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

func TestMigrateLegacyCarrierUsesConfiguredStateDB(t *testing.T) {
	home := t.TempDir()
	writeFixture(t, filepath.Join(home, ".claude", ".cc-ls-hidden"), "retired-chat\n")
	statePath := filepath.Join(home, "operator-state", "pfm.db")
	installer := &engine{
		options: Options{
			MCPConfigPath: testConfigPath(t),
			Mode:          ModeApply,
			Home:          home,
			StateDB:       statePath,
			Stdout:        io.Discard,
		},
		apply: true,
	}
	if err := installer.migrateLegacyCarrier(context.Background()); err != nil {
		t.Fatal(err)
	}
	state := fleetdb.OpenSharedState(context.Background(), paths.Values{StateDB: statePath})
	killed, err := state.KilledAt(context.Background())
	closeErr := state.Close()
	if err != nil || closeErr != nil || killed["retired-chat"] != 0 || len(killed) != 1 {
		t.Fatalf("configured state killed=%v err=%v close=%v", killed, err, closeErr)
	}
}

func TestInstallerChangeWritesAndReports(t *testing.T) {
	failure := errors.New("write failed")
	for _, test := range []struct {
		name  string
		apply bool
		err   error
	}{
		{name: "apply", apply: true},
		{name: "action error", apply: true, err: failure},
		{name: "preview"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "asset")
			writeFixture(t, path, "original\n")
			var output bytes.Buffer
			installer := &engine{options: Options{Stdout: &output}, apply: test.apply}
			err := installer.change("write "+path, func() error {
				if err := os.WriteFile(path, []byte("installed\n"), 0o600); err != nil {
					return err
				}
				return test.err
			})
			if !errors.Is(err, test.err) {
				t.Fatalf("change error=%v, want %v", err, test.err)
			}
			if got, want := output.String(), "  change  write "+path+"\n"; got != want {
				t.Fatalf("output=%q, want %q", got, want)
			}
			want := "original\n"
			if test.apply {
				want = "installed\n"
			}
			if got := readFixture(t, path); got != want {
				t.Fatalf("asset=%q, want %q", got, want)
			}
			if installer.report.Changed != 1 {
				t.Fatalf("changed=%d, want one change", installer.report.Changed)
			}
		})
	}
}

func TestEnsureLinkRefusesUnpublishedManagedAsset(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(managedRootForHome(home), "reload.command.md")
	target := filepath.Join(home, "commands", "reload.md")
	symlinkFixture(t, "old-owned-source", target)
	e := &engine{
		apply:       true,
		managedRoot: managedRootForHome(home),
		options:     Options{Home: home, Stdout: &bytes.Buffer{}},
	}
	if _, err := e.ensureLink(source, target); err == nil {
		t.Fatal("registry linked an unpublished managed asset")
	}
	assertLink(t, target, filepath.Join(filepath.Dir(target), "old-owned-source"))
}

func stageVSCodeExtensionFixture(t *testing.T, managedRoot string) {
	t.Helper()
	for _, name := range []string{"package.json", "extension.js"} {
		asset := vscodeExtensionSource + "/" + name
		content, err := readAsset(asset)
		if err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(managedRoot, filepath.FromSlash(asset)), string(content))
	}
}

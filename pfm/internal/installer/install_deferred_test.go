package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestInstallDeferredTrustFailureLandsLaterSteps(t *testing.T) {
	home := t.TempDir()
	account, binary, _ := stageResumeUnkillAccount(t, home)
	writeFixture(t, filepath.Join(account, "hooks.json"), `{"hooks":{}}`)
	script, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	// The discovery answers no handler; initialization and the write reply
	// retain the same native API exchange as the successful account fixture.
	lines := strings.Split(string(script), "\n")
	for index, line := range lines {
		if strings.Contains(line, `printf '{"id":1,"result":{"data"`) {
			lines[index] = `      printf '%s\n' '{"id":1,"result":{"data":[]}}' ;;`
		}
	}
	if err := testjail.WriteExecutable(binary, []byte(strings.Join(lines, "\n")), 0o700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	_, err = Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: &output,
		CodexHomes: []string{account}, CodexBinary: binary, MCPConfigPath: testConfigPath(t),
	})
	if err == nil || !strings.Contains(err.Error(), "(found 0)") {
		t.Fatalf("install error=%v, want failed discovery", err)
	}
	for _, line := range []string{
		"  change  trust resume-unkill hook " + account + "\n",
		"  FAIL    Codex hook trust for " + account + ": ", "log default:", "zshrc",
	} {
		if !strings.Contains(output.String(), line) {
			t.Errorf("output missing %q:\n%s", line, output.String())
		}
	}
	if _, err := os.Stat(filepath.Join(managedRootForHome(home), binaryOwnershipName)); err != nil {
		t.Fatalf("later metadata did not land: %v", err)
	}
}

func TestInstallDeferredPluginFailureSurvivesLaterLedgerError(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	store := filepath.Join(home, ".claude")
	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{manager: true}, Stdout: &output,
		MCPConfigPath: testConfigPath(t), MCPEnabled: map[string]bool{"chat": true},
		ClaudeRosterHost: true, PrimaryConfigDir: store,
		Sleep: func(time.Duration) {
			writeFixture(t, filepath.Join(managedRootForHome(home), vscodeOwnershipName), "{")
		},
	})
	failure := "claude plugins in " + store + ": "
	if err == nil || !strings.Contains(err.Error(), failure) ||
		!strings.Contains(err.Error(), "read VS Code ownership ") {
		t.Fatalf("install error=%v, want plugin and VS Code ledger failures", err)
	}
	if !strings.Contains(output.String(), "  FAIL    "+failure) {
		t.Fatalf("plugin failure was not printed:\n%s", output.String())
	}
}

func TestInstallDeferredRestartFailureUsesFAILAndLandsLaterSteps(t *testing.T) {
	if schedulerIsLaunchd {
		return
	}
	home := t.TempDir()
	_, recorder := obs.Test(t)
	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{manager: true, mcpState: "failed"}, Stdout: &output,
		MCPConfigPath: testConfigPath(t), MCPEnabled: map[string]bool{"chat": true}, Sleep: func(time.Duration) {},
	})
	want := "fleet unit pfm-mcp.service is failed 3s after start — journalctl --user -u pfm-mcp.service -n 20"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("install error=%v, want %q", err, want)
	}
	if !strings.Contains(output.String(), "  FAIL    "+want+"\n") || strings.Contains(output.String(), "  fail    ") ||
		!strings.Contains(output.String(), "zshrc") {
		t.Fatalf("restart failure or later step not reported:\n%s", output.String())
	}
	requireInstallerFailureRecord(t, recorder, want)
}

type installEnableRunner struct {
	*fakeRunner
	failEnable bool
	timerState string
}

func (runner *installEnableRunner) Run(ctx context.Context, name string, args ...string) error {
	err := runner.fakeRunner.Run(ctx, name, args...)
	if runner.failEnable && name+" "+strings.Join(args, " ") ==
		"systemctl --user enable --now pfm-name-sync.path pfm-name-sync.timer pfm-reminder.timer" {
		return errors.New("exit 1")
	}
	return err
}

func (runner *installEnableRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	call := name + " " + strings.Join(args, " ")
	for _, unit := range []string{nameSyncPathUnit, nameSyncTimerUnit, reminderTimerUnit} {
		if call == "systemctl --user show --property=ActiveState --value "+unit {
			runner.calls = append(runner.calls, call)
			if unit == reminderTimerUnit {
				return []byte(runner.timerState + "\n"), nil
			}
			return []byte("active\n"), nil
		}
	}
	return runner.fakeRunner.Output(ctx, name, args...)
}

func TestInstallDeferredEnableFailureLandsLaterSteps(t *testing.T) {
	if schedulerIsLaunchd {
		return
	}
	home := t.TempDir()
	_, recorder := obs.Test(t)
	var output bytes.Buffer
	runner := &installEnableRunner{fakeRunner: &fakeRunner{manager: true}, failEnable: true, timerState: "inactive"}
	_, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: runner, Stdout: &output, MCPConfigPath: testConfigPath(t),
	})
	want := "systemctl --user enable --now pfm-name-sync.path pfm-name-sync.timer pfm-reminder.timer: exit 1"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("install error=%v, want %q", err, want)
	}
	if !strings.Contains(output.String(), "  FAIL    "+want+"\n") || !strings.Contains(output.String(), "zshrc") {
		t.Fatalf("enable failure or later step not reported:\n%s", output.String())
	}
	if _, err := os.Stat(filepath.Join(managedRootForHome(home), binaryOwnershipName)); err != nil {
		t.Fatalf("later metadata did not land: %v", err)
	}
	requireInstallerFailureRecord(t, recorder, want)
}

func requireInstallerFailureRecord(t *testing.T, recorder *obs.Recorder, want string) {
	t.Helper()
	for _, record := range recorder.Records() {
		decision, _ := record.Field("decision")
		step, _ := record.Field("step")
		if record.Message == "installer.step" && decision == "fail" && step == want {
			if value, _ := record.Field(obs.FieldErr); value == nil || value == "" {
				t.Fatal("failure record has no error context")
			}
			return
		}
	}
	t.Fatalf("no failure record for %q: %s", want, recorder.Raw())
}

func TestInstallDeferredRerunRepairsInactiveTimerAndKeepsHealthyUnits(t *testing.T) {
	if schedulerIsLaunchd {
		return
	}
	for _, state := range []string{"inactive", "active"} {
		t.Run(state, func(t *testing.T) {
			home := t.TempDir()
			runner := &installEnableRunner{fakeRunner: &fakeRunner{manager: true}, timerState: "active"}
			var output bytes.Buffer
			options := Options{
				Mode:          ModeApply,
				Home:          home,
				Runner:        runner,
				Stdout:        &output,
				MCPConfigPath: testConfigPath(t),
			}
			if _, err := Run(context.Background(), options); err != nil {
				t.Fatal(err)
			}
			runner.calls, runner.timerState = nil, state
			output.Reset()
			if _, err := Run(context.Background(), options); err != nil {
				t.Fatal(err)
			}
			calls := strings.Join(runner.calls, "\n")
			enable := "systemctl --user enable --now pfm-name-sync.path pfm-name-sync.timer pfm-reminder.timer"
			if state == "inactive" {
				if !strings.Contains(calls, "systemctl --user daemon-reload") || !strings.Contains(calls, enable) {
					t.Fatalf("inactive timer not retried:\n%s", calls)
				}
			} else if strings.Contains(calls, enable) {
				t.Fatalf("healthy units enabled again:\n%s", calls)
			}
		})
	}
}

func TestUninstallPreflightDamagedLoginFenceKeepsInstallation(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, ".config", "Code", "User", "settings.json")
	writeFixture(t, settings, `{}`)
	var output bytes.Buffer
	options := Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: &output, MCPConfigPath: testConfigPath(t),
		SourceRepo: t.TempDir(), VSCode: true, vscodePlatform: "linux", vscodeSettingsPaths: []string{settings},
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(home, ".profile"), loginDefaultTestBegin+"\n"+loginDefaultTestBegin+"\n")
	options.Mode = ModeUninstall
	output.Reset()
	_, err := Run(context.Background(), options)
	if err == nil || !strings.Contains(err.Error(), "two pfm claude-config-dir begin markers") ||
		!strings.Contains(err.Error(), "preflight uninstall plan") {
		t.Fatalf("uninstall error=%v output=%s, want damaged-fence failure", err, output.String())
	}
	for _, name := range []string{"source-repo", binaryOwnershipName} {
		if _, err := os.Stat(filepath.Join(managedRootForHome(home), name)); err != nil {
			t.Errorf("preflight refusal removed installation metadata %s: %v", name, err)
		}
	}
	if got := readFixture(t, settings); !strings.Contains(got, `"PFM"`) {
		t.Fatalf("preflight refusal changed VS Code installation:\n%s", got)
	}
}

func TestInstallDeferredConfigSeedDryRunAndApply(t *testing.T) {
	for _, mode := range []Mode{ModeDryRun, ModeApply} {
		t.Run(map[Mode]string{ModeDryRun: "dry run", ModeApply: "apply"}[mode], func(t *testing.T) {
			home := t.TempDir()
			example, target := filepath.Join(t.TempDir(), "example.json"), filepath.Join(home, "cfg", "pfm.config.json")
			writeFixture(t, example, `{"version":2}`)
			var output bytes.Buffer
			_, err := Run(context.Background(), Options{
				Mode:              mode,
				Home:              home,
				Runner:            &fakeRunner{},
				Stdout:            &output,
				ConfigSeed:        example,
				ConfigSeedContent: []byte(`{"version":2}`),
				MCPConfigPath:     target,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), "  change  seed "+target+" from "+example+"\n") ||
				!strings.Contains(
					output.String(),
					"  change  write log default ",
				) || !strings.Contains(output.String(), " into "+target+"\n") {
				t.Fatalf("seed and log rows missing:\n%s", output.String())
			}
			if mode == ModeDryRun {
				entries, err := os.ReadDir(home)
				if err != nil || len(entries) != 0 {
					t.Fatalf("dry run wrote files: entries=%v err=%v", entries, err)
				}
				return
			}
			info, err := os.Stat(target)
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("seed mode: info=%v err=%v", info, err)
			}
			var config map[string]any
			if err := json.Unmarshal([]byte(readFixture(t, target)), &config); err != nil || config["log"] == nil {
				t.Fatalf("seed has no log block: config=%v err=%v", config, err)
			}
		})
	}
}

func TestInstallDeferredSchedulerRefusalPrecedesSeed(t *testing.T) {
	home := t.TempDir()
	example, target := filepath.Join(t.TempDir(), "example.json"), filepath.Join(home, "cfg", "pfm.config.json")
	writeFixture(t, example, `{"version":2}`)
	_, err := Run(context.Background(), Options{
		Mode:              ModeApply,
		Home:              home,
		Runner:            &fakeRunner{nameSyncActive: true},
		ConfigSeed:        example,
		ConfigSeedContent: []byte(`{"version":2}`),
		MCPConfigPath:     target,
	})
	if !errors.Is(err, ErrNameSyncRunning) {
		t.Fatalf("install error=%v, want name-sync refusal", err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("seed written before refusal: %v", err)
	}
}

func TestInstallDeferredReminderWaitThroughRun(t *testing.T) {
	home := t.TempDir()
	var output bytes.Buffer
	var slept time.Duration
	_, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{nameSyncIdle: true, reminderActive: true}, Stdout: &output,
		MCPConfigPath: testConfigPath(t), Sleep: func(d time.Duration) { slept += d },
	})
	if !errors.Is(err, ErrReminderRunning) || slept != 90*time.Second ||
		!strings.Contains(
			output.String(),
			"  wait    the pfm reminder is delivering; waiting up to 1m30s for it to finish\n",
		) {
		t.Fatalf("wait slept=%s error=%v output=%s", slept, err, output.String())
	}
}

func TestInstallDeferredUnprobedGateUsesSharedNote(t *testing.T) {
	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: t.TempDir(), Runner: &fakeRunner{}, Stdout: &output, MCPConfigPath: testConfigPath(t),
	})
	if err != nil || !strings.Contains(output.String(), "  skip    "+nameSyncGateUnprobedNote+"\n") {
		t.Fatalf("install error=%v output=%s", err, output.String())
	}
}

func TestUninstallDeferredPrunesClaudeAndRemovesCodexHookLedger(t *testing.T) {
	home := t.TempDir()
	account, _, _ := stageResumeUnkillAccount(t, home)
	options := Options{
		Mode:          ModeApply,
		Home:          home,
		CodexHomes:    []string{account},
		Runner:        &fakeRunner{},
		MCPConfigPath: testConfigPath(t),
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	ledgerPath := settingsHookOwnershipPath(managedRootForHome(home))
	ledger, _, err := readSettingsHookOwnership(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	ledger[filepath.Join(home, ".claude", "settings.json")] = settingsHookCounts{
		settingsHookKey{Event: "SessionStart", Matcher: "resume", Command: resumeUnkillCommand(home)}: 1,
	}
	encoded, err := encodeSettingsHookOwnership(ledger)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, ledgerPath, string(encoded))
	options.Mode = ModeUninstall
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ledgerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstall ledger remains: %v", err)
	}
}

func TestInstallDeferredApplyFailuresLandLaterSteps(t *testing.T) {
	for _, scenario := range []string{"managed", "hooks", "mcp"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			var output bytes.Buffer
			options, err := normalizeInstallerOptions(
				Options{
					Mode:                  ModeApply,
					Home:                  home,
					Runner:                &fakeRunner{},
					Stdout:                &output,
					MCPConfigPath:         testConfigPath(t),
					RequireManagedCleanup: scenario == "managed",
					ManagedSettingsDir:    filepath.Join(home, "managed"),
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "managed" {
				options.writeManaged = func(string, []byte) error { return errors.New("disk full") }
			}
			if scenario == "hooks" {
				options.CodexHomes = []string{filepath.Join(home, "codex")}
				writeFixture(t, settingsHookOwnershipPath(managedRootForHome(home)), "{")
			}
			if scenario == "mcp" {
				options.MCPEnabled = map[string]bool{"chat": true}
				options.MCPConfigPath = filepath.Join(home, "invalid-config")
				writeFixture(t, options.MCPConfigPath, "{")
			}
			e := &engine{apply: true, stamp: "test", managedRoot: managedRootForHome(home), options: options}
			err = e.install(context.Background())
			if err == nil && len(e.deferred) == 0 {
				t.Fatal("apply failure must be returned or aggregated")
			}
			if _, err := os.Stat(filepath.Join(managedRootForHome(home), binaryOwnershipName)); err != nil {
				t.Fatalf("later metadata did not land after %s: %v\n%s", scenario, err, output.String())
			}
		})
	}
}

func TestUninstallPreflightRefusesOwnedHookBeforeTeardown(t *testing.T) {
	home := t.TempDir()
	old := filepath.Join(home, "retired", "hooks.json")
	key := settingsHookKey{Event: "SessionStart", Matcher: "resume", Command: resumeUnkillCommand(home)}
	raw, err := encodeSettingsHookOwnership(map[string]settingsHookCounts{old: {key: 1}})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, settingsHookOwnershipPath(managedRootForHome(home)), string(raw))
	keep := filepath.Join(home, ".local", "bin", "claude")
	symlinkFixture(t, "operator-launcher", keep)
	_, err = Run(
		context.Background(),
		Options{
			Mode:          ModeUninstall,
			Home:          home,
			Runner:        &fakeRunner{},
			MCPConfigPath: testConfigPath(t),
			Stdout:        &bytes.Buffer{},
		},
	)
	if err == nil || !strings.Contains(err.Error(), "preflight uninstall plan") {
		t.Fatalf("known hook refusal reached teardown: %v", err)
	}
	assertLink(t, keep, filepath.Join(filepath.Dir(keep), "operator-launcher"))
}

func TestFailedAssetStageDoesNotWireDependentRegistries(t *testing.T) {
	home := t.TempDir()
	managed := managedRootForHome(home)
	writer := &storeMutationWriter{match: "  change  write " + managed, mutate: func() {
		if err := os.RemoveAll(managed); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, managed, "blocked")
	}}
	_, err := Run(
		context.Background(),
		Options{Mode: ModeApply, Home: home, Runner: &fakeRunner{}, MCPConfigPath: testConfigPath(t), Stdout: writer},
	)
	if err == nil {
		t.Fatal("staging failure was hidden")
	}
	for _, path := range []string{filepath.Join(home, ".claude", "commands", "reload.md"), filepath.Join(home, ".claude", "skills", "handoff", "SKILL.md")} {
		if target, err := os.Readlink(path); err == nil {
			t.Errorf("failed asset stage published dependent link %s -> %s", path, target)
		}
	}
}

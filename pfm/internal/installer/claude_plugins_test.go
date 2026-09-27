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

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// pluginCall is one argv the fake runner saw, with the account it ran for.
type pluginCall struct {
	argv      string
	configDir string
}

// pluginRunner records every Run and answers from fail, keyed by
// "<CLAUDE_CONFIG_DIR> <argv[1:]>". Start is never reached by this step.
type pluginRunner struct {
	deps.Runner
	calls []pluginCall
	fail  map[string]string
}

func (runner *pluginRunner) Run(_ context.Context, argv []string, options deps.RunOptions) (deps.RunResult, error) {
	configDir := ""
	for _, entry := range options.Env {
		if value, ok := strings.CutPrefix(entry, claudeConfigDirEnv+"="); ok {
			configDir = value
		}
	}
	joined := strings.Join(argv[1:], " ")
	runner.calls = append(runner.calls, pluginCall{argv: joined, configDir: configDir})
	if stderr, failed := runner.fail[configDir+" "+joined]; failed {
		return deps.RunResult{ExitCode: 1, Stderr: []byte(stderr)}, nil
	}
	return deps.RunResult{}, nil
}

func (*pluginRunner) LookPath(name string) (string, error) {
	return "", errors.New("pluginRunner: no " + name)
}

func pluginFixture(t *testing.T) (home, binary, first, second string) {
	t.Helper()
	home = t.TempDir()
	binary = writeScript(t, t.TempDir(), "claude-real", "#!/bin/sh\nexit 0\n")
	first = filepath.Join(home, ".claude")
	second = filepath.Join(home, ".cc", "2")
	for _, dir := range []string{first, second} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return home, binary, first, second
}

func pluginEngine(home, binary, first, second string, runner deps.Runner, out *bytes.Buffer, apply bool) *engine {
	return &engine{options: Options{
		Home:          home,
		ConfigDir:     first,
		ConfigDirs:    []string{second},
		ClaudeBinary:  binary,
		ProcessRunner: runner,
		Stdout:        out,
	}, apply: apply}
}

func TestEnsureClaudePluginsRunsAddThenInstallPerAccount(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	runner := &pluginRunner{}
	var out bytes.Buffer
	if err := pluginEngine(home, binary, first, second, runner, &out, true).ensureClaudePlugins(
		context.Background(),
	); err != nil {
		t.Fatalf("ensureClaudePlugins: %v\n%s", err, out.String())
	}
	var want []pluginCall
	for _, dir := range []string{first, second} {
		for _, plugin := range claudePlugins {
			want = append(want,
				pluginCall{argv: "plugin marketplace add " + plugin.Source, configDir: dir},
				pluginCall{argv: "plugin install " + plugin.ID + " -y", configDir: dir},
			)
		}
	}
	if len(runner.calls) != len(want) {
		t.Fatalf("calls=%v, want %v", runner.calls, want)
	}
	for index := range want {
		if runner.calls[index] != want[index] {
			t.Fatalf("call %d=%v, want %v (all %v)", index, runner.calls[index], want[index], runner.calls)
		}
	}
}

func TestEnsureClaudePluginsSkipsAnAlreadyEnabledPlugin(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	enabled := `{"enabledPlugins":{"cache-live-control@cache-live-control":true,` +
		`"sub-agent-compact@sub-agent-compact":true}}`
	writeFixture(t, filepath.Join(first, "settings.json"), enabled)
	writeInstalledPlugins(t, first, claudePlugins[0].ID, claudePlugins[1].ID)
	runner := &pluginRunner{}
	var out bytes.Buffer
	if err := pluginEngine(home, binary, first, second, runner, &out, true).ensureClaudePlugins(
		context.Background(),
	); err != nil {
		t.Fatalf("ensureClaudePlugins: %v\n%s", err, out.String())
	}
	for _, call := range runner.calls {
		if call.configDir == first {
			t.Fatalf("already-enabled account ran %v\n%s", call, out.String())
		}
	}
	wantOK := "ok      claude plugin sub-agent-compact@sub-agent-compact installed and enabled in " + first
	if !strings.Contains(out.String(), wantOK) {
		t.Fatalf("no ok line for the enabled account:\n%s", out.String())
	}
	if len(runner.calls) != 2*len(claudePlugins) {
		t.Fatalf("second account calls=%v, want add+install per plugin", runner.calls)
	}
}

// writeInstalledPlugins writes dir's plugins/installed_plugins.json the way
// claude records an install: one entry per id whose installPath exists.
func writeInstalledPlugins(t *testing.T, dir string, ids ...string) {
	t.Helper()
	plugins := map[string]any{}
	for _, id := range ids {
		installPath := filepath.Join(dir, "plugins", "cache", id)
		if err := os.MkdirAll(installPath, 0o700); err != nil {
			t.Fatal(err)
		}
		plugins[id] = []any{map[string]any{"scope": "user", "installPath": installPath}}
	}
	raw, err := json.Marshal(map[string]any{"version": 2, "plugins": plugins})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, "plugins", "installed_plugins.json"), string(raw))
}

// Accounts share one settings.json through a symlink, so "enabled" there says
// nothing about this account: a plugin enabled but never installed in this
// account's config dir still gets installed.
func TestEnsureClaudePluginsInstallsWhereEnabledButNotInstalled(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	enabled := `{"enabledPlugins":{"cache-live-control@cache-live-control":true,` +
		`"sub-agent-compact@sub-agent-compact":true}}`
	writeFixture(t, filepath.Join(first, "settings.json"), enabled)
	writeFixture(t, filepath.Join(second, "settings.json"), enabled)
	writeInstalledPlugins(t, first, claudePlugins[0].ID, claudePlugins[1].ID)
	runner := &pluginRunner{}
	var out bytes.Buffer
	if err := pluginEngine(home, binary, first, second, runner, &out, true).ensureClaudePlugins(
		context.Background(),
	); err != nil {
		t.Fatalf("ensureClaudePlugins: %v\n%s", err, out.String())
	}
	installs := 0
	for _, call := range runner.calls {
		if call.configDir == first {
			t.Fatalf("the installed account ran %v\n%s", call, out.String())
		}
		if strings.HasPrefix(call.argv, "plugin install ") {
			installs++
		}
	}
	if installs != len(claudePlugins) {
		t.Fatalf(
			"installs in the uninstalled account=%d, want %d; calls=%v",
			installs,
			len(claudePlugins),
			runner.calls,
		)
	}
}

func TestClaudePluginsNotInstalledReadsTheAccountRecord(t *testing.T) {
	all := []string{claudePlugins[0].ID, claudePlugins[1].ID}
	for _, test := range []struct {
		name    string
		prepare func(t *testing.T, dir string)
		want    []string
		wantErr bool
	}{
		{name: "no record file", prepare: func(*testing.T, string) {}, want: all},
		{name: "both installed", prepare: func(t *testing.T, dir string) {
			writeInstalledPlugins(t, dir, all...)
		}, want: nil},
		{name: "install path gone", prepare: func(t *testing.T, dir string) {
			writeInstalledPlugins(t, dir, all...)
			if err := os.RemoveAll(filepath.Join(dir, "plugins", "cache", all[1])); err != nil {
				t.Fatal(err)
			}
		}, want: all[1:]},
		{name: "malformed record", prepare: func(t *testing.T, dir string) {
			writeFixture(t, filepath.Join(dir, "plugins", "installed_plugins.json"), "{")
		}, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			test.prepare(t, dir)
			got, err := ClaudePluginsNotInstalled(dir)
			if test.wantErr != (err != nil) {
				t.Fatalf("err=%v, wantErr=%t", err, test.wantErr)
			}
			if strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Fatalf("not installed=%v, want %v", got, test.want)
			}
		})
	}
}

func TestEnsureClaudePluginsFailureNamesTheAccountAndOthersContinue(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	runner := &pluginRunner{fail: map[string]string{
		first + " plugin marketplace add rezzminator/sub-agent-compact":  "marketplace unreachable",
		first + " plugin install sub-agent-compact@sub-agent-compact -y": "plugin not found",
	}}
	var out bytes.Buffer
	err := pluginEngine(home, binary, first, second, runner, &out, true).ensureClaudePlugins(context.Background())
	if err == nil || !strings.Contains(err.Error(), first) {
		t.Fatalf("err=%v, want a failure naming %s\n%s", err, first, out.String())
	}
	wantLine := "FAIL    claude plugin sub-agent-compact@sub-agent-compact in " + first
	if !strings.Contains(out.String(), wantLine) || !strings.Contains(out.String(), "plugin not found") {
		t.Fatalf("output missing %q with stderr:\n%s", wantLine, out.String())
	}
	secondRan := 0
	for _, call := range runner.calls {
		if call.configDir == second {
			secondRan++
		}
	}
	if secondRan != 2*len(claudePlugins) {
		t.Fatalf("second account ran %d commands after the first failed, want %d", secondRan, 2*len(claudePlugins))
	}
}

func TestEnsureClaudePluginsMarketplaceAlreadyAddedIsNotAFailure(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	runner := &pluginRunner{fail: map[string]string{
		first + " plugin marketplace add rezzminator/cache-live-control": "already installed",
	}}
	var out bytes.Buffer
	if err := pluginEngine(home, binary, first, second, runner, &out, true).ensureClaudePlugins(
		context.Background(),
	); err != nil {
		t.Fatalf("an already-added marketplace failed the step: %v\n%s", err, out.String())
	}
}

func TestEnsureClaudePluginsDryRunSaysWhatWouldRun(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	runner := &pluginRunner{}
	var out bytes.Buffer
	if err := pluginEngine(home, binary, first, second, runner, &out, false).ensureClaudePlugins(
		context.Background(),
	); err != nil {
		t.Fatalf("ensureClaudePlugins: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("dry run executed %v", runner.calls)
	}
	want := "change  run CLAUDE_CONFIG_DIR=" + second + " " + binary +
		" plugin marketplace add rezzminator/sub-agent-compact && " + binary +
		" plugin install sub-agent-compact@sub-agent-compact -y"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("output missing %q:\n%s", want, out.String())
	}
}

func TestEnsureClaudePluginsUnresolvedBinaryIsAVisibleSkip(t *testing.T) {
	home, _, first, second := pluginFixture(t)
	t.Setenv("PATH", t.TempDir())
	runner := &pluginRunner{}
	var out bytes.Buffer
	if err := pluginEngine(home, "no-such-claude", first, second, runner, &out, true).ensureClaudePlugins(
		context.Background(),
	); err != nil {
		t.Fatalf("ensureClaudePlugins: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("unresolved binary still ran %v", runner.calls)
	}
	want := "skip    claude plugin cache-live-control@cache-live-control in " + first +
		": real claude binary not resolved:"
	if !strings.Contains(out.String(), want) || !strings.Contains(out.String(), "no-such-claude") {
		t.Fatalf("output missing %q naming the binary:\n%s", want, out.String())
	}
}

func TestInstallRunsTheClaudePluginStep(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	runner := &pluginRunner{}
	var out bytes.Buffer
	if _, err := Run(context.Background(), Options{
		Mode:          ModeApply,
		Home:          home,
		ConfigDir:     first,
		ConfigDirs:    []string{first, second},
		ClaudeBinary:  binary,
		ProcessRunner: runner,
		Runner:        &outputRunner{printOutput: "state = not running\n"},
		Stdout:        &out,
	}); err != nil {
		t.Fatalf("Run: %v\n%s", err, out.String())
	}
	installs := map[string]int{}
	for _, call := range runner.calls {
		if strings.HasPrefix(call.argv, "plugin install ") {
			installs[call.configDir]++
		}
	}
	if installs[first] != len(claudePlugins) || installs[second] != len(claudePlugins) {
		t.Fatalf("plugin installs per account=%v, want %d each\n%s", installs, len(claudePlugins), out.String())
	}
}

func TestUpdateSettingsAddsClaudeEnvDefaultsKeepingUserValues(t *testing.T) {
	home := t.TempDir()
	for _, test := range []struct {
		name string
		raw  string
		want map[string]string
	}{
		{
			name: "env absent",
			raw:  `{}`,
			want: map[string]string{
				"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS": "1",
				"CLAUDE_CODE_AUTO_COMPACT_WINDOW":   "100000",
			},
		},
		{
			name: "user value and neighbor kept",
			raw:  `{"env":{"CLAUDE_CODE_AUTO_COMPACT_WINDOW":"250000","OTHER":"x"}}`,
			want: map[string]string{
				"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS": "1",
				"CLAUDE_CODE_AUTO_COMPACT_WINDOW":   "250000",
				"OTHER":                             "x",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			updated, _, _, err := updateSettings([]byte(test.raw), home, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			var document struct {
				Env map[string]string `json:"env"`
			}
			if err := json.Unmarshal(updated, &document); err != nil {
				t.Fatal(err)
			}
			if len(document.Env) != len(test.want) {
				t.Fatalf("env=%v, want %v", document.Env, test.want)
			}
			for key, value := range test.want {
				if document.Env[key] != value {
					t.Fatalf("env[%s]=%q, want %q (env=%v)", key, document.Env[key], value, document.Env)
				}
			}
		})
	}
	userEnv := []byte(`{"env":{"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS":"1"}}`)
	uninstalled, _, _, err := updateSettings(userEnv, home, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(uninstalled), "CLAUDE_CODE_ENABLE_FUNCTION_HOOKS") {
		t.Fatalf("uninstall removed a user env key: %s", uninstalled)
	}
}

package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
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
	envs  [][]string
	fail  map[string]string
	onRun func(configDir, argv string)
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
	runner.envs = append(runner.envs, options.Env)
	if runner.onRun != nil {
		runner.onRun(configDir, joined)
	}
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
	first = filepath.Join(home, ".cc", "1")
	second = filepath.Join(home, ".cc", "2")
	for _, dir := range []string{filepath.Join(home, ".claude"), first, second} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return home, binary, first, second
}

func pluginEngine(home, binary, first, second string, runner deps.Runner, out *bytes.Buffer, apply bool) *engine {
	return &engine{options: Options{
		Home:             home,
		ConfigDir:        filepath.Join(home, ".claude"),
		PrimaryConfigDir: first,
		ClaudeAccounts:   []pfmconfig.Account{{ID: 1, ConfigDir: first}, {ID: 2, ConfigDir: second}},
		ClaudeBinary:     binary,
		ProcessRunner:    runner,
		Stdout:           out,
	}, apply: apply}
}

// TestEnsureClaudePluginsRunsOnceThroughPrimaryOrStore: a roster host runs
// the step in the primary account's dir; a host with no roster, where
// ~/.claude is an ordinary config dir, keeps running it in Options.ConfigDir.
func TestEnsureClaudePluginsRunsOnceThroughPrimaryOrStore(t *testing.T) {
	for _, roster := range []bool{false, true} {
		t.Run(fmt.Sprint(roster), func(t *testing.T) {
			home, binary, first, account := pluginFixture(t)
			runner := &pluginRunner{}
			var out bytes.Buffer
			inst := pluginEngine(home, binary, first, account, runner, &out, true)
			inst.options.PrimaryConfigDir = account
			dir := account
			if !roster {
				inst.options.ClaudeAccounts, inst.options.PrimaryConfigDir = nil, ""
				dir = inst.options.ConfigDir
			}
			if err := inst.ensureClaudePlugins(context.Background()); err != nil {
				t.Fatal(err)
			}
			var want []pluginCall
			for _, p := range claudePlugins {
				want = append(
					want,
					pluginCall{"plugin marketplace add " + p.Source, dir},
					pluginCall{"plugin install " + p.ID + " -y", dir},
				)
			}
			if fmt.Sprint(runner.calls) != fmt.Sprint(want) {
				t.Fatalf("calls=%v want %v", runner.calls, want)
			}
			for _, p := range claudePlugins {
				line := "  change  run CLAUDE_CONFIG_DIR=" + dir + " " + binary + " plugin marketplace add " + p.Source + " && " + binary + " plugin install " + p.ID + " -y\n"
				if !strings.Contains(out.String(), line) {
					t.Fatalf("missing %q: %s", line, out.String())
				}
			}
		})
	}
}

// TestClaudePluginStepRefusesTheStoreOnARosterHost: with no primary dir
// resolved, the fallback to Options.ConfigDir (the store) is refused the way a
// launch refuses it, and no claude command runs with the store as its dir.
func TestClaudePluginStepRefusesTheStoreOnARosterHost(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	t.Setenv(paths.EnvHome, home)
	runner := &pluginRunner{}
	var out bytes.Buffer
	inst := pluginEngine(home, binary, first, second, runner, &out, true)
	inst.options.PrimaryConfigDir = ""
	store := inst.options.ConfigDir
	err := inst.ensureClaudePlugins(context.Background())
	if err == nil || !strings.Contains(err.Error(), store+" resolves to the Claude store") {
		t.Fatalf("err=%v, want the store refusal\n%s", err, out.String())
	}
	if len(runner.calls) != 0 {
		t.Fatalf("ran %v with the store as config dir\n%s", runner.calls, out.String())
	}
	if !strings.Contains(out.String(), "  FAIL    claude plugins in "+store+": ") {
		t.Fatalf("no FAIL line for the refusal:\n%s", out.String())
	}
}

// TestClaudePluginCommandDropsTheLoginDefaultSentinel: the child runs on the
// dir pfm chose, so the login default's sentinel never rides along with it.
func TestClaudePluginCommandDropsTheLoginDefaultSentinel(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	t.Setenv(claudelaunch.ConfigDirDefaultEnv, first)
	runner := &pluginRunner{}
	var out bytes.Buffer
	if err := pluginEngine(home, binary, first, second, runner, &out, true).ensureClaudePlugins(
		context.Background(),
	); err != nil {
		t.Fatalf("ensureClaudePlugins: %v\n%s", err, out.String())
	}
	if len(runner.envs) == 0 {
		t.Fatalf("no plugin command ran\n%s", out.String())
	}
	for index, environment := range runner.envs {
		for _, entry := range environment {
			if strings.HasPrefix(entry, claudelaunch.ConfigDirDefaultEnv+"=") {
				t.Fatalf("command %v carries %s", runner.calls[index], entry)
			}
		}
	}
}

func TestEnsureClaudePluginsSkipsAnAlreadyEnabledPlugin(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	enabled := `{"enabledPlugins":{"cache-live-control@cache-live-control":true,` +
		`"sub-agent-compact@sub-agent-compact":true,"agent-effort@agent-effort":true}}`
	writeFixture(t, filepath.Join(first, "settings.json"), enabled)
	writeInstalledPlugins(t, first, claudePlugins[0].ID, claudePlugins[1].ID, claudePlugins[2].ID)
	runner := &pluginRunner{}
	var out bytes.Buffer
	installer := pluginEngine(home, binary, first, second, runner, &out, true)
	if err := installer.ensureClaudePlugins(
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
	if len(runner.calls) != 0 {
		t.Fatalf("present plugins ran: %v", runner.calls)
	}
	for _, p := range claudePlugins {
		line := "  ok      claude plugin " + p.ID + " installed and enabled in " + first + "\n"
		if strings.Count(out.String(), line) != 1 {
			t.Fatalf("missing once %q: %s", line, out.String())
		}
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

func TestEnsureClaudePluginsInstallsWhereEnabledButNotInstalled(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	enabled := `{"enabledPlugins":{"cache-live-control@cache-live-control":true,` +
		`"sub-agent-compact@sub-agent-compact":true,"agent-effort@agent-effort":true}}`
	writeFixture(t, filepath.Join(first, "settings.json"), enabled)
	writeFixture(t, filepath.Join(second, "settings.json"), enabled)
	writeInstalledPlugins(t, first, claudePlugins[0].ID, claudePlugins[1].ID, claudePlugins[2].ID)
	runner := &pluginRunner{}
	var out bytes.Buffer
	inst := pluginEngine(home, binary, first, second, runner, &out, true)
	inst.options.PrimaryConfigDir = second
	if err := inst.ensureClaudePlugins(
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
	all := []string{claudePlugins[0].ID, claudePlugins[1].ID, claudePlugins[2].ID}
	for _, test := range []struct {
		name    string
		prepare func(t *testing.T, dir string)
		want    []string
		wantErr bool
	}{
		{name: "no record file", prepare: func(*testing.T, string) {}, want: all},
		{name: "all installed", prepare: func(t *testing.T, dir string) {
			writeInstalledPlugins(t, dir, all...)
		}, want: nil},
		{name: "install path gone", prepare: func(t *testing.T, dir string) {
			writeInstalledPlugins(t, dir, all...)
			if err := os.RemoveAll(filepath.Join(dir, "plugins", "cache", all[1])); err != nil {
				t.Fatal(err)
			}
		}, want: all[1:2]},
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

func TestEnsureClaudePluginsFailureNamesThePluginAndOthersContinue(t *testing.T) {
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
	if len(runner.calls) != 2*len(claudePlugins) {
		t.Fatalf("other plugins did not continue: %v", runner.calls)
	}
	if runner.calls[len(runner.calls)-1].argv != "plugin install "+claudePlugins[len(claudePlugins)-1].ID+" -y" {
		t.Fatal(runner.calls)
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
	installer := pluginEngine(home, binary, first, second, runner, &out, false)
	if err := installer.ensureClaudePlugins(
		context.Background(),
	); err != nil {
		t.Fatalf("ensureClaudePlugins: %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("dry run executed %v", runner.calls)
	}
	want := "change  run CLAUDE_CONFIG_DIR=" + first + " " + binary +
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
	home, binary, _, second := pluginFixture(t)
	store := filepath.Join(home, ".claude")
	runner := &pluginRunner{}
	var out bytes.Buffer
	if _, err := Run(context.Background(), Options{
		Mode:          ModeApply,
		Home:          home,
		ConfigDir:     store,
		ClaudeBinary:  binary,
		ProcessRunner: runner,
		Runner:        &outputRunner{printOutput: "state = not running\n"},
		MCPConfigPath: testConfigPath(t),
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
	if installs[store] != len(claudePlugins) || installs[second] != 0 {
		t.Fatalf(
			"plugin installs per dir=%v, want %d only in the store\n%s",
			installs,
			len(claudePlugins),
			out.String(),
		)
	}
}

func TestClaudePluginDoorLeavesSettingsEnvAlone(t *testing.T) {
	for _, test := range []struct{ name, content string }{
		{"env absent", "{}\n"},
		{"operator env", "{\"env\":{\"CLAUDE_CODE_AUTO_COMPACT_WINDOW\":\"250000\"}}\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			home, binary, first, second := pluginFixture(t)
			path := filepath.Join(first, "settings.json")
			writeFixture(t, path, test.content)
			var out bytes.Buffer
			installer := pluginEngine(home, binary, first, second, &pluginRunner{}, &out, true)
			if err := installer.ensureClaudePlugins(context.Background()); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != test.content {
				t.Fatalf("settings=%q err=%v, want %q", got, err, test.content)
			}
		})
	}
}

func TestClaudePluginDoorSkipsLiveChatsAnywhere(t *testing.T) {
	for _, onPrimary := range []bool{false, true} {
		t.Run(fmt.Sprint(onPrimary), func(t *testing.T) {
			home, binary, first, second := pluginFixture(t)
			liveDir := first
			if onPrimary {
				liveDir = second
			}
			proc := filepath.Join(home, "proc")
			if err := os.MkdirAll(filepath.Join(proc, "4242"), 0o700); err != nil {
				t.Fatal(err)
			}
			writeFixture(t, filepath.Join(liveDir, "sessions", "4242.json"), "{}")
			runner := &pluginRunner{}
			var out bytes.Buffer
			inst := pluginEngine(home, binary, first, second, runner, &out, true)
			inst.options.PrimaryConfigDir = second
			inst.options.ProcRoot = proc
			if err := inst.ensureClaudePlugins(context.Background()); err != nil {
				t.Fatal(err)
			}
			want := "  skip    claude plugins: live chats 4242 on " + liveDir + " — close them and rerun pfm install --yes\n"
			if out.String() != want || len(runner.calls) != 0 {
				t.Fatalf("got %q calls=%v want %q", out.String(), runner.calls, want)
			}
		})
	}
}

func TestClaudePluginDoorLiveChatReadErrorNamesAccount(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	writeFixture(t, filepath.Join(second, "sessions"), "not a directory")
	runner := &pluginRunner{}
	var out bytes.Buffer
	installer := pluginEngine(home, binary, first, second, runner, &out, true)
	installer.options.ProcRoot = filepath.Join(home, "proc")
	err := installer.ensureClaudePlugins(context.Background())
	if err == nil || !strings.Contains(err.Error(), second) || !strings.Contains(err.Error(), "live chats") ||
		!strings.Contains(out.String(), "FAIL    claude plugin "+claudePlugins[0].ID+" in "+first) {
		t.Fatalf("err=%v output=%s", err, out.String())
	}
	for _, call := range runner.calls {
		if call.configDir == second {
			t.Fatalf("account ran after guard failed: %v", call)
		}
	}
	if len(runner.calls) != 0 {
		t.Fatalf("commands after unreadable live chats: %v", runner.calls)
	}
}

// recordedPIDs reads every pid a fixture script appended to path.
func recordedPIDs(t *testing.T, path string) []int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var pids []int
	for _, field := range strings.Fields(string(raw)) {
		pid, convErr := strconv.Atoi(field)
		if convErr != nil {
			t.Fatalf("parse recorded pid %q: %v", field, convErr)
		}
		pids = append(pids, pid)
	}
	return pids
}

func TestClaudePluginCommandTimeoutKillsTheWholeProcessGroup(t *testing.T) {
	home := t.TempDir()
	account := filepath.Join(home, ".claude")
	if err := os.MkdirAll(account, 0o700); err != nil {
		t.Fatal(err)
	}
	// The fake claude records its own pid and a backgrounded sleep's pid under
	// the account dir (the one path the command's environment carries), then
	// hangs, the way a stuck plugin command does.
	binary := writeScript(t, t.TempDir(), "claude-hang", "#!/bin/sh\n"+
		"sleep 300 &\n"+
		"echo $! >> \"$CLAUDE_CONFIG_DIR/child.pids\"\n"+
		"echo $$ >> \"$CLAUDE_CONFIG_DIR/main.pids\"\n"+
		"exec sleep 300\n")
	t.Cleanup(func() {
		for _, name := range []string{"child.pids", "main.pids"} {
			for _, pid := range recordedPIDs(t, filepath.Join(account, name)) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	previous := claudePluginTimeout
	claudePluginTimeout = 300 * time.Millisecond
	t.Cleanup(func() { claudePluginTimeout = previous })

	var out bytes.Buffer
	installer := &engine{options: Options{
		Home: home, ConfigDir: account, ClaudeBinary: binary, Stdout: &out,
	}, apply: true}
	finished := make(chan error, 1)
	go func() { finished <- installer.ensureClaudePlugins(context.Background()) }()
	var err error
	select {
	case err = <-finished:
	case <-time.After(15 * time.Second):
		t.Fatal("ensureClaudePlugins did not return within 15s: the hung command was not stopped")
	}
	if err == nil || !strings.Contains(err.Error(), "timed out after 300ms") {
		t.Fatalf("error=%v, want it to say the command timed out after 300ms", err)
	}
	pids := append(recordedPIDs(t, filepath.Join(account, "child.pids")),
		recordedPIDs(t, filepath.Join(account, "main.pids"))...)
	if len(pids) < 2 {
		t.Fatalf("fixture recorded pids %v, want a shell and a backgrounded child", pids)
	}
	for _, pid := range pids {
		deadline := time.Now().Add(5 * time.Second)
		for syscall.Kill(pid, 0) == nil {
			if time.Now().After(deadline) {
				t.Fatalf("process %d survived the plugin command timeout", pid)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func TestClaudePluginFailedCommandKeepsItsWrites(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	writeFixture(t, filepath.Join(second, "settings.json"), "{}\n")
	argv := "plugin install " + claudePlugins[0].ID + " -y"
	runner := &pluginRunner{fail: map[string]string{second + " " + argv: "plugin failed"}}
	settings := `{"enabledPlugins":{"bad":true}}`
	plugins := `{"plugins":{}}`
	runner.onRun = func(dir, command string) {
		if dir == second && command == argv {
			writeFixture(t, filepath.Join(dir, "settings.json"), settings)
			writeFixture(t, filepath.Join(dir, "plugins", "installed_plugins.json"), plugins)
		}
	}
	var output bytes.Buffer
	installer := pluginEngine(home, binary, first, second, runner, &output, true)
	installer.options.PrimaryConfigDir = second
	err := installer.ensureClaudePlugins(context.Background())
	want := "claude plugin " + claudePlugins[0].ID + " in " + second + ": " + argv +
		`: exit 1 stderr="plugin failed"`
	if err == nil || err.Error() != want || !strings.Contains(output.String(), "  FAIL    "+want+"\n") {
		t.Fatalf("failure = %v, want %q\n%s", err, want, output.String())
	}
	for path, content := range map[string]string{
		filepath.Join(second, "settings.json"):                     settings,
		filepath.Join(second, "plugins", "installed_plugins.json"): plugins,
	} {
		if got := readFixture(t, path); got != content {
			t.Fatalf("failed plugin changed %s to %q, want %q", path, got, content)
		}
	}
}

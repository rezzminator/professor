package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

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
		`"sub-agent-compact@sub-agent-compact":true,"agent-effort@agent-effort":true}}`
	writeFixture(t, filepath.Join(first, "settings.json"), enabled)
	writeInstalledPlugins(t, first, claudePlugins[0].ID, claudePlugins[1].ID, claudePlugins[2].ID)
	runner := &pluginRunner{}
	var out bytes.Buffer
	installer := pluginEngine(home, binary, first, second, runner, &out, true)
	journal := NewJournal(context.Background(), LayoutEnv{Home: home})
	installer.options.Journal = journal
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
	if len(runner.calls) != 2*len(claudePlugins) {
		t.Fatalf("second account calls=%v, want add+install per plugin", runner.calls)
	}
	for _, record := range journal.records {
		if record.Destination == filepath.Join(first, "settings.json") ||
			record.Destination == filepath.Join(first, "plugins") {
			t.Fatalf("already enabled account journaled %+v", record)
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

// Accounts share one settings.json through a symlink, so "enabled" there says
// nothing about this account: a plugin enabled but never installed in this
// account's config dir still gets installed.
func TestEnsureClaudePluginsInstallsWhereEnabledButNotInstalled(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	enabled := `{"enabledPlugins":{"cache-live-control@cache-live-control":true,` +
		`"sub-agent-compact@sub-agent-compact":true,"agent-effort@agent-effort":true}}`
	writeFixture(t, filepath.Join(first, "settings.json"), enabled)
	writeFixture(t, filepath.Join(second, "settings.json"), enabled)
	writeInstalledPlugins(t, first, claudePlugins[0].ID, claudePlugins[1].ID, claudePlugins[2].ID)
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
	installer := pluginEngine(home, binary, first, second, runner, &out, false)
	journal := NewJournal(context.Background(), LayoutEnv{Home: home})
	journal.dryRun = true
	installer.options.Journal = journal
	if err := installer.ensureClaudePlugins(
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
	requireJournalPaths(t, journal.Planned(), filepath.Join(first, "settings.json"), filepath.Join(first, "plugins"),
		filepath.Join(second, "settings.json"), filepath.Join(second, "plugins"))
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
	if installs[first] != len(claudePlugins) || installs[second] != len(claudePlugins) {
		t.Fatalf("plugin installs per account=%v, want %d each\n%s", installs, len(claudePlugins), out.String())
	}
}

func TestClaudePluginDoorJournalsAndRollsBack(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	for _, dir := range []string{first, second} {
		writeFixture(t, filepath.Join(dir, "settings.json"), "{}\n")
	}
	journal := NewJournal(context.Background(), LayoutEnv{Home: home})
	runner := &pluginRunner{}
	runner.onRun = func(dir, argv string) {
		if len(journal.records) < 2 {
			t.Fatalf("command %s in %s ran without snapshots", argv, dir)
		}
		settings := journal.records[len(journal.records)-2]
		plugins := journal.records[len(journal.records)-1]
		if settings.Destination != filepath.Join(dir, "settings.json") ||
			plugins.Destination != filepath.Join(dir, "plugins") ||
			settings.Result != layoutRecordPending || plugins.Result != layoutRecordPending {
			t.Fatalf("command %s in %s saw records %+v %+v", argv, dir, settings, plugins)
		}
		if !strings.HasPrefix(argv, "plugin install ") {
			return
		}
		settingsPath := filepath.Join(dir, "settings.json")
		if err := os.WriteFile(settingsPath, []byte(`{"enabledPlugins":{}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o700); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(dir, "plugins", "marker"), argv)
	}
	var out bytes.Buffer
	installer := pluginEngine(home, binary, first, second, runner, &out, true)
	installer.options.Journal = journal
	if err := installer.ensureClaudePlugins(context.Background()); err != nil {
		t.Fatalf("ensureClaudePlugins: %v\n%s", err, out.String())
	}
	if len(journal.records) != 4*len(claudePlugins) {
		t.Fatalf("records=%+v", journal.records)
	}
	for _, record := range journal.records {
		if record.Row != layoutRowInstall || record.Result != layoutRecordApplied {
			t.Fatalf("record not applied: %+v", record)
		}
	}
	if err := RollbackLayout(
		context.Background(), LayoutEnv{Home: home}, filepath.Base(journal.Dir()), false, io.Discard,
	); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	for _, dir := range []string{first, second} {
		got, err := os.ReadFile(filepath.Join(dir, "settings.json"))
		if err != nil || string(got) != "{}\n" {
			t.Fatalf("settings in %s after rollback=%q err=%v", dir, got, err)
		}
		if _, err := os.Lstat(filepath.Join(dir, "plugins")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("plugins in %s after rollback: %v", dir, err)
		}
	}
}

func TestClaudePluginFailedCommandRestoresAndClosesJournal(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	before := []byte("{}\n")
	writeFixture(t, filepath.Join(second, "settings.json"), string(before))
	journal := NewJournal(context.Background(), LayoutEnv{Home: home})
	runner := &pluginRunner{fail: map[string]string{
		second + " plugin install " + claudePlugins[0].ID + " -y": "plugin failed",
	}}
	runner.onRun = func(dir, argv string) {
		if dir != second || !strings.HasPrefix(argv, "plugin install ") {
			return
		}
		writeFixture(t, filepath.Join(dir, "settings.json"), `{"enabledPlugins":{"bad":true}}`)
		writeFixture(t, filepath.Join(dir, "plugins", "installed_plugins.json"), `{"plugins":{}}`)
	}
	var out bytes.Buffer
	installer := pluginEngine(home, binary, first, second, runner, &out, true)
	installer.options.Journal = journal
	err := installer.ensureClaudePlugins(context.Background())
	if err == nil || !strings.Contains(err.Error(), second) || !strings.Contains(err.Error(), claudePlugins[0].ID) ||
		!strings.Contains(out.String(), "FAIL    claude plugin "+claudePlugins[0].ID+" in "+second) {
		t.Fatalf("failure did not name account and plugin: err=%v\n%s", err, out.String())
	}
	got, readErr := os.ReadFile(filepath.Join(second, "settings.json"))
	if readErr != nil || !bytes.Equal(got, before) {
		t.Fatalf("settings after failure=%q err=%v", got, readErr)
	}
	if _, statErr := os.Lstat(filepath.Join(second, "plugins")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("plugins after failure: %v", statErr)
	}
	if len(journal.records) != 2*len(claudePlugins)+2 {
		t.Fatalf("records=%+v", journal.records)
	}
	for _, record := range journal.records[len(journal.records)-2:] {
		if record.Result != layoutRecordRestored {
			t.Fatalf("failed plugin record=%+v", record)
		}
	}
	for _, record := range journal.records[:len(journal.records)-2] {
		if record.Result != layoutRecordApplied {
			t.Fatalf("other account did not apply: %+v", record)
		}
	}
	raw, err := os.ReadFile(filepath.Join(journal.Dir(), "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted []layoutJournalRecord
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	for _, record := range persisted {
		if record.Result == layoutRecordPending {
			t.Fatalf("journal.json contains pending record: %+v", record)
		}
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

func TestClaudePluginDoorSkipsLiveChatsOnAccountAndSharer(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "own account"
		if shared {
			name = "shared settings"
		}
		t.Run(name, func(t *testing.T) {
			home, binary, first, second := pluginFixture(t)
			procRoot := filepath.Join(home, "proc")
			if err := os.MkdirAll(filepath.Join(procRoot, "4242"), 0o700); err != nil {
				t.Fatal(err)
			}
			liveDir := second
			var extra string
			if shared {
				extra = filepath.Join(home, ".cc", "3")
				if err := os.MkdirAll(extra, 0o700); err != nil {
					t.Fatal(err)
				}
				writeFixture(t, filepath.Join(first, "settings.json"), "{}")
				for _, dir := range []string{second, extra} {
					sharedSettings := filepath.Join(first, "settings.json")
					if err := os.Symlink(sharedSettings, filepath.Join(dir, "settings.json")); err != nil {
						t.Fatal(err)
					}
				}
				liveDir = extra
			}
			writeFixture(t, filepath.Join(liveDir, "sessions", "4242.json"), "{}")
			runner := &pluginRunner{}
			var out bytes.Buffer
			installer := pluginEngine(home, binary, first, second, runner, &out, true)
			installer.options.ProcRoot = procRoot
			if shared {
				installer.options.ConfigDirs = append(installer.options.ConfigDirs, extra)
			}
			if err := installer.ensureClaudePlugins(context.Background()); err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{second, extra} {
				if dir == "" {
					continue
				}
				physical := physicalSettingsPath(filepath.Join(dir, "settings.json"))
				want := "skip    claude plugins in " + dir + ": live chats 4242 on " + physical +
					" — close them and rerun pfm install --yes"
				if !strings.Contains(out.String(), want) {
					t.Fatalf("missing %q:\n%s", want, out.String())
				}
				for _, call := range runner.calls {
					if call.configDir == dir {
						t.Fatalf("live account ran %v", call)
					}
				}
			}
			if !shared {
				if len(runner.calls) != 2*len(claudePlugins) || runner.calls[0].configDir != first {
					t.Fatalf("other account did not install: %v", runner.calls)
				}
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
		!strings.Contains(out.String(), "FAIL    claude plugin "+claudePlugins[0].ID+" in "+second) {
		t.Fatalf("err=%v output=%s", err, out.String())
	}
	for _, call := range runner.calls {
		if call.configDir == second {
			t.Fatalf("account ran after guard failed: %v", call)
		}
	}
	if len(runner.calls) != 2*len(claudePlugins) {
		t.Fatalf("other account did not run: %v", runner.calls)
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

package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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

// testPlugin is a row of the default list the install tests ensure: its Name,
// the GitHub marketplace Source and the ID of its GitHub copy.
type testPlugin struct{ Name, Source, ID string }

// testPlugins is claude.plugins' default as these tests spell it out.
var testPlugins = []testPlugin{
	{Name: "cache-live-control", Source: "rezzminator/cache-live-control", ID: "cache-live-control@cache-live-control"},
	{Name: "sub-agent-compact", Source: "rezzminator/sub-agent-compact", ID: "sub-agent-compact@sub-agent-compact"},
	{
		Name:   "effort-live-control",
		Source: "rezzminator/effort-live-control",
		ID:     "effort-live-control@effort-live-control",
	},
	{
		Name:   "tool-visibility-controller",
		Source: "rezzminator/tool-visibility-controller",
		ID:     "tool-visibility-controller@tool-visibility-controller",
	},
}

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
	// dirs is each Run's working directory, "" for the caller's own.
	dirs  []string
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
	runner.dirs = append(runner.dirs, options.Dir)
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
		ClaudePlugins:    pfmconfig.DefaultClaudePlugins(),
		ProcessRunner:    runner,
		Stdout:           out,
	}, apply: apply}
}

// TestEnsureClaudePluginsRunsOnceThroughPrimaryOrSkipsTheStore: a roster host
// runs the step in the primary account's dir; a host with no roster, where the
// only config dir is the store, runs no claude command and prints one named
// skip line, so nothing writes account identity into the store.
func TestEnsureClaudePluginsRunsOnceThroughPrimaryOrSkipsTheStore(t *testing.T) {
	for _, roster := range []bool{false, true} {
		t.Run(fmt.Sprint(roster), func(t *testing.T) {
			home, binary, first, account := pluginFixture(t)
			runner := &pluginRunner{}
			var out bytes.Buffer
			inst := pluginEngine(home, binary, first, account, runner, &out, true)
			inst.options.PrimaryConfigDir = account
			if !roster {
				inst.options.ClaudeAccounts, inst.options.PrimaryConfigDir = nil, ""
			}
			if err := inst.ensureClaudePlugins(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !roster {
				store := inst.options.ConfigDir
				want := "  skip    claude plugins: not run — no account roster, so the only config dir is the Claude store " +
					store + ", where a claude process writes account identity; add an account to pfm.config.json and rerun pfm install --yes\n"
				if out.String() != want || len(runner.calls) != 0 {
					t.Fatalf("got %q calls=%v, want only %q", out.String(), runner.calls, want)
				}
				return
			}
			var want []pluginCall
			for _, p := range testPlugins {
				want = append(
					want,
					pluginCall{"plugin marketplace add " + p.Source, account},
					pluginCall{"plugin install " + p.ID + " -y", account},
				)
			}
			if fmt.Sprint(runner.calls) != fmt.Sprint(want) {
				t.Fatalf("calls=%v want %v", runner.calls, want)
			}
			for _, p := range testPlugins {
				line := "  change  run CLAUDE_CONFIG_DIR=" + account + " " + binary + " plugin marketplace add " + p.Source + " && " + binary + " plugin install " + p.ID + " -y\n"
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

// TestClaudePluginStepRefusesAConfigDirStoreOnARosterHost: --config-dir
// leaves the roster unpassed; on a roster host a --config-dir that resolves to
// the store, spelled directly or through a symlink, is refused the same way.
func TestClaudePluginStepRefusesAConfigDirStoreOnARosterHost(t *testing.T) {
	for _, spelling := range []string{"store", "symlink"} {
		t.Run(spelling, func(t *testing.T) {
			home, binary, first, second := pluginFixture(t)
			t.Setenv(paths.EnvHome, home)
			runner := &pluginRunner{}
			var out bytes.Buffer
			inst := pluginEngine(home, binary, first, second, runner, &out, true)
			inst.options.ClaudeAccounts, inst.options.PrimaryConfigDir = nil, ""
			inst.options.ClaudeRosterHost = true
			dir := inst.options.ConfigDir
			if spelling == "symlink" {
				dir = filepath.Join(t.TempDir(), "claude-link")
				if err := os.Symlink(inst.options.ConfigDir, dir); err != nil {
					t.Fatal(err)
				}
				inst.options.ConfigDir = dir
			}
			err := inst.ensureClaudePlugins(context.Background())
			if err == nil || !strings.Contains(err.Error(), dir+" resolves to the Claude store") {
				t.Fatalf("err=%v, want the store refusal\n%s", err, out.String())
			}
			if len(runner.calls) != 0 {
				t.Fatalf("ran %v with the store as config dir\n%s", runner.calls, out.String())
			}
			if !strings.Contains(out.String(), "  FAIL    claude plugins in "+dir+": ") {
				t.Fatalf("no FAIL line for the refusal:\n%s", out.String())
			}
		})
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
		`"sub-agent-compact@sub-agent-compact":true,"effort-live-control@effort-live-control":true,"tool-visibility-controller@tool-visibility-controller":true}}`
	writeFixture(t, filepath.Join(first, "settings.json"), enabled)
	writeInstalledPlugins(t, first, testPlugins[0].ID, testPlugins[1].ID, testPlugins[2].ID, testPlugins[3].ID)
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
	for _, p := range testPlugins {
		line := "  ok      claude plugin " + p.ID + " installed and enabled in " + first + "\n"
		if strings.Count(out.String(), line) != 1 {
			t.Fatalf("missing once %q: %s", line, out.String())
		}
	}
}

// writeInstalledPlugins writes dir's plugins/installed_plugins.json the way
// claude records an install: one user entry per id at checkoutVersion whose
// installPath exists; a -dev id's holds the files of its checkout, as the
// snapshot Claude took of it.
func writeInstalledPlugins(t *testing.T, dir string, ids ...string) {
	t.Helper()
	plugins := map[string]any{}
	for _, id := range ids {
		installPath := filepath.Join(dir, "plugins", "cache", id)
		if err := os.MkdirAll(installPath, 0o700); err != nil {
			t.Fatal(err)
		}
		if name, marketplace, _ := strings.Cut(id, "@"); marketplace == devMarketplaceName(name) {
			writePluginFiles(t, installPath, name)
		}
		plugins[id] = []any{map[string]any{"scope": "user", "installPath": installPath, "version": checkoutVersion}}
	}
	raw, err := json.Marshal(map[string]any{"version": 2, "plugins": plugins})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, "plugins", "installed_plugins.json"), string(raw))
}

// writeKnownMarketplaces records the marketplaces Claude Code knows in dir, the way
// `claude plugin marketplace add` leaves plugins/known_marketplaces.json.
func writeKnownMarketplaces(t *testing.T, dir string, names ...string) {
	t.Helper()
	known := map[string]any{}
	for _, name := range names {
		known[name] = map[string]any{"source": map[string]any{"source": "github", "repo": "o/" + name}}
	}
	raw, err := json.Marshal(known)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, "plugins", "known_marketplaces.json"), string(raw))
}

// pluginArgvs lists the argv of every command the fake runner saw, in order.
func pluginArgvs(runner *pluginRunner) []string {
	argvs := make([]string, 0, len(runner.calls))
	for _, call := range runner.calls {
		argvs = append(argvs, call.argv)
	}
	return argvs
}

// TestEnsureClaudePluginsInstallsTheListedPlugins: the list decides what is installed: a bare name
// from its GitHub marketplace, a third-party id with a source through marketplace add then install,
// and one whose marketplace Claude Code already knows through install alone.
func TestEnsureClaudePluginsInstallsTheListedPlugins(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	writeKnownMarketplaces(t, first, "claude-plugins-official")
	runner := &pluginRunner{}
	var out bytes.Buffer
	inst := pluginEngine(home, binary, first, second, runner, &out, true)
	inst.options.ClaudePlugins = []pfmconfig.ClaudePlugin{
		{ID: "effort-live-control"},
		{ID: "typesafe@typesafe-ai", Source: "typesafe-ai/skills"},
		{ID: "clangd-lsp@claude-plugins-official"},
		{ID: "odd@odd-market", Source: "o/r;x"},
	}
	if err := inst.ensureClaudePlugins(context.Background()); err != nil {
		t.Fatalf("ensureClaudePlugins: %v\n%s", err, out.String())
	}
	want := []string{
		"plugin marketplace add rezzminator/effort-live-control",
		"plugin install effort-live-control@effort-live-control -y",
		"plugin marketplace add typesafe-ai/skills",
		"plugin install typesafe@typesafe-ai -y",
		"plugin install clangd-lsp@claude-plugins-official -y",
		"plugin marketplace add o/r;x",
		"plugin install odd@odd-market -y",
	}
	if got := pluginArgvs(runner); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands\n got %q\nwant %q\n%s", got, want, out.String())
	}
	// The change line is a shell line a person may paste: a source that is not one plain word is quoted.
	for _, line := range []string{
		"  change  run CLAUDE_CONFIG_DIR=" + first + " " + binary + " plugin marketplace add typesafe-ai/skills && ",
		"  change  run CLAUDE_CONFIG_DIR=" + first + " " + binary + " plugin marketplace add 'o/r;x' && ",
	} {
		if !strings.Contains(out.String(), line) {
			t.Fatalf("missing %q:\n%s", line, out.String())
		}
	}
}

// TestEnsureClaudePluginsUnreadableMarketplaceRecordFailsOnlyItsPlugins: an unreadable
// plugins/known_marketplaces.json fails each listed id that needs it, naming the record, and the
// plugins that do not need it still install.
func TestEnsureClaudePluginsUnreadableMarketplaceRecordFailsOnlyItsPlugins(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	known := filepath.Join(first, "plugins", "known_marketplaces.json")
	writeFixture(t, known, "{")
	runner := &pluginRunner{}
	var out bytes.Buffer
	inst := pluginEngine(home, binary, first, second, runner, &out, true)
	inst.options.ClaudePlugins = []pfmconfig.ClaudePlugin{{ID: "typesafe@typesafe-ai"}, {ID: "effort-live-control"}}
	err := inst.ensureClaudePlugins(context.Background())
	want := "claude plugin typesafe@typesafe-ai in " + first + ": marketplace record unreadable, state unknown: parse " +
		known + ": unexpected end of JSON input"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v\nwant %s\n%s", err, want, out.String())
	}
	wantCommands := []string{
		"plugin marketplace add rezzminator/effort-live-control",
		"plugin install effort-live-control@effort-live-control -y",
	}
	if got := pluginArgvs(runner); !reflect.DeepEqual(got, wantCommands) {
		t.Fatalf("commands\n got %q\nwant %q\n%s", got, wantCommands, out.String())
	}
}

// TestEnsureClaudePluginsReportsAPluginItCannotInstall: a third-party id whose marketplace Claude
// Code does not know and whose entry has no source is a FAIL naming the plugin, the reason and the
// repair, and runs no command for it; the other listed plugins install, and an id already installed
// and enabled needs no marketplace.
func TestEnsureClaudePluginsReportsAPluginItCannotInstall(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	writeKnownMarketplaces(t, first, "some-other")
	writeFixture(t, filepath.Join(first, "settings.json"), `{"enabledPlugins":{"installed@gone-market":true}}`)
	writeInstalledPlugins(t, first, "installed@gone-market")
	runner := &pluginRunner{}
	var out bytes.Buffer
	inst := pluginEngine(home, binary, first, second, runner, &out, true)
	inst.options.ClaudePlugins = []pfmconfig.ClaudePlugin{
		{ID: "typesafe@typesafe-ai"}, {ID: "effort-live-control"}, {ID: "installed@gone-market"},
	}
	err := inst.ensureClaudePlugins(context.Background())
	want := "claude plugin typesafe@typesafe-ai in " + first + ": marketplace typesafe-ai is not known to Claude Code" +
		" and its claude.plugins entry has no source — " + ClaudePluginUnresolvedFix
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v\nwant %s\n%s", err, want, out.String())
	}
	wantCommands := []string{
		"plugin marketplace add rezzminator/effort-live-control",
		"plugin install effort-live-control@effort-live-control -y",
	}
	if got := pluginArgvs(runner); !reflect.DeepEqual(got, wantCommands) {
		t.Fatalf("commands\n got %q\nwant %q\n%s", got, wantCommands, out.String())
	}
	if !strings.Contains(out.String(), "FAIL    claude plugin typesafe@typesafe-ai in "+first) {
		t.Fatalf("no FAIL line for the plugin pfm cannot install:\n%s", out.String())
	}
}

// TestEnsureClaudePluginsLeavesUnlistedPluginsUntouched: only the listed plugins are installed,
// enabled or retired; every other enabledPlugins entry (another copy of a plugin pfm used to
// install included) and its pluginConfigs keep their value.
func TestEnsureClaudePluginsLeavesUnlistedPluginsUntouched(t *testing.T) {
	root := t.TempDir()
	inst, runner, out, _, first := alphaPluginEngine(t, root)
	for _, p := range testPlugins {
		writePluginCheckout(t, root, p.Name)
	}
	inst.options.ClaudePlugins = []pfmconfig.ClaudePlugin{{ID: "effort-live-control"}}
	simulateClaude(t, runner)
	writeFixture(t, filepath.Join(first, "settings.json"), `{"enabledPlugins":{`+
		`"cache-live-control@cache-live-control":true,"cache-live-control@cache-live-control-dev":true,`+
		`"swift-lsp@claude-plugins-official":true,"buddy@buddy":false,"effort-live-control@effort-live-control":true},`+
		`"pluginConfigs":{"cache-live-control@cache-live-control":{"options":{"keep":1}}}}`)
	if err := inst.ensureClaudePlugins(context.Background()); err != nil {
		t.Fatalf("ensureClaudePlugins: %v\n%s", err, out.String())
	}
	var got struct {
		Enabled map[string]any `json:"enabledPlugins"`
		Configs map[string]any `json:"pluginConfigs"`
	}
	if err := json.Unmarshal([]byte(readFixture(t, filepath.Join(first, "settings.json"))), &got); err != nil {
		t.Fatal(err)
	}
	wantEnabled := map[string]any{
		"cache-live-control@cache-live-control":       true,
		"cache-live-control@cache-live-control-dev":   true,
		"swift-lsp@claude-plugins-official":           true,
		"buddy@buddy":                                 false,
		"effort-live-control@effort-live-control":     false,
		"effort-live-control@effort-live-control-dev": true,
	}
	wantConfigs := map[string]any{
		"cache-live-control@cache-live-control": map[string]any{"options": map[string]any{"keep": float64(1)}},
	}
	if !reflect.DeepEqual(got.Enabled, wantEnabled) || !reflect.DeepEqual(got.Configs, wantConfigs) {
		t.Fatalf(
			"settings enabledPlugins=%v pluginConfigs=%v\nwant %v %v",
			got.Enabled,
			got.Configs,
			wantEnabled,
			wantConfigs,
		)
	}
	for _, argv := range pluginArgvs(runner) {
		if !strings.Contains(argv, "effort-live-control") {
			t.Fatalf("a command touched an unlisted plugin: %q", argv)
		}
	}
}

func TestEnsureClaudePluginsInstallsWhereEnabledButNotInstalled(t *testing.T) {
	home, binary, first, second := pluginFixture(t)
	enabled := `{"enabledPlugins":{"cache-live-control@cache-live-control":true,` +
		`"sub-agent-compact@sub-agent-compact":true,"effort-live-control@effort-live-control":true,"tool-visibility-controller@tool-visibility-controller":true}}`
	writeFixture(t, filepath.Join(first, "settings.json"), enabled)
	writeFixture(t, filepath.Join(second, "settings.json"), enabled)
	writeInstalledPlugins(t, first, testPlugins[0].ID, testPlugins[1].ID, testPlugins[2].ID, testPlugins[3].ID)
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
	if installs != len(testPlugins) {
		t.Fatalf(
			"installs in the uninstalled account=%d, want %d; calls=%v",
			installs,
			len(testPlugins),
			runner.calls,
		)
	}
}

func TestClaudePluginsNotInstalledReadsTheAccountRecord(t *testing.T) {
	all := []string{testPlugins[0].ID, testPlugins[1].ID, testPlugins[2].ID, testPlugins[3].ID}
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
			got, err := ClaudePluginsNotInstalled(
				dir,
				ClaudePluginTargets(ClaudePluginBuild{Plugins: pfmconfig.DefaultClaudePlugins()}),
			)
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
	// A bare name that fails to install names the entry, the repository pfm
	// tried and the repair, in the install's own FAIL line.
	for _, want := range []string{
		`claude.plugins entry "sub-agent-compact"`,
		"github.com/rezzminator/sub-agent-compact",
		"fix or remove the entry in claude.plugins if that repository does not exist",
		BareClaudePluginRepair("sub-agent-compact"),
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("FAIL line lacks %q:\n%s", want, out.String())
		}
	}
	if len(runner.calls) != 2*len(testPlugins) {
		t.Fatalf("other plugins did not continue: %v", runner.calls)
	}
	if runner.calls[len(runner.calls)-1].argv != "plugin install "+testPlugins[len(testPlugins)-1].ID+" -y" {
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
	home, binary, _, _ := pluginFixture(t)
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
		ClaudePlugins: pfmconfig.DefaultClaudePlugins(),
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
	// A host with no roster has only the store as a config dir: the step runs
	// no claude command there.
	if len(installs) != 0 || len(runner.calls) != 0 ||
		!strings.Contains(out.String(), "  skip    claude plugins: not run — no account roster") {
		t.Fatalf(
			"plugin installs per dir=%v calls=%v, want the named skip and no command\n%s",
			installs,
			runner.calls,
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
	account := filepath.Join(home, ".cc", "1") // not the store: the command then carries CLAUDE_CONFIG_DIR
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
		ClaudePlugins: pfmconfig.DefaultClaudePlugins(),
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
	argv := "plugin install " + testPlugins[0].ID + " -y"
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
	want := "claude plugin " + testPlugins[0].ID + " in " + second + ": " + argv +
		`: exit 1 stderr="plugin failed" — ` + BareClaudePluginRepair(testPlugins[0].Name)
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

func TestClaudePluginStepFailsUnreadableState(t *testing.T) {
	for _, record := range []bool{false, true} {
		t.Run(fmt.Sprint(record), func(t *testing.T) {
			home, binary, first, second := pluginFixture(t)
			path := filepath.Join(first, "settings.json")
			state := "settings"
			if record {
				path = filepath.Join(first, "plugins", "installed_plugins.json")
				state = "install record"
			}
			writeFixture(t, path, "{")
			runner := &pluginRunner{}
			var output bytes.Buffer
			installer := pluginEngine(home, binary, first, second, runner, &output, true)
			err := installer.ensureClaudePlugins(context.Background())
			want := "claude plugins in " + first + ": " + state + " unreadable, state unknown: parse " +
				path + ": unexpected end of JSON input"
			if err == nil || err.Error() != want {
				t.Fatalf("error = %v, want %q", err, want)
			}
			if output.String() != "  FAIL    "+want+"\n" || len(runner.calls) != 0 {
				t.Fatalf(
					"output = %q calls=%v, want the complete FAIL line and no commands",
					output.String(),
					runner.calls,
				)
			}
		})
	}
}

func TestClaudePluginGapsNamesEnabledLegacyEffort(t *testing.T) {
	for _, row := range []struct {
		id      string
		enabled bool
		warning bool
	}{
		{"agent-effort@agent-effort", true, true},
		{"agent-effort@agent-effort-dev", true, true},
		{"agent-effort@agent-effort", false, false},
		{"effort-live-control@effort-live-control", true, false},
	} {
		t.Run(fmt.Sprintf("%s enabled=%v", row.id, row.enabled), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			raw, err := json.Marshal(map[string]any{"enabledPlugins": map[string]bool{row.id: row.enabled}})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			gaps, err := ClaudePluginGaps(path, nil)
			if err != nil {
				t.Fatal(err)
			}
			var want []ClaudePluginGap
			if row.warning {
				want = []ClaudePluginGap{
					{
						Problem: "plugin " + row.id + " retired (now effort-live-control) but still enabled in " + path,
						Fix:     "claude plugin uninstall " + row.id + " --scope user; if claude.plugins still lists it, replace that entry with effort-live-control",
					},
				}
			}
			if !reflect.DeepEqual(gaps, want) {
				t.Fatalf("legacy plugin gaps = %+v, want %+v", gaps, want)
			}
		})
	}
}

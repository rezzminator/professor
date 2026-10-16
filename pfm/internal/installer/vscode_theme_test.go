package installer

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// vscodeThemeDriverJS stubs the 'vscode' module and fs.watchFile, activates
// the real embedded extension.js, and walks the `pfm theme` contract
// (themesync's {stateDir}/current.json) through every state the extension
// meets: absent, set, the same generation again, a new generation dropping a
// key, a user edit, off, a null and an array contract, a malformed file and a
// refused settings write. After
// each step it prints the user's global workbench.colorCustomizations, the
// ack the extension wrote, how many settings writes it made so far and
// whether its applied-keys record exists. Mode "default-dir" prints only the
// path the extension watches when professor.theme.stateDir is empty.
const vscodeThemeDriverJS = `
const Module = require('module');
const fs = require('fs');
const path = require('path');
const [extensionPath, mode, stateDir] = process.argv.slice(2);

let colors = { 'editor.background': '#000000', 'terminal.background': '#abcdef' };
let writes = 0;
let refuse = false;
const watched = [];
fs.watchFile = (file) => { watched.push(file); };
fs.unwatchFile = () => {};
const vscodeStub = {
  ConfigurationTarget: { Global: 1, Workspace: 2, WorkspaceFolder: 3 },
  workspace: {
    getConfiguration(section) {
      if (section === 'workbench') {
        return {
          inspect: (key) => (key === 'colorCustomizations' ? { globalValue: JSON.parse(JSON.stringify(colors)) } : undefined),
          update: (key, value, target) => {
            if (refuse) return Promise.reject(new Error('settings.json refused the write'));
            if (key !== 'colorCustomizations' || target !== 1) return Promise.reject(new Error('unexpected update ' + key + ' ' + target));
            writes++;
            colors = value;
            return Promise.resolve();
          },
        };
      }
      if (section === 'professor.theme') {
        // get is the merged value, workspace over user; a workspace-dir run sets
        // the directory only in the workspace layer, where a cloned repo can.
        const layers = mode === 'default-dir' ? {} : mode === 'workspace-dir' ? { workspaceValue: stateDir } : { globalValue: stateDir };
        return {
          get: (key) => (key === 'stateDir' ? layers.workspaceValue ?? layers.globalValue ?? '' : undefined),
          inspect: (key) => (key === 'stateDir' ? { defaultValue: '', ...layers } : undefined),
        };
      }
      return { get: () => undefined };
    },
  },
  window: {
    onDidOpenTerminal: () => ({ dispose() {} }),
    registerTerminalProfileProvider: () => ({ dispose() {} }),
  },
  commands: { registerCommand: () => ({ dispose() {} }), executeCommand: () => Promise.resolve() },
  ThemeIcon: function (id) { this.id = id; },
  ThemeColor: function (id) { this.id = id; },
  TerminalProfile: function (options) { return options; },
};
const originalLoad = Module._load;
Module._load = function (request) {
  if (request === 'vscode') return vscodeStub;
  return originalLoad.apply(this, arguments);
};

const store = {};
const context = {
  extension: { id: 'professor.professor' },
  subscriptions: [],
  globalState: { get: (key, def) => (key in store ? store[key] : def), update: (key, value) => { store[key] = value; } },
};
const api = require(extensionPath).activate(context);
// An extension without the theme API applies nothing; the steps below then
// report what it left, so the test fails on what it asserts, not on a crash.
const syncTheme = (api && api.syncTheme) || (() => Promise.resolve());
if (mode === 'default-dir' || mode === 'workspace-dir') {
  process.stdout.write(JSON.stringify(watched));
  process.exit(0);
}

const current = path.join(stateDir, 'current.json');
const contract = (generation, enabled, palette) => JSON.stringify({ enabled, generation, palette: 'solarized', title: 'Solarized', mode: 'auto', appearance: 'dark', colors: palette });
const steps = [
  ['inert', null],
  ['set', contract(1, true, { 'terminal.background': '#111111', 'terminal.foreground': '#eeeeee', 'terminal.tab.activeBorder': '#222222', 'editor.background': '#ff0000', 'terminal.ansiRed': 'red; url(x)' })],
  ['same-generation', null],
  ['new-generation', contract(2, true, { 'terminal.background': '#333333', 'terminal.foreground': '#dddddd' })],
  ['user-edit', null],
  ['off', contract(3, false, {})],
  ['null', 'null'],
  ['array', '[]'],
  ['malformed', '{not json'],
  ['refused', contract(4, true, { 'terminal.background': '#444444' })],
];
(async () => {
  const out = [];
  for (const [name, body] of steps) {
    if (name === 'user-edit') colors = { ...colors, 'terminal.foreground': '#999999' };
    if (name === 'refused') refuse = true;
    if (body !== null) fs.writeFileSync(current, body);
    await syncTheme();
    let ack = null;
    try { ack = JSON.parse(fs.readFileSync(path.join(stateDir, 'vscode-ack.json'), 'utf8')); } catch (err) { if (err.code !== 'ENOENT') throw err; }
    out.push({ step: name, colors, ack, writes, record: fs.existsSync(path.join(stateDir, 'vscode-applied.json')) });
  }
  process.stdout.write(JSON.stringify({ watched, steps: out }));
})().catch((err) => { process.stderr.write(String(err && err.stack || err)); process.exit(1); });
`

type vscodeThemeAck struct {
	Generation *int     `json:"generation"`
	AppliedAt  string   `json:"appliedAt"`
	Keys       []string `json:"keys"`
	Error      string   `json:"error"`
}

type vscodeThemeStep struct {
	Step   string            `json:"step"`
	Colors map[string]string `json:"colors"`
	Ack    *vscodeThemeAck   `json:"ack"`
	Writes int               `json:"writes"`
	Record bool              `json:"record"`
}

// TestVSCodeExtensionFollowsThePfmThemeContract: the extension merges the
// contract's colours into the user's global colorCustomizations, touching no
// other key; drops a key a newer generation no longer carries; writes nothing
// when nothing changed; on off restores each key's prior value unless the user
// changed it since; acks every generation it applied and names every failure
// in the ack rather than going silent; and with no contract does nothing.
func TestVSCodeExtensionFollowsThePfmThemeContract(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	var run struct {
		Watched []string          `json:"watched"`
		Steps   []vscodeThemeStep `json:"steps"`
	}
	if err := json.Unmarshal(runVSCodeThemeDriver(t, "contract", stateDir, nil), &run); err != nil {
		t.Fatalf("decode theme driver output: %v", err)
	}
	if want := []string{filepath.Join(stateDir, "current.json")}; !reflect.DeepEqual(run.Watched, want) {
		t.Fatalf("watched %v, want %v", run.Watched, want)
	}
	user := map[string]string{"editor.background": "#000000", "terminal.background": "#abcdef"}
	gen := func(n int) *int { return &n }
	allKeys := []string{"terminal.background", "terminal.foreground", "terminal.tab.activeBorder"}
	cases := []struct {
		step       string
		colors     map[string]string
		generation *int
		keys       []string
		errorPart  string
		writes     int
		record     bool
	}{
		{step: "inert", colors: user},
		{step: "set", colors: map[string]string{
			"editor.background": "#000000", "terminal.background": "#111111",
			"terminal.foreground": "#eeeeee", "terminal.tab.activeBorder": "#222222",
		}, generation: gen(1), keys: allKeys, writes: 1, record: true},
		{step: "same-generation", colors: map[string]string{
			"editor.background": "#000000", "terminal.background": "#111111",
			"terminal.foreground": "#eeeeee", "terminal.tab.activeBorder": "#222222",
		}, generation: gen(1), keys: allKeys, writes: 1, record: true},
		{step: "new-generation", colors: map[string]string{
			"editor.background": "#000000", "terminal.background": "#333333", "terminal.foreground": "#dddddd",
		}, generation: gen(2), keys: []string{"terminal.background", "terminal.foreground"}, writes: 2, record: true},
		{step: "user-edit", colors: map[string]string{
			"editor.background": "#000000", "terminal.background": "#333333", "terminal.foreground": "#999999",
		}, generation: gen(2), keys: []string{"terminal.background", "terminal.foreground"}, writes: 2, record: true},
		{step: "off", colors: map[string]string{
			"editor.background": "#000000", "terminal.background": "#abcdef", "terminal.foreground": "#999999",
		}, generation: gen(3), keys: []string{}, writes: 3},
		// JSON that parses to no contract object is a broken contract, acked as
		// one: never a crash that leaves the last ack standing, never an off.
		{step: "null", colors: map[string]string{
			"editor.background": "#000000", "terminal.background": "#abcdef", "terminal.foreground": "#999999",
		}, keys: []string{}, errorPart: "current.json", writes: 3},
		{step: "array", colors: map[string]string{
			"editor.background": "#000000", "terminal.background": "#abcdef", "terminal.foreground": "#999999",
		}, keys: []string{}, errorPart: "current.json", writes: 3},
		{step: "malformed", colors: map[string]string{
			"editor.background": "#000000", "terminal.background": "#abcdef", "terminal.foreground": "#999999",
		}, keys: []string{}, errorPart: "current.json", writes: 3},
		{
			step: "refused", colors: map[string]string{
				"editor.background": "#000000", "terminal.background": "#abcdef", "terminal.foreground": "#999999",
			}, generation: gen(4), keys: []string{"terminal.background"}, errorPart: "settings.json refused the write",
			writes: 3,
		},
	}
	if len(run.Steps) != len(cases) {
		t.Fatalf("driver ran %d steps, want %d: %+v", len(run.Steps), len(cases), run.Steps)
	}
	for i, want := range cases {
		got := run.Steps[i]
		if got.Step != want.step {
			t.Fatalf("step %d = %q, want %q", i, got.Step, want.step)
		}
		if !reflect.DeepEqual(got.Colors, want.colors) {
			t.Errorf("%s: colorCustomizations = %v, want %v", want.step, got.Colors, want.colors)
		}
		if got.Writes != want.writes || got.Record != want.record {
			t.Errorf(
				"%s: writes=%d record=%v, want writes=%d record=%v",
				want.step, got.Writes, got.Record, want.writes, want.record,
			)
		}
		if want.step == "inert" {
			if got.Ack != nil {
				t.Errorf("inert: ack written with no contract: %+v", got.Ack)
			}
			continue
		}
		if got.Ack == nil {
			t.Errorf("%s: no ack written", want.step)
			continue
		}
		if !reflect.DeepEqual(got.Ack.Generation, want.generation) {
			t.Errorf("%s: ack generation = %v, want %v", want.step, got.Ack.Generation, want.generation)
		}
		if !slices.Equal(got.Ack.Keys, want.keys) {
			t.Errorf("%s: ack keys = %v, want %v", want.step, got.Ack.Keys, want.keys)
		}
		if (want.errorPart == "") != (got.Ack.Error == "") || !strings.Contains(got.Ack.Error, want.errorPart) {
			t.Errorf("%s: ack error = %q, want it to hold %q", want.step, got.Ack.Error, want.errorPart)
		}
		if _, err := time.Parse(time.RFC3339, got.Ack.AppliedAt); err != nil {
			t.Errorf("%s: ack appliedAt %q is not RFC3339: %v", want.step, got.Ack.AppliedAt, err)
		}
	}
}

// TestVSCodeExtensionThemeStateDirDefaultsToPfmsAndActivatesAtStartup: an
// empty professor.theme.stateDir, or one only a workspace sets, watches
// themesync.DefaultStateDir(home); the setting is machine-scoped, so no
// workspace can point it elsewhere; and the manifest activates at startup so a
// window follows without a terminal.
func TestVSCodeExtensionThemeStateDirDefaultsToPfmsAndActivatesAtStartup(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	want := []string{filepath.Join(home, ".local", "state", "pfm", "theme", "current.json")}
	for _, mode := range []string{"default-dir", "workspace-dir"} {
		var watched []string
		if err := json.Unmarshal(
			runVSCodeThemeDriver(t, mode, filepath.Join(t.TempDir(), "repo-controlled"), []string{"HOME=" + home}),
			&watched,
		); err != nil {
			t.Fatalf("decode %s output: %v", mode, err)
		}
		if !reflect.DeepEqual(watched, want) {
			t.Errorf("%s: watched %v, want %v", mode, watched, want)
		}
	}
	raw, err := embeddedAssets.ReadFile("assets/" + vscodeExtensionSource + "/package.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		ActivationEvents []string `json:"activationEvents"`
		Contributes      struct {
			Configuration struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"configuration"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(manifest.ActivationEvents, "onStartupFinished") {
		t.Fatalf("activationEvents %v missing onStartupFinished", manifest.ActivationEvents)
	}
	var setting struct {
		Scope string `json:"scope"`
	}
	raw, ok := manifest.Contributes.Configuration.Properties["professor.theme.stateDir"]
	if !ok {
		t.Fatalf("package.json contributes no professor.theme.stateDir setting")
	}
	if err := json.Unmarshal(raw, &setting); err != nil || setting.Scope != "machine" {
		t.Fatalf("professor.theme.stateDir scope = %q (%v), want machine", setting.Scope, err)
	}
}

// runVSCodeThemeDriver runs vscodeThemeDriverJS under Node over the embedded
// extension.js and returns its stdout.
func runVSCodeThemeDriver(t *testing.T, mode, stateDir string, env []string) []byte {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("named gap: node unavailable; extension.js theme sync not exercised")
	}
	extensionPath, err := filepath.Abs(filepath.Join("assets", "vscode", "professor", "extension.js"))
	if err != nil {
		t.Fatal(err)
	}
	driverPath := filepath.Join(t.TempDir(), "theme-driver.js")
	if err := os.WriteFile(driverPath, []byte(vscodeThemeDriverJS), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, driverPath, extensionPath, mode, stateDir)
	command.Env = append(os.Environ(), env...)
	output, err := command.Output()
	if err != nil {
		var stderr []byte
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stderr = exitErr.Stderr
		}
		t.Fatalf("run theme driver (mode %s): %v: %s%s", mode, err, output, stderr)
	}
	return output
}

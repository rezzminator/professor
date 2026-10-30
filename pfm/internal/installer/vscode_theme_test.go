package installer

import (
	"encoding/json"
	"errors"
	"fmt"
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
// path the extension watches when professor.theme.stateDir is empty. Mode
// "scenario" is one window described by the JSON in PFM_THEME_SCENARIO: its
// remote name and workspace file, the colours its Global and Workspace
// settings already hold, Peacock's presence and excludedSettings, an ack an
// earlier window left, and the steps to walk (a contract to write, a
// peacock.excludedSettings edit, a configuration change to fire, a refused
// Workspace write); after each step it prints what both settings layers, the
// ack and the chrome record hold, and how many Workspace-target updates were
// attempted.
const vscodeThemeDriverJS = `
const Module = require('module');
const fs = require('fs');
const path = require('path');
const [extensionPath, mode, stateDir] = process.argv.slice(2);
const scenario = mode === 'scenario' ? JSON.parse(process.env.PFM_THEME_SCENARIO) : {};
const clone = (value) => (value === undefined ? undefined : JSON.parse(JSON.stringify(value)));

let colors = clone(scenario.global) || { 'editor.background': '#000000', 'terminal.background': '#abcdef' };
let workspaceColors = clone(scenario.workspace);
let writes = 0;
let workspaceUpdates = 0;
let refuse = false;
let refuseWorkspace = false;
let excluded = scenario.peacock ? scenario.peacock.excluded : [];
const configListeners = [];
const watched = [];
const messages = [];
fs.watchFile = (file) => { watched.push(file); };
fs.unwatchFile = () => {};
const vscodeStub = {
  ConfigurationTarget: { Global: 1, Workspace: 2, WorkspaceFolder: 3 },
  env: { remoteName: scenario.remoteName || undefined },
  ExtensionKind: { UI: 1, Workspace: 2 },
  workspace: { textDocuments: [], onDidOpenTextDocument: () => ({ dispose() {} }), onDidCloseTextDocument: () => ({ dispose() {} }), // prompt lint
    workspaceFile: scenario.workspaceFile ? { scheme: scenario.workspaceScheme || 'file', fsPath: scenario.workspaceFile,
      toString: () => (scenario.workspaceScheme || 'file') + '://' + (scenario.remoteName ? 'ssh-remote+fixture' : '') + scenario.workspaceFile } : undefined,
    workspaceFolders: scenario.folder ? [{ uri: { toString: () => scenario.folder } }] : undefined,
    onDidChangeConfiguration: (listener) => {
      configListeners.push(listener);
      return { dispose() { configListeners.splice(configListeners.indexOf(listener), 1); } };
    },
    getConfiguration(section) {
      if (section === 'workbench') {
        return {
          inspect: (key) => (key === 'colorCustomizations' ? { globalValue: clone(colors), workspaceValue: clone(workspaceColors) } : undefined),
          update: (key, value, target) => {
            if (key === 'colorCustomizations' && target === 2) {
              workspaceUpdates++;
              if (refuseWorkspace) return Promise.reject(new Error('workspace file refused the write'));
              workspaceColors = clone(value);
              return Promise.resolve();
            }
            if (refuse) return Promise.reject(new Error('settings.json refused the write'));
            if (key !== 'colorCustomizations' || target !== 1) return Promise.reject(new Error('unexpected update ' + key + ' ' + target));
            writes++;
            colors = value;
            return Promise.resolve();
          },
        };
      }
      if (section === 'peacock') {
        return { get: (key, def) => (key === 'excludedSettings' ? clone(excluded) : def) };
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
    showErrorMessage: (message) => { messages.push({ kind: 'error', message }); return Promise.resolve(); },
    showWarningMessage: (message) => { messages.push({ kind: 'warning', message }); return Promise.resolve(); },
  },
  languages: { createDiagnosticCollection: () => ({ dispose() {} }), registerCodeActionsProvider: () => ({ dispose() {} }), match: () => 0 },
  Disposable: { from: () => ({ dispose() {} }) }, CodeActionKind: { QuickFix: 'quickfix' }, // prompt lint (lint.js), unexercised here
  extensions: { getExtension: (id) => (id === 'johnpapa.vscode-peacock' && scenario.peacock ? { id } : undefined) },
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
  extension: { id: 'professor.professor', extensionKind: scenario.extensionKind },
  subscriptions: [],
  globalState: { get: (key, def) => (key in store ? store[key] : def), update: (key, value) => { store[key] = value; } },
};
` + vscodeThemeLockSetupJS + `
const api = require(extensionPath).activate(context);
// An extension without the theme API applies nothing; the steps below then
// report what it left, so the test fails on what it asserts, not on a crash.
const syncTheme = (api && api.syncTheme) || (() => Promise.resolve());
if (mode === 'default-dir' || mode === 'workspace-dir') {
  process.stdout.write(JSON.stringify(watched));
  process.exit(0);
}

const current = path.join(stateDir, 'current.json');
const readJSONFile = (name) => {
  try { return JSON.parse(fs.readFileSync(path.join(stateDir, name), 'utf8')); } catch (err) { if (err.code !== 'ENOENT') throw err; }
  return null;
};
const snapshot = (name) => ({
  step: name, colors, workspace: workspaceColors === undefined ? null : workspaceColors, ack: readJSONFile('vscode-ack.json'),
  writes, workspaceUpdates, chromeRecord: readJSONFile('vscode-chrome.json'), record: fs.existsSync(path.join(stateDir, 'vscode-applied.json')),
  messages: [...messages],
});
// A fired configuration change reaches the extension's own listener, which queues its own sync;
// the driver calls none, and waits for the ack that run rewrites.
const settled = async (before) => {
  for (let waited = 0; waited < 1500; waited += 10) {
    await new Promise((resolve) => setTimeout(resolve, 10));
    if (JSON.stringify(readJSONFile('vscode-ack.json')) !== before) return;
  }
};
` + vscodeThemeLockResultJS + `
if (mode === 'scenario') {
  (async () => {
    if (scenario.prevAck) fs.writeFileSync(path.join(stateDir, 'vscode-ack.json'), JSON.stringify(scenario.prevAck));
    const out = [];
    for (const step of scenario.steps) {
      if (step.peacockExcluded !== undefined) excluded = step.peacockExcluded;
      if (step.refuseWorkspace !== undefined) refuseWorkspace = step.refuseWorkspace;
      if (step.contract) fs.writeFileSync(current, JSON.stringify(step.contract));
      if (step.fire) {
        const before = JSON.stringify(readJSONFile('vscode-ack.json'));
        for (const listener of [...configListeners]) listener({ affectsConfiguration: (section) => section === step.fire });
        await settled(before);
      } else {
        await syncTheme();
      }
      out.push(snapshot(step.name));
    }
    process.stdout.write(JSON.stringify({ steps: out }));
  })().catch((err) => { process.stderr.write(String(err && err.stack || err)); process.exit(1); });
  return;
}
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
	Generation *int                            `json:"generation"`
	AppliedAt  string                          `json:"appliedAt"`
	Keys       []string                        `json:"keys"`
	Error      string                          `json:"error"`
	Chrome     map[string]vscodeChromeAckEntry `json:"chrome"`
}

// vscodeChromeAckEntry is one local workspace's entry in the ack's chrome map.
type vscodeChromeAckEntry struct {
	Generation *int     `json:"generation"`
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

// chromeWanted is the chrome a contract carries for the tests below: invented
// hex values under the eight prefixes the extension accepts.
var chromeWanted = map[string]string{
	"statusBar.debuggingBorder":      "#2aa198",
	"window.activeBorder":            "#2aa198",
	"window.inactiveBorder":          "#174a56",
	"titleBar.activeBackground":      "#0b2a33",
	"titleBar.activeForeground":      "#e6f4f1",
	"activityBar.background":         "#102f38",
	"activityBarBadge.background":    "#2aa198",
	"commandCenter.background":       "#0b2a33",
	"sash.hoverBorder":               "#2aa198",
	"statusBar.background":           "#0f3a44",
	"statusBarItem.hoverBackground":  "#174a56",
	"statusBarItem.remoteBackground": "#1e5a68",
}

// chromeJunk is what the extension drops from a contract's chrome: a key
// outside the eight prefixes and a value that is not a hex colour.
var chromeJunk = map[string]string{
	"editor.background": "#ff0000", "statusBar.border": "red; url(x)", "window.invalidBorder": "#12345",
}

var terminalWanted = map[string]string{"terminal.background": "#111111", "terminal.foreground": "#eeeeee"}

// vscodePainted is a workspace file's colorCustomizations as Peacock leaves
// them: its cyan on some chrome keys, a key of the user's own and a
// per-theme block.
func vscodePainted() map[string]any {
	return map[string]any{
		"titleBar.activeBackground":      "#15c2cb",
		"titleBar.inactiveBackground":    "#15c2cb99",
		"activityBar.background":         "#15c2cb",
		"statusBar.background":           "#15c2cb",
		"editor.lineHighlightBackground": "#2a3a4a",
		"[Solarized Dark]": map[string]any{
			"editor.background":         "#002b36",
			"titleBar.activeBackground": "#073642",
		},
	}
}

// vscodeChromeStepSpec is one step of a "scenario" driver run.
type vscodeChromeStepSpec struct {
	Name            string         `json:"name"`
	Contract        map[string]any `json:"contract,omitempty"`
	PeacockExcluded []string       `json:"peacockExcluded,omitempty"`
	RefuseWorkspace *bool          `json:"refuseWorkspace,omitempty"`
	Fire            string         `json:"fire,omitempty"`
}

// vscodeScenario is one window: PFM_THEME_SCENARIO for the "scenario" driver mode.
type vscodeScenario struct {
	RemoteName string `json:"remoteName,omitempty"`
	// ExtensionKind is where the extension host runs (1 UI, 2 Workspace:
	// vscode.ExtensionKind), 0 left unset as in a local window.
	ExtensionKind   int                    `json:"extensionKind,omitempty"`
	WorkspaceFile   string                 `json:"workspaceFile,omitempty"`
	WorkspaceScheme string                 `json:"workspaceScheme,omitempty"`
	Folder          string                 `json:"folder,omitempty"`
	Global          map[string]any         `json:"global,omitempty"`
	Workspace       map[string]any         `json:"workspace,omitempty"`
	Peacock         *vscodeScenarioPeacock `json:"peacock,omitempty"`
	PrevAck         map[string]any         `json:"prevAck,omitempty"`
	Steps           []vscodeChromeStepSpec `json:"steps"`
}

type vscodeScenarioPeacock struct {
	Excluded []string `json:"excluded"`
}

type vscodeChromeRecord map[string]struct {
	Generation *int `json:"generation"`
	Keys       map[string]struct {
		Value string  `json:"value"`
		Prior *string `json:"prior"`
	} `json:"keys"`
}

// vscodeScenarioStep is what the driver printed after one scenario step.
type vscodeScenarioStep struct {
	Step             string             `json:"step"`
	Colors           map[string]any     `json:"colors"`
	Workspace        map[string]any     `json:"workspace"`
	Ack              *vscodeThemeAck    `json:"ack"`
	Writes           int                `json:"writes"`
	WorkspaceUpdates int                `json:"workspaceUpdates"`
	ChromeRecord     vscodeChromeRecord `json:"chromeRecord"`
	Record           bool               `json:"record"`
	Messages         []struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	} `json:"messages"`
}

func chromeContract(generation int, enabled bool, colors, chrome map[string]string) map[string]any {
	return map[string]any{
		"enabled": enabled, "generation": generation, "palette": "solarized", "title": "Solarized",
		"mode": "auto", "appearance": "dark", "colors": colors, "chrome": chrome,
	}
}

// merged copies base and sets over's entries on it.
func merged(base map[string]any, over ...map[string]string) map[string]any {
	out := map[string]any{}
	for key, value := range base {
		out[key] = value
	}
	for _, layer := range over {
		for key, value := range layer {
			out[key] = value
		}
	}
	return out
}

// union is the colours of every layer, later layers winning.
func union(layers ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, layer := range layers {
		for key, value := range layer {
			out[key] = value
		}
	}
	return out
}

func sortedKeys(maps ...map[string]string) []string {
	seen := map[string]bool{}
	for _, layer := range maps {
		for key := range layer {
			seen[key] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func runVSCodeScenario(t *testing.T, stateDir string, scenario vscodeScenario) []vscodeScenarioStep {
	t.Helper()
	encoded, err := json.Marshal(scenario)
	if err != nil {
		t.Fatal(err)
	}
	var run struct {
		Steps []vscodeScenarioStep `json:"steps"`
	}
	output := runVSCodeThemeDriver(t, "scenario", stateDir, []string{"PFM_THEME_SCENARIO=" + string(encoded)})
	if err := json.Unmarshal(output, &run); err != nil {
		t.Fatalf("decode scenario output: %v", err)
	}
	if len(run.Steps) != len(scenario.Steps) {
		t.Fatalf("scenario ran %d steps, want %d: %+v", len(run.Steps), len(scenario.Steps), run.Steps)
	}
	return run.Steps
}

// chromeEntry returns step's ack entry for workspace, failing the test when the ack or the entry is absent.
func chromeEntry(t *testing.T, step vscodeScenarioStep, workspace string) vscodeChromeAckEntry {
	t.Helper()
	if step.Ack == nil {
		t.Fatalf("%s: no ack written", step.Step)
	}
	entry, ok := step.Ack.Chrome[workspace]
	if !ok {
		t.Fatalf("%s: ack chrome %v has no entry for %s", step.Step, step.Ack.Chrome, workspace)
	}
	return entry
}

func assertChromeEntry(t *testing.T, step vscodeScenarioStep, workspace string, generation int, keys []string) {
	t.Helper()
	entry := chromeEntry(t, step, workspace)
	if entry.Generation == nil || *entry.Generation != generation {
		t.Errorf("%s: ack chrome generation = %v, want %d", step.Step, entry.Generation, generation)
	}
	if !slices.Equal(entry.Keys, keys) {
		t.Errorf("%s: ack chrome keys = %v, want %v", step.Step, entry.Keys, keys)
	}
	if entry.Error != "" {
		t.Errorf("%s: ack chrome error = %q, want none", step.Step, entry.Error)
	}
}

// Every window, including Remote-SSH, must read the local contract and receive its frame.
func TestVSCodeEveryWindowFollowsTheFrameContract(t *testing.T) {
	t.Parallel()
	for _, window := range []vscodeScenario{
		{},
		{RemoteName: "ssh-remote", ExtensionKind: 2, WorkspaceFile: "/remote/project.code-workspace", WorkspaceScheme: "vscode-remote"},
		{WorkspaceFile: "/w/Untitled-1.code-workspace", WorkspaceScheme: "untitled"},
	} {
		window.Global = map[string]any{"editor.background": "#000000"}
		window.Steps = []vscodeChromeStepSpec{
			{Name: "set", Contract: chromeContract(1, true, terminalWanted, chromeWanted)},
		}
		got := runVSCodeScenario(t, t.TempDir(), window)[0]
		wantGlobal := merged(window.Global, terminalWanted, chromeWanted)
		wantWorkspace := merged(nil, chromeWanted)
		if window.RemoteName != "" {
			wantGlobal = window.Global
			wantWorkspace = merged(nil, terminalWanted, chromeWanted)
		}
		if want := wantGlobal; !reflect.DeepEqual(got.Colors, want) {
			t.Errorf(
				"remote=%q scheme=%q: Global frame = %v, want %v",
				window.RemoteName,
				window.WorkspaceScheme,
				got.Colors,
				want,
			)
		}
		if window.WorkspaceFile != "" && !reflect.DeepEqual(got.Workspace, wantWorkspace) {
			t.Errorf(
				"remote=%q scheme=%q: Workspace frame = %v, want contract chrome",
				window.RemoteName,
				window.WorkspaceScheme,
				got.Workspace,
			)
		}
	}
	raw, err := embeddedAssets.ReadFile("assets/" + vscodeExtensionSource + "/package.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		ExtensionKind []string `json:"extensionKind"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	// VS Code takes the client copy's extensionKind when Professor is on both sides
	// (extensionHostKind.ts ExtensionInfo.kind): any "ui" there lets a Remote-SSH window
	// run Professor on the client, against the client's contract and the client's pfm.
	if !slices.Equal(manifest.ExtensionKind, []string{"workspace"}) {
		t.Errorf(
			"extensionKind = %v, want [workspace]: a Remote-SSH window runs Professor on the remote host, where its pfm, contract and Claude chats are, or not at all",
			manifest.ExtensionKind,
		)
	}
}

// TestVSCodeExtensionPaintsALocalWindowsChromeIntoItsWorkspace: a local window
// on a saved workspace takes the contract's chrome colours into that
// workspace's colorCustomizations over what Peacock left there, touching no
// other workspace key and no Global key; a repeated generation writes nothing;
// a newer one restores each key it drops; off puts the workspace back exactly.
func TestVSCodeExtensionPaintsALocalWindowsChromeIntoItsWorkspace(t *testing.T) {
	t.Parallel()
	const workspace = "/w/mac.code-workspace"
	original := vscodePainted()
	globalStart := map[string]any{"editor.background": "#000000", "terminal.background": "#abcdef"}
	subset := map[string]string{
		"titleBar.activeForeground": "#f0f8f6", "activityBar.background": "#102f38", "statusBar.background": "#0a2c35",
	}
	steps := runVSCodeScenario(t, t.TempDir(), vscodeScenario{
		WorkspaceFile: workspace,
		Global:        globalStart,
		Workspace:     original,
		Steps: []vscodeChromeStepSpec{
			{Name: "set", Contract: chromeContract(1, true, terminalWanted, union(chromeWanted, chromeJunk))},
			{Name: "same-generation"},
			{Name: "new-generation", Contract: chromeContract(2, true, terminalWanted, subset)},
			{Name: "off", Contract: chromeContract(3, false, map[string]string{}, map[string]string{})},
		},
	})
	globalSet := merged(globalStart, terminalWanted, chromeWanted)
	cases := []struct {
		step       string
		workspace  map[string]any
		global     map[string]any
		updates    int
		generation int
		keys       []string
		record     bool
	}{
		{"set", frameMerged(original, chromeWanted), globalSet, 1, 1, sortedKeys(chromeWanted), true},
		{"same-generation", frameMerged(original, chromeWanted), globalSet, 1, 1, sortedKeys(chromeWanted), true},
		// Keys the new generation drops go back to what the workspace held before: Peacock's cyan,
		// or nothing for a key it never held.
		{
			"new-generation",
			frameMerged(original, subset),
			merged(globalStart, terminalWanted, subset),
			2,
			2,
			sortedKeys(subset),
			true,
		},
		{"off", original, globalStart, 3, 3, []string{}, false},
	}
	for i, want := range cases {
		got := steps[i]
		if got.Step != want.step {
			t.Fatalf("step %d = %q, want %q", i, got.Step, want.step)
		}
		if !reflect.DeepEqual(got.Workspace, want.workspace) {
			t.Errorf("%s: workspace colorCustomizations = %v, want %v", want.step, got.Workspace, want.workspace)
		}
		if !reflect.DeepEqual(got.Colors, want.global) {
			t.Errorf("%s: global colorCustomizations = %v, want %v", want.step, got.Colors, want.global)
		}
		if got.WorkspaceUpdates != want.updates {
			t.Errorf("%s: workspace updates = %d, want %d", want.step, got.WorkspaceUpdates, want.updates)
		}
		if has := got.ChromeRecord != nil; has != want.record {
			t.Errorf("%s: chrome record present = %v, want %v: %v", want.step, has, want.record, got.ChromeRecord)
		}
		assertChromeEntry(t, got, workspace, want.generation, want.keys)
	}
	if want := sortedKeys(terminalWanted); !slices.Equal(steps[0].Ack.Keys, want) {
		t.Errorf("set: ack keys = %v, want the terminal keys %v", steps[0].Ack.Keys, want)
	}
	// The record holds each key's value and what it replaced, per workspace.
	record := steps[0].ChromeRecord[workspace]
	if record.Generation == nil || *record.Generation != 1 || len(record.Keys) != len(chromeWanted)+1 {
		t.Fatalf("set: chrome record = %+v, want generation 1 over %d keys", record, len(chromeWanted))
	}
	title := record.Keys["titleBar.activeBackground"]
	if title.Value != "#0b2a33" || title.Prior == nil || *title.Prior != "#15c2cb" {
		t.Errorf("set: titleBar.activeBackground record = %+v, want value #0b2a33 over prior #15c2cb", title)
	}
	sash := record.Keys["sash.hoverBorder"]
	if sash.Value != "#2aa198" || sash.Prior != nil {
		t.Errorf("set: sash.hoverBorder record = %+v, want value #2aa198 over no prior", sash)
	}
}

// Global covers empty windows; the Workspace API paints saved, folder-only, untitled and remote windows.
func TestVSCodeExtensionPaintsEveryWorkspaceKind(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, target string
		window       vscodeScenario
	}{
		{"empty", "global", vscodeScenario{}},
		{"remote", "vscode-remote://ssh-remote+fixture/w/project.code-workspace", vscodeScenario{RemoteName: "ssh-remote", ExtensionKind: 2, WorkspaceFile: "/w/project.code-workspace", WorkspaceScheme: "vscode-remote"}},
		{"folder-only", "file:///w/project", vscodeScenario{Folder: "file:///w/project"}},
		{"remote-folder", "vscode-remote://ssh-remote+fixture/project", vscodeScenario{RemoteName: "ssh-remote", ExtensionKind: 2, Folder: "vscode-remote://ssh-remote+fixture/project"}},
		{"untitled", "untitled:///w/Untitled-1.code-workspace", vscodeScenario{WorkspaceFile: "/w/Untitled-1.code-workspace", WorkspaceScheme: "untitled"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.window.Global = map[string]any{"editor.background": "#000000"}
			tc.window.Workspace = vscodePainted()
			tc.window.PrevAck = map[string]any{
				"chrome": map[string]any{
					"/w/other.code-workspace": map[string]any{"generation": 1, "keys": []string{}, "error": ""},
				},
			}
			tc.window.Steps = []vscodeChromeStepSpec{
				{Name: "set", Contract: chromeContract(2, true, terminalWanted, chromeWanted)},
				{Name: "off", Contract: chromeContract(3, false, nil, nil)},
			}
			steps := runVSCodeScenario(t, t.TempDir(), tc.window)
			for i, got := range steps {
				wantGlobal, wantWorkspace := tc.window.Global, vscodePainted()
				if i == 0 {
					if tc.window.RemoteName == "" {
						wantGlobal = merged(wantGlobal, terminalWanted, chromeWanted)
					} else {
						wantWorkspace = frameMerged(wantWorkspace, terminalWanted)
					}
					if tc.target != "global" {
						wantWorkspace = frameMerged(wantWorkspace, chromeWanted)
					}
				}
				if !reflect.DeepEqual(got.Colors, wantGlobal) || !reflect.DeepEqual(got.Workspace, wantWorkspace) {
					t.Errorf(
						"%s: global=%v workspace=%v, want global=%v workspace=%v",
						got.Step,
						got.Colors,
						got.Workspace,
						wantGlobal,
						wantWorkspace,
					)
				}
				if tc.target == "global" && got.WorkspaceUpdates != 0 {
					t.Errorf("empty window wrote Workspace settings")
				}
				keys := []string{}
				if i == 0 {
					keys = sortedKeys(chromeWanted)
				}
				assertChromeEntry(t, got, tc.target, 2+i, keys)
				assertChromeEntry(t, got, "/w/other.code-workspace", 1, []string{})
			}
		})
	}
}

// Existing theme-scoped overrides beat unscoped values: expected managed keys follow there too.
func frameMerged(base map[string]any, over map[string]string) map[string]any {
	out := merged(base, over)
	for key, value := range base {
		if !strings.HasPrefix(key, "[") {
			continue
		}
		if block, ok := value.(map[string]any); ok {
			cloned := merged(block)
			for color, hex := range over {
				if _, exists := block[color]; exists {
					cloned[color] = hex
				}
			}
			out[key] = cloned
		}
	}
	return out
}

// TestVSCodeExtensionWithholdsChromePeacockWouldRepaint: Peacock rewrites a
// workspace's colorCustomizations at every window start except the keys in
// peacock.excludedSettings, so with Peacock installed and a wanted key missing
// from that list nothing is written and the workspace's ack entry says so; the
// next sync, fired by an edit of that setting, paints once the list covers
// every key; and putting the workspace back is never withheld.
func TestVSCodeExtensionWithholdsChromePeacockWouldRepaint(t *testing.T) {
	t.Parallel()
	const workspace = "/w/mac.code-workspace"
	original := vscodePainted()
	globalStart := map[string]any{"editor.background": "#000000", "terminal.background": "#abcdef"}
	keys := sortedKeys(chromeWanted)
	covered := keys[:3]
	wantError := fmt.Sprintf(
		"Peacock repaints %d of %d chrome keys at every window start: "+
			"list them in this workspace's peacock.excludedSettings",
		len(keys)-len(covered), len(keys),
	)
	steps := runVSCodeScenario(t, t.TempDir(), vscodeScenario{
		WorkspaceFile: workspace,
		Global:        globalStart,
		Workspace:     original,
		Peacock:       &vscodeScenarioPeacock{Excluded: covered},
		Steps: []vscodeChromeStepSpec{
			{Name: "withheld", Contract: chromeContract(1, true, terminalWanted, chromeWanted)},
			{Name: "covered", PeacockExcluded: keys, Fire: "peacock.excludedSettings"},
			{Name: "uncovered", PeacockExcluded: covered, Fire: "peacock.excludedSettings"},
			{Name: "off", Contract: chromeContract(2, false, map[string]string{}, map[string]string{})},
		},
	})
	withheld, painted, again, off := steps[0], steps[1], steps[2], steps[3]
	if withheld.WorkspaceUpdates != 0 || withheld.ChromeRecord != nil {
		t.Errorf("withheld: updates=%d record=%v, want none", withheld.WorkspaceUpdates, withheld.ChromeRecord)
	}
	if !reflect.DeepEqual(withheld.Workspace, original) {
		t.Errorf("withheld: workspace = %v, want it untouched: %v", withheld.Workspace, original)
	}
	if entry := chromeEntry(t, withheld, workspace); entry.Error != wantError {
		t.Errorf("withheld: ack chrome error = %q, want %q", entry.Error, wantError)
	}
	if want := merged(globalStart, terminalWanted, chromeWanted); !reflect.DeepEqual(withheld.Colors, want) {
		t.Errorf("withheld: global = %v, want the terminal keys applied regardless: %v", withheld.Colors, want)
	}
	wantPainted := frameMerged(original, chromeWanted)
	if !reflect.DeepEqual(painted.Workspace, wantPainted) || painted.WorkspaceUpdates != 1 {
		t.Errorf("covered: workspace=%v updates=%d want %v", painted.Workspace, painted.WorkspaceUpdates, wantPainted)
	}
	assertChromeEntry(t, painted, workspace, 1, keys)
	// The list shrinking again is reported, and what the workspace holds is left as it is.
	if entry := chromeEntry(t, again, workspace); entry.Error != wantError {
		t.Errorf("uncovered: ack chrome error = %q, want %q", entry.Error, wantError)
	}
	if !reflect.DeepEqual(again.Workspace, painted.Workspace) || again.WorkspaceUpdates != 1 {
		t.Errorf("uncovered: workspace=%v updates=%d, want no write", again.Workspace, again.WorkspaceUpdates)
	}
	if !reflect.DeepEqual(off.Workspace, original) || off.ChromeRecord != nil {
		t.Errorf("off: workspace=%v record=%v, want the original despite the guard", off.Workspace, off.ChromeRecord)
	}
	assertChromeEntry(t, off, workspace, 2, []string{})
}

// TestVSCodeExtensionKeepsEachWorkspaceAndTheGlobalRecordIndependent: a window
// on a workspace not yet painted at the current generation paints it even when
// the Global colours are done, and its ack keeps the entry the earlier window
// wrote; a refused Workspace write is that entry's error, leaves the Global
// result standing, and is retried by the next sync.
func TestVSCodeExtensionKeepsEachWorkspaceAndTheGlobalRecordIndependent(t *testing.T) {
	t.Parallel()
	const workspaceA, workspaceB = "/w/a.code-workspace", "/w/b.code-workspace"
	stateDir := t.TempDir()
	first := runVSCodeScenario(t, stateDir, vscodeScenario{
		WorkspaceFile: workspaceA,
		Workspace:     vscodePainted(),
		Steps: []vscodeChromeStepSpec{
			{Name: "set", Contract: chromeContract(1, true, terminalWanted, chromeWanted)},
		},
	})
	assertChromeEntry(t, first[0], workspaceA, 1, sortedKeys(chromeWanted))

	refused := true
	second := runVSCodeScenario(t, stateDir, vscodeScenario{
		WorkspaceFile: workspaceB,
		Workspace:     vscodePainted(),
		Steps: []vscodeChromeStepSpec{
			{Name: "refused", RefuseWorkspace: &refused},
			{Name: "retried", RefuseWorkspace: new(bool)},
		},
	})
	failed, retried := second[0], second[1]
	// The Global record is at generation 1, so this window writes no Global colour.
	if failed.Writes != 0 || retried.Writes != 0 {
		t.Errorf("global writes = %d, %d, want none: the Global record was done", failed.Writes, retried.Writes)
	}
	// The extension's own start-up sync tries the refused write too, so the attempts are counted
	// relative to each other, not absolutely.
	if failed.WorkspaceUpdates < 1 || !reflect.DeepEqual(failed.Workspace, vscodePainted()) {
		t.Errorf("refused: updates=%d workspace=%v, want a refused write", failed.WorkspaceUpdates, failed.Workspace)
	}
	if record := failed.ChromeRecord[workspaceB]; record.Keys != nil {
		t.Errorf("refused: chrome record %+v for the refused workspace, want none", record)
	}
	if entry := chromeEntry(t, failed, workspaceB); !strings.Contains(entry.Error, "workspace file refused the write") {
		t.Errorf("refused: ack chrome error = %q, want the refusal named", entry.Error)
	}
	if failed.Ack.Error != "" {
		t.Errorf("refused: ack error = %q, want the Workspace failure kept to its entry", failed.Ack.Error)
	}
	wantPainted := frameMerged(vscodePainted(), chromeWanted)
	if !reflect.DeepEqual(retried.Workspace, wantPainted) || retried.WorkspaceUpdates != failed.WorkspaceUpdates+1 {
		t.Errorf("retried: workspace=%v updates=%d want %v", retried.Workspace, retried.WorkspaceUpdates, wantPainted)
	}
	assertChromeEntry(t, retried, workspaceB, 1, sortedKeys(chromeWanted))
	assertChromeEntry(t, retried, workspaceA, 1, sortedKeys(chromeWanted))
	for _, workspace := range []string{workspaceA, workspaceB} {
		if retried.ChromeRecord[workspace].Generation == nil {
			t.Errorf("retried: chrome record has no entry for %s: %v", workspace, retried.ChromeRecord)
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

// Upgrading at the same generation still gains newly managed frame keys, Global and Workspace.
func TestVSCodeFrameMigrationAtTheSameGeneration(t *testing.T) {
	t.Parallel()
	original := vscodePainted()
	steps := runVSCodeScenario(t, t.TempDir(), vscodeScenario{
		WorkspaceFile: "/w/migration.code-workspace", Workspace: original,
		Steps: []vscodeChromeStepSpec{
			{
				Name: "old",
				Contract: chromeContract(
					1,
					true,
					terminalWanted,
					map[string]string{"titleBar.activeBackground": chromeWanted["titleBar.activeBackground"]},
				),
			},
			{Name: "upgrade", Contract: chromeContract(1, true, terminalWanted, chromeWanted)},
			{Name: "off", Contract: chromeContract(2, false, nil, nil)},
		},
	})
	if got := steps[1]; !reflect.DeepEqual(got.Workspace, frameMerged(original, chromeWanted)) ||
		got.WorkspaceUpdates != 2 {
		t.Errorf("upgrade: workspace=%v updates=%d", got.Workspace, got.WorkspaceUpdates)
	}
	if got := steps[2]; !reflect.DeepEqual(got.Workspace, original) || got.ChromeRecord != nil {
		t.Errorf(
			"off: workspace=%v record=%v, want original scoped and unscoped values",
			got.Workspace,
			got.ChromeRecord,
		)
	}
}

func TestVSCodeEmptyWindowAcknowledgesPeacockConflict(t *testing.T) {
	t.Parallel()
	steps := runVSCodeScenario(t, t.TempDir(), vscodeScenario{
		Peacock: &vscodeScenarioPeacock{},
		Steps: []vscodeChromeStepSpec{
			{Name: "conflict", Contract: chromeContract(1, true, terminalWanted, chromeWanted)},
		},
	})
	entry := chromeEntry(t, steps[0], "global")
	if !strings.Contains(entry.Error, "peacock.excludedSettings") {
		t.Errorf("error=%q, want actionable conflict", entry.Error)
	}
	if steps[0].Colors["window.activeBorder"] != chromeWanted["window.activeBorder"] {
		t.Error("Global frame was not painted")
	}
}

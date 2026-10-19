package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// vscodeNativeDriverJS stubs the 'vscode' module and fs.watchFile, wraps
// child_process.execFile to know when a pfm run is in flight, activates the
// extension copy at argv[2] (whose themes/index.json the test wrote), and
// walks the steps of the JSON in PFM_NATIVE_SCENARIO: a contract to write and
// sync (pfm, or Claude Code's /theme through the follower, changing the
// palette), a Color Theme the user accepts in the picker (the setting changes
// and the configuration event fires), or a sync of whatever contract the fake
// pfm left. With fireOnUpdate, a Color Theme update fires the configuration
// event as VS Code does, after writing moveContractTo's contract once when
// set (a palette change racing the update). After each step it prints
// workbench.colorTheme, every settings update other than the terminal
// colours, the pfm runs so far, the messages shown and the vscode-native.json
// record.
const vscodeNativeDriverJS = `
const Module = require('module');
const fs = require('fs');
const path = require('path');
const childProcess = require('child_process');
const [extensionPath, stateDir] = process.argv.slice(2);
const scenario = JSON.parse(process.env.PFM_NATIVE_SCENARIO);

let colorTheme = scenario.colorTheme;
let colors = {};
const updates = [];
const messages = [];
const listeners = [];
let inFlight = 0;
let moveContractTo = scenario.moveContractTo;
const fire = () => {
  for (const listener of [...listeners]) {
    listener({ affectsConfiguration: (key) => key === 'workbench' || key === 'workbench.colorTheme' });
  }
};
const execFile = childProcess.execFile;
childProcess.execFile = (file, args, options, callback) => {
  inFlight++;
  return execFile(file, args, options, (...result) => {
    inFlight--;
    callback(...result);
  });
};
fs.watchFile = () => {};
fs.unwatchFile = () => {};
const vscodeStub = {
  ConfigurationTarget: { Global: 1, Workspace: 2, WorkspaceFolder: 3 },
  ProgressLocation: { Notification: 15 },
  env: { remoteName: scenario.remoteName || undefined },
  workspace: {
    workspaceFile: undefined,
    onDidChangeConfiguration: (listener) => {
      listeners.push(listener);
      return { dispose() {} };
    },
    getConfiguration(section) {
      if (section === 'workbench') {
        return {
          get: (key) => (key === 'colorTheme' ? colorTheme : undefined),
          inspect: (key) => (key === 'colorCustomizations' ? { globalValue: colors } : undefined),
          update: (key, value, target) => {
            if (key === 'colorCustomizations') {
              colors = value;
              return Promise.resolve();
            }
            updates.push({ key, value, target });
            if (scenario.refuseUpdate) return Promise.reject(new Error('settings.json refused the write'));
            if (key === 'colorTheme') colorTheme = value;
            if (key === 'colorTheme' && scenario.fireOnUpdate) {
              if (moveContractTo) {
                fs.writeFileSync(path.join(stateDir, 'current.json'), JSON.stringify({
                  enabled: true, generation: 99, palette: moveContractTo, colors: {}, chrome: {},
                }));
                moveContractTo = undefined;
              }
              fire();
            }
            return Promise.resolve();
          },
        };
      }
      if (section === 'professor.theme') {
        return { get: () => stateDir, inspect: (key) => (key === 'stateDir' ? { globalValue: stateDir } : undefined) };
      }
      return { get: () => undefined, inspect: () => undefined };
    },
  },
  window: {
    state: { focused: scenario.unfocused !== true },
    onDidOpenTerminal: () => ({ dispose() {} }),
    registerTerminalProfileProvider: () => ({ dispose() {} }),
    showErrorMessage: (message) => { messages.push({ kind: 'error', message }); return Promise.resolve(); },
    showWarningMessage: (message) => { messages.push({ kind: 'warning', message }); return Promise.resolve(); },
    showInformationMessage: (message) => { messages.push({ kind: 'info', message }); return Promise.resolve(); },
    setStatusBarMessage: (message) => { messages.push({ kind: 'status', message }); return { dispose() {} }; },
  },
  extensions: { getExtension: () => undefined },
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

const context = { extension: { id: 'professor.professor' }, subscriptions: [], globalState: { get: (key, def) => def, update() {} } };
const api = require(extensionPath).activate(context);
const tick = () => new Promise((resolve) => setTimeout(resolve, 5));
// A fired change reaches the extension's own listener, which queues its pick; the pick is idle
// once no pfm run is in flight for a few ticks in a row.
const idle = async () => {
  for (let quiet = 0, waited = 0; quiet < 4 && waited < 30000; waited += 5) {
    await tick();
    quiet = inFlight ? 0 : quiet + 1;
  }
};
const readJSON = (file) => {
  try { return JSON.parse(fs.readFileSync(file, 'utf8')); } catch (err) { if (err.code !== 'ENOENT') throw err; }
  return null;
};
const calls = () => (fs.readFileSync(path.join(process.env.HOME, 'fake-pfm', 'calls.log'), { encoding: 'utf8', flag: 'a+' }).split('\n').filter(Boolean));
(async () => {
  const out = [];
  for (const step of scenario.steps) {
    if (step.contract) fs.writeFileSync(path.join(stateDir, 'current.json'), JSON.stringify(step.contract));
    if (step.pick !== undefined) {
      colorTheme = step.pick;
      fire();
      await idle();
    } else {
      await api.syncTheme();
      if (scenario.fireOnUpdate) await idle();
    }
    out.push({
      step: step.name, colorTheme, updates: JSON.parse(JSON.stringify(updates)), calls: calls(),
      messages: [...messages], record: readJSON(path.join(stateDir, 'vscode-native.json')),
    });
  }
  process.stdout.write(JSON.stringify(out));
})().catch((err) => { process.stderr.write(String(err && err.stack || err)); process.exit(1); });
`

// nativeStep is one step of the walk: a contract to write and sync, a Color
// Theme the user accepts, or (neither) a sync of the contract on disk.
type nativeStep struct {
	Name     string         `json:"name"`
	Contract map[string]any `json:"contract,omitempty"`
	Pick     *string        `json:"pick,omitempty"`
}

type nativeScenario struct {
	ColorTheme     string       `json:"colorTheme"`
	RemoteName     string       `json:"remoteName,omitempty"`
	Unfocused      bool         `json:"unfocused,omitempty"`
	RefuseUpdate   bool         `json:"refuseUpdate,omitempty"`
	FireOnUpdate   bool         `json:"fireOnUpdate,omitempty"`
	MoveContractTo string       `json:"moveContractTo,omitempty"`
	StateMissing   bool         `json:"-"`
	Steps          []nativeStep `json:"steps"`
}

type nativeSnapshot struct {
	Step       string `json:"step"`
	ColorTheme string `json:"colorTheme"`
	Updates    []struct {
		Key    string `json:"key"`
		Value  string `json:"value"`
		Target int    `json:"target"`
	} `json:"updates"`
	Calls    []string `json:"calls"`
	Messages []struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	} `json:"messages"`
	Record *struct {
		Palette string  `json:"palette"`
		ID      *string `json:"id"`
	} `json:"record"`
}

// nativeIndex is the themes/index.json pfm install writes beside the
// extension: two Professor themes.
const nativeIndex = `{"themes": [
  {"id": "professor-lagoon", "label": "Professor · Lagoon", "uiTheme": "vs-dark", "path": "./themes/professor-lagoon.json", "value": "custom:lagoon"},
  {"id": "professor-ultrafunk", "label": "Professor · Ultrafunk", "uiTheme": "vs-dark", "path": "./themes/professor-ultrafunk.json", "value": "custom:ultrafunk"}
]}
`

func nativeContract(generation int, palette string) map[string]any {
	return map[string]any{
		"enabled":    true,
		"generation": generation,
		"palette":    palette,
		"title":      palette,
		"mode":       "auto",
		"appearance": "dark",
		"colors":     map[string]string{"terminal.background": "#101010"},
		"chrome":     map[string]string{},
	}
}

func pick(id string) *string { return &id }

// runVSCodeNative copies the embedded extension beside an index, puts a fake
// pfm at ~/.local/bin/pfm — `theme use {value}` logs its arguments and, like
// the real one, writes a contract naming that palette at the next generation,
// or fails with useStderr when useExit is not zero — and walks scenario.
// With StateMissing the state dir does not exist yet.
func runVSCodeNative(t *testing.T, scenario nativeScenario, useExit int, useStderr string) []nativeSnapshot {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("named gap: node unavailable; native.js Color Theme sync not exercised")
	}
	home, stateDir, extension := t.TempDir(), t.TempDir(), t.TempDir()
	if scenario.StateMissing {
		stateDir = filepath.Join(stateDir, "not-yet")
	}
	source := filepath.Join("assets", "vscode", "professor")
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		writeFixture(t, filepath.Join(extension, entry.Name()), readFixture(t, filepath.Join(source, entry.Name())))
	}
	writeFixture(t, filepath.Join(extension, "themes", "index.json"), nativeIndex)
	writeFixture(t, filepath.Join(home, "fake-pfm", "use.err"), useStderr)
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> "$HOME/fake-pfm/calls.log"
if [ "$1 $2" != "theme use" ]; then echo "fake pfm: unexpected arguments: $*" >&2; exit 64; fi
if [ %d -ne 0 ]; then cat "$HOME/fake-pfm/use.err" >&2; exit %d; fi
generation=$(( $(sed -n 's/.*"generation": *\([0-9]*\).*/\1/p' %q/current.json) + 1 ))
printf '{"enabled": true, "generation": %%d, "palette": "%%s", "colors": {}, "chrome": {}}\n' "$generation" "$3" > %q/current.json
`, useExit, useExit, stateDir, stateDir)
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatalf("mkdir fake pfm bin %s: %v", bin, err)
	}
	if err := testjail.WriteExecutable(filepath.Join(bin, "pfm"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(scenario)
	if err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(t.TempDir(), "native-driver.js")
	if err := os.WriteFile(driver, []byte(vscodeNativeDriverJS), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, driver, filepath.Join(extension, "extension.js"), stateDir)
	command.Env = append(os.Environ(), "HOME="+home, "PFM_NATIVE_SCENARIO="+string(encoded))
	output, err := command.Output()
	if err != nil {
		var stderr []byte
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stderr = exitErr.Stderr
		}
		t.Fatalf("run native driver: %v: %s%s", err, output, stderr)
	}
	var steps []nativeSnapshot
	if err := json.Unmarshal(output, &steps); err != nil {
		t.Fatalf("decode native driver output %s: %v", output, err)
	}
	if len(steps) != len(scenario.Steps) {
		t.Fatalf("native driver walked %d of %d steps: %s", len(steps), len(scenario.Steps), output)
	}
	return steps
}

func recordPalette(step nativeSnapshot) string {
	if step.Record == nil {
		return "<no record>"
	}
	return step.Record.Palette
}

func TestNativeColorThemeAndPfmFollowEachOtherWithoutALoop(t *testing.T) {
	t.Parallel()
	steps := runVSCodeNative(t, nativeScenario{ColorTheme: "Sonar", Steps: []nativeStep{
		{Name: "first-sight", Contract: nativeContract(1, "custom:lagoon")},
		{Name: "pfm-elsewhere", Contract: nativeContract(2, "custom:ultrafunk")},
		{Name: "own-theme", Pick: pick("Sonar")},
		{Name: "resync", Contract: nativeContract(3, "custom:ultrafunk")},
		{Name: "native-pick", Pick: pick("professor-lagoon")},
		{Name: "follower-sees-pick"},
		{Name: "pick-already-active", Pick: pick("professor-lagoon")},
		{Name: "builtin-palette", Contract: nativeContract(9, "dark")},
	}}, 0, "")
	at := map[string]nativeSnapshot{}
	for _, step := range steps {
		at[step.Step] = step
	}

	first := at["first-sight"]
	if first.ColorTheme != "Sonar" || len(first.Updates) != 0 || recordPalette(first) != "custom:lagoon" {
		t.Fatalf("the first contract must be recorded, never followed: %+v", first)
	}
	elsewhere := at["pfm-elsewhere"]
	if elsewhere.ColorTheme != "professor-ultrafunk" || len(elsewhere.Updates) != 1 ||
		elsewhere.Updates[0].Key != "colorTheme" || elsewhere.Updates[0].Target != 1 {
		t.Fatalf("a palette changed elsewhere must set the Global Color Theme to its Professor theme: %+v", elsewhere)
	}
	if own := at["own-theme"]; len(own.Calls) != 0 {
		t.Fatalf("a non-Professor theme must run no pfm: %+v", own)
	}
	if resync := at["resync"]; resync.ColorTheme != "Sonar" || len(resync.Updates) != 1 {
		t.Fatalf("a resync of the same palette must leave the user's own theme alone: %+v", resync)
	}
	native := at["native-pick"]
	if !slices.Equal(native.Calls, []string{"theme use custom:lagoon"}) {
		t.Fatalf("a Professor theme picked natively must run pfm theme use {value} once, got %q", native.Calls)
	}
	if len(native.Messages) != 1 || native.Messages[0].Kind != "status" ||
		!strings.Contains(native.Messages[0].Message, "Professor · Lagoon") {
		t.Fatalf("a picked theme reports its label in the status bar: %+v", native.Messages)
	}
	follower := at["follower-sees-pick"]
	if len(follower.Updates) != 1 || follower.ColorTheme != "professor-lagoon" ||
		recordPalette(follower) != "custom:lagoon" {
		t.Fatalf("the contract pfm wrote for the pick must not set the Color Theme again: %+v", follower)
	}
	if again := at["pick-already-active"]; len(again.Calls) != 1 {
		t.Fatalf("a pick naming the active palette must run no pfm: %q", again.Calls)
	}
	builtin := at["builtin-palette"]
	if builtin.ColorTheme != "professor-lagoon" || len(builtin.Updates) != 1 ||
		recordPalette(builtin) != "dark" || builtin.Record.ID != nil {
		t.Fatalf("a palette without a Professor theme must leave the Color Theme as it is: %+v", builtin)
	}
}

func TestNativeColorThemeNeverMovesARemoteOrUnfocusedWindow(t *testing.T) {
	t.Parallel()
	walk := []nativeStep{
		{Name: "seed", Contract: nativeContract(1, "custom:lagoon")},
		{Name: "pfm-elsewhere", Contract: nativeContract(2, "custom:ultrafunk")},
		{Name: "native-pick", Pick: pick("professor-lagoon")},
	}
	remote := runVSCodeNative(t, nativeScenario{ColorTheme: "Sonar", RemoteName: "ssh-remote", Steps: walk}, 0, "")
	last := remote[len(remote)-1]
	if last.ColorTheme != "professor-lagoon" || len(last.Updates) != 0 || len(last.Calls) != 0 || last.Record != nil {
		t.Fatalf("a remote window must neither set the Color Theme nor run pfm nor record: %+v", last)
	}
	unfocused := runVSCodeNative(t, nativeScenario{ColorTheme: "Sonar", Unfocused: true, Steps: walk}, 0, "")
	if last := unfocused[len(unfocused)-1]; len(last.Calls) != 0 {
		t.Fatalf("an unfocused window must leave the pick to the focused one: %q", last.Calls)
	}
}

func TestNativeColorThemeShowsAFailedRunAndARefusedSwitch(t *testing.T) {
	t.Parallel()
	failed := runVSCodeNative(t, nativeScenario{ColorTheme: "Sonar", Steps: []nativeStep{
		{Name: "seed", Contract: nativeContract(1, "custom:ultrafunk")},
		{Name: "native-pick", Pick: pick("professor-lagoon")},
	}}, 3, "theme custom:lagoon not installed")
	pickStep := failed[1]
	if len(pickStep.Messages) != 1 || pickStep.Messages[0].Kind != "error" ||
		!strings.Contains(pickStep.Messages[0].Message, "theme custom:lagoon not installed") {
		t.Fatalf("a failed pfm run must be shown in pfm's own words: %+v", pickStep.Messages)
	}
	refused := runVSCodeNative(t, nativeScenario{ColorTheme: "Sonar", RefuseUpdate: true, Steps: []nativeStep{
		{Name: "seed", Contract: nativeContract(1, "custom:lagoon")},
		{Name: "pfm-elsewhere", Contract: nativeContract(2, "custom:ultrafunk")},
	}}, 0, "")
	switchStep := refused[1]
	if len(switchStep.Messages) != 1 || switchStep.Messages[0].Kind != "error" ||
		!strings.Contains(switchStep.Messages[0].Message, "settings.json refused the write") {
		t.Fatalf("a refused Color Theme write must be shown: %+v", switchStep.Messages)
	}
}

// TestNativeColorThemeNeverUndoesAContractThatMovedDuringItsOwnSwitch: the
// configuration event a follow's own Color Theme update fires runs no pfm,
// even when the contract has moved to another palette by then.
func TestNativeColorThemeNeverUndoesAContractThatMovedDuringItsOwnSwitch(t *testing.T) {
	t.Parallel()
	for _, moved := range []string{"", "custom:lagoon"} {
		steps := runVSCodeNative(t, nativeScenario{
			ColorTheme: "Sonar", FireOnUpdate: true, MoveContractTo: moved, Steps: []nativeStep{
				{Name: "seed", Contract: nativeContract(1, "custom:lagoon")},
				{Name: "pfm-elsewhere", Contract: nativeContract(2, "custom:ultrafunk")},
			},
		}, 0, "")
		last := steps[len(steps)-1]
		if last.ColorTheme != "professor-ultrafunk" || len(last.Updates) != 1 || len(last.Calls) != 0 {
			t.Fatalf("moved %q: a follow's own switch must run no pfm: %+v", moved, last)
		}
	}
}

// TestNativeColorThemePickedBeforeTheFirstSyncStillRuns: a Professor theme
// picked before pfm ever wrote the state dir records into a dir it creates
// and runs pfm theme use.
func TestNativeColorThemePickedBeforeTheFirstSyncStillRuns(t *testing.T) {
	t.Parallel()
	steps := runVSCodeNative(t, nativeScenario{ColorTheme: "Sonar", StateMissing: true, Steps: []nativeStep{
		{Name: "native-pick", Pick: pick("professor-lagoon")},
	}}, 0, "")
	native := steps[0]
	if !slices.Equal(native.Calls, []string{"theme use custom:lagoon"}) || recordPalette(native) != "custom:lagoon" {
		t.Fatalf("a pick with no state dir yet must record and run pfm theme use: %+v", native)
	}
}

// TestNativeColorThemeRetriesARefusedSwitchAtTheNextSync: a Color Theme write
// VS Code refuses leaves the prior record, so a resync of the same palette
// tries the switch again.
func TestNativeColorThemeRetriesARefusedSwitchAtTheNextSync(t *testing.T) {
	t.Parallel()
	steps := runVSCodeNative(t, nativeScenario{ColorTheme: "Sonar", RefuseUpdate: true, Steps: []nativeStep{
		{Name: "seed", Contract: nativeContract(1, "custom:lagoon")},
		{Name: "pfm-elsewhere", Contract: nativeContract(2, "custom:ultrafunk")},
		{Name: "resync", Contract: nativeContract(3, "custom:ultrafunk")},
	}}, 0, "")
	if refused := steps[1]; recordPalette(refused) != "custom:lagoon" {
		t.Fatalf("a refused switch must leave the prior record: %+v", refused)
	}
	if resync := steps[2]; len(resync.Updates) != 2 || len(resync.Messages) != 2 {
		t.Fatalf("the next sync of the same palette must try the switch again: %+v", resync)
	}
}

//go:build jail

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

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// fakePfm is the pfm the Choose Theme command finds at ~/.local/bin/pfm in a
// test: a script that logs its arguments and answers `theme list --json` and
// `theme use` with the given output and exit codes.
type fakePfm struct {
	list       string
	listStderr string
	listExit   int
	useStderr  string
	useExit    int
	// absent leaves ~/.local/bin/pfm out: a host pfm is not installed on.
	absent bool
}

// chooseWindow is the window the command runs in: remoteName "" a local one,
// kind where the extension host runs (1 UI, 2 Workspace), 0 left unset.
type chooseWindow struct {
	remoteName string
	kind       int
}

type chooseRun struct {
	Registered bool `json:"registered"`
	QuickPicks []struct {
		Items []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
			Detail      string `json:"detail"`
		} `json:"items"`
	} `json:"quickPicks"`
	Messages []struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	} `json:"messages"`
	Contexts [][]any `json:"contexts"`
}

// runVSCodeChooseTheme runs the professor.chooseTheme handler in a local
// window (pick the quick-pick label chosen, "" a cancel) over a fake pfm, and
// returns what the extension showed and the argument line of every pfm run.
func runVSCodeChooseTheme(t *testing.T, pick string, pfm fakePfm) (chooseRun, []string) {
	t.Helper()
	return runVSCodeChooseThemeIn(t, chooseWindow{}, pick, pfm)
}

// runVSCodeChooseThemeIn is runVSCodeChooseTheme in the given window.
func runVSCodeChooseThemeIn(t *testing.T, window chooseWindow, pick string, pfm fakePfm) (chooseRun, []string) {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	data := filepath.Join(home, "fake-pfm")
	for _, dir := range []string{bin, data} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{"list.out": pfm.list, "list.err": pfm.listStderr, "use.err": pfm.useStderr}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(data, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" >> "$HOME/fake-pfm/calls.log"
case "$1 $2" in
  "theme list") cat "$HOME/fake-pfm/list.out"; cat "$HOME/fake-pfm/list.err" >&2; exit %d ;;
  "theme use") cat "$HOME/fake-pfm/use.err" >&2; exit %d ;;
esac
echo "fake pfm: unexpected arguments: $*" >&2
exit 64
`, pfm.listExit, pfm.useExit)
	if !pfm.absent {
		if err := testjail.WriteExecutable(filepath.Join(bin, "pfm"), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	scenario, err := json.Marshal(map[string]any{
		"remoteName": window.remoteName, "extensionKind": window.kind, "pick": pick,
	})
	if err != nil {
		t.Fatal(err)
	}
	output := runVSCodeExtensionDriverAt(t, "choose", home, []string{"PROFESSOR_CHOOSE_SCENARIO=" + string(scenario)})
	var run chooseRun
	if err := json.Unmarshal(output, &run); err != nil {
		t.Fatalf("decode choose driver output: %v: %s", err, output)
	}
	var calls []string
	logged, err := os.ReadFile(filepath.Join(data, "calls.log"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(logged) > 0 {
		calls = strings.Split(strings.TrimSuffix(string(logged), "\n"), "\n")
	}
	return run, calls
}

const chooseThemeList = `{"current":"dark","themes":[
 {"name":"dusk","value":"custom:dusk","title":"Dusk","kind":"custom","current":false},
 {"name":"ember","value":"custom:ember","title":"Ember Glow","kind":"custom","current":false,
  "missing":["~/.claude-b","~/.claude-c"]},
 {"name":"dark","value":"dark","title":"dark","kind":"builtin","current":true},
 {"name":"light","value":"light","title":"light","kind":"builtin","current":false}],"errors":[]}`

// chooseThemeListColliding has a custom theme whose stem is a built-in id:
// only its value names it, the bare name being the built-in.
const chooseThemeListColliding = `{"current":"dark","themes":[
 {"name":"dark","value":"custom:dark","title":"Dark Custom","kind":"custom","current":false},
 {"name":"dark","value":"dark","title":"dark","kind":"builtin","current":true}],"errors":[]}`

const chooseThemeListWithErrors = `{"current":"dark","themes":[
 {"name":"dark","value":"dark","title":"dark","kind":"builtin","current":true}],
 "errors":[{"path":"~/.claude-b/themes","error":"permission denied"},
  {"path":"~/.claude-c/settings.json","error":"invalid JSON"}]}`

// TestVSCodeChooseThemeRunsPfmListThenUseInALocalWindow: the command lists the
// themes through `pfm theme list --json`, sets the picked one through
// `pfm theme use {value}` (the one token naming exactly the picked theme)
// and says so; a cancel runs nothing more; every
// failure reaches the user in pfm's own words, never silently.
func TestVSCodeChooseThemeRunsPfmListThenUseInALocalWindow(t *testing.T) {
	t.Parallel()
	const listCall, useCall = "theme list --json", "theme use custom:dusk"
	for _, tc := range []struct {
		name      string
		pick      string
		pfm       fakePfm
		calls     []string
		quickPick bool
		kind      string   // the one message shown, "" for none
		fragments []string // each must appear in that message
	}{
		{
			name: "picking a theme sets it", pick: "Dusk", pfm: fakePfm{list: chooseThemeList},
			calls: []string{listCall, useCall}, quickPick: true, kind: "info",
			fragments: []string{"Theme Dusk set: new chats follow; run /theme in open chats"},
		},
		{
			name: "a custom theme sharing a built-in's name is set by its value", pick: "Dark Custom",
			pfm:   fakePfm{list: chooseThemeListColliding},
			calls: []string{listCall, "theme use custom:dark"}, quickPick: true, kind: "info",
			fragments: []string{"Theme Dark Custom set"},
		},
		{name: "a cancel runs nothing more", pfm: fakePfm{list: chooseThemeList}, calls: []string{listCall}, quickPick: true},
		{
			name: "a use failure shows pfm's stderr", pick: "Dusk",
			pfm:   fakePfm{list: chooseThemeList, useExit: 1, useStderr: "pfm: theme use: refused in ~/.claude-b\n"},
			calls: []string{listCall, useCall}, quickPick: true, kind: "error",
			fragments: []string{"refused in ~/.claude-b"},
		},
		{
			name: "a use failure with no stderr shows the exit code", pick: "Dusk",
			pfm:   fakePfm{list: chooseThemeList, useExit: 3},
			calls: []string{listCall, useCall}, quickPick: true, kind: "error",
			fragments: []string{"exit code 3"},
		},
		{
			name: "a list failure shows pfm's stderr", pick: "Dusk",
			pfm:   fakePfm{listExit: 2, listStderr: "pfm: theme list: cannot read state\n"},
			calls: []string{listCall}, kind: "error", fragments: []string{"cannot read state"},
		},
		{
			name: "list output that is not JSON is an error", pick: "Dusk", pfm: fakePfm{list: "this is not json"},
			calls: []string{listCall}, kind: "error", fragments: []string{"JSON"},
		},
		{
			name: "unreadable theme sources are one warning naming each path",
			pfm: fakePfm{
				list: chooseThemeListWithErrors, listExit: 1, listStderr: "pfm: 2 unreadable\n",
			},
			calls: []string{listCall}, quickPick: true, kind: "warning",
			fragments: []string{"~/.claude-b/themes", "~/.claude-c/settings.json"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run, calls := runVSCodeChooseTheme(t, tc.pick, tc.pfm)
			if !run.Registered {
				t.Fatal("extension.js registers no professor.chooseTheme handler")
			}
			if !slices.Equal(calls, tc.calls) {
				t.Errorf("pfm runs = %q, want %q", calls, tc.calls)
			}
			if got := len(run.QuickPicks) == 1; got != tc.quickPick {
				t.Errorf("quick picks shown = %d, want one = %v", len(run.QuickPicks), tc.quickPick)
			}
			if tc.kind == "" {
				if len(run.Messages) != 0 {
					t.Errorf("messages = %+v, want none", run.Messages)
				}
				return
			}
			if len(run.Messages) != 1 || run.Messages[0].Kind != tc.kind {
				t.Fatalf("messages = %+v, want exactly one %s", run.Messages, tc.kind)
			}
			for _, fragment := range tc.fragments {
				if !strings.Contains(run.Messages[0].Message, fragment) {
					t.Errorf("%s message = %q, want it to hold %q", tc.kind, run.Messages[0].Message, fragment)
				}
			}
		})
	}
}

// TestVSCodeChooseThemeOffersTheCurrentThemeFirstAndMarksMissingOnes: the
// quick pick has one item per theme, titled by the theme's title and
// described by its value; the current theme comes first and says so; a theme
// an account lacks says where it is missing.
func TestVSCodeChooseThemeOffersTheCurrentThemeFirstAndMarksMissingOnes(t *testing.T) {
	t.Parallel()
	run, _ := runVSCodeChooseTheme(t, "", fakePfm{list: chooseThemeList})
	if len(run.QuickPicks) != 1 {
		t.Fatalf("quick picks shown = %d, want 1 (registered=%v)", len(run.QuickPicks), run.Registered)
	}
	type item struct{ label, description string }
	var got []item
	details := map[string]string{}
	for _, entry := range run.QuickPicks[0].Items {
		got = append(got, item{entry.Label, entry.Description})
		details[entry.Label] = entry.Detail
	}
	want := []item{{"dark", "dark"}, {"Dusk", "custom:dusk"}, {"Ember Glow", "custom:ember"}, {"light", "light"}}
	if !slices.Equal(got, want) {
		t.Errorf("quick pick items = %v, want %v", got, want)
	}
	if !strings.Contains(details["dark"], "current") {
		t.Errorf("current theme detail = %q, want it marked current", details["dark"])
	}
	if want := "missing in ~/.claude-b, ~/.claude-c"; !strings.Contains(details["Ember Glow"], want) {
		t.Errorf("Ember Glow detail = %q, want it to hold %q", details["Ember Glow"], want)
	}
	if details["Dusk"] != "" || details["light"] != "" {
		t.Errorf("details of plain themes = %q, %q, want none", details["Dusk"], details["light"])
	}
}

// TestVSCodeChooseThemeNeverRunsPfmInARemoteWindowPfmDoesNotRunIn: a remote
// window whose extension host has no pfm, or one the extension serves from
// the UI side, refuses with a message and executes nothing; the manifest
// shows the command in a local window or where pfm runs.
func TestVSCodeChooseThemeNeverRunsPfmInARemoteWindowPfmDoesNotRunIn(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		window chooseWindow
		pfm    fakePfm
	}{
		"remote host without pfm": {chooseWindow{"ssh-remote", 2}, fakePfm{list: chooseThemeList, absent: true}},
		"served from the UI side": {chooseWindow{"ssh-remote", 1}, fakePfm{list: chooseThemeList}},
	} {
		run, calls := runVSCodeChooseThemeIn(t, tc.window, "Dusk", tc.pfm)
		if !run.Registered {
			t.Fatal("extension.js registers no professor.chooseTheme handler")
		}
		if len(calls) != 0 || len(run.QuickPicks) != 0 {
			t.Errorf(
				"%s: remote window ran pfm %q and showed %d quick picks, want neither",
				name,
				calls,
				len(run.QuickPicks),
			)
		}
		// A window served from the client's side also says once, at activation, that it paints
		// nothing; the command's own refusal is the one naming Choose Theme.
		var refusals int
		for _, message := range run.Messages {
			if message.Kind == "warning" && strings.Contains(message.Message, "Choose Theme") {
				refusals++
			}
		}
		if refusals != 1 {
			t.Errorf(
				"%s: remote window messages = %+v, want exactly one Choose Theme warning saying why",
				name,
				run.Messages,
			)
		}
		if want := [][]any{{"setContext", "professor.pfmHere", false}}; !reflect.DeepEqual(run.Contexts, want) {
			t.Errorf("%s: activate set contexts %v, want %v", name, run.Contexts, want)
		}
	}

	raw, err := embeddedAssets.ReadFile("assets/" + vscodeExtensionSource + "/package.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Contributes struct {
			Commands []struct {
				Command string `json:"command"`
				Title   string `json:"title"`
			} `json:"commands"`
			Menus struct {
				CommandPalette []struct {
					Command string `json:"command"`
					When    string `json:"when"`
				} `json:"commandPalette"`
			} `json:"menus"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	titled := false
	for _, command := range manifest.Contributes.Commands {
		titled = titled || command.Command == "professor.chooseTheme" && command.Title == "Professor: Choose Theme"
	}
	if !titled {
		t.Errorf("package.json contributes no professor.chooseTheme titled %q: %+v", "Professor: Choose Theme",
			manifest.Contributes.Commands)
	}
	hidden := false
	for _, entry := range manifest.Contributes.Menus.CommandPalette {
		hidden = hidden || entry.Command == "professor.chooseTheme" && entry.When == "!remoteName || professor.pfmHere"
	}
	if !hidden {
		t.Errorf(
			"package.json commandPalette has no `!remoteName || professor.pfmHere` entry for professor.chooseTheme: %+v",
			manifest.Contributes.Menus.CommandPalette,
		)
	}
}

// TestVSCodeChooseThemeRunsInARemoteWindowWhosePfmIsHere: on a Linux host
// driven through Remote-SSH the extension host runs pfm, so the command lists
// and sets the theme there as in a local window.
func TestVSCodeChooseThemeRunsInARemoteWindowWhosePfmIsHere(t *testing.T) {
	t.Parallel()
	run, calls := runVSCodeChooseThemeIn(t, chooseWindow{"ssh-remote", 2}, "Dusk", fakePfm{list: chooseThemeList})
	if want := []string{"theme list --json", "theme use custom:dusk"}; !slices.Equal(calls, want) {
		t.Fatalf("remote window whose host runs pfm ran %q, want %q", calls, want)
	}
	if want := [][]any{{"setContext", "professor.pfmHere", true}}; !reflect.DeepEqual(run.Contexts, want) {
		t.Errorf("activate set contexts %v, want %v", run.Contexts, want)
	}
}

// vscodeExtensionDriverJS stubs the 'vscode' module (Module._load
// interception, the documented way to inject a require-time fake with no
// package on disk) and drives the real embedded extension.js through
// activate() -> registerTerminalProfileProvider -> provideTerminalProfile(),
// the same call chain VS Code itself makes when a Professor terminal opens.
// Its mode (argv[3]) picks the output: "profile" prints the provided terminal
// profile; "route" runs the handlers activate() registered for the
// professor.newChatTerminal command and VS Code's own
// workbench.action.terminal.new (the + button, Ctrl+Shift+`) and prints every
// command they executed; "choose" runs the professor.chooseTheme handler for
// the window the JSON in PROFESSOR_CHOOSE_SCENARIO describes (its remote name,
// the extension host's kind, the quick-pick label the user picks, if any) and
// prints whether the command was registered, every quick pick shown, every
// message shown and every setContext activate ran. Activate's own commands
// are cleared before a handler runs, so "route" prints the handlers' alone.
// "promptLint" opens the prompt file PROFESSOR_PROMPT_LINT_SCENARIO describes (its path
// under HOME, its text, settings over package.json's defaults) in the active
// editor (lensClick: in no active editor, the command the file's CodeLens
// carries, run with the arguments it carries), runs the professor.promptLint
// handler (runs times; secondClick clicks again while the first runs;
// editDuringRun edits the text then; cancelAfterMs cancels the progress;
// cancelLensAfterMs runs the command the running lens carries then) and prints
// the messages, the professor-prompt-lint diagnostics left, every progress shown,
// the quick fixes offered over the whole file (after editBeforeFix appends to
// that 1-based line), every setContext, the lenses the file had before the
// run (lensIdle), mid-run when cancelLensAfterMs is set (lensRunning) and
// after it (lensAfter), the selector the lens provider registered for and how
// many times it announced a change (lensFires); globsAfter sets
// professor.promptLint.globs before the run and announces the change, printing
// the lens announcements it caused (globsFires) and the lenses after it
// (lensAfterGlobs).
const vscodeExtensionDriverJS = `
const Module = require('module');
const fs = require('fs');
const path = require('path');
const extensionPath = process.argv[2];
const mode = process.argv[3];
const scenarioEnv = { choose: 'PROFESSOR_CHOOSE_SCENARIO', promptLint: 'PROFESSOR_PROMPT_LINT_SCENARIO' }[mode];
const scenario = scenarioEnv ? JSON.parse(process.env[scenarioEnv]) : {};
const messages = [];
const quickPicks = [];
const progresses = [];
const shown = (kind) => (message) => { messages.push({ kind, message }); return Promise.resolve(undefined); };
const manifest = JSON.parse(fs.readFileSync(path.join(path.dirname(extensionPath), 'package.json'), 'utf8'));
const settingDefault = (section, key) => manifest.contributes.configuration.properties[section + '.' + key]?.default;

function Range(startLine, startCharacter, endLine, endCharacter) {
  this.start = { line: startLine, character: startCharacter };
  this.end = { line: endLine, character: endCharacter };
}
const globMatch = (glob, file) => new RegExp('^' + glob.split('**/').map((part) => part.split('*')
  .map((piece) => piece.replace(/[.+?^$()|[\]\\{}]/g, '\\$&')).join('[^/]*')).join('(?:.*/)?') + '$').test(file);

let document;
if (scenario.file) {
  const lines = scenario.text.split('\n');
  const fsPath = path.join(process.env.HOME, scenario.file);
  document = {
    lines,
    uri: { fsPath, scheme: 'file', toString: () => 'file://' + fsPath },
    version: 1,
    get lineCount() { return lines.length; },
    lineAt(index) {
      if (index < 0 || index >= lines.length) throw new Error('lineAt(' + index + ') is out of range');
      return { text: lines[index] };
    },
    getText(range) {
      const span = lines.slice(range.start.line, range.end.line + 1);
      span[span.length - 1] = span[span.length - 1].slice(0, range.end.character);
      span[0] = span[0].slice(range.start.character);
      return span.join('\n');
    },
  };
}
const diagnostics = new Map();
let collectionName;
let codeActionProvider;
let codeLensProvider;
let lensSelector;
let lensFires = 0;
const configListeners = [];

const defaults = {
  shellPath: '/bin/zsh',
  shellArgs: ['-l'],
  env: { PFM_AUTO_OPEN: 'pfm' },
  icons: ['rocket'],
  colors: ['terminal.ansiRed'],
};

let provider;
const registered = {};
const executed = [];
const vscodeStub = {
  env: { remoteName: scenario.remoteName || undefined },
  ExtensionKind: { UI: 1, Workspace: 2 },
  ProgressLocation: { Notification: 15 },
  workspace: {
    getConfiguration(section) {
      const settings = scenario.settings || {};
      return {
        get: (key) => (key in settings ? settings[key] : key in defaults ? defaults[key] : settingDefault(section, key)),
        inspect: () => undefined,
      };
    },
    onDidChangeConfiguration: (listener) => {
      configListeners.push(listener);
      return { dispose() {} };
    },
    textDocuments: document ? [document] : [],
    asRelativePath: (uri) => path.relative(process.env.HOME, uri.fsPath),
  },
  languages: {
    createDiagnosticCollection(name) {
      collectionName = name;
      return {
        get: (uri) => diagnostics.get(uri.toString()),
        set: (uri, list) => { diagnostics.set(uri.toString(), list); },
        delete: (uri) => { diagnostics.delete(uri.toString()); },
        dispose() {},
      };
    },
    match: (selector, doc) => (globMatch(selector.pattern, doc.uri.fsPath) ? 10 : 0),
    registerCodeActionsProvider: (selector, provider) => {
      codeActionProvider = provider;
      return { dispose() {} };
    },
    registerCodeLensProvider: (selector, lensProvider) => {
      codeLensProvider = lensProvider;
      lensSelector = selector;
      lensProvider.onDidChangeCodeLenses(() => { lensFires++; });
      return { dispose() {} };
    },
  },
  Disposable: { from: (...items) => ({ dispose() { items.forEach((item) => item.dispose()); } }) },
  Range,
  CodeLens: function (range, command) { Object.assign(this, { range, command }); },
  EventEmitter: function () {
    const listeners = [];
    this.event = (listener) => { listeners.push(listener); return { dispose() {} }; };
    this.fire = () => listeners.slice().forEach((listener) => listener());
    this.dispose = () => { listeners.length = 0; };
  },
  Diagnostic: function (range, message, severity) { Object.assign(this, { range, message, severity }); },
  DiagnosticSeverity: { Error: 0, Warning: 1, Information: 2, Hint: 3 },
  CodeAction: function (title, kind) { Object.assign(this, { title, kind }); },
  CodeActionKind: { QuickFix: 'quickfix' },
  WorkspaceEdit: function () {
    this.replacements = [];
    this.replace = (uri, range, text) => { this.replacements.push({ uri: uri.toString(), range, text }); };
  },
  window: {
    activeTextEditor: document && !scenario.lensClick ? { document } : undefined,
    onDidOpenTerminal: () => ({ dispose() {} }),
    registerTerminalProfileProvider: (id, terminalProvider) => {
      provider = terminalProvider;
      return { dispose() {} };
    },
    createTerminal: () => ({ show() {} }),
    showQuickPick: (items, options) => {
      quickPicks.push({ items: JSON.parse(JSON.stringify(items)), options });
      return Promise.resolve(items.find((item) => item.label === scenario.pick));
    },
    showInformationMessage: shown('info'),
    showWarningMessage: shown('warning'),
    showErrorMessage: shown('error'),
    withProgress: (options, task) => {
      progresses.push(options);
      const listeners = [];
      const token = {
        isCancellationRequested: false,
        onCancellationRequested: (listener) => { listeners.push(listener); return { dispose() {} }; },
      };
      if (scenario.cancelAfterMs) {
        setTimeout(() => { token.isCancellationRequested = true; listeners.forEach((listener) => listener()); }, scenario.cancelAfterMs);
      }
      return task({ report() {} }, token);
    },
  },
  commands: {
    registerCommand: (id, handler) => {
      registered[id] = handler;
      return { dispose() {} };
    },
    executeCommand: (...call) => {
      executed.push(call);
      return Promise.resolve();
    },
  },
  ThemeIcon: function (id) { this.id = id; },
  ThemeColor: function (id) { this.id = id; },
  TerminalProfile: function (options) { return options; },
};

const originalLoad = Module._load;
Module._load = function (request, parent, isMain) {
  if (request === 'vscode') return vscodeStub;
  return originalLoad.apply(this, arguments);
};

const extension = require(extensionPath);
const store = {};
const context = {
  extension: { id: 'professor.professor', extensionKind: scenario.extensionKind },
  subscriptions: [],
  globalState: {
    get: (key, def) => (key in store ? store[key] : def),
    update: (key, value) => { store[key] = value; },
  },
};
extension.activate(context);
const contexts = executed.filter((call) => call[0] === 'setContext');
executed.length = 0;
if (mode === 'route') {
  for (const id of ['professor.newChatTerminal', 'workbench.action.terminal.new']) {
    if (!registered[id]) {
      process.stderr.write('extension.js registers no handler for ' + id + '; registered: ' + Object.keys(registered).join(', '));
      process.exit(3);
    }
  }
  registered['professor.newChatTerminal']();
  registered['workbench.action.terminal.new']();
  registered['workbench.action.terminal.new']({ config: { profileName: 'bash' }, location: 2 });
  process.stdout.write(JSON.stringify(executed));
} else if (mode === 'choose') {
  const handler = registered['professor.chooseTheme'];
  (async () => {
    if (handler) await handler();
    process.stdout.write(JSON.stringify({ registered: Boolean(handler), quickPicks, messages, contexts }));
  })().catch((err) => { process.stderr.write(String(err && err.stack || err)); process.exit(1); });
} else if (mode === 'promptLint') {
  const handler = registered['professor.promptLint'];
  (async () => {
    if (!handler) throw new Error('extension.js registers no professor.promptLint handler');
    const lensView = () => (codeLensProvider.provideCodeLenses(document) || []).map((lens) => ({
      line: lens.range.start.line, title: lens.command.title, command: lens.command.command,
      uris: lens.command.arguments.map((uri) => uri.toString()),
    }));
    const lensIdle = lensView();
    let lensRunning;
    let globsFires;
    let lensAfterGlobs;
    if (scenario.globsAfter) {
      const before = lensFires;
      scenario.settings = { ...(scenario.settings || {}), globs: scenario.globsAfter };
      configListeners.forEach((listener) => listener({ affectsConfiguration: (key) => key === 'professor.promptLint.globs' }));
      globsFires = lensFires - before;
      lensAfterGlobs = lensView();
    }
    for (let run = 0; run < (scenario.runs || 1); run++) {
      const lens = codeLensProvider.provideCodeLenses(document)[0];
      const runs = [scenario.lensClick ? registered[lens.command.command](...lens.command.arguments) : handler()];
      if (scenario.editDuringRun) document.version++;
      if (scenario.secondClick) runs.push(handler());
      if (scenario.cancelLensAfterMs) {
        runs.push(new Promise((resolve) => setTimeout(resolve, scenario.cancelLensAfterMs)).then(() => {
          lensRunning = lensView();
          const running = codeLensProvider.provideCodeLenses(document)[0];
          return registered[running.command.command](...running.command.arguments);
        }));
      }
      await Promise.all(runs);
    }
    if (scenario.editBeforeFix) document.lines[scenario.editBeforeFix - 1] += ' edited';
    const whole = new Range(0, 0, document.lineCount - 1, 0);
    const actions = codeActionProvider.provideCodeActions(document, whole) || [];
    process.stdout.write(JSON.stringify({
      collectionName,
      messages,
      progresses,
      contexts,
      lensIdle,
      lensRunning,
      lensAfter: lensView(),
      lensFires,
      lensSelector,
      globsFires,
      lensAfterGlobs,
      diagnostics: (diagnostics.get(document.uri.toString()) || []).map((d) => ({
        startLine: d.range.start.line, endLine: d.range.end.line, endCharacter: d.range.end.character,
        severity: d.severity, message: d.message, source: d.source, code: d.code,
      })),
      actions: actions.map((action) => ({
        title: action.title, kind: action.kind, codes: action.diagnostics.map((d) => d.code),
        replacements: action.edit.replacements.map((r) => ({
          startLine: r.range.start.line, endLine: r.range.end.line, endCharacter: r.range.end.character, text: r.text,
        })),
      })),
    }));
  })().catch((err) => { process.stderr.write(String(err && err.stack || err)); process.exit(1); });
} else {
  process.stdout.write(JSON.stringify(provider.provideTerminalProfile()));
}
`

// runVSCodeExtensionDriverAt is runVSCodeExtensionDriver with the HOME the
// extension sees (theme.js reads ~/.local/state/pfm/theme, choose.js runs
// ~/.local/bin/pfm) and extra environment.
func runVSCodeExtensionDriverAt(t *testing.T, mode, home string, env []string) []byte {
	t.Helper()
	extensionPath, err := filepath.Abs(filepath.Join("assets", "vscode", "professor", "extension.js"))
	if err != nil {
		t.Fatal(err)
	}
	return runVSCodeExtensionDriverFrom(t, extensionPath, mode, home, env)
}

// runVSCodeExtensionDriverFrom is runVSCodeExtensionDriverAt over the
// extension.js at extensionPath, a staged copy beside files the embed lacks.
func runVSCodeExtensionDriverFrom(t *testing.T, extensionPath, mode, home string, env []string) []byte {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("named gap: node unavailable; extension.js behaviour not exercised")
	}
	if _, statErr := os.Stat(extensionPath); statErr != nil {
		t.Fatalf("embedded extension asset %s: %v", extensionPath, statErr)
	}

	driverPath := filepath.Join(t.TempDir(), "driver.js")
	if err := os.WriteFile(driverPath, []byte(vscodeExtensionDriverJS), 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(node, driverPath, extensionPath, mode)
	command.Env = append(append(os.Environ(), "HOME="+home), env...)
	output, err := command.Output()
	if err != nil {
		var stderr []byte
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stderr = exitErr.Stderr
		}
		t.Fatalf("run extension.js under node (mode %s): %v: %s%s", mode, err, output, stderr)
	}
	return output
}

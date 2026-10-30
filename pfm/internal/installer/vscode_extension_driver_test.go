package installer

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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
// "lint" opens the prompt file PROFESSOR_LINT_SCENARIO describes (its path
// under HOME, its text, settings over package.json's defaults) in the active
// editor (titleClick: in no active editor, the handler given its uri as the
// title-bar button gives it), runs the professor.analyzePrompt handler (runs times; secondClick
// clicks again while the first runs; editDuringRun edits the text then;
// cancelAfterMs cancels the progress) and prints the messages, the
// professor-prompt diagnostics left, every progress shown, the quick fixes
// offered over the whole file (after editBeforeFix appends to that 1-based
// line) and every setContext.
const vscodeExtensionDriverJS = `
const Module = require('module');
const fs = require('fs');
const path = require('path');
const extensionPath = process.argv[2];
const mode = process.argv[3];
const scenarioEnv = { choose: 'PROFESSOR_CHOOSE_SCENARIO', lint: 'PROFESSOR_LINT_SCENARIO' }[mode];
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
    onDidChangeConfiguration: () => ({ dispose() {} }),
    textDocuments: document ? [document] : [],
    onDidOpenTextDocument: () => ({ dispose() {} }),
    onDidCloseTextDocument: () => ({ dispose() {} }),
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
  },
  Disposable: { from: (...items) => ({ dispose() { items.forEach((item) => item.dispose()); } }) },
  Range,
  Diagnostic: function (range, message, severity) { Object.assign(this, { range, message, severity }); },
  DiagnosticSeverity: { Error: 0, Warning: 1, Information: 2, Hint: 3 },
  CodeAction: function (title, kind) { Object.assign(this, { title, kind }); },
  CodeActionKind: { QuickFix: 'quickfix' },
  WorkspaceEdit: function () {
    this.replacements = [];
    this.replace = (uri, range, text) => { this.replacements.push({ uri: uri.toString(), range, text }); };
  },
  window: {
    activeTextEditor: document && !scenario.titleClick ? { document } : undefined,
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
} else if (mode === 'lint') {
  const handler = registered['professor.analyzePrompt'];
  (async () => {
    if (!handler) throw new Error('extension.js registers no professor.analyzePrompt handler');
    for (let run = 0; run < (scenario.runs || 1); run++) {
      const runs = [scenario.titleClick ? handler(document.uri) : handler()];
      if (scenario.editDuringRun) document.version++;
      if (scenario.secondClick) runs.push(handler());
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

// runVSCodeExtensionDriver runs the embedded extension.js under Node through
// vscodeExtensionDriverJS in the given mode and returns its stdout.
func runVSCodeExtensionDriver(t *testing.T, mode string) []byte {
	t.Helper()
	return runVSCodeExtensionDriverAt(t, mode, t.TempDir(), nil)
}

// runVSCodeExtensionDriverAt is runVSCodeExtensionDriver with the HOME the
// extension sees (theme.js reads ~/.local/state/pfm/theme, choose.js runs
// ~/.local/bin/pfm) and extra environment.
func runVSCodeExtensionDriverAt(t *testing.T, mode, home string, env []string) []byte {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("named gap: node unavailable; extension.js behaviour not exercised")
	}

	extensionPath, err := filepath.Abs(filepath.Join("assets", "vscode", "professor", "extension.js"))
	if err != nil {
		t.Fatal(err)
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

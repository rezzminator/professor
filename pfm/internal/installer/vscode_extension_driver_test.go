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
// the quick-pick label the user picks, if any) and prints whether the command
// was registered, every quick pick shown and every message shown.
const vscodeExtensionDriverJS = `
const Module = require('module');
const extensionPath = process.argv[2];
const mode = process.argv[3];
const scenario = mode === 'choose' ? JSON.parse(process.env.PROFESSOR_CHOOSE_SCENARIO) : {};
const messages = [];
const quickPicks = [];
const shown = (kind) => (message) => { messages.push({ kind, message }); return Promise.resolve(undefined); };

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
  ProgressLocation: { Notification: 15 },
  workspace: {
    getConfiguration() {
      return { get: (key) => defaults[key], inspect: () => undefined };
    },
    onDidChangeConfiguration: () => ({ dispose() {} }),
  },
  window: {
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
    withProgress: (options, task) => task({ report() {} }),
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
  extension: { id: 'professor.professor' },
  subscriptions: [],
  globalState: {
    get: (key, def) => (key in store ? store[key] : def),
    update: (key, value) => { store[key] = value; },
  },
};
extension.activate(context);
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
    process.stdout.write(JSON.stringify({ registered: Boolean(handler), quickPicks, messages }));
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

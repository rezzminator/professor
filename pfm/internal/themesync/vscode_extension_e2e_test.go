package themesync

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// vscodeRemoteWindowJS runs the shipped extension once, as VS Code runs it in
// a Remote-SSH window whose extension host is this machine (extensionKind
// Workspace) on a saved workspace file of the client's (scheme vscode-local):
// it loads both settings layers from the JSON file it is given, applies the
// contract under HOME, writes the layers back and exits.
const vscodeRemoteWindowJS = `
const Module = require('module');
const fs = require('fs');
const [extensionPath, layersPath] = process.argv.slice(2);
const layers = JSON.parse(fs.readFileSync(layersPath, 'utf8'));
const file = '/w/mac/project.code-workspace';
fs.watchFile = () => {};
fs.unwatchFile = () => {};
const vscodeStub = {
  ConfigurationTarget: { Global: 1, Workspace: 2, WorkspaceFolder: 3 },
  ExtensionKind: { UI: 1, Workspace: 2 },
  env: { remoteName: 'ssh-remote' },
  workspace: {
    workspaceFile: { scheme: 'vscode-local', fsPath: file, toString: () => 'vscode-local:' + file },
    workspaceFolders: undefined,
    onDidChangeConfiguration: () => ({ dispose() {} }),
    // activate also starts prompt lint (lint.js); this run exercises none of it.
    textDocuments: [],
    onDidOpenTextDocument: () => ({ dispose() {} }),
    onDidCloseTextDocument: () => ({ dispose() {} }),
    getConfiguration(section) {
      if (section !== 'workbench') return { get: () => undefined, inspect: () => undefined };
      return {
        get: () => undefined,
        inspect: (key) => (key === 'colorCustomizations' ? { globalValue: layers.global, workspaceValue: layers.workspace } : undefined),
        update: (key, value, target) => {
          if (key !== 'colorCustomizations') return Promise.reject(new Error('unexpected update ' + key));
          if (target === 2) layers.workspace = value; else layers.global = value;
          return Promise.resolve();
        },
      };
    },
  },
  window: {
    state: { focused: false },
    onDidOpenTerminal: () => ({ dispose() {} }),
    registerTerminalProfileProvider: () => ({ dispose() {} }),
    showErrorMessage: (message) => { process.stderr.write('error: ' + message + '\n'); return Promise.resolve(); },
    showWarningMessage: (message) => { process.stderr.write('warning: ' + message + '\n'); return Promise.resolve(); },
  },
  languages: {
    createDiagnosticCollection: () => ({ dispose() {} }),
    registerCodeActionsProvider: () => ({ dispose() {} }),
    match: () => 0,
  },
  Disposable: { from: () => ({ dispose() {} }) },
  CodeActionKind: { QuickFix: 'quickfix' },
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
const context = {
  extension: { id: 'professor.professor', extensionKind: 2 },
  subscriptions: [],
  globalState: { get: (key, def) => def, update() {} },
};
(async () => {
  await require(extensionPath).activate(context).syncTheme();
  fs.writeFileSync(layersPath, JSON.stringify(layers));
  process.exit(0);
})().catch((err) => { process.stderr.write(String(err && err.stack || err)); process.exit(1); });
`

type vscodeWindowLayers struct {
	Global    map[string]string `json:"global"`
	Workspace map[string]string `json:"workspace"`
}

// runRemoteWindow applies the contract under h.home through the shipped
// extension, carrying the window's settings layers in layersPath.
func runRemoteWindow(t *testing.T, h *harness, node, layersPath string) vscodeWindowLayers {
	t.Helper()
	extension, err := filepath.Abs(filepath.Join("..", "installer", "assets", "vscode", "professor", "extension.js"))
	if err != nil {
		t.Fatal(err)
	}
	driver := filepath.Join(t.TempDir(), "remote-window.js")
	if err := os.WriteFile(driver, []byte(vscodeRemoteWindowJS), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(node, driver, extension, layersPath)
	command.Env = append(os.Environ(), "HOME="+h.home)
	if output, err := command.CombinedOutput(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run %s: %v", driver, err)
		}
		t.Fatalf("extension run failed: %v: %s", err, output)
	}
	raw, err := os.ReadFile(layersPath)
	if err != nil {
		t.Fatal(err)
	}
	var layers vscodeWindowLayers
	if err := json.Unmarshal(raw, &layers); err != nil {
		t.Fatalf("decode window layers %s: %v", raw, err)
	}
	return layers
}

// TestVSCodeExtensionAcksEveryFlipAndPfmThemeReadsInSync walks the whole
// devbox loop: pfm writes a generation, the extension, running on this host
// for a Remote-SSH window, paints the window's Workspace frame with the
// contract's chrome and acks it, and pfm's read-back judges vscode IN-SYNC;
// a theme flip writes the next generation and the same window follows it,
// frame and ack, to IN-SYNC again.
func TestVSCodeExtensionAcksEveryFlipAndPfmThemeReadsInSync(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("named gap: node unavailable; the extension's ack of a pfm generation not exercised")
	}
	h := newHarness(t)
	layersPath := filepath.Join(t.TempDir(), "layers.json")
	if err := os.WriteFile(layersPath, []byte(`{"global":{"editor.background":"#000000"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	chromeOf := func() map[string]string {
		var contract vscodeContract
		if err := json.Unmarshal([]byte(h.read(".local/state/pfm/theme/current.json")), &contract); err != nil {
			t.Fatal(err)
		}
		for key, value := range contract.Colors {
			contract.Chrome[key] = value
		}
		return contract.Chrome
	}

	if report := h.on(); report.Generation != 1 {
		t.Fatalf("On wrote generation %d, want 1", report.Generation)
	}
	first := chromeOf()
	if layers := runRemoteWindow(t, h, node, layersPath); !reflect.DeepEqual(layers.Workspace, first) {
		t.Fatalf("generation 1: Workspace frame = %v, want the contract's chrome %v", layers.Workspace, first)
	}
	wantVerdict(t, h.status(), surfaceVscode, VerdictInSync, "window chrome in 1 workspace")

	h.write(".claude/themes/ultrafunk.json", shippedClaudeTheme(t, "ultrafunk"))
	h.write(settingsRel, `{"theme":"custom:ultrafunk","model":"opus"}`)
	if report := h.sync(false); report.Generation != 2 {
		t.Fatalf("the flip wrote generation %d, want 2", report.Generation)
	}
	second := chromeOf()
	if reflect.DeepEqual(first, second) {
		t.Fatalf("the flip left the chrome as it was: %v", second)
	}
	if layers := runRemoteWindow(t, h, node, layersPath); !reflect.DeepEqual(layers.Workspace, second) {
		t.Fatalf("generation 2: Workspace frame = %v, want the flipped contract's chrome %v", layers.Workspace, second)
	}
	report := h.status()
	wantVerdict(t, report, surfaceVscode, VerdictInSync, "window chrome in 1 workspace")
	if got := row(t, report, surfaceVscode); got.Generation != 2 {
		t.Errorf("vscode row generation = %d, want 2", got.Generation)
	}
}

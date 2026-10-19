// pfm's themes in VS Code's own Color Theme picker (Preferences: Color Theme). `pfm install` renders
// one complete VS Code color theme per installed pfm theme (themesync.RenderVSCodeTheme) into this
// extension's themes/ directory, lists each in package.json's contributes.themes as
// "Professor · {name}", and writes themes/index.json — {themes: [{id, label, uiTheme, path, value}]},
// `value` the Claude Code theme value the contract names as its `palette` — the one map from a VS
// Code theme id to a pfm theme. Nothing here derives a colour.
//
// Both directions run in a local window only: a remote window's settings are another machine's
// story, and its colorTheme is left to that window's workspace.
//
// - Native → pfm: the user accepts a Professor theme in the picker (workbench.colorTheme changes to
//   one of the index's ids); the focused window runs `pfm theme use {value}`, which sets Claude
//   Code's theme in every account and syncs every surface — tmux, Herdr, this extension's terminal
//   and chrome — exactly as the command line does. A theme outside the index is the user's own and
//   is left alone; pfm still paints the terminal and chrome over it.
// - pfm → native: the contract's palette changes from anywhere else (`pfm theme use`, Claude
//   Code's /theme followed by pfm's follower); the window that first sees the new palette sets the
//   user's Global workbench.colorTheme to the matching Professor theme. A palette with no Professor
//   theme (a built-in Claude Code theme) leaves colorTheme as it is.
//
// The loop guard is the palette: {stateDir}/vscode-native.json — {palette, id} — records the last
// palette followed, so a resync of the same palette (a new generation) never takes the user's own
// pick away, and a pick whose value is already the contract's palette runs nothing; the
// configuration event a follow's own update fires is passed over once, so a contract that moved
// meanwhile is never undone. The first contract this extension ever sees is recorded, not
// followed: installing it never switches a theme the user chose. A switch VS Code refuses puts
// the prior record back, so the next sync tries again.
const vscode = require('vscode');
const fs = require('fs');
const path = require('path');

const INDEX = path.join(__dirname, 'themes', 'index.json');
const RECORD = 'vscode-native.json';
const CONTRACT = 'current.json';
const USE_TIMEOUT_MS = 30000;
const STATUS_MS = 5000;

function readJSON(file) {
  return JSON.parse(fs.readFileSync(file, 'utf8'));
}

// loadIndex returns the themes pfm rendered, [] when it rendered none; an unreadable index is
// reported and treated as none, so the rest of the extension still runs.
function loadIndex(file = INDEX) {
  try {
    const themes = readJSON(file)?.themes;
    if (!Array.isArray(themes)) throw new Error('holds no themes list');
    return themes.filter((theme) => theme && typeof theme.id === 'string' && typeof theme.value === 'string');
  } catch (err) {
    if (err.code !== 'ENOENT') console.error(`Professor: VS Code theme index ${file} unreadable, no Professor theme follows pfm: ${err?.message ?? err}`);
    return [];
  }
}

function local() {
  return vscode.env.remoteName === undefined;
}

function readRecord(dir) {
  const file = path.join(dir, RECORD);
  try {
    const record = readJSON(file);
    return record && typeof record === 'object' && typeof record.palette === 'string' ? record : null;
  } catch (err) {
    if (err.code !== 'ENOENT') console.error(`Professor: ${file} unreadable, the next contract is recorded without a switch: ${err?.message ?? err}`);
    return null;
  }
}

// writeRecord creates the state dir when pfm has not yet: a pick made before the first sync still
// records, then runs pfm, which writes the contract there.
function writeRecord(dir, palette, id) {
  const file = path.join(dir, RECORD);
  const temp = path.join(dir, `.${RECORD}.${process.pid}.tmp`);
  fs.mkdirSync(dir, { recursive: true });
  fs.writeFileSync(temp, JSON.stringify({ palette, id }, null, 2) + '\n');
  fs.renameSync(temp, file);
}

function currentContract(dir) {
  try {
    const contract = readJSON(path.join(dir, CONTRACT));
    return contract && typeof contract === 'object' && !Array.isArray(contract) ? contract : null;
  } catch (err) {
    if (err.code !== 'ENOENT') console.error(`Professor: pfm theme contract in ${dir} unreadable: ${err?.message ?? err}`);
    return null;
  }
}

// startNativeThemes wires both directions for the state dir and the pfm runner choose.js uses.
// follow(contract) is pfm → native, called by theme.js after every applied contract; the returned
// disposable ends the picker listener.
function startNativeThemes({ dir, runPfm, why, index = loadIndex() }) {
  const byID = new Map(index.map((theme) => [theme.id, theme]));
  const byValue = new Map(index.map((theme) => [theme.value, theme]));
  let picking = Promise.resolve();
  // The id follow last set: the configuration event its own update fires is passed over once,
  // whatever the contract names by then, so a contract moved meanwhile is never undone.
  let echo = null;

  async function follow(contract) {
    if (!local() || !byID.size || !contract || !contract.enabled || typeof contract.palette !== 'string') return;
    const record = readRecord(dir);
    if (record && record.palette === contract.palette) return;
    const theme = byValue.get(contract.palette);
    writeRecord(dir, contract.palette, theme ? theme.id : null);
    if (!record || !theme) return;
    const workbench = vscode.workspace.getConfiguration('workbench');
    if (workbench.get('colorTheme') === theme.id) return;
    echo = theme.id;
    try {
      await workbench.update('colorTheme', theme.id, vscode.ConfigurationTarget.Global);
    } catch (err) {
      echo = null;
      // The palette was not followed: the prior record stands, so the next sync tries again.
      writeRecord(dir, record.palette, record.id ?? null);
      vscode.window.showErrorMessage(`Professor: could not switch VS Code to ${theme.label} — ${err?.message ?? err}`);
    }
  }

  async function picked() {
    const id = vscode.workspace.getConfiguration('workbench').get('colorTheme');
    const followed = echo;
    echo = null;
    if (id === followed || !local() || !vscode.window.state.focused) return;
    const theme = byID.get(id);
    if (!theme) return;
    const contract = currentContract(dir);
    if (contract && contract.enabled && contract.palette === theme.value) return;
    // Recorded before pfm runs: the contract it writes then names a palette already followed.
    writeRecord(dir, theme.value, theme.id);
    const result = await runPfm(['theme', 'use', theme.value], USE_TIMEOUT_MS);
    if (result.ok) {
      vscode.window.setStatusBarMessage(`Professor: ${theme.label} on every surface`, STATUS_MS);
    } else {
      vscode.window.showErrorMessage(`Professor: could not switch every surface to ${theme.label} — ${why(result, USE_TIMEOUT_MS)}`);
    }
  }

  const listener = vscode.workspace.onDidChangeConfiguration((event) => {
    if (!byID.size || !event.affectsConfiguration('workbench.colorTheme')) return;
    // One pick at a time; each re-reads the setting, so the last pick wins.
    picking = picking.then(picked).catch((err) => {
      console.error(`Professor: switching every surface to the picked VS Code theme failed: ${err?.stack ?? err}`);
      vscode.window.showErrorMessage(`Professor: could not switch every surface to the picked theme — ${err?.message ?? err}`);
    });
  });
  return { follow, disposable: listener, settled: () => picking };
}

module.exports = { startNativeThemes, loadIndex };

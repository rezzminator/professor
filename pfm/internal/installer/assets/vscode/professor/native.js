// pfm's themes in VS Code's own Color Theme picker (Preferences: Color Theme). `pfm install` renders
// one complete VS Code color theme per installed pfm theme (themesync.RenderVSCodeTheme) into this
// extension's themes/ directory, lists each in package.json's contributes.themes as
// "Professor · {name}", and writes themes/index.json — {themes: [{id, label, uiTheme, path, value}]},
// `value` the Claude Code theme value the contract names as its `palette` — the one map from a VS
// Code theme id to a pfm theme. Nothing here derives a colour.
//
// Both directions run only where pfm runs (choose.js pfmHere): a local window, or a remote window
// whose extension host is pfm's machine (a Linux host driven through Remote-SSH, where pfm,
// Claude Code, tmux and Herdr all live). Any other remote window's settings are another machine's
// story, and its colorTheme is left to that window's workspace.
//
// - Native → pfm: the user accepts a Professor theme in the picker (workbench.colorTheme changes to
//   one of the index's ids); the focused window runs `pfm theme use {value}`, which sets Claude
//   Code's theme in every account and syncs every surface — tmux, Herdr, this extension's terminal
//   and chrome — exactly as the command line does. A theme outside the index is the user's own and
//   is left alone; pfm still paints the terminal and chrome over it.
// - pfm → native: the contract's palette changes from anywhere else (`pfm theme use`, Claude
//   Code's /theme followed by pfm's follower); local windows set Global workbench.colorTheme;
//   each remote workspace independently sets its Workspace layer. A palette with no Professor
//   theme (a built-in Claude Code theme) leaves colorTheme as it is.
//
// The loop guard records the last palette per setting scope in {stateDir}/vscode-native.json.
// A resync of the same palette never takes the user's own pick away. Each new scope records its
// first contract without switching; legacy {palette, id} history remains the baseline for every
// scope not yet migrated. Unknown history is preserved and visibly refused, never treated as a
// first install. Settings must succeed before their follow guard advances; failed picks restore
// only their own unchanged guard. The shared apply lock serializes record changes across windows.
// The configuration event a follow fires is passed over once, even if the contract moved meanwhile.
const vscode = require('vscode');
const fs = require('fs');
const path = require('path');
const { chromeTarget, withLock } = require('./theme');

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

function readRecord(dir) {
  const file = path.join(dir, RECORD);
  const object = (value) => value && typeof value === 'object' && !Array.isArray(value);
  const entry = (value) => object(value) && typeof value.palette === 'string' &&
    (value.id === undefined || value.id === null || typeof value.id === 'string');
  try {
    const record = readJSON(file);
    if (object(record) && record.version === undefined && entry(record)) {
      return { version: 2, scopes: {}, legacy: record };
    }
    if (!object(record) || record.version !== 2 || !object(record.scopes) ||
        Object.entries(record.scopes).some(([scope, value]) =>
          (scope !== 'global' && !scope.startsWith('workspace:')) || !entry(value)) ||
        (record.legacy !== undefined && !entry(record.legacy))) {
      throw new Error('invalid native theme history');
    }
    return record;
  } catch (err) {
    if (err.code === 'ENOENT') return { version: 2, scopes: {} };
    throw new Error(`read native theme history ${file}: ${err?.message ?? err}`);
  }
}

function writeRecord(dir, record) {
  const file = path.join(dir, RECORD);
  const temp = path.join(dir, `.${RECORD}.${process.pid}.tmp`);
  fs.writeFileSync(temp, JSON.stringify(record, null, 2) + '\n');
  fs.renameSync(temp, file);
}

function nativeScope() {
  if (vscode.env.remoteName === undefined) return 'global';
  const target = chromeTarget();
  if (!target) throw new Error('remote window has no workspace; native theme left unchanged');
  return `workspace:${target}`;
}

function withRecord(dir, work) {
  // A native pick may precede pfm's first contract and state directory.
  fs.mkdirSync(dir, { recursive: true });
  return withLock(dir, () => work(readRecord(dir), nativeScope()));
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

// startNativeThemes wires both directions for the state dir and the pfm runner choose.js uses;
// here() says whether this window is pfm's (choose.js pfmHere), whyNot() why not (choose.js notHere).
// follow(contract) is pfm → native, called by theme.js after every applied contract; the returned
// disposable ends the picker listener.
function startNativeThemes({ dir, runPfm, why, here, whyNot, index = loadIndex() }) {
  const byID = new Map(index.map((theme) => [theme.id, theme]));
  const byValue = new Map(index.map((theme) => [theme.value, theme]));
  let picking = Promise.resolve();
  // The id follow last set: the configuration event its own update fires is passed over once,
  // whatever the contract names by then, so a contract moved meanwhile is never undone.
  let echo = null;

  async function follow(contract) {
    if (!here() || !byID.size || !contract || !contract.enabled || typeof contract.palette !== 'string') return;
    await withRecord(dir, async (record, scope) => {
      const prior = record.scopes[scope] || record.legacy;
      if (prior && prior.palette === contract.palette && record.scopes[scope]) return;
      const theme = byValue.get(contract.palette);
      if (prior && prior.palette !== contract.palette && theme) {
        const workbench = vscode.workspace.getConfiguration('workbench');
        if (workbench.get('colorTheme') !== theme.id) {
          echo = theme.id;
          try {
            const target = vscode.env.remoteName === undefined
              ? vscode.ConfigurationTarget.Global : vscode.ConfigurationTarget.Workspace;
            await workbench.update('colorTheme', theme.id, target);
          } catch (err) {
            echo = null;
            throw new Error(`could not switch VS Code to ${theme.label}: ${err?.message ?? err}`);
          }
        }
      }
      record.scopes[scope] = { palette: contract.palette, id: theme ? theme.id : null };
      writeRecord(dir, record);
    });
  }

  async function picked() {
    const id = vscode.workspace.getConfiguration('workbench').get('colorTheme');
    const followed = echo;
    echo = null;
    if (id === followed || !vscode.window.state.focused) return;
    const theme = byID.get(id);
    if (!theme) return;
    // A Professor theme picked where pfm does not run changes no Claude chat: said, never silent.
    if (!here()) {
      vscode.window.showWarningMessage(`Professor: ${theme.label} changes VS Code alone — ${whyNot()}.`);
      return;
    }
    const next = { palette: theme.value, id: theme.id };
    const saved = await withRecord(dir, (record, scope) => {
      const prior = record.scopes[scope];
      // Recorded before pfm runs, but only for this setting's scope: another remote workspace
      // must still see the new contract as a palette transition.
      record.scopes[scope] = next;
      writeRecord(dir, record);
      return { prior, scope };
    });
    const contract = currentContract(dir);
    if (contract && contract.enabled && contract.palette === theme.value) return;
    const rollback = () => withRecord(dir, (record) => {
      if (JSON.stringify(record.scopes[saved.scope]) !== JSON.stringify(next)) return;
      if (saved.prior) record.scopes[saved.scope] = saved.prior;
      else delete record.scopes[saved.scope];
      writeRecord(dir, record);
    });
    let result;
    try {
      // Do not hold the settings lock while pfm waits for extension acknowledgements.
      result = await runPfm(['theme', 'use', theme.value], USE_TIMEOUT_MS);
    } catch (err) {
      await rollback();
      throw new Error(`run pfm for ${theme.label}: ${err?.message ?? err}`);
    }
    if (result.ok) {
      vscode.window.setStatusBarMessage(`Professor: ${theme.label} on every surface`, STATUS_MS);
    } else {
      await rollback();
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

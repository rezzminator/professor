// `pfm theme` inside VS Code. pfm (pfm/internal/themesync) renders Claude Code's theme into every surface
// the user looks at; VS Code's are the integrated terminal and a local window's title, activity and
// status bars. pfm writes the contract {stateDir}/current.json — {enabled, generation, palette,
// title, mode, appearance, colors, chrome} — and this module merges `colors` (the terminal keys)
// into the user's GLOBAL workbench.colorCustomizations, touching no other key, then acknowledges
// in {stateDir}/vscode-ack.json — {generation, appliedAt, keys, error, chrome} — which `pfm theme
// status` reads back, so a window that did not follow shows as out of sync instead of looking
// themed. With no contract file nothing here writes anything.
//
// Every key it sets is recorded, with the value it replaced and the generation that set it, in
// {stateDir}/vscode-applied.json; a key the next generation drops, or every key on
// `pfm theme off` (enabled:false), goes back to that prior value — unless the user changed it
// since, in which case the user's value stays.
// Each window runs its own copy against one settings file, so the read-merge-write runs under a
// lock directory in the state dir: a second window waits, then finds the work already done.
//
// `chrome` (title, activity and status bar keys) is per window and never Global: a LOCAL window
// on a saved .code-workspace merges it into that workspace's own workbench.colorCustomizations,
// where it beats the user's Global values and Peacock's (the workspace layer is the one Peacock
// writes too). A remote window, a folder-only window (its Workspace layer is a repo's
// .vscode/settings.json) and an untitled workspace get none. Its record is
// {stateDir}/vscode-chrome.json — {workspace file: {generation, keys: {key: {value, prior}}}} —
// kept per workspace, so a window opening a workspace not yet painted at the generation paints
// it even when the Global colours are done; its ack is the chrome map of vscode-ack.json,
// {workspace file: {generation, keys, error}}, each window replacing its own entry and keeping
// the others'.
// Peacock rewrites a workspace's colorCustomizations at every window start except the keys in
// its peacock.excludedSettings, so chrome is written only once that list holds every key to
// paint; until then the workspace's ack entry says which setting to edit, and the edit re-runs
// the sync.
const vscode = require('vscode');
const fs = require('fs');
const os = require('os');
const path = require('path');

const CONTRACT = 'current.json';
const ACK = 'vscode-ack.json';
const RECORD = 'vscode-applied.json';
const CHROME_RECORD = 'vscode-chrome.json';
const PEACOCK = 'johnpapa.vscode-peacock';
const LOCK = 'vscode-apply.lock';
const POLL_MS = 2000;
const LOCK_STALE_MS = 30000;
const LOCK_WAIT_MS = 100;
const LOCK_TRIES = 100;

// themesync.DefaultStateDir(home): the same path pfm writes, unless the user points elsewhere.
// Only the user's own value counts: the setting is machine-scoped, and a workspace value is
// ignored, so no repo's .vscode/settings.json can feed the global settings a contract of its own.
function stateDir() {
  const configured = vscode.workspace.getConfiguration('professor.theme').inspect('stateDir')?.globalValue;
  return configured || path.join(os.homedir(), '.local', 'state', 'pfm', 'theme');
}

// The keys pfm renders (themesync/vscode.go) are the integrated terminal's (`colors`) and the window
// chrome's (`chrome`); a contract key outside its map's keys, or a value that is not a hex colour,
// is never written.
const TERMINAL_KEY = /^terminal(Cursor)?\./;
const CHROME_KEY = /^(titleBar|activityBar|activityBarTop|activityBarBadge|commandCenter|sash|statusBar|statusBarItem)\./;
const COLOR_VALUE = /^#[0-9a-fA-F]{3,8}$/;

function hexColors(colors, keyPattern) {
  const out = {};
  if (!colors || typeof colors !== 'object') return out;
  for (const [key, value] of Object.entries(colors)) {
    if (keyPattern.test(key) && typeof value === 'string' && COLOR_VALUE.test(value)) out[key] = value;
  }
  return out;
}

// chromeTarget is the saved workspace file this window's chrome goes into, or null: a remote window
// has none, a folder-only window's Workspace layer is a repo's own .vscode/settings.json, and an
// untitled workspace has no file to write.
function chromeTarget() {
  if (vscode.env.remoteName !== undefined) return null;
  const file = vscode.workspace.workspaceFile;
  return file && file.scheme === 'file' ? file.fsPath : null;
}

// peacockUnlisted names the wanted chrome keys Peacock would overwrite or delete at the next window
// start: with Peacock installed, every key outside the merged peacock.excludedSettings.
function peacockUnlisted(wantChrome) {
  if (!vscode.extensions.getExtension(PEACOCK)) return [];
  const excluded = vscode.workspace.getConfiguration('peacock').get('excludedSettings', []);
  const listed = new Set(Array.isArray(excluded) ? excluded : []);
  return Object.keys(wantChrome).filter((key) => !listed.has(key));
}

function readJSON(file) {
  return JSON.parse(fs.readFileSync(file, 'utf8'));
}

function writeJSONAtomic(file, value) {
  const temp = path.join(path.dirname(file), `.${path.basename(file)}.${process.pid}.tmp`);
  fs.writeFileSync(temp, JSON.stringify(value, null, 2) + '\n');
  fs.renameSync(temp, file);
}

// planColors is the merge, pure: `global` the colorCustomizations of the layer written (Global, or
// a workspace's), `record` the keys a previous generation set ({key: {value, prior}}, prior null
// = the key was absent), `want` the colours this generation asks for ({} when off). It returns
// the next customizations and the record to keep.
function planColors(global, record, want) {
  const next = { ...global };
  const kept = {};
  for (const [key, entry] of Object.entries(record)) {
    if (key in want || next[key] !== entry.value) continue;
    if (entry.prior === null || entry.prior === undefined) delete next[key];
    else next[key] = entry.prior;
  }
  for (const [key, value] of Object.entries(want)) {
    const entry = record[key];
    const prior = entry && next[key] === entry.value ? entry.prior : next[key] ?? null;
    next[key] = value;
    kept[key] = { value, prior };
  }
  return { next, record: kept };
}

function sameColors(a, b) {
  const keys = Object.keys(a);
  return keys.length === Object.keys(b).length && keys.every((key) => b[key] === a[key]);
}

async function withLock(dir, work) {
  const lock = path.join(dir, LOCK);
  for (let attempt = 0; ; attempt++) {
    try {
      fs.mkdirSync(lock);
      break;
    } catch (err) {
      if (err.code !== 'EEXIST') throw new Error(`lock ${lock}: ${err.message}`);
      const age = Date.now() - fs.statSync(lock, { throwIfNoEntry: false })?.mtimeMs;
      if (age > LOCK_STALE_MS) {
        fs.rmSync(lock, { recursive: true, force: true });
        continue;
      }
      if (attempt >= LOCK_TRIES) throw new Error(`lock ${lock} held by another window for ${Math.round(age)} ms`);
      await new Promise((resolve) => setTimeout(resolve, LOCK_WAIT_MS));
    }
  }
  try {
    return await work();
  } finally {
    fs.rmSync(lock, { recursive: true, force: true });
  }
}

// previousChrome is the chrome map of the ack on disk: the entries other windows wrote. An ack that
// is absent or unreadable starts an empty map.
function previousChrome(dir) {
  const ackPath = path.join(dir, ACK);
  try {
    const chrome = readJSON(ackPath)?.chrome;
    if (!chrome || typeof chrome !== 'object' || Array.isArray(chrome)) return {};
    return Object.fromEntries(Object.entries(chrome).filter(([, entry]) => entry && typeof entry === 'object'));
  } catch (err) {
    if (err.code !== 'ENOENT') console.error(`Professor: pfm theme ack ${ackPath} unreadable, its window entries start over: ${err?.stack ?? err}`);
    return {};
  }
}

// writeAck writes the ack with the chrome map of the ack on disk, this window's entry (when it has
// one) replacing its own.
function writeAck(dir, ack, target, entry) {
  const chrome = previousChrome(dir);
  if (target) chrome[target] = entry;
  writeJSONAtomic(path.join(dir, ACK), { ...ack, appliedAt: new Date().toISOString(), chrome });
}

// applyGlobal brings the user's Global colorCustomizations to the contract's terminal colours, under
// the lock.
async function applyGlobal(dir, contract, generation, want) {
  const recordPath = path.join(dir, RECORD);
  let record = { generation: null, keys: {} };
  try {
    record = { generation: null, keys: {}, ...readJSON(recordPath) };
  } catch (err) {
    if (err.code !== 'ENOENT') throw new Error(`read ${recordPath}: ${err.message}`);
  }
  // A generation applies once: another window, or this one restarting, finds it done, and a
  // colour the user set by hand since stays theirs until the next `pfm theme on` or sync.
  if (contract.enabled && generation !== null && record.generation === generation) return;
  const workbench = vscode.workspace.getConfiguration('workbench');
  const global = workbench.inspect('colorCustomizations')?.globalValue || {};
  const plan = planColors(global, record.keys, want);
  if (!sameColors(plan.next, global)) {
    await workbench.update('colorCustomizations', plan.next, vscode.ConfigurationTarget.Global);
  }
  if (Object.keys(plan.record).length) writeJSONAtomic(recordPath, { generation, keys: plan.record });
  else fs.rmSync(recordPath, { force: true });
}

// applyChrome brings one workspace's colorCustomizations to the contract's chrome colours, under the
// lock, and returns that workspace's ack entry; a failure is the entry's `error`, never thrown, so
// it cannot hide the Global result. Every generation applies once per workspace; a workspace that
// Peacock would repaint is left as it is, its record not advanced, so the next sync retries.
// Restoring (nothing wanted) is never held back.
async function applyChrome(dir, target, contract, generation, wantChrome) {
  const entry = { generation, keys: Object.keys(wantChrome).sort(), error: '' };
  try {
    const unlisted = peacockUnlisted(wantChrome);
    if (unlisted.length) {
      entry.error = `Peacock repaints ${unlisted.length} of ${entry.keys.length} chrome keys at every window start: ` +
        "list them in this workspace's peacock.excludedSettings";
      return entry;
    }
    const recordPath = path.join(dir, CHROME_RECORD);
    let records = {};
    try {
      records = readJSON(recordPath);
      if (records === null || typeof records !== 'object' || Array.isArray(records)) throw new Error('holds no workspace map');
    } catch (err) {
      if (err.code !== 'ENOENT') throw new Error(`read ${recordPath}: ${err.message}`);
    }
    const record = { generation: null, keys: {}, ...records[target] };
    if (contract.enabled && generation !== null && record.generation === generation) return entry;
    const workbench = vscode.workspace.getConfiguration('workbench');
    const workspace = workbench.inspect('colorCustomizations')?.workspaceValue || {};
    const plan = planColors(workspace, record.keys, wantChrome);
    if (!sameColors(plan.next, workspace)) {
      // An emptied layer drops the setting instead of leaving `{}` in the user's workspace file.
      const next = Object.keys(plan.next).length ? plan.next : undefined;
      await workbench.update('colorCustomizations', next, vscode.ConfigurationTarget.Workspace);
    }
    if (Object.keys(plan.record).length) records[target] = { generation, keys: plan.record };
    else delete records[target];
    if (Object.keys(records).length) writeJSONAtomic(recordPath, records);
    else fs.rmSync(recordPath, { force: true });
  } catch (err) {
    entry.error = `apply generation ${generation} to the workbench.colorCustomizations of ${target}: ${err?.message ?? err}`;
  }
  return entry;
}

// applyContract reads the contract and brings the user's settings to it; every outcome but an
// absent contract ends in an ack, a failure carried in its `error`, never dropped. The ack is
// written inside the lock, so two windows never drop each other's chrome entry. It returns the
// contract it read, or undefined when there was none to read.
async function applyContract(dir) {
  const contractPath = path.join(dir, CONTRACT);
  let contract;
  try {
    contract = readJSON(contractPath);
    // JSON that parses to no object (null, an array, a scalar) is a broken contract, never an off.
    if (contract === null || typeof contract !== 'object' || Array.isArray(contract)) {
      const kind = contract === null ? 'null' : Array.isArray(contract) ? 'an array' : `a ${typeof contract}`;
      throw new Error(`holds ${kind}, not a contract object`);
    }
  } catch (err) {
    if (err.code === 'ENOENT') return;
    const broken = { generation: null, appliedAt: '', keys: [], error: `read ${contractPath}: ${err.message}` };
    try {
      await withLock(dir, () => writeAck(dir, broken, null, null));
    } catch (lockErr) {
      console.error(`Professor: pfm theme ack of the unreadable ${contractPath} written without the lock: ${lockErr?.stack ?? lockErr}`);
      writeAck(dir, broken, null, null);
    }
    return;
  }
  const want = contract.enabled ? hexColors(contract.colors, TERMINAL_KEY) : {};
  const wantChrome = contract.enabled ? hexColors(contract.chrome, CHROME_KEY) : {};
  const generation = Number.isInteger(contract.generation) ? contract.generation : null;
  const ack = { generation, appliedAt: '', keys: Object.keys(want).sort(), error: '' };
  const target = chromeTarget();
  let entry = null;
  try {
    await withLock(dir, async () => {
      try {
        await applyGlobal(dir, contract, generation, want);
      } catch (err) {
        ack.error = `apply generation ${generation} to workbench.colorCustomizations: ${err?.message ?? err}`;
      }
      if (target) entry = await applyChrome(dir, target, contract, generation, wantChrome);
      writeAck(dir, ack, target, entry);
    });
  } catch (err) {
    // The lock could not be taken (or the ack could not be written under it): still ack, with the
    // other windows' entries as the ack on disk last held them.
    ack.error = `apply generation ${generation} to workbench.colorCustomizations: ${err?.message ?? err}`;
    if (target) entry = { generation, keys: Object.keys(wantChrome).sort(), error: ack.error };
    writeAck(dir, ack, target, entry);
  }
  return contract;
}

// startThemeSync watches the contract (polling: the file and its directory may not exist until
// the first `pfm theme on`) and applies it at start and on every change, one apply at a time; an
// edit of peacock.excludedSettings applies it again, so listing the chrome keys takes effect
// without a reload. Each applied contract then goes to onApplied (native.js's follow, which
// switches the Color Theme). It returns the disposable for activate's subscriptions and
// syncTheme, the extension's exported API.
function startThemeSync(dir = stateDir(), onApplied = () => {}) {
  const contractPath = path.join(dir, CONTRACT);
  let chain = Promise.resolve();
  const syncTheme = () => {
    chain = chain.then(() => applyContract(dir)).then((contract) => contract && onApplied(contract)).catch((err) => {
      console.error(`Professor: pfm theme sync of ${contractPath} failed: ${err?.stack ?? err}`);
    });
    return chain;
  };
  // unref: the watcher alone never keeps a host process alive; VS Code's extension host lives on.
  fs.watchFile(contractPath, { interval: POLL_MS }, () => syncTheme())?.unref?.();
  const settings = vscode.workspace.onDidChangeConfiguration((event) => {
    if (event.affectsConfiguration('peacock.excludedSettings')) syncTheme();
  });
  syncTheme();
  return {
    disposable: {
      dispose: () => {
        fs.unwatchFile(contractPath);
        settings.dispose();
      },
    },
    syncTheme,
  };
}

module.exports = { startThemeSync, stateDir, planColors };

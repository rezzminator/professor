// pfm writes current.json; this UI-host extension reads the LOCAL contract in every window,
// including Remote-SSH. Terminal colours and the frame go into Global settings; Workspace
// chrome overrides old workspace/Peacock colours, including folder-only and remote windows.
// Owned keys (also inside existing [Theme] blocks) retain their prior values for theme off.
// Windows serialize records and acknowledgements under vscode-apply.lock.
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
const CHROME_KEY = /^(window|titleBar|activityBar|activityBarTop|activityBarBadge|commandCenter|sash|statusBar|statusBarItem)\./;
const COLOR_VALUE = /^#(?:[0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$/;

function hexColors(colors, keyPattern) {
  const out = {};
  if (!colors || typeof colors !== 'object') return out;
  for (const [key, value] of Object.entries(colors)) {
    if (keyPattern.test(key) && typeof value === 'string' && COLOR_VALUE.test(value)) out[key] = value;
  }
  return out;
}

// Keep legacy local workspace paths; every other target uses the full URI, authority included.
// The configuration API writes the Workspace layer even for a remote filesystem.
function chromeTarget() {
  const file = vscode.workspace.workspaceFile;
  if (file) return file.scheme === 'file' ? file.fsPath : file.toString();
  return vscode.workspace.workspaceFolders?.[0]?.uri.toString() || null;
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
    if (key.startsWith('[') || key in want || next[key] !== entry.value) continue;
    if (entry.prior === null || entry.prior === undefined) delete next[key];
    else next[key] = entry.prior;
  }
  for (const [key, value] of Object.entries(want)) {
    const entry = record[key];
    const prior = entry && next[key] === entry.value ? entry.prior : next[key] ?? null;
    next[key] = value;
    kept[key] = { value, prior };
  }
  // A theme-scoped colour beats the unscoped setting even in a lower configuration layer.
  // Replace only managed keys that already exist in each block, with per-key restore records.
  for (const key of new Set([...Object.keys(global), ...Object.keys(record)])) {
    if (!key.startsWith('[')) continue;
    const block = global[key];
    if (block !== undefined && (!block || typeof block !== 'object' || Array.isArray(block))) continue;
    const scopedWant = Object.fromEntries(Object.entries(want).filter(([color]) => color in (block || {})));
    const scoped = planColors(block || {}, record[key]?.keys || {}, scopedWant);
    if (Object.keys(scoped.next).length) next[key] = scoped.next;
    else delete next[key];
    if (Object.keys(scoped.record).length) kept[key] = { keys: scoped.record };
  }
  return { next, record: kept };
}

function sameColors(a, b) {
  const keys = Object.keys(a);
  return keys.length === Object.keys(b).length && keys.every((key) => JSON.stringify(b[key]) === JSON.stringify(a[key]));
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

// applyGlobal brings Global terminal and frame colours to the contract, under
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
  if (contract.enabled && generation !== null && record.generation === generation &&
      Object.keys(want).every((key) => record.keys[key]?.value === want[key])) return;
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
    if (contract.enabled && generation !== null && record.generation === generation &&
        Object.keys(wantChrome).every((key) => record.keys[key]?.value === wantChrome[key])) return entry;
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
  const chromeEntry = { generation, keys: Object.keys(wantChrome).sort(), error: '' };
  let entry = null;
  try {
    await withLock(dir, async () => {
      try {
        await applyGlobal(dir, contract, generation, { ...want, ...wantChrome });
      } catch (err) {
        ack.error = `apply generation ${generation} to workbench.colorCustomizations: ${err?.message ?? err}`;
      }
      if (target) entry = await applyChrome(dir, target, contract, generation, wantChrome);
      if (!target) {
        const unlisted = peacockUnlisted(wantChrome);
        if (unlisted.length) chromeEntry.error = `Peacock repaints ${unlisted.length} frame keys: list them in peacock.excludedSettings`;
      }
      writeAck(dir, ack, target || 'global', target ? entry : chromeEntry);
    });
  } catch (err) {
    // The lock could not be taken (or the ack could not be written under it): still ack, with the
    // other windows' entries as the ack on disk last held them.
    ack.error = `apply generation ${generation} to workbench.colorCustomizations: ${err?.message ?? err}`;
    if (target) entry = { generation, keys: Object.keys(wantChrome).sort(), error: ack.error };
    writeAck(dir, ack, target || 'global', target ? entry : { ...chromeEntry, error: ack.error });
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

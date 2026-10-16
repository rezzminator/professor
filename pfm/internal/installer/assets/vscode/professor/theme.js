// `pfm theme` inside VS Code. pfm (pfm/internal/themesync) renders one palette into every surface
// the user looks at; VS Code's is the integrated terminal. pfm writes the contract
// {stateDir}/current.json — {enabled, generation, palette, title, mode, appearance, colors} — and
// this module merges `colors` into the user's GLOBAL workbench.colorCustomizations, touching no
// other key, then acknowledges in {stateDir}/vscode-ack.json — {generation, appliedAt, keys,
// error} — which `pfm theme status` reads back, so a window that did not follow shows as out of
// sync instead of looking themed. With no contract file nothing here writes anything.
//
// Every key it sets is recorded, with the value it replaced and the generation that set it, in
// {stateDir}/vscode-applied.json; a key the next generation drops, or every key on
// `pfm theme off` (enabled:false), goes back to that prior value — unless the user changed it
// since, in which case the user's value stays.
// Each window runs its own copy against one settings file, so the read-merge-write runs under a
// lock directory in the state dir: a second window waits, then finds the work already done.
const vscode = require('vscode');
const fs = require('fs');
const os = require('os');
const path = require('path');

const CONTRACT = 'current.json';
const ACK = 'vscode-ack.json';
const RECORD = 'vscode-applied.json';
const LOCK = 'vscode-apply.lock';
const POLL_MS = 2000;
const LOCK_STALE_MS = 30000;
const LOCK_WAIT_MS = 100;
const LOCK_TRIES = 100;

// themesync.DefaultStateDir(home): the same path pfm writes, unless the user points elsewhere.
function stateDir() {
  const configured = vscode.workspace.getConfiguration('professor.theme').get('stateDir');
  return configured || path.join(os.homedir(), '.local', 'state', 'pfm', 'theme');
}

function readJSON(file) {
  return JSON.parse(fs.readFileSync(file, 'utf8'));
}

function writeJSONAtomic(file, value) {
  const temp = path.join(path.dirname(file), `.${path.basename(file)}.${process.pid}.tmp`);
  fs.writeFileSync(temp, JSON.stringify(value, null, 2) + '\n');
  fs.renameSync(temp, file);
}

// planColors is the merge, pure: `global` the user's current colorCustomizations, `record` the
// keys a previous generation set ({key: {value, prior}}, prior null = the key was absent), `want`
// the colours this generation asks for ({} when off). It returns the next customizations and the
// record to keep.
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

// applyContract reads the contract and brings the user's settings to it; every outcome but an
// absent contract ends in an ack, a failure carried in its `error`, never dropped.
async function applyContract(dir) {
  const contractPath = path.join(dir, CONTRACT);
  let contract;
  try {
    contract = readJSON(contractPath);
  } catch (err) {
    if (err.code === 'ENOENT') return;
    writeJSONAtomic(path.join(dir, ACK), {
      generation: null, appliedAt: new Date().toISOString(), keys: [], error: `read ${contractPath}: ${err.message}`,
    });
    return;
  }
  const want = contract.enabled && contract.colors && typeof contract.colors === 'object' ? contract.colors : {};
  const ack = { generation: Number.isInteger(contract.generation) ? contract.generation : null, appliedAt: '', keys: Object.keys(want).sort(), error: '' };
  try {
    await withLock(dir, async () => {
      const recordPath = path.join(dir, RECORD);
      let record = { generation: null, keys: {} };
      try {
        record = { generation: null, keys: {}, ...readJSON(recordPath) };
      } catch (err) {
        if (err.code !== 'ENOENT') throw new Error(`read ${recordPath}: ${err.message}`);
      }
      // A generation applies once: another window, or this one restarting, finds it done, and a
      // colour the user set by hand since stays theirs until the next `pfm theme set` or sync.
      if (contract.enabled && ack.generation !== null && record.generation === ack.generation) return;
      const workbench = vscode.workspace.getConfiguration('workbench');
      const global = workbench.inspect('colorCustomizations')?.globalValue || {};
      const plan = planColors(global, record.keys, want);
      if (!sameColors(plan.next, global)) {
        await workbench.update('colorCustomizations', plan.next, vscode.ConfigurationTarget.Global);
      }
      if (Object.keys(plan.record).length) writeJSONAtomic(recordPath, { generation: ack.generation, keys: plan.record });
      else fs.rmSync(recordPath, { force: true });
    });
  } catch (err) {
    ack.error = `apply generation ${ack.generation} to workbench.colorCustomizations: ${err?.message ?? err}`;
  }
  ack.appliedAt = new Date().toISOString();
  writeJSONAtomic(path.join(dir, ACK), ack);
}

// startThemeSync watches the contract (polling: the file and its directory may not exist until
// the first `pfm theme set`) and applies it at start and on every change, one apply at a time. It
// returns the disposable for activate's subscriptions and syncTheme, the extension's exported API.
function startThemeSync() {
  const dir = stateDir();
  const contractPath = path.join(dir, CONTRACT);
  let chain = Promise.resolve();
  const syncTheme = () => {
    chain = chain.then(() => applyContract(dir)).catch((err) => {
      console.error(`Professor: pfm theme sync of ${contractPath} failed: ${err?.stack ?? err}`);
    });
    return chain;
  };
  // unref: the watcher alone never keeps a host process alive; VS Code's extension host lives on.
  fs.watchFile(contractPath, { interval: POLL_MS }, () => syncTheme())?.unref?.();
  syncTheme();
  return { disposable: { dispose: () => fs.unwatchFile(contractPath) }, syncTheme };
}

module.exports = { startThemeSync, planColors };

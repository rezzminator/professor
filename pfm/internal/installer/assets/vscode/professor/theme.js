// pfm writes current.json; the extension reads the contract of the host it runs on, which is the
// window's own: a local window's machine, or a Remote-SSH window's remote host (extensionKind
// ["workspace"]); a remote window served from the client's side paints nothing. Terminal colours
// and the frame go into Global settings; Workspace chrome overrides old workspace/Peacock colours,
// including folder-only and remote windows.
// Owned keys (also inside existing [Theme] blocks) retain their prior values for theme off.
// Windows serialize settings and records under vscode-apply.lock, acknowledgements under
// vscode-ack.lock (also when applying failed). Locks are never evicted by age.
const vscode = require('vscode');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { randomUUID } = require('crypto');

const CONTRACT = 'current.json';
const ACK = 'vscode-ack.json';
const RECORD = 'vscode-applied.json';
const CHROME_RECORD = 'vscode-chrome.json';
const PEACOCK = 'johnpapa.vscode-peacock';
const LOCK = 'vscode-apply.lock';
const ACK_LOCK = 'vscode-ack.lock';
// Publication failures cannot use the failed mutex or overwrite shared history.
// A replacement may reclaim only a provably dead owner in its own process scope.
const ACK_FAILURES = 'vscode-ack-failures';
const ACK_OWNER = randomUUID();
const ACK_PROCESS = { pid: process.pid, scope: ackProcessScope() };
const POLL_MS = 2000;
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
function planColors(global, record, want, inherited = {}) {
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
  for (const key of new Set([...Object.keys(global), ...Object.keys(record), ...Object.keys(inherited)])) {
    if (!key.startsWith('[')) continue;
    const block = global[key];
    if (block !== undefined && (!block || typeof block !== 'object' || Array.isArray(block))) continue;
    const lower = inherited[key];
    const inheritedBlock = lower && typeof lower === 'object' && !Array.isArray(lower) ? lower : {};
    const scopedWant = Object.fromEntries(Object.entries(want).filter(([color]) => color in (block || {}) || color in inheritedBlock));
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

// A slow or suspended extension host still owns its lock. A dead host's lock must be removed
// manually after closing all extension hosts; age cannot distinguish these cases safely.
async function withLock(dir, work, name = LOCK) {
  const lock = path.join(dir, name);
  for (let attempt = 0; ; attempt++) {
    try {
      fs.mkdirSync(lock);
      break;
    } catch (err) {
      if (err.code !== 'EEXIST') throw new Error(`lock ${lock}: ${err.message}`);
      if (attempt >= LOCK_TRIES) throw new Error(`lock ${lock} held by another window; left intact`);
      await new Promise((resolve) => setTimeout(resolve, LOCK_WAIT_MS));
    }
  }
  const identity = fs.statSync(lock);
  const owner = randomUUID();
  const ownerPath = path.join(lock, 'owner');
  fs.writeFileSync(ownerPath, owner, { flag: 'wx' });
  try {
    return await work();
  } finally {
    const current = fs.statSync(lock, { throwIfNoEntry: false });
    if (!current || current.dev !== identity.dev || current.ino !== identity.ino ||
        fs.readFileSync(ownerPath, 'utf8') !== owner) {
      throw new Error(`lock ${lock} ownership changed; replacement left intact`);
    }
    fs.rmSync(lock, { recursive: true });
  }
}

// Only genuinely absent history starts empty. Unknown history belongs to other
// windows too: leave its bytes intact and publish a separate diagnostic.
function previousChrome(dir) {
  const ackPath = path.join(dir, ACK);
  try {
    const ack = readJSON(ackPath);
    const object = (value) => value && typeof value === 'object' && !Array.isArray(value);
    if (!object(ack)) throw new Error('expected an acknowledgement object');
    if (ack.chrome === undefined) return {}; // legacy terminal-only acknowledgement
    if (!object(ack.chrome) || Object.values(ack.chrome).some((entry) => !object(entry))) {
      throw new Error('invalid window chrome history');
    }
    return ack.chrome;
  } catch (err) {
    if (err.code === 'ENOENT') return {};
    throw new Error(`read acknowledgement history ${ackPath}: ${err.message}`);
  }
}

// Boot UUIDs scope PIDs to one machine boot; Linux also needs its PID namespace.
// Without this proof, retain predecessor evidence rather than guessing it dead.
function ackProcessScope() {
  try {
    if (process.platform === 'linux') {
      const boot = fs.readFileSync('/proc/sys/kernel/random/boot_id', 'utf8').trim();
      const namespace = fs.readlinkSync('/proc/self/ns/pid');
      if (boot && namespace) return `linux:${boot}:${namespace}`;
    } else if (process.platform === 'darwin') {
      const boot = require('child_process').execFileSync('/usr/sbin/sysctl', ['-n', 'kern.bootsessionuuid'], { encoding: 'utf8', timeout: 1000 }).trim();
      if (boot) return `darwin:${boot}`;
    }
    throw new Error('no boot/process namespace identity');
  } catch (err) {
    console.error(`Professor: cannot prove predecessor ACK owners dead; diagnostics will be retained: ${err.message}`);
    return null;
  }
}

// Run only after a successful serialized ACK merge. ESRCH is proof only within
// the exact same boot/process namespace; EPERM, reused PIDs and unknown owners
// remain visible to Go. Never repair or discard unreadable shared ACK history.
function clearDeadAckFailures(dir) {
  if (!ACK_PROCESS.scope) return;
  let names;
  try { names = fs.readdirSync(dir); } catch (err) {
    if (err.code === 'ENOENT') return;
    throw new Error(`enumerate acknowledgement failures ${dir}: ${err.message}`);
  }
  for (const name of names) {
    if (name.startsWith('.') || !name.endsWith('.json') || name === `${ACK_OWNER}.json`) continue;
    const file = path.join(dir, name);
    let identity, failure;
    try {
      identity = fs.lstatSync(file);
      if (!identity.isFile()) continue;
      failure = readJSON(file);
    } catch (err) {
      if (err.code === 'ENOENT') continue; // reporting owner recovered concurrently
      throw new Error(`read acknowledgement failure ${file}: ${err.message}`);
    }
    const owner = failure?.owner;
    if (owner?.scope !== ACK_PROCESS.scope || !Number.isSafeInteger(owner.pid) || owner.pid <= 0) continue;
    try { process.kill(owner.pid, 0); continue; } catch (err) {
      if (err.code !== 'ESRCH') {
        console.error(`Professor: acknowledgement owner ${file} is not proven dead: ${err.message}`);
        continue;
      }
    }
    const current = fs.lstatSync(file, { throwIfNoEntry: false });
    if (current && current.dev === identity.dev && current.ino === identity.ino) fs.rmSync(file);
  }
}

// Every shared acknowledgement merges under its mutex. Failure evidence is a
// separate per-owner file, also read by Go before accepting same-generation ACKs.
async function writeAck(dir, ack, target, entry) {
  const failureDir = path.join(dir, ACK_FAILURES);
  const failurePath = path.join(failureDir, `${ACK_OWNER}.json`);
  try {
    await withLock(dir, () => {
      const chrome = previousChrome(dir);
      if (target) chrome[target] = entry;
      writeJSONAtomic(path.join(dir, ACK), { ...ack, appliedAt: new Date().toISOString(), chrome });
      if (!ack.error && !entry?.error) clearDeadAckFailures(failureDir);
    }, ACK_LOCK);
    fs.rmSync(failurePath, { force: true });
  } catch (err) {
    const error = `publish acknowledgement generation ${ack.generation}: ${err.message}`;
    fs.mkdirSync(failureDir, { recursive: true });
    writeJSONAtomic(failurePath, { generation: ack.generation, target, error, owner: ACK_PROCESS });
    throw new Error(error);
  }
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
async function applyChrome(dir, target, contract, generation, wantChrome, wantTerminal = {}) {
  const want = { ...wantTerminal, ...wantChrome };
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
        Object.keys(want).every((key) => record.keys[key]?.value === want[key])) return entry;
    const workbench = vscode.workspace.getConfiguration('workbench');
    const layers = workbench.inspect('colorCustomizations');
    const workspace = layers?.workspaceValue || {};
    // Theme-scoped Global keys outrank unscoped Workspace keys. Shadow only
    // managed inherited keys here, recording their true Workspace absence for off.
    const plan = planColors(workspace, record.keys, want, layers?.globalValue || {});
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
// merged inside its own mutex, so two windows never drop each other's chrome entry. It returns the
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
    await writeAck(dir, broken, null, null);
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
      const remote = vscode.env.remoteName !== undefined;
      try {
        // Global is the client's user settings even from a remote host. A remote
        // contract must not repaint other hosts' windows or capture their prior values.
        if (!remote) await applyGlobal(dir, contract, generation, { ...want, ...wantChrome });
        else if (!target) throw new Error('remote window has no workspace; client settings left unchanged');
      } catch (err) {
        ack.error = `apply generation ${generation} to workbench.colorCustomizations: ${err?.message ?? err}`;
      }
      if (target) {
        entry = await applyChrome(dir, target, contract, generation, wantChrome, remote ? want : {});
        if (remote && entry.error) ack.error = entry.error;
      }
      if (!target) {
        const unlisted = peacockUnlisted(wantChrome);
        if (unlisted.length) chromeEntry.error = `Peacock repaints ${unlisted.length} frame keys: list them in peacock.excludedSettings`;
      }
    });
  } catch (err) {
    ack.error = `apply generation ${generation} to workbench.colorCustomizations: ${err?.message ?? err}`;
    if (target) entry = { generation, keys: Object.keys(wantChrome).sort(), error: ack.error };
    else chromeEntry.error = ack.error;
  }
  // Publication is attempted once, outside the apply catch. Retrying a failed
  // ACK mutex cannot report its own failure and used to leave stale success.
  await writeAck(dir, ack, target || 'global', target ? entry : chromeEntry);
  return contract;
}

// startThemeSync watches the contract (polling: the file and its directory may not exist until
// the first `pfm theme on`) and applies it at start and on every change, one apply at a time; an
// edit of peacock.excludedSettings applies it again, so listing the chrome keys takes effect
// without a reload. Each applied contract then goes to onApplied (native.js's follow, which
// switches the Color Theme). It returns the disposable for activate's subscriptions and
// syncTheme, the extension's exported API. A refusal (choose.js notHere, for a remote window
// served from the client's side) applies nothing at all: the contract under this host's home is
// the client's, whose theme is not the remote chats'; it is shown once, and no ack is written,
// so the remote host's `pfm theme` keeps reporting the generation no window applied.
function startThemeSync(dir = stateDir(), onApplied = () => {}, refusal = '') {
  if (refusal) {
    vscode.window.showWarningMessage(`Professor: this window's frame and terminal stay unpainted — ${refusal}.`);
    return { disposable: { dispose: () => {} }, syncTheme: () => Promise.resolve() };
  }
  const contractPath = path.join(dir, CONTRACT);
  let chain = Promise.resolve();
  const syncTheme = () => {
    chain = chain.then(() => applyContract(dir)).then((contract) => contract && onApplied(contract)).catch((err) => {
      console.error(`Professor: pfm theme sync of ${contractPath} failed: ${err?.stack ?? err}`);
      vscode.window.showErrorMessage(`Professor: pfm theme sync failed: ${err?.message ?? err}`);
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

module.exports = { startThemeSync, stateDir, planColors, chromeTarget, withLock };

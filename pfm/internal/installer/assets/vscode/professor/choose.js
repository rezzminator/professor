// `Professor: Choose Theme` — picking Claude Code's theme from inside VS Code. The extension never
// writes a setting itself: it asks the installed pfm what there is to choose from
// (`pfm theme list --json`, themesync.ThemeList) and tells it the pick (`pfm theme use {name}`),
// which sets `theme` in every Claude account's settings.json and runs one sync; the contract
// theme.js follows does the rest. pfm is run by execFile with an argument array, never through a
// shell, and only where pfmHere holds: a local window, or a remote window whose extension host is
// the machine pfm runs on (a Linux host driven through Remote-SSH, where pfm, Claude Code, tmux
// and Herdr all live). Anywhere else the Claude settings are another machine's, so the handler
// refuses before it runs anything (package.json also hides the command from that palette).
const vscode = require('vscode');
const { execFile } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const LIST_TIMEOUT_MS = 30000;
const USE_TIMEOUT_MS = 30000;
const MAX_BUFFER = 4 * 1024 * 1024;

// themecmd's pfmLauncher: the installed pfm, the one door a host asset runs.
function pfmBinary() {
  return path.join(os.homedir(), '.local', 'bin', 'pfm');
}

// clientSide says whether this is a remote window whose extension host is the client's (UI) side:
// package.json's extensionKind ["workspace"] never puts Professor there, but a client copy older
// than 0.6.0 ("ui", whose kind VS Code takes over the remote copy's) or a remote.extensionKind
// override does, and there pfm, the contract and the Claude chats are the client's, not the window's.
function clientSide(extensionKind) {
  return vscode.env.remoteName !== undefined && extensionKind !== vscode.ExtensionKind?.Workspace;
}

// notHere is why a remote window keeps out of pfm, and what the user does about it.
function notHere(extensionKind) {
  const remote = vscode.env.remoteName;
  if (clientSide(extensionKind)) {
    return `this ${remote} window runs Professor on this computer, not on the ${remote} host its Claude chats live on: run pfm install on the ${remote} host, update Professor on this computer (pfm install) or drop its remote.extensionKind entry for professor.professor, then reload the window`;
  }
  return `the ${remote} host has no ${pfmBinary()}: run pfm install there, then reload the window`;
}

// pfmHere says whether this window's Claude settings are the ones the extension host's pfm sets:
// always in a local window; in a remote window only when the extension runs on the remote side
// (extensionKind Workspace) and that machine has the installed pfm. A remote window served from
// the client's side, or whose remote host has no pfm, keeps out.
function pfmHere(extensionKind) {
  if (vscode.env.remoteName === undefined) return true;
  if (clientSide(extensionKind)) return false;
  try {
    fs.accessSync(pfmBinary(), fs.constants.X_OK);
    return true;
  } catch (err) {
    if (err.code !== 'ENOENT') console.error(`Professor: ${pfmBinary()} is not runnable here, this ${vscode.env.remoteName} window keeps out of pfm: ${err?.message ?? err}`);
    return false;
  }
}

// runPfm resolves, never rejects: {ok, exited, code, stdout, stderr, cause}. `exited` tells a pfm that
// ran and exited non-zero (a verdict, with its stderr) from one that could not run or was killed at
// the timeout (the cause).
function runPfm(args, timeoutMs) {
  return new Promise((resolve) => {
    execFile(pfmBinary(), args, { timeout: timeoutMs, maxBuffer: MAX_BUFFER, encoding: 'utf8' }, (err, stdout, stderr) => {
      if (!err) {
        resolve({ ok: true, exited: true, code: 0, stdout, stderr, cause: null });
        return;
      }
      const exited = typeof err.code === 'number';
      resolve({ ok: false, exited, code: err.code, stdout: stdout ?? '', stderr: stderr ?? '', cause: err });
    });
  });
}

// why is a failed run in pfm's own words: its stderr, else its exit code, else why it did not run.
function why(result, timeoutMs) {
  const stderr = result.stderr.trim();
  if (stderr) return stderr;
  if (result.exited) return `exit code ${result.code}`;
  if (result.cause.killed) return `${pfmBinary()} did not finish within ${Math.round(timeoutMs / 1000)} s`;
  return result.cause.message;
}

// readList turns the run of `pfm theme list --json` into the list it printed, or throws what the user
// should read. A non-zero exit is a failure unless it is exit 1 with the list's `errors` filled in:
// pfm prints the JSON then too, so the readable themes are still offered and the errors shown.
function readList(result) {
  if (!result.ok && !result.exited) throw new Error(why(result, LIST_TIMEOUT_MS));
  let list;
  try {
    list = JSON.parse(result.stdout);
  } catch (err) {
    throw new Error(result.ok ? `pfm theme list --json printed unparsable JSON: ${err.message}` : why(result, LIST_TIMEOUT_MS));
  }
  const themes = list?.themes;
  const valid = Array.isArray(themes) && themes.every((theme) => theme && typeof theme.name === 'string' && typeof theme.value === 'string');
  if (!valid) throw new Error(result.ok ? 'pfm theme list --json printed JSON without a themes list' : why(result, LIST_TIMEOUT_MS));
  const errors = Array.isArray(list.errors) ? list.errors : [];
  if (!result.ok && !(result.code === 1 && errors.length)) throw new Error(why(result, LIST_TIMEOUT_MS));
  return { themes, errors };
}

function detail(theme) {
  const parts = [];
  if (theme.current) parts.push('current');
  if (Array.isArray(theme.missing) && theme.missing.length) parts.push(`missing in ${theme.missing.join(', ')}`);
  return parts.length ? parts.join(' · ') : undefined;
}

// chooseTheme is the command's handler; here is pfmHere and whyNot notHere, bound to this extension host.
async function chooseTheme(here, whyNot) {
  if (!here()) {
    vscode.window.showWarningMessage(`Professor: Choose Theme sets no Claude theme here — ${whyNot()}.`);
    return;
  }
  let list;
  try {
    list = readList(await runPfm(['theme', 'list', '--json'], LIST_TIMEOUT_MS));
  } catch (err) {
    vscode.window.showErrorMessage(`Professor: could not list Claude Code themes — ${err?.message ?? err}`);
    return;
  }
  if (list.errors.length) {
    const unread = list.errors.map((entry) => `${entry.path}: ${entry.error}`).join('; ');
    vscode.window.showWarningMessage(`Professor: pfm could not read ${list.errors.length} theme source(s) — ${unread}`);
  }
  const items = list.themes.map((theme) => ({
    label: theme.title || theme.name,
    description: theme.value,
    detail: detail(theme),
    theme,
  }));
  // The current theme leads, so the pick opens on it.
  items.sort((a, b) => Number(Boolean(b.theme.current)) - Number(Boolean(a.theme.current)));
  const picked = await vscode.window.showQuickPick(items, {
    title: 'Professor: Choose Theme',
    placeHolder: "Claude Code's theme for new chats",
    matchOnDescription: true,
  });
  if (!picked) return;
  await vscode.window.withProgress(
    { location: vscode.ProgressLocation.Notification, title: `Setting Claude Code theme ${picked.label}…` },
    async () => {
      // The value, never the name: a custom theme whose stem is a built-in id shares that id's name.
      const result = await runPfm(['theme', 'use', picked.theme.value], USE_TIMEOUT_MS);
      if (result.ok) {
        vscode.window.showInformationMessage(`Theme ${picked.label} set: new chats follow; run /theme in open chats`);
      } else {
        vscode.window.showErrorMessage(`Professor: could not set theme ${picked.label} — ${why(result, USE_TIMEOUT_MS)}`);
      }
    },
  );
}

module.exports = { chooseTheme, clientSide, notHere, pfmHere, runPfm, why };

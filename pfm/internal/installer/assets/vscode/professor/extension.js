// Professor — the framework's assistant inside VS Code. pfm installs it (`pfm install --vscode`)
// and links it into every VS Code extensions directory on the host.
//
// Its first surface is the Professor terminal profile: a login shell that opens the pfm chat
// fleet, whose tab carries the attached chat's live name, and which puts you in the terminal
// you just made. Each new terminal also takes the next icon+colour pair — icons and colours
// advance on independent counters persisted in globalState — so tabs read apart at a glance.
//
// Two entry points reach the terminal: the contributed profile (the + dropdown's "Professor"
// entry) and the professor.newChatTerminal command (palette + its keybinding). Both resolve
// through provideTerminalProfile -> nextTerminal, the ONE builder below, so both carry the same
// icon/colour and env. The onDidOpenTerminal focus hook covers both entry points too.
const vscode = require('vscode');

// Terminals this activation handed to VS Code and has not yet seen open, keyed by the marker
// each one carries in its env. In-memory on purpose: a terminal revived by a window reload was
// not asked for by this activation, so it never steals focus.
const pending = new Set();

function nextTerminal(context) {
  const cfg = vscode.workspace.getConfiguration('professor.terminal');
  const icons = cfg.get('icons'), colors = cfg.get('colors');
  const n = context.globalState.get('terminal.n', 0);
  context.globalState.update('terminal.n', n + 1);
  const marker = `${n}-${Date.now()}`;
  pending.add(marker);
  // No `name`: a named terminal gets a static title, and VS Code's label then ignores the
  // ${sequence} title tmux sends — every chat tab would read the same fixed word forever.
  return {
    shellPath: cfg.get('shellPath'),
    shellArgs: cfg.get('shellArgs'),
    // A terminal app launched FROM inside a chat (VS Code relaunched by a chat's `code .`, a
    // window manager a chat spawned, …) inherits that chat's own environment, so every terminal
    // it then opens would look like a shell running INSIDE the chat that never existed —
    // CC_SESSION_UNSET in pfm.zsh (~line 56) is the canonical list of those chat-identity
    // markers, plus the TMUX pair that names its tmux server. A terminal this extension opens is
    // an app surface, not a nested chat, so it must not carry them. `null` is how VS Code deletes
    // an inherited env var rather than merely leaving it unset here.
    env: {
      ...cfg.get('env'),
      CLAUDECODE: null,
      CLAUDE_CODE_SESSION_ID: null,
      CLAUDE_CODE_CHILD_SESSION: null,
      TMUX: null,
      TMUX_PANE: null,
      PROFESSOR_TERMINAL: marker,
    },
    iconPath: new vscode.ThemeIcon(icons[n % icons.length]),
    color: new vscode.ThemeColor(colors[n % colors.length]),
  };
}

function activate(context) {
  context.subscriptions.push(
    // A contributed profile's terminal reaches the workbench with no id to focus, so it
    // activates the newest instance it already knows — the PREVIOUS terminal — and focus lands
    // one behind. The ext host fires onDidOpenTerminal once the workbench registers ours;
    // showing it then puts the user in the terminal they just made.
    vscode.window.onDidOpenTerminal((terminal) => {
      const marker = terminal.creationOptions?.env?.PROFESSOR_TERMINAL;
      if (marker && pending.delete(marker)) terminal.show(false);
    }),
    vscode.window.registerTerminalProfileProvider('professor.terminal', {
      provideTerminalProfile: () => new vscode.TerminalProfile(nextTerminal(context)),
    }),
    // Delegates to the SAME contributed-profile route the + dropdown uses — never
    // vscode.window.createTerminal(options) with its own options, which renders the default
    // profile's icon/colour instead of the extension's own (issue #24 finding 10).
    // extensionIdentifier is 'publisher.name' from this extension's own manifest; id/title match
    // the profile declared in package.json's contributes.terminal.profiles.
    vscode.commands.registerCommand('professor.newChatTerminal', () => {
      vscode.commands.executeCommand('workbench.action.terminal.newWithProfile', {
        config: { extensionIdentifier: context.extension.id, id: 'professor.terminal', title: 'Professor' },
      }).then(undefined, (err) => {
        // A refused route (profile not registered, VS Code too old) is shown, never dropped.
        vscode.window.showErrorMessage(`Professor: could not open a chat terminal — ${err?.message ?? err}`);
      });
    }),
  );
}
function deactivate() {}
module.exports = { activate, deactivate };

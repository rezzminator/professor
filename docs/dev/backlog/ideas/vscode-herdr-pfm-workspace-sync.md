# VS Code workspace sync with Herdr and pfm chats

**Status:** idea, not built — **Home:** `pfm/internal/installer/assets/vscode/professor/` (extend the shipped extension, not a new one)

## Intent

VS Code's workspaces stay in sync with Herdr's workspaces and with pfm's open chats, keyed by directory. Every directory that appears in `pfm ls` (a repo, a git worktree, a Herdr workspace's cwd, any place a chat runs) has a matching VS Code workspace or folder, so opening, closing or moving work in one surface shows in the others.

## Facts

- pfm already ships a VS Code extension at `pfm/internal/installer/assets/vscode/professor/` (`extension.js`, `package.json`, `theme.js`); its `professor.terminal.env` default sets `PFM_AUTO_OPEN`. The sync extends it.
- `docs/dev/vscode-terminal-from-outside.md` is adjacent: it settles that an external process reaches a running VS Code window only through an extension (route 1, a URI handler) or macOS UI scripting, with the multi-root terminal-folder picker as a known trap. The sync's pfm → VS Code direction faces the same constraint.
- Herdr workspaces can be listed and created over Herdr's CLI and socket API: `herdr workspace create`, `herdr api snapshot` (protocol 22).
- The sync depends on pfm's Herdr seats (the herdr-substrate work): native chats in Herdr panes, a seat row per chat carrying its Herdr workspace.
- `pfm ls` groups chats by places (`pfm/internal/ui/places.go`): a place is a directory chats ran under, derived from the rows' project root and never probed, shown as the picker's places row with per-directory counts. Nested repositories and linked worktrees fall under their place.

## Open questions

- Which direction(s) the sync runs: VS Code → Herdr/pfm, Herdr/pfm → VS Code, or both.
- Whether a VS Code workspace means a multi-root `.code-workspace`, a window per directory, or folders in one window.
- What happens to a VS Code workspace when its last chat ends.

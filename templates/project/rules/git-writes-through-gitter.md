---
visible-to:
  include: ["main"]
---

# Only Claude's gitter writes git

Binds the main chat on every engine, and every loop where `visible-to` is inert, except a Claude agent whose type is exactly `gitter`; Claude's git-guard hook and pfm's Codex and OpenCode engine guard enforce it, and where neither is installed this rule is the guard.

- Yours: each read the guard's allowlist names (`status`, `log`, `diff`, `show`, `blame`, `branch` and `tag` listing, `ls-remote`, config reads), the dry runs `add -n`, `rm -n`, `clean -n` and `apply --check`, `git apply` to the working tree, `git stash push -- <own paths>` with `stash pop|apply|list|show` (on Codex and OpenCode only `list|show`), and `git checkout -- <named files>` or `git restore <named files>` (never `--staged`, `.`, a glob or the repo root).
- `.claude/scripts/worktree.sh`, where present: a Claude main chat's alone, every verb its header declares in sync with this item (`create`, `land`, `remove`, `list`, `prune`), while git tracks it and any `worktree/` helper directory beside it unchanged from HEAD; no sub-agent but gitter runs it; every refusal goes to gitter.
- Everything else is gitter's, `git fetch` included, as it moves remote-tracking refs: check a remote with `git ls-remote origin <branch>`.
- Hand off on Claude: spawn `gitter` with the absolute repo path, the phase (SETUP, COMMIT, MERGE, PUSH, PULL, RELEASE) or the exact change, and the check that passed.
- On Codex and OpenCode no chat or role writes git, gitter included: hand the exact change to the user.
- A denied git command goes to its hand-off as written: never retried, rephrased, split or wrapped to get past the guard.
- Push, tag and release run only on the user's explicit ask in the current turn, through gitter.

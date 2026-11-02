# Only Claude's gitter writes git

Binds every agent and the main chat on every engine, except a Claude agent whose type is exactly `gitter`; Claude's git-guard hook and pfm's Codex and OpenCode engine guard enforce it, and where neither is installed this rule is the guard.

- Yours: every read (`status`, `log`, `diff`, `show`, `blame`, `branch` and `tag` listing, `ls-remote`, config reads, `.claude/scripts/worktree.sh list` where present), the dry runs `add -n`, `rm -n`, `clean -n` and `apply --check`, `git apply` to the working tree, `git stash push -- <own paths>` with `stash pop|apply|list|show` (on Codex and OpenCode only `list|show`), and `git checkout -- <named files>` or `git restore <named files>` (never `--staged`, `.`, a glob or the repo root).
- Everything else is gitter's, `git fetch` included: it moves remote-tracking refs, so check a remote with `git ls-remote origin <branch>`, never fetch.
- Hand off on Claude: spawn `gitter` with the absolute repo path, the phase (SETUP, COMMIT, MERGE, PUSH, PULL, RELEASE) or the exact change, and the check that passed.
- On Codex and OpenCode no chat or role writes git, gitter included: hand the exact change to the user.
- A denied git command goes to its hand-off as written: never retried, rephrased, split or wrapped to get past the guard.
- Push, tag and release run only on the user's explicit ask in the current turn, through gitter.

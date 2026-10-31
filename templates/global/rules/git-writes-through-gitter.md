# Only gitter writes git

Binds every agent and the main chat on every engine, except an agent whose type is exactly `gitter`; on Claude the git-guard hook enforces it, elsewhere this rule is the guard.

- Yours: every read (`status`, `log`, `diff`, `show`, `blame`, `branch` and `tag` listing, `ls-remote`, config reads, `.claude/scripts/worktree.sh list` where present), the dry runs `add -n`, `rm -n`, `clean -n` and `apply --check`, `git apply` to the working tree, `git stash push -- <own paths>` with `stash pop|apply|list|show`, and `git checkout -- <named files>` or `git restore <named files>` (never `--staged`, `.`, a glob or the repo root).
- Everything else is gitter's, `git fetch` included: it moves remote-tracking refs, so check a remote with `git ls-remote origin <branch>`, never fetch.
- Hand off: spawn `gitter` with the absolute repo path, the phase (SETUP, COMMIT, MERGE, PUSH, PULL, RELEASE) or the exact change, and the check that passed.
- A denied command goes to gitter as written: never retried, rephrased, split or wrapped to get past the guard.
- Push, tag and release run only on the user's explicit ask in the current turn, through gitter.
- Where this engine has no gitter agent, the main chat makes a scoped git write only after the user's explicit authorization in the current turn; a sub-agent never does.

# Only gitter writes git

Binds every agent and the main chat on every engine; the one exception is an agent whose type is exactly `gitter`. On Claude the git-guard `PreToolUse` hook denies these writes; on an engine without it, this rule is the guard.

- Run yourself: every git read (`status`, `log`, `diff`, `show`, `blame`, `branch` and `tag` listing, `remote -v`, `ls-remote`, `worktree list`, config reads), `.claude/scripts/worktree.sh list` where present, `git stash push -- <own paths>` with `stash pop|apply|list|show`, `git checkout -- <named files>` and `git restore <named files>` without `--staged` (never `.`, `:/`, `*`, `**` or the repo root), the dry runs `add -n`, `rm -n`, `clean -n`, `git apply` to the working tree and `apply --check`, `archive`.
- Everything else that writes shared state goes to gitter: commit, merge, rebase, cherry-pick, revert, am, pull, push, fetch, `switch` or a branch checkout, every `reset`, staging (`add`, `rm`, `mv`, `restore --staged`), branch and tag writes, `worktree add|remove|prune|move|lock`, `.claude/scripts/worktree.sh create|remove|prune` where present, a whole-tree stash, `stash drop|clear`, `clean`, config writes, `gc`, `submodule add|update|deinit|sync`.
- Hand off: spawn `gitter` with the absolute repo path, the phase (SETUP, COMMIT, MERGE, PUSH, PULL, RELEASE) or the exact change, and the check that passed.
- A denied git command goes to gitter as it stands: never retried, rephrased, split, wrapped or obfuscated (`$(which git)`, a variable, `bash -c`) to get past the guard.
- Push, tag and release run only on the user's explicit ask in the current turn, through gitter too.
- Where this engine has no gitter agent, the main chat makes a scoped git write only after the user's explicit authorization in the current turn; a sub-agent never does.

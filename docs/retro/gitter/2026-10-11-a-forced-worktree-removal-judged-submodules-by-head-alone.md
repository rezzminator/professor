# 2026-10-11 — a forced worktree removal judged submodules by HEAD alone

Observed: the shipped `worktree.sh remove` (safety-repairs flight) refused unpushed submodule work by checking only each submodule's HEAD; the landing review proved a commit on a local submodule branch (HEAD back on the recorded commit) and a submodule stash were both deleted by `git worktree remove --force` with rc 0, since a linked worktree's submodule gitdirs live under its private Git directory. The fix judges `git rev-list -n 1 HEAD --branches --not --remotes --tags` plus `refs/stash`.

Amend: templates/global/agents/gitter.md#Phases — the Worktree remove rule proves "HEAD is on its remote"; it should prove no commit reachable from HEAD or a local branch is missing from every remote branch and tag (or from the main checkout's copy), and that no stash exists.

Resolved: 2026-10-11 — templates/global/agents/gitter.md and .claude/agents/gitter.md, Worktree remove: rev-list over HEAD and local branches against remotes and tags, plus the stash check

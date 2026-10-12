---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through /pcm); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: housekeeping
description: Clears stale worktrees, branches, stashes — `/housekeeping [repo]`, or asks like "are these worktrees stale", "clean up old branches", "housekeeping time". Returns a state and evidence row per item, what stayed and why, recovery lines.
argument-hint: "[repo]"
---

# Housekeeping

Repository: $ARGUMENTS — with none, the main checkout of the current one. This chat only reads and deletes the `UNLANDED.md` of a worktree removed (§ 4); every git write goes to `gitter`.

## 1. Inventory

One read-only shell call gathers:

- `{base}`: `develop` where it exists, else the remote default branch (`git symbolic-ref --short refs/remotes/origin/HEAD`)
- every linked worktree (`git worktree list --porcelain`): branch, tip, ahead and behind `{base}`, dirty count, last commit age and subject
- local branches `git branch --no-merged {base}`, and the remote branch of each
- `git stash list`, each entry with `git stash show --include-untracked --stat`
- every `$HOME/.local/state/pfm/flights/{project}/*/UNLANDED.md`: a flight that passed its gate and never landed; its worktree is WAITING, and a gone worktree makes the file stale, reported for deletion

`{base}`, the release branch and branches sharing no history with `{base}` (`gh-pages`-like) are not work branches: list them under left alone.

## 2. Classify

Every worktree and branch is one of:

- MERGED: its tip is an ancestor of `{base}` (`git merge-base --is-ancestor`).
- LANDED: a squash or rebase put its change on `{base}` under another hash. `git cherry` reports a squash as `+`, so a same-subject commit on `{base}` is the lead, never the proof. The proof: `git diff --stat {tip} {landing} -- {files the branch touched}` shows only what the landing refined, and every test the branch has and `{base}` lacks at that path is found on `{base}` by name (`git grep -n "{TestName}" {base}`). A test found nowhere makes it WAITING.
- WAITING: any other work not on `{base}`.

A worktree's dirty files are compared with `{base}` byte for byte (`git show {base}:{f} | cmp -s - {f}`), each one same, older (the diff shows `{base}` holding the newer lines) or new. A new hunk makes the worktree WAITING unless `{base}` covers its behaviour another way, named in one line.

## 3. Submodule guard

A worktree holding initialised submodules is removable only when, per submodule (`git -C {worktree} submodule foreach`): its status is clean or only `D` entries (a half-populated checkout holds no edits), and its HEAD is in the main checkout's copy (`git -C {repo}/{path} cat-file -e {sha}`) or on its remote (`git branch -r --contains {sha}`). A modification, an untracked file or a HEAD found nowhere makes it WAITING.

## 4. Remove through gitter

One `gitter` dispatch carries every local removal. Per item: the path, the branch, the tip to re-verify (a moved tip stops that item), the evidence line, and the command:

- the removal: `.claude/scripts/worktree.sh remove {name}` when the repository has that script (it frees the ports, tears the stack down and judges submodules itself), with `--discard` named only for dirty files classified same or older; without the script, `git worktree remove`, with `--force` named only for those files or submodules § 3 cleared
- `git branch -d` for MERGED; `git branch -D` for LANDED, its landing commit named
- `git worktree prune` last

Once the final `git worktree list` shows an item's worktree gone, this chat deletes the `UNLANDED.md` whose worktree line names it: a removed worktree leaves its flight landed or discarded, and the file lies outside the repository, so no `gitter` dispatch carries it.

Out of scope in that brief: pushes, remote branches, stashes, `{base}` and the main checkout's untracked files. A refusal `gitter` returns is traced to its cause; a cause the evidence clears goes back with that evidence, any other cause makes the item WAITING.

## 5. Remote branches and stashes

Both are listed for the user before anything happens: a remote branch with its tip matched to the removed local tip, a stash with its branch, message and `--stat`. Each runs only on the user's answer.

- A remote delete is a push: `gitter` gets it with the user's ask quoted from their current message. When `gitter` still refuses, give the user the line to run from their own prompt: `! git push origin --delete {branch} && git fetch --prune origin`.
- A dropped stash's report carries its recovery line, `git stash store -m '{message}' {sha}`, the sha taken from `gitter`'s `Dropped … ({sha})` output.

## 6. WAITING work

WAITING work stays. Report what it holds and what finishing it takes; when the user asked for the work to be done, dispatch `flights-foreman` with the branch, the worktree and what is left.

## Report

- a table: item · state · evidence (landing commit, file comparison, where moved tests live)
- what was left alone, and why
- recovery lines for dropped stashes
- the `UNLANDED.md` files deleted, one line each
- the final `git worktree list`, `git branch --list` and `git stash list`, read by this chat, never relayed from `gitter`

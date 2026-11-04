#!/usr/bin/env bash
# worktree.sh — this repo's flight worktrees.
#
# Interface, fixed: pfm's git-guard keys on this path (.claude/scripts/worktree.sh) and on the verb names
# below, and lets the main chat run create, land and remove only while git tracks this file unchanged from
# HEAD; pruning, discarding and every refusal go to gitter. Every refusal exits non-zero with one stderr
# line. Renaming or dropping a verb, or exiting 0 on a refusal, breaks the main chat's access. This block
# stays in sync with .claude/rules/git-writes-through-gitter.md; everything behind the verbs is free.
#
#   .claude/scripts/worktree.sh create <name> [base]           # base defaults to BASE_BRANCH
#   .claude/scripts/worktree.sh land <name> [-m <msg>]         # fast-forward BASE_BRANCH, then remove
#   .claude/scripts/worktree.sh remove <name> [--delete-branch]
#   .claude/scripts/worktree.sh list
#
# <name> is the kebab-case flight name: branch flight/<name>, path .worktrees/<name>.
# <name> is the kebab-case flight name: branch flight/<name>, path .worktrees/<name>.

set -euo pipefail

# The main checkout, also when this copy runs from inside a worktree.
ROOT="$(cd "$(git -C "$(dirname "$0")" rev-parse --path-format=absolute --git-common-dir)/.." && pwd)"
# The branch every flight starts from and lands on.
BASE_BRANCH="develop"

usage() {
  echo "Usage: $0 {create <name> [base]|land <name> [-m <msg>]|remove <name> [--delete-branch]|list}" >&2
  return 2
}

# init_from_main_copy <worktree> <name> <path>: populate one plugin from the main checkout's copy.
# A remote clone that landed but lacks the gitlink keeps its remote and fetches the copy's HEAD and
# branches; with no clone, the plugin clones from the copy, which hands over every object it holds.
init_from_main_copy() {
  if [[ -e "$1/$3/.git" ]]; then
    git -C "$1/$3" fetch -q "$ROOT/$3" HEAD 'refs/heads/*:refs/remotes/main-checkout/*' &&
      git -C "$1" submodule update -- "$3"
  else
    git -C "$1" -c protocol.file.allow=always -c "submodule.$2.url=$ROOT/$3" submodule update --init -- "$3"
  fi
}

cmd_create() {
  local name="$1" base="${2:-$BASE_BRANCH}"
  local branch="flight/${name}" worktree_dir="${ROOT}/.worktrees/${name}" sha key path sub
  if git -C "$ROOT" show-ref --verify -q "refs/heads/${branch}"; then
    echo "Error: branch $branch already exists; nothing created" >&2; return 1
  fi
  if [[ -e "$worktree_dir" || -L "$worktree_dir" ]]; then
    echo "Error: path $worktree_dir already exists; nothing created" >&2; return 1
  fi
  if ! sha="$(git -C "$ROOT" rev-parse --verify -q "${base}^{commit}")"; then
    echo "Error: base $base is not a commit; nothing created" >&2; return 1
  fi
  if ! git -C "$ROOT" worktree add -q -b "$branch" "$worktree_dir" "$sha"; then
    echo "Error: git worktree add failed (see git above): $worktree_dir" >&2; return 1
  fi
  # The fence mounts the worktree read-only and cannot clone, so every plugin is populated now. A
  # gitlink the plugin's remote does not hold yet fails to fetch: that one inits from the main
  # checkout's copy instead (init_from_main_copy).
  local -a failed=()
  if [ -f "$worktree_dir/.gitmodules" ]; then
    local entries
    if ! entries="$(git -C "$worktree_dir" config -f .gitmodules --get-regexp '^submodule\..*\.path$')"; then
      echo "Error: could not read .gitmodules; partial worktree: $worktree_dir" >&2; return 1
    fi
    while read -r key path; do
      sub="${key#submodule.}"; sub="${sub%.path}"
      git -C "$worktree_dir" submodule update --init -- "$path" ||
        init_from_main_copy "$worktree_dir" "$sub" "$path" ||
        failed+=("$path")
    done <<< "$entries"
  fi
  if (( ${#failed[@]} )); then
    local names
    printf -v names '%s, ' "${failed[@]}"
    echo "Error: submodule init failed for ${names%, }; partial worktree: $worktree_dir" >&2; return 1
  fi
  echo "Worktree ready: $worktree_dir"
  echo "Branch: $branch (from $base at ${sha:0:12})"
}

# A worktree's uncommitted changes, plugin content included.
land_status() {
  git -C "$1" status --porcelain --untracked-files=all --ignore-submodules=none
}

# Land a flight: commit its leftover work when -m names a message, fast-forward BASE_BRANCH in the
# main checkout to its branch, then remove the worktree and delete the branch. Anything but a
# fast-forward is refused, the branch keeping its commits: gitter syncs it (Phase MERGE).
cmd_land() {
  local name="$1"
  shift
  local msg=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
      -m)
        if [[ $# -lt 2 ]]; then echo "Error: -m needs a message; nothing landed" >&2; return 2; fi
        msg="$2"; shift ;;
      *) echo "Error: unknown land option: $1; nothing landed" >&2; return 2 ;;
    esac
    shift
  done
  local branch="flight/${name}" worktree_dir="${ROOT}/.worktrees/${name}"
  local top head status cur gitdir rc=0
  if [ ! -d "$worktree_dir" ] || ! top="$(git -C "$worktree_dir" rev-parse --show-toplevel 2>/dev/null)" \
    || [ "$top" != "$(cd "$worktree_dir" && pwd -P)" ]; then
    echo "Error: no worktree at $worktree_dir; nothing landed" >&2; return 1
  fi
  if ! head="$(git -C "$worktree_dir" symbolic-ref -q --short HEAD)" || [ "$head" != "$branch" ]; then
    echo "Error: $worktree_dir is on ${head:-a detached HEAD}, not its own branch $branch; nothing landed" >&2; return 1
  fi
  if git -C "$worktree_dir" rev-parse -q --verify MERGE_HEAD >/dev/null; then
    echo "Error: a merge is in progress in $worktree_dir (MERGE_HEAD); gitter concludes it (Phase MERGE); nothing landed" >&2
    return 1
  fi
  if ! status="$(land_status "$worktree_dir")"; then
    echo "Error: could not inspect $worktree_dir; nothing landed" >&2; return 1
  fi
  if [[ -n "$status" ]]; then
    if [[ -z "$msg" ]]; then
      echo "Error: $worktree_dir has uncommitted changes; pass -m <message> to commit them; nothing landed" >&2; return 1
    fi
    if ! git -C "$worktree_dir" add -A; then
      echo "Error: could not stage the changes in $worktree_dir; nothing landed" >&2; return 1
    fi
    if ! git -C "$worktree_dir" diff --cached --quiet && ! git -C "$worktree_dir" commit -q -m "$msg"; then
      echo "Error: commit failed in $worktree_dir (its changes stay staged); nothing landed" >&2; return 1
    fi
    # git add stages no edit inside a plugin: what is left is work the commit could not take.
    if ! status="$(land_status "$worktree_dir")"; then
      echo "Error: could not inspect $worktree_dir after the commit; nothing landed" >&2; return 1
    fi
    if [[ -n "$status" ]]; then
      echo "Error: $worktree_dir is still dirty after the commit (work inside a plugin?): ${status//$'\n'/, }; nothing landed" >&2
      return 1
    fi
  fi
  if ! cur="$(git -C "$ROOT" symbolic-ref -q --short HEAD)" || [ "$cur" != "$BASE_BRANCH" ]; then
    echo "Error: the main checkout is on ${cur:-a detached HEAD}, not $BASE_BRANCH; gitter syncs it (Phase MERGE); nothing landed" >&2
    return 1
  fi
  git -C "$ROOT" merge-base --is-ancestor "$BASE_BRANCH" "$branch" || rc=$?
  case "$rc" in
    0) ;;
    1) echo "Error: $BASE_BRANCH has commits $branch lacks; gitter syncs it (Phase MERGE); $branch keeps its commits; nothing landed" >&2
       return 1 ;;
    *) echo "Error: could not compare $BASE_BRANCH with $branch; nothing landed" >&2; return 1 ;;
  esac
  if ! git -C "$ROOT" merge --ff-only -q "$branch"; then
    echo "Error: fast-forward of $BASE_BRANCH to $branch refused (see git above); nothing landed" >&2; return 1
  fi
  echo "Landed: $branch on $BASE_BRANCH"
  if ! gitdir="$(git -C "$worktree_dir" rev-parse --absolute-git-dir)"; then
    echo "Error: $branch landed on $BASE_BRANCH, but its worktree's Git directory is unreadable; worktree kept: $worktree_dir" >&2
    return 1
  fi
  if [ -e "$gitdir/locked" ]; then
    echo "Worktree kept (locked): $worktree_dir — $(cat "$gitdir/locked")"
    echo "Branch kept: $branch"
    return 0
  fi
  if ! cmd_remove "$name" --delete-branch; then
    echo "Error: $branch landed on $BASE_BRANCH, but closing its worktree failed (see above)" >&2; return 1
  fi
}

# Remove a clean worktree. --force is passed only once the worktree is proven clean and every plugin
# in it holds no work its private Git directory alone keeps: its status clean or only staged
# deletions (a half-populated checkout), no stash, and every local commit on a remote branch, a tag
# or the main checkout's copy.
cmd_remove() {
  local name="$1"
  shift
  local delete_branch="" status unsafe
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --delete-branch) delete_branch=1 ;;
      *) echo "Error: unknown removal option: $1 (discarding is gitter's); nothing removed" >&2; return 2 ;;
    esac
    shift
  done
  local branch="flight/${name}" worktree_dir="${ROOT}/.worktrees/${name}" gitdir
  if [ -d "$worktree_dir" ]; then
    if ! gitdir="$(git -C "$worktree_dir" rev-parse --absolute-git-dir)"; then
      echo "Error: could not locate the Git directory of $worktree_dir; nothing removed" >&2; return 1
    fi
    if [ -e "$gitdir/locked" ]; then
      echo "Error: worktree is locked ($(cat "$gitdir/locked")); its keeper unlocks it; nothing removed: $worktree_dir" >&2
      return 1
    fi
    if ! status="$(git -C "$worktree_dir" status --porcelain --untracked-files=all)"; then
      echo "Error: could not inspect worktree; nothing removed: $worktree_dir" >&2; return 1
    fi
    if [[ -n "$status" ]]; then
      echo "Error: dirty worktree refused; gitter decides on discarding it; nothing removed: $worktree_dir" >&2; return 1
    fi
    # shellcheck disable=SC2016 # expanded by each plugin's shell, not this one
    if ! unsafe="$(env MAIN_CHECKOUT="$ROOT" git -C "$worktree_dir" submodule foreach --quiet --recursive '
        s="$(git status --porcelain --untracked-files=all)" || exit 1
        if [ -n "$(printf "%s\n" "$s" | grep -v "^D  " | grep -v "^\$")" ]; then echo "$displaypath (uncommitted)"; fi
        if git rev-parse -q --verify refs/stash >/dev/null; then echo "$displaypath (stash)"; fi
        c="$(git rev-list -n 1 HEAD --branches --not --remotes --tags)" || exit 1
        if [ -n "$c" ] && ! git -C "$MAIN_CHECKOUT/$displaypath" cat-file -e "$c^{commit}" 2>/dev/null; then
          echo "$displaypath (commit $c on no remote)"
        fi')"; then
      echo "Error: could not inspect plugin state; nothing removed: $worktree_dir" >&2; return 1
    fi
    if [[ -n "$unsafe" ]]; then
      echo "Error: plugin work only this worktree holds: ${unsafe//$'\n'/, }; nothing removed: $worktree_dir" >&2; return 1
    fi
    if ! git -C "$ROOT" worktree remove --force "$worktree_dir"; then
      echo "Error: worktree removal failed; nothing removed: $worktree_dir" >&2; return 1
    fi
    echo "Worktree removed: $worktree_dir"
  else
    echo "No worktree found: $worktree_dir"
  fi
  if [ -z "$delete_branch" ]; then
    echo "Branch kept: $branch (pass --delete-branch to delete)"
  elif git -C "$ROOT" branch -q -d "$branch"; then
    echo "Branch deleted: $branch"
  else
    echo "Error: branch deletion refused (unmerged?), branch kept: $branch" >&2; return 1
  fi
}

cmd_list() {
  git -C "$ROOT" worktree list
}

# A name never leaves .worktrees/ nor spells an option.
if [[ $# -ge 2 && ! "$2" =~ ^[a-z0-9][a-z0-9._-]*$ ]]; then
  echo "Error: invalid flight name: $2 (kebab-case); nothing done" >&2; exit 2
fi
case "${1:-}" in
  create) [[ $# -ge 2 && $# -le 3 ]] || usage; cmd_create "$2" "${3:-}" ;;
  land)   [[ $# -ge 2 ]] || usage; cmd_land "$2" "${@:3}" ;;
  remove) [[ $# -ge 2 ]] || usage; cmd_remove "$2" "${@:3}" ;;
  list)   cmd_list ;;
  *)      usage ;;
esac

#!/usr/bin/env bash
# This repo's own worktree script: create populates every plugin (from the main checkout's copy when
# the plugin's remote lacks the gitlink), land only fast-forwards develop, remove keeps plugin work.
set -uo pipefail
SOURCE_ROOT="${SOURCE_ROOT:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)}"
SUT="${1:-$SOURCE_ROOT/.claude/scripts/worktree.sh}"
SHTEST_TAG=worktree-local
source "$SOURCE_ROOT/scripts/shtest.sh"
shtest_isolate_host
export GIT_AUTHOR_NAME=fixture GIT_AUTHOR_EMAIL=fixture@example.invalid GIT_COMMITTER_NAME=fixture GIT_COMMITTER_EMAIL=fixture@example.invalid
export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=protocol.file.allow GIT_CONFIG_VALUE_0=always
g() { /usr/bin/git -C "$1" -c commit.gpgsign=false "${@:2}"; }
remote="$T/plugin-remote"; repo="$T/repo space"
g "$T" init -q -b develop "$remote" && printf 'v1\n' > "$remote/lib.txt" && g "$remote" add lib.txt && g "$remote" commit -q -m v1 || exit 2
g "$T" init -q -b develop "$repo" && printf 'base\n' > "$repo/base.txt" || exit 2
mkdir -p "$repo/.claude/scripts" && cp "$SUT" "$repo/.claude/scripts/worktree.sh" || exit 2
printf '.worktrees/\n' > "$repo/.gitignore"
g "$repo" submodule add -q "$remote" plugins/p && g "$repo" config -f .gitmodules submodule.plugins/p.ignore dirty || exit 2
g "$repo" add -A && g "$repo" commit -q -m base || exit 2
invoke() { (cd "$repo" && bash .claude/scripts/worktree.sh "$@") > "$T/out" 2>&1; rc=$?; out="$(cat "$T/out")"; }
has_branch() { g "$repo" show-ref --verify --quiet "refs/heads/flight/$1"; }
# commit_in <name>: one committed change on the flight's branch; tip is its commit.
commit_in() { printf '%s\n' "$1" > "$repo/.worktrees/$1/work.txt" && g "$repo/.worktrees/$1" add work.txt && g "$repo/.worktrees/$1" commit -q -m "$1" && tip="$(g "$repo/.worktrees/$1" rev-parse HEAD)" || exit 2; }

invoke create clean
w="$repo/.worktrees/clean"
if [[ "$rc" -eq 0 && "$(g "$w" symbolic-ref --short HEAD)" == flight/clean && "$(cat "$w/plugins/p/lib.txt" 2>&1)" == v1 ]]; then
  ok 'create: a flight worktree on flight/<name> from develop, its plugin populated'
else bad 'create: a flight worktree on flight/<name> from develop, its plugin populated' "rc=$rc; out=$out"; fi
invoke create clean
if [[ "$rc" -ne 0 && "$out" == 'Error: branch flight/clean already exists; nothing created' ]]; then
  ok 'create: an existing branch refuses in one line'
else bad 'create: an existing branch refuses in one line' "rc=$rc; out=$out"; fi
commit_in clean
invoke land clean
if [[ "$rc" -eq 0 && "$(g "$repo" rev-parse develop)" == "$tip" && ! -d "$w" && "$out" == *'Landed: flight/clean on develop'* ]] && ! has_branch clean; then
  ok 'land: a clean branch fast-forwards develop, then the worktree and branch are removed'
else bad 'land: a clean branch fast-forwards develop, then the worktree and branch are removed' "rc=$rc; out=$out"; fi

# A gitlink the plugin's remote does not hold: only the main checkout's copy has the commit.
printf 'v2\n' > "$repo/plugins/p/lib.txt" && g "$repo/plugins/p" commit -q -am v2 && g "$repo" commit -q -am 'bump p' || exit 2
local_sha="$(g "$repo/plugins/p" rev-parse HEAD)"
invoke create fallback
if [[ "$rc" -eq 0 && "$(g "$repo/.worktrees/fallback/plugins/p" rev-parse HEAD 2>&1)" == "$local_sha" ]]; then
  ok "create: a plugin whose remote lacks the gitlink inits from the main checkout's copy"
else bad "create: a plugin whose remote lacks the gitlink inits from the main checkout's copy" "rc=$rc; out=$out"; fi
mv "$remote" "$remote.gone" || exit 2
invoke create offline
mv "$remote.gone" "$remote" || exit 2
if [[ "$rc" -eq 0 && "$(g "$repo/.worktrees/offline/plugins/p" rev-parse HEAD 2>&1)" == "$local_sha" ]]; then
  ok "create: a plugin whose remote cannot be cloned at all clones from the main checkout's copy"
else bad "create: a plugin whose remote cannot be cloned at all clones from the main checkout's copy" "rc=$rc; out=$out"; fi

invoke create dirty
w="$repo/.worktrees/dirty"; before="$(g "$repo" rev-parse develop)"
printf 'uncommitted\n' > "$w/base.txt"
invoke land dirty
if [[ "$rc" -ne 0 && "$(cat "$w/base.txt")" == uncommitted && "$(g "$repo" rev-parse develop)" == "$before" && "$out" == "Error: $w has uncommitted changes; pass -m <message> to commit them; nothing landed" ]]; then
  ok 'land: a dirty worktree without -m refuses in one line, landing nothing'
else bad 'land: a dirty worktree without -m refuses in one line, landing nothing' "rc=$rc; out=$out"; fi
printf 'plugin edit\n' > "$w/plugins/p/lib.txt"
invoke land dirty -m 'leftover'
if [[ "$rc" -ne 0 && "$(g "$w" log -1 --format=%s)" == leftover && "$(g "$repo" rev-parse develop)" == "$before" && "$out" == *'still dirty after the commit'*'nothing landed' && "$(cat "$w/plugins/p/lib.txt")" == 'plugin edit' ]]; then
  ok 'land: -m commits what it can, then an edit inside a plugin (ignore = dirty) still refuses, landing nothing'
else bad 'land: -m commits what it can, then an edit inside a plugin (ignore = dirty) still refuses, landing nothing' "rc=$rc; out=$out"; fi
g "$w/plugins/p" checkout -q -- lib.txt || exit 2
invoke land dirty -m 'unused'
if [[ "$rc" -eq 0 && "$(g "$repo" log -1 --format=%s develop)" == leftover && "$(g "$repo" show develop:base.txt)" == uncommitted && ! -d "$w" ]]; then
  ok 'land: the committed leftover lands once the plugin is clean'
else bad 'land: the committed leftover lands once the plugin is clean' "rc=$rc; out=$out"; fi

invoke create moved
commit_in moved
printf 'moved\n' > "$repo/moved.txt" && g "$repo" add moved.txt && g "$repo" commit -q -m moved || exit 2
moved="$(g "$repo" rev-parse develop)"
invoke land moved
if [[ "$rc" -ne 0 && "$(g "$repo" rev-parse develop)" == "$moved" && "$(g "$repo" rev-parse flight/moved)" == "$tip" && -d "$repo/.worktrees/moved" && "$out" == 'Error: develop has commits flight/moved lacks; gitter syncs it (Phase MERGE); flight/moved keeps its commits; nothing landed' ]]; then
  ok 'land: a develop that moved refuses in one line, keeping the branch, its commit and the worktree'
else bad 'land: a develop that moved refuses in one line, keeping the branch, its commit and the worktree' "rc=$rc; out=$out"; fi

invoke create locked
commit_in locked
g "$repo" worktree lock --reason 'gate running' "$repo/.worktrees/locked" || exit 2
invoke land locked
if [[ "$rc" -eq 0 && "$(g "$repo" rev-parse develop)" == "$tip" && -d "$repo/.worktrees/locked" && "$out" == *"Worktree kept (locked): $repo/.worktrees/locked — gate running"* ]] && has_branch locked; then
  ok 'land: a locked worktree lands and is kept with its branch'
else bad 'land: a locked worktree lands and is kept with its branch' "rc=$rc; out=$out"; fi

invoke create unpushed
w="$repo/.worktrees/unpushed"
# A commit on a plugin branch with HEAD back on the gitlink: status is clean, the commit lives only here.
recorded="$(g "$w/plugins/p" rev-parse HEAD)" || exit 2
g "$w/plugins/p" switch -q -c feature && printf 'local only\n' > "$w/plugins/p/lib.txt" && g "$w/plugins/p" commit -q -am local || exit 2
g "$w/plugins/p" checkout -q --detach "$recorded" || exit 2
invoke remove unpushed --delete-branch
if [[ "$rc" -ne 0 && -d "$w/plugins/p" && "$out" == *'plugin work only this worktree holds'*'nothing removed'* ]] && has_branch unpushed; then
  ok 'remove: a plugin commit on no remote and absent from the main copy refuses removal'
else bad 'remove: a plugin commit on no remote and absent from the main copy refuses removal' "rc=$rc; out=$out"; fi
invoke remove fallback --delete-branch
if [[ "$rc" -eq 0 && ! -d "$repo/.worktrees/fallback" && "$out" == *'Branch deleted: flight/fallback'* ]]; then
  ok "remove: a plugin commit the main checkout's copy holds is removed with --force, branch deleted"
else bad "remove: a plugin commit the main checkout's copy holds is removed with --force, branch deleted" "rc=$rc; out=$out"; fi
invoke remove unpushed --discard
if [[ "$rc" -eq 2 && -d "$w" && "$out" == 'Error: unknown removal option: --discard (discarding is gitter'"'"'s); nothing removed' ]]; then
  ok 'remove: --discard is refused, discarding stays gitter'"'"'s'
else bad 'remove: --discard is refused, discarding stays gitter'"'"'s' "rc=$rc; out=$out"; fi
# The fixed interface: the header, the case dispatch and the rule name one verb set (git-guard keys on it).
RULE="${2:-$SOURCE_ROOT/.claude/rules/git-writes-through-gitter.md}"
source "$SOURCE_ROOT/scripts/worktree-verbs.sh"
worktree_verbs_agree "$SUT" "$RULE"; agree=$?
if [[ "$agree" -eq 0 && "$WORKTREE_VERBS_REPORT" == "header($SUT)={create land list remove } case={create land list remove } rule($RULE)={create land list remove }" ]]; then
  ok 'interface: the header, the case dispatch and the rule name the same verbs'
else bad 'interface: the header, the case dispatch and the rule name the same verbs' "rc=$agree; $WORKTREE_VERBS_REPORT"; fi
sed 's/^  list)/  ls)/' "$SUT" > "$T/drifted.sh" || exit 2
worktree_verbs_agree "$T/drifted.sh" "$RULE"; agree=$?
if [[ "$agree" -eq 1 && "$WORKTREE_VERBS_REPORT" == "header($T/drifted.sh)={create land list remove } case={create land ls remove } rule($RULE)={create land list remove }" ]]; then
  ok 'interface: a dispatch verb the header and the rule do not name is a mismatch'
else bad 'interface: a dispatch verb the header and the rule do not name is a mismatch' "rc=$agree; $WORKTREE_VERBS_REPORT"; fi
shtest_end

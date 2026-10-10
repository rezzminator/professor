#!/usr/bin/env bash
set -uo pipefail
SOURCE_ROOT="${SOURCE_ROOT:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)}"
SUT="${1:-$SOURCE_ROOT/templates/project/scripts/worktree.sh}"
SHTEST_TAG=worktree-safety
source "$SOURCE_ROOT/scripts/shtest.sh"
shtest_isolate_host
mkdir -p "$T/bin"
cat > "$T/bin/git" <<'EOF'
#!/usr/bin/env bash
if [[ "$*" == *'worktree remove'* && "${FAIL_REMOVE:-0}" == 1 ]]; then echo 'synthetic removal failure' >&2; exit 3; fi
if [[ "$*" == *'worktree list'* && "${FAIL_LIST:-0}" == 1 ]]; then echo 'synthetic enumeration failure' >&2; exit 3; fi
if [[ "$*" == *'submodule update'* && "${FAIL_SUBMODULE:-0}" == 1 ]]; then echo 'synthetic submodule failure' >&2; exit 3; fi
exec /usr/bin/git "$@"
EOF
chmod +x "$T/bin/git"
# lifecycle <install case>: the script under test with the roster the case names: 0 none,
# 1 an install that fails, 2 a multi-command install whose last command succeeds after a failure.
lifecycle() {
  python3 -I - "$SUT" "$repo/.runner/scripts/lifecycle.sh" "$1" "${NUKE_CASE:-0}" <<'PY'
from pathlib import Path
import sys
infra='.' if sys.argv[4] == '1' else '-'
source=Path(sys.argv[1]).read_text().replace('INFRA_MAKE_DIR="{project}"', 'INFRA_MAKE_DIR="' + infra + '"')
install={'1': 'false', '2': 'false; printf finished'}.get(sys.argv[3])
if install:
    source=source.replace('PROJECTS=(\n', 'PROJECTS=(\n  ".|install|' + install + '|-"\n')
Path(sys.argv[2]).write_text(source)
PY
}
seq=0
fixture() {
  seq=$((seq + 1)); repo="$T/repo space $seq"
  mkdir -p "$repo/.runner/scripts" "$repo/.claude/scripts"
  /usr/bin/git init -q -b main "$repo" || exit 2
  printf 'base\n' > "$repo/base.txt"
  /usr/bin/git -C "$repo" add base.txt
  /usr/bin/git -C "$repo" -c user.name=fixture -c user.email=fixture@example.invalid -c commit.gpgsign=false commit -q -m base || exit 2
  lifecycle "${INSTALL_CASE:-0}"
  cat > "$repo/.claude/scripts/alloc-ports.sh" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "${ALLOC_TRACE:?}"
if [[ "$1" == free && "${FAIL_FREE:-0}" == 1 ]]; then echo 'allocation-release-failed' >&2; exit 3; fi
if [[ "$1" == alloc ]]; then echo 'FIXTURE_PORT=12345'; fi
EOF
  chmod +x "$repo/.claude/scripts/alloc-ports.sh"
  trace="$repo/alloc-trace"
}
invoke() {
  (cd "$repo" && env PATH="$T/bin:$PATH" ALLOC_TRACE="$trace" FAIL_REMOVE="${FAIL_REMOVE:-0}" FAIL_LIST="${FAIL_LIST:-0}" FAIL_SUBMODULE="${FAIL_SUBMODULE:-0}" FAIL_FREE="${FAIL_FREE:-0}" bash .runner/scripts/lifecycle.sh "$@") > "$T/out" 2>&1
  rc=$?; out="$(cat "$T/out")"
}
add_worktree() { /usr/bin/git -C "$repo" worktree add -q -b "pipeline/$1" "$repo/.worktrees/$1" || exit 2; }
fixture; add_worktree dirty
printf 'uncommitted\n' > "$repo/.worktrees/dirty/base.txt"
invoke remove dirty
if [[ "$rc" -ne 0 && -f "$repo/.worktrees/dirty/base.txt" && "$out" != *'Worktree removed'* && ! -f "$trace" ]]; then
  ok 'F1: dirty removal preserves changes and performs no teardown'
else bad 'F1: dirty removal preserves changes and performs no teardown' "rc=$rc; out=$out"; fi
fixture; add_worktree failed
FAIL_REMOVE=1 invoke remove failed --delete-branch
if [[ "$rc" -ne 0 && -d "$repo/.worktrees/failed" && ! -f "$trace" && "$out" != *'Worktree removed'* ]] && /usr/bin/git -C "$repo" show-ref --verify --quiet refs/heads/pipeline/failed; then
  ok 'F1: removal error is propagated before branch or port teardown'
else bad 'F1: removal error is propagated before branch or port teardown' "rc=$rc; out=$out"; fi
fixture; add_worktree discard
printf 'uncommitted\n' > "$repo/.worktrees/discard/base.txt"
invoke remove discard --delete-branch --discard
if [[ "$rc" -eq 0 && ! -d "$repo/.worktrees/discard" && "$out" == *'Pipeline cleaned up'* ]] && grep -qx 'free discard' "$trace" && ! /usr/bin/git -C "$repo" show-ref --verify --quiet refs/heads/pipeline/discard; then
  ok 'F1: explicit discard accepts both options and completes cleanup'
else bad 'F1: explicit discard accepts both options and completes cleanup' "rc=$rc; out=$out"; fi
fixture
/usr/bin/git -C "$repo" worktree add -q -b live "$repo/.worktrees/live space" || exit 2
mkdir -p "$repo/.worktrees/orphan" "$repo/.worktrees/foreign" "$repo/.worktrees/dangling"
printf 'valuable\n' > "$repo/.worktrees/live space/base.txt"
printf 'unknown git registration\n' > "$repo/.worktrees/foreign/.git"
ln -s "$repo/missing-git-dir" "$repo/.worktrees/dangling/.git"
printf 'valuable\n' > "$repo/.worktrees/dangling/keep"
invoke prune
if [[ "$rc" -eq 0 && -f "$repo/.worktrees/live space/base.txt" && -f "$repo/.worktrees/foreign/.git" && -f "$repo/.worktrees/dangling/keep" && -L "$repo/.worktrees/dangling/.git" && ! -d "$repo/.worktrees/orphan" && "$out" == *'REGISTERED-NO-DOCS: live space'* ]]; then
  ok 'F9: NUL porcelain preserves registered paths with spaces, unknown Git checkouts and dangling Git metadata'
else bad 'F9: NUL porcelain preserves registered paths with spaces, unknown Git checkouts and dangling Git metadata' "rc=$rc; out=$out"; fi
fixture; mkdir -p "$repo/.worktrees/orphan"
FAIL_FREE=1 invoke prune
if [[ "$rc" -ne 0 && -d "$repo/.worktrees/orphan" && "$out" == *'port allocation release failed for orphan'* ]]; then
  ok 'F9: failed port release keeps the directory and fails visibly'
else bad 'F9: failed port release keeps the directory and fails visibly' "rc=$rc; out=$out"; fi
invoke prune
if [[ "$rc" -eq 0 && ! -d "$repo/.worktrees/orphan" && "$out" == *'PRUNED: orphan'* ]] && grep -qx 'free orphan' "$trace"; then
  ok 'F9: the next prune retries the kept directory and releases its ports'
else bad 'F9: the next prune retries the kept directory and releases its ports' "rc=$rc; out=$out"; fi
fixture; mkdir -p "$repo/.worktrees/orphan"
FAIL_LIST=1 invoke prune
if [[ "$rc" -ne 0 && -d "$repo/.worktrees/orphan" && ! -f "$trace" && "$out" == *'enumerat'* ]]; then
  ok 'F9: failed registration enumeration deletes nothing and fails visibly'
else bad 'F9: failed registration enumeration deletes nothing and fails visibly' "rc=$rc; out=$out"; fi
fixture
FAIL_SUBMODULE=1 invoke create submodule-failed
if [[ "$rc" -ne 0 && -d "$repo/.worktrees/submodule-failed" && "$out" == *'partial worktree:'* && "$out" != *'Worktree ready:'* && ! -f "$trace" ]]; then
  ok 'F16: failed submodule setup names partial worktree and never claims readiness'
else bad 'F16: failed submodule setup names partial worktree and never claims readiness' "rc=$rc; out=$out"; fi
INSTALL_CASE=1 fixture
invoke create install-failed
if [[ "$rc" -ne 0 && -d "$repo/.worktrees/install-failed" && "$out" == *'partial worktree:'* && "$out" != *'Worktree ready:'* && ! -f "$trace" ]]; then
  ok 'F16: failed dependency installation names partial worktree and never claims readiness'
else bad 'F16: failed dependency installation names partial worktree and never claims readiness' "rc=$rc; out=$out"; fi
INSTALL_CASE=2 fixture
invoke create multi-command
if [[ "$rc" -ne 0 && -d "$repo/.worktrees/multi-command" && "$out" == *'partial worktree:'* && "$out" != *'Worktree ready:'* && ! -f "$trace" ]]; then
  ok 'F16: a failing command followed by a succeeding one fails dependency installation'
else bad 'F16: a failing command followed by a succeeding one fails dependency installation' "rc=$rc; out=$out"; fi
INSTALL_CASE=1 fixture
invoke create partial-retry
[[ "$rc" -ne 0 && -d "$repo/.worktrees/partial-retry" ]] || { echo 'test-worktree: setup failure: the first create did not leave a partial worktree' >&2; exit 2; }
invoke create partial-retry
if [[ "$rc" -ne 0 && "$out" != *'Worktree ready:'* && "$out" != *'already exists'* && -d "$repo/.worktrees/partial-retry" && ! -f "$trace" ]]; then
  ok 'F16: a retry whose installation still fails reports failure, never an existing worktree'
else bad 'F16: a retry whose installation still fails reports failure, never an existing worktree' "rc=$rc; out=$out"; fi
lifecycle 0
invoke create partial-retry
if [[ "$rc" -eq 0 && "$out" == *'Worktree ready:'* ]] && grep -qx 'alloc partial-retry' "$trace"; then
  ok 'F16: a retry with the cause fixed completes provisioning and reports ready'
else bad 'F16: a retry with the cause fixed completes provisioning and reports ready' "rc=$rc; out=$out"; fi
invoke create partial-retry
if [[ "$rc" -eq 0 && "$out" == *'Worktree already exists:'* && "$out" != *'Resuming'* ]]; then
  ok 'F16: a provisioned worktree is reported as existing'
else bad 'F16: a provisioned worktree is reported as existing' "rc=$rc; out=$out"; fi
NUKE_CASE=1 fixture; add_worktree teardown
invoke remove teardown
if [[ "$rc" -ne 0 && ! -d "$repo/.worktrees/teardown" && "$out" == *'teardown failed'* && "$out" != *'Pipeline cleaned up'* ]]; then
  ok 'F1: teardown failure after successful removal is propagated, never reported as cleaned'
else bad 'F1: teardown failure after successful removal is propagated, never reported as cleaned' "rc=$rc; out=$out"; fi
# A repo with a submodule: create populates it, so a clean worktree holds an initialized submodule
# (`ignore = dirty`, as this repo's plugins carry it, hides the submodule's own edits from plain status).
submodule_fixture() {
  fixture
  local sub="$T/sub $seq"
  /usr/bin/git init -q -b main "$sub" || exit 2
  printf 'vendored\n' > "$sub/lib.txt"
  /usr/bin/git -C "$sub" add lib.txt
  /usr/bin/git -C "$sub" -c user.name=fixture -c user.email=fixture@example.invalid -c commit.gpgsign=false commit -q -m lib || exit 2
  /usr/bin/git -C "$repo" submodule add -q "$sub" vendor || exit 2
  /usr/bin/git -C "$repo" config -f .gitmodules submodule.vendor.ignore dirty || exit 2
  /usr/bin/git -C "$repo" add .gitmodules
  /usr/bin/git -C "$repo" -c user.name=fixture -c user.email=fixture@example.invalid -c commit.gpgsign=false commit -q -m vendor || exit 2
  invoke create "$1"
  [[ "$rc" -eq 0 && -f "$repo/.worktrees/$1/vendor/lib.txt" ]] || { echo "test-worktree: setup failure: create did not populate the submodule: $out" >&2; exit 2; }
}
export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=protocol.file.allow GIT_CONFIG_VALUE_0=always
submodule_fixture clean-sub
invoke remove clean-sub
if [[ "$rc" -eq 0 && ! -d "$repo/.worktrees/clean-sub" && "$out" == *'Worktree removed'* ]] && grep -qx 'free clean-sub' "$trace"; then
  ok 'F1: a clean script-created worktree (its own .env.ports, an initialized submodule) is removed without --discard'
else bad 'F1: a clean script-created worktree (its own .env.ports, an initialized submodule) is removed without --discard' "rc=$rc; out=$out"; fi
submodule_fixture dirty-sub
printf 'uncommitted\n' > "$repo/.worktrees/dirty-sub/vendor/lib.txt"
invoke remove dirty-sub
if [[ "$rc" -ne 0 && "$(cat "$repo/.worktrees/dirty-sub/vendor/lib.txt")" == uncommitted && "$out" != *'Worktree removed'* ]] && ! grep -qx 'free dirty-sub' "$trace"; then
  ok 'F1: an edit inside a submodule ignored as dirty still refuses removal without --discard'
else bad 'F1: an edit inside a submodule ignored as dirty still refuses removal without --discard' "rc=$rc; out=$out"; fi
submodule_fixture unpushed-sub
w="$repo/.worktrees/unpushed-sub"
printf 'local only\n' > "$w/vendor/lib.txt"
/usr/bin/git -C "$w/vendor" -c user.name=fixture -c user.email=fixture@example.invalid -c commit.gpgsign=false commit -q -am local || exit 2
/usr/bin/git -C "$w" add vendor || exit 2
/usr/bin/git -C "$w" -c user.name=fixture -c user.email=fixture@example.invalid -c commit.gpgsign=false commit -q -m bump || exit 2
invoke remove unpushed-sub
if [[ "$rc" -ne 0 && -d "$w/vendor" && "$out" == *'on no remote branch'* && "$out" != *'Worktree removed'* ]] && ! grep -qx 'free unpushed-sub' "$trace"; then
  ok 'F1: a submodule commit on no remote branch refuses removal without --discard (its objects go with the worktree)'
else bad 'F1: a submodule commit on no remote branch refuses removal without --discard (its objects go with the worktree)' "rc=$rc; out=$out"; fi
sgit() { /usr/bin/git -C "$1" -c user.name=fixture -c user.email=fixture@example.invalid -c commit.gpgsign=false "${@:2}"; }
# A commit on a local submodule branch whose HEAD went back to the recorded commit: status is clean,
# yet the branch's objects live only in this worktree's private Git directory.
submodule_fixture branch-sub
w="$repo/.worktrees/branch-sub"
recorded="$(sgit "$w/vendor" rev-parse HEAD)"
sgit "$w/vendor" switch -q -c feature && printf 'branch only\n' > "$w/vendor/lib.txt" && sgit "$w/vendor" commit -q -am feature || exit 2
sgit "$w/vendor" checkout -q --detach "$recorded" || exit 2
invoke remove branch-sub
if [[ "$rc" -ne 0 && "$out" == *'on no remote branch'* && "$out" != *'Worktree removed'* ]] && sgit "$w/vendor" rev-parse -q --verify feature >/dev/null; then
  ok 'F1: a submodule branch commit on no remote refuses removal even with HEAD back on the recorded commit'
else bad 'F1: a submodule branch commit on no remote refuses removal even with HEAD back on the recorded commit' "rc=$rc; out=$out"; fi
submodule_fixture stash-sub
w="$repo/.worktrees/stash-sub"
printf 'stashed\n' > "$w/vendor/lib.txt" && sgit "$w/vendor" stash -q || exit 2
invoke remove stash-sub
if [[ "$rc" -ne 0 && "$out" != *'Worktree removed'* ]] && sgit "$w/vendor" rev-parse -q --verify refs/stash >/dev/null; then
  ok 'F1: a stash inside a submodule refuses removal without --discard'
else bad 'F1: a stash inside a submodule refuses removal without --discard' "rc=$rc; out=$out"; fi
# A submodule pinned to a release tag whose branch is gone: the remote holds the commit.
submodule_fixture tag-sub
w="$repo/.worktrees/tag-sub"; sub="$T/sub $seq"
sgit "$sub" switch -q -c release && printf 'released\n' > "$sub/lib.txt" && sgit "$sub" commit -q -am release && sgit "$sub" tag v1 || exit 2
sgit "$sub" switch -q main && sgit "$sub" branch -q -D release || exit 2
sgit "$w/vendor" fetch -q --tags origin && sgit "$w/vendor" checkout -q --detach v1 || exit 2
sgit "$w" add vendor && sgit "$w" commit -q -m pin || exit 2
invoke remove tag-sub
if [[ "$rc" -eq 0 && ! -d "$w" && "$out" == *'Worktree removed'* ]]; then
  ok 'F1: a submodule pinned to a remote tag on no branch is removed without --discard'
else bad 'F1: a submodule pinned to a remote tag on no branch is removed without --discard' "rc=$rc; out=$out"; fi
unset GIT_CONFIG_COUNT GIT_CONFIG_KEY_0 GIT_CONFIG_VALUE_0
shtest_end

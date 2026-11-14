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
if [[ "$*" == *'hash-object'* && "${FAIL_HASH:-0}" == 1 ]]; then echo 'synthetic content hash failure' >&2; exit 3; fi
exec /usr/bin/git "$@"
EOF
chmod +x "$T/bin/git"
# lifecycle <install case>: the script under test with the roster the case names: 0 none,
# 1 an install that fails, 2 a multi-command install whose last command succeeds after a failure;
# ENV_FILES (default none) names the env files the roster entry copies from main into a worktree.
lifecycle() {
  python3 -I - "$SUT" "$repo/.runner/scripts/lifecycle.sh" "$1" "${NUKE_CASE:-0}" "${ENV_FILES:--}" "${REWRITE_ENV:-1}" <<'PY'
from pathlib import Path
import sys
infra='.' if sys.argv[4] == '1' else '-'
source=Path(sys.argv[1]).read_text().replace('INFRA_MAKE_DIR="{project}"', 'INFRA_MAKE_DIR="' + infra + '"')
install={'1': 'false', '2': 'false; printf finished'}.get(sys.argv[3], '')
if install or sys.argv[5] != '-':
    source=source.replace('PROJECTS=(\n', 'PROJECTS=(\n  ".|install|' + install + '|' + sys.argv[5] + '"\n')
if sys.argv[5] == '.env.test' and sys.argv[6] == '1':
    source=source.replace('  # {ENV_FILE_PROVISION} — SETUP fills this from the roster\'s env_files + port map.',
                          '  printf \'PORT=%s\\n\' "$FIXTURE_PORT" > "$worktree_dir/.env.test"')
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
  (cd "$repo" && env PATH="$T/bin:$PATH" ALLOC_TRACE="$trace" FAIL_REMOVE="${FAIL_REMOVE:-0}" FAIL_LIST="${FAIL_LIST:-0}" FAIL_SUBMODULE="${FAIL_SUBMODULE:-0}" FAIL_FREE="${FAIL_FREE:-0}" FAIL_HASH="${FAIL_HASH:-0}" bash .runner/scripts/lifecycle.sh "$@") > "$T/out" 2>&1
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
# A Makefile owning the stack teardown: it records each pipeline it nukes in nuke-trace and fails for a pipeline
# whose name ends in -nukefails.
nuke_makefile() {
  printf 'nuke-test-pipeline:\n\t@echo $(PIPELINE) >> nuke-trace\n\t@case "$(PIPELINE)" in *-nukefails) exit 1;; esac\n' > "$repo/Makefile"
}
fgit() { /usr/bin/git -C "$1" -c user.name=fixture -c user.email=fixture@example.invalid -c commit.gpgsign=false "${@:2}"; }
NUKE_CASE=1 fixture; nuke_makefile; add_worktree unmerged
fgit "$repo/.worktrees/unmerged" commit -q --allow-empty -m work || exit 2
invoke remove unmerged --delete-branch
if [[ "$rc" -ne 0 && ! -d "$repo/.worktrees/unmerged" && "$out" == *'branch deletion failed'* && "$out" != *'Pipeline cleaned up'* ]] && grep -qx 'free unmerged' "$trace" && grep -qx unmerged "$repo/nuke-trace" && /usr/bin/git -C "$repo" show-ref --verify --quiet refs/heads/pipeline/unmerged; then
  ok 'F4: a refused branch deletion still frees the ports and tears the stack down, and fails visibly'
else bad 'F4: a refused branch deletion still frees the ports and tears the stack down, and fails visibly' "rc=$rc; out=$out; nuke-trace=$(cat "$repo/nuke-trace" 2>&1)"; fi
NUKE_CASE=1 fixture; nuke_makefile; mkdir -p "$repo/.worktrees/a-nukefails" "$repo/.worktrees/b-orphan"
invoke prune
if [[ "$rc" -ne 0 && ! -d "$repo/.worktrees/a-nukefails" && ! -d "$repo/.worktrees/b-orphan" && "$out" == *'Prune complete: 2 orphaned'* && "$out" == *'teardown failed for 1 pruned worktree(s): a-nukefails'* ]] && grep -qx a-nukefails "$repo/nuke-trace" && grep -qx b-orphan "$repo/nuke-trace" && grep -qx 'free b-orphan' "$trace"; then
  ok 'F5: a failed stack teardown does not abort the prune: the rest is pruned, the summary printed, the failure named and non-zero'
else bad 'F5: a failed stack teardown does not abort the prune: the rest is pruned, the summary printed, the failure named and non-zero' "rc=$rc; out=$out"; fi
# A caller umask of 077 checks the tree out world-readable (a container of another uid bind-mounts its
# files), while the env files create writes after the checkout keep that umask.
mode() { ls -ld "$1" | cut -c1-10; }
ENV_FILES=.env.test fixture
mkdir -p "$repo/init" && printf 'init\n' > "$repo/init/01-init.txt" && printf '#!/bin/sh\n' > "$repo/init/02-init.sh" || exit 2
chmod 755 "$repo/init/02-init.sh" && fgit "$repo" add init && fgit "$repo" commit -q -m init || exit 2
printf 'PORT=1\n' > "$repo/.env.test" && chmod 644 "$repo/.env.test" || exit 2
caller_umask="$(umask)"; umask 077
invoke create umask-077
umask "$caller_umask"
w="$repo/.worktrees/umask-077"
modes="file=$(mode "$w/init/01-init.txt") exec=$(mode "$w/init/02-init.sh") dir=$(mode "$w/init")"
if [[ "$rc" -eq 0 && "$modes" == 'file=-rw-r--r-- exec=-rwxr-xr-x dir=drwxr-xr-x' ]]; then
  ok 'umask: a 077 caller gets a checkout readable by another uid (644 files, 755 executables and dirs)'
else bad 'umask: a 077 caller gets a checkout readable by another uid (644 files, 755 executables and dirs)' "rc=$rc; $modes; out=$out"; fi
envmodes="env=$(mode "$w/.env.test") ports=$(mode "$w/.env.ports")"
if [[ "$rc" -eq 0 && "$envmodes" == 'env=-rw------- ports=-rw-------' ]]; then
  ok 'umask: the env files create writes after the checkout keep the caller 077 umask (600)'
else bad 'umask: the env files create writes after the checkout keep the caller 077 umask (600)' "rc=$rc; $envmodes; out=$out"; fi
# provisioned_fixture <name> [tracked|ignored]: copy/rewrite inside create, before final bytes are recorded.
provisioned_fixture() {
  ENV_FILES=.env.test fixture
  printf 'PORT=1\n' > "$repo/.env.test"
  if [[ "${2:-}" == tracked ]]; then fgit "$repo" add .env.test && fgit "$repo" commit -q -m env || exit 2; fi
  if [[ "${2:-}" == ignored ]]; then printf '.env.test\n.env.ports\n' > "$repo/.gitignore"; fgit "$repo" add .gitignore && fgit "$repo" commit -q -m ignored || exit 2; fi
  invoke create "$1"
  [[ "$rc" -eq 0 && -f "$repo/.worktrees/$1/.env.test" && -f "$repo/.worktrees/$1/.env.ports" ]] || { echo "test-worktree: setup failure: create did not provision the env file: $out" >&2; exit 2; }
  local expected=12345
  [[ "${REWRITE_ENV:-1}" == 1 ]] || expected=1
  [[ "$(cat "$repo/.worktrees/$1/.env.test")" == "PORT=$expected" ]] || { echo 'test-worktree: setup failure: env rewrite did not run inside create' >&2; exit 2; }
}
provisioned_fixture env-untracked
invoke remove env-untracked
if [[ "$rc" -eq 0 && ! -d "$repo/.worktrees/env-untracked" && "$out" == *'Worktree removed'* ]] && grep -qx 'free env-untracked' "$trace"; then
  ok 'F6: an untracked env file the script provisioned and rewrote does not make a worktree dirty'
else bad 'F6: an untracked env file the script provisioned and rewrote does not make a worktree dirty' "rc=$rc; out=$out"; fi
provisioned_fixture env-tracked tracked
invoke remove env-tracked
if [[ "$rc" -eq 0 && ! -d "$repo/.worktrees/env-tracked" && "$out" == *'Worktree removed'* ]] && grep -qx 'free env-tracked' "$trace"; then
  ok 'F6: a tracked env file the script provisioned and rewrote does not make a worktree dirty'
else bad 'F6: a tracked env file the script provisioned and rewrote does not make a worktree dirty' "rc=$rc; out=$out"; fi
provisioned_fixture env-and-work
printf 'uncommitted\n' > "$repo/.worktrees/env-and-work/base.txt"
invoke remove env-and-work
if [[ "$rc" -ne 0 && "$(cat "$repo/.worktrees/env-and-work/base.txt")" == uncommitted && "$out" == *'dirty worktree refused'* && "$out" != *'Worktree removed'* ]] && ! grep -qx 'free env-and-work' "$trace"; then
  ok 'F6: a real dirty file still refuses removal beside a provisioned env file'
else bad 'F6: a real dirty file still refuses removal beside a provisioned env file' "rc=$rc; out=$out"; fi
for kind in untracked tracked; do
  REWRITE_ENV=0 provisioned_fixture "env-copied-$kind" "$kind"
  invoke remove "env-copied-$kind"
  if [[ "$rc" -eq 0 && ! -d "$repo/.worktrees/env-copied-$kind" && "$out" == *'Pipeline cleaned up'* ]]; then
    ok "F6: unchanged copied $kind env bytes permit removal"
  else bad "F6: unchanged copied $kind env bytes permit removal" "rc=$rc; out=$out"; fi
done
for kind in untracked tracked ignored; do
  provisioned_fixture "env-edited-$kind" "$kind"
  w="$repo/.worktrees/env-edited-$kind"
  cp -p "$w/.env.test" "$repo/env-before"
  printf 'PORT=54321\n' > "$w/.env.test"
  touch -r "$repo/env-before" "$w/.env.test"
  # An already-ready create must not bless edits by recording a fresh hash.
  invoke create "env-edited-$kind"
  [[ "$rc" -eq 0 && "$out" == *'Worktree already exists:'* ]] || { echo "test-worktree: setup failure: ready create failed: $out" >&2; exit 2; }
  invoke remove "env-edited-$kind"
  if [[ "$rc" -ne 0 && -f "$w/.env.test" && "$(cat "$w/.env.test")" == 'PORT=54321' && "$out" == *'dirty worktree refused'* ]]; then
    ok "F6: later $kind env edit with unchanged size/mtime is preserved and refused"
  else bad "F6: later $kind env edit with unchanged size/mtime is preserved and refused" "rc=$rc; out=$out"; fi
done
provisioned_fixture env-ports-edited ignored
w="$repo/.worktrees/env-ports-edited"
printf 'USER_PORT=54321\n' >> "$w/.env.ports"
invoke remove env-ports-edited
if [[ "$rc" -ne 0 && -f "$w/.env.ports" && "$out" == *'dirty worktree refused'* ]] && grep -qx 'USER_PORT=54321' "$w/.env.ports"; then
  ok 'F6: later edits to ignored generated port bytes are preserved and refused'
else bad 'F6: later edits to ignored generated port bytes are preserved and refused' "rc=$rc; out=$out"; fi
for change in deleted symlink; do
  provisioned_fixture "env-$change" ignored
  w="$repo/.worktrees/env-$change"
  rm "$w/.env.test"
  if [[ "$change" == symlink ]]; then ln -s "$repo/.env.test" "$w/.env.test"; fi
  invoke remove "env-$change"
  if [[ "$rc" -ne 0 && -d "$w" && "$out" == *'dirty worktree refused'* ]]; then
    ok "F6: $change provisioned env is refused even when gitignored"
  else bad "F6: $change provisioned env is refused even when gitignored" "rc=$rc; out=$out"; fi
done
# A pre-metadata worktree has provisioned bytes, not a trusted hash receipt.
NUKE_CASE=1 fixture; nuke_makefile; add_worktree legacy
w="$repo/.worktrees/legacy"
printf 'FIXTURE_PORT=12345\n' > "$w/.env.ports"
printf 'keep this work\n' > "$w/base.txt"
invoke remove legacy
if [[ "$rc" -ne 0 && -f "$w/.env.ports" && "$(cat "$w/base.txt")" == 'keep this work' && "$out" == *'back up'* && "$out" == *'--discard'* && "$out" == *'stack volumes'* && ! -f "$trace" && ! -f "$repo/nuke-trace" ]]; then
  ok 'F15: legacy metadata refusal names an explicit backup-and-discard route and preserves work and stack'
else bad 'F15: legacy metadata refusal names an explicit backup-and-discard route and preserves work and stack' "rc=$rc; out=$out"; fi

for change in missing corrupt; do
  provisioned_fixture "env-metadata-$change" ignored
  w="$repo/.worktrees/env-metadata-$change"
  hashes="$(fgit "$w" rev-parse --absolute-git-dir)/provisioned-env-hashes"
  if [[ "$change" == missing ]]; then rm -rf "$hashes"; else mkdir -p "$hashes"; printf 'broken\n' > "$hashes/complete"; fi
  invoke remove "env-metadata-$change"
  if [[ "$rc" -ne 0 && -f "$w/.env.test" && "$(cat "$w/.env.test")" == 'PORT=12345' && "$out" == *'could not inspect provisioned env hashes'* ]]; then
    ok "F6: $change provisioning metadata keeps user bytes and fails visibly"
  else bad "F6: $change provisioning metadata keeps user bytes and fails visibly" "rc=$rc; out=$out"; fi
done
fixture
FAIL_HASH=1 invoke create hash-failed
w="$repo/.worktrees/hash-failed"
if [[ "$rc" -ne 0 && -f "$w/.env.ports" && "$out" == *'could not record provisioned env hashes'* && "$out" != *'Worktree ready:'* ]]; then
  ok 'F6: failed content hashing leaves incomplete provisioning and reports why'
else bad 'F6: failed content hashing leaves incomplete provisioning and reports why' "rc=$rc; out=$out"; fi
invoke create hash-failed
if [[ "$rc" -eq 0 && "$out" == *'Worktree ready:'* && "$out" == *'Resuming incomplete provisioning:'* ]]; then
  ok 'F6: retry after hashing recovers records final bytes before readiness'
else bad 'F6: retry after hashing recovers records final bytes before readiness' "rc=$rc; out=$out"; fi
# A repo with a submodule: create populates it, so a clean worktree holds an initialized submodule
# (`ignore = dirty`, as this repo's plugins carry it, hides the submodule's own edits from plain status).
# submodule_repo: a fixture whose main checkout holds the initialized submodule vendor; submodule_fixture
# then creates worktree <name> from it.
submodule_repo() {
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
}
submodule_fixture() {
  submodule_repo
  invoke create "$1"
  [[ "$rc" -eq 0 && -f "$repo/.worktrees/$1/vendor/lib.txt" ]] || { echo "test-worktree: setup failure: create did not populate the submodule: $out" >&2; exit 2; }
}
export GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=protocol.file.allow GIT_CONFIG_VALUE_0=always
# A gitlink only the main checkout's clone holds: its remote never received the commit.
submodule_repo
sgit() { /usr/bin/git -C "$1" -c user.name=fixture -c user.email=fixture@example.invalid -c commit.gpgsign=false "${@:2}"; }
printf 'local only\n' > "$repo/vendor/lib.txt" && sgit "$repo/vendor" commit -q -am local && sgit "$repo" commit -q -am 'bump vendor' || exit 2
local_sha="$(sgit "$repo/vendor" rev-parse HEAD)"
invoke create unpushed-pin
if [[ "$rc" -eq 0 && "$out" == *'Worktree ready:'* && "$(sgit "$repo/.worktrees/unpushed-pin/vendor" rev-parse HEAD 2>&1)" == "$local_sha" && "$(grep -c "^WARN submodule vendor: pinned ${local_sha:0:12} not fetchable from remote — initialised from local clone $repo/vendor (push it)$" "$T/out")" == 2 ]]; then
  ok 'F16: a pinned submodule commit missing on its remote inits from the main checkout clone, WARN on stderr and in the summary, exit 0'
else bad 'F16: a pinned submodule commit missing on its remote inits from the main checkout clone, WARN on stderr and in the summary, exit 0' "rc=$rc; out=$out"; fi
submodule_repo
FAIL_SUBMODULE=1 invoke create submodule-failed
if [[ "$rc" -eq 0 && "$out" == *'Worktree ready:'* && ! -e "$repo/.worktrees/submodule-failed/vendor/lib.txt" && "$(grep -c '^WARN submodule vendor: not initialised (synthetic submodule failure) — worktree created without it$' "$T/out")" == 2 ]] && grep -qx 'alloc submodule-failed' "$trace"; then
  ok 'F16: a submodule neither its remote nor the main checkout clone serves leaves the create complete, WARN on stderr and in the summary, exit 0'
else bad 'F16: a submodule neither its remote nor the main checkout clone serves leaves the create complete, WARN on stderr and in the summary, exit 0' "rc=$rc; out=$out"; fi
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
# land commits in the worktree under this identity.
export GIT_AUTHOR_NAME=fixture GIT_AUTHOR_EMAIL=fixture@example.invalid GIT_COMMITTER_NAME=fixture GIT_COMMITTER_EMAIL=fixture@example.invalid
# land_fixture <name>: a script-created worktree holding one committed change; tip is its branch's commit.
land_fixture() {
  fixture
  invoke create "$1"
  [[ "$rc" -eq 0 ]] || { echo "test-worktree: setup failure: create $1 failed: $out" >&2; exit 2; }
  w="$repo/.worktrees/$1"
  printf 'work\n' > "$w/work.txt" && fgit "$w" add work.txt && fgit "$w" commit -q -m work || exit 2
  tip="$(fgit "$w" rev-parse HEAD)" && base="$(fgit "$repo" rev-parse main)" || exit 2
}
has_branch() { /usr/bin/git -C "$repo" show-ref --verify --quiet "refs/heads/pipeline/$1"; }
land_fixture clean-land
invoke land clean-land
if [[ "$rc" -eq 0 && "$(fgit "$repo" rev-parse main)" == "$tip" && ! -d "$w" && "$out" == *'Landed: pipeline/clean-land on main'* ]] && ! has_branch clean-land && grep -qx 'free clean-land' "$trace"; then
  ok 'land: a clean branch fast-forwards main, then the worktree and branch are removed'
else bad 'land: a clean branch fast-forwards main, then the worktree and branch are removed' "rc=$rc; out=$out"; fi
land_fixture dirty-land
printf 'uncommitted\n' > "$w/base.txt"
invoke land dirty-land
if [[ "$rc" -ne 0 && "$(cat "$w/base.txt")" == uncommitted && "$(fgit "$repo" rev-parse main)" == "$base" && "$out" == "Error: $w has uncommitted changes; pass -m <message> to commit them; nothing landed" ]] && has_branch dirty-land; then
  ok 'land: a dirty worktree without -m refuses in one line, landing and committing nothing'
else bad 'land: a dirty worktree without -m refuses in one line, landing and committing nothing' "rc=$rc; out=$out"; fi
land_fixture message-land
printf 'leftover\n' > "$w/base.txt"
invoke land message-land -m 'leftover work'
if [[ "$rc" -eq 0 && "$(fgit "$repo" log -1 --format=%s main)" == 'leftover work' && "$(fgit "$repo" show main:base.txt)" == leftover && "$(fgit "$repo" show --name-only --format= main)" == base.txt && ! -d "$w" ]] && ! has_branch message-land; then
  ok 'land: -m commits the dirty work (never the provisioned .env.ports) and lands it'
else bad 'land: -m commits the dirty work (never the provisioned .env.ports) and lands it' "rc=$rc; out=$out"; fi
land_fixture moved-land
printf 'moved\n' > "$repo/moved.txt" && fgit "$repo" add moved.txt && fgit "$repo" commit -q -m moved || exit 2
moved="$(fgit "$repo" rev-parse main)"
invoke land moved-land
if [[ "$rc" -ne 0 && "$(fgit "$repo" rev-parse main)" == "$moved" && "$(fgit "$repo" rev-parse pipeline/moved-land)" == "$tip" && -d "$w" && "$out" == 'Error: main has commits pipeline/moved-land lacks; gitter syncs it (Phase MERGE); pipeline/moved-land keeps its commits; nothing landed' ]]; then
  ok 'land: a base that moved refuses in one line, keeping the branch, its commit and the worktree'
else bad 'land: a base that moved refuses in one line, keeping the branch, its commit and the worktree' "rc=$rc; out=$out"; fi
land_fixture locked-land
/usr/bin/git -C "$repo" worktree lock --reason 'post-merge gate running' "$w" || exit 2
invoke land locked-land
if [[ "$rc" -eq 0 && "$(fgit "$repo" rev-parse main)" == "$tip" && -d "$w" && "$out" == *"Worktree kept (locked): $w — post-merge gate running"* ]] && has_branch locked-land && ! grep -qx 'free locked-land' "$trace"; then
  ok 'land: a locked worktree lands and is kept with its branch, its lock reason reported'
else bad 'land: a locked worktree lands and is kept with its branch, its lock reason reported' "rc=$rc; out=$out"; fi
# The fixed interface: the header, the case dispatch and the rule name one verb set (git-guard keys on it).
RULE="${2:-$SOURCE_ROOT/templates/project/rules/git-writes-through-gitter.md}"
source "$SOURCE_ROOT/scripts/worktree-verbs.sh"
worktree_verbs_agree "$SUT" "$RULE"; agree=$?
if [[ "$agree" -eq 0 && "$WORKTREE_VERBS_REPORT" == "header($SUT)={create land list prune remove } case={create land list prune remove } rule($RULE)={create land list prune remove }" ]]; then
  ok 'interface: the header, the case dispatch and the rule name the same verbs'
else bad 'interface: the header, the case dispatch and the rule name the same verbs' "rc=$agree; $WORKTREE_VERBS_REPORT"; fi
sed 's/^  prune)/  sweep)/' "$SUT" > "$T/drifted.sh" || exit 2
worktree_verbs_agree "$T/drifted.sh" "$RULE"; agree=$?
if [[ "$agree" -eq 1 && "$WORKTREE_VERBS_REPORT" == "header($T/drifted.sh)={create land list prune remove } case={create land list remove sweep } rule($RULE)={create land list prune remove }" ]]; then
  ok 'interface: a dispatch verb the header and the rule do not name is a mismatch'
else bad 'interface: a dispatch verb the header and the rule do not name is a mismatch' "rc=$agree; $WORKTREE_VERBS_REPORT"; fi
shtest_end

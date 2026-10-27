#!/usr/bin/env bash
# Fixture-driven tests for infra/fence/fence-env.sh's engine-sync source: the
# fence mounts $ENGINE_SYNC_SRC, else the worktree's own plugins/engine-sync, else the main checkout's,
# read-only at /engine-sync; an absent source mounts the common Git directory in
# its place and names the absence in PFM_DEV_ENGINE_SYNC_MISSING, and a relative
# ENGINE_SYNC_SRC is refused. A real main checkout and a linked worktree, no docker.
#
#   bash infra/fence/lanes/tests/fence-env_test.sh
#   FENCE_ENV_SUT=/tmp/old/fence-env.sh bash …/fence-env_test.sh   # red-first
#
# BROKEN STATE: a missing SUT exits 2 before any assertion.
set -uo pipefail

SUT="${FENCE_ENV_SUT:-$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)/fence-env.sh}"
SHTEST_TAG=fence-env-test
# shellcheck source=/dev/null
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"
[ -f "$SUT" ] || { echo "fence-env_test: no fence-env.sh at $SUT" >&2; exit 2; }
shtest_isolate_host

MAIN="$T/main"
mkdir -p "$MAIN"
git -C "$MAIN" init -q && git -C "$MAIN" -c user.email=t@t -c user.name=t commit -q --allow-empty -m fixture
git -C "$MAIN" worktree add -q --detach "$T/linked" HEAD
MAIN="$(cd "$MAIN" && pwd -P)"
COMMON="$MAIN/.git"

resolve() { # resolve <worktree> [VAR=value…]: the engine-sync exports, one per line, or the resolver's error and exit
  local root="$1"; shift
  (
    unset ENGINE_SYNC_SRC HARVESTER_SRC
    [ $# -eq 0 ] || export "$@"
    ROOT="$root" FENCE_CALLER=fence-env-test . "$SUT"
    printf 'SRC=%s\nMISSING=%s\n' "$PFM_DEV_ENGINE_SYNC_SRC" "$PFM_DEV_ENGINE_SYNC_MISSING"
  ) 2>&1
}

# 1 — no source anywhere: the common Git directory stands in, and the absence is named.
out="$(resolve "$T/linked")"; rc=$?
want="$(printf 'SRC=%s\nMISSING=%s' "$COMMON" "$MAIN/plugins/engine-sync: no go.mod (check out plugins/engine-sync, or set ENGINE_SYNC_SRC)")"
if [ "$rc" -eq 0 ] && [ "$out" = "$want" ]; then
  ok 'an absent plugins/engine-sync mounts the common Git directory and names the absence'
else bad 'absent source' "rc=$rc" "got: $out" "want: $want"; fi

# 2 — the main checkout's plugins/engine-sync, found from a linked worktree.
mkdir -p "$MAIN/plugins/engine-sync"
printf 'module example.test/engine-sync\n' >"$MAIN/plugins/engine-sync/go.mod"
out="$(resolve "$T/linked")"; rc=$?
want="$(printf 'SRC=%s\nMISSING=' "$MAIN/plugins/engine-sync")"
if [ "$rc" -eq 0 ] && [ "$out" = "$want" ]; then
  ok "a linked worktree mounts the main checkout's plugins/engine-sync"
else bad 'main checkout source' "rc=$rc" "got: $out" "want: $want"; fi
out="$(resolve "$MAIN")"; rc=$?
if [ "$rc" -eq 0 ] && [ "$out" = "$want" ]; then
  ok 'the main checkout mounts its own plugins/engine-sync'
else bad 'main checkout from itself' "rc=$rc" "got: $out" "want: $want"; fi

# 2b — the worktree's own plugins/engine-sync (the commit the tree pins) wins over the main checkout's.
mkdir -p "$T/linked/plugins/engine-sync"
printf 'module example.test/engine-sync-pinned\n' >"$T/linked/plugins/engine-sync/go.mod"
out="$(resolve "$T/linked")"; rc=$?
want="$(printf 'SRC=%s\nMISSING=' "$T/linked/plugins/engine-sync")"
if [ "$rc" -eq 0 ] && [ "$out" = "$want" ]; then
  ok "a linked worktree holding its own plugins/engine-sync mounts that one, not the main checkout's"
else bad 'worktree engine-sync' "rc=$rc" "got: $out" "want: $want"; fi
rm -rf "$T/linked/plugins"

# 3 — an absolute ENGINE_SYNC_SRC wins over the checkout's copy.
mkdir -p "$T/elsewhere"
printf 'module example.test/engine-sync\n' >"$T/elsewhere/go.mod"
out="$(resolve "$T/linked" ENGINE_SYNC_SRC="$T/elsewhere")"; rc=$?
want="$(printf 'SRC=%s\nMISSING=' "$T/elsewhere")"
if [ "$rc" -eq 0 ] && [ "$out" = "$want" ]; then
  ok 'an absolute ENGINE_SYNC_SRC is the mounted source'
else bad 'ENGINE_SYNC_SRC override' "rc=$rc" "got: $out" "want: $want"; fi

# 4 — an ENGINE_SYNC_SRC without go.mod is named, never silently replaced by the checkout's copy.
mkdir -p "$T/empty"
out="$(resolve "$T/linked" ENGINE_SYNC_SRC="$T/empty")"; rc=$?
want="$(printf 'SRC=%s\nMISSING=%s' "$COMMON" "$T/empty: no go.mod (check out plugins/engine-sync, or set ENGINE_SYNC_SRC)")"
if [ "$rc" -eq 0 ] && [ "$out" = "$want" ]; then
  ok 'an ENGINE_SYNC_SRC without go.mod is named as the missing source'
else bad 'ENGINE_SYNC_SRC without go.mod' "rc=$rc" "got: $out" "want: $want"; fi

# 5 — a relative ENGINE_SYNC_SRC is refused by name.
out="$(resolve "$T/linked" ENGINE_SYNC_SRC=plugins/engine-sync)"; rc=$?
if [ "$rc" -eq 1 ] && [ "$out" = 'fence-env-test: ENGINE_SYNC_SRC must be an absolute path: plugins/engine-sync' ]; then
  ok 'a relative ENGINE_SYNC_SRC exits 1 naming it'
else bad 'relative ENGINE_SYNC_SRC' "rc=$rc" "got: $out"; fi

harvester_resolve() {
  local root="$1"; shift
  (
    unset HARVESTER_SRC
    [ $# -eq 0 ] || export "$@"
    ROOT="$root" FENCE_CALLER=fence-env-test . "$SUT"
    printf 'SRC=%s\nMISSING=%s\n' "$PFM_DEV_HARVESTER_SRC" "$PFM_DEV_HARVESTER_MISSING"
  ) 2>&1
}
out="$(harvester_resolve "$T/linked")"; rc=$?
if [ "$rc" -eq 0 ] && [[ "$out" == *"MISSING=$MAIN/plugins/harvester: no go.mod"* ]]; then
  ok 'missing harvester source is named'
else bad 'missing harvester source' "rc=$rc" "$out"; fi
mkdir -p "$MAIN/plugins/harvester" "$T/linked/plugins/harvester"
printf 'module example.test/harvester\n' >"$MAIN/plugins/harvester/go.mod"
out="$(harvester_resolve "$T/linked")"
if [ "$out" = "$(printf 'SRC=%s\nMISSING=' "$MAIN/plugins/harvester")" ]; then
  ok 'linked worktree resolves harvester from the main checkout'
else bad 'main harvester source' "$out"; fi
printf 'module example.test/pinned-harvester\n' >"$T/linked/plugins/harvester/go.mod"
out="$(harvester_resolve "$T/linked")"
if [ "$out" = "$(printf 'SRC=%s\nMISSING=' "$T/linked/plugins/harvester")" ]; then
  ok 'worktree harvester source wins over the main checkout'
else bad 'pinned harvester source' "$out"; fi
out="$(harvester_resolve "$T/linked" HARVESTER_SRC=plugins/harvester)"; rc=$?
if [ "$rc" -eq 1 ] && [[ "$out" == *'HARVESTER_SRC must be an absolute path: plugins/harvester' ]]; then
  ok 'relative harvester source is refused by name'
else bad 'relative harvester source' "rc=$rc" "$out"; fi
shtest_end

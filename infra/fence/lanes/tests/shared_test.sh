#!/usr/bin/env bash
# infra/fence/shared.sh, the shared fence container's host side, against a docker stub: the knob
# validation and paths fence_shared_init derives, fence_shared_map's three answers, fence_shared_ensure
# (a running container with this call's caps is entered as it is; other caps are applied with docker
# update, a refused update is a WARN and the run goes on; no container starts one with the caps, the
# labels and the mounts; the lock is released on every path; a dead holder's lock is broken; a name
# still in use is retried; a start that fails is a named iso line; the container runs its key's own
# image tag, made from an image whose label was read, rebuilt when another checkout moved the plain
# tag; stale keys' tags are removed; a name in use by a running container is entered), the detached
# start housekeeping (a dead caller's run directory reaped, a live one's kept, the log rotated), and
# fence_shared_exec (the shared-run.sh argv, the run's env, exit 75 or a container gone before the run
# started retried once in a fresh container, any other refusal returned as it is, a worktree whose key
# changed leaving its old container and another worktree's container untouched, an engine-sync
# source outside the roots refused). docker is a shell function recording its argv; no container or
# image is touched.
#   bash infra/fence/lanes/tests/shared_test.sh
# BROKEN STATE: a missing shared.sh exits 2 naming it; a case that cannot run is a FAIL line.
set -uo pipefail
FENCE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
SUT="${SHARED_SUT:-$FENCE/shared.sh}"
SHTEST_TAG=fence-shared-test
# shellcheck source=/dev/null
. "$FENCE/../../scripts/shtest.sh"
shtest_unset_gate_env
PFM_FENCE_PROFILE=desktop
[ -f "$SUT" ] || { echo "shared_test: no shared.sh at $SUT" >&2; exit 2; }
# shellcheck source=/dev/null
. "$SUT"

PROJ="sharedt$$"
MAIN="$T/.$PROJ"
mkdir -p "$MAIN/.git" "$MAIN/.worktrees/wt1" "$MAIN/plugins/engine-sync" "$T/ext" "$T/elsewhere"
MAIN="$(cd "$MAIN" && pwd -P)"
shtest_clean_also "/tmp/$PROJ"
REC="$T/docker.rec"
fence_volumes_ensure() { echo volumes >>"$REC"; }
fence_housekeeping() { :; }
stub_docker() { # STUB_INSPECT: the inspect answer (empty = no container); STUB_RUN_RC, STUB_UPDATE_RC; $T/run.inuse (one in-use refusal), $T/exec.rcs (space list) — files, as docker runs in $( ) subshells
  { printf 'docker'; printf ' %q' "$@"; printf '\n'; } >>"$REC"
  case "$1" in
    info) echo "${STUB_INFO:-Docker Desktop|10|8267812044}" ;;
    inspect) # $T/gone: while it exists, the container has stopped and gone (`run --rm`)
      [ ! -e "$T/gone" ] || return 1
      local insp="${STUB_INSPECT:-}"; [ ! -e "$T/inspect.after" ] || insp="$(cat "$T/inspect.after")"
      [ -n "$insp" ] || return 1
      if [ "$2" = -f ] && [ "$3" = '{{.State.Running}}' ]; then echo "${insp%% *}";
      elif [[ "$3" == '{{.HostConfig.NanoCpus}}'* ]]; then echo "${insp#* }";
      else echo "$insp"; fi ;;
    image) # STUB_KTAG_LABEL: the k- tag's label (unset = no such tag); STUB_IMG: the plain tag's "id label"
      case "$2" in
        inspect) case "${*: -1}" in
            *:k-*) [ -n "${STUB_KTAG_LABEL+x}" ] || return 1; echo "$STUB_KTAG_LABEL" ;;
            *) echo "${STUB_IMG-sha256:img1 $PFM_DEV_INPUTS_KEY}" ;;
          esac ;;
        ls) printf '%s\n' "${STUB_KTAGS:-}" ;;
        rm) return 0 ;;
      esac ;;
    tag) return 0 ;;
    compose) [ -z "${STUB_IMG_AFTER_BUILD:-}" ] || STUB_IMG="$STUB_IMG_AFTER_BUILD"; return "${STUB_BUILD_RC:-0}" ;;
    update)
      [[ "${STUB_UPDATE_RC:-0}" == 0 ]] || return "$STUB_UPDATE_RC"
      if [[ "${STUB_UPDATE_NOOP:-0}" != 1 ]]; then
        local cpu mem cpuset
        cpu="$(_fence_sh_nanocpus "$3")"; mem="$(_fence_sh_bytes "$5")"; cpuset="$9"
        printf 'true %s %s %s|%s\n' "$cpu" "$mem" "$cpuset" "$mem" > "$T/inspect.after"
      fi ;;
    rm) touch "$T/gone" ;;
    run) if [ -e "$T/run.inuse" ]; then rm -f "$T/run.inuse"; [ -z "${STUB_INUSE_INSPECT:-}" ] || echo "$STUB_INUSE_INSPECT" >"$T/inspect.after"; echo 'Conflict. The container name is already in use'; return 125; fi
         [ "${STUB_RUN_RC:-0}" = 0 ] || { echo 'boom: no such image'; return "$STUB_RUN_RC"; }; echo cid ;;
    ps) printf '%s\n' "${STUB_PS:-}" ;;
    exec)
      if [[ "$*" == *fence-live-count* ]]; then if [[ -n "${STUB_LIVE_DIR:-}" ]]; then find "$STUB_LIVE_DIR" -type f | wc -l; else echo "${STUB_LIVE_COUNT:-0}"; fi; return 0; fi
      if [[ "$*" == *fence-reserve* && -n "${STUB_LIVE_DIR:-}" ]]; then touch "$STUB_LIVE_DIR/${*: -1}"; return 0; fi
      if [[ "$*" == *'rm -f /var/fence/live/'* && -n "${STUB_LIVE_DIR:-}" ]]; then
        local live_path="${*: -1}"; rm -f "$STUB_LIVE_DIR/${live_path##*/}"; return 0
      fi
      if [[ "$*" == *fence-reserve* || "$*" == *'rm -f /var/fence/live/'* ]]; then return 0; fi
      local rcs rc
      rcs="$(cat "$T/exec.rcs" 2>/dev/null)"; rc="${rcs%% *}"; [ -n "$rc" ] || rc=0
      if [ "$rcs" = "${rcs#* }" ]; then : >"$T/exec.rcs"; else echo "${rcs#* }" >"$T/exec.rcs"; fi
      return "$rc" ;;
  esac
}
docker() { stub_docker "$@"; }
sleep() { :; }
# A start's detached housekeeping is recorded, not run (STUB_AFTER_SLEEP: it takes that long);
# real_after_start is the real one, driven directly below.
eval "real_after_start() $(declare -f _fence_sh_after_start | tail -n +2)"
_fence_sh_after_start() { echo after_start >>"$REC"; [ -z "${STUB_AFTER_SLEEP:-}" ] || command sleep "$STUB_AFTER_SLEEP"; }
PFM_DEV_GIT_COMMON="$MAIN/.git"
init() { # init [tree]: a fresh fence_shared_init from MAIN (or the given tree)
  local PFM_FENCE_CPUS="${PFM_FENCE_CPUS-6}" PFM_FENCE_MEM="${PFM_FENCE_MEM-5g}"
  REPO_ROOT="${1:-$MAIN}"; fence_shared_init
}
reset() { : >"$REC"; unset STUB_INSPECT STUB_RUN_RC STUB_UPDATE_RC STUB_UPDATE_NOOP STUB_PS STUB_KTAG_LABEL STUB_IMG STUB_IMG_AFTER_BUILD STUB_BUILD_RC STUB_KTAGS STUB_INUSE_INSPECT STUB_AFTER_SLEEP; FENCE_IMAGE_BUILD=(); rm -f "$T/exec.rcs" "$T/run.inuse" "$T/gone" "$T/inspect.after"; }
has() { if grep -qF -- "$2" "$REC"; then ok "$1"; else bad "$1" "no docker call with: $2" "$(cat "$REC")"; fi; }
lacks() { if grep -qF -- "$2" "$REC"; then bad "$1" "a docker call with: $2" "$(cat "$REC")"; else ok "$1"; fi; }
PFM_DEV_INPUTS_KEY=abcdef0123456789abcdef; FENCE_IMAGE_BUILD=(); FENCE_SH_COMPOSE="$T/docker-compose.yml"

# --- init: knobs and paths ---------------------------------------------------------------------------------
for bad in 'PFM_FENCE_CPUS=two' 'PFM_FENCE_MEM=5 GB' 'PFM_FENCE_TEST_P=0'; do
  out="$(env PFM_FENCE_PROFILE=desktop "$bad" bash -c '. "$1"; REPO_ROOT="$2" PFM_DEV_GIT_COMMON="$2/.git" fence_shared_init' _ "$SUT" "$MAIN" 2>&1)"; rc=$?
  if [ "$rc" != 0 ] && grep -q "^iso: ${bad%%=*} must be" <<<"$out"; then ok "$bad is refused, named"; else bad "$bad refusal" "rc=$rc" "$out"; fi
done
init
if [ "$FENCE_SH_HOST" = "/tmp/$PROJ/fence/shared" ] && [ "$FENCE_SH_LOCK" = "/tmp/$PROJ/fence/shared.lock.d" ] && [ "$FENCE_SH_WT" = "$PROJ" ] && [ -d "$FENCE_SH_HOST/$PROJ/timing" ]; then
  ok "init derives the host directory, lock and worktree name from the main checkout minus its dot"
else bad "init paths" "$FENCE_SH_HOST $FENCE_SH_LOCK $FENCE_SH_WT"; fi
if [ "$FENCE_SH_CPUS $FENCE_SH_MEM" = '6 5g' ]; then ok "the caps default to 6 CPUs and 5g"; else bad "default caps" "$FENCE_SH_CPUS $FENCE_SH_MEM"; fi
PFM_FENCE_CPUS=3.5 PFM_FENCE_MEM=6144m init
if [ "$FENCE_SH_CPUS $FENCE_SH_MEM" = '3.5 6144m' ]; then ok "PFM_FENCE_CPUS and PFM_FENCE_MEM set the caps per call"; else bad "knob caps" "$FENCE_SH_CPUS $FENCE_SH_MEM"; fi
unset PFM_FENCE_CPUS PFM_FENCE_MEM
init "$MAIN/.worktrees/wt1"
n1="$(_fence_sh_name)"
if [ "$FENCE_SH_WT" = wt1 ] && [ -z "$FENCE_SH_EXT" ]; then ok "a worktree under the main checkout needs no extra root"; else bad "worktree init" "$FENCE_SH_WT ext=$FENCE_SH_EXT"; fi
init
if [ "$(_fence_sh_name)" = "$n1" ]; then ok "every worktree under one main checkout shares one container name"; else bad "shared name" "$n1 vs $(_fence_sh_name)"; fi
if [[ "$n1" =~ ^pfm-fence-[0-9a-f]{12}-abcdef012345$ ]]; then ok "the name is pfm-fence-<roots12>-<key12>"; else bad "the name" "$n1"; fi

# --- map ---------------------------------------------------------------------------------------------------
init
if [ "$(fence_shared_map "$MAIN/.worktrees/wt1")" = /repo/.worktrees/wt1 ]; then ok "a path under the main checkout maps under /repo"; else bad "map main" "$(fence_shared_map "$MAIN/.worktrees/wt1")"; fi
if fence_shared_map "$T/elsewhere" >/dev/null; then bad "a path outside the roots maps" "$(fence_shared_map "$T/elsewhere")"; else ok "a path outside the roots does not map"; fi
init "$T/ext"
if [ "$(fence_shared_map "$T/ext")" = /repo-ext ] && [ "$(_fence_sh_name)" != "$n1" ]; then ok "a tree outside the main checkout maps to /repo-ext in its own container"
else bad "map ext" "$(fence_shared_map "$T/ext") $(_fence_sh_name)"; fi

# --- ensure ------------------------------------------------------------------------------------------------
init; reset
STUB_INSPECT='true 6000000000 5368709120'
fence_shared_ensure; rc=$?
if [ "$rc" = 0 ]; then ok "a running container with this call's caps is entered"; else bad "ensure running" "rc=$rc"; fi
lacks "a running container with equal caps is neither updated nor started" 'docker update'
lacks "a running container is never started again" 'docker run'
reset; STUB_INSPECT='true 6000000000 5368709120'
PFM_FENCE_CPUS=4 PFM_FENCE_MEM=8g init
out="$(fence_shared_ensure 2>&1)"; rc=$?
has "other caps are applied to the running container with docker update" "docker update --cpus 4 --memory 8g --memory-swap 8g --cpuset-cpus '' $FENCE_SH_NAME"
if [ "$rc" = 0 ] && grep -qF "fence: $FENCE_SH_NAME caps now cpus=4 mem=8g" <<<"$out"; then ok "a cap update is named"; else bad "cap update line" "$out"; fi
reset; STUB_INSPECT='true 6000000000 5368709120'; STUB_UPDATE_RC=1
out="$(fence_shared_ensure 2>&1)"; rc=$?
if [ "$rc" = 0 ] && grep -q '^WARN fence: docker update of .* refused — the run goes on under the running caps' <<<"$out"; then ok "a refused cap update is a WARN and the run goes on"; else bad "refused update" "rc=$rc" "$out"; fi
reset
PFM_FENCE_CPUS=2 PFM_FENCE_MEM=3g init
out="$(fence_shared_ensure 2>&1)"; rc=$?
has "no container: one is started with the call's CPU cap" '--cpus 2 --memory 3g --memory-swap 3g'
has "the container carries the long-lived and shared labels" "--label pfm.fence.long-lived=1 --label pfm.fence.shared=$FENCE_SH_ROOTS"
has "the main checkout is mounted read-only at /repo" "-v $MAIN:/repo:ro"
has "the host directory is mounted at /pfm-fence" "-v /tmp/$PROJ/fence/shared:/pfm-fence"
has "the container gets the capabilities a run's namespaces need" '--cap-add SYS_ADMIN --cap-add NET_ADMIN'
has "the container removes itself when its daemon exits" 'docker run -d --rm --init'
has "the cache volumes are ensured first" volumes
if [ "$rc" = 0 ] && [ ! -e "$FENCE_SH_LOCK" ]; then ok "a start releases the lock"; else bad "start lock" "rc=$rc lock=$(ls -d "$FENCE_SH_LOCK" 2>&1)"; fi
reset; STUB_AFTER_SLEEP=3; t0=$SECONDS
out="$(fence_shared_ensure 2>&1)"; rc=$?; took=$((SECONDS - t0))
if [ "$rc" = 0 ] && [ "$took" -lt 2 ]; then ok "a start's housekeeping is detached: the caller's output closes without waiting for it"
else bad "detached housekeeping" "rc=$rc took=${took}s (housekeeping 3 s)" "$out"; fi
reset; STUB_RUN_RC=125
out="$(fence_shared_ensure 2>&1)"; rc=$?
if [ "$rc" = 1 ] && grep -qF "iso: the shared fence container $FENCE_SH_NAME did not start: boom: no such image" <<<"$out" && [ ! -e "$FENCE_SH_LOCK" ]; then
  ok "a failed start is a named iso line, return 1, the lock released"
else bad "failed start" "rc=$rc" "$out"; fi
reset; : >"$T/run.inuse"
fence_shared_ensure >/dev/null 2>&1; rc=$?
if [ "$rc" = 0 ] && [ "$(grep -c '^docker run' "$REC")" = 2 ]; then ok "a name still in use is retried"; else bad "in-use retry" "rc=$rc" "$(cat "$REC")"; fi
reset; : >"$T/run.inuse"; STUB_INUSE_INSPECT='true 6000000000 5368709120'
fence_shared_ensure >/dev/null 2>&1; rc=$?
if [ "$rc" = 0 ] && [ "$(grep -c '^docker run' "$REC")" = 1 ] && [ ! -e "$FENCE_SH_LOCK" ]; then ok "a name in use by a running container is entered, never started again"
else bad "in-use running" "rc=$rc" "$(cat "$REC")"; fi
reset
mkdir -p "$FENCE_SH_LOCK"; echo 999999 >"$FENCE_SH_LOCK/pid"
fence_shared_ensure >/dev/null 2>&1; rc=$?
if [ "$rc" = 0 ] && grep -q '^docker run' "$REC" && [ ! -e "$FENCE_SH_LOCK" ]; then ok "a dead holder's lock is broken"; else bad "dead lock" "rc=$rc" "$(cat "$REC")"; fi
# the image: the key's own tag, never the plain tag another checkout may rebuild between build and start
KTAG="$FENCE_SHARED_IMAGE:k-abcdef012345"
reset
fence_shared_ensure >/dev/null 2>&1; rc=$?
has "no k- tag: the plain tag's image id, its label this key, is tagged with the key" "docker tag sha256:img1 $KTAG"
lacks "a plain tag already carrying this key is not rebuilt" 'docker compose'
if [ "$rc" = 0 ] && grep -q "^docker run .* $KTAG bash -c" "$REC"; then ok "the container runs the key's own tag"; else bad "run image" "rc=$rc" "$(cat "$REC")"; fi
reset; STUB_KTAG_LABEL="$PFM_DEV_INPUTS_KEY"
fence_shared_ensure >/dev/null 2>&1; rc=$?
lacks "a k- tag carrying this key needs no tag" 'docker tag'
lacks "a k- tag carrying this key needs no build" 'docker compose'
if [ "$rc" = 0 ] && grep -q "^docker run .* $KTAG bash -c" "$REC"; then ok "an existing k- tag is run as it is"; else bad "existing k- tag" "rc=$rc" "$(cat "$REC")"; fi
reset; FENCE_IMAGE_BUILD=(--build); STUB_IMG='sha256:other otherkey'; STUB_IMG_AFTER_BUILD="sha256:img2 $PFM_DEV_INPUTS_KEY"
out="$(fence_shared_ensure 2>&1)"; rc=$?
has "a key whose image is absent is built" 'docker compose -f'
has "the built image's id is tagged" "docker tag sha256:img2 $KTAG"
reset; STUB_IMG='sha256:other otherkey'
out="$(fence_shared_ensure 2>&1)"; rc=$?
if [ "$rc" = 1 ] && [ "$(grep -c '^docker compose' "$REC")" = 2 ] && grep -qF 'another checkout rebuilt it; building again' <<<"$out" &&
  grep -qF 'iso: no professor-pfm-dev image with inputs abcdef012345 after 3 builds' <<<"$out" && ! grep -q '^docker run' "$REC" && [ ! -e "$FENCE_SH_LOCK" ]; then
  ok "a plain tag another checkout keeps rebuilding is built again, then refused by name, never run"
else bad "stolen tag" "rc=$rc" "$out" "$(cat "$REC")"; fi
reset; STUB_BUILD_RC=1; FENCE_IMAGE_BUILD=(--build)
out="$(fence_shared_ensure 2>&1)"; rc=$?
if [ "$rc" = 1 ] && grep -qF 'iso: the professor-pfm-dev image could not be built' <<<"$out" && [ ! -e "$FENCE_SH_LOCK" ]; then ok "a failed build is named, the lock released"
else bad "failed build" "rc=$rc" "$out"; fi
reset; FENCE_SH_IMAGE="$KTAG"
STUB_KTAGS="$KTAG
$FENCE_SHARED_IMAGE:k-111111111111
$FENCE_SHARED_IMAGE:k-222222222222"
STUB_PS="$FENCE_SHARED_IMAGE:k-222222222222"
_fence_sh_untag_stale >/dev/null
has "a stale key's tag no container runs is removed" "docker image rm $FENCE_SHARED_IMAGE:k-111111111111"
lacks "a tag a running container runs is kept" "docker image rm $FENCE_SHARED_IMAGE:k-222222222222"
lacks "this key's own tag is kept" "docker image rm $KTAG"
reset; STUB_PS="pfm-fence-old oldkey"
fence_shared_ensure >/dev/null 2>&1
lacks "a start supersedes no other container: another worktree's key keeps its warm container" 'docker exec'
# the detached start housekeeping: a dead caller's run directory goes, a live one's stays, the log rotates
reset; FENCE_SH_IMAGE="$KTAG"
command sleep 60 & live=$!
dead="$FENCE_SH_HOST/.runs/20261018T000000Z-999999-123"; alive="$FENCE_SH_HOST/.runs/20261018T000000Z-$live-456"
mkdir -p "$dead" "$alive"; echo secret >"$dead/leak-terms.txt"
HKLOG="/tmp/$PROJ/fence/housekeeping.log"
head -c 1048577 /dev/zero >"$HKLOG"
real_after_start
if [ ! -e "$dead" ] && [ -d "$alive" ]; then ok "a dead caller's run directory (its leak terms) is reaped, a live caller's kept"; else bad "run reap" "dead=$(ls -d "$dead" 2>&1) alive=$(ls -d "$alive" 2>&1)"; fi
if [ -s "$HKLOG.1" ] && [ "$(wc -c <"$HKLOG" | tr -d ' ')" -lt 1048576 ] && grep -qF "reaped the run directory $dead" "$HKLOG"; then ok "the housekeeping log rotates past 1 MiB and names the reap"
else bad "log rotation" "$(ls -l "$HKLOG"* 2>&1)"; fi
if [ ! -e "$FENCE_SH_LOCK" ]; then ok "the untag releases the start lock it held"; else bad "untag lock" "$(ls -d "$FENCE_SH_LOCK")"; fi
kill "$live" 2>/dev/null; wait "$live" 2>/dev/null; rm -rf "$alive"

# --- exec ----------------------------------------------------------------------------------------------------
unset PFM_FENCE_CPUS PFM_FENCE_MEM
init "$MAIN/.worktrees/wt1"; reset
STUB_INSPECT='true 6000000000 5368709120'
PFM_DEV_ENGINE_SYNC_SRC="$MAIN/plugins/engine-sync" PFM_DEV_GIT_DIR_REL=worktrees/wt1 fence_shared_exec private 'echo hi'; rc=$?
if [ "$rc" = 0 ]; then ok "exec returns the run's code"; else bad "exec rc" "rc=$rc" "$(cat "$REC")"; fi
has "exec runs shared-run.sh from the caller's tree in the container" "/repo/.worktrees/wt1/infra/fence/shared-run.sh run"
has "exec passes the mode, the tree, the engine-sync source and the worktree name" "private /repo/.worktrees/wt1 /repo/plugins/engine-sync wt1 -- bash -c echo\\ hi"
has "exec feeds the heartbeat on stdin" "docker exec -i"
has "exec names the run's id in its env" "PFM_FENCE_RUN_ID="
has "exec passes the CPU cap to the run" "PFM_FENCE_CPUS=6"
if [ -z "$(ls -A "$FENCE_SH_HOST/.runs")" ]; then ok "exec removes the run's host directory"; else bad "exec left a host run dir" "$(ls "$FENCE_SH_HOST/.runs")"; fi
reset; STUB_INSPECT='true 6000000000 5368709120'; echo '75 0' >"$T/exec.rcs"
PFM_DEV_ENGINE_SYNC_SRC="$MAIN/plugins/engine-sync" PFM_DEV_GIT_DIR_REL=worktrees/wt1 fence_shared_exec shared 'true'; rc=$?
if [ "$rc" = 0 ] && [ "$(grep -c '^docker exec -i' "$REC")" = 2 ]; then ok "exit 75 before the run started is retried once"; else bad "75 retry" "rc=$rc" "$(cat "$REC")"; fi
reset; STUB_INSPECT='true 6000000000 5368709120'; echo '75 75' >"$T/exec.rcs"
PFM_DEV_ENGINE_SYNC_SRC="$MAIN/plugins/engine-sync" PFM_DEV_GIT_DIR_REL=worktrees/wt1 fence_shared_exec shared 'true'; rc=$?
if [ "$rc" = 75 ] && [ "$(grep -c '^docker exec -i' "$REC")" = 2 ]; then ok "a second 75 is returned, never a third try"; else bad "75 twice" "rc=$rc" "$(cat "$REC")"; fi
# docker's own refusal of a container that stopped between ensure and exec: retried in a new one
reset; STUB_INSPECT='true 6000000000 5368709120'; echo '1 0' >"$T/exec.rcs"
docker() { # the container goes at the first exec; a docker run makes a new one
  if [ "$1" = exec ] && [ "$(cat "$T/exec.rcs" 2>/dev/null)" = '1 0' ]; then : >"$T/gone"; fi
  if [ "$1" = run ]; then rm -f "$T/gone"; fi
  stub_docker "$@"
}
PFM_DEV_ENGINE_SYNC_SRC="$MAIN/plugins/engine-sync" PFM_DEV_GIT_DIR_REL=worktrees/wt1 fence_shared_exec shared 'true' 2>"$T/gone.err"; rc=$?
docker() { stub_docker "$@"; }
if [ "$rc" = 0 ] && [ "$(grep -c '^docker exec -i' "$REC")" = 2 ] && grep -q '^docker run' "$REC" && grep -qF 'closed before the run started (exit 1)' "$T/gone.err"; then
  ok "a container gone before the run started (docker's non-75 refusal) is retried once in a new container"
else bad "gone retry" "rc=$rc" "$(cat "$REC" "$T/gone.err")"; fi
reset; STUB_INSPECT='true 6000000000 5368709120'; echo '1 0' >"$T/exec.rcs"
PFM_DEV_ENGINE_SYNC_SRC="$MAIN/plugins/engine-sync" PFM_DEV_GIT_DIR_REL=worktrees/wt1 fence_shared_exec shared 'true' 2>/dev/null; rc=$?
if [ "$rc" = 1 ] && [ "$(grep -c '^docker exec -i' "$REC")" = 1 ]; then ok "a refusal from a container still running is returned as it is, never retried"; else bad "running refusal" "rc=$rc" "$(cat "$REC")"; fi
# a worktree whose key changed leaves its old container; a first call or the same key touches none
init "$MAIN/.worktrees/wt1"; reset; rm -f "$FENCE_SH_HOST/wt1/key"
STUB_INSPECT='true 6000000000 5368709120'
PFM_DEV_ENGINE_SYNC_SRC="$MAIN/plugins/engine-sync" PFM_DEV_GIT_DIR_REL=worktrees/wt1 fence_shared_exec shared 'true' 2>/dev/null
if [ "$(cat "$FENCE_SH_HOST/wt1/key" 2>/dev/null)" = "$PFM_DEV_INPUTS_KEY" ] && ! grep -q 'docker exec pfm-fence-.* sh -c' "$REC"; then ok "a worktree's first call records its key and leaves no container"
else bad "first key" "$(cat "$FENCE_SH_HOST/wt1/key" 2>&1)" "$(cat "$REC")"; fi
reset; STUB_INSPECT='true 6000000000 5368709120'; echo 0123456789abcdefold >"$FENCE_SH_HOST/wt1/key"
PFM_DEV_ENGINE_SYNC_SRC="$MAIN/plugins/engine-sync" PFM_DEV_GIT_DIR_REL=worktrees/wt1 fence_shared_exec shared 'true' 2>/dev/null
has "a worktree whose key changed leaves its old key's container, by name, as itself" "docker exec pfm-fence-$FENCE_SH_ROOTS-0123456789ab sh -c"
if grep -q "docker exec pfm-fence-$FENCE_SH_ROOTS-0123456789ab sh -c .* _ wt1\$" "$REC" && [ "$(cat "$FENCE_SH_HOST/wt1/key")" = "$PFM_DEV_INPUTS_KEY" ]; then ok "the leave names this worktree and records the new key"
else bad "leave argv" "$(cat "$REC")"; fi
reset; STUB_INSPECT='true 6000000000 5368709120'
PFM_DEV_ENGINE_SYNC_SRC="$MAIN/plugins/engine-sync" PFM_DEV_GIT_DIR_REL=worktrees/wt1 fence_shared_exec shared 'true' 2>/dev/null
lacks "an unchanged key leaves no container" ' sh -c '
# the leave's own text: the old container is superseded only once no worktree is left in it
LEAVE="$(sed -n "s/.*docker exec \"\$oname\" sh -c '\([^']*\)' _ .*/\1/p" "$SUT")"
U="$T/leave"; mkdir -p "$U/worktrees"; : >"$U/worktrees/wt1"; : >"$U/worktrees/wt2"
sh -c "${LEAVE//\/var\/fence/$U}" _ wt1
if [ ! -e "$U/worktrees/wt1" ] && [ ! -e "$U/superseded" ]; then ok "a container another worktree still uses is not superseded when one leaves"; else bad "leave with a user left" "$(ls -R "$U")"; fi
sh -c "${LEAVE//\/var\/fence/$U}" _ wt2
if [ -e "$U/superseded" ]; then ok "the last worktree to leave supersedes the container"; else bad "last leave" "LEAVE=$LEAVE" "$(ls -R "$U")"; fi
reset; STUB_INSPECT='true 6000000000 5368709120'
out="$(PFM_DEV_ENGINE_SYNC_SRC="$T/elsewhere" PFM_DEV_GIT_DIR_REL=worktrees/wt1 fence_shared_exec shared 'true' 2>&1)"; rc=$?
if [ "$rc" = 1 ] && grep -q '^iso: the engine-sync source .* is outside the shared container' <<<"$out"; then ok "an engine-sync source outside the roots is refused, named"; else bad "engine-sync refusal" "rc=$rc" "$out"; fi
lacks "a refused engine-sync source never reaches docker exec" 'docker exec'

# Public configuration must reject malformed supplied knobs, not silently run with defaults.
for assignment in 'PFM_FENCE_HEARTBEAT_S=bad' 'PFM_FENCE_IDLE_S=0' 'PFM_FENCE_CPUSET=2--4' 'PFM_FENCE_GOMAXPROCS=-1' 'STEPS_JOBS=0' 'PFM_FENCE_MEM=0g' 'STEPPROF_GRACE_TICKS=08' 'PFM_FENCE_NET=outside'; do
  out="$(env PFM_FENCE_PROFILE=desktop "$assignment" bash -c '. "$1"; REPO_ROOT="$2" PFM_DEV_GIT_COMMON="$2/.git" fence_shared_init' _ "$SUT" "$MAIN" 2>&1)"; rc=$?
  if [[ $rc != 0 && "$out" == *"${assignment%%=*}"* ]]; then ok "entry rejects $assignment by name"; else bad "entry rejects $assignment by name" "rc=$rc $out"; fi
done
for profile in desktop native; do
  out="$(PFM_FENCE_PROFILE="$profile" PFM_FENCE_CPUS=2 PFM_FENCE_MEM=5g bash -c '. "$1"; REPO_ROOT="$2" PFM_DEV_GIT_COMMON="$2/.git" fence_shared_init; printf "%s|%s|%s" "${PFM_FENCE_PROFILE:-}" "$FENCE_SH_CPUS" "$FENCE_SH_MEM"' _ "$SUT" "$MAIN" 2>&1)"
  [[ "$out" == "$profile|2|5g" ]] && ok "$profile preserves explicit cap and legacy memory" || bad "$profile preserves explicit cap and legacy memory" "$out"
done
reset; STUB_INSPECT='true 6000000000 5368709120'; STUB_LIVE_COUNT=1
PFM_FENCE_PROFILE=desktop PFM_FENCE_CPUS=4 PFM_FENCE_MEM=8g init
out="$( { fence_shared_ensure && printf 'effective=%s|%s|%s\n' "$FENCE_SH_CPUS" "$FENCE_SH_MEM" "$FENCE_SH_CPUSET"; } 2>&1)"; rc=$?
lacks "a live run prevents caps changing beneath it" 'docker update'
if [[ $rc == 0 && "$out" == *'fence: CAPS CONFLICT requested cpus=4 mem=8g cpuset= — running under live cpus=6 mem=5368709120 cpuset= (another run is live)'* ]]; then ok "caps conflict is loud and names requested and effective caps"; else bad "caps conflict is loud and names requested and effective caps" "$out"; fi
[[ "$out" == *'effective=6|5368709120|'* ]] && ok "caps conflict forwards effective caps, never requested ones" || bad "caps conflict effective receipt" "$out"
unset STUB_LIVE_COUNT

# Effective bytes from Docker must survive the same validation used inside shared-run.sh.
if PFM_FENCE_MEM=5368709120 fence_config_values; then ok "effective byte memory passes in-run validation"; else bad "effective byte memory validation"; fi
reset; STUB_INSPECT='true 6000000000 5368709120'; STUB_UPDATE_NOOP=1
PFM_FENCE_CPUS=4 PFM_FENCE_MEM=8g init
out="$( { fence_shared_ensure && printf 'effective=%s|%s\n' "$FENCE_SH_CPUS" "$FENCE_SH_MEM"; } 2>&1)"; rc=$?
if [[ $rc == 0 && "$out" == *'update did not apply every requested cap'* && "$out" == *'effective=6|5368709120'* ]]; then ok "successful no-op updates report inspected caps, never requested ones"; else bad "no-op update receipt" "$out"; fi
for clear in quota cpuset; do
  reset
  if [[ "$clear" == quota ]]; then STUB_INSPECT='true 6000000000 5368709120'; PFM_FENCE_CPUS=0 init
  else STUB_INSPECT='true 6000000000 5368709120 1-8'; PFM_FENCE_CPUS=6 PFM_FENCE_CPUSET='' init; fi
  out="$(fence_shared_ensure 2>&1)"; rc=$?
  if [[ $rc == 0 && "$out" == *'recreating idle'* ]] && grep -q '^docker rm -f ' "$REC" && grep -q '^docker run ' "$REC" && ! grep -q '^docker update ' "$REC"; then ok "idle $clear clear recreates instead of issuing a Docker no-op"; else bad "idle $clear clear" "$out" "$(cat "$REC")"; fi
  unset PFM_FENCE_CPUS PFM_FENCE_CPUSET
 done
reset; STUB_PS=$'pfm-fence-old-key\npfm-fence-new-key'
dead="$FENCE_SH_HOST/.runs/20261018T000000Z-999999-987"; mkdir -p "$dead"
_fence_sh_reap_runs > "$T/reap-keys.log" 2>&1
if [[ ! -d "$dead" ]] && grep -q '^docker exec pfm-fence-old-key ' "$REC" && grep -q '^docker exec pfm-fence-new-key ' "$REC"; then ok "dead caller reservations are reaped across all image keys"; else bad "cross-key reap" "$(cat "$REC")"; fi

# Detection is Docker facts, never the host name; explicit profile avoids the probe.
for facts in 'Docker Desktop|10|8267812044|desktop' 'Docker Desktop|24|126701535232|desktop' 'Ubuntu Linux|23|126701535232|native' 'Ubuntu Linux|8|126701535232|desktop' 'Debian Linux|23|5368709120|desktop'; do
  want="${facts##*|}"; STUB_INFO="${facts%|*}"
  if got="$(PFM_FENCE_PROFILE=auto fence_config_init && printf '%s' "$PFM_FENCE_PROFILE")" && [[ "$got" == "$want" ]]; then ok "Docker facts select $want"; else bad "Docker facts select $want" "$got"; fi
done
out="$(PFM_FENCE_PROFILE=native STUB_INFO=broken fence_config_init 2>&1)"; rc=$?
[[ $rc == 0 ]] && ok "explicit profile wins over unreadable Docker facts" || bad "explicit profile precedence" "$out"
out="$(PFM_FENCE_PROFILE=auto STUB_INFO=broken fence_config_init 2>&1)"; rc=$?
[[ $rc != 0 && "$out" == *'PFM_FENCE_PROFILE'* ]] && ok "failed auto detection is not a silent profile fallback" || bad "failed auto detection" "$out"
for assignment in 'PFM_FENCE_CPUS=0' 'PFM_FENCE_CPUSET=1-8,10' 'PFM_FENCE_GOMAXPROCS=8'; do
  if env PFM_FENCE_PROFILE=desktop "$assignment" bash -c '. "$1"; fence_config_init' _ "$SUT"; then ok "valid override $assignment"; else bad "valid override $assignment"; fi
done

# Hold the first run between reservation and supervisor registration. A competing caller
# must see the reservation, not an empty machine whose caps it may change.
reset; STUB_INSPECT='true 6000000000 5368709120'; STUB_LIVE_DIR="$T/reservations"; mkdir -p "$STUB_LIVE_DIR"
PFM_FENCE_CPUS=4 PFM_FENCE_MEM=8g init
FENCE_SH_PENDING_ID=admit-first fence_shared_ensure >"$T/admit-first.log" 2>&1; first_rc=$?
PFM_FENCE_CPUS=8 PFM_FENCE_MEM=12g init
out="$(FENCE_SH_PENDING_ID=admit-second fence_shared_ensure 2>&1)"; rc=$?
if [[ $first_rc == 0 && $rc == 0 && "$(grep -c '^docker update' "$REC")" == 1 && -f "$STUB_LIVE_DIR/admit-first" && -f "$STUB_LIVE_DIR/admit-second" && "$out" == *'CAPS CONFLICT'* && ! -e "$FENCE_SH_LOCK" ]]; then
  ok "admission reservation closes the cap-update versus registration race"
else bad "admission reservation closes the cap-update versus registration race" "$out" "$(cat "$REC")"; fi
unset STUB_LIVE_DIR


# Admission reserves PID 1 before setup; a setup error must release it before discarding
# the host record, or no subsequent reaper can discover the abandoned reservation.
for setup_failure in leak-copy fifo; do
  reset; STUB_INSPECT='true 6000000000 5368709120'
  STUB_LIVE_DIR="$T/setup-$setup_failure-live"; mkdir -p "$STUB_LIVE_DIR"
  PFM_FENCE_CPUS=6 PFM_FENCE_MEM=5g init
  setup_host="$T/setup-$setup_failure-host"; mkdir -p "$setup_host/.runs" "$setup_host/$FENCE_SH_WT"
  echo private > "$T/setup-terms"
  out="$(
    FENCE_SH_HOST="$setup_host"
    if [[ "$setup_failure" == leak-copy ]]; then
      FENCE_SH_TERMS="$T/setup-terms"
      cp() { return 1; }
    else
      FENCE_SH_TERMS=''
      mkfifo() { return 1; }
    fi
    PFM_DEV_ENGINE_SYNC_SRC="$MAIN/plugins/engine-sync" PFM_DEV_GIT_DIR_REL=worktrees/wt1 fence_shared_exec private true
  )"; rc=$?
  if [[ $rc != 0 && -z "$(find "$STUB_LIVE_DIR" -type f)" && -z "$(find "$setup_host/.runs" -mindepth 1 -maxdepth 1)" ]] &&
    ! grep -q 'shared-run.sh.*run' "$REC"; then
    ok "$setup_failure failure after admission releases reservation and host record without starting a run"
  else bad "$setup_failure failure after admission releases reservation" "rc=$rc $out" "$(find "$STUB_LIVE_DIR" -type f)" "$(cat "$REC")"; fi
  unset STUB_LIVE_DIR
done

# The measured native profile must resolve without carrying machine-specific CPU IDs.
out="$(env PFM_FENCE_PROFILE=native bash -c '
. "$1"
unset PFM_FENCE_CPUS PFM_FENCE_MEM PFM_FENCE_CPUSET PFM_FENCE_GOMAXPROCS PFM_FENCE_TEST_P STEPS_JOBS STEPS_HEAVY_JOBS
fence_config_init || exit $?
printf "%s|%s|%s|%s|%s|%s|%s" "$PFM_FENCE_CPUS" "$PFM_FENCE_MEM" "$PFM_FENCE_CPUSET" "$PFM_FENCE_GOMAXPROCS" "$PFM_FENCE_TEST_P" "$STEPS_JOBS" "$STEPS_HEAVY_JOBS"
' _ "$SUT" 2>&1)"; rc=$?
[[ $rc == 0 && "$out" == '12|12g|||6|6|2' ]] && ok "native defaults match the measured portable bounded scheduler" || bad "measured native defaults" "rc=$rc $out"

# Different fixture defaults exercise the real resolver without shipping a tuning or a seam.
profile_fixture='
. "$1"
fence_config_schema() { printf "%s\n" "PFM_FENCE_CPUS|6|cpu|8" "PFM_FENCE_MEM|5g|size|=" "PFM_FENCE_GOMAXPROCS|4|optional_int|"; }
unset PFM_FENCE_CPUS PFM_FENCE_MEM PFM_FENCE_GOMAXPROCS
[[ "${2:-}" != override ]] || PFM_FENCE_CPUS=6
[[ "${2:-}" != empty ]] || PFM_FENCE_GOMAXPROCS=""
fence_config_init || exit $?
printf "%s|%s|%s|%s" "$PFM_FENCE_CPUS" "$PFM_FENCE_MEM" "$PFM_FENCE_GOMAXPROCS" "$(_fence_config_default PFM_FENCE_CPUS)"
'
for spec in desktop:6:4:default native:8::default native:6::override desktop:6::empty; do
  IFS=: read -r profile want_cpu want_go mode <<< "$spec"
  out="$(env PFM_FENCE_PROFILE="$profile" bash -c "$profile_fixture" _ "$SUT" "$mode" 2>&1)"; rc=$?
  helper_cpu=6; [[ "$profile" != native ]] || helper_cpu=8
  if [[ $rc == 0 && "$out" == "$want_cpu|5g|$want_go|$helper_cpu" ]]; then
    ok "$profile selects its own defaults, inheritance, empty native default and $mode precedence"
  else bad "$profile profile-specific defaults ($mode)" "rc=$rc $out"; fi
done
out="$(PFM_FENCE_PROFILE=native bash -c '. "$1"; fence_config_schema() { echo "PFM_FENCE_CPUS|6|cpu|broken"; }; unset PFM_FENCE_CPUS; fence_config_init' _ "$SUT" 2>&1)"; rc=$?
[[ $rc != 0 && "$out" == *'PFM_FENCE_CPUS must be cpu'* ]] && ok "selected native default is validated by name" || bad "selected native default validation" "rc=$rc $out"

# Every declared knob has a valid default and rejects a supplied broken value by name.
while IFS='|' read -r knob default kind native; do
  out="$(env "$knob=invalid!" bash -c '. "$1"; fence_config_values' _ "$SUT" 2>&1)"; rc=$?
  if [[ $rc != 0 && "$out" == *"$knob must be"* ]]; then ok "schema rejects malformed $knob"; else bad "schema rejects malformed $knob" "$out"; fi
done < <(fence_config_schema)

shtest_end

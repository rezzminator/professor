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
    inspect) # $T/gone: while it exists, the container has stopped and gone (`run --rm`)
      [ ! -e "$T/gone" ] || return 1
      local insp="${STUB_INSPECT:-}"; [ ! -e "$T/inspect.after" ] || insp="$(cat "$T/inspect.after")"
      [ -n "$insp" ] || return 1
      if [ "$2" = -f ] && [ "$3" = '{{.State.Running}}' ]; then echo "${insp%% *}"; else echo "$insp"; fi ;;
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
    update) return "${STUB_UPDATE_RC:-0}" ;;
    run) if [ -e "$T/run.inuse" ]; then rm -f "$T/run.inuse"; [ -z "${STUB_INUSE_INSPECT:-}" ] || echo "$STUB_INUSE_INSPECT" >"$T/inspect.after"; echo 'Conflict. The container name is already in use'; return 125; fi
         [ "${STUB_RUN_RC:-0}" = 0 ] || { echo 'boom: no such image'; return "$STUB_RUN_RC"; }; echo cid ;;
    ps) printf '%s\n' "${STUB_PS:-}" ;;
    exec)
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
  REPO_ROOT="${1:-$MAIN}"; fence_shared_init
}
reset() { : >"$REC"; unset STUB_INSPECT STUB_RUN_RC STUB_UPDATE_RC STUB_PS STUB_KTAG_LABEL STUB_IMG STUB_IMG_AFTER_BUILD STUB_BUILD_RC STUB_KTAGS STUB_INUSE_INSPECT STUB_AFTER_SLEEP; FENCE_IMAGE_BUILD=(); rm -f "$T/exec.rcs" "$T/run.inuse" "$T/gone" "$T/inspect.after"; }
has() { if grep -qF -- "$2" "$REC"; then ok "$1"; else bad "$1" "no docker call with: $2" "$(cat "$REC")"; fi; }
lacks() { if grep -qF -- "$2" "$REC"; then bad "$1" "a docker call with: $2" "$(cat "$REC")"; else ok "$1"; fi; }
PFM_DEV_INPUTS_KEY=abcdef0123456789abcdef; FENCE_IMAGE_BUILD=(); FENCE_SH_COMPOSE="$T/docker-compose.yml"

# --- init: knobs and paths ---------------------------------------------------------------------------------
for bad in 'PFM_FENCE_CPUS=two' 'PFM_FENCE_MEM=5 GB' 'PFM_FENCE_TEST_P=0'; do
  out="$(env "$bad" bash -c '. "$1"; REPO_ROOT="$2" PFM_DEV_GIT_COMMON="$2/.git" fence_shared_init' _ "$SUT" "$MAIN" 2>&1)"; rc=$?
  if [ "$rc" != 0 ] && grep -q "^iso: ${bad%%=*} must be" <<<"$out"; then ok "$bad is refused, named"; else bad "$bad refusal" "rc=$rc" "$out"; fi
done
init
if [ "$FENCE_SH_HOST" = "/tmp/$PROJ/fence/shared" ] && [ "$FENCE_SH_LOCK" = "/tmp/$PROJ/fence/shared.lock.d" ] && [ "$FENCE_SH_WT" = "$PROJ" ] && [ -d "$FENCE_SH_HOST/$PROJ/timing" ]; then
  ok "init derives the host directory, lock and worktree name from the main checkout minus its dot"
else bad "init paths" "$FENCE_SH_HOST $FENCE_SH_LOCK $FENCE_SH_WT"; fi
if [ "$FENCE_SH_CPUS $FENCE_SH_MEM" = '6 5g' ]; then ok "the caps default to 6 CPUs and 5g"; else bad "default caps" "$FENCE_SH_CPUS $FENCE_SH_MEM"; fi
PFM_FENCE_CPUS=3.5 PFM_FENCE_MEM=6144m init
if [ "$FENCE_SH_CPUS $FENCE_SH_MEM" = '3.5 6144m' ]; then ok "PFM_FENCE_CPUS and PFM_FENCE_MEM set the caps per call"; else bad "knob caps" "$FENCE_SH_CPUS $FENCE_SH_MEM"; fi
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
has "other caps are applied to the running container with docker update" "docker update --cpus 4 --memory 8g --memory-swap 8g $FENCE_SH_NAME"
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

shtest_end

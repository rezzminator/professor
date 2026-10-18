#!/usr/bin/env bash
# infra/fence/shared.sh, the shared fence container's host side, against a docker stub: the knob
# validation and paths fence_shared_init derives, fence_shared_map's three answers, fence_shared_ensure
# (a running container with this call's caps is entered as it is; other caps are applied with docker
# update, a refused update is a WARN and the run goes on; no container starts one with the caps, the
# labels and the mounts; the lock is released on every path; a dead holder's lock is broken; a name
# still in use is retried; a start that fails is a named iso line; the container runs its key's own
# image tag, made from an image whose label was read, rebuilt when another checkout moved the plain
# tag; stale keys' tags are removed; a new image key supersedes the root's other containers), and fence_shared_exec (the shared-run.sh argv, the run's env, exit 75
# before the run started retried once in a fresh container, an engine-sync source outside the roots
# refused). docker is a shell function recording its argv; no container or image is touched.
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
docker() { # STUB_INSPECT: the inspect answer (empty = no container); STUB_RUN_RC, STUB_UPDATE_RC; $T/run.inuse (one in-use refusal), $T/exec.rcs (space list) — files, as docker runs in $( ) subshells
  { printf 'docker'; printf ' %q' "$@"; printf '\n'; } >>"$REC"
  case "$1" in
    inspect) [ -n "${STUB_INSPECT:-}" ] || return 1; echo "$STUB_INSPECT" ;;
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
    run) if [ -e "$T/run.inuse" ]; then rm -f "$T/run.inuse"; echo 'Conflict. The container name is already in use'; return 125; fi
         [ "${STUB_RUN_RC:-0}" = 0 ] || { echo 'boom: no such image'; return "$STUB_RUN_RC"; }; echo cid ;;
    ps) printf '%s\n' "${STUB_PS:-}" ;;
    exec)
      local rcs rc
      rcs="$(cat "$T/exec.rcs" 2>/dev/null)"; rc="${rcs%% *}"; [ -n "$rc" ] || rc=0
      if [ "$rcs" = "${rcs#* }" ]; then : >"$T/exec.rcs"; else echo "${rcs#* }" >"$T/exec.rcs"; fi
      return "$rc" ;;
  esac
}
sleep() { :; }
PFM_DEV_GIT_COMMON="$MAIN/.git"
init() { # init [tree]: a fresh fence_shared_init from MAIN (or the given tree)
  REPO_ROOT="${1:-$MAIN}"; fence_shared_init
}
reset() { : >"$REC"; unset STUB_INSPECT STUB_RUN_RC STUB_UPDATE_RC STUB_PS STUB_KTAG_LABEL STUB_IMG STUB_IMG_AFTER_BUILD STUB_BUILD_RC STUB_KTAGS; FENCE_IMAGE_BUILD=(); rm -f "$T/exec.rcs" "$T/run.inuse"; }
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
reset; STUB_RUN_RC=125
out="$(fence_shared_ensure 2>&1)"; rc=$?
if [ "$rc" = 1 ] && grep -qF "iso: the shared fence container $FENCE_SH_NAME did not start: boom: no such image" <<<"$out" && [ ! -e "$FENCE_SH_LOCK" ]; then
  ok "a failed start is a named iso line, return 1, the lock released"
else bad "failed start" "rc=$rc" "$out"; fi
reset; : >"$T/run.inuse"
fence_shared_ensure >/dev/null 2>&1; rc=$?
if [ "$rc" = 0 ] && [ "$(grep -c '^docker run' "$REC")" = 2 ]; then ok "a name still in use is retried"; else bad "in-use retry" "rc=$rc" "$(cat "$REC")"; fi
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

reset; STUB_PS="pfm-fence-old oldkey
$FENCE_SH_NAME $PFM_DEV_INPUTS_KEY"
out="$(fence_shared_ensure 2>&1)"
has "a new image key supersedes the root's other container" 'docker exec pfm-fence-old touch /var/fence/superseded'
lacks "a container never supersedes itself" "docker exec $FENCE_SH_NAME touch"
has "the supersede lists this root's containers only" "docker ps --filter label=pfm.fence.shared=$FENCE_SH_ROOTS"

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
reset; STUB_INSPECT='true 6000000000 5368709120'
out="$(PFM_DEV_ENGINE_SYNC_SRC="$T/elsewhere" PFM_DEV_GIT_DIR_REL=worktrees/wt1 fence_shared_exec shared 'true' 2>&1)"; rc=$?
if [ "$rc" = 1 ] && grep -q '^iso: the engine-sync source .* is outside the shared container' <<<"$out"; then ok "an engine-sync source outside the roots is refused, named"; else bad "engine-sync refusal" "rc=$rc" "$out"; fi
lacks "a refused engine-sync source never reaches docker exec" 'docker exec'

shtest_end

#!/usr/bin/env bash
# Fixture-driven tests for `.claude/scripts/dev.sh iso`'s doors into the shared fence container:
# every action but sim enters the one container with `docker exec` of infra/fence/shared-run.sh
# (install alone, and run under PFM_FENCE_NET=shared, in the shared network; every other action and
# run by default in the run's own); a running container is entered with no
# housekeeping and no start; a missing one has its cache volumes ensured, is started once, and only
# then is housekeeping run, detached; a stale image is built before the start; the container's name
# and label carry the image key; iso gate is one exec of the egress recorder, then the ledger ingest,
# its status passed through, and refuses a target; a set knob is forwarded, an unset one left out;
# iso sim keeps its own compose run with the caches ensured first and the sim key. docker is a stub
# and dev.sh runs from a throwaway git repo, so nothing is built or run.
#
#   bash infra/fence/lanes/tests/iso-housekeeping_test.sh
#   DEV_SH_SUT=/tmp/new/dev.sh bash …/iso-housekeeping_test.sh   # a candidate dev.sh
#
# BROKEN STATE: a missing SUT exits 2 before any assertion.
set -uo pipefail

REPO="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../../.." && pwd -P)"
SUT="${DEV_SH_SUT:-$REPO/.claude/scripts/dev.sh}"
SHTEST_TAG=iso-housekeeping-test
# shellcheck source=/dev/null
source "$REPO/scripts/shtest.sh"
shtest_unset_gate_env
[ -f "$SUT" ] || { echo "iso-housekeeping_test: no dev.sh at $SUT" >&2; exit 2; }

FXNAME="isofx-$$"
R="$T/.$FXNAME"
shtest_clean_also "/tmp/$FXNAME"
mkdir -p "$R/.claude/scripts" "$R/pfm/scripts" "$R/infra/fence/lanes"
cp "$SUT" "$R/.claude/scripts/dev.sh"
cp "$REPO/pfm/scripts/repo-git.sh" "$R/pfm/scripts/"
cp "$REPO/infra/fence/housekeeping.sh" "$REPO/infra/fence/fence-env.sh" "$REPO/infra/fence/image-key.sh" "$REPO/infra/fence/docker-compose.yml" \
  "$REPO/infra/fence/steps.sh" "$REPO/infra/fence/stepprof.sh" "$REPO/infra/fence/checks.sh" "$REPO/infra/fence/gate-history.sh" \
  "$REPO/infra/fence/shared.sh" "$REPO/infra/fence/shared-run.sh" "$REPO/infra/fence/.dockerignore" \
  "$REPO/infra/fence/engine-sync.sh" "$REPO/infra/fence/tools.env" "$REPO/infra/fence/tools.sh" "$R/infra/fence/"
cp -R "$REPO/infra/fence/jscpd" "$R/infra/fence/"
printf 'FROM scratch\n' >"$R/infra/fence/pfm-dev.Dockerfile"
# A fixture volume makes a hard-coded copy of today's list fail the order check.
sed -i 's/^\(FENCE_CACHE_VOLUMES="[^"]*\)"/\1 fixture-cache"/' "$R/infra/fence/housekeeping.sh"
printf 'printf "%%s\\n" aaa\n' >"$R/infra/fence/lanes/root.sh"
(cd "$R" && git init -q && git add -A && git -c user.email=t@t -c user.name=t commit -qm fixture)

BIN="$T/bin"
mkdir -p "$BIN"
cat >"$BIN/docker" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$STUB_DOCKER_LOG"
case "$1 ${2:-}" in
  "volume inspect") [ "${STUB_VOLUME:-1}" = 1 ] || { echo "Error response from daemon: no such volume" >&2; exit 1; } ;;
  "compose -f")
    if [[ "$*" == *' config '* ]]; then
      service=pfm-dev; [[ "$*" == *' config pfm-sim' ]] && service=pfm-sim
      cat <<EOF
services:
  $service:
    build:
      context: $STUB_CONTEXT
      dockerfile: pfm-dev.Dockerfile
      target: $service
      labels:
        pfm.fence.inputs: unkeyed
    image: professor-$service
EOF
    elif [[ "$*" == *' run '* || "$*" == *' build '* ]]; then
      printf 'KEY %s\n' "${PFM_DEV_INPUTS_KEY:-unset}" >>"$STUB_DOCKER_LOG"
      # a build labels the plain tag with the key it was given
      [[ "$*" != *' build '* ]] || printf '%s\n' "${PFM_DEV_INPUTS_KEY:-unset}" >"$STUB_BUILT"
      exit "${STUB_RUN_RC:-0}"
    fi ;;
  "image inspect") # the plain tag's label: the last build's key, else STUB_LABEL; no k- tag exists
    label="$(cat "$STUB_BUILT" 2>/dev/null)"; label="${label:-${STUB_LABEL:-unkeyed}}"
    case "${*: -1}" in *:k-*) exit 1 ;; esac
    if [[ "$*" == *'{{.Id}}'* ]]; then printf 'sha256:stub %s\n' "$label"; else printf '%s\n' "$label"; fi ;;
  "inspect -f") [ -n "${STUB_RUNNING:-}" ] || exit 1; echo 'true 6000000000 5368709120' ;;
  "run -d") echo cid ;;
  "exec -i"|"exec -it") exit "${STUB_RUN_RC:-0}" ;;
esac
exit 0
STUB
chmod +x "$BIN/docker"
export PATH="$BIN:$PATH" STUB_DOCKER_LOG="$T/docker.log" STUB_BUILT="$T/image.built" STUB_CONTEXT="$R/infra/fence" PFM_FENCE_STAMP_DIR="$T/stamp"
export PFM_GATE_HISTORY_DIR="$T/ledger"

iso() { : >"$STUB_DOCKER_LOG"; rm -f "$STUB_BUILT"; OUT="$(bash "$R/.claude/scripts/dev.sh" iso "$@" 2>&1)"; RC=$?; }
line_of() { grep -n -- "$1" "$STUB_DOCKER_LOG" | head -1 | cut -d: -f1; }
ensured_before() { # ensured_before <docker line regexp>
  local e c volumes
  volumes="$(sed -n 's/^FENCE_CACHE_VOLUMES="\([^"]*\)"/\1/p' "$R/infra/fence/housekeeping.sh")"
  e="$(line_of "^volume inspect $volumes\$")"; c="$(line_of "$1")"
  [ -n "$e" ] && [ -n "$c" ] && [ "$e" -lt "$c" ]
}
exec_mode() { sed -n 's|^exec -i.*/shared-run\.sh run [^ ]* \([a-z]*\) .*|\1|p' "$STUB_DOCKER_LOG" | head -1; }
wait_housekept() { # the detached housekeeping writes `image prune` once it ran: up to 10 s
  local i
  for ((i = 0; i < 100; i++)); do grep -q '^image prune' "$STUB_DOCKER_LOG" && return 0; sleep 0.1; done
  return 1
}

# --- a running container: entered, never started, no housekeeping ---------------------------------------
export STUB_RUNNING=1
for a in status "run true"; do
  # shellcheck disable=SC2086
  iso $a
  if [ "$RC" -eq 0 ] && grep -q '^exec -i ' "$STUB_DOCKER_LOG" && ! grep -q '^run -d' "$STUB_DOCKER_LOG" && ! grep -q '^image prune' "$STUB_DOCKER_LOG"; then
    ok "iso $a: a running shared container is entered with docker exec — no start, no housekeeping"
  else bad "iso $a" "rc=$RC" "$OUT" "$(cat "$STUB_DOCKER_LOG")"; fi
done
# install alone fetches from outside, so it alone shares the container's network; every other action, and
# `iso run` unless PFM_FENCE_NET=shared opts in, gets its own namespace: a fixed loopback port never collides.
iso install
if [ "$(exec_mode)" = shared ]; then ok "iso install runs in the shared network"; else bad "iso install network" "$(exec_mode)" "$(cat "$STUB_DOCKER_LOG")"; fi
for a in status build typecheck verify test e2e cover all gate "run true"; do
  # shellcheck disable=SC2086
  iso $a
  if [ "$(exec_mode)" = private ]; then ok "iso $a runs in its own network namespace"; else bad "iso $a network" "$(exec_mode)" "$(cat "$STUB_DOCKER_LOG")"; fi
done
PFM_FENCE_NET=shared iso run true
if [ "$(exec_mode)" = shared ]; then ok "iso run under PFM_FENCE_NET=shared runs in the shared network"; else bad "iso run shared" "$(cat "$STUB_DOCKER_LOG")"; fi
PFM_FENCE_NET=bogus iso run true
if [ "$RC" -eq 2 ] && grep -qF 'iso run: PFM_FENCE_NET must be private or shared: bogus' <<<"$OUT"; then ok "a bad PFM_FENCE_NET is refused, exit 2"; else bad "bad PFM_FENCE_NET" "rc=$RC" "$OUT"; fi

# steps.sh sources stepprof.sh from its own directory: a fixture without it dies at dev.sh's source line.
iso status
if [ "$RC" -eq 0 ] && ! grep -q 'cannot source' <<<"$OUT"; then ok "dev.sh sources steps.sh with the stepprof.sh the fixture holds"
else bad "dev.sh sources steps.sh" "rc=$RC" "$OUT"; fi

# --- iso gate -----------------------------------------------------------------------------------------
iso gate
if [ "$RC" -eq 0 ] && [ "$(grep -c '^exec -i ' "$STUB_DOCKER_LOG")" -eq 1 ] \
  && grep -q '^exec -i .*bash -c .*bash infra/fence/egress\.sh run \./\.claude/scripts/dev\.sh gate all$' "$STUB_DOCKER_LOG" \
  && grep -q '^gate-history:' <<<"$OUT"; then
  ok "iso gate: one exec of the egress recorder, then the ledger ingest"
else bad "iso gate" "rc=$RC" "$OUT" "$(cat "$STUB_DOCKER_LOG")"; fi
export STUB_RUN_RC=3
iso gate
unset STUB_RUN_RC
if [ "$RC" -eq 3 ] && grep -q '^gate-history:' <<<"$OUT"; then
  ok "iso gate red: the ledger ingest still runs and dev.sh exits with the gate's own status"
else bad "iso gate red" "rc=$RC (want 3)" "$OUT"; fi
iso gate pfm
if [ "$RC" -eq 2 ] && grep -qF 'usage: dev.sh iso gate — the full gate runs pfm and templates together' <<<"$OUT" && ! grep -q '^exec' "$STUB_DOCKER_LOG"; then
  ok "iso gate refuses a target, exit 2, before any exec"
else bad "iso gate pfm" "rc=$RC" "$OUT"; fi

# --- knobs -------------------------------------------------------------------------------------------------
export STEPS_HEAVY_JOBS=3
unset PFM_TEST_PROFILE
iso run true
unset STEPS_HEAVY_JOBS
if [ "$RC" -eq 0 ] && grep -q -- '-e STEPS_HEAVY_JOBS=3 ' "$STUB_DOCKER_LOG" && ! grep -q 'PFM_TEST_PROFILE' "$STUB_DOCKER_LOG"; then
  ok "iso forwards a set profiling knob and leaves an unset one out"
else bad "iso knobs" "rc=$RC" "$OUT" "$(cat "$STUB_DOCKER_LOG")"; fi
PFM_FENCE_TEST_P=3 iso run true
if grep -q -- '-e PFM_FENCE_TEST_P=3 ' "$STUB_DOCKER_LOG"; then ok "PFM_FENCE_TEST_P reaches the run"; else bad "PFM_FENCE_TEST_P" "$(cat "$STUB_DOCKER_LOG")"; fi
PFM_FENCE_CPUS=two iso run true
if [ "$RC" -ne 0 ] && grep -qF 'iso: PFM_FENCE_CPUS must be a number of CPUs: two' <<<"$OUT" && ! grep -q '^exec' "$STUB_DOCKER_LOG"; then
  ok "a bad PFM_FENCE_CPUS is refused before any exec"
else bad "bad PFM_FENCE_CPUS" "rc=$RC" "$OUT"; fi

# --- no container: caches, start, then housekeeping detached ------------------------------------------------
unset STUB_RUNNING
# shellcheck source=/dev/null
. "$R/infra/fence/image-key.sh"
dev_key="$(fence_image_key "$R/infra/fence/docker-compose.yml" pfm-dev)"
iso run true
if [ "$RC" -eq 0 ] && ensured_before '^run -d' && grep -q '^compose .* build pfm-dev' "$STUB_DOCKER_LOG" \
  && [ "$(line_of '^compose .* build pfm-dev')" -lt "$(line_of '^run -d')" ] && grep -qx "KEY $dev_key" "$STUB_DOCKER_LOG" \
  && grep -q 'rebuilds — inputs ' <<<"$OUT"; then
  ok "no container, a stale image: the caches ensured, the image built with its key, then the container started"
else bad "iso run, no container, stale image" "rc=$RC" "$OUT" "$(cat "$STUB_DOCKER_LOG")"; fi
if grep -q "^run -d .*--name pfm-fence-[0-9a-f]\{12\}-${dev_key:0:12} .*--label pfm.fence.inputs=$dev_key " "$STUB_DOCKER_LOG"; then
  ok "the container's name and label carry the image key"
else bad "container key" "$(grep '^run -d' "$STUB_DOCKER_LOG")"; fi
if wait_housekept && [ "$(line_of '^run -d')" -lt "$(line_of '^image prune')" ]; then ok "housekeeping runs once the container started, detached"
else bad "detached housekeeping" "$(cat "$STUB_DOCKER_LOG")"; fi
export STUB_LABEL="$dev_key"
iso run true
unset STUB_LABEL
if [ "$RC" -eq 0 ] && grep -q "fence image: professor-pfm-dev current (inputs ${dev_key:0:12})" <<<"$OUT" && ! grep -q '^compose .* build' "$STUB_DOCKER_LOG"; then
  ok "a current image is not rebuilt"
else bad "iso run current image" "rc=$RC" "$OUT" "$(cat "$STUB_DOCKER_LOG")"; fi
STUB_VOLUME=0 iso status
if [ "$RC" -eq 0 ] && grep -qx 'volume create pfm-dev-gocache' "$STUB_DOCKER_LOG" && grep -qx 'volume create pfm-dev-lintcache' "$STUB_DOCKER_LOG" && grep -qx 'volume create fixture-cache' "$STUB_DOCKER_LOG"; then
  ok "absent caches are created before the container starts"
else bad "caches created" "rc=$RC" "$(cat "$STUB_DOCKER_LOG")"; fi

# --- sim keeps its own compose run -----------------------------------------------------------------------
sim_key="$(fence_image_key "$R/infra/fence/docker-compose.yml" pfm-sim)"
iso sim true
if [ "$RC" -eq 0 ] && grep -qx "KEY $sim_key" "$STUB_DOCKER_LOG" && ensured_before '^compose .* run ' && ! grep -q '^exec' "$STUB_DOCKER_LOG"; then
  ok "iso sim: its own compose run with the sim-stage key, the caches ensured first"
else bad "iso sim" "rc=$RC" "$OUT" "$(cat "$STUB_DOCKER_LOG")"; fi

shtest_end

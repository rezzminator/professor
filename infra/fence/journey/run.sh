#!/usr/bin/env bash
# run.sh — pfm's journey suite on a disposable Linux machine: builds the machine image once per input
# hash (machine.Dockerfile), fetches Go modules into the kept cache volumes, boots the machine with
# systemd and no network, prepares the owner (prepare.sh) and runs the suite inside it as the owner.
#   bash infra/fence/journey/run.sh [--keep] [go test flags…]     e.g. -run 'Journey/install/day/plain-pfm$'
# Prints the go test output as it comes and ends with `RUN DIR: {path}`; exits with the suite's status.
# Every run, green or red, is profiled and its coverage of pfm's source measured (pfm/journey's TestMain
# writes steps.tsv, waits.tsv, summary.md and the coverage-* files into the artifacts, the raw counters in
# covdata/): the artifacts land in {tree}/tmp/journey/{run id}, `latest` linking the newest; the last
# KEEP_PROFILES runs stay there.
# Every container, directory and artifact is named for this run, so two runs never collide. The
# container goes on exit; the run dir goes on a pass, and stays on a failure or with --keep.
# Kept across runs: the machine image (one: an older tag is removed when a new one is built) and the
# volumes pfm-journey-gomod and pfm-journey-gocache.
# BROKEN STATE: `journey: FAIL {stage}: {cause}` and a non-zero exit before the suite runs; a machine
# that does not boot prints systemd's state and its failed units.
set -euo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
ROOT="$(cd -- "$HERE/../../.." && pwd -P)"
# shellcheck source=scripts/platform.sh
. "$ROOT/scripts/platform.sh"

KEEP=0
if [ "${1:-}" = --keep ]; then KEEP=1; shift; fi
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)-$$"
# The tree's git directory: a worktree shares the main checkout's, and the scratch root carries that checkout's name.
common="$(git -C "$ROOT" rev-parse --path-format=absolute --git-common-dir)" ||
  { echo "journey: FAIL setup: $ROOT is not a git checkout" >&2; exit 1; }
PROJECT="$(basename "$(dirname "$common")")"
RUN_DIR="/tmp/${PROJECT#.}/journey/$RUN_ID"
NAME="pfm-journey-$RUN_ID"
BOOT_SECS=90
PROFILES="$ROOT/tmp/journey"
KEEP_PROFILES=10
fail() { echo "journey: FAIL $*" >&2; exit 1; }
# stage prints how long the stage just finished took, from the previous mark.
mark=$SECONDS
stage() { echo "journey: $1 $((SECONDS - mark))s"; mark=$SECONDS; }

# publish copies the run's artifacts into the tree's ignored tmp folder, links them as latest and
# removes the profiles past the newest KEEP_PROFILES (run ids start with their UTC time, so they sort
# by age). A step that fails prints its own line and leaves the run's status alone.
publish() {
  [ -n "$(ls -A "$RUN_DIR/artifacts" 2>/dev/null)" ] || return 0
  if ! { mkdir -p "$PROFILES" && cp -R "$RUN_DIR/artifacts" "$PROFILES/$RUN_ID" && ln -sfn "$RUN_ID" "$PROFILES/latest"; }; then
    echo "journey: warning: cleanup: cannot publish the artifacts into $PROFILES/$RUN_ID (above)" >&2
    return 0
  fi
  if [ -f "$PROFILES/$RUN_ID/summary.md" ]; then
    echo "journey: profile $PROFILES/$RUN_ID/summary.md"
  else
    echo "journey: profile $PROFILES/$RUN_ID holds no summary.md: the suite ended before its TestMain wrote one (above)"
  fi
  find "$PROFILES" -mindepth 1 -maxdepth 1 -type d -name '[0-9]*' | LC_ALL=C sort |
    awk -v keep="$KEEP_PROFILES" '{ old[NR] = $0 } END { for (i = 1; i <= NR - keep; i++) print old[i] }' |
    while IFS= read -r old; do
      rm -rf "$old" || echo "journey: warning: cleanup: cannot remove the old profile $old (above)" >&2
    done
}

# cleanup hands the artifacts the machine's owner wrote back to whoever runs this script, from inside the
# machine as root (the owner's uid need not be the caller's, as on CI's runner), then removes what this
# run made. A cleanup step that fails prints its own line; the suite's status stays the exit status.
cleanup() {
  local rc=$?
  set +e
  # The machine's pfm logs travel with its results: each home's log roots (pfm's paths.LogRoot: the clone
  # its install recorded, the default clone once the uninstall took the record, and the state fallback)
  # into logs/{user}/{clone|state}/.
  if docker container inspect "$NAME" >/dev/null 2>&1 && ! docker exec -u 0 "$NAME" bash -c '
    for home in /home/*; do
      repo=$(cat "$home/.local/share/pfm/install/source-repo" 2>/dev/null) || repo="$home/.professor"
      for pair in "clone:$repo/tmp/logs/pfm" "state:$home/.local/state/pfm/log"; do
        to="/artifacts/logs/${home##*/}/${pair%%:*}"
        [ ! -d "${pair#*:}" ] || { mkdir -p "$to" && cp -R "${pair#*:}/." "$to/"; } || exit 1
      done
    done'; then
    echo "journey: warning: cleanup: cannot copy the machine's pfm logs into $RUN_DIR/artifacts/logs (above)" >&2
  fi
  if docker container inspect "$NAME" >/dev/null 2>&1 &&
    ! docker exec -u 0 "$NAME" chown -R "$(id -u):$(id -g)" /artifacts; then
    echo "journey: warning: cleanup: cannot hand $RUN_DIR/artifacts back to uid $(id -u) (above)" >&2
  fi
  publish
  docker rm -f "$NAME" "$NAME-fetch" >/dev/null 2>&1
  rm -rf "$RUN_DIR/image" || echo "journey: warning: cleanup: cannot remove $RUN_DIR/image (above)" >&2
  if [ "$rc" -eq 0 ] && [ "$KEEP" -eq 0 ] && ! rm -rf "$RUN_DIR"; then
    echo "journey: warning: cleanup: cannot remove $RUN_DIR (above)" >&2
  fi
  echo "RUN DIR: $RUN_DIR"
  exit "$rc"
}
mkdir -p "$RUN_DIR/image" "$RUN_DIR/artifacts" || fail "setup: cannot create $RUN_DIR"
trap cleanup EXIT
# The suite writes its artifacts as the owner (uid 1000), whoever runs this script.
chmod 0777 "$RUN_DIR/artifacts"

# 1. The machine image, tagged by a hash of everything it is built from.
GO_VERSION="$(sed -n 's/^go //p' "$ROOT/pfm/go.mod")"
GO_ARCH="$(platform_arch)"
[ -n "$GO_VERSION" ] && [ "$GO_ARCH" != unknown ] ||
  fail "image: go version '$GO_VERSION' or architecture '$GO_ARCH' unreadable"
cp "$HERE/machine.Dockerfile" "$ROOT/infra/fence/pinned-tools.sh" "$RUN_DIR/image/"
hash="$(
  cd "$RUN_DIR/image" &&
    { printf '%s\n' "$GO_VERSION" "$GO_ARCH"; find . -type f | LC_ALL=C sort | while IFS= read -r f; do platform_sha256 "$f"; done; } |
    platform_sha256 | cut -c1-16
)" || fail "image: cannot hash the inputs"
IMAGE="pfm-journey-machine:$hash"
if ! docker image inspect "$IMAGE" >/dev/null 2>&1; then
  echo "journey: building $IMAGE"
  docker build -q -f "$RUN_DIR/image/machine.Dockerfile" \
    --build-arg "GO_VERSION=$GO_VERSION" --build-arg "GO_ARCH=$GO_ARCH" -t "$IMAGE" "$RUN_DIR/image" >/dev/null ||
    fail "image: docker build of $IMAGE failed (above)"
  docker images --format '{{.Repository}}:{{.Tag}}' pfm-journey-machine | { grep -vxF "$IMAGE" || true; } |
    xargs -r docker rmi >/dev/null ||
    echo "journey: warning: an older pfm-journey-machine image could not be removed (above); remove it once nothing uses it" >&2
fi
stage "image $IMAGE"

# The tree under test, mounted read-only at its own path, with its git directory when it is a worktree.
mounts=(-v "$ROOT:$ROOT:ro" -v pfm-journey-gomod:/home/tester/go/pkg/mod -v pfm-journey-gocache:/home/tester/.cache/go-build)
case "$common" in "$ROOT"/*) ;; *) mounts+=(-v "$common:$common:ro") ;; esac
owner=(-u 1000:1000 -e HOME=/home/tester -e USER=tester -e LOGNAME=tester -e SHELL=/usr/bin/zsh -e LANG=C.UTF-8
  -e PATH=/home/tester/.local/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin -e GOTOOLCHAIN=local)

# 2. Go modules, with the network, into the kept module cache.
docker run --rm --name "$NAME-fetch" "${mounts[@]}" "${owner[@]}" "$IMAGE" bash -c \
  'go -C "$1/pfm" mod download && go -C "$1/plugins/engine-sync" mod download' _ "$ROOT" ||
  fail "modules: go mod download failed (above)"
stage modules

# 3. The machine: systemd as PID 1, no network.
docker run -d --name "$NAME" --hostname journey --network none \
  --cgroupns=private --security-opt writable-cgroups=true --tmpfs /run --tmpfs /run/lock -e container=docker \
  "${mounts[@]}" -v "$RUN_DIR/artifacts:/artifacts" "$IMAGE" >/dev/null || fail "boot: docker run failed (above)"
deadline=$((SECONDS + BOOT_SECS))
while :; do
  read -r state user < <(docker exec "$NAME" bash -c \
    's="$(systemctl is-system-running 2>/dev/null)" u="$(systemctl is-active user@1000.service 2>/dev/null)"
    printf "%s %s\n" "${s:-unreachable}" "${u:-unknown}"' 2>/dev/null || echo "unreachable unknown")
  [ "$state" = running ] && [ "$user" = active ] && break
  if [ "$state" = degraded ] || [ "$SECONDS" -ge "$deadline" ]; then
    docker exec "$NAME" systemctl --failed --no-legend 2>&1 | head -n 20 >&2 || true
    fail "boot: systemd '$state', user@1000.service '$user' (bound ${BOOT_SECS}s)"
  fi
  sleep 0.5 # POLL-STEP: systemd's own state, bounded by BOOT_SECS
done
stage boot

session=("${owner[@]}" -e XDG_RUNTIME_DIR=/run/user/1000 -e DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus
  -e PFM_DEV_FENCE=1 -e GOPROXY=off -e GOCOVERDIR=/artifacts/covdata -w "$ROOT")

# 4. The owner's machine: fake engines, Herdr, the snapshot INSTALL.md clones.
docker exec "${session[@]}" "$NAME" \
  bash "$HERE/prepare.sh" "$ROOT" || fail "prepare: prepare.sh failed (above)"
stage prepare

# 5. The suite, as the owner.
rc=0
docker exec "${session[@]}" -e JOURNEY_ARTIFACTS=/artifacts "$NAME" \
  go -C "$ROOT/pfm" test -tags journey -count=1 -v -timeout 20m ./journey/ "$@" || rc=$?
stage suite
exit "$rc"

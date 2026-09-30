#!/usr/bin/env bash
# root.sh — builds or reuses the hermetic lane root, keyed by tree content.
#
#   root.sh [--rebuild] [--tag NAME] [--print-hash]
#
# From the pfm-dev fence image, in order:
#   1. provision.sh tools — pfm and the mock linked as all three engines
#   2. provision.sh seats — two fixture seats and fixture Codex/OpenCode homes
#   3. provision.sh install — pfm integration and invented local projects
#   4. adopt.sh — deterministic adoption of /work/express
#   5. pfm ls --plain — the fleet answers and is empty
#   6. cred-scan.sh — every credential is a registered fixture
#   7. docker commit — pfm-lane-root:<hash>
#
# The hash covers exactly the image's build inputs: product files (pfm/**,
# templates/**, VERSION, docs/SETUP.md, docs/PLACEHOLDERS.md, .githooks/pre-push),
# fence image files (pfm-dev.Dockerfile, docker-compose.yml, tools.env, tools.sh,
# fence-env.sh), and lane build files (root.sh, container.sh, provision.sh,
# adopt.sh, cred-scan.sh, fixtures/, scenarios/), with dirty diff and untracked
# content. Lane scripts, lib.sh, registries and tests load from /worktree at run
# time, so editing them reuses the root. Seats are lane inputs, not image inputs.
# The image carries fixture credentials only and stays LOCAL ONLY. A registry
# --tag is refused; this script never pushes.
#
# BROKEN STATE: an incomplete hash input is HASH-UNDERIVABLE (exit 2); missing
# docker or its daemon is TOOLCHAIN-MISSING (exit 2). Every failed build step
# names its exit status; no image is committed. EXIT removes the container and
# releases its private base pin once, including on INT/TERM/HUP (130/143/129).
set -uo pipefail

HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
ROOT="$(cd -- "$HERE/../../.." && pwd -P)"
REBUILD=0 PRINT_HASH=0 TAG=""
HASH_PATHS="pfm templates VERSION docs/SETUP.md docs/PLACEHOLDERS.md .githooks/pre-push infra/fence/pfm-dev.Dockerfile infra/fence/docker-compose.yml infra/fence/tools.env infra/fence/tools.sh infra/fence/fence-env.sh infra/fence/lanes/root.sh infra/fence/lanes/container.sh infra/fence/lanes/provision.sh infra/fence/lanes/adopt.sh infra/fence/lanes/cred-scan.sh infra/fence/lanes/fixtures infra/fence/lanes/scenarios"

while [ $# -gt 0 ]; do
  case "$1" in
    --rebuild) REBUILD=1; shift ;;
    --tag) TAG="$2"; shift 2 ;;
    --print-hash) PRINT_HASH=1; shift ;;
    -h|--help) sed -n '2,10p' "$0"; exit 0 ;;
    *) echo "usage: root.sh [--rebuild] [--tag NAME] [--print-hash]" >&2; exit 2 ;;
  esac
done

fatal() { echo "root: $1" >&2; exit 2; }
say() { echo "root: $1"; }

sha256_stdin() {
  if command -v sha256sum >/dev/null; then sha256sum
  elif command -v shasum >/dev/null; then shasum -a 256
  else echo "root: TOOLCHAIN-MISSING — neither sha256sum nor shasum" >&2; return 1
  fi
}

# The hash inputs, in one stream: index entries (content-addressed), the dirty
# diff against HEAD, and every untracked file's blob hash. A failure in ANY of
# the three is fatal — a hash over a partial list would silently reuse a stale image.
hash_inputs() {
  git -C "$ROOT" ls-files -s -- $HASH_PATHS || return 1
  git -C "$ROOT" diff HEAD -- $HASH_PATHS || return 1
  git -C "$ROOT" ls-files -o --exclude-standard -- $HASH_PATHS |
    while IFS= read -r f; do
      printf '%s %s\n' "$f" "$(git -C "$ROOT" hash-object "$ROOT/$f" 2>/dev/null || echo UNHASHABLE)"
    done
}

root_hash() {
  local out
  out="$(hash_inputs 2>&1)" || { echo "root: HASH-UNDERIVABLE: $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-200)" >&2; return 2; }
  [ -n "$out" ] || { echo "root: HASH-UNDERIVABLE: the input list for [$HASH_PATHS] is empty" >&2; return 2; }
  printf '%s' "$out" | sha256_stdin | cut -c1-12
}

HASH="$(root_hash)" || exit 2
IMAGE="pfm-lane-root:$HASH"
if [ -n "$TAG" ]; then
  # A push-capable tag is one docker would resolve to a registry: it carries a
  # repository path (`/`) or a host-looking first segment (`host.tld:port/...`).
  # The root is local only, so a registry tag is refused.
  case "$TAG" in
    */*) fatal "--tag '$TAG' names a repository path — the root image is local only and is never pushed" ;;
  esac
  case "${TAG%%:*}" in
    *.*) fatal "--tag '$TAG' names a registry host — the root image is local only and is never pushed" ;;
  esac
  case "$TAG" in
    *:*) IMAGE="$TAG" ;;
    *) IMAGE="$TAG:$HASH" ;;
  esac
fi

if [ "$PRINT_HASH" -eq 1 ]; then
  printf '%s\n' "$HASH"
  exit 0
fi

command -v docker >/dev/null || fatal "TOOLCHAIN-MISSING — docker"
docker info >/dev/null 2>&1 || fatal "TOOLCHAIN-MISSING — the docker daemon is not reachable ('docker info' failed)"

T0="$(date +%s)"
if [ "$REBUILD" -eq 0 ] && docker image inspect "$IMAGE" >/dev/null 2>&1; then
  say "reusing $IMAGE (same hash: no root build input changed) · $(( $(date +%s) - T0 ))s"
  printf '%s\n' "$IMAGE"
  exit 0
fi

say "building $IMAGE — no image for this hash yet$( [ "$REBUILD" -eq 1 ] && printf ' (--rebuild)')"
BUILD="pfm-lane-build-$HASH"
docker rm -f "$BUILD" >/dev/null 2>&1

# The fence image, its mounts and the container's env live in one place for both
# root.sh and run.sh — lanes/container.sh.
# shellcheck source=container.sh
. "$HERE/container.sh"
FENCE_CALLER=lanes-root lane_fence_env "$ROOT"
# shellcheck source=../housekeeping.sh
. "$HERE/../housekeeping.sh"
fence_housekeeping "$HASH"
fence_volumes_ensure
# The build container goes first, then the pin it held — on every exit path
# (commit, step failure, fatal, Ctrl-C), so pfm-lane-base:<hash> never outlives
# its build.
cleanup() { docker rm -f "$BUILD" >/dev/null 2>&1; lane_base_release "$HASH"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP
BASE="$(lane_base_image "$ROOT" "$HASH")" || fatal "the pfm-dev fence image could not be built"
say "base image $BASE (pinned for this build, so a concurrent dev.sh iso rebuild cannot orphan it)"
lane_run "$BUILD" "$BASE" || fatal "the fence container would not start from $BASE"

step_failed() { # step_failed <step> <exit>
  echo "root: ✗ $1 failed (exit $2) — output above; no image was tagged" >&2
  exit 1
}

x() { docker exec -w /tmp "$@"; }

say "step 1/7 · pfm and mock engines (provision.sh tools)"
x "$BUILD" bash /worktree/infra/fence/lanes/provision.sh tools || step_failed "provision.sh tools" $?

say "step 2/7 · two fixture seats (provision.sh seats)"
x "$BUILD" bash /worktree/infra/fence/lanes/provision.sh seats || step_failed "provision.sh seats" $?

say "step 3/7 · pfm integration (provision.sh install)"
x "$BUILD" bash /worktree/infra/fence/lanes/provision.sh install || step_failed "provision.sh install" $?

say "step 4/7 · express adoption (adopt.sh)"
x "$BUILD" bash /worktree/infra/fence/lanes/adopt.sh || step_failed "adopt.sh" $?

say "step 5/7 · the fleet answers and is empty"
rows="$(x "$BUILD" bash -c 'pfm ls --plain 2>&1')" || step_failed "pfm ls --plain" $?
live="$(printf '%s\n' "$rows" | grep -cE '^[●↻]' || true)"
if [ "$live" -ne 0 ]; then
  echo "root: ✗ the root must have an EMPTY fleet, $live row(s) found:" >&2
  printf '%s\n' "$rows" >&2
  step_failed "empty fleet" 1
fi
say "fleet: 0 row(s)"

say "step 6/7 · registered fixture credential scan"
scan="$(x "$BUILD" bash /worktree/infra/fence/lanes/cred-scan.sh /root /tmp /home 2>&1)" || step_failed "cred-scan.sh — $scan" $?
printf '%s\n' "$scan"

say "step 7/7 · commit"
docker commit --change "LABEL professor.lane-root=$HASH" --change 'CMD ["sleep","infinity"]' "$BUILD" "$IMAGE" >/dev/null || step_failed "docker commit" $?
say "$IMAGE committed in $(( $(date +%s) - T0 ))s — LOCAL ONLY: fixture credentials only"
printf '%s\n' "$IMAGE"

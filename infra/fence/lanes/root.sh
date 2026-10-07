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
# adopt.sh, cred-scan.sh, fixtures/, scenarios/). It hashes on-disk content,
# identically whether dirty, staged or committed; test files, testdata and the
# e2e tree are excluded. Lane scripts, lib.sh and registries load from /worktree
# at run time, so editing them reuses the root. Seats are lane inputs, not image inputs.
# The image carries fixture credentials only and stays LOCAL ONLY. A registry
# --tag is refused; this script never pushes.
#
# BROKEN STATE: an incomplete hash input is HASH-UNDERIVABLE (exit 2); missing
# docker or its daemon is TOOLCHAIN-MISSING (exit 2). Every failed build step
# names its exit status; no image is committed. A lost containerd lease at
# commit is retried on the same container at most three times, with diagnostics
# for each lost attempt. EXIT removes the container and releases its private
# base pin once, including on INT/TERM/HUP (130/143/129).
set -uo pipefail

HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
ROOT="$(cd -- "$HERE/../../.." && pwd -P)"
REBUILD=0 PRINT_HASH=0 TAG=""
HASH_PATHS="pfm templates VERSION docs/SETUP.md docs/PLACEHOLDERS.md .githooks/pre-push infra/fence/pfm-dev.Dockerfile infra/fence/docker-compose.yml infra/fence/tools.env infra/fence/tools.sh infra/fence/jscpd infra/fence/fence-env.sh infra/fence/lanes/root.sh infra/fence/lanes/container.sh infra/fence/lanes/provision.sh infra/fence/lanes/adopt.sh infra/fence/lanes/cred-scan.sh infra/fence/lanes/fixtures infra/fence/lanes/scenarios"

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

# One path, its on-disk git blob hash and executable bit per line; a symlink
# carries its target text instead, as git stores it, dangling or not. A failed
# listing or hash is fatal: a partial stream could silently reuse a stale image.
hash_inputs() {
  local paths files hashes path blob executable
  # shellcheck disable=SC2086 # HASH_PATHS is the fixed, space-delimited input roster.
  paths="$(git -C "$ROOT" ls-files --cached --others --exclude-standard -- $HASH_PATHS \
    ':(exclude)pfm/**/*_test.go' ':(exclude)pfm/*_test.go' \
    ':(exclude)pfm/**/testdata/**' ':(exclude)pfm/testdata/**' \
    ':(exclude)pfm/e2e/**' \
    ':(exclude)pfm/internal/harvestpy/assets/**/*_test.py' \
    ':(exclude)pfm/internal/harvestpy/assets/*_test.py' |
    LC_ALL=C sort -u |
    while IFS= read -r path; do
      if [ -f "$ROOT/$path" ] || [ -L "$ROOT/$path" ]; then printf '%s\n' "$path"; fi
    done)" || return 1
  [ -n "$paths" ] || return 0
  files="$(while IFS= read -r path; do [ -L "$ROOT/$path" ] || printf '%s\n' "$path"; done <<<"$paths")"
  hashes=""
  if [ -n "$files" ]; then
    hashes="$(printf '%s\n' "$files" | git -C "$ROOT" hash-object --stdin-paths)" || return 1
  fi
  while IFS= read -r path; do
    if [ -L "$ROOT/$path" ]; then
      printf '%s\tlink:%s\t0\n' "$path" "$(readlink "$ROOT/$path")"
      continue
    fi
    IFS= read -r blob <&3 || return 1
    executable=0
    [ -x "$ROOT/$path" ] && executable=1
    printf '%s\t%s\t%s\n' "$path" "$blob" "$executable"
  done 3<<<"$hashes" <<<"$paths"
}

root_hash() {
  local out
  out="$(hash_inputs 2>&1)" || { echo "root: HASH-UNDERIVABLE: $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-200)" >&2; return 2; }
  [ -n "$out" ] || { echo "root: HASH-UNDERIVABLE: the input list for [$HASH_PATHS] is empty" >&2; return 2; }
  printf '%s' "$out" | sha256_stdin | cut -c1-12
}

HASH="$(root_hash)" || exit 2
IMAGE="pfm-lane-root:$HASH"
SUFFIX="${BASHPID}${RANDOM}${RANDOM}"
BUILD="pfm-lane-build-$HASH-$SUFFIX"
PIN="pfm-lane-base:$HASH-$SUFFIX"
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
PROJECT="$(basename "$ROOT")"; PROJECT="${PROJECT#.}"
LANE_TMP="/tmp/$PROJECT/lanes"
mkdir -p "$LANE_TMP" || fatal "the lane scratch directory could not be created: $LANE_TMP"
STEP_LOG="$LANE_TMP/root-step-$SUFFIX.log"
: >"$STEP_LOG" || fatal "the step output file could not be written: $STEP_LOG"

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
# (commit, step failure, fatal, Ctrl-C), so this build's base pin never outlives
# its build.
cleanup() { docker rm -f "$BUILD" >/dev/null 2>&1; lane_base_release "$PIN"; rm -f "$STEP_LOG"; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP
BASE="$(lane_base_image "$ROOT" "$PIN")" || fatal "the pfm-dev fence image could not be built"
say "base image $BASE (pinned for this build, so a concurrent dev.sh iso rebuild cannot orphan it)"
lane_run "$BUILD" "$BASE" '' \
  -v pfm-lane-harvest-cache:/root/.local/state/pfm/harvest-python/cache \
  -v pfm-lane-uv-cache:/root/.cache/uv \
  -e UV_CACHE_DIR=/root/.cache/uv -e UV_LINK_MODE=copy || fatal "the fence container would not start from $BASE"

root_failure_diagnostics() { # root_failure_diagnostics <step> <exit> <output-file> [attempt] [retry]
  local dir stamp file
  dir="$LANE_TMP/root-failures"
  mkdir -p "$dir" || return 1
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  file="$dir/$HASH-$stamp-$SUFFIX${4:+-attempt$4}.txt"
  (
    set -e
    printf 'failed step: %s\nexit code: %s\ntarget image: %s\nbuild container: %s\nbase pin: %s\n' "$1" "$2" "$IMAGE" "$BUILD" "$PIN"
    printf 'daemon error output:\n'; cat "$3"
    # Retry diagnostics cannot issue another Docker request between commits.
    if [ "${5:-}" = retry ]; then exit 0; fi
    printf 'build container ID:\n'; docker inspect --format '{{.Id}}' "$BUILD" 2>&1 || true
    printf 'pinned base image ID:\n'; docker image inspect --format '{{.Id}}' "$PIN" 2>&1 || true
    printf 'daemon server version:\n'; docker info --format '{{.ServerVersion}}' 2>&1 || true
    printf 'daemon storage driver:\n'; docker info --format '{{.Driver}}' 2>&1 || true
    printf 'running pfm.fence=1 containers:\n'; docker ps --filter label=pfm.fence=1 --format '{{.ID}} {{.Names}} {{.CreatedAt}}' 2>&1 || true
  ) >"$file" || { rm -f "$file"; return 1; }
  printf '%s\n' "$file"
}

step_failed() { # step_failed <step> <exit>
  local diag detail
  detail="$(tail -n 1 "$STEP_LOG")"
  if diag="$(root_failure_diagnostics "$1" "$2" "$STEP_LOG" "${3:-}" 2>&1)"; then
    echo "root: ✗ $1 failed (exit $2) — $detail; no image was tagged; diagnostics: $diag" >&2
  else
    echo "root: ✗ $1 failed (exit $2) — $detail; no image was tagged; diagnostics could not be written: $diag" >&2
  fi
  exit 1
}

x() { docker exec -w /tmp "$@"; }
run_step() { # run_step <name> <command> [args…]
  local step="$1" rc=0
  shift
  "$@" >"$STEP_LOG" 2>&1 || rc=$?
  cat "$STEP_LOG"
  [ "$rc" -eq 0 ] || step_failed "$step" "$rc"
}

commit_lost_lease() { # commit_lost_lease <output-file>
  if grep -Fq 'failed to export layer: CreateDiff' "$1" &&
    grep -Fq 'failed to commit: rename' "$1" &&
    grep -Fq 'no such file or directory' "$1"; then return 0; fi
  grep -Fq 'failed to apply diff: failed to Lchown' "$1" &&
    grep -Fq 'no such file or directory' "$1"
}

say "step 1/7 · pfm and mock engines (provision.sh tools)"
run_step "provision.sh tools" x "$BUILD" bash /worktree/infra/fence/lanes/provision.sh tools

say "step 2/7 · two fixture seats (provision.sh seats)"
run_step "provision.sh seats" x "$BUILD" bash /worktree/infra/fence/lanes/provision.sh seats

say "step 3/7 · pfm integration (provision.sh install)"
run_step "provision.sh install" x "$BUILD" bash /worktree/infra/fence/lanes/provision.sh install

say "step 4/7 · express adoption (adopt.sh)"
run_step "adopt.sh" x "$BUILD" bash /worktree/infra/fence/lanes/adopt.sh

say "step 5/7 · the fleet answers and is empty"
run_step "pfm ls --plain" x "$BUILD" bash -c 'pfm ls --plain 2>&1'
rows="$(cat "$STEP_LOG")"
live="$(printf '%s\n' "$rows" | grep -cE '^[●↻]' || true)"
if [ "$live" -ne 0 ]; then
  echo "root: ✗ the root must have an EMPTY fleet, $live row(s) found:" >&2
  printf '%s\n' "$rows" >&2
  step_failed "empty fleet" 1
fi
say "fleet: 0 row(s)"

say "step 6/7 · registered fixture credential scan"
run_step "cred-scan.sh" x "$BUILD" bash /worktree/infra/fence/lanes/cred-scan.sh /root /tmp /home

say "step 7/7 · commit"
for attempt in 1 2 3; do
  rc=0
  docker commit --change "LABEL professor.lane-root=$HASH" --change 'CMD ["sleep","infinity"]' "$BUILD" "$IMAGE" >"$STEP_LOG" 2>&1 || rc=$?
  cat "$STEP_LOG"
  [ "$rc" -eq 0 ] && break
  commit_lost_lease "$STEP_LOG" || step_failed "docker commit" "$rc"
  [ "$attempt" -lt 3 ] || step_failed "docker commit (attempt 3/3)" "$rc" 3
  if diag="$(root_failure_diagnostics "docker commit (attempt $attempt/3)" "$rc" "$STEP_LOG" "$attempt" retry 2>&1)"; then
    echo "root: commit attempt $attempt/3 lost its containerd lease — committing the same container again; diagnostics: $diag" >&2
  else
    echo "root: ✗ docker commit (attempt $attempt/3) failed (exit $rc) — diagnostics could not be written: $diag" >&2
    exit 1
  fi
done
say "$IMAGE committed in $(( $(date +%s) - T0 ))s — LOCAL ONLY: fixture credentials only"
printf '%s\n' "$IMAGE"

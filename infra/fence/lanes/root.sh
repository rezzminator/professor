#!/usr/bin/env bash
# root.sh — builds (or reuses) the lane root image: ONE pre-warmed machine every
# lane run forks from, keyed by the hash of what can change its behaviour.
#
#   root.sh [--rebuild] [--no-adopt] [--accounts 1,2,3] [--tag NAME] [--print-hash]
#
# From the `pfm-dev` fence image, in order:
#   1. infra/demo/setup.sh tools    — pfm built from the mounted tree, the real
#                                     Claude Code + Codex + OpenCode, Starship
#   2. lanes/creds.sh --print-config → the container's pfm config (seats re-homed
#                                     on ~/.cc/<id>), then creds.sh stages the seats
#   3. infra/demo/setup.sh install  — `pfm install --yes`, the seats' first-run
#                                     state, themes, the MCP daemon, /work projects
#   4. infra/demo/adopt.sh          — express cloned, `pfm init`, the install
#                                     interview run by a REAL Claude chat (~10-15 min,
#                                     one seat). `--no-adopt` stops before it.
#   5. `pfm ls --plain`             — the fleet answers; with --no-adopt it is empty
#   6. docker commit                → pfm-lane-root:<hash>
#
# Steps 1-3 are the live demo's own scripts, not a second copy of them: the demo
# fence and the lane root are the same machine, built once.
#
# <hash> = sha256 over the tracked content of pfm/**, templates/**,
# docs/SETUP.md, infra/fence/** (`git ls-files -s`) PLUS the worktree's dirty
# diff and its untracked files there — so an uncommitted edit changes the hash
# and can never be served by a stale image. Same hash → the image is reused and
# said so by name; the ~15-minute interview is paid once per template change.
#
# The image carries real seat tokens. It is LOCAL ONLY: this script never runs
# `docker push`, and it refuses a --tag that names a registry (anything with a
# `/`, or a host-looking first segment), because such a tag exists to be pushed.
#
# --no-adopt is the no-live-turn build: the express interview AND the OpenCode
# liveness probe inside setup.sh install are skipped (lane E3's `need` prelude
# makes the OpenCode home when that lane runs). Use it to prove the build path
# without spending a model turn.
#
# BROKEN STATE: a git command that cannot produce the input list is
# `HASH-UNDERIVABLE: <why>` (exit 2) — never a hash over a partial list; docker
# missing or its daemon unreachable is TOOLCHAIN-MISSING (exit 2); every
# in-container step exits non-zero with its own output and the build container
# is removed, leaving no half-built image tagged as a root.
set -uo pipefail

HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
ROOT="$(cd -- "$HERE/../../.." && pwd -P)"
REBUILD=0 ADOPT=1 PRINT_HASH=0 TAG="" ACCOUNTS=""
HASH_PATHS="pfm templates docs/SETUP.md infra/fence"

while [ $# -gt 0 ]; do
  case "$1" in
    --rebuild) REBUILD=1; shift ;;
    --no-adopt) ADOPT=0; shift ;;
    --accounts) ACCOUNTS="$2"; shift 2 ;;
    --tag) TAG="$2"; shift 2 ;;
    --print-hash) PRINT_HASH=1; shift ;;
    -h|--help) sed -n '2,10p' "$0"; exit 0 ;;
    *) echo "usage: root.sh [--rebuild] [--no-adopt] [--accounts 1,2,3] [--tag NAME] [--print-hash]" >&2; exit 2 ;;
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
  # This image carries seat tokens, so such a tag is refused, not sanitized.
  case "$TAG" in
    */*) fatal "--tag '$TAG' names a repository path — the root image carries seat tokens and is never pushed" ;;
  esac
  case "${TAG%%:*}" in
    *.*) fatal "--tag '$TAG' names a registry host — the root image carries seat tokens and is never pushed" ;;
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
  say "reusing $IMAGE (same hash: pfm/**, templates/**, docs/SETUP.md, infra/fence/** unchanged) · $(( $(date +%s) - T0 ))s"
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
BASE="$(lane_base_image "$ROOT" "$HASH")" || fatal "the pfm-dev fence image could not be built"
say "base image $BASE (pinned, so a concurrent dev.sh iso rebuild cannot orphan it)"
lane_run "$BUILD" "$BASE" || fatal "the fence container would not start from $BASE"

cleanup() { docker rm -f "$BUILD" >/dev/null 2>&1; }
step_failed() { # step_failed <step> <exit>
  echo "root: ✗ $1 failed (exit $2) — output above; no image was tagged" >&2
  cleanup
  exit 1
}

x() { docker exec -w /tmp "$@"; }

say "step 1/6 · toolchain (infra/demo/setup.sh tools)"
x "$BUILD" bash /worktree/infra/demo/setup.sh tools || step_failed "setup.sh tools" $?

say "step 2/6 · container config + seat credentials (lanes/creds.sh)"
CC="$(bash "$HERE/creds.sh" --print-config ${ACCOUNTS:+--accounts "$ACCOUNTS"})" ||
  step_failed "creds.sh --print-config" $?
docker exec -i "$BUILD" sh -c 'mkdir -p /root/.config/pfm && cat > /root/.config/pfm/pfm.config.json' <<<"$CC" ||
  step_failed "writing the container pfm config" $?
say "config written with $(jq '.accounts | length' <<<"$CC") Claude seat(s) + 1 Codex home"
bash "$HERE/creds.sh" --container "$BUILD" ${ACCOUNTS:+--accounts "$ACCOUNTS"} || step_failed "creds.sh" $?

say "step 3/6 · pfm install (infra/demo/setup.sh install)"
if [ "$ADOPT" -eq 1 ]; then
  x "$BUILD" bash /worktree/infra/demo/setup.sh install || step_failed "setup.sh install" $?
else
  say "--no-adopt: the OpenCode liveness probe is SKIPPED (no model turn); lane E3's prelude makes that home"
  docker exec -w /tmp -e DEMO_OPENCODE_PROBE=0 "$BUILD" bash /worktree/infra/demo/setup.sh install ||
    step_failed "setup.sh install" $?
fi

if [ "$ADOPT" -eq 1 ]; then
  say "step 4/6 · express adopted by a real Claude chat (infra/demo/adopt.sh) — ~10-15 min, one seat"
  x "$BUILD" bash /worktree/infra/demo/adopt.sh || step_failed "adopt.sh" $?
else
  say "step 4/6 · SKIPPED by --no-adopt — /work/express carries no Professor install, so lane A cannot run from this image"
fi

say "step 5/6 · the fleet answers"
rows="$(x "$BUILD" bash -c 'pfm ls --plain 2>&1')" || step_failed "pfm ls --plain" $?
live="$(printf '%s\n' "$rows" | grep -c '^●' || true)"
if [ "$ADOPT" -eq 0 ] && [ "$live" -ne 0 ]; then
  echo "root: ✗ a --no-adopt root must have an EMPTY fleet, $live live row(s) found:" >&2
  printf '%s\n' "$rows" >&2
  cleanup
  exit 1
fi
say "fleet: $live live row(s) · $(printf '%s\n' "$rows" | grep -c . ) listing line(s)"

say "step 6/6 · commit"
if ! docker commit --change "LABEL professor.lane-root=$HASH" --change 'CMD ["sleep","infinity"]' "$BUILD" "$IMAGE" >/dev/null; then
  echo "root: ✗ docker commit failed — if it named a missing content digest, the base image was replaced under this container while it built; lanes/container.sh pins it as pfm-lane-base:$HASH to prevent exactly that, so re-run root.sh and keep other fence builds out of the window" >&2
  cleanup
  exit 1
fi
cleanup
say "$IMAGE committed in $(( $(date +%s) - T0 ))s — LOCAL ONLY: it carries seat tokens and is never pushed"
printf '%s\n' "$IMAGE"

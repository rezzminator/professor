#!/usr/bin/env bash
# container.sh — sourced by root.sh and run.sh: ONE definition of how a lane
# container is started, so the image the root is built in and the image the
# lanes run in are the same machine.
#
#   . "$HERE/container.sh"
#   lane_base_image <root> <pin-tag> # prepare the fence image and pin it privately
#   lane_base_release <pin-tag>     # drop that pin once the root build ends
#   lane_run <name> <image> [network] [docker-run args…] # detached container
#
# Why the private tag: the fence image `professor-pfm-dev` is rebuilt on this
# host whenever its build inputs change (infra/fence/image-key.sh), and under
# docker's containerd image store a
# rebuild re-points the tag and drops the old manifest's content — after which
# `docker commit` of a container created from it fails with
# `NotFound: content digest … not found` (observed 2026-09-17, mid-build, while
# another session used the fence). Tagging the built image as
# `pfm-lane-base:<hash>-<suffix>` gives that manifest a reference of our own, so a
# concurrent rebuild can never pull the floor out from under a root build.
#
# The mount and env contract is the fence's (infra/fence/docker-compose.yml),
# resolved through infra/fence/fence-env.sh like every other caller: the
# worktree read-only at /worktree, the common git dir read-only, the build
# caches as named volumes (never committed — docker commit excludes mounts).
# PFM_PRICES_OFFLINE=1 keeps pfm install, doctor and model-cost from fetching
# prices in any lane step. Every lane container carries --label pfm.fence=1, so
# fence housekeeping (infra/fence/housekeeping.sh) reaps it once exited or past
# its age limit.
#
# BROKEN STATE: a failing build or run prints docker's own message and returns
# non-zero; neither function ever falls back to a host-local execution.

. "$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)/image-key.sh"

lane_fence_env() { # resolve PFM_DEV_* once, from the worktree root
  local root="$1"
  ROOT="$root" FENCE_CALLER="${FENCE_CALLER:-lanes}" . "$root/infra/fence/fence-env.sh"
}

lane_base_image() { # lane_base_image <root> <pin-tag> — prints the pinned base tag
  local root="$1" base="$2"
  fence_image_prepare "$root/infra/fence/docker-compose.yml" pfm-dev || return 1
  if [ "${#FENCE_IMAGE_BUILD[@]}" -ne 0 ]; then
    docker compose -f "$root/infra/fence/docker-compose.yml" build pfm-dev >&2 || return 1
  fi
  docker tag professor-pfm-dev "$base" || return 1
  printf '%s\n' "$base"
}

lane_base_release() { # lane_base_release <pin-tag> — drops the pin once the build window closes
  # The pin only guards the build container's lifetime; the committed root
  # image keeps the layers it needs. `rmi` without -f only untags a shared
  # image and refuses one a container still uses. Never fails the caller.
  docker rmi "$1" >/dev/null 2>&1 || true
}

lane_run() { # lane_run <name> <image> [network] [docker-run args…]
  local name="$1" image="$2" network="${3:-}"
  shift 2
  [ $# -eq 0 ] || shift
  if [ "$network" = none ]; then set -- --network none -e GOPROXY=off "$@"
  elif [ -n "$network" ]; then set -- --network "$network" "$@"; fi
  docker run -d --init --name "$name" --label pfm.fence=1 \
    "$@" \
    -v "$PFM_DEV_WORKTREE:/worktree:ro" \
    -v "$PFM_DEV_GIT_COMMON:/pfm-git-common:ro" \
    -v pfm-dev-gocache:/root/.cache/go-build \
    -v pfm-dev-gomod:/root/go/pkg/mod \
    -w /worktree \
    -e PFM_DEV_FENCE=1 -e IS_SANDBOX=1 -e LANG=C.UTF-8 -e GOFLAGS=-buildvcs=false \
    -e PFM_CONFIG=/root/.local/state/pfm/pfm.config.json \
    -e MOCK_ENGINE_SCENARIO=/root/.local/share/pfm-lanes/default.json \
    -e PFM_PRICES_OFFLINE=1 \
    -e "PFM_DEV_REPO_GIT_DIR=/pfm-git-common/$PFM_DEV_GIT_DIR_REL" \
    -e PFM_DEV_REPO_WORK_TREE=/worktree \
    "$image" sleep infinity >/dev/null
}

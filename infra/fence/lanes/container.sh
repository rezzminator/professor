#!/usr/bin/env bash
# container.sh — sourced by root.sh and run.sh: ONE definition of how a lane
# container is started, so the image the root is built in and the image the
# lanes run in are the same machine.
#
#   . "$HERE/container.sh"
#   lane_base_image <hash>          # build the fence image and pin it privately
#   lane_run <name> <image>         # start a detached lane container from it
#
# Why the private tag: the fence image `professor-pfm-dev` is rebuilt by every
# `dev.sh iso` run on this host, and under docker's containerd image store a
# rebuild re-points the tag and drops the old manifest's content — after which
# `docker commit` of a container created from it fails with
# `NotFound: content digest … not found` (observed 2026-09-17, mid-build, while
# another session used the fence). Tagging the built image as
# `pfm-lane-base:<hash>` gives that manifest a reference of our own, so a
# concurrent rebuild can never pull the floor out from under a root build.
#
# The mount and env contract is the fence's (infra/fence/docker-compose.yml),
# resolved through infra/fence/fence-env.sh like every other caller: the
# worktree read-only at /worktree, the common git dir read-only, the build
# caches as named volumes (never committed — docker commit excludes mounts).
#
# BROKEN STATE: a failing build or run prints docker's own message and returns
# non-zero; neither function ever falls back to a host-local execution.

lane_fence_env() { # resolve PFM_DEV_* once, from the worktree root
  local root="$1"
  ROOT="$root" FENCE_CALLER="${FENCE_CALLER:-lanes}" . "$root/infra/fence/fence-env.sh"
}

lane_base_image() { # lane_base_image <root> <hash> — prints the pinned base tag
  local root="$1" base="pfm-lane-base:$2"
  docker compose -f "$root/infra/fence/docker-compose.yml" build pfm-dev >&2 || return 1
  docker tag professor-pfm-dev "$base" || return 1
  printf '%s\n' "$base"
}

lane_run() { # lane_run <name> <image> — a detached lane container, fence contract
  local name="$1" image="$2"
  docker run -d --init --name "$name" \
    -v "$PFM_DEV_WORKTREE:/worktree:ro" \
    -v "$PFM_DEV_GIT_COMMON:/pfm-git-common:ro" \
    -v pfm-dev-gocache:/root/.cache/go-build \
    -v pfm-dev-gomod:/root/go/pkg/mod \
    -v pfm-dev-npm-cache:/root/.npm \
    -w /worktree \
    -e PFM_DEV_FENCE=1 -e IS_SANDBOX=1 -e LANG=C.UTF-8 -e GOFLAGS=-buildvcs=false \
    -e "PFM_DEV_REPO_GIT_DIR=/pfm-git-common/$PFM_DEV_GIT_DIR_REL" \
    -e PFM_DEV_REPO_WORK_TREE=/worktree \
    "$image" sleep infinity >/dev/null
}

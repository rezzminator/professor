#!/usr/bin/env bash
# Fence housekeeping, run on the HOST before a fence build — sourced by
# .claude/scripts/dev.sh (iso) and infra/fence/lanes/root.sh.
#
#   fence_housekeeping      # both steps below; never fails its caller
#
# fence_prune_dangling: `compose run --build` retags professor-pfm-{dev,sim}
# whenever the build context changes and leaves the previous image dangling
# (~3 GB each). Only dangling images of the fence compose project go; docker
# skips any image a container still uses.
#
# fence_gocache_budget: Go trims its build cache only by age (entries unused
# for 5 days), never by size, so busy fence days grow pfm-dev-gocache to tens
# of GB. Checked at most once a day; over PFM_FENCE_GOCACHE_MB (default 15000)
# its files are deleted — the cost is one cold build. Skipped while any
# container mounts the volume. Files only: the 00..ff directories stay, so a
# build that starts mid-clear still finds its cache layout.

FENCE_GOCACHE_VOLUME=pfm-dev-gocache

fence_prune_dangling() {
  docker image prune -f --filter label=com.docker.compose.project=fence >/dev/null 2>&1 \
    || echo "fence: WARN pruning dangling fence images failed" >&2
  return 0
}

fence_gocache_mounted() { [ -n "$(docker ps -q --filter "volume=$FENCE_GOCACHE_VOLUME")" ]; }

fence_gocache_budget() {
  local budget_mb="${PFM_FENCE_GOCACHE_MB:-15000}"
  local stamp="${PFM_FENCE_STAMP_DIR:-${TMPDIR:-/tmp}}/pfm-fence-gocache-budget.stamp"
  local used
  [ -n "$(find "$stamp" -mmin -1440 2>/dev/null)" ] && return 0
  docker volume inspect "$FENCE_GOCACHE_VOLUME" >/dev/null 2>&1 || return 0
  fence_gocache_mounted && return 0
  used="$(docker run --rm -v "$FENCE_GOCACHE_VOLUME:/c" alpine du -sm /c 2>/dev/null | cut -f1)"
  case "$used" in '' | *[!0-9]*)
    echo "fence: WARN could not size $FENCE_GOCACHE_VOLUME — budget check skipped" >&2
    return 0 ;;
  esac
  touch "$stamp" 2>/dev/null
  [ "$used" -gt "$budget_mb" ] || return 0
  fence_gocache_mounted && return 0
  echo "fence: go build cache ${used} MB > ${budget_mb} MB budget — clearing $FENCE_GOCACHE_VOLUME" >&2
  docker run --rm -v "$FENCE_GOCACHE_VOLUME:/c" alpine find /c -type f -delete >/dev/null 2>&1 \
    || echo "fence: WARN clearing $FENCE_GOCACHE_VOLUME failed" >&2
  return 0
}

fence_housekeeping() {
  fence_prune_dangling
  fence_gocache_budget
  return 0
}

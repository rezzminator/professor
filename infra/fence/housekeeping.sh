#!/usr/bin/env bash
# Fence housekeeping — sourced on the HOST before a fence image build, so stale
# fence containers, images and caches never pile up. Callers:
# .claude/scripts/dev.sh (the iso actions that build and test: install build
# typecheck verify test e2e cover all — never status, run, shell, sim),
# infra/fence/lanes/root.sh (before a root build, with its own hash),
# infra/fence/host-rehearsal.sh (every rehearsal) and
# infra/fence/release-rehearsal.sh (`up`). dev.sh and infra/demo/up.sh also
# call fence_volumes_ensure before every compose run.
#
#   fence_housekeeping [lane-hash]   # the five steps below, in order
#   fence_volumes_ensure             # the five shared cache volumes exist
#
# Only fence-owned objects are ever removed: containers and dangling images
# labelled pfm.fence=1 (pfm-dev.Dockerfile, docker-compose.yml and every fence
# `docker run` set it), lane images (label professor.lane-root, tag
# pfm-lane-base:*), pfm-dev-gocache, and volumes named ^(fence|infra)_pfm-dev-.
# No other container, image or volume on the host is ever named to docker.
#
#   containers      labelled, exited: removed; labelled, running and created
#                   more than PFM_FENCE_CONTAINER_MAX_HOURS (default 6) ago: forced
#                   out (a leaked lane or root build, a forgotten host rehearsal).
#                   A running container also labelled pfm.fence.long-lived=1 — an
#                   `iso shell`, the live demo, a release rehearsal mid-release —
#                   is never age-reaped: someone is using it, and another
#                   checkout's gate run must not end it.
#   images          dangling images labelled pfm.fence=1 — each rebuild's previous
#                   professor-pfm-{dev,sim} or lane root, ~3 GB each; docker skips
#                   any a container uses. A running lane root build skips the
#                   prune; an overlapping prune is a skip, not a WARN.
#   lane-images     lane roots whose hash is not current in any checkout, and
#                   pfm-lane-base:<hash> pins of such hashes no running
#                   pfm-lane-build-<hash> holds; `docker rmi` without -f, so a
#                   root a container still uses is kept. The current hashes are
#                   the argument (root.sh passes its own) plus
#                   lanes/root.sh --print-hash of every checkout
#                   `git worktree list` names (a missing one is skipped). A
#                   failing listing, an underivable checkout or an empty keep-set
#                   removes no lane image and WARNs the cause.
#   legacy-volumes  the compose-prefixed caches from before the volumes were
#                   named (fence_pfm-dev-*), removed once no container uses them.
#   gocache-budget  Go trims its build cache by age only, never by size. At most
#                   once a day pfm-dev-gocache is sized from `docker system df -v`
#                   (bounded to 120 s where the host has `timeout`) against
#                   PFM_FENCE_GOCACHE_MB (default 15000); over budget the volume
#                   is removed — `docker volume rm`, which the daemon refuses
#                   atomically while any container mounts it — and the next fence
#                   run recreates it empty: one cold build. The day's stamp lives
#                   in /tmp/{project}/fence/ ({project}: the main checkout's name
#                   minus a leading dot; PFM_FENCE_STAMP_DIR overrides).
#
# BROKEN STATE: housekeeping never changes its caller's exit status, whatever
# the caller's shell options (set -euo pipefail included), and never writes to
# stdout. Each failed step prints one stderr line
#   WARN fence housekeeping: {step} failed: {reason}
# and the later steps still run. An over-budget cache that could not be removed,
# or could not be sized, names the size or the failure and writes no stamp, so
# the next build retries; an underivable lane hash removes no lane image. Every
# removal is printed as `fence housekeeping: removed …`.

FENCE_HK_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" 2>/dev/null && pwd -P)" || FENCE_HK_DIR=""
FENCE_CACHE_VOLUMES="pfm-dev-gocache pfm-dev-gomod pfm-dev-lintcache pfm-lane-harvest-cache pfm-lane-uv-cache"
FENCE_GOCACHE_VOLUME=pfm-dev-gocache

_fence_hk_warn() { printf 'WARN fence housekeeping: %s failed: %s\n' "$1" "$2" >&2; }
_fence_hk_say() { printf 'fence housekeeping: %s\n' "$1" >&2; }
_fence_hk_line() { printf '%s' "$1" | tr '\n' ' ' | sed 's/  */ /g; s/ $//' | cut -c1-300; }
_fence_hk_posint() { case "$1" in '' | *[!0-9]*) return 1 ;; esac; [ "$((10#$1))" -gt 0 ]; }

# docker's RFC 3339 creation time → epoch seconds (GNU date, then BSD date).
_fence_hk_epoch() {
  local t="${1%%.*}"; t="${t%Z}Z"
  date -u -d "$t" +%s 2>/dev/null || date -u -j -f '%Y-%m-%dT%H:%M:%SZ' "$t" +%s 2>/dev/null
}

# A docker size ("708.9MB", "2.034GB", "0B") → whole MB, decimal units as docker prints them.
_fence_hk_mb() {
  printf '%s\n' "$1" | awk '
    match($0, /^[0-9]+(\.[0-9]+)?/) {
      n = substr($0, 1, RLENGTH); u = substr($0, RLENGTH + 1)
      f = (u == "B") ? 1e-6 : (u == "kB" || u == "KB") ? 1e-3 : (u == "MB") ? 1 : (u == "GB") ? 1e3 : (u == "TB") ? 1e6 : (u == "PB") ? 1e9 : -1
      if (f >= 0) { printf "%d\n", n * f + 0.5; ok = 1 }
    }
    END { exit !ok }'
}

_fence_hk_containers() {
  local max="${PFM_FENCE_CONTAINER_MAX_HOURS:-6}" ids out now name created ts age
  if ! _fence_hk_posint "$max"; then
    _fence_hk_warn containers "PFM_FENCE_CONTAINER_MAX_HOURS='$max' is not a positive integer — using 6"
    max=6
  fi
  max=$((10#$max))
  if ! ids="$(docker ps -a -q --filter label=pfm.fence=1 --filter status=exited 2>&1)"; then
    _fence_hk_warn containers "listing exited fence containers: $(_fence_hk_line "$ids")"
  elif [ -n "$ids" ]; then
    # shellcheck disable=SC2086 # container ids, one word each
    if out="$(docker rm $ids 2>&1)"; then _fence_hk_say "removed exited fence container(s) $(_fence_hk_line "$ids")"
    else _fence_hk_warn containers "docker rm of exited fence containers: $(_fence_hk_line "$out")"; fi
  fi
  if ! ids="$(docker ps -q --filter label=pfm.fence=1 2>&1)"; then
    _fence_hk_warn containers "listing running fence containers: $(_fence_hk_line "$ids")"; return
  fi
  [ -n "$ids" ] || return 0
  # shellcheck disable=SC2086 # container ids, one word each
  out="$(docker inspect -f '{{.Name}} {{.Created}} {{index .Config.Labels "pfm.fence.long-lived"}}' $ids 2>&1)"
  grep -q '^/' <<<"$out" || { _fence_hk_warn containers "docker inspect of running fence containers: $(_fence_hk_line "$out")"; return; }
  now="$(date +%s)"
  while read -r name created longlived; do
    case "$name" in /*) name="${name#/}" ;; *) continue ;; esac
    [ "$longlived" = 1 ] && continue
    if ! ts="$(_fence_hk_epoch "$created")"; then
      _fence_hk_warn containers "unparsable creation time '$created' of $name — kept"; continue
    fi
    age=$((now - ts))
    [ "$age" -gt $((max * 3600)) ] || continue
    if out="$(docker rm -f "$name" 2>&1)"; then _fence_hk_say "removed $name, running $((age / 3600))h > ${max}h"
    else _fence_hk_warn containers "docker rm -f $name: $(_fence_hk_line "$out")"; fi
  done <<<"$out"
}

_fence_hk_images() {
  local out builds
  if ! builds="$(docker ps --filter name=pfm-lane-build- --format '{{.Names}}' 2>&1)"; then
    _fence_hk_warn images "listing running lane builds, prune skipped: $(_fence_hk_line "$builds")"
    return 0
  fi
  if [ -n "$builds" ]; then
    _fence_hk_say "skipped the dangling fence image prune — lane root build(s) in flight: $(_fence_hk_line "$builds")"
    return 0
  fi
  if out="$(docker image prune -f --filter label=pfm.fence=1 2>&1)"; then
    case "$out" in *Deleted*) _fence_hk_say "removed dangling fence images — $(grep -i 'reclaimed' <<<"$out")" ;; esac
    return 0
  fi
  case "$out" in *"prune operation is already running"*) return 0 ;; esac
  _fence_hk_warn images "docker image prune: $(_fence_hk_line "$out")"
}

_fence_hk_rmi() { # _fence_hk_rmi <ref> — untag/remove without -f; in use is a keep
  local out
  if out="$(docker rmi "$1" 2>&1)"; then _fence_hk_say "removed image $1"; return 0; fi
  case "$out" in
    *"No such image"*) _fence_hk_say "image $1 already removed"; return 0 ;;
    *conflict* | *"is using"* | *"being used"*) _fence_hk_say "kept image $1 — a container still uses it"; return 0 ;;
  esac
  _fence_hk_warn lane-images "docker rmi $1: $(_fence_hk_line "$out")"
}

_fence_hk_has_line() { [[ $'\n'"$1"$'\n' == *$'\n'"$2"$'\n'* ]]; }

_fence_hk_lane_images() {
  local keep="${1:-}" repo out wt h bad all current ids pins builds id ref tag tag_hash
  # The keep-set: the argument plus every registered checkout's current hash.
  repo="$(cd -- "$FENCE_HK_DIR/../.." 2>/dev/null && pwd -P)" || repo="$FENCE_HK_DIR"
  if ! out="$(git -C "$repo" worktree list --porcelain 2>&1)"; then
    _fence_hk_warn lane-images "git worktree list in $repo failed, no lane image removed: $(_fence_hk_line "$out")"; return
  fi
  while IFS= read -r wt; do
    case "$wt" in worktree\ *) wt="${wt#worktree }" ;; *) continue ;; esac
    [ -f "$wt/infra/fence/lanes/root.sh" ] || continue
    if ! h="$(bash "$wt/infra/fence/lanes/root.sh" --print-hash 2>&1)"; then
      _fence_hk_warn lane-images "the lane-root hash of checkout $wt is underivable, no lane image removed: $(_fence_hk_line "$h")"; return
    fi
    keep="$keep"$'\n'"${h##*$'\n'}"
  done <<<"$out"
  keep="$(sort -u <<<"$keep")"
  keep="${keep#$'\n'}"
  [ -n "$keep" ] || { _fence_hk_warn lane-images "no checkout yields a lane-root hash, no lane image removed"; return; }
  bad=""
  while IFS= read -r h; do
    case "$h" in *[!0123456789abcdef]*) bad="$h"; break ;; esac
  done <<<"$keep"
  [ -z "$bad" ] || { _fence_hk_warn lane-images "'$bad' is not a lane-root hash, no lane image removed"; return; }
  all="$(docker image ls --filter label=professor.lane-root --format '{{.ID}} {{.Repository}}:{{.Tag}}' 2>&1)" ||
    { _fence_hk_warn lane-images "listing lane roots: $(_fence_hk_line "$all")"; return; }
  current=""
  while read -r h; do
    ids="$(docker image ls --filter "label=professor.lane-root=$h" --format '{{.ID}}' 2>&1)" ||
      { _fence_hk_warn lane-images "listing the lane root of $h: $(_fence_hk_line "$ids")"; return; }
    current="$current"$'\n'"$ids"
  done <<<"$keep"
  while read -r id ref; do
    [ -n "$id" ] || continue
    _fence_hk_has_line "$current" "$id" && continue
    [ "$ref" = "<none>:<none>" ] && ref="$id"
    _fence_hk_rmi "$ref"
  done <<<"$all"
  pins="$(docker image ls pfm-lane-base --format '{{.Tag}}' 2>&1)" ||
    { _fence_hk_warn lane-images "listing pfm-lane-base pins: $(_fence_hk_line "$pins")"; return; }
  builds="$(docker ps --filter name=pfm-lane-build- --format '{{.Names}}' 2>&1)" ||
    { _fence_hk_warn lane-images "listing running lane builds: $(_fence_hk_line "$builds")"; return; }
  while read -r tag; do
    case "$tag" in '' | "<none>") continue ;; esac
    tag_hash="${tag%%-*}"
    _fence_hk_has_line "$keep" "$tag_hash" && continue
    _fence_hk_has_line "$builds" "pfm-lane-build-$tag" && continue
    _fence_hk_rmi "pfm-lane-base:$tag"
  done <<<"$pins"
}

_fence_hk_legacy_volumes() {
  local names v out
  names="$(docker volume ls -q 2>&1)" || { _fence_hk_warn legacy-volumes "docker volume ls: $(_fence_hk_line "$names")"; return; }
  while read -r v; do
    if out="$(docker volume rm "$v" 2>&1)"; then _fence_hk_say "removed legacy volume $v"
    else
      case "$out" in *"in use"*) _fence_hk_say "kept legacy volume $v — a container still uses it" ;;
        *) _fence_hk_warn legacy-volumes "docker volume rm $v: $(_fence_hk_line "$out")" ;; esac
    fi
  done < <(grep -E '^(fence|infra)_pfm-dev-' <<<"$names")
}

_fence_hk_stamp_dir() { # /tmp/{project}/fence — {project} from the main checkout, so every worktree shares one stamp
  local repo common project
  if [ -n "${PFM_FENCE_STAMP_DIR:-}" ]; then printf '%s\n' "$PFM_FENCE_STAMP_DIR"; return; fi
  repo="$(cd -- "$FENCE_HK_DIR/../.." 2>/dev/null && pwd -P)" || repo="$FENCE_HK_DIR"
  if common="$(git -C "$repo" rev-parse --git-common-dir 2>/dev/null)"; then
    case "$common" in /*) ;; *) common="$repo/$common" ;; esac
    repo="$(cd -- "$common/.." 2>/dev/null && pwd -P)" || :
  fi
  project="$(basename -- "$repo")"
  printf '/tmp/%s/fence\n' "${project#.}"
}

_fence_hk_gocache_budget() {
  local budget="${PFM_FENCE_GOCACHE_MB:-15000}" v="$FENCE_GOCACHE_VOLUME" dir stamp out rc size used
  if ! _fence_hk_posint "$budget"; then
    _fence_hk_warn gocache-budget "PFM_FENCE_GOCACHE_MB='$budget' is not a positive integer — using 15000"
    budget=15000
  fi
  budget=$((10#$budget))
  dir="$(_fence_hk_stamp_dir)"; stamp="$dir/gocache-budget.stamp"
  [ -n "$(find "$stamp" -mmin -1440 2>/dev/null)" ] && return 0
  if ! out="$(docker volume inspect "$v" 2>&1)"; then
    case "$out" in *[Nn]"o such volume"*) return 0 ;; esac
    _fence_hk_warn gocache-budget "docker volume inspect $v: $(_fence_hk_line "$out")"; return
  fi
  local df=(docker system df -v --format '{{range .Volumes}}{{.Name}}\t{{.Size}}{{println}}{{end}}')
  command -v timeout >/dev/null 2>&1 && df=(timeout 120 "${df[@]}")
  out="$("${df[@]}" 2>&1)"; rc=$?
  if [ "$rc" -ne 0 ]; then
    [ "$rc" -eq 124 ] && out="timed out after 120 s"
    _fence_hk_warn gocache-budget "could not size $v (docker system df -v): $(_fence_hk_line "$out")"; return
  fi
  size="$(awk -F'\t' -v v="$v" '$1 == v { print $2; exit }' <<<"$out")"
  if ! used="$(_fence_hk_mb "$size")"; then
    _fence_hk_warn gocache-budget "could not size $v: docker system df -v reported '${size:-no row}'"; return
  fi
  if [ "$used" -gt "$budget" ]; then
    _fence_hk_say "go build cache $used MB > $budget MB budget — removing $v; the next fence run recreates it empty"
    if ! out="$(docker volume rm "$v" 2>&1)"; then
      _fence_hk_warn gocache-budget "$v is $used MB > $budget MB and was kept — docker volume rm: $(_fence_hk_line "$out")"; return
    fi
  fi
  out="$( { mkdir -p -- "$dir" && touch -- "$stamp"; } 2>&1)" ||
    _fence_hk_warn gocache-budget "the day's stamp $stamp could not be written (the check repeats next build): $(_fence_hk_line "$out")"
}

# Each public entry runs in a subshell with the caller's shell options reset, so
# no failure, unset variable or ERR trap inside it can end or fail the caller.
fence_housekeeping() { # fence_housekeeping [lane-hash]
  (
    set +eEu +o pipefail; trap - ERR; IFS=$' \t\n'
    _fence_hk_containers
    _fence_hk_images
    _fence_hk_lane_images "${1:-}"
    _fence_hk_legacy_volumes
    _fence_hk_gocache_budget
  ) >&2 || :
}

fence_volumes_ensure() { # compose declares the caches external: they must exist before it runs
  (
    set +eEu +o pipefail; trap - ERR; IFS=$' \t\n'
    # shellcheck disable=SC2086 # five fixed names
    docker volume inspect $FENCE_CACHE_VOLUMES >/dev/null 2>&1 && exit 0
    for v in $FENCE_CACHE_VOLUMES; do
      out="$(docker volume create "$v" 2>&1)" || _fence_hk_warn volumes "docker volume create $v: $(_fence_hk_line "$out")"
    done
  ) >&2 || :
}

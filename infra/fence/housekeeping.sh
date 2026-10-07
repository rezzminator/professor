#!/usr/bin/env bash
# Fence housekeeping — sourced on the HOST before a fence image build, so stale
# fence containers, images and caches never pile up. Callers:
# .claude/scripts/dev.sh (the iso actions that build and test: install build
# typecheck verify test e2e cover all — never status, run, shell, sim),
# infra/fence/lanes/root.sh (before a root build, with its own hash),
# infra/fence/release-rehearsal.sh (`up`). dev.sh and infra/demo/up.sh also
# call fence_volumes_ensure before every compose run.
#
#   fence_housekeeping [lane-hash]   # the nine steps below, in order
#   fence_volumes_ensure             # the five shared cache volumes exist
#   fence_sim_volume <checkout>      # that checkout's `iso sim` harvester volume
#
# Only fence-owned objects are ever removed: containers and dangling images
# labelled pfm.fence=1 (pfm-dev.Dockerfile, docker-compose.yml and every fence
# `docker run` set it), lane images (label professor.lane-root, tag
# pfm-lane-base:*), the five shared cache volumes, volumes named
# ^(fence|infra)_pfm-dev- and this clone's recorded pfm-sim-harvest- volumes,
# build cache no build used for a week, and scratch under the roots this repo's
# scripts derive. No other
# container, image or volume on the host is ever named to docker.
#
# Each step is an owner: trigger (every run of this script, at most daily where
# stamped) and bound below. Every removal prints `fence housekeeping: removed …`
# with what it freed where docker or du can say.
#
#   containers      labelled, exited: removed; labelled, running and created
#                   more than PFM_FENCE_CONTAINER_MAX_HOURS (default 6) ago: forced
#                   out (a leaked lane or root build).
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
#   checkouts       a checkout's own fence state lives as long as the checkout:
#                   its `iso sim` volume pfm-sim-harvest-<key> (fence_sim_volume)
#                   and its scratch root /tmp/<checkout name minus a leading dot>
#                   (dev.sh, lanes/run.sh, the guard derive it from the checkout
#                   they run in). Every checkout `git worktree list` names is
#                   recorded in the ledger {stamp dir}/checkouts as three
#                   tab-separated fields: checkout path, scratch root, sim volume.
#                   Once it is gone, only its recorded sim volume is removed
#                   (docker refuses one a container mounts: kept for retry).
#                   Only timing/, build/, lanes/ and guard/ in its recorded root
#                   are removed, each after 1 day without modification. Other
#                   content stays; rmdir removes an empty root. The row stays
#                   while its root is nonempty or its sim volume needs retry.
#                   Unrecorded roots and volumes are never touched; malformed
#                   rows are dropped with a WARN and no removal. A failing
#                   listing removes nothing.
#   build-cache     at most once a day, `docker builder prune --filter until=`
#                   PFM_FENCE_BUILDCACHE_HOURS (default 168) h: build cache no
#                   build has used for a week, any builder's. BuildKit's until is
#                   last-use age and it never prunes a record a running build
#                   holds, so an active build loop keeps its cache.
#   cache-budget    a tool trims its cache by age only, never by size. At most
#                   once a day every shared cache volume is sized from one
#                   `docker system df -v` (bounded to 120 s where the host has
#                   `timeout`) against its budget in MB — pfm-dev-gocache
#                   PFM_FENCE_GOCACHE_MB (15000), pfm-dev-gomod PFM_FENCE_GOMOD_MB
#                   (4000), pfm-dev-lintcache PFM_FENCE_LINTCACHE_MB (2000),
#                   pfm-lane-uv-cache PFM_FENCE_UVCACHE_MB (8000),
#                   pfm-lane-harvest-cache PFM_FENCE_HARVESTCACHE_MB (8000); over
#                   budget the volume is removed — `docker volume rm`, which the
#                   daemon refuses atomically while any container mounts it — and
#                   the next fence run recreates it empty: one cold build.
#   scratch         age: under /tmp/{project}, each live checkout's scratch root
#                   and each live checkout's repo-local tmp/, an entry with no
#                   file modified in PFM_FENCE_SCRATCH_DAYS (default 7) days is
#                   removed; a younger directory is walked one level down by the
#                   same rule (timing/run.*, lanes/<run>). Kept always:
#                   /tmp/{project}/fence (the stamps and ledger) and tmp/tools
#                   (infra/fence/tools.sh). The fence runs as root, so a tree the
#                   host user cannot remove is removed through a local fence
#                   image (`docker run --rm --network none`); no image, a WARN.
#
# Stamps and the ledger live in /tmp/{project}/fence/ ({project}: the main
# checkout's name minus a leading dot; PFM_FENCE_STAMP_DIR overrides).
#
# BROKEN STATE: housekeeping never changes its caller's exit status, whatever
# the caller's shell options (set -euo pipefail included), and never writes to
# stdout. Each failed step prints one stderr line
#   WARN fence housekeeping: {step} failed: {reason}
# and the later steps still run. A daily step that failed, or an over-budget
# cache that could not be removed or sized, writes no stamp, so the next build
# retries; an underivable lane hash removes no lane image; a failed checkout
# listing removes no sim volume and no scratch root.

FENCE_HK_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" 2>/dev/null && pwd -P)" || FENCE_HK_DIR=""
FENCE_CACHE_VOLUMES="pfm-dev-gocache pfm-dev-gomod pfm-dev-lintcache pfm-lane-harvest-cache pfm-lane-uv-cache"
# volume · budget variable · default MB, one row per shared cache volume
FENCE_CACHE_BUDGETS="pfm-dev-gocache PFM_FENCE_GOCACHE_MB 15000
pfm-dev-gomod PFM_FENCE_GOMOD_MB 4000
pfm-dev-lintcache PFM_FENCE_LINTCACHE_MB 2000
pfm-lane-uv-cache PFM_FENCE_UVCACHE_MB 8000
pfm-lane-harvest-cache PFM_FENCE_HARVESTCACHE_MB 8000"

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

_fence_hk_project_dir() { # /tmp/{project} — {project} from the main checkout, so every worktree shares it
  local repo common project
  if [ -n "${PFM_FENCE_PROJECT_TMP:-}" ]; then printf '%s\n' "$PFM_FENCE_PROJECT_TMP"; return; fi
  repo="$(cd -- "$FENCE_HK_DIR/../.." 2>/dev/null && pwd -P)" || repo="$FENCE_HK_DIR"
  if common="$(git -C "$repo" rev-parse --git-common-dir 2>/dev/null)"; then
    case "$common" in /*) ;; *) common="$repo/$common" ;; esac
    repo="$(cd -- "$common/.." 2>/dev/null && pwd -P)" || :
  fi
  project="$(basename -- "$repo")"
  printf '/tmp/%s\n' "${project#.}"
}

_fence_hk_stamp_dir() { # the stamps and the checkout ledger: /tmp/{project}/fence
  if [ -n "${PFM_FENCE_STAMP_DIR:-}" ]; then printf '%s\n' "$PFM_FENCE_STAMP_DIR"; return; fi
  printf '%s/fence\n' "$(_fence_hk_project_dir)"
}

_fence_hk_stamp_fresh() { [ -n "$(find "$1" -mmin -1440 2>/dev/null)" ]; } # written in the last 24 h

_fence_hk_stamp() { # _fence_hk_stamp <step> <stamp> — a failed write repeats the step next build
  local out
  out="$( { mkdir -p -- "${2%/*}" && touch -- "$2"; } 2>&1)" ||
    _fence_hk_warn "$1" "the day's stamp $2 could not be written (the step repeats next build): $(_fence_hk_line "$out")"
}

_fence_hk_cache_budget() {
  local dir stamp out rc v var def budget size used all_ok=1 present=""
  dir="$(_fence_hk_stamp_dir)"; stamp="$dir/cache-budget.stamp"
  _fence_hk_stamp_fresh "$stamp" && return 0
  while read -r v var def; do
    if out="$(docker volume inspect "$v" 2>&1)"; then present="$present $v"
    else
      case "$out" in *[Nn]"o such volume"*) ;; *) _fence_hk_warn cache-budget "docker volume inspect $v: $(_fence_hk_line "$out")"; all_ok=0 ;; esac
    fi
  done <<<"$FENCE_CACHE_BUDGETS"
  if [ -n "$present" ]; then
    local df=(docker system df -v --format '{{range .Volumes}}{{.Name}}\t{{.Size}}{{println}}{{end}}')
    command -v timeout >/dev/null 2>&1 && df=(timeout 120 "${df[@]}")
    out="$("${df[@]}" 2>&1)"; rc=$?
    if [ "$rc" -ne 0 ]; then
      [ "$rc" -eq 124 ] && out="timed out after 120 s"
      _fence_hk_warn cache-budget "could not size the cache volumes (docker system df -v): $(_fence_hk_line "$out")"; return
    fi
    local sizes="$out"
    while read -r v var def; do
      case " $present " in *" $v "*) ;; *) continue ;; esac
      budget="${!var:-$def}"
      if ! _fence_hk_posint "$budget"; then
        _fence_hk_warn cache-budget "$var='$budget' is not a positive integer — using $def"
        budget="$def"
      fi
      budget=$((10#$budget))
      size="$(awk -F'\t' -v v="$v" '$1 == v { print $2; exit }' <<<"$sizes")"
      if ! used="$(_fence_hk_mb "$size")"; then
        _fence_hk_warn cache-budget "could not size $v: docker system df -v reported '${size:-no row}'"; all_ok=0; continue
      fi
      [ "$used" -gt "$budget" ] || continue
      if out="$(docker volume rm "$v" 2>&1)"; then
        _fence_hk_say "removed $v, $used MB > $budget MB budget, freed $used MB — the next fence run recreates it empty"
      else
        _fence_hk_warn cache-budget "$v is $used MB > $budget MB and was kept — docker volume rm: $(_fence_hk_line "$out")"; all_ok=0
      fi
    done <<<"$FENCE_CACHE_BUDGETS"
  fi
  [ "$all_ok" = 1 ] && _fence_hk_stamp cache-budget "$stamp"
}

_fence_hk_build_cache() {
  local hours="${PFM_FENCE_BUILDCACHE_HOURS:-168}" dir stamp out
  if ! _fence_hk_posint "$hours"; then
    _fence_hk_warn build-cache "PFM_FENCE_BUILDCACHE_HOURS='$hours' is not a positive integer — using 168"
    hours=168
  fi
  hours=$((10#$hours))
  dir="$(_fence_hk_stamp_dir)"; stamp="$dir/build-cache.stamp"
  _fence_hk_stamp_fresh "$stamp" && return 0
  if out="$(docker builder prune -f --filter "until=${hours}h" 2>&1)"; then
    _fence_hk_say "removed build cache unused for ${hours}h — $(grep -i 'reclaimed' <<<"$out" || echo 'Total reclaimed space: 0B')"
    _fence_hk_stamp build-cache "$stamp"; return 0
  fi
  case "$out" in *"prune operation is already running"*) return 0 ;; esac
  _fence_hk_warn build-cache "docker builder prune: $(_fence_hk_line "$out")"
}

# The checkouts `git worktree list` names, physical paths, one per line; 1 on failure.
_fence_hk_checkout_list() {
  local repo out wt
  repo="$(cd -- "$FENCE_HK_DIR/../.." 2>/dev/null && pwd -P)" || repo="$FENCE_HK_DIR"
  out="$(git -C "$repo" worktree list --porcelain 2>&1)" || { printf '%s\n' "$out"; return 1; }
  while IFS= read -r wt; do
    case "$wt" in worktree\ *) wt="${wt#worktree }" ;; *) continue ;; esac
    (cd -- "$wt" 2>/dev/null && pwd -P) || printf '%s\n' "$wt"
  done <<<"$out"
}

_fence_hk_scratch_root() { local n; n="$(basename -- "$1")"; printf '/tmp/%s\n' "${n#.}"; } # dev.sh's TMP_BASE rule

_fence_hk_kb() { du -sk -- "$1" 2>/dev/null | awk '{ print $1; exit }'; }

# _fence_hk_rmtree <step> <path>: rm -rf, then through a local fence image for
# what the fence wrote as root; prints the MB freed.
_fence_hk_rmtree() {
  local step="$1" path="$2" kb out img
  kb="$(_fence_hk_kb "$path")"; kb="${kb:-0}"
  if ! out="$(rm -rf -- "$path" 2>&1)" || [ -e "$path" ]; then
    img="$(docker image ls --filter label=pfm.fence=1 --filter dangling=false -q 2>/dev/null | head -n 1)"
    if [ -z "$img" ]; then
      _fence_hk_warn "$step" "$path not removed (no local fence image to remove root-owned files): $(_fence_hk_line "$out")"; return 1
    fi
    if ! out="$(docker run --rm --network none --label pfm.fence=1 -v "${path%/*}:/reap" --entrypoint rm "$img" -rf -- "/reap/${path##*/}" 2>&1)" || [ -e "$path" ]; then
      _fence_hk_warn "$step" "$path not removed: $(_fence_hk_line "$out")"; return 1
    fi
  fi
  _fence_hk_say "removed $path, freed $(( (kb + 512) / 1024 )) MB"
}

# _fence_hk_stale <path> <days>: nothing under it was modified in <days> days.
# A tree find cannot read whole is never stale: it is kept, with a WARN.
_fence_hk_stale() {
  local out
  if out="$(find "$1" -mtime "-$2" -print -quit 2>&1)"; then [ -z "$out" ]; return; fi
  grep -q '^/' <<<"$out" && return 1
  _fence_hk_warn scratch "kept $1 — find could not read it whole: $(_fence_hk_line "$out")"; return 1
}

# _fence_hk_age_reap <root> <days> <keep-name>…: depth 1, then one level into a young dir.
_fence_hk_age_reap() {
  local root="$1" days="$2" e sub k skip; shift 2
  [ -d "$root" ] || return 0
  for e in "$root"/* "$root"/.[!.]*; do
    [ -e "$e" ] || [ -L "$e" ] || continue
    skip=0; for k in "$@"; do [ "${e##*/}" = "$k" ] && skip=1; done
    [ "$skip" = 1 ] && continue
    if _fence_hk_stale "$e" "$days"; then _fence_hk_rmtree scratch "$e"; continue; fi
    [ -d "$e" ] && [ ! -L "$e" ] || continue
    for sub in "$e"/* "$e"/.[!.]*; do
      [ -e "$sub" ] || [ -L "$sub" ] || continue
      _fence_hk_stale "$sub" "$days" && _fence_hk_rmtree scratch "$sub"
    done
  done
}

_fence_hk_checkouts() {
  local list dir ledger live_keys="" live_roots="" live_rows="" c root v row out project kept="" bad
  local rows="" names volumes_ok=1 pending purpose contents
  if ! list="$(_fence_hk_checkout_list)"; then
    _fence_hk_warn checkouts "git worktree list failed, no sim volume or scratch root removed: $(_fence_hk_line "$list")"; return
  fi
  dir="$(_fence_hk_stamp_dir)"; ledger="$dir/checkouts"; project="$(_fence_hk_project_dir)"
  while IFS= read -r c; do
    [ -n "$c" ] || continue
    v="$(fence_sim_volume "$c")"; root="$(_fence_hk_scratch_root "$c")"
    live_keys="$live_keys"$'\n'"$v"
    live_roots="$live_roots"$'\n'"$root"
    live_rows="$live_rows"$'\n'"$c"$'\t'"$root"$'\t'"$v"
  done <<<"$list"
  if ! out="$(mkdir -p -- "$dir" 2>&1)"; then _fence_hk_warn checkouts "ledger dir $dir: $(_fence_hk_line "$out")"; return; fi
  if [ -e "$ledger" ] || [ -L "$ledger" ]; then
    if ! rows="$(cat -- "$ledger" 2>&1)"; then
      _fence_hk_warn checkouts "reading ledger $ledger, nothing removed: $(_fence_hk_line "$rows")"; return
    fi
  fi
  if ! names="$(docker volume ls -q 2>&1)"; then
    _fence_hk_warn checkouts "docker volume ls: $(_fence_hk_line "$names")"; volumes_ok=0
  fi
  while IFS= read -r row; do
    [ -n "$row" ] || continue
    IFS=$'\t' read -r c root v <<<"$row"
    case "$root" in # only the shape _fence_hk_scratch_root writes: /tmp/<name>
      /tmp/*/* | /tmp/.* | /tmp/) bad=1 ;; /tmp/*) bad=0 ;; *) bad=1 ;;
    esac
    case "$c" in /*) ;; *) bad=1 ;; esac
    if [ "$row" != "$c"$'\t'"$root"$'\t'"$v" ] ||
      [ "$root" != "$(_fence_hk_scratch_root "$c")" ] || [ "$v" != "$(fence_sim_volume "$c")" ]; then bad=1; fi
    if [ "$bad" = 1 ]; then
      _fence_hk_warn checkouts "ledger line '$row' in $ledger is not a checkout, /tmp/<name> root and matching sim volume — dropped, nothing removed"; continue
    fi
    _fence_hk_has_line "$list" "$c" && continue # the current row is written below
    pending=0
    # A prefix is not ownership: only this gone checkout's validated record can authorize removal.
    if ! _fence_hk_has_line "$live_keys" "$v"; then
      if [ "$volumes_ok" = 0 ]; then pending=1
      elif _fence_hk_has_line "$names" "$v"; then
        if out="$(docker volume rm "$v" 2>&1)"; then _fence_hk_say "removed $v — its checkout is gone"
        else
          pending=1
          case "$out" in *"in use"*) _fence_hk_say "kept $v — a container still uses it" ;;
            *) _fence_hk_warn checkouts "docker volume rm $v: $(_fence_hk_line "$out")" ;; esac
        fi
      fi
    fi
    if _fence_hk_has_line "$live_roots" "$root" || [ "$root" = "$project" ]; then pending=1
    elif [ -e "$root" ] || [ -L "$root" ]; then
      if [ -L "$root" ] || [ ! -d "$root" ]; then
        _fence_hk_warn checkouts "kept $root — recorded scratch root is not a directory or is a symlink"; pending=1
      else
        for purpose in timing build lanes guard; do
          [ -d "$root/$purpose" ] && [ ! -L "$root/$purpose" ] || continue
          _fence_hk_stale "$root/$purpose" 1 && _fence_hk_rmtree checkouts "$root/$purpose"
        done
        if ! contents="$(find "$root" -mindepth 1 -maxdepth 1 -print -quit 2>&1)"; then
          _fence_hk_warn checkouts "kept $root — could not check whether it is empty: $(_fence_hk_line "$contents")"; pending=1
        elif [ -n "$contents" ]; then pending=1
        elif out="$(rmdir -- "$root" 2>&1)"; then _fence_hk_say "removed $root (empty), freed 0 MB"
        else _fence_hk_warn checkouts "rmdir $root: $(_fence_hk_line "$out")"; pending=1
        fi
      fi
    fi
    [ "$pending" = 1 ] && kept="$kept"$'\n'"$row"
  done <<<"$rows"
  out="$( { printf '%s\n' "$kept" "$live_rows" | sed '/^$/d' | sort -u > "$ledger.new" && mv -f -- "$ledger.new" "$ledger"; } 2>&1)" ||
    _fence_hk_warn checkouts "the ledger $ledger could not be written: $(_fence_hk_line "$out")"
}

_fence_hk_scratch() {
  local days="${PFM_FENCE_SCRATCH_DAYS:-7}" list project c
  if ! _fence_hk_posint "$days"; then
    _fence_hk_warn scratch "PFM_FENCE_SCRATCH_DAYS='$days' is not a positive integer — using 7"
    days=7
  fi
  days=$((10#$days))
  if ! list="$(_fence_hk_checkout_list)"; then
    _fence_hk_warn scratch "git worktree list failed, no scratch removed: $(_fence_hk_line "$list")"; return
  fi
  project="$(_fence_hk_project_dir)"
  _fence_hk_age_reap "$project" "$days" fence
  while IFS= read -r c; do
    [ -n "$c" ] || continue
    [ "$(_fence_hk_scratch_root "$c")" = "$project" ] || _fence_hk_age_reap "$(_fence_hk_scratch_root "$c")" "$days"
    _fence_hk_age_reap "$c/tmp" "$days" tools
  done <<<"$list"
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
    _fence_hk_checkouts
    _fence_hk_build_cache
    _fence_hk_cache_budget
    _fence_hk_scratch
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

fence_sim_volume() { # fence_sim_volume <checkout>: its basename in docker's volume alphabet + a checksum of its physical path
  local path wt
  path="$(cd -- "$1" 2>/dev/null && pwd -P)" || path="$1"
  wt="$(basename -- "$path" | tr -c 'A-Za-z0-9_.\n-' '-')"; wt="${wt#.}"
  printf 'pfm-sim-harvest-%s-%s\n' "$wt" "$(printf '%s' "$path" | cksum | cut -d' ' -f1)"
}

#!/usr/bin/env bash
# infra/fence/shared-run.sh — one run inside the shared fence container (infra/fence/shared.sh starts
# the container and `docker exec`s this file from the caller's own tree).
#
#   shared-run.sh run <id> <private|shared|tty> <tree> <engine-sync|-> <worktree> -- <command>...
#   shared-run.sh kill <id>
#   shared-run.sh inner …            (the run's pid 1; never called by hand)
#
# run: registers /var/fence/live/<id> (this supervisor's pid; the container's daemon kills and
# removes the run of a dead supervisor), refuses with exit 75 when the container is closing, writes
# the host-visible start marker /pfm-fence/.runs/<id>/started, records its worktree as a user of the
# container (/var/fence/worktrees/<worktree>, which also lifts a superseded mark: a worktree still on this
# key keeps it), counts the live runs into
# PFM_FENCE_CONCURRENT, makes /var/fence/runs/<id>/disk, and starts the command under
# `unshare --fork --pid --mount-proc --mount` (plus --net for private): its own pid namespace and
# /proc, so a process scan or a pkill sees this run alone; its own mounts — the caller's tree
# read-only at /worktree, the engine-sync source read-only at /engine-sync; capped, noswap tmpfs
# on /root, /tmp, /var/tmp and /dev/shm; the worktree's timing base on /pfm-timing and the run's
# host directory on /pfm-run; its disk-backed escape on /pfm-disk (PFM_FENCE_DISK_DIR), then
# /var/fence and /pfm-fence hidden behind capped, noswap tmpfs — so no
# fixed path under HOME or /tmp is shared with another run; and for private its own network
# namespace with lo up (a dummy0 default route makes an outside connect leave a packet the egress
# recorder sees, never a delivery), so a fixed test port never collides. A private run first makes
# sure the module caches hold pfm's and engine-sync's modules (`go mod download` in the container's
# network, stamped by go.sum). Not tty: a monitor reads the caller's heartbeat on stdin and kills the
# run's namespaces on EOF or after PFM_FENCE_HEARTBEAT_S (default 4) seconds without a beat. The
# run's directory is removed at the end; the exit code is the command's.
# kill: SIGKILL to the run's namespaces (the pid namespace's init and all it holds die with it).
# BROKEN STATE: a bad argument exits 2 naming it; a mount, unshare or module download that fails is a
# `shared-run: …` line on stderr and exit 1 — never a run outside its namespaces.
# Seams for infra/fence/lanes/tests/shared-run_test.sh: FENCE_STATE_DIR (default /var/fence) and
# FENCE_HOST_DIR (default /pfm-fence), each outside /root, /tmp and /var/tmp (the run binds over those
# before it hides the two).
set -uo pipefail
STATE="${FENCE_STATE_DIR:-/var/fence}"
MNT="${FENCE_HOST_DIR:-/pfm-fence}"
SELF="${BASH_SOURCE[0]}"

die() { echo "shared-run: $*" >&2; exit "${2:-1}"; }
valid_id() { [[ "$1" =~ ^[A-Za-z0-9._-]+$ ]] || die "bad run id: $1" 2; }

live_count() {
  local n=0 f pid
  for f in "$STATE"/live/*; do
    [[ -e "$f" ]] || continue
    pid="$(cat "$f" 2>/dev/null)"
    [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null && n=$((n + 1))
  done
  echo "$n"
}

modules_ready() { # modules_ready <module dir>...: go mod download once per go.sum content
  local d sum stamp out tmp
  mkdir -p /cache/gomod/.pfm-fence-ready || return 1
  for d in "$@"; do
    [[ -f "$d/go.sum" ]] || continue
    sum="$(sha256sum "$d/go.sum")"; stamp="/cache/gomod/.pfm-fence-ready/${sum%% *}"
    [[ -e "$stamp" ]] && continue
    if ! out="$(cd "$d" && go mod download 2>&1)"; then
      echo "shared-run: go mod download in $d failed — a private run has no network to fetch modules: ${out//$'\n'/ }" >&2
      return 1
    fi
    # The stamp appears whole, by rename, only after the download; concurrent downloads of one module
    # are safe on Go's own module-cache file locks.
    tmp="$(mktemp "$stamp.XXXXXX")" && mv -f "$tmp" "$stamp" \
      || echo "shared-run: the module stamp $stamp could not be written — the next private run downloads again" >&2
  done
}

kill_run() { # kill_run <id>
  local ns
  ns="$(cat "$STATE/runs/$1/ns.pid" 2>/dev/null)" || return 0
  [[ -n "$ns" ]] || return 0
  pkill -KILL -P "$ns" 2>/dev/null
  kill -KILL "$ns" 2>/dev/null
  return 0
}

cmd_run() {
  local id="$1" mode="$2" tree="$3" es="$4" wt="$5" live run ns mon rc=0
  shift 5
  [[ "${1:-}" == -- ]] && shift
  valid_id "$id"
  local d
  for d in "$STATE" "$MNT"; do
    case "$d/" in /root/* | /tmp/* | /var/tmp/*) die "$d lies under /root, /tmp or /var/tmp, which a run binds over" 2 ;; esac
  done
  case "$mode" in private|shared|tty) ;; *) die "bad mode: $mode" 2 ;; esac
  [[ -d "$tree" ]] || die "the tree $tree is not visible in the container" 2
  [[ "$es" == - || -d "$es" ]] || die "the engine-sync source $es is not visible in the container" 2
  [[ $# -gt 0 ]] || die "no command" 2
  mkdir -p "$STATE/live" "$STATE/runs" || die "$STATE could not be created"
  live="$STATE/live/$id"; run="$STATE/runs/$id"
  echo "$$" > "$live"
  if [[ -e "$STATE/closing" ]]; then rm -f "$live"; echo "shared-run: the container is closing — start a new one" >&2; exit 75; fi
  [[ ! -d "$MNT/.runs/$id" ]] || : > "$MNT/.runs/$id/started"
  if mkdir -p "$STATE/worktrees" && : > "$STATE/worktrees/$wt"; then rm -f "$STATE/superseded"
  else echo "shared-run: the worktree record $STATE/worktrees/$wt could not be written — the container may be superseded under this worktree" >&2; fi
  PFM_FENCE_CONCURRENT="$(live_count)"; export PFM_FENCE_CONCURRENT
  mkdir -p "$run/disk" "$MNT/$wt/timing" || { rm -rf "$run" "$live"; die "the run directory $run could not be made"; }
  chmod 0700 "$run/disk" || { rm -rf "$run" "$live"; die "the run disk directory $run/disk could not be made private"; }
  if [[ "$mode" == private ]]; then
    local mods=("$tree/pfm")
    [[ "$es" == - ]] || mods+=("$es")
    modules_ready "${mods[@]}" || { rm -rf "$run" "$live"; exit 1; }
  fi
  local -a flags=(--fork --pid --mount-proc --mount --kill-child=SIGKILL)
  [[ "$mode" != private ]] || flags+=(--net)
  if [[ "$mode" == tty ]]; then
    unshare "${flags[@]}" -- bash "$SELF" inner "$id" "$mode" "$tree" "$es" "$wt" -- "$@" || rc=$?
  else
    exec 3<&0
    # A background job of this non-interactive shell starts with SIGINT and SIGQUIT ignored, an ignore
    # every process of the run would inherit and none could trap: env restores their defaults.
    env --default-signal=INT,QUIT unshare "${flags[@]}" -- bash "$SELF" inner "$id" "$mode" "$tree" "$es" "$wt" -- "$@" </dev/null &
    ns=$!
    echo "$ns" > "$run/ns.pid"
    (
      hb="${PFM_FENCE_HEARTBEAT_S:-4}"
      while IFS= read -r -t "$hb" -u 3 _; do :; done
      echo "shared-run: the caller of run $id is gone (heartbeat ended) — killing the run" >&2
      kill_run "$id"
    ) &
    mon=$!
    wait "$ns" || rc=$?
    kill -KILL "$mon" 2>/dev/null
    wait "$mon" 2>/dev/null
  fi
  rm -rf "$run" "$live"
  exit "$rc"
}

cmd_inner() {
  local id="$1" mode="$2" tree="$3" es="$4" wt="$5" run
  shift 5
  [[ "${1:-}" == -- ]] && shift
  run="$STATE/runs/$id"
  mount --make-rprivate / 2>/dev/null
  bind_ro() { mount --bind "$1" "$2" && { mount -o remount,bind,ro "$2" 2>/dev/null || true; }; }
  mkdir -p /worktree /engine-sync /pfm-timing /pfm-run /pfm-disk 2>/dev/null
  bind_ro "$tree" /worktree || die "the tree $tree could not be bound on /worktree"
  if [[ "$es" != - ]]; then bind_ro "$es" /engine-sync || die "the engine-sync source $es could not be bound"; fi
  # Baseline gate scratch peaked near 1 GiB in /tmp; leave 50% headroom, not half the VM's
  # RAM per mount. noswap is deliberate: an unsupported kernel fails closed, never spills
  # disposable scratch to the VM's swap. All pages also count against the container cap.
  scratch_mount() { mount -t tmpfs -o "size=$2,mode=$3,noswap" tmpfs "$1" || die "$1 could not be made capped, non-swappable scratch"; }
  scratch_mount /root 256m 0700
  scratch_mount /tmp 1536m 1777
  scratch_mount /var/tmp 16m 1777
  scratch_mount /dev/shm 64m 1777
  mount --bind "$run/disk" /pfm-disk || die "the run disk directory could not be bound on /pfm-disk"
  export PFM_FENCE_DISK_DIR=/pfm-disk
  mount --bind "$MNT/$wt/timing" /pfm-timing || die "the timing base could not be bound on /pfm-timing"
  if [[ -d "$MNT/.runs/$id" ]]; then mount --bind "$MNT/.runs/$id" /pfm-run || die "the run's host directory could not be bound"; fi
  scratch_mount "$STATE" 1m 0700
  scratch_mount "$MNT" 1m 0700
  if [[ "$mode" == private ]]; then
    ip link set lo up || die "lo could not be brought up in the private network namespace"
    if ip link add dummy0 type dummy 2>/dev/null; then
      ip link set dummy0 up && ip addr add 192.0.2.1/32 dev dummy0 && ip route add default dev dummy0 \
        || echo "shared-run: the dummy0 default route failed — an outside connect fails with no packet" >&2
    fi
  fi
  export HOME=/root
  cd /worktree || die "/worktree cannot be entered"
  if [[ -x /sbin/docker-init ]]; then exec /sbin/docker-init -- "$@"; fi
  exec "$@"
}

case "${1:-}" in
  run) shift; [[ $# -ge 7 ]] || die "usage: shared-run.sh run <id> <mode> <tree> <es> <worktree> -- <command>..." 2; cmd_run "$@" ;;
  inner) shift; cmd_inner "$@" ;;
  kill) [[ $# -eq 2 ]] || die "usage: shared-run.sh kill <id>" 2; valid_id "$2"; kill_run "$2" ;;
  *) die "usage: shared-run.sh {run|kill} …" 2 ;;
esac

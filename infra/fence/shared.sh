# shellcheck shell=bash
# infra/fence/shared.sh — the shared fence container, host side; sourced by .claude/scripts/dev.sh
# (cmd_iso) after infra/fence/fence-env.sh, image-key.sh and housekeeping.sh.
#
# ONE long-lived pfm-dev container per (main checkout, image key) serves every fenced call of every
# worktree: `fence_shared_ensure` starts it on demand, `fence_shared_exec` enters it with `docker
# exec` and hands the call to infra/fence/shared-run.sh, which gives the run its own pid, mount and
# (mode private) network namespace and its own HOME, /tmp, /var/tmp and /dev/shm — every fixed path a
# test writes is per run by construction. Design: docs/dev/isolated-dev-foundation.md.
#
#   fence_shared_init              # FENCE_SH_* from REPO_ROOT and the fence-env.sh exports
#   fence_shared_ensure            # admit and reserve under effective caps; 1 = could not
#   fence_shared_exec <mode> <cmd> # mode private | shared | tty; the command's own exit code
#
# Container: image professor-pfm-dev:k-<key12> (_fence_sh_image), name pfm-fence-<roots12>-<key12>,
# labels pfm.fence=1, pfm.fence.long-lived=1 (fence housekeeping never age-reaps it),
# pfm.fence.shared=<roots12>, pfm.fence.inputs=<image key>.
# Mounts, all fixed at creation: the main checkout read-only at /repo (every worktree under it,
# .worktrees/ included, visible as it appears), a caller tree outside it read-only at /repo-ext (its
# own container: the roots are part of the name), the common Git directory read-only at
# /pfm-git-common, the host directory /tmp/{project}/fence/shared read-write at /pfm-fence (each
# worktree's timing base <worktree>/timing and each run's host directory .runs/<id>), and the shared
# cache volumes at /cache/{go-build,gomod,lint} (GOCACHE, GOMODCACHE, GOLANGCI_LINT_CACHE).
# Knobs, read per call (named in dev.sh usage):
#   PFM_FENCE_CPUS (6)  PFM_FENCE_MEM (5g)  the container's CPU and memory caps (memory = memory+swap);
#                       only an idle container takes different caps through `docker update`; another
#                       live run keeps its caps, with a loud CAPS CONFLICT and effective-cap receipt
#   PFM_FENCE_TEST_P    go test -p (pfm/Makefile TESTFLAGS); unset, the container's CPU cap
#   PFM_FENCE_IDLE_S (1800)       a container with no live run this long exits (read at creation)
#   PFM_FENCE_HEARTBEAT_S (4)     a run whose caller's heartbeat stops this long is killed
#   PFM_FENCE_NET (private)       iso run's network: shared opts into the container's network
# Creation and admission are serialized: /tmp/{project}/fence/shared.lock.d
# (infra/fence/mkdir-lock.sh) covers inspect, image build, container start, live-run detection, caps
# update and the live reservation. Executions overlap freely after admission. A new image key starts a new container. Each run records its worktree in the container
# (/var/fence/worktrees/<worktree>); a worktree whose key changed (its last key, <worktree>/key under the
# host directory) leaves its old key's container, and an old container no worktree uses any more is
# superseded: it drains its live runs, then exits. Two worktrees on different keys each keep their
# own warm container.
# Housekeeping runs detached, only when a container is created, under the start lock: the k- tags no
# running container uses are removed, the host run directories of dead callers (.runs/<id>, the pid
# in the id) are reaped, then infra/fence/housekeeping.sh; its output goes to
# /tmp/{project}/fence/housekeeping.log, rotated to housekeeping.log.1 past 1 MiB.
# Kill: a heartbeat feeder writes to the run's stdin every 0.5 s while every pid in $$ and
# PFM_FENCE_WATCH_PIDS lives; the caller's death closes it, and shared-run.sh kills the run's
# namespaces on EOF or after PFM_FENCE_HEARTBEAT_S without a beat. A TERM or INT to the caller does
# the same before it exits 143 / 130.
# BROKEN STATE: every failure is a named `iso: …` line on stderr and a non-zero return — an image that
# cannot be built, a container that does not start, a lock held past 600 s by a live process, a path
# the container cannot see (ENGINE_SYNC_SRC outside the mounted roots), a run dir that cannot be made;
# never a host fallback. A cap update docker refuses is a WARN line, and the run goes on under the
# running caps.

FENCE_SHARED_IMAGE=professor-pfm-dev
# shellcheck source=infra/fence/mkdir-lock.sh
if [[ ${BASH_SOURCE[0]} == */* ]]; then FENCE_SHARED_HERE=${BASH_SOURCE[0]%/*}; else FENCE_SHARED_HERE=.; fi
. "$FENCE_SHARED_HERE/mkdir-lock.sh" || { echo "iso: infra/fence/mkdir-lock.sh could not be sourced" >&2; return 1 2>/dev/null || exit 1; }

# One source of run-shaping defaults. Native uses the measured bounded-scheduler profile;
# Desktop retains its baseline. Auto selects native only when Docker can supply its caps.
# name|desktop-default|type|native-default; native = inherits desktop, a literal empty
# native field selects empty. Tune native in this column only; env overrides win.
fence_config_schema() {
  local row
  while IFS= read -r row; do printf '%s\n' "$row"; done <<'CONFIG'
PFM_FENCE_NET|private|network|=
PFM_FENCE_CPUS|6|cpu|12
PFM_FENCE_MEM|5g|size|12g
PFM_FENCE_CPUSET||cpuset|=
PFM_FENCE_GOMAXPROCS||optional_int|=
PFM_FENCE_TEST_P||optional_int|6
PFM_FENCE_TEST_PARALLEL|4|int|=
PFM_FENCE_TEST_COUNT|1|int|=
PFM_FENCE_AFFECTED_P||optional_int|=
PFM_FENCE_AFFECTED_PARALLEL||optional_int|=
PFM_FENCE_AFFECTED_TIMEOUT|10m|duration|=
PFM_FENCE_TEST_TIMEOUT|25m|duration|=
PFM_FENCE_E2E_P|1|int|=
PFM_FENCE_E2E_PARALLEL||optional_int|=
PFM_FENCE_E2E_TIMEOUT|25m|duration|=
PFM_FENCE_RACE_TIMEOUT|10m|duration|=
PFM_FENCE_SHARDS_CMD|4|int|=
PFM_FENCE_SHARDS_UPDATE|2|int|=
PFM_FENCE_IDLE_S|1800|int|=
PFM_FENCE_HEARTBEAT_S|4|number|=
PFM_FENCE_BEAT_S|0.5|number|=
PFM_FENCE_DAEMON_POLL_S|2|number|=
PFM_FENCE_LOCK_TICKS|3000|int|=
PFM_FENCE_LOCK_POLL_S|0.2|number|=
PFM_FENCE_IMAGE_ATTEMPTS|3|int|=
PFM_FENCE_START_ATTEMPTS|10|int|=
PFM_FENCE_EXEC_ATTEMPTS|2|int|=
PFM_FENCE_RETRY_S|2|number|=
PFM_FENCE_START_RETRY_S|1|number|=
PFM_FENCE_GONE_TICKS|10|int|=
PFM_FENCE_FEED_GRACE_TICKS|30|int|=
PFM_FENCE_HISTORY_LOCK_TICKS|1200|int|=
PFM_FENCE_SAMPLER_READY_TICKS|50|int|=
PFM_FENCE_HOME_MIB|256|int|=
PFM_FENCE_TMP_MIB|1536|int|=
PFM_FENCE_VARTMP_MIB|16|int|=
PFM_FENCE_SHM_MIB|64|int|=
PFM_FENCE_HIDE_MIB|1|int|=
PFM_FENCE_HEADROOM_MIB|1024|int|=
PFM_FENCE_COMPOSE_MEM|3g|size|=
PFM_FENCE_SIM_SHM|2g|size|=
PFM_FENCE_GATE_ALL_S|198|int|=
PFM_FENCE_GATE_PFM_S|198|int|=
PFM_FENCE_GATE_TEMPLATES_S|32|int|=
PFM_FENCE_GATE_TOLERANCE|1.25|number|=
PFM_FENCE_GATE_FAIL_FACTOR|2|number|=
PFM_FENCE_STEP_DEFAULT_S|1500|int|=
EGRESS_LISTEN_WAIT_S|5|number|=
EGRESS_END_WAIT_S|5|number|=
SKILL_TEST_SHARDS||optional_int|=
PFM_FENCE_SKILL_SHARD_MAX|8|int|=
STEPS_JOBS||optional_int|6
STEPS_HEAVY_JOBS|2|int|=
STEPS_BOUND_S||optional_int|=
STEPPROF_GRACE_TICKS|20|uint|=
STEPPROF_TRACE|err|trace|=
SAMPLER_INTERVAL_S|0.5|number|=
SAMPLER_SPIN_ITERS|10000|int|=
PFM_FENCE_CONTAINER_MAX_HOURS|6|int|=
PFM_FENCE_BUILDCACHE_HOURS|168|int|=
PFM_FENCE_BUILDCACHE_MINUTES|1440|int|=
PFM_FENCE_BUDGET_MINUTES|120|int|=
PFM_FENCE_SCRATCH_DAYS|7|int|=
PFM_FENCE_GOCACHE_MB|15000|int|=
PFM_FENCE_GOMOD_MB|4000|int|=
PFM_FENCE_LINTCACHE_MB|2000|int|=
PFM_FENCE_DF_TIMEOUT_S|120|int|=
LANE_EGRESS_READY_SECS|10|uint|=
LANE_EGRESS_VERDICT_SECS|30|uint|=
LANE_STORM_WAIT_SECS|60|int|=
LANE_STORM_EVERY_SECS|0.5|number|=
LANE_ALIVE_RETRY_SECS|1|number|=
LANE_WAIT_LAST_EVERY_SECS|0.5|number|=
LANE_WAIT_FOR_EVERY_SECS|0.2|number|=
PFM_FENCE_LANE_ROOT_LOCK_TICKS|18000|int|=
PFM_FENCE_LANE_TOLERANCE|1.25|number|=
PFM_FENCE_LANE_SEQUENCE_S|763|int|=
PFM_FENCE_LANE_O1_S|70|int|=
PFM_FENCE_LANE_E1_S|194|int|=
PFM_FENCE_LANE_E2_S|39|int|=
PFM_FENCE_LANE_E3_S|14|int|=
PFM_FENCE_LANE_F_S|134|int|=
PFM_FENCE_LANE_M_S|156|int|=
PFM_FENCE_LANE_A_S|49|int|=
PFM_FENCE_LANE_H_S|unpinned|budget|=
PFM_FENCE_LANE_O2_S|86|int|=
CONFIG
}

_fence_config_default() {
  local key value kind native
  while IFS='|' read -r key value kind native; do
    if [[ "$key" == "$1" ]]; then
      [[ "${PFM_FENCE_PROFILE:-desktop}" != native || "$native" == = ]] || value="$native"
      printf '%s\n' "$value"; return 0
    fi
  done < <(fence_config_schema)
  echo "iso: no declared fence default for $1" >&2; return 1
}

fence_config_resolve() {
  case "$1" in PFM_FENCE_*|LANE_*) [[ -n "${!1+x}" ]] || { echo "iso: undeclared budget variable $1" >&2; return 1; }; printf '%s\n' "${!1}" ;; *) printf '%s\n' "$1" ;; esac
}

fence_config_values() { # defaults/validation only: safe inside a run and for standalone consumers
  local name default kind native value valid
  while IFS='|' read -r name default kind native; do
    [[ "${PFM_FENCE_PROFILE:-desktop}" != native || "$native" == = ]] || default="$native"
    if [[ -z "${!name+x}" ]]; then printf -v "$name" '%s' "$default"; fi
    value="${!name}"; valid=0
    case "$kind" in
      uint) [[ "$value" == 0 || "$value" =~ ^[1-9][0-9]{0,8}$ ]] && valid=1 ;;
      int) [[ "$value" =~ ^[1-9][0-9]{0,8}$ ]] && valid=1 ;;
      optional_int) [[ -z "$value" || "$value" =~ ^[1-9][0-9]{0,8}$ ]] && valid=1 ;;
      number) [[ "$value" =~ ^[0-9]+(\.[0-9]+)?$ ]] && awk -v n="$value" 'BEGIN {exit !(n>0 && n<=1000000000)}' && valid=1 ;;
      cpu) [[ "$value" =~ ^[0-9]+(\.[0-9]+)?$ ]] && awk -v n="$value" 'BEGIN {exit !(n>=0 && n<=1000000)}' && valid=1 ;;
      size) [[ "$value" =~ ^([1-9][0-9]{0,17}|[1-9][0-9]{0,8}[kKmMgG])$ ]] && valid=1 ;;
      cpuset) [[ -z "$value" || "$value" =~ ^[0-9]+(-[0-9]+)?(,[0-9]+(-[0-9]+)?)*$ ]] && valid=1
        if [[ $valid == 1 && -n "$value" ]]; then
          awk -v v="$value" 'BEGIN {n=split(v,a,",");for(i=1;i<=n;i++){m=split(a[i],b,"-");if(b[1]>1000000 || (m==2 && (b[2]<b[1] || b[2]>1000000)))exit 1}}' || valid=0
        fi ;;
      duration) [[ "$value" =~ ^[1-9][0-9]{0,8}(ms|s|m|h)$ ]] && valid=1 ;;
      network) [[ "$value" == private || "$value" == shared ]] && valid=1 ;;
      trace) [[ "$value" == err || "$value" == x || "$value" == off ]] && valid=1 ;;
      budget) [[ "$value" == unpinned || "$value" =~ ^[1-9][0-9]{0,8}$ ]] && valid=1 ;;
    esac
    if [[ $valid != 1 ]]; then echo "iso: $name must be $kind (got '$value')" >&2; return 1; fi
    export "$name"
  done < <(fence_config_schema)
}

fence_config_test_p() {
  local p="${PFM_FENCE_TEST_P:-}" quota
  if [[ -z "$p" ]]; then
    quota="$(awk '$1 != "max" && $2 > 0 {q=$1/$2; p=int(q); if(p<q)p++; print p}' /sys/fs/cgroup/cpu.max 2>/dev/null)" || quota=""
    p="$quota"
    if [[ -z "$p" ]]; then
      if awk -v n="$PFM_FENCE_CPUS" 'BEGIN {exit !(n==0)}'; then
        p="$(nproc 2>/dev/null)" || p="$(getconf _NPROCESSORS_ONLN 2>/dev/null)" || { echo 'iso: CPU concurrency detection failed; set PFM_FENCE_TEST_P' >&2; return 1; }
      else p="$(awk -v c="$PFM_FENCE_CPUS" 'BEGIN {p=int(c);if(p<c)p++;print p}')"; fi
    fi
  fi
  [[ "$p" =~ ^[1-9][0-9]*$ ]] || { echo "iso: PFM_FENCE_TEST_P could not be resolved: $p" >&2; return 1; }
  printf '%s\n' "$p"
}

fence_config_init() {
  local profile="${PFM_FENCE_PROFILE-auto}" facts os cpus memory native_cpu native_mem
  case "$profile" in
    desktop|native) ;;
    auto)
      facts="$(docker info --format '{{.OperatingSystem}}|{{.NCPU}}|{{.MemTotal}}' 2>&1)" || { echo "iso: PFM_FENCE_PROFILE detection failed: $facts" >&2; return 1; }
      IFS='|' read -r os cpus memory <<<"$facts"
      [[ "$cpus" =~ ^[1-9][0-9]*$ && "$memory" =~ ^[1-9][0-9]*$ ]] || { echo "iso: PFM_FENCE_PROFILE detection returned invalid Docker resources: $facts" >&2; return 1; }
      case "$os" in *[Dd]esktop*) profile=desktop ;; *[Ll]inux*|Ubuntu*|Debian*|Fedora*) profile=native ;; *) echo "iso: PFM_FENCE_PROFILE unsupported Docker OperatingSystem: $os" >&2; return 1 ;; esac
      if [[ "$profile" == native ]]; then
        native_cpu="$(PFM_FENCE_PROFILE=native _fence_config_default PFM_FENCE_CPUS)" || return 1
        native_mem="$(_fence_sh_bytes "$(PFM_FENCE_PROFILE=native _fence_config_default PFM_FENCE_MEM)")" || return 1
        if ! awk -v have_cpu="$cpus" -v have_mem="$memory" -v need_cpu="$native_cpu" -v need_mem="$native_mem" 'BEGIN {exit !(have_cpu>=need_cpu && have_mem>=need_mem)}'; then
          profile=desktop # A smaller native daemon keeps the conservative baseline.
        fi
      fi ;;
    *) echo "iso: PFM_FENCE_PROFILE must be auto, desktop or native (got '$profile')" >&2; return 1 ;;
  esac
  PFM_FENCE_PROFILE="$profile"; export PFM_FENCE_PROFILE
  fence_config_values
}

_fence_sh_sha12() { # _fence_sh_sha12 <text>
  local out
  if command -v sha256sum >/dev/null 2>&1; then out="$(printf '%s' "$1" | sha256sum)"
  else out="$(printf '%s' "$1" | shasum -a 256)"; fi
  printf '%s\n' "${out:0:12}"
}

fence_shared_init() {
  local common tree
  common="$(cd "$PFM_DEV_GIT_COMMON" && pwd -P)" || { echo "iso: the common Git directory $PFM_DEV_GIT_COMMON cannot be entered" >&2; return 1; }
  FENCE_SH_MAIN="$(cd "$(dirname "$common")" && pwd -P)" || return 1
  tree="$(cd "$REPO_ROOT" && pwd -P)" || return 1
  FENCE_SH_TREE="$tree"
  FENCE_SH_EXT=""
  case "$tree/" in "$FENCE_SH_MAIN/"*) ;; *) FENCE_SH_EXT="$tree" ;; esac
  FENCE_SH_PROJECT="$(basename "$FENCE_SH_MAIN")"; FENCE_SH_PROJECT="${FENCE_SH_PROJECT#.}"
  FENCE_SH_HOST="/tmp/$FENCE_SH_PROJECT/fence/shared"
  FENCE_SH_LOCK="/tmp/$FENCE_SH_PROJECT/fence/shared.lock.d"
  FENCE_SH_ROOTS="$(_fence_sh_sha12 "$FENCE_SH_MAIN|$FENCE_SH_EXT")"
  FENCE_SH_WT="$(basename "$tree")"; FENCE_SH_WT="${FENCE_SH_WT#.}"
  fence_config_init || return 1
  FENCE_SH_CPUS="$PFM_FENCE_CPUS"
  FENCE_SH_MEM="$PFM_FENCE_MEM"
  FENCE_SH_CPUSET="$PFM_FENCE_CPUSET"
  mkdir -p "$FENCE_SH_HOST/.runs" "$FENCE_SH_HOST/$FENCE_SH_WT/timing" || { echo "iso: $FENCE_SH_HOST could not be created" >&2; return 1; }
}

fence_shared_map() { # fence_shared_map <host path>: its path inside the container, or return 1
  local p
  p="$(cd "$1" 2>/dev/null && pwd -P)" || return 1
  case "$p/" in
    "$FENCE_SH_MAIN/"*) printf '/repo%s\n' "${p#"$FENCE_SH_MAIN"}" ;;
    *) [[ -n "$FENCE_SH_EXT" ]] || return 1
       case "$p/" in "$FENCE_SH_EXT/"*) printf '/repo-ext%s\n' "${p#"$FENCE_SH_EXT"}" ;; *) return 1 ;; esac ;;
  esac
}

_fence_sh_bytes() { # _fence_sh_bytes <5g|6144m|…>: bytes
  local v="$1" n="${1%[kKmMgG]}"
  case "$v" in
    *[kK]) echo $((n * 1024)) ;; *[mM]) echo $((n * 1024 * 1024)) ;; *[gG]) echo $((n * 1024 * 1024 * 1024)) ;; *) echo "$n" ;;
  esac
}

_fence_sh_nanocpus() { awk -v c="$1" 'BEGIN { printf "%.0f\n", c * 1000000000 }'; }

_fence_sh_name() { printf 'pfm-fence-%s-%s\n' "$FENCE_SH_ROOTS" "${PFM_DEV_INPUTS_KEY:0:12}"; }

_fence_sh_caps() { # Called only with the common host admission lock held.
  local want_cpu want_mem live out state cpu="$2" mem="$3" cpuset="${4:-}" swap="${5:-$3}"
  want_cpu="$(_fence_sh_nanocpus "$FENCE_SH_CPUS")"; want_mem="$(_fence_sh_bytes "$FENCE_SH_MEM")"
  if [[ "$cpu" != "$want_cpu" || "$mem" != "$want_mem" || "$cpuset" != "$FENCE_SH_CPUSET" || "$swap" != "$want_mem" ]]; then
    live="$(docker exec "$1" bash -c 'n=0; for f in /var/fence/live/*; do [ -f "$f" ] || continue; p=$(cat "$f" 2>/dev/null) || { [ ! -e "$f" ] && continue; echo "cannot read live record $f" >&2; exit 1; }; if kill -0 "$p" 2>/dev/null; then n=$((n+1)); fi; done; echo "$n"' fence-live-count 2>&1)" || { echo "iso: fence live-run detection failed: $live" >&2; return 1; }
    [[ "$live" =~ ^[0-9]+$ ]] || { echo "iso: fence live-run detection returned invalid count: $live" >&2; return 1; }
    if (( live > 0 )); then
      out="$(awk -v n="$cpu" 'BEGIN {printf "%g", n/1000000000}')"
      echo "fence: CAPS CONFLICT requested cpus=$FENCE_SH_CPUS mem=$FENCE_SH_MEM cpuset=$FENCE_SH_CPUSET — running under live cpus=$out mem=$mem cpuset=$cpuset (another run is live)" >&2
    elif [[ "$want_cpu" == 0 && "$cpu" != 0 || -z "$FENCE_SH_CPUSET" && -n "$cpuset" ]]; then
      # Docker update ignores zero NanoCPUs and an empty cpuset. Recreate only
      # this idle container; its persistent cache volumes and timing stay intact.
      if out="$(docker rm -f "$1" 2>&1)"; then
        echo "fence: recreating idle $1 to clear CPU quota or cpuset" >&2
        return 75
      fi
      echo "WARN fence: idle recreation of $1 refused — keeping live caps: $out" >&2
    elif out="$(docker update --cpus "$FENCE_SH_CPUS" --memory "$FENCE_SH_MEM" --memory-swap "$FENCE_SH_MEM" --cpuset-cpus "$FENCE_SH_CPUSET" "$1" 2>&1)"; then
      state="$(docker inspect -f '{{.HostConfig.NanoCpus}} {{.HostConfig.Memory}} {{.HostConfig.CpusetCpus}}|{{.HostConfig.MemorySwap}}' "$1" 2>&1)" || { echo "iso: effective caps inspection failed for $1: $state" >&2; return 1; }
      read -r cpu mem cpuset <<<"${state%%|*}"; swap="${state#*|}"; [[ "$swap" != "$state" ]] || swap="$mem"
      [[ "$cpu" =~ ^[0-9]+$ && "$mem" =~ ^[1-9][0-9]*$ && "$swap" =~ ^-?[0-9]+$ ]] || { echo "iso: invalid effective caps inspection for $1: $state" >&2; return 1; }
      if [[ "$cpu" == "$want_cpu" && "$mem" == "$want_mem" && "$cpuset" == "$FENCE_SH_CPUSET" && "$swap" == "$want_mem" ]]; then
        echo "fence: $1 caps now cpus=$FENCE_SH_CPUS mem=$FENCE_SH_MEM cpuset=$FENCE_SH_CPUSET" >&2
        return 0
      fi
      echo "WARN fence: $1 update did not apply every requested cap — using inspected live caps cpus=$cpu mem=$mem cpuset=$cpuset swap=$swap" >&2
    else
      echo "WARN fence: docker update of $1 refused — the run goes on under the running caps: ${out//$'\n'/ }" >&2
    fi
  else return 0
  fi
  FENCE_SH_CPUS="$(awk -v n="$cpu" 'BEGIN {printf "%g", n/1000000000}')"
  FENCE_SH_MEM="$mem"; FENCE_SH_CPUSET="$cpuset"
}

_fence_sh_reserve() {
  [[ -n "${FENCE_SH_PENDING_ID:-}" ]] || return 0
  # PID 1 holds the reservation until the supervisor replaces it with its PID. The host
  # releases it on every exec exit (including failures before the supervisor starts).
  FENCE_SH_RESERVATION_ATTEMPTED=1
  docker exec "$FENCE_SH_NAME" bash -c 'mkdir -p /var/fence/live; echo 1 > "/var/fence/live/$1"' fence-reserve "$FENCE_SH_PENDING_ID" \
    || { echo "iso: fence live-run reservation failed for $FENCE_SH_PENDING_ID" >&2; return 1; }
}

fence_shared_ensure() {
  local rc=0
  _fence_sh_lock || return 1
  _fence_sh_reap_runs
  _fence_sh_ensure_locked || rc=$?
  if [[ $rc == 0 ]]; then _fence_sh_reserve || rc=$?; fi
  _fence_sh_unlock
  return "$rc"
}

_fence_sh_lock() { # _fence_sh_lock [owner pid, default $$]: the start lock; a stale one is broken
  local i me="${1:-$$}"
  mkdir -p "${FENCE_SH_LOCK%/*}" || return 1
  for ((i = 0; i < PFM_FENCE_LOCK_TICKS; i++)); do
    if mkdir_lock_try "$FENCE_SH_LOCK" "$me"; then return 0; fi
    (( i % 50 != 0 )) || [[ $i == 0 ]] || echo "fence: waiting for the container start held by pid ${MKDIR_LOCK_HOLDER:-?} ($FENCE_SH_LOCK)" >&2
    sleep "$PFM_FENCE_LOCK_POLL_S"
  done
  echo "iso: the fence start lock $FENCE_SH_LOCK is still held by pid ${MKDIR_LOCK_HOLDER:-?} after $PFM_FENCE_LOCK_TICKS polls of $PFM_FENCE_LOCK_POLL_S s" >&2
  return 1
}

_fence_sh_unlock() { rm -rf "$FENCE_SH_LOCK"; }

# The container's main process: it reaps the live-run records whose supervisor died (killing the
# run's namespaces first), and exits — `run --rm` removes the container — once no run is live and the
# container is superseded or idle PFM_FENCE_IDLE_S. Before exiting it creates /var/fence/closing and
# looks once more, so a run that registered meanwhile keeps it alive; a run that finds closing set
# exits 75 and its caller starts a new container.
# shellcheck disable=SC2016 # the daemon's text expands inside the container
FENCE_SHARED_DAEMON='
mkdir -p /var/fence/live /var/fence/runs
idle="$PFM_FENCE_IDLE_S"; last=$(date +%s)
trap "exit 0" TERM INT
count() {
  local n=0 f pid id ns
  for f in /var/fence/live/*; do
    [ -e "$f" ] || continue
    id=${f##*/}; pid=$(cat "$f" 2>/dev/null)
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then n=$((n + 1)); continue; fi
    ns=$(cat "/var/fence/runs/$id/ns.pid" 2>/dev/null)
    [ -z "$ns" ] || kill -KILL "$ns" 2>/dev/null
    rm -rf "/var/fence/runs/$id" "$f"
  done
  echo "$n"
}
while :; do
  n=$(count); now=$(date +%s)
  if [ "$n" -gt 0 ]; then last=$now
  elif [ -e /var/fence/superseded ] || [ $((now - last)) -ge "$idle" ]; then
    mkdir /var/fence/closing 2>/dev/null
    [ "$(count)" -gt 0 ] || exit 0
    rmdir /var/fence/closing; last=$now
  fi
  sleep "$PFM_FENCE_DAEMON_POLL_S"
done'

# _fence_sh_image: FENCE_SH_IMAGE, the image this key's container runs — the tag <image>:k-<key12>,
# pinned to an image whose pfm.fence.inputs label is this key. The plain tag is shared with iso sim's
# compose run and with every checkout whose inputs differ, any of which may rebuild it between this
# call's build and its start; the key's own tag, made from an image id whose label was read, cannot
# move. A plain tag another checkout rebuilt meanwhile is built again, up to three times.
_fence_sh_image() {
  local tag="$FENCE_SHARED_IMAGE:k-${PFM_DEV_INPUTS_KEY:0:12}" got id attempt
  got="$(docker image inspect -f '{{index .Config.Labels "pfm.fence.inputs"}}' "$tag" 2>/dev/null)" || got=""
  if [[ "$got" == "$PFM_DEV_INPUTS_KEY" ]]; then FENCE_SH_IMAGE="$tag"; return 0; fi
  for ((attempt=1; attempt<=PFM_FENCE_IMAGE_ATTEMPTS; attempt++)); do
    if [[ $attempt -gt 1 || ${#FENCE_IMAGE_BUILD[@]} -gt 0 ]]; then
      docker compose -f "$FENCE_SH_COMPOSE" build pfm-dev >&2 \
        || { echo "iso: the $FENCE_SHARED_IMAGE image could not be built — see the build output above" >&2; return 1; }
    fi
    id=""; got=""
    read -r id got < <(docker image inspect -f '{{.Id}} {{index .Config.Labels "pfm.fence.inputs"}}' "$FENCE_SHARED_IMAGE" 2>/dev/null) || true
    if [[ -n "$id" && "$got" == "$PFM_DEV_INPUTS_KEY" ]] && docker tag "$id" "$tag" >/dev/null 2>&1; then
      FENCE_SH_IMAGE="$tag"; return 0
    fi
    echo "fence: $FENCE_SHARED_IMAGE carries inputs ${got:-none}, not ${PFM_DEV_INPUTS_KEY:0:12} — another checkout rebuilt it; building again" >&2
  done
  echo "iso: no $FENCE_SHARED_IMAGE image with inputs ${PFM_DEV_INPUTS_KEY:0:12} after $PFM_FENCE_IMAGE_ATTEMPTS builds — another checkout keeps rebuilding the tag" >&2
  return 1
}

# _fence_sh_untag_stale: every other key's k- tag that no running shared container runs is removed, so
# its image turns dangling and fence housekeeping prunes it (detached, after a container start, under
# the start lock: a starter holds it from resolving its tag through `docker run`, so a tag never goes
# between the two).
_fence_sh_untag_stale() {
  local tag used
  used="$(docker ps --filter label=pfm.fence.long-lived=1 --format '{{.Image}}' 2>/dev/null)" || return 0
  while read -r tag; do
    [[ -n "$tag" && "$tag" != "$FENCE_SH_IMAGE" ]] || continue
    ! grep -qxF -- "$tag" <<<"$used" || continue
    if docker image rm "$tag" >/dev/null 2>&1; then echo "fence: untagged $tag — no shared container runs it"; fi
  done < <(docker image ls --filter "reference=$FENCE_SHARED_IMAGE:k-*" --format '{{.Repository}}:{{.Tag}}' 2>/dev/null)
}

# _fence_sh_reap_runs: every host run directory whose caller is gone (the pid in its id
# <stamp>-<pid>-<rand>) is removed — a SIGKILLed caller never reaches its own rm, and the directory
# may hold a copy of the private leak terms.
_fence_sh_reap_runs() {
  local d id pid containers container out cleared
  for d in "$FENCE_SH_HOST"/.runs/*; do
    [[ -d "$d" ]] || continue
    id="${d##*/}"; pid="${id#*-}"; pid="${pid%%-*}"
    [[ "$pid" =~ ^[1-9][0-9]*$ ]] || continue
    kill -0 "$pid" 2>/dev/null && continue
    # Host run records span image keys. Clear the dead caller's reservation in
    # every running container for these roots, not just this caller's image key.
    if ! containers="$(docker ps --filter "label=pfm.fence.shared=$FENCE_SH_ROOTS" --format '{{.Names}}' 2>&1)"; then
      echo "WARN fence: cannot enumerate stale reservation owners for $id: $containers; retained $d for retry" >&2
      continue
    fi
    cleared=1
    while IFS= read -r container; do
      [[ -n "$container" ]] || continue
      if ! out="$(docker exec "$container" bash -c '[ "$(cat "/var/fence/live/$1" 2>/dev/null)" != 1 ] || rm -f "/var/fence/live/$1"' fence-stale-reservation "$id" 2>&1)"; then
        echo "WARN fence: reservation cleanup in $container failed for $id: $out; retained $d for retry" >&2
        cleared=0
      fi
    done <<<"$containers"
    [[ "$cleared" == 1 ]] || continue
    if rm -rf "$d"; then echo "fence: reaped the run directory $d (its caller $pid is gone)"; else echo "fence: the run directory $d could not be reaped"; fi
  done
}

# _fence_sh_after_start: the detached housekeeping of one container start, in its own subshell.
_fence_sh_after_start() {
  local log="/tmp/$FENCE_SH_PROJECT/fence/housekeeping.log" me size
  size="$(wc -c <"$log" 2>/dev/null || echo 0)"
  if [[ "${size// /}" -gt 1048576 ]]; then mv -f "$log" "$log.1" 2>/dev/null || true; fi
  {
    me="$(exec sh -c 'echo "$PPID"')" # this subshell's pid: $$ is the caller's, which may exit first
    if _fence_sh_lock "$me"; then
      _fence_sh_untag_stale; _fence_sh_unlock
    else echo "fence: the stale k- tags stay — the start lock could not be taken"; fi
    _fence_sh_reap_runs
    fence_housekeeping
  } </dev/null >>"$log" 2>&1
}

# _fence_sh_leave_old_key: when this worktree's last key (<worktree>/key) is another, this worktree
# leaves that key's container; one no worktree uses any more is superseded (it drains, then exits).
_fence_sh_leave_old_key() {
  local f="$FENCE_SH_HOST/$FENCE_SH_WT/key" old oname
  old="$(cat "$f" 2>/dev/null || true)"
  [[ "$old" != "$PFM_DEV_INPUTS_KEY" ]] || return 0
  printf '%s\n' "$PFM_DEV_INPUTS_KEY" >"$f" || echo "WARN fence: $f could not be written — the next call looks for an old container again" >&2
  [[ -n "$old" ]] || return 0
  oname="pfm-fence-$FENCE_SH_ROOTS-${old:0:12}"
  # shellcheck disable=SC2016 # expands inside the container
  if docker exec "$oname" sh -c 'rm -f "/var/fence/worktrees/$1"; [ -n "$(ls -A /var/fence/worktrees 2>/dev/null)" ] || touch /var/fence/superseded' _ "$FENCE_SH_WT" >/dev/null 2>&1; then
    echo "fence: $FENCE_SH_WT left $oname for image ${PFM_DEV_INPUTS_KEY:0:12} — it exits once no other worktree uses it and its live runs end" >&2
  fi
}

_fence_sh_running() { [[ "$(docker inspect -f '{{.State.Running}}' "$FENCE_SH_NAME" 2>/dev/null)" == true ]]; }

_fence_sh_gone() { # the container stopped or vanished: 0 once it is not running, within about 2 s
  local i
  for ((i = 0; i < PFM_FENCE_GONE_TICKS; i++)); do
    _fence_sh_running || return 0
    sleep "$PFM_FENCE_LOCK_POLL_S"
  done
  return 1
}

_fence_sh_ensure_locked() {
  local name state running cpu mem cpuset swap attempt out rc
  local -a mounts
  name="$(_fence_sh_name)"
  FENCE_SH_NAME="$name"
  if state="$(docker inspect -f '{{.State.Running}} {{.HostConfig.NanoCpus}} {{.HostConfig.Memory}} {{.HostConfig.CpusetCpus}}|{{.HostConfig.MemorySwap}}' "$name" 2>/dev/null)"; then
    read -r running cpu mem cpuset <<<"${state%%|*}"; swap="${state#*|}"; [[ "$swap" != "$state" ]] || swap="$mem"
    if [[ "$running" == true ]]; then
      rc=0; _fence_sh_caps "$name" "$cpu" "$mem" "${cpuset:-}" "$swap" || rc=$?
      [[ "$rc" == 75 ]] || return "$rc"
    fi
  fi
  _fence_sh_image || { return 1; }
  fence_volumes_ensure
  mounts=(-v "$FENCE_SH_MAIN:/repo:ro" -v "$PFM_DEV_GIT_COMMON:/pfm-git-common:ro" -v "$FENCE_SH_HOST:/pfm-fence"
    -v pfm-dev-gocache:/cache/go-build -v pfm-dev-gomod:/cache/gomod -v pfm-dev-lintcache:/cache/lint)
  [[ -z "$FENCE_SH_EXT" ]] || mounts+=(-v "$FENCE_SH_EXT:/repo-ext:ro")
  for ((attempt=1; attempt<=PFM_FENCE_START_ATTEMPTS; attempt++)); do
    if out="$(docker run -d --rm --init --name "$name" --hostname "pfm-fence-${FENCE_SH_ROOTS:0:6}" \
      --label pfm.fence=1 --label pfm.fence.long-lived=1 --label "pfm.fence.shared=$FENCE_SH_ROOTS" \
      --label "pfm.fence.inputs=$PFM_DEV_INPUTS_KEY" \
      --cpus "$FENCE_SH_CPUS" --memory "$FENCE_SH_MEM" --memory-swap "$FENCE_SH_MEM" --cpuset-cpus "$FENCE_SH_CPUSET" \
      --cap-add SYS_ADMIN --cap-add NET_ADMIN --security-opt apparmor=unconfined --security-opt systempaths=unconfined \
      "${mounts[@]}" -w / \
      -e PFM_DEV_FENCE=1 -e GOFLAGS=-buildvcs=false -e GOCACHE=/cache/go-build -e GOMODCACHE=/cache/gomod \
      -e GOLANGCI_LINT_CACHE=/cache/lint -e "PFM_FENCE_IDLE_S=$PFM_FENCE_IDLE_S" -e "PFM_FENCE_DAEMON_POLL_S=$PFM_FENCE_DAEMON_POLL_S" \
      "$FENCE_SH_IMAGE" bash -c "$FENCE_SHARED_DAEMON" 2>&1)"; then
      echo "fence: started $name (cpus=$FENCE_SH_CPUS mem=$FENCE_SH_MEM image ${PFM_DEV_INPUTS_KEY:0:12})" >&2
      # Housekeeping leaves the per-call path: detached, once per container start, off the caller's
      # stdin, stdout and stderr, so a caller reading this call's output (test.sh's tee) never waits on it.
      ( _fence_sh_after_start ) </dev/null >/dev/null 2>&1 &
      return 0
    fi
    # The name in use: a container another starter made running (use it), or the same name still
    # exiting (`--rm` removes it a moment after exit: wait for it to go).
    case "$out" in *"is already in use"*)
      if state="$(docker inspect -f '{{.State.Running}} {{.HostConfig.NanoCpus}} {{.HostConfig.Memory}} {{.HostConfig.CpusetCpus}}|{{.HostConfig.MemorySwap}}' "$name" 2>/dev/null)"; then
        read -r running cpu mem cpuset <<<"${state%%|*}"; swap="${state#*|}"; [[ "$swap" != "$state" ]] || swap="$mem"
        if [[ "$running" == true ]]; then _fence_sh_caps "$name" "$cpu" "$mem" "${cpuset:-}" "$swap"; return $?; fi
      fi
      sleep "$PFM_FENCE_START_RETRY_S"; continue ;;
    esac
    break
  done
  echo "iso: the shared fence container $name did not start: ${out//$'\n'/ }" >&2
  return 1
}

# _fence_sh_feed <fifo> <cli pid file> <run id> <tree in container> <pid>...: the heartbeat. While
# every pid lives it writes a beat to the run's stdin each 0.5 s; the first dead pid ends it, closing
# the stream (the run is killed on EOF), and the docker CLI still running 3 s later is killed after
# shared-run.sh's kill of the run.
_fence_sh_feed() {
  local fifo="$1" pidfile="$2" id="$3" tree="$4" p alive i cli
  shift 4
  exec 3>"$fifo" || exit 1
  while :; do
    alive=1
    for p in "$@"; do kill -0 "$p" 2>/dev/null || { alive=0; break; }; done
    [[ $alive == 1 ]] || break
    printf '.\n' >&3 2>/dev/null || exit 0
    sleep "$PFM_FENCE_BEAT_S"
  done
  exec 3>&-
  cli="$(cat "$pidfile" 2>/dev/null || true)"
  for ((i = 0; i < PFM_FENCE_FEED_GRACE_TICKS; i++)); do
    [[ -n "$cli" ]] && kill -0 "$cli" 2>/dev/null || exit 0
    sleep 0.1
  done
  docker exec "$FENCE_SH_NAME" bash "$tree/infra/fence/shared-run.sh" kill "$id" >/dev/null 2>&1 || true
  kill -TERM "$cli" 2>/dev/null || true
  exit 0
}

_fence_sh_stop() { # _fence_sh_stop <feeder> <cli> <id> <tree> <host run dir>: a TERM or INT to the caller
  kill -KILL "$1" 2>/dev/null || true
  docker exec "$FENCE_SH_NAME" bash "$4/infra/fence/shared-run.sh" kill "$3" >/dev/null 2>&1 || true
  wait "$2" 2>/dev/null || true
  docker exec "$FENCE_SH_NAME" rm -f "/var/fence/live/$3" >/dev/null 2>&1 || true
  rm -rf "$5" || true
}

# A failed setup has no supervisor to clear its PID-1 reservation. Release it before
# deleting the host record; if Docker refuses, retain that record for the dead-caller reaper.
_fence_sh_abort_setup() { # <id> <host run dir>
  if [[ "${FENCE_SH_RESERVATION_ATTEMPTED:-0}" != 1 ]]; then
    rm -rf "$2" || echo "WARN fence: failed setup directory $2 could not be removed" >&2
    return
  fi
  if docker exec "${FENCE_SH_NAME:-$(_fence_sh_name)}" rm -f "/var/fence/live/$1" >/dev/null 2>&1; then
    rm -rf "$2" || echo "WARN fence: failed setup directory $2 could not be removed" >&2
  else
    echo "WARN fence: reservation cleanup failed for $1; retained $2 for the dead-caller reaper" >&2
  fi
}

# fence_shared_exec <private|shared|tty> <command string>: one run in the shared container, from the
# caller's tree mounted at /worktree. FENCE_SH_ENV (an array the caller fills) adds -e pairs.
fence_shared_exec() {
  local mode="$1" cmd="$2" id hdir tree es="-" hs="-" terms rc attempt fifo feeder cli
  local requested_cpu="$FENCE_SH_CPUS" requested_mem="$FENCE_SH_MEM" requested_cpuset="$FENCE_SH_CPUSET"
  local -a envs watch
  tree="$(fence_shared_map "$FENCE_SH_TREE")" || { echo "iso: $FENCE_SH_TREE is not under the mounted roots" >&2; return 1; }
  if [[ -z "${PFM_DEV_ENGINE_SYNC_MISSING:-}" ]]; then
    es="$(fence_shared_map "$PFM_DEV_ENGINE_SYNC_SRC")" || {
      echo "iso: the engine-sync source $PFM_DEV_ENGINE_SYNC_SRC is outside the shared container's roots ($FENCE_SH_MAIN${FENCE_SH_EXT:+, $FENCE_SH_EXT}) — set ENGINE_SYNC_SRC to a checkout inside them" >&2
      return 1; }
  fi
  if [[ -z "${PFM_DEV_HARVESTER_MISSING:-}" && -n "${PFM_DEV_HARVESTER_SRC:-}" ]]; then
    hs="$(fence_shared_map "$PFM_DEV_HARVESTER_SRC")" || {
      echo "iso: the harvester source $PFM_DEV_HARVESTER_SRC is outside the shared container roots — set HARVESTER_SRC inside them" >&2
      return 1; }
  fi
  for ((attempt=1; attempt<=PFM_FENCE_EXEC_ATTEMPTS; attempt++)); do
    FENCE_SH_CPUS="$requested_cpu"; FENCE_SH_MEM="$requested_mem"; FENCE_SH_CPUSET="$requested_cpuset"
    id="$(date -u +%Y%m%dT%H%M%SZ)-$$-$RANDOM$RANDOM"
    FENCE_SH_PENDING_ID="$id"
    FENCE_SH_RESERVATION_ATTEMPTED=0
    hdir="$FENCE_SH_HOST/.runs/$id"
    mkdir -p "$hdir" || { echo "iso: the run directory $hdir could not be created" >&2; unset FENCE_SH_PENDING_ID; return 1; }
    if ! fence_shared_ensure; then
      _fence_sh_abort_setup "$id" "$hdir"
      unset FENCE_SH_PENDING_ID
      if [[ $attempt -lt $PFM_FENCE_EXEC_ATTEMPTS ]] && _fence_sh_gone; then
        echo "fence: $FENCE_SH_NAME closed during admission — starting a new container" >&2
        sleep "$PFM_FENCE_RETRY_S"; continue
      fi
      return 1
    fi
    unset FENCE_SH_PENDING_ID
    _fence_sh_leave_old_key
    envs=(-e "PFM_FENCE_RUN_ID=$id" -e "PFM_DEV_REPO_GIT_DIR=/pfm-git-common/$PFM_DEV_GIT_DIR_REL" -e PFM_DEV_REPO_WORK_TREE=/worktree
      -e "PFM_DEV_ENGINE_SYNC_MISSING=${PFM_DEV_ENGINE_SYNC_MISSING:-}" -e PFM_TEST_TIMING_DIR=/pfm-timing
      -e "HARVESTER_FENCE_SRC=$hs" -e "PFM_DEV_HARVESTER_MISSING=${PFM_DEV_HARVESTER_MISSING:-}")
    # Gate knobs: a suite the gate nests starts from none of them (scripts/shtest.sh
    # shtest_unset_gate_env; infra/fence/lanes/tests/steps_test.sh derives this block's names).
    # Only generated timing artifacts are writable: /pfm-timing is the worktree's timing base.
    envs+=(-e "PFM_FENCE_CPUS=$FENCE_SH_CPUS" -e "PFM_FENCE_HEARTBEAT_S=$PFM_FENCE_HEARTBEAT_S"
      -e "PFM_TEST_TIMING_HOST=$FENCE_SH_HOST/$FENCE_SH_WT/timing")
    [[ -z "${PFM_FENCE_TEST_P:-}" ]] || envs+=(-e "PFM_FENCE_TEST_P=$PFM_FENCE_TEST_P")
    [[ -z "${TIMING_RUN_NOTE:-}" ]] || envs+=(-e "PFM_TEST_RUN_NOTE=/pfm-timing/${TIMING_RUN_NOTE##*/}")
    # end of the gate knobs
    terms="${FENCE_SH_TERMS:-}"
    if [[ -n "$terms" && -f "$terms" ]]; then
      cp "$terms" "$hdir/leak-terms.txt" || { echo "iso: the leak terms $terms could not be staged" >&2; _fence_sh_abort_setup "$id" "$hdir"; return 1; }
      envs+=(-e LEAK_TERMS=/pfm-run/leak-terms.txt)
    fi
    local knob
    while IFS='|' read -r knob _ _ _; do
      case "$knob" in PFM_FENCE_CPUS|PFM_FENCE_MEM|PFM_FENCE_CPUSET) continue ;; esac
      envs+=(-e "$knob=${!knob}")
    done < <(fence_config_schema)
    envs+=(-e "PFM_FENCE_PROFILE=$PFM_FENCE_PROFILE" -e "PFM_FENCE_CPUS=$FENCE_SH_CPUS" -e "PFM_FENCE_MEM=$FENCE_SH_MEM" -e "PFM_FENCE_CPUSET=$FENCE_SH_CPUSET")
    [[ -z "$PFM_FENCE_GOMAXPROCS" ]] || envs+=(-e "GOMAXPROCS=$PFM_FENCE_GOMAXPROCS")
    echo "fence: profile=$PFM_FENCE_PROFILE cpus=$FENCE_SH_CPUS mem=$FENCE_SH_MEM cpuset=$FENCE_SH_CPUSET GOMAXPROCS=${PFM_FENCE_GOMAXPROCS:-inherited}" >&2
    envs+=(${FENCE_SH_ENV[@]+"${FENCE_SH_ENV[@]}"})
    local -a argv=(bash "$tree/infra/fence/shared-run.sh" run "$id" "$mode" "$tree" "$es" "$FENCE_SH_WT" -- bash -c "$cmd")
    rc=0
    if [[ "$mode" == tty ]]; then
      docker exec -it "${envs[@]}" "$FENCE_SH_NAME" "${argv[@]}" || rc=$?
    else
      fifo="$hdir/beat"
      mkfifo "$fifo" || { echo "iso: the heartbeat fifo $fifo could not be created" >&2; _fence_sh_abort_setup "$id" "$hdir"; return 1; }
      watch=("$$")
      local w
      for w in ${PFM_FENCE_WATCH_PIDS:-}; do
        if [[ "$w" =~ ^[1-9][0-9]*$ ]]; then watch+=("$w"); fi
      done
      _fence_sh_feed "$fifo" "$hdir/cli.pid" "$id" "$tree" "${watch[@]}" &
      feeder=$!
      docker exec -i "${envs[@]}" "$FENCE_SH_NAME" "${argv[@]}" <"$fifo" &
      cli=$!
      echo "$cli" > "$hdir/cli.pid"
      # A TERM or INT to the caller stops the run before the caller goes: the beat stops (EOF in the
      # container kills the run's namespaces), then the CLI is waited for.
      trap '_fence_sh_stop "$feeder" "$cli" "$id" "$tree" "$hdir"; exit 143' TERM
      trap '_fence_sh_stop "$feeder" "$cli" "$id" "$tree" "$hdir"; exit 130' INT
      wait "$cli" || rc=$?
      trap - TERM INT
      kill -KILL "$feeder" 2>/dev/null || true
      wait "$feeder" 2>/dev/null || true
    fi
    docker exec "$FENCE_SH_NAME" rm -f "/var/fence/live/$id" >/dev/null 2>&1 || echo "WARN fence: reservation cleanup failed for $id" >&2
    # Before the run started, the container was closing (exit 75) or stopped or vanished between
    # ensure and exec (docker's own refusal): start a new one, once.
    if [[ $rc != 0 && ! -e "$hdir/started" &&  $attempt -lt $PFM_FENCE_EXEC_ATTEMPTS ]] && { [[ $rc == 75 ]] || _fence_sh_gone; }; then
      echo "fence: $FENCE_SH_NAME closed before the run started (exit $rc) — starting a new container" >&2
      rm -rf "$hdir"; sleep "$PFM_FENCE_RETRY_S"; continue
    fi
    rm -rf "$hdir"
    return "$rc"
  done
  return "$rc"
}

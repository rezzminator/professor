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
#   fence_shared_ensure            # the container runs, with this call's caps; 1 = could not
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
#                       a caller whose caps differ from the running container's applies its own with
#                       `docker update` — the caps are the container's, every live run shares them
#   PFM_FENCE_TEST_P    go test -p (pfm/Makefile TESTFLAGS); unset, the container's CPU cap
#   PFM_FENCE_IDLE_S (1800)       a container with no live run this long exits (read at creation)
#   PFM_FENCE_HEARTBEAT_S (4)     a run whose caller's heartbeat stops this long is killed
#   PFM_FENCE_NET (private)       iso run's network: shared opts into the container's network
# Only creation is serialized: the lock directory /tmp/{project}/fence/shared.lock.d around inspect,
# image build and `docker run`; runs never queue behind each other. A new image key starts a new
# container and marks the root's other containers superseded: each drains its live runs, then exits.
# Housekeeping (infra/fence/housekeeping.sh) runs detached, only when a container is created; its
# output goes to /tmp/{project}/fence/housekeeping.log.
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
  FENCE_SH_CPUS="${PFM_FENCE_CPUS:-6}"
  FENCE_SH_MEM="${PFM_FENCE_MEM:-5g}"
  [[ "$FENCE_SH_CPUS" =~ ^[0-9]+(\.[0-9]+)?$ ]] || { echo "iso: PFM_FENCE_CPUS must be a number of CPUs: $FENCE_SH_CPUS" >&2; return 1; }
  [[ "$FENCE_SH_MEM" =~ ^[0-9]+[kKmMgG]?$ ]] || { echo "iso: PFM_FENCE_MEM must be a size like 5g or 6144m: $FENCE_SH_MEM" >&2; return 1; }
  if [[ -n "${PFM_FENCE_TEST_P:-}" && ! "$PFM_FENCE_TEST_P" =~ ^[1-9][0-9]*$ ]]; then
    echo "iso: PFM_FENCE_TEST_P must be a positive integer: $PFM_FENCE_TEST_P" >&2; return 1
  fi
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

_fence_sh_caps() { # _fence_sh_caps <name> <nanocpus now> <memory now>
  local want_cpu want_mem out
  want_cpu="$(_fence_sh_nanocpus "$FENCE_SH_CPUS")"; want_mem="$(_fence_sh_bytes "$FENCE_SH_MEM")"
  [[ "$2" != "$want_cpu" || "$3" != "$want_mem" ]] || return 0
  if out="$(docker update --cpus "$FENCE_SH_CPUS" --memory "$FENCE_SH_MEM" --memory-swap "$FENCE_SH_MEM" "$1" 2>&1)"; then
    echo "fence: $1 caps now cpus=$FENCE_SH_CPUS mem=$FENCE_SH_MEM" >&2
  else
    echo "WARN fence: docker update of $1 to cpus=$FENCE_SH_CPUS mem=$FENCE_SH_MEM refused — the run goes on under the running caps: ${out//$'\n'/ }" >&2
  fi
}

_fence_sh_lock() { # mkdir lock with the holder's pid; a dead holder's lock is broken
  local i holder
  mkdir -p "${FENCE_SH_LOCK%/*}" || return 1
  for ((i = 0; i < 3000; i++)); do
    if mkdir "$FENCE_SH_LOCK" 2>/dev/null; then echo "$$" > "$FENCE_SH_LOCK/pid"; return 0; fi
    holder="$(cat "$FENCE_SH_LOCK/pid" 2>/dev/null || true)"
    if [[ -n "$holder" ]] && ! kill -0 "$holder" 2>/dev/null; then
      rm -rf "$FENCE_SH_LOCK"; continue
    fi
    if [[ -z "$holder" && -n "$(find "$FENCE_SH_LOCK" -maxdepth 0 -mmin +1 2>/dev/null)" ]]; then
      rm -rf "$FENCE_SH_LOCK"; continue
    fi
    (( i % 50 != 0 )) || [[ $i == 0 ]] || echo "fence: waiting for the container start held by pid ${holder:-?} ($FENCE_SH_LOCK)" >&2
    sleep 0.2
  done
  echo "iso: the fence start lock $FENCE_SH_LOCK is still held by pid ${holder:-?} after 600 s" >&2
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
idle="${PFM_FENCE_IDLE_S:-1800}"; last=$(date +%s)
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
  sleep 2
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
  for attempt in 1 2 3; do
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
  echo "iso: no $FENCE_SHARED_IMAGE image with inputs ${PFM_DEV_INPUTS_KEY:0:12} after 3 builds — another checkout keeps rebuilding the tag" >&2
  return 1
}

# _fence_sh_untag_stale: every other key's k- tag that no running shared container runs is removed, so
# its image turns dangling and fence housekeeping prunes it (detached, after a container start).
_fence_sh_untag_stale() {
  local tag used
  used="$(docker ps --filter label=pfm.fence.long-lived=1 --format '{{.Image}}' 2>/dev/null)" || return 0
  while read -r tag; do
    [[ -n "$tag" && "$tag" != "$FENCE_SH_IMAGE" ]] || continue
    ! grep -qxF -- "$tag" <<<"$used" || continue
    if docker image rm "$tag" >/dev/null 2>&1; then echo "fence: untagged $tag — no shared container runs it"; fi
  done < <(docker image ls --filter "reference=$FENCE_SHARED_IMAGE:k-*" --format '{{.Repository}}:{{.Tag}}' 2>/dev/null)
}

fence_shared_ensure() {
  local name state running cpu mem other okey attempt out
  local -a mounts
  name="$(_fence_sh_name)"
  FENCE_SH_NAME="$name"
  if state="$(docker inspect -f '{{.State.Running}} {{.HostConfig.NanoCpus}} {{.HostConfig.Memory}}' "$name" 2>/dev/null)"; then
    read -r running cpu mem <<<"$state"
    if [[ "$running" == true ]]; then _fence_sh_caps "$name" "$cpu" "$mem"; return 0; fi
  fi
  _fence_sh_lock || return 1
  if state="$(docker inspect -f '{{.State.Running}} {{.HostConfig.NanoCpus}} {{.HostConfig.Memory}}' "$name" 2>/dev/null)"; then
    read -r running cpu mem <<<"$state"
    if [[ "$running" == true ]]; then _fence_sh_unlock; _fence_sh_caps "$name" "$cpu" "$mem"; return 0; fi
  fi
  _fence_sh_image || { _fence_sh_unlock; return 1; }
  # A new key supersedes this root's other containers: each drains its live runs, then exits.
  while read -r other okey; do
    [[ -n "$other" && "$other" != "$name" && "$okey" != "$PFM_DEV_INPUTS_KEY" ]] || continue
    if docker exec "$other" touch /var/fence/superseded >/dev/null 2>&1; then
      echo "fence: $other superseded by image ${PFM_DEV_INPUTS_KEY:0:12} — it exits once its live runs end" >&2
    fi
  done < <(docker ps --filter "label=pfm.fence.shared=$FENCE_SH_ROOTS" --format '{{.Names}} {{.Label "pfm.fence.inputs"}}' 2>/dev/null)
  fence_volumes_ensure
  mounts=(-v "$FENCE_SH_MAIN:/repo:ro" -v "$PFM_DEV_GIT_COMMON:/pfm-git-common:ro" -v "$FENCE_SH_HOST:/pfm-fence"
    -v pfm-dev-gocache:/cache/go-build -v pfm-dev-gomod:/cache/gomod -v pfm-dev-lintcache:/cache/lint)
  [[ -z "$FENCE_SH_EXT" ]] || mounts+=(-v "$FENCE_SH_EXT:/repo-ext:ro")
  for attempt in 1 2 3 4 5 6 7 8 9 10; do
    if out="$(docker run -d --rm --init --name "$name" --hostname "pfm-fence-${FENCE_SH_ROOTS:0:6}" \
      --label pfm.fence=1 --label pfm.fence.long-lived=1 --label "pfm.fence.shared=$FENCE_SH_ROOTS" \
      --label "pfm.fence.inputs=$PFM_DEV_INPUTS_KEY" \
      --cpus "$FENCE_SH_CPUS" --memory "$FENCE_SH_MEM" --memory-swap "$FENCE_SH_MEM" \
      --cap-add SYS_ADMIN --cap-add NET_ADMIN --security-opt apparmor=unconfined --security-opt systempaths=unconfined \
      "${mounts[@]}" -w / \
      -e PFM_DEV_FENCE=1 -e GOFLAGS=-buildvcs=false -e GOCACHE=/cache/go-build -e GOMODCACHE=/cache/gomod \
      -e GOLANGCI_LINT_CACHE=/cache/lint -e "PFM_FENCE_IDLE_S=${PFM_FENCE_IDLE_S:-1800}" \
      "$FENCE_SH_IMAGE" bash -c "$FENCE_SHARED_DAEMON" 2>&1)"; then
      _fence_sh_unlock
      echo "fence: started $name (cpus=$FENCE_SH_CPUS mem=$FENCE_SH_MEM image ${PFM_DEV_INPUTS_KEY:0:12})" >&2
      # Housekeeping leaves the per-call path: detached, once per container start.
      ( _fence_sh_untag_stale; fence_housekeeping ) </dev/null >>"/tmp/$FENCE_SH_PROJECT/fence/housekeeping.log" 2>&1 &
      return 0
    fi
    # The same name still exiting (`--rm` removes it a moment after exit): wait for it to go.
    case "$out" in *"is already in use"*) sleep 1; continue ;; esac
    break
  done
  _fence_sh_unlock
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
    sleep 0.5
  done
  exec 3>&-
  cli="$(cat "$pidfile" 2>/dev/null || true)"
  for ((i = 0; i < 30; i++)); do
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
  rm -rf "$5" || true
}

# fence_shared_exec <private|shared|tty> <command string>: one run in the shared container, from the
# caller's tree mounted at /worktree. FENCE_SH_ENV (an array the caller fills) adds -e pairs.
fence_shared_exec() {
  local mode="$1" cmd="$2" id hdir tree es="-" terms rc attempt fifo feeder cli
  local -a envs watch
  tree="$(fence_shared_map "$FENCE_SH_TREE")" || { echo "iso: $FENCE_SH_TREE is not under the mounted roots" >&2; return 1; }
  if [[ -z "${PFM_DEV_ENGINE_SYNC_MISSING:-}" ]]; then
    es="$(fence_shared_map "$PFM_DEV_ENGINE_SYNC_SRC")" || {
      echo "iso: the engine-sync source $PFM_DEV_ENGINE_SYNC_SRC is outside the shared container's roots ($FENCE_SH_MAIN${FENCE_SH_EXT:+, $FENCE_SH_EXT}) — set ENGINE_SYNC_SRC to a checkout inside them" >&2
      return 1; }
  fi
  for attempt in 1 2; do
    fence_shared_ensure || return 1
    id="$(date -u +%Y%m%dT%H%M%SZ)-$$-$RANDOM$RANDOM"
    hdir="$FENCE_SH_HOST/.runs/$id"
    mkdir -p "$hdir" || { echo "iso: the run directory $hdir could not be created" >&2; return 1; }
    envs=(-e "PFM_FENCE_RUN_ID=$id" -e "PFM_DEV_REPO_GIT_DIR=/pfm-git-common/$PFM_DEV_GIT_DIR_REL" -e PFM_DEV_REPO_WORK_TREE=/worktree
      -e "PFM_DEV_ENGINE_SYNC_MISSING=${PFM_DEV_ENGINE_SYNC_MISSING:-}" -e PFM_TEST_TIMING_DIR=/pfm-timing)
    # Gate knobs: a suite the gate nests starts from none of them (scripts/shtest.sh
    # shtest_unset_gate_env; infra/fence/lanes/tests/steps_test.sh derives this block's names).
    # Only generated timing artifacts are writable: /pfm-timing is the worktree's timing base.
    envs+=(-e "PFM_FENCE_CPUS=$FENCE_SH_CPUS" -e "PFM_FENCE_HEARTBEAT_S=${PFM_FENCE_HEARTBEAT_S:-4}"
      -e "PFM_TEST_TIMING_HOST=$FENCE_SH_HOST/$FENCE_SH_WT/timing")
    [[ -z "${PFM_FENCE_TEST_P:-}" ]] || envs+=(-e "PFM_FENCE_TEST_P=$PFM_FENCE_TEST_P")
    [[ -z "${TIMING_RUN_NOTE:-}" ]] || envs+=(-e "PFM_TEST_RUN_NOTE=/pfm-timing/${TIMING_RUN_NOTE##*/}")
    # end of the gate knobs
    terms="${FENCE_SH_TERMS:-}"
    if [[ -n "$terms" && -f "$terms" ]]; then
      cp "$terms" "$hdir/leak-terms.txt" || { echo "iso: the leak terms $terms could not be staged" >&2; rm -rf "$hdir"; return 1; }
      envs+=(-e LEAK_TERMS=/pfm-run/leak-terms.txt)
    fi
    envs+=(${FENCE_SH_ENV[@]+"${FENCE_SH_ENV[@]}"})
    local -a argv=(bash "$tree/infra/fence/shared-run.sh" run "$id" "$mode" "$tree" "$es" "$FENCE_SH_WT" -- bash -c "$cmd")
    rc=0
    if [[ "$mode" == tty ]]; then
      docker exec -it "${envs[@]}" "$FENCE_SH_NAME" "${argv[@]}" || rc=$?
    else
      fifo="$hdir/beat"
      mkfifo "$fifo" || { echo "iso: the heartbeat fifo $fifo could not be created" >&2; rm -rf "$hdir"; return 1; }
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
    # Exit 75 before the run started: the container was closing; start a new one, once.
    if [[ $rc == 75 && ! -e "$hdir/started" && $attempt == 1 ]]; then rm -rf "$hdir"; sleep 2; continue; fi
    rm -rf "$hdir"
    return "$rc"
  done
  return "$rc"
}

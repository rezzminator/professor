#!/usr/bin/env bash
# Outer orchestration only: imports a root built by dev.sh iso and boots it.
JOURNEY_CONTAINER_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
. "$JOURNEY_CONTAINER_DIR/../shared.sh" || return 1
fence_config_values || return 1

journey_fence_env() {
  local root="$1"
  ROOT="$root" FENCE_CALLER=journey . "$JOURNEY_CONTAINER_DIR/../fence-env.sh" || return 2
  REPO_ROOT="$root" fence_shared_init || return 2
  JOURNEY_CACHE_CONTROL="$FENCE_SH_HOST/journey-roots"
  mkdir -p "$JOURNEY_CACHE_CONTROL" || return 2
  touch "$JOURNEY_CACHE_CONTROL/build.lock" || return 2
  JOURNEY_CACHE_PROJECTED="$FENCE_SH_HOST/$FENCE_SH_WT/timing/journey-control"
  mkdir -p "$JOURNEY_CACHE_PROJECTED" || return 2
  # A hard link gives the nested fence the same lock inode through its timing mount.
  ln -f "$JOURNEY_CACHE_CONTROL/build.lock" "$JOURNEY_CACHE_PROJECTED/build.lock" || return 2
}

journey_cache_inventory() {
  (flock 9
   python3 "$JOURNEY_CONTAINER_DIR/root-cache.py" inventory "$JOURNEY_CACHE_CONTROL/containers.json" &&
   cp "$JOURNEY_CACHE_CONTROL/containers.json" "$JOURNEY_CACHE_PROJECTED/containers.json"
  ) 9>"$JOURNEY_CACHE_CONTROL/build.lock"
}

journey_cache_retain() {
  (flock 9
   bash "$JOURNEY_CONTAINER_DIR/root.sh" --cache-control "$FENCE_SH_HOST" "$JOURNEY_CACHE_CONTROL/containers.json" "$1"
  ) 9>"$JOURNEY_CACHE_CONTROL/build.lock"
}

journey_cache_images() {
  local plan="$1" root roots=()
  while IFS= read -r root; do roots+=("$root"); done < <(printf '%s\n' "$plan" | sed -n 's/^JOURNEY_PRUNE_IMAGE=//p')
  (flock 9; python3 "$JOURNEY_CONTAINER_DIR/root-cache.py" images "$JOURNEY_CACHE_CONTROL/containers.json" "${roots[@]}") 9>"$JOURNEY_CACHE_CONTROL/build.lock"
}

journey_checkpoint_save() {
  (flock 9
   python3 "$JOURNEY_CONTAINER_DIR/root-cache.py" checkpoints &&
   docker commit "$1" "pfm-journey-checkpoint:$2" >/dev/null &&
   python3 "$JOURNEY_CONTAINER_DIR/root-cache.py" checkpoints
  ) 9>"$JOURNEY_CACHE_CONTROL/build.lock"
}

journey_import() {
  local archive="$1" image="pfm-journey-root:$2"
  [ -s "$archive" ] || { printf 'journey-root: archive missing %s\n' "$archive" >&2; return 2; }
  if ! docker image inspect "$image" >/dev/null 2>&1; then
    docker import --change 'CMD ["/sbin/init"]' --change "LABEL pfm.fence=1 pfm.journey=1 professor.journey-root=$2" "$archive" "$image" >&2 || return 2
  fi
  printf '%s\n' "$image"
}

journey_container() { # NAME IMAGE [docker arguments]; a separate user manager per run.
  local name="$1" image="$2"
  shift 2
  (
  flock 9
  docker run -d --name "$name" --label pfm.fence=1 --label pfm.journey=1 \
    --cgroupns=private --security-opt writable-cgroups=true \
    --tmpfs /run --tmpfs /run/lock --network none -e container=docker \
    --cpus "$PFM_FENCE_CPUS" --memory "$PFM_FENCE_MEM" --memory-swap "$PFM_FENCE_MEM" \
    -v "$PFM_DEV_WORKTREE:/worktree:ro" \
    -v "$PFM_DEV_GIT_COMMON:/pfm-git-common:ro" \
    -v "$PFM_DEV_ENGINE_SYNC_SRC:/engine-sync:ro" \
    -v "$PFM_DEV_HARVESTER_SRC:/harvester:ro" \
    -e PFM_DEV_FENCE=1 -e IS_SANDBOX=1 -e LANG=C.UTF-8 \
    -e PFM_PRICES_OFFLINE=1 \
    -e ENGINE_SYNC_BIN=/usr/local/libexec/pfm-journey/engine-sync \
    -e "PFM_DEV_REPO_GIT_DIR=/pfm-git-common/$PFM_DEV_GIT_DIR_REL" \
    -e PFM_DEV_REPO_WORK_TREE=/worktree -w /worktree \
    "$@" "$image" /sbin/init || exit 2
  if ! python3 "$JOURNEY_CONTAINER_DIR/root-cache.py" inventory "$JOURNEY_CACHE_CONTROL/containers.json"; then
    docker rm -f "$name" >/dev/null 2>&1
    exit 2
  fi
  ) 9>"$JOURNEY_CACHE_CONTROL/build.lock"
}

journey_exec() { docker exec "$@"; }

journey_boot() { # NAME OUT; a running Docker process is not a ready system manager.
  local name="$1" out="$2" state=unreachable running deadline started
  started="$SECONDS"; deadline="$((SECONDS + 60))"
  while :; do
    running="$(docker inspect --format '{{.State.Running}}' "$name" 2>/dev/null)" || running=unknown
    [ "$running" = true ] || break
    state="$(docker exec "$name" timeout 2 systemctl is-system-running 2>/dev/null)" || :
    if [ "$state" = running ]; then
      printf 'journey machine-ready systemd=running wall_ms=%s\n' "$(( (SECONDS-started)*1000 ))"
      return 0
    fi
    case "$state" in degraded|maintenance|stopping|offline) break ;; esac
    [ "$SECONDS" -lt "$deadline" ] || break
    # Poll the private manager's named boot state, never retry a lane action.
    sleep 0.1
  done
  {
    printf 'BOOT-FAILED docker-running=%s systemd=%s\n' "$running" "${state:-unreachable}"
    docker inspect --format '{{json .State}}' "$name"
    docker logs "$name"
    docker exec "$name" timeout 3 systemctl --failed --no-legend
    docker exec "$name" timeout 3 journalctl -b --no-pager
  } >"$out/root-boot.log" 2>&1
  printf 'journey BOOT-FAILED systemd=%s evidence=%s/root-boot.log\n' "${state:-unreachable}" "$out" >&2
  return 2
}

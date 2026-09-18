#!/usr/bin/env bash
set -euo pipefail

# release-rehearsal.sh — the fenced adopter machine /pfm:release rehearses an
# update on. One long-lived container (pfm-release-rehearsal) from the pfm-dev
# image. Its only Professor source is a rehearsal-local upstream
# (/root/upstream.git) built from this repo's git objects, so a candidate
# release is "published" inside the fence and never touches GitHub. `snapshot`
# archives the stable-installed HOME (pfm installs touch nothing outside it) into
# the snapshot volume; `revert` starts a fresh container from the base image and
# restores that HOME, so every update attempt starts from the identical stable
# install. A real adopter machine carries no PFM_DEV_FENCE, so neither does this.
#
# BROKEN STATE: an unreachable docker daemon reports TOOLCHAIN-MISSING and exits
# 1; every other failure exits non-zero naming the step that failed (a missing
# container, a tag absent from upstream, a snapshot archive that is absent or
# empty). No
# path prints success for a step whose in-container check did not pass.

# PFM_REHEARSAL_NAME runs a second machine beside the first (an adopter several releases behind).
NAME="${PFM_REHEARSAL_NAME:-pfm-release-rehearsal}"
SNAPSHOT_VOLUME="$NAME-snapshot"
SNAPSHOT=/snapshot/home.tar
IMAGE=professor-pfm-dev
UPSTREAM=/root/upstream.git
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd -P)"

die() { echo "release-rehearsal: $*" >&2; exit 1; }

usage() {
  cat >&2 <<'EOF'
usage: infra/fence/release-rehearsal.sh <command>
  up                             build the pfm-dev image, start the rehearsal container
  seed <stable-tag>              upstream = main at <stable-tag>, no newer tag or branch
  publish <tag> <commit>         fast-forward upstream main to <commit>, tag it <tag>
  snapshot                       archive the stable-installed HOME into the snapshot volume
  revert                         replace the container with a fresh one holding the snapshot HOME
  exec <command-string>          run a command in the container (bash -lc)
  status                         container, snapshot, upstream main + tags
  down                           remove the container and the snapshot volume
EOF
  exit 2
}

need_docker() {
  command -v docker >/dev/null 2>&1 || die "TOOLCHAIN-MISSING — docker not on PATH"
  docker info >/dev/null 2>&1 || die "TOOLCHAIN-MISSING — the docker daemon is not reachable ('docker info' failed)"
}

exists() { docker container inspect "$NAME" >/dev/null 2>&1; }
require_running() {
  exists || die "no container $NAME — run 'up' (or 'revert' after a snapshot) first"
  [[ "$(docker container inspect -f '{{.State.Running}}' "$NAME")" == true ]] || die "container $NAME exists but is not running"
}
inside() { docker exec "$NAME" bash -lc "$1"; }

git_common() {
  local dir
  dir="$(git -C "$REPO_ROOT" rev-parse --git-common-dir)"
  [[ "$dir" == /* ]] || dir="$REPO_ROOT/$dir"
  (cd "$dir" && pwd -P)
}

start_from() {
  docker run -d --name "$NAME" --init \
    -v "$(git_common):/pfm-git-common:ro" \
    -v pfm-dev-gomod:/root/go/pkg/mod \
    -v pfm-dev-gocache:/root/.cache/go-build \
    -v "$SNAPSHOT_VOLUME:/snapshot" \
    "$IMAGE" sleep infinity >/dev/null || die "start container from $IMAGE failed"
  require_running
  # The host-owned git mount is foreign to the container's root; trust it
  # explicitly or every git read of it fails the dubious-ownership check.
  inside 'git config --global --get-all safe.directory | grep -qx /pfm-git-common || git config --global --add safe.directory /pfm-git-common' \
    || die "trust /pfm-git-common inside the container failed"
  inside 'echo "fence: container=$(hostname) HOME=$HOME"'
}

cmd_up() {
  exists && die "container $NAME already exists — 'down' it or 'revert' to the snapshot"
  docker build -q -t "$IMAGE" -f "$REPO_ROOT/infra/fence/pfm-dev.Dockerfile" "$REPO_ROOT/infra/fence" >/dev/null \
    || die "build image $IMAGE failed"
  start_from
}

cmd_seed() {
  local tag="${1:-}"
  [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "seed: expected a vMAJOR.MINOR.PATCH tag, got '${tag:-<missing>}'"
  require_running
  inside "set -euo pipefail
    rm -rf $UPSTREAM
    git init -q --bare -b main $UPSTREAM
    git -C $UPSTREAM fetch -q /pfm-git-common 'refs/tags/$tag:refs/tags/$tag'
    git -C $UPSTREAM update-ref refs/heads/main \"\$(git -C $UPSTREAM rev-parse '$tag^{commit}')\"
    git -C $UPSTREAM fetch -q /pfm-git-common '+refs/tags/v*:refs/tags/v*'
    newest=\"\$(git -C $UPSTREAM tag --list 'v*' --sort=-v:refname | head -1)\"
    for t in \$(git -C $UPSTREAM tag --list 'v*'); do
      git -C $UPSTREAM merge-base --is-ancestor \"\$t\" main || git -C $UPSTREAM tag -d \"\$t\" >/dev/null
    done
    latest=\"\$(git -C $UPSTREAM tag --list 'v*' --sort=-v:refname | head -1)\"
    [[ \"\$latest\" == '$tag' ]] || { echo \"seed: newest reachable tag is \$latest, not $tag\" >&2; exit 1; }
    echo \"upstream seeded: main=\$(git -C $UPSTREAM rev-parse --short main) latest=\$latest\"" \
    || die "seed $tag failed"
}

cmd_publish() {
  local tag="${1:-}" commit="${2:-}"
  [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ && -n "$commit" ]] || die "publish: usage publish <vX.Y.Z> <commit>"
  local sha
  sha="$(git -C "$REPO_ROOT" rev-parse --verify "$commit^{commit}")" || die "publish: $commit is not a commit in this repo"
  require_running
  # A re-publish after a fix moves the rehearsal-local tag; it never leaves this container.
  inside "set -euo pipefail
    git -C $UPSTREAM fetch -q /pfm-git-common '$sha'
    git -C $UPSTREAM merge-base --is-ancestor main '$sha' || { echo 'publish: $sha does not fast-forward upstream main' >&2; exit 1; }
    git -C $UPSTREAM update-ref refs/heads/main '$sha'
    git -C $UPSTREAM -c user.name=rehearsal -c user.email=rehearsal@invalid tag -f -a '$tag' -m 'rehearsal $tag' '$sha' >/dev/null
    echo \"upstream published: $tag -> \$(git -C $UPSTREAM rev-parse --short '$tag^{commit}')\"" \
    || die "publish $tag failed"
}

# The Go caches are shared volumes, not machine state — they stay out of the archive.
cmd_snapshot() {
  require_running
  inside "tar -C / --exclude=root/go/pkg/mod --exclude=root/.cache/go-build -cpf $SNAPSHOT.tmp root && mv $SNAPSHOT.tmp $SNAPSHOT && test -s $SNAPSHOT" \
    || die "snapshot: archive HOME into $SNAPSHOT failed"
  echo "snapshot: $SNAPSHOT_VOLUME:$SNAPSHOT ($(inside "du -h $SNAPSHOT | cut -f1"))"
}

cmd_revert() {
  if exists; then docker rm -f "$NAME" >/dev/null || die "revert: remove container $NAME failed"; fi
  start_from
  inside "test -s $SNAPSHOT" || die "revert: no snapshot archive in $SNAPSHOT_VOLUME — run 'snapshot' after the stable install"
  inside "find /root -mindepth 1 -maxdepth 1 ! -name go ! -name .cache -exec rm -rf {} + && tar -C / -xpf $SNAPSHOT" \
    || die "revert: restore HOME from $SNAPSHOT failed"
  echo "reverted: $NAME restarted with the snapshot HOME"
}

cmd_status() {
  if exists; then
    echo "container: $NAME $(docker container inspect -f '{{.State.Status}}' "$NAME")"
    inside "git -C $UPSTREAM rev-parse --short main 2>/dev/null | sed 's/^/upstream main: /' || echo 'upstream: not seeded'
      git -C $UPSTREAM tag --list 'v*' --sort=-v:refname 2>/dev/null | head -3 | sed 's/^/upstream tag: /'"
  else
    echo "container: absent"
  fi
  if docker volume inspect "$SNAPSHOT_VOLUME" >/dev/null 2>&1; then echo "snapshot volume: $SNAPSHOT_VOLUME"; else echo "snapshot volume: absent"; fi
}

cmd_down() {
  if exists; then docker rm -f "$NAME" >/dev/null || die "down: remove container failed"; fi
  if docker volume inspect "$SNAPSHOT_VOLUME" >/dev/null 2>&1; then docker volume rm "$SNAPSHOT_VOLUME" >/dev/null || die "down: remove snapshot volume failed"; fi
  echo "down: container and snapshot volume removed"
}

[[ $# -ge 1 ]] || usage
need_docker
case "$1" in
  up) cmd_up ;;
  seed) cmd_seed "${2:-}" ;;
  publish) cmd_publish "${2:-}" "${3:-}" ;;
  snapshot) cmd_snapshot ;;
  revert) cmd_revert ;;
  exec) [[ $# -ge 2 ]] || usage; require_running; inside "$2" ;;
  status) cmd_status ;;
  down) cmd_down ;;
  *) usage ;;
esac

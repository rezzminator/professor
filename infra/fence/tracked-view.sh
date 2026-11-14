#!/usr/bin/env bash
# tracked-view.sh — runs one command with the checkout shown as a CI checkout holds it: every path git
# ignores (pfm.config.json, the generated fleet prompts, tmp/, the engine mirrors) and every untracked
# directory holding no file is absent, under a read-only overlay in a mount namespace of the command's
# own. The gate's go-test steps (pfm.unit, pfm.e2e, pfm.race, infra/fence/checks.sh) run through it, so
# a jail test that reads a host-only file sees exactly what CI sees; an untracked file stays (the
# change under test), and every other step, the leak scan included, keeps the tree as it is.
#
#   infra/fence/tracked-view.sh CHECKOUT -- COMMAND [ARG…]
#
# In the dev fence only (PFM_DEV_FENCE=1 and the container marker, TRACKED_VIEW_MARKER, default
# /.dockerenv): anywhere else (a hosted runner's fresh clone holds no host state) COMMAND runs as is.
# The list comes from git over the tree under test (pfm/scripts/repo-git.sh repo_git: the fence's
# mounted git dir); each path becomes a whiteout in a scratch layer stacked over CHECKOUT.
# BROKEN STATE: a bad argument exits 2; a list git could not read, a namespace, layer or overlay that
# could not be made, or a hidden path still visible exits 1 naming it, and COMMAND never runs unviewed.
set -uo pipefail

SELF="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)/${BASH_SOURCE[0]##*/}"
die() { echo "tracked-view: $*" >&2; exit 1; }
if (( $# < 3 )) || [[ "$2" != -- ]]; then
  echo "usage: tracked-view.sh CHECKOUT -- COMMAND [ARG…]" >&2
  exit 2
fi
checkout="$1"
shift 2
if ! { [[ "${PFM_DEV_FENCE:-}" == 1 ]] && [[ -e "${TRACKED_VIEW_MARKER:-/.dockerenv}" ]]; }; then
  exec "$@"
fi
if [[ -z "${TRACKED_VIEW_NS:-}" ]]; then
  [[ "$(id -u)" == 0 ]] || die "the view of $checkout needs root (the fence's user) for its mount namespace"
  TRACKED_VIEW_NS=1 exec unshare --mount --propagation private -- bash "$SELF" "$checkout" -- "$@"
  die "unshare --mount could not start a namespace for the view of $checkout"
fi

# shellcheck source=../../pfm/scripts/repo-git.sh
source "${SELF%/infra/fence/*}/pfm/scripts/repo-git.sh" || die "cannot source pfm/scripts/repo-git.sh"
ignored="$(mktemp)" && untracked="$(mktemp)" || die "no scratch file for the hidden list"
repo_git -C "$checkout" ls-files -z --others --ignored --exclude-standard --directory > "$ignored" ||
  die "git could not list the ignored paths of $checkout"
repo_git -C "$checkout" ls-files -z --others --exclude-standard --directory > "$untracked" ||
  die "git could not list the untracked paths of $checkout"
hidden=()
while IFS= read -r -d '' path; do hidden+=("${path%/}"); done < "$ignored"
while IFS= read -r -d '' path; do
  [[ "$path" == */ ]] || continue
  [[ -z "$(find "$checkout/$path" -mindepth 1 \( -type f -o -type l \) -print -quit 2>&1)" ]] && hidden+=("${path%/}")
done < "$untracked"
rm -f -- "$ignored" "$untracked"
(( ${#hidden[@]} )) || exec "$@"

layer="$(mktemp -d)" || die "no scratch directory for the whiteout layer"
mount -t tmpfs -o mode=0755 tmpfs "$layer" || die "no tmpfs for the whiteout layer at $layer"
for path in "${hidden[@]}"; do
  # A path under a directory already whited out is hidden with it.
  parent="$path"
  while [[ "$parent" == */* ]]; do
    parent="${parent%/*}"
    [[ -e "$layer/$parent" && ! -d "$layer/$parent" ]] && continue 2
  done
  mkdir -p -- "$layer/$(dirname -- "$path")" && mknod -- "$layer/$path" c 0 0 ||
    die "no whiteout for $path in $layer"
done
mount -t overlay overlay -o "ro,lowerdir=$layer:$checkout" "$checkout" ||
  die "the overlay hiding ${#hidden[@]} path(s) could not be mounted on $checkout"
for path in "${hidden[@]}"; do
  [[ ! -e "$checkout/$path" && ! -L "$checkout/$path" ]] || die "$checkout/$path is still visible under the view"
done
# A cwd inside the checkout still names the tree below the overlay until it is entered again.
cd -- "$PWD" || die "cannot re-enter $PWD under the view"
exec "$@"

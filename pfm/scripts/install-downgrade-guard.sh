#!/usr/bin/env bash
# host-install's first line of defence against a silent downgrade: a build
# from an older tree replacing a binary that already carries a newer commit.
# Every pfm build stamps its own commit via -buildvcs=true (go build's
# default), readable back as `go version -m <binary>` -> a
# `build	vcs.revision=<sha>` line. This script compares that revision
# against the calling repo's HEAD and refuses to let host-install proceed
# when the installed binary is not an ancestor of HEAD — unless FORCE=1.
#
# usage: install-downgrade-guard.sh <installed-binary-path> [repo-dir]
#   repo-dir defaults to the git toplevel of this script's own tree.
#
# Every outcome prints exactly one line prefixed "install-guard: ". A missing
# binary or an unreadable vcs.revision are NOT failures of the guard itself —
# they are warnings that the check could not run, so they exit 0 and let
# host-install proceed; an ERROR (git itself misbehaving) exits 2 and must
# never read as ok. A genuine downgrade refuses with exit 1, or with FORCE=1
# set prints a FORCED line and exits 0.
set -uo pipefail

usage() {
  echo "usage: install-downgrade-guard.sh <installed-binary-path> [repo-dir]" >&2
  exit 2
}

BIN_PATH="${1:-}"
[ -n "$BIN_PATH" ] || usage

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=/dev/null
source "$SCRIPT_DIR/repo-git.sh"

if [ -n "${2:-}" ]; then
  REPO_DIR="$2"
else
  REPO_DIR="$(repo_git -C "$SCRIPT_DIR" rev-parse --show-toplevel 2>/dev/null)"
  if [ -z "$REPO_DIR" ]; then
    echo "install-guard: ERROR could not resolve this tree's git toplevel from $SCRIPT_DIR" >&2
    exit 2
  fi
fi

git_() { repo_git -C "$REPO_DIR" "$@"; }

# refuse <reason>: prints the refusal (or, with FORCE=1, the FORCED line) and
# exits accordingly. Never called for an ERROR — that path exits on its own.
refuse() {
  local reason=$1
  if [ "${FORCE:-}" = "1" ]; then
    echo "install-guard: FORCED past refusal — $reason"
    exit 0
  fi
  echo "install-guard: $reason" >&2
  exit 1
}

if [ ! -e "$BIN_PATH" ]; then
  echo "install-guard: no installed binary at $BIN_PATH — nothing to protect"
  exit 0
fi

gv_out="$(go version -m "$BIN_PATH" 2>&1)"
gv_rc=$?
rev=""
if [ "$gv_rc" -eq 0 ]; then
  rev="$(printf '%s\n' "$gv_out" \
    | awk '$1 == "build" && $2 ~ /^vcs\.revision=/ { sub(/^vcs\.revision=/, "", $2); print $2; exit }')"
fi
if [ "$gv_rc" -ne 0 ] || [ -z "$rev" ]; then
  echo "install-guard: cannot verify — $BIN_PATH carries no vcs.revision" >&2
  exit 0
fi

head_sha="$(git_ rev-parse HEAD 2>/dev/null)"
if [ -z "$head_sha" ]; then
  echo "install-guard: ERROR could not resolve HEAD in $REPO_DIR" >&2
  exit 2
fi
head_short="$(git_ rev-parse --short "$head_sha" 2>/dev/null)"

if ! git_ cat-file -e "${rev}^{commit}" 2>/dev/null; then
  refuse "REFUSED — installed pfm was built from unknown revision $rev, which this tree does not know; installing would be unverifiable. Rebase onto develop, or run FORCE=1 make host-install to override."
fi

rev_full="$(git_ rev-parse "${rev}^{commit}" 2>/dev/null)"
if [ -z "$rev_full" ]; then
  echo "install-guard: ERROR could not resolve $rev to a commit in $REPO_DIR" >&2
  exit 2
fi
rev_short="$(git_ rev-parse --short "$rev_full" 2>/dev/null)"

git_ merge-base --is-ancestor "$rev_full" "$head_sha"
mb_rc=$?
case "$mb_rc" in
  0)
    echo "install-guard: ok — installed $rev_short is in this tree (HEAD $head_short)"
    exit 0
    ;;
  1)
    subject="$(git_ log -1 --format=%s "$rev_full" 2>/dev/null)"
    refuse "REFUSED — installed pfm was built from $rev_short \"$subject\", which this tree's HEAD $head_short does not contain; installing would downgrade it. Rebase onto develop, or run FORCE=1 make host-install to override."
    ;;
  *)
    echo "install-guard: ERROR git merge-base --is-ancestor failed (rc $mb_rc) for $rev_full vs $head_sha" >&2
    exit 2
    ;;
esac

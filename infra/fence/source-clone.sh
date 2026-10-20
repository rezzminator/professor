#!/usr/bin/env bash
# A writable fixture clone for install's generated files. The mounted checkout
# and its Git directory stay untouched; this snapshot owns its own fixture Git
# metadata so install can arm its pre-push hook without following a host gitdir.
set -euo pipefail
[ "${PFM_DEV_FENCE:-}" = 1 ] || { echo 'source-clone: requires the fence' >&2; exit 1; }
source_root="${1:?source-clone: source root required}"
clone="${2:?source-clone: destination required}"
snapshot="$clone.source"
if [ -L "$clone" ]; then
  if [ "$(readlink "$clone")" = "$snapshot" ] && [ -d "$snapshot/.git" ]; then
    exit 0
  fi
  echo "source-clone: destination is a symlink outside its fixture snapshot: $clone" >&2
  exit 1
fi
if [ -e "$clone" ] || [ -e "$snapshot" ]; then
  echo "source-clone: destination already exists without its fixture link: $clone" >&2
  exit 1
fi
mkdir -p "$snapshot"
tar -C "$source_root" --exclude='.git' --exclude='.worktrees' --exclude='./tmp' --exclude='./pfm/harness-prompts/generated' -cf - . |
  tar -C "$snapshot" --no-same-owner -xf -
git -C "$snapshot" init -q -b develop
git -C "$snapshot" add .
git -C "$snapshot" -c user.name=fixture -c user.email=fixture@example.invalid commit -qm 'fence source snapshot'
# The linked blueprint remains part of O1's installed-host contract, but the
# link now resolves to a writable snapshot rather than the read-only mount.
ln -s "$snapshot" "$clone"

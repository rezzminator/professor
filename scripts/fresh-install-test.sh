#!/usr/bin/env bash
# fresh-install-test.sh — installs this checkout on a fresh machine the way
# INSTALL.md tells a user to, then updates and rolls back across releases.
#
#   bash scripts/fresh-install-test.sh
#
# 1. Snapshots the working tree (tracked and untracked files, no submodule
#    checkouts, as a plain clone has none) into a scratch repository, its
#    VERSION set to the release this tree becomes and tagged v{X.Y.Z}.
# 2. Runs every INSTALL.md block marked `<!-- fresh-install` as written, in a
#    fresh HOME, swapping only https://github.com/${REPO}.git for that scratch
#    repository. The engine-sync binary INSTALL.md has a Claude session produce
#    is linked at its stable path first: $ENGINE_SYNC_BIN when set (CI's
#    engine-sync action), else built from $ENGINE_SYNC_FENCE_SRC (the fence's
#    /engine-sync, else plugins/engine-sync).
# 3. Requires `pfm doctor` on that host to exit 0 or 1 with no FAIL row.
# 4. Runs pfm's release-upgrade e2e tests (pfm/e2e/release_upgrade_e2e_test.go)
#    against the real repository's tags, which need a full clone.
#
# Bash 3.2-safe: macOS runs it with the system bash. Runs on a CI runner
# (.github/workflows/ci.yml) and in the fence:
#   .claude/scripts/dev.sh iso run 'bash scripts/fresh-install-test.sh'
#
# BROKEN STATE: each failure ends with `fresh-install: FAIL {step}: {cause}`
# and exit 1; success ends with `fresh-install: PASS`.
set -euo pipefail

fail() {
  echo "fresh-install: FAIL $*" >&2
  exit 1
}

root=$(cd "$(dirname "$0")/.." && pwd) || fail "setup: cannot resolve the repository root"
for tool in git go tar awk sed tmux jq python3 zsh; do
  command -v "$tool" >/dev/null 2>&1 || fail "setup: $tool is not on PATH"
done

# The fence mounts a worktree whose .git file points at the host: it exports
# the git dir and work tree instead.
src_git() {
  if [ -n "${PFM_DEV_REPO_GIT_DIR:-}" ]; then
    git -C "$root" --git-dir="$PFM_DEV_REPO_GIT_DIR" --work-tree="${PFM_DEV_REPO_WORK_TREE:-$root}" \
      -c safe.directory='*' "$@"
  else
    git -C "$root" "$@"
  fi
}
common=$(src_git rev-parse --path-format=absolute --git-common-dir) ||
  fail "setup: $root is not a git checkout"
project=$(basename "$(dirname "$common")")
project=${project#.}
# A fence git dir sits at the filesystem root: name the scratch after the tree.
case "$project" in /|"") project=$(basename "$root") ;; esac
scratch="${TMPDIR:-/tmp}"
scratch="${scratch%/}/$project/fresh-install"
case "$scratch" in
  /*/fresh-install) ;;
  *) fail "setup: refusing scratch path $scratch" ;;
esac
rm -rf "$scratch"
mkdir -p "$scratch/mirror" "$scratch/home" || fail "setup: cannot create $scratch"

version=$(cat "$root/VERSION") || fail "setup: cannot read $root/VERSION"
release=${version%%-*}
tag="v$release"

# 1. The tree under test as the repository INSTALL.md clones.
src_git ls-files -z -co --exclude-standard |
  (
    cd "$root"
    while IFS= read -r -d '' path; do
      # A directory entry is a submodule checkout; a deleted file is gone.
      if [ -L "$path" ] || [ -f "$path" ]; then printf '%s\0' "$path"; fi
    done
  ) |
  (cd "$root" && tar -cf - --null -T -) |
  (cd "$scratch/mirror" && tar -xf -) || fail "snapshot: cannot copy the working tree into $scratch/mirror"
printf '%s\n' "$release" >"$scratch/mirror/VERSION"
mirror_git() {
  git -C "$scratch/mirror" -c user.name=fresh-install -c user.email=fresh-install@invalid \
    -c commit.gpgsign=false -c tag.gpgsign=false "$@"
}
{
  mirror_git init -q &&
    mirror_git add -A -f &&
    mirror_git commit -q -m "release $tag" &&
    mirror_git tag "$tag"
} || fail "snapshot: cannot commit and tag $tag in $scratch/mirror"

# 2. INSTALL.md's own commands, marked blocks only.
awk '
  /^<!-- fresh-install/ { marked = 1; next }
  marked && /^```bash$/ { inside = 1; marked = 0; blocks++; next }
  marked { print "MARKER WITHOUT A BASH BLOCK"; exit 1 }
  inside && /^```$/ { inside = 0; next }
  inside { print }
  END { if (blocks == 0) { print "NO MARKED BLOCK"; exit 1 } }
' "$root/INSTALL.md" >"$scratch/install-blocks.sh" ||
  fail "extract: $(tail -1 "$scratch/install-blocks.sh")"
# shellcheck disable=SC2016 # the literal INSTALL.md writes
url='https://github.com/${REPO}.git'
grep -qF "$url" "$scratch/install-blocks.sh" || fail "extract: the marked blocks never name $url"
sed "s#https://github.com/\${REPO}.git#file://$scratch/mirror#g" "$scratch/install-blocks.sh" >"$scratch/install.sh"
if grep -n 'github.com' "$scratch/install.sh"; then
  fail "extract: a marked block still reaches GitHub after the repository swap"
fi
echo "fresh-install: running INSTALL.md's marked blocks as $scratch/install.sh:"
cat "$scratch/install.sh"

# The Go caches survive the HOME swap: the fresh machine still downloads
# nothing it would not, and CI reuses what setup-go restored.
gocache=$(go env GOCACHE) || fail "setup: go env GOCACHE failed"
gomodcache=$(go env GOMODCACHE) || fail "setup: go env GOMODCACHE failed"
fresh_home="$scratch/home"
fresh() {
  env -i HOME="$fresh_home" USER="${USER:-runner}" LOGNAME="${LOGNAME:-${USER:-runner}}" \
    PATH="$fresh_home/.local/bin:$PATH" SHELL="$(command -v zsh)" LANG="${LANG:-C.UTF-8}" TERM=dumb \
    TMPDIR="${TMPDIR:-/tmp}" GOCACHE="$gocache" GOMODCACHE="$gomodcache" GOTOOLCHAIN=local "$@"
}

if [ -n "${ENGINE_SYNC_BIN:-}" ]; then
  engine_sync="$ENGINE_SYNC_BIN"
  [ -x "$engine_sync" ] || fail "engine-sync: ENGINE_SYNC_BIN=$engine_sync is not executable"
else
  # shellcheck source=infra/fence/engine-sync.sh
  . "$root/infra/fence/engine-sync.sh"
  if [ -z "${ENGINE_SYNC_FENCE_SRC:-}" ] && [ ! -f /engine-sync/go.mod ]; then
    ENGINE_SYNC_FENCE_SRC="$root/plugins/engine-sync"
  fi
  engine_sync="$scratch/engine-sync/engine-sync"
  GOCACHE="$gocache" GOMODCACHE="$gomodcache" engine_sync_build "$engine_sync" ||
    fail "engine-sync: cannot build the binary"
fi
if ! mkdir -p "$fresh_home/.local/share/engine-sync/bin" ||
  ! ln -sfn "$engine_sync" "$fresh_home/.local/share/engine-sync/bin/engine-sync"; then
  fail "engine-sync: cannot link the stable path in $fresh_home"
fi

(cd "$fresh_home" && fresh bash -euo pipefail "$scratch/install.sh") ||
  fail "install: INSTALL.md's marked blocks failed as written (above)"
installed=$(fresh pfm --version 2>&1) || fail "install: the installed pfm does not run: $installed"
case "$installed" in
  *"$tag"*) ;;
  *) fail "install: pfm --version reads '$installed', want $tag" ;;
esac

# 3. The fresh host's doctor: warnings are allowed, a failure is not.
doctor_rc=0
(cd "$fresh_home/.professor" && fresh pfm doctor) >"$scratch/doctor.out" 2>&1 || doctor_rc=$?
cat "$scratch/doctor.out"
if [ "$doctor_rc" -gt 1 ]; then
  fail "doctor: exited $doctor_rc on the fresh install"
fi
if grep -nE '(^|[^[:alnum:]])FAIL([^[:alnum:]]|$)' "$scratch/doctor.out"; then
  fail "doctor: a FAIL row on the fresh install (above)"
fi

# 4. Updates from older releases and rollbacks, against the real tags.
work_tree=$(src_git rev-parse --show-toplevel) || fail "e2e: cannot resolve the work tree"
git_dir=$(src_git rev-parse --absolute-git-dir) || fail "e2e: cannot resolve the git dir"
PFM_DEV_FENCE=1 PFM_HARVESTPY_OFFLINE=1 \
  PFM_E2E_SOURCE_REPO="${PFM_E2E_SOURCE_REPO:-$work_tree}" \
  PFM_DEV_REPO_WORK_TREE="${PFM_DEV_REPO_WORK_TREE:-$work_tree}" \
  PFM_DEV_REPO_GIT_DIR="${PFM_DEV_REPO_GIT_DIR:-$git_dir}" \
  go -C "$root/pfm" test -count=1 -tags e2e -p 1 -timeout 30m \
  -run '^(TestUpdateFromReleasesE2E|TestUpdateRollbackRestoresTheHostE2E|TestUpdateRollbackFromPreviousReleaseE2E|TestUpdateOnALivedInHostE2E)$' \
  ./e2e/ || fail "e2e: the release-upgrade tests failed (above)"

rm -rf "$scratch"
echo "fresh-install: PASS"

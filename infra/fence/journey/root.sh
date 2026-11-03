#!/usr/bin/env bash
# Build an immutable journey machine INSIDE dev.sh iso; stdout names its archive.
# root.sh [--rebuild] [--print-hash]. The host runner only imports and boots it.
set -euo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# Cache retirement is host orchestration; fixture/build operations remain fence-only.
if [ "${1:-}" = --cache-control ]; then
  shift
  exec python3 "$HERE/root-cache.py" prune "$@"
fi
. "$HERE/../../../scripts/platform.sh"
platform_in_fence || { echo 'journey-root: requires dev.sh iso' >&2; exit 2; }
REBUILD=0 PRINT_HASH=0
while [ "$#" -gt 0 ]; do
  case "$1" in --rebuild) REBUILD=1 ;; --print-hash) PRINT_HASH=1 ;; *) echo 'usage: root.sh [--rebuild] [--print-hash]' >&2; exit 2 ;; esac
  shift
done
: "${PFM_TEST_TIMING_HOST:?fence timing artifact mapping required}"
CACHE=/pfm-timing/journey-roots
mkdir -p "$CACHE"
chown "$(stat -c '%u:%g' /pfm-timing)" "$CACHE"
CONTROL=/pfm-timing/journey-control
PREVIOUS="$(jq -er '.previous_release' "$HERE/fixtures/root-inventory.json")"
root_hash() {
  local previous_tree versions
  previous_tree="$(git --git-dir="$PFM_DEV_REPO_GIT_DIR" --work-tree=/worktree rev-parse "$PREVIOUS^{tree}")" || return 2
  versions="$(herdr --version && tmux -V && zsh --version && go version)" || { echo 'journey-root: HASH-UNDERIVABLE tool versions' >&2; return 2; }
  python3 - "$previous_tree" "$versions" <<'PY'
import hashlib, os, pathlib, sys
root = pathlib.Path('/worktree')
digest = hashlib.sha256('\n'.join(sys.argv[1:]).encode())
for name in ['pfm', 'templates', 'plugins', 'VERSION', 'infra/fence/pfm-dev.Dockerfile',
             'infra/fence/journey/root.sh', 'infra/fence/journey/root-build.sh', 'infra/fence/journey/root-baseline.py',
             'infra/fence/journey/provision.sh', 'infra/fence/journey/bootstrap.py', 'infra/fence/journey/projects.py',
             'infra/fence/journey/marketplace.py', 'infra/fence/journey/previous-units.py', 'infra/fence/journey/isolation.py',
             'infra/fence/journey/relocate.py', 'infra/fence/journey/runtime.py', 'infra/fence/journey/ssh.py',
             'infra/fence/journey/fixtures', 'infra/fence/journey/scenarios', 'infra/fence/journey/goldens',
             '/engine-sync', '/harvester', '/usr/local/bin/herdr', '/opt/pfm-tools/tools.env']:
    base = pathlib.Path(name) if name.startswith('/') else root / name
    if not base.exists():
        raise SystemExit(f'journey-root: HASH-UNDERIVABLE {name}')
    for path in sorted(base.rglob('*')) if base.is_dir() else [base]:
        relative = path.relative_to(base) if base.is_dir() else pathlib.Path(path.name)
        if any(part in {'.git', 'target', 'node_modules', '.worktrees', '__pycache__'} for part in relative.parts) or (path.is_dir() and not path.is_symlink()) or path.name.endswith(('_test.go', '_test.py')):
            continue
        digest.update(f'{name}/{relative}:{path.lstat().st_mode}\n'.encode())
        digest.update(os.readlink(path).encode() if path.is_symlink() else path.read_bytes())
print(digest.hexdigest()[:20])
PY
}
HASH="$(root_hash)" || exit 2
[ "$PRINT_HASH" -eq 0 ] || { printf '%s\n' "$HASH"; exit 0; }
DEST="$CACHE/$HASH"
[ -r "$CONTROL/containers.json" ] && [ -f "$CONTROL/build.lock" ] || {
  echo 'journey-root: ROOT-CACHE-USAGE-UNAVAILABLE; run through journey/run.sh to inspect existing containers' >&2; exit 2;
}
mkdir -p "$DEST"
chown "$(stat -c '%u:%g' /pfm-timing)" "$DEST"
(
  flock 9
  [ -r "$CONTROL/containers.json" ] || { echo 'journey-root: ROOT-CACHE-USAGE-UNAVAILABLE; run through journey/run.sh to inspect existing containers' >&2; exit 2; }
  [ "$(root_hash)" = "$HASH" ] || { echo 'journey-root: INPUTS-CHANGED before build' >&2; exit 2; }
  printf '{"owner":"journey-root","root":"%s"}\n' "$HASH" >"$DEST/owner.json"
  if [ "$REBUILD" -eq 0 ] && [ -s "$DEST/root.tar" ] && [ -s "$DEST/manifest.json" ]; then
    printf 'journey-root: reuse %s\n' "$HASH" >&2
  else
    bash "$HERE/root-build.sh" "$DEST" "$PREVIOUS" "$HASH"
  fi
  if [ "$(root_hash)" != "$HASH" ]; then
    rm -f "$DEST/root.tar" "$DEST/manifest.json"
    echo 'journey-root: INPUTS-CHANGED during build; unpublished artifact refused' >&2
    exit 2
  fi
  touch "$DEST/accessed"
) 9>"$CONTROL/build.lock"
printf 'JOURNEY_ROOT_HASH=%s\n' "$HASH"
printf 'JOURNEY_ROOT_ARCHIVE=%s/journey-roots/%s/root.tar\n' "$PFM_TEST_TIMING_HOST" "$HASH"
printf 'JOURNEY_ROOT_MANIFEST=%s/journey-roots/%s/manifest.json\n' "$PFM_TEST_TIMING_HOST" "$HASH"

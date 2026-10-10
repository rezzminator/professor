#!/usr/bin/env bash
# install-engine-sync.sh {engine-sync checkout}: the engine-sync half of
# `make host-install`. It builds the checkout's cmd/engine-sync unstamped,
# reads its own `version` report, installs it into {engine-sync home}/versions/
# {version}/engine-sync, then points {engine-sync home}/bin/engine-sync at it.
# The plugin wrapper caches release builds in the same layout; a development
# build uses its own version slot. pfm resolves the stable link at
# internal/enginesync. {engine-sync home} is $ENGINE_SYNC_HOME when absolute,
# else $XDG_DATA_HOME/engine-sync when absolute, else
# $HOME/.local/share/engine-sync.
#
# BROKEN STATE: a missing checkout, a failed build, an unusable or unparseable
# `engine-sync {version}` report, or a stable path that is not a link (the
# user's own file) each exit 1 with a named line, leaving the installed
# version and the stable link as they were. A checksum mismatch in the release
# cache is retired by name; an unknown cache without proof is visibly kept.
set -euo pipefail

fail() { echo "install-engine-sync: $*" >&2; exit 1; }

is_version() { [[ $1 =~ ^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$ ]]; }
# Match the wrapper's ordering: numeric core, then release over prerelease.
version_older() {
  awk -v a="$1" -v b="$2" 'BEGIN {
    split(a, x, /[-+]/); split(b, y, /[-+]/)
    split(x[1], ac, /\./); split(y[1], bc, /\./)
    for (i=1; i<=3; i++) { if (ac[i]+0 < bc[i]+0) exit 0; if (ac[i]+0 > bc[i]+0) exit 1 }
    exit !(a ~ /-/ && b !~ /-/)
  }'
}
checksum() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1";
  else shasum -a 256 "$1"; fi
}


[ $# -eq 1 ] || { echo "usage: install-engine-sync.sh {engine-sync checkout}" >&2; exit 2; }
checkout=$1
[ -f "$checkout/go.mod" ] && [ -d "$checkout/cmd/engine-sync" ] \
  || fail "no engine-sync checkout at $checkout (go.mod and cmd/engine-sync) — run git submodule update --init plugins/engine-sync"

case ${ENGINE_SYNC_HOME:-} in
  /*) home=$ENGINE_SYNC_HOME ;;
  *)
    case ${XDG_DATA_HOME:-} in
      /*) home=$XDG_DATA_HOME/engine-sync ;;
      *)
        [ -n "${HOME:-}" ] || fail "HOME is unset and neither ENGINE_SYNC_HOME nor XDG_DATA_HOME is absolute"
        home=$HOME/.local/share/engine-sync
        ;;
    esac
    ;;
esac
link=$home/bin/engine-sync

if [ -e "$link" ] && [ ! -L "$link" ]; then
  fail "$link is not a link (the user's own file?) — left as is, nothing built"
fi

# Stage on the destination filesystem before the binary tells us its version.
mkdir -p "$home/versions" "$(dirname "$link")" \
  || fail "cannot create $home/versions or $(dirname "$link")"
tmp=$(mktemp "$home/versions/engine-sync.new.XXXXXX") || fail "mktemp under $home/versions failed"
tmplink=$home/bin/.link-$$
trap 'rm -f "$tmp" "$tmplink"' EXIT

CGO_ENABLED=0 go -C "$checkout" build -o "$tmp" ./cmd/engine-sync \
  || fail "build FAILED — installed versions and $link untouched"
reported=$("$tmp" version 2>&1) \
  || fail "version command FAILED ($reported) — installed versions and $link untouched"
case $reported in
  'engine-sync '*) version=${reported#engine-sync } ;;
  *) fail "no usable version report (read '$reported') — installed versions and $link untouched" ;;
esac
is_version "$version" || fail "no usable version report (read '$reported') — installed versions and $link untouched"
manifest="$checkout/plugins/engine-sync/.claude-plugin/plugin.json"
release=$(jq -er '.version | select(type == "string")' "$manifest")   || fail "cannot read release version from $manifest"
is_version "$release" || fail "invalid plugin release version '$release' in $manifest"
[ "$version" != "$release" ] || fail "host build reports $version: release slot versions/$release belongs to the published wrapper download"

previous=$(readlink "$link" 2>/dev/null || true)
if [ -x "$link" ]; then
  installed=$("$link" version) || fail "cannot read installed engine-sync version at $link"
  installed=${installed#engine-sync }
  is_version "$installed" || fail "no usable installed version at $link (read '$installed')"
  if version_older "$version" "$installed"; then
    if [ "${FORCE:-}" != 1 ]; then
      fail "engine-sync $version is older than installed $installed — stable link kept; FORCE=1 explicitly permits a backwards move"
    fi
    echo "install-engine-sync: FORCED stable link backwards: $installed -> $version"
  fi
fi

# Authenticate the release cache before migrating an old host-built release slot.
slot="$home/versions/$release"
if [ -L "$slot" ] || [ -L "$slot/engine-sync" ]; then
  fail "release cache $slot is a user symlink — cannot prove ownership"
fi
if [ -f "$slot/engine-sync" ]; then
  case $(uname -s) in Darwin) platform=darwin ;; Linux) platform=linux ;; *) platform=unknown ;; esac
  case $(uname -m) in arm64|aarch64) architecture=arm64 ;; x86_64|amd64) architecture=amd64 ;; *) architecture=unknown ;; esac
  asset="engine-sync_${release}_${platform}_${architecture}"
  sums="$checkout/plugins/engine-sync/libexec/SHA256SUMS"
  expected=""
  if [ -f "$sums" ]; then expected=$(awk -v asset="$asset" '$2 == asset { print $1 }' "$sums"); fi
  remove=0
  if [[ $expected =~ ^[0-9a-fA-F]{64}$ ]]; then
    actual=$(checksum "$slot/engine-sync") || fail "cannot checksum release cache $slot/engine-sync"
    [ "${actual%% *}" = "$expected" ] || remove=1
  else
    cached=$("$slot/engine-sync" version) || fail "cannot inspect release cache $slot/engine-sync"
    if [ "$cached" != "engine-sync $release" ] || [ -f "$slot/.host-built" ]; then remove=1;
    else echo "install-engine-sync: WARNING release cache $slot is unverified — no checksum for $asset; kept"; fi
  fi
  if [ "$remove" = 1 ]; then
    rm -f "$slot/engine-sync" "$slot/.host-built" "$slot/.last-use" || fail "cannot retire mislabeled host build $slot"
    if ! rmdir "$slot" 2>/dev/null; then echo "install-engine-sync: kept nonempty release directory $slot"; fi
    echo "install-engine-sync: removed mislabeled host build $slot/engine-sync (not the published $release asset)"
  fi
fi
find "$home/versions" -maxdepth 1 -type f -name 'engine-sync.new.*' -mmin +60   -exec sh -c 'rm -f "$1" && printf "install-engine-sync: removed stale build %s\n" "$1"' _ {} \;   || fail "cannot sweep stale builds under $home/versions"
if [ -n "${ENGINE_SYNC_LINK_JOURNAL:-}" ]; then
  journal=$ENGINE_SYNC_LINK_JOURNAL
  [ -d "$journal" ] || fail "rollback journal $journal is not a directory"
  printf '%s\n' "$link" > "$journal/path" || fail "cannot journal stable link $link"
  if [ -L "$link" ]; then cp -P "$link" "$journal/previous" || fail "cannot snapshot stable link $link"; fi
  ln -s "../versions/$version/engine-sync" "$journal/installed" || fail "cannot journal installed version $version"
fi
target=$home/versions/$version/engine-sync
mkdir -p "$(dirname "$target")" || fail "cannot create $(dirname "$target")"
# On the same filesystem, so the move is a rename(2): a running copy keeps its image.
mv -f "$tmp" "$target" || fail "move the build onto $target failed"

printf 'host-built\n' > "$(dirname "$target")/.host-built" || fail "cannot record host-build provenance for $target"
rm -f "$tmplink"
ln -s "../versions/$version/engine-sync" "$tmplink" || fail "cannot create the link $tmplink"
# mv -f onto a link to a directory would move into it; the target is a file.
mv -f "$tmplink" "$link" || fail "cannot move the stable link onto $link"
echo "installed: $target (engine-sync $version); $link -> ../versions/$version/engine-sync${previous:+ (was $previous)}"

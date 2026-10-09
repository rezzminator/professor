#!/usr/bin/env bash
# install-engine-sync.sh {engine-sync checkout}: the engine-sync half of
# `make host-install`. It builds the checkout's cmd/engine-sync, stamped with
# the plugin manifest's version, into {engine-sync home}/versions/{version}/
# engine-sync, then points the stable link {engine-sync home}/bin/engine-sync
# at it — the layout the plugin wrapper (plugins/engine-sync/libexec/
# engine-sync) caches into, and the stable path pfm resolves the binary at
# (internal/enginesync). {engine-sync home} is $ENGINE_SYNC_HOME when
# absolute, else $XDG_DATA_HOME/engine-sync when absolute, else
# $HOME/.local/share/engine-sync.
#
# The version is the first "version" string of
# {checkout}/plugins/engine-sync/.claude-plugin/plugin.json, the one the
# wrapper reads.
#
# BROKEN STATE: a missing checkout, an unreadable or malformed version, a
# failed build, a smoke test that does not report the version, or a stable
# path that is not a link (the user's own file) each exit 1 with a named
# line, leaving the installed version and the stable link as they were.
set -euo pipefail

fail() { echo "install-engine-sync: $*" >&2; exit 1; }

[ $# -eq 1 ] || { echo "usage: install-engine-sync.sh {engine-sync checkout}" >&2; exit 2; }
checkout=$1
[ -f "$checkout/go.mod" ] && [ -d "$checkout/cmd/engine-sync" ] \
  || fail "no engine-sync checkout at $checkout (go.mod and cmd/engine-sync) — run git submodule update --init plugins/engine-sync"

manifest=$checkout/plugins/engine-sync/.claude-plugin/plugin.json
[ -r "$manifest" ] || fail "cannot read the plugin manifest $manifest"
version=$(sed -n 's/^[[:space:]]*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$manifest" | head -n 1)
case $version in
  '' | *[!0-9A-Za-z._+-]*) fail "no usable \"version\" in $manifest (read '$version')" ;;
esac

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
target=$home/versions/$version/engine-sync
link=$home/bin/engine-sync

if [ -e "$link" ] && [ ! -L "$link" ]; then
  fail "$link is not a link (the user's own file?) — left as is, nothing built"
fi

mkdir -p "$(dirname "$target")" "$(dirname "$link")" || fail "cannot create $(dirname "$target") or $(dirname "$link")"
tmp=$(mktemp "$target.new.XXXXXX") || fail "mktemp beside $target failed"
tmplink=$home/bin/.link-$$
trap 'rm -f "$tmp" "$tmplink"' EXIT

CGO_ENABLED=0 go -C "$checkout" build -ldflags "-X main.version=$version" -o "$tmp" ./cmd/engine-sync \
  || fail "build FAILED — $target and $link untouched"
reported=$("$tmp" version 2>&1) || fail "smoke test FAILED ($reported) — $target and $link untouched"
[ "$reported" = "engine-sync $version" ] \
  || fail "smoke test reported '$reported', not 'engine-sync $version' — $target and $link untouched"
# Beside the target, so the move is a rename(2): a running copy keeps its image.
mv -f "$tmp" "$target" || fail "move the build onto $target failed"

previous=$(readlink "$link" 2>/dev/null || true)
rm -f "$tmplink"
ln -s "../versions/$version/engine-sync" "$tmplink" || fail "cannot create the link $tmplink"
# mv -f onto a link to a directory would move into it; the target is a file.
mv -f "$tmplink" "$link" || fail "cannot move the stable link onto $link"
echo "installed: $target (engine-sync $version); $link -> ../versions/$version/engine-sync${previous:+ (was $previous)}"

#!/usr/bin/env bash
# Vendors the canonical plugin-log library (plugins/plugin-log/) into every plugin checkout, or checks every copy.
# Each copy is plain source at one fixed path per language, byte-identical to its canonical file.
#   --write   copy each canonical file over its plugin copy; a file there that is not a vendored copy (no marker
#             line) is refused unless --adopt, so a plugin's own module is never clobbered unasked
#   --check   report every copy: ok, DRIFT or MISSING
#   --plugins-dir D   where the plugin checkouts live (default: this repo's plugins/)
#   plugin...         only these plugins (default: the whole roster)
# A plugin with no checkout (no .git under its directory, as in a fence worktree) is NOT CHECKED, never a pass.
# An ACCEPTED copy differs from its canonical file by one released change the canonical file cannot take while the
# other copies' plugins still ship the old text; it is pinned by its exact content (git blob id), so any further
# change is DRIFT again, and --write leaves it. Drop the entry once canonical and every copy agree.
# PLUGIN_LOG_SYNC_ACCEPTED replaces the list (the self-test's fixtures).
# Exit: 0 clean or written, 1 drift or refused, 2 usage, 3 some checkout absent and nothing else wrong.
set -euo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
CANON="$ROOT/plugins/plugin-log"
MARKER='vendored copy of the plugin-log canonical source'
usage() { echo "plugin-log-sync: $*; usage: plugin-log-sync.sh --write|--check [--adopt] [--plugins-dir D] [plugin...]" >&2; exit 2; }

# plugin, canonical file, copy path inside the plugin's checkout
ROSTER='auto-effort-control ts/plugin-log.ts plugins/auto-effort-control/src/plugin-log.ts
buddy ts/plugin-log.ts plugins/buddy/src/plugin-log.ts
cache-keepwarm ts/plugin-log.ts plugins/cache-keepwarm/src/plugin-log.ts
cache-live-control ts/plugin-log.ts plugins/cache-live-control/src/plugin-log.ts
forward-compaction ts/plugin-log.ts plugins/forward-compaction/src/plugin-log.ts
tool-visibility-controller ts/plugin-log.ts plugins/tool-visibility-controller/src/plugin-log.ts
callmeter go/pluginlog.go internal/pluginlog/pluginlog.go
engine-sync go/pluginlog.go internal/pluginlog/pluginlog.go
harvester go/pluginlog.go internal/pluginlog/pluginlog.go
gpt-gateway python/plugin_log.py gpt_gateway/plugin_log.py'

# plugin, git blob id of its accepted copy, why
ACCEPTED_DEFAULT='cache-keepwarm 90b5588b7ace4d71ab02d5e1feb3d6e1ea5fa785 cache-keepwarm v0.6.0 rewords one comment ($.process.run -> process.run) its structure gate required'
ACCEPTED=${PLUGIN_LOG_SYNC_ACCEPTED-$ACCEPTED_DEFAULT}
# accepted_reason plugin dest: prints the reason and succeeds when dest is that plugin's accepted copy.
accepted_reason() {
  local blob plugin_entry id reason
  [ -n "$ACCEPTED" ] && [ -f "$2" ] || return 1
  # From /: the blob id needs no repository, and a fence worktree's own .git does not resolve.
  blob=$(cd / && git hash-object --no-filters --stdin < "$2") || { echo "plugin-log-sync: git hash-object failed on $2" >&2; exit 2; }
  while read -r plugin_entry id reason; do
    if [ "$plugin_entry" = "$1" ] && [ "$id" = "$blob" ]; then printf '%s\n' "$reason"; return 0; fi
  done <<< "$ACCEPTED"
  return 1
}

mode=; adopt=0; plugins_dir="$ROOT/plugins"; only=()
while [ "$#" -gt 0 ]; do
  case "$1" in
    --write|--check) [ -z "$mode" ] || usage 'one of --write or --check'; mode=${1#--} ;;
    --adopt) adopt=1 ;;
    --plugins-dir) [ "$#" -ge 2 ] || usage 'missing value for --plugins-dir'; plugins_dir=$2; shift ;;
    -*) usage "unknown option $1" ;;
    *) printf '%s\n' "$ROSTER" | cut -d' ' -f1 | grep -qxF -- "$1" || usage "unknown plugin $1"; only+=("$1") ;;
  esac
  shift
done
[ -n "$mode" ] || usage 'expected --write or --check'
[ "$adopt" = 0 ] || [ "$mode" = write ] || usage '--adopt is --write only'
[ -d "$plugins_dir" ] || usage "no plugins directory $plugins_dir"

checked=0; drift=0; absent=0; accepted=0
while read -r plugin source copy; do
  if [ "${#only[@]}" -gt 0 ] && ! printf '%s\n' "${only[@]}" | grep -qxF -- "$plugin"; then continue; fi
  canon="$CANON/$source"
  [ -f "$canon" ] || { echo "plugin-log-sync: canonical file missing: $canon" >&2; exit 2; }
  checkout="$plugins_dir/$plugin"
  if [ ! -e "$checkout/.git" ]; then
    echo "NOT CHECKED $plugin: no checkout at $checkout"; absent=$((absent + 1)); continue
  fi
  dest="$checkout/$copy"
  checked=$((checked + 1))
  if [ "$mode" = check ]; then
    if [ ! -f "$dest" ]; then echo "MISSING $plugin $copy"; drift=$((drift + 1))
    elif cmp -s "$canon" "$dest"; then echo "ok $plugin $copy"
    elif reason=$(accepted_reason "$plugin" "$dest"); then echo "ACCEPTED $plugin $copy: $reason"; accepted=$((accepted + 1))
    else echo "DRIFT $plugin $copy (diff $canon $dest)"; drift=$((drift + 1)); fi
    continue
  fi
  if [ -f "$dest" ] && cmp -s "$canon" "$dest"; then echo "unchanged $plugin $copy"; continue; fi
  if reason=$(accepted_reason "$plugin" "$dest"); then echo "ACCEPTED $plugin $copy: $reason"; accepted=$((accepted + 1)); continue; fi
  if [ -e "$dest" ] && [ "$adopt" = 0 ] && ! grep -qF -- "$MARKER" "$dest"; then
    echo "REFUSED $plugin $copy: the plugin's own file, not a vendored copy; --adopt replaces it once its adapter uses the library"
    drift=$((drift + 1)); continue
  fi
  verb=added; [ ! -e "$dest" ] || verb=updated
  mkdir -p -- "$(dirname -- "$dest")"
  cp -- "$canon" "$dest.plugin-log-sync"
  chmod 644 -- "$dest.plugin-log-sync"
  mv -f -- "$dest.plugin-log-sync" "$dest"
  echo "$verb $plugin $copy"
done <<< "$ROSTER"

echo "plugin-log-sync: $mode: $checked checked, $drift $([ "$mode" = check ] && echo drifted || echo refused), $absent not checked, $accepted accepted"
[ "$drift" = 0 ] || exit 1
[ "$absent" = 0 ] || exit 3

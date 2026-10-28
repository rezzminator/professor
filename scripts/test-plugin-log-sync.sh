#!/usr/bin/env bash
# Self-test of scripts/plugin-log-sync.sh against fixture plugin checkouts.
set -uo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SHTEST_TAG=plugin-log-sync-test
source "$ROOT/scripts/shtest.sh"
SUT="$ROOT/scripts/plugin-log-sync.sh"
P="$T/plugins"
TS="$ROOT/plugins/plugin-log/ts/plugin-log.ts"
GO="$ROOT/plugins/plugin-log/go/pluginlog.go"
PY="$ROOT/plugins/plugin-log/python/plugin_log.py"
run_sut() { RC=0; bash "$SUT" "$@" --plugins-dir "$P" > "$T/out" 2> "$T/err" || RC=$?; }
expect() { if eval "$2"; then ok "$1"; else bad "$1" "rc=$RC" "$(cat "$T/out" "$T/err")"; fi; }
checkout() { mkdir -p "$P/$1"; : > "$P/$1/.git"; }

mkdir -p "$P"
for p in auto-effort-control buddy cache-keepwarm cache-live-control forward-compaction tool-visibility-controller callmeter engine-sync harvester gpt-gateway; do checkout "$p"; done

run_sut --check
expect 'a check of checkouts with no copy reports each MISSING and fails' '[ "$RC" = 1 ] && [ "$(grep -c "^MISSING " "$T/out")" = 10 ]'

run_sut --write
expect 'a write adds every copy at its fixed path' '[ "$RC" = 0 ] && [ "$(grep -c "^added " "$T/out")" = 10 ] && cmp -s "$TS" "$P/buddy/plugins/buddy/src/plugin-log.ts" && cmp -s "$GO" "$P/callmeter/internal/pluginlog/pluginlog.go" && cmp -s "$PY" "$P/gpt-gateway/gpt_gateway/plugin_log.py"'
expect 'a written copy is 0644' '[ "$(stat -c %a "$P/engine-sync/internal/pluginlog/pluginlog.go")" = 644 ]'

run_sut --check
expect 'a check after a write is clean, every copy ok' '[ "$RC" = 0 ] && [ "$(grep -c "^ok " "$T/out")" = 10 ] && grep -q "10 checked, 0 drifted, 0 not checked" "$T/out"'

run_sut --write
expect 'a second write changes nothing' '[ "$RC" = 0 ] && [ "$(grep -c "^unchanged " "$T/out")" = 10 ]'

printf '// a local edit\n' >> "$P/buddy/plugins/buddy/src/plugin-log.ts"
run_sut --check
expect 'an edited copy is DRIFT and fails the check' '[ "$RC" = 1 ] && grep -q "^DRIFT buddy plugins/buddy/src/plugin-log.ts" "$T/out"'
run_sut --write buddy
expect 'a write restores a drifted vendored copy' '[ "$RC" = 0 ] && grep -q "^updated buddy" "$T/out" && cmp -s "$TS" "$P/buddy/plugins/buddy/src/plugin-log.ts"'

printf 'package pluginlog // the plugin'"'"'s own\n' > "$P/harvester/internal/pluginlog/pluginlog.go"
run_sut --write harvester
expect 'a write refuses a file that is the plugin'"'"'s own and leaves it' '[ "$RC" = 1 ] && grep -q "^REFUSED harvester" "$T/out" && grep -q "the plugin'"'"'s own" "$P/harvester/internal/pluginlog/pluginlog.go"'
run_sut --write --adopt harvester
expect '--adopt replaces it' '[ "$RC" = 0 ] && cmp -s "$GO" "$P/harvester/internal/pluginlog/pluginlog.go"'

printf '// a local edit\n' >> "$P/cache-keepwarm/plugins/cache-keepwarm/src/plugin-log.ts"
rm -f "$P/cache-keepwarm/.git"
run_sut --check
expect 'a plugin with no checkout is NOT CHECKED and the check exits 3, never 0' '[ "$RC" = 3 ] && grep -q "^NOT CHECKED cache-keepwarm" "$T/out" && grep -q "9 checked, 0 drifted, 1 not checked" "$T/out"'
run_sut --write
expect 'a write skips an absent checkout and touches nothing there' '[ "$RC" = 3 ] && tail -n 1 "$P/cache-keepwarm/plugins/cache-keepwarm/src/plugin-log.ts" | grep -qx "// a local edit"'

run_sut --check nosuch
expect 'an unknown plugin is a usage error' '[ "$RC" = 2 ]'
run_sut --check --adopt
expect '--adopt with --check is a usage error' '[ "$RC" = 2 ]'
run_sut
expect 'no mode is a usage error' '[ "$RC" = 2 ]'
shtest_end

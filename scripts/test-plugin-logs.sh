#!/usr/bin/env bash
set -uo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SHTEST_TAG=plugin-logs-test
source "$ROOT/scripts/shtest.sh"
shtest_isolate_host
# The mounted worktree's .git points at a host path. Even git config --file
# discovers it from cwd; fixture commands must start outside that repository.
cd "$T" || exit 2
SUT="$ROOT/scripts/plugin-logs.sh"
mkdir -p "$T/root" "$T/logs" "$T/home"
cat > "$T/root/.gitmodules" <<'EOF'
[submodule "callmeter"]
 path = plugins/callmeter
[submodule "buddy"]
 path = plugins/buddy
[submodule "agent-effort"]
 path = plugins/effort-live-control
[submodule "tvc"]
 path = plugins/tool-visibility-controller
[submodule "engine-sync"]
 path = plugins/engine-sync
[submodule "gpt-gateway"]
 path = plugins/gpt-gateway
EOF
row() { # plugin level event time message
  jq -cn --arg p "$1" --arg l "$2" --arg e "$3" --arg t "$4" --arg m "$5" \
    '{ts:$t,plugin:$p,level:$l,event:$e,msg:$m,where:"internal/hookentry/callmeter.go#store",session:"s1",detail:{count:1}}'
}
run_sut() {
  RC=0
  bash "$SUT" "$@" --root "$T/root" > "$T/out" 2> "$T/err" || RC=$?
}
expect() {
  if eval "$2"; then ok "$1"; else bad "$1" "rc=$RC" "$(cat "$T/out" "$T/err")"; fi
}
mkdir -p "$T/logs/callmeter" "$T/logs/buddy" "$T/logs/tool-visibility-controller"
row callmeter error store-failed 2026-10-14T15:20:31.123Z 'database is locked' > "$T/logs/callmeter/errors.jsonl"
row callmeter error store-failed 2026-10-14T15:21:31.123Z 'still locked' >> "$T/logs/callmeter/errors.jsonl"
row callmeter error hook-panic 2026-10-14T15:22:31.123Z 'hook panicked' >> "$T/logs/callmeter/errors.jsonl"
row callmeter info started 2026-10-14T15:19:31.123Z started > "$T/logs/callmeter/info.jsonl"
row tool-visibility-controller info started 2026-10-14T15:19:31.123Z started > "$T/logs/tool-visibility-controller/info.jsonl"
run_sut report --dir "$T/logs"
expect 'report groups event and where with exact count, first/last and last message' '[ "$RC" = 0 ] && grep -Fxq "store-failed · internal/hookentry/callmeter.go#store · 2 · 2026-10-14T15:20:31.123Z · 2026-10-14T15:21:31.123Z · still locked" "$T/out"'
expect 'report fixed section ordering and exact info count' '[ "$(grep -E "^(ERRORS|WARNINGS|INFO)$" "$T/out" | paste -sd, -)" = ERRORS,WARNINGS,INFO ] && grep -Fxq "callmeter 1" "$T/out"'
expect 'no valid rows lists roster plugins with zero actual rows' '[ "$RC" = 0 ] && grep -Fxq "no valid rows: buddy, effort-live-control, engine-sync, gpt-gateway" "$T/out"'
expect 'header states real span, no rotate and folder' 'grep -Fq "$T/logs" "$T/out" && grep -Fq "2026-10-14T15:19:31.123Z .. 2026-10-14T15:22:31.123Z" "$T/out" && grep -Fq "last rotate: none" "$T/out" && grep -Fq "contract-skipped: 0" "$T/out"'
printf '\n{bad json}\n' > "$T/logs/buddy/warnings.jsonl"
for filter in '.level="WARN"' '.extra=true' '.ts="not-a-date"'; do
  row buddy warning degraded 2026-10-14T15:20:31.123Z invalid | jq -c "$filter" >> "$T/logs/buddy/warnings.jsonl"
done
run_sut report --dir "$T/logs"
expect 'contract-skipped count distinguishes malformed input from silence' '[ "$RC" = 0 ] && grep -Fxq "no valid rows: buddy, effort-live-control, engine-sync, gpt-gateway" "$T/out" && grep -Fq "contract-skipped: 5" "$T/out"'
row buddy warning degraded 2026-10-14T15:20:31.123Z fallback >> "$T/logs/buddy/warnings.jsonl"
run_sut report --dir "$T/logs"
expect 'a valid row after malformed rows contributes to grouping and roster activity' '[ "$RC" = 0 ] && grep -Fxq "degraded · internal/hookentry/callmeter.go#store · 1 · 2026-10-14T15:20:31.123Z · 2026-10-14T15:20:31.123Z · fallback" "$T/out" && grep -Fxq "no valid rows: effort-live-control, engine-sync, gpt-gateway" "$T/out"'
row callmeter warning capped 2026-10-14T15:23:31.123Z capped > "$T/logs/callmeter/warnings.jsonl.1"
run_sut report --dir "$T/logs"
expect 'report includes capped .1 rows' 'grep -Fq "capped · internal/hookentry/callmeter.go#store · 1" "$T/out"'
run_sut report --dir "$T/missing"
expect 'missing folder exits 3 and names it' '[ "$RC" = 3 ] && grep -Fxq "no log folder: $T/missing" "$T/out"'
unset PLUGIN_LOG_DIR
run_sut report
expect 'unset variable with no dir exits 2 visibly' '[ "$RC" = 2 ] && grep -q PLUGIN_LOG_DIR "$T/err"'
mkdir -p "$T/empty-root"; : > "$T/empty-root/.gitmodules"
RC=0; bash "$SUT" report --dir "$T/logs" --root "$T/empty-root" > "$T/out" 2> "$T/err" || RC=$?
expect 'empty roster is a derivation error' '[ "$RC" = 2 ] && grep -Fq "DERIVE-FAILED: no plugins in .gitmodules" "$T/err"'
printf '[broken\n' > "$T/empty-root/.gitmodules"
RC=0; bash "$SUT" report --dir "$T/logs" --root "$T/empty-root" > "$T/out" 2> "$T/err" || RC=$?
expect 'failed roster enumeration is not an empty healthy roster' '[ "$RC" = 2 ] && grep -q DERIVE-FAILED "$T/err"'
RC=0; PATH="$T/no-tools" /bin/bash "$SUT" report --dir "$T/logs" > "$T/out" 2> "$T/err" || RC=$?
expect 'absent jq exits 5' '[ "$RC" = 5 ] && grep -q jq "$T/err"'
run_sut report --dir "$T/logs" --archive ../../escape
expect 'archive traversal is usage failure' '[ "$RC" = 2 ] && grep -q date "$T/err"'
run_sut rotate --dir "$T/logs" --today 2026-02-30
expect 'impossible date is usage failure' '[ "$RC" = 2 ] && grep -q date "$T/err"'
# Exercise permission denial as nobody even though the fence itself is root.
mkdir -p "$T/denied"; chmod 500 "$T/denied"
if [ "$(id -u)" != 0 ]; then
  run_sut report --dir "$T/denied"
  expect 'unreadable folder exits 4' '[ "$RC" = 4 ] && grep -q "folder unreadable:" "$T/out"'
elif command -v runuser > /dev/null; then
  chmod a+rx "$T" "$T/root"
  RC=0; runuser -u nobody -- bash "$SUT" report --dir "$T/denied" --root "$T/root" > "$T/out" 2> "$T/err" || RC=$?
  expect 'unreadable folder exits 4 on non-root runner' '[ "$RC" = 4 ] && grep -q "folder unreadable:" "$T/out"'
else
  printf 'SKIP unreadable-folder skip-because:superuser (runuser unavailable)\n'
fi
chmod 700 "$T/denied"
# An unreadable level entry remains an operational failure.
mkdir "$T/logs/callmeter/info.jsonl.1"
run_sut report --dir "$T/logs"
expect 'unreadable level entry exits visibly' '[ "$RC" = 4 ] && grep -q "folder unreadable:" "$T/out"'
rmdir "$T/logs/callmeter/info.jsonl.1"
mkdir -p "$T/logs/callmeter/archive/2026-09-17" "$T/logs/callmeter/archive/2026-09-18" "$T/logs/callmeter/decisions"
printf sentinel > "$T/logs/callmeter/untouched"
printf old > "$T/logs/callmeter/decisions/old.jsonl"
printf recent > "$T/logs/callmeter/decisions/recent.jsonl"
touch -t 202609170000 "$T/logs/callmeter/decisions/old.jsonl"
touch -t 202609190000 "$T/logs/callmeter/decisions/recent.jsonl"
run_sut rotate --dir "$T/logs" --today 2026-10-16
expect 'rotate moves level and capped rows into date archive' '[ "$RC" = 0 ] && [ -s "$T/logs/callmeter/archive/2026-10-16/errors.jsonl" ] && [ -s "$T/logs/callmeter/archive/2026-10-16/warnings.jsonl.1" ] && [ ! -e "$T/logs/callmeter/errors.jsonl" ]'
expect 'retention strictly older than 28 days, decision mtime and outside sentinel' '[ ! -e "$T/logs/callmeter/archive/2026-09-17" ] && [ -d "$T/logs/callmeter/archive/2026-09-18" ] && [ ! -e "$T/logs/callmeter/decisions/old.jsonl" ] && [ -f "$T/logs/callmeter/decisions/recent.jsonl" ] && [ "$(cat "$T/logs/callmeter/untouched")" = sentinel ]'
run_sut report --dir "$T/logs" --archive 2026-10-16
expect 'archive report reads moved rows and newest rotate' '[ "$RC" = 0 ] && grep -Fq "store-failed · internal/hookentry/callmeter.go#store · 2" "$T/out" && grep -Fq "last rotate: 2026-10-16" "$T/out"'
row callmeter error store-failed 2026-10-16T15:20:31.123Z again > "$T/logs/callmeter/errors.jsonl"
run_sut rotate --dir "$T/logs" --today 2026-10-16
expect 'same-day collision uses errors-2.jsonl without overwriting' '[ "$RC" = 0 ] && [ -s "$T/logs/callmeter/archive/2026-10-16/errors-2.jsonl" ] && [ "$(wc -l < "$T/logs/callmeter/archive/2026-10-16/errors.jsonl")" = 3 ]'
run_sut report --dir "$T/logs" --archive 2026-10-16
expect 'archive report includes collision-suffixed rows' '[ "$RC" = 0 ] && grep -Fq "store-failed · internal/hookentry/callmeter.go#store · 3" "$T/out"'
run_sut rotate --dir "$T/missing" --today 2026-10-16
expect 'missing rotate target exits 3' '[ "$RC" = 3 ] && grep -Fxq "no log folder: $T/missing" "$T/out"'
# Ignore is asserted with git's own producer without creating a real log folder.
mkdir -p "$T/ignore"
cp "$ROOT/.gitignore" "$T/ignore/.gitignore"
git -C "$T/ignore" init -q
if git -C "$T/ignore" check-ignore -q --no-index plugins/tmp/logs/x/errors.jsonl; then ok 'plugins/tmp logs are ignored'; else bad 'plugins/tmp logs are ignored'; fi
mkdir -p "$T/report/callmeter"
run_sut report --dir "$T/report" --archive 2026-10-01
expect 'missing selected archive is named rather than reported as empty' '[ "$RC" = 0 ] && grep -Fxq "no archive 2026-10-01" "$T/out" && grep -Fxq "no valid rows: buddy, callmeter, effort-live-control, engine-sync, gpt-gateway, tool-visibility-controller" "$T/out"'
mkdir -p "$T/report/callmeter/archive/2026-10-01"
run_sut report --dir "$T/report" --archive 2026-10-01
expect 'existing empty archive has a distinct visible state' '[ "$RC" = 0 ] && grep -Fxq "archive: 2026-10-01" "$T/out" && grep -Fq "span: none" "$T/out"'
mkdir -p "$T/bin"; REAL_JQ=$(command -v jq)
printf '%140000s\n' x > "$T/report/callmeter/errors.jsonl"
row callmeter error store-failed 2026-10-14T15:20:31.123Z recovered >> "$T/report/callmeter/errors.jsonl"
run_sut report --dir "$T/report"
expect 'valid row after oversized input is grouped exactly once' '[ "$RC" = 0 ] && grep -Fxq "store-failed · internal/hookentry/callmeter.go#store · 1 · 2026-10-14T15:20:31.123Z · 2026-10-14T15:20:31.123Z · recovered" "$T/out" && grep -Fq "contract-skipped: 1" "$T/out"'
printf '#!/usr/bin/env bash\nif [ "${1:-}" = -cen ]; then echo "injected jq operational failure" >&2; exit 3; fi\nexec %q "$@"\n' "$REAL_JQ" > "$T/bin/jq"; chmod 700 "$T/bin/jq"
row callmeter error store-failed 2026-10-14T15:20:31.123Z broken > "$T/report/callmeter/errors.jsonl"
PATH="$T/bin:$PATH" run_sut report --dir "$T/report"
expect 'jq operational failure is not a malformed-row success' '[ "$RC" = 4 ] && grep -q "injected jq operational failure" "$T/out"'
RC=0; "$SUT" report --dir "$T/report" --root "$T/root" > "$T/out" 2> "$T/err" || RC=$?
expect 'documented direct executable entry works' '[ "$RC" = 0 ]'
# A symlink selected as the root (also as in a symlinked HOME) is legitimate.
mkdir -p "$T/physical-root/callmeter/archive/2026-01-01" "$T/physical-root/callmeter/decisions"
row callmeter error store-failed 2026-10-14T15:20:31.123Z aliased > "$T/physical-root/callmeter/errors.jsonl"
mkdir -p "$T/physical-root/retired-plugin"
row retired-plugin error store-failed 2026-10-14T15:20:31.123Z aliased > "$T/physical-root/retired-plugin/errors.jsonl"
printf old > "$T/physical-root/callmeter/decisions/old.jsonl"
touch -t 202001010000 "$T/physical-root/callmeter/decisions/old.jsonl"
ln -s "$T/physical-root" "$T/root-alias"
PLUGIN_LOG_DIR="$T/root-alias" run_sut rotate --today 2026-10-16
expect 'legitimate symlink root rotates and prunes within its physical root' '[ "$RC" = 0 ] && grep -Fq aliased "$T/physical-root/callmeter/archive/2026-10-16/errors.jsonl" && [ ! -e "$T/physical-root/callmeter/archive/2026-01-01" ] && [ ! -e "$T/physical-root/callmeter/decisions/old.jsonl" ]'
PLUGIN_LOG_DIR="$T/root-alias" run_sut report --archive 2026-10-16
expect 'legitimate symlink root reports archived rows' '[ "$RC" = 0 ] && grep -Fq "store-failed · internal/hookentry/callmeter.go#store · 1" "$T/out"'
expect 'symlink root discovers and rotates writers outside the roster' '[ "$RC" = 0 ] && grep -Fxq "retired-plugin" "$T/out" && grep -Fq aliased "$T/physical-root/retired-plugin/archive/2026-10-16/errors.jsonl"'
mkdir -p "$T/empty-rotation"
run_sut rotate --dir "$T/empty-rotation" --today 2026-10-16
expect 'empty rotation succeeds' '[ "$RC" = 0 ] && grep -Fxq "rotated: $T/empty-rotation · 2026-10-16" "$T/out"'
run_sut report --dir "$T/empty-rotation"
expect 'empty live period lists the full roster' '[ "$RC" = 0 ] && grep -Fxq "no valid rows: buddy, callmeter, effort-live-control, engine-sync, gpt-gateway, tool-visibility-controller" "$T/out"'
for plugin in buddy callmeter effort-live-control engine-sync gpt-gateway tool-visibility-controller; do
  mkdir -p "$T/empty-rotation/$plugin"
  row "$plugin" info started 2026-10-14T15:20:31.123Z started > "$T/empty-rotation/$plugin/info.jsonl"
done
run_sut report --dir "$T/empty-rotation"
expect 'a period where every roster plugin wrote prints none' '[ "$RC" = 0 ] && grep -Fxq "no valid rows: none" "$T/out"'
# A writer's cap rename can happen after find or immediately before our mv.
mkdir -p "$T/race-bin"
REAL_MV=$(command -v mv); REAL_FIND=$(command -v find)
cat > "$T/race-bin/mv" <<'EOF'
#!/usr/bin/env bash
if [ "$RACE_PHASE" = move ] && [ "${2:-}" = "$RACE_SOURCE" ]; then
  "$REAL_MV" -- "$RACE_SOURCE" "$RACE_SOURCE.1" || exit $?
fi
exec "$REAL_MV" "$@"
EOF
cat > "$T/race-bin/find" <<'EOF'
#!/usr/bin/env bash
"$REAL_FIND" "$@" || exit $?
if [ "$RACE_PHASE" = list ] && [ "${1:-}" = "${RACE_SOURCE%/*}" ]; then
  "$REAL_MV" -- "$RACE_SOURCE" "$RACE_SOURCE.1" || exit $?
fi
EOF
chmod +x "$T/race-bin/mv" "$T/race-bin/find"
for phase in move list; do
  race="$T/race-$phase"; mkdir -p "$race/callmeter"
  row callmeter error store-failed 2026-10-14T15:20:31.123Z raced > "$race/callmeter/errors.jsonl"
  row callmeter warning degraded 2026-10-14T15:20:31.123Z preserved > "$race/callmeter/warnings.jsonl"
  PATH="$T/race-bin:$PATH" RACE_PHASE="$phase" RACE_SOURCE="$race/callmeter/errors.jsonl" REAL_MV="$REAL_MV" REAL_FIND="$REAL_FIND" run_sut rotate --dir "$race" --today 2026-10-16
  expect "rotate names writer rename during $phase and continues other files" '[ "$RC" = 0 ] && grep -Fxq "skipped vanished level file: $race/callmeter/errors.jsonl" "$T/out" && grep -Fq preserved "$race/callmeter/archive/2026-10-16/warnings.jsonl" && grep -Fq raced "$race/callmeter/errors.jsonl.1"'
done
# A report-stage failure happens after read_rows has opened a real level file.
printf '#!/usr/bin/env bash\nif [ "${1:-}" = -rs ]; then echo "injected report jq failure" >&2; exit 3; fi\nexec %q "$@"\n' "$REAL_JQ" > "$T/bin/jq"
PATH="$T/bin:$PATH" run_sut report --dir "$T/report"
expect 'report-stage errors remain visible on caller stderr' '[ "$RC" = 3 ] && grep -Fxq "injected report jq failure" "$T/err"'
shtest_end

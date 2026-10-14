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
row callmeter info logger-ready 2026-10-14T15:19:31.123Z ready > "$T/logs/callmeter/info.jsonl"
row tool-visibility-controller info logger-ready 2026-10-14T15:19:31.123Z ready > "$T/logs/tool-visibility-controller/info.jsonl"
run_sut report --dir "$T/logs"
expect 'report groups event and where with exact count, first/last and last message' '[ "$RC" = 0 ] && grep -Fxq "store-failed · internal/hookentry/callmeter.go#store · 2 · 2026-10-14T15:20:31.123Z · 2026-10-14T15:21:31.123Z · still locked" "$T/out"'
expect 'report fixed section ordering and exact info count' '[ "$(grep -E "^(ERRORS|WARNINGS|INFO|COVERAGE|CONTRACT|STRAYS)$" "$T/out" | paste -sd, -)" = ERRORS,WARNINGS,INFO,COVERAGE,CONTRACT,STRAYS ] && grep -Fxq "callmeter 1" "$T/out"'
expect 'coverage distinguishes wrote, dark, quiet and no folder' 'grep -Fxq "callmeter wrote" "$T/out" && grep -Fxq "buddy dark" "$T/out" && grep -Fxq "tool-visibility-controller quiet" "$T/out" && grep -Fxq "engine-sync no folder" "$T/out" && grep -Fxq "effort-live-control no folder" "$T/out"'
expect 'header states real span, no rotate and folder' 'grep -Fq "$T/logs" "$T/out" && grep -Fq "2026-10-14T15:19:31.123Z .. 2026-10-14T15:22:31.123Z" "$T/out" && grep -Fq "last rotate: none" "$T/out"'
printf '\n{bad json}\n' > "$T/logs/buddy/warnings.jsonl"
run_sut report --dir "$T/logs"
expect 'blank and malformed rows are counted at exact file line' '[ "$RC" = 0 ] && grep -Fxq "buddy 2 (warnings.jsonl:1)" "$T/out"'
row buddy warning degraded 2026-10-14T15:20:31.123Z fallback > "$T/logs/buddy/warnings.jsonl"
printf '{bad json}\n' >> "$T/logs/buddy/warnings.jsonl"
run_sut report --dir "$T/logs"
expect 'malformed row identifies warnings.jsonl line 2' 'grep -Fxq "buddy 1 (warnings.jsonl:2)" "$T/out"'
# Every form rejected here is producer-independent row-contract input.
printf '%s\n' '{"ts":"not-a-date"}' '[]' 'null' '{"ts":"2026-99-99T00:00:00Z","plugin":"buddy","level":"warning","event":"bad","msg":"x","where":"x"}' > "$T/logs/buddy/errors.jsonl"
row callmeter warning wrong-plugin 2026-10-14T15:20:31.123Z wrong >> "$T/logs/buddy/errors.jsonl"
row buddy info wrong-file 2026-10-14T15:20:31.123Z wrong >> "$T/logs/buddy/errors.jsonl"
row buddy error bad-shape 2026-10-14T15:20:31.123Z wrong | jq -c '.detail=[]' >> "$T/logs/buddy/errors.jsonl"
row buddy error oversized 2026-10-14T15:20:31.123Z "$(printf '%5000s' x)" >> "$T/logs/buddy/errors.jsonl"
run_sut report --dir "$T/logs"
expect 'contract rejects every malformed shape, wrong identity, level, date and oversized row' '[ "$RC" = 0 ] && grep -Fxq "buddy 9 (errors.jsonl:1)" "$T/out"'
mkdir -p "$HOME/.local/state/gpt-gateway/logs"
for n in 1 2 3; do row gpt-gateway error db-rows-lost 2026-10-14T15:20:31.123Z lost; done > "$HOME/.local/state/gpt-gateway/logs/errors.jsonl"
PLUGIN_LOG_DIR="$T/logs" run_sut report --dir "$T/logs"
expect 'strays name fallback directory and all rows' '[ "$RC" = 0 ] && grep -Fq "gpt-gateway 3 rows" "$T/out" && grep -Fq "$HOME/.local/state/gpt-gateway/logs" "$T/out"'
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
# A level path that exists but cannot be read is not absence.
ln -s "$T/missing-row" "$T/logs/callmeter/info.jsonl.1"
run_sut report --dir "$T/logs"
expect 'symlink level path is visibly refused' '[ "$RC" = 4 ] && grep -q "folder unreadable:" "$T/out"'
rm "$T/logs/callmeter/info.jsonl.1"
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
mkdir -p "$T/outside/2026-01-01" "$T/logs/engine-sync"
printf protected > "$T/outside/2026-01-01/sentinel"
ln -s "$T/outside" "$T/logs/engine-sync/archive"
run_sut rotate --dir "$T/logs" --today 2026-10-16
expect 'rotation refuses archive symlink and preserves outside content' '[ "$RC" = 4 ] && [ "$(cat "$T/outside/2026-01-01/sentinel")" = protected ]'
# Ignore is asserted with git's own producer without creating a real log folder.
mkdir -p "$T/ignore"
cp "$ROOT/.gitignore" "$T/ignore/.gitignore"
git -C "$T/ignore" init -q
if git -C "$T/ignore" check-ignore -q --no-index plugins/tmp/logs/x/errors.jsonl; then ok 'plugins/tmp logs are ignored'; else bad 'plugins/tmp logs are ignored'; fi
# Reachable hostile producer metadata cannot manufacture report sections.
rm "$T/logs/engine-sync/archive"
mkdir -p "$T/check"
row callmeter error store-failed 2026-10-14T15:20:31.123Z $'broken\nCOVERAGE\033[2J' > "$T/check/errors.jsonl"
mkdir -p "$T/report/callmeter"; cp "$T/check/errors.jsonl" "$T/report/callmeter/"
run_sut report --dir "$T/report"
expect 'producer controls are escaped, not emitted as report structure' '[ "$RC" = 0 ] && [ "$(grep -cx COVERAGE "$T/out")" = 1 ] && ! LC_ALL=C grep -q $'"'"'\033'"'"' "$T/out"'
run_sut report --dir "$T/report" --archive 2026-10-01
expect 'missing selected archive is explicit rather than dark' '[ "$RC" = 0 ] && grep -Fxq "callmeter no archive 2026-10-01" "$T/out"'
# Read a large real line without passing an unbounded argument to jq.
mkdir -p "$T/bin"; REAL_JQ=$(command -v jq)
printf '#!/usr/bin/env bash\nif [ "${1:-}" = -cen ] && [ "${#4}" -gt 4095 ]; then touch %q; fi\nexec %q "$@"\n' "$T/unbounded-jq" "$REAL_JQ" > "$T/bin/jq"; chmod 700 "$T/bin/jq"
printf '%140000s\n' x > "$T/report/callmeter/errors.jsonl"
PATH="$T/bin:$PATH" run_sut report --dir "$T/report"
expect 'oversized input is counted before entering jq as an unbounded argument' '[ "$RC" = 0 ] && grep -Fxq "callmeter 1 (errors.jsonl:1)" "$T/out" && [ ! -e "$T/unbounded-jq" ]'
printf '#!/usr/bin/env bash\nif [ "${1:-}" = -cen ]; then echo "injected jq operational failure" >&2; exit 3; fi\nexec %q "$@"\n' "$REAL_JQ" > "$T/bin/jq"; chmod 700 "$T/bin/jq"
row callmeter error store-failed 2026-10-14T15:20:31.123Z broken > "$T/report/callmeter/errors.jsonl"
PATH="$T/bin:$PATH" run_sut report --dir "$T/report"
expect 'jq operational failure is not a malformed-row success' '[ "$RC" = 4 ] && grep -q "injected jq operational failure" "$T/out"'
RC=0; "$SUT" report --dir "$T/report" --root "$T/root" > "$T/out" 2> "$T/err" || RC=$?
expect 'documented direct executable entry works' '[ "$RC" = 0 ]'
# Caller ruling 2: resolve each destructive target, including a parent changed
# after inventory. These swaps are synthetic, not a hostile-ancestor policy.
REAL_FIND=$(command -v find)
mkdir -p "$T/contain-bin"
cat > "$T/contain-bin/find" <<'EOF'
#!/usr/bin/env bash
set -eu
"$REAL_FIND" "$@"
if [ "$1" = "$SWAP_PARENT" ]; then
  mv -- "$SWAP_PARENT" "$SWAP_PARENT.saved"
  ln -s "$SWAP_OUTSIDE" "$SWAP_PARENT"
fi
EOF
chmod 700 "$T/contain-bin/find"
export REAL_FIND
for door in rotate archive-prune decision-prune; do
  base="$T/contain-$door"; outside="$T/contain-$door-outside"
  mkdir -p "$base/callmeter" "$outside"
  if [ "$door" = decision-prune ]; then
    parent="$base/callmeter/decisions"
    mkdir -p "$parent"
    printf protected > "$outside/old.jsonl"; printf inside > "$parent/old.jsonl"
    touch -t 202001010000 "$outside/old.jsonl" "$parent/old.jsonl"
    protected="$outside/old.jsonl"
  else
    parent="$base/callmeter/archive"
    mkdir -p "$parent/2026-01-01" "$outside/2026-01-01"
    printf protected > "$outside/2026-01-01/sentinel"
    protected="$outside/2026-01-01/sentinel"
    if [ "$door" = rotate ]; then
      row callmeter error store-failed 2026-10-14T15:20:31.123Z inside > "$base/callmeter/errors.jsonl"
    fi
  fi
  SWAP_PARENT="$parent" SWAP_OUTSIDE="$outside" PATH="$T/contain-bin:$PATH" \
    run_sut rotate --dir "$base" --today 2026-10-16
  expect "resolved containment refuses $door outside root" '[ "$RC" = 4 ] && grep -Fq "resolved target outside log root:" "$T/out" && [ -f "$protected" ] && [ "$(cat "$protected")" = protected ] && [ ! -e "$outside/2026-10-16" ]'
  expect "failed $door releases its own rotate lock" '[ ! -e "$base/.rotate.lock" ]'
done
# A symlink selected as the root (also as in a symlinked HOME) is legitimate.
mkdir -p "$T/physical-root/callmeter/archive/2026-01-01" "$T/physical-root/callmeter/decisions"
row callmeter error store-failed 2026-10-14T15:20:31.123Z aliased > "$T/physical-root/callmeter/errors.jsonl"
mkdir -p "$T/physical-root/retired-plugin"
row retired-plugin error store-failed 2026-10-14T15:20:31.123Z aliased > "$T/physical-root/retired-plugin/errors.jsonl"
printf old > "$T/physical-root/callmeter/decisions/old.jsonl"
touch -t 202001010000 "$T/physical-root/callmeter/decisions/old.jsonl"
ln -s "$T/physical-root" "$T/root-alias"
PLUGIN_LOG_DIR="$T/root-alias" run_sut rotate --today 2026-10-16
expect 'legitimate symlink root rotates and prunes within its physical root' '[ "$RC" = 0 ] && grep -Fq aliased "$T/physical-root/callmeter/archive/2026-10-16/errors.jsonl" && [ ! -e "$T/physical-root/callmeter/archive/2026-01-01" ] && [ ! -e "$T/physical-root/callmeter/decisions/old.jsonl" ] && [ ! -e "$T/physical-root/.rotate.lock" ]'
PLUGIN_LOG_DIR="$T/root-alias" run_sut report --archive 2026-10-16
expect 'legitimate symlink root reports archived rows' '[ "$RC" = 0 ] && grep -Fq "store-failed · internal/hookentry/callmeter.go#store · 1" "$T/out"'
expect 'symlink root discovers and rotates writers outside the roster' '[ "$RC" = 0 ] && grep -Fxq "retired-plugin wrote" "$T/out" && grep -Fq aliased "$T/physical-root/retired-plugin/archive/2026-10-16/errors.jsonl"'
# Caller ruling 3: two real rotators, with the first paused at its move.
mkdir -p "$T/locked/callmeter" "$T/lock-bin"
row callmeter error store-failed 2026-10-14T15:20:31.123Z first-generation > "$T/locked/callmeter/errors.jsonl"
cp "$T/locked/callmeter/errors.jsonl" "$T/first-generation"
mkfifo "$T/rotate-ready" "$T/rotate-release"
REAL_MV=$(command -v mv)
cat > "$T/lock-bin/mv" <<'EOF'
#!/usr/bin/env bash
set -eu
printf 'ready\n' > "$ROTATE_READY"
timeout 15 bash -c 'IFS= read -r release < "$1"' _ "$ROTATE_RELEASE"
exec "$REAL_MV" "$@"
EOF
chmod 700 "$T/lock-bin/mv"
export REAL_MV
ROTATE_READY="$T/rotate-ready" ROTATE_RELEASE="$T/rotate-release" PATH="$T/lock-bin:$PATH" \
  bash "$SUT" rotate --dir "$T/locked" --today 2026-10-16 --root "$T/root" > "$T/first-out" 2> "$T/first-err" &
rotator=$!
ready_rc=0; timeout 10 cat "$T/rotate-ready" > "$T/ready" || ready_rc=$?
expect 'first rotator reaches deterministic move barrier' '[ "$ready_rc" = 0 ] && grep -Fxq ready "$T/ready"'
run_sut rotate --dir "$T/locked" --today 2026-10-16
expect 'second rotator refuses active lock and moves no live generation' '[ "$RC" != 0 ] && grep -Fxq "rotate already running: $T/locked/.rotate.lock" "$T/err" && cmp -s "$T/first-generation" "$T/locked/callmeter/errors.jsonl" && [ -d "$T/locked/.rotate.lock" ]'
run_sut report --dir "$T/locked"
expect 'report ignores the reserved active rotate lock' '[ "$RC" = 0 ] && grep -Fxq "callmeter wrote" "$T/out"'
release_rc=0; timeout 5 bash -c 'printf "release\n" > "$1"' _ "$T/rotate-release" || release_rc=$?
first_rc=0; wait "$rotator" || first_rc=$?
expect 'first rotator preserves its generation and cleans its lock on exit' '[ "$release_rc" = 0 ] && [ "$first_rc" = 0 ] && cmp -s "$T/first-generation" "$T/locked/callmeter/archive/2026-10-16/errors.jsonl" && [ ! -e "$T/locked/.rotate.lock" ]'
mkdir -p "$T/stale/callmeter" "$T/stale/.rotate.lock"
row callmeter error store-failed 2026-10-14T15:20:31.123Z stale-generation > "$T/stale/callmeter/errors.jsonl"
cp "$T/stale/callmeter/errors.jsonl" "$T/stale-generation"
touch -t 200001010000 "$T/stale/.rotate.lock"
run_sut rotate --dir "$T/stale" --today 2026-10-16
expect 'stale rotate lock is reported, retained and never stolen' '[ "$RC" != 0 ] && grep -Fxq "rotate already running: $T/stale/.rotate.lock" "$T/err" && grep -Fq "stale rotate lock:" "$T/err" && grep -Fq "remove by hand" "$T/err" && [ -d "$T/stale/.rotate.lock" ] && cmp -s "$T/stale-generation" "$T/stale/callmeter/errors.jsonl" && [ ! -e "$T/stale/callmeter/archive" ]'
rmdir "$T/stale/.rotate.lock"
run_sut rotate --dir "$T/stale" --today 2026-10-16
expect 'manual stale-lock removal permits the next rotation' '[ "$RC" = 0 ] && cmp -s "$T/stale-generation" "$T/stale/callmeter/archive/2026-10-16/errors.jsonl" && [ ! -e "$T/stale/.rotate.lock" ]'
mkdir -p "$T/empty-rotation"
run_sut rotate --dir "$T/empty-rotation" --today 2026-10-16
expect 'empty rotation succeeds and cleans its lock' '[ "$RC" = 0 ] && [ ! -e "$T/empty-rotation/.rotate.lock" ]'
if command -v runuser > /dev/null && [ "$(id -u)" = 0 ]; then
  chmod a+rx "$T" "$T/root"; chmod 555 "$T/denied"
  RC=0; runuser -u nobody -- bash "$SUT" rotate --dir "$T/denied" --today 2026-10-16 --root "$T/root" > "$T/out" 2> "$T/err" || RC=$?
  expect 'failed lock mkdir is an operation error, not a concurrent rotation' '[ "$RC" = 4 ] && grep -Fq "folder unreadable:" "$T/out" && ! grep -Fq "rotate already running:" "$T/err" && [ ! -e "$T/denied/.rotate.lock" ]'
fi
shtest_end

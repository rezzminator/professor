#!/usr/bin/env bash
set -uo pipefail
SHTEST_TAG=steps-test
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"
SUT="${STEPS_SUT:-$(dirname -- "${BASH_SOURCE[0]}")/../../steps.sh}"

if out="$(bash -c 'source "$1"; source "$2"' _ "$SUT" "$(dirname "$SUT")/checks.sh" 2>&1)" && [ -z "$out" ]; then
  ok 'source both: silent and successful'
else bad 'source both' "$out"; fi
source "$SUT"

busy_step() {
  printf '%s start\n' "$EPOCHREALTIME" >> "$T/events"
  sleep 0.12
  printf '%s end\n' "$EPOCHREALTIME" >> "$T/events"
}
steps_reset
for n in 1 2 3 4 5 6; do steps_add "s$n" busy_step; done
STEPS_JOBS=2
if steps_run "$T/concurrent" >"$T/concurrent.out" && [ "$(grep -c ' start$' "$T/events")" -eq 6 ] &&
  [ "$(sort -n "$T/events" | awk '{n+=($2=="start"?1:-1); if(n>max)max=n} END{print max+0}')" -le 2 ]; then
  ok 'six steps respect two concurrent jobs'
else bad 'concurrency bound' "$(cat "$T/concurrent.out")"; fi

emits() { echo stdout; echo stderr >&2; }
steps_reset; steps_add output emits
if steps_run "$T/logs" >"$T/logs.out" && grep -qx stdout "$T/logs/steps/output.log" && grep -qx stderr "$T/logs/steps/output.log"; then
  ok 'stdout and stderr share the step log'
else bad 'per-step log'; fi

steps_reset
steps_add c true; steps_add a true; steps_add b true
if steps_run "$T/table" >"$T/table.out" &&
  [ "$(cut -f1 "$T/table/gate.tsv" | paste -sd, -)" = 'step,c,a,b,WALL' ] &&
  [ "$(head -1 "$T/table/gate.tsv")" = "$(printf 'step\tverdict\tseconds')" ] &&
  awk -F '\t' 'NR>1 {if(NF!=3 || $3 !~ /^[0-9]+\.[0-9]$/) exit 1} END{if(NR!=5)exit 1}' "$T/table/gate.tsv" &&
  cmp -s "$T/table/gate.tsv" "$T/table.out"; then
  ok 'table preserves registration order and prints one-decimal seconds'
else bad 'table shape or output'; fi
if grep -q "$(printf '^c\tPASS\t')" "$T/table/gate.tsv"; then ok 'zero exit and zero FAILURES pass'; else bad 'pass verdict'; fi

exit_three() { return 3; }
steps_reset; steps_add broken exit_three
if ! steps_run "$T/exit" >"$T/exit.out" && grep -q "$(printf '^broken\tFAIL\t')" "$T/exit/gate.tsv" &&
  grep -q "$T/exit/steps/broken.log" "$T/exit.out"; then
  ok 'non-zero exit fails and prints its log path'
else bad 'exit failure'; fi

fail_step() { FAILURES=$((FAILURES + 1)); }
counted_failure() { fail_step; return 0; }
steps_reset; steps_add counted counted_failure
if ! steps_run "$T/count" >"$T/count.out" && grep -q "$(printf '^counted\tFAIL\t')" "$T/count/gate.tsv"; then
  ok 'FAILURES makes a zero exit red'
else bad 'FAILURES failure'; fi

steps_reset; steps_add absent no-such-cmd
if ! steps_run "$T/missing" >"$T/missing.out" && grep -q "$(printf '^absent\tNOT-RUN\t')" "$T/missing/gate.tsv"; then
  ok 'missing command is NOT-RUN'
else bad 'missing command'; fi

steps_reset; steps_add blocked true
mkdir -p "$T/unwritable/steps/blocked.log"
if ! steps_run "$T/unwritable" >"$T/unwritable.out" 2>"$T/unwritable.err" &&
  grep -q "$(printf '^blocked\tNOT-RUN\t')" "$T/unwritable/gate.tsv"; then
  ok 'unwritable step log is NOT-RUN with a table'
else bad 'unwritable step log'; fi

for sig in TERM KILL; do
  signalled() { kill -s "$sig" "$BASHPID"; }
  steps_reset; steps_add signal signalled
  if ! steps_run "$T/signal-$sig" >"$T/signal-$sig.out" 2>&1 && grep -q "$(printf '^signal\tNOT-RUN\t')" "$T/signal-$sig/gate.tsv"; then
    ok "$sig makes a step NOT-RUN"
  else bad "$sig verdict"; fi
done

cat > "$T/signal-program.sh" <<'EOF'
trap 'printf "trapped\n"' "$1"
kill -s "$1" "$BASHPID"
printf 'survived\n'
EOF
signal_runner='source "$1"; steps_reset; steps_add signal bash "$3" "$4"; steps_run "$2"'
for sig in INT QUIT; do
  run="$T/program-$sig"
  if env --default-signal=INT,QUIT bash -c "$signal_runner" _ "$SUT" "$run" "$T/signal-program.sh" "$sig" >"$run.out" 2>&1 &&
    grep -qx trapped "$run/steps/signal.log" && grep -q "$(printf '^signal\tPASS\t')" "$run/gate.tsv"; then
    ok "$sig reaches a step's program"
  else bad "$sig program disposition" "$(cat "$run.out")"; fi
done

for sig in INT QUIT; do
  run="$T/program-ignored-$sig"
  if env --ignore-signal=INT,QUIT bash -c "$signal_runner" _ "$SUT" "$run" "$T/signal-program.sh" "$sig" >"$run.out" 2>&1 &&
    grep -qx survived "$run/steps/signal.log" && ! grep -q trapped "$run/steps/signal.log" &&
    grep -q "$(printf '^signal\tPASS\t')" "$run/gate.tsv"; then
    ok "runner ignores $sig in a step program"
  else bad "ignored $sig program disposition" "$(cat "$run.out")"; fi
done

ends_step_shell() { exit 0; }
steps_reset; steps_add ended ends_step_shell
if ! steps_run "$T/step-exit" >"$T/step-exit.out" && grep -q "$(printf '^ended\tFAIL\t')" "$T/step-exit/gate.tsv"; then
  ok 'a step shell exit without a count fails closed'
else bad 'step shell exit verdict'; fi

steps_reset; steps_add first exit_three; steps_add second true
if bash -euo pipefail -c 'source "$1"; steps_reset; steps_add first false; steps_add second true; steps_run "$2"' _ "$SUT" "$T/strict" >"$T/strict.out" 2>&1; then
  bad 'strict caller should receive a red return'
elif grep -q "$(printf '^second\tPASS\t')" "$T/strict/gate.tsv" && grep -q "$(printf '^first\tFAIL\t')" "$T/strict/gate.tsv"; then
  ok 'strict caller gets full table and red return'
else bad 'strict caller did not finish all steps' "$(cat "$T/strict.out")"; fi

steps_reset
if ! steps_add 'A B' true 2>"$T/name.err" && grep -q 'A B' "$T/name.err" && [ "${#STEPS_NAMES[@]}" -eq 0 ]; then
  ok 'bad name refused without registration'
else bad 'bad name'; fi
steps_add same true
if ! steps_add same true 2>"$T/name.err" && grep -q same "$T/name.err" && [ "${#STEPS_NAMES[@]}" -eq 1 ]; then
  ok 'duplicate name refused without registration'
else bad 'duplicate name'; fi

timed_step() {
  local name="$1" delay="$2"
  printf '%s %s start\n' "$EPOCHREALTIME" "$name" >> "$T/phase.events"
  sleep "$delay"
  printf '%s %s end\n' "$EPOCHREALTIME" "$name" >> "$T/phase.events"
}
steps_reset
steps_add a timed_step a 0.10; steps_add b timed_step b 0.14
steps_barrier
steps_add c timed_step c 0.01; steps_add d timed_step d 0.01
STEPS_JOBS=4
if steps_run "$T/barrier" >"$T/barrier.out" &&
  [ "$(cut -f1 "$T/barrier/gate.tsv" | paste -sd, -)" = 'step,a,b,c,d,WALL' ] &&
  cmp -s "$T/barrier/gate.tsv" "$T/barrier.out" &&
  awk '$2=="a" || $2=="b" {if($3=="end" && $1>latest)latest=$1} $2=="c" || $2=="d" {if($3=="start" && (!earliest || $1<earliest))earliest=$1} END {exit !(latest>0 && earliest>latest)}' "$T/phase.events"; then
  ok 'barrier waits for both earlier steps and keeps table order'
else bad 'barrier order or table' "$(cat "$T/barrier.out")"; fi

steps_reset
steps_barrier
steps_add single true
steps_barrier
if steps_run "$T/empty-phase" >"$T/empty-phase.out" &&
  [ "$(cut -f1 "$T/empty-phase/gate.tsv" | paste -sd, -)" = 'step,single,WALL' ]; then
  ok 'empty phases preserve the single step'
else bad 'empty barrier side'; fi

heavy_step() {
  local name="$1"
  printf '%s %s start\n' "$EPOCHREALTIME" "$name" >> "$T/heavy.events"
  sleep 0.12
  printf '%s %s end\n' "$EPOCHREALTIME" "$name" >> "$T/heavy.events"
}
steps_reset
for n in 1 2 3 4 5 6; do steps_add_heavy "h$n" heavy_step "h$n"; done
STEPS_JOBS=6; STEPS_HEAVY_JOBS=2
if steps_run "$T/heavy" >"$T/heavy.out" &&
  [ "$(grep -c ' start$' "$T/heavy.events")" -eq 6 ] &&
  [ "$(sort -n "$T/heavy.events" | awk '{n+=($3=="start"?1:-1); if(n>max)max=n} END{print max+0}')" -le 2 ]; then
  ok 'six heavy steps respect two heavy slots'
else bad 'heavy concurrency bound' "$(cat "$T/heavy.out")"; fi

steps_reset
steps_add_heavy h1 timed_step h1 0.16
steps_add_heavy h2 timed_step h2 0.16
steps_add_heavy h3 timed_step h3 0.01
steps_add light timed_step light 0.01
STEPS_JOBS=3; STEPS_HEAVY_JOBS=2
: > "$T/phase.events"
if steps_run "$T/overtake" >"$T/overtake.out" &&
  awk '$2=="light" && $3=="start" {light=$1} $2=="h3" && $3=="start" {h3=$1} END {exit !(light>0 && h3>light)}' "$T/phase.events"; then
  ok 'light step overtakes a heavy step waiting for its slot'
else bad 'light overtake'; fi

steps_reset
for n in 1 2 3 4; do steps_add_heavy "default$n" heavy_step "default$n"; done
unset STEPS_JOBS STEPS_HEAVY_JOBS
: > "$T/heavy.events"
if steps_run "$T/heavy-default" >"$T/heavy-default.out" &&
  [ "$(sort -n "$T/heavy.events" | awk '{n+=($3=="start"?1:-1); if(n>max)max=n} END{print max+0}')" -eq 2 ] &&
  [ "$(steps_jobs)" = "$(nproc)" ]; then
  ok 'default heavy cap is two and default jobs follows CPU count'
else bad 'default heavy cap or jobs'; fi

steps_reset; steps_add valid true
STEPS_HEAVY_JOBS=0
if ! steps_run "$T/bad-heavy-cap" >"$T/bad-heavy-cap.out" 2>"$T/bad-heavy-cap.err" &&
  [ "$(cat "$T/bad-heavy-cap.err")" = 'steps_run: STEPS_HEAVY_JOBS must be a positive integer: 0' ]; then
  ok 'invalid heavy cap fails before scheduling'
else bad 'invalid heavy cap'; fi
unset STEPS_HEAVY_JOBS

steps_reset; steps_add known true
if ! steps_add_heavy 'A B' true 2>"$T/heavy-name.err" && grep -q 'A B' "$T/heavy-name.err" &&
  [ "${STEPS_NAMES[*]}" = known ]; then
  ok 'heavy step refuses invalid name'
else bad 'heavy invalid name'; fi
if ! steps_add_heavy known true 2>"$T/heavy-name.err" && grep -q known "$T/heavy-name.err" &&
  [ "${STEPS_NAMES[*]}" = known ]; then
  ok 'heavy step refuses duplicate name'
else bad 'heavy duplicate name'; fi

shtest_end

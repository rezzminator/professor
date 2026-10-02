#!/usr/bin/env bash
set -uo pipefail
SHTEST_TAG=steps-test
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"
SUT="${STEPS_SUT:-$(dirname -- "${BASH_SOURCE[0]}")/../../steps.sh}"
shtest_unset_gate_env # run nested under the gate's own stepprof wrapper, the suite starts from none of its knobs

if out="$(bash -c 'source "$1"; source "$2"' _ "$SUT" "$(dirname "$SUT")/checks.sh" 2>&1)" && [ -z "$out" ]; then
  ok 'source both: silent and successful'
else bad 'source both' "$out"; fi
source "$SUT"
if declare -F stepprof_run >/dev/null; then ok 'sourcing steps.sh defines stepprof_run'; else bad 'stepprof_run not defined after sourcing'; fi

# scripts/shtest.sh: the gate knobs a suite inherits from the gate's stepprof wrapper go, BASH_ENV stays
gate_knobs='PFM_TEST_ARTIFACT_DIR PFM_TEST_PROFILE PFM_TEST_PROFILE_PARENT PFM_TEST_DEADLINE_EPOCH PFM_TEST_TIMING_DIR STEPPROF_TRACE STEPPROF_GRACE_TICKS STEPPROF_PSI_DIR STEPS_BOUND_S STEPS_JOBS STEPS_HEAVY_JOBS SAMPLER_INTERVAL_S SAMPLER_SPIN_ITERS SAMPLER_CGROUP_DIR SAMPLER_PROC_DIR PFM_GATE_FIXTURES PFM_GATE_HISTORY_DIR'
if out="$(
  for n in $gate_knobs; do export "$n=inherited"; done
  export BASH_ENV="$T/bash_env"
  shtest_unset_gate_env
  for n in $gate_knobs; do [ -z "${!n+x}" ] || { echo "still set: $n"; exit 1; }; done
  [ "${BASH_ENV:-}" = "$T/bash_env" ] || { echo "BASH_ENV is now '${BASH_ENV:-}'"; exit 1; }
)"; then
  ok 'shtest_unset_gate_env clears every gate and profiler knob and keeps BASH_ENV'
else bad 'shtest_unset_gate_env' "$out"; fi

# a command forked and TERMed before its exec runs this shell's inherited EXIT trap: it must not remove $T
# (a TERM taken before the exec is lost to the exec'd program, so the forked sleep is short: the wait is bounded)
( eval "$(trap -p EXIT)"; exit 0 )
for ((i = 0; i < 20; i++)); do sleep 0.2 & forked=$!; kill -TERM "$forked"; { wait "$forked"; } 2>/dev/null; done
if [ -d "$T" ] && [ "$(trap -p EXIT)" ]; then
  ok 'a child that runs the sourcing shell EXIT trap, TERMed at once or not, leaves the suite scratch'
else bad 'a forked child removed the suite scratch dir'; mkdir -p "$T"; fi

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
  [ "$(cut -f1 "$T/table/gate.tsv" | paste -sd, -)" = 'step,c,a,b,STEPS' ] &&
  [ "$(head -1 "$T/table/gate.tsv")" = "$(printf 'step\tverdict\tseconds')" ] &&
  awk -F '\t' 'NR>1 {if(NF!=3 || $3 !~ /^[0-9]+\.[0-9]$/) exit 1} END{if(NR!=5)exit 1}' "$T/table/gate.tsv" &&
  cmp -s "$T/table/gate.tsv" "$T/table.out"; then
  ok 'table preserves registration order and prints one-decimal seconds'
else bad 'table shape or output'; fi
if grep -q "$(printf '^c\tPASS\t')" "$T/table/gate.tsv"; then ok 'zero exit and zero FAILURES pass'; else bad 'pass verdict'; fi

warn_step() { printf 'GATE-WARN 2 timing warning(s): package-a, SUITE(unit)\n'; }
steps_reset; steps_add warned warn_step
if steps_run "$T/warn" >"$T/warn.out" &&
  grep -q "$(printf '^warned\tPASS\t')" "$T/warn/gate.tsv" &&
  grep -Fxq "WARN warned: GATE-WARN 2 timing warning(s): package-a, SUITE(unit) — $T/warn/steps/warned.log" "$T/warn.out"; then
  ok 'PASS step surfaces its generic GATE-WARN with the log path'
else bad 'PASS warning surface' "$(cat "$T/warn.out")"; fi

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
if [ "$(cat "$T/count/steps/counted.failures")" = 1 ] && grep -q "$(printf '^counted\tFAIL\t')" "$T/count.out" &&
  grep -q "$(printf '^STEPS\tFAIL\t')" "$T/count/gate.tsv"; then
  ok 'the profiled step still writes its FAILURES count, and the STEPS row goes FAIL'
else bad 'FAILURES count file or STEPS row' "$(cat "$T/count.out")"; fi

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
  [ "$(cut -f1 "$T/barrier/gate.tsv" | paste -sd, -)" = 'step,a,b,c,d,STEPS' ] &&
  cmp -s "$T/barrier/gate.tsv" "$T/barrier.out" &&
  awk '$2=="a" || $2=="b" {if($3=="end" && $1>latest)latest=$1} $2=="c" || $2=="d" {if($3=="start" && (!earliest || $1<earliest))earliest=$1} END {exit !(latest>0 && earliest>latest)}' "$T/phase.events"; then
  ok 'barrier waits for both earlier steps and keeps table order'
else bad 'barrier order or table' "$(cat "$T/barrier.out")"; fi

steps_reset
steps_barrier
steps_add single true
steps_barrier
if steps_run "$T/empty-phase" >"$T/empty-phase.out" &&
  [ "$(cut -f1 "$T/empty-phase/gate.tsv" | paste -sd, -)" = 'step,single,STEPS' ]; then
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
  [ "$(sort -n "$T/heavy.events" | awk '{n+=($3=="start"?1:-1); if(n>max)max=n} END{print max+0}')" -eq 3 ] &&
  [ "$(steps_jobs)" = "$(nproc)" ]; then
  ok 'default heavy cap is three and default jobs follows CPU count'
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

prof_keys='step,ended,rc,bound_s,start_epoch,finish_epoch,wall_s,children_cpu,cpu_s,trace,err_records'
prof_get() { awk -F '\t' -v k="$2" '$1==k {print $2; exit}' "$1"; }
if [ "$(head -11 "$T/table/steps/c.prof.tsv" | cut -f1 | paste -sd, -)" = "$prof_keys" ] &&
  [ "$(prof_get "$T/table/steps/c.prof.tsv" step)" = c ] && [ "$(prof_get "$T/table/steps/c.prof.tsv" ended)" = exit ] &&
  [ "$(prof_get "$T/table/steps/c.prof.tsv" rc)" = 0 ] && [ "$(prof_get "$T/table/steps/c.prof.tsv" bound_s)" = 1500 ] &&
  [ -f "$T/table/steps/a.prof.tsv" ] && [ -f "$T/table/steps/b.prof.tsv" ]; then
  ok 'every step leaves prof.tsv with the contract keys, default bound 1500'
else bad 'prof.tsv shape' "$(cat "$T/table/steps/c.prof.tsv" 2>&1)"; fi

# stepprof_run is replaced by a runner that writes no profile: the step ran, so its verdict stays PASS
# and the missing profile is named after the table.
if (
    stepprof_run() { shift 4; "$@"; }
    steps_reset; steps_add ghost true
    steps_run "$T/profile-missing" >"$T/profile-missing.out" 2>&1
  ) && grep -q "$(printf '^ghost\tPASS\t')" "$T/profile-missing/gate.tsv" &&
  [ "$(tail -1 "$T/profile-missing.out")" = "PROFILE-MISSING ghost — $T/profile-missing/steps/ghost.prof.tsv" ] &&
  [ "$(grep -c '^PROFILE-MISSING' "$T/profile-missing.out")" -eq 1 ]; then
  ok 'a launched step with no prof.tsv is named after the table, its verdict untouched'
else bad 'PROFILE-MISSING line' "$(cat "$T/profile-missing.out")"; fi

hang_step() { sleep 60; }
steps_reset
steps_add hang hang_step; steps_set_bound 1
steps_add fine true
if STEPPROF_GRACE_TICKS=2 steps_run "$T/timeout" >"$T/timeout.out" 2>&1; then
  bad 'a step past its bound must make steps_run red' "$(cat "$T/timeout.out")"
elif grep -q "$(printf '^hang\tTIMEOUT\t')" "$T/timeout/gate.tsv" && grep -q "$(printf '^fine\tPASS\t')" "$T/timeout/gate.tsv" &&
  grep -q "$(printf '^STEPS\tFAIL\t')" "$T/timeout/gate.tsv" &&
  [ "$(prof_get "$T/timeout/steps/hang.prof.tsv" ended)" = timeout ] && [ "$(prof_get "$T/timeout/steps/hang.prof.tsv" bound_s)" = 1 ] &&
  [ "$(prof_get "$T/timeout/steps/fine.prof.tsv" bound_s)" = 1500 ] && [ -s "$T/timeout/steps/hang.hang/tree.txt" ] &&
  grep -Fxq "$(printf 'hang\tTIMEOUT\t%s\t%s/steps/hang.log\t%s/steps/hang.hang/tree.txt' "$(grep '^hang' "$T/timeout/gate.tsv" | cut -f3)" "$T/timeout" "$T/timeout")" "$T/timeout.out"; then
  ok 'a step past its bound is TIMEOUT with its log and hang tree in the table; others keep the default bound'
else bad 'TIMEOUT verdict' "$(cat "$T/timeout.out")"; fi

exit_124() { return 124; }
steps_reset; steps_add own124 exit_124
if ! steps_run "$T/exit124" >"$T/exit124.out" 2>&1 && grep -q "$(printf '^own124\tFAIL\t')" "$T/exit124/gate.tsv" &&
  [ "$(prof_get "$T/exit124/steps/own124.prof.tsv" ended)" = exit ] && [ "$(prof_get "$T/exit124/steps/own124.prof.tsv" rc)" = 124 ]; then
  ok 'a step that returns 124 on its own is FAIL, never TIMEOUT'
else bad 'own exit 124' "$(cat "$T/exit124.out")"; fi

err_step() { false; return 3; }
steps_reset; steps_add erring err_step; steps_add clean true
if ! steps_run "$T/err" >"$T/err.out" 2>&1 && [ "$(prof_get "$T/err/steps/erring.prof.tsv" err_records)" -gt 0 ] &&
  grep -q '^ERR ' "$T/err/steps/erring.xtrace" &&
  grep -Fxq "$(printf 'erring\tFAIL\t%s\t%s/steps/erring.log\t%s/steps/erring.xtrace' "$(grep '^erring' "$T/err/gate.tsv" | cut -f3)" "$T/err" "$T/err")" "$T/err.out" &&
  [ "$(grep -c 'xtrace' "$T/err.out")" -eq 1 ]; then
  ok 'a failed step with ERR records prints its xtrace path; a clean one prints none'
else bad 'ERR records pointer' "$(cat "$T/err.out")"; fi

steps_reset; steps_add one true
if ! steps_set_bound abc 2>"$T/bound.err" && [ "$(cat "$T/bound.err")" = 'steps_set_bound: bound must be a positive integer: abc' ] &&
  ! steps_set_bound 0 2>/dev/null && ! steps_set_bound 1.5 2>/dev/null && ! steps_set_bound -3 2>/dev/null && ! steps_set_bound '' 2>/dev/null &&
  [ -z "${STEPS_BOUND[0]}" ]; then
  ok 'a bound that is not a positive integer is refused on stderr and changes nothing'
else bad 'steps_set_bound validation' "$(cat "$T/bound.err")"; fi
steps_reset
if ! steps_set_bound 5 2>"$T/bound.err" && grep -q 'no step registered' "$T/bound.err"; then
  ok 'steps_set_bound with no step registered is refused'
else bad 'steps_set_bound before steps_add'; fi
steps_reset; steps_add first true; steps_set_bound 5; steps_add second true
if steps_run "$T/bounds" >"$T/bounds.out" 2>&1 && [ "$(prof_get "$T/bounds/steps/first.prof.tsv" bound_s)" = 5 ] &&
  [ "$(prof_get "$T/bounds/steps/second.prof.tsv" bound_s)" = 1500 ]; then
  ok 'steps_set_bound sets the last-registered step only'
else bad 'per-step bound' "$(cat "$T/bounds.out")"; fi

steps_reset; steps_add first true
if STEPS_BOUND_S=30 steps_run "$T/caller-bound" >"$T/caller-bound.out" 2>&1 && [ "$(prof_get "$T/caller-bound/steps/first.prof.tsv" bound_s)" = 30 ]; then
  ok 'STEPS_BOUND_S is the default per-step bound'
else bad 'STEPS_BOUND_S default' "$(cat "$T/caller-bound.out")"; fi
for v in 0 abc 1.5; do
  rm -rf "$T/bad-bound"
  if ! STEPS_BOUND_S="$v" steps_run "$T/bad-bound" >"$T/bad-bound.out" 2>"$T/bad-bound.err" &&
    [ "$(cat "$T/bad-bound.err")" = "steps_run: STEPS_BOUND_S must be a positive integer: $v" ] && [ ! -e "$T/bad-bound/gate.tsv" ]; then
    ok "invalid STEPS_BOUND_S '$v' fails before any step starts"
  else bad "invalid STEPS_BOUND_S $v" "$(cat "$T/bad-bound.err")"; fi
done

mkdir -p "$T/noprof"
cp "$SUT" "$T/noprof/steps.sh"
out="$(bash -c 'source "$1"' _ "$T/noprof/steps.sh" 2>&1)"; rc=$?
if [ "$rc" -eq 1 ] && [ "$out" = "steps.sh: cannot source $T/noprof/stepprof.sh" ]; then
  ok 'steps.sh without stepprof.sh beside it names the missing file and fails'
else bad 'missing stepprof.sh' "rc=$rc $out"; fi

shtest_end

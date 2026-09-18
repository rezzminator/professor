#!/usr/bin/env bash
# Regression test for scripts/test-sweep.sh's --plan dry run (spec: "measured
# by its report, not a unit test; its dry-run (--plan) prints the matrix and
# is asserted in a shell test under pfm/scripts/"). arch-check.sh, the sibling
# this was asked to mirror, has no dedicated shell test anywhere in this
# repo's git history (see the header of test-timing_test.sh for the check);
# this file follows that same file's bash assert_-style harness instead.
#
# Deliberately does NOT invoke `go test` or docker — that is the RND run
# itself (docs/dev/testing/concurrency-sweep.md), not a unit test.
set -uo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="$ROOT/scripts/test-sweep.sh"
T="$(mktemp -d "${TMPDIR:-/tmp}/pfm-test-sweep-test.XXXXXX")"
cleanup() { rm -rf -- "$T"; }
trap cleanup EXIT

PASS=0
FAIL=0
ok()  { printf 'PASS  %s\n' "$1"; PASS=$((PASS+1)); }
bad() { printf 'FAIL  %s\n' "$1" >&2; shift; [ $# -gt 0 ] && printf '      %s\n' "$@" >&2; FAIL=$((FAIL+1)); }

# ---- 1: --plan names both sweeps, the cache warm-up, and the rep count -----

out="$T/plan.out"
if bash "$SUT" --plan >"$out" 2>&1; then
  if grep -q 'cache-warmup' "$out" && grep -q 'p-sweep' "$out" && grep -q 'parallel-sweep' "$out" \
    && grep -qE '\bp in \{1 2 4 8( [0-9]+)?\}' "$out" \
    && grep -q 'parallel in {1 4 8 16}' "$out" \
    && grep -q '3x' "$out"; then
    ok "plan: names cache-warmup + p-sweep + parallel-sweep + rep count"
  else
    bad "plan: expected matrix text missing" "$(cat "$out")"
  fi
else
  bad "plan: expected exit 0" "$(cat "$out")"
fi

# ---- 2: --plan never shells out to $GOCMD or docker ------------------------

out2="$T/plan-nopath.out"
if env GOCMD="$T/no-such-go" bash "$SUT" --plan >"$out2" 2>&1; then
  ok "plan: succeeds with GOCMD pointed at a nonexistent binary (no execution happens in --plan)"
else
  bad "plan: should not need a working \$GOCMD" "$(cat "$out2")"
fi

# ---- 3: invalid repetition counts fail before a run can look green --------

out_reps="$T/plan-bad-reps.out"
if bash "$SUT" --plan --reps 0 >"$out_reps" 2>&1; then
  bad "plan-reps: expected exit 2 for --reps 0" "$(cat "$out_reps")"
else
  rc=$?
  if [ "$rc" -eq 2 ] && grep -q 'ERROR.*--reps' "$out_reps"; then
    ok "plan-reps: rejects zero repetition count with an explicit error"
  else
    bad "plan-reps: rc=$rc" "$(cat "$out_reps")"
  fi
fi

# ---- 4: a real run refuses while a fence container is already live --------

fakebin="$T/fakebin"
mkdir -p "$fakebin"
cat > "$fakebin/docker" <<'SH'
#!/usr/bin/env bash
if [ "$1" = ps ]; then
  echo "infra-pfm-dev-run-deadbeef1234"
  exit 0
fi
exit 0
SH
chmod +x "$fakebin/docker"
out3="$T/refuse.out"
if env PATH="$fakebin:$PATH" GOCMD="$T/no-such-go" bash "$SUT" --run --label host --out "$T/sweep.tsv" >"$out3" 2>&1; then
  bad "run: expected refusal while a fence container is live" "$(cat "$out3")"
else
  if grep -qi 'REFUSING' "$out3"; then
    ok "run: refuses to start while docker ps shows a live pfm-dev container"
  else
    bad "run: refused for the wrong reason (or not at all)" "$(cat "$out3")"
  fi
fi

# ---- 5: run consumes its delimiter, shows test output, and propagates fail -

fakego="$T/fake-go"
cat > "$fakego" <<'SH'
#!/usr/bin/env bash
case "${1:-}" in
  clean) exit 0 ;;
  test) echo SWEEP_FIXTURE_OUTPUT; exit 7 ;;
  *) echo "unexpected fake-go args: $*" >&2; exit 9 ;;
esac
SH
chmod +x "$fakego"
cat > "$fakebin/nproc" <<'SH'
#!/usr/bin/env bash
echo 1
SH
chmod +x "$fakebin/nproc"
cat > "$fakebin/docker" <<'SH'
#!/usr/bin/env bash
exit 0
SH
chmod +x "$fakebin/docker"
out5="$T/run-fail.out"
if env PATH="$fakebin:$PATH" GOCMD="$fakego" bash "$SUT" --run --label host --reps 1 --out "$T/run-fail.tsv" >"$out5" 2>&1; then
  bad "run-failing: expected non-zero exit" "$(cat "$out5")"
else
  rc=$?
  if [ "$rc" -eq 1 ] && grep -q 'SWEEP_FIXTURE_OUTPUT' "$out5" && grep -q 'FAIL' "$out5"; then
    ok "run-failing: command output is visible and test failure propagates"
  else
    bad "run-failing: rc=$rc or output/status missing" "$(cat "$out5")"
  fi
fi

# ---- 6: a passing run records every leg and its actual arguments ------------

passgo="$T/pass-go"
argslog="$T/pass-go.args"
cat > "$passgo" <<'SH'
#!/usr/bin/env bash
echo "$*" >> "$SWEEP_ARGS_LOG"
case "${1:-}" in
  clean) exit 0 ;;
  test) echo SWEEP_PASS_OUTPUT; exit 0 ;;
  *) exit 9 ;;
esac
SH
chmod +x "$passgo"
out6="$T/run-pass.out"
if env PATH="$fakebin:$PATH" GOCMD="$passgo" SWEEP_ARGS_LOG="$argslog" bash "$SUT" --run --label host --reps 1 --out "$T/run-pass.tsv" >"$out6" 2>&1; then
  rows="$(tail -n +2 "$T/run-pass.tsv" | wc -l | tr -d ' ')"
  if [ "$rows" -eq 10 ] && grep -q 'SWEEP_PASS_OUTPUT' "$out6" \
    && grep -q -- '-shuffle=on' "$argslog" && grep -q -- '-p 1 -parallel 1' "$argslog"; then
    ok "run-passing: records warm-up, both sweeps, shuffle, and measured flags"
  else
    bad "run-passing: expected complete 10-row run and arguments" "$(cat "$out6")" "$(cat "$T/run-pass.tsv")" "$(cat "$argslog")"
  fi
else
  bad "run-passing: expected exit 0" "$(cat "$out6")"
fi

# ---- 7: report derives independent knees and accepts fence-only input ------

report_tsv="$T/report.tsv"
{
  printf 'phase\tp\tparallel\trep\twall_s\tcpu_s\tstatus\n'
  printf 'cache-warmup\t-\t-\t1\t12.000\t4.000\tPASS\n'
  for rep in 1 2 3; do
    printf 'p-sweep\t1\t8\t%s\t10.000\t4.000\tPASS\n' "$rep"
    printf 'p-sweep\t2\t8\t%s\t5.000\t3.000\tPASS\n' "$rep"
    printf 'p-sweep\t4\t8\t%s\t5.000\t3.000\tPASS\n' "$rep"
    printf 'p-sweep\t8\t8\t%s\t5.000\t3.000\tPASS\n' "$rep"
    printf 'parallel-sweep\t2\t1\t%s\t5.000\t3.000\tPASS\n' "$rep"
    printf 'parallel-sweep\t2\t4\t%s\t4.000\t3.000\tPASS\n' "$rep"
    printf 'parallel-sweep\t2\t8\t%s\t4.000\t3.000\tPASS\n' "$rep"
    printf 'parallel-sweep\t2\t16\t%s\t4.000\t3.000\tPASS\n' "$rep"
    printf 'shuffle\t2\t4\t%s\t4.000\t3.000\tPASS\n' "$rep"
  done
} > "$report_tsv"
report_doc="$T/report.md"
report_make="$T/Makefile"
printf 'TESTFLAGS ?= -p 1 -parallel 1\n' > "$report_make"
out6="$T/report.out"
if bash "$SUT" --report "$report_tsv" --doc "$report_doc" --makefile "$report_make" >"$out6" 2>&1; then
  if grep -q 'measurement: not run' "$report_doc" && grep -q 'Knee.*p=2' "$report_doc" \
    && grep -q 'Parallel knee.*parallel=4' "$report_doc" \
    && grep -q 'TESTFLAGS ?= -p 2 -parallel 4' "$report_make"; then
    ok "report-knees: fence-only report pins p knee and independent parallel knee"
  else
    bad "report-knees: report or pin is wrong" "$(cat "$report_doc" 2>&1)" "$(cat "$report_make")"
  fi
else
  bad "report-knees: expected exit 0" "$(cat "$out6")"
fi

# ---- 8: --wait-quiet gates each capture and records both edges -------------

quietbin="$T/quiet-bin"
mkdir -p "$quietbin"
quiet_state="$T/uptime.state"
quiet_sleep_log="$T/sleep.log"
cat > "$quietbin/uptime" <<'SH'
state="${UPTIME_STATE:?}"
count=0
if [ -f "$state" ]; then count="$(cat "$state")"; fi
printf '%s\n' "$((count + 1))" > "$state"
if [ "$count" -eq 0 ]; then
  echo '00:00 up 1 min, 1 user, load average: 5.00, 0.00, 0.00'
else
  echo '00:00 up 1 min, 1 user, load average: 1.00, 0.00, 0.00'
fi
SH
chmod +x "$quietbin/uptime"
cat > "$quietbin/sleep" <<'SH'
echo "$*" >> "${SLEEP_LOG:?}"
exit 0
SH
chmod +x "$quietbin/sleep"
quiet_tsv="$T/run-quiet.tsv"
quiet_out="$T/run-quiet.out"
if env PATH="$quietbin:$fakebin:$PATH" UPTIME_STATE="$quiet_state" SLEEP_LOG="$quiet_sleep_log" GOCMD="$passgo" SWEEP_ARGS_LOG="$argslog" bash "$SUT" --run --label host --reps 1 --wait-quiet --out "$quiet_tsv" >"$quiet_out" 2>&1; then
  quiet_edges="$(tail -n +2 "$quiet_tsv.load.log" | wc -l | tr -d ' ')"
  quiet_sleeps="$(wc -l < "$quiet_sleep_log" | tr -d ' ')"
  if [ "$quiet_edges" -eq 20 ] && [ "$quiet_sleeps" -ge 1 ] && grep -qx '60' "$quiet_sleep_log" \
    && grep -q 'before' "$quiet_tsv.load.log" && grep -q 'after' "$quiet_tsv.load.log" \
    && grep -q 'cache-warmup' "$quiet_tsv.load.log" && grep -q 'shuffle' "$quiet_tsv.load.log"; then
    ok "wait-quiet: high load waits outside timing and records before/after evidence"
  else
    bad "wait-quiet: missing cooldown or edge evidence" "$(cat "$quiet_out")" "$(cat "$quiet_tsv.load.log" 2>&1)" "$(cat "$quiet_sleep_log" 2>&1)"
  fi
else
  bad "wait-quiet: expected exit 0" "$(cat "$quiet_out")"
fi

# Sustained load warns and proceeds after the bounded wait. The production
# default is five 60-second sleeps; the fixture lowers that bound to one so
# this regression remains instant while exercising the same branch.
cappedbin="$T/capped-quiet-bin"
mkdir -p "$cappedbin"
capped_state="$T/capped-uptime.state"
capped_sleep_log="$T/capped-sleep.log"
cat > "$cappedbin/uptime" <<'SH'
state="${UPTIME_STATE:?}"
count=0
if [ -f "$state" ]; then count="$(cat "$state")"; fi
printf '%s\n' "$((count + 1))" > "$state"
if [ "$count" -lt 2 ]; then
  echo '00:00 up 1 min, 1 user, load average: 5.00, 0.00, 0.00'
else
  echo '00:00 up 1 min, 1 user, load average: 1.00, 0.00, 0.00'
fi
SH
chmod +x "$cappedbin/uptime"
cat > "$cappedbin/sleep" <<'SH'
echo "$*" >> "${SLEEP_LOG:?}"
exit 0
SH
chmod +x "$cappedbin/sleep"
capped_tsv="$T/run-quiet-capped.tsv"
capped_out="$T/run-quiet-capped.out"
if env PATH="$cappedbin:$fakebin:$PATH" UPTIME_STATE="$capped_state" SLEEP_LOG="$capped_sleep_log" \
    SWEEP_QUIET_MAX_WAITS=1 GOCMD="$passgo" SWEEP_ARGS_LOG="$argslog" \
    bash "$SUT" --run --label host --reps 1 --wait-quiet --out "$capped_tsv" >"$capped_out" 2>&1; then
  capped_sleeps="$(wc -l < "$capped_sleep_log" | tr -d ' ')"
  if [ "$capped_sleeps" -eq 1 ] && grep -q 'WARNING.*proceeding' "$capped_out" \
      && awk -F'\t' '$2=="before" && $7+0>=4 {found=1} END{exit !found}' "$capped_tsv.load.log"; then
    ok "wait-quiet-cap: sustained load warns and proceeds after the configured bound"
  else
    bad "wait-quiet-cap: expected one wait, warning, and high-load evidence" \
      "$(cat "$capped_out")" "$(cat "$capped_tsv.load.log" 2>&1)" "$(cat "$capped_sleep_log" 2>&1)"
  fi
else
  bad "wait-quiet-cap: bounded high load must not block the run" "$(cat "$capped_out")"
fi

# A failed test command still gets an after edge in the evidence log.
failquietgo="$T/fail-quiet-go"
cat > "$failquietgo" <<'SH'
case "${1:-}" in
  clean) exit 0 ;;
  test) echo SWEEP_QUIET_FAIL_OUTPUT; exit 7 ;;
  *) exit 9 ;;
esac
SH
chmod +x "$failquietgo"
failquiet_tsv="$T/run-quiet-fail.tsv"
failquiet_out="$T/run-quiet-fail.out"
if env PATH="$quietbin:$fakebin:$PATH" UPTIME_STATE="$quiet_state" SLEEP_LOG="$quiet_sleep_log" GOCMD="$failquietgo" bash "$SUT" --run --label host --reps 1 --wait-quiet --out "$failquiet_tsv" >"$failquiet_out" 2>&1; then
  bad "wait-quiet-failure: expected test failure to propagate" "$(cat "$failquiet_out")"
else
  rc=$?
  failquiet_edges="$(tail -n +2 "$failquiet_tsv.load.log" | wc -l | tr -d ' ')"
  if [ "$rc" -eq 1 ] && [ "$failquiet_edges" -eq 2 ] && grep -q 'after' "$failquiet_tsv.load.log"; then
    ok "wait-quiet-failure: failed capture still records its after edge"
  else
    bad "wait-quiet-failure: rc=$rc or after evidence missing" "$(cat "$failquiet_out")" "$(cat "$failquiet_tsv.load.log" 2>&1)"
  fi
fi

# An unreadable load probe fails immediately rather than sleeping forever.
badquietbin="$T/bad-quiet-bin"
mkdir -p "$badquietbin"
cat > "$badquietbin/uptime" <<'SH'
echo uptime-output-has-no-load-field
exit 0
SH
chmod +x "$badquietbin/uptime"
badquiet_tsv="$T/run-quiet-bad.tsv"
badquiet_out="$T/run-quiet-bad.out"
if env PATH="$badquietbin:$fakebin:$PATH" GOCMD="$passgo" bash "$SUT" --run --label host --reps 1 --wait-quiet --out "$badquiet_tsv" >"$badquiet_out" 2>&1; then
  bad "wait-quiet-invalid-load: expected non-zero exit" "$(cat "$badquiet_out")"
else
  rc=$?
  if [ "$rc" -eq 2 ] && grep -q 'SWEEP-ERROR.*load' "$badquiet_out"; then
    ok "wait-quiet-invalid-load: malformed load is an explicit bounded error"
  else
    bad "wait-quiet-invalid-load: rc=$rc or error missing" "$(cat "$badquiet_out")"
  fi
fi

# ---- 9: a second sweep at a different p is an invalid report ---------------

wrong_p_tsv="$T/wrong-p.tsv"
awk -F'\t' 'BEGIN{OFS="\t"} {if ($1=="parallel-sweep" && $2==2) $2=4; print}' "$report_tsv" > "$wrong_p_tsv"
wrong_p_doc="$T/wrong-p.md"
wrong_p_make="$T/wrong-p.Makefile"
printf 'TESTFLAGS ?= -p 1 -parallel 1\n' > "$wrong_p_make"
cp "$wrong_p_make" "$T/wrong-p.before"
out_wrong_p="$T/report-wrong-p.out"
if bash "$SUT" --report "$wrong_p_tsv" --doc "$wrong_p_doc" --makefile "$wrong_p_make" >"$out_wrong_p" 2>&1; then
  bad "report-second-p: expected non-zero exit" "$(cat "$out_wrong_p")"
else
  rc=$?
  if [ "$rc" -eq 2 ] && grep -q 'second sweep was measured at p=4' "$out_wrong_p" \
      && cmp -s "$T/wrong-p.before" "$wrong_p_make" && [ ! -e "$wrong_p_doc" ]; then
    ok "report-second-p: second sweep must use the recomputed p knee"
  else
    bad "report-second-p: rc=$rc or target mutated" "$(cat "$out_wrong_p")"
  fi
fi

# ---- 10: --json retains every timed stdout stream at exact paths -----------

json_dir="$T/raw-json"
out_json="$T/run-json.out"
if env PATH="$fakebin:$PATH" GOCMD="$passgo" SWEEP_ARGS_LOG="$argslog" bash "$SUT" \
    --run --label host --reps 1 --json "$json_dir" --out "$T/run-json.tsv" >"$out_json" 2>&1; then
  json_count="$(find "$json_dir" -type f -name '*.json' | wc -l | tr -d ' ')"
  parallel_json="$(find "$json_dir" -type f -name 'parallel-sweep-*.json' | head -n 1)"
  if [ "$json_count" -eq 10 ] && [ -n "$parallel_json" ]; then
    ok "run-json: retains each timed test stdout stream under the requested directory"
  else
    bad "run-json: expected ten exact JSON capture paths" "$(cat "$out_json")" "$(find "$json_dir" -type f -print 2>&1)"
  fi
else
  bad "run-json: expected exit 0" "$(cat "$out_json")"
fi

# ---- 11: empty report is an error and cannot mutate its targets -------------

empty_tsv="$T/empty.tsv"
: > "$empty_tsv"
empty_doc="$T/empty.md"
empty_make="$T/empty.Makefile"
printf 'TESTFLAGS ?= -p 1 -parallel 1\n' > "$empty_make"
cp "$empty_make" "$T/empty.before"
out7="$T/report-empty.out"
if bash "$SUT" --report "$empty_tsv" --doc "$empty_doc" --makefile "$empty_make" >"$out7" 2>&1; then
  bad "report-empty: expected non-zero exit" "$(cat "$out7")"
else
  rc=$?
  if [ "$rc" -eq 2 ] && grep -q 'ERROR' "$out7" && cmp -s "$empty_make" "$T/empty.before" && [ ! -e "$empty_doc" ]; then
    ok "report-empty: malformed input is an explicit error with no target mutation"
  else
    bad "report-empty: rc=$rc or target mutated" "$(cat "$out7")" "$(diff -u "$T/empty.before" "$empty_make" || true)"
  fi
fi

# ---- 12: a failed raw run cannot be reported or pinned ---------------------

failed_tsv="$T/failed.tsv"
awk -F'\t' 'BEGIN{OFS="\t"} {if ($1=="parallel-sweep" && $3==4) $7="FAIL"; print}' "$report_tsv" > "$failed_tsv"
failed_doc="$T/failed.md"
failed_make="$T/failed.Makefile"
printf 'TESTFLAGS ?= -p 1 -parallel 1\n' > "$failed_make"
cp "$failed_make" "$T/failed.before"
out8="$T/report-failed.out"
if bash "$SUT" --report "$failed_tsv" --doc "$failed_doc" --makefile "$failed_make" >"$out8" 2>&1; then
  bad "report-failed: expected non-zero exit" "$(cat "$out8")"
else
  rc=$?
if [ "$rc" -eq 2 ] && grep -q 'ERROR' "$out8" && cmp -s "$failed_make" "$T/failed.before" && [ ! -e "$failed_doc" ]; then
    ok "report-failed: failed raw rows cannot produce a report or pin"
  else
    bad "report-failed: rc=$rc or target mutated" "$(cat "$out8")" "$(diff -u "$T/failed.before" "$failed_make" || true)"
  fi
fi

# ---- 13: Docker probe failures are explicit on preflight -------------------

probe_bin="$T/probe-bin"
mkdir -p "$probe_bin"
cat > "$probe_bin/docker" <<'SH'
#!/usr/bin/env bash
echo docker-daemon-unreachable >&2
exit 42
SH
chmod +x "$probe_bin/docker"
out9="$T/preflight-docker.out"
if env PATH="$probe_bin:$PATH" bash "$SUT" --preflight >"$out9" 2>&1; then
  bad "preflight-docker: expected non-zero exit" "$(cat "$out9")"
else
  rc=$?
  if [ "$rc" -eq 2 ] && grep -q 'SWEEP-ERROR: docker ps probe failed' "$out9"; then
    ok "preflight-docker: Docker probe failure is explicit"
  else
    bad "preflight-docker: rc=$rc or probe error missing" "$(cat "$out9")"
  fi
fi

printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]

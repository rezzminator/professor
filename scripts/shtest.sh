#!/usr/bin/env bash
# The one harness every fixture-driven shell test in this repo sources: a
# trap-cleaned scratch dir $T, the ok/bad counters, and shtest_end, the verdict.
#   SHTEST_TAG=my-suite; source …/scripts/shtest.sh; …; shtest_end
# shtest_unset_gate_env clears the gate and profiler knobs a suite can inherit.
# BROKEN STATE: an unset SHTEST_TAG or a failed mktemp exits 2 with a named
# line before any assertion runs — a suite never reports "0 failed" from a
# harness that did not start.
[ -n "${SHTEST_TAG:-}" ] || { echo "shtest: SHTEST_TAG unset — the harness did not start" >&2; exit 2; }
T="$(mktemp -d "${TMPDIR:-/tmp}/${SHTEST_TAG}.XXXXXX")" || { echo "shtest: mktemp failed for $SHTEST_TAG" >&2; exit 2; }
# The EXIT trap removes $T only in the shell that sourced this file: a command forked from
# it and TERMed before its exec runs this trap too, and must not delete the suite's scratch.
SHTEST_PID="${BASHPID:-$$}"
trap 'if [ "${BASHPID:-$$}" = "$SHTEST_PID" ]; then rm -rf -- "$T"; fi' EXIT
PASS=0
FAIL=0
ok() { printf 'PASS  %s\n' "$1"; PASS=$((PASS + 1)); }
bad() { printf 'FAIL  %s\n' "$1" >&2; shift; [ $# -gt 0 ] && printf '      %s\n' "$@" >&2; FAIL=$((FAIL + 1)); }
shtest_compose_config() { # shtest_compose_config <build-context>
  cat <<EOF
services:
  pfm-dev:
    build:
      context: $1
      dockerfile: pfm-dev.Dockerfile
      target: pfm-dev
      labels:
        pfm.fence.inputs: unkeyed
    image: professor-pfm-dev
EOF
}
# A suite that asserts on gate or profiler behaviour starts from none of the knobs the gate's own
# stepprof wrapper exports into the whole step tree this suite runs in. BASH_ENV is never unset:
# it carries the ERR trace of every child, and a suite's own commands keep it.
shtest_unset_gate_env() {
  unset PFM_TEST_ARTIFACT_DIR PFM_TEST_PROFILE PFM_TEST_PROFILE_PARENT PFM_TEST_DEADLINE_EPOCH \
    PFM_TEST_TIMING_DIR STEPPROF_TRACE STEPPROF_GRACE_TICKS STEPPROF_PSI_DIR STEPS_BOUND_S \
    STEPS_JOBS STEPS_HEAVY_JOBS SAMPLER_INTERVAL_S SAMPLER_SPIN_ITERS SAMPLER_CGROUP_DIR \
    SAMPLER_PROC_DIR PFM_GATE_FIXTURES PFM_GATE_HISTORY_DIR
}
shtest_end() { printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"; [ "$FAIL" -eq 0 ]; }

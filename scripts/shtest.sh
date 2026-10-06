#!/usr/bin/env bash
# The one harness every fixture-driven shell test in this repo sources: a
# trap-cleaned scratch dir $T, the ok/bad counters, and shtest_end, the verdict.
#   SHTEST_TAG=my-suite; source …/scripts/shtest.sh; …; shtest_end
# shtest_unset_gate_env clears the gate and profiler knobs a suite can inherit.
# shtest_clean_also <path>... makes the EXIT trap remove those paths too, beside $T and every earlier call's; a suite never sets its own trap.
# shtest_probe_trap_guard <path> proves the EXIT trap's PID guard, <path> standing in for a shtest_clean_also path.
# shtest_isolate_host points HOME at $T/home and git at no host config or inherited repository, so a fixture commit never reads the host's.
# BROKEN STATE: an unset SHTEST_TAG or a failed mktemp exits 2 with a named
# line before any assertion runs — a suite never reports "0 failed" from a
# harness that did not start.
[ -n "${SHTEST_TAG:-}" ] || { echo "shtest: SHTEST_TAG unset — the harness did not start" >&2; exit 2; }
T="$(mktemp -d "${TMPDIR:-/tmp}/${SHTEST_TAG}.XXXXXX")" || { echo "shtest: mktemp failed for $SHTEST_TAG" >&2; exit 2; }
# The EXIT trap removes $T (and the paths shtest_clean_also names) only in the shell that sourced this
# file: a command forked from it and TERMed before its exec runs this trap too, and must not delete the
# suite's scratch. $T is read when the trap runs; the extra paths are quoted into it when it is installed.
SHTEST_PID="${BASHPID:-$$}"
SHTEST_CLEAN_EXTRA=''
shtest_clean_also() { # shtest_clean_also <path>...: adds to the paths of every earlier call
  local p
  for p in "$@"; do SHTEST_CLEAN_EXTRA="$SHTEST_CLEAN_EXTRA $(printf '%q' "$p")"; done
  trap 'if [ "${BASHPID:-$$}" = "$SHTEST_PID" ]; then rm -rf -- "$T"'"$SHTEST_CLEAN_EXTRA"'; fi' EXIT
}
shtest_clean_also
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
    PFM_TEST_TIMING_DIR PFM_TEST_TIMING_HOST PFM_TEST_RUN_NOTE TESTFLAGS STEPPROF_TRACE STEPPROF_GRACE_TICKS \
    STEPPROF_PSI_DIR STEPS_BOUND_S STEPS_JOBS STEPS_HEAVY_JOBS SAMPLER_INTERVAL_S SAMPLER_SPIN_ITERS \
    SAMPLER_CGROUP_DIR SAMPLER_PROC_DIR PFM_GATE_FIXTURES PFM_GATE_HISTORY_DIR
}
# A suite that runs git or reads $HOME starts from none of the host's: its fixture commits and worktrees see
# no signing key, hooksPath or identity of the machine it runs on.
shtest_isolate_host() {
  mkdir -p "$T/home" || { echo "shtest: mkdir $T/home failed for $SHTEST_TAG — host isolation did not start" >&2; exit 2; }
  export HOME="$T/home" GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
  # a git hook's GIT_DIR, an outer `git -c` (GIT_CONFIG_PARAMETERS, GIT_CONFIG_COUNT) and an exported template,
  # namespace or alternate object store reach no fixture either
  unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE GIT_COMMON_DIR GIT_OBJECT_DIRECTORY GIT_CONFIG_PARAMETERS GIT_CONFIG_COUNT \
    GIT_TEMPLATE_DIR GIT_NAMESPACE GIT_ALTERNATE_OBJECT_DIRECTORIES
}
shtest_end() { printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"; [ "$FAIL" -eq 0 ]; }
# shtest_probe_trap_guard <path>: the suite's EXIT trap, run by a forked shell, must leave $T and <path> alone.
# <path> is baked into the trap by shtest_clean_also, so the probe swaps it for a sacrificial directory: a
# regressed guard deletes only probe paths, never the live suite's. An empty trap FAILs the probe, never PASSes it.
shtest_probe_trap_guard() {
  local trapf="$T/exit-trap"
  trap -p EXIT | sed "s|$1|$T/guard-extra|" > "$trapf"
  mkdir -p "$T/guard-probe" "$T/guard-extra"
  T="$T/guard-probe" SHTEST_PID="$SHTEST_PID" bash -c 'eval "$(cat "$1")"; exit 0' _ "$trapf"
  if [ ! -s "$trapf" ]; then bad "trap: no EXIT trap installed"
  elif [ -d "$T/guard-probe" ] && [ -d "$T/guard-extra" ]; then ok "trap: a forked shell running the EXIT trap leaves the scratch alone"
  else bad "trap: a forked shell running the EXIT trap removed the scratch"; fi
}

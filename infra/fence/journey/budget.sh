#!/usr/bin/env bash
# budget.sh — the journey tree's budgets, judged outside the test, over the artifacts a run wrote:
# - time: the root row of steps.tsv (the harness records the tree's wall time there, first step start to
#   last step end) fails a tree that took longer than BUDGET_SECS. Its rule: the slowest of three
#   consecutive green local trees (94.4 s, at load 8) times 3.5, CI's Linux leg against local time in
#   phase 1, times 1.5 headroom, rounded up to the next 100 s. Remeasure when the tree grows.
# - CPU: every row of cpu.tsv (pfm/journey fleet_whole_test.go recordCPU: the tree's elapsed time at a
#   look, a pfm process's pid, the CPU seconds it used, its command) fails a process that used more than
#   CPU_SHARE of the tree's elapsed time: an indicator, a blinker or a daemon is spinning.
#   bash infra/fence/journey/budget.sh {artifacts dir}
# Prints `journey: budget: {n} looks at pfm's CPU time, none over 20% of the tree's` and
# `journey: budget: the tree took {s} s, allowed 500 s` and exits 0 within both; a `FAIL` line per
# process over its share and for a tree over its time, and exits 1.
# BROKEN STATE: `journey: budget: ERROR {why}` and exit 2 when steps.tsv or cpu.tsv is missing, steps.tsv
# holds no root row, cpu.tsv has no header (an empty one included), or a cpu.tsv row is malformed: a run
# it cannot read never passes.
set -euo pipefail
BUDGET_SECS=500
CPU_SHARE=0.20
[ $# -eq 1 ] || { echo "journey: budget: ERROR usage: budget.sh {artifacts dir}" >&2; exit 2; }
steps="$1/steps.tsv" cpu="$1/cpu.tsv"
[ -r "$steps" ] || { echo "journey: budget: ERROR cannot read $steps" >&2; exit 2; }
[ -r "$cpu" ] || { echo "journey: budget: ERROR cannot read $cpu" >&2; exit 2; }
# The root's path is the one with no slash; its wall_s is the third column.
wall="$(awk -F'\t' 'NR > 1 && $1 !~ /\// { print $3; exit }' "$steps")"
[[ "$wall" =~ ^[0-9]+(\.[0-9]+)?$ ]] || { echo "journey: budget: ERROR $steps holds no root row with a wall_s" >&2; exit 2; }
# One verdict line per cpu.tsv row past its header: ERROR for a malformed one, FAIL for one over its share.
judged="$(awk -F'\t' -v share="$CPU_SHARE" '
  NR == 1 { if ($0 != "elapsed_s\tpid\tcpu_s\tcommand") { print "ERROR " FILENAME " has no CPU table header"; exit } next }
  NF < 4 || $1 !~ /^[0-9]+(\.[0-9]+)?$/ || $2 !~ /^[0-9]+$/ || $3 !~ /^[0-9]+(\.[0-9]+)?$/ {
    print "ERROR " FILENAME " row " NR " is not elapsed_s, pid, cpu_s, command: " $0; exit
  }
  $3 > share * $1 {
    printf "FAIL pfm pid %s used %s s of CPU in the tree'"'"'s %s s (over %d%%): %s\n", $2, $3, $1, share * 100, $4
  }
  END { if (NR == 0) print "ERROR " FILENAME " has no CPU table header"; else print "LOOKS " NR - 1 }' "$cpu")"
if grep -q '^ERROR ' <<<"$judged"; then
  sed -n 's/^ERROR /journey: budget: ERROR /p' <<<"$judged" >&2
  exit 2
fi
rc=0
if grep -q '^FAIL ' <<<"$judged"; then
  sed -n 's/^FAIL /journey: budget: FAIL /p' <<<"$judged" >&2
  rc=1
else
  echo "journey: budget: $(sed -n 's/^LOOKS //p' <<<"$judged") looks at pfm's CPU time, none over ${CPU_SHARE#0.}% of the tree's"
fi
if awk -v wall="$wall" -v budget="$BUDGET_SECS" 'BEGIN { exit !(wall > budget) }'; then
  echo "journey: budget: FAIL the tree took $wall s, allowed $BUDGET_SECS s" >&2
  exit 1
fi
echo "journey: budget: the tree took $wall s, allowed $BUDGET_SECS s"
exit "$rc"

#!/usr/bin/env bash
# journey-budget_test.sh — infra/fence/journey/budget.sh over steps.tsv and cpu.tsv fixtures: a tree at or
# under the 500 s budget passes, one a millisecond over fails; a pfm process at 20% of the tree's elapsed
# time passes, one a millisecond of CPU over fails naming its pid and command; a steps.tsv or cpu.tsv it
# cannot read, or a malformed cpu.tsv row, is an error, never a pass.
set -uo pipefail
SHTEST_TAG=journey-budget-test
# shellcheck source=../../../scripts/shtest.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../scripts/shtest.sh"
SUT="$(cd "$(dirname -- "${BASH_SOURCE[0]}")/../journey" && pwd)/budget.sh"
CPU_HEADER=$'elapsed_s\tpid\tcpu_s\tcommand'

# steps <dir> <root wall_s>: a steps.tsv as the harness writes it, a child row ahead of the root's, and a
# cpu.tsv of one look at one pfm process well within its share.
steps() {
  mkdir -p "$1"
  printf 'path\tverdict\twall_s\town_s\nroot/a\tPASS\t900.000\t900.000\nroot\tPASS\t%s\t1.000\n' "$2" >"$1/steps.tsv"
  printf '%s\n100.000\t41\t1.000\tpfm ls\n' "$CPU_HEADER" >"$1/cpu.tsv"
}

# check <name> <want rc> <want output> <dir>: budget.sh's exit status and its whole output.
check() {
  local out rc=0
  out="$(bash "$SUT" "$4" 2>&1)" || rc=$?
  if [ "$rc" = "$2" ] && [ "$out" = "$3" ]; then ok "$1"; else bad "$1" "want rc $2: $3" "got rc $rc: $out"; fi
}

within="journey: budget: 1 looks at pfm's CPU time, none over 20% of the tree's"
for row in "499.999|0|$within"$'\n''journey: budget: the tree took 499.999 s, allowed 500 s' \
  "500.000|0|$within"$'\n''journey: budget: the tree took 500.000 s, allowed 500 s' \
  "500.001|1|$within"$'\n''journey: budget: FAIL the tree took 500.001 s, allowed 500 s'; do
  IFS='|' read -r -d '' wall rc out <<<"$row"
  out="${out%$'\n'}"
  steps "$T/$wall" "$wall"
  check "a tree of $wall s" "$rc" "$out" "$T/$wall"
done

# A process at exactly its share passes; one a millisecond of CPU over fails by pid and command, every
# look of it, each judged against the tree's elapsed time at that look.
steps "$T/share" 10.000
printf '%s\n100.000\t42\t20.000\tpfm statusline\n50.000\t43\t10.001\tpfm mcp serve --stdio\n' "$CPU_HEADER" >"$T/share/cpu.tsv"
check 'a process a millisecond over its share' 1 \
  "journey: budget: FAIL pfm pid 43 used 10.001 s of CPU in the tree's 50.000 s (over 20%): pfm mcp serve --stdio"$'\n'\
'journey: budget: the tree took 10.000 s, allowed 500 s' "$T/share"
printf '%s\n100.000\t42\t20.000\tpfm statusline\n' "$CPU_HEADER" >"$T/share/cpu.tsv"
check 'a process at its share' 0 "$within"$'\n''journey: budget: the tree took 10.000 s, allowed 500 s' "$T/share"
printf '%s\n' "$CPU_HEADER" >"$T/share/cpu.tsv"
check 'a run that never looked at the CPU' 0 \
  "journey: budget: 0 looks at pfm's CPU time, none over 20% of the tree's"$'\n''journey: budget: the tree took 10.000 s, allowed 500 s' "$T/share"

check 'no steps.tsv' 2 "journey: budget: ERROR cannot read $T/none/steps.tsv" "$T/none"
mkdir -p "$T/rootless" && printf 'path\tverdict\twall_s\town_s\nroot/a\tPASS\t1.000\t1.000\n' >"$T/rootless/steps.tsv"
printf '%s\n' "$CPU_HEADER" >"$T/rootless/cpu.tsv"
check 'a steps.tsv with no root row' 2 "journey: budget: ERROR $T/rootless/steps.tsv holds no root row with a wall_s" "$T/rootless"
steps "$T/nocpu" 10.000 && rm "$T/nocpu/cpu.tsv"
check 'no cpu.tsv' 2 "journey: budget: ERROR cannot read $T/nocpu/cpu.tsv" "$T/nocpu"
steps "$T/bad" 10.000 && printf '%s\nabc\t42\t1.000\tpfm ls\n' "$CPU_HEADER" >"$T/bad/cpu.tsv"
check 'a malformed cpu.tsv row' 2 \
  "journey: budget: ERROR $T/bad/cpu.tsv row 2 is not elapsed_s, pid, cpu_s, command: abc"$'\t'"42"$'\t'"1.000"$'\t'"pfm ls" "$T/bad"
printf 'pid\tcpu_s\n' >"$T/bad/cpu.tsv"
check 'a cpu.tsv with no header' 2 "journey: budget: ERROR $T/bad/cpu.tsv has no CPU table header" "$T/bad"
: >"$T/bad/cpu.tsv"
check 'an empty cpu.tsv' 2 "journey: budget: ERROR $T/bad/cpu.tsv has no CPU table header" "$T/bad"
shtest_end

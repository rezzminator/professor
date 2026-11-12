#!/usr/bin/env bash
# Fixture-driven tests for scripts/test-coverdiff.sh over a one-package module: a statement only the
# move test covers is move-only, one the journey or a unit test also covers is not; a red run is named
# and exits 1; every way it cannot look is COVERDIFF ERROR rc 2. Runs go test on the fixture (fence).
#   bash pfm/scripts/test-coverdiff_test.sh
set -uo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${SUT:-$ROOT/scripts/test-coverdiff.sh}"
SHTEST_TAG=pfm-test-coverdiff-test
# shellcheck source=/dev/null
source "$ROOT/../scripts/shtest.sh"
eq() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected: $2" "actual:   $3"; fi; }

module() { # module <dir> <move-test-body>: package f with A (three blocks), B and C; a unit and a move test
  mkdir -p "$1/f"
  printf 'module fx\n\ngo 1.22\n' > "$1/go.mod"
  cat > "$1/f/f.go" <<'GO'
package f

func A(x int) int {
	if x > 0 {
		return 1
	}
	return 0
}

func B() int { return 2 }

func C() int { return 3 }
GO
  cat > "$1/f/f_test.go" <<GO
package f

import "testing"

func TestUnit(t *testing.T) {
	if A(1) != 1 {
		t.Fatal("A(1)")
	}
}

func TestMove(t *testing.T) {
	_ = "state.db"
	$2
}
GO
}
module "$T/mod" 'if A(0) != 0 || B() != 2 { t.Fatal("A(0), B") }'
J=$T/journey.out
printf 'mode: atomic\nfx/f/f.go:10.16,10.26 1 3\nfx/f/f.go:12.16,12.26 1 0\n' > "$J"

out=$(bash "$SUT" "$J" "$T/out" "$T/mod" 2>&1); rc=$?
eq "the summary line: one package, the one statement only the move test holds" \
  "0|COVERDIFF 1 packages · move-only 1 statements on 1 lines · runs 2 (0 red, 0 unrecorded) · journey $J" "$rc|$out"
eq "report.tsv: B is the journey's, A's first two blocks the unit test's, so A's return 0 alone is move-only" \
  "$(printf 'package\tstatements\tjourney\tunit\tmove\tmove_only\tmove_only_lines\nfx/f\t5\t1\t2\t3\t1\t1')" \
  "$(cat "$T/out/report.tsv")"
eq "move-only.txt names the block by file and lines" "$(printf 'fx/f/f.go:7-7\t1')" "$(cat "$T/out/move-only.txt")"
eq "runs.tsv: one run per side, green, each with its profile" \
  "$(printf 'side\tdir\ttests\trc\tprofile\nmove\tf\t1\t0\tyes\nunit\tf\t1\t0\tyes')" "$(cat "$T/out/runs.tsv")"

module "$T/red" 't.Fatal("red")'
out=$(bash "$SUT" "$J" "$T/out-red" "$T/red" 2>&1); rc=$?
eq "a red run is counted and exits 1 after the report" \
  "1|COVERDIFF 1 packages · move-only 0 statements on 0 lines · runs 2 (1 red, 0 unrecorded) · journey $J" "$rc|$out"
eq "runs.tsv names the red run" "$(printf 'move\tf\t1\t1\tyes')" "$(awk -F '\t' '$1 == "move"' "$T/out-red/runs.tsv")"
out=$(bash "$SUT" "$J" "$T/out-red" "$T/mod" 2>&1); rc=$?
eq "an out-dir holding an earlier run is ERROR, its files never merged into this one" \
  "2|COVERDIFF ERROR $T/out-red/raw already holds a run — name an empty out-dir" "$rc|$out"

# Two files of one package, each with a move-only block on its line 3: two lines, not one (fx/f is the
# journey's package, the second).
mkdir -p "$T/two/g"
printf 'module fx\n\ngo 1.22\n' > "$T/two/go.mod"
printf 'package g\n\nfunc D() int { return 4 }\n' > "$T/two/g/d.go"
printf 'package g\n\nfunc E() int { return 5 }\n' > "$T/two/g/e.go"
printf 'package g\n\nimport "testing"\n\nfunc TestMove(t *testing.T) {\n\t_ = "state.db"\n\t_, _ = D(), E()\n}\n' \
  > "$T/two/g/g_test.go"
out=$(bash "$SUT" "$J" "$T/out-two" "$T/two" 2>&1); rc=$?
eq "move-only lines are counted per file: line 3 of d.go and of e.go are two lines" \
  "0|COVERDIFF 2 packages · move-only 2 statements on 2 lines · runs 1 (0 red, 0 unrecorded) · journey $J" "$rc|$out"

# A job whose shell is killed before it records itself is a named killed row, never a missing one
# (stdout only: xargs names the killed shell on stderr in its own words).
mkdir -p "$T/killer"
printf '#!/bin/sh\nkill -9 "$PPID"\n' > "$T/killer/go"; chmod +x "$T/killer/go"
out=$(PATH="$T/killer:$PATH" bash "$SUT" "$J" "$T/out-killed" "$T/mod" 2>/dev/null); rc=$?
eq "every job killed: no profile is ERROR" "2|COVERDIFF ERROR no run under $T/out-killed/raw wrote a profile" "$rc|$out"
eq "runs.tsv names each killed job" \
  "$(printf 'side\tdir\ttests\trc\tprofile\nmove\tf\t1\tkilled\tno\nunit\tf\t1\tkilled\tno')" "$(cat "$T/out-killed/runs.tsv")"

printf 'mode: atomic\nfx/f/f.go:10.16,10.26 1\n' > "$T/short.out"
out=$(bash "$SUT" "$T/short.out" "$T/out-short" "$T/mod" 2>&1); rc=$?
eq "a malformed profile line is ERROR naming it" \
  "2|COVERDIFF ERROR $T/short.out: not a coverage profile line: fx/f/f.go:10.16,10.26 1" "$rc|$out"

out=$(bash "$SUT" "$T/absent.out" "$T/out2" "$T/mod" 2>&1); rc=$?
eq "a missing journey profile is ERROR" "2|COVERDIFF ERROR journey profile $T/absent.out unreadable" "$rc|$out"
printf 'mode: atomic\n' > "$T/empty.out"
out=$(bash "$SUT" "$T/empty.out" "$T/out3" "$T/mod" 2>&1); rc=$?
eq "a journey profile with no block is ERROR" "2|COVERDIFF ERROR journey profile $T/empty.out holds no coverage block" "$rc|$out"
out=$(PFM_COVERDIFF_JOBS=0 bash "$SUT" "$J" "$T/out4" "$T/mod" 2>&1); rc=$?
eq "a bad job count is ERROR" "2|COVERDIFF ERROR PFM_COVERDIFF_JOBS must be a positive integer (got '0')" "$rc|$out"

shtest_end

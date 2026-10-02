#!/usr/bin/env bash
# profile-report.sh: one case per behaviour of the failure pointers (`failures`)
# and the run summary (`summary`: PROFILE block, profile.tsv, INDEX.txt). Fixture
# run directories live in $T; the judge is the real pfm/scripts/test-contention.sh
# over fixture resources.tsv files with a known quiet and a known contended phase.
# The threshold-judged cases read a fixture thresholds file in a fixture tree ($T/tree)
# and never pfm/.testcontention.yml, so a calibration of that file turns none of them
# red; the real file is read only by the real-tree case, which proves the production
# wiring finds and accepts it.
# PROFILE_REPORT_SUT points at a copy of the script (mutation runs).
set -uo pipefail
SHTEST_TAG=profile-report-test
# shellcheck source=../../../../scripts/shtest.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"
SUT="${PROFILE_REPORT_SUT:-$(dirname -- "${BASH_SOURCE[0]}")/../../profile-report.sh}"
[[ $SUT == /* ]] || SUT=$PWD/$SUT
JUDGE=$(cd "$(dirname -- "$SUT")/../.." && pwd)/pfm/scripts/test-contention.sh
REAL_SUT=$SUT
REAL_JUDGE=$JUDGE
# profile-report.sh hands its judge PFM=<script dir>/../../pfm, so the fixture tree holds a copy of the
# script, a copy of the real judge and the fixture thresholds; every sut, judged and JUDGE call below goes
# through it.
mkdir -p "$T/tree/infra/fence" "$T/tree/pfm/scripts"
cp "$SUT" "$T/tree/infra/fence/profile-report.sh"
cp "$JUDGE" "$T/tree/pfm/scripts/test-contention.sh"
printf '%s\n' 'tick_deficit_max: -0.05' 'cpu_psi_share_min: 0.20' 'spin_ratio_min: 1.5' 'run_delay_ratio_min: 0.5' 'min_samples: 4' \
  > "$T/tree/pfm/.testcontention.yml"
SUT=$T/tree/infra/fence/profile-report.sh
JUDGE=$T/tree/pfm/scripts/test-contention.sh
GO=github.com/rezzminator/professor/pfm
T0=1700000000

eq() { # eq <name> <expected> <actual>
  if [[ $3 == "$2" ]]; then ok "$1"; else bad "$1" "expected: $2" "actual:   $3"; fi
}
has() { # has <name> <needle> <haystack>
  if [[ $3 == *"$2"* ]]; then ok "$1"; else bad "$1" "missing: $2" "in: $3"; fi
}
ends() { # ends <name> <suffix> <line>
  if [[ $3 == *"$2" ]]; then ok "$1"; else bad "$1" "line should end: $2" "line:            $3"; fi
}
sut() { bash "$SUT" "$@"; }
run_sut() { # run_sut <args...>: OUT, ERR and RC of one call
  OUT=$(sut "$@" 2> "$T/stderr")
  RC=$?
  ERR=$(< "$T/stderr")
}
line_with() { grep -F -- "$2" <<< "$1" | head -n 1; } # the first line of <text> containing <needle>
cell() { # cell <profile.tsv> <step> <column>
  awk -F'\t' -v s="$2" -v c="$3" 'NR == 1 { for (i = 1; i <= NF; i++) if ($i == c) k = i; next } $1 == s { print $k }' "$1"
}
ep() { awk -v a="$T0" -v b="$1" 'BEGIN { printf "%.6f", a + b }'; } # an epoch <b> seconds after the fixture start
judged() { # judged <resources> <from> <to>: the judge's own answer as `attribution <WORD> (<evidence>)`
  local answer
  answer=$(bash "$JUDGE" window --resources "$1" --from "$2" --to "$3")
  printf 'attribution %s (%s)' "${answer%%$'\t'*}" "${answer#*$'\t'}"
}

# --- fixture builders ---------------------------------------------------------
# stream <file> <pkg>[:<test>]...: a go test -json stream of fail events, then one pass
stream() {
  local f=$1 e pkg test
  shift
  : > "$f"
  for e in "$@"; do
    pkg=${e%%:*}
    test=
    [[ $e == *:* ]] && test=${e#*:}
    if [[ -n $test ]]; then
      printf '{"Time":"2026-10-01T10:00:00Z","Action":"fail","Package":"%s","Test":"%s"}\n' "$pkg" "$test" >> "$f"
    else
      printf '{"Time":"2026-10-01T10:00:00Z","Action":"fail","Package":"%s","Elapsed":1.5}\n' "$pkg" >> "$f"
    fi
  done
  printf '{"Time":"2026-10-01T10:00:01Z","Action":"pass","Package":"%s/internal/other","Elapsed":0.1}\n' "$GO" >> "$f"
}
# proc <profile-dir> <name> <event> <exit> <helper_of> [bundle reason...]: a process directory
proc() {
  local d=$1/$2 r list=
  mkdir -p "$d"
  for r in "${@:6}"; do
    mkdir -p "$d/$r"
    echo "diagnosis of $2 at $r" > "$d/$r/DIAGNOSIS.txt"
    list+="\"$r\","
  done
  printf '{"package":"%s","pid":%s,"helper_of":"%s","event":"%s","exit_code":%s,"wall_s":2.5,"bundles":[%s],"run_delay_s":0.123}\n' \
    "${2%.*}" "${2##*.}" "$5" "$3" "$4" "${list%,}" > "$d/summary.json"
}
# mkres <file> <rows> <contended-from>: <rows> samples one second apart from $T0, 4 cpus; every
# counter grows linearly (cpu 2 s/s, io 10 MB/s read + 5 MB/s write); from row <contended-from> on
# the cgroup's cpu pressure grows 0.5 s/s. Spin is flat.
mkres() {
  mkdir -p "$(dirname -- "$1")"
  awk -v t0="$T0" -v n="$2" -v cf="$3" 'BEGIN {
    OFS = "\t"
    print "epoch_s", "uptime_s", "cg_cpu_s", "vm_busy_s", "cpus", "cg_io_read_mb", "cg_io_write_mb", "cg_mem_mb", "cg_mem_peak_mb", "cg_swap_mb", \
      "cg_psi_cpu_some_s", "cg_psi_io_some_s", "cg_psi_io_full_s", "cg_psi_mem_some_s", "cg_psi_mem_full_s", \
      "vm_psi_cpu_some_s", "vm_psi_io_some_s", "vm_psi_io_full_s", "vm_psi_mem_some_s", "vm_psi_mem_full_s", "spin_us", "load1"
    for (i = 0; i < n; i++) {
      pc = (i >= cf) ? 0.5 * (i - cf) : 0
      printf "%.6f\t%.2f\t%.6f\t%.6f\t4\t%.3f\t%.3f\t%d\t%d\t%d\t%.6f\t%.6f\t0.000000\t0.000000\t0.000000\t%.6f\t%.6f\t0.000000\t%.6f\t0.000000\t1000\t1.00\n", \
        t0 + i, 5000 + i, 2.0 * i, 2.0 * i + 10, 10.0 * i, 5.0 * i, 100 + i, 200 + i, i % 5, pc, 0.1 * i, 0.01 * i, 0.02 * i, 0.03 * i
    }
  }' > "$1"
}
# mkprof <run> <name> <ended> <rc> <start> <finish> <wall> <cpu> <err_records> <psi_cpu> <psi_io> <psi_mem>
mkprof() {
  mkdir -p "$1/steps"
  printf 'step\t%s\nended\t%s\nrc\t%s\nbound_s\t600\nstart_epoch\t%s\nfinish_epoch\t%s\nwall_s\t%s\nchildren_cpu\t0m1.000s user 0m0.500s sys\ncpu_s\t%s\ntrace\terr\nerr_records\t%s\npsi_cpu_some_s\t%s\npsi_cpu_full_s\t0.000000\npsi_io_some_s\t%s\npsi_io_full_s\t0.000000\npsi_memory_some_s\t%s\npsi_memory_full_s\t0.000000\n' \
    "$2" "$3" "$4" "$5" "$6" "$7" "$8" "$9" "${10}" "${11}" "${12}" > "$1/steps/$2.prof.tsv"
}
# mkgate <run> <steps-verdict|-> <budget-verdict|-> [<name> <verdict> <seconds>]...: gate.tsv
mkgate() {
  local run=$1 sv=$2 bv=$3
  shift 3
  mkdir -p "$run"
  {
    printf 'step\tverdict\tseconds\n'
    while (($# >= 3)); do
      printf '%s\t%s\t%s\n' "$1" "$2" "$3"
      shift 3
    done
    [[ $sv == - ]] || printf 'STEPS\t%s\t125.3\n' "$sv"
    [[ $bv == - ]] || printf 'BUDGET\t%s\t126\n' "$bv"
  } > "$run/gate.tsv"
}

# ============================== failures ========================================
P=$T/profile
mkdir -p "$P"
proc "$P" internal_fleet.4711 exit 1 "" exit
proc "$P" internal_fleet_extra.55 exit 1 "" exit
stream "$T/fleet.json" "$GO/internal/fleet:TestX" "$GO/internal/fleet"
run_sut failures "$T/fleet.json" "$P"
eq "failing test: one line, the package's DIAGNOSIS path (a package whose label only starts the same is not it)" \
  "  PROFILE $GO/internal/fleet: $P/internal_fleet.4711/exit/DIAGNOSIS.txt" "$OUT"
eq "failing test: exit 0, nothing on stderr" "0|" "$RC|$ERR"

stream "$T/pkgfail.json" "$GO/internal/fleet"
run_sut failures "$T/pkgfail.json" "$P"
eq "a package-level fail with no failing test is listed" "  PROFILE $GO/internal/fleet: $P/internal_fleet.4711/exit/DIAGNOSIS.txt" "$OUT"

stream "$T/testonly.json" "$GO/internal/fleet:TestX"
run_sut failures "$T/testonly.json" "$P/"
eq "a stream holding only the failing test's event is listed; a trailing slash on the profile dir is dropped" \
  "  PROFILE $GO/internal/fleet: $P/internal_fleet.4711/exit/DIAGNOSIS.txt" "$OUT"

P2=$T/profile-shard
mkdir -p "$P2"
proc "$P2" cmd_pfm.20 exit 1 "" exit
proc "$P2" cmd_pfm.9 exit 0 ""
proc "$P2" cmd_pfm.30 exit 1 "20" exit
stream "$T/shard.json" "$GO/cmd/pfm:TestShard"
run_sut failures "$T/shard.json" "$P2"
eq "sharded package: one line per non-helper dir in pid order — the no-bundle line, then the bundle; the helper dir is not listed" \
  "  PROFILE $GO/cmd/pfm: no bundle — $P2/cmd_pfm.9/summary.json (event exit, exit 0)
  PROFILE $GO/cmd/pfm: $P2/cmd_pfm.20/exit/DIAGNOSIS.txt" "$OUT"

P3=$T/profile-helpers
mkdir -p "$P3"
proc "$P3" cmd_pfm.31 exit 1 "20" exit
run_sut failures "$T/shard.json" "$P3"
eq "a package whose only dirs are helpers says so, never silence" \
  "  PROFILE $GO/cmd/pfm: NOT RECORDED — every cmd_pfm.* under $P3 is a helper process" "$OUT"

stream "$T/norecord.json" "$GO/internal/unrecorded:TestX"
run_sut failures "$T/norecord.json" "$P"
eq "no record: NOT RECORDED naming the label and the profile dir" \
  "  PROFILE $GO/internal/unrecorded: NOT RECORDED — no internal_unrecorded.* under $P" "$OUT"
run_sut failures "$T/norecord.json" "$T/no-such-profile-dir"
eq "no profile dir at all is NOT RECORDED too, exit 0" "  PROFILE $GO/internal/unrecorded: NOT RECORDED — no internal_unrecorded.* under $T/no-such-profile-dir|0" "$OUT|$RC"

stream "$T/foreign.json" "example.org/elsewhere/pkg:TestX"
run_sut failures "$T/foreign.json" "$P"
has "an import path outside the module has no label and is said so" "  PROFILE example.org/elsewhere/pkg: NOT RECORDED — no label" "$OUT"

stream "$T/many.json" "$GO/internal/unrecorded:TestX" "$GO/cmd/pfm:TestShard" "$GO/internal/fleet:TestX"
run_sut failures "$T/many.json" "$P2"
eq "several failing packages: one block each, in import-path order" \
  "$GO/cmd/pfm|$GO/cmd/pfm|$GO/internal/fleet|$GO/internal/unrecorded" \
  "$(sed -n 's/^  PROFILE \([^:]*\):.*/\1/p' <<< "$OUT" | paste -sd'|' -)"

stream "$T/green.json"
run_sut failures "$T/green.json" "$P"
eq "green stream: nothing printed, exit 0" "|0|" "$OUT|$RC|$ERR"

run_sut failures "$T/absent.json" "$P"
eq "unreadable stream (absent): exit 2" 2 "$RC"
has "unreadable stream: stderr names the stream" "$T/absent.json" "$ERR"
: > "$T/empty.json"
run_sut failures "$T/empty.json" "$P"
eq "an empty stream is unreadable, never an empty list: exit 2" "2|" "$RC|$OUT"
has "an empty stream names itself on stderr" "$T/empty.json" "$ERR"
printf 'not json at all\n' > "$T/garbage.json"
run_sut failures "$T/garbage.json" "$P"
eq "a stream holding no event is unreadable: exit 2" "2|" "$RC|$OUT"

{
  echo 'go: downloading something'
  cat "$T/fleet.json"
} > "$T/noisy.json"
run_sut failures "$T/noisy.json" "$P"
eq "a non-JSON line in the stream is warned about and the failures still list" \
  "  PROFILE $GO/internal/fleet: $P/internal_fleet.4711/exit/DIAGNOSIS.txt|0" "$OUT|$RC"
has "the warning names the stream and the line" "$T/noisy.json: 1 line(s) are not JSON events (first: line 1)" "$ERR"

for args in "" "bogus" "failures" "failures $T/fleet.json" "failures a b c" "summary" "summary a b"; do
  # shellcheck disable=SC2086 # the cases are word lists
  run_sut $args
  eq "bad usage '$args': exit 64 and the usage on stderr" "64|1" "$RC|$([[ $ERR == *usage:* ]] && echo 1 || echo 0)"
done
run_sut help
eq "help prints the usage, exit 0" "0|1" "$RC|$([[ $OUT == *"usage: profile-report.sh failures"* ]] && echo 1 || echo 0)"

# failures: what a process directory can be in
P4=$T/profile-odd
mkdir -p "$P4"
mkdir -p "$P4/internal_fleet.1"
proc "$P4" internal_fleet.2 exit 1 ""
rm -rf "$P4/internal_fleet.2/exit"
printf '{"bundles":["exit"],"event":"exit","exit_code":1,"helper_of":""}\n' > "$P4/internal_fleet.2/summary.json"
proc "$P4" internal_fleet.3 timeout-imminent 0 "" timeout exit
mkdir -p "$P4/internal_fleet.4"
printf '{broken' > "$P4/internal_fleet.4/summary.json"
mkdir -p "$P4/internal_fleet.5/deadline"
echo late > "$P4/internal_fleet.5/deadline/DIAGNOSIS.txt"
printf '{"bundles":[],"event":"started-no-exit-recorded","exit_code":-1,"helper_of":""}\n' > "$P4/internal_fleet.5/summary.json"
run_sut failures "$T/fleet.json" "$P4"
eq "a dir without summary.json is NO SUMMARY; a bundle the summary lists with no file is said missing; several bundles share their dir's line; a corrupt summary is named; a bundle on disk is found without the summary listing it" \
  "  PROFILE $GO/internal/fleet: no bundle — $P4/internal_fleet.1/summary.json NO SUMMARY
  PROFILE $GO/internal/fleet: $P4/internal_fleet.2/exit/DIAGNOSIS.txt (listed in summary.json, file missing)
  PROFILE $GO/internal/fleet: $P4/internal_fleet.3/timeout/DIAGNOSIS.txt · $P4/internal_fleet.3/exit/DIAGNOSIS.txt
  PROFILE $GO/internal/fleet: no bundle — $P4/internal_fleet.4/summary.json SUMMARY UNREADABLE
  PROFILE $GO/internal/fleet: $P4/internal_fleet.5/deadline/DIAGNOSIS.txt" "$OUT"
has "a corrupt summary.json is named on stderr" "$P4/internal_fleet.4/summary.json" "$ERR"

# ============================== summary ==========================================
R=$T/timing/run.MAIN
mkres "$R/resources.tsv" 30 15
mkgate "$R" FAIL WARN \
  pfm.unit FAIL 61.2 pfm.e2e FAIL 40.0 fixture.go-spin FAIL 5.0 templates.leak FAIL 2.0 \
  templates.a PASS 30.0 templates.b PASS 20.0 templates.c PASS 10.0 templates.err PASS 9.0 templates.d PASS 8.0 templates.e PASS 1.0 \
  slow.step TIMEOUT 120.0 skipped.step NOT-RUN 0.0
mkprof "$R" pfm.unit exit 1 "$(ep 18)" "$(ep 28)" 61.200000 55.000 0 1.500000 0.200000 0.000000
mkprof "$R" pfm.e2e exit 1 "$(ep 1.25)" "$(ep 9.75)" 40.000000 30.123 0 0.100000 0.200000 0.300000
{
  printf 'step\tfixture.go-spin\nended\texit\nrc\t1\nbound_s\t600\nstart_epoch\t%s\nfinish_epoch\t%s\nwall_s\t5.000000\n' "$(ep 16)" "$(ep 26)"
  printf 'children_cpu\t0m1.000s user 0m0.500s sys\ncpu_s\t4.500\ntrace\terr\nerr_records\t0\npsi\tUNAVAILABLE no pressure lines under /proc/pressure\n'
} > "$R/steps/fixture.go-spin.prof.tsv"
mkprof "$R" templates.leak exit 1 "$(ep 1)" "$(ep 3)" 2.000000 0.500 0 0.000000 0.000000 0.000000
mkprof "$R" templates.a exit 0 "$(ep 2)" "$(ep 12)" 30.000000 12.000 0 0.000000 0.000000 0.000000
mkprof "$R" templates.b exit 0 "$(ep 3)" "$(ep 13)" 20.000000 8.000 0 0.000000 0.000000 0.000000
mkprof "$R" templates.c exit 0 "$(ep 4)" "$(ep 14)" 10.000000 4.000 0 0.000000 0.000000 0.000000
mkprof "$R" templates.err exit 0 "$(ep 1)" "$(ep 9)" 9.000000 3.000 2 0.000000 0.000000 0.000000
printf 'ERR rc=1 %s /x/foo.sh:12 do_thing: false\n  at /x/foo.sh:40 main\nERR rc=2 %s /x/bar.sh:3 other: exit 2\n' "$(ep 5)" "$(ep 6)" > "$R/steps/templates.err.xtrace"
mkprof "$R" templates.d exit 0 "$(ep 5)" "$(ep 10)" 8.000000 2.000 0 0.000000 0.000000 0.000000
printf 'this is not a prof file\n' > "$R/steps/templates.e.prof.tsv"
mkprof "$R" slow.step timeout 124 "$(ep 2)" "$(ep 12)" 120.000000 9.000 0 0.000000 0.000000 0.000000
mkdir -p "$R/steps/slow.step.hang"
echo "tree" > "$R/steps/slow.step.hang/tree.txt"
printf 'cg_swap_mb\tmemory.swap.current unreadable\n' > "$R/resources.err"
proc "$R/profile" internal_fleet.4711 exit 1 "" exit
proc "$R/profile" cmd_pfm.2 exit 0 "4711"
mkdir -p "$R/profile/cmd_pfm.3"
mkdir -p "$R/profile/x.4"
printf '{oops' > "$R/profile/x.4/summary.json"
INDEX=$R/profile/INDEX.txt

run_sut summary "$R"
eq "summary: exit 0" 0 "$RC"
has "summary: the unreadable prof.tsv is named on stderr" "$R/steps/templates.e.prof.tsv" "$ERR"
mapfile -t L <<< "$OUT"
eq "summary: 13 lines — gate, 6 non-PASS steps, 5 slowest PASS steps, index" 13 "${#L[@]}"
eq "summary: the gate line first, the index line last" "PROFILE gate|PROFILE index $INDEX" "$(awk '{print $1, $2}' <<< "${L[0]}" | head -n 1)|${L[12]}"
eq "summary: non-PASS steps in gate order, then the five slowest PASS steps (the sixth, templates.e, is left out)" \
  "pfm.unit pfm.e2e fixture.go-spin templates.leak slow.step skipped.step templates.a templates.b templates.c templates.err templates.d" \
  "$(awk '$2 == "step" { print $3 }' <<< "$OUT" | paste -sd' ' -)"
GATE_JUDGED=$(judged "$R/resources.tsv" 0 99999999999)
eq "summary: the gate line carries the run's wall, CPU, I/O, memory peak, swap and the judge's whole-file answer" \
  "PROFILE gate FAIL 125.3s · cpu 58.0s · io 290.0/145.0 MB · mem peak 229.0 MB · swap 4.0 MB · $GATE_JUDGED" "${L[0]}"
has "summary: the whole file is CONTENTION (cpu pressure 24% of the file)" "attribution CONTENTION (" "${L[0]}"

UNIT=$(line_with "$OUT" "PROFILE step pfm.unit ")
eq "step line: pfm.unit — wall, CPU, PSI, the judge's answer over the step window, then the profiles pointer" \
  "PROFILE step pfm.unit FAIL 61.2s · cpu 55.0s · psi cpu/io/mem 1.50/0.20/0.00s · $(judged "$R/resources.tsv" "$(ep 18)" "$(ep 28)") · profiles $INDEX" "$UNIT"
has "step line: a window inside the contended phase is CONTENTION" "attribution CONTENTION (" "$UNIT"
E2E=$(line_with "$OUT" "PROFILE step pfm.e2e ")
eq "step line: pfm.e2e is red too, so it points at the profiles; its quiet window is CODE" \
  "PROFILE step pfm.e2e FAIL 40.0s · cpu 30.1s · psi cpu/io/mem 0.10/0.20/0.30s · $(judged "$R/resources.tsv" "$(ep 1.25)" "$(ep 9.75)") · profiles $INDEX" "$E2E"
has "step line: pfm.e2e attribution is CODE" "attribution CODE (" "$E2E"
FIX=$(line_with "$OUT" "PROFILE step fixture.go-spin ")
has "step line: fixture.go-* red points at the profiles" " · profiles $INDEX" "$FIX"
has "step line: PSI that was UNAVAILABLE is NA, never 0" "psi cpu/io/mem NA/NA/NAs" "$FIX"
LEAK=$(line_with "$OUT" "PROFILE step templates.leak ")
ends "step line: a red step that is not a Go step has no profiles pointer" "· attribution not measured ($(bash "$JUDGE" window --resources "$R/resources.tsv" --from "$(ep 1)" --to "$(ep 3)" | cut -f2))" "$LEAK"
SLOW=$(line_with "$OUT" "PROFILE step slow.step ")
ends "step line: a TIMEOUT step ends with its hang pointer" " · hang $R/steps/slow.step.hang/tree.txt" "$SLOW"
has "step line: a TIMEOUT step is shown with its verdict and wall" "PROFILE step slow.step TIMEOUT 120.0s · cpu 9.0s" "$SLOW"
SKIP=$(line_with "$OUT" "PROFILE step skipped.step ")
eq "step line: a NOT-RUN step is NA throughout and says it did not run" \
  "PROFILE step skipped.step NOT-RUN 0.0s · cpu NA · psi cpu/io/mem NA/NA/NAs · attribution not measured (step did not run)" "$SKIP"
ERRL=$(line_with "$OUT" "PROFILE step templates.err ")
ends "step line: ERR records end the line with the xtrace pointer and the first record" \
  " · xtrace $R/steps/templates.err.xtrace (2 ERR; first: ERR rc=1 $(ep 5) /x/foo.sh:12 do_thing: false)" "$ERRL"
eq "step line: a green step has no pointer" "0" "$(grep -c -e ' · profiles ' -e ' · hang ' -e ' · xtrace ' <<< "$(line_with "$OUT" 'PROFILE step templates.a ')")"

TSV=$R/profile.tsv
eq "profile.tsv: the header" "step	verdict	wall_s	cpu_s	io_mb	psi_cpu_s	psi_io_s	psi_mem_s	attribution	evidence" "$(head -n 1 "$TSV")"
eq "profile.tsv: one row per gate.tsv step plus GATE, last" "14|GATE|10" "$(awk 'END { print NR }' "$TSV")|$(tail -n 1 "$TSV" | cut -f1)|$(awk -F'\t' '{ print NF }' "$TSV" | sort -u | paste -sd, -)"
eq "profile.tsv: steps in registration order" \
  "pfm.unit pfm.e2e fixture.go-spin templates.leak templates.a templates.b templates.c templates.err templates.d templates.e slow.step skipped.step GATE" \
  "$(tail -n +2 "$TSV" | cut -f1 | paste -sd' ' -)"
eq "profile.tsv: pfm.unit — verdict, wall from gate.tsv, CPU and PSI from prof.tsv, I/O over the window" \
  "FAIL 61.2 55.000 150.000 1.500000 0.200000 0.000000 CONTENTION" \
  "$(for c in verdict wall_s cpu_s io_mb psi_cpu_s psi_io_s psi_mem_s attribution; do cell "$TSV" pfm.unit "$c"; done | paste -sd' ' -)"
eq "profile.tsv: I/O over a window between two samples is interpolated (8.5 s at 15 MB/s)" 127.500 "$(cell "$TSV" pfm.e2e io_mb)"
eq "profile.tsv: a window of 3 samples is in the judge's hands: not measured, its I/O still counted" "not measured|30.000" "$(cell "$TSV" templates.leak attribution)|$(cell "$TSV" templates.leak io_mb)"
eq "profile.tsv: unavailable PSI cells are NA" "4.500|NA|NA|NA" "$(cell "$TSV" fixture.go-spin cpu_s)|$(cell "$TSV" fixture.go-spin psi_cpu_s)|$(cell "$TSV" fixture.go-spin psi_io_s)|$(cell "$TSV" fixture.go-spin psi_mem_s)"
eq "profile.tsv: the evidence cell is the judge's evidence" "$(bash "$JUDGE" window --resources "$R/resources.tsv" --from "$(ep 1.25)" --to "$(ep 9.75)" | cut -f2)" "$(cell "$TSV" pfm.e2e evidence)"
eq "profile.tsv: a step that did not run is NA with its reason" "NOT-RUN|0.0|NA|NA|NA|NA|NA|not measured|step did not run" \
  "$(for c in verdict wall_s cpu_s io_mb psi_cpu_s psi_io_s psi_mem_s attribution evidence; do cell "$TSV" skipped.step "$c"; done | paste -sd'|' -)"
eq "profile.tsv: an unreadable prof.tsv is NA and not measured, saying why" "PASS|1.0|NA|NA|not measured" \
  "$(cell "$TSV" templates.e verdict)|$(cell "$TSV" templates.e wall_s)|$(cell "$TSV" templates.e cpu_s)|$(cell "$TSV" templates.e io_mb)|$(cell "$TSV" templates.e attribution)"
has "profile.tsv: its reason names the prof.tsv" "templates.e.prof.tsv unreadable" "$(cell "$TSV" templates.e evidence)"
eq "profile.tsv: the GATE row — STEPS wall, CPU, I/O and VM PSI deltas over the file, the whole-file answer" \
  "FAIL 125.3 58.000 435.000 0.290 0.580 0.870 CONTENTION" \
  "$(for c in verdict wall_s cpu_s io_mb psi_cpu_s psi_io_s psi_mem_s attribution; do cell "$TSV" GATE "$c"; done | paste -sd' ' -)"

# The one case that reads the real thresholds file: the script at its own location, the judge its PFM names,
# whatever values pfm/.testcontention.yml holds. It runs on a copy of the run, so $R's own profile.tsv stays;
# the copy sits outside $T/timing, whose run.* siblings the judge reads for its spin reference.
RR=$T/real/run.REAL
mkdir -p "$T/real"
cp -R "$R" "$RR"
bash "$REAL_SUT" summary "$RR" > /dev/null 2> "$T/stderr"
REAL_RC=$?
REAL_ERR=$(< "$T/stderr")
REAL_JRC=0
REAL_ANSWER=$(env PFM="$(cd "$(dirname -- "$REAL_JUDGE")/.." && pwd)" bash "$REAL_JUDGE" window --resources "$R/resources.tsv" --from 0 --to 99999999999 2> "$T/stderr") || REAL_JRC=$?
REAL_JERR=$(< "$T/stderr")
REAL_WORD=${REAL_ANSWER%%$'\t'*}
REAL_CELL=$(cell "$RR/profile.tsv" GATE attribution)
if [[ $REAL_RC -eq 0 && $REAL_JRC -eq 0 && -n $REAL_WORD && $REAL_CELL == "$REAL_WORD" ]]; then
  ok "the real thresholds file: the real tree's GATE row is the real judge's whole-file answer"
else
  bad "the real thresholds file: the real tree's GATE row is the real judge's whole-file answer" \
    "summary exit $REAL_RC, judge exit $REAL_JRC; the GATE row's attribution: $REAL_CELL; the judge's word: $REAL_WORD" \
    "summary stderr: $REAL_ERR" "judge stderr: $REAL_JERR"
fi

# INDEX
mapfile -t IX < "$INDEX"
has "INDEX: the header names the run and a UTC stamp" "# profile index $R · " "${IX[0]}"
if [[ ${IX[0]} =~ ^"# profile index $R · "[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]]; then ok "INDEX: the stamp is a UTC second"; else bad "INDEX: the stamp is a UTC second" "${IX[0]}"; fi
IXT=$(< "$INDEX")
has "INDEX: Go test processes — count" "## Go test processes (4)" "$IXT"
eq "INDEX: a helper process line says whose helper it is" "$R/profile/cmd_pfm.2 · event exit · exit 0 · wall 2.5s · run delay 0.123s · helper of 4711" "$(line_with "$IXT" "/cmd_pfm.2 ")"
eq "INDEX: a dir without summary.json is NO SUMMARY" "$R/profile/cmd_pfm.3 NO SUMMARY" "$(line_with "$IXT" "/cmd_pfm.3 ")"
eq "INDEX: a process line carries its bundle" "$R/profile/internal_fleet.4711 · event exit · exit 1 · wall 2.5s · run delay 0.123s → $R/profile/internal_fleet.4711/exit/DIAGNOSIS.txt" "$(line_with "$IXT" "/internal_fleet.4711 ")"
eq "INDEX: a corrupt summary.json is named, never skipped" "$R/profile/x.4 SUMMARY UNREADABLE" "$(line_with "$IXT" "/x.4 ")"
has "INDEX: Steps — one line per prof.tsv" "## Steps (11)" "$IXT"
eq "INDEX: a step line — verdict, ended, rc, wall, CPU" "pfm.unit · FAIL · ended exit · rc 1 · wall 61.2s · cpu 55.0s" "$(line_with "$IXT" "pfm.unit · ")"
eq "INDEX: a timed-out step carries its hang pointer" "slow.step · TIMEOUT · ended timeout · rc 124 · wall 120.0s · cpu 9.0s · hang $R/steps/slow.step.hang/tree.txt" "$(line_with "$IXT" "slow.step · ")"
has "INDEX: a step with ERR records carries its xtrace pointer" "templates.err · PASS · ended exit · rc 0 · wall 9.0s · cpu 3.0s · xtrace $R/steps/templates.err.xtrace (2 ERR; first: ERR rc=1" "$(line_with "$IXT" "templates.err · ")"
eq "INDEX: an unreadable prof.tsv is PROF UNREADABLE" "templates.e PROF UNREADABLE" "$(line_with "$IXT" "templates.e ")"
has "INDEX: Resources — the file and its samples" "$R/resources.tsv · 30 samples" "$IXT"
has "INDEX: Resources — each column named in resources.err" "cg_swap_mb NA — memory.swap.current unreadable" "$IXT"

cp "$TSV" "$T/tsv.first"
run_sut summary "$R"
eq "summary: the rerun's profile.tsv is byte-identical" "$(< "$T/tsv.first")" "$(< "$TSV")"
eq "summary: the rerun still exits 0 and ends on the index line" "0|PROFILE index $INDEX" "$RC|$(tail -n 1 <<< "$OUT")"

# --- no resources.tsv ----------------------------------------------------------
R2=$T/timing/run.NORES
mkgate "$R2" PASS - pfm.unit PASS 10.0
mkprof "$R2" pfm.unit exit 0 "$(ep 1)" "$(ep 9)" 10.000000 5.000 0 0.200000 0.100000 0.000000
run_sut summary "$R2"
eq "no resources: the step row keeps prof.tsv's cells, the resource cells are NA, attribution is not measured" \
  "5.000|NA|0.200000|not measured" "$(cell "$R2/profile.tsv" pfm.unit cpu_s)|$(cell "$R2/profile.tsv" pfm.unit io_mb)|$(cell "$R2/profile.tsv" pfm.unit psi_cpu_s)|$(cell "$R2/profile.tsv" pfm.unit attribution)"
eq "no resources: the GATE row's CPU, I/O and PSI are NA" "NA|NA|NA|NA|NA|not measured" \
  "$(cell "$R2/profile.tsv" GATE cpu_s)|$(cell "$R2/profile.tsv" GATE io_mb)|$(cell "$R2/profile.tsv" GATE psi_cpu_s)|$(cell "$R2/profile.tsv" GATE psi_io_s)|$(cell "$R2/profile.tsv" GATE psi_mem_s)|$(cell "$R2/profile.tsv" GATE attribution)"
eq "no resources: the gate line says not measured with the judge's reason, never zeros" \
  "PROFILE gate PASS 125.3s · cpu NA · io NA/NA MB · mem peak NA MB · swap NA MB · attribution not measured ($R2/resources.tsv absent)" "$(head -n 1 <<< "$OUT")"
has "no resources: the step line says not measured (…)" "attribution not measured ($R2/resources.tsv absent)" "$(line_with "$OUT" "PROFILE step pfm.unit ")"
has "no resources: INDEX says NOT RECORDED" "$R2/resources.tsv NOT RECORDED" "$(< "$R2/profile/INDEX.txt")"
has "no resources: INDEX says no process was recorded" "## Go test processes (0)" "$(< "$R2/profile/INDEX.txt")"
has "no resources: INDEX says the profile dir was not recorded, not that it was empty" "$R2/profile NOT RECORDED" "$(< "$R2/profile/INDEX.txt")"

R3=$T/timing/run.EMPTYRES
mkgate "$R3" PASS - pfm.unit PASS 10.0
mkprof "$R3" pfm.unit exit 0 "$(ep 1)" "$(ep 9)" 10.000000 5.000 0 0.200000 0.100000 0.000000
: > "$R3/resources.tsv"
mkdir -p "$R3/profile"
run_sut summary "$R3"
has "an empty resources.tsv is not measured with the judge's reason" "attribution not measured ($R3/resources.tsv empty)" "$(head -n 1 <<< "$OUT")"
has "an empty resources.tsv is EMPTY in INDEX" "$R3/resources.tsv EMPTY" "$(< "$R3/profile/INDEX.txt")"
has "an existing but empty profile dir says none recorded, not just a zero" "none recorded — no <label>.<pid> directory under $R3/profile" "$(< "$R3/profile/INDEX.txt")"

# --- the judge fails -------------------------------------------------------------
R4=$T/timing/run.BADRES
mkgate "$R4" PASS - pfm.unit PASS 10.0
mkprof "$R4" pfm.unit exit 0 "$(ep 1)" "$(ep 9)" 10.000000 5.000 0 0.200000 0.100000 0.000000
printf 'bogus header\n1\n' > "$R4/resources.tsv"
run_sut summary "$R4"
eq "judge fails: exit 0, the report still lands" "0|10" "$RC|$(awk -F'\t' 'NR == 1 { print NF }' "$R4/profile.tsv")"
ATTR=$(cell "$R4/profile.tsv" pfm.unit evidence)
has "judge fails: the evidence is the judge's own stderr line" "judge failed: test-contention: resources $R4/resources.tsv: header differs" "$ATTR"
eq "judge fails: attribution is not measured in the table, on the step line and on the gate line" "not measured|1|1" \
  "$(cell "$R4/profile.tsv" pfm.unit attribution)|$(grep -c 'attribution not measured (judge failed: test-contention' <<< "$(line_with "$OUT" 'PROFILE step pfm.unit ')")|$(grep -c 'attribution not measured (judge failed: test-contention' <<< "$(head -n 1 <<< "$OUT")")"
eq "judge fails: the GATE row too" "not measured" "$(cell "$R4/profile.tsv" GATE attribution)"

mkdir -p "$T/nojudge/infra/fence"
cp "$SUT" "$T/nojudge/infra/fence/profile-report.sh"
OUT=$(bash "$T/nojudge/infra/fence/profile-report.sh" summary "$R4" 2> "$T/stderr")
RC=$?
eq "a judge that is not there: exit 0, attribution not measured naming it" "0|1" "$RC|$(grep -c 'attribution not measured (judge failed: .*test-contention.sh' <<< "$(head -n 1 <<< "$OUT")")"

# --- GATE verdict ------------------------------------------------------------------
n=0
for row in "PASS - PASS" "FAIL - FAIL" "PASS PASS PASS" "PASS FAIL FAIL" "PASS ERROR FAIL" "PASS WARN WARN" "PASS UNPINNED PASS" "FAIL WARN FAIL" "FAIL PASS FAIL"; do
  read -r sv bv want <<< "$row"
  n=$((n + 1))
  RG=$T/timing/run.VERDICT$n
  mkgate "$RG" "$sv" "$bv" step.one PASS 1.0
  run_sut summary "$RG"
  eq "GATE verdict: STEPS $sv BUDGET $bv → $want" "$want" "$(cell "$RG/profile.tsv" GATE verdict)"
done
RG=$T/timing/run.NOSTEPS
mkgate "$RG" - - step.one PASS 1.0
run_sut summary "$RG"
eq "GATE with no STEPS row: verdict and wall are NA, and the gap is named on stderr" "NA|NA|1" "$(cell "$RG/profile.tsv" GATE verdict)|$(cell "$RG/profile.tsv" GATE wall_s)|$([[ $ERR == *"no STEPS row"* ]] && echo 1 || echo 0)"
has "GATE with no STEPS row: the gate line says NA" "PROFILE gate NA NA" "$(head -n 1 <<< "$OUT")"

# --- pointers in the corners -------------------------------------------------------
R5=$T/timing/run.CORNERS
mkres "$R5/resources.tsv" 30 30
mkgate "$R5" FAIL - pfm.unit PASS 70.0 hang.step FAIL 60.0 long.err FAIL 3.0 lost.err FAIL 2.0 cpu.step PASS 1.0 gone.step TIMEOUT 9.0
mkprof "$R5" pfm.unit exit 0 "$(ep 1)" "$(ep 9)" 70.000000 1.000 0 0.000000 0.000000 0.000000
mkprof "$R5" hang.step timeout 124 "$(ep 1)" "$(ep 9)" 60.000000 1.000 0 0.000000 0.000000 0.000000
mkprof "$R5" long.err exit 1 "$(ep 1)" "$(ep 9)" 3.000000 1.000 1 0.000000 0.000000 0.000000
printf 'ERR rc=1 %s %s\n' "$(ep 2)" "$(printf 'x%.0s' $(seq 1 300))" > "$R5/steps/long.err.xtrace"
mkprof "$R5" lost.err exit 1 "$(ep 1)" "$(ep 9)" 2.000000 1.000 3 0.000000 0.000000 0.000000
mkprof "$R5" cpu.step exit 0 "$(ep 1)" "$(ep 9)" 1.000000 "NA unparsable times line 'x'" 0 "UNAVAILABLE absent at step start" 0.000000 0.000000
run_sut summary "$R5"
eq "a prof.tsv value that is NA or UNAVAILABLE is an NA cell, never the text and never 0" "NA|NA|0.000000" "$(cell "$R5/profile.tsv" cpu.step cpu_s)|$(cell "$R5/profile.tsv" cpu.step psi_cpu_s)|$(cell "$R5/profile.tsv" cpu.step psi_io_s)"
eq "a Go step that is PASS has no profiles pointer" "1|0" "$(grep -c '^PROFILE step pfm.unit PASS' <<< "$OUT")|$(grep -c '^PROFILE step pfm.unit PASS.* · profiles ' <<< "$OUT")"
GONE=$(line_with "$OUT" "PROFILE step gone.step ")
ends "a TIMEOUT verdict with no prof.tsv still points at the hang tree, saying MISSING" " · hang $R5/steps/gone.step.hang/tree.txt (MISSING)" "$GONE"
has "a step with no prof.tsv has no window and says why" "attribution not measured (no window: $R5/steps/gone.step.prof.tsv absent)" "$GONE"
HANG=$(line_with "$OUT" "PROFILE step hang.step ")
ends "a step whose prof.tsv says ended timeout points at the hang tree, saying MISSING when tree.txt is not there" " · hang $R5/steps/hang.step.hang/tree.txt (MISSING)" "$HANG"
LONG=$(line_with "$OUT" "PROFILE step long.err ")
FIRST=${LONG#* · xtrace *first: }
FIRST=${FIRST%)}
BODY=${FIRST%…}
eq "ERR records: the first record is cut to 200 characters, the last of them the cut marker" "199|1" "${#BODY}|$([[ $FIRST == *… ]] && echo 1 || echo 0)"
has "ERR records: the count of one reads 1 ERR" "(1 ERR; first: ERR rc=1 " "$LONG"
LOST=$(line_with "$OUT" "PROFILE step lost.err ")
has "ERR records with no xtrace file say so, never a blank" "(3 ERR; first: UNREADABLE " "$LOST"
eq "a PASS step among the five slowest is listed" "1" "$(grep -c '^PROFILE step cpu.step PASS' <<< "$OUT")"

# --- counters: backwards, and windows that run past the samples ------------------
R6=$T/timing/run.COUNTERS
mkres "$R6/resources.tsv" 30 30
awk -F'\t' -v OFS='\t' 'NR > 1 { $6 = sprintf("%.3f", 1000 - 10 * (NR - 2)) } 1' "$R6/resources.tsv" > "$R6/resources.new" && mv "$R6/resources.new" "$R6/resources.tsv"
mkgate "$R6" PASS - back.step PASS 1.0
mkprof "$R6" back.step exit 0 "$(ep 2)" "$(ep 12)" 1.000000 1.000 0 0.000000 0.000000 0.000000
run_sut summary "$R6"
eq "a counter that went backwards is NA, never a negative number" "NA|NA" "$(cell "$R6/profile.tsv" back.step io_mb)|$(cell "$R6/profile.tsv" GATE io_mb)"

R7=$T/timing/run.EDGES
mkres "$R7/resources.tsv" 30 30
mkgate "$R7" PASS - near.step PASS 1.0 far.step PASS 1.0 before.step PASS 1.0
mkprof "$R7" near.step exit 0 "$(ep 25)" "$(ep 29.5)" 1.000000 1.000 0 0.000000 0.000000 0.000000
mkprof "$R7" far.step exit 0 "$(ep 25)" "$(ep 40)" 1.000000 1.000 0 0.000000 0.000000 0.000000
mkprof "$R7" before.step exit 0 "$(ep -40)" "$(ep -30)" 1.000000 1.000 0 0.000000 0.000000 0.000000
run_sut summary "$R7"
eq "a window that ends within one sample past the last row is read at that row; one that ends far past it, or lies before every sample, is NA" \
  "60.000|NA|NA" "$(cell "$R7/profile.tsv" near.step io_mb)|$(cell "$R7/profile.tsv" far.step io_mb)|$(cell "$R7/profile.tsv" before.step io_mb)"

# --- a judge that answers wrongly -----------------------------------------------------
for kind in short garbled; do
  FJ=$T/fakejudge-$kind
  mkdir -p "$FJ/infra/fence" "$FJ/pfm/scripts"
  cp "$SUT" "$FJ/infra/fence/profile-report.sh"
  case $kind in
    short) printf '#!/usr/bin/env bash\nprintf "only\\tCODE\\tone answer\\n"\n' > "$FJ/pfm/scripts/test-contention.sh" ;;
    garbled) printf '#!/usr/bin/env bash\nprintf "one\\tfield\\ntwo\\n"\n' > "$FJ/pfm/scripts/test-contention.sh" ;;
  esac
  OUT=$(bash "$FJ/infra/fence/profile-report.sh" summary "$R4" 2> "$T/stderr")
  eq "a judge that answers $kind is a judge failure, not a verdict" "1" "$(grep -c 'attribution not measured (judge failed: ' <<< "$(head -n 1 <<< "$OUT")")"
done
OUT=$(bash "$T/fakejudge-short/infra/fence/profile-report.sh" summary "$R4" 2> "$T/stderr")
has "a judge that answers too few rows says how many" "judge failed: 1 answers for 2 windows" "$OUT"

# --- errors of the command itself ----------------------------------------------------
run_sut summary "$T/no-such-run"
eq "summary: no run directory is exit 2 naming it" "2|1" "$RC|$([[ $ERR == *"$T/no-such-run"* ]] && echo 1 || echo 0)"
mkdir -p "$T/timing/run.NOGATE"
run_sut summary "$T/timing/run.NOGATE"
eq "summary: no gate.tsv is exit 2 naming it" "2|1|" "$RC|$([[ $ERR == *"$T/timing/run.NOGATE/gate.tsv"* ]] && echo 1 || echo 0)|$OUT"
mkdir -p "$T/timing/run.BADGATE"
printf 'step\tverdict\nonly\tPASS\n' > "$T/timing/run.BADGATE/gate.tsv"
run_sut summary "$T/timing/run.BADGATE"
eq "summary: a gate.tsv with the wrong header is exit 2 naming the file" "2|1" "$RC|$([[ $ERR == *"$T/timing/run.BADGATE/gate.tsv"* ]] && echo 1 || echo 0)"
printf 'step\tverdict\tseconds\nonly\tPASS\n' > "$T/timing/run.BADGATE/gate.tsv"
run_sut summary "$T/timing/run.BADGATE"
eq "summary: a gate.tsv row with the wrong width is exit 2 naming file and line" "2|1" "$RC|$([[ $ERR == *"/gate.tsv:2:"* ]] && echo 1 || echo 0)"
RW=$T/timing/run.UNWRITABLE
mkgate "$RW" PASS - step.one PASS 1.0
mkdir -p "$RW/profile.tsv"
run_sut summary "$RW"
eq "summary: an output that cannot be written is exit 71 naming it, nothing printed" "71|1|" "$RC|$([[ $ERR == *"cannot write $RW/profile.tsv"* ]] && echo 1 || echo 0)|$OUT"

shtest_end

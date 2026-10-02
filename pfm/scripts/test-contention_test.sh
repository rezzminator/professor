#!/usr/bin/env bash
# Fixture-driven tests for scripts/test-contention.sh: one case per behaviour of
# the judge (signals, verdicts, batch, thresholds, every exit-2 door). Every
# resources.tsv is built here; nothing reads a real gate run. The threshold
# cases judge against a fixture thresholds file this suite writes; the real
# pfm/.testcontention.yml is read only by the thresholds-file checks and the
# default --yml case, so calibrating it never turns a case red.
#
# Run directly: bash scripts/test-contention_test.sh
set -uo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${TEST_CONTENTION_SUT:-$ROOT/scripts/test-contention.sh}"
YML="$ROOT/.testcontention.yml"
SHTEST_TAG=pfm-test-contention-test
# shellcheck source=/dev/null
source "$ROOT/../scripts/shtest.sh"
TAB=$'\t'

COLUMNS_LINE='epoch_s uptime_s cg_cpu_s vm_busy_s cpus cg_io_read_mb cg_io_write_mb cg_mem_mb cg_mem_peak_mb cg_swap_mb cg_psi_cpu_some_s cg_psi_io_some_s cg_psi_io_full_s cg_psi_mem_some_s cg_psi_mem_full_s vm_psi_cpu_some_s vm_psi_io_some_s vm_psi_io_full_s vm_psi_mem_some_s vm_psi_mem_full_s spin_us load1'
HEADER=${COLUMNS_LINE// /$TAB}

# rows <n> <t0> <dt> <cpus> <cg_rate> <vm_rate> <psi_rate> <spin_us>: n sampler rows, one per dt
# seconds from epoch t0; the cgroup, VM and pressure counters rise at the given rates per second.
rows() {
  awk -v n="$1" -v t0="$2" -v dt="$3" -v cpus="$4" -v cg="$5" -v vm="$6" -v psi="$7" -v spin="$8" 'BEGIN {
    for (i = 0; i < n; i++) {
      t = i * dt
      printf "%.6f\t%.6f\t%.6f\t%.6f\t%d\t1.000\t2.000\t100.000\t150.000\t0.000\t%.6f\t0.000000\t0.000000\t0.000000\t0.000000\t0.000000\t0.000000\t0.000000\t0.000000\t0.000000\t%s\t0.50\n", t0 + t, 1000 + t, cg * t, vm * t, cpus, psi * t, spin
    }
  }'
}
res() { # res <file>: a resources.tsv from the rows on stdin, header first
  mkdir -p "$(dirname -- "$1")"
  { printf '%s\n' "$HEADER"; cat; } > "$1"
}

eq() { # eq <name> <expected> <actual>
  if [[ $3 == "$2" ]]; then ok "$1"; else bad "$1" "expected: $2" "actual:   $3"; fi
}
has() { # has <name> <needle> <haystack>: the haystack contains the needle
  if [[ $3 == *"$2"* ]]; then ok "$1"; else bad "$1" "missing: $2" "in:      $3"; fi
}
lacks() { # lacks <name> <needle> <haystack>
  if [[ $3 != *"$2"* ]]; then ok "$1"; else bad "$1" "unexpected: $2" "in:         $3"; fi
}
judge() { # judge <args…>: OUT, ERR, RC of the judge; the default thresholds file is the repo's
  OUT=$(bash "$SUT" "$@" 2> "$T/stderr")
  RC=$?
  ERR=$(< "$T/stderr")
}
word() { printf '%s' "${OUT%%"$TAB"*}"; }

QUIET_LINE="CODE${TAB}tick-deficit +0.0% (> -5%) · cpu-psi +3.0% (< 20%) · spin ×1.0 (< ×1.5; ref 1000µs from this file) · run-delay NA (not given)"

# The threshold cases judge against this fixture file, so calibrating pfm/.testcontention.yml never
# moves them; the real file is read only by the thresholds-file checks and the default --yml case.
FIX="$T/thresholds.yml"
cat > "$FIX" <<'FIXTURE'
tick_deficit_max: -0.05
cpu_psi_share_min: 0.20
spin_ratio_min: 1.5
run_delay_ratio_min: 0.5
min_samples: 4
FIXTURE

# --- sampling-free fixtures: one run directory per scenario ------------------
# starved: 4 vCPUs, the cgroup used 4.0 cpu-s/s while /proc/stat ticked 3.4 → (3.4-4.0)/4 = -15 %
rows 10 1000 1 4 4.0 3.4 0.01 1000 | res "$T/starved/run.s1/resources.tsv"
# quiet: ticks equal the cgroup, 3 % cpu pressure, flat spin
rows 10 1000 1 4 2.0 2.0 0.03 1000 | res "$T/quiet/run.q1/resources.tsv"
# vm-wait: 30 % of the time the cgroup's work waited for a cpu inside the VM
rows 10 1000 1 4 2.0 2.0 0.30 1000 | res "$T/vmwait/run.w1/resources.tsv"

# --- host starvation ---------------------------------------------------------
judge window --resources "$T/starved/run.s1/resources.tsv" --yml "$FIX"
eq "host starvation: verdict CONTENTION, exit 0" "CONTENTION 0" "$(word) $RC"
has "host starvation: evidence tick-deficit -15.0% (≤ -5%)" "tick-deficit -15.0% (≤ -5%)" "$OUT"

# --- quiet window ------------------------------------------------------------
judge window --resources "$T/quiet/run.q1/resources.tsv" --yml "$FIX"
eq "quiet window: CODE and every signal's value and threshold" "$QUIET_LINE" "$OUT"
eq "quiet window: exit 0" 0 "$RC"

# --- in-VM CPU wait ----------------------------------------------------------
judge window --resources "$T/vmwait/run.w1/resources.tsv" --yml "$FIX"
eq "in-VM cpu wait: CONTENTION" CONTENTION "$(word)"
has "in-VM cpu wait: evidence names cpu-psi with its line" "cpu-psi +30.0% (≥ 20%)" "$OUT"
has "in-VM cpu wait: tick-deficit stays under its line" "tick-deficit +0.0% (> -5%)" "$OUT"

# --- slow spin ---------------------------------------------------------------
{ rows 6 1000 1 4 2.0 2.0 0.01 1000; rows 6 1006 1 4 2.0 2.0 0.01 2000; } | res "$T/spin/run.a1/resources.tsv"
judge window --resources "$T/spin/run.a1/resources.tsv" --from 1006 --to 1011 --yml "$FIX"
eq "slow spin, no siblings: CONTENTION" CONTENTION "$(word)"
has "slow spin, no siblings: reference is this file's minimum" "spin ×2.0 (≥ ×1.5; ref 1000µs from this file)" "$OUT"
rows 6 1000 1 4 2.0 2.0 0.01 500 | res "$T/spin/run.b1/resources.tsv"
judge window --resources "$T/spin/run.a1/resources.tsv" --from 1006 --to 1011 --yml "$FIX"
has "slow spin, a sibling: reference is the sibling's minimum, named" "spin ×4.0 (≥ ×1.5; ref 500µs from run.b1 (min of 2 files))" "$OUT"

# One outlier row does not make a slow window: the window's median is judged, not its mean.
{ rows 4 1000 1 4 2.0 2.0 0.01 1000; rows 1 1004 1 4 2.0 2.0 0.01 9000; } | res "$T/median/run.d1/resources.tsv"
judge window --resources "$T/median/run.d1/resources.tsv" --yml "$FIX"
eq "spin: the median of the window, an outlier row does not cross" "CODE${TAB}tick-deficit +0.0% (> -5%) · cpu-psi +0.0% (< 20%) · spin ×1.0 (< ×1.5; ref 1000µs from this file) · run-delay NA (not given)" "$OUT"
# A counter that went backwards (a restarted cgroup) is NA, never a negative share.
rows 6 1000 1 4 2.0 2.0 0.01 1000 | awk -F'\t' -v OFS='\t' 'NR == 1 { $3 = "100.000000"; $11 = "50.000000" } { print }' | res "$T/backwards/run.k1/resources.tsv"
judge window --resources "$T/backwards/run.k1/resources.tsv" --yml "$FIX"
eq "a counter that went backwards: the cpu signals are NA, the others decide" "CODE${TAB}tick-deficit NA (a cpu counter went backwards) · cpu-psi NA (the pressure counter went backwards) · spin ×1.0 (< ×1.5; ref 1000µs from this file) · run-delay NA (not given)" "$OUT"

# The reference reads this file and the 20 newest siblings: the 21st-newest is not read.
rows 6 1000 1 4 2.0 2.0 0.01 1000 | res "$T/many/run.m00/resources.tsv"
for i in $(seq 1 22); do
  d=$(printf '%s/many/run.n%02d' "$T" "$i")
  rows 6 1000 1 4 2.0 2.0 0.01 $((i == 1 ? 100 : 900)) | res "$d/resources.tsv"
  touch -d "@$((1700000000 + i))" "$d/resources.tsv"
done
touch -d "@1800000000" "$T/many/run.m00/resources.tsv"
judge window --resources "$T/many/run.m00/resources.tsv" --yml "$FIX"
has "a sibling beyond the 20 newest is not read: its 100 µs is not the reference" "ref 900µs from run.n" "$OUT"
touch -d "@1700000100" "$T/many/run.n01/resources.tsv"
judge window --resources "$T/many/run.m00/resources.tsv" --yml "$FIX"
has "a sibling among the 20 newest is read: its 100 µs is the reference" "ref 100µs from run.n01 (min of 21 files)" "$OUT"

# A file's floor is its nearest-rank 5th-percentile spin_us (rank ceil(n/20)), so one sample a VM
# clock step shortened does not become the reference; a file of 20 or fewer samples floors at its least.
rows 40 1000 1 4 2.0 2.0 0.01 1000 | res "$T/stepsib/run.a1/resources.tsv"
{ rows 40 1000 1 4 2.0 2.0 0.01 1000; rows 1 1040 1 4 2.0 2.0 0.01 100; } | res "$T/stepsib/run.b1/resources.tsv"
touch -d "@1800000000" "$T/stepsib/run.a1/resources.tsv"
judge window --resources "$T/stepsib/run.a1/resources.tsv" --yml "$FIX"
has "a stepped sample in a sibling: the reference stays 1000 µs, spin ×1.0" "spin ×1.0 (< ×1.5; ref 1000µs from this file (min of 2 files))" "$OUT"
eq "a stepped sample in a sibling: CODE" CODE "$(word)"
{ rows 40 1000 1 4 2.0 2.0 0.01 1000; rows 1 1040 1 4 2.0 2.0 0.01 100; } | res "$T/stepown/run.o1/resources.tsv"
judge window --resources "$T/stepown/run.o1/resources.tsv" --yml "$FIX"
has "a stepped sample in this file: the reference is 1000 µs from this file" "spin ×1.0 (< ×1.5; ref 1000µs from this file)" "$OUT"
eq "a stepped sample in this file: no sibling, so no min-of count" "CODE 0" "$(word) $RC"
rows 40 1000 1 4 2.0 2.0 0.01 1000 | res "$T/lowtail/run.a1/resources.tsv"
{ rows 37 1000 1 4 2.0 2.0 0.01 1000; rows 3 1037 1 4 2.0 2.0 0.01 500; } | res "$T/lowtail/run.t1/resources.tsv"
touch -d "@1800000000" "$T/lowtail/run.a1/resources.tsv"
judge window --resources "$T/lowtail/run.a1/resources.tsv" --yml "$FIX"
has "a real low tail: 3 of 40 samples at 500 µs, rank 2 falls inside them, the reference is 500 µs" "spin ×2.0 (≥ ×1.5; ref 500µs from run.t1 (min of 2 files))" "$OUT"
eq "a real low tail: CONTENTION" CONTENTION "$(word)"
{ rows 19 1000 1 4 2.0 2.0 0.01 1000; rows 1 1019 1 4 2.0 2.0 0.01 100; } | res "$T/rank20/run.s20/resources.tsv"
{ rows 20 1000 1 4 2.0 2.0 0.01 1000; rows 1 1020 1 4 2.0 2.0 0.01 100; } | res "$T/rank20/run.s21/resources.tsv"
rows 20 1000 1 4 2.0 2.0 0.01 1000 | res "$T/rank20/run.a1/resources.tsv"
touch -d "@1700000000" "$T/rank20/run.s21/resources.tsv"
touch -d "@1800000000" "$T/rank20/run.a1/resources.tsv"
judge window --resources "$T/rank20/run.a1/resources.tsv" --yml "$FIX"
has "a file of 21 samples: rank 2, its one 100 µs sample is not the floor; one of 20 is" "ref 100µs from run.s20 (min of 3 files)" "$OUT"
rm -f "$T/rank20/run.s20/resources.tsv"
judge window --resources "$T/rank20/run.a1/resources.tsv" --yml "$FIX"
has "a file of 21 samples, its 100 µs sample alone: rank 2, the reference stays 1000 µs" "ref 1000µs from this file (min of 2 files)" "$OUT"
# spin_us NA rows are not counted in n: 20 numeric samples among 30 NA rows still floor at their least.
{ rows 19 1000 1 4 2.0 2.0 0.01 1000; rows 1 1019 1 4 2.0 2.0 0.01 100; rows 30 1020 1 4 2.0 2.0 0.01 NA; } | res "$T/nacount/run.n1/resources.tsv"
rows 20 1000 1 4 2.0 2.0 0.01 1000 | res "$T/nacount/run.a1/resources.tsv"
touch -d "@1800000000" "$T/nacount/run.a1/resources.tsv"
judge window --resources "$T/nacount/run.a1/resources.tsv" --yml "$FIX"
has "NA spin_us rows are not counted in n: 20 numeric samples floor at their least" "ref 100µs from run.n1 (min of 2 files)" "$OUT"
rows 10 1000 1 4 2.0 2.0 0.01 NA | res "$T/naonly/run.n1/resources.tsv"
rows 10 1000 1 4 2.0 2.0 0.01 1000 | res "$T/naonly/run.a1/resources.tsv"
touch -d "@1800000000" "$T/naonly/run.a1/resources.tsv"
judge window --resources "$T/naonly/run.a1/resources.tsv" --yml "$FIX"
has "a sibling with no numeric spin_us gives no floor and is not counted" "ref 1000µs from this file)" "$OUT"
lacks "a sibling with no numeric spin_us gives no floor: no min-of count" "min of" "$OUT"

# A sibling that is not a resources.tsv is skipped with a stderr line; the judge still answers.
mkdir -p "$T/bad/run.z1" "$T/bad/run.z2"
rows 6 1000 1 4 2.0 2.0 0.01 700 | res "$T/bad/run.z1/resources.tsv"
printf 'not a header\n' > "$T/bad/run.z2/resources.tsv"
judge window --resources "$T/bad/run.z1/resources.tsv" --yml "$FIX"
has "a malformed sibling: skipped, named on stderr" "sibling $T/bad/run.z2/resources.tsv skipped" "$ERR"
eq "a malformed sibling: the verdict is still given, exit 0" "CODE 0" "$(word) $RC"

# --- run delay ---------------------------------------------------------------
judge window --resources "$T/quiet/run.q1/resources.tsv" --run-delay 6 --cpu 10 --yml "$FIX"
eq "run delay: 6/10 crosses" CONTENTION "$(word)"
has "run delay: run-delay 0.60 against its line" "run-delay 0.60 (≥ 0.5)" "$OUT"
judge window --resources "$T/quiet/run.q1/resources.tsv" --run-delay 2 --cpu 10 --yml "$FIX"
has "run delay: 2/10 stays under" "run-delay 0.20 (< 0.5)" "$OUT"
eq "run delay: under its line the window is CODE" CODE "$(word)"
judge window --resources "$T/quiet/run.q1/resources.tsv" --run-delay 6 --yml "$FIX"
eq "run delay without --cpu: exit 2" 2 "$RC"
has "run delay without --cpu: the line names both flags" "--run-delay and --cpu" "$ERR"
judge window --resources "$T/quiet/run.q1/resources.tsv" --cpu 10 --yml "$FIX"
eq "--cpu without --run-delay: exit 2" 2 "$RC"
judge window --resources "$T/quiet/run.q1/resources.tsv" --run-delay 6 --cpu 0 --yml "$FIX"
has "run delay over 0 cpu-s: NA with its reason, not a division" "run-delay NA (cpu is 0 s)" "$OUT"

# --- short window ------------------------------------------------------------
judge window --resources "$T/quiet/run.q1/resources.tsv" --from 1000 --to 1002 --yml "$FIX"
eq "short window: three rows < min_samples 4 is not measured" "not measured${TAB}tick-deficit NA (3 samples < 4) · cpu-psi NA (3 samples < 4) · spin NA (3 samples < 4) · run-delay NA (not given)" "$OUT"
judge window --resources "$T/quiet/run.q1/resources.tsv" --from 1000 --to 1003 --yml "$FIX"
eq "short window: exactly min_samples rows is measured (bounds are inclusive)" CODE "$(word)"
judge window --resources "$T/quiet/run.q1/resources.tsv" --from 2000 --to 3000 --yml "$FIX"
has "short window: no row inside reads 0 samples" "tick-deficit NA (0 samples < 4)" "$OUT"
judge window --resources "$T/starved/run.s1/resources.tsv" --from 1000 --to 1002 --run-delay 6 --cpu 10 --yml "$FIX"
eq "short window: a given run-delay is still judged" CONTENTION "$(word)"
# a signal whose column is NA in every row is NA, naming the column; the rest stay measured
rows 10 1000 1 4 2.0 2.0 0.03 1000 | awk -F'\t' -v OFS='\t' '{ $11 = "NA"; print }' | res "$T/napsi/run.p1/resources.tsv"
judge window --resources "$T/napsi/run.p1/resources.tsv" --yml "$FIX"
has "a column NA in every row: the signal is NA, named" "cpu-psi NA (0 samples < 4, cg_psi_cpu_some_s NA in 10 of 10 rows)" "$OUT"
eq "a column NA in every row: the other signals decide" CODE "$(word)"

# --- missing resources -------------------------------------------------------
judge window --resources "$T/none/resources.tsv" --yml "$FIX"
eq "resources absent: not measured, the path in the evidence, exit 0" "not measured${TAB}$T/none/resources.tsv absent 0" "$OUT $RC"
mkdir -p "$T/none" && : > "$T/none/resources.tsv"
judge window --resources "$T/none/resources.tsv" --yml "$FIX"
eq "resources empty: not measured, the path in the evidence, exit 0" "not measured${TAB}$T/none/resources.tsv empty 0" "$OUT $RC"
printf '%s\n' "$HEADER" > "$T/none/resources.tsv"
judge window --resources "$T/none/resources.tsv" --yml "$FIX"
eq "a header and no row: every signal NA, not measured" "not measured 0" "$(word) $RC"

# --- batch -------------------------------------------------------------------
printf 'quiet\t1000\t1009\nstarved\t2000\t2009\t2\t10\nshort\t1000\t1002\n' > "$T/w3.tsv"
{ rows 10 1000 1 4 2.0 2.0 0.03 1000; rows 10 2000 1 4 4.0 3.4 0.01 1000; } | res "$T/batch/run.b1/resources.tsv"
judge windows --resources "$T/batch/run.b1/resources.tsv" --windows "$T/w3.tsv" --yml "$FIX"
eq "batch: 3 windows, 3 rows, exit 0" "3 0" "$(printf '%s\n' "$OUT" | wc -l | tr -d ' ') $RC"
eq "batch: labels and verdicts in input order" "quiet:CODE starved:CONTENTION short:not measured" "$(printf '%s\n' "$OUT" | awk -F'\t' '{printf "%s%s:%s", (NR > 1 ? " " : ""), $1, $2}')"
has "batch: a row carries its own evidence, its run-delay included" "starved${TAB}CONTENTION${TAB}tick-deficit -15.0% (≤ -5%)" "$OUT"
has "batch: the run-delay columns of a row are judged" "run-delay 0.20 (< 0.5)" "$OUT"
printf 'only\t1000\t1009\n' > "$T/w1.tsv"
judge windows --resources "$T/batch/run.b1/resources.tsv" --windows "$T/w1.tsv" --yml "$FIX"
single=$OUT
judge window --resources "$T/batch/run.b1/resources.tsv" --from 1000 --to 1009 --yml "$FIX"
eq "batch row equals the single window's verdict and evidence" "$single" "only${TAB}$OUT"
printf 'a\t1000\t1009\nb\t1000\n' > "$T/wbad.tsv"
judge windows --resources "$T/batch/run.b1/resources.tsv" --windows "$T/wbad.tsv" --yml "$FIX"
eq "batch, a malformed row: exit 2, no verdict printed" "2 []" "$RC [$OUT]"
has "batch, a malformed row: the line names the file and line 2" "$T/wbad.tsv:2" "$ERR"
printf 'a\t1000\tsoon\n' > "$T/wbad.tsv"
judge windows --resources "$T/batch/run.b1/resources.tsv" --windows "$T/wbad.tsv" --yml "$FIX"
eq "batch, a non-numeric bound: exit 2 naming line 1" "2 yes" "$RC $([[ $ERR == *"$T/wbad.tsv:1"* ]] && echo yes || echo no)"
printf 'a\t1009\t1000\n' > "$T/wbad.tsv"
judge windows --resources "$T/batch/run.b1/resources.tsv" --windows "$T/wbad.tsv" --yml "$FIX"
eq "batch, a window ending before it starts: exit 2" 2 "$RC"
printf 'a\t1000\t1009\t5\tNA\n' > "$T/wbad.tsv"
judge windows --resources "$T/batch/run.b1/resources.tsv" --windows "$T/wbad.tsv" --yml "$FIX"
eq "batch, run_delay_s without cpu_s: exit 2" 2 "$RC"
printf 'a\t1000\t1009\tNA\tNA\n\n' > "$T/wna.tsv"
judge windows --resources "$T/batch/run.b1/resources.tsv" --windows "$T/wna.tsv" --yml "$FIX"
has "batch, NA run-delay columns and a blank line: the run-delay is not given" "run-delay NA (not given)" "$OUT"
judge windows --resources "$T/none/absent.tsv" --windows "$T/w3.tsv" --yml "$FIX"
eq "batch with resources absent: every row not measured, exit 0" "3 0 $T/none/absent.tsv absent" "$(printf '%s\n' "$OUT" | grep -c "${TAB}not measured${TAB}") $RC ${OUT##*"$TAB"}"
judge windows --resources "$T/batch/run.b1/resources.tsv" --windows "$T/none/missing-windows.tsv" --yml "$FIX"
eq "batch, windows file missing: exit 2" 2 "$RC"

# --- bad header / malformed resources ---------------------------------------
{ printf 'epoch_s\tuptime_s\n'; } > "$T/hdr.tsv"
judge window --resources "$T/hdr.tsv"
eq "bad header: exit 2, no verdict" "2 []" "$RC [$OUT]"
has "bad header: stderr names the file" "$T/hdr.tsv" "$ERR"
{ printf '%s\n' "${HEADER/uptime_s/uptime}"; rows 5 1000 1 4 2.0 2.0 0.03 1000; } > "$T/hdr.tsv"
judge window --resources "$T/hdr.tsv"
eq "bad header: a renamed column is exit 2 too" 2 "$RC"
{ printf '%s\n' "$HEADER"; rows 3 1000 1 4 2.0 2.0 0.03 1000; printf '1003.0\t1003.0\n'; } > "$T/hdr.tsv"
judge window --resources "$T/hdr.tsv"
eq "a row of the wrong width: exit 2 naming its line" "2 yes" "$RC $([[ $ERR == *"$T/hdr.tsv:5"* ]] && echo yes || echo no)"
{ printf '%s\n' "$HEADER"; rows 3 1000 1 4 2.0 2.0 0.03 1000 | awk -F'\t' -v OFS='\t' 'NR == 2 { $5 = "four" } { print }'; } > "$T/hdr.tsv"
judge window --resources "$T/hdr.tsv"
eq "a cell that is neither a number nor NA: exit 2 naming its line" "2 yes" "$RC $([[ $ERR == *"$T/hdr.tsv:3"* && $ERR == *cpus* ]] && echo yes || echo no)"

# --- thresholds --------------------------------------------------------------
eq "thresholds: the five keys" 5 "$(grep -Ec '^(tick_deficit_max|cpu_psi_share_min|spin_ratio_min|run_delay_ratio_min|min_samples):' "$YML")"
has "thresholds: tick_deficit_max carries its evidence comment" "run.vHOMER" "$(grep -B12 '^tick_deficit_max' "$YML")"
has "thresholds: the comment names the quiet gates too" "run.VTFyAw" "$(grep -B12 '^tick_deficit_max' "$YML")"
for key in tick_deficit_max cpu_psi_share_min spin_ratio_min run_delay_ratio_min min_samples; do
  grep -v "^$key:" "$FIX" > "$T/missing.yml"
  judge window --resources "$T/quiet/run.q1/resources.tsv" --yml "$T/missing.yml"
  eq "thresholds, $key missing: exit 2 naming the key" "2 yes" "$RC $([[ $ERR == *"missing key $key"* ]] && echo yes || echo no)"
done
sed 's/^spin_ratio_min:.*/spin_ratio_min: fast/' "$FIX" > "$T/nan.yml"
judge window --resources "$T/quiet/run.q1/resources.tsv" --yml "$T/nan.yml"
eq "thresholds, a non-number: exit 2 naming the key and line" "2 yes" "$RC $([[ $ERR == *spin_ratio_min* && $ERR == *"nan.yml:"* ]] && echo yes || echo no)"
sed 's/^min_samples:.*/min_samples: 2.5/' "$FIX" > "$T/frac.yml"
judge window --resources "$T/quiet/run.q1/resources.tsv" --yml "$T/frac.yml"
eq "thresholds, a fractional min_samples: exit 2" 2 "$RC"
judge window --resources "$T/quiet/run.q1/resources.tsv" --yml "$T/no-such.yml"
eq "thresholds, unreadable file: exit 2 naming it" "2 yes" "$RC $([[ $ERR == *"$T/no-such.yml"* ]] && echo yes || echo no)"
judge window --resources "$T/none/absent.tsv" --yml "$T/no-such.yml"
eq "thresholds, unreadable with resources absent: still exit 2, never a quiet not-measured" 2 "$RC"
printf 'tick_deficit_max -0.05\n' > "$T/junk.yml"
judge window --resources "$T/quiet/run.q1/resources.tsv" --yml "$T/junk.yml"
eq "thresholds, a line that is not key: value: exit 2 naming line 1" "2 yes" "$RC $([[ $ERR == *"junk.yml:1"* ]] && echo yes || echo no)"
{ cat "$FIX"; echo 'spin_ratio_min: 3'; } > "$T/dup.yml"
judge window --resources "$T/quiet/run.q1/resources.tsv" --yml "$T/dup.yml"
eq "thresholds, a key given twice: exit 2" 2 "$RC"
sed 's/^tick_deficit_max:.*/tick_deficit_max: -0.20/' "$FIX" > "$T/loose.yml"
judge window --resources "$T/starved/run.s1/resources.tsv" --yml "$T/loose.yml"
eq "thresholds are read from the file: -15 % under a -20 % line is CODE" CODE "$(word)"
has "thresholds are read from the file: the line shown is the file's" "tick-deficit -15.0% (> -20%)" "$OUT"
sed 's/^min_samples:.*/min_samples: 11/' "$FIX" > "$T/minsamples.yml"
judge window --resources "$T/starved/run.s1/resources.tsv" --yml "$T/minsamples.yml"
has "min_samples comes from the file: 10 rows < 11" "tick-deficit NA (10 samples < 11)" "$OUT"
judge window --resources "$T/quiet/run.q1/resources.tsv" --yml "$YML"
REAL_LINE=$OUT
if [[ $RC == 0 && -n $REAL_LINE ]]; then
  ok "the real thresholds file: the judge accepts it, exit 0"
else
  bad "the real thresholds file: the judge accepts it, exit 0" "expected: exit 0 and a verdict line" "actual:   exit $RC, stdout [$REAL_LINE], stderr [$ERR]"
fi
OUT=$(cd "$T" && bash "$SUT" window --resources "$T/quiet/run.q1/resources.tsv" 2>&1)
eq "--yml defaults to pfm/.testcontention.yml, from any directory" "$REAL_LINE" "$OUT"

# --- usage ------------------------------------------------------------------
judge
eq "no subcommand: exit 2, a usage line" "2 yes" "$RC $([[ $ERR == *usage* ]] && echo yes || echo no)"
judge frobnicate --resources x
eq "unknown subcommand: exit 2" 2 "$RC"
judge window
eq "window without --resources: exit 2" 2 "$RC"
judge window --resources "$T/quiet/run.q1/resources.tsv" --bogus 1
eq "unknown flag: exit 2" 2 "$RC"
judge window --resources "$T/quiet/run.q1/resources.tsv" --from soon
eq "non-numeric --from: exit 2" 2 "$RC"
judge window --resources "$T/quiet/run.q1/resources.tsv" --from 1005 --to 1001
eq "--to before --from: exit 2" 2 "$RC"
judge window --resources "$T/quiet/run.q1/resources.tsv" --from
eq "a flag without its value: exit 2" 2 "$RC"
judge windows --resources "$T/quiet/run.q1/resources.tsv"
eq "windows without --windows: exit 2" 2 "$RC"
judge window --resources "$T/quiet/run.q1/resources.tsv" --windows "$T/w3.tsv"
eq "window given --windows: exit 2" 2 "$RC"

shtest_end

#!/usr/bin/env bash
# sampler.sh: one case per behaviour of the gate-wide resource sampler, run
# against fixture cgroup and /proc trees (SAMPLER_CGROUP_DIR, SAMPLER_PROC_DIR)
# at SAMPLER_INTERVAL_S=0.1. Linux only (a real /proc for the --parent watch);
# runs in the fence.
set -uo pipefail
SHTEST_TAG=sampler-test
# shellcheck source=../../../../scripts/shtest.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"
shtest_unset_gate_env # run nested under the gate's own stepprof wrapper, the suite starts from none of its knobs
SUT="${SAMPLER_SUT:-$(dirname -- "${BASH_SOURCE[0]}")/../../sampler.sh}"
TAB=$'\t'

HEADER="epoch_s${TAB}uptime_s${TAB}cg_cpu_s${TAB}vm_busy_s${TAB}cpus${TAB}cg_io_read_mb${TAB}cg_io_write_mb${TAB}cg_mem_mb${TAB}cg_mem_peak_mb${TAB}cg_swap_mb${TAB}cg_psi_cpu_some_s${TAB}cg_psi_io_some_s${TAB}cg_psi_io_full_s${TAB}cg_psi_mem_some_s${TAB}cg_psi_mem_full_s${TAB}vm_psi_cpu_some_s${TAB}vm_psi_io_some_s${TAB}vm_psi_io_full_s${TAB}vm_psi_mem_some_s${TAB}vm_psi_mem_full_s${TAB}spin_us${TAB}load1"
export SAMPLER_INTERVAL_S=0.1
export SAMPLER_CGROUP_DIR=$T/cg SAMPLER_PROC_DIR=$T/proc
TCK=$(getconf CLK_TCK)

eq() { # eq <name> <expected> <actual>
  if [[ $3 == "$2" ]]; then ok "$1"; else bad "$1" "expected: $2" "actual:   $3"; fi
}
psi() { printf '%s avg10=0.00 avg60=0.00 avg300=0.00 total=%d\n' "$2" "$3" >> "$1"; } # psi <file> <some|full> <µs>

fixtures() { # the fixture trees, every value distinct so a column cannot be read from another's source
  rm -rf "$T/cg" "$T/proc"
  mkdir -p "$T/cg" "$T/proc/pressure"
  printf 'usage_usec 2500000\nuser_usec 1000000\nsystem_usec 1500000\n' > "$T/cg/cpu.stat"
  printf '8:0 rbytes=1048576 wbytes=2097152 rios=1 wios=2 dbytes=0 dios=0\n8:16 rbytes=3145728 wbytes=1048576 rios=3 wios=1 dbytes=0 dios=0\n' > "$T/cg/io.stat"
  echo 104857600 > "$T/cg/memory.current"
  echo 209715200 > "$T/cg/memory.peak"
  echo 5242880 > "$T/cg/memory.swap.current"
  : > "$T/cg/cpu.pressure"; psi "$T/cg/cpu.pressure" some 1234567; psi "$T/cg/cpu.pressure" full 111
  : > "$T/cg/io.pressure"; psi "$T/cg/io.pressure" some 2000000; psi "$T/cg/io.pressure" full 500000
  : > "$T/cg/memory.pressure"; psi "$T/cg/memory.pressure" some 300000; psi "$T/cg/memory.pressure" full 100000
  : > "$T/proc/pressure/cpu"; psi "$T/proc/pressure/cpu" some 7000000; psi "$T/proc/pressure/cpu" full 222
  : > "$T/proc/pressure/io"; psi "$T/proc/pressure/io" some 8000000; psi "$T/proc/pressure/io" full 900000
  : > "$T/proc/pressure/memory"; psi "$T/proc/pressure/memory" some 4000000; psi "$T/proc/pressure/memory" full 600000
  printf 'cpu  100 20 300 4000 50 6 7 8 0 0\ncpu0 25 5 75 1000 12 1 2 2 0 0\ncpu1 25 5 75 1000 12 1 2 2 0 0\ncpu2 25 5 75 1000 13 2 2 2 0 0\ncpu3 25 5 75 1000 13 2 1 2 0 0\nintr 1 2 3\nctxt 4\n' > "$T/proc/stat"
  echo '1000.00 3000.00' > "$T/proc/uptime"
  echo '0.52 0.40 0.30 1/200 12345' > "$T/proc/loadavg"
}

lines() { # lines <var> <file>: the file's line count, without a process
  local -a all=()
  mapfile -t all 2> /dev/null < "$2"
  printf -v "$1" '%d' "${#all[@]}"
}
wait_lines() { # wait_lines <file> <count>: at least <count> lines in the file, a 10 s bound in 0.1 s ticks
  local n=0 tick
  for ((tick = 0; tick < 100; tick++)); do
    lines n "$1"
    ((n >= $2)) && return 0
    sleep 0.1
  done
  return 1
}
wait_exit() { # wait_exit <pid> <ticks>: the pid is gone within <ticks> 0.1 s ticks
  local tick
  for ((tick = 0; tick < $2; tick++)); do
    kill -0 "$1" 2> /dev/null || return 0
    sleep 0.1
  done
  return 1
}
# stop_sampler <pid> <signal>: send it, wait up to 3 s for the exit, KILL a sampler that ignored it;
# GONE is yes when the signal alone ended it, RC is its exit status — a hung sampler cannot hang the suite
stop_sampler() {
  kill "-$2" "$1" 2> /dev/null
  if wait_exit "$1" 30; then GONE=yes; else GONE=no; kill -KILL "$1" 2> /dev/null; fi
  wait "$1" 2> /dev/null
  RC=$?
}
col() { # col <file> <column>: that column of every data row, one per line
  awk -F'\t' -v c="$2" 'NR == 1 { for (i = 1; i <= NF; i++) if ($i == c) k = i; next } { print $k }' "$1"
}
uniq_col() { col "$1" "$2" | sort -u | paste -sd, -; }

# capture <name>: run the sampler into $T/<name>/resources.tsv until it has three rows, stop it
# with TERM; RC is its exit status
capture() {
  mkdir -p "$T/$1"
  rm -f "$T/$1/resources.tsv"
  bash "$SUT" run --out "$T/$1/resources.tsv" 2> "$T/$1/stderr" &
  local pid=$!
  wait_lines "$T/$1/resources.tsv" 4
  stop_sampler "$pid" TERM
}

# --- sampling ---------------------------------------------------------------
fixtures
before=$(date +%s)
R=$T/s1/resources.tsv
mkdir -p "$T/s1"
bash "$SUT" run --out "$R" 2> "$T/s1.stderr" &
pid=$!
wait_lines "$R" 3 && alive=yes || alive=no
kill -0 "$pid" 2> /dev/null && running=yes || running=no
eq "sampling: rows are visible while the sampler runs (flushed as written)" "yes yes" "$alive $running"
sleep 1.0
stop_sampler "$pid" TERM
after=$(date +%s)
eq "sampling: the file starts with the contract header" "$HEADER" "$(head -n 1 "$R")"
lines n "$R"
if ((n >= 7 && n <= 16)); then ok "sampling: a row about every interval ($((n - 1)) rows in ~1.2 s at 0.1 s)"; else bad "sampling: a row about every interval" "$n lines in $R"; fi
eq "sampling: every row has the 22 columns" 22 "$(awk -F'\t' '{print NF}' "$R" | sort -u | paste -sd, -)"
eq "sampling: uptime_s is /proc/uptime field 1" 1000.000000 "$(uniq_col "$R" uptime_s)"
epoch=$(uniq_col "$R" epoch_s)
if [[ $epoch =~ ^[0-9]+\.[0-9]{6}$ ]] && awk -v e="$epoch" -v a="$before" -v b="$after" 'BEGIN { exit !(e >= a - 1 && e <= b + 1) }'; then
  ok "sampling: epoch_s is the epoch at start (/proc/uptime did not move)"
else bad "sampling: epoch_s is the epoch at start" "epoch_s: $epoch, start between $before and $after"; fi
eq "sampling: cg_cpu_s is cpu.stat usage_usec" 2.500000 "$(uniq_col "$R" cg_cpu_s)"
eq "sampling: vm_busy_s is /proc/stat fields 1,2,3,6,7,8 over CLK_TCK" "$(awk -v t="$TCK" 'BEGIN { printf "%.6f", (100 + 20 + 300 + 6 + 7 + 8) / t }')" "$(uniq_col "$R" vm_busy_s)"
eq "sampling: cpus counts the cpuN rows" 4 "$(uniq_col "$R" cpus)"
eq "sampling: cg_io_read_mb sums rbytes over the devices" 4.000 "$(uniq_col "$R" cg_io_read_mb)"
eq "sampling: cg_io_write_mb sums wbytes over the devices" 3.000 "$(uniq_col "$R" cg_io_write_mb)"
eq "sampling: cg_mem_mb is memory.current in MiB" 100.000 "$(uniq_col "$R" cg_mem_mb)"
eq "sampling: cg_mem_peak_mb is memory.peak in MiB" 200.000 "$(uniq_col "$R" cg_mem_peak_mb)"
eq "sampling: cg_swap_mb is memory.swap.current in MiB" 5.000 "$(uniq_col "$R" cg_swap_mb)"
eq "sampling: cg pressure columns are the total= of each line, in seconds" "1.234567 2.000000 0.500000 0.300000 0.100000" \
  "$(for c in cg_psi_cpu_some_s cg_psi_io_some_s cg_psi_io_full_s cg_psi_mem_some_s cg_psi_mem_full_s; do uniq_col "$R" "$c"; done | paste -sd' ' -)"
eq "sampling: vm pressure columns are /proc/pressure totals, in seconds" "7.000000 8.000000 0.900000 4.000000 0.600000" \
  "$(for c in vm_psi_cpu_some_s vm_psi_io_some_s vm_psi_io_full_s vm_psi_mem_some_s vm_psi_mem_full_s; do uniq_col "$R" "$c"; done | paste -sd' ' -)"
eq "sampling: load1 is /proc/loadavg field 1" 0.52 "$(uniq_col "$R" load1)"
if [[ $(col "$R" spin_us | grep -Evc '^[0-9]+$') == 0 && $(col "$R" spin_us | sort -n | head -n 1) -gt 0 ]]; then ok "sampling: spin_us is a positive integer in every row"; else bad "sampling: spin_us" "$(col "$R" spin_us | paste -sd, -)"; fi
eq "sampling: nothing read failed, so there is no resources.err" "no" "$([[ -e $T/s1/resources.err ]] && echo yes || echo no)"
eq "sampling: nothing on stderr" "" "$(cat "$T/s1.stderr")"

# a source read per row follows the file: a counter that rises is read again
fixtures
mkdir -p "$T/s2"
bash "$SUT" run --out "$T/s2/resources.tsv" 2> /dev/null &
pid=$!
wait_lines "$T/s2/resources.tsv" 3
printf 'usage_usec 9000000\n' > "$T/cg/cpu.stat.new" && mv "$T/cg/cpu.stat.new" "$T/cg/cpu.stat"
wait_lines "$T/s2/resources.tsv" 8
stop_sampler "$pid" TERM
eq "sampling: a source is read at every row (cg_cpu_s moved from 2.5 to 9)" "2.500000,9.000000" "$(uniq_col "$T/s2/resources.tsv" cg_cpu_s)"

# --- spin_us is the time of SAMPLER_SPIN_ITERS loop iterations --------------
fixtures
SAMPLER_SPIN_ITERS=1000 capture sp1
spin_t0=$EPOCHREALTIME
SAMPLER_SPIN_ITERS=200000 capture sp2
spin_t1=$EPOCHREALTIME
small=$(col "$T/sp1/resources.tsv" spin_us | sort -n | sed -n 2p)
large=$(col "$T/sp2/resources.tsv" spin_us | sort -n | sed -n 2p)
spent=$(col "$T/sp2/resources.tsv" spin_us | awk '{ sum += $1 } END { print sum }')
wall=$(( ${spin_t1/./} - ${spin_t0/./} ))
if ((spent <= wall && spent * 10 >= wall)); then ok "spin: spin_us is wall microseconds (${spent} µs of spinning in a ${wall} µs run)"; else bad "spin: spin_us is wall microseconds" "spin_us summed to ${spent} µs in a ${wall} µs run"; fi
if ((large > small * 10)); then ok "spin: 200x the iterations take well over 10x the time ($small µs -> $large µs)"; else bad "spin: iterations scale spin_us" "1000 iterations: $small µs, 200000: $large µs"; fi

# --- stop ---------------------------------------------------------------------
fixtures
mkdir -p "$T/st"
SAMPLER_INTERVAL_S=60 bash "$SUT" run --out "$T/st/resources.tsv" 2> "$T/st.stderr" &
pid=$!
wait_lines "$T/st/resources.tsv" 2
lines first "$T/st/resources.tsv"
stop_sampler "$pid" TERM
lines last "$T/st/resources.tsv"
eq "stop: TERM exits 0 within 3 s although the interval is 60 s" "yes 0" "$GONE $RC"
eq "stop: TERM writes one final row" "2 3" "$first $last"
eq "stop: the final row is complete" 22 "$(awk -F'\t' 'END { print NF }' "$T/st/resources.tsv")"
# a background job of a script ignores INT unless job control is on, as it is for an interactive Ctrl-C
set -m
SAMPLER_INTERVAL_S=60 bash "$SUT" run --out "$T/st/int.tsv" 2> /dev/null &
pid=$!
set +m
wait_lines "$T/st/int.tsv" 2
stop_sampler "$pid" INT
lines last "$T/st/int.tsv"
eq "stop: INT is a stop too: exit 0, one final row" "yes 0 3" "$GONE $RC $last"

# --- parent gone --------------------------------------------------------------
fixtures
mkdir -p "$T/pg"
sleep 600 &
parent=$!
SAMPLER_INTERVAL_S=0.3 bash "$SUT" run --out "$T/pg/resources.tsv" --parent "$parent" 2> "$T/pg.stderr" &
pid=$!
wait_lines "$T/pg/resources.tsv" 3
sleep 0.7
kill -0 "$pid" 2> /dev/null && alive=yes || alive=no
lines before "$T/pg/resources.tsv"
t0=$EPOCHREALTIME
kill "$parent"; wait "$parent" 2> /dev/null
wait_exit "$pid" 30 && gone=yes || gone=no
t1=$EPOCHREALTIME
[[ $gone == yes ]] || kill -KILL "$pid" 2> /dev/null
wait "$pid"; rc=$?
lines after "$T/pg/resources.tsv"
waited=$(( (${t1/./} - ${t0/./}) / 1000 ))
eq "parent gone: the sampler runs while the parent lives" yes "$alive"
eq "parent gone: the sampler exits 0 once the parent is gone" "yes 0" "$gone $rc"
if ((waited <= 2000)); then ok "parent gone: it exits within two intervals (${waited} ms of 0.3 s intervals, 2 s bound for load)"; else bad "parent gone: exits within two intervals" "${waited} ms"; fi
if ((after > before)); then ok "parent gone: a final row was written after the parent died"; else bad "parent gone: a final row" "rows $before -> $after"; fi
eq "parent gone: nothing on stderr" "" "$(cat "$T/pg.stderr")"
# a zombie parent (exited, not reaped) is gone too
bash -c 'sleep 0.2 & echo $!; exec sleep 600' > "$T/zombie.pid" &
holder=$!
wait_lines "$T/zombie.pid" 1
zpid=$(< "$T/zombie.pid")
sleep 0.5
SAMPLER_INTERVAL_S=0.3 bash "$SUT" run --out "$T/pg/zombie.tsv" --parent "$zpid" 2> /dev/null &
pid=$!
wait_exit "$pid" 30 && gone=yes || gone=no
[[ $gone == yes ]] || kill -KILL "$pid" 2> /dev/null
wait "$pid"; rc=$?
kill "$holder" 2> /dev/null; wait "$holder" 2> /dev/null
eq "parent gone: an exited, unreaped (zombie) parent counts as gone" "yes 0" "$gone $rc"

# --- missing source -----------------------------------------------------------
fixtures
rm "$T/cg/memory.peak"
capture m1
eq "missing source: memory.peak absent reads cg_mem_peak_mb NA in every row" NA "$(uniq_col "$T/m1/resources.tsv" cg_mem_peak_mb)"
eq "missing source: exit 0, the other memory columns still read" "0 100.000 5.000" "$RC $(uniq_col "$T/m1/resources.tsv" cg_mem_mb) $(uniq_col "$T/m1/resources.tsv" cg_swap_mb)"
lines rows "$T/m1/resources.tsv"
eq "missing source: resources.err holds the column and its reason once, in $((rows - 1)) rows" "1 cg_mem_peak_mb" "$(grep -c '' "$T/m1/resources.err") $(cut -f1 "$T/m1/resources.err")"
eq "missing source: the reason names the path, tab separated" "yes" "$([[ $(cut -f2 "$T/m1/resources.err") == "$T/cg/memory.peak"* ]] && echo yes || echo no)"
eq "missing source: never 0" "no" "$(col "$T/m1/resources.tsv" cg_mem_peak_mb | grep -q '^0' && echo yes || echo no)"
# every source, removed one at a time: exactly its columns go NA, each logged once
check_missing() { # check_missing <source path under a root> <column>...
  local file=$1
  shift
  fixtures
  rm "$file"
  rm -rf "$T/mm"
  capture mm
  local na="" c
  for c in "$@"; do na+="$c=$(uniq_col "$T/mm/resources.tsv" "$c") "; done
  local want=""
  for c in "$@"; do want+="$c=NA "; done
  local logged
  logged=$(cut -f1 "$T/mm/resources.err" 2> /dev/null | sort | paste -sd' ' -)
  local expect
  expect=$(printf '%s\n' "$@" | sort | paste -sd' ' -)
  if [[ $na == "$want" && $RC == 0 && $logged == "$expect" ]]; then
    ok "missing source: ${file#"$T"/} gone -> $* NA, logged once each"
  else bad "missing source: ${file#"$T"/}" "columns: $na" "logged: $logged (want $expect), rc $RC"; fi
  # no other column is NA
  local others
  others=$(awk -F'\t' 'NR == 1 { for (i = 1; i <= NF; i++) h[i] = $i; next } { for (i = 1; i <= NF; i++) if ($i == "NA") print h[i] }' "$T/mm/resources.tsv" | sort -u | paste -sd' ' -)
  [[ $others == "$expect" ]] && ok "missing source: ${file#"$T"/} gone -> no other column is NA" || bad "missing source: ${file#"$T"/} -> other NA columns" "NA columns: $others"
}
check_missing "$T/cg/cpu.stat" cg_cpu_s
check_missing "$T/cg/io.stat" cg_io_read_mb cg_io_write_mb
check_missing "$T/cg/memory.current" cg_mem_mb
check_missing "$T/cg/memory.swap.current" cg_swap_mb
check_missing "$T/cg/cpu.pressure" cg_psi_cpu_some_s
check_missing "$T/cg/io.pressure" cg_psi_io_some_s cg_psi_io_full_s
check_missing "$T/cg/memory.pressure" cg_psi_mem_some_s cg_psi_mem_full_s
check_missing "$T/proc/stat" vm_busy_s cpus
check_missing "$T/proc/loadavg" load1
check_missing "$T/proc/pressure/cpu" vm_psi_cpu_some_s
check_missing "$T/proc/pressure/io" vm_psi_io_some_s vm_psi_io_full_s
check_missing "$T/proc/pressure/memory" vm_psi_mem_some_s vm_psi_mem_full_s
# a source that exists but cannot be parsed is NA with a reason, never 0
fixtures
echo "max" > "$T/cg/memory.current"
printf 'some avg10=0.00\n' > "$T/cg/io.pressure"
capture m2
eq "missing source: a malformed memory.current is NA" NA "$(uniq_col "$T/m2/resources.tsv" cg_mem_mb)"
eq "missing source: a pressure file without total= is NA for both lines" "NA NA" "$(uniq_col "$T/m2/resources.tsv" cg_psi_io_some_s) $(uniq_col "$T/m2/resources.tsv" cg_psi_io_full_s)"
eq "missing source: each malformed column is logged once with a reason" "cg_mem_mb cg_psi_io_full_s cg_psi_io_some_s" "$(cut -f1 "$T/m2/resources.err" | sort | paste -sd' ' -)"
# a stale resources.err of an earlier run is not kept
fixtures
mkdir -p "$T/m3"
printf 'cg_cpu_s\tstale\n' > "$T/m3/resources.err"
capture m3
eq "missing source: a stale resources.err is removed when nothing is NA" no "$([[ -e $T/m3/resources.err ]] && echo yes || echo no)"
# an --out that does not end in .tsv logs to <out>.err
fixtures
rm "$T/cg/memory.peak"
mkdir -p "$T/plain"
bash "$SUT" run --out "$T/plain/samples" 2> /dev/null &
pid=$!
wait_lines "$T/plain/samples" 3
stop_sampler "$pid" TERM
eq "missing source: --out without .tsv logs to <out>.err" "cg_mem_peak_mb" "$(cut -f1 "$T/plain/samples.err" 2> /dev/null)"

# --- clock step ---------------------------------------------------------------
# /proc/uptime rises 5 s per 0.05 s of wall time: epoch_s must follow it (it is
# anchored on uptime, never re-read from the wall clock) and never go backwards.
fixtures
mkdir -p "$T/cs"
(
  up=1000
  while :; do
    up=$((up + 5))
    echo "$up.00 3000.00" > "$T/proc/uptime.new" && mv "$T/proc/uptime.new" "$T/proc/uptime"
    sleep 0.05
  done
) &
updater=$!
bash "$SUT" run --out "$T/cs/resources.tsv" 2> /dev/null &
pid=$!
wait_lines "$T/cs/resources.tsv" 6
stop_sampler "$pid" TERM
kill "$updater" 2> /dev/null; wait "$updater" 2> /dev/null
verdict=$(awk -F'\t' 'NR == 2 { e0 = $1; u0 = $2 } NR > 2 { if ($1 < prev) back++; if (($1 - e0) - ($2 - u0) > 0.000002 || ($2 - u0) - ($1 - e0) > 0.000002) off++; if ($2 > u0 + 4) rose++ } { prev = $1 } END { printf "backwards=%d off=%d rose=%d", back, off, rose }' "$T/cs/resources.tsv")
case $verdict in
  "backwards=0 off=0 rose="[1-9]*) ok "clock step: epoch_s rises exactly with /proc/uptime, never backwards ($verdict)" ;;
  *) bad "clock step: epoch_s follows /proc/uptime" "$verdict" "$(head -n 4 "$T/cs/resources.tsv" | cut -f1,2)" ;;
esac

# --- unwritable out -----------------------------------------------------------
fixtures
bash "$SUT" run --out "$T/no/such/dir/resources.tsv" 2> "$T/uw.stderr"
rc=$?
eq "unwritable out: exit 2 before sampling" 2 "$rc"
eq "unwritable out: the line names the path" "yes" "$([[ $(< "$T/uw.stderr") == *"$T/no/such/dir/resources.tsv"* ]] && echo yes || echo no)"
eq "unwritable out: nothing was created" "no" "$([[ -e $T/no ]] && echo yes || echo no)"

# --- usage and knobs -----------------------------------------------------------
bash "$SUT" run 2> "$T/u.stderr"; rc=$?
eq "usage: no --out is exit 64 with a usage line" "64 yes" "$rc $([[ $(< "$T/u.stderr") == *usage* ]] && echo yes || echo no)"
bash "$SUT" run --out "$T/u.tsv" --parent soon 2> /dev/null; rc=$?
eq "usage: a --parent that is not a pid is exit 64" 64 "$rc"
bash "$SUT" run --out "$T/u.tsv" --bogus 2> /dev/null; rc=$?
eq "usage: an unknown argument is exit 64" 64 "$rc"
bash "$SUT" 2> /dev/null; rc=$?
eq "usage: no subcommand is exit 64" 64 "$rc"
SAMPLER_INTERVAL_S=0 bash "$SUT" run --out "$T/u.tsv" 2> "$T/u.stderr"; rc=$?
eq "knobs: a zero interval is exit 2 naming SAMPLER_INTERVAL_S" "2 yes" "$rc $([[ $(< "$T/u.stderr") == *SAMPLER_INTERVAL_S* ]] && echo yes || echo no)"
SAMPLER_SPIN_ITERS=lots bash "$SUT" run --out "$T/u.tsv" 2> "$T/u.stderr"; rc=$?
eq "knobs: a spin count that is not a number is exit 2 naming SAMPLER_SPIN_ITERS" "2 yes" "$rc $([[ $(< "$T/u.stderr") == *SAMPLER_SPIN_ITERS* ]] && echo yes || echo no)"
fixtures
rm "$T/proc/uptime"
bash "$SUT" run --out "$T/u.tsv" 2> "$T/u.stderr"; rc=$?
eq "no time base: /proc/uptime unreadable at start is exit 2 naming it" "2 yes" "$rc $([[ $(< "$T/u.stderr") == *"$T/proc/uptime"* ]] && echo yes || echo no)"

# --- no process per sample --------------------------------------------------------
# PATH holds getconf and rm only (the sampler's two start-up commands): any other
# command a sample starts is "command not found" on stderr and a missing column.
fixtures
mkdir -p "$T/bin"
for tool in getconf rm; do ln -sf "$(command -v "$tool")" "$T/bin/$tool"; done
PATH=$T/bin "$BASH" "$SUT" run --out "$T/np.tsv" 2> "$T/np.stderr" &
pid=$!
wait_lines "$T/np.tsv" 6
stop_sampler "$pid" TERM
eq "no process per sample: with PATH holding only getconf and rm, exit 0, stderr empty, no column NA" "0 [] 0" "$RC [$(cat "$T/np.stderr")] $(grep -c 'NA' "$T/np.tsv")"

shtest_end

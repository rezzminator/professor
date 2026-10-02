#!/usr/bin/env bash
# stepprof.sh: one case per behaviour of the step profiler, function and CLI.
# Linux only (process tree, /proc); runs in the fence. Bounds stay ≤ 3 s and the
# TERM grace is shortened to 5 ticks, so the suite stays under 15 s.
set -uo pipefail
SHTEST_TAG=stepprof-test
# shellcheck source=../../../../scripts/shtest.sh
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"
SUT="${STEPPROF_SUT:-$(dirname -- "${BASH_SOURCE[0]}")/../../stepprof.sh}"
shtest_unset_gate_env # run nested under the gate's own stepprof wrapper, the suite starts from none of its knobs

eq() { # eq <name> <expected> <actual>
  if [[ $3 == "$2" ]]; then ok "$1"; else bad "$1" "expected: $2" "actual:   $3"; fi
}
has() { # has <name> <file> <ERE>: some line of the file matches
  if grep -Eq -- "$3" "$2" 2>/dev/null; then ok "$1"; else bad "$1" "no line matches: $3" "in $2: $(head -c 600 "$2" 2>&1)"; fi
}
lacks() { # lacks <name> <file> <ERE>: no line of the file matches (the file exists)
  if [[ -f $2 ]] && ! grep -Eq -- "$3" "$2"; then ok "$1"; else bad "$1" "a line matches, or the file is missing: $3" "in $2"; fi
}
pv() { awk -F'\t' -v k="$2" '$1 == k {print $2; found = 1; exit} END {exit !found}' "$1"; } # value of a prof.tsv key
keys() { cut -f1 "$1" | paste -sd, -; }
psi_file() { # psi_file <path> <some µs> <full µs>
  printf 'some avg10=0.00 avg60=0.00 avg300=0.00 total=%d\nfull avg10=0.00 avg60=0.00 avg300=0.00 total=%d\n' "$2" "$3" > "$1"
}
us() { local v=${1/./}; echo $((10#$v)); } # decimal epoch seconds → integer µs

# --- sourced quietly -------------------------------------------------------
for mode in 'set -euo pipefail' ':'; do
  if out=$(bash -c "$mode; a=\$(set +o); source \"\$1\"; b=\$(set +o); [[ \$a == \"\$b\" ]] && declare -F stepprof_run > /dev/null" _ "$SUT" 2>&1) && [[ -z $out ]]; then
    ok "sourced in a shell run with '$mode': silent, no option changed, stepprof_run defined"
  else bad "sourced quietly after '$mode'" "rc/output: $out"; fi
done
# shellcheck source=../../stepprof.sh
source "$SUT"

export STEPPROF_PSI_DIR=$T/psi
mkdir -p "$STEPPROF_PSI_DIR"
for r in cpu io memory; do psi_file "$STEPPROF_PSI_DIR/$r" 1000000 400000; done
export STEPPROF_GRACE_TICKS=5

# --- command exits ---------------------------------------------------------
returns3() { return 3; }
stepprof_run "$T/exit" s 10 -- returns3 > "$T/exit.out" 2>&1; rc=$?
P=$T/exit/s.prof.tsv
eq 'command exits: stepprof_run returns the command status' 3 "$rc"
eq 'command exits: prof.tsv keys in contract order' \
  'step,ended,rc,bound_s,start_epoch,finish_epoch,wall_s,children_cpu,cpu_s,trace,err_records,psi_cpu_some_s,psi_cpu_full_s,psi_io_some_s,psi_io_full_s,psi_memory_some_s,psi_memory_full_s' "$(keys "$P")"
eq 'command exits: step, ended, rc, bound_s, trace' 's exit 3 10 err' \
  "$(pv "$P" step) $(pv "$P" ended) $(pv "$P" rc) $(pv "$P" bound_s) $(pv "$P" trace)"
if [[ $(pv "$P" start_epoch) =~ ^[0-9]+\.[0-9]{6}$ && $(pv "$P" finish_epoch) =~ ^[0-9]+\.[0-9]{6}$ && $(pv "$P" wall_s) =~ ^[0-9]+\.[0-9]{6}$ ]] &&
  (($(us "$(pv "$P" finish_epoch)") >= $(us "$(pv "$P" start_epoch)"))); then
  ok 'command exits: epochs and wall_s carry six decimals, finish after start'
else bad 'command exits: epochs and wall_s' "$(cat "$P")"; fi
eq 'command exits: only prof.tsv remains in the out dir (a nonzero return is no ERR record)' 's.prof.tsv' "$(ls "$T/exit")"
eq 'command exits: err_records 0' 0 "$(pv "$P" err_records)"

# --- bound reached ---------------------------------------------------------
mkfifo "$T/ff"
stepprof_run "$T/hang" h 2 -- cat "$T/ff" > "$T/hang.out" 2>&1; rc=$?
P=$T/hang/h.prof.tsv TREE=$T/hang/h.hang/tree.txt
eq 'bound reached: returns 124' 124 "$rc"
eq 'bound reached: ended timeout, rc 124' 'timeout 124' "$(pv "$P" ended) $(pv "$P" rc)"
has 'bound reached: tree.txt names cat, its state and its wchan' "$TREE" '^pid [0-9]+  state [A-Z] cat  wchan [^ ]'
has 'bound reached: tree.txt holds the fifo path in the cmdline' "$TREE" "^  cmd: cat $T/ff "
has 'bound reached: tree.txt names the fifo, nobody holding it, cat awaiting it' "$TREE" "^$T/ff held by: nobody"
has 'bound reached: tree.txt names the process awaiting the fifo' "$TREE" "^$T/ff awaited by .*: [0-9]+\\(cat\\)"
alive=0
for p in $(sed -n 's/^pid \([0-9]*\) .*/\1/p' "$TREE"); do kill -0 "$p" 2>/dev/null && alive=$((alive + 1)); done
eq 'bound reached: no process of the tree is left alive' 0 "$alive"
if (($(us "$(pv "$P" wall_s)") >= 2000000)); then ok 'bound reached: wall_s covers the bound'; else bad 'bound reached: wall_s' "$(pv "$P" wall_s)"; fi

# a tree that ignores TERM is KILLed after the grace ticks; a fifo held open read-write has real holders
mkfifo "$T/ff2"
stepprof_run "$T/kill" k 2 -- bash -c 'trap "" TERM; exec 9<>"$1"; cat "$1"' _ "$T/ff2" > "$T/kill.out" 2>&1; rc=$?
P=$T/kill/k.prof.tsv TREE=$T/kill/k.hang/tree.txt
eq 'TERM ignored: returns 124' 124 "$rc"
if (($(us "$(pv "$P" wall_s)") >= 2500000)); then ok 'TERM ignored: the step ends only after the bound plus STEPPROF_GRACE_TICKS ticks'; else bad 'TERM ignored: wall_s' "$(pv "$P" wall_s)"; fi
alive=0
for p in $(sed -n 's/^pid \([0-9]*\) .*/\1/p' "$TREE"); do kill -0 "$p" 2>/dev/null && alive=$((alive + 1)); done
eq 'TERM ignored: no process of the tree is left alive' 0 "$alive"
has 'TERM ignored: tree.txt lists the bash holder of the fifo' "$TREE" "^$T/ff2 held by:.* [0-9]+\\(bash\\)"
has 'TERM ignored: tree.txt lists the cat holder of the fifo' "$TREE" "^$T/ff2 held by:.* [0-9]+\\(cat\\)"
lacks 'TERM ignored: nothing survived the KILL' "$TREE" '^# still alive after KILL'

# an fd link with no target (the fd closed between the listing and readlink) is skipped, never used as a subscript
mkfifo "$T/ff3"
find() { command find "$@"; printf '/proc/%d/fd/9\t\n' "$$"; : > "$T/empty.injected"; }
stepprof_run "$T/empty" e 2 -- cat "$T/ff3" > "$T/empty.out" 2>&1; rc=$?
unset -f find
TREE=$T/empty/e.hang/tree.txt
eq 'empty fd target: returns 124' 124 "$rc"
eq 'empty fd target: the holder scan listed the target-less link' yes "$([[ -e $T/empty.injected ]] && echo yes || echo no)"
lacks 'empty fd target: tree.txt holds no bad array subscript' "$TREE" 'bad array subscript'
has 'empty fd target: tree.txt still names the fifo and its holders' "$TREE" "^$T/ff3 held by:"

# --- ERR records -----------------------------------------------------------
stepprof_run "$T/strict" strict 10 -- bash -euo pipefail -c 'f() { false; }; g() { f; }; g' > "$T/strict.out" 2>&1; rc=$?
X=$T/strict/strict.xtrace
eq 'strict caller dies: its status propagates' 1 "$rc"
has 'strict caller dies: ERR record with rc, epoch, file:line, function and command' "$X" '^ERR rc=1 [0-9]+\.[0-9]+ [^ ]*:[0-9]+ f: false$'
has 'strict caller dies: the record carries its call stack' "$X" '^  at [^ ]*:[0-9]+ g$'
eq 'strict caller dies: err_records 1' 1 "$(pv "$T/strict/strict.prof.tsv" err_records)"
eq 'strict caller dies: step output holds nothing the trap caused' '' "$(cat "$T/strict.out")"

# FUNCNAME is unset at a script's top level: the trap reads it unset-safe and never aborts a set -u script
printf 'echo before\nsh -c "exit 2"\necho not-reached\n' > "$T/top.sh"
stepprof_run "$T/top" top 10 -- bash -euo pipefail "$T/top.sh" > "$T/top.out" 2>&1; rc=$?
X=$T/top/top.xtrace
eq 'strict top-level failure: its status 2 propagates' 2 "$rc"
has 'strict top-level failure: one ERR record with rc, epoch, file:line, main and the command' "$X" "^ERR rc=2 [0-9]+\\.[0-9]+ $T/top\\.sh:[0-9]+ main: sh -c \"exit 2\"$"
eq 'strict top-level failure: err_records 1' 1 "$(pv "$T/top/top.prof.tsv" err_records)"
eq 'strict top-level failure: step output holds nothing the trap caused' before "$(cat "$T/top.out")"

printf '( exit 3 )\necho "sub=$?"\nx=$( exit 4 )\necho "subst=$?"\nexit 5\n' > "$T/sub.sh"
stepprof_run "$T/sub" sub 10 -- bash -u "$T/sub.sh" > "$T/sub.out" 2>&1; rc=$?
eq 'set -u top-level subshell and command substitution: $? is 3, then 4, and the script ends with its own status 5' "sub=3 subst=4 5" "$(paste -sd' ' "$T/sub.out") $rc"
eq 'set -u top-level subshell and command substitution: each failure is one ERR record' 2 "$(pv "$T/sub/sub.prof.tsv" err_records)"

fbody() { false; true; }
stepprof_run "$T/fbody" fbody 10 -- fbody > "$T/fbody.out" 2>&1; rc=$?
X=$T/fbody/fbody.xtrace
eq 'failing function body: the step succeeds' 0 "$rc"
has 'failing function body: the function own ERR record is in xtrace' "$X" '^ERR rc=1 [0-9]+\.[0-9]+ [^ ]*:[0-9]+ fbody: false$'
eq 'failing function body: err_records 1' 1 "$(pv "$T/fbody/fbody.prof.tsv" err_records)"

if stepprof_run "$T/cond" cond 10 -- fbody > "$T/cond.out" 2>&1; then
  eq 'failing function body: still recorded when the caller tests stepprof_run in an if' 1 "$(pv "$T/cond/cond.prof.tsv" err_records)"
else bad 'failing function body: in an if' 'stepprof_run returned nonzero'; fi

clean() { true; }
stepprof_run "$T/clean" c 10 -- clean > "$T/clean.out" 2>&1; rc=$?
eq 'clean step: succeeds' 0 "$rc"
if [[ ! -e $T/clean/c.xtrace ]]; then ok 'clean step: no xtrace file'; else bad 'clean step: xtrace left behind' "$(cat "$T/clean/c.xtrace")"; fi
eq 'clean step: err_records 0' 0 "$(pv "$T/clean/c.prof.tsv" err_records)"

mkdir -p "$T/stale/stale.hang"
echo 'ERR rc=9 old' > "$T/stale/stale.xtrace"; echo old > "$T/stale/stale.hang/tree.txt"
stepprof_run "$T/stale" stale 10 -- clean > "$T/stale.out" 2>&1
if [[ ! -e $T/stale/stale.xtrace && ! -e $T/stale/stale.hang ]]; then ok 'rerun in the same out dir: the previous trace and hang tree are cleared'
else bad 'rerun in the same out dir' "$(ls -A "$T/stale")"; fi

# --- trace modes -----------------------------------------------------------
traced() { local x=1; echo "$x" > /dev/null; }
STEPPROF_TRACE=x stepprof_run "$T/full" f 10 -- bash -u -c 'g() { echo in-g > /dev/null; }; g' > "$T/full.out" 2>&1; rc=$?
X=$T/full/f.xtrace
eq 'full trace: a bash -u step still succeeds' 0 "$rc"
eq 'full trace: a bash -u step prints nothing the trace caused' '' "$(cat "$T/full.out")"
has 'full trace: xtrace holds +<epoch> <file>:<line> <func>: lines' "$X" '^\+[0-9]+\.[0-9]+ [^ ]*:[0-9]+ g: echo in-g'
eq 'full trace: prof.tsv trace x, no ERR records counted' 'x 0' "$(pv "$T/full/f.prof.tsv" trace) $(pv "$T/full/f.prof.tsv" err_records)"
STEPPROF_TRACE=x stepprof_run "$T/fullfn" f 10 -- traced > "$T/fullfn.out" 2>&1
has 'full trace: a shell function command is traced (one + per eval depth)' "$T/fullfn/f.xtrace" '^\++[0-9]+\.[0-9]+ [^ ]*:[0-9]+ traced: local x=1'

STEPPROF_TRACE=off stepprof_run "$T/off" o 10 -- bash -euo pipefail -c 'f() { false; }; f' > "$T/off.out" 2>&1
if [[ ! -e $T/off/o.xtrace ]]; then ok 'trace off: no xtrace file'; else bad 'trace off: xtrace written' "$(cat "$T/off/o.xtrace")"; fi
eq 'trace off: prof.tsv says trace off' off "$(pv "$T/off/o.prof.tsv" trace)"

STEPPROF_TRACE=y stepprof_run "$T/badtrace" b 10 -- true > "$T/badtrace.out" 2>&1; rc=$?
eq 'bad trace mode: exit 64' 64 "$rc"
has 'bad trace mode: the message names the choices' "$T/badtrace.out" '^stepprof: STEPPROF_TRACE must be err, x or off$'

# --- bad input -------------------------------------------------------------
stepprof_run "$T/usage" u 10 > "$T/usage1.out" 2>&1; rc=$?
eq 'bad usage: fewer than 5 args exits 64' 64 "$rc"
has 'bad usage: fewer than 5 args prints the usage line' "$T/usage1.out" '^usage: .*<out-dir> <name> <bound-s> -- <command\.\.\.>$'
stepprof_run "$T/usage" u 10 x true > "$T/usage2.out" 2>&1; rc=$?
eq 'bad usage: no -- exits 64' 64 "$rc"
has 'bad usage: no -- prints the usage line' "$T/usage2.out" '^usage: '
stepprof_run "$T/usage" u soon -- true > "$T/bound.out" 2>&1; rc=$?
eq 'bad bound: exit 64' 64 "$rc"
has 'bad bound: the message names it' "$T/bound.out" "^stepprof: bound must be decimal seconds, got 'soon'$"
stepprof_run "$T/usage" a/b 10 -- true > "$T/name.out" 2>&1; rc=$?
eq 'bad name: exit 64' 64 "$rc"
STEPPROF_GRACE_TICKS=slow stepprof_run "$T/usage" u 10 -- true > "$T/grace.out" 2>&1; rc=$?
eq 'bad grace ticks: exit 64' 64 "$rc"
has 'bad grace ticks: the message names the value' "$T/grace.out" "^stepprof: STEPPROF_GRACE_TICKS must be a whole number, got 'slow'$"

: > "$T/blocker"
stepprof_run "$T/blocker/sub" w 10 -- true > "$T/unwritable.out" 2>&1; rc=$?
eq 'unwritable out dir: exit 71' 71 "$rc"
has 'unwritable out dir: the line names the directory' "$T/unwritable.out" "^stepprof: cannot create $T/blocker/sub$"

# --- pressure --------------------------------------------------------------
bump() {
  psi_file "$STEPPROF_PSI_DIR/cpu" 3500000 400250
  psi_file "$STEPPROF_PSI_DIR/io" 2000000 4000000
  psi_file "$STEPPROF_PSI_DIR/memory" 300001 100000
}
psi_file "$STEPPROF_PSI_DIR/io" 2000000 1000000
psi_file "$STEPPROF_PSI_DIR/memory" 300000 100000
psi_file "$STEPPROF_PSI_DIR/cpu" 1000000 400000
stepprof_run "$T/psi" p 10 -- bump > "$T/psi.out" 2>&1
P=$T/psi/p.prof.tsv
eq 'PSI readable: deltas over the step for every some/full line' \
  'psi_cpu_some_s 2.500000 psi_cpu_full_s 0.000250 psi_io_some_s 0.000000 psi_io_full_s 3.000000 psi_memory_some_s 0.000001 psi_memory_full_s 0.000000' \
  "$(grep '^psi_' "$P" | tr '\t\n' '  ' | sed 's/ $//')"

STEPPROF_PSI_DIR=$T/no-such-psi stepprof_run "$T/nopsi" p 10 -- true > "$T/nopsi.out" 2>&1
P=$T/nopsi/p.prof.tsv
eq 'PSI unreadable: one psi UNAVAILABLE line naming the path' "psi	UNAVAILABLE $T/no-such-psi/cpu" "$(grep '^psi' "$P")"
eq 'PSI unreadable: no psi number is written' 0 "$(grep -c '^psi_' "$P")"
eq 'PSI unreadable: the rest of the profile is still written' 'step,ended,rc,bound_s,start_epoch,finish_epoch,wall_s,children_cpu,cpu_s,trace,err_records,psi' "$(keys "$P")"

# --- deadline and CPU ------------------------------------------------------
stepprof_run "$T/dl" d 7.5 -- bash -c 'echo "$PFM_TEST_DEADLINE_EPOCH" > "$1"' _ "$T/dl.txt" > "$T/dl.out" 2>&1
eq 'deadline export: a child sees start_epoch + bound' "$(($(us "$(pv "$T/dl/d.prof.tsv" start_epoch)") + 7500000))" "$(us "$(cat "$T/dl.txt")")"
dlfn() { echo "$PFM_TEST_DEADLINE_EPOCH" > "$T/dlfn.txt"; }
stepprof_run "$T/dlfn" d 3 -- dlfn > "$T/dlfn.out" 2>&1
eq 'deadline export: a shell function sees start_epoch + bound' "$(($(us "$(pv "$T/dlfn/d.prof.tsv" start_epoch)") + 3000000))" "$(us "$(cat "$T/dlfn.txt")")"
if [[ -z ${PFM_TEST_DEADLINE_EPOCH:-} ]]; then ok 'deadline export: the caller shell is untouched'; else bad 'deadline export leaked into the caller'; fi

burn() { bash -c 'i=0; while ((i < 150000)); do ((i++)); done'; }
stepprof_run "$T/cpu" c 20 -- burn > "$T/cpu.out" 2>&1
P=$T/cpu/c.prof.tsv
want=$(pv "$P" children_cpu | awk '{split($1, u, /[ms]/); split($3, s, /[ms]/); printf "%.3f", u[1] * 60 + u[2] + s[1] * 60 + s[2]}')
eq 'CPU accounting: cpu_s equals children user + sys' "$want" "$(pv "$P" cpu_s)"
if awk -v c="$(pv "$P" cpu_s)" 'BEGIN {exit !(c > 0)}'; then ok 'CPU accounting: a child that burns CPU gives cpu_s > 0'; else bad 'CPU accounting: cpu_s' "$(cat "$P")"; fi

# --- CLI -------------------------------------------------------------------
stepprof_run "$T/fnrun" s 10 -- bash -c 'exit 3' > "$T/fnrun.out" 2>&1; fn_rc=$?
bash "$SUT" "$T/cli" s 10 -- bash -c 'exit 3' > "$T/cli.out" 2>&1; cli_rc=$?
eq 'CLI: the same exit status as the function' "$fn_rc" "$cli_rc"
eq 'CLI: the same files' "$(ls "$T/fnrun")" "$(ls "$T/cli")"
eq 'CLI: the same prof.tsv keys' "$(keys "$T/fnrun/s.prof.tsv")" "$(keys "$T/cli/s.prof.tsv")"
bash "$SUT" > "$T/cli-usage.out" 2>&1; rc=$?
eq 'CLI: no arguments exits 64' 64 "$rc"
has 'CLI: no arguments prints the usage line' "$T/cli-usage.out" '^usage: '
STEPPROF_GRACE_TICKS=3 bash "$SUT" "$T/clihang" h 2 -- cat "$T/ff" > "$T/clihang.out" 2>&1; rc=$?
eq 'CLI: a bound reached exits 124' 124 "$rc"
has 'CLI: a bound reached writes the hang tree' "$T/clihang/h.hang/tree.txt" '^pid [0-9]+  state [A-Z] cat '

shtest_end

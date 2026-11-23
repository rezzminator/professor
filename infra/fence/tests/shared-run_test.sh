#!/usr/bin/env bash
# infra/fence/shared-run.sh against real namespaces: a run's exit code, its read-only tree on
# /worktree, its own HOME, /tmp, /var/tmp and /dev/shm (two concurrent runs writing the same fixed
# paths never see each other's), the hidden state and host directories, the live record and
# PFM_FENCE_CONCURRENT, its own pid namespace, the private network (two concurrent runs bind the same
# fixed port; an outside connect fails), the kill on a heartbeat EOF and on a stalled heartbeat, the
# kill subcommand, exit 75 while closing, the started marker and /pfm-run, the worktree's user record, tty mode's foreground run,
# the argument refusals, and the container's main process (idle exit, dead-supervisor reap, superseded
# drain). It needs unshare with CAP_SYS_ADMIN and CAP_NET_ADMIN — the shared fence
# container grants both — and re-runs itself in a private mount namespace with a tmpfs on /mnt, where
# the state and host directories live (FENCE_STATE_DIR, FENCE_HOST_DIR).
#   bash infra/fence/tests/shared-run_test.sh
# BROKEN STATE: no unshare or no capability is a named FAILED line and a non-zero exit, never a skip;
# a missing shared-run.sh exits 2 naming it.
set -uo pipefail
unset HARVESTER_FENCE_SRC
FENCE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${SHARED_RUN_SUT:-$FENCE/shared-run.sh}"
[ -f "$SUT" ] || { echo "shared-run_test: no shared-run.sh at $SUT" >&2; exit 2; }
if [ -z "${SHARED_RUN_TEST_NS:-}" ]; then
  SHTEST_TAG=fence-shared-run-test-root # a plain assignment: one prefixed to `.` ends with the source, and shtest_as_root names it
  # shellcheck source=/dev/null
  . "$FENCE/../../scripts/shtest.sh"
  shtest_as_root "${BASH_SOURCE[0]}" "$@"
  rc=0; SHARED_RUN_TEST_NS=1 unshare --mount -- bash "${BASH_SOURCE[0]}" "$@" || rc=$?
  [ "$rc" = 0 ] || echo "shared-run_test: FAILED (exit $rc) — the suite needs unshare with CAP_SYS_ADMIN and CAP_NET_ADMIN, as the shared fence container grants" >&2
  exit "$rc"
fi
mount --make-rprivate / || { echo "shared-run_test: mount --make-rprivate / failed" >&2; exit 1; }
mount -t tmpfs tmpfs /mnt || { echo "shared-run_test: no tmpfs on /mnt" >&2; exit 1; }
SHTEST_TAG=fence-shared-run-test
# shellcheck source=/dev/null
. "$FENCE/../../scripts/shtest.sh"
export FENCE_STATE_DIR=/mnt/state FENCE_HOST_DIR=/mnt/host PFM_FENCE_HEARTBEAT_S=2
mkdir -p "$FENCE_STATE_DIR" "$FENCE_HOST_DIR/.runs" "$T/tree"
echo here >"$T/tree/marker"
TREE="$T/tree"
# A fixed cgroup fixture makes admission independent of other runs in the shared container.
export PFM_FENCE_CGROUP_ROOT=/mnt/cgroup
mkdir -p "$PFM_FENCE_CGROUP_ROOT"
printf '%s\n' 5368709120 > "$PFM_FENCE_CGROUP_ROOT/memory.max"
printf '%s\n' 1073741824 > "$PFM_FENCE_CGROUP_ROOT/memory.current"

# A run's command waits through the wait door, copied into its tree (/worktree/shtest-wait.sh); runs
# meet and are released through the worktree's timing base, $TIMING outside and /pfm-timing inside.
cp "$FENCE/../../scripts/shtest-wait.sh" "$TREE/shtest-wait.sh"
TIMING="$FENCE_HOST_DIR/wt/timing"
mkdir -p "$TIMING"
srun() { # srun <id> <mode> <command string>: a run whose caller holds its heartbeat stream open; prints its output
  # One beat, then the stream stays open without another until the run ends: the run's monitor waits
  # PFM_FENCE_HEARTBEAT_S=120 (the hung-only limit) for the next. The heartbeat kills have their own runs below.
  local beat="$T/beat.$1" holder rc=0
  mkfifo "$beat"
  { printf '.\n'; shtest_hold "beat-$1"; } >"$beat" &
  holder=$!
  PFM_FENCE_HEARTBEAT_S=120 bash "$SUT" run "$1" "$2" "$TREE" - wt -- bash -c "$3" <"$beat" 2>&1 || rc=$?
  kill "$holder" 2>/dev/null; wait "$holder" 2>/dev/null; rm -f "$beat"
  return "$rc"
}
release() { # release <fifo>: lets the command blocked reading the FIFO go on (its read sees EOF)
  : >"$1"
}

# --- exit code, tree, cwd -----------------------------------------------------------------------------
out="$(srun r-exit shared 'exit 7')"; rc=$?
if [ "$rc" = 7 ]; then ok "a run exits with its command's code"; else bad "a run exits with its command's code" "rc=$rc" "$out"; fi
touch "$FENCE_STATE_DIR/superseded"
out="$(srun r-user shared 'true')"; rc=$?
if [ "$rc" = 0 ] && [ -e "$FENCE_STATE_DIR/worktrees/wt" ] && [ ! -e "$FENCE_STATE_DIR/superseded" ]; then ok "a run records its worktree as the container's user and lifts a superseded mark"
else bad "the worktree record" "rc=$rc" "$(ls -A "$FENCE_STATE_DIR")" "$out"; fi
out="$(srun r-tree shared '[ "$PWD" = /worktree ] && [ "$(cat /worktree/marker)" = here ] && ! touch /worktree/x 2>/dev/null && [ "$HOME" = /root ]')"; rc=$?
if [ "$rc" = 0 ]; then ok "a run starts in its tree on /worktree, read-only, HOME=/root"; else bad "the run's tree, cwd and HOME" "rc=$rc" "$out"; fi
if [ ! -e "$TREE/x" ]; then ok "a run never writes its tree"; else bad "a run wrote its tree"; fi
# A run's command gets SIGINT and SIGQUIT at their defaults, never the ignore bash gives a background job
# (an ignore inherited at entry cannot be trapped, so a test that sends INT would see it vanish).
out="$(srun r-sig shared 'awk "/^SigIgn:/ { print \$2 }" /proc/self/status')"; rc=$?
if [ "$rc" = 0 ] && [[ "$out" =~ ^[0-9a-f]+$ ]] && (( (16#$out & 6) == 0 )); then ok "a run's command has SIGINT and SIGQUIT at their defaults"; else bad "a run's command ignores SIGINT or SIGQUIT" "rc=$rc SigIgn=$out"; fi

# --- admission decision: source definitions only, without the command dispatcher ---------------------------
out="$(bash -c 'source "$1" kill r-admission; declare -F scratch_uses_ram >/dev/null && scratch_uses_ram' _ "$SUT" 2>&1)"; rc=$?
if [ "$rc" = 0 ] && [ -z "$out" ]; then ok "RAM admission reads a cgroup with room without a fallback line"
else bad "RAM admission when room exists" "rc=$rc" "$out"; fi

# --- capped, non-swappable scratch and explicit disk semantics ---------------------------------------------
out="$(srun r-memory shared '
for spec in /root:268435456:700 /tmp:1610612736:1777 /var/tmp:16777216:1777 /dev/shm:67108864:1777 /mnt/state:1048576:700 /mnt/host:1048576:700; do
  path=${spec%%:*}; rest=${spec#*:}; bytes=${rest%%:*}; mode=${rest##*:}
  [ "$(findmnt -bn -o FSTYPE -T "$path" | tail -n 1)" = tmpfs ] || { echo "$path is not tmpfs"; exit 1; }
  [ "$(findmnt -bn -o SIZE -T "$path" | tail -n 1)" = "$bytes" ] || { echo "$path has the wrong cap"; exit 1; }
  case ",$(findmnt -n -o OPTIONS -T "$path" | tail -n 1)," in *,noswap,*) ;; *) echo "$path can swap"; exit 1 ;; esac
  [ "$(stat -c %a "$path")" = "$mode" ] || { echo "$path has the wrong mode"; exit 1; }
done
if cap_error=$(dd if=/dev/zero of=/var/tmp/over-cap bs=1M count=17 status=none 2>&1); then
  echo "/var/tmp exceeded its cap"; exit 1
fi
grep -q "No space left on device" <<<"$cap_error" || { echo "/var/tmp cap did not report ENOSPC: $cap_error"; exit 1; }
rm -f /var/tmp/over-cap
[ "$PFM_FENCE_DISK_DIR" = /pfm-disk ] && [ "$GOTMPDIR" = /pfm-disk ] && [ "$(findmnt -n -o TARGET -T /pfm-disk | tail -n 1)" = /pfm-disk ] && echo disk > /pfm-disk/probe
')"; rc=$?
# This suite's STATE is on tmpfs itself; the explicit disk escape must bind the run's directory,
# not silently claim that its backing device is always disk in nested harnesses.
if [ "$rc" = 0 ]; then ok "all six scratch mounts are capped tmpfs, noswap, with private modes and a writable disk escape that holds Go's build scratch"
else bad "scratch mount type, cap, noswap, mode or disk escape" "rc=$rc" "$out"; fi

# Overrides change both the mounted cap and the admission sum (all six caps plus headroom).
out="$(PFM_FENCE_VARTMP_MIB=8 srun r-override shared '[ "$(findmnt -bn -o SIZE -T /var/tmp | tail -n 1)" = 8388608 ]')"; rc=$?
if [[ $rc == 0 ]]; then ok "scratch override changes the real private tmpfs cap"; else bad "scratch override changes the real private tmpfs cap" "$out"; fi
out="$(PFM_FENCE_HEADROOM_MIB=4096 bash -c 'source "$1" kill r-admission; scratch_uses_ram' _ "$SUT" 2>&1)"; rc=$?
if [[ $rc != 0 && "$out" == 'shared-run: disk scratch: free 4096 MiB, needed 5970 MiB' ]]; then
  ok "RAM admission sums configured caps and headroom, not the old constant"
else bad "RAM admission sums configured caps and headroom" "rc=$rc $out"; fi
# Page cache the kernel can drop (inactive_file) is free for admission, not used.
printf '%s\n' 4294967296 > "$PFM_FENCE_CGROUP_ROOT/memory.current"
out="$(bash -c 'source "$1" kill r-cache; scratch_uses_ram' _ "$SUT" 2>&1)"; rc=$?
printf 'anon 1073741824\ninactive_file 3221225472\n' > "$PFM_FENCE_CGROUP_ROOT/memory.stat"
out2="$(bash -c 'source "$1" kill r-cache; scratch_uses_ram' _ "$SUT" 2>&1)"; rc2=$?
if [[ $rc != 0 && "$out" == 'shared-run: disk scratch: free 1024 MiB, needed 2898 MiB' && $rc2 == 0 && -z "$out2" ]]; then
  ok "RAM admission counts reclaimable page cache as free"
else bad "RAM admission counts reclaimable page cache as free" "without stat rc=$rc $out" "with stat rc=$rc2 $out2"; fi
rm "$PFM_FENCE_CGROUP_ROOT/memory.stat"
printf '%s\n' 1073741824 > "$PFM_FENCE_CGROUP_ROOT/memory.current"

# The suite's STATE is itself tmpfs: verify bind sources, not the backing filesystem type.
disk_probe='for spec in /root:home:700 /tmp:tmp:1777 /var/tmp:vartmp:1777; do
  path=${spec%%:*}; rest=${spec#*:}; dir=${rest%%:*}; mode=${rest##*:}
  [ "$(findmnt -n -o FSROOT -T "$path" | tail -n 1)" = "/state/runs/$ID/$dir" ] || { echo "$path is not the run disk bind"; exit 1; }
  [ "$(stat -c %a "$path")" = "$mode" ] || { echo "$path has the wrong disk mode"; exit 1; }
  echo private > "$path/probe"
done
[ "$PFM_FENCE_DISK_DIR" = /pfm-disk ]'
printf '%s\n' 2147483648 > "$PFM_FENCE_CGROUP_ROOT/memory.max"
printf '%s\n' 536870912 > "$PFM_FENCE_CGROUP_ROOT/memory.current"
out="$(srun r-low shared "ID=r-low; $disk_probe")"; rc=$?
if [ "$rc" = 0 ] && [ "$out" = 'shared-run: disk scratch: free 1536 MiB, needed 2898 MiB' ]; then
  ok "low cgroup memory selects private disk binds and one free/needed fallback line"
else bad "disk admission when memory is low" "rc=$rc" "$out"; fi
rm "$PFM_FENCE_CGROUP_ROOT/memory.current"
out="$(srun r-unreadable shared "ID=r-unreadable; $disk_probe")"; rc=$?
if [ "$rc" = 0 ] && [ "$out" = "shared-run: disk scratch: cgroup read failed ($PFM_FENCE_CGROUP_ROOT/memory.max, $PFM_FENCE_CGROUP_ROOT/memory.current); free unknown MiB, needed 2898 MiB" ]; then
  ok "a failed cgroup read selects disk and one explicit read-failed fallback line"
else bad "disk admission when the cgroup read fails" "rc=$rc" "$out"; fi
printf '%s\n' 5368709120 > "$PFM_FENCE_CGROUP_ROOT/memory.max"
printf '%s\n' 1073741824 > "$PFM_FENCE_CGROUP_ROOT/memory.current"

# --- per-run fixed paths ----------------------------------------------------------------------------------
# Each run writes its fixed paths, marks it, and reads them back only once the other has written its own.
fixed='. /worktree/shtest-wait.sh
for d in /tmp /root /var/tmp /dev/shm; do echo "$ID" > "$d/fixed"; done; : > "/pfm-timing/fixed-$ID"
both_wrote() { [ -e /pfm-timing/fixed-a ] && [ -e /pfm-timing/fixed-b ]; }
shtest_wait_until "the other run writes its fixed paths" both_wrote || exit 1
for d in /tmp /root /var/tmp /dev/shm; do [ "$(cat "$d/fixed")" = "$ID" ] || { echo "$d/fixed is $(cat "$d/fixed"), not $ID"; exit 1; }; done'
( out="$(srun r-fa shared "ID=a; $fixed")"; echo "$?" >"$T/fa.rc"; printf '%s\n' "$out" >"$T/fa.out" ) &
( out="$(srun r-fb shared "ID=b; $fixed")"; echo "$?" >"$T/fb.rc"; printf '%s\n' "$out" >"$T/fb.out" ) &
wait
if [ "$(cat "$T/fa.rc")" = 0 ] && [ "$(cat "$T/fb.rc")" = 0 ]; then ok "two concurrent runs writing the same fixed paths under /tmp, HOME, /var/tmp and /dev/shm each keep their own"
else bad "two concurrent runs' fixed paths" "$(cat "$T/fa.out" "$T/fb.out")"; fi
if [ ! -e /tmp/fixed ] && [ ! -e /var/tmp/fixed ] && [ ! -e /dev/shm/fixed ]; then ok "a run's fixed paths never reach the container's"; else bad "a run's fixed path reached the container's"; fi
if [ -z "$(ls -A "$FENCE_STATE_DIR/runs" 2>/dev/null)" ] && [ -z "$(ls -A "$FENCE_STATE_DIR/live" 2>/dev/null)" ]; then
  ok "an ended run leaves no run directory and no live record"
else bad "an ended run left state" "$(ls -R "$FENCE_STATE_DIR")"; fi
out="$(srun r-hide shared '[ -z "$(ls -A /mnt/state)" ] && [ -z "$(ls -A /mnt/host)" ]')"; rc=$?
if [ "$rc" = 0 ]; then ok "a run sees neither the state directory nor the host directory"; else bad "a run sees the state or host directory" "$out"; fi

# --- live record, PFM_FENCE_CONCURRENT, pid namespace -------------------------------------------------------
# r-la holds a process named sleep (cat under that name, blocked on a FIFO) until r-lb has looked for it.
mkdir -p "$T/tree/hold" && cp "$(command -v cat)" "$T/tree/hold/sleep"
mkfifo "$TIMING/la.hold"
( srun r-la shared 'echo "$PFM_FENCE_CONCURRENT" > /pfm-timing/conc-a; exec /worktree/hold/sleep /pfm-timing/la.hold' >"$T/la.out" ) &
la=$!
la_holds() { [ -s "$TIMING/conc-a" ] && pgrep -fx '/worktree/hold/sleep /pfm-timing/la.hold' >/dev/null; }
shtest_wait_until "run r-la holds its sleep" la_holds || bad "run r-la never started" "$(cat "$T/la.out")"
if [ -n "$(cat "$FENCE_STATE_DIR/live/r-la" 2>/dev/null)" ]; then ok "a live run has its live record"; else bad "no live record for a live run" "$(ls -R "$FENCE_STATE_DIR")"; fi
out="$(srun r-lb shared 'echo "$PFM_FENCE_CONCURRENT" > /pfm-timing/conc-b; n=$(ls -d /proc/[0-9]* | wc -l); [ "$n" -lt 12 ] || { echo "$n processes visible"; exit 1; }; ! pgrep -x sleep')"; rc=$?
if [ "$rc" = 0 ]; then ok "a run sees only its own processes (another run's sleep is invisible)"; else bad "a run sees other runs' processes" "$out"; fi
if [ "$(cat "$FENCE_HOST_DIR/wt/timing/conc-b")" = 2 ]; then ok "PFM_FENCE_CONCURRENT counts the live runs, this one included"
else bad "PFM_FENCE_CONCURRENT" "conc-a=$(cat "$FENCE_HOST_DIR/wt/timing/conc-a") conc-b=$(cat "$FENCE_HOST_DIR/wt/timing/conc-b")"; fi
release "$TIMING/la.hold"
shtest_wait_gone "run r-la ends" "$la"
wait

# --- private network ------------------------------------------------------------------------------------
# Each run binds the fixed port, marks it bound, and holds the socket until both are bound at once.
cat >"$T/tree/port.py" <<'PORT'
import socket, sys
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 0); s.bind(("127.0.0.1", 18765)); s.listen(1)
open("/pfm-timing/port-" + sys.argv[1], "w").close()
open("/pfm-timing/port-" + sys.argv[1] + ".hold").read()
PORT
mkfifo "$TIMING/port-a.hold" "$TIMING/port-b.hold"
( srun r-pa private 'python3 /worktree/port.py a' >"$T/pa.out"; echo "$?" >"$T/pa.rc" ) &
pa=$!
( srun r-pb private 'python3 /worktree/port.py b' >"$T/pb.out"; echo "$?" >"$T/pb.rc" ) &
pb=$!
both_bound() { [ -e "$TIMING/port-a" ] && [ -e "$TIMING/port-b" ]; }
shtest_wait_until "both private runs hold port 18765" both_bound
for x in a b; do [ ! -e "$TIMING/port-$x" ] || release "$TIMING/port-$x.hold"; done
shtest_wait_gone "run r-pa ends" "$pa"; shtest_wait_gone "run r-pb ends" "$pb"
wait
if [ "$(cat "$T/pa.rc")" = 0 ] && [ "$(cat "$T/pb.rc")" = 0 ]; then ok "two concurrent private runs bind the same fixed port"
else bad "two private runs on one port" "$(cat "$T/pa.out" "$T/pb.out")"; fi
out="$(srun r-pc private 'python3 -c "import socket; socket.create_connection((\"192.0.2.10\", 80), 2)" 2>/dev/null && exit 1; ip -o link show lo | grep -q "UP" ')"; rc=$?
if [ "$rc" = 0 ]; then ok "a private run has lo up and no outside connection"; else bad "a private run's network" "$out"; fi

# --- the heartbeat kill -----------------------------------------------------------------------------------
# Each run's command holds until killed: a run that ends at all was killed, and the door's hung-only
# limit turns a kill that never comes into a failure. The marker is this test's alone: the gate runs
# other suites' processes in this pid namespace.
hold_cmd() { printf "exec -a 'shtest-hold %s.%s' tail -f /dev/null" "$1" "$$"; } # hold_cmd <run id>
holding() { pgrep -f "^shtest-hold $1.$$ " >/dev/null; } # holding <run id>: the run's command is alive
# The heartbeat EOF: one beat, then the caller closes the stream once the run's command holds.
( { printf '.\n'; shtest_wait_until "run r-eof holds its command" holding r-eof; } \
  | bash "$SUT" run r-eof shared "$TREE" - wt -- bash -c "$(hold_cmd r-eof)" >"$T/eof.out" 2>&1; echo "${PIPESTATUS[1]}" >"$T/eof.rc" ) &
eof_run=$!
shtest_wait_gone "run r-eof is killed" "$eof_run" || { bad "a heartbeat EOF kill" "run r-eof was not killed: the test kills it"; bash "$SUT" kill r-eof; }
wait "$eof_run"
out="$(cat "$T/eof.out")"; rc="$(cat "$T/eof.rc")"
if [ "$rc" != 0 ]; then ok "a heartbeat EOF kills the run"; else bad "a heartbeat EOF kill" "rc=$rc" "$out"; fi
if grep -qF 'shared-run: the caller of run r-eof is gone (heartbeat ended) — killing the run' <<<"$out"; then ok "the EOF kill is named"; else bad "the EOF kill is named" "$out"; fi
if ! holding r-eof; then ok "a killed run leaves no process"; else bad "a killed run left a process" "$(pgrep -af "shtest-hold r-eof")"; fi
# The stalled caller: a writer holding the run's stdin open without a beat. A fifo, not < <(…): the
# command substitution would wait for its process substitution's exit.
mkfifo "$T/stall"
( exec 4>"$T/stall"; shtest_hold stall-writer ) &
stall_writer=$!
( bash "$SUT" run r-stall shared "$TREE" - wt -- bash -c "$(hold_cmd r-stall)" <"$T/stall" >"$T/stall.out" 2>&1; echo "$?" >"$T/stall.rc" ) &
stall_run=$!
shtest_wait_gone "run r-stall is killed" "$stall_run" || { bad "a stalled heartbeat kill" "run r-stall was not killed: the test kills it"; bash "$SUT" kill r-stall; }
wait "$stall_run"
out="$(cat "$T/stall.out")"; rc="$(cat "$T/stall.rc")"
kill "$stall_writer" 2>/dev/null
if [ "$rc" != 0 ]; then ok "a stalled heartbeat kills the run after PFM_FENCE_HEARTBEAT_S"; else bad "a stalled heartbeat kill" "rc=$rc" "$out"; fi
if ! holding r-stall; then ok "a stalled run leaves no process"; else bad "a stalled run left a process" "$(pgrep -af 'shtest-hold r-stall')"; fi
( srun r-kill shared "$(hold_cmd r-kill)" >"$T/kill.out"; echo "$?" >"$T/kill.rc" ) &
kill_run=$!
shtest_wait_until "run r-kill holds its command" holding r-kill || bad "run r-kill never started" "$(cat "$T/kill.out")"
bash "$SUT" kill r-kill
shtest_wait_gone "run r-kill ends" "$kill_run"
wait
if [ "$(cat "$T/kill.rc")" != 0 ] && ! holding r-kill; then ok "kill <id> ends the run and all it holds"
else bad "kill <id>" "rc=$(cat "$T/kill.rc")" "$(pgrep -af 'shtest-hold r-kill')"; fi

# --- closing, markers, tty, refusals ----------------------------------------------------------------------
mkdir -p "$FENCE_STATE_DIR/closing"
out="$(srun r-closing shared 'exit 0')"; rc=$?
rmdir "$FENCE_STATE_DIR/closing"
if [ "$rc" = 75 ] && grep -qF 'shared-run: the container is closing — start a new one' <<<"$out" && [ ! -e "$FENCE_STATE_DIR/live/r-closing" ]; then
  ok "a closing container refuses a run with exit 75 and keeps no record of it"
else bad "a closing container" "rc=$rc" "$out"; fi
mkdir -p "$FENCE_HOST_DIR/.runs/r-mark"
out="$(srun r-mark shared 'echo hi > /pfm-run/note')"; rc=$?
if [ "$rc" = 0 ] && [ -e "$FENCE_HOST_DIR/.runs/r-mark/started" ] && [ "$(cat "$FENCE_HOST_DIR/.runs/r-mark/note")" = hi ]; then
  ok "a run marks its host directory started and writes it on /pfm-run"
else bad "the started marker and /pfm-run" "rc=$rc" "$out"; fi
# A tty run on a closed stdin lives on until its caller releases it: no heartbeat monitor reads that EOF.
mkfifo "$TIMING/tty.hold"
( bash "$SUT" run r-tty tty "$TREE" - wt -- bash -c ': > /pfm-timing/tty-up; cat /pfm-timing/tty.hold; exit 3' </dev/null >"$T/tty.out" 2>&1; echo "$?" >"$T/tty.rc" ) &
tty_run=$!
shtest_wait_until "the tty run r-tty starts" test -e "$TIMING/tty-up" && release "$TIMING/tty.hold"
shtest_wait_gone "the tty run r-tty ends" "$tty_run"; wait "$tty_run"
out="$(cat "$T/tty.out")"; rc="$(cat "$T/tty.rc")"
if [ "$rc" = 3 ]; then ok "a tty run needs no heartbeat"; else bad "a tty run" "rc=$rc" "$out"; fi
for args in 'run a/b shared TREE - wt -- true' 'run r1 bogus TREE - wt -- true' 'run r1 shared /nonexistent - wt -- true' 'run r1 shared TREE /nonexistent wt -- true' 'kill a/b'; do
  # shellcheck disable=SC2086 # the cases are word lists
  out="$(bash "$SUT" ${args//TREE/$TREE} 2>&1 </dev/null)"; rc=$?
  if [ "$rc" = 2 ] && grep -q '^shared-run: ' <<<"$out"; then ok "'$args' exits 2 naming the problem"; else bad "'$args' refusal" "rc=$rc" "$out"; fi
done
out="$(FENCE_STATE_DIR=/tmp/state bash "$SUT" run r1 shared "$TREE" - wt -- true 2>&1 </dev/null)"; rc=$?
if [ "$rc" = 2 ] && grep -qF 'shared-run: /tmp/state lies under /root, /tmp or /var/tmp, which a run binds over' <<<"$out"; then ok "a state directory under /tmp is refused"
else bad "a state directory under /tmp" "rc=$rc" "$out"; fi


# --- the container's main process (infra/fence/shared.sh FENCE_SHARED_DAEMON) ---------------------------
# It reads the fixed /var/fence; this suite's private mount namespace gives it a tmpfs there.
DAEMON="$(bash -c '. "$1" && printf "%s" "$FENCE_SHARED_DAEMON"' _ "$FENCE/shared.sh")"
if [ -n "$DAEMON" ] && mkdir -p /var/fence && mount -t tmpfs tmpfs /var/fence; then
  none_left() { local p; for p; do [ ! -e "$p" ] || return 1; done; } # none_left <path>…
  daemon_pass() { # daemon_pass <record>: a dead supervisor's record the daemon removes on a pass of its loop
    echo 999999 > "/var/fence/live/$1"
    shtest_wait_until "the daemon removes the dead record $1" none_left "/var/fence/live/$1"
  }
  # Two passes the daemon made, each read by the record it removed, stand for "past its idle time":
  # PFM_FENCE_IDLE_S=0, so every pass is past it; PFM_FENCE_DAEMON_POLL_S=0 makes the passes back to back.
  # idle: a live run holds it past PFM_FENCE_IDLE_S; once none is live it closes and exits 0
  mkdir -p /var/fence/live /var/fence/runs
  shtest_hold d-held & held=$!
  echo "$held" > /var/fence/live/d-held
  PFM_FENCE_IDLE_S=0 PFM_FENCE_DAEMON_POLL_S=0 bash -c "$DAEMON" & dmn=$!
  daemon_pass d-pass1 && daemon_pass d-pass2
  if kill -0 "$dmn" 2>/dev/null; then ok "the container stays up while a run is live, past its idle time"
  else bad "the container exited with a live run"; fi
  kill "$held" 2>/dev/null; wait "$held" 2>/dev/null
  if shtest_wait_gone "the idle daemon exits" "$dmn"; then
    wait "$dmn"; rc=$?
    if [ "$rc" = 0 ] && [ -d /var/fence/closing ]; then ok "with no live run past PFM_FENCE_IDLE_S it marks closing and exits 0"
    else bad "the idle exit" "rc=$rc closing=$(ls -d /var/fence/closing 2>&1)"; fi
  else bad "no idle exit after the last run's end"; kill -KILL "$dmn" 2>/dev/null; fi
  rm -rf /var/fence/*
  # reap: a dead supervisor's run is killed and its records removed; superseded drains, then exits
  mkdir -p /var/fence/live /var/fence/runs/d-dead
  shtest_hold d-orphan & orphan=$!
  echo 999999 > /var/fence/live/d-dead; echo "$orphan" > /var/fence/runs/d-dead/ns.pid
  PFM_FENCE_IDLE_S=600 PFM_FENCE_DAEMON_POLL_S=0 bash -c "$DAEMON" & dmn=$!
  # The daemon kills the run, then removes its records: the removal is waited for, never read at the kill.
  if shtest_wait_gone "the dead supervisor's run is killed" "$orphan" \
    && shtest_wait_until "the dead supervisor's records are removed" none_left /var/fence/live/d-dead /var/fence/runs/d-dead; then
    ok "a run whose supervisor died is killed and its records removed"
  else bad "the dead supervisor's run" "orphan alive=$(kill -0 "$orphan" 2>/dev/null && echo yes || echo no)" "$(ls -R /var/fence)"; kill "$orphan" 2>/dev/null; fi
  wait "$orphan" 2>/dev/null
  shtest_hold d-held2 & held=$!
  echo "$held" > /var/fence/live/d-held2
  touch /var/fence/superseded
  daemon_pass d-pass3 && daemon_pass d-pass4
  if kill -0 "$dmn" 2>/dev/null; then ok "a superseded container drains: it stays up while a run is live"
  else bad "a superseded container exited with a live run"; fi
  kill "$held" 2>/dev/null; wait "$held" 2>/dev/null
  if shtest_wait_gone "the superseded daemon exits" "$dmn"; then ok "a superseded container exits once its last live run ends"
  else bad "the superseded container did not exit after its last run"; kill -KILL "$dmn" 2>/dev/null; fi
  wait "$dmn" 2>/dev/null
  umount /var/fence
else bad "the container daemon's text could not be read from $FENCE/shared.sh, or no tmpfs on /var/fence"; fi

shtest_end

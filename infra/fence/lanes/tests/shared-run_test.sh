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
#   bash infra/fence/lanes/tests/shared-run_test.sh
# BROKEN STATE: no unshare or no capability is a named FAILED line and a non-zero exit, never a skip;
# a missing shared-run.sh exits 2 naming it.
set -uo pipefail
FENCE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
SUT="${SHARED_RUN_SUT:-$FENCE/shared-run.sh}"
[ -f "$SUT" ] || { echo "shared-run_test: no shared-run.sh at $SUT" >&2; exit 2; }
if [ -z "${SHARED_RUN_TEST_NS:-}" ]; then
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

beat() { while printf '.\n' 2>/dev/null; do sleep 0.3; done; }
srun() { # srun <id> <mode> <command string>: a run with a live heartbeat; prints its output
  beat | bash "$SUT" run "$1" "$2" "$TREE" - wt -- bash -c "$3" 2>&1
  return "${PIPESTATUS[1]}" # the beat dies of SIGPIPE once the run ends; the run's code is the verdict
}
wait_for() { # wait_for <path>: up to 10 s
  local i
  for ((i = 0; i < 100; i++)); do [ -e "$1" ] && return 0; sleep 0.1; done
  return 1
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

# --- per-run fixed paths ----------------------------------------------------------------------------------
fixed='for d in /tmp /root /var/tmp /dev/shm; do echo "$ID" > "$d/fixed"; done; sleep 1.5
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
( srun r-la shared 'echo "$PFM_FENCE_CONCURRENT" > /pfm-timing/conc-a; sleep 3' >"$T/la.out" ) &
wait_for "$FENCE_HOST_DIR/wt/timing/conc-a" || bad "run r-la never started" "$(cat "$T/la.out")"
if [ -n "$(cat "$FENCE_STATE_DIR/live/r-la" 2>/dev/null)" ]; then ok "a live run has its live record"; else bad "no live record for a live run" "$(ls -R "$FENCE_STATE_DIR")"; fi
out="$(srun r-lb shared 'echo "$PFM_FENCE_CONCURRENT" > /pfm-timing/conc-b; n=$(ls -d /proc/[0-9]* | wc -l); [ "$n" -lt 12 ] || { echo "$n processes visible"; exit 1; }; ! pgrep -x sleep')"; rc=$?
if [ "$rc" = 0 ]; then ok "a run sees only its own processes (another run's sleep is invisible)"; else bad "a run sees other runs' processes" "$out"; fi
if [ "$(cat "$FENCE_HOST_DIR/wt/timing/conc-b")" = 2 ]; then ok "PFM_FENCE_CONCURRENT counts the live runs, this one included"
else bad "PFM_FENCE_CONCURRENT" "conc-a=$(cat "$FENCE_HOST_DIR/wt/timing/conc-a") conc-b=$(cat "$FENCE_HOST_DIR/wt/timing/conc-b")"; fi
wait

# --- private network ------------------------------------------------------------------------------------
port='python3 -c "import socket,time; s=socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 0); s.bind((\"127.0.0.1\", 18765)); s.listen(1); time.sleep(1.5)"'
( srun r-pa private "$port" >"$T/pa.out"; echo "$?" >"$T/pa.rc" ) &
( srun r-pb private "$port" >"$T/pb.out"; echo "$?" >"$T/pb.rc" ) &
wait
if [ "$(cat "$T/pa.rc")" = 0 ] && [ "$(cat "$T/pb.rc")" = 0 ]; then ok "two concurrent private runs bind the same fixed port"
else bad "two private runs on one port" "$(cat "$T/pa.out" "$T/pb.out")"; fi
out="$(srun r-pc private 'python3 -c "import socket; socket.create_connection((\"192.0.2.10\", 80), 2)" 2>/dev/null && exit 1; ip -o link show lo | grep -q "UP" ')"; rc=$?
if [ "$rc" = 0 ]; then ok "a private run has lo up and no outside connection"; else bad "a private run's network" "$out"; fi

# --- the heartbeat kill -----------------------------------------------------------------------------------
start=$(date +%s)
eof_sleep="sleep 60.$$" # a marker of this test alone: the gate runs other suites' sleeps in this pid namespace
out="$( { printf '.\n'; sleep 1; } | bash "$SUT" run r-eof shared "$TREE" - wt -- bash -c "$eof_sleep" 2>&1)"; rc=$?
el=$(( $(date +%s) - start ))
if [ "$rc" != 0 ] && [ "$el" -le 5 ]; then ok "a heartbeat EOF kills the run within 5 s (${el}s)"; else bad "a heartbeat EOF kill" "rc=$rc after ${el}s" "$out"; fi
if grep -qF 'shared-run: the caller of run r-eof is gone (heartbeat ended) — killing the run' <<<"$out"; then ok "the EOF kill is named"; else bad "the EOF kill is named" "$out"; fi
if ! pgrep -f "$eof_sleep" >/dev/null; then ok "a killed run leaves no process"; else bad "a killed run left a process" "$(pgrep -af "$eof_sleep")"; fi
# The stalled caller: a writer holding the run's stdin open without a beat. A fifo, not < <(…): the
# command substitution would wait for its process substitution's exit.
mkfifo "$T/stall"
( exec 4>"$T/stall"; sleep 30 ) &
start=$(date +%s)
out="$(bash "$SUT" run r-stall shared "$TREE" - wt -- bash -c 'sleep 61' 2>&1 <"$T/stall")"; rc=$?
el=$(( $(date +%s) - start ))
pkill -f '^sleep 30$' 2>/dev/null
if [ "$rc" != 0 ] && [ "$el" -le 5 ]; then ok "a stalled heartbeat kills the run after PFM_FENCE_HEARTBEAT_S (${el}s)"; else bad "a stalled heartbeat kill" "rc=$rc after ${el}s" "$out"; fi
if ! pgrep -f 'sleep 61' >/dev/null; then ok "a stalled run leaves no process"; else bad "a stalled run left a process" "$(pgrep -af 'sleep 61')"; fi
( srun r-kill shared 'sleep 62' >"$T/kill.out"; echo "$?" >"$T/kill.rc" ) &
wait_for "$FENCE_STATE_DIR/runs/r-kill/ns.pid" || bad "run r-kill never started" "$(cat "$T/kill.out")"
start=$(date +%s)
bash "$SUT" kill r-kill
wait
el=$(( $(date +%s) - start ))
if [ "$(cat "$T/kill.rc")" != 0 ] && [ "$el" -le 3 ] && ! pgrep -f 'sleep 62' >/dev/null; then ok "kill <id> ends the run and all it holds (${el}s)"
else bad "kill <id>" "rc=$(cat "$T/kill.rc") after ${el}s" "$(pgrep -af 'sleep 62')"; fi

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
out="$(bash "$SUT" run r-tty tty "$TREE" - wt -- bash -c 'sleep 3; exit 3' </dev/null 2>&1)"; rc=$?
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
  wait_gone() { # wait_gone <pid> <tenths>: 0 once the pid is gone
    local i
    for ((i = 0; i < $2; i++)); do kill -0 "$1" 2>/dev/null || return 0; sleep 0.1; done
    return 1
  }
  # idle: a live run holds it past PFM_FENCE_IDLE_S; once none is live it closes and exits 0
  mkdir -p /var/fence/live /var/fence/runs
  sleep 30 & held=$!
  echo "$held" > /var/fence/live/d-held
  PFM_FENCE_IDLE_S=1 bash -c "$DAEMON" & dmn=$!
  sleep 4
  if kill -0 "$dmn" 2>/dev/null; then ok "the container stays up while a run is live, past its idle time"
  else bad "the container exited with a live run"; fi
  kill "$held" 2>/dev/null; wait "$held" 2>/dev/null
  if wait_gone "$dmn" 80; then
    wait "$dmn"; rc=$?
    if [ "$rc" = 0 ] && [ -d /var/fence/closing ]; then ok "with no live run past PFM_FENCE_IDLE_S it marks closing and exits 0"
    else bad "the idle exit" "rc=$rc closing=$(ls -d /var/fence/closing 2>&1)"; fi
  else bad "no idle exit within 8 s of the last run's end"; kill -KILL "$dmn" 2>/dev/null; fi
  rm -rf /var/fence/*
  # reap: a dead supervisor's run is killed and its records removed; superseded drains, then exits
  mkdir -p /var/fence/live /var/fence/runs/d-dead
  sleep 31 & orphan=$!
  echo 999999 > /var/fence/live/d-dead; echo "$orphan" > /var/fence/runs/d-dead/ns.pid
  PFM_FENCE_IDLE_S=600 bash -c "$DAEMON" & dmn=$!
  if wait_gone "$orphan" 50 && [ ! -e /var/fence/live/d-dead ] && [ ! -e /var/fence/runs/d-dead ]; then
    ok "a run whose supervisor died is killed and its records removed"
  else bad "the dead supervisor's run" "orphan alive=$(kill -0 "$orphan" 2>/dev/null && echo yes || echo no)" "$(ls -R /var/fence)"; kill "$orphan" 2>/dev/null; fi
  wait "$orphan" 2>/dev/null
  sleep 30 & held=$!
  echo "$held" > /var/fence/live/d-held2
  touch /var/fence/superseded
  sleep 3
  if kill -0 "$dmn" 2>/dev/null; then ok "a superseded container drains: it stays up while a run is live"
  else bad "a superseded container exited with a live run"; fi
  kill "$held" 2>/dev/null; wait "$held" 2>/dev/null
  if wait_gone "$dmn" 60; then ok "a superseded container exits once its last live run ends"
  else bad "the superseded container did not exit within 6 s of its last run"; kill -KILL "$dmn" 2>/dev/null; fi
  wait "$dmn" 2>/dev/null
  umount /var/fence
else bad "the container daemon's text could not be read from $FENCE/shared.sh, or no tmpfs on /var/fence"; fi

shtest_end

# shellcheck shell=bash
# The one wait door of this repo's shell tests: a test that waits for something outside its own shell
# (a background process, a file a fake writes, a FIFO line) waits here, on a NAMED condition, and passes
# no duration. The door polls at its own pace and gives up only at SHTEST_HUNG_LIMIT_S, the hung-only
# limit (120 s, the Go door's testwait.HungLimit): it turns a real hang into a failure naming what was
# awaited, and no test passes or fails by it in normal running. This file is the one place a shell test
# touches the real clock; scripts/shtest-clock-check.sh refuses it everywhere else.
#   shtest_wait_until <name> <command> [args...]  polls until the command exits 0
#   shtest_wait_gone <name> <pid>                 polls until the process is gone (a child: `wait` it after)
#   shtest_wait_line <name> <fifo>                blocks for one line a writer puts in the FIFO; sets SHTEST_LINE
#   shtest_hold [marker]                          blocks until killed: a fake that must stay alive (`shtest_hold m &`);
#                                                 writes nothing and keeps the caller's stderr open (Go twin: testwait.Hold)
# BROKEN STATE: a condition not reached within the limit prints `WAIT HUNG {name}` on stderr, returns 1 and
# counts in SHTEST_WAITS_HUNG, which the harnesses' verdicts (shtest_end, testlib's finish) add to their failures,
# so a wait whose status a test ignores still turns its suite red.
# Sourced by scripts/shtest.sh and infra/plugin-lab/tests/testlib.sh; source it alone where neither is.
SHTEST_HUNG_LIMIT_S=120
SHTEST_WAIT_PACE_S=0.05
SHTEST_WAITS_HUNG=0 # the waits of this shell that hung: the harness's verdict counts each as a failure

shtest_wait_hung() { # shtest_wait_hung <name>: the one failure line
  printf 'WAIT HUNG %s: not reached within the %ss hung-only limit\n' "$1" "$SHTEST_HUNG_LIMIT_S" >&2
  SHTEST_WAITS_HUNG=$((SHTEST_WAITS_HUNG + 1))
  return 1
}

shtest_wait_until() {
  local name=$1 start=$SECONDS
  shift
  until "$@"; do
    (( SECONDS - start < SHTEST_HUNG_LIMIT_S )) || { shtest_wait_hung "$name"; return 1; }
    sleep "$SHTEST_WAIT_PACE_S"
  done
}

shtest_wait_gone() {
  shtest_wait_until "$1" shtest_wait_pid_gone "$2"
}
shtest_wait_pid_gone() { ! kill -0 "$1" 2>/dev/null; }

shtest_wait_line() {
  local fd rc
  # read-write open never blocks on a FIFO with no writer yet, and keeps the read from seeing EOF between writers
  exec {fd}<>"$2" || { printf 'WAIT %s: cannot open the FIFO %s\n' "$1" "$2" >&2; return 1; }
  # shellcheck disable=SC2034 # SHTEST_LINE is the caller's result
  IFS= read -r -t "$SHTEST_HUNG_LIMIT_S" -u "$fd" SHTEST_LINE; rc=$?
  exec {fd}<&-
  (( rc == 0 )) || { shtest_wait_hung "$1"; return 1; }
}

shtest_hold() { # tail warns on stderr where the host has no inotify instance to spare: fd 9 keeps the caller's
  # stderr open, unwritten, and stderr goes to /dev/null
  exec -a "shtest-hold${1:+ $1}" tail -f /dev/null 9>&2 2>/dev/null
}

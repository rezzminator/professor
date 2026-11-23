#!/usr/bin/env bash
# Self-test of scripts/shtest-wait.sh, the one wait door of this repo's shell tests: each form returns
# once its named condition holds, and a condition never reached fails naming it (the hung-only limit
# is set to 0 here, so the give-up path runs at once instead of after 120 s).
set -uo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SHTEST_TAG=shtest-wait-test
# shellcheck source=scripts/shtest.sh
source "$ROOT/scripts/shtest.sh"

[ "$SHTEST_HUNG_LIMIT_S" = 120 ] && ok 'the hung-only limit is 120 s' || bad 'the hung-only limit is 120 s' "actual $SHTEST_HUNG_LIMIT_S"

# until: returns 0 once the condition holds, made true by a writer the test releases.
mkfifo "$T/go"
( IFS= read -r _ < "$T/go"; : > "$T/ready" ) &
writer=$!
printf 'go\n' > "$T/go"
if shtest_wait_until 'ready file written' test -e "$T/ready"; then ok 'until: returns once the condition holds'
else bad 'until: returns once the condition holds' "no $T/ready"; fi
wait "$writer"

# gone: returns once a held fake is killed; the fake was alive before the kill.
shtest_hold wait-test &
held=$!
shtest_wait_until 'hold started' test "$(ps -o args= -p "$held" 2>/dev/null)" = 'shtest-hold wait-test -f /dev/null' \
  && ok 'hold: the fake stays alive under its marker' || bad 'hold: the fake stays alive under its marker' "ps args: $(ps -o args= -p "$held")"
kill "$held"
if shtest_wait_gone 'held fake exited' "$held"; then ok 'gone: returns once the process exits'; else bad 'gone: returns once the process exits'; fi
wait "$held" 2>/dev/null

# hold: writes nothing where tail warns on stderr (a host with no inotify instance to spare): a tail that
# prints the runner's warning, then execs the real one, stands first on PATH.
mkdir "$T/warn-bin"
printf '#!/bin/sh\necho "tail: inotify cannot be used, reverting to polling: Too many open files" >&2\n: > "%s"\nexec %s "$@"\n' \
  "$T/warned" "$(command -v tail)" > "$T/warn-bin/tail"
chmod +x "$T/warn-bin/tail"
( export PATH="$T/warn-bin:$PATH"; shtest_hold quiet-test ) 2> "$T/hold.err" &
held=$!
shtest_wait_until 'the warning tail ran' test -e "$T/warned"
kill "$held"
wait "$held" 2>/dev/null
if [ "$(cat "$T/hold.err")" = '' ]; then ok 'hold: writes nothing on its stderr where tail warns'
else bad 'hold: writes nothing on its stderr where tail warns' "expected: (empty)" "actual: $(cat "$T/hold.err")"; fi

# line: blocks for one line a writer puts in the FIFO and hands it back.
mkfifo "$T/lines"
printf 'booted 42\n' > "$T/lines" &
if shtest_wait_line 'boot line' "$T/lines" && [ "$SHTEST_LINE" = 'booted 42' ]; then ok 'line: hands back the line written'
else bad 'line: hands back the line written' "expected: booted 42" "actual: ${SHTEST_LINE:-}"; fi
wait

# The give-up path, with the limit at 0: the exact failure line and rc 1, for each form.
SHTEST_HUNG_LIMIT_S=0
err="$(shtest_wait_until 'never true' false 2>&1)"; rc=$?
[ "$rc" = 1 ] && [ "$err" = 'WAIT HUNG never true: not reached within the 0s hung-only limit' ] \
  && ok 'until: an unreached condition fails naming it' || bad 'until: an unreached condition fails naming it' "rc $rc: $err"
mkfifo "$T/silent"
err="$(shtest_wait_line 'silent writer' "$T/silent" 2>&1)"; rc=$?
[ "$rc" = 1 ] && [ "$err" = 'WAIT HUNG silent writer: not reached within the 0s hung-only limit' ] \
  && ok 'line: a FIFO nobody writes fails naming it' || bad 'line: a FIFO nobody writes fails naming it' "rc $rc: $err"

# A hung wait whose status the suite ignores still turns the suite red, in both harnesses.
printf '%s\n' 'SHTEST_TAG=shtest-wait-child; . "$1/scripts/shtest.sh"; SHTEST_HUNG_LIMIT_S=0' \
  'shtest_wait_until "ignored wait" false; ok "after the wait"; shtest_end' > "$T/child.sh"
out="$(bash "$T/child.sh" "$ROOT" 2>&1)"; rc=$?
want=$'WAIT HUNG ignored wait: not reached within the 0s hung-only limit\nPASS  after the wait\nFAIL  1 waits hung (each WAIT HUNG line above)\n\n1 passed, 1 failed'
[ "$rc" = 1 ] && [ "$out" = "$want" ] && ok 'shtest_end: an ignored hung wait is a failure' \
  || bad 'shtest_end: an ignored hung wait is a failure' "expected rc 1:" "$want" "actual rc $rc:" "$out"
printf '%s\n' 'LAB_DIR="$1/infra/plugin-lab"; . "$LAB_DIR/tests/testlib.sh"; SHTEST_HUNG_LIMIT_S=0' \
  'shtest_wait_until "ignored wait" false; ok "after the wait"; finish' > "$T/labchild.sh"
out="$(bash "$T/labchild.sh" "$ROOT" 2>&1)"; rc=$?
want=$'WAIT HUNG ignored wait: not reached within the 0s hung-only limit\nok - after the wait\nFAIL - 1 waits hung (each WAIT HUNG line above)\nlabchild.sh: 1 passed, 1 failed'
[ "$rc" = 1 ] && [ "$out" = "$want" ] && ok "testlib finish: an ignored hung wait is a failure" \
  || bad "testlib finish: an ignored hung wait is a failure" "expected rc 1:" "$want" "actual rc $rc:" "$out"

shtest_end

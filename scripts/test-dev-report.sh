#!/usr/bin/env bash
# Regression for dev.sh's go_test_report under `set -euo pipefail`: a read of a
# failing `go test -json` stream must reach its closing `log:` line. A filter
# that matches nothing (grep exits 1) or a `head` that closes its pipe early
# (SIGPIPE) inside the report used to abort the whole gate with no verdict.
#   bash scripts/test-dev-report.sh [path/to/dev.sh]
set -uo pipefail
DEV=${1:-"$(cd "$(dirname "$0")/.." && pwd)/.claude/scripts/dev.sh"}
[[ -f "$DEV" ]] || { printf 'test-dev-report: dev.sh not found: %s\n' "$DEV" >&2; exit 2; }
SHTEST_TAG=dev-report
# shellcheck source=scripts/shtest.sh
source "$(dirname "$0")/shtest.sh"

FUNCS="$T/funcs.sh"
awk '/^trim_line\(\) \{/,/^\}/ { print } /^go_test_report\(\) \{/,/^\}/ { print }' "$DEV" > "$FUNCS"
grep -q '^go_test_report() {' "$FUNCS" && grep -q '^trim_line() {' "$FUNCS" \
  || { echo "test-dev-report: go_test_report or trim_line not found in $DEV — the harness did not start" >&2; exit 2; }

# report <json>: run the extracted report exactly as dev.sh does, under -euo pipefail.
report() {
  bash -c 'set -euo pipefail
    info() { printf "info: %s\n" "$*"; }; fail_step() { printf "fail_step: %s\n" "$*"; }
    source "$1"; go_test_report "$2"' _ "$FUNCS" "$1" 2>&1
}

# Case 1: a package fails with no failing test and its only output is the FAIL line.
J1="$T/pkg-fail-filtered.json"
cat > "$J1" <<'EOF'
{"Action":"run","Package":"p/ok","Test":"TestFine"}
{"Action":"pass","Package":"p/ok","Test":"TestFine","Elapsed":0}
{"Action":"output","Package":"p/a","Output":"FAIL\tp/a\t0.01s\n"}
{"Action":"fail","Package":"p/a","Elapsed":0.01}
EOF
out=$(report "$J1"); rc=$?
if [[ $rc -eq 0 && "$out" == *"FAIL  p/a — the package failed with no failing test"* && "$out" == *"info: log: "* ]]; then
  ok "a failed package whose output is only its FAIL line reaches the log line"
else
  bad "a failed package whose output is only its FAIL line aborted the report (rc $rc)" "${out:-<no output>}"
fi

# Case 2: a failing test whose output runs far past the cap (head closes the pipe early).
J2="$T/test-fail-long.json"
{
  echo '{"Action":"run","Package":"p/b","Test":"TestLoud"}'
  for i in $(seq 1 4000); do
    printf '{"Action":"output","Package":"p/b","Test":"TestLoud","Output":"line %d %s\\n"}\n' "$i" "$(printf 'x%.0s' $(seq 1 120))"
  done
  echo '{"Action":"fail","Package":"p/b","Test":"TestLoud","Elapsed":0.1}'
  echo '{"Action":"fail","Package":"p/b","Elapsed":0.1}'
} > "$J2"
out=$(report "$J2"); rc=$?
if [[ $rc -eq 0 && "$out" == *"FAIL  p/b TestLoud"* && "$out" == *"more output line(s) in the log"* && "$out" == *"info: log: "* ]]; then
  ok "a failing test with output past the cap reaches the log line"
else
  bad "a failing test with output past the cap aborted the report (rc $rc)" "$(printf '%s\n' "$out" | tail -3)"
fi

# Case 3: a package build failure whose output runs far past the cap.
J3="$T/pkg-fail-long.json"
{
  echo '{"Action":"run","Package":"p/ok","Test":"TestFine"}'
  for i in $(seq 1 4000); do
    printf '{"Action":"output","Package":"p/c","Output":"p/c/x.go:%d: undefined: thing %s\\n"}\n' "$i" "$(printf 'y%.0s' $(seq 1 120))"
  done
  echo '{"Action":"fail","Package":"p/c","Elapsed":0.1}'
} > "$J3"
out=$(report "$J3"); rc=$?
if [[ $rc -eq 0 && "$out" == *"FAIL  p/c — the package failed with no failing test"* && "$out" == *"info: log: "* ]]; then
  ok "a package build failure with output past the cap reaches the log line"
else
  bad "a package build failure with output past the cap aborted the report (rc $rc)" "$(printf '%s\n' "$out" | tail -3)"
fi

# Case 4: a stream with no test event whose output lines are all blank.
J4="$T/no-events-blank.json"
printf '{"Action":"output","Package":"p/d","Output":"\\n"}\n{"Action":"fail","Package":"p/d","Elapsed":0}\n' > "$J4"
out=$(report "$J4"); rc=$?
if [[ $rc -eq 0 && "$out" == *"NO TEST EVENTS"* && "$out" == *"info: log: "* ]]; then
  ok "a stream with no test event and only blank output reaches the log line"
else
  bad "a stream with no test event and only blank output aborted the report (rc $rc)" "${out:-<no output>}"
fi

shtest_end

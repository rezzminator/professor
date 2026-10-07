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
  awk 'BEGIN {
    for (j = 1; j <= 120; j++) pad = pad "x"
    for (i = 1; i <= 4000; i++)
      printf "{\"Action\":\"output\",\"Package\":\"p/b\",\"Test\":\"TestLoud\",\"Output\":\"line %d %s\\n\"}\n", i, pad
  }'
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
  awk 'BEGIN {
    for (j = 1; j <= 120; j++) pad = pad "y"
    for (i = 1; i <= 4000; i++)
      printf "{\"Action\":\"output\",\"Package\":\"p/c\",\"Output\":\"p/c/x.go:%d: undefined: thing %s\\n\"}\n", i, pad
  }'
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

# Case 6: a red stream holding both failure kinds reads to exactly this report: failing
# tests sorted and deduplicated, a package that holds a failing test not listed again,
# a package failing with no test listed with its own output.
J6="$T/red-mixed.json"
cat > "$J6" <<'EOF'
{"Action":"run","Package":"p/a","Test":"TestTwo"}
{"Action":"output","Package":"p/a","Test":"TestTwo","Output":"--- FAIL: TestTwo (0.00s)\n"}
{"Action":"output","Package":"p/a","Test":"TestTwo","Output":"two broke\n"}
{"Action":"fail","Package":"p/a","Test":"TestTwo","Elapsed":0}
{"Action":"run","Package":"p/a","Test":"TestOne"}
{"Action":"output","Package":"p/a","Test":"TestOne","Output":"boom\n"}
{"Action":"fail","Package":"p/a","Test":"TestOne","Elapsed":0}
{"Action":"fail","Package":"p/a","Test":"TestOne","Elapsed":0}
{"Action":"fail","Package":"p/a","Elapsed":0}
{"Action":"output","Package":"p/b","Output":"p/b/x.go:1: undefined: q\n"}
{"Action":"output","Package":"p/b","Output":"FAIL\tp/b [build failed]\n"}
{"Action":"fail","Package":"p/b","Elapsed":0}
{"Action":"run","Package":"p/ok","Test":"TestFine"}
{"Action":"pass","Package":"p/ok","Test":"TestFine","Elapsed":0}
EOF
want="  FAIL  p/a TestOne
        boom
  FAIL  p/a TestTwo
        two broke
  FAIL  p/b — the package failed with no failing test (build or setup error)
        p/b/x.go:1: undefined: q
info: 3 failing test(s)/package(s) summarised above, capped at 25 output line(s) each
info: log: $(cd "$(dirname "$J6")" && pwd)/$(basename "$J6")"
out=$(report "$J6"); rc=$?
if [[ $rc -eq 0 && "$out" == "$want" ]]; then
  ok "a red stream with failing tests and a failing package reads to the exact report"
else
  bad "a red stream with failing tests and a failing package read to a different report (rc $rc)" "$(diff <(printf '%s\n' "$want") <(printf '%s\n' "$out"))"
fi

# Case 5: a run dir and its base are readable by a non-root reader, even under
# umask 077 (the fence runs as root; the host reads its timing TSVs), and an
# uncreatable base fails naming the base with no path printed.
TRD="$T/timing-run-dir.sh"
awk '/^timing_run_dir\(\) \{/,/^\}/ { print }' "$DEV" > "$TRD"
if ! grep -q '^timing_run_dir() {' "$TRD"; then
  bad "timing_run_dir not found in $DEV — the timing run dir cannot be checked"
else
  base="$T/timing-base/nested"
  out=$(bash -c 'umask 077; source "$1"; timing_run_dir "$2"' _ "$TRD" "$base" 2>"$T/trd.err"); rc=$?
  mode=$( [[ -n "$out" && -d "$out" ]] && ls -ld "$out" | cut -c1-10 )
  base_mode=$( [[ -d "$base" ]] && ls -ld "$base" | cut -c1-10 )
  if [[ $rc -eq 0 && "$out" == "$base"/run.* && -d "$out" && "$mode" == "drwxr-xr-x" && "$base_mode" == "drwxr-xr-x" ]]; then
    ok "an absent base and its run dir are readable by a non-root reader under umask 077"
  else
    bad "an absent base or its run dir is not readable by a non-root reader under umask 077 (rc $rc, run dir ${mode:-none}, base ${base_mode:-none})" "stdout: ${out:-<none>}" "stderr: $(cat "$T/trd.err")"
  fi
  : > "$T/plain-file"
  base="$T/plain-file/timing"
  out=$(bash -c 'source "$1"; timing_run_dir "$2"' _ "$TRD" "$base" 2>"$T/trd.err"); rc=$?
  err=$(cat "$T/trd.err")
  if [[ $rc -ne 0 && -z "$out" && "$err" == *"$base"* ]]; then
    ok "an uncreatable timing base fails, names the base on stderr and prints no path"
  else
    bad "an uncreatable timing base did not fail cleanly (rc $rc)" "stdout: ${out:-<none>}" "stderr: ${err:-<none>}"
  fi
fi

shtest_end

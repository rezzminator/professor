#!/usr/bin/env bash
# Regression for dev.sh's go_test_report under `set -euo pipefail`: a read of a
# failing `go test -json` stream must reach its closing `log:` line. A filter
# that matches nothing (grep exits 1) or a `head` that closes its pipe early
# (SIGPIPE) inside the report used to abort the whole gate with no verdict.
# Also node_test_suite: the exact verdict line of an unsharded and a sharded run, each test
# run once across the shards, and each state that proves nothing named.
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

# node_test_suite: a three-test fixture through the real runner and scripts/node-test-helpers.mjs.
# Each test appends its name to $RAN, so a test run twice or never shows in the sorted list.
NTS="$T/node-test-suite.sh"
HELPERS="$(cd "$(dirname "$0")" && pwd)/node-test-helpers.mjs"
awk '/^node_test_suite\(\) \{/,/^\}/ { print }' "$DEV" > "$NTS"
if ! grep -q '^node_test_suite() {' "$NTS"; then
  bad "node_test_suite not found in $DEV — the node suite runner cannot be checked"
elif ! command -v node >/dev/null 2>&1; then
  bad "node is not on PATH — the node suite runner cannot be checked"
else
  mkdir -p "$T/node"
  node_fixture() { # node_fixture <file> <name of the test that fails, or none>
    cat > "$1" <<FIXTURE
import { appendFileSync } from "node:fs";
import { test, assert } from "$HELPERS";
for (const name of ["alpha", "beta", "gamma"]) {
  test(name, () => { appendFileSync(process.env.RAN, name + "\\n"); assert.notEqual(name, "$2"); });
}
FIXTURE
  }
  node_fixture "$T/node/green.test.mjs" none
  node_fixture "$T/node/red.test.mjs" beta
  suite() { # suite <NODE_TEST_SHARDS or ''> <file>: the extracted runner under dev.sh's shell options
    : > "$T/node/ran"
    RAN="$T/node/ran" NODE_TEST_SHARDS="$1" bash -c 'set -euo pipefail
      ok() { printf "ok: %s\n" "$*"; }; fail_step() { printf "fail_step: %s\n" "$*"; }
      source "$1"; node_test_suite three "$2" "$3"' _ "$NTS" "$T/node/tap" "$2" 2>&1
  }
  failed='fail_step: three tests FAILED — a test regressed, or node could not run the suite (see output)'
  # shards | fixture | the runner's last line | the tests that ran, sorted (a fourth shard holds no test)
  while IFS='|' read -r shards fixture want_last want_ran; do
    out=$(suite "$shards" "$T/node/$fixture.test.mjs"); rc=$?
    last=$(printf '%s\n' "$out" | tail -n 1); ran=$(sort "$T/node/ran" | paste -sd' ' -)
    if [[ $rc -eq 0 && "$last" == "$want_last" && "$ran" == "$want_ran" ]]; then
      ok "node_test_suite, ${shards:-no} shard(s), $fixture fixture: $want_last"
    else
      bad "node_test_suite, ${shards:-no} shard(s), $fixture fixture (rc $rc)" "expected: $want_last | ran: $want_ran" "actual:   $last | ran: $ran" "tap: $T/node/tap"
    fi
  done <<CASES
|green|ok: three tests hold (3 passing)|alpha beta gamma
2|green|ok: three tests hold (3 passing)|alpha beta gamma
3|green|ok: three tests hold (3 passing)|alpha beta gamma
4|green|$failed|alpha beta gamma
|red|$failed|alpha beta gamma
2|red|$failed|alpha beta gamma
0|green|fail_step: three tests NOT RUN — NODE_TEST_SHARDS must be a positive integer: 0|
CASES
  # Two shards split the three tests round-robin, each stream under its own heading.
  suite 2 "$T/node/green.test.mjs" >/dev/null
  split=$(grep -E '^(# shard |ok [0-9]+ - )' "$T/node/tap")
  want_split=$'# shard 1/2\nok 1 - alpha\nok 2 - gamma\n# shard 2/2\nok 1 - beta'
  if [[ "$split" == "$want_split" ]]; then
    ok "two shards take every second test in registration order, each stream under its heading"
  else
    bad "two shards split the suite differently" "$(diff <(printf '%s\n' "$want_split") <(printf '%s\n' "$split"))" "tap: $T/node/tap"
  fi
  if grep -qx '# NODE_TEST_SHARD 4/4 holds no test: the suite registers 3' <(suite 4 "$T/node/green.test.mjs"); then
    ok "a shard left with no test names itself in the output"
  else
    bad "a shard left with no test did not name itself" "tap: $T/node/tap"
  fi
  err=$(RAN="$T/node/ran" NODE_TEST_SHARD=3/2 node "$T/node/green.test.mjs" 2>&1); rc=$?
  if [[ $rc -ne 0 && "$err" == *'NODE_TEST_SHARD must be {i}/{n} with 1 <= i <= n: 3/2'* ]]; then
    ok "a shard index past its count is refused by name before any test registers"
  else
    bad "a shard index past its count was not refused (rc $rc)" "$(printf '%s\n' "$err" | tail -3)"
  fi
fi

shtest_end

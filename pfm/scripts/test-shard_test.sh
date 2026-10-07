#!/usr/bin/env bash
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SUT="${TEST_SHARD_SUT:-$ROOT/scripts/test-shard.sh}"
SHTEST_TAG=pfm-test-shard-test
source "$ROOT/../scripts/shtest.sh"
PKG=github.com/rezzminator/professor/pfm/cmd/pfm
QUICK=github.com/rezzminator/professor/pfm/internal/quick

cat >"$T/a.json" <<JSON
{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"$PKG","Test":"TestA"}
{"Time":"2024-01-01T00:00:04Z","Action":"pass","Package":"$PKG","Test":"TestA","Elapsed":4}
{"Time":"2024-01-01T00:00:04Z","Action":"pass","Package":"$PKG","Elapsed":4}
JSON
cat >"$T/b.json" <<JSON
{"Time":"2024-01-01T00:00:01Z","Action":"run","Package":"$PKG","Test":"TestB"}
{"Time":"2024-01-01T00:00:08Z","Action":"pass","Package":"$PKG","Test":"TestB","Elapsed":7}
{"Time":"2024-01-01T00:00:08Z","Action":"pass","Package":"$PKG","Elapsed":7}
JSON
cat >"$T/quick.json" <<JSON
{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"$QUICK","Test":"TestQuick"}
{"Time":"2024-01-01T00:00:01Z","Action":"pass","Package":"$QUICK","Test":"TestQuick","Elapsed":1}
{"Time":"2024-01-01T00:00:01Z","Action":"pass","Package":"$QUICK","Elapsed":1}
JSON

check_merge() {
  local label="$1" expected_rc="$2" expected_action="$3"; shift 3
  local rc=0
  bash "$SUT" merge --out "$T/merged.json" "$@" >"$T/merge.log" 2>&1 || rc=$?
  if [ "$rc" -eq "$expected_rc" ] && python3 - "$T/merged.json" "$PKG" "$expected_action" <<'PY'
import json, sys
events = [json.loads(line) for line in open(sys.argv[1])]
terminals = [e for e in events if e.get('Package') == sys.argv[2] and not e.get('Test') and e.get('Action') in ('pass', 'fail', 'skip')]
assert len(terminals) == 1, terminals
assert terminals[0]['Action'] == sys.argv[3], terminals
assert terminals[0]['Elapsed'] == 7, terminals
assert terminals[0]['Time'] == '2024-01-01T00:00:08Z', terminals
PY
  then ok "$label"; else bad "$label: rc=$rc" "$(cat "$T/merge.log")"; fi
}
check_merge merge-pass 0 pass "$T/quick.json" "$T/a.json" "$T/b.json"
if cmp -s "$T/quick.json" <(head -n 3 "$T/merged.json"); then ok unsharded-identical; else bad unsharded-identical; fi
cat >"$T/timing.yml" <<YML
tolerance: 1.25
fail_factor: 2
suites:
  u:
    wall_s: 20
    packages:
      $PKG: 10
      $QUICK: 2
YML
rc=0; bash "$ROOT/scripts/test-timing.sh" --check --yml "$T/timing.yml" --suite u --out "$T/timing.tsv" "$T/merged.json" >"$T/timing.log" 2>&1 || rc=$?
if [ "$rc" -eq 0 ] && ! grep -q 'TIMING-INCOMPLETE' "$T/timing.log"; then ok timing-accepts-merge; else bad "timing-accepts-merge: rc=$rc" "$(cat "$T/timing.log")"; fi
python3 - "$T/b.json" "$T/b-fail.json" <<'PY'
import sys
text = open(sys.argv[1]).read()
text = text.replace('"Action":"pass","Package":"github.com/rezzminator/professor/pfm/cmd/pfm","Elapsed":7', '"Action":"fail","Package":"github.com/rezzminator/professor/pfm/cmd/pfm","Elapsed":7')
open(sys.argv[2], 'w').write(text)
PY
check_merge merge-fail 1 fail "$T/a.json" "$T/b-fail.json"
head -n 2 "$T/b.json" >"$T/b-truncated.json"
rc=0; bash "$SUT" merge --out "$T/merged.json" "$T/a.json" "$T/b-truncated.json" >"$T/merge.log" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && python3 - "$T/merged.json" "$PKG" <<'PY'
import json, sys
events = [json.loads(line) for line in open(sys.argv[1])]
assert [e['Action'] for e in events if e.get('Package') == sys.argv[2] and not e.get('Test') and e.get('Action') in ('pass','fail')] == ['fail']
PY
then ok missing-terminal; else bad "missing-terminal: rc=$rc" "$(cat "$T/merge.log")"; fi
# go test -json reports a compile failure by ImportPath, with no Package field.
cat >"$T/quick-build.json" <<JSON
{"ImportPath":"$QUICK [$QUICK.test]","Action":"build-output","Output":"# $QUICK [$QUICK.test]\n"}
{"ImportPath":"$QUICK [$QUICK.test]","Action":"build-output","Output":"quick_test.go:3:40: cannot use \"s\" as int value\n"}
{"ImportPath":"$QUICK [$QUICK.test]","Action":"build-fail"}
{"Time":"2024-01-01T00:00:00Z","Action":"start","Package":"$QUICK"}
{"Time":"2024-01-01T00:00:00Z","Action":"output","Package":"$QUICK","Output":"FAIL\t$QUICK [build failed]\n"}
{"Time":"2024-01-01T00:00:00Z","Action":"fail","Package":"$QUICK","Elapsed":0,"FailedBuild":"$QUICK [$QUICK.test]"}
JSON
rc=0; bash "$SUT" merge --out "$T/merged.json" "$T/quick-build.json" "$T/a.json" "$T/b.json" >"$T/merge.log" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && python3 - "$T/merged.json" "$PKG" "$QUICK" <<'PY'
import json, sys
events = [json.loads(line) for line in open(sys.argv[1])]
builds = [e for e in events if e.get('Action') == 'build-output' and 'cannot use' in e.get('Output', '')]
assert len(builds) == 1, events
def terminal(package):
    return [e['Action'] for e in events if e.get('Package') == package and not e.get('Test') and e.get('Action') in ('pass', 'fail', 'skip')]
assert terminal(sys.argv[3]) == ['fail'], events
assert terminal(sys.argv[2]) == ['pass'], events
PY
then ok build-failure-kept; else bad "build-failure-kept: rc=$rc" "$(cat "$T/merge.log")"; fi

mkdir -p "$T/bin"
cat >"$T/bin/go" <<'SH'
#!/usr/bin/env bash
set -eu
printf '%s\n' "$*" >>"$STUB_LOG"
case "$1" in
  list)
    [ "${STUB_LIST_FAIL:-0}" = 0 ] || exit 9
    if [ -n "${STUB_LIST_PACKAGES:-}" ]; then printf '%s\n' $STUB_LIST_PACKAGES
    else printf '%s\n' github.com/rezzminator/professor/pfm/cmd/pfm github.com/rezzminator/professor/pfm/internal/quick; fi ;;
  build)
    target="${*: -1}"
    if [ -n "${STUB_BUILD_RENDEZVOUS:-}" ]; then
      # A directory: each build leaves its mark and waits (at most 5 s) for the other's; one that waits alone says so.
      : >"$STUB_BUILD_RENDEZVOUS/${target##*/}"
      tick=0
      while [ "$(ls "$STUB_BUILD_RENDEZVOUS" | wc -l)" -lt 2 ] && [ "$tick" -lt 50 ]; do sleep 0.1; tick=$((tick + 1)); done
      if [ "$(ls "$STUB_BUILD_RENDEZVOUS" | wc -l)" -ge 2 ]; then printf 'build-overlap %s\n' "$target" >>"$STUB_LOG"; else printf 'build-alone %s\n' "$target" >>"$STUB_LOG"; fi
    fi
    if [[ " $* " == *" ./cmd/mock-engine "* ]]; then
      [ "${STUB_MOCK_PREBUILD_FAIL:-0}" = 0 ] || { printf 'mock-build.go:7: broken fixture\n' >&2; exit 1; }
    else
      [ "${STUB_PREBUILD_FAIL:-0}" = 0 ] || { printf 'prebuild.go:7: broken fixture\n' >&2; exit 1; }
    fi
    while [ "$1" != -o ]; do shift; done
    shift
    printf '#!/bin/sh\nexit 0\n' >"$1"
    chmod +x "$1"
    printf 'build-done %s\n' "$target" >>"$STUB_LOG" ;;
  test)
    printf 'child-env %s\n' "${PFM_TEST_PFM_BINARY-<unset>}" >>"$STUB_LOG"
    printf 'mock-env %s\n' "${PFM_TEST_MOCK_ENGINE_BINARY-<unset>}" >>"$STUB_LOG"
    args=" $* "
    if [[ "$args" == *" -c "* ]]; then
      [ "${STUB_COMPILE_FAIL:-0}" = 0 ] || { printf 'compile.go:7: broken fixture\n' >&2; exit 1; }
      while [ "$1" != -o ]; do shift; done
      shift
      binary="$1"
      cat >"$binary" <<'BIN'
#!/usr/bin/env bash
set -eu
printf 'binary %s\n' "$*" >>"$STUB_LOG"
printf 'child-env %s\n' "${PFM_TEST_PFM_BINARY-<unset>}" >>"$STUB_LOG"
printf 'mock-env %s\n' "${PFM_TEST_MOCK_ENGINE_BINARY-<unset>}" >>"$STUB_LOG"
if [[ " $* " == *" -test.list "* ]]; then
  [ "${STUB_TEST_LIST_FAIL:-0}" = 0 ] || { echo 'fixture list failed' >&2; exit 8; }
  printf 'TestHeavyA\nTestHeavyB\nTestLost\nExampleOptional\nBenchmarkDrop\n'
  exit 0
fi
for name in TestHeavyA TestHeavyB ${STUB_UNSHARDED_KILLED:+TestLost}; do
  [[ " $* " == *"$name"* ]] || continue
  printf '{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"%s","Test":"%s"}\n' "$STUB_PACKAGE" "$name"
  printf '{"Time":"2024-01-01T00:00:01Z","Action":"pass","Package":"%s","Test":"%s","Elapsed":1}\n' "$STUB_PACKAGE" "$name"
done
printf '{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"%s","Elapsed":2}\n' "$STUB_PACKAGE"
BIN
      chmod +x "$binary"
      exit 0
    fi
    if [[ "$args" == *" -list "* ]]; then
      [ "${STUB_TEST_LIST_FAIL:-0}" = 0 ] || exit 8
      printf 'TestHeavyA\nTestHeavyB\nTestLost\nExampleOptional\nBenchmarkDrop\nok  \t%s\t0.001s\n' "${*: -1}"
      exit 0
    fi
    pkg="${*: -1}"
    if [[ "$args" == *" -run "* ]]; then
      for name in TestHeavyA TestHeavyB ${STUB_UNSHARDED_KILLED:+TestLost}; do
        [[ "$args" == *"$name"* ]] || continue
        printf '{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"%s","Test":"%s"}\n' "$pkg" "$name"
        printf '{"Time":"2024-01-01T00:00:01Z","Action":"pass","Package":"%s","Test":"%s","Elapsed":1}\n' "$pkg" "$name"
      done
    else
      printf '{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"%s","Test":"TestQuick"}\n' "$pkg"
      printf '{"Time":"2024-01-01T00:00:01Z","Action":"pass","Package":"%s","Test":"TestQuick","Elapsed":1}\n' "$pkg"
    fi
    printf '{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"%s","Elapsed":2}\n' "$pkg"
    # Killed between packages: the packages it never reached leave no event.
    [ -z "${STUB_UNSHARDED_KILLED:-}" ] || [[ "$args" == *" -run "* ]] || exit 137 ;;
  tool)
    [ "$2" = test2json ] || exit 7
    printf 'child-env %s\n' "${PFM_TEST_PFM_BINARY-<unset>}" >>"$STUB_LOG"
    printf 'mock-env %s\n' "${PFM_TEST_MOCK_ENGINE_BINARY-<unset>}" >>"$STUB_LOG"
    shift 5
    "$@" ;;
  *) exit 7 ;;
esac
SH
chmod +x "$T/bin/go"
export PATH="$T/bin:$PATH" STUB_LOG="$T/go.log" STUB_PACKAGE="$PKG"
: >"$STUB_LOG"

cat >"$T/history.json" <<JSON
{"Action":"pass","Package":"$PKG","Test":"TestHeavyA","Elapsed":18}
{"Action":"pass","Package":"$PKG","Test":"TestHeavyB","Elapsed":17}
{"Action":"pass","Package":"$PKG","Test":"TestObsolete","Elapsed":0.1}
JSON
cat >"$T/thrashed.json" <<JSON
{"Action":"pass","Package":"$PKG","Test":"TestHeavyA","Elapsed":180}
{"Action":"pass","Package":"$PKG","Test":"TestHeavyB","Elapsed":170}
{"Action":"pass","Package":"$QUICK","Test":"TestQuick","Elapsed":1000}
JSON
rc=0; bash "$SUT" plan --out "$T/plan.json" >"$T/plain.tsv" 2>"$T/plan.err" || rc=$?
plain_rc=$rc
rc=0; bash "$SUT" plan --out "$T/plan.json" --history "$T/thrashed.json" >"$T/thrashed.tsv" 2>"$T/plan.err" || rc=$?
if [ "$plain_rc" -eq 0 ] && [ "$rc" -eq 0 ] && cmp -s <(cut -f1,2 "$T/plain.tsv") <(cut -f1,2 "$T/thrashed.tsv"); then ok thrashed-history-same-shards; else bad "thrashed-history-same-shards: rc=$rc" "$(cat "$T/plan.err")"; fi
: >"$STUB_LOG"
rc=0; mkdir "$T/rendezvous"; STUB_BUILD_RENDEZVOUS="$T/rendezvous" bash "$SUT" run --out "$T/run.json" -- -p 4 >"$T/run.log" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && python3 - "$STUB_LOG" <<'PY'
import os, sys
lines = open(sys.argv[1]).read().splitlines()
builds = {line.split()[-1]: line.split() for line in lines if line.startswith('build ')}
assert sorted(builds) == ['./cmd/mock-engine', './cmd/pfm'], lines
for parts in builds.values():
    assert parts[:3] == ['build', '-trimpath', '-buildvcs=false'], lines
binary = builds['./cmd/pfm'][builds['./cmd/pfm'].index('-o') + 1]
mock_binary = builds['./cmd/mock-engine'][builds['./cmd/mock-engine'].index('-o') + 1]
# The two builds ran at once: each met the other at the rendezvous, neither waited alone.
assert sorted(line for line in lines if line.startswith('build-overlap ')) == ['build-overlap ./cmd/mock-engine', 'build-overlap ./cmd/pfm'], lines
assert not [line for line in lines if line.startswith('build-alone ')], lines
# Both finished before any test process started.
done = [i for i, line in enumerate(lines) if line.startswith('build-done ')]
assert len(done) == 2, lines
first_test = min(i for i, line in enumerate(lines) if line.startswith(('test ', 'tool test2json ', 'binary ')))
assert max(done) < first_test, lines
assert [line for line in lines if line.startswith('child-env ')], lines
assert all(line == 'child-env ' + binary for line in lines if line.startswith('child-env ')), lines
assert all(line == 'mock-env ' + mock_binary for line in lines if line.startswith('mock-env ')), lines
# No --bin-dir: the prebuilt binaries lived in a temp dir the run removed.
assert not os.path.exists(binary) and not os.path.exists(mock_binary), (binary, mock_binary)
PY
then ok prebuilt-before-tests; else bad "prebuilt-before-tests: rc=$rc" "$(cat "$STUB_LOG")"; fi
: >"$STUB_LOG"
rc=0; STUB_PREBUILD_FAIL=1 bash "$SUT" run --out "$T/run.json" -- -p 4 >"$T/run.log" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && grep -q 'test-shard: prebuilt pfm build failed: prebuild.go:7: broken fixture' "$T/run.log" && grep -q '"Action": "build-fail", "ImportPath": "./cmd/pfm"' "$T/run.json" && python3 - "$STUB_LOG" <<'PY'
import sys
children = [line for line in open(sys.argv[1]).read().splitlines() if line.startswith('child-env ')]
assert children and all(line == 'child-env <unset>' for line in children), children
PY
then ok prebuild-failure-kept; else bad "prebuild-failure-kept: rc=$rc" "$(cat "$T/run.log")"; fi
: >"$STUB_LOG"
rc=0; STUB_MOCK_PREBUILD_FAIL=1 bash "$SUT" run --out "$T/run.json" -- -p 4 >"$T/run.log" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && grep -q 'test-shard: prebuilt mock-engine build failed: mock-build.go:7: broken fixture' "$T/run.log" && grep -q '"Action": "build-output", "ImportPath": "./cmd/mock-engine"' "$T/run.json" && grep -q '"Action": "build-fail", "ImportPath": "./cmd/mock-engine"' "$T/run.json" && python3 - "$STUB_LOG" <<'PY'
import sys
lines = open(sys.argv[1]).read().splitlines()
assert any(line.startswith('child-env ') and line != 'child-env <unset>' for line in lines), lines
assert [line for line in lines if line.startswith('mock-env ')] and all(line == 'mock-env <unset>' for line in lines if line.startswith('mock-env ')), lines
PY
then ok mock-prebuild-failure-kept; else bad "mock-prebuild-failure-kept: rc=$rc" "$(cat "$T/run.log")"; fi

# --bin-dir: the prebuilt pfm and mock-engine are written there and left there.
: >"$STUB_LOG"
rc=0; bash "$SUT" run --out "$T/kept.json" --bin-dir "$T/kept-bin/nested" -- -p 4 >"$T/run.log" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && [ -x "$T/kept-bin/nested/pfm" ] && [ -x "$T/kept-bin/nested/mock-engine" ] &&
  grep -Fxq "child-env $T/kept-bin/nested/pfm" "$STUB_LOG" && grep -Fxq "mock-env $T/kept-bin/nested/mock-engine" "$STUB_LOG" &&
  ! grep -q '^child-env <unset>$' "$STUB_LOG" && ! grep -q '^mock-env <unset>$' "$STUB_LOG"; then
  ok bin-dir-kept
else bad "bin-dir-kept: rc=$rc" "$(cat "$T/run.log")" "$(cat "$STUB_LOG")"; fi
# A relative --bin-dir means the caller's directory: the builds and the tests run in other ones.
: >"$STUB_LOG"
mkdir "$T/rel"
rc=0; (cd "$T/rel" && bash "$SUT" run --out "$T/rel.json" --bin-dir rel-bin -- -p 4) >"$T/run.log" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && [ -x "$T/rel/rel-bin/pfm" ] && grep -Fxq "child-env $T/rel/rel-bin/pfm" "$STUB_LOG"; then
  ok bin-dir-relative
else bad "bin-dir-relative: rc=$rc" "$(cat "$T/run.log")" "$(cat "$STUB_LOG")"; fi
# A binary an earlier run left there is not this run's when its build fails.
mkdir "$T/stale-bin"
printf '#!/bin/sh\nexit 0\n' >"$T/stale-bin/pfm"; chmod +x "$T/stale-bin/pfm"
: >"$STUB_LOG"
rc=0; STUB_PREBUILD_FAIL=1 bash "$SUT" run --out "$T/stale.json" --bin-dir "$T/stale-bin" -- -p 4 >"$T/run.log" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && [ ! -e "$T/stale-bin/pfm" ] && [ -x "$T/stale-bin/mock-engine" ] && grep -q 'test-shard: prebuilt pfm build failed' "$T/run.log"; then
  ok bin-dir-failed-build-leaves-none
else bad "bin-dir-failed-build-leaves-none: rc=$rc" "$(cat "$T/run.log")"; fi
# A --bin-dir that cannot be made is exit 2 before any go call.
: >"$T/a-file"
for dir in "$T/a-file" "$T/a-file/sub"; do
  : >"$STUB_LOG"
  rc=0; bash "$SUT" run --out "$T/run.json" --bin-dir "$dir" -- -p 4 >"$T/error.log" 2>&1 || rc=$?
  if [ "$rc" -eq 2 ] && [ ! -s "$STUB_LOG" ] && grep -q "^test-shard: --bin-dir $dir: " "$T/error.log"; then ok "bin-dir-uncreatable-before-go ($dir)"; else bad "bin-dir-uncreatable-before-go ($dir): rc=$rc" "$(cat "$T/error.log")" "$(cat "$STUB_LOG")"; fi
done
: >"$STUB_LOG"
rc=0; bash "$SUT" plan --out "$T/plan.json" --bin-dir "$T/plan-bin" >"$T/error.log" 2>&1 || rc=$?
if [ "$rc" -eq 2 ] && [ ! -s "$STUB_LOG" ] && [ ! -e "$T/plan-bin" ] && grep -q 'unrecognized arguments' "$T/error.log"; then ok bin-dir-run-only; else bad "bin-dir-run-only: rc=$rc" "$(cat "$T/error.log")"; fi

# The unsharded packages start longest first by the package terminal events of --history; a package the history
# does not hold comes first, in go list order, and ties keep go list order.
P=github.com/rezzminator/professor/pfm/internal
export STUB_LIST_PACKAGES="$PKG $P/mid $P/quick $P/tiea $P/fresh $P/slow $P/tieb"
cat >"$T/order-history.json" <<JSON
{"Action":"pass","Package":"$PKG","Test":"TestHeavyA","Elapsed":18}
{"Action":"pass","Package":"$PKG","Test":"TestHeavyB","Elapsed":17}
{"Action":"pass","Package":"$P/slow","Elapsed":30}
{"Action":"pass","Package":"$P/mid","Test":"TestInside","Elapsed":99}
{"Action":"pass","Package":"$P/mid","Elapsed":12}
{"Action":"fail","Package":"$P/tiea","Elapsed":5}
{"Action":"skip","Package":"$P/tieb","Elapsed":5}
JSON
unsharded_args() { grep '^test -timeout 25m ' "$STUB_LOG" | sed 's/.* -json //; s#github.com/rezzminator/professor/pfm/internal/##g'; }
: >"$STUB_LOG"
rc=0; bash "$SUT" run --out "$T/order.json" --history "$T/order-history.json" -- -p 4 >"$T/run.log" 2>&1 || rc=$?
if [ "$(unsharded_args)" = 'quick fresh slow mid tiea tieb' ]; then ok longest-first-order; else bad "longest-first-order: rc=$rc" "$(cat "$STUB_LOG")" "$(cat "$T/run.log")"; fi
printf '{"Action":"output","Package":"%s","Output":"hello"}\n' "$P/slow" >"$T/order-eventless.json"
for kind in absent unreadable eventless; do
  history_arg=(--history "$T/order-$kind.json")
  [ "$kind" != absent ] || history_arg=()
  [ "$kind" != unreadable ] || mkdir -p "$T/order-unreadable.json"
  : >"$STUB_LOG"
  rc=0; bash "$SUT" run --out "$T/order.json" "${history_arg[@]}" -- -p 4 >"$T/run.log" 2>&1 || rc=$?
  if [ "$(unsharded_args)" = 'mid quick tiea fresh slow tieb' ]; then ok "no-history-$kind-go-list-order"; else bad "no-history-$kind-go-list-order: rc=$rc" "$(cat "$STUB_LOG")" "$(cat "$T/run.log")"; fi
done
unset STUB_LIST_PACKAGES

cat >"$T/proc.stat" <<'STAT'
cpu  100 0 100 100 0 0 0 0 0 0
cpu0 50 0 50 50 0 0 0 0 0 0
cpu1 50 0 50 50 0 0 0 0 0 0
STAT
printf 'usage_usec 1000000\n' >"$T/cpu.stat"
# The wall clock steps back on every read, as a VM clock correction does: the record's time still rises.
mkdir -p "$T/clock"
cat >"$T/clock/sitecustomize.py" <<'PY'
import time
_base = time.time()
_reads = [0]
def _stepping_back():
    _reads[0] += 1
    return _base - _reads[0] * 0.001
time.time = _stepping_back
PY
rc=0; PYTHONPATH="$T/clock" PFM_TEST_SHARD_PROC_STAT="$T/proc.stat" PFM_TEST_SHARD_CPU_STAT="$T/cpu.stat" bash "$SUT" run --out "$T/load.json" -- -p 4 >"$T/load.log" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && python3 - "$T/load.load" <<'PY'
import sys
rows = open(sys.argv[1]).read().splitlines()
assert rows[0] == 'epoch_s\tvm_busy_s\town_s\tcpus', rows
assert len(rows) >= 3, rows
assert all(len(row.split('\t')) == 4 and row.split('\t')[3] == '2' for row in rows[1:]), rows
epochs = [float(row.split('\t')[0]) for row in rows[1:]]
assert all(later > earlier for earlier, later in zip(epochs, epochs[1:])), epochs
PY
then ok load-record-fixture; else bad "load-record-fixture: rc=$rc" "$(cat "$T/load.log")"; fi
rc=0; PFM_TEST_SHARD_PROC_STAT="$T/missing.stat" bash "$SUT" run --out "$T/unavailable.json" -- -p 4 >"$T/unavailable.log" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && grep -q "^UNAVAILABLE.*$T/missing.stat" "$T/unavailable.load"; then ok load-unavailable; else bad "load-unavailable: rc=$rc" "$(cat "$T/unavailable.log")"; fi
rc=0; bash "$SUT" run --out "$T/real.json" -- -p 4 >"$T/real.log" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && python3 - "$T/real.load" <<'PY'
import re, sys
rows = open(sys.argv[1]).read().splitlines()
if rows[-1].startswith('UNAVAILABLE\t'):
    assert '/proc/stat' in rows[-1] or '/sys/fs/cgroup/cpu.stat' in rows[-1], rows
else:
    assert len(rows) >= 3, rows
    with open('/proc/stat') as source:
        cpus = sum(bool(re.match(r'cpu[0-9]+ ', line)) for line in source)
    assert all(int(row.split('\t')[3]) == cpus for row in rows[1:]), rows
PY
then ok load-record-real; else bad "load-record-real: rc=$rc" "$(cat "$T/real.log")"; fi
ln -s /dev/full "$T/write-fail.load"
rc=0; bash "$SUT" run --out "$T/write-fail.json" -- -p 4 >"$T/write-fail.log" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && grep -q "test-shard: load record $T/write-fail.load:" "$T/write-fail.log" && python3 - "$T/write-fail.json" "$QUICK" <<'PY'
import json, sys
assert any(json.loads(line).get('Package') == sys.argv[2] for line in open(sys.argv[1]))
PY
then ok load-write-failure-continues; else bad "load-write-failure-continues: rc=$rc" "$(cat "$T/write-fail.log")"; fi

# sample: the load record alone, for a run this script does not drive (the e2e suite), until TERM or INT.
sample_start() { # sample_start <load file> [proc.stat]: the sampler in the background, its pid in SAMPLER_PID
  PFM_TEST_SHARD_PROC_STAT="${2:-$T/proc.stat}" PFM_TEST_SHARD_CPU_STAT="$T/cpu.stat" bash "$SUT" sample --out "$1" >"$T/sample.log" 2>&1 &
  SAMPLER_PID=$!
}
wait_lines() { # wait_lines <file> <n>: until <file> holds n lines, at most 15 s in 0.1 s ticks
  local tick=0
  while [ "$(wc -l <"$1" 2>/dev/null || echo 0)" -lt "$2" ] && [ "$tick" -lt 150 ]; do sleep 0.1; tick=$((tick + 1)); done
}
: >"$STUB_LOG"
sample_start "$T/e2e.load"
wait_lines "$T/e2e.load" 5
kill -TERM "$SAMPLER_PID"; rc=0; wait "$SAMPLER_PID" || rc=$?
if [ "$rc" -eq 0 ] && [ ! -s "$STUB_LOG" ] && python3 - "$T/e2e.load" <<'PY'
import sys
rows = open(sys.argv[1]).read().splitlines()
assert rows[0] == 'epoch_s\tvm_busy_s\town_s\tcpus', rows
assert len(rows) >= 6, rows
assert all(len(row.split('\t')) == 4 and row.split('\t')[3] == '2' for row in rows[1:]), rows
epochs = [float(row.split('\t')[0]) for row in rows[1:]]
gaps = [later - earlier for earlier, later in zip(epochs, epochs[1:])]
assert all(gap > 0 for gap in gaps), epochs
assert all(gap >= 0.45 for gap in gaps[:-1]), gaps
PY
then ok sample-record-every-half-second; else bad "sample-record-every-half-second: rc=$rc" "$(cat "$T/sample.log")" "$(cat "$T/e2e.load")" "$(cat "$STUB_LOG")"; fi
for signal in TERM INT; do
  sample_start "$T/stop-$signal.load"
  wait_lines "$T/stop-$signal.load" 2
  kill -"$signal" "$SAMPLER_PID"; rc=0; wait "$SAMPLER_PID" || rc=$?
  # TERM lands well inside the first half second: a second row can only be the final one.
  if [ "$rc" -eq 0 ] && [ "$(wc -l <"$T/stop-$signal.load")" -ge 3 ]; then ok "sample-final-row-on-$signal"; else bad "sample-final-row-on-$signal: rc=$rc" "$(cat "$T/sample.log")" "$(cat "$T/stop-$signal.load")"; fi
done
sample_start "$T/unavailable-sample.load" "$T/missing.stat"
wait_lines "$T/unavailable-sample.load" 2
kill -TERM "$SAMPLER_PID"; rc=0; wait "$SAMPLER_PID" || rc=$?
if [ "$rc" -eq 0 ] && [ "$(wc -l <"$T/unavailable-sample.load")" -eq 2 ] && grep -q "^UNAVAILABLE.*$T/missing.stat" "$T/unavailable-sample.load"; then ok sample-unavailable-row; else bad "sample-unavailable-row: rc=$rc" "$(cat "$T/sample.log")" "$(cat "$T/unavailable-sample.load")"; fi
: >"$STUB_LOG"
rc=0; bash "$SUT" sample --out "$T/not-here/e2e.load" >"$T/error.log" 2>&1 || rc=$?
if [ "$rc" -eq 2 ] && [ ! -e "$T/not-here/e2e.load" ] && [ ! -s "$STUB_LOG" ]; then ok sample-out-unwritable-before-sampling; else bad "sample-out-unwritable-before-sampling: rc=$rc" "$(cat "$T/error.log")"; fi
ln -s /dev/full "$T/sample-full.load"
rc=0; bash "$SUT" sample --out "$T/sample-full.load" >"$T/error.log" 2>&1 || rc=$?
if [ "$rc" -eq 2 ] && grep -q "test-shard: load record $T/sample-full.load:" "$T/error.log"; then ok sample-write-failure-exits-2; else bad "sample-write-failure-exits-2: rc=$rc" "$(cat "$T/error.log")"; fi
rc=0; bash "$SUT" plan --out "$T/plan.json" --history "$T/history.json" >"$T/plan.tsv" 2>"$T/plan.err" || rc=$?
if [ "$rc" -eq 0 ] && python3 - "$T/plan.tsv" "$PKG" <<'PY'
import sys
rows = [line.rstrip('\n').split('\t') for line in open(sys.argv[1]) if line.startswith(sys.argv[2]+'\t')]
assert len(rows) == 4, rows
assert {name for r in rows for name in r[2].split(',') if name} == {'TestHeavyA','TestHeavyB','TestLost','ExampleOptional'}, rows
assert [r[1] for r in rows if 'TestHeavyA' in r[2]] != [r[1] for r in rows if 'TestHeavyB' in r[2]], rows
assert [float(r[3]) for r in rows if 'TestLost' in r[2]] == [17.0], rows
PY
then ok lpt-plan; else bad "lpt-plan: rc=$rc" "$(cat "$T/plan.err")"; fi

printf '{"Action":"output","Package":"%s","Output":"hello"}\n' "$PKG" >"$T/eventless.json"
mkdir "$T/unreadable.json"
for kind in absent missing unreadable eventless; do
  history_arg=(--history "$T/$kind.json")
  if [ "$kind" = absent ]; then history_arg=(); fi
  rc=0; bash "$SUT" plan --out "$T/plan.json" "${history_arg[@]}" >"$T/default.tsv" 2>"$T/plan.err" || rc=$?
  if [ "$rc" -eq 0 ] && [ "$(awk -F '\t' -v p="$PKG" '$1==p {n++} END {print n+0}' "$T/default.tsv")" -eq 4 ]; then ok "no-history-$kind-default"; else bad "no-history-$kind-default: rc=$rc" "$(cat "$T/plan.err")"; fi
done

rc=0; bash "$SUT" run --out "$T/run.json" --history "$T/history.json" -- -p 4 >"$T/run.log" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && grep -q 'SHARD-LOST TestLost' "$T/run.json" && grep -q '"Action":"fail","Package":"'"$PKG"'"' "$T/run.json" && [ -d "$T/unit-shards" ]; then ok lost-test-and-raw-streams; else bad "lost-test-and-raw-streams: rc=$rc" "$(cat "$T/run.log")"; fi
if cmp -s <(head -n 3 "$T/unit-shards/1.json") <(head -n 3 "$T/run.json"); then ok run-unsharded-identical; else bad run-unsharded-identical; fi
if [ -s "$T/run.json" ] && grep -q 'ExampleOptional' "$STUB_LOG" && ! grep -q 'BenchmarkDrop' "$STUB_LOG" && ! grep -q 'SHARD-LOST ExampleOptional' "$T/run.json"; then ok example-optional-benchmark-dropped; else bad example-optional-benchmark-dropped; fi

rc=0; STUB_UNSHARDED_KILLED=1 bash "$SUT" run --out "$T/run.json" --history "$T/history.json" -- -p 4 >"$T/run.log" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && ! grep -q 'SHARD-LOST' "$T/run.json" && grep -q 'exited 137' "$T/run.log"; then ok killed-process-fails; else bad "killed-process-fails: rc=$rc" "$(cat "$T/run.log")"; fi

for failure in STUB_LIST_FAIL STUB_TEST_LIST_FAIL; do
  : >"$STUB_LOG"
  rc=0; env "$failure=1" bash "$SUT" run --out "$T/run.json" --history "$T/history.json" -- -p 4 >"$T/error.log" 2>&1 || rc=$?
  if [ "$rc" -eq 2 ] && grep -q 'package' "$T/error.log" && ! grep -q '^tool test2json ' "$STUB_LOG"; then ok "$failure"; else bad "$failure: rc=$rc" "$(cat "$T/error.log")"; fi
done
: >"$STUB_LOG"
rc=0; STUB_COMPILE_FAIL=1 bash "$SUT" run --out "$T/run.json" -- -p 4 >"$T/error.log" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && grep -q 'compile.go:7: broken fixture' "$T/error.log" && python3 - "$T/run.json" "$PKG" <<'PY'
import json, sys
events = [json.loads(line) for line in open(sys.argv[1]) if line.strip()]
package = [e for e in events if e.get('Package') == sys.argv[2]]
assert [e['Action'] for e in package] == ['output', 'fail'], package
assert 'compile.go:7' in package[0]['Output'], package
PY
then ok sharded-compile-failure; else bad "sharded-compile-failure: rc=$rc" "$(cat "$T/error.log")"; fi
: >"$STUB_LOG"
rc=0; bash "$SUT" run --out "$T/run.json" -- -p 4 -parallel 4 -short -failfast -cpu=2 -race -tags=demo -count=2 -timeout=3m >"$T/run.log" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && grep -q '^test -c .* -race -tags demo ' "$STUB_LOG" && grep -q -- '-test.parallel=4' "$STUB_LOG" && grep -q -- '-test.short' "$STUB_LOG" && grep -q -- '-test.failfast' "$STUB_LOG" && grep -q -- '-test.cpu=2' "$STUB_LOG" && ! grep '^tool test2json ' "$STUB_LOG" | grep -q -- '-test.count=2' && ! grep '^tool test2json ' "$STUB_LOG" | grep -v -q -- '-test.timeout=3m$' && grep '^test .* -json ' "$STUB_LOG" | grep -q -- '-timeout=3m -count=1 -json '; then ok accepted-flags-split; else bad "accepted-flags-split: rc=$rc" "$(cat "$STUB_LOG")"; fi
: >"$STUB_LOG"
rc=0; bash "$SUT" run --out "$T/run.json" -- -race=false >"$T/run.log" 2>&1 || rc=$?
if [ "$rc" -eq 1 ] && grep -q '^test -c .* -race=false ' "$STUB_LOG"; then ok boolean-build-flag; else bad "boolean-build-flag: rc=$rc" "$(cat "$STUB_LOG")"; fi
for flag in -cover --threshold-s --max-shards; do
  : >"$STUB_LOG"
  rc=0; bash "$SUT" run --out "$T/run.json" -- "$flag" >"$T/error.log" 2>&1 || rc=$?
  if [ "$rc" -eq 2 ] && grep -q -- "$flag" "$T/error.log" && [ ! -s "$STUB_LOG" ]; then ok "unsupported-$flag"; else bad "unsupported-$flag: rc=$rc" "$(cat "$T/error.log")"; fi
done
: >"$STUB_LOG"
rc=0; bash "$SUT" run --out "$T/not-here/run.json" --history "$T/history.json" >"$T/error.log" 2>&1 || rc=$?
if [ "$rc" -eq 2 ] && [ ! -s "$STUB_LOG" ]; then ok out-unwritable-before-go; else bad "out-unwritable-before-go: rc=$rc" "$(cat "$T/error.log")"; fi
: >"$STUB_LOG"
rc=0; bash "$SUT" run --out /sys/test-shard-unwritable.json --history "$T/history.json" >"$T/error.log" 2>&1 || rc=$?
if [ "$rc" -eq 2 ] && [ ! -s "$STUB_LOG" ]; then ok out-read-only-before-go; else bad "out-read-only-before-go: rc=$rc" "$(cat "$T/error.log")"; fi

shtest_end

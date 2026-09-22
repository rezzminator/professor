#!/usr/bin/env bash
# Fixture-driven tests for scripts/skip-check.sh: a listed skip passes and is
# named, an unlisted one is red and named, a subtest rides its parent's row,
# and every way the gate can fail to look is ERROR rc 2, never PASS.
#   bash pfm/scripts/skip-check_test.sh
set -uo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${SUT:-$ROOT/scripts/skip-check.sh}"
SHTEST_TAG=pfm-skip-check-test
# shellcheck source=/dev/null
source "$ROOT/../scripts/shtest.sh"

printf 'p/a\tTestListed\thelper\tfixture\n' > "$T/list.tsv"
ev() { printf '{"Action":"%s","Package":"%s","Test":"%s"}\n' "$1" "$2" "$3"; }
check() { # check <name> <want-rc> <want-grep> <json>
  # VERBOSE on: the per-skip GAP line is the audit trail these cases assert.
  local out rc; out=$(PFM_KNOWN_SKIPS="$T/list.tsv" PFM_SKIP_CHECK_VERBOSE=1 bash "$SUT" "$4" 2>&1); rc=$?
  if [ "$rc" -eq "$2" ] && grep -q -- "$3" <<< "$out"; then ok "$1"; else bad "$1" "rc=$rc want $2" "$out"; fi
}

{ ev pass p/a TestRan; ev skip p/a TestListed; } > "$T/listed.json"
check "listed skip passes, named" 0 'GAP skipped test: p/a TestListed \[helper\]' "$T/listed.json"
check "listed skip verdict" 0 'SKIPS PASS 1 skipped' "$T/listed.json"

{ ev pass p/a TestRan; ev skip p/a TestNew; } > "$T/unlisted.json"
check "unlisted skip is red, named" 1 'SKIP-UNLISTED p/a TestNew' "$T/unlisted.json"

{ ev pass p/a TestRan; ev skip p/b TestListed; } > "$T/otherpkg.json"
check "same test name in another package is unlisted" 1 'SKIP-UNLISTED p/b TestListed' "$T/otherpkg.json"

{ ev pass p/a TestRan; ev skip p/a TestListed/sub; } > "$T/sub.json"
check "subtest rides its parent row" 0 'TestListed/sub \[helper\]' "$T/sub.json"

{ ev pass p/a TestRan; } > "$T/none.json"
check "no skips passes with zero" 0 'SKIPS PASS 0 skipped' "$T/none.json"

printf '{"Action":"output","Package":"p/a"}\n' > "$T/empty.json"
check "a run with no test event is ERROR" 2 'SKIPS ERROR no test event' "$T/empty.json"
printf 'not json\n' > "$T/garbage.json"
check "unparseable input is ERROR" 2 'SKIPS ERROR' "$T/garbage.json"
check "missing input is ERROR" 2 'SKIPS ERROR input' "$T/absent.json"
out=$(PFM_KNOWN_SKIPS="$T/no-list.tsv" bash "$SUT" "$T/listed.json" 2>&1); rc=$?
if [ "$rc" -eq 2 ] && grep -q 'allow-list' <<< "$out"; then ok "missing allow-list is ERROR"; else bad "missing allow-list is ERROR" "rc=$rc" "$out"; fi

shtest_end

#!/usr/bin/env bash
# Fixture-driven tests for scripts/testplan-check.sh: a plan citing only tests and
# files that exist passes, a deleted test or a renamed file is red and named, a
# `Test…*` prefix matches any test with it, and every way the gate can fail to
# look is ERROR rc 2, never PASS.
#   bash pfm/scripts/testplan-check_test.sh
set -uo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${SUT:-$ROOT/scripts/testplan-check.sh}"
SHTEST_TAG=pfm-testplan-check-test
# shellcheck source=/dev/null
source "$ROOT/../scripts/shtest.sh"

mkdir -p "$T/pfm/internal/pkg" "$T/pfm/cmd/pfm"
printf 'package pkg\n\nfunc Thing() {}\n' > "$T/pfm/internal/pkg/thing.go"
printf 'package pkg\n\nfunc TestThingWorks() {}\nfunc TestThingFails() {}\n' > "$T/pfm/internal/pkg/thing_test.go"
printf 'package main\n' > "$T/pfm/cmd/pfm/main.go"
printf '#!/bin/sh\n' > "$T/root-script.sh"

check() { # check <name> <want-rc> <want-grep> <plan-file>
  local out rc; out=$(PFM_PLAN_ROOT="$T/pfm" bash "$SUT" "$4" 2>&1); rc=$?
  if [ "$rc" -eq "$2" ] && grep -q -- "$3" <<< "$out"; then ok "$1"; else bad "$1" "rc=$rc want $2" "$out"; fi
}

cat > "$T/good.md" <<'EOF'
| flow | safety | source | regression |
| a flow | JAIL | `internal/pkg/thing.go:3-9`, `cmd/pfm/main.go` | `internal/pkg/thing_test.go: TestThingWorks` |
| a prefix | JAIL | `thing.go` | `TestThing*` |
| a runtime file is no source | JAIL | `.credentials.json` | |
EOF
check "a plan citing only existing tests and files passes" 0 'PLAN PASS' "$T/good.md"

cat > "$T/test-gone.md" <<'EOF'
| a flow | JAIL | `internal/pkg/thing.go` | `internal/pkg/thing_test.go: TestThingWorks` |
| a deleted test | JAIL | `internal/pkg/thing.go` | `internal/pkg/thing_test.go: TestThingDeleted` |
EOF
check "a cited test that no longer exists is red, named with its line" 1 'PLAN-STALE test TestThingDeleted (line 2)' "$T/test-gone.md"

cat > "$T/file-gone.md" <<'EOF'
| a renamed file | JAIL | `internal/pkg/old_name.go:10-20`, `thing.go` | |
EOF
check "a cited file that no longer exists is red, named with its line" 1 'PLAN-STALE file internal/pkg/old_name.go (line 1)' "$T/file-gone.md"

printf '| a prefix with no test | JAIL | `TestNothingLike*` |\n' > "$T/prefix-gone.md"
check "a Test prefix matching no test is red" 1 'PLAN-STALE test TestNothingLike\*' "$T/prefix-gone.md"

printf '| a repo-root script | JAIL | `root-script.sh`, `cmd/pfm/main.go` |\n' > "$T/root.md"
check "a bare name found only under the repo root passes" 0 'PLAN PASS' "$T/root.md" 

printf 'prose with nothing cited\n' > "$T/nothing.md"
check "a plan citing nothing is ERROR" 2 'PLAN ERROR no test or file cited' "$T/nothing.md"
check "an unreadable plan is ERROR" 2 'PLAN ERROR plan' "$T/absent.md"
out=$(PFM_PLAN_ROOT="$T/empty" bash "$SUT" "$T/good.md" 2>&1); rc=$?
if [ "$rc" -eq 2 ] && grep -q 'PLAN ERROR' <<< "$out"; then ok "a pfm root without a tree is ERROR"; else bad "a pfm root without a tree is ERROR" "rc=$rc" "$out"; fi
mkdir -p "$T/notests"; printf 'package x\n' > "$T/notests/x.go"
out=$(PFM_PLAN_ROOT="$T/notests" bash "$SUT" "$T/good.md" 2>&1); rc=$?
if [ "$rc" -eq 2 ] && grep -q 'no test func' <<< "$out"; then ok "a tree with no test func is ERROR, never an empty pass"; else bad "a tree with no test func is ERROR" "rc=$rc" "$out"; fi

shtest_end

#!/usr/bin/env bash
# Fixture-driven tests for scripts/test-kinds.sh: a test is real when its file is a jail file and unit
# otherwise, whatever its body touches; the suites outside the unit bundle and commented functions are
# not counted, and every way the script can fail to look is KINDS ERROR rc 2, never a zero count.
#   bash pfm/scripts/test-kinds_test.sh
set -uo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${SUT:-$ROOT/scripts/test-kinds.sh}"
SHTEST_TAG=pfm-test-kinds-test
# shellcheck source=/dev/null
source "$ROOT/../scripts/shtest.sh"
eq() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected: $2" "actual:   $3"; fi; }

M=$T/mod
mkdir -p "$M/a" "$M/b" "$M/journey" "$M/e2e" "$M/a/testdata" "$M/.hidden" "$M/tmp"
printf 'module p\n' > "$M/go.mod"
cat > "$M/a/a_test.go" <<'GO'
package a

import (
	"os/exec"
	"testing"
)

func TestMain(m *testing.M) { m.Run() }

// func TestCommentedOut(t *testing.T) {}

func TestMemory(t *testing.T) {}

func TestStartsAProcess(t *testing.T) {
	_ = exec.Command("tmux", "new-session").Run()
}

func FuzzParse(f *testing.F) {}

func helperIsNoTest(t *testing.T) {}
GO
cat > "$M/a/server_jail_test.go" <<'GO'
//go:build jail

package a

import "testing"

func TestInAJailFileOfA(t *testing.T) {}
GO
cat > "$M/b/b_jail_test.go" <<'GO'
//go:build jail

package b

import "testing"

func TestInAJailFile(t *testing.T) {}
GO
cat > "$M/b/seat_jail_linux_test.go" <<'GO'
//go:build jail

package b

import "testing"

func TestInAnOSSelectedJailFile(t *testing.T) {}
GO
cat > "$M/b/killjail_test.go" <<'GO'
package b

import "testing"

func TestJailIsNotItsOwnWord(t *testing.T) {}
GO
cat > "$M/b/new_jail_stubs_test.go" <<'GO'
package b

import "testing"

func TestJailStubsIsNoOS(t *testing.T) {}
GO
for skip in journey e2e a/testdata .hidden tmp; do
  printf 'package x\n\nimport "testing"\n\nfunc TestNotCounted(t *testing.T) {}\n' > "$M/$skip/x_test.go"
done

out=$(bash "$SUT" "$M" 2>&1); rc=$?
eq "the count line: real = the jail files' tests, unit = every other Test and Fuzz, each once" \
  "0|KINDS unit 5 · real 3 · total 8" "$rc|$out"

out=$(bash "$SUT" --list "$M" 2>&1); rc=$?
want=$(printf '%s\n' \
  $'unit\ta\ta_test.go\tTestMemory' \
  $'unit\ta\ta_test.go\tTestStartsAProcess' \
  $'unit\ta\ta_test.go\tFuzzParse' \
  $'real\ta\tserver_jail_test.go\tTestInAJailFileOfA' \
  $'real\tb\tb_jail_test.go\tTestInAJailFile' \
  $'unit\tb\tkilljail_test.go\tTestJailIsNotItsOwnWord' \
  $'unit\tb\tnew_jail_stubs_test.go\tTestJailStubsIsNoOS' \
  $'real\tb\tseat_jail_linux_test.go\tTestInAnOSSelectedJailFile')
eq "--list: one row per test, real only in a jail file (an OS-selected one included), a body that starts a process still unit, TestMain, helpers and commented functions not tests" "0|$want" "$rc|$out"

mkdir -p "$T/notests"; printf 'module p\n' > "$T/notests/go.mod"
out=$(bash "$SUT" "$T/notests" 2>&1); rc=$?
eq "a tree with no test function is ERROR, never zero" "2|KINDS ERROR no test function found under $T/notests" "$rc|$out"
mkdir -p "$T/nomod"
out=$(bash "$SUT" "$T/nomod" 2>&1); rc=$?
eq "a dir without go.mod is ERROR" "2|KINDS ERROR $T/nomod has no go.mod — not a Go module root" "$rc|$out"
out=$(bash "$SUT" "$T/absent" 2>&1); rc=$?
eq "a missing dir is ERROR" "2|KINDS ERROR pfm dir $T/absent is not a readable directory" "$rc|$out"
printf '\xff\xfe' > "$M/b/broken_test.go"
out=$(bash "$SUT" "$M" 2>&1); rc=$?
eq "an unreadable test file is ERROR naming it" "2|KINDS ERROR cannot read $M/b/broken_test.go" "$rc|${out%%:*}"

shtest_end

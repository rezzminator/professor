#!/usr/bin/env bash
# Fixture-driven tests for scripts/test-kinds.sh: each kind is read from its marker, a helper's
# marker reaches every test naming it (transitively), a `_jail_test.go` file is tmux whole,
# commented markers and the suites outside the unit bundle are not counted, and every way the
# script can fail to look is KINDS ERROR rc 2, never a zero count.
#   bash pfm/scripts/test-kinds_test.sh
set -uo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${SUT:-$ROOT/scripts/test-kinds.sh}"
SHTEST_TAG=pfm-test-kinds-test
# shellcheck source=/dev/null
source "$ROOT/../scripts/shtest.sh"
eq() { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1" "expected: $2" "actual:   $3"; fi; }

M=$T/mod
mkdir -p "$M/a" "$M/b" "$M/journey" "$M/e2e" "$M/a/testdata" "$M/.hidden"
printf 'module p\n' > "$M/go.mod"
cat > "$M/a/a_test.go" <<'GO'
package a

import "testing"

func TestMain(m *testing.M) { m.Run() }

func TestMemory(t *testing.T) {
	if 1+1 != 2 {
		t.Fatal("sum")
	}
}

func TestTemp(t *testing.T) { _ = t.TempDir() }

func TestCommentedMarkerIsIgnored(t *testing.T) {
	// exec.Command("tmux") would be tmux, but this is a comment
	_ = 1
}

func TestHome(t *testing.T) {
	home := testjail.Fleet(t)
	_ = home
}

func TestThroughHelper(t *testing.T) {
	startFake(t)
}

func startFake(t *testing.T) {
	deeper(t)
}

func deeper(t *testing.T) {
	cmd := exec.Command("sh", "-c", "true")
	_ = cmd
}

func TestTmuxServer(t *testing.T) {
	_ = exec.Command("tmux", "-S", "x", "new-session", "-d")
}

func TestSQLite(t *testing.T) { _ = sqlitedb.Open("x") }

func FuzzParse(f *testing.F) { f.Fuzz(func(t *testing.T, s string) {}) }

func helperNotATest(t *testing.T) { _ = exec.Command("tmux") }

type fakeRunner struct{}

func (fakeRunner) Run() { _ = exec.Command("sh") }

func TestNamesTheMethodOnlyInAStringOrLocal(t *testing.T) {
	Run := "Run"
	_ = Run
}

func TestCallsTheMethod(t *testing.T) { fakeRunner{}.Run() }

var spawn = func(t *testing.T) {
	_ = exec.Command("sh")
}

func TestThroughAClosureVar(t *testing.T) { spawn(t) }
GO
cat > "$M/b/b_jail_test.go" <<'GO'
package b

import "testing"

func TestInAJailFile(t *testing.T) {}
GO
for skip in journey e2e a/testdata .hidden; do
  printf 'package x\n\nimport "testing"\n\nfunc TestNotCounted(t *testing.T) {}\n' > "$M/$skip/x_test.go"
done

out=$(bash "$SUT" "$M" 2>&1); rc=$?
eq "the count line: unit = nothing + temp-dir, move = the bottom three, every Test and Fuzz once" \
  "0|KINDS unit 5 (nothing 4 · temp-dir 1) · move 7 (home-sqlite 2 · process 3 · tmux 2) · total 12" "$rc|$out"

out=$(bash "$SUT" --list "$M" 2>&1); rc=$?
want=$(printf '%s\n' \
  $'nothing\ta\ta_test.go\tTestMemory' \
  $'temp-dir\ta\ta_test.go\tTestTemp' \
  $'nothing\ta\ta_test.go\tTestCommentedMarkerIsIgnored' \
  $'home-sqlite\ta\ta_test.go\tTestHome' \
  $'process\ta\ta_test.go\tTestThroughHelper' \
  $'tmux\ta\ta_test.go\tTestTmuxServer' \
  $'home-sqlite\ta\ta_test.go\tTestSQLite' \
  $'nothing\ta\ta_test.go\tFuzzParse' \
  $'nothing\ta\ta_test.go\tTestNamesTheMethodOnlyInAStringOrLocal' \
  $'process\ta\ta_test.go\tTestCallsTheMethod' \
  $'process\ta\ta_test.go\tTestThroughAClosureVar' \
  $'tmux\tb\tb_jail_test.go\tTestInAJailFile')
eq "--list: one row per test with its kind, a helper two calls deep or a closure var reaching its test, a method only through a selector, a jail file tmux whole" "0|$want" "$rc|$out"

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

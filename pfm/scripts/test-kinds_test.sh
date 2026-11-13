#!/usr/bin/env bash
# Fixture-driven tests for scripts/test-kinds.sh: each kind is read from its marker, a helper's
# marker reaches every test naming it (transitively), a jail file's test is real and its helpers tmux,
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

func TestSQLite(t *testing.T) { _ = sqlitedb.OpenStore(ctx, "x") }

func TestMemoryStore(t *testing.T) { _, _ = store.OpenMemory() }

func TestFileStore(t *testing.T) { _, _ = store.OpenContext(ctx) }

func TestDatabaseFiles(t *testing.T) { testjail.DatabaseFiles(t, t.TempDir()) }

func TestForeignBuiltBySQLOpen(t *testing.T) { _, _ = sql.Open("sqlite", "x") }

func TestReachesAJailHelper(t *testing.T) { startServer(t) }

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

func TestWritesAnExecutableToRun(t *testing.T) { _ = testjail.WriteExecutable("x", nil, 0o700) }

func TestWritesAnExecutableNeverRun(t *testing.T) { _ = testjail.WriteExecutableNeverRun("x", nil, 0o700) }
GO
cat > "$M/b/b_jail_test.go" <<'GO'
package b

import "testing"

func TestInAJailFile(t *testing.T) {}
GO
cat > "$M/a/server_jail_test.go" <<'GO'
package a

import "testing"

func startServer(t *testing.T) {}

func TestInAJailFileOfA(t *testing.T) { startServer(t) }
GO
cat > "$M/b/seat_jail_linux_test.go" <<'GO'
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
for skip in journey e2e a/testdata .hidden; do
  printf 'package x\n\nimport "testing"\n\nfunc TestNotCounted(t *testing.T) {}\n' > "$M/$skip/x_test.go"
done

out=$(bash "$SUT" "$M" 2>&1); rc=$?
eq "the count line: unit = nothing + temp-dir, a door open memory, real = the jail files, move = the bottom three, every Test and Fuzz once" \
  "0|KINDS unit 12 (nothing 9 · temp-dir 3) · real 3 · move 8 (home-sqlite 2 · process 4 · tmux 2) · total 23" "$rc|$out"

out=$(bash "$SUT" --list "$M" 2>&1); rc=$?
want=$(printf '%s\n' \
  $'nothing\ta\ta_test.go\tTestMemory' \
  $'temp-dir\ta\ta_test.go\tTestTemp' \
  $'nothing\ta\ta_test.go\tTestCommentedMarkerIsIgnored' \
  $'temp-dir\ta\ta_test.go\tTestHome' \
  $'process\ta\ta_test.go\tTestThroughHelper' \
  $'tmux\ta\ta_test.go\tTestTmuxServer' \
  $'nothing\ta\ta_test.go\tTestSQLite' \
  $'nothing\ta\ta_test.go\tTestMemoryStore' \
  $'nothing\ta\ta_test.go\tTestFileStore' \
  $'home-sqlite\ta\ta_test.go\tTestDatabaseFiles' \
  $'home-sqlite\ta\ta_test.go\tTestForeignBuiltBySQLOpen' \
  $'tmux\ta\ta_test.go\tTestReachesAJailHelper' \
  $'nothing\ta\ta_test.go\tFuzzParse' \
  $'nothing\ta\ta_test.go\tTestNamesTheMethodOnlyInAStringOrLocal' \
  $'process\ta\ta_test.go\tTestCallsTheMethod' \
  $'process\ta\ta_test.go\tTestThroughAClosureVar' \
  $'process\ta\ta_test.go\tTestWritesAnExecutableToRun' \
  $'temp-dir\ta\ta_test.go\tTestWritesAnExecutableNeverRun' \
  $'real\ta\tserver_jail_test.go\tTestInAJailFileOfA' \
  $'real\tb\tb_jail_test.go\tTestInAJailFile' \
  $'nothing\tb\tkilljail_test.go\tTestJailIsNotItsOwnWord' \
  $'nothing\tb\tnew_jail_stubs_test.go\tTestJailStubsIsNoOS' \
  $'real\tb\tseat_jail_linux_test.go\tTestInAnOSSelectedJailFile')
eq "--list: one row per test with its kind, a helper two calls deep or a closure var reaching its test, a method only through a selector, an executable written to run process and one never run temp-dir, a jail file's test real and its helper tmux to a test outside it" "0|$want" "$rc|$out"

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

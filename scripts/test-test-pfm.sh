#!/usr/bin/env bash
# Exercise .claude/scripts/test-pfm.sh — the only way an agent runs pfm's tests — against a fake
# repository: its usage line, every path spelling, the outside / two-file / deleted / missing rules,
# the routing of a file to its runner, the -run built from a file's Test funcs or taken from a
# ::selector byte for byte (an empty selector is the usage line), Test and Fuzz funcs both in the built
# -run, -v on every go invocation, the zero-test / unlisted-skip / no-test-files verdicts (a run that
# exits 0 yet ran no test, or skipped one pfm/scripts/known-skips.tsv does not list for its package, is
# red and names the file or selector; a listed skip is green), the
# one fence container per run, ALL, the first non-zero exit code, the empty-selection message naming
# every dropped file, the log and the two green lines. dev.sh, go, make and docker are stubs in the fake root that record
# their argv and exit as the test tells them; the host checkout and the real fence are never touched.
#   bash scripts/test-test-pfm.sh [path to test-pfm.sh]
# BROKEN STATE: a missing test-pfm.sh fails every case (the copy error is printed), never 0 failed.
set -euo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${1:-$HERE/.claude/scripts/test-pfm.sh}"
SHTEST_TAG=test-test-pfm
# shellcheck source=scripts/shtest.sh
source "$HERE/scripts/shtest.sh"

NAME="tpfm$$"
# shellcheck source=scripts/stub-root.sh
source "$HERE/scripts/stub-root.sh"
for s in go make docker; do ln -s .stub "$BIN/$s"; done
# The SUT runs with PATH=$BIN alone: the host's go, make and docker are never reachable.
for t in bash env sh git realpath dirname basename date mkdir cat grep sed awk tail head rm wc tr sort mktemp cp chmod ls cut uniq tee; do
  p="$(command -v "$t")" || continue
  ln -s "$p" "$BIN/$t"
done

cp "$SUT" "$R/.claude/scripts/test-pfm.sh" || true
cp "$HERE/.claude/scripts/unit-path.sh" "$R/.claude/scripts/unit-path.sh"
chmod +x "$R/.claude/scripts/test-pfm.sh" 2>/dev/null || true

# --- fixture repository ---------------------------------------------------------------------------
mkdir -p "$R/pfm/internal/a" "$R/pfm/e2e" "$R/pfm/scripts" "$R/pfm/docs" "$R/docs"
printf 'package a\n\nfunc TestOne(t *testing.T) {}\nfunc TestTwo(t *testing.T) {}\nfunc FuzzSeed(f *testing.F) {}\nfunc helperTest(t *testing.T) {}\nfunc helperFuzz(f *testing.F) {}\n// func TestCommented(t *testing.T) {}\n' >"$R/pfm/internal/a/a_test.go"
printf 'package a\n\nfunc FuzzOnly(f *testing.F) {}\n' >"$R/pfm/internal/a/fuzz_test.go"
printf 'module example.test/pfm\n\ngo 1.22\n' >"$R/pfm/go.mod"
printf '# header\nexample.test/pfm/internal/a\tTestListed\thelper\tre-exec fixture\n' >"$R/pfm/scripts/known-skips.tsv"
printf 'package a\n\nfunc TestMain(m *testing.M) {}\n' >"$R/pfm/internal/a/helper_test.go"
printf 'package a\n\nfunc TestGone(t *testing.T) {}\n' >"$R/pfm/internal/a/gone_test.go"
printf 'package a\n' >"$R/pfm/internal/a/a.go"
printf 'package e2e\n\nfunc TestE2E(t *testing.T) {}\n' >"$R/pfm/e2e/x_test.go"
# shellcheck disable=SC2016 # the fixture's body is literal: its $0 and $REC expand when it runs
printf '#!/usr/bin/env bash\nprintf "bash %%s\\n" "$0" >>"$REC"\n' >"$R/pfm/scripts/s_test.sh"
echo x >"$R/pfm/docs/x.md"
echo x >"$R/docs/x.md"
echo x >"$R/docs/only.md"
git -C "$R" init -q
git -C "$R" add -A
git -C "$R" -c user.name=t -c user.email=t@t commit -qm base
rm "$R/pfm/internal/a/gone_test.go"
R="$(cd "$R" && pwd -P)"

# --- harness ----------------------------------------------------------------------------------------
rule() { printf '%s\001%s\001%s\001%s\n' "$1" "$2" "$3" "${4-}" >>"$RULES"; } # rule <tool> <argv substring> <rc> [output]
run() { # run <cwd> <arg>...: OUT holds stdout+stderr, RC the exit code
  local cwd="$1"; shift
  : >"$REC"; : >"$REC.env"
  OUT="$(cd "$cwd" && env -u PFM_DEV_FENCE PATH="$BIN" REC="$REC" RULES="$RULES" STUBLIB="$STUBLIB" \
    "$R/.claude/scripts/test-pfm.sh" "$@" 2>&1)" && RC=0 || RC=$?
}
reset() { : >"$RULES"; }
expect() { # expect <name> <rc> <line that must be in OUT>
  if [ "$RC" = "$2" ] && grep -qxF -- "$3" <<<"$OUT"; then ok "$1"; else bad "$1" "rc=$RC want $2, want line: $3" "$OUT"; fi
}
rc_is() { if [ "$RC" = "$2" ]; then ok "$1"; else bad "$1" "rc=$RC want $2" "$OUT"; fi; }
out_has() { if grep -qxF -- "$2" <<<"$OUT"; then ok "$1"; else bad "$1" "no output line: $2" "$OUT"; fi; }
out_lacks() { if grep -qxF -- "$2" <<<"$OUT"; then bad "$1" "unexpected output line: $2" "$OUT"; else ok "$1"; fi; }
rec_line() { # rec_line <tool> <arg>...: the 1-based line of $REC holding exactly that call, or empty
  local want
  want="$1$(printf ' %q' "${@:2}")"
  grep -nxF -- "$want" "$REC" | head -1 | cut -d: -f1 || true
}
rec_has() { # rec_has <name> <tool> <arg>...
  local name="$1"; shift
  if [ -n "$(rec_line "$@")" ]; then ok "$name"; else bad "$name" "call not recorded: $*" "$(cat "$REC")"; fi
}
dev_calls() { grep -c '^dev.sh ' "$REC" || true; }
no_dev() { if [ "$(dev_calls)" = 0 ]; then ok "$1"; else bad "$1" "dev.sh was called" "$(cat "$REC")"; fi; }

U='usage: test-pfm.sh ALL | <test file>[::<go -run regexp>]...'
GO_A=(go -C pfm test -v -count=1 ./internal/a/ -run '^(TestOne|TestTwo|FuzzSeed)$')
GO_FZ=(go -C pfm test -v -count=1 ./internal/a/ -run '^(FuzzOnly)$')
GO_E=(go -C pfm test -v -tags e2e -count=1 ./e2e/ -run '^(TestE2E)$')

# --- the usage rule -------------------------------------------------------------------------------
run "$R"
expect "no argument prints the usage line, exit 2" 2 "$U"
for a in '' -x --all 'pfm/internal/*_test.go' 'pfm/internal/a/[ab]_test.go' 'pfm/internal/a/?_test.go'; do
  run "$R" "$a"
  expect "argument '$a' prints the usage line, exit 2" 2 "$U"
  no_dev "argument '$a' never reaches dev.sh"
done
run "$R" ALL pfm/internal/a/a_test.go
expect "ALL beside a file prints the usage line, exit 2" 2 "$U"
no_dev "ALL beside a file never reaches dev.sh"
reset
run "$R" 'pfm/internal/a/a_test.go::Test[AB]'
rc_is "a glob character in the selector is not a pattern" 0
rec_has "a glob character in the selector reaches -run" "${GO_A[@]:0:${#GO_A[@]}-1}" 'Test[AB]'
for a in 'pfm/internal/a/a_test.go::' 'pfm/scripts/s_test.sh::'; do
  run "$R" "$a"
  expect "an empty selector ('$a') prints the usage line, exit 2" 2 "$U"
  no_dev "an empty selector ('$a') never reaches dev.sh"
done
run "$R" pfm/e2e/x_test.go 'pfm/internal/a/a_test.go::'
expect "an empty selector beside a valid file prints the usage line, exit 2" 2 "$U"
no_dev "an empty selector beside a valid file never reaches dev.sh"

# --- path spellings, all through the resolver -----------------------------------------------------
run "$R" pfm/internal/a/a_test.go
rc_is "a repository-relative path runs" 0
rec_has "a repository-relative path runs its package" "${GO_A[@]}"
run "$R" internal/a/a_test.go
rec_has "a unit-relative path runs its package" "${GO_A[@]}"
run "$R/pfm/internal" a/a_test.go
rec_has "a cwd-relative path runs its package" "${GO_A[@]}"
run "$T" "$R/pfm/internal/a/a_test.go"
rec_has "an absolute path runs its package" "${GO_A[@]}"
run "$R" docs/only.md
expect "a root file is outside pfm, exit 2 naming it" 2 "test-pfm.sh: docs/only.md is outside pfm"
run "$R" docs/x.md
expect "a spelling that resolves to two files exits 2 naming both" 2 "test-pfm.sh: docs/x.md resolves to two files: docs/x.md and pfm/docs/x.md"
run "$R" /etc/hostname
expect "an absolute path outside the repository is outside pfm, exit 2" 2 "test-pfm.sh: /etc/hostname is outside pfm"
run "$R" pfm/internal/a/nope_test.go
expect "a path that is no file exits 2 naming it" 2 "test-pfm.sh: pfm/internal/a/nope_test.go is no such file in pfm"
no_dev "a path that is no file never reaches dev.sh"
run "$R" pfm/internal/a/a.go
expect "a file that is no test file exits 2 naming it" 2 "test-pfm.sh: pfm/internal/a/a.go is not a test file"
no_dev "a file that is no test file never reaches dev.sh"
run "$R" pfm/docs/x.md
expect "a document is no test file, exit 2" 2 "test-pfm.sh: pfm/docs/x.md is not a test file"

# --- deletions drop ---------------------------------------------------------------------------------
run "$R" pfm/internal/a/gone_test.go pfm/internal/a/a_test.go
rc_is "a file deleted at HEAD drops silently beside a live one" 0
rec_has "the live file still runs after a drop" "${GO_A[@]}"
run "$R" pfm/internal/a/gone_test.go
expect "a deletion-only selection exits 2 naming the dropped file" 2 "test-pfm.sh: no file left to run — deleted at HEAD: pfm/internal/a/gone_test.go"
no_dev "a deletion-only selection never reaches dev.sh"
run "$R" pfm/internal/a/gone_test.go pfm/internal/a/helper_test.go
expect "an emptied selection names the deletion and the no-Test file together" 2 "test-pfm.sh: no file left to run — deleted at HEAD: pfm/internal/a/gone_test.go; no Test func: pfm/internal/a/helper_test.go"
no_dev "an emptied mixed selection never reaches dev.sh"
run "$R/pfm/internal" a/gone_test.go a/helper_test.go
expect "a deletion is named as the caller spelled it" 2 "test-pfm.sh: no file left to run — deleted at HEAD: a/gone_test.go; no Test func: pfm/internal/a/helper_test.go"

# --- routing and the -run regexp ----------------------------------------------------------------------
run "$R" pfm/e2e/x_test.go
rec_has "an e2e file runs with the e2e tag in its own package" "${GO_E[@]}"
run "$R" pfm/scripts/s_test.sh
rec_has "a shell test runs through bash by its path" bash pfm/scripts/s_test.sh
run "$R" 'pfm/scripts/s_test.sh::x'
expect "a shell test takes no selector, exit 2" 2 "test-pfm.sh: pfm/scripts/s_test.sh::x: a shell test takes no selector"
no_dev "a shell test with a selector never reaches dev.sh"
# shellcheck disable=SC2016 # the $( ) is the point: a selector no shell may evaluate
run "$R" 'pfm/internal/a/a_test.go::TestOne/sub case$(touch PWNED)|x$'
# shellcheck disable=SC2016 # literal, as above
rec_has "a selector passes byte for byte into -run, never through a shell" go -C pfm test -v -count=1 ./internal/a/ -run 'TestOne/sub case$(touch PWNED)|x$'
if [ -e "$R/PWNED" ]; then bad "a selector is never evaluated by a shell" "PWNED exists"; else ok "a selector is never evaluated by a shell"; fi
run "$R" pfm/internal/a/fuzz_test.go
rec_has "a file with only Fuzz funcs is run, its Fuzz func in -run" "${GO_FZ[@]}"
out_lacks "a file with only Fuzz funcs is not dropped" "test-pfm.sh: no Test func, dropped: pfm/internal/a/fuzz_test.go"
run "$R" pfm/internal/a/helper_test.go
expect "a Go file with no Test func (TestMain is none) exits 2 naming it" 2 "test-pfm.sh: no file left to run — no Test func: pfm/internal/a/helper_test.go"
no_dev "a Go file with no Test func never reaches dev.sh"
run "$R" pfm/internal/a/helper_test.go pfm/e2e/x_test.go
rec_has "a Go file with no Test func drops beside a live one" "${GO_E[@]}"
out_has "the dropped Go file is named on stderr" "test-pfm.sh: no Test func, dropped: pfm/internal/a/helper_test.go"

# --- one container, in order, first non-zero code ------------------------------------------------------
run "$R" pfm/internal/a/a_test.go pfm/e2e/x_test.go pfm/scripts/s_test.sh
if [ "$(dev_calls)" = 1 ]; then ok "several files run in one dev.sh call"; else bad "several files run in one dev.sh call" "$(cat "$REC")"; fi
la="$(rec_line "${GO_A[@]}")"; le="$(rec_line "${GO_E[@]}")"; ls_="$(rec_line bash pfm/scripts/s_test.sh)"
if [ -n "$la" ] && [ -n "$le" ] && [ -n "$ls_" ] && [ "$la" -lt "$le" ] && [ "$le" -lt "$ls_" ]; then
  ok "the invocations run in the order named"
else bad "the invocations run in the order named" "$la $le $ls_"; fi
reset; rule go './internal/a/' 3; rule go './e2e/' 5
run "$R" pfm/internal/a/a_test.go pfm/e2e/x_test.go
rc_is "the first non-zero code wins (first named fails)" 3
rec_has "a failed invocation does not stop the next" "${GO_E[@]}"
run "$R" pfm/e2e/x_test.go pfm/internal/a/a_test.go
rc_is "the first non-zero code wins (second named fails too)" 5
reset; rule go './internal/a/' 3
run "$R" pfm/e2e/x_test.go pfm/internal/a/a_test.go
rc_is "a later failure surfaces when earlier ones pass" 3
reset

# --- a run that ran no test is red ----------------------------------------------------------------------
Z=pfm/internal/a/a_test.go
G2='Green. When your work is done, run .claude/scripts/check-pfm.sh <your task'\''s files> once, then write your return.'
rule go '' 0
run "$R" "$Z"
expect "a go run that exits 0 with no test line is NO TEST RAN" 1 "test-pfm.sh: NO TEST RAN in $Z"
out_lacks "a NO TEST RAN run never prints the Green line" "$G2"
reset; rule go '' 0 'ok  \tpfm/internal/a\t0.01s [no tests to run]'
run "$R" "$Z::NoSuchTest"
expect "a selector that matches nothing is NO TEST RAN, naming the selector" 1 "test-pfm.sh: NO TEST RAN in $Z::NoSuchTest"
reset; rule go '' 0 '?   \tpfm/internal/a\t[no test files]'
run "$R" "$Z"
expect "[no test files] is NO TEST RAN" 1 "test-pfm.sh: NO TEST RAN in $Z"
reset; rule go '' 0 '--- PASS: TestOne (0.00s)\nok  \tpfm/internal/a\t0.01s [no tests to run]'
run "$R" "$Z"
expect "[no tests to run] is NO TEST RAN even beside a PASS line" 1 "test-pfm.sh: NO TEST RAN in $Z"
reset; rule go '' 0 '=== RUN   TestOne\n--- SKIP: TestOne (0.00s)\n--- PASS: TestTwo (0.00s)'
run "$R" "$Z"
expect "an unlisted skipped test is UNLISTED SKIP, even beside a PASS line" 1 "test-pfm.sh: UNLISTED SKIP TestOne in $Z"
out_lacks "an UNLISTED SKIP run never prints the Green line" "$G2"
reset; rule go '' 0 '--- PASS: TestOne (0.00s)\n    --- SKIP: TestOne/sub (0.00s)'
run "$R" "$Z"
expect "an unlisted skipped subtest is UNLISTED SKIP, named whole" 1 "test-pfm.sh: UNLISTED SKIP TestOne/sub in $Z"
reset; rule go '' 0 '--- PASS: TestOne (0.00s)\nPASS'
run "$R" "$Z"
rc_is "a go run with a PASS line is green" 0
out_has "a green go run prints the Green line" "$G2"
reset; rule go './internal/a/' 0 '--- PASS: TestOne (0.00s)'; rule go './e2e/' 0
run "$R" "$Z" pfm/e2e/x_test.go
expect "only the file that ran no test is named" 1 "test-pfm.sh: NO TEST RAN in pfm/e2e/x_test.go"
out_lacks "a file that ran is not named NO TEST RAN" "test-pfm.sh: NO TEST RAN in $Z"
reset; rule go './internal/a/' 3; rule go './e2e/' 0
run "$R" "$Z" pfm/e2e/x_test.go
rc_is "the first non-zero code wins over a later NO TEST RAN" 3
out_has "a NO TEST RAN after a red file is still named" "test-pfm.sh: NO TEST RAN in pfm/e2e/x_test.go"
reset; rule go './internal/a/' 0; rule go './e2e/' 5
run "$R" "$Z" pfm/e2e/x_test.go
rc_is "an earlier NO TEST RAN wins over a later non-zero code" 1
reset; rule go './internal/a/' 3 '--- SKIP: TestOne (0.00s)'
run "$R" "$Z"
rc_is "a red run that also skipped keeps the runner's code" 3
out_lacks "a red run is not re-labelled UNLISTED SKIP" "test-pfm.sh: UNLISTED SKIP TestOne in $Z"
reset; rule go '' 0 '--- SKIP: TestListed (0.00s)\n--- PASS: TestOne (0.00s)'
run "$R" "$Z"
rc_is "a listed skip beside a PASS line is green" 0
out_has "a listed skip run prints the Green line" "$G2"
out_lacks "a listed skip is never named UNLISTED SKIP" "test-pfm.sh: UNLISTED SKIP TestListed in $Z"
reset; rule go '' 0 '--- PASS: TestListed (0.00s)\n    --- SKIP: TestListed/sub (0.00s)'
run "$R" "$Z"
rc_is "a subtest is covered by its parent test's row" 0
reset; rule go '' 0 '--- SKIP: TestListed (0.00s)\n--- SKIP: TestOne (0.00s)\n--- PASS: TestTwo (0.00s)'
run "$R" "$Z"
expect "an unlisted skip beside a listed one is named alone" 1 "test-pfm.sh: UNLISTED SKIP TestOne in $Z"
out_lacks "the listed skip beside an unlisted one is not named" "test-pfm.sh: UNLISTED SKIP TestListed in $Z"
reset; rule go './e2e/' 0 '--- SKIP: TestListed (0.00s)\n--- PASS: TestE2E (0.00s)'
run "$R" pfm/e2e/x_test.go
expect "a skip listed for another package is UNLISTED SKIP" 1 "test-pfm.sh: UNLISTED SKIP TestListed in pfm/e2e/x_test.go"
reset; rule go '' 0 '--- SKIP: TestListed (0.00s)\n--- PASS: TestOne (0.00s)'
mv "$R/pfm/scripts/known-skips.tsv" "$R/known-skips.bak"
run "$R" "$Z"
mv "$R/known-skips.bak" "$R/pfm/scripts/known-skips.tsv"
expect "an unreadable allow-list makes every skip unlisted" 1 "test-pfm.sh: UNLISTED SKIP TestListed in $Z"
out_has "an unreadable allow-list is named" "test-pfm.sh: pfm/scripts/known-skips.tsv unreadable — every skip counts as unlisted"
reset
run "$R" pfm/scripts/s_test.sh
rc_is "a shell test is judged by its exit code alone (no test line needed)" 0
out_has "a green shell test prints the Green line" "$G2"
reset

# --- ALL ----------------------------------------------------------------------------------------------
run "$R" ALL
rc_is "ALL exits 0 on a green run" 0
if [ "$(head -1 <<<"$OUT")" = "After a fix, run only the files that failed last round." ]; then
  ok "ALL prints its rerun reminder first"
else bad "ALL prints its rerun reminder first" "$OUT"; fi
rec_has "ALL runs every test in the fence" dev.sh iso test pfm
rule dev.sh 'iso test pfm' 7 'stub red'
run "$R" ALL
rc_is "ALL exits with the runner's code" 7
reset

# --- log and green lines ------------------------------------------------------------------------------
G1='A file that failed in a wider run and now passes alone, with no change that explains it, fails alongside others: rerun the selection it failed in.'
rule go '' 0 'ran-go\n--- PASS: TestStub (0.00s)'
run "$R" pfm/internal/a/a_test.go
LOG="$(sed -n 's/^log: //p' <<<"$OUT")"
if [[ "$LOG" =~ ^/tmp/$NAME/test-pfm/[0-9]{8}T[0-9]{6}Z-[0-9]+\.log$ ]]; then ok "the log path is printed under /tmp/{project}/test-pfm"; else bad "the log path is printed under /tmp/{project}/test-pfm" "$OUT"; fi
if [ -f "$LOG" ] && grep -qxF ran-go "$LOG"; then ok "the runner's output is in the log"; else bad "the runner's output is in the log" "$LOG"; fi
out_lacks "a green run keeps the runner's output out of stdout" ran-go
out_has "a green file selection prints the reruns line" "$G1"
out_has "a green file selection prints the Green line" "$G2"
run "$R" ALL
out_lacks "ALL never prints the reruns line" "$G1"
out_lacks "ALL never prints the Green line" "$G2"
reset
rule go '' 1 "$(printf 'L%d\\n' $(seq 1 50))"
run "$R" pfm/internal/a/a_test.go
rc_is "a red file selection exits with the runner's code" 1
out_lacks "a red file selection never prints the Green line" "$G2"
out_has "a red run prints the log's last line" L50
out_has "a red run prints the log's last 40 lines" L11
out_lacks "a red run prints no more than the log's last 40 lines" L10
reset

shtest_end

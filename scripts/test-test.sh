#!/usr/bin/env bash
# Exercise .claude/scripts/test.sh — the one test command — against a fake repository: its usage line,
# every path spelling through the one resolver (the outside, two-file, missing and deleted rules), the
# derivation of each path's affected tests (a Go test file's Test and Fuzz funcs in -run or its
# ::selector byte for byte, TestMain excluded, a test file with none or a non-test .go file running its
# whole package, the e2e tag, shell, TAP and unittest runners, a script bringing its sibling test, a
# file bringing none), the one fence call per run (PFM_FENCE_NET=private, the caller's pid watched), the
# TEST line verdicts (a red runner, a run that proved nothing, an unlisted skip, a skipped TAP or
# unittest test), every check after one fails (fmt, typecheck's scope, lint-new's scope, arch,
# launch-literals, rumdl, leak and the templates unit's checks), the install gap, the host's verdict
# when the fence dies (docker absent, its daemon down, an exit before a verdict, a PASS its exit
# contradicts), the log, the red tail and --full. dev.sh and every tool are stubs in the fake root that
# record their argv and exit as the test tells them; `iso run` executes its command string with
# PFM_DEV_FENCE=1 like the real fence. The host checkout and the real fence are never touched.
#   bash scripts/test-test.sh [path to test.sh]
# BROKEN STATE: a missing test.sh fails every case (the copy error is printed), never 0 failed.
set -euo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${1:-$HERE/.claude/scripts/test.sh}"
SHTEST_TAG=test-test
# shellcheck source=scripts/shtest.sh
source "$HERE/scripts/shtest.sh"
shtest_isolate_host

NAME="ttest$$"
shtest_clean_also "/tmp/$NAME"
shtest_probe_trap_guard "/tmp/$NAME"
# shellcheck source=scripts/stub-root.sh
source "$HERE/scripts/stub-root.sh"
for s in go make golangci-lint node python3 jq rumdl; do ln -s .stub "$BIN/$s"; done
# The SUT runs with PATH=$BIN alone: the host's go, make, node, python3 and docker are never reachable;
# docker exists only while a case links it.
for t in bash env sh git realpath dirname basename date mkdir cat grep sed awk tail head rm wc tr sort mktemp cp chmod ls cut uniq tee; do
  p="$(command -v "$t")" || continue
  ln -s "$p" "$BIN/$t"
done

cp "$SUT" "$R/.claude/scripts/test.sh" || true
cp "$HERE/.claude/scripts/unit-path.sh" "$R/.claude/scripts/unit-path.sh"
chmod +x "$R/.claude/scripts/test.sh" 2>/dev/null || true

# --- fixture repository ---------------------------------------------------------------------------
mkdir -p "$R/pfm/internal/a" "$R/pfm/internal/b" "$R/pfm/e2e" "$R/pfm/scripts" "$R/pfm/docs" "$R/docs" \
  "$R/scripts" "$R/infra/fence/lanes/tests" "$R/tools" "$R/templates/global/commands"
printf 'package a\n\nfunc TestOne(t *testing.T) {}\nfunc TestTwo(t *testing.T) {}\nfunc FuzzSeed(f *testing.F) {}\nfunc helperTest(t *testing.T) {}\nfunc helperFuzz(f *testing.F) {}\n// func TestCommented(t *testing.T) {}\n' >"$R/pfm/internal/a/a_test.go"
printf 'package a\n\nfunc FuzzOnly(f *testing.F) {}\n' >"$R/pfm/internal/a/fuzz_test.go"
printf 'package a\n\nfunc TestMain(m *testing.M) {}\n' >"$R/pfm/internal/a/helper_test.go"
printf 'package a\n\nfunc TestGone(t *testing.T) {}\n' >"$R/pfm/internal/a/gone_test.go"
printf 'package a\n' >"$R/pfm/internal/a/a.go"
printf 'package b\n' >"$R/pfm/internal/b/b.go"
printf 'package e2e\n\nfunc TestE2E(t *testing.T) {}\n' >"$R/pfm/e2e/x_test.go"
printf 'module example.test/pfm\n\ngo 1.22\n' >"$R/pfm/go.mod"
printf '# header\nexample.test/pfm/internal/a\tTestListed\thelper\tre-exec fixture\n' >"$R/pfm/scripts/known-skips.tsv"
# Every fixture script records its own run: "ran <path> <args>".
# shellcheck disable=SC2016 # the fixture body is literal: $0, $* and $REC expand when it runs
REC_BODY='#!/usr/bin/env bash\nprintf "ran %%s%%s\\n" "${0#./}" "${*:+ $*}" >>"$REC"\n'
for f in pfm/scripts/s_test.sh pfm/scripts/s.sh scripts/test-w.sh scripts/w.sh scripts/test-dev-report.sh \
  infra/fence/f.sh infra/fence/lanes/tests/f_test.sh; do
  # shellcheck disable=SC2059 # the format is the fixture body
  printf "$REC_BODY" >"$R/$f"
done
echo 'print(1)' >"$R/scripts/z.py"
echo 'print(1)' >"$R/scripts/test-z.py"
echo 'print(1)' >"$R/tools/y.py"
echo 'print(1)' >"$R/tools/y_test.py"
echo 'export {}' >"$R/templates/global/commands/x.mjs"
echo 'export {}' >"$R/templates/global/commands/x.test.mjs"
echo x >"$R/pfm/docs/x.md"
echo x >"$R/docs/x.md"
echo x >"$R/docs/only.md"
echo x >"$R/docs/gone.md"
echo x >"$R/docs/plain.txt"
# The checks' own scripts: each records its argv and exits as a rule for its name says.
stubscript() { # stubscript <path> <name>
  printf '#!/usr/bin/env bash\nsource "$STUBLIB"\nstub_record %s "$@"\nstub_rule %s "$@"\nexit "$RULE_RC"\n' "$2" "$2" >"$R/$1"
}
stubscript scripts/leak-check.sh leak-check.sh
stubscript scripts/clone-check.sh clone-check.sh
stubscript scripts/description-check.sh description-check.sh
stubscript infra/check-self-hosted-manifest.sh check-self-hosted-manifest.sh
echo 'export {}' >"$R/scripts/check-codex-markers.mjs"
# The catalogue's scratch-path sweep lists the tracked files through repo_git (pfm/scripts/repo-git.sh).
cp "$HERE/pfm/scripts/repo-git.sh" "$R/pfm/scripts/"
# shellcheck disable=SC2016 # literal: the functions expand when test.sh sources this file
printf '%s\n' 'source "$STUBLIB"' \
  'checks_templates_placeholders() { stub_record placeholders; stub_rule placeholders; [ "$RULE_RC" = 0 ] || fail_step "placeholders (exit $RULE_RC)"; }' \
  'checks_templates_scratch_paths() { stub_record scratch-paths; declare -F repo_git >/dev/null || { fail_step "scratch-paths: repo_git is not defined"; return; }; stub_rule scratch-paths; [ "$RULE_RC" = 0 ] || fail_step "scratch-paths (exit $RULE_RC)"; }' \
  >"$R/infra/fence/checks.sh"
git -C "$R" init -q
git -C "$R" add -A
git -C "$R" -c user.name=t -c user.email=t@t commit -qm base
rm "$R/pfm/internal/a/gone_test.go" "$R/docs/gone.md"
R="$(cd "$R" && pwd -P)"

# --- harness ----------------------------------------------------------------------------------------
rule() { printf '%s\001%s\001%s\001%s\n' "$1" "$2" "$3" "${4-}" >>"$RULES"; } # rule <tool> <argv substring> <rc> [output]
reset() { : >"$RULES"; }
docker_on() { ln -sf .stub "$BIN/docker"; }
docker_off() { rm -f "$BIN/docker"; }
run() { # run <cwd> <arg>...: OUT holds stdout+stderr, RC the exit code
  local cwd="$1"; shift
  : >"$REC"; : >"$REC.env"
  OUT="$(cd "$cwd" && env -u PFM_DEV_FENCE -u PFM_FENCE_NET -u PFM_FENCE_WATCH_PIDS PATH="$BIN" REC="$REC" \
    RULES="$RULES" STUBLIB="$STUBLIB" "$R/.claude/scripts/test.sh" "$@" 2>&1)" && RC=0 || RC=$?
}
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
rec_lacks() { # rec_lacks <name> <line prefix>
  if grep -q -- "^$2" "$REC"; then bad "$1" "recorded: $2" "$(cat "$REC")"; else ok "$1"; fi
}
ran_has() { if grep -qxF -- "ran $2" "$REC"; then ok "$1"; else bad "$1" "no run of: $2" "$(cat "$REC")"; fi; }
dev_calls() { grep -c '^dev.sh ' "$REC" || true; }
no_dev() { if [ "$(dev_calls)" = 0 ]; then ok "$1"; else bad "$1" "dev.sh was called" "$(cat "$REC")"; fi; }
checks() { sed -n 's/^CHECK \([A-Z]*\) \([a-z-]*\).*/\2/p' <<<"$OUT" | tr '\n' ' '; }

U='usage: test.sh <path>[::<selector>]... | test.sh --full'
GO_A=(go -C pfm test -v -count=1 ./internal/a/ -run '^(TestOne|TestTwo|FuzzSeed)$')
GO_A_PKG=(go -C pfm test -v -count=1 ./internal/a/)
GO_E=(go -C pfm test -v -tags e2e -count=1 ./e2e/ -run '^(TestE2E)$')
A=pfm/internal/a/a_test.go
docker_off

# --- the usage rule -------------------------------------------------------------------------------
run "$R"
expect "no argument prints the usage line, exit 2" 2 "$U"
for a in '' -x --all ALL 'pfm/internal/*_test.go' 'pfm/internal/a/[ab]_test.go' 'pfm/internal/a/?_test.go' "$A::"; do
  run "$R" "$a"
  if [ "$a" = ALL ]; then expect "ALL is no argument any more: a missing file, exit 2" 2 "test.sh: ALL is no such file in the repository"
  else expect "argument '$a' prints the usage line, exit 2" 2 "$U"; fi
  no_dev "argument '$a' never reaches dev.sh"
done
run "$R" --full x
expect "--full beside another argument prints the usage line, exit 2" 2 "$U"
run "$R" pfm/e2e/x_test.go "$A::"
expect "an empty selector beside a valid file prints the usage line, exit 2" 2 "$U"
no_dev "an empty selector beside a valid file never reaches dev.sh"

# --- path spellings, all through the resolver -----------------------------------------------------
reset
run "$R" "$A"
rc_is "a repository-relative path runs green" 0
rec_has "a repository-relative path runs its file's Test and Fuzz funcs" "${GO_A[@]}"
run "$R" internal/a/a_test.go
rec_has "a pfm-relative path runs its package" "${GO_A[@]}"
run "$R/pfm/internal" a/a_test.go
rec_has "a cwd-relative path runs its package" "${GO_A[@]}"
run "$T" "$R/$A"
rec_has "an absolute path runs its package" "${GO_A[@]}"
run "$R/pfm" docs/x.md
if [ "$RC" = 2 ] && grep -q '^test.sh: docs/x.md resolves to two files: ' <<<"$OUT"; then ok "a spelling that resolves to two files exits 2 naming them"
else bad "a spelling that resolves to two files exits 2 naming them" "$OUT"; fi
: >"$T/outside.txt"
run "$R" "$T/outside.txt"
expect "a path outside the repository exits 2 naming it" 2 "test.sh: $T/outside.txt is outside the repository"
run "$R" pfm/internal/a/nope_test.go
expect "a path that is no file exits 2 naming it" 2 "test.sh: pfm/internal/a/nope_test.go is no such file in the repository"
no_dev "a path that is no file never reaches dev.sh"

# --- deletions: dropped, the unit's checks still run ------------------------------------------------
run "$R" pfm/internal/a/gone_test.go "$A"
rc_is "a file deleted at HEAD drops silently beside a live one" 0
rec_has "the live file still runs after a drop" "${GO_A[@]}"
run "$R" pfm/internal/a/gone_test.go
rc_is "a deletion-only pfm selection is green when its checks are" 0
out_has "a deletion-only selection runs no test" "TESTS NONE (no test file covers the named paths)"
rec_has "a deletion-only pfm selection vets the whole module" go -C pfm vet ./...
out_has "a deletion-only selection skips fmt" "CHECK SKIP fmt (no named file it applies to)"
out_has "a deletion-only selection skips leak (no file left to scan)" "CHECK SKIP leak (no named file it applies to)"
run "$R" docs/gone.md
out_has "a deletion-only templates selection runs the templates checks" "CHECK PASS clone"
rec_lacks "a deletion-only templates selection runs no pfm check" "go -C pfm vet"

# --- derivation: each path's affected tests ----------------------------------------------------------
run "$R" pfm/e2e/x_test.go
rec_has "an e2e file runs with the e2e tag in its own package" "${GO_E[@]}"
# shellcheck disable=SC2016 # the $( ) is the point: a selector no shell may evaluate
run "$R" "$A"'::TestOne/sub case$(touch PWNED)|x$'
# shellcheck disable=SC2016 # literal, as above
rec_has "a selector passes byte for byte into -run, never through a shell" go -C pfm test -v -count=1 ./internal/a/ -run 'TestOne/sub case$(touch PWNED)|x$'
if [ -e "$R/PWNED" ] || [ -e "$R/pfm/PWNED" ]; then bad "a selector is never evaluated by a shell" "PWNED exists"; else ok "a selector is never evaluated by a shell"; fi
run "$R" "$A::Test[AB]"
rec_has "a glob character in the selector is not a pattern" "${GO_A_PKG[@]}" -run 'Test[AB]'
run "$R" pfm/internal/a/fuzz_test.go
rec_has "a file with only Fuzz funcs runs its Fuzz func" go -C pfm test -v -count=1 ./internal/a/ -run '^(FuzzOnly)$'
run "$R" pfm/internal/a/helper_test.go
rec_has "a test file with no Test func (TestMain is none) runs its whole package" "${GO_A_PKG[@]}"
out_has "the whole-package run is labelled by its package" "TEST PASS pfm/internal/a/"
run "$R" pfm/internal/a/a.go
rec_has "a non-test Go file runs its whole package" "${GO_A_PKG[@]}"
run "$R" pfm/internal/a/a.go "$A"
if [ "$(grep -c '^go -C pfm test -v' "$REC")" = 2 ]; then ok "a package and a file of it are two invocations"; else bad "a package and a file of it are two invocations" "$(cat "$REC")"; fi
run "$R" pfm/internal/a/a.go pfm/internal/a/a.go
if [ "$(grep -c '^go -C pfm test -v' "$REC")" = 1 ]; then ok "a repeated invocation runs once"; else bad "a repeated invocation runs once" "$(cat "$REC")"; fi
run "$R" "pfm/internal/a/a.go::TestOne"
expect "a non-test Go file takes no selector, exit 2" 2 "$U"
out_has "the selector refusal names the file" "test.sh: pfm/internal/a/a.go: a non-test Go file (its whole package runs) takes no selector"
run "$R" pfm/scripts/s_test.sh
ran_has "a pfm shell test runs through bash by its path" pfm/scripts/s_test.sh
run "$R" pfm/scripts/s.sh
ran_has "a pfm script brings its _test.sh sibling" pfm/scripts/s_test.sh
run "$R" pfm/scripts/s_test.sh::x
expect "a shell test takes no selector, exit 2" 2 "$U"
no_dev "a shell test with a selector never reaches dev.sh"
run "$R" scripts/w.sh
ran_has "scripts/x.sh brings scripts/test-x.sh" scripts/test-w.sh
run "$R" infra/fence/f.sh
ran_has "infra/fence/x.sh brings infra/fence/lanes/tests/x_test.sh" infra/fence/lanes/tests/f_test.sh
run "$R" .claude/scripts/dev.sh
ran_has "dev.sh brings scripts/test-dev-report.sh run against the tree's dev.sh" "scripts/test-dev-report.sh $R/.claude/scripts/dev.sh"
reset; rule node '--test' 0 'ok 1 - a case\n# pass 1'; rule python3 'unittest' 0 'Ran 2 tests\n\nOK'; rule python3 'test-z.py' 0 'Ran 1 test\n\nOK'
run "$R" templates/global/commands/x.mjs
rec_has "a .mjs file brings its .test.mjs sibling under node --test" node --test --test-reporter=tap templates/global/commands/x.test.mjs
run "$R" templates/global/commands/x.test.mjs::'a case'
rec_has "a .test.mjs selector is --test-name-pattern" node --test --test-reporter=tap '--test-name-pattern=a case' templates/global/commands/x.test.mjs
run "$R" tools/y.py
rec_has "a .py file brings its _test.py sibling under unittest" python3 -m unittest tools/y_test.py
run "$R" tools/y_test.py::case
rec_has "a _test.py selector is -k" python3 -m unittest tools/y_test.py -k case
run "$R" scripts/z.py
rec_has "scripts/x.py brings scripts/test-x.py" python3 scripts/test-z.py
rc_is "a green derived python run is green" 0
reset
run "$R" docs/plain.txt
rc_is "a file no test covers runs its checks only, green" 0
out_has "a file no test covers prints TESTS NONE" "TESTS NONE (no test file covers the named paths)"
run "$R" docs/plain.txt::x
expect "a selector on a file that is no test file is the usage line, exit 2" 2 "$U"

# --- one fence call, in order ------------------------------------------------------------------------
run "$R" "$A" pfm/e2e/x_test.go pfm/scripts/s_test.sh docs/only.md
if [ "$(dev_calls)" = 1 ]; then ok "every named path runs in one dev.sh call"; else bad "every named path runs in one dev.sh call" "$(cat "$REC")"; fi
if grep -q '^dev.sh iso run ' "$REC"; then ok "the one call is dev.sh iso run"; else bad "the one call is dev.sh iso run" "$(cat "$REC")"; fi
if grep -qE '^dev\.sh PFM_FENCE_NET=private WATCH=[0-9]+$' "$REC.env"; then ok "the fence call runs PFM_FENCE_NET=private, watching the caller's pid"
else bad "the fence call runs PFM_FENCE_NET=private, watching the caller's pid" "$(cat "$REC.env")"; fi
la="$(rec_line "${GO_A[@]}")"; le="$(rec_line "${GO_E[@]}")"; ls_="$(grep -nxF 'ran pfm/scripts/s_test.sh' "$REC" | cut -d: -f1)"
lc="$(grep -n '^golangci-lint fmt' "$REC" | head -1 | cut -d: -f1)"
if [ -n "$la" ] && [ -n "$le" ] && [ -n "$ls_" ] && [ -n "$lc" ] && [ "$la" -lt "$le" ] && [ "$le" -lt "$ls_" ] && [ "$ls_" -lt "$lc" ]; then
  ok "the tests run in the order named, then the checks"
else bad "the tests run in the order named, then the checks" "$la $le $ls_ $lc" "$(cat "$REC")"; fi
out_has "a mixed selection runs the pfm checks" "CHECK PASS lint-new"
out_has "a mixed selection runs the templates checks" "CHECK PASS codex-markers"
out_has "a mixed green run counts every test" "TESTS PASS 3"

# --- the TEST verdicts ------------------------------------------------------------------------------
reset; rule go './internal/a/' 3; rule go './e2e/' 0 '--- PASS: TestE2E (0.00s)'
run "$R" "$A" pfm/e2e/x_test.go
rc_is "a red runner exits 1" 1
out_has "a red runner is TEST FAIL with its exit code" "TEST FAIL $A (exit 3)"
out_has "a red runner does not stop the next" "TEST PASS pfm/e2e/x_test.go"
out_has "TESTS counts the failures" "TESTS FAIL 1 of 2"
out_has "a red test still runs every check" "CHECK PASS launch-literals"
out_has "a red run prints VERDICT FAIL" "VERDICT FAIL"
reset; rule go 'test -v' 0
run "$R" "$A"
expect "a go run that exits 0 with no PASS line is NO-TEST-RAN" 1 "TEST NO-TEST-RAN $A"
reset; rule go 'test -v' 0 'ok  \tpfm/internal/a\t0.01s [no tests to run]'
run "$R" "$A::NoSuchTest"
expect "a selector that matches nothing is NO-TEST-RAN, naming the selector" 1 "TEST NO-TEST-RAN $A::NoSuchTest"
reset; rule go 'test -v' 0 '?   \tpfm/internal/a\t[no test files]'
run "$R" "$A"
expect "[no test files] is NO-TEST-RAN" 1 "TEST NO-TEST-RAN $A"
reset; rule go 'test -v' 0 '--- PASS: TestOne (0.00s)\nok  \tpfm/internal/a\t0.01s [no tests to run]'
run "$R" "$A"
expect "[no tests to run] is NO-TEST-RAN even beside a PASS line" 1 "TEST NO-TEST-RAN $A"
reset; rule go 'test -v' 0 '=== RUN   TestOne\n--- SKIP: TestOne (0.00s)\n--- PASS: TestTwo (0.00s)'
run "$R" "$A"
expect "an unlisted skipped test is UNLISTED-SKIP, even beside a PASS line" 1 "TEST UNLISTED-SKIP TestOne in $A"
out_lacks "an UNLISTED-SKIP run is never TEST PASS" "TEST PASS $A"
reset; rule go 'test -v' 0 '--- PASS: TestOne (0.00s)\n    --- SKIP: TestOne/sub (0.00s)'
run "$R" "$A"
expect "an unlisted skipped subtest is UNLISTED-SKIP, named whole" 1 "TEST UNLISTED-SKIP TestOne/sub in $A"
reset; rule go 'test -v' 0 '--- SKIP: TestListed (0.00s)\n--- PASS: TestOne (0.00s)'
run "$R" "$A"
expect "a listed skip beside a PASS line is green" 0 "TEST PASS $A"
reset; rule go 'test -v' 0 '--- PASS: TestListed (0.00s)\n    --- SKIP: TestListed/sub (0.00s)'
run "$R" "$A"
rc_is "a subtest is covered by its parent test's row" 0
reset; rule go 'test -v' 0 '--- SKIP: TestListed (0.00s)\n--- SKIP: TestOne (0.00s)\n--- PASS: TestTwo (0.00s)'
run "$R" "$A"
expect "an unlisted skip beside a listed one is named alone" 1 "TEST UNLISTED-SKIP TestOne in $A"
out_lacks "the listed skip beside an unlisted one is not named" "TEST UNLISTED-SKIP TestListed in $A"
reset; rule go './e2e/' 0 '--- SKIP: TestListed (0.00s)\n--- PASS: TestE2E (0.00s)'
run "$R" pfm/e2e/x_test.go
expect "a skip listed for another package is UNLISTED-SKIP" 1 "TEST UNLISTED-SKIP TestListed in pfm/e2e/x_test.go"
reset; rule go 'test -v' 0 '--- SKIP: TestListed (0.00s)\n--- PASS: TestOne (0.00s)'
mv "$R/pfm/scripts/known-skips.tsv" "$R/known-skips.bak"
run "$R" "$A"
mv "$R/known-skips.bak" "$R/pfm/scripts/known-skips.tsv"
expect "an unreadable allow-list makes every skip unlisted" 1 "TEST UNLISTED-SKIP TestListed in $A"
if grep -qF 'test.sh: pfm/scripts/known-skips.tsv unreadable — every skip counts as unlisted' <<<"$OUT"; then ok "an unreadable allow-list is named"
else bad "an unreadable allow-list is named" "$OUT"; fi
reset; rule go 'test -v' 3 '--- SKIP: TestOne (0.00s)'
run "$R" "$A"
expect "a red run that also skipped stays TEST FAIL with the runner's code" 1 "TEST FAIL $A (exit 3)"
out_lacks "a red run is not re-labelled UNLISTED-SKIP" "TEST UNLISTED-SKIP TestOne in $A"
TM=templates/global/commands/x.test.mjs
reset; rule node '--test' 0 '# pass 0'
run "$R" "$TM"
expect "a TAP run with # pass 0 is NO-TEST-RAN" 1 "TEST NO-TEST-RAN $TM"
reset; rule node '--test' 0 "ok 1 - $TM\n# pass 1"
run "$R" "$TM::nomatch"
expect "a TAP run whose only pass is its own file is NO-TEST-RAN" 1 "TEST NO-TEST-RAN $TM::nomatch"
reset; rule node '--test' 0 'ok 1 - a\n# pass 1\n# skipped 1'
run "$R" "$TM"
expect "a TAP run with a skipped test is SKIPPED" 1 "TEST SKIPPED $TM"
reset; rule node '--test' 0 'ok 1 - a\n# pass 1\n# todo 1'
run "$R" "$TM"
expect "a TAP run with a todo test is SKIPPED" 1 "TEST SKIPPED $TM"
reset; rule python3 'unittest' 0 'Ran 0 tests\n\nOK'
run "$R" tools/y_test.py
expect "a unittest run with Ran 0 tests is NO-TEST-RAN" 1 "TEST NO-TEST-RAN tools/y_test.py"
reset; rule python3 'unittest' 0 'Ran 2 tests\n\nOK (skipped=1)'
run "$R" tools/y_test.py
expect "a unittest run with skipped=N is SKIPPED" 1 "TEST SKIPPED tools/y_test.py"
reset; rule python3 'unittest' 5 'Ran 0 tests\n\nNO TESTS RAN'
run "$R" tools/y_test.py::nomatch
expect "a unittest exit 5 NO TESTS RAN is NO-TEST-RAN" 1 "TEST NO-TEST-RAN tools/y_test.py::nomatch"
reset; rule python3 'unittest' 4 'Traceback'
run "$R" tools/y_test.py
expect "a unittest red is TEST FAIL with its code" 1 "TEST FAIL tools/y_test.py (exit 4)"
reset
run "$R" pfm/scripts/s_test.sh
expect "a shell test is judged by its exit code alone" 0 "TEST PASS pfm/scripts/s_test.sh"

# --- the pfm checks -----------------------------------------------------------------------------------
reset
run "$R" pfm/internal/a/a.go
if [ "$(checks)" = 'fmt typecheck lint-new arch launch-literals rumdl leak ' ]; then ok "the checks run in the order fmt, typecheck, lint-new, arch, launch-literals, rumdl, leak"
else bad "the check order" "$(checks)"; fi
rec_has "fmt gets the named go file relative to pfm" golangci-lint fmt --diff internal/a/a.go
rec_has "typecheck is go vet over the named package" go -C pfm vet ./internal/a
rec_lacks "a named go file never vets the whole module" 'go -C pfm vet ./\.\.\.$'
rec_has "lint-new runs the make target over the named package" make -C pfm --no-print-directory lint-new LINT_PKGS=./internal/a
rec_has "arch runs the make target" make -C pfm --no-print-directory arch
rec_has "launch-literals runs its one test" go -C pfm test ./internal/claudelaunch/ -run TestNoLaunchLiteralOutsideRegistry -count=1
rec_has "leak scans the named file" leak-check.sh --files pfm/internal/a/a.go
out_has "no named .md file skips rumdl" "CHECK SKIP rumdl (no named file it applies to)"
rec_lacks "a pfm-only selection runs no templates check" clone-check.sh
if grep -qE 'GOPROXY=off -C pfm mod download$' "$REC.env"; then ok "the install probe runs offline"; else bad "the install probe runs offline" "$(cat "$REC.env")"; fi
run "$R" pfm/docs/x.md
rec_has "no named go file vets the whole module" go -C pfm vet ./...
out_has "no named go file skips fmt" "CHECK SKIP fmt (no named file it applies to)"
out_has "no named go file skips lint-new" "CHECK SKIP lint-new (no named file it applies to)"
rec_has "a named .md file goes to rumdl" rumdl check --output-format concise pfm/docs/x.md
reset; rule golangci-lint fmt 0 'diff a/internal/a/a.go b/internal/a/a.go'
run "$R" pfm/internal/a/a.go
expect "a formatter diff is CHECK FAIL fmt, exit 1" 1 "CHECK FAIL fmt"
out_has "after fmt fails, arch still runs" "CHECK PASS arch"
reset; rule go ' list ' 0 'example.test/pfm\t\nexample.test/pfm/internal/a\tfmt \nexample.test/pfm/internal/c\tfmt example.test/pfm/internal/a \nexample.test/pfm/internal/d\t example.test/pfm/internal/a\nexample.test/pfm/internal/e\tos example.test/pfm/internal/c '
run "$R" pfm/internal/a/a.go
rec_has "typecheck vets the named package and its direct importers (imports and test imports)" go -C pfm vet ./internal/a ./internal/c ./internal/d
reset; rule go ' list ' 1 'go: pattern ./...: boom'
run "$R" pfm/internal/a/a.go
expect "a failed go list is CHECK FAIL typecheck" 1 "CHECK FAIL typecheck"
reset; rule go ' vet ' 1 'internal/b/b.go:5:25: undefined: a.F'
run "$R" pfm/docs/x.md
expect "a vet failure anywhere is CHECK FAIL typecheck" 1 "CHECK FAIL typecheck"
reset; rule make lint-new 1 'internal/a/a.go:3:1: a finding on the named file (lll)'
run "$R" pfm/internal/a/a.go
expect "a lint finding on the named file (pfm-relative) is CHECK FAIL lint-new" 1 "CHECK FAIL lint-new"
reset; rule make lint-new 1 "$R/pfm/internal/a/a.go:3:1: a finding on the named file (lll)"
run "$R" pfm/internal/a/a.go
expect "a lint finding on the named file (absolute) is CHECK FAIL lint-new" 1 "CHECK FAIL lint-new"
reset; rule make lint-new 1 'internal/b/b.go:9:9: elsewhere (lll)\ninternal/a/aa.go:1:1: a longer name sharing the prefix (lll)'
run "$R" pfm/internal/a/a.go
expect "lint findings only on other files are burn-down: CHECK PASS lint-new" 0 "CHECK PASS lint-new (burn-down, not your red: 2 finding(s), none on a file you named)"
out_lacks "no bare FAIL for a burn-down" "CHECK FAIL lint-new"
reset; rule make lint-new 1 'internal/b/b.go:5:25: undefined: a.F (typecheck)'
run "$R" pfm/internal/a/a.go
expect "a typecheck finding on any file is CHECK FAIL lint-new" 1 "CHECK FAIL lint-new"
reset; rule make lint-new 2 "Error: can't load config"
run "$R" pfm/internal/a/a.go
expect "a lint-new tool error with no finding line is CHECK FAIL lint-new" 1 "CHECK FAIL lint-new"
reset; rule make lint-new 1 'internal/b/b.go:9:9: elsewhere (lll)'
run "$R" pfm/docs/x.md
out_has "no named go file: lint-new is skipped whatever it would find" "CHECK SKIP lint-new (no named file it applies to)"
reset; rule make arch 1; rule go claudelaunch 1
run "$R" pfm/internal/a/a.go
expect "a failing arch is CHECK FAIL arch" 1 "CHECK FAIL arch"
out_has "a failing launch test is CHECK FAIL launch-literals" "CHECK FAIL launch-literals"
out_has "after arch fails, leak still runs" "CHECK PASS leak"
out_has "a failing check prints STATIC FAIL" "STATIC FAIL"
reset; rule leak-check.sh '--files' 0 'PRIVATE COVERAGE SKIPPED (no private patterns)'
run "$R" pfm/internal/a/a.go
expect "a leak scan without private patterns is named structural-only" 0 "CHECK PASS leak (structural-only)"
reset; rule leak-check.sh '--files' 1 'LEAK pfm/internal/a/a.go:1'
run "$R" pfm/internal/a/a.go
expect "a leak finding is CHECK FAIL leak" 1 "CHECK FAIL leak"
reset; rule rumdl check 1 'pfm/docs/x.md:1:1: [MD041] first line'
run "$R" pfm/docs/x.md
expect "a rumdl finding is CHECK FAIL rumdl" 1 "CHECK FAIL rumdl"

# --- the templates checks -----------------------------------------------------------------------------
reset
run "$R" docs/only.md
if [ "$(checks)" = 'rumdl leak clone placeholders scratch-paths descriptions manifest codex-markers ' ]; then ok "the templates checks run in their order after rumdl and leak"
else bad "the templates check order" "$(checks)"; fi
out_has "the scratch-path sweep has repo_git to list the tracked files" "CHECK PASS scratch-paths"
rec_has "manifest checks the self-hosted manifest of both units" check-self-hosted-manifest.sh "$R" templates pfm
rec_has "codex-markers runs its node check" node scripts/check-codex-markers.mjs
reset; rule placeholders '' 1; rule description-check.sh '' 1
run "$R" docs/only.md
expect "a failing catalogue function is CHECK FAIL placeholders" 1 "CHECK FAIL placeholders"
out_has "a failing description check is CHECK FAIL descriptions" "CHECK FAIL descriptions"
out_has "after a templates check fails, the next still runs" "CHECK PASS manifest"
reset

# --- a tool the fence image lacks: one named FAIL line, no check runs --------------------------------
rm "$BIN/make"
run "$R" pfm/internal/a/a.go
expect "a missing tool is one CHECK FAIL image line, exit 1" 1 "CHECK FAIL image (the fence image lacks: make — infra/fence/pfm-dev.Dockerfile installs every check's tool; no check ran)"
out_has "a missing tool prints VERDICT FAIL" "VERDICT FAIL"
if [ "$(checks)" = 'image ' ]; then ok "a missing tool runs no check"; else bad "a missing tool runs no check" "$(checks)"; fi
out_has "a missing tool still runs and reports the tests" "TEST PASS pfm/internal/a/"
ln -s .stub "$BIN/make"
rm "$BIN/jq"
run "$R" docs/only.md
expect "a missing templates tool names it, exit 1" 1 "CHECK FAIL image (the fence image lacks: jq — infra/fence/pfm-dev.Dockerfile installs every check's tool; no check ran)"
ln -s .stub "$BIN/jq"
rm "$BIN/rumdl"
run "$R" pfm/docs/x.md
expect "rumdl is needed only when a .md file is named" 1 "CHECK FAIL image (the fence image lacks: rumdl — infra/fence/pfm-dev.Dockerfile installs every check's tool; no check ran)"
run "$R" pfm/internal/a/a.go
rc_is "no .md file named: a missing rumdl is no failure" 0
ln -s .stub "$BIN/rumdl"
if grep -q 'tools\.sh' "$SUT"; then bad "test.sh needs no tools.sh step" "$(grep -n 'tools\.sh' "$SUT")"
else ok "test.sh needs no tools.sh step"; fi
reset; rule go 'mod download' 1 'go: example.test/dep@v1.0.0: module lookup disabled by GOPROXY=off'
run "$R" pfm/internal/a/a.go
expect "modules go cannot find offline are a named install FAIL" 1 "CHECK FAIL install (Go modules the fence's module cache lacks — the run's start downloads them; see the log)"
reset; rule go 'mod download' 1 'go: errors parsing go.mod: unknown directive: bogus'
run "$R" pfm/internal/a/a.go
expect "any other offline download failure is CHECK FAIL install, exit 1" 1 "CHECK FAIL install (go mod download failed offline — see the log)"
reset

# --- the host's verdict when the fence dies --------------------------------------------------------------
rule dev.sh 'iso run' 4 'boom'
run "$R" "$A"
expect "the fence dies with docker absent: named, exit 1" 1 "VERDICT FAIL (fence: docker not found)"
docker_on; rule docker info 1
run "$R" "$A"
expect "the fence dies with the daemon down: named, exit 1" 1 "VERDICT FAIL (fence: the docker daemon is not reachable — start Docker, then rerun)"
reset; rule dev.sh 'iso run' 4 'boom'
run "$R" "$A"
expect "the fence dies with the daemon up: the exit code named, exit 1" 1 "VERDICT FAIL (fence: dev.sh iso run exited 4 before a verdict — see the log)"
out_has "a dead fence run prints its log's tail" boom
reset; rule dev.sh 'iso run' 1 'VERDICT PASS'
run "$R" "$A"
expect "a PASS the run's non-zero exit contradicts is a FAIL" 1 "VERDICT FAIL (fence: dev.sh iso run exited 1 after its PASS verdict — see the log)"
if [ "$(tail -n 2 <<<"$OUT" | head -n 1)" = "VERDICT FAIL (fence: dev.sh iso run exited 1 after its PASS verdict — see the log)" ]; then ok "a contradicted PASS ends on the FAIL verdict"
else bad "a contradicted PASS ends on the FAIL verdict" "$OUT"; fi
docker_off; reset

# --- log, red tail, green quiet -----------------------------------------------------------------------
rule go 'test -v' 0 'ran-go\n--- PASS: TestStub (0.00s)'
run "$R" "$A"
LOG="$(sed -n 's/^log: //p' <<<"$OUT")"
if [[ "$LOG" =~ ^/tmp/$NAME/test/[0-9]{8}T[0-9]{6}Z-[0-9]+\.log$ ]]; then ok "the log path is printed under /tmp/{project}/test"; else bad "the log path is printed under /tmp/{project}/test" "$OUT"; fi
if [ "$(tail -n 1 <<<"$OUT")" = "log: $LOG" ] && [ "$(tail -n 2 <<<"$OUT" | head -n 1)" = 'VERDICT PASS' ]; then ok "a green run ends VERDICT PASS, then the log line"
else bad "a green run ends VERDICT PASS, then the log line" "$OUT"; fi
if [ -f "$LOG" ] && grep -qxF '  | ran-go' "$LOG"; then ok "the runner's output is in the log, indented"; else bad "the runner's output is in the log, indented" "$LOG"; fi
out_lacks "a green run keeps the runner's output out of stdout" "  | ran-go"
reset; rule go 'test -v' 0 'TESTS PASS 9\nCHECK FAIL fake\nVERDICT FAIL\n--- PASS: TestStub (0.00s)'
run "$R" "$A"
expect "a runner's own summary-looking lines never become this run's verdict" 0 "VERDICT PASS"
out_lacks "a runner's CHECK line is not this run's" "CHECK FAIL fake"
out_lacks "a runner's TESTS line is not this run's" "TESTS PASS 9"
reset; rule go 'test -v' 1 "$(printf 'L%d\\n' $(seq 1 50))"
run "$R" "$A"
rc_is "a red run exits 1" 1
out_has "a red run prints the log's tail" "TESTS FAIL 1 of 1"
out_has "a red run prints the runner's last line" "  | L50"
out_lacks "a red run prints no more than the log's last 40 lines" "  | L1"
reset

# --- --full -----------------------------------------------------------------------------------------
run "$R" --full
rec_has "--full is the full gate: dev.sh iso gate" dev.sh iso gate
if grep -qE '^dev\.sh PFM_FENCE_NET= WATCH=[0-9]+$' "$REC.env"; then ok "--full watches the caller's pid"; else bad "--full watches the caller's pid" "$(cat "$REC.env")"; fi
rule dev.sh 'iso gate' 7 'stub red'
run "$R" --full
rc_is "--full exits with the gate's code" 7

shtest_end

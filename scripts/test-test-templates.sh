#!/usr/bin/env bash
# Exercise .claude/scripts/test-templates.sh, the only way an agent runs the templates unit's tests,
# against a fake repository: usage, every path spelling, the outside / two-file / deleted / missing
# rules, the routing by file kind (selectors byte for byte, scripts/test-*.py and *_test.sh take none,
# the four tests that take the script under test), the zero-test and skipped-test reds (a node file
# with `# pass 0`, `# skipped N` or `# todo N` or only its own file as a passing test (node 22 on a pattern
# that matches nothing), a unittest run with `Ran 0 tests` or `skipped=N` is
# exit 1 on a runner exit 0, and python 3.12's exit 5 `NO TESTS RAN` is NO TEST RAN, code 1; the first
# non-zero still wins), the EXIT trap's PID guard (a forked shell running it keeps the suite's scratch), directories, ALL, the exit code, the log
# and the closing green lines. dev.sh, node and python3 are stubs that record their arguments and print
# a passing TAP / unittest report unless STUB_PASS, STUB_SKIP, STUB_TODO, STUB_RAN, STUB_PYSKIP or STUB_PYNONE (exit 5, `NO TESTS RAN`) say
# otherwise; `iso run` executes its command string in the fake root.
#   bash scripts/test-test-templates.sh [path to test-templates.sh]
# BROKEN STATE: a missing script under test fails every case (the copy error is printed), never 0 failed.
# shellcheck disable=SC2016 # the bash -c bodies are single-quoted on purpose: they expand in the child
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${1:-$HERE/.claude/scripts/test-templates.sh}"
SHTEST_TAG=test-templates
# shellcheck source=scripts/shtest.sh
source "$HERE/scripts/shtest.sh"

PROJECT="ttroot$$"
R="$T/.$PROJECT"
REC="$T/rec"
trap 'if [ "${BASHPID:-$$}" = "$SHTEST_PID" ]; then rm -rf -- "$T" "/tmp/$PROJECT"; fi' EXIT
USAGE='usage: test-templates.sh ALL | <test file>[::<test id>]...'
GREEN1='A file that failed in a wider run and now passes alone, with no change that explains it, fails alongside others: rerun the selection it failed in.'
GREEN2='Green. When your work is done, run .claude/scripts/check-templates.sh <your task'"'"'s files> once, then write your return.'
ODD='templates/global/commands/it'"'"'s $HOME.test.mjs'

# One stub serves every tool and every test script: it records its name and arguments, prints
# STUB_LINES numbered lines, and exits with the next code of $REC/rc.<name>.<first arg> or rc.<name>.
mkdir -p "$T/bin" "$R/.claude/scripts"
cat > "$T/stub.sh" <<'STUB'
#!/usr/bin/env bash
name="${0##*/}"
{ printf '%s' "$name"; [ "$#" = 0 ] || printf ' %q' "$@"; printf '\n'; } >> "$STUB_REC/calls"
case "$name" in
  node)
    [ -z "${STUB_FILEONLY:-}" ] || printf 'TAP version 13\n1..0\n# Subtest: %s\nok 1 - %s\n1..1\n' "${!#}" "${!#}"
    printf '# tests 1\n# pass %s\n# fail 0\n# skipped %s\n# todo %s\n' "${STUB_PASS-1}" "${STUB_SKIP-0}" "${STUB_TODO-0}" ;;
  python3)
    if [ -n "${STUB_PYNONE:-}" ]; then printf '\n----------\nRan 0 tests in 0.000s\n\nNO TESTS RAN\n'; exit 5; fi
    printf '.\n----------\nRan %s tests in 0.001s\n\nOK%s\n' "${STUB_RAN-1}" "${STUB_PYSKIP:+ (skipped=$STUB_PYSKIP)}" ;;
esac
[ -z "${STUB_LINES:-}" ] || seq -f 'line %g' 1 "$STUB_LINES"
for key in "$name.${1##*/}" "$name"; do
  f="$STUB_REC/rc.$key"
  if [ -s "$f" ]; then rc="$(head -n1 "$f")"; sed -i 1d "$f"; exit "$rc"; fi
done
exit 0
STUB
cat > "$R/.claude/scripts/dev.sh" <<'STUB'
#!/usr/bin/env bash
{ printf 'dev'; printf ' %q' "$@"; printf '\n'; } >> "$STUB_REC/dev.argv"
echo "DEVOUT $1 $2"
if [ "$1 $2" = "iso run" ]; then cd "$(dirname "$0")/../.." && bash -c "$3"; exit $?; fi
exit "${DEV_RC:-0}"
STUB
chmod +x "$T/stub.sh" "$R/.claude/scripts/dev.sh"
cp "$T/stub.sh" "$T/bin/node"; cp "$T/stub.sh" "$T/bin/python3"
cp "$HERE/.claude/scripts/unit-path.sh" "$R/.claude/scripts/unit-path.sh"
cp "$SUT" "$R/.claude/scripts/test-templates.sh" || echo "test-test-templates: cannot copy $SUT" >&2
for f in scripts/test-pfm-guard.sh scripts/test-codex-sync.sh scripts/test-format-md.sh scripts/test-dev-report.sh \
  scripts/test-plain.sh scripts/test-x.py infra/fence/lanes/tests/y_test.sh; do
  mkdir -p "$R/$(dirname "$f")"; cp "$T/stub.sh" "$R/$f"
done
for f in templates/global/commands/x.test.mjs templates/global/commands/gone.test.mjs "$ODD" \
  templates/global/skills/s/s_test.py docs/a.md scripts/a.sh pfm/internal/a/a.go docs/two/a.test.mjs two/a.test.mjs; do
  mkdir -p "$R/$(dirname "$f")"; echo x > "$R/$f"
done
git -C "$R" init -q && git -C "$R" add -A && git -C "$R" -c user.name=t -c user.email=t@t commit -qm base
rm "$R/templates/global/commands/gone.test.mjs"
R="$(cd "$R" && pwd -P)"

reset() { rm -rf "$REC"; mkdir -p "$REC"; }
rcseq() { local key="$1"; shift; printf '%s\n' "$@" > "$REC/rc.$key"; }
go() { OUT="$(cd "${CWD:-$R}" && PATH="$T/bin:$PATH" STUB_REC="$REC" bash "$R/.claude/scripts/test-templates.sh" "$@" 2>&1)"; RC=$?; }
want() { printf '%s' "$1"; shift; [ "$#" = 0 ] || printf ' %q' "$@"; }
chk() { local n="$1"; shift; if "$@"; then ok "$n"; else bad "$n" "rc=$RC" "$OUT"; fi; }
# The EXIT trap keeps shtest.sh's PID guard: run by a forked shell it must leave the suite's scratch alone.
trap -p EXIT > "$T/exit-trap"
TRAPF="$T/exit-trap"; mkdir -p "$T/guard-probe"
T="$T/guard-probe" PROJECT="guardprobe$$" SHTEST_PID="$SHTEST_PID" bash -c 'eval "$(cat "$1")"; exit 0' _ "$TRAPF"
if [ -d "$T/guard-probe" ]; then ok "trap: a forked shell running the EXIT trap leaves the scratch alone"
else bad "trap: a forked shell running the EXIT trap removed the scratch"; fi
no_runner() { [ ! -e "$REC/dev.argv" ]; }
refused() { [ "$RC" = 2 ] && grep -qxF -- "$1" <<<"$OUT" && no_runner; }
ran() { [ "$RC" = "$1" ] && grep -qxF -- "$2" "$REC/calls"; }
green_tail() { [ "$(tail -n2 <<<"$OUT")" = "$GREEN1"$'\n'"$GREEN2" ]; }
logfile() { sed -n 's/^log: //p' <<<"$OUT"; }

for a in '' -x --all 'scripts/test-*.sh' 'x.test.m?s' '[a].test.mjs'; do
  reset; go "$a"; chk "usage: argument '$a' prints the usage line, exit 2, no runner" refused "$USAGE"
done
reset; go; chk "usage: no argument prints the usage line, exit 2, no runner" refused "$USAGE"
reset; go ALL scripts/test-plain.sh; chk "usage: ALL beside another argument" refused "$USAGE"
reset; go scripts/test-plain.sh ALL; chk "usage: ALL after a file" refused "$USAGE"

X=templates/global/commands/x.test.mjs
NODE="$(want node --test --test-reporter=tap "$X")"
reset; go "$X"; chk "path: repository-relative" ran 0 "$NODE"
reset; CWD="$R/templates/global/commands" go x.test.mjs; chk "path: cwd-relative resolves to the repository path" ran 0 "$NODE"
reset; CWD="$T" go "$R/$X"; chk "path: absolute" ran 0 "$NODE"
reset; go ./templates/./global/commands/x.test.mjs; chk "path: ./ segments collapse" ran 0 "$NODE"
reset; go "$ODD"; chk "path: a name with a space, quote and dollar reaches the runner as one word" ran 0 "$(want node --test --test-reporter=tap "$ODD")"

reset; go pfm/internal/a/a.go
chk "outside: a pfm/ file exits 2 naming it, no runner" refused "test-templates.sh: pfm/internal/a/a.go is outside templates"
reset; CWD="$R/docs" go two/a.test.mjs
chk "two existing files exit 2 naming both" refused "test-templates.sh: two/a.test.mjs resolves to two files: docs/two/a.test.mjs and two/a.test.mjs"
reset; go templates/global/commands/gone.test.mjs "$X"
chk "deleted: a file deleted at HEAD drops silently beside a live one" ran 0 "$NODE"
reset; go templates/global/commands/gone.test.mjs
chk "deleted: a selection dropped to empty exits 2 naming it, no runner" refused "test-templates.sh: no file left to run — deleted at HEAD: templates/global/commands/gone.test.mjs"
reset; go nope.test.mjs
chk "no such file exits 2 naming it, no runner" refused "test-templates.sh: nope.test.mjs is no such file in templates"
reset; go docs/a.md
chk "a file no runner takes exits 2 naming it" refused "test-templates.sh: docs/a.md is not a test file"
reset; go scripts/a.sh
chk "a script that is not a test exits 2 naming it" refused "test-templates.sh: scripts/a.sh is not a test file"

reset; go "$X::a b|c\$*"
chk "route: node selector passes byte for byte as --test-name-pattern" ran 0 "$(want node --test --test-reporter=tap '--test-name-pattern=a b|c$*' "$X")"
S=templates/global/skills/s/s_test.py
reset; go "$S"; chk "route: *_test.py runs python3 -m unittest" ran 0 "$(want python3 -m unittest "$S")"
reset; go "$S::TestA.test_b"; chk "route: *_test.py selector becomes -k, byte for byte" ran 0 "$(want python3 -m unittest "$S" -k TestA.test_b)"
reset; go scripts/test-x.py; chk "route: scripts/test-*.py runs python3 <file>" ran 0 "$(want python3 scripts/test-x.py)"
reset; go 'scripts/test-x.py::case one'
chk "route: scripts/test-*.py takes no selector, exit 2, no runner" refused "test-templates.sh: scripts/test-x.py::case one has a selector; scripts/test-*.py takes no selector"
reset; go docs
chk "path: a directory exits 2 naming it, no runner" refused "test-templates.sh: docs is not a file"
reset; go infra/fence/lanes/tests/y_test.sh; chk "route: *_test.sh runs bash <file>" ran 0 "$(want y_test.sh)"
reset; go scripts/test-plain.sh; chk "route: scripts/test-*.sh runs bash <file>" ran 0 "$(want test-plain.sh)"
reset; go scripts/test-plain.sh::x; chk "route: a shell test has no selector, exit 2, no runner" refused "test-templates.sh: scripts/test-plain.sh::x has a selector; shell tests have none"
reset; go "$X::"; chk "route: an empty selector is usage" refused "$USAGE"

# A runner that exits 0 without proving a test is red: the line names the file and the selector, the
# exit is 1 (the first non-zero code still wins), the line is in the log and on the screen, no green line.
msg_in() { grep -qxF -- "$1" <<<"$OUT" && grep -qxF -- "$1" "$(logfile)"; }
no_green() { ! grep -qF 'Green.' <<<"$OUT" && ! grep -qF 'A file that failed' <<<"$OUT"; }
red_with() { [ "$RC" = "$1" ] && msg_in "$2" && no_green; }
reset; STUB_PASS=0 go "$X::no such test"
chk "zero: node '# pass 0' on exit 0 is red 1, NO TEST RAN names file and ::id" red_with 1 "test-templates.sh: NO TEST RAN in $X::no such test"
reset; STUB_PASS=0 go "$X"
chk "zero: node '# pass 0' without a selector names the file only" red_with 1 "test-templates.sh: NO TEST RAN in $X"
reset; STUB_FILEONLY=1 go "$X::no such test"
chk "zero: node 22 passes the FILE itself when the pattern matches nothing ('ok 1 - {file}', '# pass 1'): red 1, NO TEST RAN" red_with 1 "test-templates.sh: NO TEST RAN in $X::no such test"
reset; STUB_SKIP=2 go "$X"
chk "zero: node '# skipped 2' on exit 0 is red 1, SKIPPED" red_with 1 "test-templates.sh: SKIPPED in $X"
reset; STUB_TODO=1 go "$X::a"
chk "zero: node '# todo 1' on exit 0 is red 1, SKIPPED" red_with 1 "test-templates.sh: SKIPPED in $X::a"
reset; STUB_RAN=0 go "$S::TestA.nope"
chk "zero: unittest 'Ran 0 tests' on exit 0 is red 1, NO TEST RAN names file and ::id" red_with 1 "test-templates.sh: NO TEST RAN in $S::TestA.nope"
reset; STUB_PYSKIP=1 go "$S"
chk "zero: unittest 'skipped=1' on exit 0 is red 1, SKIPPED" red_with 1 "test-templates.sh: SKIPPED in $S"
reset; STUB_RAN=0 go scripts/test-x.py
chk "zero: scripts/test-*.py with 'Ran 0 tests' is red 1, NO TEST RAN" red_with 1 "test-templates.sh: NO TEST RAN in scripts/test-x.py"
reset; rcseq node 3; STUB_RAN=0 go "$X" "$S"
chk "zero: the first non-zero code (3) wins over a later zero-test red, which is still named" bash -c '[ "$1" = 3 ] && grep -qxF "test-templates.sh: NO TEST RAN in $2" "$3"' "$REC" "$RC" "$S" "$(logfile)"
reset; STUB_PYNONE=1 go "$S::zzNoSuch"
chk "zero: python 3.12 exit 5 'NO TESTS RAN' is red 1, NO TEST RAN names file and ::id" red_with 1 "test-templates.sh: NO TEST RAN in $S::zzNoSuch"
reset; STUB_PYNONE=1 go "$S"
chk "zero: python exit 5 'NO TESTS RAN' without a selector names the file only" red_with 1 "test-templates.sh: NO TEST RAN in $S"
reset; rcseq node 3; STUB_PYNONE=1 go "$X" "$S"
chk "zero: the first non-zero code (3) wins over a later exit 5 NO TESTS RAN, which is still named" bash -c '[ "$1" = 3 ] && grep -qxF "test-templates.sh: NO TEST RAN in $2" "$3"' "$REC" "$RC" "$S" "$(logfile)"
reset; STUB_PASS=0 rcseq python3 5; STUB_PASS=0 go "$X" "$S"
chk "zero: a zero-test red (1) wins over a later runner failure (5), which still ran" bash -c '[ "$1" = 1 ] && [ "$(wc -l < "$0/calls")" = 2 ]' "$REC" "$RC"
reset; STUB_PASS=3 go "$X" "$S"
chk "zero: passing tests with nothing skipped stay green" bash -c '[ "$1" = 0 ]' "$REC" "$RC"

# infra/fence/checks.sh passes these four their scripts under test (checks_templates_codex_sync,
# _pfm_guard, _format_md, _dev_report): one run per path, in this order.
sut_case() { # sut_case <test file> <script under test>...
  local f="$1"; shift; local s calls=()
  reset; go "$f"
  for s in "$@"; do calls+=("$(want "${f##*/}" "$R/$s")"); done
  chk "route: $f runs once per script under test: $*" bash -c '[ "$1" = 0 ] && shift && diff <(printf "%s\n" "$@") <(grep -v "^dev" "$0") >/dev/null' "$REC/calls" "$RC" "${calls[@]}"
}
sut_case scripts/test-codex-sync.sh templates/project/scripts/codex-sync.sh .claude/scripts/codex-sync.sh
sut_case scripts/test-pfm-guard.sh templates/project/scripts/pfm-guard.sh .claude/scripts/pfm-guard.sh
sut_case scripts/test-format-md.sh templates/project/scripts/format-md.sh .claude/scripts/format-md.sh
sut_case scripts/test-dev-report.sh .claude/scripts/dev.sh

reset; go "$X" scripts/test-plain.sh scripts/test-x.py
chk "one container: every invocation of a selection runs inside ONE iso run" bash -c '[ "$(wc -l < "$0/dev.argv")" = 1 ] && grep -q "^dev iso run " "$0/dev.argv" && [ "$(wc -l < "$0/calls")" = 3 ]' "$REC"
reset; go "$X" scripts/test-plain.sh
chk "order: invocations run in argument order" bash -c 'diff <(cut -d" " -f1 "$0/calls") <(printf "node\ntest-plain.sh\n")' "$REC"

reset; rcseq test-codex-sync.sh 3 5; go scripts/test-codex-sync.sh
chk "exit: the first non-zero invocation code wins and the later one still ran" bash -c '[ "$1" = 3 ] && [ "$(wc -l < "$0/calls")" = 2 ]' "$REC" "$RC"
reset; rcseq test-codex-sync.sh 0 5; go scripts/test-codex-sync.sh
chk "exit: a later non-zero code is the exit when the first passed" [ "$RC" = 5 ]
reset; go scripts/test-codex-sync.sh
chk "exit: all green is 0" [ "$RC" = 0 ]

reset; go ALL
chk "ALL: the rerun reminder is the first line" bash -c '[ "$(head -n1 <<<"$0")" = "After a fix, run only the files that failed last round." ]' "$OUT"
chk "ALL: runs dev.sh iso test templates, one call, no test runner of its own" bash -c '[ "$(cat "$0/dev.argv")" = "dev iso test templates" ] && [ ! -e "$0/calls" ]' "$REC"
reset; DEV_RC=7 go ALL; chk "ALL: the exit is the fence's code" [ "$RC" = 7 ]

reset; rcseq node 1; export STUB_LINES=50; go "$X"; unset STUB_LINES
LOGF="$(logfile)"
chk "log: the path is printed as 'log: /tmp/{project}/test-templates/{UTC}-{pid}.log'" bash -c '[[ "$0" =~ ^/tmp/'"$PROJECT"'/test-templates/[0-9]{8}T[0-9]{6}Z-[0-9]+\.log$ ]]' "$LOGF"
chk "log: the runner output is in the file, never on the screen but its last 40 lines on a red" bash -c 'grep -qx "line 1" "$0" && ! grep -qx "line 10" <<<"$1" && grep -qx "line 11" <<<"$1" && grep -qx "line 50" <<<"$1"' "$LOGF" "$OUT"
reset; go "$X"
chk "log: a green run prints the path and none of the runner's output" bash -c '[ -n "$(sed -n "s/^log: //p" <<<"$0")" ] && ! grep -q DEVOUT <<<"$0"' "$OUT"
reset; go ALL
chk "log: ALL output goes to the log, not the screen" bash -c '! grep -q DEVOUT <<<"$0" && grep -q "log: " <<<"$0" && grep -q DEVOUT "$(sed -n "s/^log: //p" <<<"$0")"' "$OUT"

reset; go "$X"
chk "green: a green file selection prints both lines, the last two" green_tail
reset; rcseq node 1; go "$X"
chk "green: a red selection prints neither line" bash -c '! grep -qF "Green." <<<"$0" && ! grep -qF "A file that failed" <<<"$0"' "$OUT"
reset; go ALL
chk "green: ALL never prints the green lines" bash -c '! grep -qF "Green." <<<"$0" && ! grep -qF "A file that failed" <<<"$0"' "$OUT"

shtest_end

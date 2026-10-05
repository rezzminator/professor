#!/usr/bin/env bash
# Exercise .claude/scripts/unit-path.sh, the one path resolver of the test-{unit}.sh and
# check-{unit}.sh scripts, against a fake repository: every path spelling, the outside rule,
# two-file ambiguity, deletions at HEAD, missing files, flags and the log path.
#   bash scripts/test-unit-path.sh [path to unit-path.sh]
# BROKEN STATE: a missing resolver fails every case (its source error is printed), never 0 failed.
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${1:-$HERE/.claude/scripts/unit-path.sh}"
SHTEST_TAG=unit-path
# shellcheck source=scripts/shtest.sh
source "$HERE/scripts/shtest.sh"

R="$T/.ulroot$$"
PROJECT="ulroot$$"
trap 'if [ "${BASHPID:-$$}" = "$SHTEST_PID" ]; then rm -rf -- "$T" "/tmp/$PROJECT"; fi' EXIT
mkdir -p "$R/pfm/internal/a" "$R/pfm/gonedir" "$R/pfm/docs" "$R/docs" "$R/pfmx"
for f in pfm/gonedir/f.go pfm/internal/a/a.go pfm/internal/a/a_test.go pfm/internal/a/gone_test.go pfm/docs/x.md docs/x.md docs/y.md pfmx/y.md; do
  echo x > "$R/$f"
done
git -C "$R" init -q && git -C "$R" add -A && git -C "$R" -c user.name=t -c user.email=t@t commit -qm base
rm -r "$R/pfm/internal/a/gone_test.go" "$R/docs/y.md" "$R/pfm/gonedir"
R="$(cd "$R" && pwd -P)"

# resolve <cwd> <mode> <unit> <arg>...: OUT holds stdout+stderr, RC the exit code.
resolve() {
  local cwd="$1"; shift
  OUT="$(cd "$cwd" && ROOT="$R" bash -c 'usage() { echo "usage: x" >&2; exit 2; }
    source "$0" || exit 99; m=$1 u=$2; shift 2; unit_paths "$m" "$u" "$@"
    for p in "${UNIT_PATHS[@]}"; do echo "PATH $p"; done; echo "RC $UNIT_PATHS_RC"' "$SUT" "$@" 2>&1)"
  RC=$?
}
expect() { # expect <name> <rc> <line that must be in OUT>
  if [ "$RC" = "$2" ] && grep -qxF -- "$3" <<<"$OUT"; then ok "$1"; else bad "$1" "rc=$RC want $2, want line: $3" "$OUT"; fi
}

resolve "$R" test pfm pfm/internal/a/a_test.go
expect "repository-relative path resolves" 0 "PATH pfm/internal/a/a_test.go"
resolve "$R" test pfm internal/a/a_test.go
expect "unit-relative path resolves" 0 "PATH pfm/internal/a/a_test.go"
resolve "$R/pfm/internal" test pfm a/a_test.go
expect "cwd-relative path resolves" 0 "PATH pfm/internal/a/a_test.go"
resolve "$T" test pfm "$R/pfm/internal/a/a_test.go"
expect "absolute path resolves" 0 "PATH pfm/internal/a/a_test.go"
resolve "$R" test pfm ./pfm/./internal/a/a_test.go
expect "./ segments collapse" 0 "PATH pfm/internal/a/a_test.go"
resolve "$R" test templates docs/x.md
expect "a templates path resolves repository-relative" 0 "PATH docs/x.md"

resolve "$R" test pfm 'internal/a/a_test.go::TestA/sub case|x$'
expect "test mode re-appends the selector byte for byte" 0 'PATH pfm/internal/a/a_test.go::TestA/sub case|x$'
resolve "$R" check pfm 'internal/a/a_test.go::TestA'
expect "check mode keeps the file without its selector" 0 "PATH pfm/internal/a/a_test.go"

resolve "$R" test pfm pfmx/y.md
expect "the full path decides outside: pfmx/ is not pfm/" 2 "unit-path.sh: pfmx/y.md is outside pfm"
resolve "$R" check templates pfm/internal/a/a.go
expect "a pfm file is outside templates" 2 "unit-path.sh: pfm/internal/a/a.go is outside templates"
resolve "$R" test pfm /etc/hostname
expect "an absolute path outside the repository is outside" 2 "unit-path.sh: /etc/hostname is outside pfm"
resolve "$R" test pfm docs/x.md
expect "two existing files exit 2 naming both" 2 "unit-path.sh: docs/x.md resolves to two files: docs/x.md and pfm/docs/x.md"

resolve "$R" test pfm pfm/internal/a/gone_test.go pfm/internal/a/a_test.go
expect "a file deleted at HEAD drops silently beside a live one" 0 "PATH pfm/internal/a/a_test.go"
resolve "$R" test pfm pfm/internal/a/gone_test.go
expect "a selection dropped to empty exits 2 naming the drops" 2 "unit-path.sh: no file left to run — deleted at HEAD: pfm/internal/a/gone_test.go"
resolve "$R" check templates docs/y.md
expect "check mode drops a deletion with no FAIL line" 0 "RC 0"
resolve "$R" test pfm pfm/internal/a/nope_test.go
expect "test mode: no such file exits 2 naming it" 2 "unit-path.sh: pfm/internal/a/nope_test.go is no such file in pfm"
resolve "$R" check pfm pfm/internal/a/nope.go pfm/internal/a/a.go
expect "check mode: no such file prints FAIL path" 0 "FAIL path pfm/internal/a/nope.go (no such file in pfm)"
expect "check mode: a FAIL path line sets RC 1 and keeps the live file" 0 "RC 1"
resolve "$R" test pfm pfm/gonedir
expect "test mode: a directory deleted at HEAD is no deletion of a file" 2 "unit-path.sh: pfm/gonedir is no such file in pfm"
resolve "$R" check pfm pfm/gonedir
expect "check mode: a directory deleted at HEAD prints FAIL path" 0 "FAIL path pfm/gonedir (no such file in pfm)"
resolve "$R" check pfm pfm/internal
expect "check mode: a directory is not a file" 0 "FAIL path pfm/internal (not a file)"

for a in '' -x --all; do
  resolve "$R" test pfm "$a"
  expect "argument '$a' prints the usage, exit 2" 2 "usage: x"
done
resolve "$R" test pfm
expect "no argument prints the usage, exit 2" 2 "usage: x"

git -C "$R" worktree add -q "$T/wt" 2>/dev/null
LOG_OUT="$(cd "$T/wt" && ROOT="$(pwd -P)" bash -c 'source "$0" || exit 99; unit_log check-pfm && echo "$UNIT_LOG"' "$SUT" 2>&1)"
if [[ "$LOG_OUT" =~ ^/tmp/$PROJECT/check-pfm/[0-9]{8}T[0-9]{6}Z-[0-9]+\.log$ ]] && [ -d "/tmp/$PROJECT/check-pfm" ]; then
  ok "unit_log names the main checkout's directory minus its dot, from a linked worktree"
else
  bad "unit_log names the main checkout's directory minus its dot, from a linked worktree" "$LOG_OUT"
fi

shtest_end

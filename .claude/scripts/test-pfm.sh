#!/usr/bin/env bash
# test-pfm.sh — the only way an agent runs pfm's tests. Every test runs in the fence (dev.sh iso).
#   test-pfm.sh ALL | <test file>[::<go -run regexp>]...
# ALL runs the whole suite (`dev.sh iso test pfm`). A file is a pfm Go test file (pfm/**/*_test.go,
# `go test` of its package; pfm/e2e/ with the e2e tag; -run is the ::selector byte for byte, else
# every Test and Fuzz func of the file — go test runs a Fuzz func's seed corpus under -run — TestMain
# excluded; an empty selector, `file::`, is the usage line) or a pfm/scripts/*_test.sh (run with bash, no
# selector). Every path goes through the one resolver, unit-path.sh. All files of one call run in one
# fence container, in order; the exit code is the first non-zero one. Every Go invocation runs with -v
# and its output is captured and appended to the run output: an invocation that exits 0 yet shows no
# `--- PASS` line, or a `--- SKIP` line for a test pfm/scripts/known-skips.tsv does not list for that
# package (the gate's own allow-list: package + test, a subtest by its parent's row), or `[no tests to
# run]` / `[no test files]`, is red — the line `test-pfm.sh: NO TEST RAN in {file}[::{selector}]` or
# `test-pfm.sh: UNLISTED SKIP {Test} in {file}[::{selector}]` names it and the exit code is 1 for it
# (the first non-zero one still wins); the green lines never follow. A listed skip is fine: the file
# can read Green. A shell test is judged by its exit code alone. Runner output goes to /tmp/{project}/test-pfm/{UTC timestamp}-{pid}.log;
# a red run also prints that log's last 40 lines.
# BROKEN STATE: a missing unit-path.sh or dev.sh errors on source/exec; a usage, path or routing
# problem exits 2 with a named line (an emptied selection names every dropped file: deleted at HEAD
# and no-Test files); a runner failure exits with the runner's code and the log tail; a run that ran
# no test or skipped an unlisted one is red, never green; an unreadable known-skips.tsv makes every
# skip unlisted and says so.
set -euo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
usage() { echo 'usage: test-pfm.sh ALL | <test file>[::<go -run regexp>]...' >&2; exit 2; }
# shellcheck source=.claude/scripts/unit-path.sh
source "$ROOT/.claude/scripts/unit-path.sh"
DEV="$ROOT/.claude/scripts/dev.sh"

[ "$#" -gt 0 ] || usage
all=0
if [ "$#" = 1 ] && [ "$1" = ALL ]; then all=1; fi
if [ "$all" = 0 ]; then
  for arg in "$@"; do
    file="${arg%%::*}"
    case "$arg" in ALL) usage ;; esac
    case "$file" in *[\*\?\[]*) usage ;; esac
    if [[ "$arg" == *::* && -z "${arg#*::}" ]]; then usage; fi
  done
  unit_paths test pfm "$@"

  invs=(); dropped=(); deleted=()
  # The import path the skip allow-list names a package by: pfm's module path plus the package dir.
  pfm_module="$(sed -n 's/^module[[:space:]][[:space:]]*//p' "$ROOT/pfm/go.mod" | head -1)"
  [ -n "$pfm_module" ] || { echo "test-pfm.sh: pfm/go.mod names no module" >&2; exit 2; }
  for p in "${UNIT_PATHS[@]}"; do
    file="${p%%::*}"; sel=; has_sel=0
    if [[ "$p" == *::* ]]; then has_sel=1; sel="${p#*::}"; fi
    case "$file" in
      pfm/scripts/*_test.sh)
        if [ "$has_sel" = 1 ]; then echo "test-pfm.sh: $p: a shell test takes no selector" >&2; exit 2; fi
        invs+=("$(printf '%q ' pfm_sh bash "$file")")
        continue ;;
      pfm/e2e/*_test.go) tags=(-tags e2e) ;;
      pfm/*_test.go) tags=() ;;
      *) echo "test-pfm.sh: $p is not a test file" >&2; exit 2 ;;
    esac
    if [ "$has_sel" = 0 ]; then
      names=()
      while IFS= read -r fn; do
        [ "$fn" != TestMain ] || continue
        names+=("$fn")
      done < <(grep -oE '^func ((Test|Fuzz)[A-Za-z0-9_]*)\(' "$ROOT/$file" | sed 's/^func //; s/($//' || true)
      if [ "${#names[@]}" = 0 ]; then dropped+=("$file"); continue; fi
      sel='^('
      for fn in "${names[@]}"; do sel+="$fn|"; done
      sel="${sel%|})\$"
    fi
    dir="${file%/*}"; dir="${dir#pfm}"; dir="${dir#/}"
    pkg="./"; [ -z "$dir" ] || pkg="./$dir/"
    pkgpath="$pfm_module"; [ -z "$dir" ] || pkgpath="$pfm_module/$dir"
    invs+=("$(printf '%q ' pfm_go "$p" "$pkgpath" go -C pfm test -v "${tags[@]}" -count=1 "$pkg" -run "$sel")")
  done
  if [ "${#invs[@]}" = 0 ]; then
    # The resolver drops a deletion silently and names it only when it alone empties the selection;
    # a selection emptied by deletions plus no-Test files is named here, deletions first.
    if [ "${#UNIT_PATHS[@]}" -lt "$#" ]; then
      for arg in "$@"; do
        probe="$( (unit_paths test pfm "$arg") 2>&1 >/dev/null || true)"
        probe="$(grep -F 'deleted at HEAD: ' <<<"$probe" | head -1 || true)"
        if [ -n "$probe" ]; then deleted+=("${probe##*deleted at HEAD: }"); fi
      done
    fi
    parts=()
    if [ "${#deleted[@]}" -gt 0 ]; then parts+=("deleted at HEAD: ${deleted[*]}"); fi
    parts+=("no Test func: ${dropped[*]}")
    msg="${parts[0]}"
    for part in "${parts[@]:1}"; do msg+="; $part"; done
    echo "test-pfm.sh: no file left to run — $msg" >&2; usage
  fi
  if [ "${#dropped[@]}" -gt 0 ]; then echo "test-pfm.sh: no Test func, dropped: ${dropped[*]}" >&2; fi
  # The fence's command string: pfm_go runs one go test -v, appends its output, and turns a zero-test
  # or unlisted-skip run that exited 0 into a red; pfm_sh runs a shell test, exit code alone.
  # shellcheck disable=SC2016 # everything here expands in the fence's shell, not here
  cmd='pfm_rc=0
pfm_note() { [ "$pfm_rc" -ne 0 ] || pfm_rc=$1; }
pfm_sh() { local c=0; "$@" || c=$?; pfm_note "$c"; }
pfm_go() {
  local label=$1 pkg=$2 out c=0 t cls skips=0 unlisted=0; shift 2
  out="$("$@" 2>&1)" || c=$?
  printf "%s\n" "$out"
  if [ "$c" = 0 ]; then
    while IFS= read -r t; do
      [ -n "$t" ] || continue
      skips=1
      cls="$(awk -F"\t" -v p="$pkg" -v t="$t" -v parent="${t%%/*}" "\$1 == p && (\$2 == t || \$2 == parent) { print \$3; exit }" pfm/scripts/known-skips.tsv)"
      if [ -z "$cls" ]; then echo "test-pfm.sh: UNLISTED SKIP $t in $label"; unlisted=1; fi
    done < <(sed -n "s/^[[:space:]]*--- SKIP: \([^ ]*\).*/\1/p" <<<"$out")
    if [ "$skips" = 1 ] && [ ! -r pfm/scripts/known-skips.tsv ]; then
      echo "test-pfm.sh: pfm/scripts/known-skips.tsv unreadable — every skip counts as unlisted"
    fi
    if [ "$unlisted" = 1 ]; then c=1
    elif grep -qE "\[no tests to run\]|\[no test files\]" <<<"$out" || ! grep -qE "^[[:space:]]*--- PASS" <<<"$out"; then
      echo "test-pfm.sh: NO TEST RAN in $label"; c=1
    fi
  fi
  pfm_note "$c"
}
'
  for inv in "${invs[@]}"; do cmd+="$inv;"$'\n'; done
  # shellcheck disable=SC2016 # same: the fence's shell expands it
  cmd+='exit "$pfm_rc"'
else
  echo "After a fix, run only the files that failed last round."
fi

unit_log test-pfm || { echo "test-pfm.sh: the log directory could not be created" >&2; exit 1; }
rc=0
if [ "$all" = 1 ]; then
  "$DEV" iso test pfm >"$UNIT_LOG" 2>&1 || rc=$?
else
  "$DEV" iso run "$cmd" >"$UNIT_LOG" 2>&1 || rc=$?
fi
echo "log: $UNIT_LOG"
if [ "$rc" != 0 ]; then tail -n 40 "$UNIT_LOG"; exit "$rc"; fi
if [ "$all" = 0 ]; then
  echo 'A file that failed in a wider run and now passes alone, with no change that explains it, fails alongside others: rerun the selection it failed in.'
  echo "Green. When your work is done, run .claude/scripts/check-pfm.sh <your task's files> once, then write your return."
fi

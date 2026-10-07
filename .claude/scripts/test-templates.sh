#!/usr/bin/env bash
# test-templates.sh — the only way an agent runs the templates unit's tests (every path outside pfm/).
#   .claude/scripts/test-templates.sh ALL | <test file>[::<test id>]...
# Test files only: *.test.mjs (node --test; ::id is --test-name-pattern), *_test.py (unittest; ::id is -k),
# scripts/test-*.py (python3; no selector, a ::id exits 2), *_test.sh and scripts/test-*.sh (bash; no
# selector). Paths go through .claude/scripts/unit-path.sh. Every test runs inside the fence: ONE
# `dev.sh iso run` container per invocation, the commands run in order, the exit is the first non-zero
# command's code. A runner that exits 0 without proving a test is red: a node file whose TAP shows
# `# pass 0`, or only the file itself as the passing test (`ok 1 - {file}`: node 22 on a pattern that
# matches nothing), or `# skipped N` / `# todo N` (N > 0), a unittest run that shows `Ran 0 tests` or `skipped=N`,
# or exits 5 with `NO TESTS RAN` (python 3.12 on a -k that matches nothing), print `test-templates.sh: NO TEST RAN in {file}{::id}` or `test-templates.sh: SKIPPED in {file}{::id}`
# and count as code 1 (the first non-zero code still wins; the rule of dev.sh node_test_suite and infra/fence/checks.sh). ALL is
# `dev.sh iso test templates`. Output goes to /tmp/{project}/test-templates/{UTC}-{pid}.log (the path is
# printed; a red also prints the log's last 40 lines).
# BROKEN STATE: a missing unit-path.sh errors on source; a bad argument, a path outside the unit, two files
# for one name, a path that is no file (a directory included), a selector on a test that takes none, a
# deletion-only selection or a non-test file exits 2 naming it with the runner never called; a dev.sh
# that cannot run exits with its code and the reason in the log; a test that ran nothing or skipped is
# exit 1 naming NO TEST RAN or SKIPPED (a unittest exit 5 `NO TESTS RAN` included), never a green line.
set -euo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
# shellcheck source=.claude/scripts/unit-path.sh
source "$ROOT/.claude/scripts/unit-path.sh"

usage() { echo 'usage: test-templates.sh ALL | <test file>[::<test id>]...' >&2; exit 2; }

# Four tests take the script under test as $1; infra/fence/checks.sh runs each once per path
# (checks_templates_codex_sync, _pfm_guard, _format_md, _dev_report). One row per test file.
sut_rows() { # sut_rows <test file>: sets SUTS, returns 1 for any other file
  case "$1" in
    scripts/test-codex-sync.sh) SUTS=(templates/project/scripts/codex-sync.sh .claude/scripts/codex-sync.sh) ;;
    scripts/test-pfm-guard.sh) SUTS=(templates/project/scripts/pfm-guard.sh .claude/scripts/pfm-guard.sh) ;;
    scripts/test-format-md.sh) SUTS=(templates/project/scripts/format-md.sh .claude/scripts/format-md.sh) ;;
    scripts/test-dev-report.sh) SUTS=(.claude/scripts/dev.sh) ;;
    *) SUTS=(); return 1 ;;
  esac
}

run_logged() { # run_logged <dev.sh args>...: the runner's output goes to the log, never a live pipe
  unit_log test-templates || { echo "test-templates.sh: the log directory could not be created" >&2; exit 1; }
  local rc=0
  "$ROOT/.claude/scripts/dev.sh" "$@" > "$UNIT_LOG" 2>&1 || rc=$?
  echo "log: $UNIT_LOG"
  if [ "$rc" != 0 ]; then tail -n 40 "$UNIT_LOG"; fi
  return "$rc"
}

[ "$#" -gt 0 ] || usage
if [ "$1" = ALL ]; then
  [ "$#" = 1 ] || usage
  echo 'After a fix, run only the files that failed last round.'
  rc=0; run_logged iso test templates || rc=$?
  exit "$rc"
fi

for arg in "$@"; do
  [ "$arg" != ALL ] || usage
  case "${arg%%::*}" in *[\*\?\[]*) usage ;; esac
done
unit_paths test templates "$@"

# Runs inside the fence, once before the invocations: `proof <label> <tap|unittest> <command>...` runs one
# test command with its output captured and printed (the log has all of it), keeps the first non-zero
# code in rc, and turns an exit 0 that proved nothing into code 1 with a named line.
PRELUDE="$(cat <<'PRELUDE_END'
rc=0
first() { [ "$rc" -ne 0 ] || rc=$1; }
proof() {
  local label=$1 kind=$2 out c; shift 2
  out=$("$@" 2>&1) && c=0 || c=$?
  printf '%s\n' "$out"
  if [ "$c" -ne 0 ]; then
    if [ "$kind" = unittest ] && [ "$c" -eq 5 ] && grep -Fxq 'NO TESTS RAN' <<<"$out"; then echo "test-templates.sh: NO TEST RAN in $label"; c=1; fi
    first "$c"; return 0
  fi
  if [ "$kind" = tap ]; then
    if ! grep -Eq '^# pass [1-9]' <<<"$out" || grep -Fxq "ok 1 - ${label%%::*}" <<<"$out"; then echo "test-templates.sh: NO TEST RAN in $label"; first 1
    elif grep -Eq '^# (skipped|todo) [1-9]' <<<"$out"; then echo "test-templates.sh: SKIPPED in $label"; first 1; fi
  else
    if ! grep -Eq '^Ran [1-9][0-9]* tests?' <<<"$out"; then echo "test-templates.sh: NO TEST RAN in $label"; first 1
    elif grep -Eq 'skipped=[1-9]' <<<"$out"; then echo "test-templates.sh: SKIPPED in $label"; first 1; fi
  fi
}
PRELUDE_END
)"
INVS=()
add_words() { local s; printf -v s '%q ' "$@"; INVS+=("${s% }"); }
for p in "${UNIT_PATHS[@]}"; do
  f="${p%%::*}"; sel=; has_sel=0
  if [[ "$p" == *::* ]]; then has_sel=1; sel="${p#*::}"; [ -n "$sel" ] || usage; fi
  case "$f" in
    *.test.mjs)
      args=(node --test --test-reporter=tap)
      [ "$has_sel" = 0 ] || args+=("--test-name-pattern=$sel")
      add_words proof "$p" tap "${args[@]}" "$f" ;;
    *_test.py)
      args=(python3 -m unittest "$f")
      [ "$has_sel" = 0 ] || args+=(-k "$sel")
      add_words proof "$p" unittest "${args[@]}" ;;
    scripts/test-*.py)
      if [ "$has_sel" = 1 ]; then echo "test-templates.sh: $p has a selector; scripts/test-*.py takes no selector" >&2; usage; fi
      add_words proof "$p" unittest python3 "$f" ;;
    *_test.sh | scripts/test-*.sh)
      if [ "$has_sel" = 1 ]; then echo "test-templates.sh: $p has a selector; shell tests have none" >&2; usage; fi
      if sut_rows "$f"; then
        for s in "${SUTS[@]}"; do
          add_words bash "$f"
          INVS[-1]+=" \"\$PWD\"/$(printf '%q' "$s")"
        done
      else
        add_words bash "$f"
      fi ;;
    *) echo "test-templates.sh: $f is not a test file" >&2; usage ;;
  esac
done

CMD="$PRELUDE"
for inv in "${INVS[@]}"; do CMD+="; $inv || { c=\$?; [ \"\$rc\" -ne 0 ] || rc=\$c; }"; done
CMD+="; exit \"\$rc\""

rc=0; run_logged iso run "$CMD" || rc=$?
if [ "$rc" = 0 ]; then
  echo 'A file that failed in a wider run and now passes alone, with no change that explains it, fails alongside others: rerun the selection it failed in.'
  echo "Green. When your work is done, run .claude/scripts/check-templates.sh <your task's files> once, then write your return."
fi
exit "$rc"

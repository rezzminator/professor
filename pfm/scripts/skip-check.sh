#!/usr/bin/env bash
# The skip gate: every test a `go test -json` run SKIPPED must be named in
# scripts/known-skips.tsv, or the run is red. A suite that passes on tests it
# never ran is a coincidence detector.
#   scripts/skip-check.sh <go-test.json>...
# Prints one `SKIP-UNLISTED <pkg> <Test>` line per UNLISTED skip — the only
# actionable rows — then the verdict:
#   SKIPS PASS|FAIL|ERROR <detail>            rc 0 | 1 | 2
# Listed skips are counted into the verdict, not printed line by line: a healthy
# run and a broken one must not read alike, and one `GAP` line per known-good
# skip buries the unlisted rows it exists to surface. `PFM_SKIP_CHECK_VERBOSE=1`
# prints them as `GAP skipped test: <pkg> <Test> [<class>]` for a human audit.
# A subtest is covered by its own row or by its parent test's row.
# BROKEN STATE: no input named, an unreadable input or allow-list, a jq that is
# missing or fails, or an input holding no test event at all = SKIPS ERROR and
# rc 2 — "we could not look" is never "nothing was skipped".
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
LIST="${PFM_KNOWN_SKIPS:-$HERE/known-skips.tsv}"
die() { echo "SKIPS ERROR $*"; exit 2; }
[ $# -gt 0 ] || die "usage: skip-check.sh <go-test.json>..."
command -v jq >/dev/null 2>&1 || die "TOOLCHAIN-MISSING — jq not on PATH"
[ -r "$LIST" ] || die "allow-list $LIST unreadable"
for f in "$@"; do [ -r "$f" ] || die "input $f unreadable"; done
events=$(jq -rs '[.[] | select(.Test != null)] | length' "$@") || die "jq could not parse $*"
[ "$events" -gt 0 ] || die "no test event in $* — the run never started a test"
skipped=$(jq -r 'select(.Action == "skip" and .Test != null) | .Package + "\t" + .Test' "$@" | sort -u) \
  || die "jq could not read the skips in $*"
listed=0 unlisted=0
while IFS=$'\t' read -r pkg test; do
  [ -n "$pkg" ] || continue
  class=$(awk -F'\t' -v p="$pkg" -v t="$test" -v parent="${test%%/*}" \
    '$1 == p && ($2 == t || $2 == parent) { print $3; exit }' "$LIST")
  if [ -n "$class" ]; then
    [ "${PFM_SKIP_CHECK_VERBOSE:-0}" = "1" ] \
      && printf 'GAP skipped test: %s %s [%s]\n' "$pkg" "$test" "$class"
    listed=$((listed + 1))
  else
    printf 'SKIP-UNLISTED %s %s\n' "$pkg" "$test"; unlisted=$((unlisted + 1))
  fi
done <<< "$skipped"
if [ "$unlisted" -gt 0 ]; then
  echo "SKIPS FAIL $unlisted skipped test(s) not in ${LIST##*/} ($listed listed) — run them, or record the reason there"; exit 1
fi
echo "SKIPS PASS $listed skipped test(s), all listed in ${LIST##*/}"

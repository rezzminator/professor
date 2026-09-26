#!/usr/bin/env bash
# The one harness every fixture-driven shell test in this repo sources: a
# trap-cleaned scratch dir $T, the ok/bad counters, and shtest_end, the verdict.
#   SHTEST_TAG=my-suite; source …/scripts/shtest.sh; …; shtest_end
# BROKEN STATE: an unset SHTEST_TAG or a failed mktemp exits 2 with a named
# line before any assertion runs — a suite never reports "0 failed" from a
# harness that did not start.
[ -n "${SHTEST_TAG:-}" ] || { echo "shtest: SHTEST_TAG unset — the harness did not start" >&2; exit 2; }
T="$(mktemp -d "${TMPDIR:-/tmp}/${SHTEST_TAG}.XXXXXX")" || { echo "shtest: mktemp failed for $SHTEST_TAG" >&2; exit 2; }
trap 'rm -rf -- "$T"' EXIT
PASS=0
FAIL=0
ok() { printf 'PASS  %s\n' "$1"; PASS=$((PASS + 1)); }
bad() { printf 'FAIL  %s\n' "$1" >&2; shift; [ $# -gt 0 ] && printf '      %s\n' "$@" >&2; FAIL=$((FAIL + 1)); }
shtest_end() { printf '\n%d passed, %d failed\n' "$PASS" "$FAIL"; [ "$FAIL" -eq 0 ]; }

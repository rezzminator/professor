#!/usr/bin/env bash
# Clone ratchet over the shell / JS / Python surface — installed jscpd (pinned in
# infra/fence/tools.env) with .jscpd.json against the committed fingerprint baseline
# .jscpd-baseline.json: a clone whose fingerprint the baseline lacks is NEW and
# fails; a removed clone is a shrink the next --measure locks in.
#   scripts/clone-check.sh            check: FAIL on any new clone (named)
#   scripts/clone-check.sh --measure  rewrite the baseline from today's tree
# Prints one verdict line: CLONES PASS|FAIL|ERROR|MEASURE <detail>.
# BROKEN STATE: jscpd missing or crashing, a scan that matched no files, or
# the baseline absent in check mode = ERROR and exit 2 — never PASS by silence.
set -uo pipefail
ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
BASELINE="$ROOT/.jscpd-baseline.json"
MODE="${1:-check}"
case "$MODE" in check|--measure) ;; *) echo "usage: clone-check.sh [--measure]" >&2; exit 2 ;; esac
# shellcheck source=../infra/fence/tools.env
source "$ROOT/infra/fence/tools.env"
[[ -n "${JSCPD_VERSION:-}" ]] || { echo "CLONES ERROR JSCPD_VERSION unset in infra/fence/tools.env"; exit 2; }
JSCPD=""
if [[ -n "${TOOLS_BIN:-}" ]]; then
  candidate="$TOOLS_BIN/jscpd"
elif command -v go >/dev/null; then
  candidate="$ROOT/tmp/tools/$(go env GOOS)-$(go env GOARCH)/bin/jscpd"
else
  candidate=""
fi
if [[ -n "$candidate" && -x "$candidate" ]]; then
  JSCPD="$candidate"
else
  JSCPD="$(command -v jscpd || true)"
fi
[[ -n "$JSCPD" ]] || { echo "CLONES ERROR TOOLCHAIN-MISSING — jscpd not installed; run infra/fence/tools.sh (make -C pfm tools)"; exit 2; }
found="$("$JSCPD" --version 2>/dev/null)" || found="unreadable"
found="${found#jscpd }"
[[ "$found" == "$JSCPD_VERSION" || "$found" == "v$JSCPD_VERSION" ]] \
  || { echo "CLONES ERROR TOOLCHAIN-MISSING — jscpd $found is not JSCPD_VERSION $JSCPD_VERSION; run infra/fence/tools.sh (make -C pfm tools)"; exit 2; }
LOG="$(mktemp)"; trap 'rm -f "$LOG"' EXIT
run() { (cd "$ROOT" && "$JSCPD" --config .jscpd.json --fail-on-empty --baseline "$BASELINE" "$@") >"$LOG" 2>&1; }
if [[ "$MODE" == --measure ]]; then
  if run --update-baseline; then echo "CLONES MEASURE $(grep -oE 'Found [0-9]+ clones' "$LOG" | tail -1 || echo 'count unreadable') -> .jscpd-baseline.json"; exit 0; fi
  echo "CLONES ERROR jscpd did not run: $(tail -1 "$LOG")"; exit 2
fi
[[ -f "$BASELINE" ]] || { echo "CLONES ERROR baseline .jscpd-baseline.json missing — cannot tell new from old"; exit 2; }
if run --fail-on-new-clones 0; then echo "CLONES PASS $(grep -oE 'Found [0-9]+ clones' "$LOG" | tail -1 || echo 'count unreadable'), none new"; exit 0; fi
if grep -qiE 'new clone' "$LOG"; then
  grep -iE 'new clone|Clone found' -A3 "$LOG" | head -40
  echo "CLONES FAIL new clone(s) above — fix, or name the baseline update in the commit"; exit 1
fi
echo "CLONES ERROR jscpd did not run: $(tail -1 "$LOG")"; exit 2

#!/usr/bin/env bash
# The flow-matrix gate: every test and file TESTPLAN.md cites must exist. A matrix
# row citing a deleted test or a renamed file reads as coverage that is not there.
#   scripts/testplan-check.sh [TESTPLAN.md]
# Checked: each `Test…` identifier is a `func Test…` in some pfm/ *_test.go (a
# trailing `*` is a name prefix, `TestChatDigest*`); each backticked token shaped
# like a source path (.go .sh .py .zsh .txtar .tsv .sql), its `:line` /
# `:line-line` / `: Name` tail dropped, is a path suffix of a file under pfm/ (the
# plan writes a bare file name, or a path relative to the engine, internal/ or a
# package) or a path under the repo root. Runtime files (.credentials.json) are
# no source and not checked.
# Prints one `PLAN-STALE test|file <token> (line N)` per miss, then the verdict:
#   PLAN PASS|FAIL|ERROR <detail>            rc 0 | 1 | 2
# BROKEN STATE: an unreadable plan, a pfm tree with no test funcs to match
# against, or a plan citing nothing at all = PLAN ERROR rc 2 — "we could not look"
# is never "nothing is stale".
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
PFM="${PFM_PLAN_ROOT:-$(cd -- "$HERE/.." && pwd -P)}"
PLAN="${1:-$PFM/TESTPLAN.md}"
die() { echo "PLAN ERROR $*"; exit 2; }
[ -r "$PLAN" ] || die "plan $PLAN unreadable"
[ -d "$PFM" ] || die "pfm root $PFM missing"
W=$(mktemp -d) || die "mktemp failed"
trap 'rm -rf "$W"' EXIT
grep -rhoE '^func (\([^)]*\) )?(Test|Fuzz)[A-Za-z0-9_]*' --include='*_test.go' "$PFM" > "$W/raw"
[ $? -le 1 ] || die "grep could not read the test files under $PFM"
sed -E 's/.* //' "$W/raw" | sort -u > "$W/funcs"
[ -s "$W/funcs" ] || die "no test func under $PFM — the enumerator did not run"
cites=0 stale=0
# Test identifiers: one line per occurrence, "<line> <name>".
grep -noE '\bTest[A-Z][A-Za-z0-9_]*\*?' "$PLAN" | tr ':' ' ' | sort -u > "$W/names" || true
while read -r ln name; do
  cites=$((cites + 1))
  case "$name" in
    *'*') grep -q "^${name%\*}" "$W/funcs" && continue ;;
    *) grep -qx "$name" "$W/funcs" && continue ;;
  esac
  echo "PLAN-STALE test $name (line $ln)"; stale=$((stale + 1))
done < "$W/names"
# File paths: backticked tokens shaped like a repo source path.
find "$PFM" \( -name .git -o -name node_modules -o -name tmp \) -prune -o -type f -print | sed "s|^$PFM||" | sort -u > "$W/files" \
  || die "find could not list $PFM"
grep -noE '`[^` ]+`' "$PLAN" | tr -d '`' > "$W/ticks" || true
while IFS=: read -r ln tok; do
  p=${tok%%::*}; p=${p%%:[0-9]*}
  case "$p" in /*|'~'*|*'*'*|*'{'*|*'<'*|*'$'*) continue ;; esac
  printf '%s' "$p" | grep -qE '^[A-Za-z0-9_./-]+\.(go|sh|py|zsh|txtar|tsv|sql)$' || continue
  cites=$((cites + 1))
  # Found when it is a path suffix of a file under pfm/ (a bare file name or a
  # package-relative path) or a repo-root path.
  grep -qF -- "/$p" "$W/files" && grep -qE -- "(^|/)$(printf '%s' "$p" | sed 's/[.]/[.]/g')\$" "$W/files" && continue
  [ -e "$PFM/../$p" ] && continue
  echo "PLAN-STALE file $p (line $ln)"; stale=$((stale + 1))
done < "$W/ticks"
[ "$cites" -gt 0 ] || die "no test or file cited in $PLAN — the extractor found nothing"
if [ "$stale" -gt 0 ]; then
  echo "PLAN FAIL $stale stale citation(s) in ${PLAN##*/} ($cites checked) — remove the row's cite, or re-point it at the renamed test or file"; exit 1
fi
echo "PLAN PASS $cites citation(s) in ${PLAN##*/}, all present"

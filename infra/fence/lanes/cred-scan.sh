#!/usr/bin/env bash
# Refuse credentials unless their complete JSON body is a registered fixture.
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
FIXTURES="$HERE/fixtures"
if [ "${1:-}" = --fixtures ]; then
  [ $# -ge 2 ] || { echo 'usage: cred-scan.sh [--fixtures DIR] [ROOT…]' >&2; exit 2; }
  FIXTURES="$2"; shift 2
fi
[ $# -gt 0 ] || set -- /root /tmp /home
failed() { printf 'cred-scan: SCAN-FAILED — %s\n' "$1" >&2; exit 2; }
command -v jq >/dev/null || failed 'jq is not on PATH'
[ -d "$FIXTURES" ] || failed "fixtures directory missing: $FIXTURES"
FIXTURES="$(cd -- "$FIXTURES" && pwd -P)" || failed 'fixtures directory unreadable'
shopt -s nullglob
registered=()
for file in "$FIXTURES/"*.json; do
  body="$(jq -S -c . "$file" 2>/dev/null)" || failed "fixture unreadable or invalid: $file"
  [ -n "$body" ] || failed "fixture empty: $file"
  registered+=("$body")
done
[ "${#registered[@]}" -gt 0 ] || failed "fixtures directory empty: $FIXTURES"
list="$(mktemp)" || failed 'cannot create enumeration file'
trap 'rm -f -- "$list"' EXIT
count=0 refused=0
for root in "$@"; do
  [ -d "$root" ] && [ -r "$root" ] && [ -x "$root" ] || failed "root unreadable: $root"
  root="$(cd -- "$root" && pwd -P)" || failed "root unreadable: $root"
  find "$root" \( -path /worktree -o -path "$FIXTURES" \) -prune -o \
    -type f \( -name '.credentials.json*' -o -name 'auth.json*' \) -print0 >"$list" 2>/dev/null ||
    failed "cannot enumerate root: $root"
  while IFS= read -r -d '' file; do
    count=$((count + 1)); match=0
    if body="$(jq -S -c . "$file" 2>/dev/null)" && [ -n "$body" ]; then
      for fixture in "${registered[@]}"; do
        if [ "$body" = "$fixture" ]; then match=1; break; fi
      done
    fi
    if [ "$match" -eq 0 ]; then
      printf 'cred-scan: ✗ CREDENTIAL-REFUSED %s — not a registered fixture\n' "$file" >&2
      refused=$((refused + 1))
    fi
  done <"$list"
done
[ "$refused" -eq 0 ] || exit 1
printf 'cred-scan: clean — %s credential file(s), every one a registered fixture\n' "$count"

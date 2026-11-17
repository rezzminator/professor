#!/usr/bin/env bash
# Prints the install-ping counts: each query in stats.sql, read-only, against
# the live D1 database through wrangler. Needs Cloudflare credentials wrangler
# can use (wrangler login, or CLOUDFLARE_API_TOKEN and CLOUDFLARE_ACCOUNT_ID).
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$here"
status=0

# Every paragraph of stats.sql is read and judged before any runs: title its
# first `-- ` line, sql its other lines joined; a paragraph of comments alone
# (the file header) is skipped. One that is not a single SELECT refuses the
# whole file, so no query reaches the live database ahead of the refusal.
titles=() queries=()
title="" sql=""
add_block() {
  [ -n "$sql" ] || return 0
  case "$sql" in
    SELECT\ *) ;;
    *) echo "stats.sh: refused \"$title\": not a SELECT: $sql" >&2; exit 2 ;;
  esac
  case "${sql%;}" in
    *\;*) echo "stats.sh: refused \"$title\": more than one statement: $sql" >&2; exit 2 ;;
  esac
  titles+=("$title") queries+=("$sql")
}
while IFS= read -r line || [ -n "$line" ]; do
  if [ -z "${line//[[:space:]]/}" ]; then
    add_block
    title="" sql=""
  elif [ -z "$title" ] && [ -z "$sql" ] && [ "${line#-- }" != "$line" ]; then
    title="${line#-- }"
  elif [ "${line#--}" = "$line" ]; then
    sql="${sql:+$sql }$line"
  fi
done < stats.sql
add_block

for i in "${!queries[@]}"; do
  printf '\n== %s\n' "${titles[$i]}"
  if ! npx --yes wrangler@4 d1 execute install-ping --remote --command "${queries[$i]}" </dev/null; then
    echo "stats.sh: query failed: ${titles[$i]}" >&2
    status=1
  fi
done
exit "$status"

#!/usr/bin/env bash
# Prints the install-ping counts: each query in stats.sql, read-only, against
# the live D1 database through wrangler, as an aligned table (needs jq). Needs Cloudflare credentials wrangler
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

# render prints wrangler's --json answer as an aligned table, header first,
# each column but the last padded to its widest cell plus two spaces; an empty
# result is "(no rows)". jq aligns it, not column(1): that is absent from
# minimal images such as the test fence.
render() {
  jq -r '.[0].results as $rows
    | if ($rows | length) == 0 then "(no rows)"
      else ($rows[0] | keys_unsorted) as $keys
        | ([$keys] + [$rows[] | [.[$keys[]] | tostring]]) as $lines
        | [range(0; $keys | length) as $c | [$lines[][$c] | length] | max] as $widths
        | $lines[]
        | [to_entries[]
            | if .key < ($widths | length) - 1
              then .value + (" " * ($widths[.key] - (.value | length) + 2))
              else .value end]
        | join("")
      end'
}

for i in "${!queries[@]}"; do
  printf '\n== %s\n' "${titles[$i]}"
  if ! answer="$(npx --yes wrangler@4 d1 execute install-ping --remote --json --command "${queries[$i]}" </dev/null)"; then
    printf '%s\n' "$answer" >&2
    echo "stats.sh: query failed: ${titles[$i]}" >&2
    status=1
  elif ! printf '%s\n' "$answer" | render; then
    printf '%s\n' "$answer" >&2
    echo "stats.sh: unreadable answer: ${titles[$i]}" >&2
    status=1
  fi
done
exit "$status"

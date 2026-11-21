#!/usr/bin/env bash
# stats.sh's own test: an npx stub on PATH records each wrangler call and
# answers with wrangler's --json shape, so the script's paragraph split, its
# query order, its table rendering and its refusals are proven without
# Cloudflare; the stub drains its stdin as wrangler may, and NPX_EMPTY=1 makes
# it answer no rows.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
mkdir -p "$work/bin" "$work/copy"
cat >"$work/bin/npx" <<'STUB'
#!/usr/bin/env bash
printf '%s\n' "$*" >>"$NPX_LOG"
cat >/dev/null
if [ -n "${NPX_EMPTY:-}" ]; then
  echo '[{"results":[],"success":true}]'
else
  echo '[{"results":[{"product":"pfm","ids":3},{"product":"harvester","ids":12}],"success":true}]'
fi
STUB
chmod +x "$work/bin/npx"
fail() { echo "stats_test: FAIL — $*" >&2; exit 1; }

export NPX_LOG="$work/calls"
PATH="$work/bin:$PATH" bash "$here/stats.sh" >"$work/out" || fail "stats.sh exited $? on the shipped stats.sql (output: $work/out)"
want_titles='== unique install ids ever, per product
== unique install ids ever, release vs -alpha builds
== daily active ids, last 30 days, per product
== ids active in the last 7 and 30 days, per product
== ids active in the last 30 days, per product and OS
== pfm ids active in the last 30 days, per enabled Professor plugin
== installs (event=install) per day, per product
== every install id: first and last day seen, days, latest version and OS'
got_titles="$(grep '^== ' "$work/out")"
[ "$got_titles" = "$want_titles" ] || fail "titles: want
$want_titles
got
$got_titles"
want_table='== unique install ids ever, per product
product    ids
pfm        3
harvester  12'
got_table="$(sed -n 2,5p "$work/out")"
[ "$got_table" = "$want_table" ] || fail "first table: want
$want_table
got
$got_table"
want_first='--yes wrangler@4 d1 execute install-ping --remote --json --command SELECT product, COUNT(DISTINCT id) AS ids FROM pings GROUP BY product ORDER BY product;'
[ "$(head -n 1 "$NPX_LOG")" = "$want_first" ] || fail "first call: want [$want_first] got [$(head -n 1 "$NPX_LOG")]"
[ "$(wc -l <"$NPX_LOG" | tr -d ' ')" = 8 ] || fail "want 8 wrangler calls, got $(wc -l <"$NPX_LOG")"

cp "$here/stats.sh" "$work/copy/stats.sh"
printf -- '-- quiet\nSELECT 1;\n' >"$work/copy/stats.sql"
NPX_EMPTY=1 PATH="$work/bin:$PATH" bash "$work/copy/stats.sh" >"$work/out" || fail "an empty answer: exited $? (output: $work/out)"
[ "$(cat "$work/out")" = '
== quiet
(no rows)' ] || fail "an empty answer: want (no rows), got [$(cat "$work/out")]"

printf -- '-- wipe\nDELETE FROM pings;\n' >"$work/copy/stats.sql"
: >"$NPX_LOG"
rc=0
PATH="$work/bin:$PATH" bash "$work/copy/stats.sh" >/dev/null 2>"$work/err" || rc=$?
[ "$rc" = 2 ] || fail "a DELETE paragraph: want exit 2, got $rc"
[ "$(cat "$work/err")" = 'stats.sh: refused "wipe": not a SELECT: DELETE FROM pings;' ] || fail "refusal text: $(cat "$work/err")"
[ ! -s "$NPX_LOG" ] || fail "a refused query still reached wrangler: $(cat "$NPX_LOG")"

printf -- '-- fine\nSELECT 1;\n\n-- smuggled\nSELECT 1; DELETE FROM pings;\n' >"$work/copy/stats.sql"
rc=0
PATH="$work/bin:$PATH" bash "$work/copy/stats.sh" >/dev/null 2>"$work/err" || rc=$?
[ "$rc" = 2 ] || fail "a second statement after a SELECT: want exit 2, got $rc"
[ "$(cat "$work/err")" = 'stats.sh: refused "smuggled": more than one statement: SELECT 1; DELETE FROM pings;' ] ||
  fail "refusal text: $(cat "$work/err")"
[ ! -s "$NPX_LOG" ] || fail "a query ran ahead of the file's refusal: $(cat "$NPX_LOG")"
echo "stats_test: PASS"

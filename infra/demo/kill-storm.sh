#!/usr/bin/env bash
# kill-storm.sh — runs INSIDE the demo fence container: ends every storm chat
# (storm.sh's STORM_<n> rows) and hides the ended rows, and touches nothing
# else — the slide chats fleet.sh spawned stay exactly as they were. Matching
# is the exact name shape STORM_<digits>, never a prefix, so a chat someone
# named STORM_REVIEW would survive too.
#
#   kill-storm.sh
#
# BROKEN STATE: a row pfm cannot end or hide is named with pfm's own message
# and the script exits 1; the closing line is the proof either way — the STORM
# rows still listed (must be 0) and the count of other live rows before → after
# (must match). A closing line that does not say 0 and equal counts is a
# storm that is still blowing, not a stopped one.
set -uo pipefail
export PATH="$HOME/.local/bin:$PATH"
cd /tmp || exit 1
storm='$5 ~ /^STORM_[0-9]+$/'
others() { pfm ls --tsv 2>/dev/null | awk -F'\t' "\$1 ~ /^live-/ && !($storm)" | wc -l | tr -d ' '; }
before="$(others)"
failed=0
for n in $(pfm ls --tsv 2>/dev/null | awk -F'\t' "\$1 ~ /^live-/ && $storm {print \$5}"); do
  if pfm chat end "$n" >/dev/null; then echo "ended $n"; else echo "kill-storm: pfm chat end $n failed (see above)" >&2; failed=1; fi
done
sleep 2
# An ended chat lingers as a resumable ↻ row; the picker should show the
# fleet, not the rehearsal, so the ended rows are hidden by id.
for id in $(pfm ls --tsv 2>/dev/null | awk -F'\t' "\$1 ~ /^resume-/ && $storm {print \$2}"); do
  if pfm chat kill "$id" >/dev/null; then echo "hid $id"; else echo "kill-storm: pfm chat kill $id failed (see above)" >&2; failed=1; fi
done
left="$(pfm ls --tsv 2>/dev/null | awk -F'\t' "$storm" | wc -l | tr -d ' ')"
after="$(others)"
echo "kill-storm: STORM rows left: $left · other live rows: $before → $after"
[ "$failed" -eq 0 ] && [ "$left" -eq 0 ] && [ "$before" -eq "$after" ]

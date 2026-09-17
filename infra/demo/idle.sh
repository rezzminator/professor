#!/usr/bin/env bash
# idle.sh — runs INSIDE the demo fence container: takes the idle slide chats
# (the names fleet.sh spawns, plus EXPRESS_INSTALL — never the presenter's own
# chats) off the sky and brings them back.
#
#   idle.sh down    end every non-storm live chat and hide its ↻ row — the cosmos
#                   then shows the storm alone
#   idle.sh up      unhide those rows and resume each chat in place (same
#                   transcript: MIGRATION still has its step 1, EXPRESS_INSTALL its
#                   install record); anything that will not resume is respawned
#                   fresh by fleet.sh
#   idle.sh roster  print the slide-chat names, one per line (verify.sh reads it)
#
# `down` records what it hid in $STATE so `up` knows what to bring back; with no
# record, `up` is fleet.sh alone (respawns whatever is missing).
# BROKEN STATE: a row pfm cannot end, hide, unhide or open is named with pfm's
# own message and the exit is 1; the closing line is the count the sky should
# show — after `down`, non-storm live rows must read 0; after `up`, the resumed
# + respawned rows must add up to what `down` took.
set -uo pipefail
export PATH="$HOME/.local/bin:$PATH"
export IS_SANDBOX=1 # root fence: Claude Code refuses the bypass flag under root without it (setup.sh)
cd /tmp || exit 1
STATE="$HOME/.local/state/pfm/demo-idle.tsv"
storm='$5 ~ /^STORM_[0-9]+$/'
failed=0
# The slide chats are exactly the names fleet.sh spawns plus the install record —
# read from fleet.sh so there is one roster. Anything else that is live (the
# presenter's own chats, an ORCHESTRATOR/WORKER pair) is never touched.
roster="$(grep -oE '^spawn [A-Z0-9_]+' "$(dirname "$0")/fleet.sh" | awk '{print $2}'; echo EXPRESS_INSTALL)"
in_roster() { grep -qxF "$1" <<<"$roster"; }
case "${1:-}" in
down)
  mkdir -p "$(dirname "$STATE")"
  # name \t id of every live slide chat, saved BEFORE anything is ended.
  : > "$STATE"
  while IFS=$'\t' read -r name id; do
    in_roster "$name" && printf '%s\t%s\n' "$name" "$id" >> "$STATE"
  done < <(pfm ls --tsv 2>/dev/null | awk -F'\t' "\$1 ~ /^live-/ && !($storm) {print \$5 \"\t\" \$2}")
  while IFS=$'\t' read -r name id; do
    [ -n "$name" ] || continue
    if pfm chat end "$name" >/dev/null; then echo "ended $name"; else echo "idle-down: pfm chat end $name failed (see above)" >&2; failed=1; fi
  done < "$STATE"
  sleep 2
  while IFS=$'\t' read -r name id; do
    [ -n "$id" ] || continue
    if pfm chat kill "$id" >/dev/null; then echo "hid $name"; else echo "idle-down: pfm chat kill $id ($name) failed (see above)" >&2; failed=1; fi
  done < "$STATE"
  left=0; while IFS=$'\t' read -r name id; do in_roster "$name" && left=$((left + 1)); done < <(pfm ls --tsv 2>/dev/null | awk -F'\t' "\$1 ~ /^live-/ {print \$5 \"\t\" \$2}")
  echo "idle-down: took $(wc -l < "$STATE" | tr -d ' ') slide chats down · slide chats still live: $left"
  [ "$failed" -eq 0 ] && [ "$left" -eq 0 ]
  ;;
up)
  resumed=0
  if [ -s "$STATE" ]; then
    while IFS=$'\t' read -r name id; do
      [ -n "$id" ] || continue
      pfm chat unkill "$id" >/dev/null 2>&1 || true # already visible is fine
      if pfm chat open "$name" </dev/null >/dev/null 2>&1; then echo "resumed $name"; resumed=$((resumed + 1)); else echo "idle-up: pfm chat open $name did not resume — fleet.sh will respawn it" >&2; fi
    done < "$STATE"
    sleep 8
  else
    echo "idle-up: no record of an idle-down — spawning whatever is missing"
  fi
  # Whatever did not come back (or was never there) is spawned fresh; live rows are kept.
  respawned="$("$(dirname "$0")/fleet.sh" | grep -c '^spawned ' || true)"
  live="$(pfm ls --tsv 2>/dev/null | awk -F'\t' "\$1 ~ /^live-/ && !($storm)" | wc -l | tr -d ' ')"
  echo "idle-up: resumed $resumed · respawned $respawned · non-storm live rows now: $live"
  [ "$live" -gt 0 ]
  ;;
roster) echo "$roster" ;;
*) echo "usage: idle.sh up | down | roster" >&2; exit 2 ;;
esac

#!/usr/bin/env bash
# fleet.sh — runs INSIDE the demo fence container after setup.sh install: spawns
# the demo fleet. Every chat is a REAL harness on a REAL seat — each spawn costs
# one short model turn, and an idle chat costs nothing after that. Names and
# projects match the deck; every project and message is invented.
#
# Spawns from this bare shell are UNSIGNED (the spawner is derived from tmux-pane
# ancestry, which a docker-exec shell lacks): the cosmos tab says so in one line.
# Chats the fleet spawns from inside a chat (chat_new) are signed.
#
# Re-running is safe: names already ● live are kept, the rest are (re)spawned.
#
# BROKEN STATE: a spawn that fails exits non-zero with pfm's message; the final
# `pfm ls --plain` must show a ● live row per chat — a ✦-only listing means the
# harness never reached pfm's statusline.
set -euo pipefail
export PATH="$HOME/.local/bin:$PATH"
export IS_SANDBOX=1 # root fence: Claude Code refuses the bypass flag under root without it (setup.sh)
cd /tmp
READY='Reply with exactly one line confirming you are ready, then wait for instructions. Do nothing else.'
"$(dirname "$0")/daemon.sh" # the professor MCP daemon the chats' stdio servers forward to; harmless when already up
# Seat B is the second configured Claude seat (the reload beat moves a chat onto
# it); with a single seat configured it falls back to the first. Read, never
# assumed: a seat the host could not hand in is not in this config.
# Seats come from up.sh's probe (the ones that answered, in config order); with
# no record, the config order is trusted.
read -r -a LIVE <<<"$(cat "$HOME/.local/state/pfm/demo-seats-live" 2>/dev/null || jq -r '[.accounts[].id] | join(" ")' "$HOME/.config/pfm/pfm.config.json")"
SEAT_A="${LIVE[0]:-}"
SEAT_B="${LIVE[1]:-$SEAT_A}"
[ -n "$SEAT_A" ] && [ "$SEAT_A" != null ] || { echo "fleet: no Claude seat in ~/.config/pfm/pfm.config.json" >&2; exit 1; }
live() { pfm ls --plain 2>/dev/null | grep -q "^● $1 "; }
spawn() { # spawn <name> <engine cc|cx> <project> <account> <prompt> — idempotent: a live row is kept
  if live "$1"; then echo "kept $1 (live)"; return; fi
  local acct=(); [ "$2" = cc ] && acct=(--account "$4")
  pfm chat new --name "$1" --engine "$2" --cwd "/work/$3" "${acct[@]}" "$5" >/dev/null
  echo "spawned $1 ($2 · $3)"
  sleep 4
}
spawn DEMO_CLAUDE cc express "$SEAT_A" "You are DEMO_CLAUDE, the Claude side of a live pfm fleet demo; DEMO_CODEX is the Codex chat beside you. $READY"
spawn DEMO_CODEX cx express 1 "You are DEMO_CODEX, the Codex side of a live pfm fleet demo; DEMO_CLAUDE is the Claude chat beside you. $READY"
spawn HARV_ORCH cc harvester "$SEAT_A" "You are HARV_ORCH, orchestrator of the harvester project. $READY"
spawn HARV_REVIEW cx harvester 1 "You are HARV_REVIEW, reviewer on the harvester project. $READY"
spawn ATLAS_ORCH cx atlas 1 "You are ATLAS_ORCH, orchestrator of the atlas project. $READY"
spawn ATLAS_REVIEW cc atlas "$SEAT_B" "You are ATLAS_REVIEW, reviewer on the atlas project. $READY"
spawn LUMEN_ORCH cc lumen "$SEAT_B" "You are LUMEN_ORCH, orchestrator of the lumen project. $READY"
spawn LUMEN_DOCS cx lumen 1 "You are LUMEN_DOCS, docs writer on the lumen project. $READY"
spawn ORBIT_ORCH cc orbit "$SEAT_A" "You are ORBIT_ORCH, orchestrator of the orbit project. $READY"
spawn ORBIT_QA cx orbit 1 "You are ORBIT_QA, QA on the orbit project. $READY"
spawn MIGRATION cc atlas "$SEAT_A" "We are migrating the ledger table to the v7 schema step by step. The table is ledger(id, account_id, amount_cents, posted_at). Step 1: write migrations/v7_ledger.sql adding a refund_reason text column and an index on (account_id, posted_at). Do step 1 now, report in two lines, then stop and wait — I will tell you when to continue."
spawn FLIGHT_RUN cc lumen "$SEAT_B" "You are FLIGHT_RUN, running a three-task flight on the lumen project. $READY"
pfm ls --plain

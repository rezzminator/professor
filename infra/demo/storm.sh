#!/usr/bin/env bash
# storm.sh — the cosmos storm, runs INSIDE the demo fence container: N cheap chats
# across engines (Claude + Codex by default; STORM_ENGINES="cc cx ox" once pfm
# can spawn OpenCode headless) round-robin, that answer
# every message they receive with a real chat_inject back to the sender AND a
# ping to another storm chat, so the sky fills with signed edges and the ledger
# ticker never stops. Each chat stops on its own after SENDS messages; `stop`
# ends them early.
#
#   storm.sh start [N=6] [SENDS=30]     spawn STORM_1..N and kick the first pings
#   storm.sh stop                       end every STORM_* chat
#
# Cheapest posture per engine, overridable from the environment:
#   STORM_CC_ACCOUNT=3 STORM_CC_MODEL=sonnet STORM_CC_EFFORT=low     (Claude seat)
#   STORM_GPT_MODEL=gpt-5.6-luna STORM_GPT_EFFORT=medium             (Codex, and OpenCode's model)
#   STORM_ENGINES="cc cx"                                             (rotation)
# Cost: N chats × SENDS one-line turns — cents, not dollars.
# Re-running `start` is safe: live STORM rows are kept and re-seeded.
# BROKEN STATE: a spawn that fails exits with pfm's message; `start` ends by
# listing the STORM rows — fewer than N live rows means an engine never came up.
set -euo pipefail
export PATH="$HOME/.local/bin:$PATH"
export IS_SANDBOX=1 # root fence: Claude Code refuses the bypass flag under root without it (setup.sh)
cd /tmp
PROJECTS=(atlas lumen orbit harvester)
# Engines round-robin. OpenCode is real in this container (picker → New OpenCode
# chat) but `pfm chat new` has no OpenCode door yet — no headless planner, no
# spawn launcher, no naming — so it is not in the default rotation.
read -r -a ENGINES <<<"${STORM_ENGINES:-cc cx}"
CC_ACCOUNT="${STORM_CC_ACCOUNT:-3}" CC_MODEL="${STORM_CC_MODEL:-sonnet}" CC_EFFORT="${STORM_CC_EFFORT:-low}"
GPT_MODEL="${STORM_GPT_MODEL:-gpt-5.6-luna}" GPT_EFFORT="${STORM_GPT_EFFORT:-medium}"
"$(dirname "$0")/daemon.sh" # chat_* over HTTP for Codex rows; harmless when already up
case "${1:-}" in
start)
  N="${2:-6}"; SENDS="${3:-30}"
  names=(); for i in $(seq 1 "$N"); do names+=("STORM_$i"); done
  roster="${names[*]}"
  for i in $(seq 1 "$N"); do
    me="STORM_$i"; project="${PROJECTS[$(( (i - 1) % ${#PROJECTS[@]} ))]}"
    engine="${ENGINES[$(( (i - 1) % ${#ENGINES[@]} ))]}"
    if pfm ls --plain 2>/dev/null | grep -q "^● $me "; then echo "kept $me (live)"; continue; fi
    case "$engine" in
      cc) posture=(--account "$CC_ACCOUNT" --model "$CC_MODEL" --effort "$CC_EFFORT"); label="$CC_MODEL · $CC_EFFORT · seat $CC_ACCOUNT" ;;
      cx) posture=(--model "$GPT_MODEL" --effort "$GPT_EFFORT"); label="$GPT_MODEL · $GPT_EFFORT" ;;
      ox) posture=(--model "openai/$GPT_MODEL" --effort "$GPT_EFFORT"); label="openai/$GPT_MODEL · $GPT_EFFORT" ;;
    esac
    pfm chat new --name "$me" --engine "$engine" "${posture[@]}" --cwd "/work/$project" \
      "You are $me, one of the storm chats: $roster. This is a live demo of chats messaging each other; nothing here is real work. Rules, every time a message from another chat arrives: (1) reply to its sender with the chat_inject tool, one line under 12 words, an ACK plus an invented status; (2) then chat_inject one OTHER storm chat from the roster (never yourself, vary your pick) with a one-line question under 12 words. Never write files, never run commands, never call any other tool. Count your sends; after $SENDS sends, stop replying entirely. Until the first message arrives, reply 'ready' and wait." >/dev/null
    echo "spawned $me ($engine · $label · $project)"
    sleep 4
  done
  sleep 6
  # Kick: up to three seeds so the storm has three fronts at once. A bare shell
  # holds no chat identity and pfm refuses an unsigned inject, so each seed is
  # SIGNED as the peer it names — a real edge from the first message on.
  sock_of() { pfm ls --tsv 2>/dev/null | awk -F'\t' -v n="$1" '$5 == n {print $11; exit}'; }
  id_of() { pfm ls --tsv 2>/dev/null | awk -F'\t' -v n="$1" '$5 == n {print $2; exit}'; }
  for pair in "STORM_1 STORM_2" "STORM_3 STORM_4" "STORM_5 STORM_6"; do
    set -- $pair
    [ "${2#STORM_}" -le "$N" ] || continue
    CHAT_SENDER_LABEL="$2" CHAT_SENDER_SESSION="$(sock_of "$2")" CHAT_SENDER_SID="$(id_of "$2")" \
      pfm chat inject "$1" "Storm is on — reply to me and ping another storm chat; keep the chain going." >/dev/null
    echo "seeded $2 → $1"
  done
  pfm ls --plain | grep -E 'STORM_' || echo "storm: no STORM rows are live"
  ;;
stop)
  # end the live rows, then hide the ended ones: an ended chat lingers as a
  # resumable ↻ row, and the picker should show the fleet, not the rehearsal.
  for n in $(pfm ls --tsv 2>/dev/null | awk -F'\t' '$1 ~ /^live-/ && $5 ~ /^STORM_/ {print $5}'); do pfm chat end "$n" >/dev/null && echo "ended $n"; done
  sleep 2
  for id in $(pfm ls --tsv 2>/dev/null | awk -F'\t' '$1 ~ /^resume-/ && $5 ~ /^STORM_/ {print $2}'); do pfm chat kill "$id" >/dev/null && echo "hid $id"; done
  ;;
*) echo "usage: storm.sh start [N] [SENDS] | stop" >&2; exit 2 ;;
esac

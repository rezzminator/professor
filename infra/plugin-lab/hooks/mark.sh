#!/usr/bin/env bash
# The one lab hook (D7, L8): hook stdin JSON -> one compact line appended to
# $LAB_MARKS. It prints nothing (SessionStart and UserPromptSubmit stdout would
# become model context) and always exits 0. bash 3.2-safe: claude's PATH finds
# /bin/bash on macOS.
event=${1:-unknown}
[ -n "${LAB_MARKS:-}" ] || exit 0
input=$(cat)
ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
line=$(printf '%s' "$input" | jq -c --arg ts "$ts" --arg e "$event" \
  '{ts: $ts, event: $e} + ({session_id, transcript_path, source, reason, agent_id, agent_type, agent_transcript_path, trigger} | with_entries(select(.value != null)))' 2>/dev/null)
[ -n "$line" ] || line=$(jq -nc --arg ts "$ts" --arg e "$event" '{ts: $ts, event: $e, error: "stdin-unparseable"}')
printf '%s\n' "$line" >> "$LAB_MARKS" 2>/dev/null
exit 0

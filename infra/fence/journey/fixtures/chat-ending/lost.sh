#!/usr/bin/env bash
set -euo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
. "$HERE/lib.sh"
. "$HERE/fixtures/chat-ending/driver.sh"
CHAT="$1" OWNER="$2" BEFORE="$3"
python3 "$HERE/chat.py" pick "$OWNER" "$(jq -r '.branch' "$CHAT")" "$(jq -r '.role // .origin' "$CHAT")"
python3 "$HERE/terminal.py" keys "$OWNER" ctrl+x escape
python3 "$HERE/fixtures/chat-ending/lifecycle.py" expect "$CHAT" ended "$BEFORE" killed
observe "$OWNER"
PROJECTION="$JOURNEY_OUT/beats/$PFM_BEAT/lost-checks.json"
python3 "$HERE/fixtures/chat-ending/projection.py" "$BEFORE" "$JOURNEY_OBSERVATION" "$CHAT" lost "$PROJECTION"
ending_assertions chat.kill-lost-seat "$PROJECTION"
ending_action chat.kill-lost-seat chat.kill picker
pass
test ! -e "$JOURNEY_OUT/$JOURNEY_LANE.failed"

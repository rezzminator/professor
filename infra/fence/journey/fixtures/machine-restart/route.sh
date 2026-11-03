#!/usr/bin/env bash
set -euo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
. "$HERE/lib.sh"
. "$HERE/fixtures/chat-ending/driver.sh"
. "$HERE/fixtures/chat-mesh/entry.sh"
. "$HERE/fixtures/machine-restart/call.sh"
_restart_call "$1" "$2" "$3"
observe "$OWNER"
ending_action chat.call-mcp chat.call mcp
pass
test ! -e "$JOURNEY_OUT/$JOURNEY_LANE.failed"

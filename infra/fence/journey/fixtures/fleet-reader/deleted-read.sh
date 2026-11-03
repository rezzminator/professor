#!/usr/bin/env bash
# Observe the real picker while the owned scratch working directory is absent.
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
. "$HERE/lib.sh"
. "$HERE/fixtures/chat-mesh/entry.sh"
ID="$1" PREFIX="$2" OWNER="$3" SCRATCH="$4"
export JOURNEY_TARGET="$OWNER"
python3 "$HERE/fixtures/fleet-reader/picker.py" "$OWNER" "$SCRATCH" "$PREFIX" deleted || exit 2
observe || exit 2
cp "$JOURNEY_OBSERVATION" "$PREFIX.deleted-observation.json"
python3 "$HERE/fixtures/fleet-reader/projection.py" deleted-cwd "$JOURNEY_OBSERVATION" "$SCRATCH" >"$PREFIX.deleted-checks.json" || exit 2
mesh_checks "$ID" "$PREFIX.deleted-checks.json"
mesh_action "$ID" picker.open typed
python3 "$HERE/terminal.py" keys "$OWNER" escape

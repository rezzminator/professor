#!/usr/bin/env bash
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
. "$HERE/lib.sh"
. "$HERE/fixtures/chat-ending/driver.sh"
. "$HERE/fixtures/chat-mesh/entry.sh"
. "$HERE/fixtures/machine-restart/call.sh"
operation="$1" slug="$2"
id=herdr.restore
[ "$operation" != daemons ] || id=lifecycle.restart
if ! beat "$slug" "$id"; then exit 0; fi
before="$JOURNEY_OUT/beats/$PFM_BEAT/before.json"
after="$JOURNEY_OUT/beats/$PFM_BEAT/after.json"
checks="$JOURNEY_OUT/beats/$PFM_BEAT/restore-checks.json"
python3 "$HERE/chat.py" snapshot "$OWNER" "$before" || { fail 'restart baseline unreadable'; exit 1; }
if [ "$operation" = herdr ]; then
  if ending_command "$OWNER" herdr-stop herdr --session "$JOURNEY_SESSION" server stop &&
     [ "$(cat "$RECEIPT.status")" = 0 ] &&
     ending_command "$OWNER" herdr-start sh -c 'nohup herdr --session "$JOURNEY_SESSION" server >"$JOURNEY_ROOT/herdr-restarted.log" 2>&1 </dev/null &' &&
     poll restored-carried-identities 60 python3 "$HERE/fixtures/machine-restart/restore.py" ready &&
     python3 -c 'import os; from bootstrap import observer_picker; from runtime import read_json; observer_picker(read_json(os.environ["JOURNEY_CARRY"])["observer_picker"])' &&
     python3 "$HERE/chat.py" snapshot "$OWNER" "$after" &&
     python3 "$HERE/fixtures/machine-restart/restore.py" checks "$before" "$after" "$checks"; then
    ending_assertions "$id" "$checks"
    if jq -e 'all(.expected == .actual)' "$checks" >/dev/null; then
      python3 "$HERE/fixtures/machine-restart/restore.py" adopt "$before" "$after" "$JOURNEY_OUT/beats/$PFM_BEAT/foreign-rebind.json" || { fail 'foreign sentinel restart transition refused'; exit 1; }
    fi
    observe "$OWNER"
    ending_action "$id" chat.resume typed
    pass
  else fail 'Herdr owner restart did not restore the observed fleet'; fi
else
  if python3 "$HERE/fixtures/machine-restart/control.py" snapshot "$RESTART_ROOT/services-before.json" &&
     ending_command "$OWNER" restart pfm restart &&
     poll restarted-services 45 python3 "$HERE/fixtures/machine-restart/control.py" snapshot "$RESTART_ROOT/services-after.json"; then
    ending_invocation "$id" "$RECEIPT" pfm restart
    assert "$id" 0 "$(cat "$RECEIPT.status")" "$RECEIPT.status"
    assert "$id" active "$(jq -r '.schedule' "$RESTART_ROOT/services-after.json")" "$RESTART_ROOT/services-after.json"
    if [ -n "${RESTART_FACTOR:-}" ] && [ "$(jq -r .kind "$RESTART_FACTOR")" = stopped-mcp ]; then
      assert "$id" '"n/a:inactive"' "$(jq -c .mcp "$RESTART_ROOT/services-before.json")" "$RESTART_ROOT/services-before.json"
      assert "$id" '"n/a:inactive"' "$(jq -c .mcp "$RESTART_ROOT/services-after.json")" "$RESTART_ROOT/services-after.json"
    else
      assert "$id" active "$(jq -r '.schedule' "$RESTART_ROOT/services-before.json")" "$RESTART_ROOT/services-before.json"
      assert "$id" true "$(jq -ns --slurpfile before "$RESTART_ROOT/services-before.json" --slurpfile after "$RESTART_ROOT/services-after.json" '$before[0].mcp != $after[0].mcp')" "$RESTART_ROOT/services-after.json"
    fi
    if poll restarted-fleet-ready 30 python3 "$HERE/fixtures/machine-restart/restore.py" ready; then
      RESTART_ASSERT_ID="$id" _restart_call "$JOURNEY_OS+$HERDR_KIND+project+claude" "$JOURNEY_OS+tmux+project+codex" "$slug" || { fail 'restarted MCP exchange failed'; exit 1; }
      observe "$OWNER"
      ending_action "$id" lifecycle.restart typed
      pass
    else fail 'restart did not preserve the ready carried fleet'; fi
  else fail 'restart receipt or supervised unit state unreadable'; fi
fi
test ! -e "$JOURNEY_OUT/$JOURNEY_LANE.failed"

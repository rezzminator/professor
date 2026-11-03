#!/usr/bin/env bash
# Restarts retain earlier identities, then exercise the resulting delivery paths.
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
. "$HERE/lib.sh"
. "$HERE/fixtures/chat-ending/driver.sh"
. "$HERE/fixtures/chat-mesh/entry.sh"
export JOURNEY_LANE=machine-restart JOURNEY_BRANCH="${JOURNEY_OS:?runner must supply JOURNEY_OS}"
export RESTART_ROOT="$JOURNEY_ROOT/machine-restart"
mkdir -p "$RESTART_ROOT"
export OWNER="$RESTART_ROOT/owner.json"
jq '.owner' "$JOURNEY_CARRY" >"$OWNER" || exit 2
export JOURNEY_TARGET="$OWNER"
case "$JOURNEY_OS" in
  linux) HERDR_KIND=herdr-shield ;;
  macos) HERDR_KIND=herdr-native ;;
  *) printf 'machine-restart unsupported OS: %s\n' "$JOURNEY_OS" >&2; exit 2 ;;
esac
export HERDR_KIND
. "$HERE/fixtures/machine-restart/call.sh"
_restart_actors() {
  local terminal role
  for terminal in "$HERDR_KIND" tmux; do
    for role in new-from-picker new-from-cli; do
      printf '%s\t%s\n' "$JOURNEY_OS+$terminal+project+claude" "$role"
    done
  done
  printf '%s\t%s\n' "$JOURNEY_OS+tmux+project+codex" new-from-picker
}
_restart_ready() {
  local branch role
  while IFS=$'\t' read -r branch role; do
    _restart_select "$branch" "$role" "$RESTART_ROOT/ready.json" &&
      python3 "$HERE/chat.py" need "$branch" "$RESTART_ROLE" >/dev/null || return "$?"
  done < <(_restart_actors)
}
_restart_reopen() {
  local branch origin result target
  while IFS=$'\t' read -r branch origin; do
    python3 "$HERE/chat.py" reopen "$OWNER" "$branch" "$origin" || return 2
  done < <(jq -r '.chats[] | select(.branch and .origin and (.expect.lifecycle // "live") == "live") | [.branch,(.role // .origin)] | @tsv' "$JOURNEY_CARRY")
  while IFS=$'\t' read -r branch origin; do
    result=0
    target="$RESTART_ROOT/$branch-$origin.json"
    _restart_select "$branch" "$origin" "$target" || result=$?
    case "$result" in
      0) python3 "$HERE/fixtures/chat-ending/lifecycle.py" expect "$target" live || return 2 ;;
      1) ;;
      *) return "$result" ;;
    esac
    python3 "$HERE/chat.py" ensure-role "$branch" "$RESTART_ROLE" "restart-$branch-$origin" "$HERE/fixtures/chat-birth/scenario.json" >"$target" || return 2
  done < <(_restart_actors)
  poll restart-chats-ready 30 _restart_ready
}
need restart-live-fleet _restart_ready _restart_reopen || exit 2
_restart_select "$JOURNEY_OS+tmux+project+codex" new-from-picker "$RESTART_ROOT/tmux-chat.json" || exit 2
jq '.terminal' "$RESTART_ROOT/tmux-chat.json" >"$RESTART_ROOT/tmux-target.json"

_restart_observation() { python3 "$HERE/chat.py" snapshot "$OWNER" "$1"; }
_restart_service_state() { python3 "$HERE/fixtures/machine-restart/control.py" snapshot "$RESTART_ROOT/services-after.json"; }

if [ "$JOURNEY_OS" = linux ]; then
  export JOURNEY_BRANCH="$JOURNEY_OS+$HERDR_KIND+project+claude"
  if beat forecast-seat-proof chat.whoami-cli; then
    ID=chat.whoami-cli
    SEAT_CHAT="$RESTART_ROOT/seat-proof-chat.json" SEAT_PREFIX="$JOURNEY_OUT/beats/$PFM_BEAT/whoami"
    if _restart_select "$JOURNEY_BRANCH" new-from-picker "$SEAT_CHAT" &&
       mesh_shell "$SEAT_CHAT" "$SEAT_PREFIX" pfm chat whoami --json &&
       poll seat-proof-receipt 20 mesh_command_ready; then
      mesh_input_finish "$SEAT_PREFIX"
      assert "$ID" 0 "$(cat "$SEAT_PREFIX.status")" "$SEAT_PREFIX.status"
      python3 "$HERE/fixtures/fleet-reader/projection.py" identity "$SEAT_PREFIX.out" "$SEAT_CHAT" >"$SEAT_PREFIX.checks.json"
      ending_assertions "$ID" "$SEAT_PREFIX.checks.json"
      assert "$ID" "$(jq -r '.seat_id' "$SEAT_CHAT")" "$(jq -r '.seat_id' "$SEAT_PREFIX.out")" "$SEAT_PREFIX.out"
      observe
      assert "$ID" "$(jq -r --arg sid "$(jq -r '.session_id' "$SEAT_CHAT")" '.chats[] | select(.status.session_id == $sid) | .terminal.identity.workspace' "$JOURNEY_OBSERVATION")" \
        "$(jq -r '.herdr_workspace' "$SEAT_PREFIX.out")" "$SEAT_PREFIX.out"
      pass
    else fail 'actual engine shell did not return its native seat proof'; fi
  fi
  export JOURNEY_BRANCH="$JOURNEY_OS"
fi

bash "$HERE/fixtures/machine-restart/walk.sh" herdr herdr-restart-restores
if [ "$JOURNEY_OS" = linux ]; then
  bash "$HERE/fixtures/machine-restart/walk.sh" herdr restore-renames-carried-tabs
fi
if beat after-restore-call-delivers chat.call-mcp; then
  if _restart_call "$JOURNEY_OS+$HERDR_KIND+project+claude" "$JOURNEY_OS+tmux+project+codex" herdr-to-tmux &&
     _restart_call "$JOURNEY_OS+tmux+project+codex" "$JOURNEY_OS+$HERDR_KIND+project+claude" tmux-to-herdr; then
    observe
    ending_action chat.call-mcp chat.call mcp
    pass
  else fail 'a restored caller did not receive pong'; fi
fi
if beat tmux-server-gone tmux.server-stopped; then
  if python3 "$HERE/fixtures/machine-restart/control.py" stopped-picker "$OWNER"       "$(jq -r '.terminal.socket' "$RESTART_ROOT/tmux-chat.json")" "$RESTART_ROOT/stopped-picker.json"; then
    assert tmux.server-stopped 'kill -CONT' "$(jq -r '.warning' "$RESTART_ROOT/stopped-picker.json")" "$RESTART_ROOT/stopped-picker.json"
    assert tmux.server-stopped true "$(jq '.elapsed_ms <= 10000' "$RESTART_ROOT/stopped-picker.json")" "$RESTART_ROOT/stopped-picker.json"
    if _restart_observation "$RESTART_ROOT/before-tmux.json" &&
       ending_command "$OWNER" tmux-stop tmux -S "$(jq -r '.terminal.socket' "$RESTART_ROOT/tmux-chat.json")" kill-server; then
      python3 "$HERE/fixtures/chat-ending/lifecycle.py" expect "$RESTART_ROOT/tmux-chat.json" ended "$RESTART_ROOT/before-tmux.json"
      observe
      assert tmux.server-stopped dead "$(jq -r --arg name "$(jq -r '.name' "$RESTART_ROOT/tmux-chat.json")" '.chats[] | select(.name == $name) | .status.state' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
      ending_action tmux.server-stopped picker.open typed
      pass
    else fail 'owned tmux server did not stop'; fi
  else fail 'picker did not render its bounded warning during the scoped tmux stop'; fi
fi
tmux_role="$(jq -r '.role // .origin' "$RESTART_ROOT/tmux-chat.json")"
python3 "$HERE/fixtures/chat-ending/lifecycle.py" expect "$RESTART_ROOT/tmux-chat.json" live &&
  python3 "$HERE/chat.py" reopen "$OWNER" "$JOURNEY_OS+tmux+project+codex" "$tmux_role" &&
  poll tmux-restored-before-machine-restart 30 python3 "$HERE/chat.py" need "$JOURNEY_OS+tmux+project+codex" "$tmux_role" &&
  python3 "$HERE/chat.py" refresh "$JOURNEY_OS+tmux+project+codex" "$tmux_role" >"$RESTART_ROOT/tmux-chat.json" || exit 2
bash "$HERE/fixtures/machine-restart/walk.sh" daemons daemons-restart
if [ "$JOURNEY_OS" = linux ]; then
  python3 "$HERE/fixtures/machine-restart/scenario.py" pruned forecast-multiplexer-herdr-runtime-event-version-pruned
  python3 "$HERE/fixtures/machine-restart/scenario.py" indicators forecast-herdr-indicators-on-runtime-event-herdr-server-stop
  python3 "$HERE/fixtures/machine-restart/scenario.py" active-timer forecast-service-manager-systemd-user-unit-state-active-before-stop-trigger-unit-timer-up
  python3 "$HERE/fixtures/machine-restart/scenario.py" preference forecast-supervisor-state-outlived-preference-change-engine-claude-code-seat-state-revived-on-restart
  python3 "$HERE/fixtures/machine-restart/scenario.py" stopped-mcp forecast-unit-state-before-stopped-mcp-daemon-supervised-or-by-hand-service-manager-systemd
else
  python3 "$HERE/fixtures/machine-restart/scenario.py" pruned forecast-seat-restart-native-seat-relaunch-engine-claude-code-recorded-binary-pruned-version
fi
_restart_route() {
  local slug="$1" caller="$2" callee="$3" route="$4"
  export JOURNEY_BRANCH="$JOURNEY_OS+$caller+project+claude+to-$callee-project-claude"
  if ! beat "$slug" chat.call-mcp; then return; fi
  local action=(bash "$HERE/fixtures/machine-restart/route.sh" "$JOURNEY_OS+$caller+project+claude" "$JOURNEY_OS+$callee+project+claude" "$slug")
  if [ "$route" = local-fallback ]; then
    python3 "$HERE/fixtures/machine-restart/control.py" scoped-unit mcp inactive "${action[@]}" || fail "local fallback or service restoration failed: $slug"
  elif _restart_service_state; then
    "${action[@]}" || fail "daemon proxy failed: $slug"
  else fail 'daemon proxy requires the real running MCP service'; fi
}
_restart_route mcp-route-herdr-to-herdr-local-fallback "$HERDR_KIND" "$HERDR_KIND" local-fallback
_restart_route mcp-route-herdr-to-herdr-daemon-proxy "$HERDR_KIND" "$HERDR_KIND" daemon-proxy
_restart_route mcp-route-herdr-to-tmux-local-fallback "$HERDR_KIND" tmux local-fallback
_restart_route mcp-route-herdr-to-tmux-daemon-proxy "$HERDR_KIND" tmux daemon-proxy
_restart_route mcp-route-tmux-to-herdr-local-fallback tmux "$HERDR_KIND" local-fallback
_restart_route mcp-route-tmux-to-herdr-daemon-proxy tmux "$HERDR_KIND" daemon-proxy
_restart_route mcp-route-tmux-to-tmux-local-fallback tmux tmux local-fallback
_restart_route mcp-route-tmux-to-tmux-daemon-proxy tmux tmux daemon-proxy
export JOURNEY_BRANCH="$JOURNEY_OS"
test ! -e "$JOURNEY_OUT/$JOURNEY_LANE.failed"

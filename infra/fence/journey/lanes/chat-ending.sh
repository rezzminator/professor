#!/usr/bin/env bash
# Exit and resume each carried origin before moving to the next leaf.
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
. "$HERE/lib.sh"
. "$HERE/fixtures/chat-ending/driver.sh"
. "$HERE/fixtures/chat-ending/history.sh"
. "$HERE/fixtures/chat-ending/archive-live.sh"
. "$HERE/fixtures/chat-ending/cycles.sh"
. "$HERE/fixtures/chat-mesh/entry.sh"
export JOURNEY_LANE=chat-ending JOURNEY_BRANCH="${JOURNEY_OS:?runner must supply JOURNEY_OS}"
END_ROOT="$JOURNEY_ROOT/chat-ending"
mkdir -p "$END_ROOT"
OWNER="$END_ROOT/owner.json"
jq '.owner' "$JOURNEY_CARRY" >"$OWNER" || exit 2
export JOURNEY_TARGET="$OWNER"

_ending_engine() {
  ENGINE="$1"
  CHAT="$END_ROOT/${JOURNEY_BRANCH//+/-}.json" TARGET="$END_ROOT/${JOURNEY_BRANCH//+/-}-target.json"
  local prelude_ok=1
  for ORIGIN in new-typed new-from-cli new-from-picker; do
    need "$JOURNEY_BRANCH:$ORIGIN" _ending_existing _ending_create || prelude_ok=0
  done
  if [ "$prelude_ok" -eq 0 ]; then printf 'chat-ending prelude incomplete: %s\n' "$JOURNEY_BRANCH" >&2; fi
  if [ "$PLACE" = project ]; then
    _ending_run exit-typed chat.exit-typed end chat.exit typed scout
  fi
  _ending_run exit-native chat.exit-native end chat.exit hook new-typed
  if [ "$PLACE" != project ]; then
    _ending_run end-from-cli chat.end-cli end chat.end typed new-from-cli
    _ending_run resume-from-picker chat.resume-picker resume chat.resume picker new-from-picker
    return
  fi
  _ending_run resume-after-native-exit-cli chat.open-cli resume chat.resume typed new-typed
  _ending_run kill-from-cli chat.kill-cli kill chat.kill typed new-typed
  _ending_run unkill-from-cli chat.unkill-cli unkill chat.unkill typed new-typed
  _ending_run resume-after-cli-kill-mcp chat.open-mcp resume chat.open-mcp mcp new-typed
  _ending_run kill-from-mcp chat.kill-mcp kill chat.kill mcp new-typed
  _ending_run unkill-from-mcp chat.unkill-mcp unkill chat.unkill-mcp mcp new-typed
  _ending_run resume-after-mcp-kill-picker chat.resume-picker resume chat.resume picker new-typed
  _ending_run kill-hides-from-picker chat.kill-picker kill chat.kill picker new-typed
  _ending_run unkill-from-picker chat.unkill-picker unkill chat.unkill picker new-typed
  _ending_run resume-after-picker-hide-cli chat.open-cli resume chat.resume typed new-typed
  _ending_run end-from-cli chat.end-cli end chat.end typed new-typed
  _ending_run history-after-end chat.open-cli resume chat.resume typed new-typed
  if [ "$JOURNEY_OS+$TERMINAL+$ENGINE" = linux+tmux+claude ]; then
    _ending_archive_live || return 2
  fi
  if [ "$JOURNEY_OS+$TERMINAL+$ENGINE" = linux+herdr-shield+codex ]; then
    _ending_branch forecast-spawn-request-codex-home-unset-codex-account-home-non-default-launch-path-chat-branch-spawn
  fi
  _ending_branch
  _ending_recover
  _ending_archive || { printf 'archive policy prelude failed: %s\n' "$JOURNEY_BRANCH" >&2; return 2; }
  _ending_run exit-from-mcp chat.exit-mcp end chat.exit mcp new-from-picker
  _ending_run resume-from-picker chat.resume-picker resume chat.resume picker new-from-picker
  _ending_run resume-from-cli chat.open-cli resume chat.resume typed new-from-picker
  if [ "$ENGINE" = claude ]; then _ending_run clear-hides-old-conversation chat.clear clear chat.clear hook new-from-picker; fi
  case "$JOURNEY_OS+$TERMINAL+$ENGINE" in
    linux+herdr-shield+claude)
      if beat forecast-seat-event-pane-reused-runtime-event-kill chat.kill-cli; then
        capability="$JOURNEY_OUT/beats/$PFM_BEAT/herdr-capability"
        if python3 "$HERE/faults.py" herdr-capabilities "$capability" >"$capability.json"; then
          assert chat.kill-cli true "$(jq '.isolated_recycle' "$capability.json")" external-gap:herdr-pane-reuse
          observe "$OWNER"
          ending_finish external-gap:herdr-pane-reuse 'Pinned Herdr has no isolated pane recycle, respawn or restart door'
        else fail 'real Herdr pane capability evidence unreadable'; fi
      fi
      _ending_run forecast-multiplexer-herdr-runtime-event-kill chat.kill-cli kill chat.kill typed new-from-picker kill-from-cli
      _ending_run forecast-multiplexer-herdr-runtime-event-clear chat.clear clear chat.clear hook new-from-picker clear-hides-old-conversation
      _ending_dead new-from-picker || return 2
      _ending_run forecast-seat-event-seat-dead-runtime-event-kill chat.kill-cli kill chat.kill typed new-from-picker kill-dead ;;
    macos+herdr-native+claude)
      _ending_run forecast-engine-event-claude-clear-herdr-seat-native-seat-name-source-pfm-chat-name chat.clear clear chat.clear hook new-from-picker clear-hides-old-conversation ;;
  esac
  if [ "$ENGINE" = opencode ]; then _ending_run kill-self-refused chat.kill-self-refused refused chat.kill mcp new-from-picker; fi
  if [ "$TERMINAL" != tmux ]; then
    _ending_run kill-lost-seat-from-picker chat.kill-lost-seat lost chat.kill picker new-from-picker
  elif [ "$ENGINE" = claude ]; then
    _ending_dead new-from-picker || return 2
    _ending_run resume-from-cli-over-ssh chat.open-ssh resume chat.resume typed new-from-picker
    if [ "$JOURNEY_OS" = linux ]; then
      _ending_dead new-from-picker || return 2
      _ending_run forecast-os-linux-caller-session-ssh-privsep-nondumpable-process-owner-foreign-uid chat.open-ssh resume chat.resume typed new-from-picker resume-from-cli-over-ssh
      _ending_dead new-from-picker || return 2
      _ending_run forecast-transport-ssh-os-linux-process-sshd-nondumpable-ancestor chat.open-ssh resume chat.resume typed new-from-picker resume-from-cli-over-ssh
    fi
  fi
}
_ending_place() {
  PLACE="$1"
  if [ "$PLACE" = project ]; then fork engine 'claude codex opencode' _ending_engine
  else fork engine claude _ending_engine; fi
}
_ending_terminal() {
  TERMINAL="$1"
  if [ "$TERMINAL" = tmux ]; then fork place project _ending_place
  else fork place 'project subfolder worktree' _ending_place; fi
}
case "$JOURNEY_OS" in
  linux) fork terminal 'herdr-shield tmux' _ending_terminal ;;
  macos) fork terminal 'herdr-native tmux' _ending_terminal ;;
  *) printf 'chat-ending unsupported OS: %s\n' "$JOURNEY_OS" >&2; exit 2 ;;
esac
test ! -e "$JOURNEY_OUT/$JOURNEY_LANE.failed"

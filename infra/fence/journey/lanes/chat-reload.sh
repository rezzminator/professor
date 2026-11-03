#!/usr/bin/env bash
# Every reload entry stays in its carried terminal, then attention checks its hooks.
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
. "$HERE/lib.sh"
. "$HERE/fixtures/chat-reload/flow.sh"
export JOURNEY_LANE=chat-reload
FIXTURE="$HERE/fixtures/chat-reload"
. "$FIXTURE/entries.sh"
: "${JOURNEY_OS:?runner must select linux or macos}"

_reload_births() { jq -c '(.terminal // .) | [.births[], (.shield | objects | .births[]) | {pid,start}]' "$1"; }

_reload_ready() {
  if [ "$DOOR" = typed ]; then
    python3 "$HERE/terminal.py" screen "$JOURNEY_TARGET" >"$ARTIFACT/typed-screen.txt" || return 2
    python3 "$FIXTURE/projection.py" typed-receipt "$ARTIFACT/typed-screen.txt" >"$ARTIFACT/typed-candidate.json" || return 2
    if jq -e '. != null' "$ARTIFACT/typed-candidate.json" >/dev/null; then
      cp "$ARTIFACT/typed-candidate.json" "$ARTIFACT/typed-receipt.json" || return 2
      jq -r '.text' "$ARTIFACT/typed-receipt.json" >"$ARTIFACT/typed-receipt.txt" || return 2
    fi
  fi
  lane_refresh || return "$?"
  lane_state idle "$ARTIFACT/status.json" || return "$?"
  python3 "$HERE/terminal.py" read "$JOURNEY_TARGET" >"$ARTIFACT/terminal.json" || return 2
  python3 "$FIXTURE/projection.py" reload-ready "$ARTIFACT/before.json" \
    "$ARTIFACT/terminal.json" "$ARTIFACT/before-chat.json" >"$ARTIFACT/engine-ready.json" || return 2
  jq -e '. == true' "$ARTIFACT/engine-ready.json" >/dev/null || return 1
  [ "$DOOR" != typed ] || test -s "$ARTIFACT/typed-receipt.txt"
}
_reload_shell() {
  PYTHONPATH="$HERE" python3 - "$ENGINE" "$ARTIFACT/command.json" "$@" <<'PY' || return 2
import shlex, sys
from runtime import write_json
from terminal import cli_command
tool, argument = {"claude": ("Bash", "command"), "codex": ("exec_command", "cmd"),
                  "opencode": ("bash", "command")}[sys.argv[1]]
receipt = sys.argv[2].removesuffix(".json")
command = cli_command(sys.argv[3:]) if sys.argv[3] == "pfm" else shlex.join(sys.argv[3:])
line = (command + " >" + shlex.quote(receipt + ".out")
        + " 2>&1; printf '%s\\n' \"$?\" >" + shlex.quote(receipt + ".status"))
write_json(sys.argv[2], [{"type": "tool_call", "tool": tool,
                         "input": {argument: line}},
                        {"type": "turn", "reply": "reload command returned", "busy_ms": 1}])
PY
  lane_input "$ARTIFACT/command.json" 'reload this chat'
}
_reload_invariants() {
  local mode="$1"
  set -- "$ARTIFACT/before.json" "$JOURNEY_OBSERVATION" "$ARTIFACT/before-chat.json" "$CHAT" "$mode"
  if [ "$mode" = continuation ]; then set -- "$@" "$ARTIFACT/handoff-index-after.json"; fi
  observe || return 2
  python3 "$FIXTURE/projection.py" reload "$@" >"$ARTIFACT/checks.json" || return 2
  lane_assert_checks "$ARTIFACT/checks.json" "$JOURNEY_OBSERVATION" || return "$?"
  assert "$ID" "$(jq -c '.terminal.identity' "$ARTIFACT/before.json")" \
    "$(jq -c '.terminal.identity' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
}
_reload_assert() { _reload_invariants "$1" && lane_action chat.reload "$DOOR"; }
_reload_receipt() { test -s "$ARTIFACT/command.status"; }
_reload_background_ready() {
  test -s "$ARTIFACT/background.pid" || return 1
  python3 "$HERE/terminal.py" screen "$JOURNEY_TARGET" >"$ARTIFACT/background-screen.txt" || return 2
  awk '$0 == "❯" || $0 ~ /^❯ +$/ {ready=1} END {exit !ready}' "$ARTIFACT/background-screen.txt"
}
_reload_background_start() {
  printf '%s\n' held >"$ARTIFACT/background.hold" || return 2
  PYTHONPATH="$HERE:$FIXTURE" python3 - "$ARTIFACT" <<'PY' || return 2
from pathlib import Path
import sys
from background import background_command
from runtime import write_json
directory = Path(sys.argv[1])
command = background_command(directory / "background.hold", directory / "background.pid")
write_json(directory / "background.json", [
    {"type": "background_agent", "name": "reload-researcher", "agent_type": "general-purpose",
     "input": {"command": command}},
    {"type": "turn", "reply": "background ready", "busy_ms": 1}])
PY
  lane_input "$ARTIFACT/background.json" 'research it in background' || return 2
  poll idle-composer-with-live-child 20 _reload_background_ready || return "$?"
  python3 "$HERE/chat.py" snapshot "$JOURNEY_TARGET" "$ARTIFACT/background-before.json" >/dev/null || return 2
  PYTHONPATH="$HERE" python3 - "$ARTIFACT" "$SID" <<'PY'
from pathlib import Path
import sys
from runtime import read_json, write_json
directory = Path(sys.argv[1])
pid = int((directory / "background.pid").read_text())
chat = next(chat for chat in read_json(directory / "background-before.json")["chats"] if chat["status"]["session_id"] == sys.argv[2])
births = [row for row in chat["process_births"] if row["pid"] == pid]
if len(births) != 1:
    raise ValueError(f"RELOAD-BACKGROUND-BIRTH: expected one live child pid={pid}; actual={births}")
write_json(directory / "background-birth.json", {key: births[0][key] for key in ("pid", "start")})
PY
}
_reload_background_end() {
  local presence
  presence="$(PYTHONPATH="$HERE:$FIXTURE" python3 - "$ARTIFACT/background-birth.json" <<'PY'
import sys
from observation import process_snapshot
from projection import birth_presence
from runtime import read_json
print(birth_presence(read_json(sys.argv[1]), process_snapshot()))
PY
)" || return 2
  rm "$ARTIFACT/background.hold" || return 2
  assert "$ID" ended "$presence" "$ARTIFACT/background-birth.json"
}
_reload_refusal() {
  _reload_shell pfm chat reload --account 9 && poll missing-account-refused 10 _reload_receipt || return "$?"
  poll refusal-keeps-chat-idle 10 lane_state idle "$ARTIFACT/status.json" || return "$?"
  lane_refresh && _reload_assert refused || return "$?"
  assert "$ID" 2 "$(cat "$ARTIFACT/command.status")" "$ARTIFACT/command.status"
  assert "$ID" "$BEFORE_BIRTHS" "$(_reload_births "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
  local accounts
  accounts="$(python3 - "$ARTIFACT/command.out" <<'PY'
import json, re, sys
from pathlib import Path
print(json.dumps(sorted({int(value) for value in re.findall(r"\b\d+\b", Path(sys.argv[1]).read_text())} - {9}), separators=(",", ":")))
PY
)" || return 2
  RELOAD_GAP_COMPLETE=1
  assert "$ID" '[1,2,3]' "$accounts" product-gap:reload-missing-account-roster
}
_reload_rejection() {
  python3 "$HERE/terminal.py" screen "$JOURNEY_TARGET" >"$ARTIFACT/rejection-screen.txt" || return 2
  awk '$0 == "Unrecognized command '\''/reload'\''. Type \"/\" for a list of supported commands." {found=1} END {exit !found}' "$ARTIFACT/rejection-screen.txt"
}
_reload_prompt() {
  PYTHONPATH="$HERE" python3 - "$CHAT" "$ARTIFACT" "$1" "${PROMPT_OFFSET:-0}" <<'PY'
from pathlib import Path
import sys
from runtime import read_json, write_text
path = Path(read_json(sys.argv[1])["records"]) / "prompts"
try:
    content = path.read_bytes()
except FileNotFoundError:
    content = b""
if sys.argv[3] == "before":
    print(len(content))
else:
    lines = content[int(sys.argv[4]):].decode().splitlines()
    if not lines:
        sys.exit(1)
    if lines[0] != "/reload":
        raise ValueError(f"RELOAD-INPUT: expected /reload, actual={lines[0]!r}, source={path}")
    write_text(Path(sys.argv[2]) / "reload-input.txt", lines[0] + "\n")
PY
}
_reload_missing() {
  case "$1" in
    reload-typed)
      DOOR=typed
      PROMPT_OFFSET="$(_reload_prompt before)" || return 2
      python3 "$HERE/terminal.py" type "$JOURNEY_TARGET" /reload || return 2
      poll native-reload-input 20 _reload_prompt after || return "$?"
      assert "$ID" /reload /reload "engine-input:$ARTIFACT/reload-input.txt" || return "$?"
      if [ "$ENGINE" = codex ]; then poll native-reload-rejection 10 _reload_rejection || return "$?"; fi ;;
    reload-from-picker)
      DOOR=picker
      python3 "$HERE/chat.py" pick "$PICKER" "$CHAT_BRANCH" "$CHAT_ORIGIN" ctrl+o >/dev/null || return 2 ;;
    reload-fresh) _reload_shell pfm chat reload --new || return 2 ;;
    reload-fresh-hidden) _reload_shell pfm chat reload --new --hide || return 2 ;;
    *) _reload_shell pfm chat reload || return 2 ;;
  esac
  case "$1" in
    reload-typed|reload-from-picker) ;;
    *)
      poll unsupported-command-returned 20 _reload_receipt || return "$?"
      assert "$ID" 2 "$(cat "$ARTIFACT/command.status")" "$ARTIFACT/command.status" ;;
  esac
  poll missing-entry-keeps-engine 10 lane_state idle "$ARTIFACT/status.json" || return "$?"
  lane_refresh && _reload_invariants refused || return "$?"
  RELOAD_GAP_COMPLETE=1
  assert "$ID" replaced "$(if [ "$(_reload_births "$JOURNEY_OBSERVATION")" = "$BEFORE_BIRTHS" ]; then printf preserved; else printf replaced; fi)" product-gap:reload-entry
}
_reload_metadata_visible() {
  lane_refresh && lane_state idle "$ARTIFACT/metadata-status.json" || return "$?"
  python3 "$HERE/terminal.py" screen "$JOURNEY_TARGET" >"$ARTIFACT/metadata-screen.txt" || return 2
  python3 "$FIXTURE/projection.py" metadata-visible "$ARTIFACT/metadata-status.json" "$CHAT" \
    "$ARTIFACT/metadata-screen.txt" "$1" >"$ARTIFACT/metadata-visible.json" || return 2
  jq -e '. == true' "$ARTIFACT/metadata-visible.json" >/dev/null
}
_reload_settle() {
  lane_refresh && lane_state idle "$ARTIFACT/metadata-status.json" || return "$?"
  python3 "$FIXTURE/projection.py" settled-metadata "$ARTIFACT/metadata-status.json" "$CHAT" \
    >"$ARTIFACT/metadata-settled.json" || return 2
  if jq -e '. == true' "$ARTIFACT/metadata-settled.json" >/dev/null; then return 0; fi
  python3 "$HERE/chat.py" snapshot "$JOURNEY_TARGET" "$ARTIFACT/metadata-before.json" >/dev/null || return 2
  local name="$NAME"
  if [ -z "$name" ]; then
    name="reload-${CHAT_BRANCH//+/-}-$PFM_RUN"
    python3 "$HERE/terminal.py" type "$JOURNEY_TARGET" "/rename $name" || return 2
  fi
  PYTHONPATH="$HERE" python3 - "$ARTIFACT/metadata-turn.json" <<'PY' || return 2
import sys
from runtime import write_json
write_json(sys.argv[1], [{"type": "turn", "reply": "reload metadata ready", "busy_ms": 1}])
PY
  lane_input "$ARTIFACT/metadata-turn.json" 'prepare reload metadata' || return 2
  poll reload-metadata-visible 20 _reload_metadata_visible "$name" || return "$?"
  python3 "$HERE/chat.py" snapshot "$JOURNEY_TARGET" "$ARTIFACT/metadata-after.json" >/dev/null || return 2
  python3 "$FIXTURE/projection.py" metadata-checks "$ARTIFACT/metadata-before.json" \
    "$ARTIFACT/metadata-after.json" "$SID" "$ENGINE" "$name" >"$ARTIFACT/metadata-checks.json" || return 2
  lane_assert_checks "$ARTIFACT/metadata-checks.json" "$ARTIFACT/metadata-after.json"
}
_reload_before() {
  if [ -z "${1:-}" ]; then _reload_settle || return "$?"; fi
  python3 "$HERE/chat.py" annotate "$CHAT_BRANCH" "$CHAT_ORIGIN" "$FIXTURE/reload-observation.json" >"$CHAT" || return 2
  cp "$CHAT" "$ARTIFACT/before-chat.json" || return 2
  if [ -n "${1:-}" ]; then cp "$1" "$ARTIFACT/before.json" || return 2
  else
    python3 "$HERE/chat.py" pick "$PICKER" "$CHAT_BRANCH" "$CHAT_ORIGIN" >/dev/null || return 2
    python3 "$HERE/chat.py" snapshot "$JOURNEY_TARGET" "$ARTIFACT/before.json" >/dev/null || return 2
  fi
  BEFORE_BIRTHS="$(_reload_births "$ARTIFACT/before.json")" || return 2
  DOOR=cli
}
_reload_complete() {
  local mode="$1" action="${2:-reload-from-cli}"
  poll reloaded-engine-birth 20 _reload_ready || return "$?"
  _reload_assert "$mode" || return "$?"
  if [ "$action" = reload-typed-with-background-work ]; then _reload_background_end || return "$?"; fi
  case "$action" in
    reload-typed|reload-after-retired-seat|reload-typed-with-background-work)
      python3 "$FIXTURE/projection.py" typed-receipt-log "$ARTIFACT/typed-receipt.json" \
        "$JOURNEY_OBSERVATION" "$SID" "$PFM_SID_DIR" >"$ARTIFACT/typed-log-checks.json" || return 2
      lane_assert_checks "$ARTIFACT/typed-log-checks.json" "$ARTIFACT/typed-receipt.json" || return "$?"
      golden "$ID" chat-reload/typed-receipt.txt "$ARTIFACT/typed-receipt.txt" || return "$?"
      assert "$ID" "$(jq -c --arg sid "$SID" '.chats[] | select(.status.session_id==$sid) | .stored.transcript.entries' "$ARTIFACT/before.json")" \
        "$(jq -c --arg sid "$SID" '.chats[] | select(.status.session_id==$sid) | .stored.transcript.entries' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION" ;;
  esac
  if [ "$mode" = account ]; then
    assert report.statusline '🥈' "$(jq -r '.terminal.text | split("\n") | map(select(startswith("🥈"))) | map(.[0:1]) | unique | join("")' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
  fi
}
_reload_run() {
  local action="$1" mode="${3:-resume}" requested_target="${2:-$SID}"
  if [ "$action" = reload-typed-with-background-work ]; then _reload_background_start || return "$?"; fi
  if [ "$mode" = continuation ]; then _reload_before "$ARTIFACT/handoff-before.json"; else _reload_before; fi || return "$?"
  if [ "$ENGINE" = opencode ] || { [ "$ENGINE" = codex ] && [ "$action" = reload-typed ]; }; then
    _reload_missing "$action"; return "$?"
  fi
  case "$action" in
    reload-typed|reload-after-retired-seat|reload-typed-with-background-work)
      DOOR=typed
      python3 "$HERE/terminal.py" type "$JOURNEY_TARGET" /reload || return 2 ;;
    reload-from-cli) _reload_shell pfm chat reload || return 2 ;;
    reload-fresh) mode=fresh; _reload_shell pfm chat reload --new || return 2 ;;
    reload-fresh-hidden) mode=fresh-hidden; _reload_shell pfm chat reload --new --hide || return 2 ;;
    reload-into-account) mode=account; _reload_shell pfm chat reload --account 2 || return 2 ;;
    reload-from-picker)
      DOOR=picker
      python3 "$HERE/chat.py" pick "$PICKER" "$CHAT_BRANCH" "$CHAT_ORIGIN" ctrl+o >/dev/null || return 2 ;;
    reload-into-missing-account-refused) _reload_refusal; return "$?" ;;
    reload-continued-chat) _reload_shell pfm chat reload "$requested_target" || return 2 ;;
    *) return 3 ;;
  esac
  _reload_complete "$mode" "$action"
}
_reload_row_action() {
  case "$1" in *-background-handoff) _reload_handoff; return "$?" ;; esac
  if [ "$1" = switch-account-keeps-quota-cache-private ]; then _reload_quotas; return "$?"; fi
  if [ "$1" = reload-continued-chat ]; then _reload_continue; return "$?"; fi
  if [ "$ENGINE" = opencode ] || { [ "$ENGINE" = codex ] && [ "$1" = reload-typed ]; }; then
    GAP_SOURCE=product-gap:reload-entry
    GAP_REASON="$ENGINE has no supported $1 owner entry"
  fi
  if [ -n "$3" ]; then
    case "$3" in
      pfm-herdr-tmux/C16)
        _reload_run reload-from-cli || return "$?"
        local capability
        python3 "$HERE/faults.py" herdr-capabilities "$ARTIFACT/herdr-capability" >"$ARTIFACT/herdr-capability.json" || return 2
        capability="$(jq -er '.isolated_recycle | tostring' "$ARTIFACT/herdr-capability.json")" || return 2
        if [ "$capability" = true ]; then
          printf 'HERDR-RECYCLE: isolated owner entry now exists; require its actual fixture walk\n' >&2
          return 2
        fi
        GAP_SOURCE=external-gap:herdr-pane-recycle
        GAP_REASON='Herdr 0.9.5 has no isolated pane recycle owner entry'
        RELOAD_GAP_COMPLETE=1
        assert "$ID" true "$capability" "$GAP_SOURCE"
        return "$?" ;;
      pfm-herdr-tmux/C11|darwin-linux-launchd-systemd-proc-sysctl-setsid/C59) _reload_continue; return "$?" ;;
      darwin-linux-launchd-systemd-proc-sysctl-setsid/C58) _reload_lost_index_isolated; return "$?" ;;
      pfm-herdr-tmux/C32|darwin-linux-launchd-systemd-proc-sysctl-setsid/C75) ;;
      *) return 3 ;;
    esac
    _reload_run reload-from-cli || return "$?"
    assert "$ID" herdr "$(jq -r '.terminal.backend' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
    if [ "$3" = darwin-linux-launchd-systemd-proc-sysctl-setsid/C75 ]; then
      assert "$ID" 'n/a:native-pane' "$(jq -r '.terminal.shield' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
      assert "$ID" 'herdr-native' "$(jq -r '.terminal_kind' "$CHAT")" "$CHAT"
    fi
    return "$?"
  fi
  if [ "$1" = reload-into-missing-account-refused ]; then
    GAP_SOURCE=product-gap:reload-missing-account-roster
    GAP_REASON='missing-account refusal omits configured accounts 1, 2 and 3'
  fi
  _reload_run "$1"
}
_reload_row() {
  local rc
  RELOAD_GAP_COMPLETE=0
  _reload_row_action "$@"; rc=$?
  if [ "$RELOAD_GAP_COMPLETE" -ne 1 ] || [ "$rc" -ne 1 ]; then GAP_SOURCE='' GAP_REASON=''; fi
  return "$rc"
}
_reload_leaf() { ENGINE="$1"; lane_walk _reload_row; }
_reload_place() {
  case "$1" in
    project) fork engine 'claude codex opencode' _reload_leaf ;;
    subfolder|worktree) fork engine claude _reload_leaf ;;
  esac
}
_reload_terminal() {
  case "$1" in
    tmux) fork place project _reload_place ;;
    herdr-shield|herdr-native) fork place 'project subfolder worktree' _reload_place ;;
  esac
}
_reload_os() {
  case "$1" in
    linux) fork terminal 'herdr-shield tmux' _reload_terminal ;;
    macos) fork terminal 'herdr-native tmux' _reload_terminal ;;
    *) printf 'RELOAD-OS: unsupported %s\n' "$1" >&2; return 2 ;;
  esac
}
if [ "${BASH_SOURCE[0]}" = "$0" ]; then lane_run "$JOURNEY_OS" _reload_os; fi

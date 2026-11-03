#!/usr/bin/env bash
# Early machine lifecycle. The destructive final half belongs after chat-ending.
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
. "$HERE/lib.sh"
machine_lifecycle_early() {
export JOURNEY_LANE=machine-lifecycle JOURNEY_BRANCH="${JOURNEY_OS:?runner must supply JOURNEY_OS}"
export JOURNEY_TARGET="$JOURNEY_ROOT/machine-target.json"
MAIN="$JOURNEY_ROOT/main-target.json"
_machine_ready() { test -s "$JOURNEY_CARRY" && test -x "$HOME/.local/bin/pfm"; }
_machine_create() { bash "$HERE/provision.sh" install-previous && python3 "$HERE/bootstrap.py" boot; }
need installed-previous-release _machine_ready _machine_create || exit 2
jq '.owner' "$JOURNEY_CARRY" >"$JOURNEY_TARGET"
jq '.chats[] | select(.name == "carried-chat") | .terminal' "$JOURNEY_CARRY" >"$MAIN"
# Staging binaries is a prelude; installation itself is typed into the owner shell.
bash "$HERE/provision.sh" stage-current || exit 2
python3 "$HERE/terminal.py" type "$JOURNEY_TARGET" "cd '$HOME/.professor'" || exit 2

_machine_receipt() { test -s "$RECEIPT.status"; }
_machine_action() {
  RECEIPT="$JOURNEY_OUT/beats/$PFM_BEAT/command"
  if [ "$1" = pfm ] && [ "${2:-}" = install ]; then
    poll installer-jobs-idle 30 python3 "$HERE/machine.py" install-idle || return 2
  fi
  python3 "$HERE/terminal.py" receipt "$JOURNEY_TARGET" "$RECEIPT" "$@" &&
    poll command-completed 90 _machine_receipt
}
_machine_stats_ready() { test -f "$HOME/.cc/1/stats-cache.json" && test ! -L "$HOME/.cc/1/stats-cache.json"; }
_machine_observe_action() {
  local id="$1" action="$2" door="$3"
  observe "${4:-$JOURNEY_TARGET}"
  assert "$id" "$action@$door" "$(python3 "$HERE/activity.py" observed-action "$JOURNEY_OBSERVATION" "$action" "$door")" "$JOURNEY_OBSERVATION"
}
_machine_untouched() {
  local id="$1"
  python3 "$HERE/machine.py" carried "$JOURNEY_OBSERVATION" >"$JOURNEY_ROOT/carried-after.json" || return 2
  assert "$id" "$(cat "$JOURNEY_ROOT/carried-before.json")" "$(cat "$JOURNEY_ROOT/carried-after.json")" carry:before-install
}
_machine_gap() {
  if python3 "$HERE/machine.py" gap-only "$JOURNEY_OUT/${JOURNEY_STATE_LANE:-$JOURNEY_LANE}.state.json" "$1"; then
    known "$2"
  else pass; fi
}

if beat install-after-stats-replaced-link lifecycle.install-stats-link; then
  assert lifecycle.install-stats-link symlink "$(if test -L "$HOME/.cc/1/stats-cache.json"; then printf symlink; else printf not-symlink; fi)" account-stats-before
  INPUT_OFFSET=0
  if [ -f "$JOURNEY_ROOT/mock-records/prompts" ]; then INPUT_OFFSET="$(wc -c <"$JOURNEY_ROOT/mock-records/prompts")"; fi
  if python3 "$HERE/terminal.py" type "$MAIN" /stats && poll stats-file-replaced 10 _machine_stats_ready; then
    assert lifecycle.install-stats-link regular-file "$(if _machine_stats_ready; then printf regular-file; else printf not-regular-file; fi)" engine-state:regular-file
    INPUT_RECEIPT="$JOURNEY_OUT/beats/$PFM_BEAT/engine-input.txt"
    tail -c "+$((INPUT_OFFSET + 1))" "$JOURNEY_ROOT/mock-records/prompts" >"$INPUT_RECEIPT" || _journey_state error 'engine input record unreadable'
    assert lifecycle.install-stats-link /stats "$(cat "$INPUT_RECEIPT")" "engine-input:$INPUT_RECEIPT"
    golden lifecycle.install-stats-link stats-cache.json "$HOME/.cc/1/stats-cache.json"
    observe "$MAIN"
    python3 "$HERE/machine.py" carried "$JOURNEY_OBSERVATION" >"$JOURNEY_ROOT/carried-before.json" || _journey_state error 'carried chat unreadable'
    pass
  else fail '/stats did not replace the account link with the recorded fixture'; fi
fi

if beat install-over-live-chat lifecycle.install; then
  if _machine_action pfm install --yes; then
    assert lifecycle.install 0 "$(cat "$RECEIPT.status")" "$RECEIPT.status"
    LIVE_SID="$(jq -er '.chats[] | select(.name == "carried-chat") | .session_id' "$JOURNEY_CARRY")"
    assert lifecycle.install "$LIVE_SID" "$(python3 - "$RECEIPT.out" "$LIVE_SID" <<'PY'
from pathlib import Path
import sys
lines = [line for line in Path(sys.argv[1]).read_text().splitlines() if 'has live chats' in line]
print(','.join(sorted({word for line in lines for word in line.split() if word == sys.argv[2]})))
PY
)" product-gap:live-chat-deferral-id
    # Report and store evidence is captured in the same bundle, including the carried chat.
    assert lifecycle.install "$HOME/.local/bin/herdr-shield" \
      "$(python3 -c 'import os,tomllib;print(tomllib.load(open(os.path.join(os.environ["HOME"],".config/herdr/config.toml"),"rb"))["terminal"]["default_shell"])')" installed-config
    assert lifecycle.install "$HOME/.local/share/pfm/install/bin/pfm-herdr-shell" \
      "$(jq -r '.shell' "$HOME/.config/herdr-shield/config.json")" installed-shield-config
    assert lifecycle.install "$HOME/.local/share/pfm/install/bin/claude" "$(readlink "$HOME/.local/bin/claude")" installed-launcher
    RECEIPT="$JOURNEY_OUT/beats/$PFM_BEAT/version"
    if python3 "$HERE/terminal.py" receipt "$JOURNEY_TARGET" "$RECEIPT" pfm version && poll installed-version 5 _machine_receipt; then
      assert lifecycle.install "pfm $(cat "$JOURNEY_TREE/VERSION")" "$(cat "$RECEIPT.out")" "$RECEIPT.out"
    else _journey_state error 'installed version unreadable'; fi
    python3 "$HERE/machine.py" installed >"$JOURNEY_ROOT/installed.json" || _journey_state error 'installed projection unreadable'
    _machine_observe_action lifecycle.install lifecycle.install typed
    _machine_untouched lifecycle.install || _journey_state error 'carried chat changed during install'
    _machine_gap product-gap:live-chat-deferral-id 'installer live-chat deferral names the account, not the live session id'
  else fail 'typed install did not complete'; fi
fi

if beat install-again-changes-nothing lifecycle.install-again; then
  if _machine_action pfm install --yes; then
    assert lifecycle.install-again 0 "$(cat "$RECEIPT.status")" "$RECEIPT.status"
    python3 "$HERE/machine.py" installed >"$JOURNEY_ROOT/installed-again.json" || _journey_state error 'reinstalled projection unreadable'
    assert lifecycle.install-again "$(jq -c '.files' "$JOURNEY_ROOT/installed.json")" \
      "$(jq -c '.files' "$JOURNEY_ROOT/installed-again.json")" carry:installed-managed-hashes
    assert lifecycle.install-again "$(jq -c --arg unit "$PFM_UNIT_PREFIX-mcp.service" '.units | del(.[$unit])' "$JOURNEY_ROOT/installed.json")" \
      "$(jq -c --arg unit "$PFM_UNIT_PREFIX-mcp.service" '.units | del(.[$unit])' "$JOURNEY_ROOT/installed-again.json")" carry:other-unit-births
    if [ "$JOURNEY_OS" = linux ]; then
      MCP_UNIT="$PFM_UNIT_PREFIX-mcp.service"
      assert lifecycle.install-again different "$(if [ "$(jq -r '.mcp.pid' "$JOURNEY_ROOT/installed.json")" != "$(jq -r '.mcp.pid' "$JOURNEY_ROOT/installed-again.json")" ]; then printf different; else printf same; fi)" product:lifecycle.install-again-mcp-pid
      assert lifecycle.install-again different "$(if [ "$(jq -r '.mcp.start' "$JOURNEY_ROOT/installed.json")" != "$(jq -r '.mcp.start' "$JOURNEY_ROOT/installed-again.json")" ]; then printf different; else printf same; fi)" product:lifecycle.install-again-mcp-start
      assert lifecycle.install-again different "$(if [ "$(jq -r '.mcp.receipt' "$JOURNEY_ROOT/installed.json")" != "$(jq -r '.mcp.receipt' "$JOURNEY_ROOT/installed-again.json")" ]; then printf different; else printf same; fi)" product:lifecycle.install-again-mcp-manager-receipt
      assert lifecycle.install-again "$(jq -r '.mcp.restarts' "$JOURNEY_ROOT/installed.json")" "$(jq -r '.mcp.restarts' "$JOURNEY_ROOT/installed-again.json")" product:lifecycle.install-again-mcp-no-automatic-restart
      assert lifecycle.install-again 1 "$(grep -Fxc "  ok      systemctl --user restart $MCP_UNIT" "$RECEIPT.out" || true)" "$RECEIPT.out"
      assert lifecycle.install-again active "$(if grep -Fqx 'ActiveState=active' <(python3 -c 'import json,sys; print(json.load(sys.stdin)["units"][sys.argv[1]])' "$MCP_UNIT" <"$JOURNEY_ROOT/installed-again.json"); then printf active; else printf inactive; fi)" product:lifecycle.install-again-mcp-serving
    else
      MCP_UNIT="gui/$(id -u)/com.professor.$PFM_UNIT_PREFIX.mcp"
      assert lifecycle.install-again "$(jq -r --arg unit "$MCP_UNIT" '.units[$unit]' "$JOURNEY_ROOT/installed.json")" \
        "$(jq -r --arg unit "$MCP_UNIT" '.units[$unit]' "$JOURNEY_ROOT/installed-again.json")" product:lifecycle.install-again-mcp-launchd-policy
    fi
    _machine_observe_action lifecycle.install-again lifecycle.install typed
    _machine_untouched lifecycle.install-again || _journey_state error 'carried chat changed during reinstall'
    python3 "$HERE/machine.py" serving >"$JOURNEY_ROOT/reinstalled-serving.json" || _journey_state error 'reinstalled MCP reachability unreadable'
    assert lifecycle.install-again "[\"http://127.0.0.1:$JOURNEY_MCP_PORT\"]" "$(jq -c '.mcp_endpoint' "$JOURNEY_ROOT/reinstalled-serving.json")" "$JOURNEY_ROOT/reinstalled-serving.json"
    pass
  else fail 'second typed install did not complete'; fi
fi

if beat doctor-reads-clean ops.doctor; then
  if _machine_action pfm doctor; then
    assert ops.doctor 0 "$(cat "$RECEIPT.status")" "$RECEIPT.status"
    python3 "$HERE/machine.py" doctor "$RECEIPT.out" >"$JOURNEY_ROOT/doctor.json" || _journey_state error 'doctor projection unreadable'
    assert ops.doctor '[]' "$(jq -c '.errors' "$JOURNEY_ROOT/doctor.json")" "$RECEIPT.out"
    assert ops.doctor '["claude","codex","herdr","mcp","opencode"]' "$(jq -c '.families' "$JOURNEY_ROOT/doctor.json")" "$RECEIPT.out"
    assert ops.doctor "[\"$HOME/.local/bin/herdr-shield\"]" "$(jq -c '.herdr_shell' "$JOURNEY_ROOT/doctor.json")" "$RECEIPT.out"
    assert ops.doctor "[\"http://127.0.0.1:$JOURNEY_MCP_PORT\"]" "$(jq -c '.mcp_endpoint' "$JOURNEY_ROOT/doctor.json")" "$RECEIPT.out"
    assert ops.doctor '["claude[1]","claude[2]","claude[3]","codex[1]","opencode"]' "$(jq -c '.hook_accounts' "$JOURNEY_ROOT/doctor.json")" product-gap:healthy-hook-rows
    _machine_observe_action ops.doctor ops.doctor typed
    _machine_gap product-gap:healthy-hook-rows 'doctor emits hook faults but no healthy hook row per engine account'
  else fail 'typed doctor did not complete'; fi
fi

_machine_chat_state() {
  python3 "$HERE/machine.py" chat-state "$MAIN_SID" "$1" "$JOURNEY_OUT/beats/$PFM_BEAT/state-$1.json"
}
if beat live-chat-adopts-release lifecycle.live-chat-upgrade; then
  MAIN_SID="$(jq -er '.chats[] | select(.name == "carried-chat") | .session_id' "$JOURNEY_CARRY")"
  if python3 "$HERE/terminal.py" type "$MAIN" journey-upgrade-check &&
     poll upgraded-chat-working 10 _machine_chat_state working && poll upgraded-chat-idle 10 _machine_chat_state idle; then
    _machine_observe_action lifecycle.live-chat-upgrade chat.prompt typed "$MAIN"
    for state in working idle; do
      assert lifecycle.live-chat-upgrade "$state" "$(jq -r '.state' "$JOURNEY_OUT/beats/$PFM_BEAT/state-$state.json")" "$JOURNEY_OUT/beats/$PFM_BEAT/state-$state.json"
    done
    assert lifecycle.live-chat-upgrade "$MAIN_SID" "$(jq -r \
      '[.activity.attributed[] | select(.action == "chat.prompt") | .session] | unique | join(",")' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
    assert lifecycle.live-chat-upgrade "$(cat "$JOURNEY_TREE/VERSION")" "$(jq -r \
      '[.activity.attributed[] | select(.action == "chat.prompt") | .version] | unique | join(",")' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
    assert lifecycle.live-chat-upgrade '{"role":"assistant","text":"journey-upgrade-ok"}' "$(jq -c \
      '.chats[] | select(.name == "carried-chat") | .stored.transcript.entries[-1] | {role,text}' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
    assert lifecycle.live-chat-upgrade '["🥇 ◇ Sonnet·effort unknown","atlas","🌿 main","🔖 carried-chat"]' "$(jq -c \
      '.terminal.text | split("\n")[] | select(startswith("🥇 ")) | split("│")[0:4] | map(gsub("^ +| +$";""))' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
    pass
  else fail 'the previous-release chat did not complete its new turn'; fi
fi
python3 "$HERE/bootstrap.py" observer || return 2
test ! -e "$JOURNEY_OUT/${JOURNEY_STATE_LANE:-$JOURNEY_LANE}.failed"
}

machine_lifecycle_final() {
  export JOURNEY_LANE=machine-lifecycle JOURNEY_BRANCH="${JOURNEY_OS:?runner must supply JOURNEY_OS}"
  export JOURNEY_TARGET="$JOURNEY_ROOT/final-owner.json"
  local fixture="$HERE/fixtures/machine-lifecycle/projection.py" receipt_root="$JOURNEY_ROOT/final-prelude"
  local root_foreign="$JOURNEY_ROOT/foreign-before-install.json"
  . "$HERE/fixtures/chat-ending/driver.sh"
  jq '.owner' "$JOURNEY_CARRY" >"$JOURNEY_TARGET" || return 2
  mkdir -p "$receipt_root" || return 2
  _final_ready() { python3 "$fixture" ended "$JOURNEY_CARRY" "$receipt_root/status.json"; }
  _final_end() {
    local chat sid target receipt
    python3 "$HERE/chat.py" snapshot "$JOURNEY_TARGET" "$receipt_root/before.json" || return 2
    while IFS= read -r chat; do
      sid="$(jq -r '.session_id' <<<"$chat")"
      target="$receipt_root/$sid.json" receipt="$receipt_root/$sid"
      printf '%s\n' "$chat" >"$target"
      if python3 "$HERE/fixtures/chat-ending/lifecycle.py" state "$target" dead "$receipt.state.json"; then continue; fi
      python3 "$HERE/terminal.py" receipt "$JOURNEY_TARGET" "$receipt" pfm chat end "$sid" &&
        poll final-chat-ended 20 python3 "$HERE/fixtures/chat-ending/lifecycle.py" state "$target" dead "$receipt.state.json" || return 2
      python3 "$HERE/fixtures/chat-ending/lifecycle.py" expect "$target" ended "$receipt_root/before.json" || return 2
    done < <(jq -c '.chats[],.foreign | select(.expect.lifecycle != "archived")' "$JOURNEY_CARRY")
    python3 "$HERE/chat.py" snapshot "$JOURNEY_TARGET" "$receipt_root/ended.json" || return 2
    jq '.foreign' "$receipt_root/ended.json" >"$JOURNEY_OUT/foreign.json"
  }
  need ended-fleet _final_ready _final_end || return 2
  if beat doctor-after-chats-ended ops.doctor-after-ends; then
    if ending_command "$JOURNEY_TARGET" doctor pfm doctor; then
      assert ops.doctor-after-ends 0 "$(cat "$RECEIPT.status")" "$RECEIPT.status"
      if python3 "$fixture" crumbs "$RECEIPT.out" "$RECEIPT.crumbs.json"; then
        assert ops.doctor-after-ends "$(jq -c . "$HERE/goldens/machine-lifecycle/doctor-crumbs.json")" \
          "$(jq -c . "$RECEIPT.crumbs.json")" "$RECEIPT.crumbs.json"
        observe
        ending_action ops.doctor-after-ends ops.doctor typed
        pass
      else fail 'doctor did not report one readable crumb audit'; fi
    else fail 'typed doctor did not complete after chat ends'; fi
  fi
  _final_uninstall() {
  local slug="$1"
  if beat "$slug" lifecycle.uninstall; then
    if [ ! -s "$root_foreign" ]; then
      fail 'root did not retain the pre-install foreign-file manifest'
    elif python3 "$HERE/chat.py" snapshot "$JOURNEY_TARGET" "$JOURNEY_OUT/beats/$PFM_BEAT/before-uninstall.json" &&
         python3 "$fixture" managed "$HOME/.local/share/pfm/install" "$receipt_root/managed.json" &&
         ending_command "$JOURNEY_TARGET" uninstall pfm uninstall; then
        assert lifecycle.uninstall 0 "$(cat "$RECEIPT.status")" "$RECEIPT.status"
        if python3 "$fixture" remaining "$receipt_root/managed.json" "$RECEIPT.remaining.json" &&
           python3 "$fixture" foreign "$root_foreign" "$RECEIPT.foreign.json" &&
           python3 "$fixture" baseline "$root_foreign" "$RECEIPT.baseline.json" &&
           python3 "$fixture" settings "$HOME/.claude/settings.json" "$RECEIPT.settings.json"; then
          assert lifecycle.uninstall '[]' "$(jq -c . "$RECEIPT.remaining.json")" "$RECEIPT.remaining.json"
          assert lifecycle.uninstall "$(jq -cS . "$RECEIPT.baseline.json")" "$(jq -cS . "$RECEIPT.foreign.json")" "$RECEIPT.foreign.json"
          [ "$(cat "$RECEIPT.status")" = 0 ] && python3 "$fixture" uninstalled "$JOURNEY_CARRY" "$JOURNEY_CARRY" || { fail 'uninstall did not finish; installed carry phase retained'; return 1; }
          python3 "$HERE/chat.py" snapshot "$JOURNEY_TARGET" "$JOURNEY_OUT/beats/$PFM_BEAT/after-uninstall.json" &&
            python3 "$fixture" uninstall-transition "$JOURNEY_OUT/beats/$PFM_BEAT/before-uninstall.json" "$JOURNEY_OUT/beats/$PFM_BEAT/after-uninstall.json" || { fail 'foreign sentinel uninstall transition refused'; return 1; }
          observe
          assert lifecycle.uninstall '{}' "$(jq -c '.machine.managed_files' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
          assert lifecycle.uninstall '[]' "$(jq -c '.machine.units' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
          python3 "$HERE/fixtures/machine-restart/control.py" loaded theme "$RECEIPT.follower.json" || { fail 'post-uninstall theme follower state unreadable'; return 1; }
          assert lifecycle.uninstall false "$(jq '.loaded' "$RECEIPT.follower.json")" "$RECEIPT.follower.json"
          ending_action lifecycle.uninstall lifecycle.uninstall typed
          python3 "$fixture" entries "$HOME" "$receipt_root/home-after-first.json" || { fail 'home inventory unreadable after uninstall'; return 1; }
          pass
        else fail 'uninstall preservation evidence unreadable'; fi
    else fail 'managed inventory or uninstall receipt unreadable'; fi
  fi
  }
  _final_uninstall uninstall-keeps-foreign
  local theme_slug
  case "$JOURNEY_OS" in
    linux) theme_slug=forecast-command-pfm-uninstall-surface-themed-terminal-and-editor-theme-sync-on ;;
    macos) theme_slug=forecast-service-manager-launchd-follower-loaded-uninstall-state-theme-sync-on-os-macos ;;
  esac
  if python3 "$HERE/chat.py" snapshot "$JOURNEY_TARGET" "$receipt_root/before-install.json" &&
     ending_command "$JOURNEY_TARGET" reinstall-for-theme pfm install --yes &&
     [ "$(cat "$RECEIPT.status")" = 0 ] && python3 "$fixture" installed "$JOURNEY_CARRY" "$JOURNEY_CARRY" &&
     python3 "$HERE/chat.py" snapshot "$JOURNEY_TARGET" "$receipt_root/after-install.json" &&
     python3 "$fixture" install-transition "$receipt_root/before-install.json" "$receipt_root/after-install.json" "$RECEIPT" &&
     ending_command "$JOURNEY_TARGET" theme-on pfm theme on --json &&
     [ "$(cat "$RECEIPT.status")" = 0 ] && jq -e '.enabled == true' "$RECEIPT.out" >/dev/null; then
    if [ "$JOURNEY_OS" = macos ]; then
      python3 "$HERE/fixtures/machine-restart/control.py" loaded theme "$receipt_root/theme-follower.json" &&
        jq -e '.loaded == true' "$receipt_root/theme-follower.json" >/dev/null || return 2
    fi
    _final_uninstall "$theme_slug"
  else printf 'final theme-on prelude did not establish actual installed sync state\n' >&2; return 2; fi
  if beat uninstall-again-changes-nothing lifecycle.uninstall-again; then
    if ending_command "$JOURNEY_TARGET" uninstall pfm uninstall &&
         python3 "$fixture" entries "$HOME" "$RECEIPT.entries.json"; then
        assert lifecycle.uninstall-again 0 "$(cat "$RECEIPT.status")" "$RECEIPT.status"
        assert lifecycle.uninstall-again "$(jq -c . "$receipt_root/home-after-first.json")" \
          "$(jq -c . "$RECEIPT.entries.json")" "$RECEIPT.entries.json"
        observe
        ending_action lifecycle.uninstall-again lifecycle.uninstall typed
        pass
    else fail 'second uninstall or home inventory unreadable'; fi
  fi
  test ! -e "$JOURNEY_OUT/${JOURNEY_STATE_LANE:-$JOURNEY_LANE}.failed"
}

# journey-end inserts its machine_lifecycle_final definition above this dispatch.
case "${1:-}" in
  --final) machine_lifecycle_final ;;
  '') machine_lifecycle_early ;;
  *) printf 'usage: machine-lifecycle.sh [--final]\n' >&2; exit 2 ;;
esac

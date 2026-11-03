#!/usr/bin/env bash
# Every terminal entry, depth-first; bash is an additional entry per backend.
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
. "$HERE/lib.sh"
export JOURNEY_LANE=terminal-open JOURNEY_BRANCH="${JOURNEY_OS:?runner must supply JOURNEY_OS}"
export JOURNEY_TARGET="$JOURNEY_ROOT/terminal-target.json"
CLIENT="$JOURNEY_ROOT/client-target.json"
_terminal_ready() {
  local release
  test -s "$JOURNEY_CARRY" && test -S "$HERDR_SOCKET_PATH" || return 1
  release="$(python3 "$HERE/machine.py" release)" || return 2
  test "$release" = "pfm $(cat "$JOURNEY_TREE/VERSION")"
}
_terminal_create() {
  bash "$HERE/provision.sh" install-current || return 2
  test -s "$JOURNEY_CARRY" || python3 "$HERE/bootstrap.py" boot
}
need picker-capable-terminals _terminal_ready _terminal_create || exit 2
HOME_WORKSPACE="$(jq -er '.workspaces["~"].id' "$JOURNEY_CARRY")" || exit 2
jq '.client' "$JOURNEY_CARRY" >"$CLIENT"
jq '.owner' "$JOURNEY_CARRY" >"$JOURNEY_TARGET"
python3 "$HERE/terminal.py" type "$JOURNEY_TARGET" "cd '$HOME'" || exit 2

_terminal_picker() { python3 "$HERE/terminal_state.py" picker-ready "$JOURNEY_TARGET"; }
_terminal_shell() { python3 "$HERE/terminal_state.py" shell-ready "$JOURNEY_TARGET"; }
_terminal_new_pane() { python3 "$HERE/terminal_state.py" new-pane "$HOME_WORKSPACE" "$JOURNEY_ROOT/panes-before.json" "$JOURNEY_TARGET"; }
_terminal_receipt() { test -s "$RECEIPT.status"; }
_terminal_assert_picker() {
  local id="$1" door="$2" projection
  projection="$(python3 "$HERE/terminal_state.py" picker "$JOURNEY_TARGET")" || return 2
  assert "$id" pfm "$(jq -r '.header' <<<"$projection")" rendered-picker
  assert "$id" '["atlas","beacon","cobalt"]' "$(jq -c '.projects' <<<"$projection")" rendered-picker
  assert "$id" 'New chat' "$(jq -r '.new_chat' <<<"$projection")" rendered-picker
  observe
  assert "$id" "picker.open@$door" "$(python3 "$HERE/activity.py" observed-action "$JOURNEY_OBSERVATION" picker.open "$door")" "$JOURNEY_OBSERVATION"
  jq '.terminal.identity' "$JOURNEY_OBSERVATION" >"$JOURNEY_ROOT/picker-identity.json"
}
_terminal_quit() {
  python3 "$HERE/terminal.py" keys "$JOURNEY_TARGET" escape && poll picker-parent-shell 10 _terminal_shell
}
_terminal_key() {
  python3 "$HERE/herdr.py" request "$HERDR_SOCKET_PATH" workspace.focus \
    "$(jq -nc --arg workspace "$HOME_WORKSPACE" '{workspace_id:$workspace}')" >/dev/null || return 2
  python3 "$HERE/terminal_state.py" panes "$HOME_WORKSPACE" "$JOURNEY_ROOT/panes-before.json" || return 2
  python3 "$HERE/terminal.py" keys "$CLIENT" ctrl+b "$1" && poll key-created-pane 10 _terminal_new_pane
}

_terminal_walk() {
  local backend="$1"
  if [ "$backend" != tmux ]; then
    if beat open-picker picker.open-herdr-tab; then
      if _terminal_key c && poll picker-auto-open 10 _terminal_picker; then
        _terminal_assert_picker picker.open-herdr-tab picker
        assert picker.open-herdr-tab "$HOME" \
          "$(python3 "$HERE/herdr.py" request "$HERDR_SOCKET_PATH" pane.get "$(jq '{pane_id:.pane}' "$JOURNEY_TARGET")" | jq -r '.result.pane.cwd')" herdr-pane
        assert picker.open-herdr-tab "$HOME_WORKSPACE" "$(jq -r '.terminal.identity.workspace' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
        if [ "$backend" = herdr-shield ]; then
          assert picker.open-herdr-tab "$HOME/.local/share/pfm/install/bin/pfm-herdr-shell" "$(jq -r '.terminal.shield.config.shell' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
          assert picker.open-herdr-tab '["-l"]' "$(jq -c '.terminal.shield.config.args' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
          assert picker.open-herdr-tab true "$(jq 'any(.terminal.shield.births[]; .chain[0].argv | test("/zsh( -l)+$"))' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
        else
          assert picker.open-herdr-tab n/a:native-pane "$(jq -r '.terminal.shield' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
          assert picker.open-herdr-tab true "$(jq 'any(.terminal.births[]; .chain[0].argv | test("/zsh( -l)+$"))' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
        fi
        assert picker.open-herdr-tab true "$(jq -e '.terminal.pane.focused and .terminal.tab.tab.focused and .terminal.workspace.workspace.focused' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
        pass
      else fail "new-tab key did not auto-open the picker through $backend"; fi
    fi
  else
    jq '.owner' "$JOURNEY_CARRY" >"$JOURNEY_TARGET"
    if beat open-picker picker.open-typed; then
      if python3 "$HERE/terminal.py" command "$JOURNEY_TARGET" pfm && poll typed-picker 10 _terminal_picker; then
        _terminal_assert_picker picker.open-typed typed
        pass
      else fail 'typed pfm did not open the picker in its tmux pane'; fi
    fi
  fi
  if beat open-picker-typed picker.open-typed; then
    _terminal_quit || { fail 'prior picker did not return to its shell'; return 1; }
    if python3 "$HERE/terminal.py" command "$JOURNEY_TARGET" pfm && poll typed-picker 10 _terminal_picker; then
      _terminal_assert_picker picker.open-typed typed
      pass
    else fail 'typed pfm did not open the picker in its shell pane'; fi
  fi
  if beat picker-quit-leaves-shell picker.quit; then
    if _terminal_quit; then
      RECEIPT="$JOURNEY_OUT/beats/$PFM_BEAT/pwd"
      if python3 "$HERE/terminal.py" receipt "$JOURNEY_TARGET" "$RECEIPT" pwd && poll pwd-receipt 5 _terminal_receipt; then
        assert picker.quit "$HOME" "$(cat "$RECEIPT.out")" "$RECEIPT.out"
        observe
        assert picker.quit 'picker.quit@picker' "$(python3 "$HERE/activity.py" observed-action "$JOURNEY_OBSERVATION" picker.quit picker)" "$JOURNEY_OBSERVATION"
        assert picker.quit "$(cat "$JOURNEY_ROOT/picker-identity.json")" \
          "$(jq '.terminal.identity' "$JOURNEY_OBSERVATION")" carry:picker-pane-identity
        pass
      else fail 'picker parent shell did not execute pwd'; fi
    else fail 'quitting picker did not restore the parent shell'; fi
  fi
  if beat open-picker-bash picker.open-bash; then
    if python3 "$HERE/terminal.py" command "$JOURNEY_TARGET" bash --login && poll bash-shell 10 _terminal_shell &&
       python3 "$HERE/terminal.py" command "$JOURNEY_TARGET" pfm && poll bash-picker 10 _terminal_picker; then
      _terminal_assert_picker picker.open-bash typed
      assert picker.open-bash true "$(jq 'any((.terminal.births + (if .terminal.shield | type == "object" then .terminal.shield.births else [] end))[]; .chain[0].argv | test("(^|/)bash --login$"))' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
      pass
    else fail 'bash did not open the picker'; fi
  fi
  if [ "$backend" != tmux ] && beat plain-shell-stays-plain picker.auto-open-off; then
    if _terminal_key C && poll plain-key-outcome 10 _terminal_entry_ready; then
      observe
      assert picker.auto-open-off shell "$(if _terminal_picker; then printf picker; else printf shell; fi)" "$JOURNEY_OBSERVATION"
      known 'pfm offers no plain-shell entry in Herdr; a new pane always arms the picker'
    else fail 'plain-shell key created no observable pane'; fi
  fi
}
_terminal_entry_ready() { _terminal_picker || _terminal_shell; }
case "$JOURNEY_OS" in
  linux) fork terminal 'herdr-shield tmux' _terminal_walk || exit 1 ;;
  macos) fork terminal 'herdr-native tmux' _terminal_walk || exit 1 ;;
  *) printf 'terminal-open unsupported OS: %s\n' "$JOURNEY_OS" >&2; exit 2 ;;
esac
test ! -e "$JOURNEY_OUT/${JOURNEY_STATE_LANE:-$JOURNEY_LANE}.failed"

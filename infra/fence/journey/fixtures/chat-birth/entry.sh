#!/usr/bin/env bash
# Birth owner entries share the public terminal and capture doors.
_birth_shell_present() {
  test -s "$SHELL_TARGET" || return 1
  python3 "$HERE/terminal.py" read "$SHELL_TARGET" >/dev/null || return 2
}
_birth_shell_create() {
  if _birth_cancel_factor; then
    cp "$OWNER" "$SHELL_TARGET"
  elif [ "${BIRTH_PLAIN:-0}" -eq 1 ]; then
    python3 "$HERE/terminal.py" create "$SHELL" "$START_CWD" >"$SHELL_TARGET"
  else
    python3 "$HERE/chat.py" shell "$JOURNEY_BRANCH" "$SHELL_TARGET" "$WORKSPACE" "$START_CWD" >/dev/null
  fi
}
_birth_tab_label() {
  local target="$1" label="$2" evidence="$3"
  python3 "$HERE/terminal.py" read "$target" >"$evidence" || return 2
  python3 "$HERE/herdr.py" request "$HERDR_SOCKET_PATH" tab.rename \
    "$(jq -c --arg label "$label" '{tab_id:.identity.tab,label:$label}' "$evidence")" >"$evidence.rename.json" || return 2
  python3 "$HERE/terminal.py" read "$target" >"$evidence"
}
_birth_prepare() {
  local origin="$1" scenario="$FIXTURE/scenario.json" BIRTH_PLAIN="${BIRTH_PLAIN:-0}"
  if _birth_waiter_factor; then BIRTH_PLAIN=1; fi
  case "$FACTORS" in login_default=sentinel_inherited,*) BIRTH_PLAIN=1 ;; esac
  SHELL_TARGET="$JOURNEY_OUT/birth/$JOURNEY_BRANCH/$origin/shell.json"
  mkdir -p "${SHELL_TARGET%/*}" || return 2
  jq '[.chats[].session_id]' "$JOURNEY_CARRY" >"${SHELL_TARGET%/*}/preserved-ids.json" || return 2
  python3 "$HERE/chat.py" snapshot "$OWNER" "${SHELL_TARGET%/*}/preserved-before.json" >/dev/null || return 2
  python3 "$FIXTURE/projection.py" preserved "${SHELL_TARGET%/*}/preserved-before.json" \
    "${SHELL_TARGET%/*}/preserved-ids.json" >"${SHELL_TARGET%/*}/preserved-before-projection.json" || return 2
  case "$SLUG" in *workspace-state-*)
    WORKSPACE_KEY="$JOURNEY_BRANCH:$SLUG:$SHELL_START"
    python3 "$HERE/workspace.py" need atlas "$WORKSPACE_KEY" "$WORKSPACE_STATE" "$START_CWD" >"${SHELL_TARGET%/*}/workspace-before.json" || return 2
    PROJECT="$(jq -er '.project' "${SHELL_TARGET%/*}/workspace-before.json")" || return 2
    CWD="$(jq -er '.cwd' "${SHELL_TARGET%/*}/workspace-before.json")" || return 2
    START_CWD="$(jq -er '.start_cwd' "${SHELL_TARGET%/*}/workspace-before.json")" || return 2
    WORKSPACE="$(jq -er '.id' "${SHELL_TARGET%/*}/workspace-before.json")" || return 2
    EXPECTED_WORKSPACE="$WORKSPACE"
    [ "$WORKSPACE_STATE" != mixed ] || EXPECTED_WORKSPACE="label:$PROJECT"
    [ "$ENTRY" != open-from-foreign-workspace ] || WORKSPACE=beacon
    need "birth-shell:$JOURNEY_BRANCH:$origin" _birth_shell_present _birth_shell_create || return 2 ;;
  *) need "birth-shell:$JOURNEY_BRANCH:$origin" _birth_shell_present _birth_shell_create || return 2 ;;
  esac
  if _birth_trust_factor || _birth_stdout_factor || _birth_waiter_factor; then scenario="$(boot_prepare "$FACTORS" "${SHELL_TARGET%/*}")" || return 2; fi
  if _birth_edge_factor; then scenario="$(edge_prepare "$FACTORS" "${SHELL_TARGET%/*}")" || return 2; fi
  if _birth_topology_factor; then scenario="$(topology_prepare "$FACTORS" "${SHELL_TARGET%/*}")" || return 2; fi
  if _birth_cancel_factor; then scenario="$(python3 "$FIXTURE/cancel.py" prepare "$FACTORS" "${SHELL_TARGET%/*}")" || return 2; fi
  python3 "$HERE/chat.py" scenario "$SHELL_TARGET" "$JOURNEY_BRANCH" "$origin" "$scenario" "$PROJECT" "$EXPECTED_WORKSPACE" >"${SHELL_TARGET%/*}/scenario.json" || return 2
  python3 "$HERE/terminal.py" command "$SHELL_TARGET" cd "$CWD" || return 2
  python3 "$HERE/chat.py" before "$SHELL_TARGET" "${SHELL_TARGET%/*}/before.json" || return 2
  python3 "$HERE/terminal.py" read "$SHELL_TARGET" >"${SHELL_TARGET%/*}/terminal-before.json" || return 2
  if [ "$origin" = new-typed-fresh-label ]; then
    _birth_tab_label "$SHELL_TARGET" 'Fresh Atlas' "${SHELL_TARGET%/*}/terminal-before.json" || return 2
  fi
  cp "$SHELL_TARGET" "$JOURNEY_TARGET"
}
_birth_workspace_assert() {
  local id="$1" phase="$2" observed expected
  [ -n "$WORKSPACE_KEY" ] || return 0
  observed="$JOURNEY_OUT/beats/$PFM_BEAT/workspace-$phase.json"
  python3 "$HERE/workspace.py" read atlas "$WORKSPACE_KEY" "$WORKSPACE_STATE" >"$observed" || return 2
  expected="$PROJECT"
  if [ "$phase" = before ] || [ "$WORKSPACE_STATE" = mixed ]; then
    expected="$(jq -er '.setup_label' "${SHELL_TARGET%/*}/workspace-before.json")" || return 2
  fi
  assert "$id" "$expected" "$(jq -r '.label' "$observed")" "$observed"
  if [ "$WORKSPACE_STATE" = mixed ]; then
    assert "$id" "$(jq -c '{pane:.foreign_pane,cwd:.foreign_cwd}' "$observed")" \
      "$(jq -c '.foreign_pane as $id | .panes | map(select(.pane_id == $id) | {pane:.pane_id,cwd:.foreground_cwd}) | if length == 1 then .[0] else . end' "$observed")" "$observed"
  fi
}
_birth_capture() {
  local origin="$1" rc
  python3 "$HERE/chat.py" capture "$SHELL_TARGET" "$JOURNEY_BRANCH" "$origin" \
    "${SHELL_TARGET%/*}/before.json" >"$CHAT.tmp"; rc=$?
  if [ "$rc" -eq 0 ]; then
    mv "$CHAT.tmp" "$CHAT" || return 2
    jq '.terminal' "$CHAT" >"$JOURNEY_TARGET" || return 2
  fi
  return "$rc"
}
_birth_input() { python3 "$HERE/terminal.py" "$@"; }
_birth_receipt_ready() { test -s "$RECEIPT.status"; }
_birth_new() {
  local origin="$1" identity="$2" entry="$3" id capture_rc typed=0 expected_name=''
  local -a prompt=()
  [ "$entry" != new-from-cli ] || expected_name="atlas-birth-$INDEX"
  if _birth_escape first-paint; then prompt=(atlas-first-paint); expected_name=atlas-first-paint; fi
  case "$entry" in new-typed|open-from-foreign-workspace) typed=1 ;; esac
  RECEIPT="$JOURNEY_OUT/beats/$PFM_BEAT/cli"
  if [ "$entry" = open-from-foreign-workspace ]; then
    assert "${identity%% *}" beacon "$(jq -r '.workspace.workspace.label' "${SHELL_TARGET%/*}/terminal-before.json")" \
      "foreign-caller-workspace:${SHELL_TARGET%/*}/terminal-before.json" || { fail 'foreign birth caller is not in beacon'; return 1; }
  fi
  if [ "$origin" = new-typed-fresh-label ]; then
    assert "${identity%% *}" 'Fresh Atlas' "$(jq -r '.tab.tab.label' "${SHELL_TARGET%/*}/terminal-before.json")" \
      "typed-fresh-label-before:${SHELL_TARGET%/*}/terminal-before.json" || { fail 'fresh tab label precondition differs'; return 1; }
  elif [ "$origin" = new-typed ] && [ "${TERMINAL#herdr-}" != "$TERMINAL" ]; then
    assert "${identity%% *}" "$(jq -r '.tab.tab.number' "${SHELL_TARGET%/*}/terminal-before.json")" \
      "$(jq -r '.tab.tab.label' "${SHELL_TARGET%/*}/terminal-before.json")" \
      "typed-default-number-before:${SHELL_TARGET%/*}/terminal-before.json" || { fail 'default tab number precondition differs'; return 1; }
  fi
  if _birth_escape alias; then _birth_alias "${identity%% *}" || { fail 'unknown Codex model refusal differs'; return 1; }; fi
  if _birth_edge_factor; then
    edge_before_entry "$FACTORS" "${SHELL_TARGET%/*}" "$SHELL_TARGET" >"$JOURNEY_OUT/beats/$PFM_BEAT/edge-before.json" || {
      fail 'native edge precondition/action failed'; return 1;
    }
  fi
  if _birth_topology_factor; then
    topology_before_entry "$FACTORS" "${SHELL_TARGET%/*}" "$SHELL_TARGET" >"$JOURNEY_OUT/beats/$PFM_BEAT/topology-before.json" || {
      fail 'native topology precondition could not be established'; return 1;
    }
  fi
  if _birth_waiter_factor; then _birth_waiter_fault "${identity%% *}" || { fail 'early-exit native launcher/waiter evidence differs'; return 1; }; fi
  if _birth_stdout_factor; then _birth_stdout_helper "${identity%% *}" || { fail 'public app-server helper did not complete and reap its stdout descendant'; return 1; }; fi
  if _birth_cancel_factor; then
    python3 "$FIXTURE/cancel.py" run "$FACTORS" "${SHELL_TARGET%/*}" "$SHELL_TARGET" "$JOURNEY_OUT/beats/$PFM_BEAT/cancellation.json" || {
      fail 'public cancellation/recovery could not be observed'; return 1;
    }
    jq '.recovery' "$JOURNEY_OUT/beats/$PFM_BEAT/cancellation.json" >"$CHAT" || return 2
    jq '.terminal' "$CHAT" >"$JOURNEY_TARGET" || return 2
    RECEIPT="${SHELL_TARGET%/*}/recovery"
    capture_rc=0
  elif _birth_escape permission; then
    RECEIPT="${SHELL_TARGET%/*}/permission-owner"
    python3 "$FIXTURE/escapes.py" permission-birth "${SHELL_TARGET%/*}" "$SHELL_TARGET" "$origin" "atlas-birth-$INDEX" >"$JOURNEY_OUT/beats/$PFM_BEAT/permission-projection.json" || {
      fail 'native permission-mode birth could not be observed'; return 1;
    }
    jq '.chat' "${SHELL_TARGET%/*}/permission-evidence.json" >"$CHAT" || return 2
    jq '.terminal' "$CHAT" >"$JOURNEY_TARGET" || return 2
    capture_rc=0
  else
  case "$entry" in
    new-from-picker) python3 "$HERE/chat.py" pick-new "$SHELL_TARGET" "$PROJECT" "$ENGINE" 1 1h ;;
    new-from-cli) _birth_input receipt "$SHELL_TARGET" "$RECEIPT" pfm chat new --engine "$ENGINE" --name "atlas-birth-$INDEX" "${prompt[@]}" ;;
    new-typed|open-from-foreign-workspace) _birth_input command "$SHELL_TARGET" "$ENGINE" ;;
    *) fail "unknown birth entry: $entry"; return 1 ;;
  esac || { fail "owner entry did not complete: $entry"; return 1; }
  if _birth_trust_factor; then
    boot_after_entry "$FACTORS" "${SHELL_TARGET%/*}" "$SHELL_TARGET" >"$JOURNEY_OUT/beats/$PFM_BEAT/boot-entry.json" || {
      fail 'native startup/trust ordering could not be observed'; return 1;
    }
  fi
  poll born-chat 20 _birth_capture "$origin"; capture_rc=$?
  fi
  if [ "$capture_rc" -ne 0 ]; then
    if [ "$capture_rc" -eq 1 ] && [ "$ENGINE" = opencode ] && { [ "$typed" -eq 1 ] || [ "$TERMINAL" = tmux ]; }; then
      observe || { fail 'missing OpenCode birth could not be observed'; return 1; }
      if [ "$typed" -eq 1 ]; then
        assert "${identity%% *}" FIXTURE-OPENCODE-PROMPT \
          "$(jq -r '.terminal.text | split("\n")[] | select(startswith("FIXTURE-OPENCODE-PROMPT")) | sub(" +$"; "")' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION" || {
            fail 'typed OpenCode did not reach its engine composer'; return 1;
          }
      elif [ "$entry" = new-from-cli ]; then
        poll refused-cli-receipt 5 _birth_receipt_ready &&
          assert "${identity%% *}" 2 "$(cat "$RECEIPT.status")" "$RECEIPT.status" &&
          assert "${identity%% *}" 'pfm chat new: OpenCode does not support headless chat' "$(cat "$RECEIPT.out")" "$RECEIPT.out" || {
            fail 'OpenCode CLI failure differs from the missing-backend refusal'; return 1;
          }
      else
        fail 'OpenCode picker did not produce a birth'; return 1
      fi
      assert "${identity%% *}" "$(jq -r '.scenario_session_id' "${SHELL_TARGET%/*}/scenario.json")" \
        "$(jq -sr 'map(.session_id) | join(",")' "$CHAT.tmp")" "$CHAT.tmp"
      known "OpenCode $entry has no supported $TERMINAL birth entry"
      return
    fi
    fail "owner entry produced no discoverable carried chat: $entry/$ENGINE"; return 1
  fi
  if [ "$entry" = new-from-cli ]; then
    poll completed-cli-receipt 5 _birth_receipt_ready || { fail 'birth CLI did not return its receipt'; return 1; }
    if _birth_trust_factor && [ "$TERMINAL" = tmux ]; then
      _birth_trust_cli "${identity%% *}" || { fail 'held trust dialog CLI receipt differs'; return 1; }
    else assert "${identity%% *}" 0 "$(cat "$RECEIPT.status")" "$RECEIPT.status"; fi
  fi
  case "$entry" in new-from-picker|new-typed|open-from-foreign-workspace)
    assert "${identity%% *}" '' "$(jq -r '.name' "$CHAT")" "$CHAT" ;;
  esac
  poll complete-birth-observation 15 _birth_snapshot_ready || { fail 'birth reports could not be read'; return 1; }
  if [ "$origin" = new-typed-owner-label ]; then
    _birth_owner_label "${identity%% *}" || { fail 'owner label did not survive native re-announce after first paint'; return 1; }
  fi
  if _birth_escape permission; then _birth_escape_compare "${identity%% *}" "${SHELL_TARGET%/*}/permission-evidence.json" || { fail 'native permission-mode arguments differ'; return 1; }; fi
  if _birth_escape title; then _birth_title "${identity%% *}" || { fail 'explicit Codex name changed on native title arrival'; return 1; }; fi
  if _birth_escape first-paint; then _birth_first_paint "${identity%% *}" || { fail 'OpenCode native first title was not painted'; return 1; }; fi
  if _birth_early_factor; then _birth_early_assert "${identity%% *}" || { fail 'native early rename was not preserved through registration'; return 1; }; fi
  if _birth_edge_factor; then _birth_edge_assert "${identity%% *}" || { fail 'native edge evidence differs'; return 1; }; fi
  if _birth_cancel_factor; then _birth_cancel_assert "${identity%% *}" || { fail 'cancelled birth left a process, pane or stored row'; return 1; }; fi
  if _birth_topology_factor; then _birth_topology_assert "${identity%% *}" || { fail 'native topology evidence differs'; return 1; }; fi
  if _birth_trust_factor; then _birth_trust_assert "${identity%% *}" || { fail 'native trust evidence differs'; return 1; }; fi
  observe || { fail 'birth bundle could not be read'; return 1; }
  for id in $identity; do _birth_assert "$id" "$expected_name" || { fail "birth observations failed: $id"; return 1; }; done
  _birth_workspace_assert "${identity%% *}" after || { fail 'workspace placement changed a foreign pane or label'; return 1; }
  python3 "$FIXTURE/projection.py" preserved "$JOURNEY_OBSERVATION" "${SHELL_TARGET%/*}/preserved-ids.json" \
    >"$JOURNEY_OUT/beats/$PFM_BEAT/preserved-after.json" || { fail 'earlier chat projection unreadable'; return 1; }
  assert "${identity%% *}" "$(cat "${SHELL_TARGET%/*}/preserved-before-projection.json")" \
    "$(cat "$JOURNEY_OUT/beats/$PFM_BEAT/preserved-after.json")" "${SHELL_TARGET%/*}/preserved-before.json"
  case "$entry" in new-from-picker) _birth_action "${identity%% *}" chat.new picker ;;
    new-from-cli) _birth_action "${identity%% *}" chat.new typed ;;
    *) _birth_action "${identity%% *}" hook.run hook ;;
  esac
  if _birth_stdout_factor; then _birth_action "${identity%% *}" ops.doctor typed; fi
  pass
}
_birth_owner_label() {
  local id="$1" directory="$JOURNEY_OUT/beats/$PFM_BEAT" sid record
  sid="$(jq -er '.session_id' "$CHAT")"; record="$(jq -er '.records' "$CHAT")/prompts"
  assert "$id" "${CWD##*/}" "$(jq -r '.terminal.tab.tab.label' "$JOURNEY_ROOT/birth-settled-chat.json")" \
    "typed-owner-label-first-paint:$JOURNEY_ROOT/birth-settled-chat.json" || return 1
  python3 "$FIXTURE/projection.py" engine-births "$JOURNEY_ROOT/birth-settled.json" "$CHAT" >"$directory/owner-first-birth.json" || return 2
  assert "$id" 1 "$(jq length "$directory/owner-first-birth.json")" "$directory/owner-first-birth.json" || return 1
  cp "$JOURNEY_ROOT/birth-settled.json" "$directory/owner-first-paint.json" && cp "$CHAT" "$directory/owner-prior-chat.json" || return 2
  _birth_tab_label "$JOURNEY_TARGET" 'Owner Atlas' "$directory/owner-label-edited.json" || return 2
  assert "$id" 'Owner Atlas' "$(jq -r '.tab.tab.label' "$directory/owner-label-edited.json")" "$directory/owner-label-edited.json" || return 1
  _birth_input type "$JOURNEY_TARGET" /reload && poll owner-label-reannounced 20 python3 "$HERE/chat.py" reload-ready \
    "$directory/owner-first-paint.json" "$directory/owner-prior-chat.json" "$JOURNEY_TARGET" "$directory/owner-reloaded-terminal.json" || return "$?"
  python3 "$FIXTURE/projection.py" reloaded "$directory/owner-first-paint.json" "$directory/owner-prior-chat.json" \
    "$directory/owner-reloaded-terminal.json" >"$directory/owner-reload-state.json" || return 2
  assert "$id" '{"engine_replaced": true}' "$(cat "$directory/owner-reload-state.json")" "$directory/owner-reload-state.json" || return 1
  poll owner-label-carry 10 _birth_refresh && poll owner-label-idle 10 _birth_snapshot_ready || return "$?"
  # The reload worker submits /exit to the old engine after the owner's /reload.
  assert "$id" $'/reload\n/exit' "$(cat "$record")" "engine-input:$record" || return 1
  assert "$id" "$sid" "$(jq -r '.session_id' "$CHAT")" "$CHAT"
}

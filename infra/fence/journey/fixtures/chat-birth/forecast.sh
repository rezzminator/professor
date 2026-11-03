#!/usr/bin/env bash
_birth_early_factor() { [ "$FACTORS" = rename_timing=before_session_registers,engine=claude_code,launch_mode=resume_in_place ]; }
_birth_early_assert() {
  local id="$1" evidence="$JOURNEY_OUT/beats/$PFM_BEAT/early-projection.json"
  python3 "$FIXTURE/early.py" run "$FACTORS" "${SHELL_TARGET%/*}" "$CHAT" >"$evidence" || return 2
  assert "$id" "$(python3 "$FIXTURE/early.py" expected "$FACTORS" | jq -cS .)" "$(jq -cS . "$evidence")" "$evidence"
}
_birth_cancel_factor() { [ "$FACTORS" = multiplexer=tmux,runtime_event=spawn_cancelled ]; }
_birth_cancel_assert() {
  local evidence="$JOURNEY_OUT/beats/$PFM_BEAT/cancellation.json"
  assert "$1" '{"actual_child_born":true,"cancelled_before_session_start":true,"owner_exit_status":"130","launcher_gone":true,"child_gone":true,"pane_gone":true,"seat_gone":true,"chat_gone":true,"launch_gone":true,"native_session_gone":true,"activity":"n/a:reporter-activity","recovery_captured":true}' \
    "$(jq -c '.projection | {actual_child_born,cancelled_before_session_start,owner_exit_status,launcher_gone,child_gone,pane_gone,seat_gone,chat_gone,launch_gone,native_session_gone,activity,recovery_captured}' "$evidence")" "$evidence"
}
_birth_topology_factor() {
  case "$FACTORS" in pane_command=compound_shell_wrapper,*|tmux_attach=update_environment_recopies,*) return 0 ;; *) return 1 ;; esac
}
_birth_topology_assert() {
  local id="$1" evidence="$JOURNEY_OUT/beats/$PFM_BEAT/topology-projection.json"
  topology_after_entry "$FACTORS" "${SHELL_TARGET%/*}" "$CHAT" >"$JOURNEY_OUT/beats/$PFM_BEAT/topology-after.json" || return 2
  topology_assert "$FACTORS" "${SHELL_TARGET%/*}" >"$evidence" || return 2
  assert "$id" "$(topology_expected "$FACTORS" | jq -cS .)" "$(jq -cS . "$evidence")" "$evidence"
}
_birth_waiter_factor() {
  case "$FACTORS" in last_pane=closed,backend=tmux,child_exit=before_waiter_connects|launcher_exit=before_wait_registered,backend=tmux_wait_for) return 0 ;; *) return 1 ;; esac
}
_birth_waiter_fault() {
  local id="$1" evidence="$JOURNEY_OUT/beats/$PFM_BEAT/waiter.json"
  python3 "$FIXTURE/waiter.py" "$FACTORS" "${SHELL_TARGET%/*}" "$SHELL_TARGET" "$evidence" || return 2
  assert "$id" '{"caller":"pty","exit_status":"23","done_waiter":"server-gone","same_waiter_birth":true}' \
    "$(jq -c '.projection | {caller,exit_status,done_waiter,same_waiter_birth}' "$evidence")" "$evidence"
}
_birth_edge_factor() {
  case "$FACTORS" in receipt_state=historical_fingerprint,*|trust_receipt=present,*|script_reference=dollar_zero_self_dir,*|login_default=sentinel_inherited,*) return 0 ;; *) return 1 ;; esac
}
_birth_edge_assert() {
  local id="$1" evidence="$JOURNEY_OUT/beats/$PFM_BEAT/edge-projection.json"
  edge_after_entry "$FACTORS" "${SHELL_TARGET%/*}" "$CHAT" >"$JOURNEY_OUT/beats/$PFM_BEAT/edge-after.json" || return 2
  edge_assert "$FACTORS" "${SHELL_TARGET%/*}" "$CHAT" >"$evidence" || return 2
  assert "$id" "$(edge_expected "$FACTORS" | jq -cS .)" "$(jq -cS . "$evidence")" "$evidence"
}
_birth_trust_factor() {
  case "$FACTORS" in trust_dialog_rows=unnumbered,*|launch_state=folder_trust_dialog,*) return 0 ;; *) return 1 ;; esac
}
_birth_stdout_factor() {
  [ "$FACTORS" = process_group=private_pgid,descendant_state=holds_stdout_pipe,helper=codex_app_server ]
}
_birth_stdout_helper() {
  local id="$1" helper="$JOURNEY_OUT/beats/$PFM_BEAT/app-server-helper" evidence="$JOURNEY_OUT/beats/$PFM_BEAT/stdout-projection.json"
  _birth_input receipt "$SHELL_TARGET" "$helper" pfm doctor || return 2
  boot_stdout_after_entry "$FACTORS" "${SHELL_TARGET%/*}" "$helper" >"$evidence" || return 2
  assert "$id" 0 "$(cat "$helper.status")" "$helper.status"
  assert "$id" '{"private_pgid":true,"actual_stdout_descendant":true,"stdout_gate_closed_at_completion":true,"stdout_child_reaped":true}' \
    "$(jq -c '{private_pgid,actual_stdout_descendant,stdout_gate_closed_at_completion,stdout_child_reaped}' "$evidence")" "$evidence"
}
_birth_trust_assert() {
  local id="$1" evidence="$JOURNEY_OUT/beats/$PFM_BEAT/boot-projection.json" confirm cancel
  boot_assert "$FACTORS" "${SHELL_TARGET%/*}" "$JOURNEY_TARGET" >"$evidence" || return 2
  case "$FACTORS" in trust_dialog_rows=unnumbered,*) confirm='❯ Yes, I trust this folder'; cancel='No, exit' ;;
    *) confirm='❯ 1. Yes, I trust this folder'; cancel='2. No, exit' ;;
  esac
  assert "$id" "$(jq -nc --arg confirm "$confirm" --arg cancel "$cancel" \
    '{native_session_captured:true,native_readiness_before_trust:true,modal_unchanged:true,confirm:$confirm,cancel:$cancel}')" \
    "$(jq -c '{native_session_captured,native_readiness_before_trust,modal_unchanged,confirm,cancel}' "$evidence")" "$evidence"
  if [ "$TERMINAL" = tmux ]; then assert "$id" on "$(jq -r '.held_remain_on_exit' "$evidence")" "$evidence"; fi
}
_birth_trust_cli() {
  local id="$1" socket expected
  socket="$(jq -er '.launch_socket | split("/")[-1]' "${SHELL_TARGET%/*}/scenario.json")" || return 2
  expected="pfm chat new: atlas-birth-$INDEX is held at Claude Code's folder-trust dialog for $CWD — pfm pressed nothing (Enter there selects \"No, exit\"); trust the folder yourself: tmux -L $socket attach -t $socket, choose \"Yes, I trust this folder\""
  assert "$id" 6 "$(cat "$RECEIPT.status")" "$RECEIPT.status"
  assert "$id" "$(jq -nc --arg line "$expected" '[$line]')" \
    "$(jq -Rsc 'split("\n") | map(select(startswith("pfm chat new: ")))' "$RECEIPT.out")" "$RECEIPT.out"
}
_birth_forecast_prepare() {
  case "$FACTORS" in
    multiplexer=herdr,runtime_event=rename|engine=codex,runtime_event=rename)
      python3 "$HERE/chat.py" ensure-role "$JOURNEY_BRANCH" "$SLUG" atlas-forecast "$FIXTURE/scenario.json" >"$CHAT" || return 2
      BIRTH_SELECTION="$SLUG" ;;
    multiplexer=herdr,chat_name=unnamed)
      _birth_prepare "$SLUG" && _birth_input command "$SHELL_TARGET" "$ENGINE" &&
        poll unnamed-forecast-chat 20 _birth_capture "$SLUG" || return 2
      BIRTH_SELECTION="$SLUG" ;;
    codex_home=ambient_real_home,engine=codex)
      _birth_input command "$SHELL_TARGET" mkdir -p "$HOME/ambient-real-home" &&
        _birth_input command "$SHELL_TARGET" export "CODEX_HOME=$HOME/ambient-real-home" || return 2
      RECEIPT="${SHELL_TARGET%/*}/ambient"
      _birth_input receipt "$SHELL_TARGET" "$RECEIPT" printenv CODEX_HOME &&
        poll ambient-codex-home 5 _birth_receipt_ready || return 2 ;;
  esac
}
_birth_forecast_assert() {
  local id="$1"
  case "$FACTORS" in
    codex_home=ambient_real_home,engine=codex)
      assert "$id" "$HOME/ambient-real-home" "$(cat "${SHELL_TARGET%/*}/ambient.out")" "${SHELL_TARGET%/*}/ambient.out" ;;
    multiplexer=herdr,chat_name=unnamed)
      _birth_select && _birth_snapshot_ready || return 2
      assert "$id" '[""]' "$(jq -c '.stored.chat | map(.label // "") | unique' "$JOURNEY_ROOT/birth-settled-chat.json")" "$JOURNEY_ROOT/birth-settled-chat.json"
      assert "$id" "${CWD##*/}" "$(jq -r '.terminal.tab.tab.label' "$JOURNEY_ROOT/birth-settled-chat.json")" \
        "typed-default-number-to-cwd-leaf:$JOURNEY_ROOT/birth-settled-chat.json" ;;
  esac
}

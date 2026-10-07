#!/usr/bin/env bash
# E1.sh — lane E1, Claude: one chat on one seat, walked depth-first from the
# spawn ceremony through the whole `/reload` matrix and every outside-in verb,
# and ended. Runs INSIDE a lane container (run.sh), never on a host.
#
#   run.sh --lanes E1            solo, from a fresh root
#   run.sh                       in the sequence, after O1
#
# Every beat asserts from pfm's OWN report (`pfm ls --tsv`, a verb's exit code,
# the chat's transcript) or from the pane, never from a model's
# prose: a beat that can only be satisfied by what the model said is a beat
# asserting the wrong thing. Beat ids are the contract in
# beats.md and map.tsv — check-map.sh fails when this file and those disagree.
#
# Cost: two fixture Claude seats and roughly 25 scripted turns.
#
# BROKEN STATE: the prelude aborts the lane by name when the seat it was told to
# spend is not configured or the chat cannot be opened; every later beat whose
# precondition beat failed reports `blocked-by`, and each ✗ carries the raw pane
# bytes in the lane log beside its assertion.
set -uo pipefail
LANES_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=lib.sh
. "$LANES_DIR/lib.sh"
lane_preamble

CHAT="${E1_CHAT:-E1_MAIN}"
ROLE_CHAT="${CHAT}_ROLE"
CWD="${E1_CWD:-/work/orbit}"
CONFIG="${PFM_CONFIG:?PFM_CONFIG is required in the container}"
SID_DIR="${PFM_SID_DIR:-${TMPDIR:-/tmp}/cc-sid}"
lane_seat_and_port "$CONFIG"

lane_begin E1

# ── prelude: what this lane needs, made when it is missing, no-op otherwise ──
lane_require_seat "$CONFIG"
ALT="$(jq -r --argjson want "$SEAT" '[.accounts[].id | select(. != $want)] | first // empty' "$CONFIG")"
SEAT_DIR="$(jq -r --argjson want "$SEAT" '.accounts[] | select(.id == $want) | .configDir' "$CONFIG")"
case "$SEAT_DIR" in "~"*) SEAT_DIR="$HOME${SEAT_DIR#\~}" ;; esac

need "the working directory $CWD" "[ -d '$CWD/.git' ]" \
  "mkdir -p '$CWD' && git -C '$CWD' init -q && git -C '$CWD' commit -q --allow-empty -m lane" ||
  lane_abort "no working directory for the chat to live in ($CWD)"
need "the pfm MCP daemon on :$PORT" \
  "[ \"\$(curl -s -o /dev/null -w '%{http_code}' -m 2 http://127.0.0.1:$PORT/mcp/professor)\" != 000 ]" \
  "lane_daemon_up" ||
  lane_abort "the professor MCP daemon never answered on :$PORT — no chat can call a chat_* tool"

# ─── E1.01 — the spawn ceremony ─────────────────────────────────────────────

# open_main — the lane's chat, opened the one way: E1.01 spawns it and the
# library's single re-open (lane_reopen) spends the same command after the
# chat dies under a later beat.
open_main() {
  pfm chat new --name "$CHAT" --engine cc --account "$SEAT" --cwd "$CWD" --await --timeout 300 \
    "You are $CHAT, the chat an automated Tier B lane drives. Reply with one word: ready. Then wait and do exactly what each next message says, nothing more." 2>&1
}
lane_reopen 'open_main'

beat E1.01-open-seat1
spends "cc:$SEAT"
target "$CHAT"
if live_chat "$CHAT"; then
  pass "$CHAT was already live (the sequence built it): kind $(live_field "$CHAT" 1) · account $(live_field "$CHAT" 9)"
else
  out="$(open_main)"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    fail "pfm chat new exited $rc: $(one_line "$out")"
  elif ! live_chat "$CHAT"; then
    fail "chat new exited 0 but no live row for $CHAT: $(one_line "$(pfm ls --plain)")"
  elif [ "$(live_field "$CHAT" 9)" != "$SEAT" ]; then
    fail "the row reports account $(live_field "$CHAT" 9), not the requested $SEAT"
  else
    pass "live row, kind $(live_field "$CHAT" 1), account $SEAT, socket $(live_field "$CHAT" 11)"
  fi
fi

# ─── E1.02 — statusline, theme, window title ────────────────────────────────

beat E1.02-statusline-theme
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  bad=""
  theme="$(jq -r '.theme // ""' "$SEAT_DIR/settings.json" 2>&1)" ||
    bad="$bad settings.json unreadable ($(one_line "$theme"));"
  case "$theme" in custom:professor-*) ;; *) bad="$bad theme=${theme:-<none>} (want custom:professor-*);" ;; esac
  tui="$(jq -r '.tui // ""' "$SEAT_DIR/settings.json" 2>/dev/null)"
  [ "$tui" = fullscreen ] || bad="$bad tui=${tui:-<none>};"
  # The socket comes from the LIVE row read at this beat, never from an earlier
  # beat's value: a reboot in between moves the chat and the old socket answers
  # "error connecting to …" — an error that must never be read as a window name.
  sock="$(live_field "$CHAT" 11)"
  wait_for 10 "grep -qF '$CHAT' <<<\"\$(tmux -S '$(_lane_tmux_dir)/$sock' list-windows -F '#{window_name}' 2>/dev/null)\"" ||
    bad="$bad tmux window name did not converge on $CHAT: ${LANE_WAIT_WHY:-no wait reason recorded};"
  if window="$(tmux -S "$(_lane_tmux_dir)/$sock" list-windows -F '#{window_name}' 2>&1)"; then
    case "$window" in *"$CHAT"*) ;; *) bad="$bad tmux window name '$(one_line "$window")' does not carry the label;" ;; esac
  else
    bad="$bad tmux list-windows FAILED on the live socket $sock ($(one_line "$window")) — the window name could not be read at all;"
  fi
  # Exit code AND output: an error message on stderr is not a render, and a
  # check that accepts either cannot tell a healthy statusline from a broken one.
  render="$(printf '{"session_id":"%s","model":{"display_name":"sonnet"},"workspace":{"current_dir":"%s"}}' \
    "$(live_field "$CHAT" 2)" "$CWD" | pfm statusline 2>&1)"
  render_rc=$?
  [ "$render_rc" -eq 0 ] || bad="$bad pfm statusline exited $render_rc ($(one_line "$render"));"
  [ -n "$render" ] || bad="$bad pfm statusline rendered nothing;"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "theme $theme · tui $tui · window '$window' · statusline $(one_line "$render" | cut -c1-120)"
  fi
fi

# ─── E1.03-E1.08 — the /reload matrix, one beat per flag ────────────────────

# reload_via_pane <needle> <flags…> — types `/reload …` into the chat the way a
# user does (the UserPromptSubmit intercept), then waits for the --then steer's
# own word. Prints nothing; returns 1 with $REPLY_WHY set on any failure.
REPLY_WHY=""
user_record_starts() {
  local records
  records="$(pfm chat read "$1" --json --tail 120 2>/dev/null)" || return 1
  jq -e --arg text "$2" 'any(.entries[]; .role == "user" and ((.text // "") | startswith($text)))' \
    <<<"$records" >/dev/null 2>&1
}
chat_idle() {
  pfm chat status "$1" --json 2>/dev/null | jq -e '.state == "idle"' >/dev/null
}
settle_chat() {
  wait_for 120 "chat_idle '$1'"
}
reload_finished() {
  tail -c "+$(($2 + 1))" "$1" 2>/dev/null |
    grep -E 'pfm chat reload: (respawned in place|.*rebooted FRESH)' >/dev/null
}
reload_hold_logged() {
  tail -c "+$(($2 + 1))" "$1" 2>/dev/null |
    grep -F "pfm chat reload: the chat's turn is still running — holding /exit until it ends" >/dev/null
}
beat_settle() {
  settle_chat "$CHAT" && return 0
  fail "$CHAT was still mid-turn when $1 began: ${LANE_WAIT_WHY:-no wait reason recorded}"
  return 1
}
wait_steer() {
  local rc
  wait_prompt "$1" "$2" "$3" || {
    rc=$?; LANE_WAIT_WHY="steer $2 prompt: $LANE_WAIT_WHY"; return "$rc"
  }
  wait_for "$3" "user_record_starts '$1' 'lane steer $2'" || {
    rc=$?; LANE_WAIT_WHY="steer $2 user record: $LANE_WAIT_WHY"; return "$rc"
  }
  settle_chat "$1" || {
    rc=$?; LANE_WAIT_WHY="steer $2 turn idle: $LANE_WAIT_WHY"; return "$rc"
  }
}
reload_via_pane() {
  local needle="$1"
  shift
  local out sock reload_log reload_offset inject_rc steer_rc
  REPLY_WHY=""
  sock="$(live_field "$CHAT" 11)"
  reload_log="$SID_DIR/reload-$sock.log"
  if [ -f "$reload_log" ]; then reload_offset="$(wc -c <"$reload_log")"; else reload_offset=0; fi
  out="$(pfm chat inject --allow-unsigned "$CHAT" "/reload $* --then \"lane steer $needle\"" 2>&1)"
  inject_rc=$?
  wait_steer "$CHAT" "$needle" 300
  steer_rc=$?
  case "$steer_rc" in
    0) ;;
    2) REPLY_WHY="$LANE_WAIT_WHY (waiting for $needle; inject rc $inject_rc: $(one_line "$out"))"; return 1 ;;
    *) REPLY_WHY="steer $needle did not settle in 300s: ${LANE_WAIT_WHY:-timeout}; inject rc $inject_rc: $(one_line "$out")"; return 1 ;;
  esac
  if ! wait_for 60 "reload_finished '$reload_log' '$reload_offset'"; then
    REPLY_WHY="the reload worker never logged its end in 60s; ${LANE_WAIT_WHY:-no wait reason recorded}; log tail: $(one_line "$(tail -n 5 "$reload_log" 2>&1)")"
    return 1
  fi
}

beat E1.03-reload-account
spends "cc:${ALT:-none}"
target_live "$CHAT"
if requires E1.01-open-seat1 && beat_settle E1.03-reload-account; then
  if [ -z "$ALT" ]; then
    # Not a failure: this run was given one seat, so there is no second seat to
    # reboot onto. An empty ALT is the run's own shape, not a defect.
    blocked "seats $LANE_SEATS" "needs --seats cc:1,cc:2 for cross-account session continuity"
  else
    before="$(live_field "$CHAT" 9)"
    before_id="$(live_field "$CHAT" 2)"
    nonce="E1CONTINUITY$(od -An -N6 -tx1 /dev/urandom | tr -d ' \n')"
    planted="$(pfm chat inject --allow-unsigned "$CHAT" "Remember the token $nonce." 2>&1)"
    if [ "$?" -ne 0 ] || ! wait_prompt "$CHAT" "$nonce" 120; then
      fail "could not plant the continuity token before reload: $(one_line "$planted") ${LANE_WAIT_WHY:-}"
    elif ! settle_chat "$CHAT"; then
      fail "could not plant the continuity token before reload: ${LANE_WAIT_WHY:-no wait reason recorded}"
    elif ! reload_via_pane "E1-ACCOUNT-$nonce" --account "$ALT"; then
      fail "the cross-account reload did not deliver its steer: $REPLY_WHY"
    elif [ "$(live_field "$CHAT" 2)" != "$before_id" ]; then
      fail "the row's session id changed from $before_id to $(live_field "$CHAT" 2)"
    elif [ "$(live_field "$CHAT" 9)" != "$ALT" ]; then
      fail "the steer ran but the row still reports account $(live_field "$CHAT" 9) (was $before, asked for $ALT)"
    else
      sock="$(live_field "$CHAT" 11)"
      start="$(tmux -S "$(_lane_tmux_dir)/$sock" list-panes -F '#{pane_start_command}' 2>&1 | head -1)"
      records="$(pfm chat read "$CHAT" --json --tail 120 2>&1)"
      if [[ "$start" != *"'--resume' '$before_id'"* ]]; then
        fail "cross-account launch on $sock did not resume $before_id: $(one_line "$start")"
      elif ! jq -e --arg nonce "$nonce" --arg steer "E1-ACCOUNT-$nonce" \
        'any(.entries[]; .role == "user" and ((.text // "") | contains($nonce))) and any(.entries[]; .role == "user" and ((.text // "") | contains($steer)))' \
        <<<"$records" >/dev/null 2>&1; then
        fail "pfm chat read did not show both user records in session $before_id: $(one_line "$records" | cut -c1-200)"
      else
        pass "session $before_id resumed on seat $ALT (account $before → $ALT), with both user records"
      fi
    fi
  fi
fi

beat E1.04-reload-model-effort
spends "cc:${ALT:-$SEAT}"
target_live "$CHAT"
if requires E1.01-open-seat1 && beat_settle E1.04-reload-model-effort; then
  if ! reload_via_pane RELOADED-MODEL --model sonnet --effort medium; then
    fail "$REPLY_WHY"
  elif ! live_chat "$CHAT"; then
    fail "the steer ran but $CHAT has no live row after --model/--effort"
  else
    sock="$(live_field "$CHAT" 11)"
    start="$(tmux -S "$(_lane_tmux_dir)/$sock" list-panes -F '#{pane_start_command}' 2>&1 | head -1)"
    if [[ "$start" != *"'--model' 'sonnet'"* || "$start" != *"'--effort' 'medium'"* ]]; then
      fail "reboot launch lacks --model sonnet or --effort medium: $(one_line "$start" | cut -c1-240)"
    else
      pass "reboot launch carries --model sonnet --effort medium; steer is a user record"
    fi
  fi
fi

beat E1.05-reload-1h
spends "cc:${ALT:-$SEAT}"
target_live "$CHAT"
if requires E1.01-open-seat1 && beat_settle E1.05-reload-1h; then
  if ! reload_via_pane RELOADED-1H-ON --cache 1h; then
    fail "--cache 1h: $REPLY_WHY"
  else
    sock="$(live_field "$CHAT" 11)"
    on_start="$(tmux -S "$(_lane_tmux_dir)/$sock" list-panes -F '#{pane_start_command}' 2>&1 | head -1)"
    if ! reload_via_pane RELOADED-1H-OFF --cache 5m; then
      fail "--cache 5m: $REPLY_WHY (the 1h reboot had already landed)"
    else
      off_start="$(tmux -S "$(_lane_tmux_dir)/$sock" list-panes -F '#{pane_start_command}' 2>&1 | head -1)"
      if [[ "$on_start" != *"CACHE_LIVE_CONTROL_MAIN_TTL='1h'"* || "$off_start" != *"CACHE_LIVE_CONTROL_MAIN_TTL='5m'"* ]]; then
        fail "cache launch did not carry 1h then 5m: $(one_line "$on_start" | cut -c1-200) → $(one_line "$off_start" | cut -c1-200)"
      else
        pass "the cache launch toggled 1h → 5m; each steer is a user record"
      fi
    fi
  fi
fi

# reload_new_on_socket <flags…> — follow the reborn chat by its socket. Wait
# for the worker's completion line as well as the new id before another reload.
NEW_ID="" NEW_NAME=""
reload_new_on_socket() {
  local sock="$1" was="$2" out log completed_before inject_rc
  shift 2
  REPLY_WHY="" NEW_ID="" NEW_NAME=""
  log="$SID_DIR/reload-$sock.log"
  completed_before="$(grep -cF 'rebooted FRESH as requested' "$log" 2>/dev/null || true)"
  completed_before="${completed_before:-0}"
  out="$(pfm chat inject --allow-unsigned "$CHAT" "/reload $* --then \"lane steer RELOADED-NEW\"" 2>&1)"
  inject_rc=$?
  if ! wait_for 300 "[ -n \"\$(socket_field '$sock' 2)\" ] && [ \"\$(socket_field '$sock' 2)\" != '$was' ]"; then
    REPLY_WHY="no fresh session id on socket $sock in 300s (it still reads '$(socket_field "$sock" 2)'); ${LANE_WAIT_WHY:-no wait reason recorded}; inject rc $inject_rc: $(one_line "$out")"
    return 1
  fi
  if ! wait_for 90 "[ \"\$(grep -cF 'rebooted FRESH as requested' '$log' 2>/dev/null || true)\" -gt '$completed_before' ]"; then
    REPLY_WHY="new id appeared on $sock but the reload worker did not finish: ${LANE_WAIT_WHY:-timeout}; log tail: $(one_line "$(tail -5 "$log" 2>&1)")"
    return 1
  fi
  NEW_ID="$(socket_field "$sock" 2)"
  NEW_NAME="$(socket_field "$sock" 5)"
  return 0
}

beat E1.06-reload-new
spends "cc:${ALT:-$SEAT}"
target_live "$CHAT"
if requires E1.01-open-seat1 && beat_settle E1.06-reload-new; then
  sock="$(live_field "$CHAT" 11)"
  id_before="$(socket_field "$sock" 2)"
  anchor_socket "$sock" # from here the NAME is not a handle; the socket is
  observed=""
  if ! reload_new_on_socket "$sock" "$id_before" --new; then
    fail "--new: $REPLY_WHY"
  else
    id_new="$NEW_ID"
    observed="after --new the live session on $sock is named '$NEW_NAME'"
    if [ "$NEW_NAME" != "$CHAT" ]; then
      name_out="$(pfm chat name "$id_new" "$CHAT" 2>&1)"
      name_rc=$?
    else
      name_out="the reborn session already carried the label" name_rc=0
    fi
    if [ "$id_new" = "$id_before" ]; then
      fail "--new kept the same session id $id_before on socket $sock — it must open a fresh session"
    elif ! LANE_ANCHOR="sock:$sock" wait_for 30 "[ \"\$(socket_field '$sock' 5)\" = '$CHAT' ]"; then
      fail "the reborn session could not be renamed back to $CHAT (pfm chat name exited $name_rc: $(one_line "$name_out")); the row on $sock reads '$(socket_field "$sock" 5)' — $observed; ${LANE_WAIT_WHY:-no wait reason recorded}"
    elif [ "$name_rc" -ne 0 ]; then
      fail "the reborn session could not be renamed back to $CHAT (pfm chat name exited $name_rc: $(one_line "$name_out")); the row on $sock reads '$(socket_field "$sock" 5)' — $observed"
    elif ! reload_new_on_socket "$sock" "$id_new" --new --hide; then
      fail "--new --hide: $REPLY_WHY (plain --new had already produced $id_new) — $observed"
    else
      id_hide="$NEW_ID"
      [ "$NEW_NAME" = "$CHAT" ] || pfm chat name "$id_hide" "$CHAT" >/dev/null 2>&1
      LANE_ANCHOR="sock:$sock" wait_for 10 "[ \"\$(pfm ls --tsv 2>/dev/null | awk -F'\\t' -v n='$CHAT' 'NR > 1 && \$5 == n && \$10 == \"false\" { c++ } END { print c + 0 }')\" = 1 ]" ||
        visible_wait="$LANE_WAIT_WHY"
      visible="$(pfm ls --tsv 2>/dev/null | awk -F'\t' -v n="$CHAT" 'NR > 1 && $5 == n && $10 == "false" { c++ } END { print c + 0 }')"
      if [ -n "${visible_wait:-}" ] || [ "$visible" -ne 1 ]; then
        fail "after --new --hide, $visible unhidden rows carry the name $CHAT (want exactly 1: the new session) — $observed; ${visible_wait:-}"
      else
        pass "fresh session ids $id_before → $id_new → $id_hide on one unchanged socket $sock; --hide left exactly one visible row · $observed"
      fi
    fi
  fi
fi

# ─── E1.26 — one live row plus its own resume row resolves to the live row ──
# Placed here: the old transcript is labelled with the live name to exercise
# the resolver's unique-live-row rule after reload relabels it by default.
# resolve.ResolveRosterName (internal/resolve/roster.go) dedupes this exact
# pair — of several exact-name matches, the unique LIVE one wins instead of
# refusing ambiguous (internal/resolve/roster_test.go
# TestResolveRosterNamePrefersTheUniqueLiveRow pins it at the unit layer).

beat E1.26-resolver-prefers-live
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.06-reload-new && beat_settle E1.26-resolver-prefers-live; then
  old_transcript="$(find "$HOME/.claude/projects" -type f -name "$id_before.jsonl" -print -quit 2>/dev/null)"
  if [ -n "$old_transcript" ]; then
    jq -cn --arg name "$CHAT" --arg id "$id_before" \
      '{type:"custom-title",customTitle:$name,sessionId:$id}' >>"$old_transcript"
  fi
  wait_for 10 "[ \"\$(pfm ls --tsv 2>/dev/null | awk -F'\\t' -v n='$CHAT' 'NR > 1 && \$5 == n && \$1 ~ /^live-/ { c++ } END { print c + 0 }')\" = 1 ] && [ \"\$(pfm ls --tsv 2>/dev/null | awk -F'\\t' -v n='$CHAT' 'NR > 1 && \$5 == n && \$1 !~ /^live-/ { c++ } END { print c + 0 }')\" -ge 1 ]" || rows_wait="$LANE_WAIT_WHY"
  live_rows="$(pfm ls --tsv 2>/dev/null | awk -F'\t' -v n="$CHAT" 'NR > 1 && $5 == n && $1 ~ /^live-/ { c++ } END { print c + 0 }')"
  resume_rows="$(pfm ls --tsv 2>/dev/null | awk -F'\t' -v n="$CHAT" 'NR > 1 && $5 == n && $1 !~ /^live-/ { c++ } END { print c + 0 }')"
  if [ -z "$old_transcript" ]; then
    fail "the old transcript for $id_before could not be found to stage the duplicate-name resolver fixture"
  elif [ -n "${rows_wait:-}" ] || [ "$live_rows" -ne 1 ] || [ "$resume_rows" -lt 1 ]; then
    fail "the shape this beat exists to assert is not present: $live_rows live row(s) and $resume_rows resume row(s) carry '$CHAT' (want exactly 1 live plus at least its own resume row) — nothing was asserted; ${rows_wait:-}"
  else
    out="$(pfm chat inject --allow-unsigned "$CHAT" "lane resolver RESOLVE-OK" 2>&1)"
    rc=$?
    if [ "$rc" -ne 0 ]; then
      fail "'$CHAT' held by 1 live row and $resume_rows resume row(s) of its own refused (exit $rc): $(one_line "$out") — resolve.ResolveRosterName's unique-live-row rule should have taken the live row"
    elif ! wait_prompt "$CHAT" RESOLVE-OK 240; then
      fail "the inject was accepted but never landed: ${LANE_WAIT_WHY:-no wait reason recorded}"
    else
      pass "'$CHAT' resolved to its one live row with $resume_rows resume row(s) of its own beside it"
    fi
  fi
fi

beat E1.07-reload-then
spends "cc:${ALT:-$SEAT}"
target_live "$CHAT"
if requires E1.01-open-seat1 && beat_settle E1.07-reload-then; then
  if ! reload_via_pane THEN-OK; then
    fail "$REPLY_WHY"
  else
    pass "a bare /reload --then rebooted and its detached waiter (pfm internal then) delivered the steer"
  fi
fi

beat E1.08-reload-sock
spends "cc:${ALT:-$SEAT}"
target_live "$CHAT"
if requires E1.01-open-seat1 && beat_settle E1.08-reload-sock; then
  sock="$(live_field "$CHAT" 11)"
  reload_log="$SID_DIR/reload-$sock.log"
  if [ -f "$reload_log" ]; then reload_offset="$(wc -c <"$reload_log")"; else reload_offset=0; fi
  out="$(pfm chat reload --sock "$sock" --then "lane steer SOCK-OK" 2>&1)"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    fail "pfm chat reload --sock $sock exited $rc: $(one_line "$out")"
  elif ! wait_steer "$CHAT" SOCK-OK 300; then
    fail "reload --sock accepted but SOCK-OK did not settle: ${LANE_WAIT_WHY:-no wait reason recorded}"
  elif ! wait_for 60 "reload_finished '$reload_log' '$reload_offset'"; then
    fail "the reload worker never logged its end in 60s; ${LANE_WAIT_WHY:-no wait reason recorded}; log tail: $(one_line "$(tail -n 5 "$reload_log" 2>&1)")"
  else
    pass "reload addressed by its own socket $sock rebooted and steered"
  fi
fi

# ─── E1.09 — reload requested while the chat is busy ────────────────────────

beat E1.09-reload-while-busy
spends "cc:${ALT:-$SEAT}"
target_live "$CHAT"
if requires E1.01-open-seat1 && beat_settle E1.09-reload-while-busy; then
  gate="$LANE_OUT_DIR/e1-reload-hold.gate"
  : >"$gate"
  steps="$(jq -cn --arg gate "$gate" '[{type:"hold",until_gone:$gate},{type:"turn",reply:"held"}]')"
  busy="$(pfm chat inject --allow-unsigned "$CHAT" "/lane hold $(mock_steps "$steps")" 2>&1)"
  busy_rc=$?
  [ "$busy_rc" -eq 0 ] || _lane_log_only "   E1.09: the busy-making inject exited $busy_rc: $(one_line "$busy")"
  wait_for 30 "grep -qi working <<<\"\$(pfm chat status '$CHAT' 2>/dev/null)\"" ||
    _lane_log_only "   E1.09: held chat never reported working: ${LANE_WAIT_WHY:-}"
  sock="$(live_field "$CHAT" 11)"
  reload_log="$SID_DIR/reload-$sock.log"
  if [ -f "$reload_log" ]; then reload_offset="$(wc -c <"$reload_log")"; else reload_offset=0; fi
  out="$(pfm chat reload --sock "$sock" --then "lane steer BUSY-RELOADED" 2>&1)"
  rc=$?
  # A headless tmux server has no client to retain display-message. The
  # worker's own log records the same hold decision for this beat's slice.
  hold_wait=""
  wait_for 10 "reload_hold_logged '$reload_log' '$reload_offset'" || hold_wait="$LANE_WAIT_WHY"
  hold="$(tail -c "+$((reload_offset + 1))" "$reload_log" 2>/dev/null |
    grep -F "pfm chat reload: the chat's turn is still running — holding /exit until it ends" | tail -1)"
  rm -f "$gate"
  if ! wait_steer "$CHAT" BUSY-RELOADED 420; then
    fail "no BUSY-RELOADED user record after a mid-turn /reload: ${LANE_WAIT_WHY:-timeout}; inject rc $rc: $(one_line "$out")"
  elif ! wait_for 60 "reload_finished '$reload_log' '$reload_offset'"; then
    fail "the reload worker never logged its end in 60s; ${LANE_WAIT_WHY:-no wait reason recorded}; log tail: $(one_line "$(tail -n 5 "$reload_log" 2>&1)")"
  elif [ -z "$hold" ] || [ -n "$hold_wait" ]; then
    fail "the reboot landed but the worker log for this beat on $sock never reported holding /exit while the turn was busy: ${hold_wait:-no wait reason recorded}"
  else
    pass "worker reported holding /exit ('$(one_line "$hold")'), then the reboot and its steer"
  fi
fi

# ─── E1.11 — a role seat keeps its role through reload ───────────────────────────

beat E1.11-role-reload
spends "cc:$SEAT"
target "$ROLE_CHAT"
if requires E1.01-open-seat1; then
  mkdir -p "$CWD/.claude/agents"
  cat >"$CWD/.claude/agents/lane-role.md" <<'ROLE'
---
name: lane-role
description: the constitution a Tier B role seat keeps through reload
---
You are the lane role. Whenever you are asked who you are, answer with exactly: LANE-ROLE.
ROLE
  out="$(pfm chat new --name "$ROLE_CHAT" --engine cc --account "$SEAT" --cwd "$CWD" --agent-role lane-role \
    --await --timeout 300 "lane role ready" 2>&1)"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    fail "pfm chat new --agent-role lane-role exited $rc: $(one_line "$out")"
  else
    role_reload="$(pfm chat inject --allow-unsigned "$ROLE_CHAT" "/reload --then \"lane steer ROLE-RELOADED\"" 2>&1)"
    role_reload_rc=$?
    if ! wait_steer "$ROLE_CHAT" ROLE-RELOADED 300; then
      fail "after reload the role steer has no user record: ${LANE_WAIT_WHY:-timeout}; inject rc $role_reload_rc: $(one_line "$role_reload")"
    else
      sock="$(live_field "$ROLE_CHAT" 11)"
      start="$(tmux -S "$(_lane_tmux_dir)/$sock" list-panes -F '#{pane_start_command}' 2>&1 | head -1)"
      if [[ "$start" != *'--system-prompt-file'* && "$start" != *'--agent-role'* ]]; then
        fail "role reload on $sock lost its prompt channel: $(one_line "$start" | cut -c1-240)"
      else
        pass "role reload preserved its prompt channel and delivered the steer"
      fi
    fi
  fi
fi

# ─── E1.12 — status, every form, and the sidechain override ─────────────────

beat E1.12-status
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.01-open-seat1 && beat_settle E1.12-status; then
  bad=""
  base="$(pfm chat status "$CHAT" 2>&1)" || bad="$bad status base exited non-zero ($(one_line "$base"));"
  base_state="$(awk -F '\t' 'NR == 1 { print $2 }' <<<"$base")"
  case "$base_state" in idle|working) ;; *) bad="$bad status base named neither idle nor working: $(one_line "$base");" ;; esac
  json="$(pfm chat status "$CHAT" --json 2>&1)" || bad="$bad status --json exited non-zero;"
  printf '%s' "$json" | jq -e . >/dev/null 2>&1 || bad="$bad status --json is not JSON: $(one_line "$json");"
  json_state="$(jq -r '.state // empty' <<<"$json" 2>/dev/null)"
  case "$json_state" in idle|working) ;; *) bad="$bad status --json state is '${json_state:-<none>}';" ;; esac
  summary="$(pfm chat status "$CHAT" --summary 2>&1)" || bad="$bad status --summary exited non-zero ($(one_line "$summary"));"
  ask="$(pfm chat status "$CHAT" --ask 2>&1)" || bad="$bad status --ask exited non-zero ($(one_line "$ask"));"
  eng="$(pfm chat status "$CHAT" --engine claude --summary 2>&1)" || bad="$bad status --engine claude --summary exited non-zero ($(one_line "$eng"));"
  mdl="$(pfm chat status "$CHAT" --model haiku --summary 2>&1)" || bad="$bad status --model haiku --summary exited non-zero ($(one_line "$mdl"));"
  # L5: a background sub-agent must keep the chat `working` even while its own
  # pane looks quiet — the sidechain override, read from the operator's side.
  pfm chat inject --allow-unsigned "$CHAT" \
    "/lane status $(mock_steps '[{"type":"background_agent","name":"lane-sub"},{"type":"turn","busy_ms":20000}]')" >/dev/null 2>&1 ||
    bad="$bad the scripted background turn was refused;"
  working="" last_state="<unread>"
  wait_for 20 "[ \"\$(pfm chat status '$CHAT' 2>/dev/null | awk -F '\t' 'NR == 1 { print \$2 }')\" = working ]" && working=yes || working_wait="$LANE_WAIT_WHY"
  last_state="$(pfm chat status "$CHAT" 2>/dev/null | awk -F '\t' 'NR == 1 { print $2 }')"
  [ -n "$working" ] || bad="$bad status never reported working while a background sub-agent ran (last state: $last_state); ${working_wait:-no wait reason recorded};"
  wait_for 300 "[ \"\$(pfm chat status '$CHAT' 2>/dev/null | awk -F '\t' 'NR == 1 { print \$2 }')\" = idle ]" || {
    last_state="$(pfm chat status "$CHAT" 2>/dev/null | awk -F '\t' 'NR == 1 { print $2 }')"
    bad="$bad the chat never returned to idle after the sub-agent (last state: ${last_state:-<unread>}); ${LANE_WAIT_WHY:-no wait reason recorded};"
  }
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "base/--json/--summary/--ask/--engine/--model all answered; working under a live sub-agent, then idle"
  fi
fi

# ─── E1.13 — last, read, stream ─────────────────────────────────────────────

beat E1.13-last-read-stream
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  bad=""
  last="$(pfm chat last "$CHAT" 2>&1)" || bad="$bad last exited non-zero;"
  [ -n "$last" ] || bad="$bad last printed nothing;"
  tail_out="$(pfm chat read "$CHAT" --tail 2 --condensed 2>&1)" || bad="$bad read --tail 2 --condensed exited non-zero ($(one_line "$tail_out"));"
  json="$(pfm chat read "$CHAT" --json 2>&1)" || bad="$bad read --json exited non-zero;"
  printf '%s' "$json" | jq -e . >/dev/null 2>&1 || bad="$bad read --json is not JSON: $(one_line "$json");"
  # C30, the excerpt-file form. `pfm chat read` takes a TARGET or an EXCERPT
  # FILE — a regular file whose extension is NOT .jsonl (cmd/pfm/chat_command.go
  # runChatRead): its CONTENT is matched against every transcript, the same
  # search `pfm chat find <excerpt-file>` prints. A transcript PATH is neither:
  # it falls through to the target form and is refused as "no chat named …".
  sid="$(live_field "$CHAT" 2)"
  excerpt=/tmp/e1-excerpt.txt
  printf '%s\n' "$last" | tail -3 >"$excerpt"
  if [ ! -s "$excerpt" ]; then
    bad="$bad the chat's last answer was empty, so no excerpt could be written — the excerpt-file form was NOT asserted;"
  else
    wait_for 30 "pfm chat find '$excerpt' 2>&1 | grep -F '$sid' >/dev/null" || find_wait="$LANE_WAIT_WHY"
    found="$(pfm chat find "$excerpt" 2>&1)"
    found_rc=$?
    [ "$found_rc" -eq 0 ] || bad="$bad chat find <excerpt-file> exited $found_rc ($(one_line "$found"));"
    [ -z "${find_wait:-}" ] || bad="$bad chat find never indexed session $sid: $find_wait;"
    grep -qF "$sid" <<<"$found" ||
      bad="$bad chat find matched a session other than the chat's own $sid: $(one_line "$found"); ${find_wait:-no wait reason recorded};"
    file_read="$(pfm chat read "$excerpt" 20 2>&1)"
    file_rc=$?
    [ "$file_rc" -eq 0 ] || bad="$bad chat read <excerpt-file> exited $file_rc ($(one_line "$file_read"));"
    grep -q 'Extracted ->' <<<"$file_read" ||
      bad="$bad chat read <excerpt-file> did not report the extracted file: $(one_line "$file_read");"
  fi
  stream_full="$(timeout 60 pfm chat stream "$CHAT" --from-start --no-follow 2>&1)"
  stream_rc=$?
  stream="$(printf '%s\n' "$stream_full" | head -20)"
  [ "$stream_rc" -eq 0 ] || bad="$bad stream --from-start --no-follow exited $stream_rc ($(one_line "$stream"));"
  [ -n "$stream" ] || bad="$bad stream --from-start --no-follow printed nothing;"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "last, read (--tail/--condensed/--json), find + read by EXCERPT FILE onto session $sid, stream — all from outside"
  fi
fi

# ─── E1.14 — capture and keys ───────────────────────────────────────────────

beat E1.14-capture-keys
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.01-open-seat1 && beat_settle E1.14-capture-keys; then
  cap="$(pfm chat capture "$CHAT" 2>&1)"
  rc=$?
  typed="LANE-KEYS-$$"
  keys_out="$(pfm chat keys --literal --capture "$CHAT" "$typed" 2>&1)"
  keys_rc=$?
  wait_for 10 'grep -qF "$typed" <<<"$(pane "$CHAT")$keys_out"' || typed_wait="$LANE_WAIT_WHY"
  after="$(pane "$CHAT")"
  pfm chat keys "$CHAT" Escape >/dev/null 2>&1
  pfm chat keys "$CHAT" C-u >/dev/null 2>&1
  if [ "$rc" -ne 0 ] || [ -z "$cap" ]; then
    fail "capture exited $rc with $(printf '%s' "$cap" | wc -c | tr -d ' ') bytes: $(one_line "$cap")"
  elif [ "$keys_rc" -ne 0 ]; then
    fail "keys --literal --capture exited $keys_rc: $(one_line "$keys_out")"
  elif [ -n "${typed_wait:-}" ] || ! grep -qF -- "$typed" <<<"$after$keys_out"; then
    fail "the literal keys never appeared on the pane or in the capture: $(one_line "$after" | cut -c1-200); ${typed_wait:-no wait reason recorded}"
  else
    pass "capture read $(printf '%s' "$cap" | wc -l | tr -d ' ') pane lines; keys typed '$typed' into the composer and Escape/C-u cleared it"
  fi
fi

# ─── E1.15 — ask ────────────────────────────────────────────────────────────

beat E1.15-ask
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.01-open-seat1 && beat_settle E1.15-ask; then
  out="$(pfm chat ask --timeout 240 "$CHAT" "/lane ask $(mock_steps '{"type":"turn","reply":"ASK-OK"}')" 2>&1)"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    fail "pfm chat ask exited $rc: $(one_line "$out")"
  elif ! grep -qF ASK-OK <<<"$out"; then
    fail "ask returned without the fresh answer: $(one_line "$out")"
  else
    pass "ask blocked until the fresh assistant turn and returned it"
  fi
fi

# ─── E1.16 — inject, every form and both guards ─────────────────────────────

beat E1.16-inject
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.01-open-seat1 && beat_settle E1.16-inject; then
  bad=""
  pfm chat inject --allow-unsigned "$CHAT" "lane inject INJECT-OK" >/dev/null 2>&1 ||
    bad="$bad base inject was refused;"
  wait_prompt "$CHAT" INJECT-OK 240 || bad="$bad the base inject has no user record;"
  pfm chat inject --allow-unsigned --force-now "$CHAT" "lane inject FORCE-OK" >/dev/null 2>&1 ||
    bad="$bad --force-now was refused;"
  wait_prompt "$CHAT" FORCE-OK 240 || bad="$bad --force-now has no user record;"
  printf 'lane inject FILE-OK\n' >/tmp/e1-inject.txt
  pfm chat inject --allow-unsigned --file /tmp/e1-inject.txt "$CHAT" >/dev/null 2>&1 ||
    bad="$bad --file was refused;"
  wait_prompt "$CHAT" FILE-OK 240 || bad="$bad --file has no user record;"
  then_gate="$LANE_OUT_DIR/e1-then-hold.gate"
  : >"$then_gate"
  then_steps="$(jq -cn --arg gate "$then_gate" '[{type:"hold",until_gone:$gate},{type:"turn",reply:"primary"}]')"
  then_out="$(CHAT_THEN_IDLE_TRIES=30 pfm chat inject --allow-unsigned --then "/lane inject THEN-CHAIN" "$CHAT" \
    "/lane inject PRIMARY-OK $(mock_steps "$then_steps")" 2>&1)"
  then_rc=$?
  then_log="$(grep -oE '/tmp/chat-then-[^ )]+' <<<"$then_out" | head -1)"
  if [ "$then_rc" -ne 0 ] || [ -z "$then_log" ]; then
    bad="$bad --then was refused or named no waiter log (rc $then_rc): $(one_line "$then_out");"
  elif ! wait_for 30 "[ -s '$then_log' ]"; then
    bad="$bad --then waiter did not start before the held primary ended: ${LANE_WAIT_WHY:-timeout};"
  fi
  rm -f "$then_gate"
  wait_prompt "$CHAT" THEN-CHAIN 120 || bad="$bad the --then steer has no user record: ${LANE_WAIT_WHY:-timeout};"
  records="$(pfm chat read "$CHAT" --json --tail 120 2>&1)"
  jq -e '[.entries[] | select(.role == "user") | (.text // "")] as $user |
    ($user | map(startswith("/lane inject PRIMARY-OK")) | index(true)) as $primary |
    ($user | index("/lane inject THEN-CHAIN")) as $then |
    $primary != null and $then != null and $then > $primary' <<<"$records" >/dev/null 2>&1 ||
    bad="$bad --then steer did not follow its primary in pfm chat read;"
  # C37: a bare /compact primary is refused by name, never typed.
  compact="$(pfm chat inject --allow-unsigned "$CHAT" "/compact" 2>&1)"
  compact_rc=$?
  if [ "$compact_rc" -eq 0 ]; then
    bad="$bad a bare /compact primary was ACCEPTED;"
  elif ! grep -qF 'never injected' <<<"$compact"; then
    bad="$bad /compact was refused but did not say never injected: $(one_line "$compact");"
  fi
  # L28: an OPEN selector menu refuses delivery by name and types nothing.
  pfm chat inject --allow-unsigned "$CHAT" \
    "/lane menu $(mock_steps '{"type":"menu","options":["one","two"],"selected":1}')" >/dev/null 2>&1 ||
    bad="$bad scripted menu step was refused;"
  pfm chat keys --literal "$CHAT" "/" >/dev/null 2>&1
  wait_for 10 'grep -qF "Enter to confirm · Esc to cancel" <<<"$(pane "$CHAT")"' ||
    bad="$bad the selector menu did not open: ${LANE_WAIT_WHY:-no wait reason recorded};"
  menu="$(pfm chat inject --allow-unsigned "$CHAT" "this must not be typed into an open menu" 2>&1)"
  menu_rc=$?
  pfm chat keys "$CHAT" Escape >/dev/null 2>&1
  pfm chat keys "$CHAT" C-u >/dev/null 2>&1
  if [ "$menu_rc" -eq 0 ]; then
    bad="$bad an inject with the slash menu OPEN was accepted (want the named ABORT: OPEN selector menu refusal);"
  elif ! grep -qi 'selector menu' <<<"$menu"; then
    bad="$bad the menu-open inject failed but did not name the open selector menu: $(one_line "$menu");"
  fi
  # The mock's menu step returns without an assistant record; finish its turn
  # after Escape so the next beat cannot inherit a still-working transcript.
  close_out="$(pfm chat inject --allow-unsigned "$CHAT" "/lane menu closed $(mock_steps '{"type":"turn","reply":"menu closed"}')" 2>&1)"
  close_rc=$?
  if [ "$close_rc" -ne 0 ]; then
    bad="$bad menu close turn was refused (exit $close_rc): $(one_line "$close_out");"
  elif ! wait_prompt "$CHAT" 'lane menu closed' 120; then
    bad="$bad menu close turn has no user record: ${LANE_WAIT_WHY:-no wait reason recorded};"
  elif ! settle_chat "$CHAT"; then
    bad="$bad the menu turn did not settle after Escape/C-u: ${LANE_WAIT_WHY:-no wait reason recorded};"
  fi
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "base/--force-now/--file/--then delivered with proof; /compact refused by name; an open menu refused by name"
  fi
fi

# ─── E1.17 — watch ──────────────────────────────────────────────────────────

beat E1.17-watch
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.01-open-seat1 && beat_settle E1.17-watch; then
  watch_file="$LANE_OUT_DIR/e1-watch.out"
  timeout 120 pfm chat watch "$CHAT" --idle-after 10 --once >"$watch_file" 2>&1 &
  watch_pid=$!
  # WINDOW: watcher needs its first idle sample before the busy inject — the transition assertion needs it.
  sleep 1
  inject_out="$(pfm chat inject --allow-unsigned "$CHAT" "/lane watch $(mock_steps '{"type":"turn","reply":"WATCH-DONE","busy_ms":3000}')" 2>&1)"
  inject_rc=$?
  wait "$watch_pid"
  rc=$?
  out="$(cat "$watch_file")"
  last_state="$(pfm chat status "$CHAT" 2>/dev/null | awk -F '\t' 'NR == 1 { print $2 }')"
  if [ "$rc" -eq 124 ]; then
    fail "watch --idle-after 10 --once timed out (last status state: ${last_state:-<unread>}; inject rc $inject_rc: $(one_line "$inject_out"))"
  elif [ "$inject_rc" -ne 0 ]; then
    fail "the scripted watch turn was refused (exit $inject_rc): $(one_line "$inject_out"); watch rc $rc: $(one_line "$out")"
  elif [ "$rc" -ne 0 ]; then
    fail "watch exited $rc: $(one_line "$out")"
  elif [ "$(grep -c '^IDLE .* idle_seconds=[0-9][0-9]*$' "$watch_file")" -ne 1 ] ||
    ! awk '/^IDLE .* idle_seconds=[0-9]+$/ { split($NF, v, "="); if (v[2] >= 10) ok++ } END { exit !(NR == 1 && ok == 1) }' "$watch_file"; then
    fail "watch did not report exactly one IDLE line with idle_seconds >= 10: $(one_line "$out")"
  else
    pass "watch --idle-after 10 --once reported the idle transition: $(one_line "$out")"
  fi
fi

# ─── E1.18 — name, and the {name}:{group} grammar ───────────────────────────

beat E1.18-name
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.01-open-seat1 && beat_settle E1.18-name; then
  grouped="$CHAT:lane"
  sock="$(live_field "$CHAT" 11)"
  out="$(pfm chat name "$CHAT" "$grouped" 2>&1)"
  rc=$?
  LANE_ANCHOR="sock:$sock" wait_for 10 "[ \"\$(socket_field '$sock' 5)\" = '$grouped' ] && grep -qF '$CHAT' <<<\"\$(tmux -S '$(_lane_tmux_dir)/$sock' list-windows -F '#{window_name}' 2>/dev/null)\"" || grouped_wait="$LANE_WAIT_WHY"
  window="$(tmux -S "$(_lane_tmux_dir)/$sock" list-windows -F '#{window_name}' 2>/dev/null | head -1)"
  row_name="$(socket_field "$sock" 5)"
  back="$(pfm chat name "$grouped" "$CHAT" 2>&1)"
  back_rc=$?
  LANE_ANCHOR="sock:$sock" wait_for 10 "[ \"\$(live_field '$CHAT' 11)\" = '$sock' ]" || back_wait="$LANE_WAIT_WHY"
  if [ "$rc" -ne 0 ]; then
    fail "pfm chat name exited $rc: $(one_line "$out")"
  elif [ "$row_name" != "$grouped" ]; then
    fail "the row for socket $sock reports name '$row_name' after the rename to '$grouped': ${grouped_wait:-no wait reason recorded}"
  elif [ "${window#*"$CHAT"}" = "$window" ] || [ -n "${grouped_wait:-}" ]; then
    fail "the tmux window name '$window' never converged on the new label: ${grouped_wait:-no wait reason recorded}"
  elif [ -n "${back_wait:-}" ] || [ "$back_rc" -ne 0 ] || [ "$(live_field "$CHAT" 11)" != "$sock" ]; then
    fail "the rename back to $CHAT failed (exit $back_rc): $(one_line "$back"); ${back_wait:-no wait reason recorded}"
  else
    pass "'{name}:{group}' label converged in the row and the tmux window ('$window'), then renamed back"
  fi
fi

# ─── E1.19 — kill / unkill, and the exit-intercept body ─────────────────────

beat E1.19-kill-unkill
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  bad=""
  kill_name="E1_KILL_$$"
  new_out="$(pfm chat new --name "$kill_name" --engine cc --account "$SEAT" --cwd "$CWD" --await --timeout 300 "lane kill probe" 2>&1)"
  new_rc=$?
  kill_id="$(live_field "$kill_name" 2)"
  kill_sock="$(live_field "$kill_name" 11)"
  [ "$new_rc" -eq 0 ] && [ -n "$kill_id" ] && [ -n "$kill_sock" ] ||
    bad="$bad disposable chat did not open (exit $new_rc): $(one_line "$new_out");"
  kill_out=""
  if [ -z "$bad" ]; then
    kill_out="$(pfm chat kill "$kill_name" 2>&1)"
    kill_rc=$?
    [ "$kill_rc" -eq 0 ] || bad="$bad kill exited $kill_rc: $(one_line "$kill_out");"
  fi
  if [ -n "$kill_id" ]; then
    LANE_ANCHOR= wait_for 10 "[ \"\$(row_field '$kill_name' 10)\" = true ] && ! tmux -S '$(_lane_tmux_dir)/$kill_sock' list-sessions >/dev/null 2>&1" ||
      kill_wait="$LANE_WAIT_WHY"
    [ "$(row_field "$kill_name" 10)" = true ] || bad="$bad the disposable row's killed column is '$(row_field "$kill_name" 10)' after kill;"
    if tmux -S "$(_lane_tmux_dir)/$kill_sock" list-sessions >/dev/null 2>&1; then
      bad="$bad killed chat's tmux server $kill_sock still answers;"
    fi
    [ -z "${kill_wait:-}" ] || bad="$bad kill state did not converge: $kill_wait;"
    unkill_out="$(pfm chat unkill "$kill_name" 2>&1)"
    unkill_rc=$?
    [ "$unkill_rc" -eq 0 ] || bad="$bad unkill by name exited $unkill_rc: $(one_line "$unkill_out");"
  fi
  if [ -n "$kill_id" ]; then
    LANE_ANCHOR= wait_for 10 "[ \"\$(row_field '$kill_name' 10)\" = false ]" ||
      unkill_wait="$LANE_WAIT_WHY"
    [ "$(row_field "$kill_name" 10)" = false ] || bad="$bad the disposable row's killed column is '$(row_field "$kill_name" 10)' after unkill;"
    [ -z "${unkill_wait:-}" ] || bad="$bad unkill state did not converge: $unkill_wait;"
    pfm chat kill "$kill_id" >/dev/null 2>&1
  fi
  live_chat "$CHAT" || bad="$bad $CHAT lost its live row during the disposable kill;"
  # X28: the prompt-hook body. Driven with a session id that belongs to NO chat,
  # so it can refuse for a named reason instead of killing this lane's chat.
  expect-log '"kind":"kill".*"err":"could not identify this chat: TMUX is empty"'
  e_out="$(printf '{"session_id":"lane-no-such-session","prompt":"e"}' | pfm internal exit-intercept 2>&1)"
  plain_out="$(printf '{"session_id":"lane-no-such-session","prompt":"hello"}' | pfm internal exit-intercept 2>&1)"
  if [ "$e_out" = "$plain_out" ] || ! grep -qF 'TMUX is empty' <<<"$e_out"; then
    bad="$bad exit-intercept answered 'e' and 'hello' identically ($(one_line "$e_out")) — the e/kill path is not distinguished;"
  fi
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "disposable kill → killed=true and server gone, unkill by name → killed=false; $CHAT still live; exit-intercept distinguishes 'e' from prose ($(one_line "$e_out" | cut -c1-120))"
  fi
fi

# ─── E1.21 — SessionEnd closes the pane without stranding a reload ──────────

beat E1.21-exit-close
spends none
target_live "$CHAT"
if requires E1.01-open-seat1; then
  bad=""
  close_out="$(printf '{"session_id":"lane-no-such-session","reason":"other"}' | pfm internal exit-close 2>&1)"
  close_rc=$?
  [ "$close_rc" -le 1 ] || bad="$bad exit-close on an unknown session exited $close_rc: $(one_line "$close_out");"
  live_chat "$CHAT" || bad="$bad the hook bodies took $CHAT down with them (no live row);"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "exit-close answers an unknown session without stranding anything; $CHAT still live"
  fi
fi

# ─── E1.22 — /handoff ───────────────────────────────────────────────────────

beat E1.22-handoff
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  parent_id="$(live_field "$CHAT" 2)"
  fork_name="E1_BRANCH_$$"
  tmux_dir="$(_lane_tmux_dir)"
  before_sockets="$(find "$tmux_dir" -maxdepth 1 -type s -exec basename '{}' \; | sort)"
  out="$(pfm chat branch --engine claude --session-id "$parent_id" --cwd "$CWD" --name "$fork_name" 2>&1)"
  rc=$?
  bad="" fork_id="" read_rc=1
  if [ "$rc" -ne 0 ]; then
    bad="pfm chat branch exited $rc: $(one_line "$out");"
  elif ! wait_for 60 "[ -n \"\$(live_field '$fork_name' 2)\" ]"; then
    bad="branch returned but $fork_name has no live row: ${LANE_WAIT_WHY:-timeout};"
  else
    fork_id="$(live_field "$fork_name" 2)"
    fork_sock="$(live_field "$fork_name" 11)"
    fork_read="$(pfm chat read "$fork_id" --json 2>&1)"; read_rc=$?
    if [ "$fork_id" = "$parent_id" ] || [ "$read_rc" -ne 0 ]; then
      bad="branch row $fork_id was not a readable new session (read rc $read_rc): $(one_line "$fork_read");"
    fi
  fi
  if [ -n "$(live_field "$fork_name" 2)" ]; then
    end_out="$(pfm chat end "$fork_name" 2>&1)"
    end_rc=$?
    [ "$end_rc" -eq 0 ] || bad="$bad fork end exited $end_rc: $(one_line "$end_out");"
  fi
  if [ -n "${fork_sock:-}" ]; then
    LANE_ANCHOR= wait_for 10 "! tmux -S '$tmux_dir/$fork_sock' list-sessions >/dev/null 2>&1" ||
      bad="$bad fork server $fork_sock was still present after end: ${LANE_WAIT_WHY:-no wait reason recorded};"
  fi
  after_sockets="$(find "$tmux_dir" -maxdepth 1 -type s -exec basename '{}' \; | sort)"
  while IFS= read -r fork_sock; do
    [ -n "$fork_sock" ] || continue
    if tmux -S "$tmux_dir/$fork_sock" list-sessions >/dev/null 2>&1; then
      tmux -S "$tmux_dir/$fork_sock" kill-server 2>/dev/null ||
        bad="$bad fork socket $fork_sock could not be killed;"
    fi
  done < <(comm -13 <(printf '%s\n' "$before_sockets") <(printf '%s\n' "$after_sockets"))
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "branch created readable new session $fork_id from $parent_id and cleaned up its pane"
  fi
fi

# ─── E1.23 — the managed launcher entries ───────────────────────────────────

beat E1.23-launcher
spends none
target_live "$CHAT"
if requires E1.01-open-seat1; then
  bad=""
  launcher="$HOME/.local/bin/claude"
  [ -e "$launcher" ] || bad="$bad no managed launcher at $launcher (pfm install stages it);"
  real="$(readlink -f "$launcher" 2>/dev/null)"
  grep -q 'pfm' <<<"$real" || bad="$bad $launcher resolves to '${real:-<unresolvable>}', not a pfm shim;"
  # `pfm internal claude-version` (internal/hookentry/claude_version.go) prints
  # the newest MANAGED build under ~/.local/share/claude/versions and exits 127
  # with EMPTY stdout when that directory holds none — the contract its own Go
  # test pins. 127 is therefore a real answer ("this machine runs Claude from
  # somewhere else"), not a missing verb: the beat asserts the branch it is in,
  # and only a third shape is a ✗.
  versions_dir="$HOME/.local/share/claude/versions"
  expect-log '"hook":"claude-version","decision":"error","exit":127'
  ver="$(pfm internal claude-version 2>/dev/null)"
  ver_rc=$?
  ver_err="$(pfm internal claude-version 2>&1 >/dev/null)"
  case "$ver_rc" in
    0)
      [ -n "$ver" ] || bad="$bad pfm internal claude-version exited 0 and printed nothing (exit 0 promises the newest build's path);"
      [ -x "$ver" ] || bad="$bad pfm internal claude-version printed '$ver', which is not an executable file;"
      version_note="managed build $ver"
      ;;
    127)
      [ -z "$ver" ] || bad="$bad pfm internal claude-version exited 127 (no managed build) but still printed '$(one_line "$ver")';"
      if [ -n "$(find "$versions_dir" -maxdepth 1 -type f -perm -u+x 2>/dev/null | head -1)" ]; then
        bad="$bad pfm internal claude-version exited 127 while $versions_dir DOES carry an executable build;"
      fi
      version_note="no managed build under $versions_dir — 127 with empty stdout, the documented absence (this container launches Claude from $real)"
      ;;
    *)
      bad="$bad pfm internal claude-version exited $ver_rc — neither 0 (a path) nor 127 (no managed build): $(one_line "$ver_err");"
      version_note="exit $ver_rc"
      ;;
  esac
  sl="$(printf '{"session_id":"x","model":{"display_name":"sonnet"},"workspace":{"current_dir":"%s"}}' "$CWD" |
    pfm internal statusline 2>&1)"
  sl_rc=$?
  [ "$sl_rc" -eq 0 ] || bad="$bad pfm internal statusline (the alias) exited $sl_rc ($(one_line "$sl"));"
  [ -n "$sl" ] || bad="$bad pfm internal statusline (the alias) rendered nothing;"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "launcher $launcher → $real · claude-version: $version_note · statusline alias renders"
  fi
fi

# ─── E1.24 — the shared headless-verb exit contract ─────────────────────────

beat E1.24-exit-contract
spends none
target_live "$CHAT"
if requires E1.01-open-seat1; then
  bad=""
  pfm chat status NO_SUCH_CHAT_LANE >/dev/null 2>&1
  rc=$?
  [ "$rc" -eq 4 ] || bad="$bad status on a nonexistent chat exited $rc (want 4, 'no such chat');"
  pfm chat last NO_SUCH_CHAT_LANE >/dev/null 2>&1
  rc=$?
  [ "$rc" -eq 4 ] || bad="$bad last on a nonexistent chat exited $rc (want 4);"
  pfm chat inject NO_SUCH_CHAT_LANE "x" >/dev/null 2>&1
  rc=$?
  [ "$rc" -eq 4 ] || [ "$rc" -eq 6 ] || bad="$bad inject on a nonexistent chat exited $rc (want 4 no-such-chat or 6 not-delivered);"
  pfm chat status >/dev/null 2>&1
  rc=$?
  [ "$rc" -eq 2 ] || bad="$bad status with no target exited $rc (want 2, usage);"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "the headless verbs hold the shared exit contract (4 no such chat, 2 usage, 6 not delivered)"
  fi
fi

# ─── E1.27 — reminder set / ls / rm, and the fire tick ─────────────────────

beat E1.27-reminder
spends none
target_live "$CHAT"
if requires E1.01-open-seat1; then
  bad=""
  prompt="lane reminder probe $$"
  session="$(live_field "$CHAT" 2)"
  set_out="$(pfm chat reminder set --every 2h --prompt "$prompt" "$CHAT" 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] || bad="$bad reminder set on $CHAT exited $rc: $(one_line "$set_out");"
  listed="$(pfm chat reminder ls --json 2>&1)"
  rc=$?
  row="$(jq -c --arg p "$prompt" '[.[] | select(.prompt == $p)] | first // empty' <<<"$listed" 2>/dev/null)"
  id="$(jq -r '.id // empty' <<<"$row" 2>/dev/null)"
  if [ "$rc" -ne 0 ] || [ -z "$id" ]; then
    bad="$bad reminder ls --json (exit $rc) does not list the reminder just set: $(one_line "$listed");"
  else
    keyed="$(jq -r '.session_id' <<<"$row")"
    [ "$keyed" = "$session" ] || bad="$bad the reminder is keyed to session '$keyed', want $CHAT's live session '$session';"
    fire_out="$(pfm internal reminder-fire 2>&1)"
    rc=$?
    [ "$rc" -eq 0 ] || bad="$bad the fire tick exited $rc with no reminder due: $(one_line "$fire_out");"
    fired="$(pfm chat reminder ls --json 2>/dev/null | jq -r --argjson id "$id" '.[] | select(.id == $id) | .last_fired' 2>/dev/null)"
    case "$fired" in
      0001-*) ;;
      "") bad="$bad reminder $id is missing from ls after the fire tick;" ;;
      *) bad="$bad the fire tick fired reminder $id two hours early (last_fired '$fired');" ;;
    esac
    rm_out="$(pfm chat reminder rm "$id" 2>&1)"
    rc=$?
    [ "$rc" -eq 0 ] || bad="$bad reminder rm $id exited $rc: $(one_line "$rm_out");"
    pfm chat reminder ls --json 2>/dev/null | jq -e --argjson id "$id" 'any(.[]; .id == $id)' >/dev/null 2>&1 &&
      bad="$bad reminder $id is still listed after rm;"
    pfm chat reminder rm "$id" >/dev/null 2>&1
    rc=$?
    [ "$rc" -eq 1 ] || bad="$bad a second rm of reminder $id exited $rc (want 1, no such reminder);"
  fi
  pfm chat reminder >/dev/null 2>&1
  rc=$?
  [ "$rc" -eq 2 ] || bad="$bad reminder with no verb exited $rc (want 2, usage);"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "a reminder set on $CHAT is keyed to its live session, left unfired by a tick before it is due, and removed by rm"
  fi
fi

# ─── E1.25 — end ────────────────────────────────────────────────────────────

beat E1.25-end
spends none
target_live "$CHAT"
if requires E1.01-open-seat1 && beat_settle E1.25-end; then
  sock="$(live_field "$CHAT" 11)"
  out="$(pfm chat end "$CHAT" 2>&1)"
  rc=$?
  LANE_ANCHOR= wait_for 10 "! live_chat '$CHAT' && ! tmux -S '$(_lane_tmux_dir)/$sock' list-sessions >/dev/null 2>&1" || end_wait="$LANE_WAIT_WHY"
  if [ "$rc" -ne 0 ]; then
    fail "pfm chat end exited $rc: $(one_line "$out")"
  elif live_chat "$CHAT"; then
    fail "end exited 0 but $CHAT is still a live row: $(one_line "$(chat_row "$CHAT")"); ${end_wait:-no wait reason recorded}"
  elif tmux -S "$(_lane_tmux_dir)/$sock" list-sessions >/dev/null 2>&1; then
    fail "end exited 0 but the tmux server on $sock still answers list-sessions: ${end_wait:-no wait reason recorded}"
  elif [ -n "${end_wait:-}" ]; then
    fail "end state did not converge inside 10s: $end_wait"
  else
    pass "the chat's whole tmux server is gone (socket $sock no longer answers)"
  fi
fi

lane_end

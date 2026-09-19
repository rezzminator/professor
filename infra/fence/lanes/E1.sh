#!/usr/bin/env bash
# E1.sh — lane E1, Claude: one chat on one seat, walked depth-first from the
# spawn ceremony through the whole `/reload` matrix and every outside-in verb,
# and ended. Runs INSIDE a lane container (run.sh), never on a host.
#
#   run.sh --lanes E1            solo, from a fresh root
#   run.sh                       in the sequence, after O1
#
# Every beat asserts from pfm's OWN report (`pfm ls --tsv`, a verb's exit code,
# the chat's last assistant message) or from the pane, never from a model's
# prose: a beat that can only be satisfied by what the model said is a beat
# asserting the wrong thing. Beat ids and their landscape ids are the contract in
# beats.md and map.tsv — check-map.sh fails when this file and those disagree.
#
# Cost: one Claude seat (`--seats cc:1`, seat 1 by default) plus one reload onto
# the next configured seat for the `--account` beat. Roughly 25 short turns.
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
CONFIG="$HOME/.config/pfm/pfm.config.json"
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
  "[ \"\$(curl -s -o /dev/null -w '%{http_code}' -m 2 http://127.0.0.1:$PORT/mcp/chat)\" != 000 ]" \
  "bash /worktree/infra/demo/daemon.sh" ||
  lane_abort "the chat MCP daemon never answered on :$PORT — no chat can call a chat_* tool"

# ─── E1.01 — the spawn ceremony ─────────────────────────────────────────────

# open_main — the lane's chat, opened the one way: E1.01 spawns it and the
# library's single re-open (lane_reopen) spends the same command after the
# chat dies under a later beat.
open_main() {
  pfm chat new --name "$CHAT" --engine cc --account "$SEAT" --cwd "$CWD" --await --timeout 300 \
    "You are $CHAT, the chat an automated Tier B lane drives. Reply with one word: ready. Then wait and do exactly what each next message says, nothing more." 2>&1
}
lane_reopen 'open_main'

beat E1.01-open-seat1 K1
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

beat E1.02-statusline-theme T31 T33 T35
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
  if window="$(tmux -S "$sock" list-windows -F '#{window_name}' 2>&1)"; then
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
reload_via_pane() {
  local needle="$1"
  shift
  local out
  REPLY_WHY=""
  out="$(pfm chat inject --allow-unsigned "$CHAT" "/reload $* --then \"reply with exactly one word: $needle\"" 2>&1)" || {
    REPLY_WHY="pfm chat inject refused the /reload prompt: $(one_line "$out")"
    return 1
  }
  wait_last "$CHAT" "$needle" 300
  case $? in
    0) return 0 ;;
    2) REPLY_WHY="$LANE_WAIT_WHY (waiting for $needle)"; return 1 ;;
    *) REPLY_WHY="no $needle from $CHAT in 300s; its last: $(one_line "$(pfm chat last "$CHAT" 2>&1)")"; return 1 ;;
  esac
}

beat E1.03-reload-account C50
spends "cc:${ALT:-none}"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  if [ -z "$ALT" ]; then
    # Not a failure: this run was given one seat, so there is no second seat to
    # reboot onto. The roster the container carries IS the run's --seats
    # (lanes/creds.sh), so an empty ALT is the run's own shape, not a defect.
    blocked "seats $LANE_SEATS" "no second seat in this run — /reload --account needs two credentialed seats (run with --seats cc:1,cc:2)"
  else
    before="$(live_field "$CHAT" 9)"
    if ! reload_via_pane RELOADED-ACCT --account "$ALT"; then
      fail "$REPLY_WHY"
    elif [ "$(live_field "$CHAT" 9)" != "$ALT" ]; then
      fail "the steer ran but the row still reports account $(live_field "$CHAT" 9) (was $before, asked for $ALT)"
    else
      pass "rebooted in place onto seat $ALT (row account $before → $ALT) and ran its --then steer"
    fi
  fi
fi

beat E1.04-reload-model-effort C51 C52
spends "cc:${ALT:-$SEAT}"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  if ! reload_via_pane RELOADED-MODEL --model sonnet --effort medium; then
    fail "$REPLY_WHY"
  elif ! live_chat "$CHAT"; then
    fail "the steer ran but $CHAT has no live row after --model/--effort"
  else
    pass "rebooted under --model sonnet --effort medium; pane statusline: $(one_line "$(pane "$CHAT" | tail -2)" | cut -c1-160)"
  fi
fi

beat E1.05-reload-1h C53 K26
spends "cc:${ALT:-$SEAT}"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  if ! reload_via_pane RELOADED-1H-ON --1h on; then
    fail "--1h on: $REPLY_WHY"
  else
    on_pane="$(pane "$CHAT" | tail -3)"
    if ! reload_via_pane RELOADED-1H-OFF --1h off; then
      fail "--1h off: $REPLY_WHY (the on-reboot had already landed)"
    else
      pass "the cache window toggled on and off, each a reboot in place; pane after --1h on: $(one_line "$on_pane" | cut -c1-120)"
    fi
  fi
fi

# reload_new_on_socket <flags…> — `/reload --new` followed BY SOCKET, not by
# name. The reboot takes the name with it: the old session id keeps `$CHAT` as a
# resume row and the fresh live session is auto-named from its own steer
# ("Reloaded-new"), so a name-addressed wait sits on a dead conversation. The
# socket is unchanged across the reboot (column 11), so the fresh session id
# appearing on it IS the assertion. Prints nothing; $REPLY_WHY on failure,
# $NEW_ID and $NEW_NAME on success.
NEW_ID="" NEW_NAME=""
reload_new_on_socket() {
  local sock="$1" was="$2" out
  shift 2
  REPLY_WHY="" NEW_ID="" NEW_NAME=""
  out="$(pfm chat inject --allow-unsigned "$CHAT" "/reload $* --then \"reply with exactly one word: RELOADED-NEW\"" 2>&1)" || {
    REPLY_WHY="pfm chat inject refused the /reload prompt: $(one_line "$out")"
    return 1
  }
  if ! wait_for 300 "[ -n \"\$(socket_field '$sock' 2)\" ] && [ \"\$(socket_field '$sock' 2)\" != '$was' ]"; then
    REPLY_WHY="no fresh session id on socket $sock in 300s (it still reads '$(socket_field "$sock" 2)'); ${LANE_WAIT_WHY:-no wait reason recorded}"
    return 1
  fi
  NEW_ID="$(socket_field "$sock" 2)"
  NEW_NAME="$(socket_field "$sock" 5)"
  return 0
}

beat E1.06-reload-new C54 C55 L33
spends "cc:${ALT:-$SEAT}"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  sock="$(live_field "$CHAT" 11)"
  id_before="$(socket_field "$sock" 2)"
  anchor_socket "$sock" # from here the NAME is not a handle; the socket is
  observed=""
  if ! reload_new_on_socket "$sock" "$id_before" --new; then
    fail "--new: $REPLY_WHY"
  else
    id_new="$NEW_ID"
    # ASSERTED OBSERVATION, not a verdict: `--new` does not carry the chat's
    # name onto the reborn session — the name stays with the id left behind
    # (now a resume row) and the live session is auto-named from its steer.
    # Whether that is the product's intent is the owner's ruling; the lane
    # records what it saw and renames the live session back so later beats,
    # which address the chat BY NAME, resolve at all.
    observed="observed: after --new the live session on $sock is named '$NEW_NAME' (the label '$CHAT' stayed with the id left behind)"
    if [ "$NEW_NAME" != "$CHAT" ]; then
      name_out="$(pfm chat name "$id_new" "$CHAT" 2>&1)"
      name_rc=$?
      sleep 2
    else
      name_out="the reborn session already carried the label" name_rc=0
    fi
    if [ "$id_new" = "$id_before" ]; then
      fail "--new kept the same session id $id_before on socket $sock — it must open a fresh session"
    elif [ "$name_rc" -ne 0 ] || [ "$(socket_field "$sock" 5)" != "$CHAT" ]; then
      fail "the reborn session could not be renamed back to $CHAT (pfm chat name exited $name_rc: $(one_line "$name_out")); the row on $sock reads '$(socket_field "$sock" 5)' — $observed"
    elif ! reload_new_on_socket "$sock" "$id_new" --new --hide; then
      fail "--new --hide: $REPLY_WHY (plain --new had already produced $id_new) — $observed"
    else
      id_hide="$NEW_ID"
      [ "$NEW_NAME" = "$CHAT" ] || pfm chat name "$id_hide" "$CHAT" >/dev/null 2>&1
      sleep 2
      visible="$(pfm ls --tsv 2>/dev/null | awk -F'\t' -v n="$CHAT" 'NR > 1 && $5 == n && $10 == "false" { c++ } END { print c + 0 }')"
      if [ "$visible" -ne 1 ]; then
        fail "after --new --hide, $visible unhidden rows carry the name $CHAT (want exactly 1: the new session) — $observed"
      else
        pass "fresh session ids $id_before → $id_new → $id_hide on one unchanged socket $sock; --hide left exactly one visible row · $observed"
      fi
    fi
  fi
fi

# ─── E1.26 — one live row plus its own resume row resolves to the live row ──
# Placed here, and nowhere else: E1.06's `--new` is what leaves the name held by
# a live session AND the conversation it replaced, which is the shape the
# resolver has to get right for every later name-addressed beat.
# resolve.ResolveRosterName (internal/resolve/roster.go) dedupes this exact
# pair — of several exact-name matches, the unique LIVE one wins instead of
# refusing ambiguous (internal/resolve/roster_test.go
# TestResolveRosterNamePrefersTheUniqueLiveRow pins it at the unit layer).

beat E1.26-resolver-prefers-live C32
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  live_rows="$(pfm ls --tsv 2>/dev/null | awk -F'\t' -v n="$CHAT" 'NR > 1 && $5 == n && $1 ~ /^live-/ { c++ } END { print c + 0 }')"
  resume_rows="$(pfm ls --tsv 2>/dev/null | awk -F'\t' -v n="$CHAT" 'NR > 1 && $5 == n && $1 !~ /^live-/ { c++ } END { print c + 0 }')"
  if [ "$live_rows" -ne 1 ] || [ "$resume_rows" -lt 1 ]; then
    fail "the shape this beat exists to assert is not present: $live_rows live row(s) and $resume_rows resume row(s) carry '$CHAT' (want exactly 1 live plus at least its own resume row) — nothing was asserted"
  else
    out="$(pfm chat inject --allow-unsigned "$CHAT" "reply with exactly one word: RESOLVE-OK" 2>&1)"
    rc=$?
    if [ "$rc" -ne 0 ]; then
      fail "'$CHAT' held by 1 live row and $resume_rows resume row(s) of its own refused (exit $rc): $(one_line "$out") — resolve.ResolveRosterName's unique-live-row rule should have taken the live row"
    elif ! wait_last "$CHAT" RESOLVE-OK 240; then
      fail "the inject was accepted but never landed: ${LANE_WAIT_WHY:-no wait reason recorded}"
    else
      pass "'$CHAT' resolved to its one live row with $resume_rows resume row(s) of its own beside it"
    fi
  fi
fi

beat E1.07-reload-then C56 X39
spends "cc:${ALT:-$SEAT}"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  if ! reload_via_pane THEN-OK; then
    fail "$REPLY_WHY"
  else
    pass "a bare /reload --then rebooted and its detached waiter (pfm internal then) delivered the steer"
  fi
fi

beat E1.08-reload-sock C57
spends "cc:${ALT:-$SEAT}"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  sock="$(live_field "$CHAT" 11)"
  out="$(pfm chat reload --sock "$sock" --then "reply with exactly one word: SOCK-OK" 2>&1)"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    fail "pfm chat reload --sock $sock exited $rc: $(one_line "$out")"
  elif ! wait_last "$CHAT" SOCK-OK 300; then
    fail "reload --sock accepted but no SOCK-OK: ${LANE_WAIT_WHY:-no wait reason recorded}; last: $(one_line "$(pfm chat last "$CHAT" 2>&1)")"
  else
    pass "reload addressed by its own socket $sock rebooted and steered"
  fi
fi

# ─── E1.09 — /reload typed WHILE the chat is busy ───────────────────────────

beat E1.09-reload-while-busy L32 X35 X36 L34
spends "cc:${ALT:-$SEAT}"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  busy="$(pfm chat inject --allow-unsigned "$CHAT" \
    "Count from 1 to 40, one number per line, pausing about a second between numbers. Do not stop early." 2>&1)"
  busy_rc=$?
  [ "$busy_rc" -eq 0 ] || _lane_log_only "   E1.09: the busy-making inject exited $busy_rc: $(one_line "$busy")"
  sleep 8
  out="$(pfm chat inject --allow-unsigned --force-now "$CHAT" \
    "/reload --then \"reply with exactly one word: BUSY-RELOADED\"" 2>&1)"
  rc=$?
  # The hold notice is a tmux display-message (Wave 0), which lands in the
  # client's MESSAGE LOG, not in the pane scrollback — both surfaces are read,
  # and the failure names both, so "not on the pane" is never mistaken for
  # "never announced".
  sock="$(live_field "$CHAT" 11)"
  hold=""
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    hold="$({ tmux -S "$sock" show-messages 2>&1; pane "$CHAT"; } |
      grep -F 'pfm reload: waiting for this turn to end' | tail -1)"
    [ -n "$hold" ] && break
    sleep 3
  done
  if [ "$rc" -ne 0 ]; then
    fail "the /reload typed during a busy turn was refused (exit $rc): $(one_line "$out")"
  elif ! wait_last "$CHAT" BUSY-RELOADED 420; then
    fail "no BUSY-RELOADED in 420s after a mid-turn /reload; last: $(one_line "$(pfm chat last "$CHAT" 2>&1)")"
  elif [ -z "$hold" ]; then
    fail "the reboot landed but neither the tmux message log (show-messages on $sock) nor the pane carried the hold notice 'pfm reload: waiting for this turn to end, then rebooting this chat' — Wave 0 (docs/dev/trains/testing-foundation/waves/0-reload-feedback) has not landed in this build"
  else
    pass "hold notice on the pane ('$(one_line "$hold")'), then the reboot and its steer"
  fi
fi

# ─── E1.10 — /reload onto a seat with no credential ─────────────────────────

beat E1.10-reload-credential K23
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  if [ -z "$ALT" ]; then
    blocked "seats $LANE_SEATS" "no second seat in this run — the absent-credential refusal needs a seat to reload ONTO (run with --seats cc:1,cc:2)"
  else
    alt_dir="$(jq -r --argjson want "$ALT" '.accounts[] | select(.id == $want) | .configDir' "$CONFIG")"
    case "$alt_dir" in "~"*) alt_dir="$HOME${alt_dir#\~}" ;; esac
    moved=0
    if [ -f "$alt_dir/.credentials.json" ]; then mv "$alt_dir/.credentials.json" "$alt_dir/.credentials.json.lane"; moved=1; fi
    # --sock is the ONE handle a caller outside the pane has: with it,
    # reloadTarget takes the socket's single live pane; without it the command
    # falls to ambient identity and refuses ("this chat is not inside tmux").
    # The socket is read from the LIVE row here, never carried from an earlier beat.
    sock="$(live_field "$CHAT" 11)"
    out="$(pfm chat reload --sock "$sock" --account "$ALT" 2>&1)"
    rc=$?
    pane_txt="$(pane "$CHAT" | tail -6)"
    [ "$moved" -eq 1 ] && mv "$alt_dir/.credentials.json.lane" "$alt_dir/.credentials.json"
    if [ "$rc" -eq 0 ] && ! printf '%s' "$out$pane_txt" | grep -qiE "credential|not.?logged|login"; then
      fail "reload --account $ALT onto a seat with NO credential was accepted (exit 0) and nothing named the seat: $(one_line "$out")"
    elif ! printf '%s' "$out$pane_txt" | grep -qiE "credential|not.?logged|login"; then
      fail "reload refused with exit $rc but named neither the credential nor the seat: $(one_line "$out")"
    else
      pass "refused by name (exit $rc): $(one_line "$out$pane_txt" | cut -c1-200)"
    fi
  fi
fi

# ─── E1.11 — the role re-arm crumb ──────────────────────────────────────────

beat E1.11-role-rearm K22 L38
spends "cc:$SEAT"
target "$ROLE_CHAT"
if requires E1.01-open-seat1; then
  mkdir -p "$CWD/.claude/agents"
  cat >"$CWD/.claude/agents/lane-role.md" <<'ROLE'
---
name: lane-role
description: the constitution a Tier B lane re-arms
---
You are the lane role. Whenever you are asked who you are, answer with exactly: LANE-ROLE.
ROLE
  out="$(pfm chat new --name "$ROLE_CHAT" --engine cc --account "$SEAT" --cwd "$CWD" --role lane-role \
    --await --timeout 300 "Reply with one word: ready." 2>&1)"
  rc=$?
  sock="$(live_field "$ROLE_CHAT" 11)"
  crumb=""
  for candidate in "$SID_DIR"/role-*; do
    [ -f "$candidate" ] || continue
    case "$candidate" in *"${sock##*/}"*) crumb="$candidate"; break ;; esac
  done
  if [ "$rc" -ne 0 ]; then
    fail "pfm chat new --role lane-role exited $rc: $(one_line "$out")"
  elif [ -z "$crumb" ]; then
    fail "no role re-arm crumb for socket ${sock:-<none>} in $SID_DIR (role-<socket>); crumbs present: $(one_line "$(printf '%s ' "$SID_DIR"/role-*)")"
  elif ! grep -q 'lane-role' "$crumb"; then
    fail "the crumb $crumb does not name the lane-role artifact: $(one_line "$(cat "$crumb")")"
  else
    if ! pfm chat inject --allow-unsigned "$ROLE_CHAT" "/reload --then \"answer who you are\"" >/dev/null 2>&1; then
      fail "the crumb was written ($crumb) but the /reload that must re-arm it was refused"
    elif ! wait_last "$ROLE_CHAT" LANE-ROLE 300; then
      fail "after the reload the chat did not answer LANE-ROLE — the role constitution was not re-applied; last: $(one_line "$(pfm chat last "$ROLE_CHAT" 2>&1)")"
    else
      pass "crumb $crumb re-applied the lane-role constitution after the reboot"
    fi
  fi
fi

# ─── E1.12 — status, every form, and the sidechain override ─────────────────

beat E1.12-status C22 C23 C24 C25 C26 C27 L5
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  bad=""
  base="$(pfm chat status "$CHAT" 2>&1)" || bad="$bad status base exited non-zero ($(one_line "$base"));"
  printf '%s' "$base" | grep -qiE 'idle|working' || bad="$bad status base named neither idle nor working: $(one_line "$base");"
  json="$(pfm chat status "$CHAT" --json 2>&1)" || bad="$bad status --json exited non-zero;"
  printf '%s' "$json" | jq -e . >/dev/null 2>&1 || bad="$bad status --json is not JSON: $(one_line "$json");"
  summary="$(pfm chat status "$CHAT" --summary 2>&1)" || bad="$bad status --summary exited non-zero ($(one_line "$summary"));"
  ask="$(pfm chat status "$CHAT" --ask 2>&1)" || bad="$bad status --ask exited non-zero ($(one_line "$ask"));"
  eng="$(pfm chat status "$CHAT" --engine claude --summary 2>&1)" || bad="$bad status --engine claude --summary exited non-zero ($(one_line "$eng"));"
  mdl="$(pfm chat status "$CHAT" --model haiku --summary 2>&1)" || bad="$bad status --model haiku --summary exited non-zero ($(one_line "$mdl"));"
  # L5: a background sub-agent must keep the chat `working` even while its own
  # pane looks quiet — the sidechain override, read from the operator's side.
  pfm chat inject --allow-unsigned "$CHAT" \
    "Use the Task tool to launch exactly one subagent that lists three files in this directory. While it works, say nothing else." >/dev/null 2>&1
  working=""
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    printf '%s' "$(pfm chat status "$CHAT" 2>&1)" | grep -qi working && { working=yes; break; }
    sleep 4
  done
  [ -n "$working" ] || bad="$bad status never reported working while a background sub-agent ran (L5 sidechain override);"
  wait_for 300 "pfm chat status '$CHAT' 2>/dev/null | grep -qi idle" ||
    bad="$bad the chat never returned to idle after the sub-agent;"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "base/--json/--summary/--ask/--engine/--model all answered; working under a live sub-agent, then idle"
  fi
fi

# ─── E1.13 — last, read, stream ─────────────────────────────────────────────

beat E1.13-last-read-stream C28 C29 C30 C31
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
    found="$(pfm chat find "$excerpt" 2>&1)"
    found_rc=$?
    [ "$found_rc" -eq 0 ] || bad="$bad chat find <excerpt-file> exited $found_rc ($(one_line "$found"));"
    printf '%s' "$found" | grep -qF "$sid" ||
      bad="$bad chat find matched a session other than the chat's own $sid: $(one_line "$found");"
    file_read="$(pfm chat read "$excerpt" 20 2>&1)"
    file_rc=$?
    [ "$file_rc" -eq 0 ] || bad="$bad chat read <excerpt-file> exited $file_rc ($(one_line "$file_read"));"
    printf '%s' "$file_read" | grep -q 'Extracted ->' ||
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

beat E1.14-capture-keys C41 C42
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  cap="$(pfm chat capture "$CHAT" 2>&1)"
  rc=$?
  typed="LANE-KEYS-$$"
  keys_out="$(pfm chat keys --literal --capture "$CHAT" "$typed" 2>&1)"
  keys_rc=$?
  sleep 2
  after="$(pane "$CHAT")"
  pfm chat keys "$CHAT" Escape >/dev/null 2>&1
  pfm chat keys "$CHAT" C-u >/dev/null 2>&1
  if [ "$rc" -ne 0 ] || [ -z "$cap" ]; then
    fail "capture exited $rc with $(printf '%s' "$cap" | wc -c | tr -d ' ') bytes: $(one_line "$cap")"
  elif [ "$keys_rc" -ne 0 ]; then
    fail "keys --literal --capture exited $keys_rc: $(one_line "$keys_out")"
  elif ! printf '%s' "$after$keys_out" | grep -qF "$typed"; then
    fail "the literal keys never appeared on the pane or in the capture: $(one_line "$after" | cut -c1-200)"
  else
    pass "capture read $(printf '%s' "$cap" | wc -l | tr -d ' ') pane lines; keys typed '$typed' into the composer and Escape/C-u cleared it"
  fi
fi

# ─── E1.15 — ask ────────────────────────────────────────────────────────────

beat E1.15-ask C39
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  out="$(pfm chat ask "$CHAT" --timeout 240 "reply with exactly one word: ASK-OK" 2>&1)"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    fail "pfm chat ask exited $rc: $(one_line "$out")"
  elif ! printf '%s' "$out" | grep -qF ASK-OK; then
    fail "ask returned without the fresh answer: $(one_line "$out")"
  else
    pass "ask blocked until the fresh assistant turn and returned it"
  fi
fi

# ─── E1.16 — inject, every form and both guards ─────────────────────────────

beat E1.16-inject C32 C33 C34 C35 C36 C37 L27 L28 L29 L30 L31
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  bad=""
  pfm chat inject --allow-unsigned "$CHAT" "reply with exactly one word: INJECT-OK" >/dev/null 2>&1 ||
    bad="$bad base inject was refused;"
  wait_last "$CHAT" INJECT-OK 240 || bad="$bad the base inject never reached the chat (no INJECT-OK);"
  pfm chat inject --allow-unsigned --force-now "$CHAT" "reply with exactly one word: FORCE-OK" >/dev/null 2>&1 ||
    bad="$bad --force-now was refused;"
  wait_last "$CHAT" FORCE-OK 240 || bad="$bad --force-now never landed;"
  printf 'reply with exactly one word: FILE-OK\n' >/tmp/e1-inject.txt
  pfm chat inject --allow-unsigned --file /tmp/e1-inject.txt "$CHAT" >/dev/null 2>&1 ||
    bad="$bad --file was refused;"
  wait_last "$CHAT" FILE-OK 240 || bad="$bad --file never landed;"
  pfm chat inject --allow-unsigned --then "reply with exactly one word: THEN-CHAIN" "$CHAT" \
    "reply with exactly one word: PRIMARY-OK" >/dev/null 2>&1 || bad="$bad --then was refused;"
  wait_last "$CHAT" THEN-CHAIN 300 || bad="$bad the --then steer never landed after the primary;"
  # C37: a bare /compact primary is refused by name, never typed.
  compact="$(pfm chat inject --allow-unsigned "$CHAT" "/compact" 2>&1)"
  compact_rc=$?
  if [ "$compact_rc" -eq 0 ]; then
    bad="$bad a bare /compact primary was ACCEPTED (it must be refused in favour of self-compact);"
  elif ! printf '%s' "$compact" | grep -qi 'self-compact'; then
    bad="$bad /compact was refused but did not name self-compact: $(one_line "$compact");"
  fi
  # L28: an OPEN selector menu refuses delivery by name and types nothing.
  pfm chat keys --literal "$CHAT" "/" >/dev/null 2>&1
  sleep 3
  menu="$(pfm chat inject --allow-unsigned "$CHAT" "this must not be typed into an open menu" 2>&1)"
  menu_rc=$?
  pfm chat keys "$CHAT" Escape >/dev/null 2>&1
  pfm chat keys "$CHAT" C-u >/dev/null 2>&1
  if [ "$menu_rc" -eq 0 ]; then
    bad="$bad an inject with the slash menu OPEN was accepted (want the named ABORT: OPEN selector menu refusal);"
  elif ! printf '%s' "$menu" | grep -qi 'selector menu'; then
    bad="$bad the menu-open inject failed but did not name the open selector menu: $(one_line "$menu");"
  fi
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "base/--force-now/--file/--then delivered with proof; /compact refused naming self-compact; an open menu refused by name"
  fi
fi

# ─── E1.17 — watch ──────────────────────────────────────────────────────────

beat E1.17-watch C40
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  out="$(timeout 240 pfm chat watch "$CHAT" --idle-after 10 --once 2>&1)"
  rc=$?
  if [ "$rc" -eq 124 ]; then
    fail "watch --idle-after 10 --once never returned inside 240s (the idle transition was never reported)"
  elif [ "$rc" -ne 0 ]; then
    fail "watch exited $rc: $(one_line "$out")"
  else
    pass "watch --idle-after 10 --once reported the idle transition: $(one_line "$out")"
  fi
fi

# ─── E1.18 — name, and the {name}:{group} grammar ───────────────────────────

beat E1.18-name C44 C45 K12
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  grouped="$CHAT:lane"
  sock="$(live_field "$CHAT" 11)"
  out="$(pfm chat name "$CHAT" "$grouped" 2>&1)"
  rc=$?
  sleep 3
  window="$(tmux -S "$sock" list-windows -F '#{window_name}' 2>/dev/null | head -1)"
  row_name="$(socket_field "$sock" 5)"
  back="$(pfm chat name "$grouped" "$CHAT" 2>&1)"
  back_rc=$?
  sleep 3
  if [ "$rc" -ne 0 ]; then
    fail "pfm chat name exited $rc: $(one_line "$out")"
  elif [ "$row_name" != "$grouped" ]; then
    fail "the row for socket $sock reports name '$row_name' after the rename to '$grouped'"
  elif [ "${window#*"$CHAT"}" = "$window" ]; then
    fail "the tmux window name '$window' never converged on the new label"
  elif [ "$back_rc" -ne 0 ] || [ "$(live_field "$CHAT" 11)" != "$sock" ]; then
    fail "the rename back to $CHAT failed (exit $back_rc): $(one_line "$back")"
  else
    pass "'{name}:{group}' label converged in the row and the tmux window ('$window'), then renamed back"
  fi
fi

# ─── E1.19 — kill / unkill, and the exit-intercept body ─────────────────────

beat E1.19-kill-unkill C46 C48 X28
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  bad=""
  pfm chat kill "$CHAT" >/dev/null 2>&1 || bad="$bad kill exited non-zero;"
  sleep 2
  [ "$(row_field "$CHAT" 10)" = true ] || bad="$bad the row's killed column is '$(row_field "$CHAT" 10)' after kill;"
  pfm chat unkill "$CHAT" >/dev/null 2>&1 || bad="$bad unkill exited non-zero;"
  sleep 2
  [ "$(row_field "$CHAT" 10)" = false ] || bad="$bad the row's killed column is '$(row_field "$CHAT" 10)' after unkill;"
  # X28: the prompt-hook body. Driven with a session id that belongs to NO chat,
  # so it can refuse for a named reason instead of killing this lane's chat.
  e_out="$(printf '{"session_id":"lane-no-such-session","prompt":"e"}' | pfm internal exit-intercept 2>&1)"
  plain_out="$(printf '{"session_id":"lane-no-such-session","prompt":"hello"}' | pfm internal exit-intercept 2>&1)"
  if [ "$e_out" = "$plain_out" ]; then
    bad="$bad exit-intercept answered 'e' and 'hello' identically ($(one_line "$e_out")) — the e/kill path is not distinguished;"
  fi
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "kill → killed=true, unkill → killed=false, exit-intercept distinguishes 'e' from prose ($(one_line "$e_out" | cut -c1-120))"
  fi
fi

# ─── E1.20 — self-compact leaves a receipt on the pane ──────────────────────

beat E1.20-self-compact C38 L35 L37
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  # `pfm chat self-compact` resolves the CALLER's own chat (ambient identity), so
  # from outside the fleet the chat is asked to call the tool on itself — the
  # same path infra/demo/verify.sh check_compact drives.
  out="$(pfm chat inject --allow-unsigned "$CHAT" \
    "Call the chat_self_compact tool now with focus 'lane-e1' and the steer 'reply with exactly one word: COMPACTED'. Do nothing else." 2>&1)"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    fail "the self-compact instruction could not be delivered (exit $rc): $(one_line "$out")"
  elif ! wait_last "$CHAT" COMPACTED 420; then
    fail "no COMPACTED in 420s after chat_self_compact; last: $(one_line "$(pfm chat last "$CHAT" 2>&1)")"
  else
    receipt="$(pane "$CHAT" | grep -iE 'compact' | tail -1)"
    if [ -z "$receipt" ]; then
      fail "the steer ran but no compaction receipt is visible on the pane: $(one_line "$(pane "$CHAT" | tail -4)")"
    else
      pass "compacted in place after its own turn, receipt on the pane ('$(one_line "$receipt")'), steer delivered"
    fi
  fi
fi

# ─── E1.21 — SessionEnd closes the pane without stranding a reload ──────────

beat E1.21-exit-close X27 X25
spends none
target_live "$CHAT"
if requires E1.01-open-seat1; then
  bad=""
  close_out="$(printf '{"session_id":"lane-no-such-session","reason":"other"}' | pfm internal exit-close 2>&1)"
  close_rc=$?
  [ "$close_rc" -le 1 ] || bad="$bad exit-close on an unknown session exited $close_rc: $(one_line "$close_out");"
  nudge_out="$(printf '{"session_id":"lane-no-such-session","prompt":"hello"}' | pfm internal compact-nudge 2>&1)"
  nudge_rc=$?
  [ "$nudge_rc" -le 1 ] || bad="$bad compact-nudge exited $nudge_rc: $(one_line "$nudge_out");"
  live_chat "$CHAT" || bad="$bad the hook bodies took $CHAT down with them (no live row);"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "exit-close and compact-nudge answer an unknown session without stranding anything; $CHAT still live"
  fi
fi

# ─── E1.22 — /handoff ───────────────────────────────────────────────────────

beat E1.22-handoff T38 C61
spends "cc:$SEAT"
target_live "$CHAT"
if requires E1.01-open-seat1; then
  before_ids="$(pfm ls --tsv 2>/dev/null | awk -F'\t' 'NR > 1 { print $2 }' | sort)"
  out="$(pfm chat inject --allow-unsigned "$CHAT" \
    "Run the /handoff skill now: write the handoff file and hand this conversation to a fresh sibling chat. Then say nothing else." 2>&1)"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    fail "the /handoff instruction could not be delivered (exit $rc): $(one_line "$out")"
  else
    sleep 45
    new_ids="$(pfm ls --tsv 2>/dev/null | awk -F'\t' 'NR > 1 { print $2 }' | sort)"
    added="$(comm -13 <(printf '%s\n' "$before_ids") <(printf '%s\n' "$new_ids") | grep -c . )"
    handoff_file="$(find "$CWD" /tmp "$HOME" -maxdepth 3 -name 'handoff*' -newermt '-10 minutes' 2>/dev/null | head -1)"
    if [ "$added" -eq 0 ]; then
      fail "/handoff produced no new chat row (rows before: $(printf '%s\n' "$before_ids" | grep -c .), after: $(printf '%s\n' "$new_ids" | grep -c .))"
    elif [ -z "$handoff_file" ]; then
      fail "a new chat appeared but no handoff file was written in the last 10 minutes under $CWD, /tmp or $HOME"
    else
      pass "handoff file $handoff_file written and $added new chat row(s) carried the conversation"
    fi
  fi
fi

# ─── E1.23 — the managed launcher entries ───────────────────────────────────

beat E1.23-launcher X20 X31 X38
spends none
target_live "$CHAT"
if requires E1.01-open-seat1; then
  bad=""
  launcher="$HOME/.local/bin/claude"
  [ -e "$launcher" ] || bad="$bad no managed launcher at $launcher (pfm install stages it);"
  real="$(readlink -f "$launcher" 2>/dev/null)"
  printf '%s' "$real" | grep -q 'pfm' || bad="$bad $launcher resolves to '${real:-<unresolvable>}', not a pfm shim;"
  # `pfm internal claude-version` (internal/hookentry/claude_version.go) prints
  # the newest MANAGED build under ~/.local/share/claude/versions and exits 127
  # with EMPTY stdout when that directory holds none — the contract its own Go
  # test pins. 127 is therefore a real answer ("this machine runs Claude from
  # somewhere else"), not a missing verb: the beat asserts the branch it is in,
  # and only a third shape is a ✗.
  versions_dir="$HOME/.local/share/claude/versions"
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

beat E1.24-exit-contract C66
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

# ─── E1.25 — end ────────────────────────────────────────────────────────────

beat E1.25-end C49
spends none
target_live "$CHAT"
if requires E1.01-open-seat1; then
  sock="$(live_field "$CHAT" 11)"
  out="$(pfm chat end "$CHAT" 2>&1)"
  rc=$?
  sleep 3
  if [ "$rc" -ne 0 ]; then
    fail "pfm chat end exited $rc: $(one_line "$out")"
  elif live_chat "$CHAT"; then
    fail "end exited 0 but $CHAT is still a live row: $(one_line "$(chat_row "$CHAT")")"
  elif tmux -S "$sock" list-sessions >/dev/null 2>&1; then
    fail "end exited 0 but the tmux server on $sock still answers list-sessions"
  else
    pass "the chat's whole tmux server is gone (socket $sock no longer answers)"
  fi
fi

lane_end

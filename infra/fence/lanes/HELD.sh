#!/usr/bin/env bash
# Lane HELD — real Herdr, real herdr-shield-wrapped panes, fake engines: every pfm
# operation a user drives from a held pane, through the whole chain the owner
# runs (fake engine → its hooks → pfm → Herdr socket → herdr-shield holder → back).
#
#   run.sh --lanes HELD           on its own machine, after the sequence
#
# The machine (run.sh, root.sh --machine held): systemd as PID 1, this lane as
# user lane (uid 1000) under a real user@1000.service, Herdr configured as the
# owner's — default_shell is herdr-shield, whose inner shell is pfm's wrapper, so
# every default-shell pane opens the picker inside a holder outside Herdr's
# process tree. HELD.topology proves that wrapping first and every later beat
# requires it: the lane can never pass on lane H's plain topology. Chats are
# born only through default-shell doors (the picker, `claude` typed in a held
# shell); the fake Claude is lane H's native fake with the turn directives
# H_HOLD, H_SUBAGENT and H_NOTIFY (herdr.py prepare).
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
. "$HERE/lib.sh"
lane_preamble
lane_begin HELD
CONFIG="${PFM_CONFIG:?PFM_CONFIG is required in the container}"
lane_seat_and_port "$CONFIG"
lane_require_seat_ops "$CONFIG"
export H_SESSION=lane-HELD H_FIXTURE=/tmp/lane-HELD-fixture H_FAKE_SCRIPT=/tmp/lane-HELD-fixture/fake.sh
F="$H_FIXTURE"
H_DB="${PFM_STATE_DB:-$HOME/.local/state/pfm/pfm.db}"
UP="$HOME/.config/herdr/sessions/$H_SESSION/herdr.sock"
SHIELD_PLUGIN="$HOME/.local/share/herdr-shield/plugin"
. "$HERE/herdr.sh"
h() { herdr --session "$H_SESSION" "$@"; }

# ─── prelude ────────────────────────────────────────────────────────────────

need held-user-manager 'systemctl --user --machine=1000@.host show --property=Version --value | grep -q .' false ||
  lane_abort "no systemd user manager for $(id -un): systemctl --user --machine=1000@.host printed no version"
# The machine is this run's alone, so its own config names the native fake: pfm
# in a held pane and pfm's user units (which read the default path) agree.
held_fixture() {
  mkdir -p "$F/bin" &&
    python3 "$HERE/herdr.py" prepare /worktree/infra/plugin-lab/tests/fakes/claude "$F" &&
    cc -Wall -Wextra -Werror -O2 "$HERE/fixtures/herdr-native.c" -o "$F/bin/claude" &&
    jq --arg binary "$F/bin/claude" '.claude.binary=$binary' "$CONFIG" >"$F/pfm.config.json" &&
    cat "$F/pfm.config.json" >"$CONFIG"
}
need held-fixture 'test -x "$F/bin/claude" && test -s "$H_FAKE_SCRIPT" && jq -e --arg b "$F/bin/claude" ".claude.binary == \$b" "$CONFIG"' held_fixture ||
  lane_abort "the native fake Claude could not be built into $F"
unset HERDR_CONFIG_PATH
h_server_start || lane_abort "the held Herdr server did not start (H SERVER-* line above)"
_lane_log_only "HELD native fake source $(sha256sum /worktree/infra/plugin-lab/tests/fakes/claude "$H_FAKE_SCRIPT" "$F/bin/claude")"
need held-plugins 'h plugin list | grep -q herdr-shield' 'h plugin link "$SHIELD_PLUGIN"' ||
  lane_abort "herdr-shield is not linked into the held Herdr server"
# pfm links its indicator plugin while theme sync is on, as on the owner's host.
theme_on() {
  pfm theme on >"$F/theme-on.out" 2>&1 || { cat "$F/theme-on.out"; return 1; }
  cat "$F/theme-on.out" >>"$LANE_LOG"
  wait_for 5 'h plugin list | grep -q professor.indicators' || { echo "$LANE_WAIT_WHY"; h plugin list; return 1; }
}
need held-indicator-plugin 'h plugin list | grep -q professor.indicators' theme_on ||
  lane_abort "pfm's indicator plugin is not linked into the held Herdr server"
need held-shield-doctor 'herdr-shield doctor | grep -q "^OK: Linux/systemd user manager"' false ||
  lane_abort "herdr-shield doctor: $(herdr-shield doctor 2>&1 | tail -n 1)"
workspace="$(h workspace create --label HELD --cwd /work/held --no-focus)" || lane_abort "workspace create failed"
printf '%s\n' "$workspace" >>"$LANE_LOG"
W="$(jq -er '.result.workspace.workspace_id' <<<"$workspace")" || lane_abort "workspace create named no workspace"
P0="$(jq -er '.result.root_pane.pane_id' <<<"$workspace")" || lane_abort "workspace create named no root pane"
ROOT_TAB="$(jq -er '.result.tab.tab_id' <<<"$workspace")" || lane_abort "workspace create named no root tab"

pane_json() { jq -nc --arg p "$1" '{pane_id:$p}'; }
agent_status() { h_request pane.get "$(pane_json "$1")" | jq -er '.result.pane.agent_status'; }
status_is() { [ "$(agent_status "$1")" = "$2" ]; }
seat_row() { # seat_row <seat> — backend|socket|pane|state
  sqlite3 "$H_DB" "select backend || '|' || herdr_socket || '|' || herdr_pane || '|' || state from seat where seat_id='$1';"
}
live_seat_on() { # live_seat_on <pane> — "<seat> <sid>" of the live seat on <pane>
  sqlite3 -separator ' ' "$H_DB" "select seat_id, session_id from seat where herdr_pane='$1' and state='live';"
}
start_of() { awk '{print $22}' "/proc/$1/stat" 2>/dev/null; }
argv_of() { tr '\0' ' ' <"/proc/$1/cmdline" 2>/dev/null; }
transcript_of() { find "$HOME" -path '*/projects/*' -name "$1.jsonl" -print -quit 2>/dev/null; }
# held_pane <pane> — herdr-shield lists <pane> PROTECTED with one live holder bound to it.
held_pane() {
  h_shield >"$F/shield-$1.json" &&
    jq -e --arg p "$1" '.enumeration == "OK" and any(.panes[]; .pane_id == $p and .status == "PROTECTED")' "$F/shield-$1.json" >/dev/null &&
    h_held_holder "$1" >"$F/holder-$1.json"
}
# exec_in <sid> <pane> <command> — H_EXEC under the fake engine of <sid>; its rc.
exec_in() {
  rm -f "$F/exec-$1.rc" "$F/exec-$1.out"
  h_type "$2" "H_EXEC $3" || return 1
  wait_for 15 "test -s '$F/exec-$1.rc'" || return 1
  cat "$F/exec-$1.out" >>"$LANE_LOG"
  return "$(cat "$F/exec-$1.rc")"
}
# turn_closed <transcript> <user line> — the fake's turn for <user line> wrote
# its assistant record and the stop-hook summary after its Stop hook returned.
turn_closed() {
  tail -n 3 "$1" | jq -se --arg l "$2" '[.[].type] == ["user","assistant","system"] and .[0].message.content == $l and .[2].subtype == "stop_hook_summary"' >/dev/null
}

# ─── beats ──────────────────────────────────────────────────────────────────

beat HELD.topology
topology_ready() {
  h_request pane.process_info "$(pane_json "$P0")" >"$F/topology-process.json" &&
    jq -e 'any(.result.process_info.foreground_processes[]; .name == "herdr-shield")' "$F/topology-process.json" >/dev/null &&
    held_pane "$P0"
}
if wait_for 15 topology_ready; then
  cat "$F/topology-process.json" "$F/shield-$P0.json" >>"$LANE_LOG"
  HOLDER0="$(cat "$F/holder-$P0.json")"
  holder_id="$(jq -r '.id' <<<"$HOLDER0")" holder_pid="$(jq -r '.holder.pid' <<<"$HOLDER0")"
  child_pid="$(jq -r '.child.pid' <<<"$HOLDER0")"
  shell_pid="$(jq -r '.result.process_info.shell_pid' "$F/topology-process.json")"
  bound="$(jq --arg p "$P0" --arg s "$UP" '[.holders[] | select(.binding.pane == $p and .binding.socket == $s)] | length' "$F/shield-$P0.json")"
  cgroup="$(cat "/proc/$holder_pid/cgroup" 2>&1)"
  ancestry="" pid="$child_pid"
  while [ -n "$pid" ] && [ "$pid" != 0 ] && [ "$pid" != 1 ]; do
    ancestry="$ancestry $pid"
    pid="$(awk '/^PPid:/ {print $2}' "/proc/$pid/status" 2>/dev/null)"
  done
  printf 'HELD topology holder=%s pid=%s child=%s shell_pid=%s bound=%s ancestry=%s cgroup=%s\n' \
    "$holder_id" "$holder_pid" "$child_pid" "$shell_pid" "$bound" "$ancestry" "$cgroup" >>"$LANE_LOG"
  # Herdr's shell_pid must be a pid, or "outside Herdr's pane process" proves nothing.
  if [ "$bound" = 1 ] && [[ "$cgroup" == *"/user@1000.service/app.slice/herdr-shield-$holder_id.service"* ]] &&
    [[ "$shell_pid" =~ ^[1-9][0-9]*$ ]] && [[ " $ancestry " != *" $shell_pid "* ]]; then
    pass "P0 $P0 PROTECTED: holder $holder_id (pid $holder_pid) in user@1000.service, its shell $child_pid outside Herdr's pane process $shell_pid"
  else fail "held topology: bound=$bound cgroup=[$cgroup] shell_pid=$shell_pid in ancestry=[$ancestry]"; fi
else
  h_shield >>"$LANE_LOG" 2>&1
  h pane read "$P0" >>"$LANE_LOG" 2>&1
  fail "P0 $P0 never became a protected held pane: $LANE_WAIT_WHY"
fi

beat HELD.birth-picker
SID="" SEAT0=""
if requires HELD.topology; then
  if ! wait_for 10 'h pane read "$P0" >"$F/picker-screen" && grep -q "New chat" "$F/picker-screen"'; then
    cat "$F/picker-screen" >>"$LANE_LOG"
    fail "the held pane's picker never showed its New chat row: $LANE_WAIT_WHY"
  else
    cat "$F/picker-screen" >>"$LANE_LOG"
    h_request pane.send_text "$(jq -nc --arg p "$P0" '{pane_id:$p,text:"new claude chat"}')" >>"$LANE_LOG" &&
      h_request pane.send_keys "$(jq -nc --arg p "$P0" '{pane_id:$p,keys:["enter","enter","enter","enter"]}')" >>"$LANE_LOG"
    born_p0() { read -r SEAT0 SID < <(live_seat_on "$P0") && [ -n "$SID" ] && ENGINE0="$(h_held_engine "$P0")"; }
    if wait_for 12 born_p0; then
      row="$(seat_row "$SEAT0")"
      if [ "$row" = "herdr|$UP|$P0|live" ]; then
        pass "picker birth in the held pane: seat $SEAT0 $row, engine pid $ENGINE0 under holder $holder_id"
      else fail "picker birth seat row $row, want herdr|$UP|$P0|live"; fi
    else
      h pane read "$P0" >>"$LANE_LOG" 2>&1
      sqlite3 -header "$H_DB" 'select seat_id,session_id,backend,herdr_socket,herdr_pane,state from seat;' >>"$LANE_LOG" 2>&1
      fail "no live seat with a claude under P0's holder: $LANE_WAIT_WHY"
    fi
  fi
fi
T0="$(transcript_of "$SID")"

beat HELD.name
if requires HELD.topology HELD.birth-picker; then
  out="$(pfm chat name "$SID" HELD_MAIN 2>&1)"; rc=$?
  printf 'HELD name rc=%s %s\n' "$rc" "$out" >>"$LANE_LOG"
  name="$(sqlite3 "$H_DB" "select name from seat where seat_id='$SEAT0';")"
  label="$(h_request pane.get "$(pane_json "$P0")" | jq -r '.result.pane.label')"
  if [ "$rc" = 0 ] && [ "$name" = HELD_MAIN ] && [ "$label" = HELD_MAIN ]; then pass "seat name and held pane label HELD_MAIN"
  else fail "name rc=$rc seat name=[$name] pane label=[$label] $out"; fi
fi

beat HELD.whoami
if requires HELD.topology HELD.birth-picker; then
  exec_in "$SID" "$P0" 'pfm whoami --json'; rc=$?
  if [ "$rc" = 0 ] && jq -e --arg sid "$SID" --arg seat "$SEAT0" --arg pane "$P0" --arg sock "$UP" \
    '.id == $sid and .source == "herdr-ancestry" and .backend == "herdr" and .pane == $pane and .socket_path == $sock and .seat_id == $seat' \
    "$F/exec-$SID.out" >/dev/null; then
    pass "the engine's own descendant resolves itself through the relay: $SID on $P0 at $UP"
  else fail "whoami from the held engine rc=$rc: $(one_line "$(cat "$F/exec-$SID.out" 2>&1)")"; fi
fi

beat HELD.discovery
if requires HELD.topology HELD.birth-picker HELD.name; then
  out="$(pfm ls --tsv 2>&1)"; rc=$?
  printf '%s\n' "$out" >>"$LANE_LOG"
  if [ "$rc" = 0 ] && awk -F'\t' -v sid="$SID" -v sock="$UP" '$1 == "live-claude" && $2 == sid && $5 == "HELD_MAIN" && $11 == sock { found = 1 } END { exit !found }' <<<"$out"; then
    pass "pfm ls lists HELD_MAIN live on the upstream socket"
  else fail "discovery rc=$rc: no live-claude row for $SID named HELD_MAIN on $UP"; fi
fi

beat HELD.status
if requires HELD.topology HELD.birth-picker; then
  out="$(pfm chat status "$SID" --json 2>&1)"; rc=$?
  printf '%s\n' "$out" >>"$LANE_LOG"
  if [ "$rc" = 0 ] && jq -e --arg sid "$SID" '.session_id == $sid and .state == "idle"' <<<"$out" >/dev/null; then pass "status idle for $SID"
  else fail "status rc=$rc $out"; fi
fi

beat HELD.indicator-idle
if requires HELD.topology HELD.birth-picker; then
  if wait_for 5 'status_is "$P0" idle'; then pass "Herdr reads the held pane idle"
  else fail "agent_status of $P0 is [$(agent_status "$P0" 2>&1)], want idle: $LANE_WAIT_WHY"; fi
fi

G="$F/hold-gate"
beat HELD.status-working
if requires HELD.topology HELD.birth-picker; then
  touch "$G"
  h_type "$P0" "H_HOLD $G"
  if wait_for 5 'pfm chat status "$SID" --json 2>/dev/null | jq -e ".state == \"working\"" && test -e "$G"'; then
    pass "status working while the held turn runs"
  else fail "status during the held turn: $(one_line "$(pfm chat status "$SID" --json 2>&1)") $LANE_WAIT_WHY"; fi
fi

beat HELD.indicator-working
if requires HELD.topology HELD.birth-picker; then
  if ! wait_for 5 'status_is "$P0" working && test -e "$G"'; then
    rm -f "$G"
    fail "agent_status during the held turn is [$(agent_status "$P0" 2>&1)], want working: $LANE_WAIT_WHY"
  else
    rm -f "$G"
    if wait_for 5 'status_is "$P0" idle'; then pass "working during the held turn, idle once it ends"
    else fail "agent_status after the held turn is [$(agent_status "$P0" 2>&1)], want idle: $LANE_WAIT_WHY"; fi
  fi
  wait_for 8 'turn_closed "$T0" "H_HOLD $G"' || _lane_log_only "HELD held turn did not close: $LANE_WAIT_WHY"
fi

beat HELD.subagent-hold
if requires HELD.topology HELD.birth-picker; then
  G2="$F/sub-gate"; touch "$G2"
  h_type "$P0" "H_SUBAGENT held-sub $G2"
  if ! wait_for 8 'turn_closed "$T0" "H_SUBAGENT held-sub $G2"'; then
    rm -f "$G2"
    fail "the sub-agent turn never closed in $T0: $LANE_WAIT_WHY"
  else
    after_stop="$(agent_status "$P0" 2>&1)"
    rm -f "$G2"
    if [ "$after_stop" = working ] && wait_for 5 'status_is "$P0" idle'; then
      pass "working after the main turn's Stop while its sub-agent runs, idle after SubagentStop"
    else fail "agent_status after Stop with a live sub-agent was [$after_stop], want working (then idle: $LANE_WAIT_WHY)"; fi
  fi
fi

beat HELD.notification-turn
if requires HELD.topology HELD.birth-picker; then
  G3="$F/notify-gate"; touch "$G3"
  h_type "$P0" "H_NOTIFY $G3"
  if ! wait_for 5 'status_is "$P0" working && test -e "$G3"'; then
    rm -f "$G3"
    fail "agent_status during the notification turn is [$(agent_status "$P0" 2>&1)], want working: $LANE_WAIT_WHY"
  else
    rm -f "$G3"
    if wait_for 5 'status_is "$P0" idle'; then pass "a notification turn reads working, then idle"
    else fail "agent_status after the notification turn is [$(agent_status "$P0" 2>&1)], want idle: $LANE_WAIT_WHY"; fi
  fi
fi

beat HELD.call
if requires HELD.topology HELD.birth-picker; then
  wait_for 8 'status_is "$P0" idle' || _lane_log_only "HELD pane not idle before the call: $LANE_WAIT_WHY"
  # Herdr tracks the agent pfm itself reports for a held pane, yet the pane's
  # foreground is herdr-shield's attach client: the call must take the raw path.
  printf 'HELD call agent.get %s\n' "$(h_request agent.get "$(jq -nc --arg p "$P0" '{target:$p}')" 2>&1)" >>"$LANE_LOG"
  out="$(pfm chat call --allow-unsigned "$SID" HELD_CALL_PROMPT 2>&1)"; rc=$?
  printf 'HELD call rc=%s %s\n' "$rc" "$out" >>"$LANE_LOG"
  if [ "$rc" = 0 ] && [[ "$out" == *"called LIVE into \"$P0\" — Herdr raw composer confirmed"* ]] && wait_prompt "$SID" HELD_CALL_PROMPT 8; then
    pass "a call into the held pane takes the raw path and the prompt lands"
  else fail "call rc=$rc $(one_line "$out") $LANE_WAIT_WHY"; fi
fi

beat HELD.keys
if requires HELD.topology HELD.birth-picker; then
  out="$(pfm chat keys --literal "$SID" HELD_KEY_MARK 2>&1)"; rc=$?
  if [ "$rc" = 0 ] && pfm chat keys "$SID" Enter >>"$LANE_LOG" 2>&1 && wait_prompt "$SID" HELD_KEY_MARK 5 &&
    h pane read "$P0" >"$F/keys-screen" && grep -q HELD_KEY_MARK "$F/keys-screen"; then
    pass "literal keys and Enter reach the transcript and the held pane"
  else fail "keys rc=$rc $out $LANE_WAIT_WHY"; fi
fi

beat HELD.capture
if requires HELD.topology HELD.birth-picker; then
  out="$(pfm chat capture "$SID" 2>&1)"; rc=$?
  if [ "$rc" = 0 ] && grep -q HELD_KEY_MARK <<<"$out"; then pass "capture shows HELD_KEY_MARK"
  else fail "capture rc=$rc $(one_line "$out")"; fi
fi

beat HELD.open
if requires HELD.topology HELD.birth-picker; then
  # The operator opens the chat from another held shell, as the picker does: a
  # new focused tab, picker dismissed, `pfm chat open` typed in its shell. The
  # chat comes to that pane's place and the shell's pane closes (its own exit
  # status goes with it), so the verdict reads Herdr and the engine.
  open_tab="$(h tab create --workspace "$W" 2>&1)"
  printf 'HELD open tab %s\n' "$open_tab" >>"$LANE_LOG"
  tab="$(jq -r '.result.tab.tab_id // empty' <<<"$open_tab")" open_pane="$(jq -r '.result.root_pane.pane_id // empty' <<<"$open_tab")"
  p0_pane() { h_request pane.get "$(pane_json "$P0")" | jq -c '.result.pane | {focused, tab_id}'; }
  before_pid="$(h_held_engine "$P0")" before_start="$(start_of "$(h_held_engine "$P0")")"
  holder_before="$(h_held_holder "$P0" | jq -r '.id')"
  opened() {
    # h_request exits 1 on Herdr's error envelope; pipefail would hide the code.
    jq -e '.error.code == "pane_not_found"' <<<"$(h_request pane.get "$(pane_json "$open_pane")" 2>/dev/null)" >/dev/null &&
      [ "$(p0_pane)" = "$(jq -nc --arg t "$tab" '{focused: true, tab_id: $t}')" ] &&
      [ "$(h_held_engine "$P0")" = "$before_pid" ]
  }
  if [ -z "$before_pid" ] || [ -z "$before_start" ] || [ -z "$holder_before" ]; then
    fail "open precondition: P0 has no engine under a held holder to keep (engine [$before_pid] start [$before_start] holder [$holder_before])"
  elif [ -z "$tab" ] || ! h tab focus "$tab" >>"$LANE_LOG" 2>&1 || ! wait_for 15 'held_pane "$open_pane"' || ! h_picker_dismiss "$open_pane"; then
    h pane read "$open_pane" >>"$LANE_LOG" 2>&1
    fail "open precondition: no focused held shell to open from ($tab $open_pane): $LANE_WAIT_WHY"
  elif [ "$(p0_pane | jq -r .focused)" != false ]; then
    fail "open precondition: P0 is still focused after $tab took the focus"
  elif ! h_type "$open_pane" "pfm chat open $SID" || ! wait_for 20 opened; then
    h pane read "$open_pane" >>"$LANE_LOG" 2>&1
    fail "open from $open_pane: P0 $(p0_pane 2>&1) engine $before_pid -> $(h_held_engine "$P0") $LANE_WAIT_WHY"
  elif [ "$(start_of "$before_pid")" = "$before_start" ] && [ "$(h_held_holder "$P0" | jq -r '.id')" = "$holder_before" ]; then
    pass "open brought P0 into $tab focused, closed the opening shell, same engine $before_pid and holder $holder_before"
  else fail "open: engine $before_pid start $before_start -> $(start_of "$before_pid"), holder $holder_before -> $(h_held_holder "$P0" 2>&1 | jq -r '.id')"; fi
fi

beat HELD.reload
if requires HELD.topology HELD.birth-picker; then
  before_pid="$(h_held_engine "$P0")"
  holder_before="$(h_held_holder "$P0" | jq -c '{id, child}')"
  rm -f "$F/exec-$SID.rc"
  h_type "$P0" 'H_EXEC pfm chat reload --model sonnet'
  pending=0
  reloaded() {
    [ "$(sqlite3 "$H_DB" "select reload_pending from seat where seat_id='$SEAT0';")" = 1 ] && pending=1
    RELOADED="$(h_held_engine "$P0")" && [ "$RELOADED" != "$before_pid" ] && [[ "$(argv_of "$RELOADED")" == *"--model sonnet"* ]] &&
      [ "$(sqlite3 "$H_DB" "select state || '|' || reload_pending || '|' || herdr_pane || '|' || session_id from seat where seat_id='$SEAT0';")" = "live|0|$P0|$SID" ]
  }
  if wait_for 30 reloaded; then
    holder_after="$(h_held_holder "$P0" | jq -c '{id, child}')"
    if [ "$(cat "$F/exec-$SID.rc" 2>/dev/null)" = 0 ] && [ "$holder_after" = "$holder_before" ] && [ "$pending" = 1 ]; then
      pass "reload relaunched the engine ($before_pid -> $RELOADED, --model sonnet) in the same holder and shell"
    else fail "reload exec rc=$(cat "$F/exec-$SID.rc" 2>&1) pending_seen=$pending holder $holder_before -> $holder_after"; fi
  else
    h pane read "$P0" >>"$LANE_LOG" 2>&1
    fail "no relaunched engine with --model sonnet under P0 (exec rc $(cat "$F/exec-$SID.rc" 2>&1), pending_seen=$pending): $LANE_WAIT_WHY"
  fi
  ENGINE0="$(h_held_engine "$P0")"
fi

beat HELD.shield-unlistable
expect-log 'herdr-shield list --json against'
if requires HELD.topology HELD.birth-picker; then
  mkdir -p "$F/shield-refuses"
  printf '#!/bin/sh\necho "HELD_SHIELD_REFUSES fixture" >&2\nexit 3\n' >"$F/shield-refuses/herdr-shield"
  chmod +x "$F/shield-refuses/herdr-shield"
  out="$(env PATH="$F/shield-refuses:/usr/local/bin:/usr/bin:/bin" "$HOME/.local/bin/pfm" chat status "$SID" --json 2>"$F/unlistable.err")"; rc=$?
  printf 'HELD unlistable rc=%s stdout=%s stderr=%s\n' "$rc" "$out" "$(cat "$F/unlistable.err")" >>"$LANE_LOG"
  if [ "$rc" != 0 ] && grep -qF "herdr-shield list --json against $UP: exit 3: HELD_SHIELD_REFUSES fixture" "$F/unlistable.err"; then
    pass "a herdr-shield that cannot list is an error naming the socket, never an absent holder"
  else fail "unlistable herdr-shield rc=$rc: $(one_line "$(cat "$F/unlistable.err")")"; fi
fi

# held_birth_launcher <PREFIX> [nested] — a new held tab, its picker dismissed,
# `claude` typed: <PREFIX>_PANE, <PREFIX>_SEAT, <PREFIX>_SID. With nested, claude
# is typed in a `zsh` started inside the held shell, so the held shell outlives it.
held_birth_launcher() {
  local result pane seat sid
  result="$(h tab create --workspace "$W" --env PFM_AUTO_OPEN=pfm --no-focus 2>&1)" || { printf 'tab create: %s\n' "$result"; return 1; }
  printf '%s\n' "$result" >>"$LANE_LOG"
  pane="$(jq -er '.result.root_pane.pane_id // .result.pane.pane_id' <<<"$result")" || { printf 'no pane in %s\n' "$result"; return 1; }
  printf -v "${1}_PANE" '%s' "$pane"
  wait_for 15 "held_pane '$pane'" || { printf 'pane %s never held: %s\n' "$pane" "$LANE_WAIT_WHY"; return 1; }
  h_picker_dismiss "$pane" || { printf 'picker in %s not dismissed: %s\n' "$pane" "$LANE_WAIT_WHY"; h pane read "$pane"; return 1; }
  if [ "${2:-}" = nested ]; then
    local shell; shell="$(h_held_holder "$pane" | jq -r '.child.pid')"
    h_type "$pane" zsh || return 1
    wait_for 8 "pgrep -P '$shell' -x zsh >/dev/null && h_shell_prompt '$pane'" || { printf 'no nested zsh under %s: %s\n' "$shell" "$LANE_WAIT_WHY"; h pane read "$pane"; return 1; }
  fi
  h_type "$pane" claude || return 1
  launched() { read -r seat sid < <(live_seat_on "$pane") && [ -n "$sid" ] && h_held_engine "$pane" >/dev/null; }
  wait_for 12 launched || { printf 'no live seat with a claude under %s: %s\n' "$pane" "$LANE_WAIT_WHY"; h pane read "$pane"; return 1; }
  printf -v "${1}_SEAT" '%s' "$seat"
  printf -v "${1}_SID" '%s' "$sid"
  [ "$(seat_row "$seat")" = "herdr|$UP|$pane|live" ] || { printf 'seat row %s\n' "$(seat_row "$seat")"; return 1; }
}

beat HELD.birth-launcher
PEER_PANE="" PEER_SEAT="" PEER_SID=""
if requires HELD.topology; then
  if held_birth_launcher PEER >"$F/birth-peer.out" 2>&1; then
    pass "claude typed in held pane $PEER_PANE: seat $PEER_SEAT live, its engine under the pane's holder"
  else
    cat "$F/birth-peer.out" >>"$LANE_LOG"
    fail "launcher birth: $(one_line "$(cat "$F/birth-peer.out")")"
  fi
fi

beat HELD.call-signed
if requires HELD.topology HELD.birth-picker HELD.name HELD.birth-launcher; then
  # The steer waits for the primary's turn to end. A held pane's waiter reads
  # the screen and delivers only after it saw that turn busy, so the primary
  # is a held turn showing Claude Code's busy footer until its gate goes.
  G4="$F/sign-gate"; touch "$G4"
  exec_in "$SID" "$P0" "pfm chat call --then 'HELD_SIGN_STEER' '$PEER_SID' 'H_HOLD $G4 HELD_SIGN_PROMPT'"; rc=$?
  primary_held() { pfm chat read "$PEER_SID" --json --tail 12 2>/dev/null | jq -e 'any(.entries[]; .role == "user" and (.text|contains("HELD_SIGN_PROMPT")))' >/dev/null; }
  primary=no
  [ "$rc" = 0 ] && wait_for 8 primary_held && primary=yes
  rm -f "$G4"
  signed() {
    pfm chat read "$PEER_SID" --json --tail 12 >"$F/signed.json" 2>/dev/null &&
      jq -e 'any(.entries[]; .role == "user" and (.text|contains("HELD_MAIN")) and (.text|contains("HELD_SIGN_PROMPT"))) and
        any(.entries[]; .role == "user" and (.text|contains("HELD_MAIN")) and (.text|contains("HELD_SIGN_STEER")))' "$F/signed.json" >/dev/null
  }
  if [ "$primary" = no ]; then
    cat "$F/exec-$SID.out" >>"$LANE_LOG" 2>&1
    fail "signed call rc=$rc: the held primary never reached HELD_PEER while its gate stood, so the steer's wait for a busy turn went unproved: $LANE_WAIT_WHY"
  elif wait_for 12 signed; then pass "a held chat's call reaches its held peer signed HELD_MAIN, prompt and steer"
  else
    cat "$F/signed.json" >>"$LANE_LOG" 2>&1
    fail "signed call rc=$rc: HELD_PEER lacks a HELD_MAIN-signed prompt and steer: $LANE_WAIT_WHY"
  fi
fi

# held_tree <pid> — every descendant of <pid>, one "pid args" line each.
held_tree() {
  local queue=("$1") pid kid
  while [ "${#queue[@]}" -gt 0 ]; do
    pid="${queue[0]}" queue=("${queue[@]:1}")
    printf '%s %s\n' "$pid" "$(argv_of "$pid")"
    for kid in $(cat /proc/"$pid"/task/*/children 2>/dev/null); do queue+=("$kid"); done
  done
}

beat HELD.reload-retired
LOST_PANE="" LOST_SEAT="" LOST_SID="" NEXT_SEAT="" NEXT_SID=""
if requires HELD.topology HELD.birth-launcher; then
  # A harness typed straight into the held shell takes that shell with it when
  # it ends (pfm.zsh _pfm_own_terminal), so the chat that dies here runs in a
  # zsh nested inside the held shell: the held shell outlives it, as it does
  # for an operator who started claude from a nested shell.
  note='retired by pfm scan: saved supervisor exited; resume recipe retained'
  retired_row() { [ "$(sqlite3 "$H_DB" "select state || '|' || herdr_pane || '|' || coalesce(lost_note,'') from seat where seat_id='$LOST_SEAT';")" = "lost|$LOST_PANE|$note" ]; }
  if ! held_birth_launcher LOST nested >"$F/birth-lost.out" 2>&1; then
    cat "$F/birth-lost.out" >>"$LANE_LOG"
    fail "retire precondition: no chat in a nested shell of a held pane: $(one_line "$(cat "$F/birth-lost.out")")"
  else
    shell="$(h_held_holder "$LOST_PANE" | jq -r '.child.pid')"
    nested="$(pgrep -P "$shell" -x zsh)"
    held_tree "$nested" >"$F/lost-tree"
    # The seat-host and everything it runs (the engine, its fake, its helpers)
    # die at once, as when the machine kills a chat.
    host="$(awk -v seat="$LOST_SEAT" '$0 ~ ("internal seat-host " seat "( |$)") {print $1; exit}' "$F/lost-tree")"
    victims="$( [ -z "$host" ] || held_tree "$host" | awk '{print $1}' | tr '\n' ' ')"
    printf 'HELD retire victims=[%s]\n' "$victims" >>"$LANE_LOG"
    cat "$F/lost-tree" >>"$LANE_LOG"
    # shellcheck disable=SC2086 # the victims are pids
    kill -KILL $victims >>"$LANE_LOG" 2>&1
    # The nested shell holds the killed harness's status for a keypress, then exits.
    back_home() { ! kill -0 "$nested" 2>/dev/null && h_shell_prompt "$LOST_PANE"; }
    if ! wait_for 8 "h pane read '$LOST_PANE' | grep -q 'press any key to close this terminal'" ||
      ! h_request pane.send_keys "$(jq -nc --arg p "$LOST_PANE" '{pane_id:$p,keys:["enter"]}')" >>"$LANE_LOG" ||
      ! wait_for 8 back_home; then
      h pane read "$LOST_PANE" >>"$LANE_LOG" 2>&1
      fail "the held shell in $LOST_PANE did not return to its prompt after the kill: $LANE_WAIT_WHY"
    else
      pfm ls --tsv >>"$LANE_LOG" 2>&1
      if ! retired_row; then
        _lane_log_only "HELD pfm ls did not retire $LOST_SEAT: $(seat_row "$LOST_SEAT"); trying pfm name-sync --apply"
        pfm name-sync --apply >>"$LANE_LOG" 2>&1
      fi
      relaunched() { read -r NEXT_SEAT NEXT_SID < <(live_seat_on "$LOST_PANE") && [ -n "$NEXT_SID" ] && h_held_engine "$LOST_PANE" >/dev/null; }
      if ! retired_row; then
        blocked held-no-writable-scan "$(sqlite3 "$H_DB" "select state || '|' || herdr_pane || '|' || coalesce(lost_note,'') from seat where seat_id='$LOST_SEAT';")"
      elif ! h_type "$LOST_PANE" claude || ! wait_for 12 relaunched; then
        h pane read "$LOST_PANE" >>"$LANE_LOG" 2>&1
        fail "no new live seat on $LOST_PANE beside the retired one: $LANE_WAIT_WHY"
      else
        before_pid="$(h_held_engine "$LOST_PANE")"
        exec_in "$NEXT_SID" "$LOST_PANE" 'pfm chat reload --model sonnet'; rc=$?
        reloaded_next() {
          RELOADED="$(h_held_engine "$LOST_PANE")" && [ "$RELOADED" != "$before_pid" ] && [[ "$(argv_of "$RELOADED")" == *"--model sonnet"* ]] &&
            [ "$(sqlite3 "$H_DB" "select state || '|' || reload_pending || '|' || herdr_pane from seat where seat_id='$NEXT_SEAT';")" = "live|0|$LOST_PANE" ]
        }
        if [ "$rc" = 0 ] && wait_for 30 reloaded_next && retired_row; then
          pass "reload in a held pane that also holds a retired seat relaunched the live chat (--model sonnet); the retired seat stays lost"
        else
          cat "$F/exec-$NEXT_SID.out" >>"$LANE_LOG" 2>&1
          fail "reload beside a retired seat rc=$rc: $(one_line "$(cat "$F/exec-$NEXT_SID.out" 2>&1)") $LANE_WAIT_WHY"
        fi
      fi
    fi
  fi
fi

beat HELD.kill
if requires HELD.topology HELD.reload-retired; then
  main_engine="$(h_held_engine "$P0")" main_start="$(start_of "$(h_held_engine "$P0")")"
  held_shell="$(h_held_holder "$LOST_PANE" | jq -r '.child.pid')" held_start="$(start_of "$held_shell")"
  # A reload types the exit command too: kill's own is the record it adds.
  exits_of() { if [ -f "$F/held-exits.log" ]; then grep -cxF "$1" "$F/held-exits.log" || :; else echo 0; fi; }
  exits_before="$(exits_of "$NEXT_SID")"
  out="$(timeout 30 pfm chat kill "$NEXT_SID" 2>&1)"; rc=$?
  # kill returns only once the held shell has ended: read once, never polled.
  shell_left="$(start_of "$held_shell")"
  printf 'HELD kill rc=%s held shell %s start %s -> [%s] %s\n' "$rc" "$held_shell" "$held_start" "$shell_left" "$out" >>"$LANE_LOG"
  killed="$(sqlite3 "$H_DB" "select count(*) from hidden where uuid='$NEXT_SID' and hidden_at > 0;")"
  # The engine behind herdr-shield's attach client ended by the exit command kill typed, never a forced close.
  exited=no
  [ "$(exits_of "$NEXT_SID")" = $((exits_before + 1)) ] && exited=yes
  pane_gone() {
    h_request pane.get "$(pane_json "$LOST_PANE")" >"$F/kill-pane.json" 2>&1
    jq -e '.error.code == "pane_not_found"' "$F/kill-pane.json" >/dev/null &&
      h_shield | jq -e --arg p "$LOST_PANE" '[.holders[] | select(.binding.pane == $p and .state == "running")] | length == 0' >/dev/null &&
      [ "$(sqlite3 "$H_DB" "select count(*) from seat where seat_id='$NEXT_SEAT';")" = 0 ]
  }
  if [ "$rc" = 0 ] && [[ "$out" == *"killed $NEXT_SID"* ]] && [ "$killed" = 1 ] && [ "$exited" = yes ] && [ -n "$main_start" ] &&
    [ -n "$held_start" ] && [ "$shell_left" != "$held_start" ] && wait_for 20 pane_gone &&
    [ "$(h_held_engine "$P0")" = "$main_engine" ] && [ "$(start_of "$main_engine")" = "$main_start" ]; then
    pass "kill typed the exit command into the held engine, closed $LOST_PANE and returned after its held shell $held_shell ended, the seat is gone and HELD_MAIN's engine $main_engine is untouched"
  else
    h_shield >>"$LANE_LOG" 2>&1
    fail "kill rc=$rc hidden=$killed exit command received=$exited (records of $NEXT_SID before kill: $exits_before) [$(cat "$F/held-exits.log" 2>&1 | tr '\n' ' ')] held shell $held_shell start $held_start at return [$shell_left] pane=$(one_line "$(cat "$F/kill-pane.json" 2>&1)") main $main_engine -> $(h_held_engine "$P0") $LANE_WAIT_WHY"
  fi
fi

beat HELD.exit
EXIT_PANE="" EXIT_SEAT="" EXIT_SID=""
if requires HELD.topology; then
  if ! held_birth_launcher EXIT >"$F/birth-exit.out" 2>&1; then
    cat "$F/birth-exit.out" >>"$LANE_LOG"
    fail "exit victim birth: $(one_line "$(cat "$F/birth-exit.out")")"
  else
    victim="$(h_held_engine "$EXIT_PANE")" victim_start="$(start_of "$(h_held_engine "$EXIT_PANE")")"
    main_engine="$(h_held_engine "$P0")" main_start="$(start_of "$(h_held_engine "$P0")")"
    out="$(timeout 30 pfm chat kill --exit "$EXIT_SID" 2>&1)"; rc=$?
    printf 'HELD exit rc=%s %s\n' "$rc" "$out" >>"$LANE_LOG"
    killed="$(sqlite3 "$H_DB" "select count(*) from hidden where uuid='$EXIT_SID' and hidden_at > 0;")"
    engine_gone() { [ "$(start_of "$victim")" != "$victim_start" ]; }
    if [ "$rc" = 0 ] && [[ "$out" == *"killed $EXIT_SID"* ]] && [ "$killed" = 1 ] && [ -n "$main_start" ] && wait_for 20 engine_gone &&
      [ "$(h_held_engine "$P0")" = "$main_engine" ] && [ "$(start_of "$main_engine")" = "$main_start" ]; then
      pass "kill --exit ended the held engine $victim; HELD_MAIN untouched"
    else fail "kill --exit rc=$rc hidden=$killed victim $victim still started $(start_of "$victim"), main [$main_engine] start [$main_start] -> $(h_held_engine "$P0") $LANE_WAIT_WHY"; fi
    printf 'HELD exit pane fate: %s\n' "$(h_request pane.get "$(pane_json "$EXIT_PANE")" 2>&1)" >>"$LANE_LOG"
  fi
fi

# The owned cleanup: a bounded stop, its ownership check and the survivor checks.
h_server_cleanup || _lane_log_only "HELD server cleanup failed (H SERVER-* line above)"
lane_end

#!/usr/bin/env bash
# Real pinned Herdr; credential-free plugin-lab fake engines, never providers.
set -uo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
. "$HERE/lib.sh"
lane_preamble
lane_begin H
CONFIG="${PFM_CONFIG:?PFM_CONFIG is required in the container}"
lane_seat_and_port "$CONFIG"
lane_require_seat_ops "$CONFIG"
export H_FIXTURE="/tmp/lane-H-fixture" H_FAKE_SCRIPT="/tmp/lane-H-fixture/fake.sh"
mkdir -p "$H_FIXTURE/bin"
if [ -n "${H_PFM_OVERRIDE:-}" ]; then
  ln -s "$H_PFM_OVERRIDE" "$H_FIXTURE/bin/pfm" || lane_abort "isolated product link failed"
  ln -sf "$H_PFM_OVERRIDE" "$HOME/.local/bin/pfm" || lane_abort "isolated managed product link failed"
fi
python3 "$HERE/herdr.py" prepare /worktree/infra/plugin-lab/tests/fakes/claude "$H_FIXTURE" || lane_abort "plugin fake adapter failed"
cc -Wall -Wextra -Werror -O2 "$HERE/fixtures/herdr-native.c" -o "$H_FIXTURE/bin/claude" || lane_abort "native fake build failed"
export PATH="$H_FIXTURE/bin:$PATH"
# Absolute binary config survives the pane login shell PATH. Keep root config intact.
jq --arg binary "$H_FIXTURE/bin/claude" '.claude.binary=$binary' "$CONFIG" >"$H_FIXTURE/pfm.config.json" || lane_abort "fake engine config failed"
# Existing lane TUI mock only for recipients; native sender fakes stay unchanged.
mkdir -p "$H_FIXTURE/mock-bin"
for engine in claude codex opencode; do
  ln -s /usr/local/libexec/pfm-lanes/mock-engine "$H_FIXTURE/mock-bin/$engine" || lane_abort "mock recipient link failed"
done
export H_MOCK_CONFIG="$H_FIXTURE/mock.config.json"
jq --arg cc "$H_FIXTURE/mock-bin/claude" --arg cx "$H_FIXTURE/mock-bin/codex" --arg ox "$H_FIXTURE/mock-bin/opencode" '.claude.binary=$cc | .codex.binary=$cx | .opencode.binary=$ox' "$CONFIG" >"$H_MOCK_CONFIG" || lane_abort "mock recipient config failed"
export PFM_CONFIG="$H_FIXTURE/pfm.config.json"
export HERDR_CONFIG_PATH="$H_FIXTURE/herdr.toml"
cat >"$HERDR_CONFIG_PATH" <<'EOF'
onboarding = false
[update]
version_check = false
manifest_check = false
[session]
resume_agents_on_restore = true
startup_per_agent_delay_ms = 100
[ui.toast]
delivery = "off"
[ui.sound]
enabled = false
EOF
# This named server exists only inside the canonical lane container.
. "$HERE/herdr.sh"
h_server_start || lane_abort "real Herdr substrate startup failed"
_lane_log_only "H native fake source $(sha256sum /worktree/infra/plugin-lab/tests/fakes/claude "$H_FAKE_SCRIPT" "$H_FIXTURE/bin/claude")"
workspace="$(herdr --session lane-H workspace create --label H --cwd /work/express --no-focus)" || lane_abort "workspace create failed"
printf '%s\n' "$workspace" | tee -a "$LANE_LOG"
export HERDR_WORKSPACE_ID="$(jq -er '.result.workspace.workspace_id' <<<"$workspace")"
export HERDR_PANE_ID="$(jq -er '.result.root_pane.pane_id' <<<"$workspace")"
h_tmux_servers() {
  ps -eo pid=,comm= | awk '$2 ~ /^tmux/ {print}' || { echo H-TMUX-PROCESS-PROBE-FAILED >&2; return 1; }
  find "${TMUX_TMPDIR:-/tmp}" -type s \( -name 'cc-*' -o -name 'cx-*' \) -print | sort || { echo H-TMUX-SOCKET-PROBE-FAILED >&2; return 1; }
}
h_tmux_servers >"$H_FIXTURE/tmux-before" || lane_abort "tmux baseline probe failed"
beat H.birth
out="$(pfm chat new --name H_MAIN --engine cc --account "$SEAT" --cwd /work/express --backend herdr 2>&1)"; rc=$?
printf '%s\n' "$out" | tee -a "$LANE_LOG"
if [ "$rc" -eq 0 ] && grep -q $'\therdr\t' <<<"$out"; then pass "real Herdr birth $out"; else fail "native birth rc=$rc $out"; fi
H_PANE="$(awk -F'\t' '$3 == "herdr" {print $5}' <<<"$out")"
H_DB="${PFM_STATE_DB:-$HOME/.local/state/pfm/pfm.db}"
H_SID="$(sqlite3 "$H_DB" "select session_id from seat where herdr_pane='$H_PANE';")"
H_SEAT="$(sqlite3 "$H_DB" "select seat_id from seat where herdr_pane='$H_PANE';")"
h_request() { python3 "$HERE/herdr.py" request "$HERDR_SOCKET_PATH" "$1" "$2"; }
h_agents() { herdr --session lane-H agent list; }
h_native_ready() {
  h_agents >"$H_FIXTURE/agents.json" && jq -e --arg p "$H_PANE" 'any(.result.agents[]; .pane_id == $p)' "$H_FIXTURE/agents.json" >/dev/null
}
beat H.seat-host
if ! requires H.birth; then :
elif wait_for 12 h_native_ready; then
  cat "$H_FIXTURE/agents.json" | tee -a "$LANE_LOG"
  h_request pane.process_info "{\"pane_id\":\"$H_PANE\"}" >"$H_FIXTURE/process.json"
  jq '{foreground: [.result.process_info.foreground_processes[] | {name,pid,argv0:.argv[0]}], group:.result.process_info.foreground_process_group_id}' "$H_FIXTURE/process.json" | tee -a "$LANE_LOG"
  h_tmux_servers >"$H_FIXTURE/tmux-after" || lane_abort "tmux birth probe failed"
  durable="$(sqlite3 "$H_DB" "select backend || '|' || herdr_socket || '|' || herdr_pane || '|' || session_id from seat where seat_id='$H_SEAT';")"
  printf 'H durable %s seat=%s\n' "$durable" "$H_SEAT" | tee -a "$LANE_LOG"
  if jq -e '.result.process_info as $p | any($p.foreground_processes[]; .name == "claude" and .argv[0] == "claude" and .pid == $p.foreground_process_group_id)' "$H_FIXTURE/process.json" >/dev/null &&
    [ -n "$H_SID" ] && [ "$durable" = "herdr|$HERDR_SOCKET_PATH|$H_PANE|$H_SID" ] &&
    cmp -s "$H_FIXTURE/tmux-before" "$H_FIXTURE/tmux-after"; then
    pass "canonical argv0=claude foreground PID=PGID; durable backend/socket/pane/SID exact; no new tmux server"
  else fail "native foreground/durable identity/tmux invariant"; fi
else
  h_agents | tee -a "$LANE_LOG"
  herdr --session lane-H pane read "$H_PANE" | tee -a "$LANE_LOG"
  fail "native fake not detected: $LANE_WAIT_WHY"
fi
h_open_live() {
  beat H.open-live
  if ! requires H.seat-host; then return; fi
  local root_tab params before_pid before_start after_start out rc phase
  root_tab="$(jq -er '.result.tab.tab_id' <<<"$workspace")"
  params="$(jq -nc --arg p "$H_PANE" '{pane_id:$p}')"
  if ! herdr --session lane-H tab focus "$root_tab" >>"$LANE_LOG" 2>&1 ||
    ! h_request pane.get "$(jq -nc --arg p "$HERDR_PANE_ID" '{pane_id:$p}')" >"$H_FIXTURE/open-root-before.json" ||
    ! jq -e '.result.pane.focused == true' "$H_FIXTURE/open-root-before.json" >/dev/null; then
    fail "live open precondition: another pane could not be independently focused"; return
  fi
  for phase in before after; do
    if ! h_request pane.get "$params" >"$H_FIXTURE/open-$phase.json" ||
      ! h_request pane.process_info "$params" >"$H_FIXTURE/open-process-$phase.json"; then
      fail "live open $phase producer observation failed"; return
    fi
    cat "$H_FIXTURE/open-$phase.json" "$H_FIXTURE/open-process-$phase.json" | tee -a "$LANE_LOG"
    if ! jq -er '.result.process_info as $p | $p.foreground_processes[] | select(.name == "claude" and .argv[0] == "claude" and .pid == $p.foreground_process_group_id) | [.pid,.argv] | @json' "$H_FIXTURE/open-process-$phase.json" >"$H_FIXTURE/open-engine-$phase"; then
      fail "live open $phase has no canonical engine process"; return
    fi
    if [ "$phase" = before ]; then
      if ! jq -e --arg p "$H_PANE" '.result.pane.pane_id == $p and .result.pane.focused == false' "$H_FIXTURE/open-before.json" >/dev/null; then fail "live open precondition: target already focused or wrong pane"; return; fi
      before_pid="$(jq -er '.result.process_info.foreground_process_group_id' "$H_FIXTURE/open-process-before.json")"
      before_start="$(awk '{print $22}' "/proc/$before_pid/stat")" || { fail "live open original process identity unreadable"; return; }
      out="$(timeout 30 pfm chat open "$H_SID" 2>&1)"; rc=$?
      printf 'H open rc=%s %s\n' "$rc" "$out" | tee -a "$LANE_LOG"
    fi
  done
  after_start="$(awk '{print $22}' "/proc/$before_pid/stat")" || { fail "live open original process disappeared"; return; }
  if [ "$rc" = 0 ] && jq -e --arg p "$H_PANE" '.result.pane.pane_id == $p and .result.pane.focused == true' "$H_FIXTURE/open-after.json" >/dev/null &&
    jq -e -s '.[0].result.pane.terminal_id == .[1].result.pane.terminal_id' "$H_FIXTURE/open-before.json" "$H_FIXTURE/open-after.json" >/dev/null &&
    cmp -s "$H_FIXTURE/open-engine-before" "$H_FIXTURE/open-engine-after" && [ -n "$before_start" ] && [ "$before_start" = "$after_start" ]; then
    pass "independent focus false->true on $H_PANE; same terminal and engine PID=$before_pid start=$before_start without relaunch"
  else fail "live open focus/survival assertion: rc=$rc target=$H_PANE PID=$before_pid start=$before_start->$after_start $out"; fi
}
h_reload() {
  beat H.reload
  if ! requires H.seat-host; then return; fi
  local before_pid worker rc pending=0 out params
  params="$(jq -nc --arg p "$H_PANE" '{pane_id:$p}')"
  h_request pane.get "$params" >"$H_FIXTURE/reload-before.json" || { fail "reload before pane probe"; return; }
  before_pid="$(jq -er '.result.process_info.foreground_process_group_id' "$H_FIXTURE/process.json")"
  # An unsent draft makes the real native worker exercise the repaired stash key.
  h_request pane.send_text "$(jq -nc --arg p "$H_PANE" '{pane_id:$p,text:"H_RELOAD_DRAFT"}')" >>"$LANE_LOG" || { fail "reload draft setup"; return; }
  timeout 40 pfm internal reload-run --sock "$HERDR_SOCKET_PATH" --pane "$H_PANE" --model sonnet >"$H_FIXTURE/reload-worker.out" 2>&1 &
  worker=$!
  while [ -d "/proc/$worker" ] && [ "$(awk '{print $3}' "/proc/$worker/stat" 2>/dev/null)" != Z ]; do
    if [ "$(sqlite3 "$H_DB" "select reload_pending from seat where seat_id='$H_SEAT';")" = 1 ]; then pending=1; fi
    read -r -t 0.05 _ < /dev/null || :
  done
  wait "$worker"; rc=$?
  cat "$H_FIXTURE/reload-worker.out" | tee -a "$LANE_LOG"
  h_request pane.get "$params" >"$H_FIXTURE/reload-after.json"
  h_request pane.process_info "$params" >"$H_FIXTURE/reload-process.json"
  jq '{foreground:[.result.process_info.foreground_processes[]|{name,pid,model:(.argv as $a|($a|index("--model")) as $i|if $i == null then null else $a[$i+1] end)}]}' "$H_FIXTURE/reload-process.json" | tee -a "$LANE_LOG"
  if [ "$rc" = 0 ] && [ "$pending" = 1 ] &&
    jq -e -s --arg p "$H_PANE" '.[0].result.pane.pane_id == $p and .[1].result.pane.pane_id == $p and .[0].result.pane.terminal_id == .[1].result.pane.terminal_id' "$H_FIXTURE/reload-before.json" "$H_FIXTURE/reload-after.json" >/dev/null &&
    jq -e --arg old "$before_pid" '.result.process_info as $p | any($p.foreground_processes[]; .name == "claude" and (.pid|tostring) != $old and .pid == $p.foreground_process_group_id and (.argv as $a | ($a|index("--model")) as $i | $i != null and $a[$i+1] == "sonnet"))' "$H_FIXTURE/reload-process.json" >/dev/null &&
    [ "$(sqlite3 "$H_DB" "select state || '|' || reload_pending || '|' || herdr_pane || '|' || session_id from seat where seat_id='$H_SEAT';")" = "live|0|$H_PANE|$H_SID" ]; then
    pass "same pane/terminal, new engine model=sonnet; pending observed 1->0 and same live SID"
  else herdr --session lane-H pane read "$H_PANE" | tee -a "$LANE_LOG"; fail "reload identity/model/pending assertion rc=$rc pending=$pending"; fi
  beat H.reload-failure
  expect-log '"hook":"reload-run","decision":"error","exit":1'
  out="$(timeout 8 pfm internal reload-run --sock "$H_FIXTURE/missing.sock" --pane "$H_PANE" --model sonnet 2>&1)"; rc=$?
  printf 'H reload failed target rc=%s %s\n' "$rc" "$out" | tee -a "$LANE_LOG"
  if [ "$rc" != 0 ] && [ "$rc" != 124 ] && grep -q 'pfm chat reload:' <<<"$out"; then pass "reload failure visible on CLI"; else fail "reload failure hidden rc=$rc $out"; fi
}
h_pane_absent() {
  local out rc
  out="$(h_request pane.get "$(jq -nc --arg p "$1" '{pane_id:$p}')" 2>&1)"; rc=$?
  [ "$rc" = 1 ] && jq -e '.error.code == "pane_not_found"' <<<"$out" >/dev/null
}
h_survivor() {
  h_request pane.process_info "$(jq -nc --arg p "$H_PANE" '{pane_id:$p}')" >"$H_FIXTURE/survivor.json" &&
    jq -e --argjson pid "$(jq -er '.result.process_info.foreground_process_group_id' "$H_FIXTURE/process.json")" '.result.process_info.foreground_process_group_id == $pid and any(.result.process_info.foreground_processes[]; .name == "claude" and .pid == $pid)' "$H_FIXTURE/survivor.json" >/dev/null
}
h_lifecycle() {
  local out rc victim_sid victim_pane victim_seat
  beat H.end
  out="$(pfm chat new --name H_END --engine cc --account "$SEAT" --cwd /work/express --backend herdr 2>&1)"; rc=$?
  printf 'H end victim birth rc=%s %s\n' "$rc" "$out" | tee -a "$LANE_LOG"
  victim_pane="$(awk -F'\t' '$3 == "herdr" {print $5}' <<<"$out")"
  victim_sid="$(sqlite3 "$H_DB" "select session_id from seat where herdr_pane='$victim_pane';")"
  victim_seat="$(sqlite3 "$H_DB" "select seat_id from seat where herdr_pane='$victim_pane';")"
  if [ "$rc" != 0 ] || [ -z "$victim_sid" ] || [ -z "$victim_seat" ]; then fail "end victim precondition"; return; fi
  pfm ls --tsv >>"$LANE_LOG" || { fail "end victim indexing"; return; }
  out="$(pfm chat end "$victim_sid" 2>&1)"; rc=$?
  printf 'H end rc=%s %s\n' "$rc" "$out" | tee -a "$LANE_LOG"
  if [ "$rc" = 0 ] && grep -q "ended $victim_sid" <<<"$out" && wait_for 5 h_pane_absent "$victim_pane" &&
    [ "$(sqlite3 "$H_DB" "select count(*) from seat where seat_id='$victim_seat';")" = 0 ] &&
    [ ! -e "$(dirname "$H_DB")/seats/$victim_seat.json" ] && h_survivor; then
    pass "end closed exact victim pane, removed durable seat, survivor engine unchanged"
  else fail "end close/durable/survivor assertion rc=$rc $LANE_WAIT_WHY"; fi
  local action killed
  for action in kill exit; do
    beat "H.$action"
    out="$(pfm chat new --name "H_${action^^}" --engine cc --account "$SEAT" --cwd /work/express --backend herdr 2>&1)"; rc=$?
    victim_pane="$(awk -F'\t' '$3 == "herdr" {print $5}' <<<"$out")"
    victim_sid="$(sqlite3 "$H_DB" "select session_id from seat where herdr_pane='$victim_pane';")"
    if [ "$rc" != 0 ] || [ -z "$victim_sid" ]; then fail "$action victim precondition rc=$rc $out"; continue; fi
    if [ "$action" = exit ]; then
      out="$(timeout 20 pfm chat kill --exit "$victim_sid" 2>&1)"; rc=$?
    else out="$(timeout 20 pfm chat kill "$victim_sid" 2>&1)"; rc=$?; fi
    printf 'H %s rc=%s %s\n' "$action" "$rc" "$out" | tee -a "$LANE_LOG"
    killed="$(sqlite3 "$H_DB" "select count(*) from hidden where uuid='$victim_sid' and hidden_at > 0;")"
    if [ "$rc" = 0 ] && grep -q "killed $victim_sid" <<<"$out" && [ "$killed" = 1 ] && wait_for 20 h_pane_absent "$victim_pane" && h_survivor; then
      pass "$action actual victim pane closed, durable kill recorded, survivor unchanged"
    else fail "$action pane/ledger/survivor assertion rc=$rc killed=$killed $LANE_WAIT_WHY"; fi
    beat "H.unkill-$action"
    out="$(pfm chat unkill "$victim_sid" 2>&1)"; rc=$?
    printf 'H unkill rc=%s %s\n' "$rc" "$out" | tee -a "$LANE_LOG"
    if [ "$rc" = 0 ] && grep -q "unkilled $victim_sid" <<<"$out" &&
      [ "$(sqlite3 "$H_DB" "select count(*) from hidden where uuid='$victim_sid';")" = 0 ] && h_survivor; then
      pass "unkill removed exact durable kill, survivor unchanged"
    else fail "unkill durable assertion rc=$rc $out"; fi
  done
}
h_restoration() {
  beat H.open-restored
  expect-log '"hook":"seat-host","decision":"error","exit":129'
  local old_pid observer rc out note="your last message was not answered — re-send it" params
  params="$(jq -nc --arg p "$H_PANE" '{pane_id:$p}')"
  h_request pane.process_info "$params" >"$H_FIXTURE/restore-before-process.json" || { fail "restore old process probe"; return; }
  old_pid="$(jq -er '.result.process_info.foreground_process_group_id' "$H_FIXTURE/restore-before-process.json")"
  h_request pane.send_text "$(jq -nc --arg p "$H_PANE" '{pane_id:$p,text:"H_LOSS_SECRET"}')" >>"$LANE_LOG" &&
    h_request pane.send_keys "$(jq -nc --arg p "$H_PANE" '{pane_id:$p,keys:["enter"]}')" >>"$LANE_LOG" || { fail "restoration interrupted turn setup"; return; }
  h_loss_indexed() {
    pfm chat read "$H_SID" --json --tail 10 >"$H_FIXTURE/loss-index.json" &&
      jq -e 'any(.entries[]; .role == "user" and .text == "H_LOSS_SECRET")' "$H_FIXTURE/loss-index.json" >/dev/null
  }
  if ! wait_for 10 h_loss_indexed; then fail "indexed interrupted producer turn absent $LANE_WAIT_WHY"; return; fi
  python3 - "$H_SID" "$H_FIXTURE/restore-observed.json" "$H_FIXTURE/observer-ready" <<'PYOBS' &
import json, pathlib, sys, time
sid, output, ready = sys.argv[1:]
pathlib.Path(ready).touch()
deadline=time.monotonic()+25
while time.monotonic()<deadline:
    for p in pathlib.Path('/proc').glob('[0-9]*/cmdline'):
        try:
            argv=p.read_bytes().rstrip(b'\0').decode().split('\0')
            for i in range(len(argv)-3):
                if argv[i:i+4] == ['chat','open','--restored',sid] and pathlib.Path(argv[i-1]).name == 'pfm':
                    pathlib.Path(output).write_text(json.dumps({'pid':p.parent.name,'argv':argv}))
                    sys.exit(0)
        except (FileNotFoundError, ProcessLookupError, PermissionError, UnicodeError):
            continue
    time.sleep(.002)
pathlib.Path(output).write_text(json.dumps({'error':'no real pfm chat open --restored process observed'}))
sys.exit(1)
PYOBS
  observer=$!
  if ! wait_for 3 'test -e "$H_FIXTURE/observer-ready"'; then wait "$observer"; fail "restored process observer startup"; return; fi
  herdr --session lane-H server stop >>"$LANE_LOG" 2>&1
  wait "$H_SERVER"; rc=$?
  printf 'H real server stopped rc=%s\n' "$rc" | tee -a "$LANE_LOG"
  herdr --session lane-H server >"$H_FIXTURE/server-restored.log" 2>&1 &
  H_SERVER=$!
  h_restored() {
    h_request pane.process_info "$params" >"$H_FIXTURE/restored-process.json" &&
      jq -e --arg old "$old_pid" --arg sid "$H_SID" '.result.process_info.foreground_processes | any(.name == "claude" and (.pid|tostring) != $old and (.argv as $a | ($a|index("--resume")) as $i | $i != null and $a[$i+1] == $sid))' "$H_FIXTURE/restored-process.json" >/dev/null &&
      [ "$(sqlite3 "$H_DB" "select state || '|' || name || '|' || session_id || '|' || herdr_pane || '|' || lost_note from seat where session_id='$H_SID' and herdr_pane='$H_PANE';")" = "live|H_MAIN|$H_SID|$H_PANE|$note" ]
  }
  if wait_for 20 h_restored; then
    wait "$observer"; rc=$?
    cat "$H_FIXTURE/restore-observed.json" | tee -a "$LANE_LOG"
    if [ "$rc" = 0 ] && h_request pane.get "$params" >"$H_FIXTURE/restored-pane.json" &&
      jq -e '.result.pane.label == "H_MAIN"' "$H_FIXTURE/restored-pane.json" >/dev/null; then
      pass "actual reporter command observed pfm chat open --restored; unattended new engine --resume SID; session/pane/name rebound live and indexed unanswered loss note"
    else fail "restored argv/name assertion rc=$rc"; fi
  else
    wait "$observer"; cat "$H_FIXTURE/restore-observed.json" "$H_FIXTURE/server-restored.log" | tee -a "$LANE_LOG"
    sqlite3 -header "$H_DB" 'select seat_id,session_id,name,state,lost_note,herdr_pane from seat;' | tee -a "$LANE_LOG"
    herdr --session lane-H pane read "$H_PANE" | tee -a "$LANE_LOG"
    fail "automatic restore new engine/binding/indexed loss assertion $LANE_WAIT_WHY"
  fi
  beat H.loss-notification
  out="$(herdr --session lane-H pane read "$H_PANE" 2>&1)"; rc=$?
  local notification
  notification="$(grep -F 'restart loss notification:' <<<"$out")"
  printf 'H notification diagnostic rc=%s %s\n' "$rc" "$notification" | tee -a "$LANE_LOG"
  if [ "$rc" = 0 ] && grep -qF 'loss note saved; Herdr notification not shown (disabled)' <<<"$notification" && ! grep -qF H_LOSS_SECRET <<<"$notification"; then
    pass "real notification disabled refusal remains visible without private prompt in diagnostic"
  else fail "notification disabled/refusal diagnostic assertion rc=$rc"; fi
  local verb
  for verb in status ls watch; do
    beat "H.loss-$verb"
    case "$verb" in
      status) out="$(timeout 8 pfm chat status "$H_SID" 2>&1)"; rc=$? ;;
      ls) out="$(timeout 8 pfm chat ls --all 2>&1)"; rc=$? ;;
      watch) out="$(timeout 8 pfm chat watch --once "$H_SID" 2>&1)"; rc=$? ;;
    esac
    printf 'H loss %s rc=%s %s\n' "$verb" "$rc" "$out" | tee -a "$LANE_LOG"
    if [ "$rc" = 0 ] && grep -qF "$note" <<<"$out"; then pass "nonempty indexed producer loss note shown by $verb";
    else fail "loss $verb truthful display assertion rc=$rc"; fi
  done
  beat H.restore-owner
  out="$(timeout 8 env HERDR_ENV=1 HERDR_SOCKET_PATH="$HERDR_SOCKET_PATH" HERDR_PANE_ID="$HERDR_PANE_ID" pfm chat open --restored "$H_SID" 2>&1)"; rc=$?
  printf 'H conflicting restored owner rc=%s %s\n' "$rc" "$out" | tee -a "$LANE_LOG"
  if [ "$rc" = 1 ] && grep -qF 'still owns a live engine' <<<"$out" &&
    [ "$(sqlite3 "$H_DB" "select state || '|' || herdr_pane || '|' || session_id from seat where session_id='$H_SID';")" = "live|$H_PANE|$H_SID" ]; then
    pass "another real pane cannot steal restored session from live owner"
  else fail "restored ownership refusal assertion rc=$rc"; fi
}
h_tab_ids() {
  h_request tab.list "$(jq -nc --arg w "$1" '{workspace_id:$w}')" | jq -er '[.result.tabs[].tab_id] | join(" ")'
}
h_tab_focused() {
  h_request tab.list "$(jq -nc --arg w "$1" '{workspace_id:$w}')" |
    jq -er '[.result.tabs[] | select(.focused)] | if length == 1 then .[0].tab_id else error("focused tabs: \(length)") end'
}
# One pane.report_agent under the product reporter's source; its seq is the
# product's UnixNano scale, so it outranks and is outranked like a real hook.
h_tp_report() {
  local body
  body="$(jq -nc --arg p "$H_PANE" --arg a "$H_TP_AGENT" --arg s "$H_SID" --arg st "$1" \
    '{pane_id:$p,source:"pfm",agent:$a,state:$st,agent_session_id:$s}')" || return 1
  h_request pane.report_agent "${body%\}},\"seq\":$(date +%s%N)}"
}
h_tp_blocked() {
  h_request agent.get "$(jq -nc --arg p "$H_PANE" '{target:$p}')" |
    jq -e --arg p "$H_PANE" '.result.agent.pane_id == $p and .result.agent.agent_status == "blocked"' >/dev/null
}
h_tp_restore() {
  local rc=0 now insert
  cmp -s "$H_FIXTURE/tab-priority.config.orig" "$PFM_CONFIG" || cp "$H_FIXTURE/tab-priority.config.orig" "$PFM_CONFIG" || rc=1
  [ -z "$H_TP_REPORTED" ] || h_tp_report idle >>"$LANE_LOG" || rc=1
  [ -z "$H_TP_SENTINEL" ] || h_request tab.close "$(jq -nc --arg t "$H_TP_SENTINEL" '{tab_id:$t}')" >>"$LANE_LOG" || rc=1
  if [ -n "$H_TP_ORDER" ]; then
    now="$(h_tab_ids "$H_TP_WS")" || rc=1
    insert="$(awk -v t="$H_TP_FLEET" -v want="$H_TP_ORDER" -v have="${now:-}" 'BEGIN{n=split(want,w," ");m=split(have,h," ");for(i=1;i<=n;i++)if(w[i]==t)a=i-1;for(i=1;i<=m;i++)if(h[i]==t)b=i-1;print a+(b<a?1:0)}')"
    [ "$now" = "$H_TP_ORDER" ] || h_request tab.move "$(jq -nc --arg t "$H_TP_FLEET" --argjson i "${insert:-0}" '{tab_id:$t,insert_index:$i}')" >>"$LANE_LOG" || rc=1
    [ "$(h_tab_ids "$H_TP_WS")" = "$H_TP_ORDER" ] || rc=1
  fi
  [ -z "$H_TP_FOCUS" ] || herdr --session lane-H tab focus "$H_TP_FOCUS" >>"$LANE_LOG" 2>&1 || rc=1
  return $rc
}
h_tab_priority() {
  beat H.tab-priority
  if ! requires H.seat-host; then return; fi
  local params count order rc off_rc on_rc
  H_TP_REPORTED="" H_TP_SENTINEL="" H_TP_ORDER="" H_TP_FOCUS="" H_TP_WS="" H_TP_FLEET="" H_TP_AGENT=""
  params="$(jq -nc --arg p "$H_PANE" '{pane_id:$p}')"
  h_request pane.get "$params" >"$H_FIXTURE/tp-pane-before.json" || { fail "tab priority fleet pane read"; return; }
  H_TP_FLEET="$(jq -er '.result.pane.tab_id' "$H_FIXTURE/tp-pane-before.json")" || { fail "tab priority fleet tab unknown"; return; }
  h_request tab.list '{}' >"$H_FIXTURE/tp-tabs-all.json" || { fail "tab priority tab list"; return; }
  H_TP_WS="$(jq -er --arg t "$H_TP_FLEET" '.result.tabs[] | select(.tab_id == $t) | .workspace_id' "$H_FIXTURE/tp-tabs-all.json")" || { fail "tab priority workspace unknown"; return; }
  H_TP_AGENT="$(h_request agent.get "$(jq -nc --arg p "$H_PANE" '{target:$p}')" | jq -er '.result.agent.agent')" || { fail "tab priority agent label unknown"; return; }
  H_TP_ORDER="$(h_tab_ids "$H_TP_WS")" || { H_TP_ORDER=""; fail "tab priority original order"; return; }
  H_TP_FOCUS="$(h_tab_focused "$H_TP_WS")" || { H_TP_FOCUS=""; fail "tab priority original focus"; return; }
  cp "$PFM_CONFIG" "$H_FIXTURE/tab-priority.config.orig" || { fail "tab priority config backup"; return; }
  # A sentinel shell tab the user stays on; the fleet tab goes to the far right.
  h_request tab.create "$(jq -nc --arg w "$H_TP_WS" '{workspace_id:$w,label:"H_TAB_SENTINEL",focus:false}')" >"$H_FIXTURE/tp-sentinel.json" &&
    H_TP_SENTINEL="$(jq -er '.result.tab.tab_id' "$H_FIXTURE/tp-sentinel.json")" || { H_TP_SENTINEL=""; h_tp_restore; fail "tab priority sentinel create"; return; }
  count="$(h_tab_ids "$H_TP_WS" | wc -w)"
  h_request tab.move "$(jq -nc --arg t "$H_TP_FLEET" --argjson i "$count" '{tab_id:$t,insert_index:$i}')" >>"$LANE_LOG" &&
    herdr --session lane-H tab focus "$H_TP_SENTINEL" >>"$LANE_LOG" 2>&1 || { h_tp_restore; fail "tab priority fixture arrangement"; return; }
  order="$(h_tab_ids "$H_TP_WS")" || { h_tp_restore; fail "tab priority arranged order"; return; }
  printf 'H tab-priority workspace=%s fleet=%s sentinel=%s order=%s\n' "$H_TP_WS" "$H_TP_FLEET" "$H_TP_SENTINEL" "$order" | tee -a "$LANE_LOG"
  if [ "${order##* }" != "$H_TP_FLEET" ] || [ "$(h_tab_focused "$H_TP_WS")" != "$H_TP_SENTINEL" ]; then
    h_tp_restore; fail "tab priority precondition: fleet tab not last or sentinel not focused ($order)"; return
  fi
  # The product reporter's own report path; the actual painter below reads it back.
  H_TP_REPORTED=1
  if ! h_tp_report blocked >>"$LANE_LOG" || ! wait_for 8 h_tp_blocked; then
    h_tp_restore; fail "tab priority blocked report not effective: $LANE_WAIT_WHY"; return
  fi
  # Default OFF: no herdrTabPriority key, tab status off; the bounded painter must
  # still run (a blocked fleet pane keeps it blinking until timeout's TERM, rc 124).
  jq '.chat.herdrTabStatus = "off" | del(.chat.herdrTabPriority)' "$H_FIXTURE/tab-priority.config.orig" >"$PFM_CONFIG" || { h_tp_restore; fail "tab priority off config"; return; }
  timeout -s TERM 3 pfm internal herdr-indicators >"$H_FIXTURE/tp-off.out" 2>"$H_FIXTURE/tp-off.err"; off_rc=$?
  cat "$H_FIXTURE/tp-off.out" "$H_FIXTURE/tp-off.err" | tee -a "$LANE_LOG"
  local off_order off_focus
  off_order="$(h_tab_ids "$H_TP_WS")"; off_focus="$(h_tab_focused "$H_TP_WS")"
  # Opt-in ON: same state, same painter; the transition memo is empty, so it reconciles.
  jq '.chat.herdrTabStatus = "off" | .chat.herdrTabPriority = true' "$H_FIXTURE/tab-priority.config.orig" >"$PFM_CONFIG" || { h_tp_restore; fail "tab priority on config"; return; }
  timeout -s TERM 4 pfm internal herdr-indicators >"$H_FIXTURE/tp-on.out" 2>"$H_FIXTURE/tp-on.err"; on_rc=$?
  cat "$H_FIXTURE/tp-on.out" "$H_FIXTURE/tp-on.err" | tee -a "$LANE_LOG"
  local on_order on_focus
  on_order="$(h_tab_ids "$H_TP_WS")"; on_focus="$(h_tab_focused "$H_TP_WS")"
  h_request pane.get "$params" >"$H_FIXTURE/tp-pane-after.json" || { h_tp_restore; fail "tab priority fleet pane after read"; return; }
  h_request tab.list "$(jq -nc --arg w "$H_TP_WS" '{workspace_id:$w}')" >"$H_FIXTURE/tp-tabs-after.json" || { h_tp_restore; fail "tab priority tabs after read"; return; }
  printf 'H tab-priority off_rc=%s off=%s focus=%s on_rc=%s on=%s focus=%s\n' "$off_rc" "$off_order" "$off_focus" "$on_rc" "$on_order" "$on_focus" | tee -a "$LANE_LOG"
  h_tp_restore; rc=$?
  if [ "$off_rc" = 124 ] && [ "$on_rc" = 124 ] && [ "$rc" = 0 ] &&
    [ "$off_order" = "$order" ] && [ "$off_focus" = "$H_TP_SENTINEL" ] &&
    [ "${on_order%% *}" = "$H_TP_FLEET" ] && [ "$on_focus" = "$H_TP_SENTINEL" ] &&
    [ "$(tr ' ' '\n' <<<"$on_order" | sort)" = "$(tr ' ' '\n' <<<"$order" | sort)" ] &&
    jq -e -s --arg t "$H_TP_FLEET" '.[0].result.pane.tab_id == $t and .[1].result.pane.tab_id == $t and .[0].result.pane.terminal_id == .[1].result.pane.terminal_id' "$H_FIXTURE/tp-pane-before.json" "$H_FIXTURE/tp-pane-after.json" >/dev/null &&
    jq -e --arg t "$H_TP_FLEET" '.result.tabs[] | select(.tab_id == $t) | .pane_count >= 1' "$H_FIXTURE/tp-tabs-after.json" >/dev/null &&
    ! grep -q 'herdr tab priority:' "$H_FIXTURE/tp-off.err" "$H_FIXTURE/tp-on.err"; then
    pass "chat.herdrTabPriority absent: zero tab movement; true: blocked fleet tab $H_TP_FLEET moved whole from last to first, user focus stayed on sentinel, config/tab order/focus restored; fixture agent reset to idle"
  else fail "tab priority assertion off_rc=$off_rc on_rc=$on_rc restore_rc=$rc order=$order off=$off_order on=$on_order focus=$off_focus/$on_focus"; fi
}
h_single=
case "${LANE_H_SCOPE:-}" in
  restoration) h_single=h_restoration ;;
  lifecycle) h_single=h_lifecycle ;;
  reload) h_single=h_reload ;;
  open-live) h_single=h_open_live ;;
  tab-priority) h_single=h_tab_priority ;;
esac
if [ -n "$h_single" ]; then
  "$h_single"
  lane_end
  exit $?
fi
if [ "${LANE_H_SCOPE:-all}" = all ]; then
beat H.native-call
out="$(pfm chat call --allow-unsigned H_MAIN H_PROMPT 2>&1)"; rc=$?
printf '%s\n' "$out" | tee -a "$LANE_LOG"
if [ "$rc" -eq 0 ] && wait_prompt H_MAIN H_PROMPT 5; then pass "native prompt reached carried transcript (unsigned steering is not credited)"; else
  cat /tmp/chat-then-herdr_sock.w1_p2.log 2>/dev/null | tee -a "$LANE_LOG"
  pfm chat read H_MAIN --json --tail 10 | tee -a "$LANE_LOG"
  fail "native call rc=$rc $out $LANE_WAIT_WHY"
fi
fi
beat H.whoami
if requires H.seat-host; then
h_request pane.send_text "$(jq -nc --arg p "$H_PANE" '{pane_id:$p,text:"H_EXEC pfm whoami --json"}')" >>"$LANE_LOG"
h_request pane.send_keys "$(jq -nc --arg p "$H_PANE" '{pane_id:$p,keys:["enter"]}')" >>"$LANE_LOG"
if wait_for 10 'test -f "$H_FIXTURE/exec-$H_SID.rc"'; then
  cat "$H_FIXTURE/exec-$H_SID.out" | tee -a "$LANE_LOG"
  if jq -e --arg seat "$H_SEAT" --arg pane "$H_PANE" --arg sock "$HERDR_SOCKET_PATH" --arg sid "$H_SID" '.id == $sid and .source == "herdr-ancestry" and .backend == "herdr" and .seat_id == $seat and .pane == $pane and .socket_path == $sock' "$H_FIXTURE/exec-$H_SID.out" >/dev/null && [ "$(cat "$H_FIXTURE/exec-$H_SID.rc")" = 0 ]; then pass "actual native fake descendant identifies carried SID"; else fail "whoami did not identify $H_SID"; fi
else fail "$LANE_WAIT_WHY"; fi
fi
h_type() {
  h_request pane.send_text "$(jq -nc --arg p "$1" --arg text "$2" '{pane_id:$p,text:$text}')" >>"$LANE_LOG" &&
    h_request pane.send_keys "$(jq -nc --arg p "$1" '{pane_id:$p,keys:["enter"]}')" >>"$LANE_LOG"
}
h_door_ready() {
  D_SID="$(sqlite3 "$H_DB" "select session_id from seat where herdr_pane='$D_PANE' and state='live';")" || return 1
  [ -n "$D_SID" ] && h_request pane.process_info "$(jq -nc --arg p "$D_PANE" '{pane_id:$p}')" >"$H_FIXTURE/door-process.json" &&
    jq -e 'any(.result.process_info.foreground_processes[]; .name == "claude")' "$H_FIXTURE/door-process.json" >/dev/null
}
h_birth_door() {
  local door="$1" cmd="$2" result rc identity
  beat "H.$door"
  result="$(herdr --session lane-H workspace create --label "H-$door" --cwd /work/express --no-focus)"; rc=$?
  printf 'H door %s workspace rc=%s %s\n' "$door" "$rc" "$result" | tee -a "$LANE_LOG"
  D_PANE="$(jq -er '.result.root_pane.pane_id' <<<"$result")" || { fail "$door shell pane missing"; return; }
  if ! h_type "$D_PANE" 'export PATH="$HOME/.local/bin:$PATH"; unset TMUX'; then fail "$door shell setup failed"; return; fi
  if [ "$door" = agent-start ]; then
    result="$(herdr --session lane-H agent start h-agent --kind claude --pane "$D_PANE" --timeout 12000 2>&1)"; rc=$?
    printf 'H agent-start rc=%s %s\n' "$rc" "$result" | tee -a "$LANE_LOG"
    [ "$rc" = 0 ] || { fail "actual Herdr agent-start refused rc=$rc $result"; return; }
  elif [ "$door" = picker ]; then
    if ! h_type "$D_PANE" 'pfm ls'; then fail "picker invocation failed"; return; fi
    if ! wait_for 8 'herdr --session lane-H pane read "$D_PANE" >"$H_FIXTURE/picker-screen" && grep -qiE "new.*chat|fleet" "$H_FIXTURE/picker-screen"'; then
      cat "$H_FIXTURE/picker-screen" | tee -a "$LANE_LOG"
      fail "actual picker did not render: $LANE_WAIT_WHY"; return
    fi
    cat "$H_FIXTURE/picker-screen" | tee -a "$LANE_LOG"
    h_request pane.send_text "$(jq -nc --arg p "$D_PANE" '{pane_id:$p,text:"new claude chat"}')" >>"$LANE_LOG" || { fail "picker filter input failed"; return; }
    h_request pane.send_keys "$(jq -nc --arg p "$D_PANE" '{pane_id:$p,keys:["enter","enter","enter","enter"]}')" >>"$LANE_LOG" || { fail "picker wizard keys failed"; return; }
  elif ! h_type "$D_PANE" "$cmd"; then fail "$door command input failed"; return; fi
  if ! wait_for 12 h_door_ready; then
    herdr --session lane-H pane read "$D_PANE" | tee -a "$LANE_LOG"
    fail "$door native birth absent: $LANE_WAIT_WHY"; return
  fi
  jq '{process:.result.process_info | {pane_id,foreground_process_group_id,foreground_processes:[.foreground_processes[] | {name,pid,argv0:.argv[0]}]}}' "$H_FIXTURE/door-process.json" | tee -a "$LANE_LOG"
  identity="$(sqlite3 "$H_DB" "select backend || '|' || herdr_socket || '|' || herdr_pane || '|' || session_id from seat where herdr_pane='$D_PANE';")"
  h_tmux_servers >"$H_FIXTURE/tmux-door" || { fail "$door tmux enumeration failed"; return; }
  if ! jq -e '.result.process_info as $p | any($p.foreground_processes[]; .name == "claude" and .argv[0] == "claude" and .pid == $p.foreground_process_group_id)' "$H_FIXTURE/door-process.json" >/dev/null ||
    [ "$identity" != "herdr|$HERDR_SOCKET_PATH|$D_PANE|$D_SID" ] || ! cmp -s "$H_FIXTURE/tmux-before" "$H_FIXTURE/tmux-door"; then
    fail "$door canonical foreground/durable/native-no-tmux assertion: $identity"; return
  fi
  if ! h_type "$D_PANE" 'H_EXEC pfm whoami --json' || ! wait_for 10 'test -f "$H_FIXTURE/exec-$D_SID.rc"'; then fail "$door descendant whoami absent"; return; fi
  cat "$H_FIXTURE/exec-$D_SID.out" | tee -a "$LANE_LOG"
  if [ "$(cat "$H_FIXTURE/exec-$D_SID.rc")" = 0 ] && jq -e --arg sid "$D_SID" --arg pane "$D_PANE" --arg sock "$HERDR_SOCKET_PATH" '.id == $sid and .pane == $pane and .socket_path == $sock and .backend == "herdr" and .source == "herdr-ancestry"' "$H_FIXTURE/exec-$D_SID.out" >/dev/null; then
    pass "$door actual native birth canonical argv0/PGID durable pane/SID real descendant identity and no tmux"
  else fail "$door descendant identity mismatch"; fi
}
if [ "${LANE_H_SCOPE:-all}" = birth ] || [ "${LANE_H_SCOPE:-all}" = all ]; then
  h_birth_door managed-launcher 'exec "$HOME/.local/bin/claude"'
  h_birth_door agent-start ''
  h_birth_door picker ''
  beat H.dead-backend
  out="$(HERDR_SOCKET_PATH="$H_FIXTURE/forced-dead.sock" pfm chat new --name H_DEAD --engine cc --account "$SEAT" --cwd /work/express --backend herdr 2>&1)"; rc=$?
  printf 'H forced-dead rc=%s %s\n' "$rc" "$out" | tee -a "$LANE_LOG"
  h_tmux_servers >"$H_FIXTURE/tmux-dead" || lane_abort "tmux refusal probe failed"
  if [ "$rc" -ne 0 ] && grep -qiE 'herdr|socket|connect' <<<"$out" && cmp -s "$H_FIXTURE/tmux-before" "$H_FIXTURE/tmux-dead"; then
    pass "forced-dead Herdr visibly nonzero with no tmux fallback"
  else fail "forced-dead backend rc=$rc or tmux fallback: $out"; fi
  if [ "${LANE_H_SCOPE:-all}" = birth ]; then lane_end; exit $?; fi
fi
h_durable_state() {
  local durable
  durable="$(sqlite3 "$H_DB" "select state || '|' || coalesce(lost_note,'') from seat where seat_id='$H_SEAT';")" || {
    echo H-DURABLE-PROBE-FAILED >&2
    return 1
  }
  printf 'H durable state %s\n' "$durable" | tee -a "$LANE_LOG"
}
h_watch() {
out="$(timeout 8 pfm chat watch --once --poll 1 H_MAIN 2>&1)"; rc=$?
printf '%s\n' "$out" | tee -a "$LANE_LOG"
if [ "$rc" -eq 0 ] && grep -q IDLE <<<"$out" && grep -q H_MAIN <<<"$out"; then pass "watch observed actual native idle"; else fail "watch rc=$rc $out"; fi
}
beat H.discovery
out="$(pfm ls --tsv 2>&1)"; rc=$?
if [ "$rc" -eq 0 ] && awk -F'\t' -v sid="$H_SID" -v sock="$HERDR_SOCKET_PATH" '$1 == "live-claude" && $2 == sid && $5 == "H_MAIN" && $11 == sock { found=1 } END{exit !found}' <<<"$out"; then pass "discovered carried SID=$H_SID on actual Herdr socket"; else fail "discovery rc=$rc $out"; fi
beat H.status
out="$(pfm chat status H_MAIN --json 2>&1)"; rc=$?
printf '%s\n' "$out" | tee -a "$LANE_LOG"
if [ "$rc" -eq 0 ] && jq -e --arg sid "$H_SID" '.session_id == $sid and .state == "idle"' <<<"$out" >/dev/null; then pass "status returns carried live session identity"; else fail "status rc=$rc $out"; fi
if [ "${LANE_H_SCOPE:-all}" = discovery ] || [ "${LANE_H_SCOPE:-all}" = all ]; then
  if [ "${LANE_H_SCOPE:-all}" = discovery ]; then
    beat H.watch
    h_watch
    beat H.reporter
    blocked "UNPLAYED-reporter-read" "snapshot/agent.get omit resume_argv; bounded trace established no supported keyed reader; reporter not credited"
  fi
  h_durable_state || lane_abort "durable state producer query failed"
  parked="$H_FIXTURE/parked-herdr.sock"
  mv "$HERDR_SOCKET_PATH" "$parked" || lane_abort "private endpoint fault setup failed"
  trap '[ ! -S "$parked" ] || mv "$parked" "$HERDR_SOCKET_PATH"; h_server_cleanup || exit 1' EXIT
  for surface in ls status watch; do
    beat "H.unreachable-$surface"
    case "$surface" in
      ls) out="$(pfm ls --tsv 2>&1)"; rc=$? ;;
      status) out="$(pfm chat status H_MAIN --json 2>&1)"; rc=$? ;;
      watch) out="$(timeout 8 pfm chat watch --once --poll 1 H_MAIN 2>&1)"; rc=$? ;;
    esac
    printf 'H unreachable %s rc=%s %s\n' "$surface" "$rc" "$out" | tee -a "$LANE_LOG"
    if [ "$rc" -ne 0 ] && [ "$rc" != 124 ] && grep -qiE 'unreachable|connect|socket|dial unix' <<<"$out"; then
      pass "$surface unreachable endpoint visibly nonzero, not absence/idle"
    else fail "$surface unreachable endpoint rc=$rc must visibly fail, never absence/idle"; fi
  done
  mv "$parked" "$HERDR_SOCKET_PATH" || lane_abort "private endpoint restoration failed"
  if [ "${LANE_H_SCOPE:-all}" = discovery ]; then lane_end; exit $?; fi
fi
if [[ "${LANE_H_SCOPE:-}" != delivery* ]]; then
beat H.keys
out="$(pfm chat keys --literal H_MAIN H_KEY_MARKER 2>&1)"; rc=$?
if [ "$rc" -eq 0 ] && pfm chat keys H_MAIN Enter >>"$LANE_LOG" 2>&1 && wait_prompt H_MAIN H_KEY_MARKER 5 && herdr --session lane-H pane read "$H_PANE" >"$H_FIXTURE/keys-screen" && grep -q H_KEY_MARKER "$H_FIXTURE/keys-screen"; then
  cat "$H_FIXTURE/keys-screen" | tee -a "$LANE_LOG"
  pass "literal keys plus Enter reached carried transcript and independent real pane"
else fail "keys rc=$rc $out $LANE_WAIT_WHY"; fi
if [ "${LANE_H_SCOPE:-}" = controls ] && [ "$LANE_FAILED" -gt 0 ]; then lane_end; exit $?; fi
beat H.capture
out="$(pfm chat capture H_MAIN 2>&1)"; rc=$?
if [ "$rc" -eq 0 ] && grep -q H_KEY_MARKER <<<"$out"; then pass "real pane capture includes typed marker H_KEY_MARKER"; else fail "capture rc=$rc $out"; fi
fi
if [ "${LANE_H_SCOPE:-all}" = controls ] || [ "${LANE_H_SCOPE:-all}" = all ]; then
  if [ "$LANE_FAILED" -gt 0 ]; then lane_end; exit $?; fi
  # The existing native fake runs this private terminal consumer; only pfm's
  # product modal door sends its Down/Enter. No driver keys select a choice.
  cat >"$H_FIXTURE/modal.py" <<'PY'
import os
import select
import termios
import time
import tty

fd = os.open('/dev/tty', os.O_RDWR)
original = termios.tcgetattr(fd)
choice = 1
pending = b''
try:
    tty.setraw(fd)
    os.write(fd, b'\r\nH_MODAL_READY 1=allow 2=deny\r\n')
    deadline = time.monotonic() + 8
    while time.monotonic() < deadline:
        if not select.select([fd], [], [], 0.1)[0]:
            continue
        pending += os.read(fd, 32)
        while pending:
            if pending.startswith(b'\x1b[') and len(pending) < 3:
                break
            if pending.startswith(b'\x1b[B'):
                choice = min(2, choice + 1)
                pending = pending[3:]
            elif pending.startswith(b'\x1b[A'):
                choice = max(1, choice - 1)
                pending = pending[3:]
            elif pending[:1] in (b'\r', b'\n'):
                os.write(fd, ('\r\nH_MODAL_CHOICE=%s\r\n' % choice).encode())
                with open(os.environ['H_FIXTURE'] + '/modal-choice', 'w') as output:
                    output.write(str(choice))
                raise SystemExit(0)
            elif pending[:1] == b'\x1b':
                os.write(fd, b'\r\nH_MODAL_CANCELLED\r\n')
                raise SystemExit(1)
            else:
                pending = pending[1:]
    raise TimeoutError('H modal consumer received no committed choice')
finally:
    termios.tcsetattr(fd, termios.TCSANOW, original)
    os.close(fd)
PY
  beat H.name
  out="$(pfm chat name H_MAIN H_RENAMED 2>&1)"; rc=$?
  printf 'H name rc=%s %s\n' "$rc" "$out" | tee -a "$LANE_LOG"
  if [ "$rc" = 0 ]; then pass "product name command succeeded"; else fail "name command rc=$rc $out"; fi
  beat H.name-durable
  durable_name="$(sqlite3 "$H_DB" "select name from seat where session_id='$H_SID';")"
  if [ "$durable_name" = H_RENAMED ]; then pass "durable name H_RENAMED"; else fail "durable name expected H_RENAMED got $durable_name"; fi
  beat H.name-pane
  if h_request pane.get "{\"pane_id\":\"$H_PANE\"}" >"$H_FIXTURE/control-pane.json" &&
    jq -e '.result.pane.label == "H_RENAMED"' "$H_FIXTURE/control-pane.json" >/dev/null; then
    cat "$H_FIXTURE/control-pane.json" | tee -a "$LANE_LOG"
    pass "real pane label H_RENAMED"
  else fail "real pane label expected H_RENAMED $(cat "$H_FIXTURE/control-pane.json")"; fi
  beat H.name-agent
  if h_agents >"$H_FIXTURE/control-agents.json" &&
    jq -e --arg p "$H_PANE" 'any(.result.agents[]; .pane_id == $p and .name == "h_renamed")' "$H_FIXTURE/control-agents.json" >/dev/null; then
    cat "$H_FIXTURE/control-agents.json" | tee -a "$LANE_LOG"
    pass "real agent label h_renamed on original pane"
  else fail "real agent label expected h_renamed $(cat "$H_FIXTURE/control-agents.json")"; fi
  beat H.modal
  if h_type "$H_PANE" 'H_EXEC python3 "$H_FIXTURE/modal.py"' &&
    wait_for 5 'herdr --session lane-H pane read "$H_PANE" >"$H_FIXTURE/modal-screen" && grep -q H_MODAL_READY "$H_FIXTURE/modal-screen"'; then
    out="$(pfm chat modal "$H_SID" deny 1 2>&1)"; rc=$?
    printf 'H modal rc=%s %s\n' "$rc" "$out" | tee -a "$LANE_LOG"
    if [ "$rc" = 0 ] && wait_for 9 'test -f "$H_FIXTURE/modal-choice"' &&
      [ "$(cat "$H_FIXTURE/modal-choice")" = 2 ] &&
      herdr --session lane-H pane read "$H_PANE" >"$H_FIXTURE/modal-screen" && grep -q H_MODAL_CHOICE=2 "$H_FIXTURE/modal-screen"; then
      cat "$H_FIXTURE/modal-screen" | tee -a "$LANE_LOG"
      pass "real modal Down selected second choice and Enter committed H_MODAL_CHOICE=2"
    else
      herdr --session lane-H pane read "$H_PANE" | tee -a "$LANE_LOG"
      fail "modal consumer expected second committed choice rc=$rc $out $LANE_WAIT_WHY"
    fi
  else fail "modal consumer readiness failed $LANE_WAIT_WHY"; fi
  if [ "${LANE_H_SCOPE:-all}" = controls ]; then lane_end; exit $?; fi
  pfm chat name "$H_SID" H_MAIN >>"$LANE_LOG" 2>&1 || lane_abort "restore carried name after controls"
fi
# Four actual sender/target directions. Calls execute beneath the real fixture
# engine process; CHAT_SENDER_* is never forged by the driver.
for birth in 'tmux H_T_SENDER' 'tmux H_T_TARGET' 'herdr H_H_TARGET'; do
  read -r backend name <<<"$birth"
  birth_config="$PFM_CONFIG"
  [ "$name" != H_T_TARGET ] || birth_config="$H_MOCK_CONFIG"
  PFM_CONFIG="$birth_config" pfm chat new --name "$name" --engine cc --account "$SEAT" --cwd /work/express --backend "$backend" >>"$LANE_LOG" 2>&1 || lane_abort "cannot create $name"
done
h_identity() {
  pfm ls --tsv >"$H_FIXTURE/fleet.tsv" && awk -F'\t' -v n="$1" '$1 == "live-claude" && $5 == n {print $2 "\t" $11; found=1; exit} END {exit !found}' "$H_FIXTURE/fleet.tsv"
}
h_exec() { # sender-name command
  local identity sid sock pane command="$2"
  identity="$(h_identity "$1")" || return 1
  IFS=$'\t' read -r sid sock <<<"$identity"
  rm -f "$H_FIXTURE/exec-$sid.rc" "$H_FIXTURE/exec-$sid.out"
  case "$sock" in *.sock)
    pane="$(sqlite3 "$H_DB" "select herdr_pane from seat where session_id='$sid';")"
    h_request pane.send_text "$(jq -nc --arg p "$pane" --arg t "H_EXEC $command" '{pane_id:$p,text:$t}')" >>"$LANE_LOG" || return 1
    h_request pane.send_keys "$(jq -nc --arg p "$pane" '{pane_id:$p,keys:["enter"]}')" >>"$LANE_LOG" || return 1 ;;
  *)
    case "$sock" in /*) ;; *) sock="$(_lane_tmux_dir)/$sock" ;; esac
    pane="$(tmux -S "$sock" display-message -p '#{pane_id}')" || return 1
    tmux -S "$sock" send-keys -t "$pane" -l -- "H_EXEC $command" && tmux -S "$sock" send-keys -t "$pane" Enter || return 1 ;;
  esac
  wait_for 15 "test -f '$H_FIXTURE/exec-$sid.rc'" || return 1
  printf 'H sender name=%s sid=%s socket=%s pane=%s command=%s rc=%s\n' "$1" "$sid" "$sock" "$pane" "$command" "$(cat "$H_FIXTURE/exec-$sid.rc")" | tee -a "$LANE_LOG"
  cat "$H_FIXTURE/exec-$sid.out" | tee -a "$LANE_LOG"
  [ "$(cat "$H_FIXTURE/exec-$sid.rc")" = 0 ]
}
h_delivery_prompt() {
  if [ "$1" = H_T_TARGET ]; then
    printf '%s %s\n' "$2" "$(mock_steps "{\"type\":\"turn\",\"reply\":\"${2}_DONE\",\"busy_ms\":3000}")"
  else printf '%s\n' "$2"; fi
}
h_outcome_matches() {
  local kind="$1" rc="$2" text="$3"
  case "$kind" in
    raw) [ "$rc" = 0 ] && [[ "$text" = *"Herdr raw composer confirmed"* ]] ;;
    busy) [ "$rc" = 0 ] && [[ "$text" = *queued* && "$text" != *delivered* ]] ;;
    blocked) [ "$rc" != 0 ] && [[ "$text" = *"not empty and ready"* && "$text" = *"menu preserved"* ]] ;;
    *) return 1 ;;
  esac
}
h_raw_outcomes() {
  local result rc raw_pane identity sid sock pane gate prompt capture
  result="$(PFM_CONFIG="$H_MOCK_CONFIG" pfm chat new --name H_RAW_CX --engine codex --account "$SEAT" --cwd /work/express --backend herdr 2>&1)"; rc=$?
  printf 'H codex birth rc=%s %s\n' "$rc" "$result" | tee -a "$LANE_LOG"
  [ "$rc" = 0 ] || { fail "Codex mock birth failed"; return; }
  raw_pane="$(awk -F'\t' '$3 == "herdr" {print $5}' <<<"$result")"
  # The producer is a TUI; readiness comes from the actual composer capture.
  if ! wait_for 12 'pfm chat capture H_RAW_CX >"$H_FIXTURE/raw-screen" && grep -q "›" "$H_FIXTURE/raw-screen"'; then
    cat "$H_FIXTURE/raw-screen" | tee -a "$LANE_LOG"; fail "Codex mock composer absent"; return
  fi
  cat "$H_FIXTURE/raw-screen" | tee -a "$LANE_LOG"
  if [ "${LANE_H_SCOPE:-}" != delivery-status ]; then
  beat H.codex-raw
  result="$(pfm chat call --allow-unsigned H_RAW_CX H_RAW_CX_PROMPT 2>&1)"; rc=$?
  printf 'H raw rc=%s %s\n' "$rc" "$result" | tee -a "$LANE_LOG"
  if h_outcome_matches raw "$rc" "$result" && wait_prompt H_RAW_CX H_RAW_CX_PROMPT 8; then
    pfm chat read H_RAW_CX --json --tail 6 | tee -a "$LANE_LOG"
    pass "Codex raw composer confirmed and recipient transcript received marker"
  else fail "Codex raw confirmation/receipt failed"; fi
  fi
  beat H.busy-queued
  identity="$(h_identity H_T_TARGET)" || { fail "busy target identity missing"; return; }
  IFS=$'\t' read -r sid sock <<<"$identity"
  case "$sock" in /*) ;; *) sock="$(_lane_tmux_dir)/$sock" ;; esac
  pane="$(tmux -S "$sock" display-message -p '#{pane_id}')" || { fail "busy target pane missing"; return; }
  gate="$H_FIXTURE/busy-gate"; touch "$gate" || { fail "hold gate failed"; return; }
  prompt="$(mock_steps "[{\"type\":\"hold\",\"until_gone\":\"$gate\"},{\"type\":\"turn\",\"reply\":\"H_BUSY_RELEASED\"}]")" || { fail "hold script invalid"; return; }
  tmux -S "$sock" send-keys -t "$pane" -l -- "$prompt" && tmux -S "$sock" send-keys -t "$pane" Enter || { fail "hold input failed"; return; }
  if ! wait_for 8 'pfm chat capture H_T_TARGET >"$H_FIXTURE/busy-screen" && grep -qiE "esc.*interrupt" "$H_FIXTURE/busy-screen"'; then
    cat "$H_FIXTURE/busy-screen" | tee -a "$LANE_LOG"; rm -f "$gate"; fail "held busy footer missing"; return
  fi
  cat "$H_FIXTURE/busy-screen" | tee -a "$LANE_LOG"
  result="$(pfm chat call --allow-unsigned H_T_TARGET H_BUSY_QUEUED 2>&1)"; rc=$?
  printf 'H held busy gate=%s rc=%s %s\n' "$(test -f "$gate" && echo held)" "$rc" "$result" | tee -a "$LANE_LOG"
  rm -f "$gate"
  if h_outcome_matches busy "$rc" "$result" && wait_prompt H_T_TARGET H_BUSY_QUEUED 8; then
    pfm chat read H_T_TARGET --json --tail 6 | tee -a "$LANE_LOG"
    pass "busy queued visibly, not delivered; recipient received after turn released"
  else fail "held busy call must report queued then arrive after release"; fi
  beat H.blocked
  # The same Codex mock can render an actual selection menu.
  h_type "$raw_pane" "$(mock_steps '{"type":"menu","options":["one","two"],"selected":1}')" || { fail "menu input failed"; return; }
  if ! wait_for 8 'pfm chat capture H_RAW_CX >"$H_FIXTURE/menu-screen" && grep -q "1. one" "$H_FIXTURE/menu-screen"'; then
    cat "$H_FIXTURE/menu-screen" | tee -a "$LANE_LOG"; fail "mock blocker absent"; return
  fi
  cat "$H_FIXTURE/menu-screen" | tee -a "$LANE_LOG"
  result="$(pfm chat call --allow-unsigned H_RAW_CX H_BLOCKED_PROMPT 2>&1)"; rc=$?
  printf 'H blocked rc=%s %s\n' "$rc" "$result" | tee -a "$LANE_LOG"
  if h_outcome_matches blocked "$rc" "$result"; then pass "Codex raw menu refusal visible, draft/menu preserved"; else fail "blocked raw recipient must refuse visibly"; fi
}
if [ "${LANE_H_SCOPE:-}" = delivery-outcomes ] || [ "${LANE_H_SCOPE:-}" = delivery-status ] || [ "${LANE_H_SCOPE:-all}" = all ]; then
  h_raw_outcomes
  if [ "${LANE_H_SCOPE:-all}" != all ]; then lane_end; exit $?; fi
fi
h_direction() {
  local sender="$1" receiver="$2" marker="$3" records prompt
  prompt="$(h_delivery_prompt "$receiver" "${marker}_PROMPT")" || { fail "mock turn input failed"; return; }
  if ! h_identity "$sender" >/dev/null || ! h_identity "$receiver" >/dev/null; then blocked "direction-prelude" "$sender or $receiver not live"; return; fi
  h_exec "$sender" "pfm whoami --json" || { fail "actual sender identity failed $sender"; return; }
  h_exec "$sender" "pfm chat call --then '${marker}_STEER' '$receiver' '$prompt'" &&
    wait_prompt "$receiver" "${marker}_PROMPT" 8 && wait_prompt "$receiver" "${marker}_STEER" 12 || {
      pfm chat read "$receiver" --json --tail 12 2>&1 | tee -a "$LANE_LOG"
      pfm chat capture "$receiver" 2>&1 | tee -a "$LANE_LOG"
      find /tmp -maxdepth 1 -name 'chat-then-*' -type f -exec tail -n 20 {} \; | tee -a "$LANE_LOG"
      fail "actual $sender to $receiver delivery/steer failed $LANE_WAIT_WHY"; return;
    }
  records="$(pfm chat read "$receiver" --json --tail 12)" || { fail "read recipient failed"; return; }
  printf '%s\n' "$records" | tee -a "$LANE_LOG"
  if jq -e --arg sender "$sender" --arg mark "${marker}_PROMPT" --arg steer "${marker}_STEER" 'any(.entries[]; .role == "user" and (.text|contains($sender)) and (.text|contains($mark))) and any(.entries[]; .role == "user" and (.text|contains($sender)) and (.text|contains($steer)))' <<<"$records" >/dev/null; then
    pass "signed $sender to $receiver prompt and same-target steer in recipient transcript"
  else fail "recipient prompt/steer lacks actual sender identity $sender"; fi
}
wait_for 10 'h_identity H_H_TARGET' || lane_abort "Herdr direction target not live"
cat "$H_FIXTURE/fleet.tsv" | tee -a "$LANE_LOG"
beat H.tmux-to-tmux
h_direction H_T_SENDER H_T_TARGET H_TT
if [ "${LANE_H_SCOPE:-}" != delivery-tmux ]; then
beat H.tmux-to-herdr
h_direction H_T_SENDER H_H_TARGET H_TH
fi
beat H.herdr-to-tmux
h_direction H_MAIN H_T_TARGET H_HT
if [ "${LANE_H_SCOPE:-}" != delivery-tmux ]; then
beat H.herdr-to-herdr
h_direction H_MAIN H_H_TARGET H_HH
fi
if [[ "${LANE_H_SCOPE:-}" = delivery* ]]; then
  lane_end
  exit $?
fi
beat H.watch
h_watch
h_open_live
h_lifecycle
h_reload
h_restoration
h_tab_priority
herdr --session lane-H server stop >>"$LANE_LOG" 2>&1
wait "$H_SERVER"
lane_end

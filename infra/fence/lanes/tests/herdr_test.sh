#!/usr/bin/env bash
# Real Herdr substrate only: no seat, engine, credentials or provider.
set -uo pipefail
SHTEST_TAG=lane-herdr-test
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"
LANES="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
BINARY="$(command -v herdr)" || { bad "Herdr binary present" "missing herdr"; shtest_end; exit 1; }
HOME_ROOT="$T/home"
mkdir -p "$HOME_ROOT" "$T/fixture"
cat >"$T/herdr.toml" <<'EOF'
onboarding = false
[update]
version_check = false
manifest_check = false
[ui.toast]
delivery = "off"
[ui.sound]
enabled = false
EOF
cat >"$T/driver.sh" <<'EOF'
#!/usr/bin/env bash
set -uo pipefail
HERE="$LANES_DIR"
. "$HERE/lib.sh"
lane_abort() { printf 'SUBSTRATE-START-FAILED %s\n' "$*" >&2; exit 1; }
EOF
# Execute the actual H server prelude, excluding every seat/engine action.
awk '/^# This named server/{copy=1} /^_lane_log_only/{copy=0} copy' "$LANES/H.sh" >>"$T/driver.sh"
cat >>"$T/driver.sh" <<'EOF'
printf 'ISOLATION HOME=%s XDG_CONFIG_HOME=%s XDG_DATA_HOME=%s XDG_STATE_HOME=%s XDG_CACHE_HOME=%s SOCKET=%s\n' "$HOME" "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$XDG_STATE_HOME" "$XDG_CACHE_HOME" "$HERDR_SOCKET_PATH"
herdr --version
sha256sum "$(command -v herdr)"
herdr --session lane-H api snapshot >"$H_FIXTURE/snapshot.json" || exit 1
cat "$H_FIXTURE/snapshot.json"
printf '%s\n' "$H_SERVER" >"$H_FIXTURE/pid"
# A fault after successful readiness must not depend on lane_end for cleanup.
exit 23
EOF
start=$SECONDS
env HOME="$HOME_ROOT" XDG_CONFIG_HOME="$HOME_ROOT/.config" XDG_DATA_HOME="$HOME_ROOT/.local/share" XDG_STATE_HOME="$HOME_ROOT/.local/state" XDG_CACHE_HOME="$HOME_ROOT/.cache" HERDR_CONFIG_PATH="$T/herdr.toml" H_FIXTURE="$T/fixture" LANES_DIR="$LANES" timeout 20 bash "$T/driver.sh" >"$T/driver.log" 2>&1
rc=$?
cat "$T/driver.log"
if [ "$rc" -eq 23 ] && jq -e '.result.snapshot.protocol == 22 and .result.snapshot.version == "0.9.5"' "$T/fixture/snapshot.json" >/dev/null; then
  ok "real pinned Herdr private socket returns protocol 22/version 0.9.5 before the intentional exit"
else bad "real pinned Herdr readiness" "rc=$rc wall=$((SECONDS-start))"; fi
pid="$(cat "$T/fixture/pid" 2>/dev/null)"
socket="$HOME_ROOT/.config/herdr/sessions/lane-H/herdr.sock"
if [ -n "$pid" ] && ! ps -p "$pid" -o pid= >/dev/null 2>&1 && [ ! -S "$socket" ]; then
  ok "independent teardown removes actual server and private socket after exit 23"
else
  bad "independent Herdr teardown" "survivor pid=$pid socket=$socket rc=$rc"
  # Dispose only this test's private session, including on the unfixed red tree.
  env HOME="$HOME_ROOT" HERDR_CONFIG_PATH="$T/herdr.toml" timeout 10 "$BINARY" --session lane-H server stop || { echo "test cleanup failed for $socket" >&2; exit 1; }
fi
if [ "$((SECONDS-start))" -lt 20 ]; then ok "readiness and teardown complete inside the 20-second outer bound"; else bad "bounded substrate lifecycle" "wall=$((SECONDS-start))"; fi
# Exercise the exact H witness under each independent producer failure.
awk '/^h_tmux_servers\(\)/ {copy=1} copy {print} copy && /^}/ {exit}' "$LANES/H.sh" >"$T/tmux-witness.sh"
cat >"$T/tmux-driver.sh" <<'EOF'
set -uo pipefail
source "$WITNESS"
ps() { [ "$FAULT" != ps ] || return 71; command ps "$@"; }
find() { [ "$FAULT" != find ] || return 72; command find "$@"; }
h_tmux_servers
EOF
mkdir -p "$T/sockets"
for fault in ps find healthy; do
  out="$(FAULT="$fault" WITNESS="$T/tmux-witness.sh" TMUX_TMPDIR="$T/sockets" bash "$T/tmux-driver.sh" 2>&1)"; rc=$?
  case "$fault" in
    ps) marker=H-TMUX-PROCESS-PROBE-FAILED ;;
    find) marker=H-TMUX-SOCKET-PROBE-FAILED ;;
    healthy) marker='' ;;
  esac
  if { [ "$fault" = healthy ] && [ "$rc" = 0 ]; } ||
    { [ "$fault" != healthy ] && [ "$rc" -ne 0 ] && grep -q "$marker" <<<"$out"; }; then
    ok "tmux witness $fault producer result is distinguished"
  else bad "tmux witness $fault producer failure is visible" "rc=$rc" "$out"; fi
done
# Exercise the exact durable diagnostic; a failed query must not print absence.
awk '/^h_durable_state\(\)/ {copy=1} copy {print} copy && /^}/ {exit}' "$LANES/H.sh" >"$T/durable-witness.sh"
if [ ! -s "$T/durable-witness.sh" ]; then
  grep -F "printf 'H durable state " "$LANES/H.sh" >"$T/durable-witness.sh"
else
  printf 'h_durable_state\n' >>"$T/durable-witness.sh"
fi
for fault in healthy sql; do
  out="$(FAULT="$fault" WITNESS="$T/durable-witness.sh" bash -c '
    H_DB=fixture H_SEAT=fixture LANE_LOG=/dev/null
    sqlite3() { if [ "$FAULT" = sql ]; then echo "sqlite fixture failure" >&2; return 71; fi; printf "live|\\n"; }
    source "$WITNESS"
  ' 2>&1)"; rc=$?
  if { [ "$fault" = healthy ] && [ "$rc" = 0 ] && grep -Fxq 'H durable state live|' <<<"$out"; } ||
    { [ "$fault" = sql ] && [ "$rc" -ne 0 ] && grep -q 'H-DURABLE-PROBE-FAILED' <<<"$out"; }; then
    ok "durable witness $fault producer result is distinguished"
  else bad "durable witness $fault producer failure is visible" "rc=$rc" "$out"; fi
done
# The actual matrix assertion requires BOTH signed user records on this target.
awk '/^h_direction\(\)/ {copy=1} copy {print} copy && /^}/ {exit}' "$LANES/H.sh" >"$T/direction.sh"
cat >"$T/direction-driver.sh" <<'EOF'
source "$WITNESS"
LANE_LOG=/dev/null LANE_WAIT_WHY=fixture
h_identity() { printf 'sid\tsocket\n'; }
h_exec() { return 0; }
h_delivery_prompt() { printf "%s\n" "$2"; }
wait_prompt() { return 0; }
blocked() { echo BLOCKED; }
pass() { echo ACCEPTED; }
fail() { echo REJECTED; }
pfm() {
  jq -nc --arg form "$FORM" '{entries:([{role:"user",text:"SENDER MARK_PROMPT"}] + (if $form == "missing-steer" then [] else [{role:"user",text:((if $form == "wrong-sender-steer" then "OTHER" else "SENDER" end) + " MARK_STEER")}] end))}'
}
h_direction SENDER TARGET MARK
EOF
for form in complete missing-steer wrong-sender-steer; do
  out="$(FORM="$form" WITNESS="$T/direction.sh" bash "$T/direction-driver.sh" 2>&1)"; rc=$?
  if { [ "$form" = complete ] && grep -q ACCEPTED <<<"$out"; } ||
    { [ "$form" != complete ] && grep -q REJECTED <<<"$out"; }; then
    ok "direction transcript $form is distinguished"
  else bad "direction transcript $form assertion" "rc=$rc" "$out"; fi
done
# Generated fake must accept an empty-composer C-s guard on a real PTY.
python3 "$LANES/herdr.py" prepare "$LANES/../../plugin-lab/tests/fakes/claude" "$T/pty-fixture"
PTY_FIXTURE="$T/pty-fixture" timeout 12 python3 - <<'PYTEST'
import os,pty,subprocess,time,json,pathlib,signal
root=pathlib.Path(os.environ['PTY_FIXTURE']); config=root/'config';config.mkdir()
(config/'settings.json').write_text(json.dumps({'statusLine':{'type':'command','command':'cat >/dev/null'}}))
master,slave=pty.openpty()
env=os.environ.copy();env.update(H_FIXTURE=str(root),CLAUDE_CONFIG_DIR=str(config),H_FAKE_SCRIPT=str(root/'fake.sh'))
p=subprocess.Popen(['bash',str(root/'fake.sh'),'--session-id','pty-probe'],stdin=slave,stdout=slave,stderr=slave,env=env,start_new_session=True)
os.close(slave)
try:
 deadline=time.monotonic()+3
 transcript=config/'projects/-fake-work/pty-probe.jsonl'
 while time.monotonic()<deadline and not transcript.exists():time.sleep(.05)
 # Wait for ready prompt before the product's empty-composer guard.
 import select
 data=b''
 while time.monotonic()<deadline and b'ready' not in data:
  if select.select([master],[],[],.1)[0]:data+=os.read(master,65536)
 os.write(master,b'\x13PTY_GUARD_PROMPT\n')
 deadline=time.monotonic()+3;received=False
 while time.monotonic()<deadline:
  if select.select([master],[],[],.1)[0]:
   data+=os.read(master,65536)
   if b'READY' in data:received=True;break
 assert received, 'empty composer guard stalled fake PTY (XOFF)'
 rows=[json.loads(line) for line in transcript.read_text().splitlines()]
 assert any(row.get('type')=='user' and row['message']['content']=='PTY_GUARD_PROMPT' for row in rows),rows
 print('PTY GUARD RECEIPT PASS')
finally:
 # Terminate only this Popen child; no shell PID lookup or external process.
 p.terminate();p.wait(timeout=3);os.close(master)
PYTEST
rc=$?
if [ "$rc" = 0 ]; then ok "fake PTY empty-composer guard receives exact prompt"; else bad "fake PTY empty-composer guard" "rc=$rc"; fi
# Drive the actual delivery-birth loop: only the tmux recipient uses the lane TUI mock.
awk '/^for birth / {copy=1} copy {print} copy && /^done$/ {exit}' "$LANES/H.sh" >"$T/delivery-birth.sh"
out="$(WITNESS="$T/delivery-birth.sh" bash -c '
  PFM_CONFIG=native-config H_MOCK_CONFIG=mock-config SEAT=1 LANE_LOG=/dev/null
  pfm() { local name; while [ "$#" -gt 0 ]; do if [ "$1" = --name ]; then name="$2"; break; fi; shift; done; printf "%s=%s\n" "$name" "$PFM_CONFIG" >&3; }
  lane_abort() { echo ABORT >&3; }
  source "$WITNESS"
' 3>&1)"; rc=$?
if [ "$rc" = 0 ] && [ "$out" = "$(printf 'H_T_SENDER=native-config\nH_T_TARGET=mock-config\nH_H_TARGET=native-config')" ]; then
  ok "delivery recipient uses mock config without changing real sender or native target"
else bad "delivery recipient config isolation" "rc=$rc" "$out"; fi
# The mock turn is input, not a raised waiter bound. Native targets stay verbatim.
awk '/^h_delivery_prompt\(\)/ {copy=1} copy {print} copy && /^}$/ {exit}' "$LANES/H.sh" >"$T/delivery-prompt.sh"
out="$(bash -c 'source "$1"; mock_steps() { printf "mock-engine: %s" "$1"; }; h_delivery_prompt H_T_TARGET H_TT_PROMPT; h_delivery_prompt H_H_TARGET H_TH_PROMPT' _ "$T/delivery-prompt.sh" 2>&1)"; rc=$?
if [ "$rc" = 0 ] && [ "$out" = "$(printf '%s\n' 'H_TT_PROMPT mock-engine: {"type":"turn","reply":"H_TT_PROMPT_DONE","busy_ms":3000}' H_TH_PROMPT)" ]; then
  ok "mock recipient turn outlasts ThenMin; native target input unchanged"
else bad "mock recipient turn input" "rc=$rc" "$out"; fi
# Row4 visible outcome predicates: positives and deceptive success/partial cases.
awk '/^h_outcome_matches\(\)/ {copy=1} copy {print} copy && /^}$/ {exit}' "$LANES/H.sh" >"$T/outcome.sh"
source "$T/outcome.sh"
for row in 'raw|0|called LIVE — Herdr raw composer confirmed|yes' 'raw|0|delivered-unproven|no' 'busy|0|queued PASTE — busy accepted|yes' 'busy|0|delivered — busy accepted|no' 'blocked|6|ABORT: native composer is not empty and ready; existing draft/menu preserved|yes' 'blocked|0|called LIVE|no'; do
  IFS='|' read -r kind code text want <<<"$row"
  got=no; h_outcome_matches "$kind" "$code" "$text" && got=yes
  if [ "$got" = "$want" ]; then ok "row4 outcome $kind $want: $text"; else bad "row4 outcome $kind expected $want" "$got"; fi
done
# A failed initial focus probe has not changed anything and must not restore
# from a backup that this beat has not created yet.
awk '/^h_tab_ids\(\)/ {copy=1} /^h_single=/ {copy=0} copy' "$LANES/H.sh" >"$T/priority-helpers.sh"
out="$(H_FIXTURE="$T/fixture" bash -c '
  source "$1"
  H_PANE=p1
  beat() { :; }; requires() { return 0; }; fail() { printf "FAIL %s\n" "$*"; }
  h_request() {
    case "$1" in
      pane.get) printf '\''{"result":{"pane":{"tab_id":"fleet"}}}\n'\'' ;;
      tab.list) printf '\''{"result":{"tabs":[{"tab_id":"fleet","workspace_id":"w1"}]}}\n'\'' ;;
      agent.get) printf '\''{"result":{"agent":{"agent":"claude","agent_status":"idle"}}}\n'\'' ;;
    esac
  }
  h_tab_ids() { printf "fleet\n"; }
  h_tab_focused() { return 17; }
  h_tp_restore() { printf "UNSAFE-RESTORE\n"; }
  h_tab_priority
' _ "$T/priority-helpers.sh" 2>&1)"; rc=$?
if [ "$out" = 'FAIL tab priority original focus' ]; then
  ok "priority focus-read failure stops before any restoration or config mutation"
else bad "priority focus-read failure must not restore nonexistent backup" "rc=$rc" "$out"; fi
shtest_end

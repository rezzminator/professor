#!/usr/bin/env bash
# Real Herdr substrate and pfm lifecycle with a Go fake; no provider credentials.
set -uo pipefail
SHTEST_TAG=lane-herdr-test
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"
LANES="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
if [ "${PFM_DEV_FENCE:-}" != 1 ] || [ "$(uname -s)" != Linux ]; then
  bad "FIXTURE-BOOTSTRAP-FAILED" "requires the Linux development fence"; shtest_end; exit 1
fi
for tool in herdr go jq sqlite3 python3 timeout ps find awk sort cmp grep tmux; do
  command -v "$tool" >/dev/null || { bad "TOOLCHAIN-MISSING" "$tool"; shtest_end; exit 1; }
done
BINARY="$(command -v herdr)"
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
# Compile before the live control's timeout; cold compilation uses the runner's bound.
REPO="$(cd -- "$LANES/../../.." && pwd -P)"
if ! go -C "$REPO/pfm" build -o "$T/pfm" ./cmd/pfm ||
   ! go -C "$REPO/pfm" build -o "$T/mock-engine" ./cmd/mock-engine; then
  bad "FIXTURE-BOOTSTRAP-FAILED" "compile pfm/cmd/pfm or pfm/cmd/mock-engine"; shtest_end; exit 1
fi
cat >"$T/lifecycle.sh" <<'LIFECYCLE'
#!/usr/bin/env bash
set -Eeuo pipefail
trap 'printf "LIFECYCLE FAIL command=%s line=%s\n" "$BASH_COMMAND" "$LINENO" >&2' ERR
ROOT="$FIXTURE_ROOT"
export HOME="$ROOT/home" PFM_HOME="$ROOT/home"
export PFM_CONFIG="$HOME/pfm.config.json" PFM_STATE_DB="$HOME/.local/state/pfm/pfm.db"
export PFM_SID_DIR="$HOME/sid" PFM_TMUX_DIR="$HOME/tmux" TMUX_TMPDIR="$HOME/tmux"
export PFM_PROC_ROOT=/proc PFM_TMUX_CONF=/dev/null
export XDG_CONFIG_HOME="$HOME/.config" XDG_DATA_HOME="$HOME/.local/share"
export XDG_STATE_HOME="$HOME/.local/state" XDG_CACHE_HOME="$HOME/.cache"
export HERDR_CONFIG_PATH="$ROOT/herdr.toml" MOCK_ENGINE_SCENARIO="$ROOT/scenario.json" MOCK_ENGINE_ENGINE=claude
export PFM_CLAUDE_ROOTS="$HOME/.claude/projects" PATH="$ROOT/bin:$HOME/.local/bin:$PATH"
export PFM_PRICES_OFFLINE=1 PFM_SKILL_SOURCES_OFFLINE=1 PFM_THEMES_OFFLINE=1
unset TMUX HERDR_ENV HERDR_PANE_ID HERDR_WORKSPACE_ID HERDR_SOCKET_PATH
HERE="$LANES_DIR"
. "$HERE/lib.sh"
. "$HERE/herdr.sh"
H_FIXTURE="$ROOT/fixture"
fail_control() { printf 'LIFECYCLE FAIL %s\n' "$*" >&2; exit 1; }
passed() { printf 'LIFECYCLE PASS %s\n' "$*"; }
external() { env -u HERDR_ENV -u HERDR_PANE_ID -u HERDR_WORKSPACE_ID "$PFM_BIN" "$@"; }
request() { python3 "$HERE/herdr.py" request "$HERDR_SOCKET_PATH" "$1" "$2"; }
tmux_witness() {
  fixture_processes tmux || return 1
  find "$TMUX_TMPDIR" -type s -print | sort
}
fixture_processes() {
  python3 - "$MOCK_ENGINE_SCENARIO" "${1:-}" <<'PY'
import os,pathlib,sys
needle=os.fsencode('MOCK_ENGINE_SCENARIO='+sys.argv[1])
for proc in pathlib.Path('/proc').glob('[0-9]*'):
    if int(proc.name) in (os.getpid(),os.getppid()):continue
    try:
        if needle not in (proc/'environ').read_bytes().split(b'\0'):continue
        if sys.argv[2] == 'tmux' and not (proc/'comm').read_text().startswith('tmux'):continue
        print(proc.name)
    except (FileNotFoundError,ProcessLookupError):pass
PY
}
fixture_gone() { fixture_processes >"$ROOT/survivors" 2>"$ROOT/process-probe.log" && [ ! -s "$ROOT/survivors" ]; }
cleanup() {
  local rc=$? p
  trap - EXIT
  if [ "$rc" != 0 ]; then
    printf 'LIFECYCLE DIAGNOSTICS root=%s child=%s\n' "$ROOT" "${child_pid:-unknown}" >&2
    for p in "$ROOT"/*.json "$ROOT"/*.txt "$ROOT"/fixture/*.log "$ROOT"/records/* "$HOME"/.local/state/pfm/log/*; do
      if [ -f "$p" ]; then printf 'ARTIFACT %s\n' "$p"; tail -n 30 "$p"; fi
    done
    if [ -f "$PFM_STATE_DB" ]; then
      sqlite3 -header "$PFM_STATE_DB" 'select session_id,name,backend,herdr_socket,herdr_pane,end_pending from seat;' || printf 'LIFECYCLE DIAGNOSTIC FAILED durable seats\n' >&2
    fi
    ps -eo pid=,ppid=,pgid=,comm=
  fi
  h_server_cleanup || { printf 'LIFECYCLE FAIL server cleanup\n' >&2; rc=1; }
  # Only sockets and processes under this fixture's private identity may be disposed.
  while IFS= read -r p; do
    tmux -S "$p" kill-server || { printf 'LIFECYCLE FAIL tmux cleanup %s\n' "$p" >&2; rc=1; }
  done < <(find "$TMUX_TMPDIR" -type s -print)
  if ! wait_for 3 fixture_gone; then
    printf 'LIFECYCLE FAIL fixture process survivors: '; cat "$ROOT/survivors"
    cat "$ROOT/process-probe.log" >&2
    while IFS= read -r p; do ps -p "$p" -o pid=,ppid=,pgid=,args=; done <"$ROOT/survivors"
    rc=1
    while IFS= read -r p; do kill -TERM "$p" 2>/dev/null || true; done <"$ROOT/survivors"
    if ! wait_for 3 fixture_gone; then
      while IFS= read -r p; do kill -KILL "$p" 2>/dev/null || true; done <"$ROOT/survivors"
      wait_for 3 fixture_gone || { printf 'LIFECYCLE FAIL survivors after KILL: '; cat "$ROOT/survivors"; }
    fi
  fi
  rm -rf -- "$ROOT" || rc=1
  [ ! -e "$ROOT" ] || { printf 'LIFECYCLE FAIL scratch survivor %s\n' "$ROOT" >&2; rc=1; }
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
mkdir -p "$ROOT/bin" "$ROOT/project" "$H_FIXTURE" "$HOME/.cc/1" "$HOME/.local/bin" \
  "$HOME/.local/state/pfm" "$HOME/.config/herdr" "$PFM_CLAUDE_ROOTS" "$PFM_SID_DIR" "$TMUX_TMPDIR"
ln -s "$PFM_CLAUDE_ROOTS" "$HOME/.cc/1/projects"
ln -s "$MOCK_BIN" "$ROOT/bin/claude"
ln -s "$PFM_BIN" "$ROOT/bin/pfm"
ln -s "$PFM_BIN" "$HOME/.local/bin/pfm"
cp "$HERDR_TEST_CONFIG" "$HERDR_CONFIG_PATH"
cat >"$PFM_CONFIG" <<JSON
{"version":2,"accounts":[{"id":1,"configDir":"~/.cc/1","emoji":"x"}],"claude":{"systemPrompt":"production","binary":"$ROOT/bin/claude","costLedger":false}}
JSON
cat >"$MOCK_ENGINE_SCENARIO" <<JSON
{"version":"2.1.270 (Claude Code)","session_id":"11111111-1111-4111-8111-111111111111","busy_ms":0,"jail":{},"record_dir":"$ROOT/records"}
JSON
# h_server_start owns traps, so restore our diagnostic/cleanup composition even on startup failure.
start_rc=0; h_server_start || start_rc=$?
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
[ "$start_rc" = 0 ] || fail_control 'FIXTURE-BOOTSTRAP-FAILED real server startup'
herdr --session lane-H api snapshot >"$ROOT/snapshot.json"
jq -e '.result.snapshot.protocol == 22 and .result.snapshot.version == "0.9.5"' "$ROOT/snapshot.json" >/dev/null || fail_control 'pinned server identity'
ws="$(herdr --session lane-H workspace create --label H --cwd "$ROOT/project" --no-focus)"
export HERDR_PANE_ID="$(jq -er '.result.root_pane.pane_id' <<<"$ws")" HERDR_WORKSPACE_ID="$(jq -er '.result.workspace.workspace_id' <<<"$ws")"
tmux_witness >"$ROOT/tmux-before"
out="$(h_run_in_pane "$HERDR_PANE_ID" "$PFM_BIN" chat new --name H_GO --engine cc --account 1 --cwd "$ROOT/project" --backend herdr)" || fail_control 'native birth'
code=0
probe="$(h_run_in_pane "$HERDR_PANE_ID" sh -c 'printf caller-status; exit 7')" || code=$?
[ "$code" = 7 ] && [ "$probe" = caller-status ] || fail_control "pane command status/output code=$code output=$probe"
passed 'actual caller pane preserves command output and nonzero status'
printf '%s\n' "$out"
child="$(awk -F'\t' '$3 == "herdr" {print $5}' <<<"$out")"
[ -n "$child" ] || fail_control 'birth returned no Herdr pane'
sid="$(sqlite3 "$PFM_STATE_DB" "select session_id from seat where name='H_GO';")"
[[ "$sid" =~ ^[0-9a-f-]{36}$ ]] || fail_control 'birth returned no unique durable session'
identity="$(sqlite3 "$PFM_STATE_DB" "select backend || '|' || herdr_socket || '|' || herdr_pane || '|' || session_id from seat where name='H_GO';")"
[ "$identity" = "herdr|$HERDR_SOCKET_PATH|$child|$sid" ] || fail_control "durable identity: $identity"
ready() {
  external chat capture "$sid" >"$ROOT/screen.txt" && grep -q '❯' "$ROOT/screen.txt" &&
    external chat status "$sid" --json >"$ROOT/status.json" &&
    jq -e --arg s "$sid" '.state == "idle" and .session_id == $s' "$ROOT/status.json" >/dev/null
}
wait_for 12 ready || fail_control "$LANE_WAIT_WHY"
request pane.process_info "{\"pane_id\":\"$child\"}" >"$ROOT/process.json"
jq -e '.result.process_info as $p | any($p.foreground_processes[]; .name == "claude" and .argv[0] == "claude" and .pid == $p.foreground_process_group_id)' "$ROOT/process.json" >/dev/null || fail_control 'canonical native foreground process'
child_pid="$(jq -er '.result.process_info.foreground_process_group_id' "$ROOT/process.json")"
[ -d "/proc/$child_pid" ] || fail_control 'native foreground process absent from real proc'
tmux_witness >"$ROOT/tmux-after"
cmp "$ROOT/tmux-before" "$ROOT/tmux-after" || fail_control 'native birth created tmux fallback'
passed "native identity $identity PID=$child_pid composer and idle"
external chat call --allow-unsigned "$sid" 'H_GO_PROBE mock-engine: {"type":"turn","reply":"H_GO_REPLY"}' || fail_control 'unsigned call'
receipt() {
  external chat read "$sid" --json --tail 10 >"$ROOT/transcript.json" &&
    jq -e 'any(.entries[]; .role == "user" and ((.text // "")|contains("H_GO_PROBE")))' "$ROOT/transcript.json" >/dev/null &&
    external chat capture "$sid" >"$ROOT/reply.txt" && grep -Fxq '⏺ H_GO_REPLY' "$ROOT/reply.txt"
}
wait_for 8 receipt || fail_control "$LANE_WAIT_WHY"
passed 'call user transcript and real pane reply'
external chat end "$sid" || fail_control 'schedule end'
ended() {
  local code=0
  request pane.get "{\"pane_id\":\"$child\"}" >"$ROOT/end.json" 2>&1 || code=$?
  [ "$code" = 1 ] && jq -e '.error.code == "pane_not_found"' "$ROOT/end.json" >/dev/null &&
    [ "$(sqlite3 "$PFM_STATE_DB" "select count(*) from seat where session_id='$sid';")" = 0 ] &&
    [ ! -d "/proc/$child_pid" ]
}
wait_for 10 ended || fail_control "$LANE_WAIT_WHY"
passed 'end removes exact pane, durable seat and native child'
code=0
HERDR_SOCKET_PATH="$ROOT/missing.sock" "$PFM_BIN" chat new --name H_REFUSED --engine cc --account 1 --cwd "$ROOT/project" --backend herdr >"$ROOT/refusal.txt" 2>&1 || code=$?
[ "$code" != 0 ] && grep -qiE 'socket|connect|herdr' "$ROOT/refusal.txt" || fail_control 'missing socket did not explicitly refuse birth'
[ "$(sqlite3 "$PFM_STATE_DB" "select count(*) from seat where name='H_REFUSED';")" = 0 ] || fail_control 'refused birth left a durable seat'
tmux_witness >"$ROOT/tmux-refusal"
cmp "$ROOT/tmux-before" "$ROOT/tmux-refusal" || fail_control 'refused birth created tmux fallback'
passed 'missing socket refuses birth without durable seat or tmux fallback'
h_server_cleanup || fail_control 'server/socket teardown'
passed 'server and socket gone'
LIFECYCLE
env FIXTURE_ROOT="$T/lifecycle" LANES_DIR="$LANES" HERDR_TEST_CONFIG="$T/herdr.toml" \
  PFM_BIN="$T/pfm" MOCK_BIN="$T/mock-engine" \
  timeout --signal=TERM --kill-after=20s 30s bash "$T/lifecycle.sh" >"$T/lifecycle.log" 2>&1
rc=$?
cat "$T/lifecycle.log"
if [ "$rc" = 0 ] && [ ! -e "$T/lifecycle" ]; then
  ok "real Herdr pfm lifecycle and failure refusal complete with private state removed"
else bad "real Herdr pfm lifecycle" "rc=$rc scratch=$T/lifecycle"; fi
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
  PFM_CONFIG=native-config H_MOCK_CONFIG=mock-config SEAT=1 LANE_LOG=/dev/null HERDR_PANE_ID=w1:p1
  pfm() { local name; while [ "$#" -gt 0 ]; do if [ "$1" = --name ]; then name="$2"; break; fi; shift; done; printf "%s=%s\n" "$name" "$PFM_CONFIG" >&3; }
  h_run_in_pane() {
    shift
    [ "$1" = env ] || return 1
    shift
    local assignment="$1"; shift
    PFM_CONFIG="${assignment#PFM_CONFIG=}" "$@"
  }
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

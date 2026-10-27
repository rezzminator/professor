#!/usr/bin/env bash
# Exercise M's registration and transport checks without a fleet or model.
set -uo pipefail
SHTEST_TAG=lane-m-test
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"
LANES="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${M_SUT:-$LANES/M.sh}"
source "$LANES/lib.sh"
if grep -q "expect-log 'has no live Codex seat'" "$SUT" &&
  grep -q "grep -q 'has no live Codex seat'" "$SUT" && ! grep -q 'has no live Codex tmux seat' "$SUT"; then
  ok 'M09 expects the backend no-live-Codex-seat wording in its result and log'
else bad 'M09 stale unknown-thread wording'; fi
# Run the real dependent blocks with the beat dependency/row writer after a failed M05.
if out="$(bash -s -- "$LANES/lib.sh" "$SUT" "$T/m07" 2>&1 <<'SH'
set -uo pipefail
export LANE_OUT_DIR="$3" LANE_PFM_LOG="$3/activity.jsonl"
mkdir -p "$LANE_OUT_DIR"
: >"$LANE_PFM_LOG"
source "$1"
lane_begin M
beat M.05-daemon-core
fail 'fixture daemon failure'
CHAT=fixture NEW_CHAT=fixture-new CWD="$LANE_OUT_DIR" PORT=18377 SEAT=1 SCRATCH="$LANE_OUT_DIR" MCP_WHY=fixture
unset n_status_chat INPROC_CFG TRANSCRIPT
mcp_tools() { echo tools >>"$LANE_OUT_DIR/transports"; MCP_WHY=fixture; return 1; }
mcp_call() { echo call >>"$LANE_OUT_DIR/transports"; return 1; }
mcp_stdio() {
  echo stdio >>"$LANE_OUT_DIR/transports"
  MCP_FRAMES='{"code":-32700}' MCP_OUT='{"result":{"tools":[]}}'
}
tmp_config() { return 1; }
_lane_tmux_dir() { printf '%s' "$LANE_OUT_DIR"; }
live_field() { printf fixture; }
live_chat() { return 1; }
pfm() { echo pfm >>"$LANE_OUT_DIR/transports"; return 1; }
tmux() { return 1; }
daemon_status() { printf '{"pid":42}'; }
block="$(sed -n '/^beat M\.07-stdio-transports/,/^# .* M\.08 / { /^# .* M\.08 /!p; }' "$2")"
[ -n "$block" ] || exit 2
eval "$block"
block="$(sed -n '/^beat M\.08-mcp-cli/,/^# .* M\.09 / { /^# .* M\.09 /!p; }' "$2")"
[ -n "$block" ] || exit 2
eval "$block"
block="$(sed -n '/^beat M\.09-chat-tools/,/^# .* M\.10 / { /^# .* M\.10 /!p; }' "$2")"
[ -n "$block" ] || exit 2
eval "$block"
block="$(sed -n '/^beat M\.10-chat-tools-gap/,/^# .* M\.13 / { /^# .* M\.13 /!p; }' "$2")"
[ -n "$block" ] || exit 2
eval "$block"
if lane_end; then exit 1; fi
[ "$LANE_FAILED" -eq 1 ] && [ "$LANE_BLOCKED" -eq 4 ] || exit 1
echo continued-after-M10
SH
)" && grep -q continued-after-M10 <<<"$out" && ! grep -q 'unbound variable' <<<"$out" &&
  [ ! -e "$T/m07/transports" ] &&
  awk -F '\t' '$2 == "M.07-stdio-transports" && $4 == "blocked" && $7 ~ /blocked-by M.05-daemon-core/ { found=1 } END { exit !found }' "$T/m07/M.timeline.tsv" &&
  awk -F '\t' '$2 == "M.08-mcp-cli" && $4 == "blocked" && $7 ~ /blocked-by M.05-daemon-core/ { found=1 } END { exit !found }' "$T/m07/M.timeline.tsv" &&
  awk -F '\t' '$2 == "M.09-chat-tools" && $4 == "blocked" && $7 ~ /blocked-by M.07-stdio-transports/ { found=1 } END { exit !found }' "$T/m07/M.timeline.tsv" &&
  awk -F '\t' '$2 == "M.10-chat-tools-gap" && $4 == "blocked" && $7 ~ /blocked-by M.09-chat-tools/ { found=1 } END { exit !found }' "$T/m07/M.timeline.tsv" &&
  awk -F '\t' '$1 == "M" && $3 == 5 && $4 == 1 && $6 == 4 { found=1 } END { exit !found }' "$T/m07/M.row.tsv"; then
  ok 'failed M05 blocks actual M07, M08, M09 and M10 by name without transport calls or unbound outputs, and the lane writes its row'
else bad 'M07/M08/M09/M10 failed-daemon dependency' "$out"; fi

mkdir -p "$T/nonrefusing/bin"
cat >"$T/nonrefusing/bin/pfm" <<'SH'
#!/usr/bin/env bash
sleep 30 &
child=$!
echo "$child" >>"$SERVE_FIXTURE_DIR/children"
trap 'kill "$child" 2>/dev/null; wait "$child" 2>/dev/null; exit 0' TERM
wait "$child"
SH
chmod +x "$T/nonrefusing/bin/pfm"
rc=0
out="$(SERVE_FIXTURE_DIR="$T/nonrefusing" PATH="$T/nonrefusing/bin:$PATH" \
  timeout -k 1 4 bash -s -- "$LANES/lib.sh" "$SUT" 2>&1 <<'SH'
set -uo pipefail
source "$1"
# Keep the production timeout shape, with short fixture durations.
timeout() {
  [ "$1" = -k ] && [ "$2" = 2 ] && [ "$3" = 10 ] || return 2
  echo bounded >>"$SERVE_FIXTURE_DIR/bounds"
  /usr/bin/timeout -k 0.2 0.2 "${@:4}"
}
tmp_config() { printf config; }
daemon_status() { printf '{"pid":42}'; }
bad='' pid0=42
probes="$(sed -n '/^  # M40 /,/^  # M44 / { /^  # M44 /!p; }' "$2")"
[ -n "$probes" ] || exit 2
eval "$probes"
probe="$(sed -n '/^[[:space:]]*running_pid=/,/^[[:space:]]*if \[ -n "\$bad" \]; then fail/ { /^[[:space:]]*if \[ -n "\$bad" \]; then fail/!p; }' "$2")"
[ -n "$probe" ] || exit 2
eval "$probe"
printf '%s\n' "$bad"
SH
)" || rc=$?
live_children=''
while read -r child; do
  if kill -0 "$child" 2>/dev/null; then
    live_children="$live_children $child"
    kill "$child" 2>/dev/null || true
  fi
done <"$T/nonrefusing/children"
if [ "$rc" -eq 0 ] && [ -z "$live_children" ] &&
  [ "$(wc -l <"$T/nonrefusing/bounds" 2>/dev/null)" -eq 4 ] &&
  grep -q 'M40:.*timed out' <<<"$out" && grep -q 'M55:.*timed out' <<<"$out"; then
  ok 'all four foreground daemon refusal probes bound a non-refusing server, name timeout failures and reap its child'
else bad 'daemon refusal bounds' "rc=$rc live_children=$live_children" "$out"; fi
for variant in canonical wrong-command wrong-args foreign-dropped restore-corrupt; do
  mkdir -p "$T/codex-$variant"
  out="$(timeout 8 bash -s -- "$LANES/lib.sh" "$SUT" "$T/codex-$variant" "$variant" <<'SH'
set -uo pipefail
source "$1"
SCRATCH="$3" CODEX_HOME="$3/codex" PFM_BIN=/fixture/pfm
mkdir -p "$CODEX_HOME"
canonical="$(printf '[mcp_servers.chat-professor]\ncommand = "%s"\nargs = ["mcp", "serve", "--stdio", "--family", "chat"]\n' "$PFM_BIN")"
printf '# BEGIN pfm mcp_servers — installer-owned\n%s\n# END pfm mcp_servers — installer-owned\n' "$canonical" >"$CODEX_HOME/config.toml"
case "$4" in
  wrong-command) sed -i 's#/fixture/pfm#/fixture/foreign#' "$CODEX_HOME/config.toml" ;;
  wrong-args) sed -i 's/"serve"/"different"/' "$CODEX_HOME/config.toml" ;;
esac
variant="$4"
beat() { :; }; spends() { :; }
pass() { printf 'PASS %s\n' "$*"; }; fail() { printf 'FAIL %s\n' "$*"; }
install_again() {
  case "$variant" in
    foreign-dropped) sed -i '/^\[mcp_servers.professor\]/,$d' "$TOML" ;;
  esac
  printf 'preserve foreign MCP client professor\n'
}
cp() {
  command cp "$@" || return
  if [ "$variant" = restore-corrupt ] && [ "$1" = "$SCRATCH/config.toml.orig" ]; then
    sed -i '/^command = /d' "$2"
  fi
}
block="$(sed -n '/^beat M\.02-register-codex$/,/^# .* M\.03 / { /^# .* M\.03 /!p; }' "$2")"
[ -n "$block" ] || exit 2
eval "$block"
SH
)"; rc=$?
  if [ "$variant" = canonical ]; then
    if [ "$rc" -eq 0 ] && [[ "$out" = PASS* ]]; then
      ok 'M02 accepts the exact chat command and args and preserves the foreign registration'
    else bad 'M02 canonical chat fence' "rc=$rc $out"; fi
  elif [ "$rc" -eq 0 ] && [[ "$out" = FAIL* ]]; then
    ok "M02 names $variant instead of accepting an incomplete or damaged registration"
  else bad "M02 $variant control" "rc=$rc $out"; fi
done

for beat_name in M.14-end-to-end M.15-live-chats-survive-daemon-restart; do
  for variant in overlap first-fails second-fails missing-receipt malformed-receipt; do
    fixture="$T/producers-$beat_name-$variant"
    mkdir -p "$fixture"
    out="$(timeout 8 bash -s -- "$LANES/lib.sh" "$SUT" "$fixture" "$beat_name" "$variant" <<'SH'
set -uo pipefail
source "$1"
SCRATCH="$3" CHAT=FIRST E1_CHAT=FIRST E2_CHAT=SECOND E1_WHY='' E2_WHY=''
SEAT=1
helpers="$(sed -n '/^E2E_WHY=""$/,/^beat M\.14-end-to-end$/ { /^beat M\.14-end-to-end$/!p; }' "$2")"
[ -n "$helpers" ] || exit 2
eval "$helpers"
variant="$5"
beat() { :; }; spends() { :; }; target_live() { :; }; requires() { return 0; }
live_chat() { return 0; }; live_field() { printf '%s-%s' "$1" "$2"; }
anchor_socket() { LANE_ANCHOR="sock:$1"; }
pass() { printf 'PASS %s\n' "$*"; }; fail() { printf 'FAIL %s\n' "$*"; }
e2e_drive() {
  local peer=FIRST i
  [ "$1" != FIRST ] || peer=SECOND
  printf '%s\n' "$BASHPID" >"$SCRATCH/$1.started"
  for ((i=0;i<40;i++)); do
    [ ! -f "$SCRATCH/$peer.started" ] || break
    sleep .01
  done
  if [ ! -f "$SCRATCH/$peer.started" ]; then E2E_WHY='independent producer never started'; return 1; fi
  case "$variant:$1" in
    first-fails:FIRST|second-fails:SECOND) E2E_WHY="fixture failure $1"; return 1 ;;
    missing-receipt:FIRST) exit 0 ;;
    malformed-receipt:FIRST) printf 'invalid\n' >"$E2E_RESULT"; exit 0 ;;
  esac
  printf 'completed\n' >"$SCRATCH/$1.completed"
}
e2e_evidence() {
  [ -f "$SCRATCH/FIRST.started" ] && [ -f "$SCRATCH/SECOND.started" ] || bad="$bad evidence before both producers started;"
  printf '%s\n' "$1" >>"$SCRATCH/evidence"
}
if [ "$4" = M.14-end-to-end ]; then
  block="$(sed -n '/^beat M\.14-end-to-end$/,/^# .* M\.15 / { /^# .* M\.15 /!p; }' "$2")"
else
  block="$(sed -n '/^beat M\.15-live-chats-survive-daemon-restart$/,/^lane_end$/ { /^lane_end$/!p; }' "$2")"
fi
[ -n "$block" ] || exit 2
eval "$block"
for chat in FIRST SECOND; do
  [ -f "$SCRATCH/$chat.started" ] || { echo 'FAIL producer absent'; exit 1; }
  pid="$(cat "$SCRATCH/$chat.started")"
  kill -0 "$pid" 2>/dev/null && { echo "FAIL worker $pid was not reaped"; exit 1; }
done
if [ "$variant" = overlap ]; then
  [ "$(wc -l <"$SCRATCH/evidence")" -eq 2 ] || exit 1
else
  [ -f "$SCRATCH/SECOND.completed" ] || [ "$variant" = second-fails ] || exit 1
fi
SH
)"; rc=$?
    if [ "$variant" = overlap ]; then
      if [ "$rc" -eq 0 ] && [[ "$out" = PASS* ]]; then
        ok "$beat_name overlaps independent producers, keeps both evidence checks and reaps both workers"
      else bad "$beat_name producer overlap barrier" "rc=$rc $out"; fi
    elif [ "$rc" -eq 0 ] && [[ "$out" = FAIL* ]] &&
      { [[ "$out" = *'fixture failure'* ]] || [[ "$out" = *'receipt'* ]]; }; then
      ok "$beat_name names $variant and still joins both workers"
    else bad "$beat_name $variant control" "rc=$rc $out"; fi
  done
done
shtest_end

#!/usr/bin/env bash
# Exercise M's search prelude and negative search beat without a fleet or model.
set -uo pipefail
SHTEST_TAG=lane-m-test
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"
LANES="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${M_SUT:-$LANES/M.sh}"
source "$LANES/lib.sh"
SCRATCH="$T/prelude"
HARVESTER_CFG="$T/harvester.config.json"
HARVEST_CACHE="$T/cache"
mkdir -p "$SCRATCH"
printf '{"ask":{"engine":"codex"}}\n' >"$HARVESTER_CFG"
lane_abort() { bad 'M prelude aborted' "$*"; return 1; }
need() { eval "$2" || { eval "$3" && eval "$2"; }; }
prelude="$(sed -n '/^# Keep the harvester/,/^need "the professor MCP daemon/ { /^need "the professor MCP daemon/!p; }' "$SUT")"
if [ -n "$prelude" ] && (
  eval "$prelude" || exit 1
  printf '%s\n' "${SEARCH_HEALTH_PID:-}" >"$T/search.pid"
); then
  SEARCH_HEALTH_PID="$(cat "$T/search.pid")"
  healthy_url="$(jq -r '.search.searxngURL' "$HARVESTER_CFG")"
  if curl -fsS -m 2 "$healthy_url/healthz" >"$T/health" 2>"$T/health.err"; then
    ok 'M prelude configures real healthy loopback search that survives its starting shell for O2'
  else bad 'M healthy search prelude' "$healthy_url" "$(cat "$T/health.err")"; fi
  if [ "$(jq -r '.ask.engine' "$HARVESTER_CFG")" = codex ] && [ -w "$HARVEST_CACHE" ]; then
    ok 'M search setup preserves unrelated settings and a writable cache'
  else bad 'M search setup config/cache' "$(cat "$HARVESTER_CFG")"; fi
else
  healthy_url=''
  bad 'M search prelude missing or failed' "$prelude"
fi

negative_calls=0
mcp_tools() { MCP_TOOLS=search_web; }
mcp_call() {
  local endpoint
  endpoint="$(jq -r '.search.searxngURL' "$HARVESTER_CFG")"
  MCP_RPCERR='' MCP_ISERR=true
  if [[ "$4" = *'"limit":21'* ]]; then
    MCP_TEXT='limit must be between 1 and 20'
    [ "$endpoint" = "$healthy_url" ] || bad="$bad limit validation retained the negative endpoint;"
  elif [ "$endpoint" != http://127.0.0.1:9 ]; then
    MCP_TEXT='unexpected healthy endpoint during negative search'
  else
    negative_calls=$((negative_calls + 1))
    MCP_TEXT='Web search failed: loopback backend unreachable'
  fi
}
one_line() { tr '\n' ' ' <<<"$1"; }
negative="$(sed -n '/^  # M23\/M24 search_web/,/^  # M25:/ { /^  # M25:/!p; }' "$SUT")"
bad=''
eval "$negative"
if [ -n "$negative" ] && [ "$negative_calls" -eq 1 ] && [ -z "$bad" ] &&
  [ "$(jq -r '.search.searxngURL' "$HARVESTER_CFG")" = "$healthy_url" ] &&
  curl -fsS -m 2 "$healthy_url/healthz" >/dev/null 2>&1; then
  ok 'M24 uses the unreachable backend only for its negative calls and restores healthy search for O2'
else
  bad 'M24 search failure scope/restore' "calls=$negative_calls errors=$bad config=$(cat "$HARVESTER_CFG")"
fi
mcp_call() { MCP_WHY='fixture transport failure'; return 1; }
bad=''
eval "$negative"
if [[ "$bad" = *'fixture transport failure'* ]] &&
  [ "$(jq -r '.search.searxngURL' "$HARVESTER_CFG")" = "$healthy_url" ] &&
  curl -fsS -m 2 "$healthy_url/healthz" >/dev/null 2>&1; then
  ok 'M24 transport failure remains named and restores the healthy search endpoint'
else bad 'M24 transport failure restore' "errors=$bad config=$(cat "$HARVESTER_CFG")"; fi
if grep -q "expect-log 'has no live Codex seat'" "$SUT" &&
  grep -q "grep -q 'has no live Codex seat'" "$SUT" && ! grep -q 'has no live Codex tmux seat' "$SUT"; then
  ok 'M09 expects the backend no-live-Codex-seat wording in its result and log'
else bad 'M09 stale unknown-thread wording'; fi
if [ -n "${SEARCH_HEALTH_PID:-}" ]; then
  kill "$SEARCH_HEALTH_PID" 2>/dev/null || true
  wait "$SEARCH_HEALTH_PID" 2>/dev/null || true
fi

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
block="$(sed -n '/^beat M\.10-chat-tools-gap/,/^# .* M\.11 / { /^# .* M\.11 /!p; }' "$2")"
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
# Keep this oracle independent of the installer and the lane's shared fixture.
codex_env='env_vars = ["HARVESTER_HOME_DIR", "HARVESTER_CONFIG", "HARVESTER_PYTHON_ROOT", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "HARVESTER_LOG_LEVEL", "HARVESTER_LOG_COMPONENTS", "HARVESTER_LOG_MIRROR", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"]'
export CODEX_ENV_FIXTURE="$codex_env"
for variant in canonical missing-name wrong-command wrong-args reinstall-drops-env foreign-dropped restore-corrupt; do
  mkdir -p "$T/codex-$variant"
  out="$(timeout 8 bash -s -- "$LANES/lib.sh" "$SUT" "$T/codex-$variant" "$variant" <<'SH'
set -uo pipefail
source "$1"
SCRATCH="$3" CODEX_HOME="$3/codex" PFM_BIN=/fixture/pfm HARVESTER_BIN=/fixture/harvester
mkdir -p "$CODEX_HOME"
canonical="$(printf '[mcp_servers.chat-professor]\ncommand = "%s"\nargs = ["mcp", "serve", "--stdio", "--family", "chat"]\n[mcp_servers.harvester-professor]\ncommand = "%s"\nargs = ["mcp"]\n%s\n' "$PFM_BIN" "$HARVESTER_BIN" "$CODEX_ENV_FIXTURE")"
printf '# BEGIN pfm mcp_servers — installer-owned\n%s\n# END pfm mcp_servers — installer-owned\n' "$canonical" >"$CODEX_HOME/config.toml"
case "$4" in
  missing-name) sed -i 's/, "no_proxy"//' "$CODEX_HOME/config.toml" ;;
  wrong-command) sed -i 's#/fixture/harvester#/fixture/foreign#' "$CODEX_HOME/config.toml" ;;
  wrong-args) sed -i 's/args = \["mcp"\]/args = ["serve"]/' "$CODEX_HOME/config.toml" ;;
esac
variant="$4"
beat() { :; }; spends() { :; }
pass() { printf 'PASS %s\n' "$*"; }; fail() { printf 'FAIL %s\n' "$*"; }
install_again() {
  case "$variant" in
    reinstall-drops-env) sed -i '/^env_vars = /d' "$TOML" ;;
    foreign-dropped) sed -i '/^\[mcp_servers.professor\]/,$d' "$TOML" ;;
  esac
  printf 'preserve foreign MCP client professor\n'
}
cp() {
  command cp "$@" || return
  if [ "$variant" = restore-corrupt ] && [ "$1" = "$SCRATCH/config.toml.orig" ]; then
    sed -i '/^env_vars = /d' "$2"
  fi
}
block="$(sed -n '/^beat M\.02-register-codex$/,/^# .* M\.03 / { /^# .* M\.03 /!p; }' "$2")"
[ -n "$block" ] || exit 2
eval "$block"
SH
)"; rc=$?
  if [ "$variant" = canonical ]; then
    if [ "$rc" -eq 0 ] && [[ "$out" = PASS* ]]; then
      ok 'M02 accepts exact command, args and sixteen inherited names and preserves the foreign registration'
    else bad 'M02 canonical environment fence' "rc=$rc $out"; fi
  elif [ "$rc" -eq 0 ] && [[ "$out" = FAIL* ]]; then
    ok "M02 names $variant instead of accepting an incomplete or damaged registration"
  else bad "M02 $variant control" "rc=$rc $out"; fi
done

for variant in canonical missing-env; do
  mkdir -p "$T/e2-codex-$variant"
  out="$(bash -s -- "$LANES/lib.sh" "$LANES/E2.sh" "$T/e2-codex-$variant" "$variant" <<'SH'
set -uo pipefail
source "$1"
toml="$3/config.toml" bin=/fixture/pfm HOME=/fixture
printf '# BEGIN pfm mcp_servers — installer-owned\n[mcp_servers.chat-professor]\ncommand = "%s"\nargs = ["mcp", "serve", "--stdio", "--family", "chat"]\n[mcp_servers.harvester-professor]\ncommand = "%s"\nargs = ["mcp"]\n%s\n# END pfm mcp_servers — installer-owned\n' "$bin" "$HOME/.local/bin/harvester" "$CODEX_ENV_FIXTURE" >"$toml"
[ "$4" != missing-env ] || sed -i '/^env_vars = /d' "$toml"
bad=''
block="$(sed -n '/^  # M34:/,/^  # The served surface/ { /^  # The served surface/!p; }' "$2")"
[ -n "$block" ] || exit 2
eval "$block"
if [ -z "$bad" ]; then echo PASS; else printf 'FAIL %s\n' "$bad"; fi
SH
)"; rc=$?
  if [ "$rc" -eq 0 ] && { { [ "$variant" = canonical ] && [[ "$out" = PASS* ]]; } ||
    { [ "$variant" = missing-env ] && [[ "$out" = FAIL* ]]; }; }; then
    ok "E2 exact harvester environment fixture: $variant"
  else bad "E2 environment $variant" "rc=$rc $out"; fi
done

for beat_name in M.14-end-to-end M.15-live-chats-survive-daemon-restart; do
  for variant in overlap first-fails second-fails missing-receipt malformed-receipt; do
    fixture="$T/producers-$beat_name-$variant"
    mkdir -p "$fixture"
    out="$(timeout 8 bash -s -- "$LANES/lib.sh" "$SUT" "$fixture" "$beat_name" "$variant" <<'SH'
set -uo pipefail
source "$1"
SCRATCH="$3" CHAT=FIRST E1_CHAT=FIRST E2_CHAT=SECOND E1_WHY='' E2_WHY=''
RFC_CLAUDE=first.md RFC_CODEX=second.md RFC_E1=first.md RFC_E2=second.md SEAT=1
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

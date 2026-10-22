#!/usr/bin/env bash
# Exercise the dropped-seat beat's overlapping read-only work and restore boundary.
set -uo pipefail
SHTEST_TAG=lane-o2-test
source "$(dirname -- "${BASH_SOURCE[0]}")/../../../../scripts/shtest.sh"
LANES="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
SUT="${O2_SUT:-$LANES/O2.sh}"

for variant in overlap doctor-warning doctor-fails runtime-fails missing-receipt malformed-receipt; do
  fixture="$T/$variant"
  mkdir -p "$fixture"
  out="$(timeout 8 bash -s -- "$LANES/lib.sh" "$SUT" "$fixture" "$variant" <<'SH'
set -uo pipefail
source "$1"
fixture_root="$3" CONFIG="$3/config.json" SPARE=2 SEAT=1 LANE_SEATS='cc:1 cc:2'
SPARE_CHAT=O2_SPARE E1_MAIN=E1_MAIN CWD=/fixture variant="$4"
printf '{"accounts":[{"id":1},{"id":2}]}\n' >"$CONFIG"
LANE_OUT_DIR="$fixture_root/out" LANE_PFM_LOG="$fixture_root/pfm.jsonl"
touch "$LANE_PFM_LOG"
lane_begin O2 >/dev/null
unset SCRATCH
beat() { :; }; spends() { :; }; target() { :; }
pass() { printf 'PASS %s\n' "$*"; }; fail() { printf 'FAIL %s\n' "$*"; }
live_chat() { [ -e "$fixture_root/live" ]; }
live_field() {
  if [ "$2" = 9 ]; then
    jq -r 'if any(.accounts[]; .id == 2) then 2 else 0 end' "$CONFIG"
  else printf 'fixture-%s-%s' "$1" "$2"; fi
}
install_again() {
  if jq -e 'any(.accounts[]; .id == 2)' "$CONFIG" >/dev/null; then
    if [ -f "$fixture_root/doctor.pid" ] && kill -0 "$(cat "$fixture_root/doctor.pid")" 2>/dev/null; then
      echo restored-before-doctor-joined >"$fixture_root/early-restore"
      return 2
    fi
    echo restore >>"$fixture_root/installs"
  else echo drop >>"$fixture_root/installs"; fi
}
wait_prompt() { touch "$fixture_root/runtime.finished"; return 0; }
pfm() {
  case "${1:-} ${2:-}" in
    'chat new') touch "$fixture_root/live"; echo ready ;;
    'chat status') echo idle ;;
    'chat call')
      local i
      touch "$fixture_root/runtime.started"
      for ((i=0;i<40;i++)); do
        [ ! -f "$fixture_root/doctor.started" ] || break
        sleep .01
      done
      if [ ! -f "$fixture_root/doctor.started" ]; then
        echo independent-doctor-never-started >&2
        touch "$fixture_root/no-overlap"
        return 2
      fi
      [ "$variant" != runtime-fails ]
      ;;
    'chat end') touch "$fixture_root/ended" ;;
    'doctor ')
      if jq -e 'any(.accounts[]; .id == 2)' "$CONFIG" >/dev/null; then
        echo 'doctor: config accounts=1:fixture,2:fixture'
        return 0
      fi
      local i
      printf '%s\n' "$BASHPID" >"$fixture_root/doctor.pid"
      touch "$fixture_root/doctor.started"
      for ((i=0;i<80;i++)); do
        [ ! -f "$fixture_root/runtime.finished" ] || break
        sleep .01
      done
      [ -f "$fixture_root/runtime.finished" ] || { echo runtime-never-finished >&2; return 2; }
      # Keep the worker alive after the parent completes its live checks.
      sleep .05
      if jq -e 'any(.accounts[]; .id == 2)' "$CONFIG" >/dev/null; then
        touch "$fixture_root/early-restore"
        return 2
      fi
      echo 'doctor: config accounts=1:fixture'
      case "$variant" in
        doctor-warning) return 1 ;;
        doctor-fails) return 2 ;;
        missing-receipt) exit 0 ;;
        malformed-receipt) printf 'invalid\n' >"$drop_doc_receipt"; exit 0 ;;
      esac
      ;;
    *) printf 'unexpected pfm call: %s\n' "$*" >&2; return 2 ;;
  esac
}
block="$(sed -n '/^beat O2\.08-dropped-seat-with-live-chat$/,/^# .* O2\.09 / { /^# .* O2\.09 /!p; }' "$2")"
[ -n "$block" ] || exit 2
[ "${SCRATCH+x}" != x ] || exit 2
eval "$block"
[ ! -f "$fixture_root/no-overlap" ] || { echo 'FAIL producer overlap barrier'; exit 1; }
[ ! -f "$fixture_root/early-restore" ] || { echo 'FAIL configuration restored before doctor joined'; exit 1; }
[ "$(cat "$fixture_root/installs")" = "$(printf 'drop\nrestore')" ] || { echo 'FAIL both real-install doors were not retained'; exit 1; }
[ -f "$fixture_root/ended" ] || { echo 'FAIL spare chat cleanup omitted'; exit 1; }
pid="$(cat "$fixture_root/doctor.pid")"
kill -0 "$pid" 2>/dev/null && { echo 'FAIL doctor worker not reaped'; exit 1; }
jq -e 'any(.accounts[]; .id == 2)' "$CONFIG" >/dev/null || exit 1
SH
)"; rc=$?
  case "$variant" in
    overlap|doctor-warning)
      if [ "$rc" -eq 0 ] && [[ "$out" = PASS* ]]; then
        ok "O2 dropped-seat $variant overlaps independent work, keeps both installs and waits before restore"
      else bad "O2 $variant producer/restore boundary" "rc=$rc $out"; fi
      ;;
    *)
      if [ "$rc" -eq 0 ] && [[ "$out" = FAIL* ]] &&
        { [[ "$out" = *'doctor exited 2'* ]] || [[ "$out" = *'call into O2_SPARE was refused'* ]] || [[ "$out" = *'receipt'* ]]; }; then
        ok "O2 $variant remains a named failure, restores config and reaps its worker"
      else bad "O2 $variant control" "rc=$rc $out"; fi
      ;;
  esac
done
shtest_end

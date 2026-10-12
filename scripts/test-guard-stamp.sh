#!/usr/bin/env bash
set -euo pipefail

# Exercise real stdin and filesystem behavior, but relocate ONLY the hard-coded
# marker parent in disposable hook copies. No hook ever writes a real marker.
REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
error() { printf 'ERROR guard-stamp: %s\n' "$*" >&2; exit 2; }
for tool in jq git python3; do command -v "$tool" >/dev/null || error "$tool is absent"; done
ROOT=$(mktemp -d "${TMPDIR:-/tmp}/guard-stamp-regression.XXXXXX") || error 'mktemp failed'
trap 'rm -rf "$ROOT"' EXIT
LAW="$ROOT/.stamp-law"; SESSION="$ROOT/.stamp-session"
MARKERS="$ROOT/markers"; SID=stamp-session
for r in "$LAW" "$SESSION"; do
  mkdir -p "$r/.claude/commands/quality" && git -C "$r" init -q || error "cannot create fixture repo $r"
done
LAW_FILE="$LAW/.claude/commands/quality/prompt.md"
: > "$LAW_FILE"
: > "$SESSION/CLAUDE.md"
L_GUARD="$MARKERS/stamp-law/guard"; S_GUARD="$MARKERS/stamp-session/guard"

sandbox_hook() { # <kind> <source> <destination>
  python3 -I - "$@" "$MARKERS" <<'PY'
import pathlib, sys
kind, source, destination, markers = sys.argv[1:]
text = pathlib.Path(source).read_text()
patterns = {
    "stamp": [("printf '/tmp/%s/guard'", "printf '" + markers + "/%s/guard'")],
    "guard": [('GUARD_DIR="/tmp/$PROJECT/guard"', 'GUARD_DIR="' + markers + '/$PROJECT/guard"'),
              ('QUALITY="/tmp/${CWD_PROJECT#.}/guard/quality_loaded', 'QUALITY="' + markers + '/${CWD_PROJECT#.}/guard/quality_loaded')],
}
for old, new in patterns[kind]:
    if text.count(old) != 1:
        sys.exit(f"ERROR guard-stamp: unsafe/unrecognized {kind} marker derivation in {source}: {old}")
    text = text.replace(old, new)
if any('/tmp/' in line.replace(markers, '') for line in text.splitlines() if line.strip() and not line.lstrip().startswith('#')):
    sys.exit(f"ERROR guard-stamp: an unrelocated marker path remains in {source}")
pathlib.Path(destination).write_text(text)
PY
}

fail=0
check() {
  local label=$1; shift
  if "$@"; then printf 'PASS guard-stamp: %s\n' "$label"
  else printf 'FAIL guard-stamp: %s\n' "$label" >&2; fail=1; fi
}
empty_markers() { [[ -z "$(find "$MARKERS" -type f -print)" ]]; }
reset_markers() { rm -rf "$MARKERS"; mkdir -p "$MARKERS"; }
event() { # <file> <session-id kind: present|empty|missing> <mode>
  local file=$1 sid_kind=$2 mode=${3:-read} json rc=0
  json=$(jq -cn --arg f "$file" --arg s "$SID" --arg c "$SESSION" --arg kind "$sid_kind" \
    '{tool_input:{file_path:$f},cwd:$c} + (if $kind == "missing" then {} else {session_id:(if $kind == "empty" then "" else $s end)} end)')
  (cd "$SESSION" && printf '%s\n' "$json" | bash "$ROOT/stamp.sh" "$mode") || rc=$?
  [[ "$rc" == 0 ]]
}
valid_stamp() {
  local value
  [[ -f "$1" ]] || return 1
  value=$(cat "$1")
  [[ "$value" =~ ^[0-9]+$ ]] && (( value >= before && value <= $(date +%s) ))
}
read_stamps_both() {
  event "$LAW_FILE" present && valid_stamp "$L_GUARD/quality_loaded.$SID" && valid_stamp "$S_GUARD/quality_loaded.$SID" &&
    [[ "$(find "$MARKERS" -type f | wc -l)" -eq 2 ]]
}
read_without_sid() { event "$LAW_FILE" "$1" && empty_markers; }
read_other() { event "$SESSION/CLAUDE.md" present && empty_markers; }
guard_agrees() {
  local json rc=0
  date +%s > "$S_GUARD/pfm_active.$SID"
  json=$(jq -cn --arg f "$SESSION/CLAUDE.md" --arg s "$SID" --arg c "$SESSION" \
    '{tool_input:{file_path:$f},session_id:$s,cwd:$c}')
  printf '%s\n' "$json" | bash "$ROOT/guard.sh" > "$ROOT/guard.out" || rc=$?
  [[ "$rc" == 0 && ! -d "$MARKERS/.stamp-session" && ! -d "$MARKERS/.stamp-law" ]]
}
stop_reaps() {
  local family now
  now=$(date +%s)
  for family in pfm_active quality_loaded; do
    printf '%s\n' "$((now - 3700))" > "$S_GUARD/$family.stale"
    printf '%s\n' "$now" > "$S_GUARD/$family.fresh"
  done
  event '' missing stop || return 1
  for family in pfm_active quality_loaded; do
    [[ ! -f "$S_GUARD/$family.stale" && "$(cat "$S_GUARD/$family.fresh")" == "$now" ]] || return 1
  done
  [[ -f "$S_GUARD/quality_loaded.$SID" ]]
}

run_twin() {
  local stamp=$1 guard=$2
  [[ -f "$stamp" && -f "$guard" ]] || error "hook missing: $stamp or $guard"
  sandbox_hook stamp "$stamp" "$ROOT/stamp.sh" || error 'stamp hook could not be safely isolated'
  sandbox_hook guard "$guard" "$ROOT/guard.sh" || error 'guard hook could not be safely isolated'
  printf 'TWIN guard-stamp: %s\n' "$stamp"
  reset_markers
  before=$(date +%s)
  check 'law read stamps law and session roots with current session timestamps' read_stamps_both
  check 'dot-stripped marker path agrees with pfm-guard and opens its gate' guard_agrees
  check 'stop reaps >1h active/quality markers and keeps fresh markers' stop_reaps
  reset_markers
  check 'other-file read exits 0 without markers' read_other
  check 'empty session_id exits 0 without markers' read_without_sid empty
  reset_markers
  check 'missing session_id exits 0 without markers' read_without_sid missing
}

case "$#" in
  0)
    run_twin "$REPO_ROOT/.claude/scripts/guard-stamp.sh" "$REPO_ROOT/.claude/scripts/pfm-guard.sh"
    run_twin "$REPO_ROOT/templates/project/scripts/guard-stamp.sh" "$REPO_ROOT/templates/project/scripts/pfm-guard.sh" ;;
  2) run_twin "$1" "$2" ;;
  *) error 'usage: test-guard-stamp.sh [STAMP_HOOK GUARD_HOOK]' ;;
esac
[[ "$fail" == 0 ]] && echo 'PASS guard-stamp: all requested twins and behaviors exercised with scratch-only markers'
exit "$fail"

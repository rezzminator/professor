#!/usr/bin/env bash
# The process reader helper owns scoped faults; these are independent owner intents.
_reader_process() {
  local operation
  case "$SLUG" in
    watch-keeps-child-activity-working) operation=watch ;;
    reap-preview-retains-live-foreign-owner) operation=reap ;;
    theme-keeps-healthy-rows-beside-stopped-tmux) operation=theme ;;
    *) return 2 ;;
  esac
  python3 - "$FIXTURES" "$operation" "$PREFIX.resources.json" "$HOME" <<'PY'
import sys
sys.path.insert(0, sys.argv[1])
from process_readers import resources_for
from native_setup import account_settings
from runtime import write_json
fixture = {"account_settings": account_settings(sys.argv[4])} if sys.argv[2] == "theme" else {}
write_json(sys.argv[3], resources_for(sys.argv[2], fixture))
PY
  [ "$?" -eq 0 ] || return 2
  if [ "$operation" = theme ]; then
    expect-log "$(python3 - "$PREFIX.resources.json" <<'PY'
import json, re, sys
from pathlib import Path
socket = json.loads(Path(sys.argv[1]).read_text())["stopped_socket"]
print("(?=.*" + re.escape(Path(socket).name) + ")(?=.*(?:tmux server did not answer|signal: killed))")
PY
)"
  fi
  _reader_before || return 2
  python3 "$FIXTURES/process_readers.py" run "$operation" "$CHAT" "$OWNER" "$PREFIX.resources.json" "$PREFIX" || return 2
  case "$operation" in
    watch)
      _reader_invocation "$PREFIX.watch" "$(jq -nc --arg name "$(jq -r '.name' "$CHAT")" '["pfm","chat","watch",$name,"--transitions","--once","--poll","10"]')" ;;
    reap)
      _reader_invocation "$PREFIX.reap-first" '["pfm","reap","--json"]'
      _reader_invocation "$PREFIX.reap-second" '["pfm","reap","--json"]' ;;
    theme)
      _reader_invocation "$PREFIX.theme-before" '["pfm","theme","status","--json"]'
      _reader_invocation "$PREFIX.theme-on" '["pfm","theme","on","--json"]'
      _reader_invocation "$PREFIX.theme-use" '["pfm","theme","use","light","--json"]'
      _reader_invocation "$PREFIX.theme-off" '["pfm","theme","off","--json"]' ;;
  esac
  mesh_checks "$ID" "$PREFIX.checks.json"
  if [ "$operation" != theme ]; then
    assert "$ID" "$(cat "$PREFIX.expected_input.txt")" "$(cat "$PREFIX.actual_input.txt")" "engine-input:$PREFIX.actual_input.txt"
  fi
  _reader_finish || return 2
  pass
}

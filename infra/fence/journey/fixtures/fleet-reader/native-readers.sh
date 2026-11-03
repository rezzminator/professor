#!/usr/bin/env bash
# Native forecasts share the helper's exact selector and public prelude doors.
_reader_native_prelude() {
  local plan role
  plan="$(jq -r '.plan' "$READER_NATIVE_PLAN")"
  case "$plan" in
    unnamed-transcript)
      python3 "$HERE/chat.py" ensure-role "$BRANCH" reader-unnamed '' "$HERE/fixtures/chat-mesh/seed.json" >"$JOURNEY_ROOT/reader-current.json" || return 2 ;;
    unknown|moved|moved-inherited|stale-seat-moved|stale-seat-missing-pane)
      role="reader-native-$plan"
      python3 "$HERE/chat.py" ensure-role "$BRANCH" "$role" "$role-${PFM_RUN: -8}" "$HERE/fixtures/chat-mesh/seed.json" >"$JOURNEY_ROOT/reader-current.json" || return 2 ;;
  esac
  cp "$READER_OPERATOR" "$JOURNEY_ROOT/reader-native-owner.json"
  python3 "$FIXTURES/native_setup.py" "$READER_NATIVE_PLAN" "$JOURNEY_ROOT/reader-current.json" "$JOURNEY_ROOT/reader-native-owner.json" "$JOURNEY_ROOT/reader-native-resources.json" "$ID"
}

_reader_native() {
  local entry result row path
  entry="$(jq -r '.entry' "$READER_NATIVE_PLAN")"
  if [ "$entry" = unnamed-mcp ]; then _reader_unnamed; return "$?"; fi
  cp "$JOURNEY_ROOT/reader-native-owner.json" "$OWNER"
  export JOURNEY_TARGET="$OWNER"
  _reader_before || return 2
  if [ "$entry" = macos-mcp ]; then
    python3 "$FIXTURES/native_mcp.py" stage "$JOURNEY_ROOT/reader-native-resources.json" "$PREFIX.native-new" || return 2
    local name role
    name="$(jq -er '.expected_child.name' "$JOURNEY_ROOT/reader-native-resources.json")"
    role="reader-$name"
    mesh_mcp "$CHAT" new "$(jq -c '.mcp_input' "$JOURNEY_ROOT/reader-native-resources.json")" "$PREFIX.native-new" || return 2
    assert "$ID" "$MESH_INPUT_EXPECTED" "$(cat "$PREFIX.native-new.input.txt")" "engine-input:$PREFIX.native-new.input.txt"
    mesh_adopt_new "$CHAT" "$name" "$PREFIX.native-new.result.json" "$PREFIX.native-new" "$role" || return 2
    python3 "$FIXTURES/native_mcp.py" complete "$JOURNEY_ROOT/reader-native-resources.json" "$PREFIX.native-new.child.json" "$PREFIX.native-new.result.json" "$OWNER" "$PREFIX.native-new" || return 2
  fi
  case "$entry" in
    list|native-list|macos-mcp|shell-transition|shell-client|shell-birth|launch-reader|presence-reader) python3 "$FIXTURES/native_forecasts.py" run "$FACTORS" "$CHAT" "$OWNER" "$JOURNEY_ROOT/reader-native-resources.json" "$PREFIX" -- pfm ls --all --tsv ;;
    launcher) python3 "$FIXTURES/native_forecasts.py" run "$FACTORS" "$CHAT" "$OWNER" "$JOURNEY_ROOT/reader-native-resources.json" "$PREFIX" -- claude --version ;;
    macos-theme) python3 "$FIXTURES/native_forecasts.py" run "$FACTORS" "$CHAT" "$OWNER" "$JOURNEY_ROOT/reader-native-resources.json" "$PREFIX" -- pfm theme sync --json ;;
    tmux-theme) python3 "$FIXTURES/native_forecasts.py" run "$FACTORS" "$CHAT" "$OWNER" "$JOURNEY_ROOT/reader-native-resources.json" "$PREFIX" -- pfm theme on --json ;;
    *) python3 "$FIXTURES/native_forecasts.py" run "$FACTORS" "$CHAT" "$OWNER" "$JOURNEY_ROOT/reader-native-resources.json" "$PREFIX" -- pfm whoami --json ;;
  esac
  result=$?
  [ "$result" -eq 0 ] || return 2
  case "$entry" in
    list|shell-transition|shell-client|shell-birth|launch-reader|presence-reader) _reader_invocation "$PREFIX.command" '["pfm","ls","--all","--tsv"]' ;;
    launcher) _reader_invocation "$PREFIX.command" '["claude","--version"]' ;;
    macos-mcp) _reader_invocation "$PREFIX.native-list" '["pfm","ls","--all","--tsv"]' ;;
    outside) _reader_invocation "$PREFIX.command" '["pfm","whoami","--json"]' ;;
    macos-theme|tmux-theme)
      python3 - "$HERE" "$FIXTURES" "$PREFIX" <<'PY'
import sys
sys.path.insert(0, sys.argv[1])
from runtime import read_json, write_json
fixture, prefix = sys.argv[2:]
intents = read_json(fixture + "/theme-intents.json")
expected = []
for receipt in read_json(prefix + ".evidence.json")["receipts"]:
    path = receipt["prefix"]
    if not path.startswith(prefix + "."):
        raise ValueError("READER-THEME-RECEIPT-PREFIX: " + path)
    label = path[len(prefix) + 1:]
    expected.append({"prefix": path, "argv": intents[label]})
write_json(prefix + ".intents.json", expected)
PY
      [ "$?" -eq 0 ] || return 2
      while IFS= read -r row; do
        path="$(jq -r '.prefix' <<<"$row")"
        _reader_invocation "$path" "$(jq -c '.argv' <<<"$row")"
      done < <(jq -c '.[]' "$PREFIX.intents.json") ;;
    *) assert "$ID" "$(cat "$PREFIX.command.expected-input.txt")" "$(cat "$PREFIX.command.input.txt")" "engine-input:$PREFIX.command.input.txt" ;;
  esac
  if [ "$entry" = launch-reader ]; then
    _reader_invocation "$PREFIX.launch" "$(jq -c '["pfm","chat","new","--engine","cc","--backend","herdr","--account","1","--cwd",.cwd,"--name",.launch_name]' "$JOURNEY_ROOT/reader-native-resources.json")" || return 2
    mesh_action "$ID" chat.new typed
  fi
  if [ "$entry" = presence-reader ]; then
    mesh_action "$ID" picker.open typed
    if [ -f "$PREFIX.end-raw.dispatch.json" ]; then
      _reader_invocation "$PREFIX.end-raw" "$(jq -nc --arg sid "$(jq -r '.session_id' "$JOURNEY_ROOT/reader-native-resources.json")" '["pfm","chat","end",$sid]')" || return 2
    fi
  fi
  if [ "$(jq -r '.plan' "$READER_NATIVE_PLAN")" = moved-replaced-client ]; then
    local old name label
    old="$(jq -er '.old_chat' "$JOURNEY_ROOT/reader-native-resources.json")"
    for label in stale-name restore-name; do
      name='Reader stale client'
      if [ "$label" = restore-name ]; then name="$(jq -r '.name' "$old")"; fi
      _reader_invocation "$PREFIX.$label" "$(jq -nc --arg sid "$(jq -r '.session_id' "$old")" --arg name "$name" '["pfm","chat","name",$sid,$name]')" || return 2
    done
    assert "$ID" "$(cat "$PREFIX.replacement-ready.expected-input.txt")" "$(cat "$PREFIX.replacement-ready.input.txt")" "engine-input:$PREFIX.replacement-ready.input.txt"
  fi
  if [ "$entry" = shell-birth ]; then
    mesh_action "$ID" chat.new picker
    _reader_invocation "$PREFIX.end-child" "$(jq -nc --arg sid "$(jq -r '.child.session_id' "$PREFIX.evidence.json")" '["pfm","chat","end",$sid]')" || return 2
    if [ "$(jq -r '.plan' "$READER_NATIVE_PLAN")" = user-tab-exec ]; then
      assert "$ID" "$(cat "$PREFIX.prior-label.expected-input.txt")" "$(cat "$PREFIX.prior-label.input.txt")" "engine-input:$PREFIX.prior-label.input.txt"
    fi
  fi
  mesh_checks "$ID" "$PREFIX.checks.json"
  cp "$PREFIX.chat.json" "$CHAT"
  jq '.terminal' "$CHAT" >"$CHAT.target"
  local moved_name
  moved_name="$(jq -r 'if .key then "key:"+.key else .name end' "$CHAT")"
  case "$(jq -r '.plan' "$READER_NATIVE_PLAN")" in moved*) moved_name="moved:$moved_name" ;; esac
  # Keep the immutable dispatch target for pass; observe through the surviving owner.
  case "$entry" in
    identity) _reader_finish "$moved_name" || return 2 ;;
    list)
      case "$(jq -r '.plan' "$READER_NATIVE_PLAN")" in
        unknown) _reader_finish || return 2 ;;
        stale-seat-*) : ;; # Helper pins the full restored bundle and every peer after the real host replacement.
        *) mesh_bundle "$ID" "$PREFIX.before.json" || return 2 ;;
      esac ;;
    outside|denied|launcher|launch-reader) JOURNEY_TARGET="$READER_SHELL" mesh_bundle "$ID" "$PREFIX.before.json" || return 2 ;;
    shell-client)
      if [ "$(jq -r '.plan' "$READER_NATIVE_PLAN")" = moved-replaced-client ]; then
        mesh_bundle_actors "$ID" "$PREFIX.before.json" "$(jq -er '.old_chat' "$JOURNEY_ROOT/reader-native-resources.json")" "$(jq -er '.replacement_chat' "$JOURNEY_ROOT/reader-native-resources.json")" || return 2
      else mesh_bundle "$ID" "$PREFIX.before.json" || return 2; fi ;;
    shell-birth)
      if [ "$(jq -r '.plan' "$READER_NATIVE_PLAN")" = user-tab-exec ]; then
        mesh_bundle_actors "$ID" "$PREFIX.before.json" "$(jq -er '.old_chat' "$JOURNEY_ROOT/reader-native-resources.json")" || return 2
      else mesh_bundle "$ID" "$PREFIX.before.json" || return 2; fi ;;
    *) mesh_bundle "$ID" "$PREFIX.before.json" || return 2 ;;
  esac
  pass
}

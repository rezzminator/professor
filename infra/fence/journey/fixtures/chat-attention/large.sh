# The engine owns the >1MiB stdout and its registered PostToolUse callback.
_attention_large_ready() {
  python3 "$FIXTURE/state.py" tool-output "$(jq -r '.records' "$CHAT")/shell.jsonl" \
    "$ARTIFACT/steps.json" "$ARTIFACT/large-tool.json"
}
_attention_large() {
  _attention_begin large 'run the large tool response' && _attention_wait working || return "$?"
  poll large-tool-completed 10 _attention_large_ready || return "$?"
  assert "$ID" "$(cat "$HERE/goldens/chat-attention/large-stdout.sha256")" \
    "$(jq -r '.stdout_sha256' "$ARTIFACT/large-tool.json")" "$ARTIFACT/large-tool.json"
  assert "$ID" 0 "$(jq -r '.exit_code' "$ARTIFACT/large-tool.json")" "$ARTIFACT/large-tool.json"
  assert "$ID" '' "$(jq -r '.stderr' "$ARTIFACT/large-tool.json")" "$ARTIFACT/large-tool.json"
  rm "$HOLD" || return 2
  _attention_wait idle || return "$?"
  assert "$ID" '[]' "$(jq -c '[.activity.records[] | select(tojson | contains("unexpected end of JSON input"))]' "$JOURNEY_OBSERVATION")" "$JOURNEY_OBSERVATION"
  lane_action chat.prompt typed
}

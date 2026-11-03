#!/usr/bin/env bash
# End-lane receipts and checks use the published journey transports.
export PYTHONPATH="$HERE${PYTHONPATH:+:$PYTHONPATH}"
ending_command() {
  local target="$1" name="$2"
  shift 2
  RECEIPT="$JOURNEY_OUT/beats/$PFM_BEAT/$name"
  python3 "$HERE/terminal.py" receipt "$target" "$RECEIPT" "$@" &&
    poll "$name-completed" 90 test -s "$RECEIPT.status"
}

ending_assertions() {
  local id="$1" source="$2" row field proof
  while IFS= read -r row; do
    field="$(jq -r '.field' <<<"$row")" proof="$source:$field"
    assert "$id" "$(jq -c '.expected' <<<"$row")" "$(jq -c '.actual' <<<"$row")" "$proof"
  done < <(jq -c '.[]' "$source")
}

ending_invocation() {
  local id="$1" prefix="$2" expected
  shift 2
  expected="$(jq -cn --args '$ARGS.positional' "$@")" || return 2
  assert "$id" "$expected" "$(jq -c .argv "$prefix.dispatch.json")" "owner-input:$prefix"
}

ending_action() {
  local id="$1" action="$2" door="$3"
  assert "$id" "$action@$door" "$(python3 "$HERE/activity.py" observed-action "$JOURNEY_OBSERVATION" "$action" "$door")" "$JOURNEY_OBSERVATION"
}

ending_finish() {
  local source="${1:-}" reason="${2:-}"
  if [ -n "$source" ] && jq -e --arg source "$source" \
      '.errors == [] and ([.assertions[] | select(.pass == false)] | length > 0 and all(.source == $source))' \
      "$JOURNEY_OUT/$JOURNEY_LANE.state.json" >/dev/null; then
    known "$reason"
  else
    pass
  fi
}

#!/usr/bin/env bash
# Fault-scoped fleet read compositions over the public journey doors.
_reader_herdr_gap() {
  local capability="$1"
  python3 "$HERE/faults.py" herdr-capabilities "$PREFIX.herdr-capability" >"$PREFIX.herdr-capability.json" || return 2
  jq -c '.methods' "$PREFIX.herdr-capability.json" >"$PREFIX.herdr-methods.json" || return 2
  golden "$ID" fleet-reader/herdr-methods.json "$PREFIX.herdr-methods.json"
  assert "$ID" 'herdr 0.9.5' "$(jq -r '.version' "$PREFIX.herdr-capability.json")" "$PREFIX.herdr-capability.json"
  assert "$ID" pane.get "$(jq -r '.[]|select(.=="pane.get")' "$PREFIX.herdr-methods.json")" "$PREFIX.herdr-capability.json"
  # This is the approved missing external precondition, never an injected factor observation.
  if [ "$capability" = isolated-pane-recycle ]; then
    assert "$ID" true "$(jq -r '.isolated_recycle' "$PREFIX.herdr-capability.json")" ruling:ruling-gap-herdr-api
  else
    assert "$ID" "available:$capability" "unavailable:$capability:Herdr0.9.5" ruling:ruling-gap-herdr-api
  fi
  _reader_known ruling:ruling-gap-herdr-api "ruling:ruling-gap-herdr-api; $capability unavailable in captured pinned schema"
}

_reader_lost() {
  local prior_sid scratch_name fault_rc mode=list
  if [ "$SLUG" = picker-refresh-with-dead-seat ]; then mode=dead; fi
  prior_sid="$(jq -r '.session_id' "$SCRATCH")"
  scratch_name="$(jq -r '.name' "$SCRATCH")"
  case "$FACTORS" in
    multiplexer=tmux,seat_event=seat_dead) assert "$ID" tmux "$(jq -r '.terminal.backend' "$SCRATCH")" "$SCRATCH" ;;
    seat_event=seat_dead,door=list) assert "$ID" herdr "$(jq -r '.terminal.backend' "$SCRATCH")" "$SCRATCH" ;;
  esac
  _reader_before || return 2
  python3 "$FIXTURES/projection.py" engine-birth "$PREFIX.before.json" "$SCRATCH" >"$PREFIX.old-birth.json" || return 2
  python3 "$HERE/faults.py" killed-process "$SCRATCH" "$PREFIX.old-birth.json" "$OWNER" -- \
    python3 "$FIXTURES/transient-read.py" "$SCRATCH" "$PREFIX.before.json" -- \
      bash "$FIXTURES/lost-read.sh" "$ID" "$PREFIX" "$OWNER" "$SCRATCH" "$mode"
  fault_rc=$?
  _reader_scratch_rebind reader-lost || return 2
  [ "$fault_rc" -eq 0 ] || return 2
  case "$SLUG@$JOURNEY_BRANCH" in
    picker-refresh-with-dead-seat@*) _reader_herdr_gap pane-get-request-count ;;
    ls-lists-lost-seat@linux+herdr-shield) _reader_herdr_gap isolated-pane-recycle ;;
    *) pass ;;
  esac
  return 0
}

_reader_scratch_rebind() {
  python3 "$HERE/chat.py" refresh "$BRANCH" "$1" >"$PREFIX.restored.json" || return 2
  jq '.terminal' "$PREFIX.restored.json" >"$PREFIX.restored.json.target"
  poll restored-scratch-idle 30 mesh_state "$PREFIX.restored.json" idle || return 2
  assert "$ID" "$prior_sid" "$(jq -r '.session_id' "$PREFIX.restored.json")" "$PREFIX.restored.json"
  mesh_mcp "$PREFIX.restored.json" whoami '{}' "$PREFIX.restored-whoami" || return 2
  python3 "$FIXTURES/projection.py" identity "$PREFIX.restored-whoami.result.json" "$PREFIX.restored.json" >"$PREFIX.restored-identity.json" || return 2
  mesh_checks "$ID" "$PREFIX.restored-identity.json"
  export JOURNEY_TARGET="$OWNER"
  observe || return 2
  python3 "$FIXTURES/projection.py" engine-birth "$JOURNEY_OBSERVATION" "$PREFIX.restored.json" >"$PREFIX.new-birth.json" || return 2
  assert "$ID" changed "$(if cmp -s "$PREFIX.old-birth.json" "$PREFIX.new-birth.json"; then printf unchanged; else printf changed; fi)" "$PREFIX.new-birth.json"
  jq --arg key "$(jq -r '.key' "$SCRATCH")" '.chats |= map(select(.key != $key))' "$JOURNEY_CARRY" >"$PREFIX.unaffected-carry.json"
  python3 "$FIXTURES/projection.py" bundle "$JOURNEY_OBSERVATION" "$PREFIX.unaffected-carry.json" "$PREFIX.before.json" >"$PREFIX.unaffected-checks.json" || return 2
  mesh_checks "$ID" "$PREFIX.unaffected-checks.json"
  assert "$ID" "$scratch_name" "$(jq -r '.name' "$PREFIX.restored.json")" "$PREFIX.restored.json"
}

_reader_deleted() {
  local prior_sid scratch_name fault_rc
  prior_sid="$(jq -r '.session_id' "$SCRATCH")"
  scratch_name="$(jq -r '.name' "$SCRATCH")"
  _reader_before || return 2
  python3 "$FIXTURES/projection.py" engine-birth "$PREFIX.before.json" "$SCRATCH" >"$PREFIX.old-birth.json" || return 2
  python3 "$HERE/faults.py" deleted-cwd "$READER_GONE_DIR" -- \
    bash "$FIXTURES/deleted-read.sh" "$ID" "$PREFIX" "$OWNER" "$SCRATCH"
  fault_rc=$?
  # Reopen after restoring the directory: the old process still holds its deleted inode.
  python3 "$HERE/faults.py" killed-process "$SCRATCH" "$PREFIX.old-birth.json" "$OWNER" -- true || return 2
  _reader_scratch_rebind reader-gone || return 2
  [ "$fault_rc" -eq 0 ] || return 2
  pass
}

_reader_forecast() {
  _reader_before || return 2
  if [ "$SLUG" = forecast-tmux-server-sigstopped-probe-site-gather-probe-without-deadline-client-pipe-held-by-stopped-server ]; then
    jq '{tmux_socket:.terminal.socket,max_wall_ms:10000}' "$JOURNEY_ROOT/reader-stopped.json" >"$PREFIX.resources.json"
  else
    printf '{}\n' >"$PREFIX.resources.json"
  fi
  python3 "$FIXTURES/forecast.py" run "$FACTORS" "$CHAT" "$OWNER" "$PREFIX" "$PREFIX.resources.json" -- pfm ls --all --tsv || return 2
  _reader_invocation "$PREFIX.command" '["pfm","ls","--all","--tsv"]'
  jq '.evidence.checks' "$PREFIX.forecast.json" >"$PREFIX.factor-checks.json" || return 2
  mesh_checks "$ID" "$PREFIX.factor-checks.json"
  assert "$ID" 0 "$(jq -r '.receipt.exit_code' "$PREFIX.forecast.json")" "$PREFIX.forecast.json"
  if [ "$SLUG" = forecast-tmux-server-sigstopped-probe-site-gather-probe-without-deadline-client-pipe-held-by-stopped-server ]; then
    python3 "$FIXTURES/projection.py" stopped-fleet "$PREFIX.command.out" "$JOURNEY_CARRY" "$(jq -r '.tmux_socket' "$PREFIX.resources.json")" >"$PREFIX.fleet-checks.json" || return 2
  else
    python3 "$FIXTURES/projection.py" text-fleet "$PREFIX.command.out" "$JOURNEY_CARRY" >"$PREFIX.fleet-checks.json" || return 2
  fi
  mesh_checks "$ID" "$PREFIX.fleet-checks.json"
  _reader_finish || return 2
  pass
  return 0
}

_reader_unavailable_kernel() {
  local result
  _reader_before || return 2
  printf '{}\n' >"$PREFIX.resources.json"
  python3 "$FIXTURES/forecast.py" run "$FACTORS" "$CHAT" "$OWNER" "$PREFIX" "$PREFIX.resources.json" -- pfm ls --all --tsv >"$PREFIX.unavailable.txt" 2>&1
  result=$?
  assert "$ID" 0 "$result" ruling:ruling-gap-darwin-empty-kinfo
  assert "$ID" 2 "$result" "$PREFIX.unavailable.txt"
  assert "$ID" 'FORECAST-ERROR: FORECAST-UNSUPPORTED: no evidenced setup for {"os": "macos", "process_table": "empty_kinfo_or_eio", "seat_owner": "herdr_seat_row"}' "$(cat "$PREFIX.unavailable.txt")" "$PREFIX.unavailable.txt"
  _reader_finish || return 2
  _reader_known ruling:ruling-gap-darwin-empty-kinfo 'ruling:ruling-gap-darwin-empty-kinfo; native failure injection is unavailable'
}


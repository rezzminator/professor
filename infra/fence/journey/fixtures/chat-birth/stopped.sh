#!/usr/bin/env bash
_birth_stopped_prepare() {
  local JOURNEY_BRANCH="$BRANCH+project+claude" BIRTH_PLAIN=1
  STOPPED_BRANCH="$JOURNEY_BRANCH"
  _birth_prepare "$SLUG"
}
_birth_stopped() {
  local id="$1" JOURNEY_BRANCH="$STOPPED_BRANCH" evidence="$JOURNEY_OUT/beats/$PFM_BEAT/stopped.json"
  ENGINE=claude
  assert "$id" pty "$(jq -r '.backend' "$SHELL_TARGET")" "$SHELL_TARGET"
  python3 "$FIXTURE/stopped.py" "$SHELL_TARGET" "$(jq -er '.socket' "$OWNER")" \
    "$JOURNEY_OUT/beats/$PFM_BEAT/refused" "$evidence" || { fail 'stopped-server launcher did not finish within its bound'; return 1; }
  assert "$id" true "$(jq '.elapsed_ms <= 10000' "$evidence")" "$evidence"
  assert "$id" 1 "$(jq -r '.status' "$evidence")" "$evidence"
  assert "$id" 'tmux server unresponsive' "$(jq -r '.output' "$evidence")" "$evidence"
  assert "$id" "$(jq -r '.clients_before' "$evidence")" "$(jq -r '.clients_after' "$evidence")" "$evidence"
  _birth_new "$SLUG" "$id" new-typed
}

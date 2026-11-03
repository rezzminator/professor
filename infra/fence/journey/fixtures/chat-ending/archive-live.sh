#!/usr/bin/env bash
# A restricted discovery namespace records a public kill without addressing the live pane.
_ending_archive_live() {
  local id=ops.archive before sid empty expected
  _ending_select new-typed || return 2
  before="$END_ROOT/archive-live-prelude.json" sid="$(jq -r '.session_id' "$CHAT")"
  python3 "$HERE/chat.py" snapshot "$OWNER" "$before" &&
    python3 "$HERE/fixtures/chat-ending/archive.py" prepare "$CHAT" "$OWNER" "$before" "$END_ROOT/archive-live-policy.json" || return 2
  empty="$END_ROOT/archive-live-empty-tmux"
  mkdir -p "$empty"
  ending_command "$OWNER" archive-live-mark env "PFM_TMUX_DIR=$empty" pfm chat kill "$sid" || return 2
  expected="$(printf 'killed %s\tde-listed only, no live pane closed' "$sid")"
  if [ "$(cat "$RECEIPT.status")" != 0 ] || [ "$(cat "$RECEIPT.out")" != "$expected" ] || ! _ending_state idle; then
    ending_command "$OWNER" archive-live-undo pfm chat unkill "$sid" || return 2
    _ending_resume || return 2
    printf 'archive-live prelude did not prove a de-listed-only public kill and a still-idle engine\n' >&2
    return 2
  fi
  python3 "$HERE/fixtures/chat-ending/lifecycle.py" expect "$CHAT" live - killed || return 2
  if ! beat archive-ends-only-selected-live-process "$id"; then return; fi
  before="$JOURNEY_OUT/beats/$PFM_BEAT/before.json"
  if python3 "$HERE/chat.py" snapshot "$OWNER" "$before" &&
     ending_command "$OWNER" archive-live pfm archive; then
    ending_invocation "$id" "$RECEIPT" pfm archive
    assert "$id" 0 "$(cat "$RECEIPT.status")" "$RECEIPT.status"
    golden "$id" chat-ending/archive-live.txt "$RECEIPT.out"
    observe "$OWNER"
    if python3 "$HERE/fixtures/chat-ending/archive.py" kept "$before" "$JOURNEY_OBSERVATION" "$END_ROOT/archive-live-checks.json"; then
      ending_assertions "$id" "$END_ROOT/archive-live-checks.json"
      pass
    else fail 'archive live-candidate preservation unreadable'; fi
  else fail 'archive live-candidate owner entry failed'; fi
  ending_command "$OWNER" archive-live-restore pfm chat unkill "$sid" &&
    python3 "$HERE/fixtures/chat-ending/lifecycle.py" expect "$CHAT" live
}

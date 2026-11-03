#!/usr/bin/env bash
# Public partial-read compositions and independent command intent fixtures.
_reader_partial_resources() {
  local folder="$JOURNEY_ROOT/reader-partial"
  case "$SLUG" in
    index-keeps-good-engine-sessions-after-bad-record)
      jq -n --slurpfile bad "$folder-bad.json" --slurpfile cc "$folder-claude.json" --slurpfile cx "$folder-codex.json" --slurpfile ox "$folder-opencode.json" \
        '{bad_chat:$bad[0],good_chats:[$cc[0],$cx[0],$ox[0]]}' >"$PREFIX.resources.json" ;;
    tokens-names-partial-transcript-read)
      jq -n --slurpfile bad "$folder-bad.json" --slurpfile good "$folder-token.json" \
        '{bad_chat:$bad[0],tokens_good_chat:$good[0]}' >"$PREFIX.resources.json" ;;
    name-sync-preserves-good-name-after-partial-read)
      jq -n --slurpfile bad "$folder-bad.json" '{bad_chat:$bad[0]}' >"$PREFIX.resources.json" ;;
    statusline-warns-on-partial-account-read)
      python3 "$FIXTURES/statusline.py" resources "$CHAT" "$PREFIX" || return 2
      mesh_checks "$ID" "$PREFIX.resources-checks.json" ;;
    headless-names-partial-usage|headless-retains-valid-answer-after-partial-read)
      jq -n --arg config "$HOME/.cc/2" '{config_dir:$config}' >"$PREFIX.resources.json" ;;
    *) return 2 ;;
  esac
}

_reader_partial_intents() {
  python3 - "$HERE" "$SLUG" "$CHAT" "$PREFIX" <<'PY'
from pathlib import Path
import sys
sys.path.insert(0, sys.argv[1])
from runtime import read_json, write_json
here, slug, chat_file, prefix = sys.argv[1:]
chat, resources = read_json(chat_file), read_json(prefix + ".resources.json")
expected = {}
if slug == "index-keeps-good-engine-sessions-after-bad-record":
    expected = {"command": ["pfm", "index", "--full"], "roster": ["pfm", "ls", "--all", "--tsv"]}
elif slug == "name-sync-preserves-good-name-after-partial-read":
    expected = {"rename": ["pfm", "chat", "name", chat["session_id"], "partial-reader-good"],
                "command": ["pfm", "name-sync", "--apply"]}
elif slug == "tokens-names-partial-transcript-read":
    observed = read_json(prefix + ".observation.json")
    good, bad = resources["tokens_good_chat"], resources["bad_chat"]
    roots = []
    for actor in (good, bad):
        rows = [row for row in observed["chats"] if row["status"].get("session_id") == actor["session_id"]]
        if len(rows) != 1:
            raise ValueError("READER-TOKEN-INTENT: one actual native transcript identity required")
        root = str(Path(rows[0]["stored"]["transcript"]["path"]).parent)
        if root not in roots:
            roots.append(root)
    command = ["pfm", "tokens", "--session", good["session_id"], "--out", prefix + ".tokens.json",
               "--prices", str(Path(here).parents[2] / "pfm/internal/tokens/testdata/fixtures/model-cost.json")]
    for root in roots:
        command += ["--root", root]
    expected = {"command": command}
elif slug.startswith("headless-"):
    expected = {"command": ["pfm", "headless", "exec", "--engine", "claude", "--account", "2",
        "--model", "claude-sonnet-4-6", "--prompt", "partial-reader-headless", "--output-format", "json",
        "--no-session-persistence", "--env", "MOCK_ENGINE_SCENARIO=" + prefix + ".headless-scenario.json"]}
write_json(prefix + ".intents.json", [{"prefix": prefix + "." + label, "argv": argv} for label, argv in expected.items()])
PY
}

_reader_partial() {
  local row path
  _reader_partial_resources && _reader_before || return 2
  python3 "$FIXTURES/partial_reads.py" run "$SLUG" "$CHAT" "$OWNER" "$PREFIX.resources.json" "$PREFIX" || return 2
  _reader_partial_intents || return 2
  while IFS= read -r row; do
    path="$(jq -r '.prefix' <<<"$row")"
    _reader_invocation "$path" "$(jq -c '.argv' <<<"$row")"
  done < <(jq -c '.[]' "$PREFIX.intents.json")
  mesh_checks "$ID" "$PREFIX.checks.json"
  while IFS= read -r row; do
    path="$(jq -r '.path' <<<"$row")"
    assert "$ID" "$(jq -r '.expected' <<<"$row")" "$(cat "$path")" "engine-input:$path"
  done < <(jq -c '.inputs[]' "$PREFIX.evidence.json")
  cp "$PREFIX.chat.json" "$CHAT"
  jq '.terminal' "$CHAT" >"$CHAT.target"
  export JOURNEY_TARGET="$OWNER"
  set --
  while IFS= read -r row; do set -- "$@" "$row"; done < <(jq -r '.changed_names[]' "$PREFIX.evidence.json")
  _reader_finish "$@" || return 2
  pass
}

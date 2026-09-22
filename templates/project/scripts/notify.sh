#!/bin/bash
set -euo pipefail

STAMP_BASE="/tmp/professor_turn_start"
THRESHOLD=30  # seconds — only notify for turns this long or longer

notify() {
  local msg="$1" title="$2"
  # Guaranteed-audible cue: afplay needs no Notification Center permission,
  # so it fires even when VS Code/tmux can't post a banner.
  afplay /System/Library/Sounds/Glass.aiff >/dev/null 2>&1 &
  # Visible banner: prefer osascript. terminal-notifier's older builds play the
  # sound but drop the banner on current macOS (bundle not notification-
  # authorized); osascript's display-notification posts reliably. Fall back to
  # terminal-notifier only when osascript is unavailable.
  if command -v osascript >/dev/null 2>&1; then
    # Escape backslashes and quotes — msg/title carry a user-set session name.
    local m=${msg//\\/\\\\}; m=${m//\"/\\\"}
    local t=${title//\\/\\\\}; t=${t//\"/\\\"}
    osascript -e "display notification \"$m\" with title \"$t\"" >/dev/null 2>&1 || true
  else
    terminal-notifier -title "$title" -message "$msg" >/dev/null 2>&1 || true
  fi
}

case "${1:-stop}" in
  start)
    # PreToolUse — stamp the turn's first tool call only. The stamp is keyed by
    # session_id so concurrent sessions of the same user never race on one
    # shared path.
    HOOK_INPUT=$(cat 2>/dev/null || true)
    SID=$(printf '%s' "$HOOK_INPUT" | sed -n 's/.*"session_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
    STAMP="${STAMP_BASE}${SID:+.$SID}"
    [ -f "$STAMP" ] || date +%s > "$STAMP"
    ;;
  stop)
    # Stop — close the /pfm edit gate (pfm-guard.sh) at turn end
    ROOT=$(git rev-parse --show-toplevel 2>/dev/null || true)
    PROJECT="$(basename "${ROOT:-$PWD}")"; PROJECT="${PROJECT#.}"
    rm -f "/tmp/$PROJECT/guard/pfm_active"
    # Hook JSON arrives on stdin (session_id, transcript_path, cwd, ...).
    # Tolerate it missing — manual invocations have no stdin payload.
    HOOK_INPUT=$(cat 2>/dev/null || true)
    # Prefer the human session name (set via /rename) over the raw id, when the
    # harness exposes one: it stores {sessionId, name} in
    # $CLAUDE_CONFIG_DIR/sessions/<pid>.json. Match on the full session_id, then
    # fall back to the 8-char id prefix when no name (or no sessions dir) is
    # found — so single-account installs without named sessions still work.
    SID=$(printf '%s' "$HOOK_INPUT" | sed -n 's/.*"session_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
    STAMP="${STAMP_BASE}${SID:+.$SID}"
    SESSION=$(printf '%s' "$SID" | cut -c1-8)
    if [ -n "$SID" ]; then
      SESS_DIR="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/sessions"
      match=$(grep -l "\"sessionId\"[[:space:]]*:[[:space:]]*\"$SID\"" "$SESS_DIR"/*.json 2>/dev/null | head -1 || true)
      if [ -n "$match" ]; then
        name=$(sed -n 's/.*"name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$match")
        [ -n "$name" ] && SESSION="$name"
      fi
    fi
    PROJECT=$(basename "${ROOT:-$PWD}")
    # Read-then-remove the per-session turn stamp (set -e safe): tolerate a
    # missing/empty stamp instead of erroring. Read-then-remove rather than
    # [ -f ] → cat avoids a TOCTOU if a session is ever double-wired.
    started=$(cat "$STAMP" 2>/dev/null || true)
    rm -f "$STAMP"
    if [ -n "$started" ]; then
      elapsed=$(( $(date +%s) - started ))
      if (( elapsed >= THRESHOLD )); then
        # Humanize the duration so a long turn reads 2m05s / 1h05m, not 125s.
        if (( elapsed < 60 )); then
          dur="${elapsed}s"
        elif (( elapsed < 3600 )); then
          dur=$(printf '%dm%02ds' "$((elapsed / 60))" "$((elapsed % 60))")
        else
          dur=$(printf '%dh%02dm' "$((elapsed / 3600))" "$(((elapsed % 3600) / 60))")
        fi
        # Last typed prompt, for banner context — the newest transcript "user"
        # line with string content that is not a tool_result or a <…>-wrapped
        # command/system blob. jq-gated and optional: no jq or no transcript
        # just omits it. transcript_path comes from the hook payload, with a
        # constructed fallback ($CONFIG/projects/<munged-cwd>/<id>.jsonl).
        PROMPT=""
        TRANSCRIPT=$(printf '%s' "$HOOK_INPUT" | sed -n 's/.*"transcript_path"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
        [ -n "$TRANSCRIPT" ] || TRANSCRIPT="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects/$(printf '%s' "${ROOT:-$PWD}" | sed 's#[/.]#-#g')/${SID}.jsonl"
        if [ -f "$TRANSCRIPT" ] && command -v jq >/dev/null 2>&1; then
          PROMPT=$(tail -n 100 "$TRANSCRIPT" 2>/dev/null | jq -rs '
            [ .[]
              | select(.type=="user" and (has("toolUseResult")|not))
              | (if (.message.content|type)=="string"
                   then .message.content
                   else (.message.content|map(select(.type=="text").text)|join(" ")) end)
              | select(. != null and . != "" and (startswith("<")|not))
            ] | last // ""' 2>/dev/null | tr '\n\t' '  ' | sed 's/  */ /g; s/^ *//; s/ *$//' || true)
          [ ${#PROMPT} -gt 48 ] && PROMPT="${PROMPT:0:48}…"
        fi
        # Banner: project (title + body lead) · session name · last prompt · duration.
        notify "$PROJECT — your turn ☕${SESSION:+ · session $SESSION}${PROMPT:+ · “${PROMPT}”} ($dur)" "{CHARACTER_NAME} 🎓 — $PROJECT"
      fi
    fi
    ;;
esac

package main

// The stub engines the jailed chat-new tests drive over a real tmux server:
// shell scripts standing in for the Codex and Claude TUIs.

// stubRecorder is the half of a stub engine that makes it a CHAT rather than a
// screen: it writes the engine's own transcript and the evidence that binds it
// to this tmux socket — a sid crumb for Claude, an open rollout descriptor
// under a jailed /proc for Codex.
//
// Without it a stub can only prove that keystrokes were sent. pfm now
// refuses to call a prompt delivered until the ENGINE has recorded being
// asked, so a jail that writes no transcript can no longer tell a delivered
// prompt from one that vanished into a modal — which is the whole failure
// being defended against.
const stubRecorder = `
_esc() { printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'; }
_sock() { if [ -n "$TMUX" ]; then basename "${TMUX%%,*}"; else printf '%s' "$STUB_SOCKET"; fi; }
_append() { printf '%s\n' "$1" >> "$STUB_TRANSCRIPT"; }
cx_user()  { _append "{\"timestamp\":\"2026-08-12T00:00:00.000Z\",\"type\":\"response_item\",\"payload\":{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"$(_esc "$1")\"}]}}"; }
cx_agent() { _append "{\"timestamp\":\"2026-08-12T00:00:01.000Z\",\"payload\":{\"type\":\"agent_message\",\"message\":\"$(_esc "$1")\"}}"; }
cc_user()  { _append "{\"type\":\"user\",\"cwd\":\"$(_esc "$PWD")\",\"message\":{\"content\":\"$(_esc "$1")\"}}"; }
cc_agent() { _append "{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"$(_esc "$1")\"}]}}"; }
answer() {
  if [ "$STUB_KIND" = cx ]; then cx_agent "${STUB_REPLY:-ack}: $1"; else cc_agent "${STUB_REPLY:-ack}: $1"; fi
}
turn() {
  [ -z "$STUB_TRANSCRIPT" ] && return 0
  if [ "$STUB_KIND" = cx ]; then cx_user "$1"; else cc_user "$1"; fi
  [ -n "$STUB_MUTE" ] && return 0
  if [ -n "$STUB_DELAY" ]; then ( sleep "$STUB_DELAY"; answer "$1" ) & else answer "$1"; fi
  return 0
}
_fakeproc() {
  [ -z "$PFM_PROC_ROOT" ] && return 0
  mkdir -p "$PFM_PROC_ROOT/$$/fd"
  printf '%s\0--jailed\0' "$1" > "$PFM_PROC_ROOT/$$/cmdline"
  printf '%s (%s) S %s 1 1 0 -1 0 0 0 0 0 0 0 0 0 0 0 20 0 100\n' "$$" "$1" "$PPID" \
    > "$PFM_PROC_ROOT/$$/stat"
  : > "$PFM_PROC_ROOT/$$/environ"
  # The entry goes when the engine does, so a chat that exits stops looking
  # alive to the very scan that has to notice it died.
  trap 'rm -rf "$PFM_PROC_ROOT/$$"' EXIT
  return 0
}
codex_live() {
  STUB_KIND=cx
  [ -z "$CX_STUB_ROLLOUT" ] && return 0
  STUB_TRANSCRIPT="$CX_STUB_ROLLOUT"
  mkdir -p "$(dirname "$STUB_TRANSCRIPT")"
  if [ ! -s "$STUB_TRANSCRIPT" ]; then
    rollout_name="${STUB_TRANSCRIPT##*/}"
    rollout_id="${rollout_name%.jsonl}"
    rollout_id="${rollout_id: -36}"
    printf '{"type":"session_meta","payload":{"id":"%s","source":"cli","thread_source":"user"}}\n' "$rollout_id" >> "$STUB_TRANSCRIPT"
  fi
  _fakeproc codex
  ln -sf "$STUB_TRANSCRIPT" "$PFM_PROC_ROOT/$$/fd/7"
  return 0
}
claude_live() {
  STUB_KIND=cc
  [ -z "$CC_STUB_TRANSCRIPT" ] && return 0
  STUB_TRANSCRIPT="$CC_STUB_TRANSCRIPT"
  mkdir -p "$(dirname "$STUB_TRANSCRIPT")" "$PFM_SID_DIR"
  : >> "$STUB_TRANSCRIPT"
  printf '%s' "$STUB_TRANSCRIPT" > "$PFM_SID_DIR/$(_sock)"
  _fakeproc claude
  return 0
}
`

// stubCodex is a Codex TUI reduced to the four states pfm's rename
// choreography navigates, driven over a REAL tty inside a REAL tmux server:
// canonical mode is turned off so a partial "/rename" is seen before Enter
// (exactly how the engine's own slash-command popup behaves), and every state
// repaints the screen so a marker that should be gone really leaves the
// capture.
const stubCodex = `#!/usr/bin/env bash
printf '%s\n' "$*" > "${CX_STUB_ARGV:-/dev/null}"
stty -icanon -echo -ixon min 1 time 0 2>/dev/null
` + stubRecorder + `
codex_live
stage=composer; buf=""; name=""
status='  019f · ~/work · Full Access · Context 0% used · 0 in · 0 out
'
modals="${CX_STUB_MODALS:-1}"
render() {
  printf '\033[2J\033[H'
  if [ "$modals" -gt 0 ]; then
    # The selection cursor is the same glyph the composer draws, and the
    # status line is gone: the exact screen that fooled the first fix.
    printf 'codex\n  Hooks\n  1 hook needs review before it can run.\n'
    printf '\u203a 2. Trust all and continue\n'
    printf '  Press enter to confirm or esc to go back\n'
    return
  fi
  case "$stage" in
    offered) printf 'codex\n› %s\n  /rename  rename the current thread\n%s' "$buf" "$status" ;;
    prompt)  printf 'codex\n| Name thread\n| Type a name and press Enter\n' ;;
    *)       if [ -n "$name" ]; then printf '* Session renamed to %s.\n' "$name"; fi
             printf 'codex\n› %s\n%s' "$buf" "$status" ;;
  esac
}
render
# -d '' -n1, never -N1: the darwin CI runner's /bin/bash is 3.2, whose read has no -N.
while IFS= read -r -d '' -n1 ch; do
  if [ "$modals" -gt 0 ]; then
    # A startup overlay: ONLY Escape gets out of it, everything else vanishes
    # into it exactly as the real hooks/trust modals swallow keystrokes.
    if [ "$ch" = $'\033' ]; then
      modals=$((modals - 1))
      # CX_STUB_SLOW_DISMISS: an engine starved of CPU repaints late after
      # the Escape that clears its last overlay, exactly as one does while a
      # whole gate runs beside it.
      if [ "$modals" -eq 0 ] && [ -n "$CX_STUB_SLOW_DISMISS" ]; then sleep "$CX_STUB_SLOW_DISMISS"; fi
    fi
    render
    continue
  fi
  case "$ch" in
    $'\n'|$'\r')
      case "$stage" in
        offered) stage=prompt; buf="" ;;
        prompt)  name="$buf"; printf '%s' "$buf" > "$CX_STUB_NAME"; stage=composer; buf="" ;;
        *)       if [ -n "$buf" ]; then
                   printf '%s\n' "$buf" >> "$CX_STUB_PROMPT"
                   turn "$buf"
                 fi
                 buf="" ;;
      esac ;;
    $'\023')
      : ;;
    $'\033')
      # Codex takes Escape as a key, never as text: a spare dismissal Escape
      # that lands on the composer leaves what is typed there untouched.
      : ;;
    $'\177'|$'\b')
      buf="${buf%?}" ;;
    *)
      buf="$buf$ch"
      if [ "$stage" = composer ] && [ "$buf" = "/rename" ] &&
         [ -z "$CX_STUB_NO_RENAME" ]; then stage=offered; fi ;;
  esac
  render
done
`

// stubClaude records the argv it was launched with, answers the prompt that
// travelled on it, and then behaves like a composer: Claude's name and first
// prompt need no keystrokes, but every LATER message does, and a chat that
// cannot be spoken to a second time is not a conversation.
//
// CC_STUB_DEAF models the failure this whole verification exists for: the
// launch prompt arrives on the command line and is never recorded, exactly as
// a startup dialog eating it would look from the outside.
//
// CC_STUB_OVERLAY models the recoverable shape of that same failure: the
// prompt is held unsent behind a startup overlay, and an Escape followed by
// an Enter submits it. Deaf is unrecoverable, overlay is what a retry saves.
//
// CC_STUB_TRUST models Claude Code's folder-trust dialog, whose default row is
// "No, exit": "now" draws it at boot, "late" draws a composer first and the
// dialog a second later. It logs one line to CC_STUB_KEYS for every key it is
// sent, so a test can prove pfm pressed none.
const stubClaude = `#!/usr/bin/env bash
printf '%s\n' "$*" > "$CC_STUB_ARGV"
stty -icanon -echo -ixon min 1 time 0 2>/dev/null
` + stubRecorder + `
claude_live
if [ -n "$CC_STUB_TRUST" ]; then
  if [ "$CC_STUB_TRUST" = late ]; then printf 'claude ready\n❯ \n'; sleep 1; fi
  printf '\033[2J\033[H Accessing workspace:\n\n %s\n\n' "$PWD"
  printf ' Quick safety check: Is this a project you created or one you trust?\n\n'
  printf ' ❯ 1. Yes, I trust this folder\n   2. No, exit\n\n Enter to confirm · Esc to cancel\n'
  while IFS= read -r -d '' -n1 ch; do printf 'key\n' >> "$CC_STUB_KEYS"; done
  exit 0
fi
prompt=""
skip=0
for argument in "$@"; do
  if [ "$skip" = 1 ]; then skip=0; continue; fi
  case "$argument" in
    --name|--model|--effort|--session-id|--settings|--mcp-config|--system-prompt-file) skip=1 ;;
    -*) ;;
    *) prompt="$argument"; break ;;
  esac
done
buf=""; note=""
render() {
  printf '\033[2J\033[H'; printf 'claude ready\n'
  [ -n "$note" ] && printf '%s\n' "$note"
  printf '❯ %s\n' "$buf"
}
if [ -n "$prompt" ] && [ -z "$CC_STUB_DEAF" ] && [ -z "$CC_STUB_OVERLAY" ]; then turn "$prompt"; fi
pending=""; dismissed=0
if [ -n "$CC_STUB_OVERLAY" ]; then pending="$prompt"; fi
render
# -d '' -n1, never -N1: the darwin CI runner's /bin/bash is 3.2, whose read has no -N.
while IFS= read -r -d '' -n1 ch; do
  case "$ch" in
    $'\033') dismissed=1 ;;
    $'\n'|$'\r')
      if [ -n "$buf" ]; then turn "$buf"
      elif [ "$dismissed" = 1 ] && [ -n "$pending" ]; then turn "$pending"; pending=""; fi
      buf=""; note="" ;;
    $'\023') buf=""; note="  draft stashed" ;;
    $'\177'|$'\b') buf="${buf%?}" ;;
    *) buf="$buf$ch" ;;
  esac
  render
done
`

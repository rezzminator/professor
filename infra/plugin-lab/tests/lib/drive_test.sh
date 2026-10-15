#!/usr/bin/env bash
# lib/drive.sh: mark waits are bounded polls (D7); input bounds.
set -uo pipefail
# shellcheck source=../testlib.sh
source "$(dirname "$0")/../testlib.sh"
lab_lib run drive
LAB_RUN=$T/run; mkdir -p "$LAB_RUN"
m=$LAB_RUN/marks.jsonl
printf '{"event":"UserPromptSubmit"}\n{"event":"Stop"}\n{"event":"SubagentStop","agent_type":"lab-echo","agent_id":"a1"}\n' > "$m"
expect_eq 'mark count' "$(lab_mark_count)" 3
expect_eq 'finds a mark after a line' "$(lab_wait_mark Stop 1 1)" 2
expect_eq 'filter on fields' "$(lab_wait_mark SubagentStop 0 1 '.agent_type=="lab-echo"')" 3
start=$(date +%s)
lab_wait_mark Stop 2 1 >/dev/null; rc=$?
expect_eq 'absent mark times out' "$rc" 1
expect_eq 'bounded wait' "$(( $(date +%s) - start <= 3 ))" 1
long=$(printf 'y%.0s' $(seq 1 2001))
out=$(say "$long" 2>&1); rc=$?
expect_eq 'say refuses over 2000 characters' "$rc $out" '2 ERROR say-too-long 2001>2000'
lab_tmux() { [[ $* == *Enter* ]] && printf '{"event":"SessionStart","session_id":"s2","transcript_path":"/t2","source":"clear"}\n' >> "$m"; return 0; }
LAB_BOOTED=1 LAB_SID=s1 LAB_TRANSCRIPT=/t1
slash /clear SessionStart 2 >/dev/null; rc=$?
expect_eq 'slash /clear follows the new session' "$rc ${LAB_SID_PREV:-} $LAB_SID $LAB_TRANSCRIPT" '0 s1 s2 /t2'
slash /model UserPromptSubmit 1 >/dev/null
expect_eq 'a slash without a SessionStart keeps the session' "$LAB_SID $LAB_TRANSCRIPT" 's2 /t2'
# a slash without a mark event returns once the transcript records the command and its output (S7 live:
# the composer remounts after a local command, dropping a line typed before it)
tr=$T/slash.jsonl; : > "$tr"; LAB_TRANSCRIPT=$tr
lab_tmux() {
  [[ $* == *Enter* ]] && printf '%s\n' '{"type":"user","message":{"content":"<command-name>/model</command-name>\n<command-args>sonnet</command-args>"}}' \
    '{"type":"user","message":{"content":"<local-command-stdout>Set model to Sonnet</local-command-stdout>"}}' >> "$tr"
  return 0
}
slash '/model sonnet' >/dev/null; rc=$?
expect_eq 'slash waits for the command output' "$rc $LAB_EV" '0 /model: Set model to Sonnet'
lab_tmux() { return 0; }
LAB_INPUT_S=1 slash '/model sonnet' >/dev/null; rc=$?
expect_has 'a slash whose output never lands is not observed' "$rc $LAB_EV" '2 slash-not-observed /model sonnet'
# a confirmation the command opens (S7 live: "Switch model?" once the conversation is cached) is answered only
# when the scenario names the option and the cursor rests on it
: > "$tr"; rm -f "$T/dialog" "$T/sent"
lab_tmux() {
  case $* in
    *capture-pane*) [[ -e $T/dialog ]] && printf '   Switch model?\n   \xe2\x9d\xaf 1. Yes, switch to Sonnet 5.5\n     2. No, go back\n'; return 0 ;;
    *Enter*)
      if [[ -e $T/dialog ]]; then rm -f "$T/dialog"
        printf '%s\n' '{"type":"user","message":{"content":"<command-name>/model</command-name>"}}' \
          '{"type":"user","message":{"content":"<local-command-stdout>Set model to Sonnet</local-command-stdout>"}}' >> "$tr"
      elif [[ ! -e $T/sent ]]; then : > "$T/sent"; : > "$T/dialog"; fi ;;
  esac
  return 0
}
LAB_INPUT_S=3 slash '/model sonnet' '' '' 'Yes, switch' >/dev/null; rc=$?
expect_eq 'slash answers the named confirmation' "$rc $LAB_EV" "0 /model: Set model to Sonnet (confirmed 'Yes, switch')"
: > "$tr"; rm -f "$T/dialog" "$T/sent"
LAB_INPUT_S=2 slash '/model sonnet' >/dev/null; rc=$?
expect_has 'an unnamed confirmation is never answered' "$rc $LAB_EV" '2 slash-not-observed /model sonnet'
# a line typed while a turn runs is delivered into it as a queued_command attachment, never a user entry (S10 live)
tq=$T/queued.jsonl
printf '%s\n' '{"type":"queue-operation","operation":"enqueue","content":"Use the Task tool. Read it."}' \
  '{"type":"attachment","attachment":{"type":"queued_command","commandMode":"prompt","prompt":"Use the Task tool. Read it."}}' \
  '{"type":"system","subtype":"stop_hook_summary"}' > "$tq"
expect_eq 'a queued line delivered into a turn is its own turn' "$(LAB_TRANSCRIPT=$tq lab_turn_probe 'Use the Task tool. Read it.' 0)" '1 true'
printf '%s\n' '{"type":"attachment","attachment":{"type":"queued_command","commandMode":"task-notification","prompt":"Use the Task tool. Read it."}}' > "$tq"
expect_eq 'a task notification is no typed line' "$(LAB_TRANSCRIPT=$tq lab_turn_probe 'Use the Task tool. Read it.' 0)" '0 false'
finish

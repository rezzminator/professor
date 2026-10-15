# shellcheck shell=bash
# lib/drive.sh: the session through a private tmux socket (D6, D7). Success is a
# mark from the lab hook, waited for by a bounded poll; the pane is saved only as
# failure evidence. Verbs return 0 PASS · 1 FAIL · 2 ERROR · 5 STOPPED and set LAB_EV.

lab_tmux() { env -u TMUX SHELL=/bin/sh tmux -u -S "$LAB_RUN/t.sock" -f /dev/null "$@"; }

lab_mark_count() { if [[ -f $LAB_RUN/marks.jsonl ]]; then wc -l < "$LAB_RUN/marks.jsonl" | tr -d ' '; else echo 0; fi; }

lab_mark_scan() { # <event> <after> <filter>: line number of the first match after line <after>
  [[ -f $LAB_RUN/marks.jsonl ]] || return 0
  jq -nR --arg e "$1" --argjson a "$2" "first(foreach inputs as \$l (0; . + 1; . as \$n
    | (\$l | fromjson? // null) | select(. != null and \$n > \$a and .event == \$e) | select($3) | \$n))" \
    "$LAB_RUN/marks.jsonl" 2>/dev/null
}

lab_wait_mark() { # <event> <after line> <timeout s> [jq filter]: prints and sets LAB_MARK_LINE; rc 1 timeout, 4 session gone
  local i ticks=$(( $3 * 10 )) hit
  LAB_MARK_LINE=''
  for (( i = 0; ; i++ )); do
    hit=$(lab_mark_scan "$1" "$2" "${4:-true}")
    if [[ -n $hit ]]; then LAB_MARK_LINE=$hit; printf '%s\n' "$hit"; return 0; fi
    (( i >= ticks )) && return 1
    if (( i > 0 && i % 200 == 0 )) && [[ -n ${LAB_TICK:-} ]]; then "$LAB_TICK" || return $?; fi
    if (( i % 10 == 0 )) && [[ -n ${LAB_PID:-} ]] && ! kill -0 "$LAB_PID" 2>/dev/null; then
      hit=$(lab_mark_scan "$1" "$2" "${4:-true}")
      if [[ -n $hit ]]; then LAB_MARK_LINE=$hit; printf '%s\n' "$hit"; return 0; fi
      return 4
    fi
    sleep 0.1
  done
}

lab_mark_field() { sed -n "${1}p" "$LAB_RUN/marks.jsonl" | jq -r "$2 // empty"; } # <line> <jq path>

lab_pane_save() { # <name>: the pane bytes as failure evidence; prints the path; rc is the capture's
  local f=$LAB_RUN/pane/$1.txt rc
  mkdir -p "$LAB_RUN/pane"
  lab_tmux capture-pane -p -t lab -S -300 > "$f" 2>&1; rc=$?
  printf '%s\n' "$f"
  return "$rc"
}

lab_after_wait() { # the meter after every mark wait
  [[ -n ${LAB_TICK:-} ]] || return 0
  "$LAB_TICK"
}

lab_wait_fail() { # <rc of lab_wait_mark> <reason>: maps a failed wait onto verdict rc and evidence
  case $1 in
    1) LAB_EV="$2 $(lab_pane_save "${LAB_BEAT:-wait}")"; return 2 ;;
    4) LAB_EV="session-gone $(lab_pane_save "${LAB_BEAT:-wait}")" LAB_FATAL=1; return 2 ;;
    *) return "$1" ;;
  esac
}

lab_boot() { # lazy: the first drive verb boots the session
  local rc
  [[ -n ${LAB_BOOTED:-} ]] && return 0
  LAB_BOOTED=1 LAB_FATAL=1
  mkdir -p "$LAB_RUN/sec" && chmod 700 "$LAB_RUN/sec"
  lab_auth_handoff "$LAB_RUN/sec/oauth.fifo" 2>/dev/null || { LAB_EV=auth-handoff; return 2; }
  if ! lab_tmux new-session -d -s lab -x 220 -y 60 "exec /bin/bash $(printf %q "$LAB_RUN/launch.sh")" \
      \; set-option -t lab remain-on-exit on > "$LAB_RUN/tmux.err" 2>&1; then
    LAB_EV="tmux $(tr '\n' ' ' < "$LAB_RUN/tmux.err")"
    return 2
  fi
  if ! lab_auth_handoff_wait 2>/dev/null; then
    rm -f "$LAB_RUN/sec/oauth.fifo"
    LAB_EV="auth-handoff launcher never opened fd 3 within ${LAB_HANDOFF_S:-30}s"
    return 2
  fi
  LAB_PID=$(lab_tmux display-message -p -t lab '#{pane_pid}' 2>/dev/null)
  printf '%s\n' "$LAB_PID" > "$LAB_RUN/claude.pid"
  lab_wait_mark SessionStart 0 "${LAB_BOOT_S:-60}" >/dev/null; rc=$?
  if (( rc != 0 )); then
    LAB_EV="startup-blocked no SessionStart mark within ${LAB_BOOT_S:-60}s $(lab_pane_save boot)"
    return 2
  fi
  LAB_SID=$(lab_mark_field "$LAB_MARK_LINE" .session_id)
  LAB_TRANSCRIPT=$(lab_mark_field "$LAB_MARK_LINE" .transcript_path)
  lab_env_observe > "$LAB_RUN/env-keys" 2> "$LAB_RUN/env-keys.err"
  LAB_FATAL=''
}

say() { # <text>: types one line; returns once its UserPromptSubmit mark arrived
  local before first rc bound=${LAB_INPUT_S:-20}
  (( ${#1} <= 2000 )) || { printf 'ERROR say-too-long %d>2000\n' "${#1}" >&2; LAB_EV="say-too-long ${#1}>2000"; return 2; }
  lab_boot || return 2
  before=$(lab_mark_count)
  LAB_SAY_TEXT=$1 LAB_SAY_SEEN=$(lab_turn_probe "$1" 0); LAB_SAY_SEEN=${LAB_SAY_SEEN%% *}
  lab_tmux send-keys -t lab -l -- "$1" && lab_tmux send-keys -t lab Enter || { LAB_EV='tmux send-keys failed' LAB_FATAL=1; return 2; }
  LAB_IN_TURN=1
  first=$(( bound < 5 ? bound : 5 ))
  lab_wait_mark UserPromptSubmit "$before" "$first" >/dev/null; rc=$?
  if (( rc == 1 )); then # a line neither the transcript nor the pane holds was dropped (the composer remounts
    # after a local command): typed once more; one still showing is only waiting for its Enter
    if [[ $(lab_turn_probe "$1" 0) == "$LAB_SAY_SEEN "* ]] && ! lab_tmux capture-pane -p -t lab 2>/dev/null | grep -qF -- "${1:0:40}"; then
      lab_tmux send-keys -t lab -l -- "$1"
    fi
    lab_tmux send-keys -t lab Enter
    lab_wait_mark UserPromptSubmit "$before" "$(( bound - first ))" >/dev/null; rc=$?
  fi
  (( rc == 0 )) || { lab_wait_fail "$rc" input-not-accepted; return $?; }
  LAB_SAY_MARK=$LAB_MARK_LINE
  lab_after_wait
}

lab_turn_probe() { # <text> <seen>: "{count} {ended}": the transcript's entries delivering the text (a user entry, or the
  # queued_command prompt attachment a line typed during a running turn becomes), and whether a stop-hook summary
  # follows the one after the first <seen>; an unreadable transcript is "0 false"
  local out
  out=$(jq -nrR --arg t "$1" --argjson s "$2" '[inputs | fromjson? // empty] as $e
    | [range(0; $e | length) | select(($e[.].type == "user" and ($e[.].message.content
        | if type == "string" then . else [.[]? | objects | select(.type == "text") | .text] | join("\n") end | contains($t)))
      or ($e[.].type == "attachment" and $e[.].attachment.type == "queued_command" and $e[.].attachment.commandMode == "prompt"
        and ($e[.].attachment.prompt | tostring | contains($t))))] as $u
    | "\($u | length) \(if ($u | length) > $s then any($e[($u[$s] + 1):][]; .type == "system" and .subtype == "stop_hook_summary") else false end)"' \
    "${LAB_TRANSCRIPT:-/dev/null}" 2>/dev/null) || out=''
  printf '%s\n' "${out:-0 false}"
}

wait_turn() { # [timeout s]: the Stop mark ending the last say's own turn: a stop-hook summary follows its prompt;
  # a Stop that ends another turn (a background agent's notification, the line queued behind it) is passed over
  local rc t=${1:-300} after=${LAB_SAY_MARK:-0} end i
  end=$(( SECONDS + t ))
  while :; do
    lab_wait_mark Stop "$after" "$(( end > SECONDS ? end - SECONDS : 1 ))" >/dev/null; rc=$?
    (( rc == 0 )) || { lab_wait_fail "$rc" "turn-timeout ${t}s"; return $?; }
    after=$LAB_MARK_LINE
    for (( i = 0; i < ${LAB_SUMMARY_TICKS:-10}; i++ )); do # the summary lands just after the Stop hooks
      [[ $(lab_turn_probe "${LAB_SAY_TEXT:-}" "${LAB_SAY_SEEN:-0}") == *' true' ]] && break 2
      sleep 0.5
    done
  done
  LAB_IN_TURN='' LAB_TURN_MARK=$after
  LAB_EV="Stop mark $after"
  lab_after_wait
}

turn() { say "$1" && wait_turn "${2:-300}"; } # <text> [timeout s]

lab_slash_probe() { # <command name> <seen>: "{count}\t{landed}\t{output}": the transcript's entries naming the
  # command, and the local-command output after the one past the first <seen>; an unreadable transcript is "0\tfalse\t"
  local out
  out=$(jq -nrR --arg c "<command-name>$1</command-name>" --argjson s "$2" '[inputs | fromjson? // empty | select(.type == "user")
      | .message.content | if type == "string" then . else [.[]? | objects | select(.type == "text") | .text] | join("\n") end] as $u
    | [range(0; $u | length) | select($u[.] | contains($c))] as $i
    | (if ($i | length) > $s then [$u[($i[$s] + 1):][] | select(test("^<local-command-std(out|err)>"))] else [] end) as $o
    | "\($i | length)\t\($o | length > 0)\t\($o | first // "" | gsub("</?local-command-std(out|err)>"; "") | gsub("\n"; " "))"' \
    "${LAB_TRANSCRIPT:-/dev/null}" 2>/dev/null) || out=''
  printf '%s\n' "${out:-$(printf '0\tfalse\t')}"
}

slash() { # <command line> [mark event] [timeout s] [confirm option]: a slash command; with an event, waits for its
  # mark; a SessionStart mark (/clear) moves the run onto the new session, the old id kept in LAB_SID_PREV; without
  # one, waits for the transcript to record the command's output (a local command such as /model), answering once a
  # confirmation whose selected line holds the confirm option (Claude Code asks before /model drops a warm cache)
  local before rc name=${1%% *} seen probe end confirmed=''
  lab_boot || return 2
  before=$(lab_mark_count)
  seen=$(lab_slash_probe "$name" 0); seen=${seen%%$'\t'*}
  lab_tmux send-keys -t lab -l -- "$1" && lab_tmux send-keys -t lab Enter || { LAB_EV='tmux send-keys failed' LAB_FATAL=1; return 2; }
  LAB_SLASH_MARK=$before
  if [[ -z ${2:-} ]]; then
    end=$(( SECONDS + ${3:-${LAB_INPUT_S:-20}} ))
    until probe=$(lab_slash_probe "$name" "$seen"); [[ $probe == *$'\t'true$'\t'* ]]; do
      (( SECONDS < end )) || { lab_wait_fail 1 "slash-not-observed $1 no command output in the transcript"; return $?; }
      if [[ -n ${4:-} && -z $confirmed ]] && lab_tmux capture-pane -p -t lab 2>/dev/null | grep -F -- "$4" | grep -qF '❯'; then
        lab_tmux send-keys -t lab Enter
        confirmed=" (confirmed '$4')"
      fi
      sleep 0.5
    done
    probe=${probe#*$'\t'true$'\t'}
    LAB_EV="$name: ${probe:0:120}$confirmed"
    lab_after_wait
    return $?
  fi
  lab_wait_mark "$2" "$before" "${3:-60}" >/dev/null; rc=$?
  (( rc == 0 )) || { lab_wait_fail "$rc" "slash-not-observed $1 no $2 mark"; return $?; }
  LAB_EV="$2 mark $LAB_MARK_LINE"
  if [[ $2 == SessionStart ]]; then
    LAB_SID_PREV=${LAB_SID:-}
    LAB_SID=$(lab_mark_field "$LAB_MARK_LINE" .session_id)
    LAB_TRANSCRIPT=$(lab_mark_field "$LAB_MARK_LINE" .transcript_path)
    LAB_EV+=" session $LAB_SID_PREV -> $LAB_SID"
  fi
  lab_after_wait
}

wait_subagent() { # <agent type> [timeout s]: the SubagentStop mark of that type after the last say
  local rc
  lab_wait_mark SubagentStop "${LAB_SAY_MARK:-0}" "${2:-600}" ".agent_type == \"$1\"" >/dev/null; rc=$?
  (( rc == 0 )) || { lab_wait_fail "$rc" "subagent-timeout $1"; return $?; }
  LAB_AGENT_ID=$(lab_mark_field "$LAB_MARK_LINE" .agent_id)
  LAB_AGENT_TRANSCRIPT=$(lab_mark_field "$LAB_MARK_LINE" .agent_transcript_path)
  LAB_EV="SubagentStop $1 $LAB_AGENT_ID"
  lab_after_wait
}

exit_session() { # /exit, then the SessionEnd mark (bounded)
  local before rc
  [[ -n ${LAB_BOOTED:-} && -z ${LAB_TORN:-} ]] || { LAB_EV='no session'; return 2; }
  before=$(lab_mark_count)
  lab_tmux send-keys -t lab -l -- /exit && lab_tmux send-keys -t lab Enter
  lab_wait_mark SessionEnd "$before" "${LAB_EXIT_S:-15}" >/dev/null; rc=$?
  (( rc == 0 )) || { lab_wait_fail "$rc" exit-not-observed; return $?; }
  LAB_EV="SessionEnd mark $LAB_MARK_LINE"
}

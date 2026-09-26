#!/usr/bin/env bash
# verify.sh — runs INSIDE the demo fence container after fleet.sh: the deck,
# checked the way it was checked by hand before the first live run — every beat
# exercised for real (chats spawned, messages injected, a storm blown, headless
# calls made) and judged from what pfm and the transcripts report, never from a
# script's own exit code. This is the end-to-end gate the fence exists for:
# up.sh runs it last, and a green run means the container carries the whole deck.
#
#   verify.sh              every check, in order
#   verify.sh CHECK...     only the named checks:
#                          seats daemon fleet express (five beats) inject reload compact storm idle headless
#
# Cost: five throwaway chats (PING_CLAUDE, PING_CODEX, RELOAD_T, COMPACT_T,
# STORM_1..2) — a few short model turns; they are ended and hidden at the end.
# Runtime ≈ 6–8 min. The slide chats (idle.sh roster) are cycled by the idle
# check and come back on their own transcripts.
#
# BROKEN STATE: every check prints `verify: ✓ name — detail` or
# `verify: ✗ name — what was seen`; the run continues so one report carries every
# failure; the closing line is `verify: N checks · M failed` and the exit is 1
# when M > 0. A check that could not start (a spawn pfm refused) is a ✗ carrying
# pfm's own message, never a silent skip; an unknown check name is a ✗ too.
set -uo pipefail
export PATH="$HOME/.local/bin:$PATH"
export IS_SANDBOX=1 # root fence: Claude Code refuses the bypass flag under root without it (setup.sh)
cd /tmp || exit 1
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
CONFIG="$HOME/.config/pfm/pfm.config.json"
read -r -a LIVE <<<"$(cat "$HOME/.local/state/pfm/demo-seats-live" 2>/dev/null || jq -r '[.accounts[].id] | join(" ")' "$CONFIG")"
SEAT_A="${LIVE[0]:-}"; SEAT_B="${LIVE[1]:-$SEAT_A}"
checks=0 failed=0
pass() { checks=$((checks + 1)); printf 'verify: ✓ %s — %s\n' "$1" "$2"; }
fail() { checks=$((checks + 1)); failed=$((failed + 1)); printf 'verify: ✗ %s — %s\n' "$1" "$2" >&2; }
expand() { case "$1" in "~"*) echo "$HOME${1#\~}";; *) echo "$1";; esac; }
seat_dir() { expand "$(jq -r --argjson id "$1" '.accounts[] | select(.id == $id) | .configDir' "$CONFIG")"; }
live() { pfm ls --plain 2>/dev/null | grep -q "^● $1 "; }
# throwaway NAME ENGINE [ACCOUNT] — a chat that answers "ready" and waits for one
# instruction; on failure prints pfm's own last lines and returns its status.
throwaway() {
  local acct=() out rc
  [ -n "${3:-}" ] && acct=(--account "$3")
  out="$(pfm chat new --name "$1" --engine "$2" "${acct[@]}" --cwd /work/orbit --await --timeout 150 \
    "You are $1, a throwaway chat in an automated check of the pfm fleet. Reply with one word: ready. Then wait; do exactly what the next message says and nothing else." 2>&1 >/dev/null)"; rc=$?
  [ "$rc" -eq 0 ] || tail -3 <<<"$out"
  return "$rc"
}
# wait_last NAME NEEDLE SECS — polls the chat's last assistant message for NEEDLE;
# on timeout prints the last message seen (or <no answer>) and returns 1.
wait_last() {
  local deadline=$((SECONDS + $3)) last=""
  while [ "$SECONDS" -lt "$deadline" ]; do
    last="$(pfm chat last "$1" 2>/dev/null || true)"
    grep -qF "$2" <<<"$last" && return 0
    sleep 5
  done
  tail -3 <<<"${last:-<no answer>}"
  return 1
}
# retire NAME... — ends the throwaways and hides their ↻ rows; never a verdict
retire() {
  local n id
  for n in "$@"; do pfm chat end "$n" >/dev/null 2>&1 || true; done
  sleep 2
  for n in "$@"; do
    for id in $(pfm ls --tsv 2>/dev/null | awk -F'\t' -v n="$n" '$1 ~ /^resume-/ && $5 == n {print $2}'); do
      pfm chat kill "$id" >/dev/null 2>&1 || true
    done
  done
}

check_seats() { # every answering seat: /reload linked, professor theme, fullscreen, no theme override
  local id dir theme theme_rc reason tui bad=""
  [ -n "$SEAT_A" ] || { fail seats "no Claude seat recorded (demo-seats-live) or configured"; return; }
  for id in "${LIVE[@]}"; do
    dir="$(seat_dir "$id")"
    [ -e "$dir/commands/reload.md" ] || bad+=" seat $id: commands/reload.md missing (pfm install did not wire it);"
    if theme="$(jq -r '.theme // ""' "$dir/settings.json" 2>&1)"; then
      [[ "$theme" == custom:professor-* ]] || bad+=" seat $id: theme=${theme:-<none>};"
    else
      theme_rc=$?; reason="$(printf '%s' "$theme" | tr '\n' ' ')"
      bad+=" seat $id: theme=UNREADABLE(${reason:-jq exit $theme_rc with no error text});"
    fi
    tui="$(jq -r '.tui // ""' "$dir/settings.json" 2>/dev/null)"; [ "$tui" = fullscreen ] || bad+=" seat $id: tui=${tui:-<none>};"
    [ "$(jq -r 'has("theme")' "$dir/.claude.json" 2>/dev/null)" = false ] || bad+=" seat $id: .claude.json carries a theme key (overrides settings.json);"
  done
  [ -z "$bad" ] && pass seats "seat(s) ${LIVE[*]}: /reload linked, professor theme, fullscreen TUI" || fail seats "$bad"
}
check_daemon() { # the professor MCP daemon every stdio forwarder targets
  local out
  if out="$("$HERE/daemon.sh" 2>&1)"; then pass daemon "$(tail -1 <<<"$out")"; else fail daemon "$(tail -3 <<<"$out" | tr '\n' ' ')"; fi
}
check_fleet() { # every slide chat ● live, every Claude chat on the professor system prompt
  local n missing="" prompts
  while read -r n; do live "$n" || missing+=" $n"; done < <("$HERE/idle.sh" roster)
  prompts="$(pgrep -fc -- '--system-prompt-file' || true)"
  if [ -n "$missing" ]; then fail fleet "not live:$missing (fleet.sh respawns them)"
  elif [ "${prompts:-0}" -lt 1 ]; then fail fleet "every slide chat is live but no claude process carries --system-prompt-file (claude.systemPrompt not applied)"
  else pass fleet "$("$HERE/idle.sh" roster | wc -l | tr -d ' ') slide chats live · $prompts claude process(es) on the professor system prompt"; fi
}
check_express() { # five independently counted install-fidelity beats on the adopted repo
  local root=/work/express out rc doctor_bad summary json_error=/tmp/verify-express-update-jq.log json_rc
  local hook_list=/tmp/verify-express-hooks.tsv
  local event command target hook_bad="" hook_count=0

  if [ ! -d "$root/.git" ]; then
    fail express "$root is not a repository (adopt.sh never ran)"
  elif (cd "$root" && git log --oneline 2>/dev/null | grep -q 'professor: install'); then
    pass express "'professor: install' marker committed"
  else
    fail express "no 'professor: install' commit — the interview did not finish"
  fi

  out=/tmp/verify-express-doctor.log
  (cd "$root" && pfm doctor) >"$out" 2>&1; rc=$?
  doctor_bad="$(grep -m1 -E 'broken|drift|stale|error=' "$out" || true)"
  if [ "$rc" -ge 2 ]; then
    fail express-doctor "pfm doctor exit $rc; full output: $out; first failure: ${doctor_bad:-<none>}; tail: $(tail -3 "$out" | tr '\n' ' ')"
  elif [ -n "$doctor_bad" ]; then
    fail express-doctor "pfm doctor exit $rc with failure row: $doctor_bad; full output: $out"
  else
    summary="$(grep -m1 '^doctor: warnings=' "$out" || true)"
    [ -n "$summary" ] || summary="$(grep -m1 '^doctor: clean$' "$out" || true)"
    pass express-doctor "pfm doctor exit $rc · ${summary:-no summary line}"
  fi

  out=/tmp/verify-express-update.json
  (cd "$root" && pfm update check --json) >"$out" 2>&1; rc=$?
  if [ "$rc" -ne 0 ]; then
    fail express-update "pfm update check --json exit $rc; full output: $out; tail: $(tail -3 "$out" | tr '\n' ' ')"
  else
    jq -e '.counts.UPDATED == 0 and .counts.NEW == 0 and .counts["GONE-UPSTREAM"] == 0 and .counts["LOCAL-DELETED"] == 0 and .reviewRequired == 0 and .terminal == "clean"' \
      "$out" >/dev/null 2>"$json_error"; json_rc=$?
    if [ "$json_rc" -eq 0 ]; then
      pass express-update "zero UPDATED/NEW/GONE-UPSTREAM/LOCAL-DELETED · reviewRequired 0 · terminal clean"
    elif [ -s "$json_error" ]; then
      fail express-update "exit 0 but JSON validation failed (jq exit $json_rc): $(tr '\n' ' ' <"$json_error"); full command output: $out"
    else
      fail express-update "exit 0 but expected zero UPDATED/NEW/GONE-UPSTREAM/LOCAL-DELETED, reviewRequired 0, terminal clean; full output: $out; saw: $(tr '\n' ' ' <"$out")"
    fi
  fi

  out=/tmp/verify-express-codex.log
  (cd "$root" && pfm codex check .) >"$out" 2>&1; rc=$?
  if [ "$rc" -ne 0 ]; then
    fail express-codex "pfm codex check . exit $rc; full output: $out; tail: $(tail -3 "$out" | tr '\n' ' ')"
  elif ! grep -q '^CODEX CHECK PASS' "$out"; then
    fail express-codex "exit 0 without CODEX CHECK PASS; full output: $out; tail: $(tail -3 "$out" | tr '\n' ' ')"
  else
    pass express-codex "$(grep '^CODEX CHECK PASS' "$out" | tail -1)"
  fi

  out=/tmp/verify-express-hooks.log
  if ! jq -r '.hooks | to_entries[] | .key as $event | .value[] | .hooks[] | select(.type == "command") | [$event, (.command // "")] | @tsv' \
      "$root/.claude/settings.json" >"$hook_list" 2>"$out"; then
    fail express-hooks "could not enumerate command hooks from .claude/settings.json; full output: $out; saw: $(tr '\n' ' ' <"$out")"
  else
    : >"$out"
    while IFS=$'\t' read -r event command; do
      hook_count=$((hook_count + 1))
      if [[ ! "$command" =~ ^\$CLAUDE_PROJECT_DIR/([A-Za-z0-9._/-]+)([[:space:]].*)?$ ]]; then
        hook_bad+=" $event: malformed/unrooted command '$command';"
        continue
      fi
      target="${BASH_REMATCH[1]}"
      if [[ "/$target/" == *"/../"* || "/$target/" == *"/./"* || "$target" == /* || "$target" == */ ]]; then
        hook_bad+=" $event: malformed project-relative target '$target';"
      elif [ ! -e "$root/$target" ]; then
        hook_bad+=" $event: missing \$CLAUDE_PROJECT_DIR/$target;"
      else
        printf '%s\t%s\n' "$event" "$command" >>"$out"
      fi
    done <"$hook_list"
    if [ "$hook_count" -eq 0 ]; then
      fail express-hooks "zero command hooks found in .claude/settings.json (enumeration completed)"
    elif [ -n "$hook_bad" ]; then
      fail express-hooks "$hook_count command hook(s) enumerated;$hook_bad full validated-hook list: $out"
    else
      pass express-hooks "$hook_count command hook path(s) rooted at \$CLAUDE_PROJECT_DIR and present"
    fi
  fi
}
check_inject() { # slide 2: a Claude chat messages a Codex chat and reads the answer back — signed both ways
  local out
  out="$(throwaway PING_CLAUDE cc "$SEAT_A")" || { fail inject "PING_CLAUDE never came up: $out"; return; }
  out="$(throwaway PING_CODEX cx)" || { fail inject "PING_CODEX never came up: $out"; return; }
  out="$(pfm chat inject --allow-unsigned PING_CLAUDE "Use the chat_inject tool to send PING_CODEX this line: 'ping from PING_CLAUDE — reply to me with chat_inject: pong'. When PING_CODEX's pong reaches you, answer with exactly the word PONG-OK and nothing else." 2>&1)" \
    || { fail inject "pfm chat inject PING_CLAUDE refused: $(tail -2 <<<"$out" | tr '\n' ' ')"; return; }
  if out="$(wait_last PING_CLAUDE PONG-OK 180)"; then pass inject "PING_CLAUDE → PING_CODEX → PING_CLAUDE round trip"
  else fail inject "no PONG-OK from PING_CLAUDE in 180 s; its last: $(tr '\n' ' ' <<<"$out")"; fi
}
check_reload() { # slide 8: /reload onto another seat, same conversation, then the steer
  local out
  out="$(throwaway RELOAD_T cc "$SEAT_A")" || { fail reload "RELOAD_T never came up: $out"; return; }
  out="$(pfm chat inject --allow-unsigned RELOAD_T "/reload --account $SEAT_B --model sonnet --then \"reply with exactly one word: RELOADED\"" 2>&1)" \
    || { fail reload "pfm chat inject RELOAD_T refused: $(tail -2 <<<"$out" | tr '\n' ' ')"; return; }
  if out="$(wait_last RELOAD_T RELOADED 180)"; then pass reload "RELOAD_T rebooted in place onto seat $SEAT_B and ran its --then steer"
  else fail reload "no RELOADED from RELOAD_T in 180 s; its last: $(tr '\n' ' ' <<<"$out")"; fi
}
check_compact() { # slide 9: chat_self_compact fires at turn end and the steer lands
  local out
  out="$(throwaway COMPACT_T cc "$SEAT_A")" || { fail compact "COMPACT_T never came up: $out"; return; }
  out="$(pfm chat inject --allow-unsigned COMPACT_T "Call the chat_self_compact tool now with focus 'verify' and the steer 'reply with exactly one word: COMPACTED'. Do nothing else." 2>&1)" \
    || { fail compact "pfm chat inject COMPACT_T refused: $(tail -2 <<<"$out" | tr '\n' ' ')"; return; }
  if out="$(wait_last COMPACT_T COMPACTED 180)"; then pass compact "COMPACT_T compacted itself and ran the steer"
  else fail compact "no COMPACTED from COMPACT_T in 180 s; its last: $(tr '\n' ' ' <<<"$out")"; fi
}
check_storm() { # slide 4: two storm chats exchange signed injects, then kill-storm leaves the rest untouched
  local out deadline last
  out="$("$HERE/storm.sh" start 2 2 2>&1)" || { fail storm "storm.sh start 2 2: $(tail -3 <<<"$out" | tr '\n' ' ')"; return; }
  # STORM_1's first answer is the literal 'ready'; the seed (signed as STORM_2)
  # makes it reply — its last message moving off 'ready' is the first real edge.
  deadline=$((SECONDS + 150)); last=""
  while [ "$SECONDS" -lt "$deadline" ]; do
    last="$(pfm chat last STORM_1 2>/dev/null | tr -d '[:space:]' | tr '[:upper:]' '[:lower:]')"
    [ -n "$last" ] && [ "$last" != ready ] && break
    sleep 5
  done
  if [ -z "$last" ] || [ "$last" = ready ]; then
    fail storm "STORM_1 never answered the seed in 150 s (last: ${last:-<none>})"; "$HERE/kill-storm.sh" >/dev/null 2>&1; return
  fi
  if out="$("$HERE/kill-storm.sh" 2>&1)"; then pass storm "storm answered the seed · $(tail -1 <<<"$out")"
  else fail storm "$(tail -2 <<<"$out" | tr '\n' ' ')"; fi
}
check_idle() { # slides 4→5: the slide chats off the sky and back on their own transcripts
  local down up
  if ! down="$("$HERE/idle.sh" down 2>&1)"; then fail idle "idle-down: $(tail -2 <<<"$down" | tr '\n' ' ')"; "$HERE/idle.sh" up >/dev/null 2>&1; return; fi
  if up="$("$HERE/idle.sh" up 2>&1)"; then pass idle "$(tail -1 <<<"$down") · $(tail -1 <<<"$up")"
  else fail idle "idle-up: $(tail -2 <<<"$up" | tr '\n' ' ')"; fi
}
check_headless() { # slide 11: the two printed commands, run as the presenter runs them, both return the schema
  local out n
  # shellcheck source=aliases.zsh
  source "$HERE/aliases.zsh"
  out="$(CLAUDE_CONFIG_DIR="$(seat_dir "$SEAT_A")" bash -c "$(headless)" 2>&1)"
  n="$(grep -c '"vendor"' <<<"$out" || true)"
  if [ "$n" -eq 2 ]; then pass headless "claude -p and pfm headless exec both returned the invoice schema"
  else fail headless "expected 2 structured answers, saw $n — $(grep -v '^\s*$' <<<"$out" | tail -3 | tr '\n' ' ')"; fi
}

all="seats daemon fleet express inject reload compact storm idle headless"
[ $# -gt 0 ] || set -- $all
for c in "$@"; do
  if declare -F "check_$c" >/dev/null; then "check_$c"; else fail "$c" "unknown check (one of: $all)"; fi
done
retire PING_CLAUDE PING_CODEX RELOAD_T COMPACT_T
echo "verify: $checks checks · $failed failed"
[ "$failed" -eq 0 ]

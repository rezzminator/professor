#!/usr/bin/env bash
# F.sh — lane F, fleet: `chat new` across every dimension beats.md lists,
# a storm beside E1's chat, `pfm ls` in every shape, the TUI picker driven key
# by key inside its own tmux server and read back with capture-pane, the idle
# state machine, name-sync convergence, kill-storm, and the sequence's first
# cross-lane effect (E1's chat after the storm). Runs INSIDE a lane container
# (run.sh), never on a host.
#
#   run.sh --lanes F             solo, from a fresh root (the prelude opens E1_MAIN itself)
#   run.sh                       in the sequence, after E1/E2/E3
#
# Every beat asserts from pfm's OWN report (`pfm ls --tsv`/`-a`/`-K`, a verb's
# exit code and output, the tmux pane start command pfm synthesized, a file pfm
# wrote) or from a tmux pane (`capture-pane`), never from a model's prose: a
# model turn is only ever the stimulus that gives a wait its needle. Beat ids
# are the contract in beats.md and map.tsv —
# check-map.sh fails when this file and those disagree.
#
# Cost: one Claude seat (`--seats cc:1`, seat 1 by default) plus the Codex home.
# Spawns: F_CC, F_CX (cx), F_GRP:lane, a 4-chat storm
# (cc+cx round-robin, 2 sends each), three parallel F_PAR_<n>, and E1_MAIN when
# the sequence did not leave it alive. Roughly 20 short turns; the TUI beats
# spend no model turn at all.
#
# BROKEN STATE: the prelude aborts by name when a seat, daemon, or E1_MAIN is
# missing; a beat whose chat dies is blocked by the beat that last saw it live.
# Every failed row kind or idle state names pfm's report, with raw pane bytes
# beside the assertion in the lane log.
set -uo pipefail
LANES_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=lib.sh
. "$LANES_DIR/lib.sh"
lane_preamble

CC="${F_CC_CHAT:-F_CC}"
CX="${F_CX_CHAT:-F_CX}"
GRP="F_GRP:lane" # the {name}:{group} label grammar, exercised as a chat name
E1_CHAT="${E1_CHAT:-E1_MAIN}"
CWD="${F_CWD:-/work/orbit}"
CONFIG="${PFM_CONFIG:?PFM_CONFIG is required in the container}"
SID_DIR="${PFM_SID_DIR:-${TMPDIR:-/tmp}/cc-sid}"
GOLDEN=/worktree/pfm/testdata/golden
THEME_SRC=/worktree/pfm/internal/theme/theme.go
OC_DB="$HOME/.local/share/opencode/opencode.db"
TUI_SOCK="${TMPDIR:-/tmp}/f-lane-tui.sock"
lane_seat_and_port "$CONFIG"

lane_begin F

# ── prelude: what this lane needs, made when it is missing, no-op otherwise ──
lane_require_seat "$CONFIG"
ACCOUNT_IDS="$(jq -r '.accounts[].id' "$CONFIG" 2>/dev/null | tr '\n' ' ')"
# The seat's medal, from pfm's own resolved config (`config accounts=1:<dir>:🥇 (default),…`).
MEDAL="$(pfm config show 2>/dev/null | sed -n 's/^config accounts=//p' | tr ',' '\n' |
  awk -F: -v s="$SEAT" '$1 == s { print $3 }' | awk '{ print $1 }')"
# The Codex home pfm's container config resolves.
CX_HOME="$(jq -r '.codex.homes[0].home // empty' "$CONFIG" 2>/dev/null)"
case "$CX_HOME" in "~"*) CX_HOME="$HOME${CX_HOME#\~}" ;; esac
CX_WHY="no .codex.homes in $CONFIG — the lane root was not built by lanes/root.sh"

for project in orbit atlas lumen; do
  need "the working directory /work/$project" "[ -d '/work/$project/.git' ]" \
    "mkdir -p '/work/$project' && git -C '/work/$project' init -q && git -C '/work/$project' commit -q --allow-empty -m lane" ||
    lane_abort "no working directory for the chats to live in (/work/$project)"
done
need "the pfm MCP daemon on :$PORT" \
  "[ \"\$(curl -s -o /dev/null -w '%{http_code}' -m 2 http://127.0.0.1:$PORT/mcp/professor)\" != 000 ]" \
  'lane_daemon_up' ||
  lane_abort "the professor MCP daemon never answered on :$PORT — no Codex chat can start and no chat can call a chat_* tool"

# open_e1_main — E1's own `chat new` line (E1.sh open_main): in the sequence
# E1.25 ended E1_MAIN, solo there never was one; either way the cross-lane beat
# F.18 needs E1's chat alive BEFORE the storm, so the prelude makes it.
open_e1_main() {
  pfm chat new --name "$E1_CHAT" --engine cc --account "$SEAT" --cwd "$CWD" --await --timeout 300 \
    "You are $E1_CHAT, the chat an automated Tier B lane drives. Reply with one word: ready. Then wait and do exactly what each next message says, nothing more." 2>&1
}
need "E1's chat $E1_CHAT (cross-lane state for F.18)" "live_chat '$E1_CHAT'" 'open_e1_main' ||
  lane_abort "E1's chat $E1_CHAT could not be made — F.18 would have nothing to assert against"

# ─── helpers: rows in every view, the picker in its own tmux server ─────────

# all_rows_named / all_field — the row(s) carrying a name in `pfm ls -a --tsv`:
# the default view (lib.sh live_row/row_field) omits killed rows by design, so
# a kill is asserted from the view that still shows them.
all_rows_named() { pfm ls -a --tsv 2>/dev/null | awk -F'\t' -v n="$1" 'NR > 1 && $5 == n'; }
all_field() { all_rows_named "$1" | head -1 | awk -F'\t' -v c="$2" '{ print $c }'; }
kinds_in_fleet() { pfm ls -a --tsv 2>/dev/null | awk -F'\t' 'NR > 1 { print $1 }' | sort -u; }
sock_base() { basename "$(live_field "$1" 11)"; }
socket_path() { printf '%s/%s' "$(_lane_tmux_dir)" "$1"; }
pane_last_two() {
  tmux -S "$(socket_path "$1")" capture-pane -p 2>&1 |
    awk 'NF { previous = last; last = $0 } END { if (previous != "") print previous; if (last != "") print last }'
}
row_kind_socket() {
  pfm ls -a --tsv 2>/dev/null | awk -F'\t' -v kind="$1" -v sock="$2" 'NR > 1 && $1 == kind && $11 == sock { found = 1 } END { exit !found }'
}
row_kind_id() {
  pfm ls -a --tsv 2>/dev/null | awk -F'\t' -v kind="$1" -v id="$2" 'NR > 1 && $1 == kind && $2 == id { found = 1 } END { exit !found }'
}
quiet_scenario() {
  local file="$LANE_OUT_DIR/f-quiet.json"
  jq '.quiet = true' "$MOCK_ENGINE_SCENARIO" >"$file" || return 1
  printf '%s' "$file"
}
boot_scenario() {
  local file="$LANE_OUT_DIR/f-boot.json"
  jq '.quiet = true | .no_transcript = true' "$MOCK_ENGINE_SCENARIO" >"$file" || return 1
  printf '%s' "$file"
}
boot_start() {
  local scenario="$1" out
  BOOT_SOCK="cc-$(date +%s)-$$-37"
  out="$(pfm internal chat-server "$BOOT_SOCK" "$CWD" \
    "env CLAUDE_CONFIG_DIR=$HOME/.cc/$SEAT MOCK_ENGINE_SCENARIO=$scenario claude" 2>&1)" || {
    BOOT_WHY="chat-server $BOOT_SOCK: $(one_line "$out")"
    return 1
  }
  return 0
}
boot_stop() {
  tmux -S "$(socket_path "$BOOT_SOCK")" kill-server 2>/dev/null
}
live_storm_rows() { pfm ls --tsv 2>/dev/null | awk -F'\t' 'NR > 1 && $1 ~ /^live-/ && $5 ~ /^STORM_[0-9]+$/'; }

# default_engine_word — the engine `chat new` falls back to with no --engine
# and no calling chat (internal/config DefaultEngine): ask.engine when its
# roster is non-empty (Codex when unset), else Claude. Printed as the roster
# word its refusal names ("requested <Word> account N is not in the roster").
default_engine_word() {
  local ask
  ask="$(pfm config show 2>/dev/null | sed -n 's/^config ask.engine=\([^ ]*\).*/\1/p' | head -1)"
  [ -n "$ask" ] || ask=cx
  case "$ask" in
    cc) echo Claude ;;
    cx) if [ -n "$CX_HOME" ]; then echo Codex; else echo Claude; fi ;;
    ox) if [ -f "$OC_DB" ]; then echo OpenCode; else echo Claude; fi ;;
    *) echo Claude ;;
  esac
}

# The picker helpers live in lib.sh. Match captured panes through a here-string:
# grep -q can close a pipe before a large capture finishes writing under pipefail.
tui_has() { grep -qF -- "$1" <<<"$(tui_pane)"; }
tui_pane_e() { tmux -S "$TUI_SOCK" capture-pane -e -p -t tui 2>&1; }
tui_cmd() { tmux -S "$TUI_SOCK" display -p -t tui '#{pane_current_command}' 2>/dev/null; }
tui_cache() { tui_pane | grep -oE '⚡ 1h|🪫 5m' | head -1; }
tui_account() { tui_pane | sed -n 's/.*account \([0-9][0-9]*\) ·.*/\1/p' | head -1; }
# tui_left — the picker has left the pane (exit or exec): 0 when the pane's
# command is no longer pfm within <secs>.
tui_left() {
  local deadline=$(( $(_lane_now) + $1 ))
  while [ "$(_lane_now)" -lt "$deadline" ]; do
    [ "$(tui_cmd)" != pfm ] && return 0
    # POLL-STEP: the picker pane command changes from pfm.
    sleep 0.2
  done
  [ "$(tui_cmd)" != pfm ]
}
# golden_line <file> <n> — the n-th frame line of a golden .ansi (Go-quoted
# strings, one per line), its escapes and colors stripped, trailing spaces cut.
golden_line() {
  sed -n "${2}p" "$1" | sed -e 's/^"//' -e 's/"$//' -e 's/\\x1b\[[0-9;]*m//g' \
    -e 's/\\"/"/g' -e 's/\\\\/\\/g' -e 's/ *$//'
}
hex_to_sgr() { # hex_to_sgr "#rrggbb" — the r;g;b triple a truecolor SGR carries
  local h="${1#\#}"
  printf '%d;%d;%d' "0x${h:0:2}" "0x${h:2:2}" "0x${h:4:2}"
}

# ─── F.01 — chat new across every engine ────────────────────────────────────

# open_cc — the lane's chat, opened the one way: F.01 spawns it (stdout and
# stderr kept apart in files, because F.04 asserts the --await contract that
# the answer owns stdout and the launch summary moves to stderr) and the
# library's single re-open spends the same command after it dies.
open_cc() {
  local rc
  pfm chat new --name "$CC" --engine cc --account "$SEAT" --cwd "$CWD" --cache 1h --model sonnet --effort low \
    --await --timeout 300 --settle 5 --progress \
    "You are $CC, the chat an automated Tier B lane drives. Reply with one word: ready. Then wait and do exactly what each next message says, nothing more." \
    >/tmp/f-cc.launch.out 2>/tmp/f-cc.launch.err
  rc=$?
  cat /tmp/f-cc.launch.out /tmp/f-cc.launch.err
  return $rc
}
lane_reopen 'open_cc'
open_cx() {
  pfm chat new --name "$CX" --engine cx --cwd /work/lumen --await --timeout 300 \
    "You are $CX, a Codex chat an automated Tier B lane drives. Reply with one word: ready. Then wait and do exactly what each next message says, nothing more." 2>&1
}

beat F.01-new-engine
spends "cc:$SEAT+cx"
target "$CC"
expect-log 'unknown engine'
expect-log 'does not support headless chat'
expect-log 'not in the configured roster'
expect-log 'not one bare socket name'
expect-log 'no chat named'
bad=""
# K8: --name is mandatory — no name, no launch, usage on stderr.
out="$(pfm chat new 2>&1)"
rc=$?
[ "$rc" -eq 2 ] && grep -q 'usage: pfm chat new' <<<"$out" ||
  bad="$bad K8 chat new without --name exited $rc (want 2 + usage): $(one_line "$out");"
# K4: --engine is parsed by the registry — an unregistered spelling is refused
# naming the accepted ones; the registered OpenCode id has no headless door in
# this tree (action.PlannerFor) and says so by name instead of spawning nothing.
out="$(pfm chat new --name F_NOPE --engine oc --cwd "$CWD" "x" 2>&1)"
rc=$?
[ "$rc" -eq 2 ] && grep -q 'unknown engine "oc"' <<<"$out" ||
  bad="$bad K4 --engine oc exited $rc without naming the unknown engine: $(one_line "$out");"
out="$(pfm chat new --name F_NOPE --engine ox --cwd "$CWD" "x" 2>&1)"
rc=$?
[ "$rc" -eq 2 ] && grep -q 'OpenCode does not support headless chat' <<<"$out" ||
  bad="$bad K4/oc --engine ox exited $rc without the named absence of the OpenCode door: $(one_line "$out");"
# K5: no --engine → the calling chat's engine, else the machine default. Both
# branches are driven with an account no roster holds, so the refusal NAMES the
# engine the fallback chose and nothing is spawned or registered.
want="$(default_engine_word)"
out="$(pfm chat new --name F_NOPE --account 999 --cwd "$CWD" "x" 2>&1)"
rc=$?
[ "$rc" -eq 2 ] && grep -q "$want account 999 is not in the configured roster" <<<"$out" ||
  bad="$bad K5 machine-default fallback: exited $rc, wanted the $want roster named: $(one_line "$out");"
out="$(CLAUDE_CODE_SESSION_ID=lane-caller pfm chat new --name F_NOPE --account 999 --cwd "$CWD" "x" 2>&1)"
rc=$?
[ "$rc" -eq 2 ] && grep -q 'Claude account 999 is not in the configured roster' <<<"$out" ||
  bad="$bad K5 caller-engine fallback (CLAUDE_CODE_SESSION_ID set): exited $rc, wanted the Claude roster named: $(one_line "$out");"
# K6: engine.FromSocket — a socket prefix no engine owns is a named absence.
out="$(pfm internal chat-server zz-1-2-3 /tmp true 2>&1)"
rc=$?
[ "$rc" -eq 2 ] && grep -q 'not one bare socket name carrying an engine prefix' <<<"$out" ||
  bad="$bad K6 an unknown socket prefix was not refused by name (exit $rc): $(one_line "$out");"
# C9/C10/C11/C12 + K7: the real launch, on the seat this run spends.
if live_chat "$CC"; then
  cc_note="$CC was already live (a previous run in this container)"
else
  out="$(open_cc)"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    bad="$bad C9 pfm chat new $CC exited $rc: $(one_line "$out");"
  elif ! live_chat "$CC"; then
    bad="$bad C9 chat new exited 0 but no live row for $CC: $(one_line "$(pfm ls --plain)");"
  fi
  cc_note="$CC spawned"
fi
if live_chat "$CC"; then
  [ "$(live_field "$CC" 1)" = live-claude ] || bad="$bad C11 $CC row kind is '$(live_field "$CC" 1)', want live-claude;"
  [ "$(live_field "$CC" 4)" = "$CWD" ] || bad="$bad C12 $CC row cwd is '$(live_field "$CC" 4)', want $CWD;"
  [ "$(live_field "$CC" 9)" = "$SEAT" ] || bad="$bad C9 $CC row account is '$(live_field "$CC" 9)', want the seat it was launched on ($SEAT);"
  grep -Eq '^cc-[0-9]+-[0-9]+-[0-9]+$' <<<"$(sock_base "$CC")" ||
    bad="$bad K7 $CC socket '$(sock_base "$CC")' breaks the <prefix><epoch>-<pid>-<rand> law;"
fi
# K9: the unnamed sentinel is a display value, never an address.
out="$(pfm chat status "(unnamed)" 2>&1)"
rc=$?
[ "$rc" -eq 4 ] || bad="$bad K9 status on the '(unnamed)' sentinel exited $rc (want 4, never addressable): $(one_line "$out");"
# C11 cx: the Codex home, when this run has one.
cx_note=""
if [ -n "$CX_HOME" ]; then
  if live_chat "$CX"; then
    cx_note="$CX already live"
  else
    out="$(open_cx)"
    rc=$?
    if [ "$rc" -ne 0 ]; then
      bad="$bad C11 pfm chat new --engine cx $CX exited $rc: $(one_line "$out");"
    elif ! live_chat "$CX"; then
      bad="$bad C11 chat new --engine cx exited 0 but no live row for $CX;"
    fi
    cx_note="$CX spawned on $CX_HOME"
  fi
  if live_chat "$CX"; then
    [ "$(live_field "$CX" 1)" = live-codex ] || bad="$bad C11 $CX row kind is '$(live_field "$CX" 1)', want live-codex;"
    grep -Eq '^cx-[0-9]+-[0-9]+-[0-9]+$' <<<"$(sock_base "$CX")" ||
      bad="$bad K7 $CX socket '$(sock_base "$CX")' breaks the socket law;"
  fi
fi
if [ -n "$bad" ]; then
  fail "$bad"
else
  pass "$cc_note (live-claude, $CWD, seat $SEAT, socket $(sock_base "$CC")) · $cx_note (live-codex, socket $(sock_base "$CX")) · --engine oc/ox, no --name, account 999 (default → $want, caller → Claude), unknown prefix, (unnamed): each refused by name"
fi

# ─── F.02 — {name}:{group}, _KILL/_HIDE, --agent-role, --prompt-file ────────

beat F.02-new-label-role
spends "cc:$SEAT"
target "$GRP"
expect-log 'mutually exclusive'
bad=""
mkdir -p "$CWD/.claude/agents"
cat >"$CWD/.claude/agents/f-role.md" <<'ROLE'
---
name: f-role
description: the constitution lane F composes ahead of a prompt file
---
F-ROLE-CONSTITUTION: you are the lane role. Do exactly what each message says, nothing more.
ROLE
printf 'F-PROMPT-FILE: reply with exactly one word: ready. Then wait and do exactly what each next message says, nothing more.\n' >/tmp/f-grp.prompt
# K21: a prompt file and an inline prompt never combine.
out="$(pfm chat new --name F_NOPE --engine cc --account "$SEAT" --cwd "$CWD" --prompt-file /tmp/f-grp.prompt "inline too" 2>&1)"
rc=$?
[ "$rc" -eq 2 ] && grep -q 'mutually exclusive' <<<"$out" ||
  bad="$bad K21 --prompt-file plus an inline prompt exited $rc without the named refusal: $(one_line "$out");"
# The launch: the label grammar as the name, the role first, the prompt from a
# file, --attach so F.04 can read the attach line this non-tty stdout received.
if live_chat "$GRP"; then
  grp_note="$GRP already live"
else
  pfm chat new --name "$GRP" --engine cc --account "$SEAT" --cwd "$CWD" --model sonnet --effort low \
    --agent-role f-role --prompt-file /tmp/f-grp.prompt --attach >/tmp/f-grp.launch.out 2>/tmp/f-grp.launch.err
  rc=$?
  [ "$rc" -eq 0 ] || bad="$bad chat new $GRP exited $rc: $(one_line "$(cat /tmp/f-grp.launch.out /tmp/f-grp.launch.err)");"
  grp_note="$GRP spawned"
fi
wait_for 30 "live_chat '$GRP'" || bad="$bad no live row for '$GRP' — ${LANE_WAIT_WHY};"
if ! live_chat "$GRP"; then
  bad="$bad no live row for '$GRP' — nothing below could be asserted: $(one_line "$(pfm ls --plain)");"
else
  sock="$(live_field "$GRP" 11)"
  # K12: the row carries the whole label; the tmux window converges on its name part.
  [ "$(live_field "$GRP" 5)" = "$GRP" ] || bad="$bad K12 the row's name is '$(live_field "$GRP" 5)', want '$GRP';"
  window="$(tmux -S "$(socket_path "$sock")" list-windows -F '#{window_name}' 2>&1 | head -1)"
  case "$window" in *F_GRP*) ;; *) bad="$bad K12 tmux window '$(one_line "$window")' does not carry the label's name part;" ;; esac
  # K20/C18: the role lives in the per-seat prompt channel, not the first user message.
  socket_name="${sock##*/}"
  role_prompt="$SID_DIR/role-prompt-$socket_name.md"
  if [ ! -f "$role_prompt" ]; then
    bad="$bad K20 no per-seat role prompt at $role_prompt; present: $(one_line "$(printf '%s ' "$SID_DIR"/role-prompt-*)");"
  elif [ "$(sed -n '1p' "$role_prompt")" != '<!-- pfm agent-role: f-role -->' ]; then
    bad="$bad K20 the first line of $role_prompt does not name f-role: $(one_line "$(sed -n '1p' "$role_prompt")");"
  elif ! grep -q 'F-ROLE-CONSTITUTION' "$role_prompt"; then
    bad="$bad K20 $role_prompt names f-role but does not carry its constitution;"
  fi
  wait_for 30 "pfm chat read '$GRP' --tail 200 --json 2>/dev/null | jq -e 'any(.entries[]; .role == \"user\")'" ||
    bad="$bad C18/C17 the transcript's first user record could not be read (pfm chat read --json): ${LANE_WAIT_WHY};"
  first_user="$(pfm chat read "$GRP" --tail 200 --json 2>/dev/null |
    jq -r '[.entries[] | select(.role == "user")][0].text // ""' | tr '\n' ' ')"
  if [ -z "$first_user" ]; then
    bad="$bad C18/C17 the transcript's first user record could not be read (pfm chat read --json);"
  else
    prompt_at="$(printf '%s' "$first_user" | awk '{ print index($0, "F-PROMPT-FILE") }')"
    [ "$prompt_at" -gt 0 ] || bad="$bad C17 the prompt file's text never reached the first user record;"
    grep -q 'F-ROLE-CONSTITUTION' <<<"$first_user" &&
      bad="$bad C18 the role constitution leaked into the first user record instead of staying in its prompt channel;"
  fi
  # K13: the name-based kill — a _KILL (or legacy _HIDE, case-insensitive) label
  # hides the chat with no store row; renaming it back is the unkill.
  for label in _KILL_F _hide_f; do
    out="$(pfm chat name "$GRP" "$label" 2>&1)" || bad="$bad K13 rename to $label failed: $(one_line "$out");"
    wait_for 10 "! pfm ls --tsv 2>/dev/null | awk -F '\t' -v n='$label' 'NR > 1 && \$5 == n { found = 1 } END { exit !found }' && [ \"\$(all_field '$label' 10)\" = true ]" ||
      bad="$bad K13 '$label' is still in the DEFAULT view or its -a killed column is not true: ${LANE_WAIT_WHY};"
    pfm ls --tsv 2>/dev/null | awk -F'\t' -v n="$label" 'NR > 1 && $5 == n { found = 1 } END { exit !found }' &&
      bad="$bad K13 '$label' is still in the DEFAULT view;"
    [ "$(all_field "$label" 10)" = true ] || bad="$bad K13 '$label' -a row killed column is '$(all_field "$label" 10)', want true;"
    out="$(pfm chat name "$label" "$GRP" 2>&1)" || bad="$bad K13 rename $label back to $GRP failed: $(one_line "$out");"
    wait_for 10 "live_chat '$GRP'" || bad="$bad K13 after the renames '$GRP' has no live row in the default view: ${LANE_WAIT_WHY};"
  done
  live_chat "$GRP" || bad="$bad K13 after the renames '$GRP' has no live row in the default view;"
  # K14 uses a separate seat: store kill can end its server, while F.04 still
  # needs the original --attach chat live to judge its launch.
  pfm chat new --name F_STORE_KILL --engine cc --account "$SEAT" --cwd "$CWD" 'store kill probe' >/dev/null 2>&1 ||
    bad="$bad K14 could not start F_STORE_KILL;"
  wait_for 20 'live_chat F_STORE_KILL' || bad="$bad K14 F_STORE_KILL has no live row: ${LANE_WAIT_WHY};"
  pfm chat kill F_STORE_KILL >/dev/null 2>&1 || bad="$bad K14 chat kill exited non-zero;"
  wait_for 10 '[ "$(all_field F_STORE_KILL 10)" = true ]' || bad="$bad K14 after kill the -a row's killed column is not true: ${LANE_WAIT_WHY};"
  [ "$(all_field F_STORE_KILL 10)" = true ] || bad="$bad K14 after kill the -a row's killed column is '$(all_field F_STORE_KILL 10)', want true;"
  pfm ls --tsv 2>/dev/null | awk -F'\t' 'NR > 1 && $5 == "F_STORE_KILL" { found = 1 } END { exit !found }' &&
    bad="$bad K14 the killed row is still in the DEFAULT view;"
  pfm chat unkill F_STORE_KILL >/dev/null 2>&1 || bad="$bad K14 chat unkill exited non-zero;"
  wait_for 10 '[ "$(all_field F_STORE_KILL 10)" = false ]' || bad="$bad K14 after unkill the killed column is not false: ${LANE_WAIT_WHY};"
  [ "$(all_field F_STORE_KILL 10)" = false ] || bad="$bad K14 after unkill the killed column is '$(all_field F_STORE_KILL 10)', want false;"
  pfm chat kill F_STORE_KILL >/dev/null 2>&1 || bad="$bad K14 could not hide F_STORE_KILL after the assertion;"
fi
if [ -n "$bad" ]; then fail "$bad"; else
  pass "$grp_note: label '$GRP' in row + window, role prompt $role_prompt names f-role while the first user record contains only the prompt-file text, store kill/unkill, _KILL_F and _hide_f hide by name and the rename back unhides"
fi

# ─── F.03 — --account / --cache / --model --effort, read off the launch pfm made ─

beat F.03-new-account-1h-model
spends "cc:$SEAT"
target_live "$CC"
expect-log 'not in the configured roster'
expect-log 'unknown Claude effort'
if requires; then
  bad=""
  sock="$(live_field "$CC" 11)"
  # The pane's start command IS the launch pfm synthesized (action.ClaudeSpawn
  # ShellCommand): the flags and the cache assignment are read back from tmux.
  start="$(tmux -S "$(socket_path "$sock")" list-panes -F '#{pane_start_command}' 2>&1 | head -1)"
  [ -n "$start" ] || bad="$bad the pane start command could not be read from $sock;"
  grep -q -- '--settings' <<<"$start" || bad="$bad C14/K25 the launch carries no --settings payload;"
  grep -Fq -- "'--model' 'sonnet'" <<<"$start" || bad="$bad C15/K28 the launch carries no '--model sonnet': $(one_line "$start" | cut -c1-200);"
  grep -Fq -- "'--effort' 'low'" <<<"$start" || bad="$bad C16/K28 the launch carries no '--effort low';"
  grep -Fq "CACHE_LIVE_CONTROL_MAIN_TTL='1h'" <<<"$start" ||
    bad="$bad C14/K25 the launch's process environment carries no CACHE_LIVE_CONTROL_MAIN_TTL='1h' (--cache 1h);"
  # C13: the row reports the seat asked for; K24: the seat's medal on the row; K25: the ⚡ badge.
  [ "$(live_field "$CC" 9)" = "$SEAT" ] || bad="$bad C13 row account is '$(live_field "$CC" 9)', want $SEAT;"
  plain_row="$(pfm ls --plain 2>/dev/null | grep -F "● $CC " | head -1)"
  [ -n "$plain_row" ] || bad="$bad no '● $CC' line in pfm ls --plain;"
  if [ -z "$MEDAL" ]; then
    bad="$bad K24 the seat's medal could not be read from pfm config show (config accounts=…);"
  else
    grep -qF "$MEDAL" <<<"$plain_row" || bad="$bad K24 the plain row lacks seat $SEAT's medal $MEDAL: $(one_line "$plain_row");"
  fi
  grep -qF '⚡' <<<"$plain_row" || bad="$bad K25 the plain row lacks the ⚡ 1h badge: $(one_line "$plain_row");"
  # The refusals, by name, nothing spawned.
  out="$(pfm chat new --name F_NOPE --engine cc --account 999 --cwd "$CWD" "x" 2>&1)"
  rc=$?
  [ "$rc" -eq 2 ] && grep -q 'Claude account 999 is not in the configured roster' <<<"$out" ||
    bad="$bad C13 --account 999 exited $rc without naming the roster: $(one_line "$out");"
  out="$(pfm chat new --name F_NOPE --engine cc --account "$SEAT" --effort bogus --cwd "$CWD" "x" 2>&1)"
  rc=$?
  [ "$rc" -eq 2 ] && grep -q 'unknown Claude effort "bogus"' <<<"$out" ||
    bad="$bad C16 --effort bogus exited $rc without the named roster of efforts: $(one_line "$out");"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "launch carries --model sonnet --effort low CACHE_LIVE_CONTROL_MAIN_TTL=1h; row account $SEAT with medal $MEDAL and ⚡; account 999 and effort bogus refused by name"
  fi
fi

# ─── F.04 — --await / --attach mechanics, headless by construction ──────────

GRP_ID=""
beat F.04-new-await-attach
spends "cc:$SEAT"
target_live "$CC"
if requires; then
  bad=""
  # C19: with --await the ANSWER owns stdout and the launch summary moves to stderr.
  if [ ! -f /tmp/f-cc.launch.out ]; then
    bad="$bad C19 $CC was not launched by this lane run (no launch capture) — the --await contract was not exercised;"
  else
    grep -qx 'ok' /tmp/f-cc.launch.out || bad="$bad C19 --await stdout does not carry the scenario reply ok: $(one_line "$(cat /tmp/f-cc.launch.out)");"
    grep -q "$(printf '\t%s\t' "$CC")" /tmp/f-cc.launch.err || bad="$bad C19 the launch summary (cc<TAB>$CC<TAB>…) is not on stderr: $(one_line "$(head -3 /tmp/f-cc.launch.err)");"
    grep -q 'attach: tmux -L' /tmp/f-cc.launch.err || bad="$bad C19 the attach hint is not on stderr;"
    grep -q "$(printf '\t%s\t' "$CC")" /tmp/f-cc.launch.out && bad="$bad C19 the launch summary leaked onto --await's stdout;"
  fi
  out="$(pfm chat new --name F_NOPE --engine cc --account "$SEAT" --cwd "$CWD" --attach --await "x" 2>&1)"
  rc=$?
  [ "$rc" -eq 2 ] && grep -q 'usage: pfm chat new' <<<"$out" ||
    bad="$bad --attach with --await exited $rc (want 2 + usage): $(one_line "$out");"
  # C20/K16: --attach on a non-tty stdout prints the attach line for the new server.
  if [ ! -f /tmp/f-grp.launch.out ]; then
    bad="$bad C20 '$GRP' was not launched with --attach by this run (no capture);"
  elif ! live_chat "$GRP"; then
    bad="$bad C20 '$GRP' has no live row, so its attach line cannot be checked against a socket;"
  else
    grp_sock="$(sock_base "$GRP")"
    grep -Fq "TMUX= tmux -L '$grp_sock' attach -t" /tmp/f-grp.launch.out ||
      bad="$bad C20/K16 --attach printed no 'TMUX= tmux -L $grp_sock attach -t …' line: $(one_line "$(cat /tmp/f-grp.launch.out)");"
  fi
  # K15: no terminal is attached to any lane chat — headless by construction.
  for name in "$CC" "$GRP"; do
    live_chat "$name" || continue
    clients="$(tmux -S "$(socket_path "$(live_field "$name" 11)")" list-clients 2>&1)"
    [ -z "$clients" ] || bad="$bad K15 $name has an attached client: $(one_line "$clients");"
  done
  # '$GRP' has served its beats: end it and hide the resumable row it leaves,
  # so F.06 has a killed row for -a/-K and F.07 a resume-claude kind.
  if live_chat "$GRP"; then
    GRP_ID="$(live_field "$GRP" 2)"
    out="$(pfm chat end "$GRP" 2>&1)" || bad="$bad pfm chat end '$GRP' failed: $(one_line "$out");"
    LANE_ANCHOR= wait_for 10 "! live_chat '$GRP'" || bad="$bad '$GRP' still has a live row after end: ${LANE_WAIT_WHY};"
    live_chat "$GRP" && bad="$bad '$GRP' still has a live row after end;"
    out="$(pfm chat kill "$GRP_ID" 2>&1)" || bad="$bad pfm chat kill $GRP_ID (hide the resume row) failed: $(one_line "$out");"
    LANE_ANCHOR= wait_for 10 "[ \"\$(all_field '$GRP' 1)\" = resume-claude ] && [ \"\$(all_field '$GRP' 10)\" = true ]" ||
      bad="$bad the ended chat's resume row is not hidden: ${LANE_WAIT_WHY};"
    [ "$(all_field "$GRP" 1)" = resume-claude ] || bad="$bad the ended chat's -a row kind is '$(all_field "$GRP" 1)', want resume-claude;"
    [ "$(all_field "$GRP" 10)" = true ] || bad="$bad the ended chat's resume row is not hidden (killed=$(all_field "$GRP" 10));"
  fi
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "--await: answer on stdout, summary on stderr; --attach+--await refused; --attach printed the attach line for ${grp_sock:-?}; no client on any lane socket; '$GRP' ended → hidden resume row $GRP_ID"
  fi
fi

# ─── F.05 — the storm ───────────────────────────────────────────────────────

STORM_RAN=0
beat F.05-storm
spends "cc:$SEAT+cx"
target "$CC"
expect-log 'unknown command'
bad=""
out="$(STORM_ENGINES='cc cx' STORM_CC_ACCOUNT="$SEAT" STORM_CC_MODEL=sonnet STORM_CC_EFFORT=low \
  lane_storm_start 4 2 2>&1)"
rc=$?
[ "$rc" -eq 0 ] || bad="$bad lane_storm_start 4 2 exited $rc: $(one_line "$(printf '%s\n' "$out" | tail -4)");"
live="$(live_storm_rows)"
n_live="$(printf '%s\n' "$live" | grep -c .)"
[ "$n_live" -eq 4 ] || bad="$bad $n_live live STORM_<n> rows (want 4): $(one_line "$(printf '%s\n' "$live" | cut -f1,5)");"
for i in 1 2 3 4; do
  kind="$(live_field "STORM_$i" 1)"
  want="live-claude"
  [ -n "$CX_HOME" ] && [ $((i % 2)) -eq 0 ] && want="live-codex"
  [ "$kind" = "$want" ] || bad="$bad STORM_$i kind '$kind' (want $want per the cc/cx rotation);"
done
[ "$n_live" -gt 0 ] && STORM_RAN=1
# K19: storm and idle are lane helpers over ordinary verbs, not pfm subcommands.
for verb in storm idle; do
  out="$(pfm "$verb" 2>&1)"
  rc=$?
  [ "$rc" -eq 2 ] && grep -q "unknown command \"$verb\"" <<<"$out" ||
    bad="$bad K19 pfm $verb exited $rc without 'unknown command' (it must not exist): $(one_line "$out" | cut -c1-120);"
done
if [ -n "$bad" ]; then
  fail "$bad"
else
  pass "lane_storm_start spawned STORM_1..4 over cc cx (kinds per rotation); pfm storm/idle are named absences (exit 2)"
fi

# ─── F.06 — pfm ls in every shape ───────────────────────────────────────────

beat F.06-ls-rows
spends "cc:$SEAT"
target_live "$CC"
expect-log 'must be auto, on, or off'
if requires; then
  bad=""
  cc_id="$(live_field "$CC" 2)"
  cc_sock="$(sock_base "$CC")"
  # C3: the stable TSV header, column for column.
  header="$(pfm ls --tsv 2>&1 | head -1)"
  [ "$header" = "$(printf 'kind\tid\tproject\tcwd\tname\tprompts\tsize\tactivity_ns\taccount\tkilled\tsocket')" ] ||
    bad="$bad C3 --tsv header is '$(one_line "$header")';"
  # C2: the plain twin groups by project and marks a live row ●.
  plain="$(pfm ls --plain 2>&1)"
  grep -q '^\[orbit\]' <<<"$plain" || bad="$bad C2 --plain has no [orbit] group;"
  grep -q "^● $CC " <<<"$plain" || bad="$bad C2 --plain has no '● $CC' row;"
  # C4/C5: the hidden row F.04 left is out of the default view, in -a, and in the killed ledger.
  if [ -z "$GRP_ID" ]; then
    bad="$bad C4/C5 no hidden row to assert against (F.04 left none);"
  else
    pfm ls --tsv 2>/dev/null | awk -F'\t' -v id="$GRP_ID" 'NR > 1 && $2 == id { f = 1 } END { exit(f ? 0 : 1) }' &&
      bad="$bad C4 the hidden row $GRP_ID is in the DEFAULT --tsv view;"
    pfm ls -a --tsv 2>/dev/null | awk -F'\t' -v id="$GRP_ID" 'NR > 1 && $2 == id && $10 == "true" { f = 1 } END { exit(f ? 0 : 1) }' ||
      bad="$bad C4 -a --tsv does not carry $GRP_ID with killed=true;"
    pfm ls --all --tsv 2>/dev/null | awk -F'\t' -v id="$GRP_ID" 'NR > 1 && $2 == id { f = 1 } END { exit(f ? 0 : 1) }' ||
      bad="$bad C4 --all (the long spelling) does not carry $GRP_ID;"
    ledger="$(pfm ls -K --tsv 2>&1)"
    printf '%s\n' "$ledger" | awk -F'\t' -v id="$GRP_ID" '$1 == id && NF == 3 { f = 1 } END { exit(f ? 0 : 1) }' ||
      bad="$bad C5 -K --tsv has no 3-column row for $GRP_ID: $(one_line "$ledger" | cut -c1-160);"
    [ "$(pfm ls --killed 2>&1)" = "$ledger" ] || bad="$bad C5 --killed and -K --tsv disagree;"
  fi
  # C8: `pfm ls <id>` opens the chat directly; off a tty the action line is printed.
  out="$(pfm ls "$cc_id" 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] && grep -q "tmux -L '$cc_sock' attach" <<<"$out" ||
    bad="$bad C8 pfm ls $cc_id exited $rc without the attach line for $cc_sock: $(one_line "$out");"
  pfm ls "$cc_id" --plain >/dev/null 2>&1
  rc=$?
  [ "$rc" -eq 2 ] || bad="$bad C8 pfm ls <id> --plain exited $rc (want 2, usage);"
  # C21: chat open by name resolves to the same action.
  out="$(pfm chat open "$CC" 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] && grep -q "tmux -L '$cc_sock' attach" <<<"$out" ||
    bad="$bad C21 pfm chat open $CC exited $rc without the attach line: $(one_line "$out");"
  # C60: the compatibility listing, this repo and everywhere.
  out="$(cd "$CWD" && pfm chat ls 2>&1)"
  grep -q 'live chats in this repo' <<<"$out" || bad="$bad C60 chat ls has no 'live chats in this repo' header: $(one_line "$out" | cut -c1-120);"
  grep -q "$CC" <<<"$out" || bad="$bad C60 chat ls (in $CWD) does not list $CC;"
  out="$(cd "$CWD" && pfm chat ls --all 2>&1)"
  grep -q 'live chats everywhere' <<<"$out" || bad="$bad C60 chat ls --all has no 'live chats everywhere' header;"
  # C7: --safe validates, and 'on' names itself in the cosmos title.
  out="$(pfm ls --safe bogus --tsv 2>&1)"
  rc=$?
  [ "$rc" -eq 2 ] && grep -q 'must be auto, on, or off' <<<"$out" ||
    bad="$bad C7 --safe bogus exited $rc without the named roster: $(one_line "$out");"
  # C1: the interactive picker entry, on a real tty inside tmux.
  if tui_open 100 30 ls; then
    tui_wait 10 '╭─ fleet' || bad="$bad C1 the picker painted no fleet frame: $(one_line "$(tui_pane)" | cut -c1-200);"
  else
    bad="$bad C1 $TUI_WHY;"
  fi
  if tui_open 100 30 ls --safe on; then
    tui_keys Tab; tui_keys Tab; tui_keys Tab
    tui_wait 10 'cosmos · safe' || bad="$bad C7 --safe on: the cosmos panel title does not read 'cosmos · safe': ${LANE_WAIT_WHY:-timed out waiting for picker};"
    tui_has 'cosmos · safe' || bad="$bad C7 --safe on: the cosmos panel title does not read 'cosmos · safe': $(one_line "$(tui_pane | grep -F cosmos | head -2)");"
  else
    bad="$bad C7 --safe on: $TUI_WHY;"
  fi
  # C6: --no-sky disables the animation clock, and the cosmos tab says so on `space`.
  if tui_open 100 30 ls --no-sky; then
    tui_keys Tab; tui_keys Tab; tui_keys Tab
    tui_wait 10 'cosmos ·' || bad="$bad C6 --no-sky: cosmos tab did not paint before Space: ${LANE_WAIT_WHY:-timed out waiting for picker};"
    tui_keys Space
    tui_has 'this picker runs --no-sky' || bad="$bad C6 --no-sky: space on the cosmos tab did not name the disabled clock: $(one_line "$(tui_pane | grep -F cosmos | head -2)");"
  else
    bad="$bad C6 --no-sky: $TUI_WHY;"
  fi
  tui_close
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "--tsv header, --plain groups, -a/--all/-K/--killed carry the hidden row $GRP_ID, ls <id> and chat open print the attach line, chat ls/--all, --safe bogus/on, --no-sky, and the interactive picker painted"
  fi
fi

# ─── F.07 — every row kind ──────────────────────────────────────────────────

beat F.07-row-kinds
spends none
target_live "$CC"
if requires; then
  bad="" held=""
  # An ended Codex chat leaves the resume-codex kind; F_CX has served F.01.
  if [ -n "$CX_HOME" ] && live_chat "$CX"; then
    out="$(pfm chat end "$CX" 2>&1)" || bad="$bad pfm chat end $CX failed: $(one_line "$out");"
    LANE_ANCHOR= wait_for 10 "grep -qx resume-codex <<<\"\$(kinds_in_fleet)\" && ! live_chat '$CX'" ||
      bad="$bad K34 no resume-codex row after ending $CX: ${LANE_WAIT_WHY};"
  fi
  kinds="$(kinds_in_fleet)"
  has_kind() { grep -qx "$1" <<<"$kinds"; }
  has_kind live-claude && held="$held K29" || bad="$bad K29 no live-claude row;"
  has_kind resume-claude && held="$held K33" || bad="$bad K33 no resume-claude row (F.04 ended '$GRP');"
  has_kind new-claude && held="$held K35" || bad="$bad K35 no new-claude row;"
  has_kind live-codex && held="$held K30" || bad="$bad K30 no live-codex row (the storm's cx chats);"
  has_kind resume-codex && held="$held K34" || bad="$bad K34 no resume-codex row after ending $CX;"
  has_kind new-codex && held="$held K36" || bad="$bad K36 no new-codex row;"
  cc_sock="$(sock_base "$CC")"
  cc_path="$(socket_path "$cc_sock")"
  split_sid="$(cat /proc/sys/kernel/random/uuid)"
  split_pane="$(tmux -S "$cc_path" split-window -d -P -F '#{pane_id}' -c "$CWD" \
    "env CLAUDE_CONFIG_DIR=$HOME/.cc/$SEAT claude --session-id $split_sid" 2>&1)"
  if [ "${split_pane#%}" = "$split_pane" ]; then
    bad="$bad K31 split-window failed on $cc_sock: $(one_line "$split_pane");"
  else
    wait_for 30 "test -s '$SID_DIR/$cc_sock.$split_pane' && row_kind_socket live-split '$cc_sock'" &&
      held="$held K31" || bad="$bad K31 no live-split row for $cc_sock with pane crumb $SID_DIR/$cc_sock.$split_pane: ${LANE_WAIT_WHY:-unknown};"
    tmux -S "$cc_path" kill-pane -t "$split_pane" 2>/dev/null || bad="$bad K31 could not kill split pane $split_pane;"
    wait_for 30 "! row_kind_socket live-split '$cc_sock'" || bad="$bad K31 live-split row remained after killing $split_pane;"
  fi
  quiet="$(quiet_scenario)" || bad="$bad K32 could not write quiet scenario under $LANE_OUT_DIR;"
  boot="$(boot_scenario)" || bad="$bad K37 could not write boot scenario under $LANE_OUT_DIR;"
  agent_sid="$(cat /proc/sys/kernel/random/uuid)"
  agent_server="lane-f-agent-$$"
  if [ -n "$quiet" ] && tmux -L "$agent_server" new-session -d -s agent -c "$CWD" \
    "env CLAUDE_CONFIG_DIR=$HOME/.cc/$SEAT MOCK_ENGINE_SCENARIO=$quiet claude --session-id $agent_sid" 2>/dev/null; then
    wait_for 30 "row_kind_id agent '$agent_sid'" && held="$held K32" ||
      bad="$bad K32 no agent row for session $agent_sid: ${LANE_WAIT_WHY:-unknown};"
    tmux -L "$agent_server" kill-server 2>/dev/null || bad="$bad K32 could not kill $agent_server;"
    wait_for 30 "! row_kind_id agent '$agent_sid'" || bad="$bad K32 agent row remained after killing $agent_server;"
  else
    bad="$bad K32 plain tmux server could not start the quiet Claude;"
  fi
  if [ -n "$boot" ] && boot_start "$boot"; then
    wait_for 30 "row_kind_socket booting '$BOOT_SOCK'" && held="$held K37" ||
      bad="$bad K37 no booting row for $BOOT_SOCK: ${LANE_WAIT_WHY:-unknown};"
    boot_stop || bad="$bad K37 could not kill $BOOT_SOCK;"
    wait_for 30 "! row_kind_socket booting '$BOOT_SOCK'" || bad="$bad K37 booting row remained after teardown;"
  else
    bad="$bad K37 ${BOOT_WHY:-boot scenario missing};"
  fi
  if [ -f "$OC_DB" ]; then
    has_kind new-opencode && held="$held K39" || bad="$bad K39 no new-opencode row while $OC_DB exists;"
    if has_kind resume-opencode; then held="$held K38"; oc_note="K38/K39 asserted"; else
      oc_note="K39 asserted; K38 NOT asserted — $OC_DB holds no session (no OpenCode run in this root)"
    fi
  else
    oc_note="K38/K39 NOT asserted — no OpenCode store at $OC_DB"
  fi
  # K40: the ProfessorUpdate row is inserted only by a RELEASE build's cached
  # update notice; a tree build inserts none. Whichever this binary is, the
  # picker frame must agree with it. On a release build the notice's cache is
  # professorUpdateCachePath(runtime) = dirname(PFM_CACHE_DB)/update-check.json
  # (pfm/internal/picker/update_row.go) — its presence is the beat's own
  # ground truth for whether the frame SHOULD carry a ⬆ row, so a release
  # build is read for it too, never credited unconditionally.
  version="$(pfm version 2>&1)"
  update_cache="$(dirname "${PFM_CACHE_DB:-$HOME/.local/state/pfm/pfm-cache.db}")/update-check.json"
  if tui_open 120 40 ls -a; then
    frame="$(tui_pane)"
    grep -qF '● ' <<<"$frame" || bad="$bad TUI frame shows no ● live row;"
    grep -qF '↻ ' <<<"$frame" || bad="$bad TUI frame shows no ↻ resume row (-a view);"
    grep -Eq '✦ .*Claude.*Codex' <<<"$frame" ||
      bad="$bad TUI frame shows no merged ✦ Claude/Codex New-chat row: $(one_line "$frame" | cut -c1-240);"
    case "$version" in
      *dev*|*-*)
        grep -qF '⬆' <<<"$frame" && bad="$bad K40 a ⬆ ProfessorUpdate row on a non-release build ($version);"
        held="$held K40(absent-on-$(one_line "$version" | tr ' ' '_'))"
        ;;
      *)
        if [ -s "$update_cache" ]; then
          grep -qF '⬆' <<<"$frame" ||
            bad="$bad K40 $update_cache holds a cached update notice but the frame shows no ⬆ ProfessorUpdate row ($version);"
          held="$held K40(release-build:$(one_line "$version" | tr ' ' '_'),cache-present-row-checked)"
        else
          grep -qF '⬆' <<<"$frame" &&
            bad="$bad K40 the frame shows a ⬆ ProfessorUpdate row but $update_cache holds no cached notice ($version);"
          held="$held K40(release-build:$(one_line "$version" | tr ' ' '_'),cache-absent-row-checked)"
        fi
        ;;
    esac
  else
    bad="$bad K40 $TUI_WHY;"
  fi
  tui_close
  if [ -n "$bad" ]; then fail "$bad — $oc_note"; else pass "row kinds asserted:$held; $oc_note"; fi
fi

# ─── F.08 — the TUI picker, every tab and every key ─────────────────────────

beat F.08-tui-picker
spends none
target_live "$CC"
if requires; then
  bad=""
  primary_before="$(pfm internal primary-get 2>/dev/null)"
  # T2/T3: the noninteractive twins render the same fleet.
  pfm ls --plain >/dev/null 2>&1 || bad="$bad T2 --plain exited non-zero;"
  pfm ls --tsv >/dev/null 2>&1 || bad="$bad T3 --tsv exited non-zero;"
  if ! tui_open 100 30 ls; then
    bad="$bad T1 $TUI_WHY;"
  else
    # T4: Chats is the default tab; T9: no literal fullscreen tab anywhere.
    tui_wait 10 'Chats · fuzzy search and all existing chat controls' || bad="$bad T4 the Chats hint is not on the default frame;"
    grep -qi 'fullscreen' <<<"$(tui_pane)" && bad="$bad T9 a 'fullscreen' view exists on the frame;"
    # T8: tab cycles Chats → Stats → Limits → cosmos → Chats; shift+tab reverses.
    tui_send Tab
    wait_for 10 "tui_has 'CPU' && tui_has 'RAM'" || bad="$bad T8 tab from Chats did not land on Stats (no CPU/RAM header): $(one_line "$(tui_pane | sed -n 1,4p)") $LANE_WAIT_WHY;"
    tui_send Tab
    tui_wait 10 'Limits · live usage windows across every account' || bad="$bad T8 second tab did not land on Limits: $(one_line "$(tui_pane | sed -n 1,4p)");"
    tui_send Tab
    tui_wait 10 'cosmos ·' || bad="$bad T8 third tab did not land on cosmos: $(one_line "$(tui_pane | sed -n 1,4p)");"
    tui_send Tab
    tui_wait 10 'Chats · fuzzy search' || bad="$bad T8 fourth tab did not wrap to Chats: $(one_line "$(tui_pane | sed -n 1,4p)");"
    tui_send BTab
    tui_wait 10 'cosmos ·' || bad="$bad T8 shift+tab from Chats did not land on cosmos: $(one_line "$(tui_pane | sed -n 1,4p)");"
    tui_keys Tab
    # T18: the query editor — type, backspace/⌃H, ⌃W, ⌃U.
    tui_send_text "$CC"
    tui_wait 10 "find › $CC" || bad="$bad T18 typing did not reach the query line: $(one_line "$(tui_pane | grep -F 'find ›')");"
    tui_has "› ● $CC" || bad="$bad T18 the query '$CC' did not select the $CC row: $(one_line "$(tui_selected)");"
    tui_send BSpace
    wait_for 10 "tui_has 'find › F_C' && ! tui_has 'find › $CC'" || bad="$bad T18 backspace did not delete one char: $(one_line "$(tui_pane | grep -F 'find ›')") $LANE_WAIT_WHY;"
    tui_keys C-h
    tui_has 'find › F_' && ! tui_has 'find › F_C' || bad="$bad T18 ⌃H did not delete one char: $(one_line "$(tui_pane | grep -F 'find ›')");"
    tui_type "x y"
    tui_keys C-w
    tui_has 'find › F_x' && ! tui_has 'find › F_x y' || bad="$bad T18 ⌃W did not delete the last word (want 'F_x'): $(one_line "$(tui_pane | grep -F 'find ›')");"
    tui_send C-u
    wait_for 10 "grep -Eq 'find › type project or name +[0-9]+/[0-9]+ visible' <<<\"\$(tui_pane)\"" || bad="$bad T18 ⌃U did not clear the query: $(one_line "$(tui_pane | grep -F 'find ›')") $LANE_WAIT_WHY;"
    # T11: left/right walk the carousel on the selected live row.
    tui_send_text "$CC"
    wait_for 10 "tui_has 'find › $CC' && tui_has '◖▶ open◗'" || bad="$bad T11 the selected row shows no ◖▶ open◗ carousel: $(one_line "$(tui_selected)");"
    tui_send Right
    tui_wait 10 '◖⚡ reboot◗' || bad="$bad T11 right did not move the carousel to reboot: $(one_line "$(tui_selected)");"
    tui_send Right
    tui_wait 10 '◖🕐 1h◗' || bad="$bad T11 second right did not reach 1h: $(one_line "$(tui_selected)");"
    # T16: enter ACTS on the carousel index — index 2 toggles the cache mode in place.
    cache0="$(tui_cache)"
    tui_send Enter
    wait_for 10 "[ -n \"\$(tui_cache)\" ] && [ \"\$(tui_cache)\" != '$cache0' ]" ||
      bad="$bad T16 enter on the 1h action did not flip the header cache: $LANE_WAIT_WHY;"
    cache1="$(tui_cache)"
    [ -n "$cache0" ] && [ -n "$cache1" ] && [ "$cache0" != "$cache1" ] || bad="$bad T16 enter on the 1h action did not flip the header cache ('$cache0' → '$cache1');"
    tui_send Enter
    wait_for 10 "[ \"\$(tui_cache)\" = '$cache0' ]" ||
      bad="$bad T16 a second enter did not flip the cache back: $LANE_WAIT_WHY;"
    [ "$(tui_cache)" = "$cache0" ] || bad="$bad T16 a second enter did not flip the cache back;"
    tui_keys Left; tui_send Left
    tui_wait 10 '◖▶ open◗' || bad="$bad T11 left did not return the carousel to open: $(one_line "$(tui_selected)");"
    # T13: ⌃E toggles the 1h cache from the Chats tab.
    tui_send C-e
    wait_for 10 "[ \"\$(tui_cache)\" != '$cache0' ]" || bad="$bad T13 ⌃E did not flip the header cache ('$cache0'): $LANE_WAIT_WHY;"
    tui_send C-e
    wait_for 10 "[ \"\$(tui_cache)\" = '$cache0' ]" || bad="$bad T13 a second ⌃E did not flip it back: $LANE_WAIT_WHY;"
    # T12: ⌃X kills the selected row NOW — receipt on the status line, store row
    # written. On a LIVE row the picker also sends /exit and kills the pane
    # (internal/kill finisher), so the key is driven on the ended '$GRP' resume
    # row, unhidden for the purpose and left hidden again by the keystroke.
    if [ -z "$GRP_ID" ]; then
      bad="$bad T12 no resume row to drive ⌃X on (F.04 left none);"
    else
      pfm chat unkill "$GRP_ID" >/dev/null 2>&1 || bad="$bad T12 pfm chat unkill $GRP_ID (making the resume row visible) exited non-zero;"
      tui_keys C-u
      wait_for 10 "[ \"\$(all_field '$GRP' 10)\" = false ]" ||
        bad="$bad T12 the unhidden resume row did not appear: $LANE_WAIT_WHY;"
      tui_send_text "F_GRP"
      wait_for 10 "grep -qF '↻ $GRP' <<<\"\$(tui_selected)\"" ||
        bad="$bad T12 the query F_GRP did not select the resume row '$GRP': $LANE_WAIT_WHY;"
      # A GROUP:NAME row renders indented under its group panel: `›   ↻ F_GRP:lane`.
      grep -qF "↻ $GRP" <<<"$(tui_selected)" || bad="$bad T12 the query F_GRP did not select the resume row '$GRP': $(one_line "$(tui_selected)");"
      tui_send C-x
      tui_wait 10 "hidden — $GRP" || bad="$bad T12 ⌃X left no 'hidden — $GRP' receipt: $(one_line "$(tui_pane | grep -F 'find ›')");"
      wait_for 10 "[ \"\$(all_field '$GRP' 10)\" = true ]" ||
        bad="$bad T12 after ⌃X the -a row's killed column is not true: $LANE_WAIT_WHY;"
      [ "$(all_field "$GRP" 10)" = true ] || bad="$bad T12 after ⌃X the -a row's killed column is '$(all_field "$GRP" 10)', want true;"
      tui_keys C-u
      tui_type "$CC"
    fi
    # T17: cursor moves over the whole list.
    tui_keys C-u
    tui_keys Home
    wait_for 10 '[ -n "$(tui_selected)" ]' || bad="$bad T17 home left no selected row: $LANE_WAIT_WHY;"
    first="$(tui_selected)"
    tui_send Down
    wait_for 10 "[ -n \"\$(tui_selected)\" ] && [ \"\$(tui_selected)\" != '$first' ]" || bad="$bad T17 down did not move the cursor ('$first' → '$(tui_selected)'): $LANE_WAIT_WHY;"
    second="$(tui_selected)"
    [ -n "$second" ] && [ "$second" != "$first" ] || bad="$bad T17 down did not move the cursor ('$first' → '$second');"
    tui_keys C-n; tui_keys C-p; tui_send Up
    wait_for 10 "[ \"\$(tui_selected)\" = '$first' ]" || bad="$bad T17 ⌃N ⌃P up did not return to the first row ('$(tui_selected)'): $LANE_WAIT_WHY;"
    tui_send End
    wait_for 10 "[ -n \"\$(tui_selected)\" ] && [ \"\$(tui_selected)\" != '$first' ]" || bad="$bad T17 end did not move to the last row: $LANE_WAIT_WHY;"
    last="$(tui_selected)"
    [ -n "$last" ] && [ "$last" != "$first" ] || bad="$bad T17 end did not move to the last row;"
    tui_send Home
    wait_for 10 "[ \"\$(tui_selected)\" = '$first' ]" || bad="$bad T17 home did not return to the first row: $LANE_WAIT_WHY;"
    # A survival check is true before its key lands: settle, never a wait.
    tui_keys NPage; tui_keys PPage
    [ -n "$(tui_selected)" ] || bad="$bad T17 pgdown/pgup left no selected row;"
    # T19/T5: Stats — every live chat where expected, focus walk, c/m sorts.
    tui_send Tab
    tui_wait 10 'CPU%' || bad="$bad T5 the Stats columns (NAME ENGINE CPU%) did not render;"
    tui_has 'NAME' && tui_has 'ENGINE' && tui_has 'CPU%' || bad="$bad T5 the Stats columns (NAME ENGINE CPU%) did not render;"
    tui_wait 8 "$CC" || bad="$bad T5 $CC never appeared in the Stats rows: $(one_line "$(tui_pane | sed -n 5,12p)");"
    tui_keys Down; tui_keys Down; tui_type c; tui_type m; tui_keys Up
    tui_has 'CPU%' && tui_has 'c CPU sort' || bad="$bad T19 after ↓↓ c m ↑ the Stats frame lost its header or footer;"
    # T20/T6: Limits — a card per seat, scroll keys survive.
    tui_send Tab
    tui_wait 20 "account $SEAT" || bad="$bad T6 no Limits card for 'account $SEAT' in 20s: $(one_line "$(tui_pane | sed -n 4,12p)");"
    tui_keys Down; tui_keys Up; tui_keys NPage; tui_keys PPage; tui_keys Home; tui_keys End
    tui_has 'Limits · live usage windows' || bad="$bad T20 the Limits frame did not survive its scroll keys: $(one_line "$(tui_pane | sed -n 1,4p)");"
    # T21-T27/T7: cosmos — selection, focus, classic sky, scrub, play, now.
    tui_send Tab
    wait_for 10 "tui_has 'cosmos ·' && tui_has edges" ||
      bad="$bad T7 the cosmos census line did not render: $LANE_WAIT_WHY;"
    tui_has 'cosmos ·' && tui_has 'edges' || bad="$bad T7 the cosmos census line did not render;"
    tui_send_text j
    tui_wait 10 'cosmos  ▸ ' || bad="$bad T22 j did not select a star (no ▸ HUD): $(one_line "$(tui_pane | grep -F cosmos | head -2)");"
    tui_type k
    tui_has 'cosmos  ▸ ' || bad="$bad T22 k lost the selection HUD: $(one_line "$(tui_pane | grep -F cosmos | head -2)");"
    tui_send_text s
    tui_wait 10 '⌖' || bad="$bad T24 s did not focus a system (no ⌖): $(one_line "$(tui_pane | grep -F cosmos | head -2)");"
    tui_type o
    tui_send_text s
    tui_wait 10 'the classic sky has no systems to focus' || bad="$bad T21 o (classic sky) then s did not name the classic sky: $(one_line "$(tui_pane | grep -F cosmos | head -2)");"
    tui_type o
    tui_send_text '['
    wait_for 10 "tui_has '⟲' && tui_has '5m'" || bad="$bad T25 [ did not scrub 5 minutes back (no ⟲ … −5m chip): $(one_line "$(tui_pane | grep -F '⟲')") $LANE_WAIT_WHY;"
    tui_send_text '{'
    wait_for 10 "grep -qE '⟲.*−1h' <<<\"\$(tui_pane)\"" || bad="$bad T25 { did not scrub an hour back: $(one_line "$(tui_pane | grep -F '⟲')") $LANE_WAIT_WHY;"
    # A paused playhead keeps its instant while now moves on, so ] } only land
    # back on the moment [ was pressed, seconds behind now (⟲ … −0m); space
    # there resumes a seconds-long replay that 60× plays out in ~70ms, faster
    # than any poll. The second } lands past now, which returns to live, and
    # space from live replays the last hour — sixty seconds of ▸▸.
    tui_type ']'; tui_type '}'; tui_type '}'
    tui_has '⟲' && bad="$bad T25 ] } } did not return to now (⟲ chip still shown): $(one_line "$(tui_pane | grep -F '⟲')");"
    tmux -S "$TUI_SOCK" send-keys -t tui Space
    space_frame="" played=0
    for attempt in 1 2 3 4 5 6 7 8 9 10; do
      space_frame="$(tui_pane)"
      if grep -qF '▸▸' <<<"$space_frame"; then played=1; break; fi
      # POLL-STEP: replay paints the ▸▸ chip before it ends.
      sleep 0.1
    done
    if [ "$played" -ne 1 ]; then
      printf 'T26 frame after Space:\n%s\n' "$space_frame" >>"$LANE_OUT_DIR/F.log"
      bad="$bad T26 space did not start replay (no ▸▸ chip): $(one_line "$(printf '%s' "$space_frame" | grep -F cosmos | head -3)");"
    fi
    tui_type n
    wait_for 10 "! tui_has '▸▸' && ! tui_has '⟲'" ||
      bad="$bad T27 n did not stop replay and return to now: $LANE_WAIT_WHY;"
    tui_has '▸▸' && bad="$bad T27 n did not stop the replay;"
    tui_has '⟲' && bad="$bad T27 n did not return to now (⟲ chip still shown);"
    # T23: enter opens the selected star — the picker leaves the pane to attach,
    # or refuses by name when the star is not a running chat. Either is the key's contract.
    tui_type j
    hud="$(tui_pane | grep -F 'cosmos  ▸' | head -1)"
    tui_send Enter
    wait_for 15 '[ "$(tui_cmd)" != pfm ] || tui_has "enter needs a live chat" || tui_has "enter refused" || tui_has "nothing selected"' ||
      bad="$bad T23 enter on the cosmos selection neither opened nor refused by name: $LANE_WAIT_WHY;"
    if [ "$(tui_cmd)" != pfm ]; then
      t23="enter on '$(one_line "$hud" | cut -c1-60)' left the picker to open it"
    elif tui_has 'enter needs a live chat' || tui_has 'enter refused' || tui_has 'nothing selected'; then
      t23="enter refused by name: $(one_line "$(tui_pane | grep -F 'cosmos  ' | head -1)" | cut -c1-100)"
    else
      bad="$bad T23 enter on the cosmos selection neither opened nor refused by name: $(one_line "$(tui_pane | grep -F cosmos | head -2)");"
      t23="?"
    fi
    tui_close
  fi
  # T10: esc and ⌃C cancel; a pending ⌃S account switch is NOT written on cancel.
  for key in Escape C-c; do
    if tui_open 100 30 ls; then
      tui_type "$CC"
      tui_keys C-s
      tui_keys "$key"
      tui_left 10 || bad="$bad T10 $key did not close the picker;"
    else
      bad="$bad T10 $TUI_WHY;"
    fi
  done
  [ "$(pfm internal primary-get 2>/dev/null)" = "$primary_before" ] ||
    bad="$bad T10/T14 a ⌃S cancelled with esc/⌃C was written (primary $primary_before → $(pfm internal primary-get 2>/dev/null));"
  # T14: ⌃S cycles the selected row's account through the configured roster.
  if tui_open 100 30 ls; then
    tui_type "$CC"
    acct0="$(tui_account)"
    tui_send C-s
    want="$(printf '%s\n' $ACCOUNT_IDS | awk -v cur="$acct0" 'NR == 1 { first = $1 } { if (hit) { print; printed = 1; exit } if ($1 == cur) hit = 1 } END { if (hit && !printed) print first }')"
    wait_for 10 "[ \"\$(tui_account)\" = '$want' ]" ||
      bad="$bad T14 ⌃S did not move the header account to $want: $LANE_WAIT_WHY;"
    acct1="$(tui_account)"
    [ -n "$acct0" ] && [ "$acct1" = "$want" ] || bad="$bad T14 ⌃S moved the header account $acct0 → $acct1, want $want (roster: $ACCOUNT_IDS);"
    tui_keys Escape
    tui_left 10
  else
    bad="$bad T14 $TUI_WHY;"
  fi
  # T16: enter on the open action attaches — the picker execs into a client of the chat's server.
  cc_sock_path="$(socket_path "$(live_field "$CC" 11)")"
  if tui_open 100 30 ls; then
    tui_type "$CC"
    tui_keys Enter
    if ! tui_left 20; then
      bad="$bad T16 enter on '$CC' did not leave the picker;"
    else
      wait_for 10 "[ -n \"\$(tmux -S '$cc_sock_path' list-clients 2>/dev/null)\" ]" ||
        bad="$bad T16 enter left the picker but no client is attached to $CC's server $cc_sock_path: $LANE_WAIT_WHY;"
      clients="$(tmux -S "$cc_sock_path" list-clients 2>&1)"
      [ -n "$clients" ] || bad="$bad T16 enter left the picker but no client is attached to $CC's server $cc_sock_path;"
    fi
    tui_close
    wait_for 10 "[ -z \"\$(tmux -S '$cc_sock_path' list-clients 2>/dev/null)\" ]" ||
      bad="$bad T16 the attach client survived the picker server's teardown: $LANE_WAIT_WHY;"
    [ -z "$(tmux -S "$cc_sock_path" list-clients 2>/dev/null)" ] || bad="$bad T16 the attach client survived the picker server's teardown;"
    live_chat "$CC" || bad="$bad T16 $CC lost its live row across the attach/detach;"
  else
    bad="$bad T16 $TUI_WHY;"
  fi
  # T15: ⌃O reboots the selected live seat — same session id, a fresh server.
  reboot_note=""
  if live_chat "$CC" && tui_open 100 30 ls; then
    id0="$(live_field "$CC" 2)"
    sock0="$(live_field "$CC" 11)"
    tui_type "$CC"
    tui_keys C-o
    # Polled by SESSION ID, in this beat's own loop: the reboot kills the old
    # server and the fresh one boots for some seconds with no live row under
    # the name — the library's waits would read that gap as a dead anchor.
    sock1="" deadline=$(( $(_lane_now) + 240 ))
    while [ "$(_lane_now)" -lt "$deadline" ]; do
      sock1="$(pfm ls --tsv 2>/dev/null | awk -F'\t' -v id="$id0" 'NR > 1 && $1 ~ /^live-/ && $2 == id { print $11; exit }')"
      [ -n "$sock1" ] && [ "$sock1" != "$sock0" ] && break
      sock1=""
      # POLL-STEP: the rebooted session ID appears on a fresh socket.
      sleep 0.5
    done
    if [ -z "$sock1" ]; then
      bad="$bad T15 ⌃O: no live row for session $id0 on a fresh socket in 240s (rows for the id now: $(one_line "$(pfm ls -a --tsv 2>/dev/null | awk -F'\t' -v id="$id0" 'NR > 1 && $2 == id { print $1 ":" $11 }')"));"
    else
      name1="$(socket_field "$sock1" 5)"
      reboot_note="rebooted in place: session $id0 moved ${sock0##*/} → ${sock1##*/}"
      if [ "$name1" != "$CC" ]; then
        # OBSERVATION, not a verdict: the reborn server's row carries '$name1'.
        # Renamed back so every later beat, which addresses the chat by name, resolves.
        pfm chat name "$id0" "$CC" >/dev/null 2>&1
        LANE_ANCHOR="sock:$sock1" wait_for 10 "[ \"\$(socket_field '$sock1' 5)\" = '$CC' ]" ||
          bad="$bad T15 the rebooted row could not be renamed back to $CC: $LANE_WAIT_WHY;"
        reboot_note="$reboot_note · observed: the resumed row was named '$name1', renamed back to $CC"
        [ "$(socket_field "$sock1" 5)" = "$CC" ] || bad="$bad T15 the rebooted row could not be renamed back to $CC (reads '$(socket_field "$sock1" 5)');"
      fi
    fi
    tui_close
    wait_for 10 "live_chat '$CC'" || bad="$bad T15 $CC has no live row after the reboot: $LANE_WAIT_WHY;"
    live_chat "$CC" || bad="$bad T15 $CC has no live row after the reboot;"
  else
    bad="$bad T15 ⌃O: ${TUI_WHY:-$CC not live};"
  fi
  tui_close
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "picker entry, four tabs both ways, no fullscreen view, query editing, carousel, enter-act (1h), ⌃E, ⌃X (hidden — $GRP), cursor keys, Stats rows + sorts, Limits card for seat $SEAT + scroll, cosmos select/focus/classic/scrub/play/now, T23 $t23, esc/⌃C drop a pending ⌃S, ⌃S cycles $acct0→$acct1, enter attached a client, ⌃O $reboot_note"
  fi
fi

# ─── F.09 — the golden shapes against the live pane ─────────────────────────

beat F.09-tui-golden
spends none
target_live "$CC"
if requires; then
  bad=""
  golden="$GOLDEN/ui_80.ansi"
  if [ ! -f "$golden" ]; then
    fail "the golden frame $golden is not readable in this container — nothing to compare the live pane against"
  else
    g_lines="$(grep -c . "$golden")"
    if ! tui_open 80 "$g_lines" ls; then
      fail "T30 $TUI_WHY"
    else
      tui_wait 10 'Chats · fuzzy search and all existing chat controls' ||
        bad="$bad T30 the golden frame's Chats hint did not paint;"
      frame="$(tui_pane)"
      n_frame="$(grep -c '' <<<"$frame")"
      # T28: the STATIC lines of the pinned frame, verbatim — tabs (up to the
      # sky widget), the Chats hint, both footer lines. The dynamic lines are
      # held to the golden's shape: header format, and three counts that agree.
      g_tabs="$(golden_line "$golden" 2 | sed 's/tab\/shift+tab.*/tab\/shift+tab/')"
      l_tabs="$(printf '%s\n' "$frame" | sed -n 2p | sed 's/tab\/shift+tab.*/tab\/shift+tab/')"
      [ "$g_tabs" = "$l_tabs" ] || bad="$bad T28 tabs line differs: golden '$g_tabs' vs live '$l_tabs';"
      g_hint="$(golden_line "$golden" 3 | sed 's/controls.*/controls/')"
      l_hint="$(printf '%s\n' "$frame" | sed -n 3p | sed 's/controls.*/controls/')"
      [ "$g_hint" = "$l_hint" ] || bad="$bad T28 Chats hint differs: golden '$g_hint' vs live '$l_hint';"
      for n in $((g_lines - 1)) "$g_lines"; do
        g_foot="$(golden_line "$golden" "$n")"
        l_foot="$(printf '%s\n' "$frame" | sed -n "${n}p" | sed 's/ *$//')"
        [ "$g_foot" = "$l_foot" ] || bad="$bad T28 footer line $n differs: golden '$g_foot' vs live '$l_foot';"
      done
      grep -Eq ' pfm  .+ account [0-9]+ · (⚡ 1h|🪫 5m) · [0-9]+ rows · [0-9]+ hidden · [0-9]+ empty' <<<"$(printf '%s\n' "$frame" | sed -n 1p)" ||
        bad="$bad T28 header line is not the pinned shape: '$(printf '%s\n' "$frame" | sed -n 1p)';"
      rows_hdr="$(printf '%s\n' "$frame" | sed -n 1p | sed -n 's/.* · \([0-9]*\) rows · .*/\1/p')"
      rows_fleet="$(printf '%s\n' "$frame" | sed -n 's/.*╭─ fleet \([0-9]*\) .*/\1/p' | head -1)"
      rows_vis="$(printf '%s\n' "$frame" | sed -n 's/.* \([0-9]*\)\/\([0-9]*\) visible.*/\1 \2/p' | head -1)"
      # `fleet N` and `N/N visible` are one count (the filtered rows); the header's
      # `rows` counts every row, the New rows the picker merges into one included.
      [ -n "$rows_fleet" ] && [ "$rows_vis" = "$rows_fleet $rows_fleet" ] && [ "${rows_hdr:-0}" -ge "$rows_fleet" ] ||
        bad="$bad T28 the frame's counts disagree: header rows '$rows_hdr', fleet '$rows_fleet', visible '$rows_vis';"
      # T29 (live-scale analog of the synthetic stress frame — the 5,000-row
      # snapshot stays Tier U): every row of this fleet fits the frame with no
      # wrapped line, so the pane holds exactly the golden's line count.
      [ "$n_frame" -eq "$g_lines" ] || bad="$bad T29 the live frame is $n_frame lines for a $g_lines-line pane — a row overflowed or the frame is short;"
      # T36: pfm's own palettes — the header background bytes of `default`
      # and `tokyo-night`, each read from internal/theme/theme.go, each on its frame.
      hexes="$(grep -oE 'HeaderBg: *"#[0-9a-fA-F]{6}"' "$THEME_SRC" 2>/dev/null | grep -oE '#[0-9a-fA-F]{6}')"
      def_hex="$(printf '%s\n' "$hexes" | sed -n 1p)"
      tok_hex="$(printf '%s\n' "$hexes" | sed -n 2p)"
      if [ -z "$def_hex" ] || [ -z "$tok_hex" ]; then
        bad="$bad T36 the two HeaderBg palette values could not be read from $THEME_SRC;"
      else
        def_sgr="48;2;$(hex_to_sgr "$def_hex")"
        tok_sgr="48;2;$(hex_to_sgr "$tok_hex")"
        grep -qF "$def_sgr" <<<"$(tui_pane_e)" || bad="$bad T36 the default palette's header background $def_hex ($def_sgr) is not in the live escapes;"
        jq '.theme = "tokyo-night"' "$CONFIG" >/tmp/f-tokyo.json 2>/dev/null
        if tui_open 80 "$g_lines" --config /tmp/f-tokyo.json ls; then
          wait_for 10 "grep -qF '$tok_sgr' <<<\"\$(tui_pane_e)\"" ||
            bad="$bad T36 the tokyo-night header background $tok_hex ($tok_sgr) is not in the live escapes: $LANE_WAIT_WHY;"
          grep -qF "$tok_sgr" <<<"$(tui_pane_e)" || bad="$bad T36 the tokyo-night header background $tok_hex ($tok_sgr) is not in the live escapes under --config theme=tokyo-night;"
          grep -qF "$def_sgr" <<<"$(tui_pane_e)" && bad="$bad T36 the tokyo-night frame still paints the default header background $def_hex;"
        else
          bad="$bad T36 tokyo-night picker: $TUI_WHY;"
        fi
      fi
      tui_close
      if [ -n "$bad" ]; then fail "$bad"; else
        pass "80×$g_lines frame: tabs, hint and footer lines verbatim from $(basename "$golden"), header shape held, fleet $rows_fleet = $rows_vis visible under $rows_hdr rows, no overflow; palettes default $def_hex and tokyo-night $tok_hex each on their frame"
      fi
    fi
  fi
fi

# ─── F.10 — N parallel chat new against the fleetdb's atomic writers ───────

beat F.10-concurrent-new
spends "cc:$SEAT"
target "$CC"
bad=""
rm -f /tmp/f-par.*.rc
for i in 1 2 3; do
  (
    pfm chat new --name "F_PAR_$i" --engine cc --account "$SEAT" --cwd /work/atlas --model sonnet --effort low \
      "You are F_PAR_$i. Reply with one word: ready. Then wait." >"/tmp/f-par.$i.out" 2>&1
    echo $? >"/tmp/f-par.$i.rc"
  ) &
done
wait
for i in 1 2 3; do
  rc="$(cat "/tmp/f-par.$i.rc" 2>/dev/null || echo missing)"
  [ "$rc" = 0 ] || bad="$bad F_PAR_$i exited $rc: $(one_line "$(cat "/tmp/f-par.$i.out" 2>/dev/null)");"
  wait_for 30 "live_chat 'F_PAR_$i'" || bad="$bad no live row for F_PAR_$i: $LANE_WAIT_WHY;"
  live_chat "F_PAR_$i" || bad="$bad no live row for F_PAR_$i;"
done
socks="$(for i in 1 2 3; do live_field "F_PAR_$i" 11; done | grep -c .)"
uniq_socks="$(for i in 1 2 3; do live_field "F_PAR_$i" 11; done | sort -u | grep -c .)"
[ "$socks" -eq 3 ] && [ "$uniq_socks" -eq 3 ] || bad="$bad $socks live sockets, $uniq_socks distinct (want 3 and 3);"
pfm ls --tsv >/dev/null 2>&1 || bad="$bad pfm ls --tsv exits non-zero after the parallel writes (pfm.db unreadable?);"
# The three have served: end them and hide the resume rows they leave.
for i in 1 2 3; do
  live_chat "F_PAR_$i" || continue
  id="$(live_field "F_PAR_$i" 2)"
  pfm chat end "F_PAR_$i" >/dev/null 2>&1 || bad="$bad pfm chat end F_PAR_$i failed;"
  LANE_ANCHOR= wait_for 10 "pfm ls -a --tsv 2>/dev/null | awk -F '\t' -v id='$id' 'NR > 1 && \$2 == id && \$1 ~ /^resume-/ { found = 1 } END { exit !found }'" ||
    bad="$bad F_PAR_$i has no resume row after end: $LANE_WAIT_WHY;"
  [ -n "$id" ] && pfm chat kill "$id" >/dev/null 2>&1
done
if [ -n "$bad" ]; then fail "$bad"; else
  pass "three parallel chat new landed three live rows on three distinct sockets, pfm.db read back clean; the three ended and their resume rows hidden"
fi

# ─── F.11 — the idle-detection state machine ────────────────────────────────

beat F.11-idle-states
spends none
target_live "$CC"
expect-log 'no chat named'
if requires; then
  bad="" held=""
  # L7: an absent chat is an explicit not-found value, never an empty success.
  out="$(pfm chat status NO_SUCH_CHAT_F --json 2>/dev/null)"
  rc=$?
  [ "$rc" -eq 4 ] && [ "$(printf '%s' "$out" | jq -r .state 2>/dev/null)" = not-found ] && held="$held L7" ||
    bad="$bad L7 status on an absent chat exited $rc with state '$(printf '%s' "$out" | jq -r .state 2>/dev/null)' (want 4 + not-found);"
  # dead: the ended '$GRP' is a resumable row with no server.
  if [ -n "$GRP_ID" ]; then
    pfm chat unkill "$GRP_ID" >/dev/null 2>&1 # a hidden row is unhidden for the read, then hidden again
    wait_for 10 "grep -q dead <<<\"\$(pfm chat status '$GRP_ID' 2>/dev/null || true)\"" ||
      bad="$bad an ended chat's status did not report dead after unkill: $LANE_WAIT_WHY;"
    out="$(pfm chat status "$GRP_ID" 2>/dev/null)"
    rc=$?
    pfm chat kill "$GRP_ID" >/dev/null 2>&1
    [ "$rc" -eq 3 ] && grep -q 'dead' <<<"$out" && held="$held dead" ||
      bad="$bad an ended chat's status exited $rc / '$(one_line "$out")' (want 3 + dead);"
  fi
  # L4 idle: the newest record is the assistant's word; L6: idle_seconds counts only then.
  pfm chat inject --allow-unsigned "$CC" 'IDLE-PROBE' >/dev/null 2>&1 ||
    bad="$bad the idle-making inject was refused;"
  if wait_prompt "$CC" IDLE-PROBE 30; then
    wait_for 10 "pfm chat status '$CC' --json 2>/dev/null | jq -e '.state == \"idle\" and .idle_seconds >= 1' >/dev/null" ||
      bad="$bad L4/L6 after the assistant answered, state did not reach idle with idle_seconds ≥ 1: $LANE_WAIT_WHY;"
    json="$(pfm chat status "$CC" --json 2>/dev/null)"
    state="$(printf '%s' "$json" | jq -r .state 2>/dev/null)"
    idle_s="$(printf '%s' "$json" | jq -r .idle_seconds 2>/dev/null)"
    [ "$state" = idle ] && held="$held L4(idle)" || bad="$bad L4 after the assistant answered, state is '$state' (want idle);"
    [ "$state" = idle ] && [ "${idle_s:-0}" -ge 1 ] && held="$held L6(idle_seconds=$idle_s)" ||
      bad="$bad L6 idle_seconds is '$idle_s' while idle (want ≥ 1);"
    # L1: the verdict is derived from transcript + socket — both are named in the JSON, no pane field is.
    [ -n "$(printf '%s' "$json" | jq -r '.session_id // empty')" ] && [ -n "$(printf '%s' "$json" | jq -r '.socket // empty')" ] &&
      held="$held L1(session_id+socket)" || bad="$bad L1 the status JSON names no session_id/socket to derive from: $(one_line "$json");"
  else
    bad="$bad the IDLE-PROBE user record was not read: ${LANE_WAIT_WHY:-no wait reason recorded};"
  fi
  # L4 working: a turn in flight — the newest record owes an answer; L6: idle_seconds is 0.
  hold_gate="$LANE_OUT_DIR/f-idle-hold"
  : >"$hold_gate"
  pfm chat inject --allow-unsigned "$CC" \
    "F-HOLD-PROBE $(mock_steps "[{\"type\":\"hold\",\"until_gone\":\"$hold_gate\"}]")" >/dev/null 2>&1 ||
    bad="$bad the busy-making inject was refused;"
  wait_prompt "$CC" F-HOLD-PROBE 30 || bad="$bad the hold prompt was not read: ${LANE_WAIT_WHY:-unknown};"
  wait_for 10 "pfm chat status '$CC' --json 2>/dev/null | jq -e '.state == \"working\" and .idle_seconds == 0' >/dev/null" ||
    bad="$bad L4/L6 mid-turn status did not become working with idle_seconds 0: $LANE_WAIT_WHY;"
  json="$(pfm chat status "$CC" --json 2>/dev/null)"
  state="$(printf '%s' "$json" | jq -r .state 2>/dev/null)"
  idle_s="$(printf '%s' "$json" | jq -r .idle_seconds 2>/dev/null)"
  [ "$state" = working ] && held="$held L4(working)" || bad="$bad L4 mid-turn state is '$state' (want working);"
  [ "$idle_s" = 0 ] && held="$held L6(working→0)" || bad="$bad L6 idle_seconds is '$idle_s' while working (want 0);"
  rm -f "$hold_gate"
  wait_for 30 "pfm chat status '$CC' --json 2>/dev/null | jq -e '.state == \"idle\"' >/dev/null" ||
    bad="$bad the chat never returned to idle after the hold gate went: ${LANE_WAIT_WHY:-no wait reason recorded};"
  boot="$(boot_scenario)" || bad="$bad L2 could not write boot scenario;"
  if [ -n "$boot" ] && boot_start "$boot"; then
    if wait_for 30 "row_kind_socket booting '$BOOT_SOCK'"; then
      wait_for 10 "pfm chat status '$BOOT_SOCK' --json 2>/dev/null | jq -e '.state == \"idle\" and .idle_seconds == 0' >/dev/null" ||
        bad="$bad L2 booting $BOOT_SOCK status did not reach idle with idle_seconds 0: $LANE_WAIT_WHY;"
      json="$(pfm chat status "$BOOT_SOCK" --json 2>&1)"
      state="$(printf '%s' "$json" | jq -r .state 2>/dev/null)"
      idle_s="$(printf '%s' "$json" | jq -r .idle_seconds 2>/dev/null)"
      # A promptless pane with no busy footer reads idle, even before a transcript exists.
      [ "$state" = idle ] && [ "$idle_s" = 0 ] && held="$held L2(idle)" ||
        bad="$bad L2 booting $BOOT_SOCK status state=$state idle_seconds=$idle_s: $(one_line "$json"); pane tail: $(one_line "$(pane_last_two "$BOOT_SOCK")");"
    else
      bad="$bad L2 no booting row for $BOOT_SOCK: ${LANE_WAIT_WHY:-unknown};"
    fi
    boot_stop || bad="$bad L2 could not kill $BOOT_SOCK;"
  else
    bad="$bad L2 ${BOOT_WHY:-boot scenario missing};"
  fi
  notx="$LANE_OUT_DIR/f-no-transcript.json"
  jq '.no_transcript = true' "$MOCK_ENGINE_SCENARIO" >"$notx" || bad="$bad L3 could not write no-transcript scenario;"
  out="$(MOCK_ENGINE_SCENARIO="$notx" pfm chat new --name F_NOTX --engine cc --account "$SEAT" --cwd "$CWD" 2>&1)"
  rc=$?
  notx_sock="$(printf '%s\n' "$out" | awk -F'\t' '$1 == "cc" {print $3; exit}')"
  if [ "$rc" -eq 0 ] && [ -n "$notx_sock" ] && wait_for 30 "row_kind_socket live-claude '$notx_sock'"; then
    wait_for 10 "pfm chat status '$notx_sock' --json 2>/dev/null | jq -e '.state == \"idle\" and .idle_seconds == 0' >/dev/null" ||
      bad="$bad L3 F_NOTX ($notx_sock) status did not reach idle with idle_seconds 0: $LANE_WAIT_WHY;"
    json="$(pfm chat status "$notx_sock" --json 2>&1)"
    state="$(printf '%s' "$json" | jq -r .state 2>/dev/null)"
    idle_s="$(printf '%s' "$json" | jq -r .idle_seconds 2>/dev/null)"
    # A promptless pane with no busy footer reads idle, even without a transcript.
    [ "$state" = idle ] && [ "$idle_s" = 0 ] && held="$held L3(idle)" ||
      bad="$bad L3 F_NOTX ($notx_sock) status state=$state idle_seconds=$idle_s: $(one_line "$json"); pane tail: $(one_line "$(pane_last_two "$notx_sock")");"
    pfm chat end "$notx_sock" >/dev/null 2>&1 || bad="$bad L3 pfm chat end $notx_sock failed;"
  else
    bad="$bad L3 F_NOTX launch exited $rc without a live-claude row on $notx_sock: $(one_line "$out") ${LANE_WAIT_WHY:-};"
  fi
  if [ -n "$bad" ]; then
    fail "$bad"
  else
    pass "idle states asserted:$held"
  fi
fi

# ─── F.12 — reap must spare a chat with a running sub-agent ──────────────────

beat F.12-idle-down-subagent
spends "cc:$SEAT"
target_live "$CC"
if requires; then
  bad=""
  cc_sock="$(sock_base "$CC")"
  pfm chat inject --allow-unsigned "$CC" \
    "F-SUBAGENT-PROBE $(mock_steps '[{"type":"background_agent","name":"lane-f"},{"type":"turn","busy_ms":30000}]')" >/dev/null 2>&1 ||
    bad="$bad the sub-agent stimulus could not be delivered;"
  if wait_for 48 "pfm chat status '$CC' --json 2>/dev/null | jq -e '.state == \"working\"' >/dev/null"; then
    # pfm's own idle-down consumer: reap classifies the chat while its pane is
    # quiet and its sub-agent works — it must be SPARED, never in the reap group.
    report="$(pfm reap --json --horizon 1s --busy-recent 60 2>/dev/null)"
    in_reap="$(printf '%s' "$report" | jq -r '.reap[].socket' 2>/dev/null | grep -cF "$cc_sock")"
    in_spared="$(printf '%s' "$report" | jq -r '.spared[].socket' 2>/dev/null | grep -cF "$cc_sock")"
    if [ "${in_reap:-0}" -gt 0 ]; then
      bad="$bad pfm reap --horizon 1s would REAP $CC ($cc_sock) while its sub-agent runs;"
    elif [ "${in_spared:-0}" -eq 0 ]; then
      bad="$bad pfm reap classified $CC ($cc_sock) neither reaped nor spared — it did not judge the chat at all: $(one_line "$report" | cut -c1-200);"
    fi
  else
    bad="$bad status never reported working after the sub-agent stimulus (L5 sidechain override unobserved): $LANE_WAIT_WHY;"
  fi
  wait_for 50 "pfm chat status '$CC' --json 2>/dev/null | jq -e '.state == \"idle\"' >/dev/null" ||
    bad="$bad the chat never returned to idle after the sub-agent: ${LANE_WAIT_WHY:-no wait reason recorded};"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "working under a live sub-agent; pfm reap --horizon 1s spared $cc_sock; then idle"
  fi
fi

# ─── F.13 — name-sync converges the window name ─────────────────────────────

beat F.13-name-sync
spends none
target_live "$CC"
if requires; then
  bad=""
  sock="$(socket_path "$(live_field "$CC" 11)")"
  win="$(tmux -S "$sock" list-windows -F '#{window_id}' 2>/dev/null | head -1)"
  drift() { tmux -S "$sock" rename-window -t "$win" LANE-DRIFT 2>&1; }
  window_name() { tmux -S "$sock" list-windows -F '#{window_name}' 2>/dev/null | head -1; }
  # L40: a drifted window is planned by the dry run (the default) and converged by --apply.
  drift || bad="$bad the drift rename-window failed on $sock;"
  plan="$(pfm name-sync 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] || bad="$bad L40 pfm name-sync (dry run) exited $rc: $(one_line "$plan");"
  grep -q "would rename ${sock##*/} .*LANE-DRIFT -> " <<<"$plan" || bad="$bad L40 the dry run did not plan the drifted window: $(one_line "$plan");"
  grep -Eq 'windows planned: [1-9]' <<<"$plan" || bad="$bad L40 'windows planned' is not ≥ 1: $(one_line "$plan");"
  [ "$(window_name)" = LANE-DRIFT ] || bad="$bad L40 the dry run RENAMED the window (reads '$(window_name)');"
  dry="$(pfm name-sync --dry-run 2>&1)"
  grep -q 'dry run is the default' <<<"$dry" || bad="$bad L40 --dry-run did not announce itself as the default alias;"
  applied="$(pfm name-sync --apply 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] || bad="$bad L41 pfm name-sync --apply exited $rc: $(one_line "$applied");"
  grep -q "renamed ${sock##*/} .*LANE-DRIFT -> " <<<"$applied" || bad="$bad L41 --apply did not report the rename: $(one_line "$applied");"
  grep -Eq 'windows converged: [1-9]' <<<"$applied" || bad="$bad L41 'windows converged' is not ≥ 1 (the rename was not re-verified): $(one_line "$applied");"
  grep -q 'tmux options converged:' <<<"$applied" || bad="$bad L41 --apply did not report the tmux options pass;"
  case "$(window_name)" in *"$CC"*) ;; *) bad="$bad L41 after --apply the window reads '$(window_name)', not the label;" ;; esac
  # L42: one writer by design — two --apply passes racing on one drift both
  # finish, the window converges once, and a third dry run has nothing left to plan.
  drift
  pfm name-sync --apply >/tmp/f-ns.a 2>&1 &
  pa=$!
  pfm name-sync --apply >/tmp/f-ns.b 2>&1 &
  pb=$!
  wait "$pa"; ra=$?
  wait "$pb"; rb=$?
  [ "$ra" -eq 0 ] && [ "$rb" -eq 0 ] || bad="$bad L42 concurrent --apply passes exited $ra and $rb: $(one_line "$(cat /tmp/f-ns.a /tmp/f-ns.b)" | cut -c1-200);"
  case "$(window_name)" in *"$CC"*) ;; *) bad="$bad L42 after two racing passes the window reads '$(window_name)';" ;; esac
  after="$(pfm name-sync 2>&1)"
  grep -q 'windows planned: 0' <<<"$after" || bad="$bad L42 a dry run after the race still plans work: $(one_line "$after");"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "drift LANE-DRIFT planned by the dry run, converged and re-verified by --apply (window '$(window_name)'), two racing --apply passes left one converged name and nothing to plan"
  fi
fi

# ─── F.14 — kill-storm ──────────────────────────────────────────────────────

beat F.14-kill-storm
spends "cc:$SEAT+cx"
target "$CC"
n_storm="$(live_storm_rows | grep -c .)"
if [ "$n_storm" -eq 0 ]; then
  blocked F.05-storm "no live STORM_<n> rows to tear down (the storm never came up)"
else
  bad=""
  others_before="$(pfm ls --tsv 2>/dev/null | awk -F'\t' 'NR > 1 && $1 ~ /^live-/ && $5 !~ /^STORM_[0-9]+$/' | wc -l | tr -d ' ')"
  out="$(lane_storm_kill 2>&1)"
  rc=$?
  closing="$(grep '^kill-storm:' <<<"$out" | tail -1)"
  [ "$rc" -eq 0 ] || bad="$bad lane_storm_kill exited $rc: $(one_line "$out" | cut -c1-200);"
  grep -q 'STORM rows left: 0' <<<"$closing" || bad="$bad the closing line does not read 'STORM rows left: 0': $(one_line "$closing");"
  [ "$(live_storm_rows | grep -c .)" -eq 0 ] || bad="$bad live STORM rows remain in pfm ls --tsv: $(one_line "$(live_storm_rows | cut -f5)");"
  wait_for 10 "! pfm ls --tsv 2>/dev/null | awk -F '\t' 'NR > 1 && \$5 ~ /^STORM_[0-9]+$/ { f = 1 } END { exit(f ? 0 : 1) }'" ||
    bad="$bad ended STORM rows are still visible in the default view (kill-storm hides them): $LANE_WAIT_WHY;"
  pfm ls --tsv 2>/dev/null | awk -F'\t' 'NR > 1 && $5 ~ /^STORM_[0-9]+$/ { f = 1 } END { exit(f ? 0 : 1) }' &&
    bad="$bad ended STORM rows are still visible in the default view (kill-storm hides them);"
  others_after="$(pfm ls --tsv 2>/dev/null | awk -F'\t' 'NR > 1 && $1 ~ /^live-/ && $5 !~ /^STORM_[0-9]+$/' | wc -l | tr -d ' ')"
  [ "$others_before" = "$others_after" ] || bad="$bad other live rows changed $others_before → $others_after;"
  live_chat "$E1_CHAT" || bad="$bad $E1_CHAT is gone after kill-storm;"
  live_chat "$CC" || bad="$bad $CC is gone after kill-storm;"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "$n_storm storm chats ended and hidden ('$(one_line "$closing")'); other live rows $others_before → $others_after; $E1_CHAT and $CC still live"
  fi
fi

# ─── F.15 — fleet-wide addressing and search ────────────────────────────────

beat F.15-addressing
spends none
target_live "$CC"
expect-log 'no chat named'
expect-log 'no tmux socket'
if requires; then
  bad=""
  cc_id="$(live_field "$CC" 2)"
  cc_sock="$(sock_base "$CC")"
  # C58: a distinctive scripted answer makes find's match unambiguous.
  pfm chat inject --allow-unsigned "$CC" "$(mock_steps '[{"type":"turn","reply":"F-FIND-PROBE"}]')" >/dev/null 2>&1 ||
    bad="$bad C58 could not inject the distinctive find probe;"
  wait_last "$CC" F-FIND-PROBE 30 || bad="$bad C58 the find probe was not read: ${LANE_WAIT_WHY:-unknown};"
  excerpt=/tmp/f-excerpt.txt
  pfm chat last "$CC" 2>/dev/null | tail -3 >"$excerpt"
  path=""
  if [ ! -s "$excerpt" ]; then
    bad="$bad C58 the chat's last answer is empty, so no excerpt could be written;"
  else
    wait_for 30 "pfm chat find '$excerpt' 2>/dev/null | awk -F '\t' -v id='$cc_id' '\$1 == id { found = 1 } END { exit !found }'" ||
      bad="$bad C58 find did not match session $cc_id: $LANE_WAIT_WHY;"
    found="$(pfm chat find "$excerpt" 2>/dev/null)"
    rc=$?
    [ "$rc" -eq 0 ] || bad="$bad C58 chat find exited $rc;"
    [ "$(printf '%s' "$found" | cut -f1)" = "$cc_id" ] || bad="$bad C58 find matched '$(one_line "$found")', not session $cc_id;"
    path="$(printf '%s' "$found" | cut -f2)"
  fi
  # C59: save appends the transcript plus an environment snapshot to a target file.
  if [ -n "$path" ] && [ -f "$path" ]; then
    rm -f /tmp/f-save.md
    out="$(cd "$CWD" && pfm chat save /tmp/f-save.md "$path" 2>&1)"
    rc=$?
    [ "$rc" -eq 0 ] && grep -q 'Appended transcript (.* user records) + env snapshot -> /tmp/f-save.md' <<<"$out" ||
      bad="$bad C59 chat save exited $rc: $(one_line "$out");"
    grep -q '# FULL TRANSCRIPT' /tmp/f-save.md 2>/dev/null || bad="$bad C59 the saved file carries no '# FULL TRANSCRIPT' section;"
    grep -q '# ENVIRONMENT SNAPSHOT' /tmp/f-save.md 2>/dev/null || bad="$bad C59 the saved file carries no environment snapshot;"
    # C62: history reads the transcript deep, by path and by sid prefix under the project slug.
    out="$(pfm chat history "$path" 5 2>&1)"
    rc=$?
    [ "$rc" -eq 0 ] && grep -q "== $path · last 5 messages ==" <<<"$out" || bad="$bad C62 history by path exited $rc: $(one_line "$out");"
    slug="$(printf '%s' "$CWD" | tr / -)"
    out="$(pfm chat history "$(printf '%s' "$cc_id" | cut -c1-8)" 3 "$slug" 2>&1)"
    rc=$?
    [ "$rc" -eq 0 ] && grep -q 'last 3 messages ==' <<<"$out" || bad="$bad C62 history by sid prefix under slug $slug exited $rc: $(one_line "$out");"
  else
    bad="$bad C59/C62 no transcript path from find ('$path') — save and history were NOT asserted;"
  fi
  # C63: modal deny drives the chat's own tmux session; an unknown session is refused by name.
  out="$(pfm chat modal "$cc_sock" deny 0 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] && [ "$out" = "modal denied on $cc_sock" ] || bad="$bad C63 modal deny 0 on $cc_sock exited $rc: $(one_line "$out");"
  out="$(pfm chat modal no-such-session-f deny 1 2>&1)"
  rc=$?
  [ "$rc" -eq 1 ] && grep -q 'no tmux socket for' <<<"$out" || bad="$bad C63 modal on an unknown session exited $rc without naming the missing socket: $(one_line "$out");"
  # C64: resolve prints socket, session and id; an unknown name is exit 4.
  out="$(pfm chat resolve "$CC" 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] && [ "$(printf '%s' "$out" | cut -f1)" = "$cc_sock" ] && [ "$(printf '%s' "$out" | cut -f3)" = "$cc_id" ] ||
    bad="$bad C64 resolve $CC exited $rc / '$(one_line "$out")' (want $cc_sock<TAB>session<TAB>$cc_id);"
  pfm chat resolve NO_SUCH_CHAT_F >/dev/null 2>&1
  rc=$?
  [ "$rc" -eq 4 ] || bad="$bad C64 resolve on an absent name exited $rc (want 4);"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "find → $cc_id, save + history (by path, by sid prefix) over $path, modal deny on $cc_sock, resolve → $cc_sock/$cc_id; the absent cases refused by name"
  fi
fi

# ─── F.16 — picker plumbing: agent-open, chat-server ────────────────────────

beat F.16-picker-plumbing
spends none
target_live "$CC"
expect-log '"hook":"agent-open","decision":"error"'
expect-log 'not one bare socket name'
if requires; then
  bad=""
  # X18: the per-window pane opener validates its request before touching anything.
  out="$(pfm internal agent-open 2>&1)"
  rc=$?
  [ "$rc" -eq 2 ] && grep -q 'usage: pfm internal agent-open' <<<"$out" || bad="$bad X18 agent-open without flags exited $rc (want 2 + usage): $(one_line "$out");"
  out="$(pfm internal agent-open --id 'lane/unsafe' --cwd "$CWD" 2>&1)"
  rc=$?
  [ "$rc" -eq 1 ] && grep -q 'agent open requires a safe session id' <<<"$out" || bad="$bad X18 an unsafe id exited $rc without the named refusal: $(one_line "$out");"
  # X19: the shim's session creator — refused for a relative cwd and a foreign
  # prefix, and a REAL server for a lawful socket name, its window named after
  # the engine, torn down as soon as it has been read.
  out="$(pfm internal chat-server cc-1-2-3 relative/dir true 2>&1)"
  rc=$?
  [ "$rc" -eq 2 ] && grep -q 'usage: pfm internal chat-server' <<<"$out" || bad="$bad X19 a relative cwd exited $rc (want 2 + usage): $(one_line "$out");"
  tmux_dir="$(_lane_tmux_dir)"
  lane_sock="cc-$(date +%s)-$$-77"
  # PAYLOAD: the planted server stays alive for the X19 window assertion.
  out="$(pfm internal chat-server "$lane_sock" /tmp 'sleep 120' 2>&1)"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    bad="$bad X19 chat-server $lane_sock exited $rc: $(one_line "$out");"
  else
    window="$(tmux -S "$tmux_dir/$lane_sock" list-windows -F '#{window_name}' 2>&1)"
    [ "$window" = Claude ] || bad="$bad X19 the created server's window is '$(one_line "$window")', want Claude (the engine's short name);"
    tmux -S "$tmux_dir/$lane_sock" kill-server >/dev/null 2>&1 || bad="$bad X19 the lane's throwaway server on $lane_sock could not be killed;"
  fi
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "agent-open refuses no flags (usage) and an unsafe id by name; chat-server refuses a relative cwd, created $lane_sock with window 'Claude' under $tmux_dir, torn down"
  fi
fi

# ─── F.17 — naming precedence and the TUI 1h toggle ─────────────────────────

beat F.17-additional-k-coverage
spends none
target_live "$CC"
if requires; then
  bad=""
  # K11: the live row's name is the indexed label, first in the precedence.
  [ "$(live_field "$CC" 5)" = "$CC" ] || bad="$bad K11 the live row's name is '$(live_field "$CC" 5)', want the indexed label $CC;"
  # K10: the resumable row keeps the custom title its chat was renamed to.
  if [ -z "$GRP_ID" ]; then
    bad="$bad K10 no ended chat to read a resumable row from (F.04 left none);"
  else
    resume_name="$(pfm ls -a --tsv 2>/dev/null | awk -F'\t' -v id="$GRP_ID" 'NR > 1 && $2 == id { print $5; exit }')"
    [ "$resume_name" = "$GRP" ] || bad="$bad K10 the resumable row $GRP_ID is named '$resume_name', want the custom title '$GRP';"
  fi
  # K27: ⌃E in the picker toggles the 1h cache mode the header shows.
  if tui_open 100 30 ls; then
    c0="$(tui_cache)"
    tui_keys C-e
    wait_for 10 "[ -n \"\$(tui_cache)\" ] && [ \"\$(tui_cache)\" != '$c0' ]" ||
      bad="$bad K27 ⌃E did not toggle the header cache: $LANE_WAIT_WHY;"
    c1="$(tui_cache)"
    tui_keys C-e
    wait_for 10 "[ \"\$(tui_cache)\" = '$c0' ]" ||
      bad="$bad K27 ⌃E did not toggle the header cache back: $LANE_WAIT_WHY;"
    c2="$(tui_cache)"
    [ -n "$c0" ] && [ "$c0" != "$c1" ] && [ "$c2" = "$c0" ] || bad="$bad K27 ⌃E did not toggle the header cache ('$c0' → '$c1' → '$c2');"
    tui_keys Escape
  else
    bad="$bad K27 $TUI_WHY;"
  fi
  tui_close
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "live row named $CC (indexed label), resumable row $GRP_ID named '$GRP' (custom title), ⌃E toggled $c0 → $c1 → $c2"
  fi
fi

# ─── F.18 — cross-lane: E1's chat after the storm ───────────────────────────

beat F.18-e1-chat-survives-storm
spends "cc:$SEAT"
target_live "$E1_CHAT"
if requires; then
  if [ "$STORM_RAN" -ne 1 ]; then
    blocked F.05-storm "the storm never ran, so there is no after-storm state to assert $E1_CHAT against"
  else
    bad=""
    out="$(pfm chat status "$E1_CHAT" 2>&1)"
    rc=$?
    [ "$rc" -eq 0 ] && grep -qiE 'idle|working' <<<"$out" || bad="$bad C22 status exited $rc / '$(one_line "$out")' after the storm;"
    out="$(pfm chat last "$E1_CHAT" 2>&1)"
    rc=$?
    [ "$rc" -eq 0 ] && [ -n "$out" ] || bad="$bad C28 last exited $rc or printed nothing after the storm;"
    out="$(pfm chat inject --allow-unsigned "$E1_CHAT" 'STORM-SURVIVED' 2>&1)"
    rc=$?
    if [ "$rc" -ne 0 ]; then
      bad="$bad C32 inject was refused after the storm (exit $rc): $(one_line "$out");"
    elif ! wait_prompt "$E1_CHAT" STORM-SURVIVED 30; then
      bad="$bad C32 the inject never landed: ${LANE_WAIT_WHY:-no wait reason recorded}; last: $(one_line "$(pfm chat last "$E1_CHAT" 2>&1)");"
    fi
    if [ -n "$bad" ]; then fail "$bad"; else
      pass "$E1_CHAT answered status, last and a delivered inject after $n_storm storm chats came and went beside it"
    fi
  fi
fi

lane_end

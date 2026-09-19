#!/usr/bin/env bash
# F.sh — lane F, fleet: `chat new` across every dimension the landscape lists,
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
# and their landscape ids are the contract in beats.md and map.tsv —
# check-map.sh fails when this file and those disagree.
#
# Cost: one Claude seat (`--seats cc:1`, seat 1 by default) plus the Codex home
# when it is configured. Spawns: F_CC, F_CX (cx), F_GRP:lane, a 4-chat storm
# (cc+cx round-robin, 2 sends each), three parallel F_PAR_<n>, and E1_MAIN when
# the sequence did not leave it alive. Roughly 20 short turns; the TUI beats
# spend no model turn at all.
#
# BROKEN STATE: the prelude aborts the lane by name when the seat it was told
# to spend is not configured, the daemon never answers, or E1_MAIN cannot be
# made; a beat whose chat has no live row is `blocked-by` the beat that last
# saw it alive (seconds, not minutes); a dimension that needs the Codex home
# and finds none configured is `blocked-by config`, by name; a row kind or idle
# state only a scripted engine can provoke is `blocked-by wave7-mock-engine`
# with what it needs from the mock spelled out — never a pass, never silence;
# every ✗ carries the raw pane bytes in the lane log beside its assertion.
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
CONFIG="$HOME/.config/pfm/pfm.config.json"
SID_DIR="${PFM_SID_DIR:-${TMPDIR:-/tmp}/cc-sid}"
DEMO=/worktree/infra/demo
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
# The Codex home pfm's config resolves (internal/config: `.codex.homes[0].home`,
# else the default ~/.codex when its auth.json carries a live token pair).
CX_HOME="$(jq -r '.codex.homes[0].home // empty' "$CONFIG" 2>/dev/null)"
case "$CX_HOME" in "~"*) CX_HOME="$HOME${CX_HOME#\~}" ;; esac
if [ -z "$CX_HOME" ] &&
  jq -e '(.tokens.access_token // "") != "" and (.tokens.account_id // "") != ""' "$HOME/.codex/auth.json" >/dev/null 2>&1; then
  CX_HOME="$HOME/.codex"
fi
CX_WHY="no .codex.homes in $CONFIG and $HOME/.codex/auth.json carries no token pair (internal/config hasValidCodexCredentials)"

for project in orbit atlas lumen; do
  need "the working directory /work/$project" "[ -d '/work/$project/.git' ]" \
    "mkdir -p '/work/$project' && git -C '/work/$project' init -q && git -C '/work/$project' commit -q --allow-empty -m lane" ||
    lane_abort "no working directory for the chats to live in (/work/$project)"
done
need "the pfm MCP daemon on :$PORT" \
  "[ \"\$(curl -s -o /dev/null -w '%{http_code}' -m 2 http://127.0.0.1:$PORT/mcp/chat)\" != 000 ]" \
  "bash $DEMO/daemon.sh" ||
  lane_abort "the chat MCP daemon never answered on :$PORT — no Codex chat can start and no chat can call a chat_* tool"

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

# tui_close/tui_pane/tui_keys/tui_type/tui_has/tui_wait/tui_open/tui_selected
# live once in lib.sh (E3 shares this exact subset) — only the extra readers
# below (this lane alone needs them) and TUI_SOCK/CWD are F's own.
tui_pane_e() { tmux -S "$TUI_SOCK" capture-pane -e -p -t tui 2>&1; }
tui_cmd() { tmux -S "$TUI_SOCK" display -p -t tui '#{pane_current_command}' 2>/dev/null; }
tui_cache() { tui_pane | grep -oE '⚡ 1h|🪫 5m' | head -1; }
tui_account() { tui_pane | sed -n 's/.*account \([0-9][0-9]*\) ·.*/\1/p' | head -1; }
# tui_left — the picker has left the pane (exit or exec): 0 when the pane's
# command is no longer pfm within <secs>.
tui_left() {
  local i=0
  while [ "$i" -lt "$1" ]; do
    [ "$(tui_cmd)" != pfm ] && return 0
    sleep 1
    i=$((i + 1))
  done
  return 1
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
  pfm chat new --name "$CC" --engine cc --account "$SEAT" --cwd "$CWD" --1h --model sonnet --effort low \
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

beat F.01-new-engine C9 C10 C11 C12 K4 K5 K6 K7 K8 K9
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
[ "$rc" -eq 2 ] && printf '%s' "$out" | grep -q 'usage: pfm chat new' ||
  bad="$bad K8 chat new without --name exited $rc (want 2 + usage): $(one_line "$out");"
# K4: --engine is parsed by the registry — an unregistered spelling is refused
# naming the accepted ones; the registered OpenCode id has no headless door in
# this tree (action.PlannerFor) and says so by name instead of spawning nothing.
out="$(pfm chat new --name F_NOPE --engine oc --cwd "$CWD" "x" 2>&1)"
rc=$?
[ "$rc" -eq 2 ] && printf '%s' "$out" | grep -q 'unknown engine "oc"' ||
  bad="$bad K4 --engine oc exited $rc without naming the unknown engine: $(one_line "$out");"
out="$(pfm chat new --name F_NOPE --engine ox --cwd "$CWD" "x" 2>&1)"
rc=$?
[ "$rc" -eq 2 ] && printf '%s' "$out" | grep -q 'OpenCode does not support headless chat' ||
  bad="$bad K4/oc --engine ox exited $rc without the named absence of the OpenCode door: $(one_line "$out");"
# K5: no --engine → the calling chat's engine, else the machine default. Both
# branches are driven with an account no roster holds, so the refusal NAMES the
# engine the fallback chose and nothing is spawned or registered.
want="$(default_engine_word)"
out="$(pfm chat new --name F_NOPE --account 999 --cwd "$CWD" "x" 2>&1)"
rc=$?
[ "$rc" -eq 2 ] && printf '%s' "$out" | grep -q "$want account 999 is not in the configured roster" ||
  bad="$bad K5 machine-default fallback: exited $rc, wanted the $want roster named: $(one_line "$out");"
out="$(CLAUDE_CODE_SESSION_ID=lane-caller pfm chat new --name F_NOPE --account 999 --cwd "$CWD" "x" 2>&1)"
rc=$?
[ "$rc" -eq 2 ] && printf '%s' "$out" | grep -q 'Claude account 999 is not in the configured roster' ||
  bad="$bad K5 caller-engine fallback (CLAUDE_CODE_SESSION_ID set): exited $rc, wanted the Claude roster named: $(one_line "$out");"
# K6: engine.FromSocket — a socket prefix no engine owns is a named absence.
out="$(pfm internal chat-server zz-1-2-3 /tmp true 2>&1)"
rc=$?
[ "$rc" -eq 2 ] && printf '%s' "$out" | grep -q 'not one bare socket name carrying an engine prefix' ||
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
  printf '%s' "$(sock_base "$CC")" | grep -Eq '^cc-[0-9]+-[0-9]+-[0-9]+$' ||
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
    printf '%s' "$(sock_base "$CX")" | grep -Eq '^cx-[0-9]+-[0-9]+-[0-9]+$' ||
      bad="$bad K7 $CX socket '$(sock_base "$CX")' breaks the socket law;"
  fi
fi
if [ -n "$bad" ]; then
  fail "$bad"
elif [ -z "$CX_HOME" ]; then
  blocked "config" "Codex home not configured — $CX_WHY; the cc dimension held ($cc_note, socket $(sock_base "$CC")); --engine oc/ox refused by name (no OpenCode headless door in this tree)"
else
  pass "$cc_note (live-claude, $CWD, seat $SEAT, socket $(sock_base "$CC")) · $cx_note (live-codex, socket $(sock_base "$CX")) · --engine oc/ox, no --name, account 999 (default → $want, caller → Claude), unknown prefix, (unnamed): each refused by name"
fi

# ─── F.02 — {name}:{group}, _KILL/_HIDE, --role, --prompt-file ──────────────

beat F.02-new-label-role K12 K13 K14 K20 K21 C17 C18
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
[ "$rc" -eq 2 ] && printf '%s' "$out" | grep -q 'mutually exclusive' ||
  bad="$bad K21 --prompt-file plus an inline prompt exited $rc without the named refusal: $(one_line "$out");"
# The launch: the label grammar as the name, the role first, the prompt from a
# file, --attach so F.04 can read the attach line this non-tty stdout received.
if live_chat "$GRP"; then
  grp_note="$GRP already live"
else
  pfm chat new --name "$GRP" --engine cc --account "$SEAT" --cwd "$CWD" --model sonnet --effort low \
    --role f-role --prompt-file /tmp/f-grp.prompt --attach >/tmp/f-grp.launch.out 2>/tmp/f-grp.launch.err
  rc=$?
  [ "$rc" -eq 0 ] || bad="$bad chat new $GRP exited $rc: $(one_line "$(cat /tmp/f-grp.launch.out /tmp/f-grp.launch.err)");"
  grp_note="$GRP spawned"
fi
if ! live_chat "$GRP"; then
  bad="$bad no live row for '$GRP' — nothing below could be asserted: $(one_line "$(pfm ls --plain)");"
else
  sock="$(live_field "$GRP" 11)"
  # K12: the row carries the whole label; the tmux window converges on its name part.
  [ "$(live_field "$GRP" 5)" = "$GRP" ] || bad="$bad K12 the row's name is '$(live_field "$GRP" 5)', want '$GRP';"
  window="$(tmux -S "$sock" list-windows -F '#{window_name}' 2>&1 | head -1)"
  case "$window" in *F_GRP*) ;; *) bad="$bad K12 tmux window '$(one_line "$window")' does not carry the label's name part;" ;; esac
  # K20/C18: the role crumb, and the constitution ahead of the prompt in the transcript.
  crumb=""
  for candidate in "$SID_DIR"/role-*; do
    [ -f "$candidate" ] || continue
    case "$candidate" in *"${sock##*/}"*) crumb="$candidate"; break ;; esac
  done
  if [ -z "$crumb" ]; then
    bad="$bad K20 no role crumb for socket ${sock##*/} in $SID_DIR (role-<socket>); present: $(one_line "$(printf '%s ' "$SID_DIR"/role-*)");"
  elif ! grep -q 'f-role' "$crumb"; then
    bad="$bad K20 the crumb $crumb does not name f-role: $(one_line "$(cat "$crumb")");"
  fi
  first_user="$(pfm chat read "$GRP" --tail 200 --json 2>/dev/null |
    jq -r '[.entries[] | select(.role == "user")][0].text // ""' | tr '\n' ' ')"
  if [ -z "$first_user" ]; then
    bad="$bad C18/C17 the transcript's first user record could not be read (pfm chat read --json);"
  else
    role_at="$(printf '%s' "$first_user" | awk '{ print index($0, "F-ROLE-CONSTITUTION") }')"
    prompt_at="$(printf '%s' "$first_user" | awk '{ print index($0, "F-PROMPT-FILE") }')"
    [ "$prompt_at" -gt 0 ] || bad="$bad C17 the prompt file's text never reached the first user record;"
    [ "$role_at" -gt 0 ] || bad="$bad C18 the role constitution is not in the first user record;"
    [ "$role_at" -gt 0 ] && [ "$prompt_at" -gt 0 ] && [ "$role_at" -ge "$prompt_at" ] &&
      bad="$bad K20 the constitution (offset $role_at) does not precede the prompt (offset $prompt_at);"
  fi
  # K14: the store-based kill — a row in the killed ledger, out of the default view, back on unkill.
  pfm chat kill "$GRP" >/dev/null 2>&1 || bad="$bad K14 chat kill exited non-zero;"
  sleep 2
  [ "$(all_field "$GRP" 10)" = true ] || bad="$bad K14 after kill the -a row's killed column is '$(all_field "$GRP" 10)', want true;"
  [ -z "$(live_row "$GRP")" ] || bad="$bad K14 the killed row is still in the DEFAULT view;"
  pfm chat unkill "$GRP" >/dev/null 2>&1 || bad="$bad K14 chat unkill exited non-zero;"
  sleep 2
  [ "$(all_field "$GRP" 10)" = false ] || bad="$bad K14 after unkill the killed column is '$(all_field "$GRP" 10)', want false;"
  # K13: the name-based kill — a _KILL (or legacy _HIDE, case-insensitive) label
  # hides the chat with no store row; renaming it back is the unkill.
  for label in _KILL_F _hide_f; do
    out="$(pfm chat name "$GRP" "$label" 2>&1)" || bad="$bad K13 rename to $label failed: $(one_line "$out");"
    sleep 2
    [ -z "$(live_row "$label")" ] || bad="$bad K13 '$label' is still in the DEFAULT view;"
    [ "$(all_field "$label" 10)" = true ] || bad="$bad K13 '$label' -a row killed column is '$(all_field "$label" 10)', want true;"
    out="$(pfm chat name "$label" "$GRP" 2>&1)" || bad="$bad K13 rename $label back to $GRP failed: $(one_line "$out");"
    sleep 2
  done
  live_chat "$GRP" || bad="$bad K13 after the renames '$GRP' has no live row in the default view;"
fi
if [ -n "$bad" ]; then fail "$bad"; else
  pass "$grp_note: label '$GRP' in row + window, role crumb $crumb, constitution ahead of the prompt-file text, store kill/unkill, _KILL_F and _hide_f hide by name and the rename back unhides"
fi

# ─── F.03 — --account / --1h / --model --effort, read off the launch pfm made ─

beat F.03-new-account-1h-model C13 C14 C15 C16 K24 K25 K28
spends "cc:$SEAT"
target_live "$CC"
expect-log 'not in the configured roster'
expect-log 'unknown Claude effort'
if requires; then
  bad=""
  sock="$(live_field "$CC" 11)"
  # The pane's start command IS the launch pfm synthesized (action.ClaudeSpawn
  # ShellCommand): the flags and the cache assignment are read back from tmux.
  start="$(tmux -S "$sock" list-panes -F '#{pane_start_command}' 2>&1 | head -1)"
  [ -n "$start" ] || bad="$bad the pane start command could not be read from $sock;"
  printf '%s' "$start" | grep -q -- '--model sonnet' || bad="$bad C15/K28 the launch carries no '--model sonnet': $(one_line "$start" | cut -c1-200);"
  printf '%s' "$start" | grep -q -- '--effort low' || bad="$bad C16/K28 the launch carries no '--effort low';"
  printf '%s' "$start" | grep -q 'ENABLE_PROMPT_CACHING_1H=1' || bad="$bad C14/K25 the launch carries no ENABLE_PROMPT_CACHING_1H=1 (--1h);"
  # C13: the row reports the seat asked for; K24: the seat's medal on the row; K25: the ⚡ badge.
  [ "$(live_field "$CC" 9)" = "$SEAT" ] || bad="$bad C13 row account is '$(live_field "$CC" 9)', want $SEAT;"
  plain_row="$(pfm ls --plain 2>/dev/null | grep -F "● $CC " | head -1)"
  [ -n "$plain_row" ] || bad="$bad no '● $CC' line in pfm ls --plain;"
  if [ -z "$MEDAL" ]; then
    bad="$bad K24 the seat's medal could not be read from pfm config show (config accounts=…);"
  else
    printf '%s' "$plain_row" | grep -qF "$MEDAL" || bad="$bad K24 the plain row lacks seat $SEAT's medal $MEDAL: $(one_line "$plain_row");"
  fi
  printf '%s' "$plain_row" | grep -qF '⚡' || bad="$bad K25 the plain row lacks the ⚡ 1h badge: $(one_line "$plain_row");"
  # The refusals, by name, nothing spawned.
  out="$(pfm chat new --name F_NOPE --engine cc --account 999 --cwd "$CWD" "x" 2>&1)"
  rc=$?
  [ "$rc" -eq 2 ] && printf '%s' "$out" | grep -q 'Claude account 999 is not in the configured roster' ||
    bad="$bad C13 --account 999 exited $rc without naming the roster: $(one_line "$out");"
  out="$(pfm chat new --name F_NOPE --engine cc --account "$SEAT" --effort bogus --cwd "$CWD" "x" 2>&1)"
  rc=$?
  [ "$rc" -eq 2 ] && printf '%s' "$out" | grep -q 'unknown Claude effort "bogus"' ||
    bad="$bad C16 --effort bogus exited $rc without the named roster of efforts: $(one_line "$out");"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "launch carries --model sonnet --effort low ENABLE_PROMPT_CACHING_1H=1; row account $SEAT with medal $MEDAL and ⚡; account 999 and effort bogus refused by name"
  fi
fi

# ─── F.04 — --await / --attach mechanics, headless by construction ──────────

GRP_ID=""
beat F.04-new-await-attach C19 C20 K15 K16
spends "cc:$SEAT"
target_live "$CC"
if requires; then
  bad=""
  # C19: with --await the ANSWER owns stdout and the launch summary moves to stderr.
  if [ ! -f /tmp/f-cc.launch.out ]; then
    bad="$bad C19 $CC was not launched by this lane run (no launch capture) — the --await contract was not exercised;"
  else
    grep -qi 'ready' /tmp/f-cc.launch.out || bad="$bad C19 --await stdout does not carry the answer: $(one_line "$(cat /tmp/f-cc.launch.out)");"
    grep -q "$(printf '\t%s\t' "$CC")" /tmp/f-cc.launch.err || bad="$bad C19 the launch summary (cc<TAB>$CC<TAB>…) is not on stderr: $(one_line "$(head -3 /tmp/f-cc.launch.err)");"
    grep -q 'attach: tmux -L' /tmp/f-cc.launch.err || bad="$bad C19 the attach hint is not on stderr;"
    grep -q "$(printf '\t%s\t' "$CC")" /tmp/f-cc.launch.out && bad="$bad C19 the launch summary leaked onto --await's stdout;"
  fi
  out="$(pfm chat new --name F_NOPE --engine cc --account "$SEAT" --cwd "$CWD" --attach --await "x" 2>&1)"
  rc=$?
  [ "$rc" -eq 2 ] && printf '%s' "$out" | grep -q 'usage: pfm chat new' ||
    bad="$bad --attach with --await exited $rc (want 2 + usage): $(one_line "$out");"
  # C20/K16: --attach on a non-tty stdout prints the attach line for the new server.
  if [ ! -f /tmp/f-grp.launch.out ]; then
    bad="$bad C20 '$GRP' was not launched with --attach by this run (no capture);"
  elif ! live_chat "$GRP"; then
    bad="$bad C20 '$GRP' has no live row, so its attach line cannot be checked against a socket;"
  else
    grp_sock="$(sock_base "$GRP")"
    grep -q "TMUX= tmux -L $grp_sock attach -t" /tmp/f-grp.launch.out ||
      bad="$bad C20/K16 --attach printed no 'TMUX= tmux -L $grp_sock attach -t …' line: $(one_line "$(cat /tmp/f-grp.launch.out)");"
  fi
  # K15: no terminal is attached to any lane chat — headless by construction.
  for name in "$CC" "$GRP"; do
    live_chat "$name" || continue
    clients="$(tmux -S "$(live_field "$name" 11)" list-clients 2>&1)"
    [ -z "$clients" ] || bad="$bad K15 $name has an attached client: $(one_line "$clients");"
  done
  # '$GRP' has served its beats: end it and hide the resumable row it leaves,
  # so F.06 has a killed row for -a/-K and F.07 a resume-claude kind.
  if live_chat "$GRP"; then
    GRP_ID="$(live_field "$GRP" 2)"
    out="$(pfm chat end "$GRP" 2>&1)" || bad="$bad pfm chat end '$GRP' failed: $(one_line "$out");"
    sleep 3
    live_chat "$GRP" && bad="$bad '$GRP' still has a live row after end;"
    out="$(pfm chat kill "$GRP_ID" 2>&1)" || bad="$bad pfm chat kill $GRP_ID (hide the resume row) failed: $(one_line "$out");"
    sleep 2
    [ "$(all_field "$GRP" 1)" = resume-claude ] || bad="$bad the ended chat's -a row kind is '$(all_field "$GRP" 1)', want resume-claude;"
    [ "$(all_field "$GRP" 10)" = true ] || bad="$bad the ended chat's resume row is not hidden (killed=$(all_field "$GRP" 10));"
  fi
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "--await: answer on stdout, summary on stderr; --attach+--await refused; --attach printed the attach line for ${grp_sock:-?}; no client on any lane socket; '$GRP' ended → hidden resume row $GRP_ID"
  fi
fi

# ─── F.05 — the storm ───────────────────────────────────────────────────────

STORM_RAN=0
beat F.05-storm K18 K19
spends "cc:$SEAT+cx"
target "$CC"
expect-log 'unknown command'
bad=""
engines="cc"
[ -n "$CX_HOME" ] && engines="cc cx"
out="$(STORM_ENGINES="$engines" STORM_CC_ACCOUNT="$SEAT" STORM_CC_MODEL=sonnet STORM_CC_EFFORT=low \
  bash "$DEMO/storm.sh" start 4 2 2>&1)"
rc=$?
[ "$rc" -eq 0 ] || bad="$bad storm.sh start 4 2 exited $rc: $(one_line "$(printf '%s\n' "$out" | tail -4)");"
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
# K19: storm and idle are demo scripts over ordinary verbs, not pfm subcommands.
for verb in storm idle; do
  out="$(pfm "$verb" 2>&1)"
  rc=$?
  [ "$rc" -eq 2 ] && printf '%s' "$out" | grep -q "unknown command \"$verb\"" ||
    bad="$bad K19 pfm $verb exited $rc without 'unknown command' (it must not exist): $(one_line "$out" | cut -c1-120);"
done
if [ -n "$bad" ]; then
  fail "$bad"
elif [ -z "$CX_HOME" ]; then
  blocked "config" "Codex home not configured — $CX_WHY; the storm ran cc-only (4 live-claude rows) and pfm storm/idle are named absences; oc has no chat new door in this tree"
else
  pass "storm.sh spawned STORM_1..4 over $engines (kinds per rotation), pfm storm/idle are named absences (exit 2); oc has no chat new door in this tree, so the storm cannot span it"
fi

# ─── F.06 — pfm ls in every shape ───────────────────────────────────────────

beat F.06-ls-rows C1 C2 C3 C4 C5 C6 C7 C8 C21 C60
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
  printf '%s' "$plain" | grep -q '^\[orbit\]' || bad="$bad C2 --plain has no [orbit] group;"
  printf '%s' "$plain" | grep -q "^● $CC " || bad="$bad C2 --plain has no '● $CC' row;"
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
  [ "$rc" -eq 0 ] && printf '%s' "$out" | grep -q "tmux -L $cc_sock attach" ||
    bad="$bad C8 pfm ls $cc_id exited $rc without the attach line for $cc_sock: $(one_line "$out");"
  pfm ls "$cc_id" --plain >/dev/null 2>&1
  rc=$?
  [ "$rc" -eq 2 ] || bad="$bad C8 pfm ls <id> --plain exited $rc (want 2, usage);"
  # C21: chat open by name resolves to the same action.
  out="$(pfm chat open "$CC" 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] && printf '%s' "$out" | grep -q "tmux -L $cc_sock attach" ||
    bad="$bad C21 pfm chat open $CC exited $rc without the attach line: $(one_line "$out");"
  # C60: the compatibility listing, this repo and everywhere.
  out="$(cd "$CWD" && pfm chat ls 2>&1)"
  printf '%s' "$out" | grep -q 'live chats in this repo' || bad="$bad C60 chat ls has no 'live chats in this repo' header: $(one_line "$out" | cut -c1-120);"
  printf '%s' "$out" | grep -q "$CC" || bad="$bad C60 chat ls (in $CWD) does not list $CC;"
  out="$(cd "$CWD" && pfm chat ls --all 2>&1)"
  printf '%s' "$out" | grep -q 'live chats everywhere' || bad="$bad C60 chat ls --all has no 'live chats everywhere' header;"
  # C7: --safe validates, and 'on' names itself in the cosmos title.
  out="$(pfm ls --safe bogus --tsv 2>&1)"
  rc=$?
  [ "$rc" -eq 2 ] && printf '%s' "$out" | grep -q 'must be auto, on, or off' ||
    bad="$bad C7 --safe bogus exited $rc without the named roster: $(one_line "$out");"
  # C1: the interactive picker entry, on a real tty inside tmux.
  if tui_open 100 30 ls; then
    tui_has '╭─ fleet' || bad="$bad C1 the picker painted no fleet frame: $(one_line "$(tui_pane)" | cut -c1-200);"
  else
    bad="$bad C1 $TUI_WHY;"
  fi
  if tui_open 100 30 ls --safe on; then
    tui_keys Tab; tui_keys Tab; tui_keys Tab
    sleep 2
    tui_has 'cosmos · safe' || bad="$bad C7 --safe on: the cosmos panel title does not read 'cosmos · safe': $(one_line "$(tui_pane | grep -F cosmos | head -2)");"
  else
    bad="$bad C7 --safe on: $TUI_WHY;"
  fi
  # C6: --no-sky disables the animation clock, and the cosmos tab says so on `space`.
  if tui_open 100 30 ls --no-sky; then
    tui_keys Tab; tui_keys Tab; tui_keys Tab
    sleep 3
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

beat F.07-row-kinds K29 K30 K31 K32 K33 K34 K35 K36 K37 K38 K39 K40
spends none
target_live "$CC"
if requires; then
  bad="" held=""
  # An ended Codex chat leaves the resume-codex kind; F_CX has served F.01.
  if [ -n "$CX_HOME" ] && live_chat "$CX"; then
    out="$(pfm chat end "$CX" 2>&1)" || bad="$bad pfm chat end $CX failed: $(one_line "$out");"
    sleep 3
  fi
  kinds="$(kinds_in_fleet)"
  has_kind() { printf '%s\n' "$kinds" | grep -qx "$1"; }
  has_kind live-claude && held="$held K29" || bad="$bad K29 no live-claude row;"
  has_kind resume-claude && held="$held K33" || bad="$bad K33 no resume-claude row (F.04 ended '$GRP');"
  has_kind new-claude && held="$held K35" || bad="$bad K35 no new-claude row;"
  if [ -n "$CX_HOME" ]; then
    has_kind live-codex && held="$held K30" || bad="$bad K30 no live-codex row (the storm's cx chats);"
    has_kind resume-codex && held="$held K34" || bad="$bad K34 no resume-codex row after ending $CX;"
    has_kind new-codex && held="$held K36" || bad="$bad K36 no new-codex row;"
    cx_note="K30/K34/K36 asserted"
  else
    cx_note="K30/K34/K36 NOT asserted — Codex home not configured ($CX_WHY)"
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
  # professorUpdateCachePath(runtime) = dirname(PFM_DB)/update-check.json
  # (pfm/internal/picker/update_row.go) — its presence is the beat's own
  # ground truth for whether the frame SHOULD carry a ⬆ row, so a release
  # build is read for it too, never credited unconditionally.
  version="$(pfm version 2>&1)"
  update_cache="$(dirname "${PFM_DB:-$HOME/.local/state/pfm/fleet.db}")/update-check.json"
  if tui_open 120 40 ls -a; then
    frame="$(tui_pane)"
    printf '%s' "$frame" | grep -q '● ' || bad="$bad TUI frame shows no ● live row;"
    printf '%s' "$frame" | grep -q '↻ ' || bad="$bad TUI frame shows no ↻ resume row (-a view);"
    printf '%s' "$frame" | grep -q '✦ New ' || bad="$bad TUI frame shows no ✦ New row;"
    case "$version" in
      *dev*|*-*)
        printf '%s' "$frame" | grep -q '⬆' && bad="$bad K40 a ⬆ ProfessorUpdate row on a non-release build ($version);"
        held="$held K40(absent-on-$(one_line "$version" | tr ' ' '_'))"
        ;;
      *)
        if [ -s "$update_cache" ]; then
          printf '%s' "$frame" | grep -q '⬆' ||
            bad="$bad K40 $update_cache holds a cached update notice but the frame shows no ⬆ ProfessorUpdate row ($version);"
          held="$held K40(release-build:$(one_line "$version" | tr ' ' '_'),cache-present-row-checked)"
        else
          printf '%s' "$frame" | grep -q '⬆' &&
            bad="$bad K40 the frame shows a ⬆ ProfessorUpdate row but $update_cache holds no cached notice ($version);"
          held="$held K40(release-build:$(one_line "$version" | tr ' ' '_'),cache-absent-row-checked)"
        fi
        ;;
    esac
  else
    bad="$bad K40 $TUI_WHY;"
  fi
  tui_close
  if [ -n "$bad" ]; then
    fail "$bad — $cx_note; $oc_note"
  else
    blocked wave7-mock-engine "K31 LiveSplit (two engine panes in one window), K32 Agent (a Claude process outside any fleet pane) and K37 Booting (a pane whose engine never writes its SID crumb) need a scripted engine; asserted and held:$held; $cx_note; $oc_note"
  fi
fi

# ─── F.08 — the TUI picker, every tab and every key ─────────────────────────

beat F.08-tui-picker T1 T2 T3 T4 T5 T6 T7 T8 T9 T10 T11 T12 T13 T14 T15 T16 T17 T18 T19 T20 T21 T22 T23 T24 T25 T26 T27
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
    tui_has 'Chats · fuzzy search and all existing chat controls' || bad="$bad T4 the Chats hint is not on the default frame;"
    tui_pane | grep -qi 'fullscreen' && bad="$bad T9 a 'fullscreen' view exists on the frame;"
    # T8: tab cycles Chats → Stats → Limits → cosmos → Chats; shift+tab reverses.
    tui_keys Tab
    tui_has 'CPU' && tui_has 'RAM' || bad="$bad T8 tab from Chats did not land on Stats (no CPU/RAM header): $(one_line "$(tui_pane | sed -n 1,4p)");"
    tui_keys Tab
    tui_has 'Limits · live usage windows across every account' || bad="$bad T8 second tab did not land on Limits;"
    tui_keys Tab
    tui_has 'cosmos ·' || bad="$bad T8 third tab did not land on cosmos;"
    tui_keys Tab
    tui_has 'Chats · fuzzy search' || bad="$bad T8 fourth tab did not wrap to Chats;"
    tui_keys BTab
    tui_has 'cosmos ·' || bad="$bad T8 shift+tab from Chats did not land on cosmos;"
    tui_keys Tab
    # T18: the query editor — type, backspace/⌃H, ⌃W, ⌃U.
    tui_type "$CC"
    tui_has "find › $CC" || bad="$bad T18 typing did not reach the query line: $(one_line "$(tui_pane | grep -F 'find ›')");"
    tui_has "› ● $CC" || bad="$bad T18 the query '$CC' did not select the $CC row: $(one_line "$(tui_selected)");"
    tui_keys BSpace
    tui_has 'find › F_C ' || tui_has 'find › F_C' || bad="$bad T18 backspace did not delete one char: $(one_line "$(tui_pane | grep -F 'find ›')");"
    tui_keys C-h
    tui_has 'find › F_' && ! tui_has 'find › F_C' || bad="$bad T18 ⌃H did not delete one char: $(one_line "$(tui_pane | grep -F 'find ›')");"
    tui_type "x y"
    tui_keys C-w
    tui_has 'find › F_x' && ! tui_has 'find › F_x y' || bad="$bad T18 ⌃W did not delete the last word (want 'F_x'): $(one_line "$(tui_pane | grep -F 'find ›')");"
    tui_keys C-u
    tui_pane | grep -F 'find ›' | grep -Eq 'find › +[0-9]+/[0-9]+ visible' || bad="$bad T18 ⌃U did not clear the query: $(one_line "$(tui_pane | grep -F 'find ›')");"
    # T11: left/right walk the carousel on the selected live row.
    tui_type "$CC"
    tui_has '◖▶ open◗' || bad="$bad T11 the selected row shows no ◖▶ open◗ carousel: $(one_line "$(tui_selected)");"
    tui_keys Right
    tui_has '◖⚡ reboot◗' || bad="$bad T11 right did not move the carousel to reboot: $(one_line "$(tui_selected)");"
    tui_keys Right
    tui_has '◖🕐 1h◗' || bad="$bad T11 second right did not reach 1h: $(one_line "$(tui_selected)");"
    # T16: enter ACTS on the carousel index — index 2 toggles the cache mode in place.
    cache0="$(tui_cache)"
    tui_keys Enter
    cache1="$(tui_cache)"
    [ -n "$cache0" ] && [ -n "$cache1" ] && [ "$cache0" != "$cache1" ] || bad="$bad T16 enter on the 1h action did not flip the header cache ('$cache0' → '$cache1');"
    tui_keys Enter
    [ "$(tui_cache)" = "$cache0" ] || bad="$bad T16 a second enter did not flip the cache back;"
    tui_keys Left; tui_keys Left
    tui_has '◖▶ open◗' || bad="$bad T11 left did not return the carousel to open;"
    # T13: ⌃E toggles the 1h cache from the Chats tab.
    tui_keys C-e
    [ "$(tui_cache)" != "$cache0" ] || bad="$bad T13 ⌃E did not flip the header cache ('$cache0');"
    tui_keys C-e
    [ "$(tui_cache)" = "$cache0" ] || bad="$bad T13 a second ⌃E did not flip it back;"
    # T12: ⌃X kills the selected row NOW — receipt on the status line, store row
    # written. On a LIVE row the picker also sends /exit and kills the pane
    # (internal/kill finisher), so the key is driven on the ended '$GRP' resume
    # row, unhidden for the purpose and left hidden again by the keystroke.
    if [ -z "$GRP_ID" ]; then
      bad="$bad T12 no resume row to drive ⌃X on (F.04 left none);"
    else
      pfm chat unkill "$GRP_ID" >/dev/null 2>&1 || bad="$bad T12 pfm chat unkill $GRP_ID (making the resume row visible) exited non-zero;"
      tui_keys C-u
      sleep 3
      tui_type "F_GRP"
      # A GROUP:NAME row renders indented under its group panel: `›   ↻ F_GRP:lane`.
      printf '%s' "$(tui_selected)" | grep -qF "↻ $GRP" || bad="$bad T12 the query F_GRP did not select the resume row '$GRP': $(one_line "$(tui_selected)");"
      tui_keys C-x
      tui_has "hidden — $GRP" || bad="$bad T12 ⌃X left no 'hidden — $GRP' receipt: $(one_line "$(tui_pane | grep -F 'find ›')");"
      sleep 1
      [ "$(all_field "$GRP" 10)" = true ] || bad="$bad T12 after ⌃X the -a row's killed column is '$(all_field "$GRP" 10)', want true;"
      tui_keys C-u
      tui_type "$CC"
    fi
    # T17: cursor moves over the whole list.
    tui_keys C-u
    sleep 2
    first="$(tui_selected)"
    tui_keys Down
    second="$(tui_selected)"
    [ -n "$second" ] && [ "$second" != "$first" ] || bad="$bad T17 down did not move the cursor ('$first' → '$second');"
    tui_keys C-n; tui_keys C-p; tui_keys Up
    [ "$(tui_selected)" = "$first" ] || bad="$bad T17 ⌃N ⌃P up did not return to the first row ('$(tui_selected)');"
    tui_keys End
    last="$(tui_selected)"
    [ -n "$last" ] && [ "$last" != "$first" ] || bad="$bad T17 end did not move to the last row;"
    tui_keys Home
    [ "$(tui_selected)" = "$first" ] || bad="$bad T17 home did not return to the first row;"
    tui_keys NPage; tui_keys PPage
    [ -n "$(tui_selected)" ] || bad="$bad T17 pgdown/pgup left no selected row;"
    # T19/T5: Stats — every live chat where expected, focus walk, c/m sorts.
    tui_keys Tab
    sleep 3
    tui_has 'NAME' && tui_has 'ENGINE' && tui_has 'CPU%' || bad="$bad T5 the Stats columns (NAME ENGINE CPU%) did not render;"
    tui_wait 8 "$CC" || bad="$bad T5 $CC never appeared in the Stats rows: $(one_line "$(tui_pane | sed -n 5,12p)");"
    tui_keys Down; tui_keys Down; tui_type c; tui_type m; tui_keys Up
    tui_has 'CPU%' && tui_has 'c CPU sort' || bad="$bad T19 after ↓↓ c m ↑ the Stats frame lost its header or footer;"
    # T20/T6: Limits — a card per seat, scroll keys survive.
    tui_keys Tab
    tui_wait 20 "account $SEAT" || bad="$bad T6 no Limits card for 'account $SEAT' in 20s: $(one_line "$(tui_pane | sed -n 4,12p)");"
    tui_keys Down; tui_keys Up; tui_keys NPage; tui_keys PPage; tui_keys Home; tui_keys End
    tui_has 'Limits · live usage windows' || bad="$bad T20 the Limits frame did not survive its scroll keys;"
    # T21-T27/T7: cosmos — selection, focus, classic sky, scrub, play, now.
    tui_keys Tab
    sleep 3
    tui_has 'cosmos ·' && tui_has 'edges' || bad="$bad T7 the cosmos census line did not render;"
    tui_type j
    tui_has 'cosmos  ▸ ' || bad="$bad T22 j did not select a star (no ▸ HUD): $(one_line "$(tui_pane | grep -F cosmos | head -2)");"
    tui_type k
    tui_has 'cosmos  ▸ ' || bad="$bad T22 k lost the selection HUD;"
    tui_type s
    tui_has '⌖' || bad="$bad T24 s did not focus a system (no ⌖): $(one_line "$(tui_pane | grep -F cosmos | head -2)");"
    tui_type o
    tui_type s
    tui_has 'the classic sky has no systems to focus' || bad="$bad T21 o (classic sky) then s did not name the classic sky: $(one_line "$(tui_pane | grep -F cosmos | head -2)");"
    tui_type o
    tui_type '['
    tui_has '⟲' && tui_has '5m' || bad="$bad T25 [ did not scrub 5 minutes back (no ⟲ … −5m chip): $(one_line "$(tui_pane | grep -F '⟲')");"
    tui_type '{'
    tui_has '⟲' && tui_has '1h' || bad="$bad T25 { did not scrub an hour back: $(one_line "$(tui_pane | grep -F '⟲')");"
    tui_type ']'; tui_type '}'
    tui_keys Space
    tui_has '▸▸' || bad="$bad T26 space did not start replay (no ▸▸ chip): $(one_line "$(tui_pane | grep -F cosmos | head -3)");"
    tui_type n
    sleep 1
    tui_has '▸▸' && bad="$bad T27 n did not stop the replay;"
    tui_has '⟲' && bad="$bad T27 n did not return to now (⟲ chip still shown);"
    # T23: enter opens the selected star — the picker leaves the pane to attach,
    # or refuses by name when the star is not a running chat. Either is the key's contract.
    tui_type j
    hud="$(tui_pane | grep -F 'cosmos  ▸' | head -1)"
    tui_keys Enter
    if tui_left 15; then
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
    tui_keys C-s
    acct1="$(tui_account)"
    want="$(printf '%s\n' $ACCOUNT_IDS | awk -v cur="$acct0" 'NR == 1 { first = $1 } { if (hit) { print; printed = 1; exit } if ($1 == cur) hit = 1 } END { if (hit && !printed) print first }')"
    [ -n "$acct0" ] && [ "$acct1" = "$want" ] || bad="$bad T14 ⌃S moved the header account $acct0 → $acct1, want $want (roster: $ACCOUNT_IDS);"
    tui_keys Escape
    tui_left 10
  else
    bad="$bad T14 $TUI_WHY;"
  fi
  # T16: enter on the open action attaches — the picker execs into a client of the chat's server.
  cc_sock_path="$(live_field "$CC" 11)"
  if tui_open 100 30 ls; then
    tui_type "$CC"
    tui_keys Enter
    if ! tui_left 20; then
      bad="$bad T16 enter on '$CC' did not leave the picker;"
    else
      sleep 2
      clients="$(tmux -S "$cc_sock_path" list-clients 2>&1)"
      [ -n "$clients" ] || bad="$bad T16 enter left the picker but no client is attached to $CC's server $cc_sock_path;"
    fi
    tui_close
    sleep 1
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
    sock1="" waited=0
    while [ "$waited" -lt 240 ]; do
      sock1="$(pfm ls --tsv 2>/dev/null | awk -F'\t' -v id="$id0" 'NR > 1 && $1 ~ /^live-/ && $2 == id { print $11; exit }')"
      [ -n "$sock1" ] && [ "$sock1" != "$sock0" ] && break
      sock1=""
      sleep 5
      waited=$((waited + 5))
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
        sleep 2
        reboot_note="$reboot_note · observed: the resumed row was named '$name1', renamed back to $CC"
        [ "$(socket_field "$sock1" 5)" = "$CC" ] || bad="$bad T15 the rebooted row could not be renamed back to $CC (reads '$(socket_field "$sock1" 5)');"
      fi
    fi
    tui_close
    sleep 2
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

beat F.09-tui-golden T28 T29 T30 T36
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
      sleep 2
      frame="$(tui_pane)"
      n_frame="$(printf '%s\n' "$frame" | grep -c '')"
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
      printf '%s\n' "$frame" | sed -n 1p | grep -Eq ' pfm  .+ account [0-9]+ · (⚡ 1h|🪫 5m) · [0-9]+ rows · [0-9]+ hidden · [0-9]+ empty' ||
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
        tui_pane_e | grep -qF "$def_sgr" || bad="$bad T36 the default palette's header background $def_hex ($def_sgr) is not in the live escapes;"
        jq '.theme = "tokyo-night"' "$CONFIG" >/tmp/f-tokyo.json 2>/dev/null
        if tui_open 80 "$g_lines" --config /tmp/f-tokyo.json ls; then
          sleep 2
          tui_pane_e | grep -qF "$tok_sgr" || bad="$bad T36 the tokyo-night header background $tok_hex ($tok_sgr) is not in the live escapes under --config theme=tokyo-night;"
          tui_pane_e | grep -qF "$def_sgr" && bad="$bad T36 the tokyo-night frame still paints the default header background $def_hex;"
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

beat F.10-concurrent-new I96
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
  live_chat "F_PAR_$i" || bad="$bad no live row for F_PAR_$i;"
done
socks="$(for i in 1 2 3; do live_field "F_PAR_$i" 11; done | grep -c .)"
uniq_socks="$(for i in 1 2 3; do live_field "F_PAR_$i" 11; done | sort -u | grep -c .)"
[ "$socks" -eq 3 ] && [ "$uniq_socks" -eq 3 ] || bad="$bad $socks live sockets, $uniq_socks distinct (want 3 and 3);"
pfm ls --tsv >/dev/null 2>&1 || bad="$bad pfm ls --tsv exits non-zero after the parallel writes (fleet.db unreadable?);"
# The three have served: end them and hide the resume rows they leave.
for i in 1 2 3; do
  live_chat "F_PAR_$i" || continue
  id="$(live_field "F_PAR_$i" 2)"
  pfm chat end "F_PAR_$i" >/dev/null 2>&1 || bad="$bad pfm chat end F_PAR_$i failed;"
  sleep 1
  [ -n "$id" ] && pfm chat kill "$id" >/dev/null 2>&1
done
if [ -n "$bad" ]; then fail "$bad"; else
  pass "three parallel chat new landed three live rows on three distinct sockets, fleet.db read back clean; the three ended and their resume rows hidden"
fi

# ─── F.11 — the idle-detection state machine ────────────────────────────────

beat F.11-idle-states L1 L2 L3 L4 L6 L7
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
    out="$(pfm chat status "$GRP_ID" 2>/dev/null)"
    rc=$?
    pfm chat kill "$GRP_ID" >/dev/null 2>&1
    [ "$rc" -eq 3 ] && printf '%s' "$out" | grep -q 'dead' && held="$held dead" ||
      bad="$bad an ended chat's status exited $rc / '$(one_line "$out")' (want 3 + dead);"
  fi
  # L4 idle: the newest record is the assistant's word; L6: idle_seconds counts only then.
  pfm chat inject --allow-unsigned "$CC" "reply with exactly one word: IDLE-PROBE" >/dev/null 2>&1 ||
    bad="$bad the idle-making inject was refused;"
  if wait_last "$CC" IDLE-PROBE 240; then
    sleep 3
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
    bad="$bad the chat never answered IDLE-PROBE: ${LANE_WAIT_WHY:-no wait reason recorded};"
  fi
  # L4 working: a turn in flight — the newest record owes an answer; L6: idle_seconds is 0.
  pfm chat inject --allow-unsigned "$CC" \
    "Count from 1 to 30, one number per line, pausing about a second between numbers. Do not stop early." >/dev/null 2>&1 ||
    bad="$bad the busy-making inject was refused;"
  sleep 8
  json="$(pfm chat status "$CC" --json 2>/dev/null)"
  state="$(printf '%s' "$json" | jq -r .state 2>/dev/null)"
  idle_s="$(printf '%s' "$json" | jq -r .idle_seconds 2>/dev/null)"
  [ "$state" = working ] && held="$held L4(working)" || bad="$bad L4 mid-turn state is '$state' (want working);"
  [ "$idle_s" = 0 ] && held="$held L6(working→0)" || bad="$bad L6 idle_seconds is '$idle_s' while working (want 0);"
  wait_for 300 "pfm chat status '$CC' 2>/dev/null | grep -q idle" ||
    bad="$bad the chat never returned to idle after the counting turn: ${LANE_WAIT_WHY:-no wait reason recorded};"
  if [ -n "$bad" ]; then
    fail "$bad"
  else
    blocked wave7-mock-engine "L2 (a live seat with no transcript at all) and L3 (ReadMeta ErrNotExist on a live seat) need a scripted engine that stays live without ever writing its transcript; asserted and held:$held"
  fi
fi

# ─── F.12 — idle-down must not take a chat with a running sub-agent ─────────

beat F.12-idle-down-subagent L5
spends "cc:$SEAT"
target_live "$CC"
if requires; then
  bad=""
  cc_sock="$(sock_base "$CC")"
  pfm chat inject --allow-unsigned "$CC" \
    "Use the Task tool to launch exactly one subagent that lists three files in this directory and then counts slowly from 1 to 20, one number per line. While it works, say nothing else." >/dev/null 2>&1 ||
    bad="$bad the sub-agent stimulus could not be delivered;"
  working=""
  for _ in 1 2 3 4 5 6 7 8 9 10 11 12; do
    printf '%s' "$(pfm chat status "$CC" 2>&1)" | grep -qi working && { working=yes; break; }
    sleep 4
  done
  if [ -z "$working" ]; then
    bad="$bad status never reported working after the sub-agent stimulus (L5 sidechain override unobserved);"
  else
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
    # The demo's idle-down over the same fleet: it must leave the working chat and E1's alone.
    out="$(bash "$DEMO/idle.sh" down 2>&1)"
    rc=$?
    idle_line="$(printf '%s\n' "$out" | grep '^idle-down:' | tail -1)"
    [ "$rc" -eq 0 ] || bad="$bad idle.sh down exited $rc: $(one_line "$out");"
    live_chat "$CC" || bad="$bad idle.sh down took $CC while its sub-agent ran;"
    live_chat "$E1_CHAT" || bad="$bad idle.sh down took $E1_CHAT;"
  fi
  wait_for 420 "pfm chat status '$CC' 2>/dev/null | grep -qi idle" ||
    bad="$bad the chat never returned to idle after the sub-agent: ${LANE_WAIT_WHY:-no wait reason recorded};"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "working under a live sub-agent; pfm reap --horizon 1s spared $cc_sock; idle.sh down ('$(one_line "${idle_line:-no closing line}")') left $CC and $E1_CHAT live; then idle"
  fi
fi

# ─── F.13 — name-sync converges the window name ─────────────────────────────

beat F.13-name-sync L40 L41 L42
spends none
target_live "$CC"
if requires; then
  bad=""
  sock="$(live_field "$CC" 11)"
  win="$(tmux -S "$sock" list-windows -F '#{window_id}' 2>/dev/null | head -1)"
  drift() { tmux -S "$sock" rename-window -t "$win" LANE-DRIFT 2>&1; }
  window_name() { tmux -S "$sock" list-windows -F '#{window_name}' 2>/dev/null | head -1; }
  # L40: a drifted window is planned by the dry run (the default) and converged by --apply.
  drift || bad="$bad the drift rename-window failed on $sock;"
  sleep 1
  plan="$(pfm name-sync 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] || bad="$bad L40 pfm name-sync (dry run) exited $rc: $(one_line "$plan");"
  printf '%s' "$plan" | grep -q "would rename ${sock##*/} .*LANE-DRIFT -> " || bad="$bad L40 the dry run did not plan the drifted window: $(one_line "$plan");"
  printf '%s' "$plan" | grep -Eq 'windows planned: [1-9]' || bad="$bad L40 'windows planned' is not ≥ 1: $(one_line "$plan");"
  [ "$(window_name)" = LANE-DRIFT ] || bad="$bad L40 the dry run RENAMED the window (reads '$(window_name)');"
  dry="$(pfm name-sync --dry-run 2>&1)"
  printf '%s' "$dry" | grep -q 'dry run is the default' || bad="$bad L40 --dry-run did not announce itself as the default alias;"
  applied="$(pfm name-sync --apply 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] || bad="$bad L41 pfm name-sync --apply exited $rc: $(one_line "$applied");"
  printf '%s' "$applied" | grep -q "renamed ${sock##*/} .*LANE-DRIFT -> " || bad="$bad L41 --apply did not report the rename: $(one_line "$applied");"
  printf '%s' "$applied" | grep -Eq 'windows converged: [1-9]' || bad="$bad L41 'windows converged' is not ≥ 1 (the rename was not re-verified): $(one_line "$applied");"
  printf '%s' "$applied" | grep -q 'tmux options converged:' || bad="$bad L41 --apply did not report the tmux options pass;"
  case "$(window_name)" in *"$CC"*) ;; *) bad="$bad L41 after --apply the window reads '$(window_name)', not the label;" ;; esac
  # L42: one writer by design — two --apply passes racing on one drift both
  # finish, the window converges once, and a third dry run has nothing left to plan.
  drift
  sleep 1
  pfm name-sync --apply >/tmp/f-ns.a 2>&1 &
  pa=$!
  pfm name-sync --apply >/tmp/f-ns.b 2>&1 &
  pb=$!
  wait "$pa"; ra=$?
  wait "$pb"; rb=$?
  [ "$ra" -eq 0 ] && [ "$rb" -eq 0 ] || bad="$bad L42 concurrent --apply passes exited $ra and $rb: $(one_line "$(cat /tmp/f-ns.a /tmp/f-ns.b)" | cut -c1-200);"
  case "$(window_name)" in *"$CC"*) ;; *) bad="$bad L42 after two racing passes the window reads '$(window_name)';" ;; esac
  after="$(pfm name-sync 2>&1)"
  printf '%s' "$after" | grep -q 'windows planned: 0' || bad="$bad L42 a dry run after the race still plans work: $(one_line "$after");"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "drift LANE-DRIFT planned by the dry run, converged and re-verified by --apply (window '$(window_name)'), two racing --apply passes left one converged name and nothing to plan"
  fi
fi

# ─── F.14 — kill-storm ──────────────────────────────────────────────────────

beat F.14-kill-storm L39
spends "cc:$SEAT+cx"
target "$CC"
n_storm="$(live_storm_rows | grep -c .)"
if [ "$n_storm" -eq 0 ]; then
  blocked F.05-storm "no live STORM_<n> rows to tear down (the storm never came up)"
else
  bad=""
  others_before="$(pfm ls --tsv 2>/dev/null | awk -F'\t' 'NR > 1 && $1 ~ /^live-/ && $5 !~ /^STORM_[0-9]+$/' | wc -l | tr -d ' ')"
  out="$(bash "$DEMO/kill-storm.sh" 2>&1)"
  rc=$?
  closing="$(printf '%s\n' "$out" | grep '^kill-storm:' | tail -1)"
  [ "$rc" -eq 0 ] || bad="$bad kill-storm.sh exited $rc: $(one_line "$out" | cut -c1-200);"
  printf '%s' "$closing" | grep -q 'STORM rows left: 0' || bad="$bad the closing line does not read 'STORM rows left: 0': $(one_line "$closing");"
  [ "$(live_storm_rows | grep -c .)" -eq 0 ] || bad="$bad live STORM rows remain in pfm ls --tsv: $(one_line "$(live_storm_rows | cut -f5)");"
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

beat F.15-addressing C58 C59 C62 C63 C64
spends none
target_live "$CC"
expect-log 'no chat named'
expect-log 'no tmux socket'
if requires; then
  bad=""
  cc_id="$(live_field "$CC" 2)"
  cc_sock="$(sock_base "$CC")"
  # C58: find by excerpt — the chat's own last words lead back to its session.
  excerpt=/tmp/f-excerpt.txt
  pfm chat last "$CC" 2>/dev/null | tail -3 >"$excerpt"
  path=""
  if [ ! -s "$excerpt" ]; then
    bad="$bad C58 the chat's last answer is empty, so no excerpt could be written;"
  else
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
    [ "$rc" -eq 0 ] && printf '%s' "$out" | grep -q 'Appended transcript (.* user records) + env snapshot -> /tmp/f-save.md' ||
      bad="$bad C59 chat save exited $rc: $(one_line "$out");"
    grep -q '# FULL TRANSCRIPT' /tmp/f-save.md 2>/dev/null || bad="$bad C59 the saved file carries no '# FULL TRANSCRIPT' section;"
    grep -q '# ENVIRONMENT SNAPSHOT' /tmp/f-save.md 2>/dev/null || bad="$bad C59 the saved file carries no environment snapshot;"
    # C62: history reads the transcript deep, by path and by sid prefix under the project slug.
    out="$(pfm chat history "$path" 5 2>&1)"
    rc=$?
    [ "$rc" -eq 0 ] && printf '%s' "$out" | grep -q "== $path · last 5 messages ==" || bad="$bad C62 history by path exited $rc: $(one_line "$out");"
    slug="$(printf '%s' "$CWD" | tr / -)"
    out="$(pfm chat history "$(printf '%s' "$cc_id" | cut -c1-8)" 3 "$slug" 2>&1)"
    rc=$?
    [ "$rc" -eq 0 ] && printf '%s' "$out" | grep -q 'last 3 messages ==' || bad="$bad C62 history by sid prefix under slug $slug exited $rc: $(one_line "$out");"
  else
    bad="$bad C59/C62 no transcript path from find ('$path') — save and history were NOT asserted;"
  fi
  # C63: modal deny drives the chat's own tmux session; an unknown session is refused by name.
  out="$(pfm chat modal "$cc_sock" deny 0 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] && [ "$out" = "modal denied on $cc_sock" ] || bad="$bad C63 modal deny 0 on $cc_sock exited $rc: $(one_line "$out");"
  out="$(pfm chat modal no-such-session-f deny 1 2>&1)"
  rc=$?
  [ "$rc" -eq 1 ] && printf '%s' "$out" | grep -q 'no tmux socket for' || bad="$bad C63 modal on an unknown session exited $rc without naming the missing socket: $(one_line "$out");"
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

beat F.16-picker-plumbing X18 X19
spends none
target_live "$CC"
expect-log 'agent open requires'
expect-log 'not one bare socket name'
if requires; then
  bad=""
  # X18: the per-window pane opener validates its request before touching anything.
  out="$(pfm internal agent-open 2>&1)"
  rc=$?
  [ "$rc" -eq 2 ] && printf '%s' "$out" | grep -q 'usage: pfm internal agent-open' || bad="$bad X18 agent-open without flags exited $rc (want 2 + usage): $(one_line "$out");"
  out="$(pfm internal agent-open --id 'lane/unsafe' --cwd "$CWD" 2>&1)"
  rc=$?
  [ "$rc" -eq 1 ] && printf '%s' "$out" | grep -q 'agent open requires a safe session id' || bad="$bad X18 an unsafe id exited $rc without the named refusal: $(one_line "$out");"
  # X19: the shim's session creator — refused for a relative cwd and a foreign
  # prefix, and a REAL server for a lawful socket name, its window named after
  # the engine, torn down as soon as it has been read.
  out="$(pfm internal chat-server cc-1-2-3 relative/dir true 2>&1)"
  rc=$?
  [ "$rc" -eq 2 ] && printf '%s' "$out" | grep -q 'usage: pfm internal chat-server' || bad="$bad X19 a relative cwd exited $rc (want 2 + usage): $(one_line "$out");"
  tmux_dir="$(dirname "$(live_field "$CC" 11)")"
  lane_sock="cc-$(date +%s)-$$-77"
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

beat F.17-additional-k-coverage K10 K11 K27
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
    c1="$(tui_cache)"
    tui_keys C-e
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

beat F.18-e1-chat-survives-storm C22 C28 C32
spends "cc:$SEAT"
target_live "$E1_CHAT"
if requires; then
  if [ "$STORM_RAN" -ne 1 ]; then
    blocked F.05-storm "the storm never ran, so there is no after-storm state to assert $E1_CHAT against"
  else
    bad=""
    out="$(pfm chat status "$E1_CHAT" 2>&1)"
    rc=$?
    [ "$rc" -eq 0 ] && printf '%s' "$out" | grep -qiE 'idle|working' || bad="$bad C22 status exited $rc / '$(one_line "$out")' after the storm;"
    out="$(pfm chat last "$E1_CHAT" 2>&1)"
    rc=$?
    [ "$rc" -eq 0 ] && [ -n "$out" ] || bad="$bad C28 last exited $rc or printed nothing after the storm;"
    out="$(pfm chat inject --allow-unsigned "$E1_CHAT" "reply with exactly one word: STORM-SURVIVED" 2>&1)"
    rc=$?
    if [ "$rc" -ne 0 ]; then
      bad="$bad C32 inject was refused after the storm (exit $rc): $(one_line "$out");"
    elif ! wait_last "$E1_CHAT" STORM-SURVIVED 240; then
      bad="$bad C32 the inject never landed: ${LANE_WAIT_WHY:-no wait reason recorded}; last: $(one_line "$(pfm chat last "$E1_CHAT" 2>&1)");"
    fi
    if [ -n "$bad" ]; then fail "$bad"; else
      pass "$E1_CHAT answered status, last and a delivered inject after $n_storm storm chats came and went beside it"
    fi
  fi
fi

lane_end

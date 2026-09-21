#!/usr/bin/env bash
# O2.sh — lane O2, ops & host, the DESTRUCTIVE TAIL: reaps the socket graveyard
# the fleet left behind, archives and indexes a real transcript history, drives
# headless on both engines, reads doctor's Codex-pane rows, crosses the two
# operator-side seams (reload-while-busy, a dropped seat under a live chat),
# proves the harvester and its sidecar for real, and ENDS THE MACHINE with
# `pfm uninstall`. Runs INSIDE a lane container (run.sh), never on a host.
#
#   run.sh --lanes O2            solo, from a fresh root
#   run.sh                       LAST in the sequence, after A
#
# Every beat asserts from pfm's OWN report (`pfm reap --json`, `pfm ls --tsv`,
# a verb's exit code, a file pfm wrote, the reload worker's own log) or the
# pane, never from a model's prose: a model turn is only ever the STIMULUS that
# gives a wait its needle. Beat ids and their landscape ids are the contract in
# beats.md and map.tsv — check-map.sh fails when this file and those disagree.
#
# Cost: one Claude seat for the stimuli (reap's busy turn, the queued inject and
# reload, the harvester's `ask`), the Codex home for one headless turn, plus —
# solo only — the storm the prelude raises and kills to make the graveyard, one
# Claude chat and one Codex chat standing in for E1's and E2's. Roughly 8 short
# turns.
#
# Every beat before O2.10 that mutates the machine restores what it changed
# (planted sockets killed, holders detached, the dropped seat re-installed, the
# spare chat ended). O2.10 restores NOTHING: it is the end of the machine, and a
# lane that ran after it would be testing an uninstalled host.
#
# BROKEN STATE: the prelude aborts by name when this container carries no pfm
# install (no config, no managed root); a precondition it could not build (E1's
# chat, E2's chat, the graveyard) is a named UNMET line and every beat that needs
# it reports `blocked-by` that need, never ✗ and never silence; a ✗ carries the
# exit code, the first line of pfm's output, and — where two things could have
# gone wrong — which one did, with the raw pane bytes beside it in the lane log.
set -uo pipefail
LANES_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=lib.sh
. "$LANES_DIR/lib.sh"
lane_preamble

CONFIG="$HOME/.config/pfm/pfm.config.json"
MANAGED="$HOME/.local/share/pfm/install"
BLUEPRINT="$HOME/.professor"
E1_MAIN="${E1_CHAT:-E1_MAIN}"
E2_MAIN="${E2_CHAT:-E2_MAIN}"
CWD="${E1_CWD:-/work/orbit}"
EXPRESS="/work/express"
SPARE_CHAT="O2_SPARE"
FORK_CHAT="O2_FORK"
# The chat socket directory, resolved the way pfm resolves it
# (internal/paths: $PFM_TMUX_DIR, else $TMUX_TMPDIR-or-/tmp + tmux-<uid>).
TMUX_DIR="${PFM_TMUX_DIR:-${TMUX_TMPDIR:-/tmp}/tmux-$(id -u)}"
SEAT="$(printf '%s\n' $LANE_SEATS | awk -F: '/^cc:/ { print $2; exit }')"
[ -n "$SEAT" ] || SEAT=1
PORT="$(jq -r '.mcp.http.port // 18377' "$CONFIG" 2>/dev/null || echo 18377)"

lane_begin O2

# ── prelude: what this lane needs, made when missing, no-op otherwise ───────
lane_require_seat_ops "$CONFIG"

need "the blueprint clone at $BLUEPRINT" "[ -e '$BLUEPRINT' ]" "ln -s /worktree '$BLUEPRINT'" ||
  lane_abort "no blueprint clone — pfm install cannot be re-run from it"
need "the managed install root $MANAGED" "[ -d '$MANAGED' ]" "(cd '$BLUEPRINT' && pfm install --yes)" ||
  lane_abort "pfm install has never completed in this container"
need "the working directory $CWD" "[ -d '$CWD/.git' ]" \
  "mkdir -p '$CWD' && git -C '$CWD' init -q && git -C '$CWD' commit -q --allow-empty -m lane" ||
  lane_abort "no working directory for the chats to live in ($CWD)"
need "the pfm MCP daemon on :$PORT" \
  "[ \"\$(curl -s -o /dev/null -w '%{http_code}' -m 2 http://127.0.0.1:$PORT/mcp/chat)\" != 000 ]" \
  "bash /worktree/infra/demo/daemon.sh" ||
  lane_abort "the chat MCP daemon never answered on :$PORT — no Codex chat can call a chat_* tool"

# open_e1_main — E1's own `chat new` line (E1.sh open_main), so a solo O2 asserts
# against the same chat E1 would have left; also the lane's ONE re-open.
open_e1_main() {
  pfm chat new --name "$E1_MAIN" --engine cc --account "$SEAT" --cwd "$CWD" --await --timeout 300 \
    "You are $E1_MAIN, the chat an automated Tier B lane drives. Reply with one word: ready. Then wait and do exactly what each next message says, nothing more." 2>&1
}
# open_e2_main — E2's chat on the Codex home, the target of O2.06's doctor rows.
open_e2_main() {
  pfm chat new --name "$E2_MAIN" --engine cx --cwd "$CWD" --await --timeout 300 \
    "You are $E2_MAIN, the Codex chat an automated Tier B lane drives. Reply with one word: ready. Then wait and do exactly what each next message says, nothing more." 2>&1
}
# make_graveyard — F's storm raised and killed (infra/demo/storm.sh then
# kill-storm.sh): the ended STORM rows become the killed ledger O2.02 archives.
make_graveyard() {
  STORM_CC_ACCOUNT="$SEAT" bash /worktree/infra/demo/storm.sh start 2 1 2>&1 || return 1
  sleep 20
  bash /worktree/infra/demo/kill-storm.sh 2>&1
}
need "E1's chat $E1_MAIN" "live_chat '$E1_MAIN'" 'open_e1_main' || true
need "E2's chat $E2_MAIN (Codex)" "live_chat '$E2_MAIN'" 'open_e2_main' || true
need "a killed-chat graveyard (F's storm, ended and hidden)" "pfm ls --killed 2>/dev/null | grep -q ." 'make_graveyard' || true
lane_reopen 'open_e1_main'

install_again() { (cd "$BLUEPRINT" && pfm install --yes 2>&1); }
hook_commands() { # hook_commands <settings.json> — every command hook's command line
  jq -r '.hooks // {} | to_entries[] | .value[] | .hooks[]? | .command // ""' "$1" 2>/dev/null
}
sock_path() { # sock_path <socket-or-path> — an absolute socket path (pfm ls col 11 may be bare)
  case "$1" in /*) printf '%s' "$1" ;; *) printf '%s/%s' "$TMUX_DIR" "$1" ;; esac
}

# ─── O2.01 — reap over the graveyard: every state a live machine can provoke ─

# The planted sockets: real tmux servers on chat-prefixed names, so the reaper's
# own probe classifies them (internal/reap/reap.go planSocket, in its order):
#   cx-lane-orph     bare shell, Codex prefix (no crumb BY DESIGN) → orph, apply → KILL
#   cc-new-lane-mate bare shell on the teammate prefix               → mate
#   cc-lane-nocrumb  bare shell, Claude prefix, no breadcrumb        → SKIP (busy-unknown)
#   cx-lane-hosts    a `sleep` in the pane — a non-chat process      → hosts
#   cc-lane-dead     server SIGKILLed, socket file aged past 1h      → dead, apply → removed
#   cc-lane-fresh    server SIGKILLed, socket file fresh             → SKIP (younger than 1h)
# and the lane's own chats: busy/active mid-turn and just after, orph once quiet,
# self via $TMUX, keep once a real client is attached (a nested tmux holder).
PLANTED="cx-lane-orph cc-new-lane-mate cc-lane-nocrumb cx-lane-hosts cc-lane-dead cc-lane-fresh"
HOLDERS="lane-hold-e1 lane-hold-e2"
reap_field() { # reap_field <json> <socket-name> <field> — from any of the three groups
  printf '%s' "$1" | jq -r --arg s "$2" --arg f "$3" \
    '[.reap[]?, .spared[]?, .unknown[]?] | map(select(.socket == $s)) | (.[0][$f] // empty)' 2>/dev/null
}
reap_sockets() { printf '%s' "$1" | jq -r '.reap[]?.socket' 2>/dev/null | sort; }
plant_shell() { tmux -S "$TMUX_DIR/$1" -f /dev/null new-session -d -s lane -x 80 -y 24 2>&1; }
plant_cmd() { tmux -S "$TMUX_DIR/$1" -f /dev/null new-session -d -s lane -x 80 -y 24 "$2" 2>&1; }
server_up() { tmux -S "$TMUX_DIR/$1" list-sessions >/dev/null 2>&1; }
kill_planted() { tmux -S "$TMUX_DIR/$1" kill-server >/dev/null 2>&1; rm -f "$TMUX_DIR/$1"; }
# corpse <name> [age-seconds] — a server started then SIGKILLed leaves its
# socket FILE with nothing behind it; the age decides dead vs still-starting.
corpse() {
  local pid
  plant_shell "$1" >/dev/null || return 1
  pid="$(tmux -S "$TMUX_DIR/$1" display-message -p '#{pid}' 2>/dev/null)"
  [ -n "$pid" ] || return 1
  kill -9 "$pid" 2>/dev/null
  sleep 1
  [ -S "$TMUX_DIR/$1" ] || return 1
  [ -n "${2:-}" ] && touch -d "@$(( $(date +%s) - $2 ))" "$TMUX_DIR/$1"
  return 0
}
# hold_attach <holder> <chat socket path> — a real tmux CLIENT on the chat's
# server: a detached tmux server of its own whose one pane runs `tmux attach`
# against the chat (the pane IS the pty a client needs). The holder's socket
# carries no chat prefix, so neither the reaper nor the picker ever sees it.
hold_attach() {
  tmux -S "$TMUX_DIR/$1" -f /dev/null new-session -d -s hold -x 200 -y 50 \
    "TMUX= exec tmux -S '$2' attach-session" >/dev/null 2>&1 || return 1
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    [ "$(tmux -S "$2" display-message -p '#{session_attached}' 2>/dev/null)" -ge 1 ] 2>/dev/null && return 0
    sleep 1
  done
  return 1
}
reap_cleanup() {
  local s
  for s in $HOLDERS; do tmux -S "$TMUX_DIR/$s" kill-server >/dev/null 2>&1; rm -f "$TMUX_DIR/$s"; done
  for s in $PLANTED; do kill_planted "$s"; done
  live_chat "$FORK_CHAT" && pfm chat end "$FORK_CHAT" >/dev/null 2>&1
  return 0
}

beat O2.01-reap L8 L9 L10 L11 L12 L13 L15 L17 L18 L19 L21 L22 L23 L24 L25 L26
spends "cc:$SEAT"
target_live "$E1_MAIN"
if requires; then
  bad="" noted="" fork_state="<not attempted>"
  err=/tmp/o2-reap.err
  e1_sock="$(sock_path "$(live_field "$E1_MAIN" 11)")"
  e1_name="$(basename "$e1_sock")"
  e2_sock=""
  live_chat "$E2_MAIN" && e2_sock="$(sock_path "$(live_field "$E2_MAIN" 11)")"
  reap_cleanup
  # 1. plant — and PROVE each plant answers, or the classification is vacuous.
  for s in cx-lane-orph cc-new-lane-mate cc-lane-nocrumb; do
    out="$(plant_shell "$s")" || bad="$bad could not plant $s: $(one_line "$out");"
  done
  out="$(plant_cmd cx-lane-hosts 'sleep 3600')" || bad="$bad could not plant cx-lane-hosts: $(one_line "$out");"
  corpse cc-lane-dead 7200 || bad="$bad could not leave a dead socket file cc-lane-dead behind;"
  corpse cc-lane-fresh || bad="$bad could not leave a fresh empty socket file cc-lane-fresh behind;"
  for s in cx-lane-orph cc-new-lane-mate cc-lane-nocrumb cx-lane-hosts; do
    server_up "$s" || bad="$bad planted server $s does not answer list-sessions;"
  done
  # 2. busy (L11) / active (L12): a real turn on E1's chat, the sweep run inside it.
  stim="$(pfm chat inject --allow-unsigned "$E1_MAIN" \
    "Count from 1 to 30, one number per line, pausing about a second between numbers. Do not stop early. Then reply with exactly one word: COUNT-DONE" 2>&1)" ||
    bad="$bad the busy stimulus was refused: $(one_line "$stim");"
  sleep 6
  j="$(pfm reap --json --busy-recent 1 2>"$err")"
  rc=$?
  [ "$rc" -eq 0 ] || bad="$bad pfm reap --json mid-turn exited $rc: $(one_line "$(cat "$err")");"
  st="$(reap_field "$j" "$e1_name" state)"
  case "$st" in
    busy | active) noted="$noted mid-turn $E1_MAIN=$st;" ;;
    *) bad="$bad mid-turn $E1_MAIN read '${st:-<no row>}' (want busy or active; reason '$(reap_field "$j" "$e1_name" reason)'; stderr: $(one_line "$(cat "$err")"));" ;;
  esac
  if ! wait_last "$E1_MAIN" COUNT-DONE 240; then
    bad="$bad the counting turn never ended: ${LANE_WAIT_WHY:-no wait reason recorded};"
  fi
  j="$(pfm reap --json 2>"$err")"
  st="$(reap_field "$j" "$e1_name" state)"
  if [ "$st" = active ]; then noted="$noted just-after $E1_MAIN=active;"; else
    bad="$bad just after its turn $E1_MAIN read '${st:-<no row>}' under the 60s default (want active: transcript written within 1m0s; reason '$(reap_field "$j" "$e1_name" reason)');"
  fi
  # 3. the fork (L14): a real detached /chat:branch seat off E1's session.
  e1_sid="$(live_field "$E1_MAIN" 2)"
  fork_out="$(timeout 120 pfm chat branch --engine claude --session-id "$e1_sid" --cwd "$CWD" --name "$FORK_CHAT" 2>&1)"
  fork_rc=$?
  fork_sock=""
  if [ "$fork_rc" -eq 0 ]; then
    sleep 8
    fork_sock="$(live_field "$FORK_CHAT" 11)"
    [ -n "$fork_sock" ] && fork_sock="$(basename "$fork_sock")"
  else
    _lane_log_only "   O2.01: pfm chat branch exited $fork_rc: $(one_line "$fork_out") — the fork state is not observable this run"
  fi
  # 4. the quiet dry run (L8 self via \$TMUX, L10, L13, L15, L18, L19, L22 default = dry).
  sleep 3
  j="$(TMUX="$TMUX_DIR/cx-lane-orph,1,0" pfm reap --json --busy-recent 1 2>"$err")"
  rc=$?
  [ "$rc" -eq 0 ] || bad="$bad the quiet pfm reap --json exited $rc: $(one_line "$(cat "$err")");"
  [ "$(printf '%s' "$j" | jq -r '.apply' 2>/dev/null)" = false ] || bad="$bad --json without --apply reports apply=$(printf '%s' "$j" | jq -r '.apply' 2>/dev/null) (want false);"
  expect_state() { # expect_state <json> <socket> <state> [reason-substring]
    local got reason
    got="$(reap_field "$1" "$2" state)"
    reason="$(reap_field "$1" "$2" reason)"
    if [ "$got" != "$3" ]; then
      bad="$bad $2 read '${got:-<no row>}' (want $3; reason '$reason');"
    elif [ -n "${4:-}" ] && ! printf '%s' "$reason" | grep -qF -- "$4"; then
      bad="$bad $2 is $3 but its reason '$reason' does not name '$4';"
    fi
  }
  expect_state "$j" "$e1_name" orph "unattached"
  expect_state "$j" cx-lane-orph self "own chat"
  expect_state "$j" cc-new-lane-mate mate "teammate"
  expect_state "$j" cc-lane-nocrumb SKIP "no breadcrumb"
  expect_state "$j" cx-lane-hosts hosts "sleep"
  expect_state "$j" cc-lane-dead dead "stale socket file"
  expect_state "$j" cc-lane-fresh SKIP "younger"
  if [ -n "$fork_sock" ]; then
    fork_state="$(reap_field "$j" "$fork_sock" state)"
    fork_state="${fork_state:-<no row for $fork_sock>}"
    noted="$noted fork seat $fork_sock=$fork_state;"
  fi
  # The text table and the flag surface (L24 --horizon, L25 --busy-recent, L26 --json).
  txt="$(pfm reap 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] || bad="$bad the text pfm reap exited $rc: $(one_line "$txt");"
  for needle in '-- REAP (' '-- SPARED (' '-- UNKNOWN (' 'KEEP:' 'dry run — nothing changed'; do
    printf '%s' "$txt" | grep -qF -- "$needle" || bad="$bad the text report lacks '$needle';"
  done
  pfm reap --horizon 1h --busy-recent 5 --json >/dev/null 2>"$err" ||
    bad="$bad pfm reap --horizon 1h --busy-recent 5 --json was refused: $(one_line "$(cat "$err")");"
  pfm reap --horizon -1s >/dev/null 2>&1
  rc=$?
  [ "$rc" -eq 2 ] || bad="$bad pfm reap --horizon -1s exited $rc (want 2, usage);"
  pfm reap --busy-recent -1 >/dev/null 2>&1
  rc=$?
  [ "$rc" -eq 2 ] || bad="$bad pfm reap --busy-recent -1 exited $rc (want 2, usage);"
  pfm reap stray-argument >/dev/null 2>&1
  rc=$?
  [ "$rc" -eq 2 ] || bad="$bad pfm reap with a positional argument exited $rc (want 2, usage);"
  # 5. keep (L9): a real client attached to each lane chat — and the GUARD before
  # --apply: a lane chat that would be reaped stops the apply cold, by name.
  hold_attach lane-hold-e1 "$e1_sock" || bad="$bad could not attach a holder client to $E1_MAIN's server ($e1_sock);"
  if [ -n "$e2_sock" ]; then
    hold_attach lane-hold-e2 "$e2_sock" || bad="$bad could not attach a holder client to $E2_MAIN's server ($e2_sock);"
  fi
  j_dry="$(pfm reap --json 2>"$err")"
  rc=$?
  [ "$rc" -eq 0 ] || bad="$bad the pre-apply pfm reap --json exited $rc: $(one_line "$(cat "$err")");"
  expect_state "$j_dry" "$e1_name" keep "attached"
  [ -n "$e2_sock" ] && expect_state "$j_dry" "$(basename "$e2_sock")" keep "attached"
  apply_ok=1
  for s in "$e1_name" ${e2_sock:+"$(basename "$e2_sock")"}; do
    st="$(reap_field "$j_dry" "$s" state)"
    [ "$st" = keep ] || { apply_ok=0; bad="$bad refusing --apply: lane chat socket $s reads '$st', the sweep would take it;"; }
  done
  if [ "$apply_ok" -eq 1 ]; then
    # 6. --apply (L17 KILL, L21 actions, L23, L22 plan parity): the same sweep, performed.
    j_apply="$(pfm reap --apply --json 2>"$err")"
    rc=$?
    [ "$rc" -eq 0 ] || bad="$bad pfm reap --apply --json exited $rc (failed=$(printf '%s' "$j_apply" | jq -r '.failed' 2>/dev/null)): $(one_line "$(cat "$err")");"
    [ "$(printf '%s' "$j_apply" | jq -r '.apply' 2>/dev/null)" = true ] || bad="$bad --apply --json reports apply!=true;"
    [ "$(printf '%s' "$j_apply" | jq -r '.failed' 2>/dev/null)" = 0 ] || bad="$bad --apply reports failed=$(printf '%s' "$j_apply" | jq -r '.failed' 2>/dev/null) (want 0);"
    if [ "$(reap_sockets "$j_dry")" != "$(reap_sockets "$j_apply")" ]; then
      bad="$bad the dry-run REAP set [$(reap_sockets "$j_dry" | tr '\n' ' ')] differs from the applied REAP set [$(reap_sockets "$j_apply" | tr '\n' ' ')] — the preview did not match the run;"
    fi
    expect_state "$j_apply" cx-lane-orph KILL
    expect_state "$j_apply" cc-lane-dead dead
    [ "$(printf '%s' "$j_apply" | jq -r '.killed' 2>/dev/null)" -ge 1 ] 2>/dev/null || bad="$bad --apply reports killed=$(printf '%s' "$j_apply" | jq -r '.killed' 2>/dev/null) (want ≥1: cx-lane-orph);"
    [ "$(printf '%s' "$j_apply" | jq -r '.dead_files' 2>/dev/null)" -ge 1 ] 2>/dev/null || bad="$bad --apply reports dead_files=$(printf '%s' "$j_apply" | jq -r '.dead_files' 2>/dev/null) (want ≥1: cc-lane-dead);"
    sleep 1
    [ ! -e "$TMUX_DIR/cc-lane-dead" ] || bad="$bad the dead socket file cc-lane-dead is still on disk after --apply;"
    server_up cx-lane-orph && bad="$bad cx-lane-orph's server still answers after --apply reported it KILLed;"
    server_up cc-lane-nocrumb || bad="$bad --apply took the SKIP socket cc-lane-nocrumb (busy-unknown must never be reaped);"
    server_up cx-lane-hosts || bad="$bad --apply took the hosts socket cx-lane-hosts (a socket hosting non-chat processes is never reapable);"
    server_up cc-new-lane-mate || bad="$bad --apply took the mate socket cc-new-lane-mate;"
    [ -S "$TMUX_DIR/cc-lane-fresh" ] || bad="$bad --apply removed the FRESH empty socket cc-lane-fresh (younger than 1h must be left alone);"
    live_chat "$E1_MAIN" || bad="$bad $E1_MAIN has no live row after --apply — the attached lane chat was reaped;"
  fi
  reap_cleanup
  live_chat "$FORK_CHAT" && bad="$bad the fork seat $FORK_CHAT is still live after cleanup;"
  unprovoked="IDLE (an attached client idle 1h+ with the transcript's last record past the 48h horizon), UNKN (an attached socket whose client-activity probe answers nobody, or an unreadable transcript)"
  [ "$fork_state" = fork ] || unprovoked="fork (the branch seat read '$fork_state' — a crumb lands before the sweep sees it untouched), $unprovoked"
  if [ -n "$bad" ]; then
    fail "$bad"
  else
    pass "every live-provokable state held ($noted self/mate/SKIP×2/hosts/dead/orph/keep/KILL); --json/--horizon/--busy-recent/dry=apply parity; text table"
  fi
fi

# ─── O2.01b — the three states no live fleet can provoke ────────────────────
# fork needs a branch seat the sweep sees before its crumb lands; IDLE and UNKN
# need an attached client idle for 1h+ or a transcript made unreadable under a
# live chat — a scripted engine (Wave 7's mock-engine) AND a clock door (`pfm
# reap` has no --now). Named here so O2.01's sixteen real assertions can be
# green while these three stay a visible gap, never a silent one.

beat O2.01b-reap-unprovokable L14 L16 L20
spends none
if requires O2.01-reap; then
  blocked wave7-mock-engine "unprovoked without a scripted engine and a clock door (pfm reap has no --now): ${unprovoked:-fork, IDLE, UNKN}"
fi

# ─── O2.02 — archive over a real history ────────────────────────────────────

beat O2.02-archive X6 X7 X8 X9 X10
spends none
killed_rows="$(pfm ls --killed 2>&1)"
killed_rc=$?
if [ "$killed_rc" -ne 0 ]; then
  fail "pfm ls --killed exited $killed_rc — the killed ledger cannot be read: $(one_line "$killed_rows")"
elif [ -z "$killed_rows" ]; then
  blocked "need graveyard" "the killed ledger is empty — F's kill-storm did not run and the prelude's storm could not make one, so archive has nothing real to move"
else
  bad=""
  plan="$(pfm archive 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] || bad="$bad pfm archive (dry run) exited $rc: $(one_line "$plan");"
  printf '%s' "$plan" | grep -qF 'mode: killed chats' || bad="$bad the dry run does not name its mode: $(one_line "$plan");"
  printf '%s' "$plan" | grep -qF 'dry run — nothing moved' || bad="$bad the dry run does not say it moved nothing;"
  n_plan="$(printf '%s\n' "$plan" | grep -c '^  plan ')"
  [ "$n_plan" -ge 1 ] || bad="$bad the dry run planned no move over $(printf '%s\n' "$killed_rows" | grep -c .) killed row(s): $(one_line "$plan");"
  applied="$(pfm archive --apply 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] || bad="$bad pfm archive --apply exited $rc: $(one_line "$applied");"
  n_moved="$(printf '%s\n' "$applied" | grep -c '^  moved ')"
  [ "$n_moved" -eq "$n_plan" ] || bad="$bad --apply moved $n_moved file(s) but the dry run planned $n_plan — the plan and the run disagree;"
  printf '%s' "$applied" | grep -qE 'kills retired: [0-9]+' || bad="$bad --apply printed no 'kills retired' count;"
  manifest="$(printf '%s\n' "$applied" | sed -n 's/^manifest: //p' | tail -1)"
  if [ -z "$manifest" ]; then
    bad="$bad --apply named no manifest path;"
  elif [ ! -s "$manifest" ]; then
    bad="$bad the manifest --apply named ($manifest) is missing or empty;"
  else
    [ "$(head -1 "$manifest")" = "$(printf 'uuid\tengine\torig_path\tbytes\tarchived_path\tarchived_at')" ] ||
      bad="$bad the manifest header is not the documented TSV: $(one_line "$(head -1 "$manifest")");"
    last_id="$(tail -1 "$manifest" | cut -f1)"
    last_orig="$(tail -1 "$manifest" | cut -f3)"
    last_arch="$(tail -1 "$manifest" | cut -f5)"
    [ -f "$last_arch" ] || bad="$bad the manifest's last row points at $last_arch, which is not on disk;"
    [ ! -e "$last_orig" ] || bad="$bad after --apply the original $last_orig is still in place;"
    restored="$(pfm archive --restore "$last_id" 2>&1)"
    rc=$?
    if [ "$rc" -ne 0 ]; then
      bad="$bad pfm archive --restore $last_id exited $rc: $(one_line "$restored");"
    else
      printf '%s' "$restored" | grep -qF "restored $last_id -> $last_orig" || bad="$bad --restore did not report 'restored <id> -> <orig>': $(one_line "$restored");"
      [ -f "$last_orig" ] || bad="$bad --restore exited 0 but $last_orig is not back on disk;"
      pfm chat kill "$last_id" >/dev/null 2>&1 # re-hide the row the restore brought back
    fi
    twice="$(pfm archive --restore "$last_id" 2>&1)"
    rc=$?
    [ "$rc" -ne 0 ] || bad="$bad a second --restore of $last_id was ACCEPTED (exit 0) though the original is back in place;"
    printf '%s' "$twice" | grep -qiE 'already exists|not in the archive manifest|missing' ||
      bad="$bad the refused second --restore did not say why: $(one_line "$twice");"
  fi
  sub="$(pfm archive --subagents --older-than 0 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] || bad="$bad pfm archive --subagents --older-than 0 exited $rc: $(one_line "$sub");"
  printf '%s' "$sub" | grep -qF 'mode: sidechain transcripts' || bad="$bad --subagents does not name its mode: $(one_line "$sub");"
  printf '%s' "$sub" | grep -qE 'younger than the age gate \(left alone\): [0-9]+' || bad="$bad --subagents printed no age-gate count;"
  printf '%s' "$sub" | grep -qF 'dry run — nothing moved' || bad="$bad --subagents without --apply did not say it moved nothing;"
  prune="$(pfm archive --prune-orphans 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] || bad="$bad pfm archive --prune-orphans exited $rc: $(one_line "$prune");"
  printf '%s' "$prune" | grep -qE 'orphaned kill\(s\); re-run with --yes to delete' || bad="$bad --prune-orphans did not report its count and the --yes hint: $(one_line "$prune");"
  pruned="$(pfm archive --prune-orphans --yes 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] || bad="$bad pfm archive --prune-orphans --yes exited $rc: $(one_line "$pruned");"
  printf '%s' "$pruned" | grep -qE 'pruned [0-9]+ orphaned kill\(s\)' || bad="$bad --prune-orphans --yes did not report what it pruned: $(one_line "$pruned");"
  pfm archive --yes >/dev/null 2>&1
  rc=$?
  [ "$rc" -eq 2 ] || bad="$bad --yes without --prune-orphans exited $rc (want 2, usage);"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "$n_plan planned = $n_moved moved (manifest $manifest); --restore put $last_id back and refused a second time; --subagents and --prune-orphans [--yes] each reported their counts"
  fi
fi

# ─── O2.03 — index ───────────────────────────────────────────────────────────

beat O2.03-index X5
spends none
bad=""
idx="$(pfm index 2>&1)"
rc=$?
[ "$rc" -eq 0 ] || bad="$bad pfm index exited $rc: $(one_line "$idx");"
printf '%s' "$idx" | grep -qE '^files=[0-9]+ skipped=[0-9]+ delta=[0-9]+ full=[0-9]+ deleted=[0-9]+ touched=[0-9]+ bytes=[0-9]+ cx_names=(true|false)$' ||
  bad="$bad pfm index did not print its counters line: $(one_line "$idx");"
files="$(printf '%s' "$idx" | sed -n 's/^files=\([0-9]*\) .*/\1/p')"
[ "${files:-0}" -ge 1 ] || bad="$bad pfm index saw files=${files:-<none>} — a fleet with a live chat and a graveyard has transcripts to index;"
full_err=/tmp/o2-index.err
full="$(pfm index --full --progress 2>"$full_err")"
rc=$?
[ "$rc" -eq 0 ] || bad="$bad pfm index --full --progress exited $rc: $(one_line "$(cat "$full_err")");"
grep -qF 'pfm index: scanning' "$full_err" || bad="$bad --progress did not announce the scan on stderr;"
grep -qE 'pfm index: done in [0-9.]+m?s' "$full_err" || bad="$bad --progress did not report its elapsed time on stderr: $(one_line "$(cat "$full_err")");"
full_n="$(printf '%s' "$full" | sed -n 's/^.* full=\([0-9]*\) .*/\1/p')"
[ "${full_n:-0}" -ge 1 ] || bad="$bad --full reparsed full=${full_n:-<none>} file(s) (want ≥1 over ${files:-?} indexed files);"
pfm index stray >/dev/null 2>&1
rc=$?
[ "$rc" -eq 2 ] || bad="$bad pfm index with a positional argument exited $rc (want 2, usage);"
if [ -n "$bad" ]; then fail "$bad"; else
  pass "incremental: $(one_line "$idx") · --full reparsed $full_n with progress on stderr"
fi

# ─── O2.04 — headless on cc and cx ──────────────────────────────────────────

beat O2.04-headless X15 X16 X17 K17
spends "cc:$SEAT+cx"
bad=""
hl_err=/tmp/o2-headless.err
cc_json="$(pfm headless exec --engine claude --account "$SEAT" --cwd /tmp --timeout 300 --output-format json \
  --tools "" --setting-sources "" --strict-mcp-config --no-session-persistence \
  --prompt "Reply with exactly one word: HEADLESS-CC" 2>"$hl_err")"
rc=$?
if [ "$rc" -ne 0 ]; then
  bad="$bad headless exec --engine claude exited $rc: $(one_line "$(cat "$hl_err")");"
elif ! printf '%s' "$cc_json" | jq -e . >/dev/null 2>&1; then
  bad="$bad --output-format json on claude is not JSON: $(one_line "$cc_json");"
else
  [ "$(printf '%s' "$cc_json" | jq -r '.engine')" = cc ] || bad="$bad the claude envelope reports engine=$(printf '%s' "$cc_json" | jq -r '.engine') (want cc);"
  [ "$(printf '%s' "$cc_json" | jq -r '.is_error')" = false ] || bad="$bad the claude envelope reports is_error=true;"
  [ "$(printf '%s' "$cc_json" | jq -r '.exit_code')" = 0 ] || bad="$bad the claude envelope reports exit_code=$(printf '%s' "$cc_json" | jq -r '.exit_code');"
  printf '%s' "$cc_json" | jq -r '.result' | grep -qF HEADLESS-CC || bad="$bad the claude result carries no HEADLESS-CC needle: $(one_line "$(printf '%s' "$cc_json" | jq -r '.result')");"
fi
schema='{"type":"object","properties":{"word":{"type":"string"}},"required":["word"],"additionalProperties":false}'
cc_schema="$(pfm headless exec --engine claude --account "$SEAT" --cwd /tmp --timeout 300 --output-format json \
  --tools "" --setting-sources "" --strict-mcp-config --no-session-persistence --json-schema "$schema" \
  --prompt "Return a JSON object whose word field is HEADLESS-SCHEMA" 2>"$hl_err")"
rc=$?
if [ "$rc" -ne 0 ]; then
  bad="$bad headless exec --json-schema on claude exited $rc: $(one_line "$(cat "$hl_err")");"
else
  word="$(printf '%s' "$cc_schema" | jq -r '.structured_output.word // empty' 2>/dev/null)"
  [ -n "$word" ] || bad="$bad --json-schema produced no structured_output.word: $(one_line "$cc_schema");"
fi
pfm headless exec --engine claude --json-schema '{not json' --prompt x >/dev/null 2>"$hl_err"
rc=$?
[ "$rc" -eq 2 ] || bad="$bad an invalid --json-schema exited $rc (want 2): $(one_line "$(cat "$hl_err")");"
# Codex: the isolation controls are UNSUPPORTED there and must be refused by
# name, then accepted only under --allow-unsupported with a diagnostic.
expect-log 'unsupported'
pfm headless exec --engine codex --cwd /tmp --timeout 30 --tools "" --prompt "x" >/dev/null 2>"$hl_err"
rc=$?
if [ "$rc" -eq 0 ]; then
  bad="$bad headless exec --engine codex --tools '' was ACCEPTED (exit 0) — Codex cannot honour tools isolation;"
elif ! grep -qF -- '--allow-unsupported' "$hl_err"; then
  bad="$bad the codex --tools refusal (exit $rc) does not name --allow-unsupported: $(one_line "$(cat "$hl_err")");"
fi
cx_json="$(pfm headless exec --engine codex --cwd /tmp --timeout 300 --output-format json --allow-unsupported --strict-mcp-config \
  --prompt "Reply with exactly one word: HEADLESS-CX" 2>"$hl_err")"
rc=$?
if [ "$rc" -ne 0 ]; then
  bad="$bad headless exec --engine codex exited $rc: $(one_line "$(cat "$hl_err")");"
elif ! printf '%s' "$cx_json" | jq -e . >/dev/null 2>&1; then
  bad="$bad --output-format json on codex is not JSON: $(one_line "$cx_json");"
else
  [ "$(printf '%s' "$cx_json" | jq -r '.engine')" = cx ] || bad="$bad the codex envelope reports engine=$(printf '%s' "$cx_json" | jq -r '.engine') (want cx);"
  printf '%s' "$cx_json" | jq -r '.result' | grep -qF HEADLESS-CX || bad="$bad the codex result carries no HEADLESS-CX needle: $(one_line "$(printf '%s' "$cx_json" | jq -r '.result')");"
  printf '%s' "$cx_json" | jq -r '.diagnostics[]? // empty' | grep -qF 'unsupported control not applied for Codex: --strict-mcp-config' ||
    bad="$bad the codex envelope under --allow-unsupported carries no diagnostic naming --strict-mcp-config: $(one_line "$(printf '%s' "$cx_json" | jq -c '.diagnostics')");"
fi
# K17: the scripting front — `headless run` IS `chat new`, `headless transcript` IS `chat read`.
run_help="$(pfm headless run --help 2>&1)"
run_rc=$?
new_help="$(pfm chat new --help 2>&1)"
new_rc=$?
[ "$run_rc" -eq "$new_rc" ] && [ "$run_help" = "$new_help" ] ||
  bad="$bad pfm headless run --help (exit $run_rc) is not pfm chat new --help (exit $new_rc) — the alias diverged: $(one_line "$run_help");"
if live_chat "$E1_MAIN"; then
  tr_out="$(pfm headless transcript "$E1_MAIN" --tail 2 --condensed 2>&1)"
  tr_rc=$?
  rd_out="$(pfm chat read "$E1_MAIN" --tail 2 --condensed 2>&1)"
  rd_rc=$?
  [ "$tr_rc" -eq "$rd_rc" ] || bad="$bad pfm headless transcript exited $tr_rc but pfm chat read exited $rd_rc ($(one_line "$rd_out"));"
  [ -n "$rd_out" ] || bad="$bad pfm chat read printed nothing for a live chat, so the alias had nothing to match;"
  [ "$tr_rc" -eq 0 ] || bad="$bad pfm headless transcript $E1_MAIN exited $tr_rc: $(one_line "$tr_out");"
  [ -n "$tr_out" ] || bad="$bad pfm headless transcript printed nothing for a live chat;"
else
  bad="$bad $E1_MAIN has no live row — the transcript alias was NOT asserted (no chat to read);"
fi
if [ -n "$bad" ]; then fail "$bad"; else
  pass "claude: JSON envelope + HEADLESS-CC, --json-schema structured_output.word='$word', invalid schema exit 2 · codex: --tools refused naming --allow-unsupported, then HEADLESS-CX with the diagnostic · run/transcript aliases match chat new/read"
fi

# ─── O2.05 — the internal plumbing verbs ────────────────────────────────────

beat O2.05-internal-plumbing T34 T37 X21 X22 X26 X29 X30 X32 X33 X34 X37 X40
spends none
bad=""
int_err=/tmp/o2-internal.err
# X21 claude-version: the newest retained native Claude version's path, or 127
# when the native versions dir holds none — the fence installs Claude from npm
# (infra/demo/setup.sh tools), so 127 with that dir ABSENT is the correct answer
# here; 127 with versions on disk, or a path that does not exist, is not.
versions_dir="$HOME/.local/share/claude/versions"
cv="$(pfm internal claude-version 2>"$int_err")"
rc=$?
case "$rc" in
  0) [ -e "$cv" ] || bad="$bad claude-version printed '$cv' (exit 0) but nothing exists there;" ;;
  127)
    if [ -n "$(find "$versions_dir" -maxdepth 1 -type f -perm -u+x 2>/dev/null | head -1)" ]; then
      bad="$bad claude-version exited 127 (none retained) while $versions_dir holds executable versions;"
    else
      cv="<none retained: $versions_dir has no executable version, npm-installed Claude>"
    fi ;;
  *) bad="$bad claude-version exited $rc: $(one_line "$(cat "$int_err")");" ;;
esac
# X22 clear-kill: fail-open — an unknown session on a SessionEnd/clear payload exits 0 and hides nothing.
printf '{"hook_event_name":"SessionEnd","reason":"clear","session_id":"lane-no-such-session"}' | pfm internal clear-kill >/dev/null 2>"$int_err"
rc=$?
[ "$rc" -eq 0 ] || bad="$bad clear-kill on an unknown session exited $rc (want 0, fail-open): $(one_line "$(cat "$int_err")");"
printf 'not json' | pfm internal clear-kill >/dev/null 2>&1
rc=$?
[ "$rc" -eq 0 ] || bad="$bad clear-kill on a malformed payload exited $rc (want 0, fail-open);"
pfm internal clear-kill stray </dev/null >/dev/null 2>&1
rc=$?
[ "$rc" -eq 2 ] || bad="$bad clear-kill with a positional argument exited $rc (want 2, usage);"
# X26 epic-inject: from a plain shell there is no chat identity — fail-open, exit 0, nothing injected.
ei="$(printf '{"transcript_path":"/tmp/none.jsonl","cwd":"%s"}' "$CWD" | pfm internal epic-inject 2>"$int_err")"
rc=$?
[ "$rc" -eq 0 ] || bad="$bad epic-inject outside a chat exited $rc (want 0, fail-open): $(one_line "$(cat "$int_err")");"
[ -z "$ei" ] || bad="$bad epic-inject outside a chat printed '$(one_line "$ei")' (want nothing);"
grep -qiE 'identify chat|window name|not inside tmux|no tmux' "$int_err" ||
  bad="$bad epic-inject outside a chat did not name the missing identity on stderr: $(one_line "$(cat "$int_err")");"
# X29 explore-deny: denies a non-haiku Explore by name, lets haiku and every other agent through.
deny="$(printf '{"tool_input":{"subagent_type":"Explore","model":"sonnet"}}' | pfm internal explore-deny 2>&1)"
rc=$?
[ "$rc" -eq 0 ] || bad="$bad explore-deny exited $rc: $(one_line "$deny");"
[ "$(printf '%s' "$deny" | jq -r '.hookSpecificOutput.permissionDecision // empty' 2>/dev/null)" = deny ] ||
  bad="$bad explore-deny did not deny a sonnet Explore: $(one_line "$deny");"
printf '%s' "$deny" | jq -r '.hookSpecificOutput.permissionDecisionReason // empty' 2>/dev/null | grep -qF 'tracer' ||
  bad="$bad the Explore denial does not point at the tracer agent;"
allow="$(printf '{"tool_input":{"subagent_type":"Explore","model":"haiku"}}' | pfm internal explore-deny 2>&1)"
[ -z "$allow" ] || bad="$bad explore-deny blocked a haiku Explore: $(one_line "$allow");"
other="$(printf '{"tool_input":{"subagent_type":"tracer","model":"sonnet"}}' | pfm internal explore-deny 2>&1)"
[ -z "$other" ] || bad="$bad explore-deny blocked a tracer dispatch: $(one_line "$other");"
# X30 kill-exit: the pane-death finisher refuses to run without its full address.
pfm internal kill-exit >/dev/null 2>"$int_err"
rc=$?
[ "$rc" -eq 2 ] || bad="$bad kill-exit with no flags exited $rc (want 2, usage): $(one_line "$(cat "$int_err")");"
pfm internal kill-exit --engine cc --id x --socket /tmp/none --socket-name none >/dev/null 2>"$int_err"
rc=$?
[ "$rc" -eq 2 ] || bad="$bad kill-exit missing --pane exited $rc (want 2, usage);"
# X32 launcher-repair: idempotent on a healthy launcher, and the shim stays.
lr="$(pfm internal launcher-repair 2>&1)"
rc=$?
[ "$rc" -eq 0 ] || bad="$bad launcher-repair exited $rc: $(one_line "$lr");"
[ -e "$HOME/.local/bin/claude" ] || bad="$bad the Claude launcher shim is gone after launcher-repair;"
pfm internal launcher-repair stray >/dev/null 2>&1
rc=$?
[ "$rc" -eq 2 ] || bad="$bad launcher-repair with a positional argument exited $rc (want 2);"
# X33/X34 primary-get / primary-set: the roster gate, then the value round-trips, then restored.
primary0="$(pfm internal primary-get 2>&1)"
rc=$?
[ "$rc" -eq 0 ] && printf '%s' "$primary0" | grep -qE '^[0-9]+$' || bad="$bad primary-get exited $rc with '$(one_line "$primary0")' (want an account number);"
ps_out="$(pfm internal primary-set 999 2>&1)"
rc=$?
[ "$rc" -eq 1 ] || bad="$bad primary-set 999 (not in the roster) exited $rc (want 1);"
printf '%s' "$ps_out" | grep -qF 'not in the configured roster' || bad="$bad primary-set 999 did not name the roster: $(one_line "$ps_out");"
pfm internal primary-set >/dev/null 2>&1
rc=$?
[ "$rc" -eq 2 ] || bad="$bad primary-set without an account exited $rc (want 2, usage);"
pfm internal primary-set "$SEAT" >/dev/null 2>"$int_err"
rc=$?
[ "$rc" -eq 0 ] || bad="$bad primary-set $SEAT exited $rc: $(one_line "$(cat "$int_err")");"
[ "$(pfm internal primary-get 2>/dev/null)" = "$SEAT" ] || bad="$bad primary-get reads $(pfm internal primary-get 2>/dev/null) after primary-set $SEAT;"
if printf '%s' "$primary0" | grep -qE '^[0-9]+$'; then
  pfm internal primary-set "$primary0" >/dev/null 2>&1 || bad="$bad could not restore the primary account to $primary0;"
fi
# X37 stale: a report either way — 'none' or STALE rows — never silence.
st="$(pfm internal stale 2>"$int_err")"
rc=$?
[ "$rc" -eq 0 ] || bad="$bad pfm internal stale exited $rc: $(one_line "$(cat "$int_err")") $(one_line "$st");"
printf '%s' "$st" | grep -qE '^stale: (none|[0-9]+ process)' || bad="$bad stale printed no verdict line: $(one_line "$st");"
pfm internal stale --bogus >/dev/null 2>&1
rc=$?
[ "$rc" -eq 2 ] || bad="$bad stale with an unknown flag exited $rc (want 2);"
# X40/T34 tmux-title-renudge: sweeps every live socket and exits 0.
tr_out="$(pfm internal tmux-title-renudge 2>&1)"
rc=$?
[ "$rc" -eq 0 ] || bad="$bad tmux-title-renudge exited $rc: $(one_line "$tr_out");"
pfm internal tmux-title-renudge stray >/dev/null 2>&1
rc=$?
[ "$rc" -eq 2 ] || bad="$bad tmux-title-renudge with a positional argument exited $rc (want 2);"
# the statusline alias: `pfm internal statusline` renders what `pfm statusline` renders.
payload="$(printf '{"session_id":"lane-none","model":{"display_name":"sonnet"},"workspace":{"current_dir":"%s"}}' "$CWD")"
sl_a="$(printf '%s' "$payload" | pfm internal statusline 2>&1)"
sl_a_rc=$?
sl_b="$(printf '%s' "$payload" | pfm statusline 2>&1)"
sl_b_rc=$?
[ "$sl_a_rc" -eq 0 ] || bad="$bad pfm internal statusline exited $sl_a_rc: $(one_line "$sl_a");"
[ "$sl_a_rc" -eq "$sl_b_rc" ] || bad="$bad pfm internal statusline exited $sl_a_rc but pfm statusline exited $sl_b_rc;"
[ -n "$sl_a" ] || bad="$bad pfm internal statusline rendered nothing;"
[ -n "$sl_b" ] || bad="$bad pfm statusline rendered nothing, so the alias had nothing to match;"
# T37: the installed /reload card carries reload.Usage itself, never the token.
card="$SEAT_DIR/commands/reload.md"
usage1="$(pfm chat reload --help 2>&1 | head -1)"
if [ ! -f "$card" ]; then
  bad="$bad no /reload command card at $card;"
else
  grep -qF '{{RELOAD_USAGE}}' "$card" && bad="$bad $card still carries the unsubstituted {{RELOAD_USAGE}} token;"
  grep -qF 'usage: pfm chat reload [--account N]' "$card" || bad="$bad $card does not carry reload.Usage's first line;"
  printf '%s' "$usage1" | grep -qF 'usage: pfm chat reload [--account N]' || bad="$bad pfm chat reload --help does not print reload.Usage: $(one_line "$usage1");"
fi
pfm internal no-such-verb >/dev/null 2>"$int_err"
rc=$?
[ "$rc" -eq 1 ] || bad="$bad an unknown internal verb exited $rc (want 1, non-blocking for a hook);"
grep -qF 'registered by a different pfm version' "$int_err" || bad="$bad the unknown-verb refusal does not explain itself: $(one_line "$(cat "$int_err")");"
if [ -n "$bad" ]; then fail "$bad"; else
  pass "claude-version → $cv · clear-kill/epic-inject fail-open · explore-deny denies sonnet Explore naming tracer, passes haiku and tracer · kill-exit usage 2 · launcher-repair 0 · primary $primary0→$SEAT→$primary0 (999 refused by roster) · stale: $(one_line "$st" | cut -c1-60) · title-renudge 0 · statusline alias matches · /reload card carries reload.Usage"
fi

# ─── O2.05b — the activity-log reader ───────────────────────────────────────

beat O2.05b-activity-log X42
spends none
bad=""
log_err=/tmp/o2-log.err
# X42 pfm log: every earlier beat of this lane ran pfm at debug, so the reader
# has records to show. An EMPTY answer here is a reader that could not read,
# never "nothing happened".
all="$(pfm log --since 60m 2>"$log_err")"
rc=$?
[ "$rc" -eq 0 ] || bad="$bad pfm log --since 60m exited $rc: $(one_line "$(cat "$log_err")");"
n_all="$(printf "%s\n" "$all" | grep -c .)"
[ "$n_all" -gt 0 ] || bad="$bad pfm log --since 60m printed nothing after a lane of debug-level pfm runs: $(one_line "$(cat "$log_err")");"
# A filter narrows, never widens.
errs="$(pfm log --since 60m --level error 2>"$log_err")"
rc=$?
[ "$rc" -eq 0 ] || bad="$bad pfm log --level error exited $rc: $(one_line "$(cat "$log_err")");"
n_err="$(printf "%s\n" "$errs" | grep -c .)"
[ "$n_err" -le "$n_all" ] || bad="$bad --level error printed $n_err record(s), more than the unfiltered $n_all;"
# A refused filter is a usage error that names the accepted set — never an empty, exit-0 listing.
pfm log --comp lane-no-such-component >/dev/null 2>"$log_err"
rc=$?
[ "$rc" -eq 2 ] || bad="$bad pfm log --comp <unknown> exited $rc (want 2, usage);"
grep -qF "is not one of" "$log_err" || bad="$bad pfm log --comp <unknown> did not name the accepted components: $(one_line "$(cat "$log_err")");"
pfm log --level lane-no-such-level >/dev/null 2>"$log_err"
rc=$?
[ "$rc" -eq 2 ] || bad="$bad pfm log --level <unknown> exited $rc (want 2, usage);"
pfm log stray >/dev/null 2>&1
rc=$?
[ "$rc" -eq 2 ] || bad="$bad pfm log with a positional argument exited $rc (want 2, usage);"
if [ -n "$bad" ]; then fail "$bad"; else
  pass "pfm log --since 60m: $n_all record(s), --level error: $n_err · unknown --comp and --level exit 2 naming the accepted set · a positional argument exits 2"
fi

# ─── O2.06 — doctor's codex_pane rows while E2's chat lives ─────────────────

beat O2.06-doctor-codex-pane I63
spends none
target "$E2_MAIN"
if ! live_chat "$E2_MAIN"; then
  blocked "need $E2_MAIN" "no live Codex chat — E2 did not run before this lane and the prelude's pfm chat new --engine cx could not open one (its UNMET line is above)"
else
  bad=""
  doc="$(pfm doctor 2>&1)"
  doc_rc=$?
  [ "$doc_rc" -le 1 ] || bad="$bad pfm doctor exited $doc_rc, so its codex_pane rows cannot be trusted: $(one_line "$(printf '%s\n' "$doc" | grep -m1 -E 'unhealthy|broken|error=' || printf '%s' "$doc" | tail -1)");"
  bind="$(printf '%s\n' "$doc" | grep -m1 '^doctor: codex_pane_bindings total=')"
  panes="$(printf '%s\n' "$doc" | grep -m1 '^doctor: codex_panes live=')"
  warns="$(printf '%s\n' "$doc" | grep -E '^doctor: warning codex_pane')"
  [ -n "$bind" ] || bad="$bad doctor printed no 'codex_pane_bindings total=' row;"
  [ -n "$panes" ] || bad="$bad doctor printed no 'codex_panes live=' row;"
  case "$panes" in
    *"live=0"*) bad="$bad doctor reads codex_panes live=0 while $E2_MAIN has a live row ($(one_line "$(live_row "$E2_MAIN")")) — the pane roster missed it;" ;;
  esac
  printf '%s' "$bind" | grep -qE ' contested=0 ' || bad="$bad a contested binding while one Codex chat lives: $bind;"
  printf '%s' "$bind" | grep -qE ' retired=0 ' || bad="$bad a retired-thread binding while one Codex chat lives: $bind;"
  printf '%s' "$bind" | grep -qE ' undecodable=0$' || bad="$bad an undecodable binding: $bind;"
  printf '%s' "$panes" | grep -qE ' unfollowable=0$' || bad="$bad an unfollowable Codex pane: $panes;"
  [ -z "$warns" ] || bad="$bad codex_pane warning row(s): $(one_line "$warns");"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "exit $doc_rc · $bind · $panes · no codex_pane warning"
  fi
fi

# ─── O2.07 — reload while busy, from the OPERATOR's side ────────────────────

beat O2.07-reload-while-busy-operator L32
spends "cc:$SEAT"
target_live "$E1_MAIN"
if requires; then
  bad=""
  # The socket is read from the LIVE row at this beat and handed to pfm exactly
  # as `pfm ls --tsv` spelled it (E1.08 does the same); a reboot in place must
  # leave that value unchanged.
  sock="$(live_field "$E1_MAIN" 11)"
  stim="$(pfm chat inject --allow-unsigned "$E1_MAIN" \
    "Count from 1 to 30, one number per line, pausing about a second between numbers. Do not stop early. Then reply with exactly one word: OP-COUNT-DONE" 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] || bad="$bad the busy stimulus was refused (exit $rc): $(one_line "$stim");"
  sleep 6
  queued="$(pfm chat inject --allow-unsigned "$E1_MAIN" "reply with exactly one word: OP-QUEUED" 2>&1)"
  q_rc=$?
  if [ "$q_rc" -ne 0 ]; then
    bad="$bad the inject during the busy turn was refused (exit $q_rc): $(one_line "$queued");"
  elif ! printf '%s' "$queued" | grep -q '^queued'; then
    bad="$bad pfm's own inject report does not start with 'queued' — the turn had already ended or the guard read the pane wrong: $(one_line "$queued");"
  fi
  rl="$(pfm chat reload --sock "$sock" --then "reply with exactly one word: OP-RELOADED" 2>&1)"
  rl_rc=$?
  rl_log="$(printf '%s\n' "$rl" | sed -n 's/.*reload scheduled in place (log \(.*\)).*/\1/p' | head -1)"
  [ "$rl_rc" -eq 0 ] || bad="$bad pfm chat reload --sock $sock during the busy turn exited $rl_rc: $(one_line "$rl");"
  [ -n "$rl_log" ] || bad="$bad pfm chat reload did not report 'reload scheduled in place (log …)': $(one_line "$rl");"
  if ! wait_last "$E1_MAIN" OP-RELOADED 420; then
    bad="$bad no OP-RELOADED in 420s after the operator-side reload: ${LANE_WAIT_WHY:-no wait reason recorded}; last: $(one_line "$(pfm chat last "$E1_MAIN" 2>&1)");"
  fi
  pfm chat read "$E1_MAIN" 2>/dev/null | grep -qF OP-QUEUED ||
    bad="$bad the queued OP-QUEUED never reached the transcript (pfm chat read) — queued but not delivered;"
  if [ -n "$rl_log" ]; then
    if [ ! -f "$rl_log" ]; then
      bad="$bad the reload worker log $rl_log pfm named was never written;"
    else
      grep -qF 'holding /exit until it ends' "$rl_log" ||
        bad="$bad the worker log carries no hold line ('the chat's turn is still running — holding /exit until it ends') — the worker did not see the busy turn: $(one_line "$(tail -3 "$rl_log")");"
      grep -qE 'respawned in place|rebooted FRESH' "$rl_log" ||
        bad="$bad the worker log never reports the reboot ('respawned in place'): $(one_line "$(tail -3 "$rl_log")");"
    fi
  fi
  [ "$(live_field "$E1_MAIN" 11)" = "$sock" ] ||
    bad="$bad after the reboot $E1_MAIN sits on socket $(live_field "$E1_MAIN" 11), not the one reloaded ($sock) — not a reboot IN PLACE;"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "inject during the turn: '$(one_line "$queued" | cut -c1-80)' · reload scheduled (log $rl_log) held /exit until the turn ended, respawned in place on $sock, and its --then steer landed"
  fi
fi

# ─── O2.08 — a seat dropped under a LIVE chat (cross-lane) ──────────────────

beat O2.08-dropped-seat-with-live-chat I38
spends "cc:${SPARE:-none}"
target "$SPARE_CHAT"
if [ -z "$SPARE" ]; then
  blocked "seats $LANE_SEATS" "no spare seat to drop — dropping the only seat would take the lane's own chats with it (run with --seats cc:1,cc:2)"
else
  bad=""
  new_out="$(pfm chat new --name "$SPARE_CHAT" --engine cc --account "$SPARE" --cwd "$CWD" --await --timeout 300 \
    "You are $SPARE_CHAT, a chat an automated Tier B lane drives. Reply with one word: ready. Then wait and do exactly what each next message says." 2>&1)"
  new_rc=$?
  if [ "$new_rc" -ne 0 ] || ! live_chat "$SPARE_CHAT"; then
    fail "could not open a chat on the spare seat $SPARE (exit $new_rc): $(one_line "$new_out") — nothing lives on the seat to drop under"
  else
    acct_before="$(live_field "$SPARE_CHAT" 9)"
    cp "$CONFIG" "$CONFIG.lane-backup"
    jq --argjson drop "$SPARE" '.accounts |= map(select(.id != $drop))' "$CONFIG" >"$CONFIG.tmp" && mv "$CONFIG.tmp" "$CONFIG"
    drop_out="$(install_again)"
    drop_rc=$?
    [ "$drop_rc" -eq 0 ] || bad="$bad the install after the drop exited $drop_rc: $(one_line "$(printf '%s\n' "$drop_out" | tail -3)");"
    spare_hooks="$(hook_commands "$SPARE_DIR/settings.json" | grep -c 'pfm')"
    [ "$spare_hooks" -eq 0 ] || bad="$bad the dropped seat $SPARE still carries $spare_hooks pfm hook(s);"
    # The RUNTIME side: the chat on the dropped seat keeps working.
    live_chat "$SPARE_CHAT" || bad="$bad $SPARE_CHAT lost its live row when its seat was dropped;"
    st_out="$(pfm chat status "$SPARE_CHAT" 2>&1)"
    st_rc=$?
    [ "$st_rc" -eq 0 ] || bad="$bad pfm chat status $SPARE_CHAT exited $st_rc after the drop: $(one_line "$st_out");"
    pfm chat inject --allow-unsigned "$SPARE_CHAT" "reply with exactly one word: SPARE-ALIVE" >/dev/null 2>&1 ||
      bad="$bad inject into $SPARE_CHAT was refused after the drop;"
    wait_last "$SPARE_CHAT" SPARE-ALIVE 240 || bad="$bad $SPARE_CHAT did not answer after its seat was dropped: ${LANE_WAIT_WHY:-no wait reason};"
    acct_dropped="$(live_field "$SPARE_CHAT" 9)"
    # The LEDGER side: doctor's roster no longer names the seat, and doctor still runs.
    doc="$(pfm doctor 2>&1)"
    doc_rc=$?
    acct_row="$(printf '%s\n' "$doc" | grep -m1 '^doctor: config accounts=')"
    [ -n "$acct_row" ] || bad="$bad doctor printed no 'config accounts=' row;"
    printf '%s' "$acct_row" | grep -qE "accounts=([^ ]*,)?$SPARE:" && bad="$bad doctor's roster still names the dropped seat $SPARE: $acct_row;"
    printf '%s' "$acct_row" | grep -qE "accounts=([^ ]*,)?$SEAT:" || bad="$bad doctor's roster lost the KEPT seat $SEAT: $acct_row;"
    [ "$doc_rc" -le 1 ] || bad="$bad pfm doctor exited $doc_rc with a live chat on the dropped seat; first failure row: $(one_line "$(printf '%s\n' "$doc" | grep -m1 -E 'unhealthy|broken|error=' || true)");"
    # Restore: config back, install, the seat's hooks and the row's account return.
    mv "$CONFIG.lane-backup" "$CONFIG"
    restore_out="$(install_again)"
    restore_rc=$?
    [ "$restore_rc" -eq 0 ] || bad="$bad the restoring install exited $restore_rc: $(one_line "$(printf '%s\n' "$restore_out" | tail -3)");"
    spare_back="$(hook_commands "$SPARE_DIR/settings.json" | grep -c 'pfm')"
    [ "$spare_back" -gt 0 ] || bad="$bad seat $SPARE did not get its hooks back after the config was restored;"
    acct_after="$(live_field "$SPARE_CHAT" 9)"
    [ "$acct_after" = "$SPARE" ] || bad="$bad after the restore $SPARE_CHAT's row reports account '$acct_after' (was $acct_before, want $SPARE);"
    pfm chat end "$SPARE_CHAT" >/dev/null 2>&1 || bad="$bad pfm chat end $SPARE_CHAT failed — the spare chat is left running;"
    if [ -n "$bad" ]; then fail "$bad"; else
      pass "seat $SPARE dropped under $SPARE_CHAT: the chat answered status and a turn (row account $acct_before → '$acct_dropped' while dropped → $acct_after restored), doctor's roster dropped it and exited $doc_rc, the seat lost $spare_hooks pfm hooks and got $spare_back back"
    fi
  fi
fi

# ─── O2.09 — the harvester and its sidecar, for real on this architecture ───

beat O2.09-harvester H1 H2 H3 H4 H5 H6 H7 H8 H9 H12
spends "cc:$SEAT"
if gap_applies O2.09-harvester; then
  known O2.09-harvester
else
  bad=""
  hv_err=/tmp/o2-harvest.err
  url_txt="https://www.rfc-editor.org/rfc/rfc2324.txt"
  url_pdf="https://www.w3.org/WAI/ER/tests/xhtml/testfiles/resources/pdf/dummy.pdf"
  local_doc=/tmp/o2-harvest-local.md
  printf '# Lane O2 local harvest source\n\nThe needle is LOCAL-HARVEST-OK.\n' >"$local_doc"
  harvest_one() { # harvest_one <what> <source> <must-succeed 0|1> [content-needle] — one --json fetch, judged
    local what="$1" src="$2" must="$3" needle="${4:-}" out rc err kind status
    out="$(pfm harvest --json "$src" 2>"$hv_err")"
    rc=$?
    if ! printf '%s' "$out" | jq -e '.[0]' >/dev/null 2>&1; then
      bad="$bad $what ($src): --json printed no result object (exit $rc): $(one_line "$out") $(one_line "$(cat "$hv_err")");"
      return
    fi
    err="$(printf '%s' "$out" | jq -r '.[0].error // empty')"
    kind="$(printf '%s' "$out" | jq -r '.[0].error_kind // empty')"
    status="$(printf '%s' "$out" | jq -r '.[0].cache_status // empty')"
    if [ -n "$err" ]; then
      if [ "$must" -eq 1 ]; then
        bad="$bad $what ($src) failed (exit $rc, kind '${kind:-<none>}'): $err;"
      elif [ -z "$kind" ] || [ "$kind" = invalid ]; then
        bad="$bad $what ($src) was not classified as its identifier kind — error_kind '${kind:-<none>}': $err;"
      else
        noted="$noted $what: resolver reached, named error kind '$kind';"
      fi
      return
    fi
    [ "$rc" -eq 0 ] || bad="$bad $what ($src) reported no error but pfm harvest exited $rc;"
    [ -n "$status" ] || bad="$bad $what ($src) succeeded without a cache_status;"
    if [ -n "$needle" ] && ! printf '%s' "$out" | jq -r '.[0].content // ""' | grep -qF -- "$needle"; then
      bad="$bad $what ($src) content lacks '$needle' (kind $(printf '%s' "$out" | jq -r '.[0].kind // "?"'), $(printf '%s' "$out" | jq -r '.[0].chars // 0') chars);"
    fi
    noted="$noted $what: ok ($(printf '%s' "$out" | jq -r '.[0].kind // "?"'), $status);"
  }
  noted=""
  expect-log 'harvest'
  harvest_one "URL" "$url_txt" 1 "Hyper Text Coffee Pot Control Protocol"
  harvest_one "local path" "$local_doc" 1 "LOCAL-HARVEST-OK"
  # H9: a PDF is not HTML — its text only exists if the pinned Python sidecar converted it.
  harvest_one "PDF via sidecar" "$url_pdf" 1 "Dummy PDF file"
  harvest_one "DOI" "10.1371/journal.pmed.0020124" 1
  harvest_one "PMID" "pmid:16060722" 0
  harvest_one "PMCID" "PMC1182327" 0
  harvest_one "ISBN" "9780262033848" 0
  # --size-only and --refresh on the text URL: the cache answers, then is bypassed.
  size="$(pfm harvest --size-only "$url_txt" 2>&1)"
  rc=$?
  [ "$rc" -eq 0 ] || bad="$bad --size-only exited $rc: $(one_line "$size");"
  printf '%s' "$size" | grep -qE 'size: [0-9]+ tokens / chars: [0-9]+ / path: .+ / cache_status: (hit|miss|refresh|public)' ||
    bad="$bad --size-only did not print the size/path/cache_status line: $(one_line "$size");"
  printf '%s' "$size" | grep -qF 'cache_status: hit' || bad="$bad a second fetch of $url_txt was not a cache hit: $(one_line "$size");"
  refresh="$(pfm harvest --json --refresh "$url_txt" 2>/dev/null | jq -r '.[0].cache_status // empty')"
  [ "$refresh" = refresh ] || bad="$bad --refresh reported cache_status '${refresh:-<none>}' (want refresh);"
  pfm harvest >/dev/null 2>&1
  rc=$?
  [ "$rc" -eq 2 ] || bad="$bad pfm harvest with no source exited $rc (want 2, usage);"
  # H2: ask — a real model turn over the harvested document; the evidence is
  # pfm's exit code and its own usage line, never the answer's words.
  ask_out="$(pfm harvest ask --engine claude -p "Answer in one word: which beverage does this RFC's protocol control?" "$url_txt" 2>"$hv_err")"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    bad="$bad pfm harvest ask exited $rc: $(one_line "$(cat "$hv_err")");"
  else
    [ -n "$ask_out" ] || bad="$bad pfm harvest ask exited 0 but printed no answer;"
    grep -qE 'pfm harvest ask: usage input=[0-9]+ cached_input=[0-9]+ output=[0-9]+' "$hv_err" ||
      bad="$bad pfm harvest ask printed no usage line on stderr: $(one_line "$(cat "$hv_err")");"
  fi
  pfm harvest ask "$url_txt" >/dev/null 2>&1
  rc=$?
  [ "$rc" -eq 2 ] || bad="$bad pfm harvest ask without -p exited $rc (want 2, usage);"
  # H9/H12: the sidecar's provisioning as doctor reports it — landed, not skipped.
  doc="$(pfm doctor 2>&1)"
  hp="$(printf '%s\n' "$doc" | grep '^doctor: harvestpy')"
  [ -n "$hp" ] || bad="$bad pfm doctor printed no harvestpy row — the sidecar's provisioning is unreported;"
  printf '%s' "$hp" | grep -qF 'harvestpy skipped' && bad="$bad doctor reports the harvestpy runtime SKIPPED — setup fell back to --skip-harvest;"
  printf '%s' "$hp" | grep -qE 'harvestpy interpreter=\(file\) .* version=' || bad="$bad doctor names no provisioned harvestpy interpreter: $(one_line "$hp");"
  printf '%s' "$hp" | grep -qF 'live_smoke=(file) healthy' || bad="$bad doctor's harvestpy live smoke is not healthy: $(one_line "$(printf '%s\n' "$hp" | grep -E 'live_smoke|broken' | head -2)");"
  printf '%s' "$hp" | grep -qE 'harvestpy .*(broken|unavailable|blocked)' && bad="$bad a broken harvestpy row: $(one_line "$(printf '%s\n' "$hp" | grep -E 'broken|unavailable|blocked' | head -1)");"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "$(uname -s | tr 'A-Z' 'a-z')-$(uname -m):$noted size-only hit, --refresh refresh; ask answered with a usage line; doctor: $(one_line "$(printf '%s\n' "$hp" | grep -E 'interpreter|live_smoke' | tr '\n' ' ')" | cut -c1-160)"
  fi
fi

# ─── O2.10 — uninstall: the end of the machine ──────────────────────────────

beat O2.10-uninstall I69 I70 I71 I72 I73 I74 I75 I76 I77 I78 I79 I80 I81 I82 I83 I84 I85 I86 I87 I88 I89 I97
spends none
bad=""
FOREIGN_HOOK="echo lane-foreign-hook"
own_cmd="$SEAT_DIR/commands/lane-own.md"
# Plant BEFORE the last install: a hook pfm did not write, and an operator's own
# command file — then re-install so the ownership ledger is written with both present.
jq --arg c "$FOREIGN_HOOK" '.hooks = (.hooks // {}) | .hooks.Stop = ((.hooks.Stop // []) + [{"hooks": [{"type": "command", "command": $c}]}])' \
  "$SEAT_DIR/settings.json" >"$SEAT_DIR/settings.json.tmp" && mv "$SEAT_DIR/settings.json.tmp" "$SEAT_DIR/settings.json" ||
  bad="$bad could not plant the foreign hook into $SEAT_DIR/settings.json;"
printf -- '---\ndescription: an operator-owned command the lane planted\n---\nSay hello.\n' >"$own_cmd"
pre="$(install_again)"
pre_rc=$?
[ "$pre_rc" -eq 0 ] || bad="$bad the install before uninstall exited $pre_rc: $(one_line "$(printf '%s\n' "$pre" | tail -3)");"
hook_commands "$SEAT_DIR/settings.json" | grep -qxF "$FOREIGN_HOOK" || bad="$bad the foreign hook did not survive the install that preceded uninstall;"
pfm_hooks_before="$(hook_commands "$SEAT_DIR/settings.json" | grep -c 'pfm')"
[ "$pfm_hooks_before" -gt 0 ] || bad="$bad seat $SEAT carried no pfm hook before uninstall — there was nothing to remove;"
express_before=""
for p in "$EXPRESS/CLAUDE.md" "$EXPRESS/.claude" "$EXPRESS/.professor"; do [ -e "$p" ] && express_before="$express_before $p"; done
n_backups_before="$(find "$SEAT_DIR" -maxdepth 1 -name 'settings.json.pre-professor-*' 2>/dev/null | wc -l | tr -d ' ')"
seat_dirs="$(jq -r '.accounts[].configDir' "$CONFIG" | sed "s|^~|$HOME|")"

un="$( (cd "$BLUEPRINT" && pfm uninstall 2>&1) )"
un_rc=$?
un_summary="$(printf '%s\n' "$un" | grep -E '^summary changed=' | tail -1)"
[ "$un_rc" -eq 0 ] || bad="$bad pfm uninstall exited $un_rc: $(one_line "$(printf '%s\n' "$un" | grep -iE 'error|refuse|failed' | head -2)") $(one_line "$(printf '%s\n' "$un" | tail -2)");"
printf '%s' "$un" | grep -qF 'MODE: uninstall' || bad="$bad pfm uninstall did not announce MODE: uninstall;"
[ -n "$un_summary" ] || bad="$bad pfm uninstall printed no 'summary changed=' line;"
printf '%s' "$un_summary" | grep -qE 'changed=[1-9]' || bad="$bad pfm uninstall reports it changed nothing: ${un_summary:-<no summary>};"
# I82/I83/I87: the managed root — every staged file gone, the ledger gone, nothing forced.
for f in settings-hook-ownership.json source-repo mcp-auth-token mcp-ownership.json; do
  [ ! -e "$MANAGED/$f" ] || bad="$bad $MANAGED/$f is still on disk (the ledger/metadata must be gone);"
done
if [ -e "$MANAGED" ]; then
  left="$(find "$MANAGED" -type f -o -type l 2>/dev/null | head -5 | tr '\n' ' ')"
  [ -z "$left" ] || bad="$bad the managed root still holds: $left;"
fi
# I69: the harvestpy runtime and cache.
for d in env cache; do
  [ ! -e "$HOME/.local/state/pfm/harvest-python/$d" ] || bad="$bad harvestpy $d still present under ~/.local/state/pfm/harvest-python;"
done
# I71/I72: the launcher and the host overlays.
for o in pfm-statusline tmux-title-renudge; do
  [ ! -e "$HOME/.local/bin/$o" ] && [ ! -L "$HOME/.local/bin/$o" ] || bad="$bad host overlay ~/.local/bin/$o still present;"
done
if [ -L "$HOME/.local/bin/claude" ]; then
  case "$(readlink "$HOME/.local/bin/claude")" in
    *"/.local/share/pfm/"*) bad="$bad ~/.local/bin/claude still links into pfm's managed root: $(readlink "$HOME/.local/bin/claude");" ;;
  esac
fi
# Per seat: I70 themes, I73 /reload, I74 handoff, I76 remnants, I78 hooks, I84 foreign kept, I85 own file kept, I88 backup.
while IFS= read -r dir; do
  [ -n "$dir" ] || continue
  [ ! -e "$dir/commands/reload.md" ] || bad="$bad $dir/commands/reload.md still present;"
  [ ! -e "$dir/skills/handoff" ] && [ ! -L "$dir/skills/handoff" ] || bad="$bad $dir/skills/handoff still present;"
  [ ! -e "$dir/commands/bb.md" ] || bad="$bad $dir/commands/bb.md remnant still present;"
  [ -z "$(find "$dir/commands/chat" -maxdepth 1 -name '*.md' 2>/dev/null | head -1)" ] || bad="$bad /chat:* command remnants still under $dir/commands/chat;"
  [ -z "$(find "$dir" -maxdepth 2 -name 'professor-*.json' 2>/dev/null | head -1)" ] || bad="$bad a staged professor-*.json theme still under $dir;"
  if [ -f "$dir/settings.json" ]; then
    n_pfm="$(hook_commands "$dir/settings.json" | grep -c 'pfm')"
    [ "$n_pfm" -eq 0 ] || bad="$bad $dir/settings.json still carries $n_pfm pfm hook(s): $(one_line "$(hook_commands "$dir/settings.json" | grep pfm | head -1)");"
    jq -e '.mcpServers.chat // .mcpServers.harvester' "$dir/.claude.json" >/dev/null 2>&1 &&
      bad="$bad $dir/.claude.json still registers pfm's MCP server(s): $(jq -c '.mcpServers | keys' "$dir/.claude.json");"
  fi
done <<EOF
$seat_dirs
EOF
hook_commands "$SEAT_DIR/settings.json" | grep -qxF "$FOREIGN_HOOK" || bad="$bad the FOREIGN hook '$FOREIGN_HOOK' was stripped from $SEAT_DIR/settings.json;"
[ -f "$own_cmd" ] || bad="$bad the operator's own command file $own_cmd was removed;"
n_backups_after="$(find "$SEAT_DIR" -maxdepth 1 -name 'settings.json.pre-professor-*' 2>/dev/null | wc -l | tr -d ' ')"
[ "$n_backups_after" -gt "$n_backups_before" ] || bad="$bad no new settings.json.pre-professor-<stamp> backup beside the rewritten $SEAT_DIR/settings.json ($n_backups_before before, $n_backups_after after);"
# I75/I78/I79: the Codex side — command mirror, hooks.json, config.toml.
if [ -f "$CODEX_HOME/config.toml" ]; then
  grep -qE 'BEGIN pfm developer_instructions|mcp_servers\.chat|mcp_servers\.harvester' "$CODEX_HOME/config.toml" &&
    bad="$bad $CODEX_HOME/config.toml still carries pfm's prompt/MCP wiring: $(one_line "$(grep -E 'BEGIN pfm developer_instructions|mcp_servers' "$CODEX_HOME/config.toml" | head -1)");"
fi
if [ -f "$CODEX_HOME/hooks.json" ]; then
  grep -qF 'pfm internal' "$CODEX_HOME/hooks.json" && bad="$bad $CODEX_HOME/hooks.json still carries a pfm hook entry;"
fi
wired="$(find "$CODEX_HOME/prompts" -maxdepth 1 -type l 2>/dev/null | while IFS= read -r l; do
  case "$(readlink "$l")" in *"/.local/share/pfm/"* | /worktree/*) printf '%s ' "$l" ;; esac
done)"
[ -z "$wired" ] || bad="$bad Codex command mirror links still point at pfm: $wired;"
# I80/I77/I81: the shell line, the units, the VS Code link.
grep -q 'pfm.zsh' "$HOME/.zshrc" 2>/dev/null && bad="$bad ~/.zshrc still sources pfm.zsh;"
units="$(find "$HOME/.config/systemd/user" -name 'pfm*' 2>/dev/null | tr '\n' ' ')"
[ -z "$units" ] || bad="$bad systemd unit file(s) still present: $units;"
vsix="$(find "$HOME/.vscode" "$HOME/.vscode-server" "$HOME/.vscode-oss" -maxdepth 3 -name 'pfm*' 2>/dev/null | head -3 | tr '\n' ' ')"
[ -z "$vsix" ] || bad="$bad VS Code extension link(s) still present: $vsix;"
# I86: the adopter's project scaffold is not pfm's to remove.
for p in $express_before; do [ -e "$p" ] || bad="$bad uninstall removed the project scaffold $p;"; done
# I89 is a documented UNKNOWN (no pruning routine): the retention is REPORTED, not judged.
if [ -n "$bad" ]; then fail "$bad"; else
  pass "exit $un_rc · $un_summary · managed root, ledger, overlays, /reload + handoff links, themes, harvestpy env+cache, MCP registrations, Codex wiring, pfm.zsh line all gone from $(printf '%s\n' "$seat_dirs" | grep -c .) seat(s); the foreign hook and $own_cmd kept; ${express_before:-no express scaffold present} untouched; backups $n_backups_before → $n_backups_after (I89: no pruning routine exists, count reported)"
fi

lane_end

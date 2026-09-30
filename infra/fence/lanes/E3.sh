#!/usr/bin/env bash
# E3.sh — lane E3, OpenCode: one chat on the fleet's one OpenCode home, walked
# depth-first through the spawn ceremony, the MCP wiring, then outside-in verbs
# in E1/F/O. Runs INSIDE a lane container (run.sh), never on a host.
#
#   run.sh --lanes E3            solo, from a fresh root
#   run.sh                       in the sequence, after E2
#
# Every beat asserts from pfm's OWN report (`pfm ls --tsv`, a verb's exit code)
# or from the pane. Beat ids are the contract in
# beats.md and map.tsv — check-map.sh fails when this file and those disagree.
#
# The picker opens the OpenCode TUI in a fleet-owned ox- socket; once its first
# prompt writes a session, pfm reports a live-opencode row with that socket
# (compose/types.go). OpenCode content reads still refuse by name; E3.03 pins
# each verb's current result against the running row.
#
# Unlike Claude and Codex, OpenCode carries no seat roster in pfm.config.json —
# it is the fleet's ONE implicit account, recognized only once its session
# store (opencode.db) exists on disk (pfm/internal/config/config.go), which
# also gates whether the picker offers "New OpenCode chat" at all
# (compose/compose.go: `includeNewOpenCode: … len(OpenCodeAccountIDs) != 0`). A
# root has no session store before the probe; this lane's prelude makes it with
# one mock `opencode run` outside pfm.
#
# Cost: one OpenCode home (no seat accounting beyond `oc`) plus one mock
# `opencode run` in the prelude when the store does not exist yet, plus one
# throwaway `pfm` TUI driven headless in its own tmux server (never pfm's own
# tmux dir, so the fleet scan never mistakes it for a chat) to reach the
# picker's merged new-chat row.
#
# BROKEN STATE: the prelude aborts the lane by name when the OpenCode home
# cannot be made (no `opencode` binary, or its first run fails) or the chat
# cannot be opened through the picker (the OpenCode row never converges, or no
# fresh ox- socket/live-opencode row appears after Enter); a
# beat whose precondition beat failed reports `blocked-by`, and each ✗ carries
# the raw pane bytes in the lane log beside its assertion.
set -uo pipefail
LANES_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=lib.sh
. "$LANES_DIR/lib.sh"
lane_preamble

WANT_NAME="${E3_CHAT:-E3_MAIN}"
CWD="${E3_CWD:-/work/lumen}"
CONFIG="${PFM_CONFIG:?PFM_CONFIG is required in the container}"
# The OpenCode session store: pfm's own root-resolution rule
# (pfm/internal/engine/builtin.go DefaultRoots, overridable by PFM_OPENCODE_ROOT
# per pfm/internal/paths/paths.go), never guessed.
OC_HOME="${PFM_OPENCODE_ROOT:-$HOME/.local/share/opencode}"
OC_DB="$OC_HOME/opencode.db"
PORT="$(jq -r '.mcp.http.port // 18377' "$CONFIG" 2>/dev/null || echo 18377)"
TUI_SOCK="${TMPDIR:-/tmp}/e3-lane-tui.sock"
PFM_BIN="$HOME/.local/bin/pfm"

lane_begin E3

# ── prelude: what this lane needs, made when it is missing, no-op otherwise ──
[ -f "$CONFIG" ] || lane_abort "no pfm config at $CONFIG — the root image was not built by lanes/root.sh"

need "the working directory $CWD" "[ -d '$CWD/.git' ]" \
  "mkdir -p '$CWD' && git -C '$CWD' init -q && git -C '$CWD' commit -q --allow-empty -m lane" ||
  lane_abort "no working directory for the chat to live in ($CWD)"
need "the pfm MCP daemon on :$PORT" \
  "[ \"\$(curl -s -o /dev/null -w '%{http_code}' -m 2 http://127.0.0.1:$PORT/mcp/professor)\" != 000 ]" \
  'lane_daemon_up' ||
  lane_abort "the professor MCP daemon never answered on :$PORT — no chat can call a chat_* tool"

# make_oc_home — the mock OpenCode CLI creates opencode.db. Its resume rows
# are found by difference and killed before E3.01 opens the lane's own chat.
make_oc_home() {
  command -v opencode >/dev/null 2>&1 || { echo "no opencode binary on PATH"; return 1; }
  local before after out rc
  before="$(pfm ls --tsv 2>/dev/null | awk -F'\t' '$1 == "resume-opencode" { print $2 }' | sort)"
  out="$(cd "$CWD" && timeout 180 opencode run 'lane probe' 2>&1)"
  rc=$?
  printf '%s\n' "$out"
  [ "$rc" -eq 0 ] || return "$rc"
  [ -f "$OC_DB" ] || { echo "opencode run exited 0 but wrote no session store at $OC_DB"; return 1; }
  after="$(pfm ls --tsv 2>/dev/null | awk -F'\t' '$1 == "resume-opencode" { print $2 }' | sort)"
  comm -13 <(printf '%s\n' "$before") <(printf '%s\n' "$after") | xargs -r -n1 pfm chat kill >/dev/null 2>&1
  [ -f "$OC_DB" ]
}
need "the OpenCode home at $OC_DB" "[ -f '$OC_DB' ]" 'make_oc_home' ||
  lane_abort "no OpenCode home — opencode.db never appeared at $OC_DB, so the picker's compose.Kind NewOpenCode row never renders (compose.go includeNewOpenCode requires len(OpenCodeAccountIDs) != 0) — no beat in this lane can reach a 'New OpenCode chat' row to press Enter on"

# ─── helpers: the picker, driven headless in its own tmux server ───────────
# tui_close/tui_pane/tui_keys/tui_type/tui_has/tui_wait/tui_open/tui_selected
# live once in lib.sh (F.sh's own subset, byte-identical here before this) —
# only TUI_SOCK and CWD are this lane's own.

# oc_tmux_dir / ox_sockets — the default tmux socket directory pfm's `-L`
# attach resolves against (spawn.FreshSocket returns a BARE name like
# "ox-<unix>-<pid>-<rand>"; action/synth.go's attachLine uses `tmux -L
# <socket>`, which is `-L`, not `-S` — resolved inside this same default dir),
# and every ox- prefixed socket currently sitting in it.
oc_tmux_dir() {
  local d
  for d in "${PFM_TMUX_DIR:-}" "${TMUX_TMPDIR:-}" "/tmp/tmux-$(id -u)"; do
    [ -n "$d" ] && [ -d "$d" ] && { printf '%s' "$d"; return 0; }
  done
  return 1
}
ox_sockets() {
  local dir
  dir="$(oc_tmux_dir)" || return 0
  find "$dir" -maxdepth 1 -name 'ox-*' -exec basename {} \; 2>/dev/null | sort
}
oc_resume_ids() { pfm ls --tsv 2>/dev/null | awk -F'\t' '$1 == "resume-opencode" { print $2 }' | sort; }

# ─── E3.01 — the spawn ceremony, then title + a real session converge ───────

# open_main — select OpenCode on the picker's New row. The selected row's
# brackets move with Right; Down cannot move through a fuzzy-filtered lone row.
# A prompt then births a live-opencode row on the new ox- socket.
OC_ID="" OC_SOCK="" OC_NAME=""
open_main() {
  local before_ox before_ids after_ox label i sockpath row new_resume
  OC_ID="" OC_SOCK="" OC_NAME=""
  before_ox="$(ox_sockets)"
  before_ids="$(oc_resume_ids)"
  if ! tui_open 100 30 ls; then
    echo "picker never opened: $TUI_WHY"
    return 1
  fi
  tui_type New
  label="$(tui_selected)"
  i=0
  while [ "$i" -lt 3 ] && [[ "$label" != *'[ OpenCode ]'* && "$label" != 'New OpenCode chat' ]]; do
    tui_keys Right
    label="$(tui_selected)"
    i=$((i + 1))
  done
  if [[ "$label" != *'[ OpenCode ]'* && "$label" != 'New OpenCode chat' ]]; then
    echo "the fuzzy-filtered 'New' row never selected OpenCode after 3 Right keys; last read: '$label'; pane: $(one_line "$(tui_pane)")"
    tui_close
    return 1
  fi
  tui_keys Enter
  sleep 2
  tui_close # the outer driver's job is done; the ox- server is independent (action/executor.go CreateChatServer runs before the exec'd attach)
  after_ox="$(ox_sockets)"
  OC_SOCK="$(comm -13 <(printf '%s\n' "$before_ox") <(printf '%s\n' "$after_ox") | head -1)"
  if [ -z "$OC_SOCK" ]; then
    echo "Enter on the OpenCode New row never produced a fresh ox- socket under $(oc_tmux_dir 2>/dev/null || echo '<no tmux dir found>'); sockets now: $(one_line "$after_ox")"
    return 1
  fi
  sockpath="$(oc_tmux_dir)/$OC_SOCK"
  # A raw prompt typed directly into the pane writes OpenCode's session row.
  tmux -S "$sockpath" send-keys -l "reply with one word: ready" 2>/dev/null
  tmux -S "$sockpath" send-keys Enter 2>/dev/null
  i=0
  while [ "$i" -lt 30 ]; do
    row="$(pfm ls --tsv 2>/dev/null | awk -F'\t' -v s="$OC_SOCK" 'NR > 1 && $1 == "live-opencode" && $11 == s { print; exit }')"
    OC_ID="$(awk -F'\t' '{ print $2 }' <<<"$row")"
    OC_NAME="$(awk -F'\t' '{ print $5 }' <<<"$row")"
    [ -n "$OC_ID" ] && break
    sleep 2
    i=$((i + 1))
  done
  if [ -z "$OC_ID" ]; then
    echo "socket $OC_SOCK is live but pfm ls reported no live-opencode row on it in 60s: $(one_line "$(pfm ls --tsv 2>&1)")"
    return 1
  fi
  new_resume="$(comm -13 <(printf '%s\n' "$before_ids") <(printf '%s\n' "$(oc_resume_ids)"))"
  if [ -n "$new_resume" ]; then
    echo "the live socket also created a new resume-opencode row: $(one_line "$new_resume")"
    return 1
  fi
  return 0
}
lane_reopen 'open_main'

beat E3.01-open-seat
spends oc
target "$WANT_NAME"
open_log="$LANE_OUT_DIR/E3.open.log"
if open_main >"$open_log" 2>&1; then
  bad=""
  sockpath="$(oc_tmux_dir)/$OC_SOCK"
  [ "$(pfm ls --tsv 2>/dev/null | awk -F'\t' -v i="$OC_ID" '$2 == i { print $1; exit }')" = live-opencode ] ||
    bad="$bad session $OC_ID is not a live-opencode row;"
  [ "$OC_NAME" = 'reply with one word: ready' ] ||
    bad="$bad the live row's name '$OC_NAME' is not the prompt text;"
  # onChatServer titles the fresh window literally OpenCode.
  if window="$(tmux -S "$sockpath" list-windows -F '#{window_name}' 2>&1)"; then
    case "$window" in
      OpenCode) ;;
      *) bad="$bad tmux window name '$(one_line "$window")' is not 'OpenCode' (action/synth.go onChatServer titles it pfmengine.OpenCode.Short);" ;;
    esac
  else
    bad="$bad tmux list-windows FAILED on the fresh socket $OC_SOCK ($(one_line "$window")) — the window could not be read at all;"
  fi
  # T31, needs:none — the CLI surface renders, fail-open, regardless of which
  # engine branch it resolves through (OpenCode exports no session/home env
  # var pfm's statusline can key off — pfm/internal/statusline/runtime.go
  # EngineFromEnvironment — so from outside the pane it renders the same
  # fail-open path any unidentified caller gets; that path IS what T31 names).
  render="$(printf '{"session_id":"%s","model":{"display_name":"opencode"},"workspace":{"current_dir":"%s"}}' \
    "$OC_ID" "$CWD" | pfm statusline 2>&1)"
  render_rc=$?
  [ "$render_rc" -eq 0 ] || bad="$bad pfm statusline exited $render_rc ($(one_line "$render"));"
  [ -n "$render" ] || bad="$bad pfm statusline rendered nothing;"
  if [ -n "$bad" ]; then
    fail "$bad"
  else
    pass "opened via the picker's OpenCode New row: live-opencode session $OC_ID on socket $OC_SOCK · window '$window' · statusline $(one_line "$render" | cut -c1-120)"
  fi
else
  fail "the picker could not open a new OpenCode chat: $(one_line "$(cat "$open_log")")"
fi
rm -f "$open_log"

# ─── E3.02 — OpenCode MCP wiring: chat local + harvester remote, doctor row ─
# The file pfm's OWN installer registers MCP into: pfm/internal/installer/
# mcp.go writeMCPOpenCodeJSON, by the same fence discipline as Claude's
# .claude.json (mcp_accounts.go) and Codex's config.toml (mcp.go wireMCP) —
# root.sh's own `pfm install --yes` (setup.sh install) wrote this file before
# this lane ran. The shared body (identical to M.03) lives once in lib.sh's
# assert_opencode_mcp_registered — this lane and M must never drift apart on
# what "MCP registered" means.

beat E3.02-mcp-registered
spends none
assert_opencode_mcp_registered "$PFM_BIN" "$PORT"

# ─── E3.03 — outside-in CLI results for the live OpenCode row ───────────────

beat E3.03-everything-else
spends oc
target "$WANT_NAME"
if requires E3.01-open-seat; then
  bad=""

  # The promptless OpenCode pane settles idle after its scripted first turn.
  st="$(pfm chat status "$OC_ID" 2>&1)"
  st_rc=$?
  [ "$st_rc" -eq 0 ] || bad="$bad status exited $st_rc for a live-opencode row: $(one_line "$st");"
  [ "$(printf '%s' "$st" | awk -F'\t' 'NR == 1 { print $2 }')" = idle ] || bad="$bad status did not report state=idle: $(one_line "$st");"

  # OpenCode's session content is deliberately unsupported by pfm readers.
  last="$(pfm chat last "$OC_ID" 2>&1)"
  last_rc=$?
  if [ "$last_rc" -ne 1 ] || ! grep -qF 'reading OpenCode session content is not supported' <<<"$last"; then
    bad="$bad last exited $last_rc without naming the OpenCode content refusal: $(one_line "$last");"
  fi
  read_out="$(pfm chat read "$OC_ID" --tail 2 --condensed 2>&1)"
  read_rc=$?
  if [ "$read_rc" -ne 1 ] || ! grep -qF 'reading OpenCode session content is not supported' <<<"$read_out"; then
    bad="$bad read exited $read_rc without naming the OpenCode content refusal: $(one_line "$read_out");"
  fi

  # capture reads the live ox- pane.
  cap="$(pfm chat capture "$OC_ID" 2>&1)"
  cap_rc=$?
  if [ "$cap_rc" -ne 0 ] || [ -z "$cap" ]; then
    bad="$bad capture exited $cap_rc without a live pane: $(one_line "$cap");"
  fi

  # inject addresses the live row by its session id.
  inj="$(pfm chat inject --allow-unsigned "$OC_ID" "reply with exactly one word: OC-INJECT-OK" 2>&1)"
  inj_rc=$?
  if [ "$inj_rc" -ne 0 ] || [ -z "$inj" ]; then
    bad="$bad inject exited $inj_rc without a delivery report: $(one_line "$inj");"
  fi

  # name is gated on chat.Live and now changes the live row.
  name_out="$(pfm chat name "$OC_ID" "$WANT_NAME" 2>&1)"
  name_rc=$?
  if [ "$name_rc" -ne 0 ] || ! grep -qF "named $OC_ID -> $WANT_NAME" <<<"$name_out"; then
    bad="$bad name exited $name_rc without renaming the live row: $(one_line "$name_out");"
  fi

  # kill/unkill work by id. The
  # killed column is read from the ALL view (`-a`): the default view omits
  # killed rows by design (same reason F.sh's all_field exists).
  all_killed_field() { pfm ls -a --tsv 2>/dev/null | awk -F'\t' -v n="$1" 'NR > 1 && $2 == n { print $10; exit }'; }
  kill_out="$(pfm chat kill "$OC_ID" 2>&1)"
  kill_rc=$?
  sleep 2
  [ "$kill_rc" -eq 0 ] || bad="$bad kill exited $kill_rc: $(one_line "$kill_out");"
  [ "$(all_killed_field "$OC_ID")" = true ] || bad="$bad the row's killed column is '$(all_killed_field "$OC_ID")' after kill (want true);"
  unkill_out="$(pfm chat unkill "$OC_ID" 2>&1)"
  unkill_rc=$?
  sleep 2
  [ "$unkill_rc" -eq 0 ] || bad="$bad unkill exited $unkill_rc: $(one_line "$unkill_out");"
  [ "$(all_killed_field "$OC_ID")" = false ] || bad="$bad the row's killed column is '$(all_killed_field "$OC_ID")' after unkill (want false);"

  if [ -n "$bad" ]; then
    fail "$bad"
  else
    pass "status(idle)/last+read(OpenCode content unsupported)/capture(live pane)/inject(delivery report)/name(live row)/kill+unkill(killed column) all asserted for live-opencode $OC_ID"
  fi
fi

lane_end

#!/usr/bin/env bash
# E3.sh — lane E3, OpenCode: one chat on the fleet's one OpenCode home, walked
# depth-first through the spawn ceremony, the confirmed-absent MCP wiring (a
# known gap), then every outside-in verb the shared CLI surface already proves
# in E1/F/O. Runs INSIDE a lane container (run.sh), never on a host.
#
#   run.sh --lanes E3            solo, from a fresh root
#   run.sh                       in the sequence, after E2
#
# Every beat asserts from pfm's OWN report (`pfm ls --tsv`, a verb's exit code,
# the chat's last assistant message) or from the pane, never from a model's
# prose: a beat that can only be satisfied by what the model said is a beat
# asserting the wrong thing. Beat ids and their landscape ids are the contract in
# beats.md and map.tsv — check-map.sh fails when this file and those disagree.
#
# Three facts this lane is built on, each read from the Go source, none
# assumed:
#   1. `pfm chat new` has NO OpenCode door. `action.RegisterPlanner` (the
#      headless-chat door `chat new` walks) is wired for Claude and Codex only
#      (pfm/cmd/pfm/engines.go); an unregistered engine's `PlannerFor` returns
#      "OpenCode does not support headless chat" (pfm/internal/action/planner.go)
#      — this holds for BOTH `--engine oc` and `--engine opencode` (the
#      accepted spellings, pfm/internal/engine/engine.go Parse/accepted). The
#      only live door is the picker's merged "New OpenCode chat" row
#      (infra/demo/storm.sh names it) — compose.Kind NewOpenCode, "launches a
#      fresh OpenCode TUI in a fleet-owned ox socket" (compose/types.go).
#   2. `pfm ls` has NO live-OpenCode row kind at all. compose.Kind's whole enum
#      (compose/types.go) is LiveClaude/LiveCodex/LiveSplit/Agent/Resume{Claude,
#      Codex,OpenCode}/New{Claude,Codex,OpenCode}/Booting/ProfessorUpdate —
#      OpenCode gets ONLY Resume/New. `openCodeSessionRow` (compose/compose.go)
#      never sets Socket, and Kind.IsAddressable() (compose/types.go) is true
#      only for LiveClaude/LiveCodex/LiveSplit/Agent/Booting — never
#      ResumeOpenCode. So `chat.Live` (pfm/internal/chat/target.go: `Live:
#      row.Kind.IsAddressable()`) is ALWAYS false for an OpenCode chat, live TUI
#      or not: this lane can never assert a "live-opencode" row, so it doesn't.
#      What IS real and deterministic: `onChatServer` (action/synth.go) titles
#      the fresh tmux WINDOW `pfmengine.MustLookup(OpenCode).Short` == literally
#      "OpenCode" — that convergence this lane asserts instead.
#   3. Every chat verb gated on `chat.Live` therefore ALWAYS refuses an
#      OpenCode target by name: `pfm chat name` ("... is not running", exit 3 —
#      chat_command.go runChatNameWith), `pfm chat capture` (same message, same
#      exit), and `pfm chat inject` (its liveSeats-only NameResolver excludes
#      any Socket-less row — chat/names.go liveSeats — so it falls through to
#      the plain "no chat named" refusal, exit 4 — chat_dispatch.go
#      writeInjectResult). `pfm chat status` still answers (a dead chat is a
#      status, not an error — chat/status.go), reporting state=dead, never
#      idle/working, because headless.Inspect only ever upgrades State away
#      from StateDead when chat.Live (headless/headless.go). `pfm chat
#      last`/`read` refuse "has not written a transcript yet" (exit 3) because
#      Row.Path is never set for an OpenCode row either. `pfm chat kill`/
#      `unkill` are the one pair that do NOT require Live — `chat.Resolve`
#      (chat/target.go) scans compose.AllView, so an id-addressed kill/unkill
#      tombstones a resume-opencode row exactly as it would a dead Claude one.
#      E3.03 asserts every one of these REAL, sourced outcomes — a refusal
#      named by pfm IS the assertion, never a fabricated pass.
#
# Unlike Claude and Codex, OpenCode carries no seat roster in pfm.config.json —
# it is the fleet's ONE implicit account, recognized only once its session
# store (opencode.db) exists on disk (pfm/internal/config/config.go), which
# also gates whether the picker offers "New OpenCode chat" at all
# (compose/compose.go: `includeNewOpenCode: … len(OpenCodeAccountIDs) != 0`). A
# --no-adopt root (lanes/root.sh) spends no model turn, so that store is never
# created there — this lane's own prelude makes it, the same way
# infra/demo/setup.sh's install phase does when DEMO_OPENCODE_PROBE=1: a bare
# `opencode run` outside pfm entirely, once.
#
# Cost: one OpenCode home (no seat accounting beyond `oc`) plus one bare
# `opencode run` in the prelude when the store does not exist yet, plus one
# throwaway `pfm` TUI driven headless in its own tmux server (never pfm's own
# tmux dir, so the fleet scan never mistakes it for a chat) to reach the
# picker's merged new-chat row. Roughly 6 short turns (the prelude's probe, one
# raw stimulus typed into the freshly opened OpenCode pane).
#
# BROKEN STATE: the prelude aborts the lane by name when the OpenCode home
# cannot be made (no `opencode` binary, or its first run never answers) or the
# chat cannot be opened through the picker (the "New OpenCode chat" row never
# converges, or no fresh ox- socket/resume-opencode row appears after Enter); a
# beat whose precondition beat failed reports `blocked-by`, and each ✗ carries
# the raw pane bytes in the lane log beside its assertion.
set -uo pipefail
export PATH="$HOME/.local/bin:$PATH"
export IS_SANDBOX=1 # root fence: Claude Code refuses the bypass flag under root without it
cd /tmp 2>/dev/null || true

LANES_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=lib.sh
. "$LANES_DIR/lib.sh"

WANT_NAME="${E3_CHAT:-E3_MAIN}" # the label E3.03 attempts (and asserts refused) — see fact 3
CWD="${E3_CWD:-/work/lumen}"
CONFIG="$HOME/.config/pfm/pfm.config.json"
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
  "[ \"\$(curl -s -o /dev/null -w '%{http_code}' -m 2 http://127.0.0.1:$PORT/mcp/chat)\" != 000 ]" \
  "bash /worktree/infra/demo/daemon.sh" ||
  lane_abort "the chat MCP daemon never answered on :$PORT — no chat can call a chat_* tool"

# make_oc_home — the ONE bare, non-pfm OpenCode turn that creates opencode.db,
# mirroring infra/demo/setup.sh's own DEMO_OPENCODE_PROBE=1 path exactly (never
# a second implementation): `opencode run` answers "ready" for real, which
# proves its ChatGPT auth is live too. OpenCode invents its own row name for
# that run ("Ready Request" one day, "Ready instruction request" the next), so
# the row this probe leaves behind is found by DIFFERENCE and killed (kill is
# Live-independent — fact 3), never by name — E3.01 opens the lane's own chat
# fresh, after this.
make_oc_home() {
  command -v opencode >/dev/null 2>&1 || { echo "no opencode binary on PATH"; return 1; }
  local before after out rc
  before="$(pfm ls --tsv 2>/dev/null | awk -F'\t' '$1 == "resume-opencode" { print $2 }' | sort)"
  out="$(cd "$CWD" && timeout 180 opencode run "reply with one word: ready" 2>&1)"
  rc=$?
  printf '%s\n' "$out"
  [ "$rc" -eq 0 ] || return "$rc"
  printf '%s' "$out" | tail -1 | grep -qi ready || { echo "opencode run never answered ready — its auth may not be live"; return 1; }
  after="$(pfm ls --tsv 2>/dev/null | awk -F'\t' '$1 == "resume-opencode" { print $2 }' | sort)"
  comm -13 <(printf '%s\n' "$before") <(printf '%s\n' "$after") | xargs -r -n1 pfm chat kill >/dev/null 2>&1
  [ -f "$OC_DB" ]
}
need "the OpenCode home at $OC_DB" "[ -f '$OC_DB' ]" 'make_oc_home' ||
  lane_abort "no OpenCode home — opencode.db never appeared at $OC_DB, so the picker's compose.Kind NewOpenCode row never renders (compose.go includeNewOpenCode requires len(OpenCodeAccountIDs) != 0) — no beat in this lane can reach a 'New OpenCode chat' row to press Enter on"

# ─── helpers: the picker, driven headless in its own tmux server ───────────
# Same shape as F.sh's tui_* helpers (its own lane-local copies, not shared via
# lib.sh) — only the subset E3 needs: open the picker, read its pane, type into
# its fuzzy search, press keys, read the highlighted row's label.

TUI_WHY=""
tui_close() { tmux -S "$TUI_SOCK" kill-server >/dev/null 2>&1; rm -f "$TUI_SOCK"; }
tui_pane() { tmux -S "$TUI_SOCK" capture-pane -p -t tui 2>&1; }
tui_keys() { tmux -S "$TUI_SOCK" send-keys -t tui "$@" 2>/dev/null; sleep 1; }
tui_type() { tmux -S "$TUI_SOCK" send-keys -t tui -l -- "$1" 2>/dev/null; sleep 1; }
tui_has() { tui_pane | grep -qF -- "$1"; }
tui_wait() { # tui_wait <secs> <needle> — 0 once the pane shows the literal needle
  local i=0
  while [ "$i" -lt "$1" ]; do
    tui_has "$2" && return 0
    sleep 1
    i=$((i + 1))
  done
  return 1
}
# tui_open <cols> <rows> <pfm args…> — the picker in its own tmux server on a
# socket OUTSIDE pfm's tmux dir (the fleet scan never mistakes it for a chat).
tui_open() {
  local cols="$1" rows="$2" out
  shift 2
  tui_close
  TUI_WHY=""
  out="$(tmux -S "$TUI_SOCK" new-session -d -s tui -x "$cols" -y "$rows" -c "$CWD" \
    "env TERM=xterm-256color COLORTERM=truecolor pfm $*" 2>&1)" || {
    TUI_WHY="tmux new-session for the picker failed: $(one_line "$out")"
    return 1
  }
  tui_wait 25 ' tabs ' && return 0
  TUI_WHY="the picker (pfm $*) never painted its tabs line in 25s; pane: $(one_line "$(tui_pane)")"
  return 1
}
# tui_selected — the highlighted row's name, columns after it (badges, size,
# AGE) cut off, matching F.sh's own reader exactly.
tui_selected() { tui_pane | grep -F '› ' | head -1 | sed -e 's/^.*› *//' -e 's/  .*//'; }

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

# open_main — the lane's chat, opened the ONE live door (fact 1): the picker's
# merged "New chat" row, cycled to OpenCode and read back from capture-pane —
# never assumed by position, since the row is Claude by default and the
# merge/no-merge picker mode changes whether Left/Right cycles it or Down
# walks to a separate row. Sets OC_ID (the resume-opencode row's ID, the
# addressable handle every later verb uses — fact 3) and OC_SOCK (the fresh
# ox- socket) on success. The library's single re-open (lane_reopen) spends
# this same command after the chat dies under a later beat.
OC_ID="" OC_SOCK=""
open_main() {
  local before_ox before_ids after_ox after_ids label i sockpath
  OC_ID="" OC_SOCK=""
  before_ox="$(ox_sockets)"
  before_ids="$(oc_resume_ids)"
  if ! tui_open 100 30 ls; then
    echo "picker never opened: $TUI_WHY"
    return 1
  fi
  tui_type New
  label="$(tui_selected)"
  i=0
  # Merged picker: Right cycles the ONE new-chat row's engine. Unmerged
  # picker: a separate "New OpenCode chat" row exists already, or Down walks
  # to it. Alternating covers both without assuming which mode is configured.
  while [ "$i" -lt 6 ] && [ "$label" != "New OpenCode chat" ]; do
    if [ $((i % 2)) -eq 0 ]; then tui_keys Right; else tui_keys Down; fi
    label="$(tui_selected)"
    i=$((i + 1))
  done
  if [ "$label" != "New OpenCode chat" ]; then
    echo "the fuzzy-filtered 'New' row never read 'New OpenCode chat' after 6 tries (Right/Down alternating); last read: '$label'; pane: $(one_line "$(tui_pane)")"
    tui_close
    return 1
  fi
  tui_keys Enter
  sleep 2
  tui_close # the outer driver's job is done; the ox- server is independent (action/executor.go CreateChatServer runs before the exec'd attach)
  after_ox="$(ox_sockets)"
  OC_SOCK="$(comm -13 <(printf '%s\n' "$before_ox") <(printf '%s\n' "$after_ox") | head -1)"
  if [ -z "$OC_SOCK" ]; then
    echo "Enter on 'New OpenCode chat' never produced a fresh ox- socket under $(oc_tmux_dir 2>/dev/null || echo '<no tmux dir found>'); sockets now: $(one_line "$after_ox")"
    return 1
  fi
  sockpath="$(oc_tmux_dir)/$OC_SOCK"
  # A raw stimulus typed directly into the pane (never a pfm verb — none can
  # address this chat yet, fact 3): the needle a later wait reads, and the
  # turn OpenCode's own session index needs before it writes a row at all.
  tmux -S "$sockpath" send-keys -l "reply with one word: ready" 2>/dev/null
  tmux -S "$sockpath" send-keys Enter 2>/dev/null
  i=0
  while [ "$i" -lt 30 ]; do
    after_ids="$(oc_resume_ids)"
    OC_ID="$(comm -13 <(printf '%s\n' "$before_ids") <(printf '%s\n' "$after_ids") | head -1)"
    [ -n "$OC_ID" ] && break
    sleep 2
    i=$((i + 1))
  done
  if [ -z "$OC_ID" ]; then
    echo "socket $OC_SOCK is live but no new resume-opencode row appeared in 60s (ids now: $(one_line "$after_ids")) — nothing this lane's other verbs can address by id"
    return 1
  fi
  return 0
}
lane_reopen 'open_main'

beat E3.01-open-seat K3 T31
spends oc
target "$WANT_NAME"
if out="$(open_main)"; then
  bad=""
  sockpath="$(oc_tmux_dir)/$OC_SOCK"
  # fact 2: onChatServer titles the fresh window literally
  # pfmengine.MustLookup(OpenCode).Short == "OpenCode" — the one deterministic
  # label/title convergence this engine has (no `pfm chat name`: fact 3).
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
    pass "opened via the picker's 'New OpenCode chat' row: session $OC_ID on socket $OC_SOCK · window '$window' · statusline $(one_line "$render" | cut -c1-120)"
  fi
else
  fail "the picker could not open a new OpenCode chat: $(one_line "$out")"
fi

# ─── E3.02 — OpenCode MCP wiring: chat local + harvester remote, doctor row ─

beat E3.02-mcp-registered M36
spends none
bad=""
OC_CFG="$HOME/.config/opencode/opencode.jsonc"
strip_jsonc() { sed 's#//.*$##' "$1"; }
# The file pfm's OWN installer registers MCP into: pfm/internal/installer/
# mcp.go writeMCPOpenCodeJSON, by the same fence discipline as Claude's
# .claude.json (mcp_accounts.go) and Codex's config.toml (mcp.go wireMCP) —
# root.sh's own `pfm install --yes` (setup.sh install) wrote this file before
# this lane ran.
if [ ! -f "$OC_CFG" ]; then
  bad="$bad no OpenCode MCP config at $OC_CFG — pfm install --yes did not write it;"
else
  strip_jsonc "$OC_CFG" | jq -e --arg bin "$PFM_BIN" \
    '.mcp.chat | .type == "local" and .command == [$bin, "mcp", "chat", "serve"] and .enabled == true' >/dev/null 2>&1 ||
    bad="$bad M36: $OC_CFG mcp.chat is not the local shape {type local, command [$PFM_BIN mcp chat serve], enabled true}: $(one_line "$(strip_jsonc "$OC_CFG" | jq -c '.mcp.chat' 2>&1)");"
  strip_jsonc "$OC_CFG" | jq -e --arg url "http://127.0.0.1:$PORT/mcp/harvester" \
    '.mcp.harvester | .type == "remote" and .url == $url and .enabled == true' >/dev/null 2>&1 ||
    bad="$bad M36: $OC_CFG mcp.harvester is not the remote shape {type remote, url http://127.0.0.1:$PORT/mcp/harvester, enabled true}: $(one_line "$(strip_jsonc "$OC_CFG" | jq -c '.mcp.harvester' 2>&1)");"
fi
oc_doctor_out="$(pfm doctor 2>&1)"
oc_row="$(printf '%s\n' "$oc_doctor_out" | grep -F 'client=opencode' | head -1)"
printf '%s\n' "$oc_row" | grep -qE 'harvester=pfm chat=pfm state=pfm$' ||
  bad="$bad M36: pfm doctor's opencode MCP row is not healthy: $(one_line "${oc_row:-no client=opencode row at all}");"
if [ -n "$bad" ]; then fail "$bad"; else
  pass "$OC_CFG: chat local ($PFM_BIN mcp chat serve) + harvester remote (:$PORT/mcp/harvester), both enabled; pfm doctor's opencode row reads harvester=pfm chat=pfm state=pfm"
fi

# ─── E3.03 — everything else: the shared CLI surface's REAL, sourced verdict
#             against a chat that is structurally never "live" (fact 2/3) ───

beat E3.03-everything-else
spends oc
target "$WANT_NAME"
if requires E3.01-open-seat; then
  bad=""

  # status: never an error for a dead chat (chat/status.go) — it must report
  # state=dead, and ONLY dead, since headless.Inspect never upgrades State
  # away from StateDead without chat.Live (fact 2).
  st="$(pfm chat status "$OC_ID" 2>&1)"
  st_rc=$?
  [ "$st_rc" -eq 3 ] || bad="$bad status exited $st_rc (want 3, codeDeadChat — a dead-but-resolved chat is a status, not a crash): $(one_line "$st");"
  [ "$(printf '%s' "$st" | awk -F'\t' 'NR == 1 { print $2 }')" = dead ] || bad="$bad status did not report state=dead: $(one_line "$st");"

  # last/read: Row.Path is never set for an OpenCode row, so both refuse with
  # the documented ErrNoTranscript wording, exit 3 (chat_dispatch.go).
  last="$(pfm chat last "$OC_ID" 2>&1)"
  last_rc=$?
  if [ "$last_rc" -ne 3 ] || ! printf '%s' "$last" | grep -qi 'has not written a transcript yet'; then
    bad="$bad last exited $last_rc without naming 'has not written a transcript yet': $(one_line "$last");"
  fi
  read_out="$(pfm chat read "$OC_ID" --tail 2 --condensed 2>&1)"
  read_rc=$?
  if [ "$read_rc" -ne 3 ] || ! printf '%s' "$read_out" | grep -qi 'has not written a transcript yet'; then
    bad="$bad read exited $read_rc without naming 'has not written a transcript yet': $(one_line "$read_out");"
  fi

  # capture: chat.Live gates it directly (chat_command.go runChatCapture) —
  # "is not running", codeDeadChat.
  cap="$(pfm chat capture "$OC_ID" 2>&1)"
  cap_rc=$?
  if [ "$cap_rc" -ne 3 ] || ! printf '%s' "$cap" | grep -qi 'is not running'; then
    bad="$bad capture exited $cap_rc without naming 'is not running': $(one_line "$cap");"
  fi

  # inject: the injector's own roster (liveSeats, chat/names.go) excludes any
  # Socket-less row outright, so it falls to the plain "no chat named"
  # refusal — codeUnknownChat (4), never a delivery.
  inj="$(pfm chat inject --allow-unsigned "$OC_ID" "reply with exactly one word: OC-INJECT-OK" 2>&1)"
  inj_rc=$?
  if [ "$inj_rc" -ne 4 ] || ! printf '%s' "$inj" | grep -qi 'no chat named'; then
    bad="$bad inject exited $inj_rc without naming 'no chat named': $(one_line "$inj");"
  fi

  # name: ALSO chat.Live-gated (chat_command.go runChatNameWith) — the
  # coordinator's "so the lane's by-name verbs work" premise does not hold for
  # OpenCode; asserting the refusal IS the correct, real assertion.
  name_out="$(pfm chat name "$OC_ID" "$WANT_NAME" 2>&1)"
  name_rc=$?
  if [ "$name_rc" -ne 3 ] || ! printf '%s' "$name_out" | grep -qi 'is not running'; then
    bad="$bad name exited $name_rc without naming 'is not running' — pfm was expected to REFUSE this rename (fact 3): $(one_line "$name_out");"
  fi

  # kill/unkill: chat.Resolve scans compose.AllView (chat/target.go) and the
  # id-based tombstone path never requires Live (chat_command.go
  # runChatKill/runChatUnkill) — these are real, working verbs here. The
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
    pass "status(dead)/last+read(no transcript)/capture(not running)/inject(no chat named)/name(not running)/kill+unkill(tombstone by id) all asserted for real against the never-live OpenCode chat $OC_ID"
  fi
fi

lane_end

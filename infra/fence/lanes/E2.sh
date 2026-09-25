#!/usr/bin/env bash
# E2.sh — lane E2, Codex: one chat on the Codex home, walked depth-first from
# the spawn ceremony through the `/reload` matrix as far as Codex supports it,
# recovery from its rollout, the appendix hook, MCP over HTTP, the outside-in
# verbs, the tmux-less kill alias, self-compact, the launcher entry and doctor's
# codex_pane rows. Runs INSIDE a lane container (run.sh), never on a host.
#
#   run.sh --lanes E2            solo, from a fresh root
#   run.sh                       in the sequence, after O1 and E1
#
# Every beat asserts from pfm's OWN report (`pfm ls --tsv`, a verb's exit code,
# a file pfm wrote, the chat's last assistant message) or from the pane, never
# from a model's prose. Codex has no UserPromptSubmit hook, so `/reload` — `$reload`
# in the Codex composer — travels THROUGH the model, which runs `pfm chat reload`
# from its tool shell: the beat asserts that path from pfm's side (the worker's
# log, the respawned pane, the row), the model is only the stimulus. Beat ids
# are the contract in beats.md and map.tsv — check-map.sh
# fails when this file and those disagree.
#
# Cost: the Codex home (`spends cx`) — roughly 12 short turns plus three reboots.
# The chat is left ALIVE at lane end on purpose: M.15 and O2.06 assert against it.
#
# BROKEN STATE: the prelude aborts the lane by name when the Codex home is not
# configured, carries no credential, has no `codex` binary, or the chat cannot be
# opened; every later beat whose precondition failed reports `blocked-by`, and
# each ✗ carries the raw pane bytes in the lane log beside its assertion.
set -uo pipefail
LANES_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=lib.sh
. "$LANES_DIR/lib.sh"
lane_preamble

CHAT="${E2_CHAT:-E2_MAIN}"
CWD="${E2_CWD:-/work/orbit}"
CONFIG="$HOME/.config/pfm/pfm.config.json"
SID_DIR="${PFM_SID_DIR:-${TMPDIR:-/tmp}/cc-sid}"
PORT="$(jq -r '.mcp.http.port // 18377' "$CONFIG" 2>/dev/null || echo 18377)"

lane_begin E2

# ── prelude: what this lane needs, made when it is missing, no-op otherwise ──
[ -f "$CONFIG" ] || lane_abort "no pfm config at $CONFIG — the root image was not built by lanes/root.sh"
CODEX_HOME="$(jq -r '.codex.homes[0].home // empty' "$CONFIG")"
[ -n "$CODEX_HOME" ] || lane_abort "no Codex home configured in $CONFIG (.codex.homes is empty) — lane E2 has no seat to spend"
case "$CODEX_HOME" in "~"*) CODEX_HOME="$HOME${CODEX_HOME#\~}" ;; esac
CODEX_HOMES="$(jq -r '.codex.homes | length' "$CONFIG")"
[ -d "$CODEX_HOME" ] || lane_abort "the configured Codex home $CODEX_HOME does not exist"
[ -s "$CODEX_HOME/auth.json" ] || lane_abort "NO CREDENTIAL — $CODEX_HOME/auth.json is missing or empty (lanes/creds.sh stages it)"
command -v codex >/dev/null 2>&1 || lane_abort "no codex binary on PATH — the root image was built without the Codex CLI"

need "the working directory $CWD" "[ -d '$CWD/.git' ]" \
  "mkdir -p '$CWD' && git -C '$CWD' init -q && git -C '$CWD' commit -q --allow-empty -m lane" ||
  lane_abort "no working directory for the chat to live in ($CWD)"
need "the pfm MCP daemon on :$PORT" \
  "[ \"\$(curl -s -o /dev/null -w '%{http_code}' -m 2 http://127.0.0.1:$PORT/mcp/professor)\" != 000 ]" \
  "bash /worktree/infra/demo/daemon.sh" ||
  lane_abort "the professor MCP daemon never answered on :$PORT — a Codex chat's professor stdio server would have no daemon to forward to"

# ─── E2.01 — the spawn ceremony on the Codex home ───────────────────────────

# open_main — the lane's chat, opened the one way: E2.01 spawns it and the
# library's single re-open (lane_reopen) spends the same command after the
# chat dies under a later beat. No --account: the Codex primary is the one home
# this run stages (lanes/creds.sh).
open_main() {
  pfm chat new --name "$CHAT" --engine cx --cwd "$CWD" --await --timeout 300 \
    "You are $CHAT, the Codex chat an automated Tier B lane drives. Reply with one word: ready. Then wait and do exactly what each next message says, nothing more." 2>&1
}
lane_reopen 'open_main'

# pane_pid — the live pane's process id, read fresh from the LIVE row's socket:
# a reload respawns the pane, so a changed pid is the pfm-independent proof
# that a reboot happened (the row alone empties and refills too fast to catch).
pane_pid() {
  local sock
  sock="$(live_field "$CHAT" 11)"
  [ -n "$sock" ] || return 1
  tmux -S "$sock" list-panes -F '#{pane_pid}' 2>/dev/null | head -1
}

# rollout_of <thread-id> — the rollout file the thread writes under the Codex
# home (internal/index/walk.go: sessions/**/rollout-*<id>*.jsonl), newest first.
rollout_of() {
  find "$CODEX_HOME/sessions" -type f -name "rollout-*$1*.jsonl" 2>/dev/null | sort | tail -1
}

# addr — the handle every name-addressed verb (last/inject/ask/watch/capture)
# gets: the LIVE row's thread id, read fresh. After E2.03's `--new --hide` the
# label is held by the hidden thread AND the live one, the exact shape the
# resolver ledgers as ambiguous (known-gaps.yml E1.26) — the id is unique,
# and pfm ls --tsv reads (live_field, row_field) stay by name.
addr() {
  local id
  id="$(live_field "$CHAT" 2)"
  printf '%s' "${id:-$CHAT}"
}

beat E2.01-open-seat
spends cx
target "$CHAT"
if live_chat "$CHAT"; then
  pass "$CHAT was already live (the sequence built it): kind $(live_field "$CHAT" 1) · thread $(live_field "$CHAT" 2) · socket $(live_field "$CHAT" 11)"
else
  out="$(open_main)"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    fail "pfm chat new --engine cx exited $rc: $(one_line "$out")"
  elif ! live_chat "$CHAT"; then
    fail "chat new exited 0 but no live row for $CHAT: $(one_line "$(pfm ls --plain)")"
  elif [ "$(live_field "$CHAT" 1)" != live-codex ]; then
    fail "the live row reports kind '$(live_field "$CHAT" 1)', not live-codex"
  else
    sock="$(live_field "$CHAT" 11)"
    window=""
    # The label converges on the window through name-sync's Codex half, not at
    # spawn — a bounded wait, and the failure names the window it did read.
    wait_for 60 "tmux -S '$sock' list-windows -F '#{window_name}' 2>/dev/null | grep -qF '$CHAT'"
    window="$(tmux -S "$sock" list-windows -F '#{window_name}' 2>&1 | head -1)"
    case "$window" in
      *"$CHAT"*) pass "live row, kind live-codex, thread $(live_field "$CHAT" 2), account $(live_field "$CHAT" 9), socket $sock; tmux window '$window' carries the label" ;;
      *) fail "live-codex row present but the tmux window name '$(one_line "$window")' never carried the label $CHAT within 60s" ;;
    esac
  fi
fi

# ─── E2.02 — statusline: the Codex usage segment, and --refresh-gpt ─────────

beat E2.02-statusline
spends cx
target_live "$CHAT"
if requires E2.01-open-seat; then
  bad=""
  # T32: --refresh-gpt execs the Codex rate-limit read and writes the cache the
  # render reads (internal/statusline/refresh_codex.go → CodexStatuslineCachePath).
  if [ -n "${PFM_HOME:-}" ]; then cache="$PFM_HOME/tmp/cc-gpt-usage-$(id -u).json"; else cache="${TMPDIR:-/tmp}/cc-gpt-usage-$(id -u).json"; fi
  refresh="$(CODEX_HOME="$CODEX_HOME" pfm statusline --refresh-gpt 2>&1)"
  refresh_rc=$?
  if [ "$refresh_rc" -ne 0 ]; then
    bad="$bad pfm statusline --refresh-gpt exited $refresh_rc ($(one_line "$refresh"));"
  elif [ ! -s "$cache" ]; then
    bad="$bad --refresh-gpt exited 0 but wrote no cache at $cache;"
  elif ! jq -e '.ts' "$cache" >/dev/null 2>&1; then
    bad="$bad the usage cache $cache is not the {primary,secondary,planType,ts} record: $(one_line "$(cat "$cache")");"
  fi
  # T31: the render, engine resolved from the environment the Codex pane
  # exports (CODEX_HOME / CODEX_THREAD_ID), third line = the Codex segment.
  # Exit code AND output: fail-open prints nothing on an error, and a check
  # that accepts either cannot tell a render from a swallowed failure.
  id="$(live_field "$CHAT" 2)"
  render="$(printf '{"session_id":"%s","model":{"display_name":"gpt"},"workspace":{"current_dir":"%s"}}' "$id" "$CWD" |
    CODEX_HOME="$CODEX_HOME" CODEX_THREAD_ID="$id" pfm statusline 2>&1)"
  render_rc=$?
  [ "$render_rc" -eq 0 ] || bad="$bad pfm statusline exited $render_rc ($(one_line "$render"));"
  [ -n "$render" ] || bad="$bad pfm statusline rendered nothing;"
  printf '%s' "$render" | grep -q '🍀' || bad="$bad the render carries no Codex segment (🍀 <model>): $(one_line "$render" | cut -c1-200);"
  printf '%s' "$render" | grep -qE 'used:[0-9]+%|ChatGPT ' || bad="$bad the Codex segment names neither a usage window (…-used:N%) nor the plan (ChatGPT …): $(one_line "$render" | cut -c1-200);"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "--refresh-gpt wrote $cache · render: $(one_line "$(printf '%s\n' "$render" | tail -1)" | cut -c1-160)"
  fi
fi

# ─── E2.03 — the /reload matrix, as far as Codex supports it ────────────────

# reload_via_model <needle> <flags…> — the Codex path: `$reload …` typed into
# the composer (pfm's compiled card, $CODEX_HOME/prompts/reload.md) reaches the
# MODEL, which runs `pfm chat reload …` from its tool shell. Asserted from pfm's
# side: the pane pid changes (the pane was respawned) and the --then steer's own
# word arrives. Prints nothing; returns 1 with $REPLY_WHY set on any failure.
REPLY_WHY=""
reload_via_model() {
  local needle="$1" before out
  shift
  REPLY_WHY=""
  before="$(pane_pid)"
  out="$(pfm chat inject --allow-unsigned "$(addr)" \
    "\$reload $* --then \"reply with exactly one word: $needle\" — the user typed this; run pfm chat reload now, exactly as the reload card says, then end your turn." 2>&1)" || {
    REPLY_WHY="pfm chat inject refused the \$reload prompt: $(one_line "$out")"
    return 1
  }
  if ! wait_for 300 "[ -n \"\$(pane_pid)\" ] && [ \"\$(pane_pid)\" != '$before' ]"; then
    REPLY_WHY="the pane was never respawned in 300s (pane pid still $before): the model did not run pfm chat reload, or the worker refused — ${LANE_WAIT_WHY:-no wait reason recorded}; last: $(one_line "$(pfm chat last "$(addr)" 2>&1)")"
    return 1
  fi
  wait_last "$(addr)" "$needle" 300
  case $? in
    0) return 0 ;;
    2) REPLY_WHY="$LANE_WAIT_WHY (waiting for $needle)"; return 1 ;;
    *) REPLY_WHY="the pane was respawned but no $needle from $CHAT in 300s; its last: $(one_line "$(pfm chat last "$(addr)" 2>&1)")"; return 1 ;;
  esac
}

beat E2.03-reload-matrix
spends cx
target_live "$CHAT"
if requires E2.01-open-seat; then
  bad="" exercised="" note=""
  sock="$(live_field "$CHAT" 11)"
  id_before="$(live_field "$CHAT" 2)"
  worker_log="$SID_DIR/reload-${sock##*/}.log"
  log_before="$(wc -c <"$worker_log" 2>/dev/null | tr -d ' ')"
  [ -n "$log_before" ] || log_before=0
  # The path this beat exists for: no UserPromptSubmit hook on the Codex home
  # (pfm's own hooks.json carries none), and the reload card the model reads.
  if [ -f "$CODEX_HOME/hooks.json" ] && grep -q 'reload-intercept' "$CODEX_HOME/hooks.json"; then
    bad="$bad $CODEX_HOME/hooks.json carries a reload-intercept hook — Codex has no UserPromptSubmit, this beat's premise is wrong;"
  fi
  [ -f "$CODEX_HOME/prompts/reload.md" ] || bad="$bad no compiled reload card at $CODEX_HOME/prompts/reload.md (pfm install → codexgen writes it) — \$reload has nothing to expand to;"
  # --model: the chat's OWN model from its rollout (pfm chat status --json), so
  # the flag is exercised without guessing a name the Codex roster may not hold.
  model="$(pfm chat status "$(addr)" --json 2>/dev/null | jq -r '.model // empty' 2>/dev/null)"
  if [ -z "$bad" ]; then
    if [ -n "$model" ]; then
      set -- --model "$model" --effort medium
      exercised="$exercised --model $model, --effort medium,"
    else
      set -- --effort medium
      note="$note --model not exercised (the rollout named no model for the chat);"
      exercised="$exercised --effort medium,"
    fi
    if ! reload_via_model RELOADED-CX-1 "$@"; then
      bad="$bad [$*]: $REPLY_WHY;"
    elif [ "$(live_field "$CHAT" 2)" != "$id_before" ]; then
      bad="$bad [$*] resumed a DIFFERENT thread ($(live_field "$CHAT" 2), was $id_before) — a flag reload must resume the same conversation;"
    fi
    exercised="$exercised --then,"
  fi
  if [ -z "$bad" ]; then
    # --new --hide, followed BY SOCKET: the reborn Codex thread is a fresh id
    # with no name, so the label stays with the thread left behind (now
    # hidden) and a name-addressed wait would sit on a dead conversation.
    anchor_socket "$sock"
    before_pid="$(pane_pid)"
    out="$(pfm chat inject --allow-unsigned "$id_before" \
      "\$reload --new --hide --then \"reply with exactly one word: RELOADED-CX-2\" — the user typed this; run pfm chat reload now, exactly as the reload card says, then end your turn." 2>&1)" ||
      bad="$bad [--new --hide]: pfm chat inject refused the \$reload prompt: $(one_line "$out");"
    if [ -z "$bad" ]; then
      if ! wait_for 300 "[ -n \"\$(socket_field '$sock' 2)\" ] && [ \"\$(socket_field '$sock' 2)\" != '$id_before' ]"; then
        bad="$bad [--new --hide]: no fresh thread id on socket $sock in 300s (it still reads '$(socket_field "$sock" 2)', pane pid $before_pid → $(tmux -S "$sock" list-panes -F '#{pane_pid}' 2>/dev/null | head -1)); ${LANE_WAIT_WHY:-no wait reason recorded};"
      else
        id_new="$(socket_field "$sock" 2)"
        new_name="$(socket_field "$sock" 5)"
        name_out="$(pfm chat name "$id_new" "$CHAT" 2>&1)"
        name_rc=$?
        sleep 3
        if [ "$name_rc" -ne 0 ] || [ "$(socket_field "$sock" 5)" != "$CHAT" ]; then
          bad="$bad [--new --hide]: fresh thread $id_new on $sock (auto-named '$new_name') could not be renamed back to $CHAT (pfm chat name exited $name_rc: $(one_line "$name_out")); the row reads '$(socket_field "$sock" 5)';"
        elif ! wait_last "$id_new" RELOADED-CX-2 300; then
          bad="$bad [--new --hide]: fresh thread $id_new but no RELOADED-CX-2: ${LANE_WAIT_WHY:-no wait reason recorded}; last: $(one_line "$(pfm chat last "$id_new" 2>&1)");"
        else
          visible="$(pfm ls --tsv 2>/dev/null | awk -F'\t' -v n="$CHAT" 'NR > 1 && $5 == n && $10 == "false" { c++ } END { print c + 0 }')"
          # -a: the thread left behind is expected to read killed=true, which
          # the default --tsv view would never show (it drops killed rows).
          hidden="$(pfm ls -a --tsv 2>/dev/null | awk -F'\t' -v i="$id_before" 'NR > 1 && $2 == i { print $10; exit }')"
          if [ "$visible" -ne 1 ]; then
            bad="$bad [--new --hide]: $visible unhidden rows carry the name $CHAT (want exactly 1: the fresh thread $id_new);"
          elif [ "$hidden" != true ]; then
            bad="$bad [--new --hide]: the thread left behind ($id_before) reads killed='${hidden:-<no row>}' — --hide must hide it;"
          fi
          exercised="$exercised --new --hide (thread $id_before → $id_new on one socket),"
        fi
      fi
    fi
  fi
  if [ -z "$bad" ]; then
    # --sock: the ONE handle a caller outside the pane has — no model, pfm
    # resolves the socket's single live pane itself.
    before_pid="$(pane_pid)"
    out="$(pfm chat reload --sock "$sock" --then "reply with exactly one word: RELOADED-CX-3" 2>&1)"
    rc=$?
    if [ "$rc" -ne 0 ]; then
      bad="$bad [--sock]: pfm chat reload --sock $sock exited $rc: $(one_line "$out");"
    elif ! printf '%s' "$out" | grep -q 'reload scheduled in place'; then
      bad="$bad [--sock]: exit 0 but the scheduler did not report 'reload scheduled in place': $(one_line "$out");"
    elif ! wait_for 300 "[ -n \"\$(pane_pid)\" ] && [ \"\$(pane_pid)\" != '$before_pid' ]"; then
      bad="$bad [--sock]: scheduled, but the pane was never respawned in 300s (pane pid still $before_pid); ${LANE_WAIT_WHY:-no wait reason recorded};"
    elif ! wait_last "$(addr)" RELOADED-CX-3 300; then
      bad="$bad [--sock]: respawned but no RELOADED-CX-3: ${LANE_WAIT_WHY:-no wait reason recorded}; last: $(one_line "$(pfm chat last "$(addr)" 2>&1)");"
    fi
    exercised="$exercised --sock,"
  fi
  # The worker's own record (cmd/pfm/chat_reload_command.go: reload-<socket>.log
  # in the SID dir): every reload above appends to it, from the model's tool
  # shell and from this shell alike.
  log_after="$(wc -c <"$worker_log" 2>/dev/null | tr -d ' ')"
  [ -n "$log_after" ] || log_after=0
  if [ ! -f "$worker_log" ]; then
    bad="$bad no reload worker log at $worker_log — the worker records every reload there;"
  elif [ "$log_after" -le "$log_before" ]; then
    bad="$bad the reload worker log $worker_log did not grow ($log_before → $log_after bytes) across the reloads;"
  elif ! tail -c "+$((log_before + 1))" "$worker_log" | grep -qE 'respawned in place|rebooted FRESH'; then
    bad="$bad the worker log grew but its new slice reports neither 'respawned in place' nor 'rebooted FRESH': $(one_line "$(tail -c "+$((log_before + 1))" "$worker_log" | tail -3)");"
  fi
  # Named, never silently omitted: what this run cannot or does not exercise.
  if [ "$CODEX_HOMES" -lt 2 ]; then
    note="$note --account not exercised: $CODEX_HOMES Codex home in this run, nothing to reload onto (pfm validates it against .codex.homes);"
  else
    note="$note --account not exercised by this beat ($CODEX_HOMES Codex homes configured);"
  fi
  note="$note --1h not supported on cx by design (the cache window is Claude's prompt-cache env; the Codex launch line carries none — internal/reload/reload.go codexRun);"
  if [ -n "$bad" ]; then fail "$bad exercised:${exercised:-none}; $note"; else
    pass "through the model:${exercised} worker log grew $log_before → $log_after bytes;$note"
  fi
fi

# ─── E2.04 — recover from the rollout ───────────────────────────────────────

beat E2.04-recover
spends cx
target_live "$CHAT"
if requires E2.01-open-seat; then
  bad=""
  id="$(live_field "$CHAT" 2)"
  rollout="$(rollout_of "$id")"
  if [ -z "$rollout" ]; then
    fail "no rollout for thread $id under $CODEX_HOME/sessions (rollout-*$id*.jsonl) — nothing to recover from; the chat's row: $(one_line "$(chat_row "$CHAT")")"
  else
    out="$(pfm chat recover "$id" 2>&1)"
    rc=$?
    dir="$CODEX_HOME/recovered-$id"
    [ "$rc" -eq 0 ] || bad="$bad pfm chat recover <thread-id> exited $rc ($(one_line "$out"));"
    summary="$(printf '%s\n' "$out" | grep -E '^messages=[0-9]+ carried=[0-9]+ malformed=[0-9]+$' | head -1)"
    [ -n "$summary" ] || bad="$bad no 'messages=N carried=N malformed=N' summary line: $(one_line "$out");"
    messages="$(printf '%s' "$summary" | sed -n 's/^messages=\([0-9]*\).*/\1/p')"
    [ "${messages:-0}" -ge 1 ] || bad="$bad recover found ${messages:-0} messages in a rollout the chat has spoken in;"
    printf '%s' "$out" | grep -qF "recovered thread $id" || bad="$bad output does not name 'recovered thread $id': $(one_line "$out");"
    for f in brief.md compaction-memory.md transcript.md; do
      [ -s "$dir/$f" ] || bad="$bad $dir/$f absent or empty;"
    done
    grep -qF "$CHAT" "$dir/transcript.md" 2>/dev/null || bad="$bad transcript.md does not carry the chat's own prompt (no '$CHAT' in it);"
    # The rollout-path form of the same verb, and the named absence.
    out_path="$(pfm chat recover "$rollout" 2>&1)"
    rc_path=$?
    [ "$rc_path" -eq 0 ] || bad="$bad pfm chat recover <rollout-path> exited $rc_path ($(one_line "$out_path"));"
    printf '%s' "$out_path" | grep -qF "recovered thread $id" || bad="$bad the rollout-path form did not recover thread $id: $(one_line "$out_path");"
    expect-log 'no rollout found'
    missing="$(pfm chat recover lane-no-such-thread 2>&1)"
    missing_rc=$?
    if [ "$missing_rc" -eq 0 ]; then
      bad="$bad recover on a thread that does not exist exited 0 ($(one_line "$missing"));"
    elif ! printf '%s' "$missing" | grep -q 'no rollout found'; then
      bad="$bad recover on a missing thread exited $missing_rc but did not say 'no rollout found': $(one_line "$missing");"
    fi
    rm -rf "$dir" 2>/dev/null
    if [ -n "$bad" ]; then fail "$bad"; else
      pass "$summary from $(basename "$rollout"); brief/compaction-memory/transcript written under $dir (removed after the assertion); rollout-path form agrees; a missing thread refused by name"
    fi
  fi
fi

# ─── E2.05 — the fleet prompt, present in the first turn ────────────────────

beat E2.05-fleet-prompt
spends cx
target_live "$CHAT"
if requires E2.01-open-seat; then
  bad=""
  config="$CODEX_HOME/config.toml"
  staged="$HOME/.local/share/pfm/install/harness-prompts/codex.md"
  # pfm writes the composed Codex prompt into developer_instructions
  # (internal/installer/codex_developer_instructions.go); the retired
  # SessionStart appendix hook must be gone from hooks.json.
  if [ ! -f "$config" ]; then
    bad="$bad no $config — the installer writes the fleet prompt there;"
  elif ! grep -q '^# BEGIN pfm developer_instructions — installer-owned$' "$config"; then
    bad="$bad $config carries no pfm developer_instructions fence;"
  fi
  if [ -f "$CODEX_HOME/hooks.json" ] && grep -q 'internal codex-appendix' "$CODEX_HOME/hooks.json"; then
    bad="$bad $CODEX_HOME/hooks.json still carries the retired SessionStart appendix hook;"
  fi
  [ -f "$staged" ] || bad="$bad no staged Codex prompt at $staged to compare the config against;"
  marker="$(head -1 "$staged" 2>/dev/null)"
  id="$(live_field "$CHAT" 2)"
  rollout="$(rollout_of "$id")"
  if [ -z "$rollout" ]; then
    bad="$bad no rollout for thread $id under $CODEX_HOME/sessions — the first turn cannot be read;"
  elif [ -n "$marker" ]; then
    # First turn: the developer message carrying the prompt must come BEFORE
    # the first assistant message in the rollout (line order).
    prompt_at="$(grep -n '"role":"developer"' "$rollout" | grep -F "$marker" | head -1 | cut -d: -f1)"
    assistant_at="$(grep -n '"role":"assistant"' "$rollout" | head -1 | cut -d: -f1)"
    if [ -z "$prompt_at" ]; then
      bad="$bad the rollout $(basename "$rollout") carries no developer message with the prompt's first line — developer_instructions never reached the session;"
    elif [ -z "$assistant_at" ]; then
      bad="$bad the rollout carries the fleet prompt (line $prompt_at) but no assistant message at all — the first turn never happened;"
    elif [ "$prompt_at" -gt "$assistant_at" ]; then
      bad="$bad the fleet prompt landed at rollout line $prompt_at, AFTER the first assistant message (line $assistant_at) — not in the first turn;"
    fi
    grep -qF 'Warning: truncated output' "$rollout" &&
      bad="$bad the rollout carries a truncated hook-output block — something is still delivering context through a capped hook;"
  fi
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "config.toml carries the pfm-owned developer_instructions fence, no appendix hook remains, and the prompt is at rollout line $prompt_at before the first assistant line $assistant_at"
  fi
fi

# ─── E2.06 — MCP over stdio: the chat_* tools the Codex session lists ──────

beat E2.06-mcp-stdio
spends cx
target_live "$CHAT"
if requires E2.01-open-seat; then
  bad=""
  toml="$CODEX_HOME/config.toml"
  bin="$HOME/.local/bin/pfm"
  # M34: the professor server wired over stdio inside the installer-owned fence
  # (internal/installer/mcp.go writeMCPCodeConfigAt).
  if [ ! -f "$toml" ]; then
    bad="$bad no $toml — the installer writes the [mcp_servers] fence there;"
  else
    fence="$(sed -n '/^# BEGIN pfm mcp_servers — installer-owned$/,/^# END pfm mcp_servers — installer-owned$/p' "$toml")"
    [ -n "$fence" ] || bad="$bad $toml carries no '# BEGIN/END pfm mcp_servers — installer-owned' fence;"
    printf '%s' "$fence" | grep -qF '[mcp_servers.professor]' || bad="$bad the fence has no [mcp_servers.professor];"
    printf '%s' "$fence" | grep -qF "command = \"$bin\"" || bad="$bad the fence does not point professor's command at $bin: $(one_line "$fence");"
    printf '%s' "$fence" | grep -qF 'args = ["mcp", "serve", "--stdio"]' || bad="$bad the fence does not wire professor's args to [\"mcp\", \"serve\", \"--stdio\"]: $(one_line "$fence");"
  fi
  # The served surface: three JSON-RPC frames on stdin to the stdio command
  # itself (initialize, notifications/initialized, tools/list) — the same
  # command a Codex session launches to forward to the daemon.
  init='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"lane-e2","version":"1"}}}'
  initd='{"jsonrpc":"2.0","method":"notifications/initialized"}'
  list='{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
  # stdin is HELD OPEN after the frames: on EOF the server ends at once and the
  # tools/list reply still in flight is lost (check-map.sh does the same).
  out="$( { printf '%s\n%s\n%s\n' "$init" "$initd" "$list"; sleep 5; } | timeout 15 "$bin" mcp serve --stdio 2>/tmp/e2-mcp-stdio.err)"
  tools=""
  if [ -z "$out" ]; then
    bad="$bad pfm mcp serve --stdio produced no output on the three-frame handshake: $(one_line "$(cat /tmp/e2-mcp-stdio.err 2>/dev/null)");"
  elif ! printf '%s' "$out" | grep -q '"serverInfo"'; then
    bad="$bad the initialize response over stdio carried no serverInfo: $(one_line "$out" | cut -c1-200);"
  else
    tools="$(printf '%s' "$out" | grep '"id":2' | jq -r '.result.tools[]?.name' 2>/dev/null | sort)"
    if [ -z "$tools" ]; then
      bad="$bad tools/list over stdio returned no tool names: $(one_line "$out" | cut -c1-200);"
    else
      for want in chat_ls chat_status chat_last chat_read chat_inject chat_self_compact chat_whoami chat_new chat_kill chat_unkill chat_name; do
        printf '%s\n' "$tools" | grep -qx "$want" || bad="$bad tools/list lacks $want;"
      done
      # The daemon's own roster (/status servers.chat) and the stdio-served
      # list must be one list — two readers of one truth.
      status_tools="$(curl -s -m 5 "http://127.0.0.1:$PORT/status" | jq -r '.servers.chat[]?' 2>/dev/null | sort)"
      if [ -z "$status_tools" ]; then
        bad="$bad /status names no servers.chat tools (the daemon's own roster is unreadable);"
      elif [ "$status_tools" != "$tools" ]; then
        bad="$bad /status servers.chat and tools/list disagree — status: $(printf '%s' "$status_tools" | tr '\n' ' ') vs served: $(printf '%s' "$tools" | tr '\n' ' ');"
      fi
    fi
  fi
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "config.toml fence wires professor's stdio command at $bin; the three-frame stdio handshake served $(printf '%s\n' "$tools" | grep -c .) tools ($(printf '%s' "$tools" | grep -c '^chat_') chat_*), matching /status"
  fi
fi

# ─── E2.07 — inject, ask, watch on the Codex home ───────────────────────────

beat E2.07-inject-ask-watch
spends cx
target_live "$CHAT"
if requires E2.01-open-seat; then
  bad=""
  id="$(addr)"
  out="$(pfm chat inject --allow-unsigned "$id" "reply with exactly one word: INJECT-CX-OK" 2>&1)" ||
    bad="$bad base inject was refused: $(one_line "$out");"
  [ -n "$bad" ] || wait_last "$id" INJECT-CX-OK 240 || bad="$bad the base inject never reached the chat (no INJECT-CX-OK): ${LANE_WAIT_WHY:-no wait reason recorded};"
  ask="$(pfm chat ask "$id" --timeout 240 "reply with exactly one word: ASK-CX-OK" 2>&1)"
  ask_rc=$?
  if [ "$ask_rc" -ne 0 ]; then
    bad="$bad pfm chat ask exited $ask_rc: $(one_line "$ask");"
  elif ! printf '%s' "$ask" | grep -qF ASK-CX-OK; then
    bad="$bad ask returned without the fresh answer: $(one_line "$ask");"
  fi
  watch="$(timeout 240 pfm chat watch "$id" --idle-after 10 --once 2>&1)"
  watch_rc=$?
  if [ "$watch_rc" -eq 124 ]; then
    bad="$bad watch --idle-after 10 --once never returned inside 240s (the idle transition was never reported);"
  elif [ "$watch_rc" -ne 0 ]; then
    bad="$bad watch exited $watch_rc: $(one_line "$watch");"
  elif [ -z "$watch" ]; then
    bad="$bad watch exited 0 but printed nothing — no transition was reported;"
  fi
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "inject delivered (INJECT-CX-OK), ask blocked for the fresh turn (ASK-CX-OK), watch --idle-after 10 --once reported: $(one_line "$watch")"
  fi
fi

# ─── E2.08 — kill self/me from the tmux-less Codex tool shell ───────────────

beat E2.08-kill-self
spends cx
target_live "$CHAT"
if requires E2.01-open-seat; then
  bad=""
  id="$(live_field "$CHAT" 2)"
  # Codex tool shells are served by app-server and carry no TMUX — only
  # CODEX_THREAD_ID names the caller (cmd/pfm/chat_command.go runChatKill →
  # chat.SeatIdentity). Driven from this shell with exactly that environment.
  for alias in self me; do
    out="$(env -u TMUX -u TMUX_PANE CODEX_THREAD_ID="$id" pfm chat kill "$alias" 2>&1)"
    rc=$?
    sleep 2
    if [ "$rc" -ne 0 ]; then
      bad="$bad kill $alias exited $rc: $(one_line "$out");"
    elif ! printf '%s' "$out" | grep -qF "killed $id"; then
      bad="$bad kill $alias exited 0 but did not report 'killed $id': $(one_line "$out");"
    elif [ "$(row_field "$CHAT" 10)" != true ]; then
      bad="$bad after kill $alias the row's killed column is '$(row_field "$CHAT" 10)', not true;"
    fi
    unkill="$(pfm chat unkill "$id" 2>&1)"
    unkill_rc=$?
    sleep 2
    if [ "$unkill_rc" -ne 0 ]; then
      bad="$bad unkill $id exited $unkill_rc: $(one_line "$unkill");"
    elif [ "$(row_field "$CHAT" 10)" != false ]; then
      bad="$bad after unkill the row's killed column is '$(row_field "$CHAT" 10)', not false;"
    fi
  done
  # A thread id no live seat carries must be refused by name, never a kill.
  expect-log 'chat kill'
  bogus="$(env -u TMUX -u TMUX_PANE CODEX_THREAD_ID=00000000-0000-4000-8000-000000000000 pfm chat kill self 2>&1)"
  bogus_rc=$?
  if [ "$bogus_rc" -eq 0 ]; then
    bad="$bad kill self with a CODEX_THREAD_ID no seat carries exited 0: $(one_line "$bogus");"
  elif [ -z "$bogus" ]; then
    bad="$bad kill self with an unknown CODEX_THREAD_ID exited $bogus_rc silently;"
  fi
  [ "$(row_field "$CHAT" 10)" = false ] || bad="$bad the unknown-thread kill touched $CHAT (killed='$(row_field "$CHAT" 10)');"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "self and me each resolved thread $id through CODEX_THREAD_ID with no TMUX → killed=true, unkill → false; an unknown thread refused (exit $bogus_rc: $(one_line "$bogus" | cut -c1-120))"
  fi
fi

# ─── E2.09 — self-compact composes the bare /compact on Codex ───────────────

# waiter_steer <socket> — the FIRST --steer of the live `pfm internal then`
# waiter for that socket, read from its argv in /proc: the composed command
# exactly as pfm scheduled it, before anything is typed.
waiter_steer() {
  local f
  for f in /proc/[0-9]*/cmdline; do
    tr '\0' '\n' <"$f" 2>/dev/null | awk -v sock="$1" '
      { a[NR] = $0 }
      END {
        then = 0; ours = 0
        for (i = 1; i < NR; i++) {
          if (a[i] == "internal" && a[i + 1] == "then") then = 1
          if (a[i] == "--socket" && index(a[i + 1], sock) > 0) ours = 1
        }
        if (!then || !ours) exit 1
        for (i = 1; i < NR; i++) if (a[i] == "--steer") { print a[i + 1]; exit 0 }
        exit 1
      }' && return 0
  done
  return 1
}

beat E2.09-self-compact
spends cx
target_live "$CHAT"
if requires E2.01-open-seat; then
  id="$(live_field "$CHAT" 2)"
  sock="$(live_field "$CHAT" 11)"
  # `pfm chat self-compact` resolves the CALLER: from a Codex tool shell that is
  # CODEX_THREAD_ID with no TMUX (internal/inject/engine.go Resolve "self"), the
  # same environment this shell reproduces.
  out="$(env -u TMUX -u TMUX_PANE CODEX_THREAD_ID="$id" pfm chat self-compact --then "reply with exactly one word: COMPACTED-CX" lane-e2 2>&1)"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    fail "pfm chat self-compact exited $rc from the tmux-less Codex shell: $(one_line "$out")"
  elif ! printf '%s' "$out" | grep -q 'scheduled COMMAND'; then
    fail "self-compact exited 0 but did not report 'scheduled COMMAND …': $(one_line "$out")"
  else
    # The composed form, from pfm's own record: the waiter's argv.
    steer=""
    for _ in 1 2 3 4 5 6 7 8 9 10; do
      steer="$(waiter_steer "$sock")" && break
      sleep 1
    done
    typed=0 landed=0
    deadline=$(( $(_lane_now) + 300 ))
    while [ "$(_lane_now)" -lt "$deadline" ]; do
      pane "$id" | grep -qF '/compact' && typed=1
      pfm chat last "$id" 2>/dev/null | grep -qF COMPACTED-CX && { landed=1; break; }
      lane_alive || break
      sleep 5
    done
    [ "$typed" -eq 1 ] || { pane "$id" | grep -qF '/compact' && typed=1; }
    steer_log="$(printf '%s' "$out" | sed -n 's/.*(log: \([^)]*\)).*/\1/p')"
    log_tail="$(tail -1 "$steer_log" 2>/dev/null)"
    if [ -z "$steer" ]; then
      fail "pfm scheduled it but no 'pfm internal then --socket $sock … --steer' waiter was seen in /proc within 10s — the composed form could not be read (typed=$typed, landed=$landed; waiter log: $(one_line "${log_tail:-<none>}"))"
    elif [ "$steer" != "/compact" ]; then
      fail "the waiter's first steer is '$(one_line "$steer")' on a Codex target — pfm composed the focus form, not the bare /compact Codex takes (held, not disproved: the composer claim is wrong)"
    elif [ "$landed" -eq 0 ]; then
      fail "pfm composed the bare /compact (waiter argv) but no COMPACTED-CX in 300s (typed on pane: $typed); last: $(one_line "$(pfm chat last "$id" 2>&1)"); waiter log: $(one_line "${log_tail:-<none>}")"
    elif [ "$typed" -eq 0 ]; then
      fail "pfm composed the bare /compact and the steer landed, but the pane never showed /compact typed — the compaction is claimed, not seen; waiter log: $(one_line "${log_tail:-<none>}")"
    else
      pass "bare /compact composed for the Codex target (waiter argv), /compact seen typed on the pane, steer delivered (COMPACTED-CX); waiter log: $(one_line "${log_tail:-<none>}" | cut -c1-160)"
    fi
  fi
fi

# ─── E2.10 — the Codex-specific launcher entry ──────────────────────────────

beat E2.10-codex-launch
spends cx
target_live "$CHAT"
if requires E2.01-open-seat; then
  bad=""
  # The pane pfm started runs the Codex binary — the launcher's product.
  sock="$(live_field "$CHAT" 11)"
  cmd="$(tmux -S "$sock" list-panes -F '#{pane_current_command}' 2>&1 | head -1)"
  case "$cmd" in
    *codex*|node) ;;
    *) bad="$bad the live pane on $sock runs '$(one_line "$cmd")', not the Codex binary;" ;;
  esac
  # `pfm internal codex-launch BINARY [args…]` (internal/hookentry/codex_launch.go):
  # resolves BINARY and execs it in place; usage on no args, a named refusal
  # for an unresolvable binary, and the real exec proven with --version.
  usage="$(pfm internal codex-launch 2>&1)"
  usage_rc=$?
  [ "$usage_rc" -eq 2 ] || bad="$bad codex-launch with no BINARY exited $usage_rc (want 2, usage): $(one_line "$usage");"
  printf '%s' "$usage" | grep -qF 'usage: pfm internal codex-launch BINARY' || bad="$bad codex-launch with no BINARY printed no usage line: $(one_line "$usage");"
  expect-log 'resolve Codex launcher'
  missing="$(pfm internal codex-launch lane-no-such-binary 2>&1)"
  missing_rc=$?
  [ "$missing_rc" -eq 1 ] || bad="$bad codex-launch of an unresolvable binary exited $missing_rc (want 1): $(one_line "$missing");"
  printf '%s' "$missing" | grep -qF 'resolve Codex launcher' || bad="$bad the unresolvable-binary refusal did not say 'resolve Codex launcher': $(one_line "$missing");"
  version="$(timeout 30 pfm internal codex-launch codex --version 2>&1)"
  version_rc=$?
  if [ "$version_rc" -ne 0 ]; then
    bad="$bad codex-launch codex --version exited $version_rc: $(one_line "$version");"
  elif ! printf '%s' "$version" | grep -qE '[0-9]+\.[0-9]+'; then
    bad="$bad codex-launch codex --version exited 0 but printed no version: $(one_line "$version");"
  fi
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "live pane runs '$cmd'; codex-launch: usage on no BINARY (2), 'resolve Codex launcher' on an unknown one (1), exec'd codex --version → $(one_line "$version")"
  fi
fi

# ─── E2.11 — doctor's codex_pane rows, clean while the chat lives ───────────

beat E2.11-doctor-codex-pane
spends cx
target_live "$CHAT"
if requires E2.01-open-seat; then
  bad=""
  report="$(pfm doctor --skip-harvest 2>&1)"
  doctor_rc=$?
  bindings="$(printf '%s\n' "$report" | grep -E '^doctor: codex_pane_bindings total=' | tail -1)"
  panes="$(printf '%s\n' "$report" | grep -E '^doctor: codex_panes live=' | tail -1)"
  warnings="$(printf '%s\n' "$report" | grep -E '^doctor: warning codex_pane' || true)"
  if [ "$doctor_rc" -eq 3 ]; then
    bad="$bad pfm doctor exited 3 (failures) — the codex_pane rows are read off a machine doctor calls unhealthy: $(one_line "$(printf '%s\n' "$report" | grep -iE 'unhealthy|failure' | head -2)");"
  fi
  if [ -z "$bindings" ]; then
    bad="$bad doctor printed no 'codex_pane_bindings total=…' row (unreadable or absent: $(one_line "$(printf '%s\n' "$report" | grep -F codex_pane_bindings | head -1)"));"
  else
    printf '%s' "$bindings" | grep -q ' contested=0 ' || bad="$bad contested bindings: $bindings;"
    printf '%s' "$bindings" | grep -q ' retired=0 ' || bad="$bad retired-thread bindings: $bindings;"
    printf '%s' "$bindings" | grep -q ' undecodable=0$' || bad="$bad undecodable bindings: $bindings;"
  fi
  if [ -z "$panes" ]; then
    bad="$bad doctor printed no 'codex_panes live=N unfollowable=N' row ($(one_line "$(printf '%s\n' "$report" | grep -F codex_panes | head -1)"));"
  else
    live_n="$(printf '%s' "$panes" | sed -n 's/.*live=\([0-9]*\).*/\1/p')"
    [ "${live_n:-0}" -ge 1 ] || bad="$bad doctor sees $live_n live Codex panes while $CHAT is live: $panes;"
    printf '%s' "$panes" | grep -q ' unfollowable=0$' || bad="$bad unfollowable Codex panes: $panes;"
  fi
  [ -z "$warnings" ] || bad="$bad codex_pane warnings: $(one_line "$warnings");"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "$bindings · $panes · no codex_pane warning (doctor exit $doctor_rc)"
  fi
fi

lane_end

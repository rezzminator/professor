#!/usr/bin/env bash
# M.sh — lane M, MCP: registration per engine from the files the installer
# wrote, the loopback daemon (port, health, the exit-75 restart on a replaced
# binary), both stdio transports, the `pfm mcp` CLI, then every tool of both
# servers driven by a DIRECT MCP client (JSON-RPC over stdio and over the
# daemon's streamable HTTP) against real targets — this lane's own live chat
# and a small public document — and last one chat-driven call per server.
# Runs INSIDE a lane container (run.sh), never on a host.
#
#   run.sh --lanes M             solo, from a fresh root
#   run.sh                       in the sequence, after F
#
# Every beat asserts from pfm's OWN report (`pfm ls --tsv`, a verb's exit code,
# the daemon's /status document, the JSON-RPC frame a server answered, a file
# pfm wrote) or from the pane, never from a model's prose: where a chat is the
# STIMULUS (M.14, M.15) the evidence is the fleet's own record of what the tool
# did (a rename in `pfm ls --tsv`, a document in the harvester's cache), and the
# model's word is only the needle a wait ends on. Beat ids and their landscape
# ids are the contract in beats.md and map.tsv — check-map.sh fails when this
# file and those disagree.
#
# Cost: one Claude seat for the lane's own chat plus one `chat_new` spawn and
# one `chat_open`, one Codex home for the HTTP wiring proof, and — cross-lane —
# E1's chat and E2's chat, opened here when the sequence did not leave them
# alive. Roughly 8 short model turns; the rest is direct JSON-RPC.
#
# BROKEN STATE: the prelude aborts the lane by name when the seat it was told
# to spend is not configured, the daemon never answers, or the lane's own chat
# cannot be opened; a cross-lane chat that could not be made leaves its beat
# `blocked` by name, never ✗ and never a pass on absence. Every ✗ carries the
# exit code or HTTP status and the first line of what came back, and where two
# things could have gone wrong (dead daemon vs empty list, transport refused vs
# tool refused) the message says which.
set -uo pipefail
LANES_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=lib.sh
. "$LANES_DIR/lib.sh"
lane_preamble

CHAT="${M_CHAT:-M_MAIN}"
NEW_CHAT="${CHAT}_NEW"
E1_CHAT="${E1_CHAT:-E1_MAIN}"
E2_CHAT="${E2_CHAT:-E2_MAIN}"
CWD="${M_CWD:-/work/orbit}"
CFG_DIR="$HOME/.config/pfm"
CONFIG="$CFG_DIR/pfm.config.json"
HARVESTER_CFG="$CFG_DIR/harvester.config.json"
MANAGED="$HOME/.local/share/pfm/install"
BLUEPRINT="$HOME/.professor"
PFM_BIN="$HOME/.local/bin/pfm"
lane_seat_and_port "$CONFIG"
MCP_PROTO=2025-06-18
INIT_FRAME='{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"lane-M","version":"0"}}}'
INITIALIZED_FRAME='{"jsonrpc":"2.0","method":"notifications/initialized"}'
# Small, stable public documents: one per chat-driven fetch so the cache can
# prove WHICH chat's call landed. The query string is ignored by the server and
# makes each run's URL its own cache key, so a re-run never reads a stale hit.
RFC_BASE="https://www.rfc-editor.org/rfc"
RFC_DIRECT="$RFC_BASE/rfc2324.txt"
RFC_CLAUDE="$RFC_BASE/rfc7168.txt?lane=M-$LANE_STAMP"
RFC_CODEX="$RFC_BASE/rfc1149.txt?lane=M-$LANE_STAMP"
RFC_E1="$RFC_BASE/rfc2549.txt?lane=M-$LANE_STAMP"
RFC_E2="$RFC_BASE/rfc6214.txt?lane=M-$LANE_STAMP"
SCRATCH="$(mktemp -d "${TMPDIR:-/tmp}/lane-m.XXXXXX")"
HARVEST_CACHE="$HOME/.cache/lane-m-harvester/$LANE_STAMP"

lane_begin M

# ── prelude: what this lane needs, made when it is missing, no-op otherwise ──
lane_require_seat "$CONFIG"
SEAT_DIR="$(jq -r --argjson want "$SEAT" '.accounts[] | select(.id == $want) | .configDir' "$CONFIG")"
case "$SEAT_DIR" in "~"*) SEAT_DIR="$HOME${SEAT_DIR#\~}" ;; esac
SPARE="$(jq -r --argjson want "$SEAT" '[.accounts[].id | select(. != $want)] | first // empty' "$CONFIG")"
CODEX_HOME="$(jq -r '.codex.homes[0].home // "~/.codex"' "$CONFIG")"
case "$CODEX_HOME" in "~"*) CODEX_HOME="$HOME${CODEX_HOME#\~}" ;; esac
for tool in curl jq tmux; do
  command -v "$tool" >/dev/null 2>&1 || lane_abort "TOOLCHAIN-MISSING — $tool (the direct MCP client cannot run)"
done

need "the working directory $CWD" "[ -d '$CWD/.git' ]" \
  "mkdir -p '$CWD' && git -C '$CWD' init -q && git -C '$CWD' commit -q --allow-empty -m lane" ||
  lane_abort "no working directory for the chats to live in ($CWD)"
need "the blueprint clone at $BLUEPRINT" "[ -e '$BLUEPRINT' ]" "ln -s /worktree '$BLUEPRINT'" ||
  lane_abort "no blueprint clone — pfm install cannot be re-run from it (M.01, M.02)"
# The harvester's default cache is <home>/.professor/.cache, and here
# <home>/.professor is the blueprint clone, mounted read-only: every stored
# result would fail with "read-only file system". The lane configures its own
# writable cache.dir under $HOME BEFORE the daemon starts, so the HTTP daemon
# (M.11, M.12, M.14) and every server started later read the same directory.
need "a writable harvester cache.dir $HARVEST_CACHE in $HARVESTER_CFG" \
  "[ \"\$(jq -r '.cache.dir // empty' '$HARVESTER_CFG' 2>/dev/null)\" = '$HARVEST_CACHE' ] && [ -d '$HARVEST_CACHE' ] && [ -w '$HARVEST_CACHE' ]" \
  "mkdir -p '$HARVEST_CACHE' && { if [ -f '$HARVESTER_CFG' ]; then jq --arg d '$HARVEST_CACHE' '.cache = ((.cache // {}) + {dir: \$d})' '$HARVESTER_CFG'; else jq -n --arg d '$HARVEST_CACHE' '{cache: {dir: \$d}}'; fi; } >'$SCRATCH/harvester.config.json' && mv -f '$SCRATCH/harvester.config.json' '$HARVESTER_CFG'" ||
  lane_abort "no writable harvester cache — every stored harvester result would fail against the read-only blueprint mount"
need "the pfm MCP daemon on :$PORT" \
  "[ \"\$(curl -s -o /dev/null -w '%{http_code}' -m 2 http://127.0.0.1:$PORT/mcp/chat)\" != 000 ]" \
  "bash /worktree/infra/demo/daemon.sh" ||
  lane_abort "the chat MCP daemon never answered on :$PORT — nothing in this lane can be driven"

# open_main — the lane's own chat, opened the one way (the library's single
# re-open spends the same command after it dies under a later beat).
open_main() {
  pfm chat new --name "$CHAT" --engine cc --account "$SEAT" --cwd "$CWD" --await --timeout 300 \
    "You are $CHAT, the chat an automated Tier B lane drives. Reply with one word: ready. Then wait and do exactly what each next message says, nothing more." 2>&1
}
# open_e1 / open_e2 — the cross-lane chats, made with the OTHER lane's own
# `chat new` line (E1.sh open_main; E2 on the Codex home) so M.14/M.15 assert
# against the same shape in solo and sequence mode.
open_e1() {
  pfm chat new --name "$E1_CHAT" --engine cc --account "$SEAT" --cwd "$CWD" --await --timeout 300 \
    "You are $E1_CHAT, the chat an automated Tier B lane drives. Reply with one word: ready. Then wait and do exactly what each next message says, nothing more." 2>&1
}
open_e2() {
  pfm chat new --name "$E2_CHAT" --engine cx --cwd "$CWD" --await --timeout 300 \
    "You are $E2_CHAT, the chat an automated Tier B lane drives. Reply with one word: ready. Then wait and do exactly what each next message says, nothing more." 2>&1
}
lane_reopen 'open_main'

need "the lane's own live chat $CHAT on cc:$SEAT" "live_chat '$CHAT'" 'open_main' ||
  lane_abort "the lane's own chat $CHAT could not be opened on seat cc:$SEAT"
# Cross-lane chats are opened BEFORE the daemon restart (M.05) on purpose: M.15
# asserts that chats alive across the restart make their next MCP call. A make
# failure is remembered and reported by the beat that needed it, never hidden.
E1_WHY="" E2_WHY=""
need "E1's live chat $E1_CHAT (cross-lane, Claude stdio)" "live_chat '$E1_CHAT'" 'open_e1' ||
  E1_WHY="$E1_CHAT could not be opened (need reported UNMET above)"
need "E2's live chat $E2_CHAT (cross-lane, Codex HTTP)" "live_chat '$E2_CHAT'" 'open_e2' ||
  E2_WHY="$E2_CHAT could not be opened on the Codex home (need reported UNMET above)"

# ─── the direct MCP client ──────────────────────────────────────────────────
# Two transports, one contract: every call sets MCP_OUT (the id-3 JSON-RPC
# frame), and returns 1 with MCP_WHY when the TRANSPORT failed — a dead daemon
# (curl exit 7), a non-200 status, a server that never answered — so a caller
# can never mistake "the daemon is gone" for "the tool returned nothing".

MCP_OUT="" MCP_WHY="" MCP_HTTP_CODE="" MCP_SESSION="" MCP_INIT="" MCP_FRAMES="" MCP_ERR="" MCP_RC=0
MCP_ISERR="" MCP_TEXT="" MCP_STRUCT="" MCP_RPCERR=""

# mcp_http <server> <method> <params-json> [port] — a fresh streamable-HTTP
# session per call: initialize (Mcp-Session-Id comes back in a header), the
# initialized notification, the call, then DELETE the session.
mcp_http() {
  local server="$1" method="$2" params="$3" port="${4:-$PORT}" url hdr resp rc frame
  MCP_OUT="" MCP_WHY="" MCP_HTTP_CODE="" MCP_SESSION="" MCP_INIT=""
  url="http://127.0.0.1:$port/mcp/$server"
  hdr="$SCRATCH/http-hdr"
  resp="$(curl -s -m 30 -D "$hdr" -w '\n%{http_code}' -X POST "$url" \
    -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
    --data "$INIT_FRAME" 2>/dev/null)"
  rc=$?
  if [ "$rc" -ne 0 ]; then
    MCP_WHY="initialize POST $url: curl exited $rc (7 = connection refused: no daemon answers on :$port)"
    return 1
  fi
  MCP_HTTP_CODE="${resp##*$'\n'}"
  MCP_INIT="${resp%$'\n'*}"
  if [ "$MCP_HTTP_CODE" != 200 ]; then
    MCP_WHY="initialize POST $url answered HTTP $MCP_HTTP_CODE: $(one_line "$MCP_INIT")"
    return 1
  fi
  MCP_SESSION="$(tr -d '\r' <"$hdr" | awk 'tolower($1) == "mcp-session-id:" { print $2; exit }')"
  if [ -z "$MCP_SESSION" ]; then
    MCP_WHY="initialize on $url answered 200 with no Mcp-Session-Id header (stateful streamable HTTP promises one)"
    return 1
  fi
  curl -s -m 30 -o /dev/null -X POST "$url" \
    -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
    -H "Mcp-Session-Id: $MCP_SESSION" -H "Mcp-Protocol-Version: $MCP_PROTO" \
    --data "$INITIALIZED_FRAME" 2>/dev/null
  frame="$(jq -cn --arg m "$method" --argjson p "$params" '{jsonrpc: "2.0", id: 3, method: $m, params: $p}')"
  resp="$(curl -s -m "${MCP_HTTP_TIMEOUT:-120}" -w '\n%{http_code}' -X POST "$url" \
    -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
    -H "Mcp-Session-Id: $MCP_SESSION" -H "Mcp-Protocol-Version: $MCP_PROTO" \
    --data "$frame" 2>/dev/null)"
  rc=$?
  curl -s -m 10 -o /dev/null -X DELETE "$url" \
    -H "Mcp-Session-Id: $MCP_SESSION" -H "Mcp-Protocol-Version: $MCP_PROTO" 2>/dev/null
  if [ "$rc" -ne 0 ]; then
    MCP_WHY="$method POST $url: curl exited $rc after ${MCP_HTTP_TIMEOUT:-120}s (28 = the call never completed)"
    return 1
  fi
  MCP_HTTP_CODE="${resp##*$'\n'}"
  MCP_OUT="${resp%$'\n'*}"
  if [ "$MCP_HTTP_CODE" != 200 ]; then
    MCP_WHY="$method POST $url answered HTTP $MCP_HTTP_CODE: $(one_line "$MCP_OUT")"
    return 1
  fi
  if ! printf '%s' "$MCP_OUT" | jq -e '.id == 3' >/dev/null 2>&1; then
    MCP_WHY="$method on $url answered 200 with no id-3 JSON-RPC frame: $(one_line "$MCP_OUT")"
    return 1
  fi
  return 0
}

# mcp_stdio <chat|harvester|bare> <method> <params-json> [timeout-s] — one
# server process over a FIFO held open until the id-3 answer lands (the
# check-map.sh mechanism, without its fixed sleep: on EOF the server closes at
# once and an answer still in flight is lost, which reads like a server with
# no tools). $MCP_PREFRAME, when set, is written BEFORE the handshake — the
# malformed-frame probe. MCP_FRAMES keeps every frame the server wrote.
mcp_stdio() {
  local server="$1" method="$2" params="$3" limit="${4:-60}" dir fifo out err pid t0
  MCP_OUT="" MCP_WHY="" MCP_FRAMES="" MCP_ERR="" MCP_RC=0
  dir="$(mktemp -d "$SCRATCH/stdio.XXXXXX")"
  fifo="$dir/in" out="$dir/out" err="$dir/err"
  mkfifo "$fifo"
  case "$server" in
    chat) pfm mcp chat serve <"$fifo" >"$out" 2>"$err" & ;;
    harvester) pfm mcp harvester serve --transport stdio <"$fifo" >"$out" 2>"$err" & ;;
    bare) pfm mcp <"$fifo" >"$out" 2>"$err" & ;;
    *) MCP_WHY="mcp_stdio: unknown server '$server'"; return 1 ;;
  esac
  pid=$!
  # A server that exits before reading (disabled by config, a usage error)
  # leaves the FIFO with no reader: the write must return EPIPE to this shell,
  # never kill it — SIGPIPE is ignored only around the writes.
  trap '' PIPE
  exec 3>"$fifo"
  [ -n "${MCP_PREFRAME:-}" ] && printf '%s\n' "$MCP_PREFRAME" >&3 2>/dev/null
  printf '%s\n' "$INIT_FRAME" "$INITIALIZED_FRAME" >&3 2>/dev/null
  jq -cn --arg m "$method" --argjson p "$params" '{jsonrpc: "2.0", id: 3, method: $m, params: $p}' >&3 2>/dev/null
  trap - PIPE
  t0="$(date +%s)"
  while ! grep -q '"id":3' "$out" 2>/dev/null; do
    [ $(( $(date +%s) - t0 )) -ge "$limit" ] && break
    kill -0 "$pid" 2>/dev/null || break
    sleep 1
  done
  exec 3>&-
  wait "$pid" 2>/dev/null
  MCP_RC=$?
  MCP_ERR="$(cat "$err" 2>/dev/null)"
  MCP_FRAMES="$(cat "$out" 2>/dev/null)"
  MCP_OUT="$(jq -c 'select(.id == 3)' <"$out" 2>/dev/null | head -1)"
  rm -rf "$dir"
  if [ -z "$MCP_OUT" ]; then
    MCP_WHY="$server stdio server answered no id-3 frame to $method in ${limit}s (exit $MCP_RC; stderr: $(one_line "$MCP_ERR"); frames: $(one_line "$MCP_FRAMES"))"
    return 1
  fi
  return 0
}

# mcp_parse — the tool-call frame's four faces: a JSON-RPC error (an unknown
# tool, bad params), isError (the tool refused), the text content, and the
# structured output typed handlers marshal.
mcp_parse() {
  MCP_RPCERR="$(printf '%s' "$MCP_OUT" | jq -r '.error.message // empty' 2>/dev/null)"
  MCP_ISERR="$(printf '%s' "$MCP_OUT" | jq -r '.result.isError // false' 2>/dev/null)"
  MCP_TEXT="$(printf '%s' "$MCP_OUT" | jq -r '[.result.content[]? | select(.type == "text") | .text] | join("\n")' 2>/dev/null)"
  MCP_STRUCT="$(printf '%s' "$MCP_OUT" | jq -c '.result.structuredContent // empty' 2>/dev/null)"
}

# mcp_call <http|stdio> <server> <tool> <args-json> [meta-json] — tools/call,
# parsed. Returns 1 only on a TRANSPORT failure (MCP_WHY says which).
mcp_call() {
  local transport="$1" server="$2" tool="$3" args="$4" meta="${5:-}" params
  params="$(jq -cn --arg n "$tool" --argjson a "$args" '{name: $n, arguments: $a}')"
  if [ -n "$meta" ]; then
    params="$(jq -cn --argjson p "$params" --argjson m "$meta" '$p + {_meta: $m}')"
  fi
  case "$transport" in
    http) mcp_http "$server" tools/call "$params" || return 1 ;;
    stdio) mcp_stdio "$server" tools/call "$params" "${MCP_STDIO_TIMEOUT:-60}" || return 1 ;;
  esac
  mcp_parse
  return 0
}

# mcp_tools <http|stdio> <server> — the served tool names, sorted, one per
# line in MCP_TOOLS (a variable, not stdout: a caller that captured stdout in a
# subshell would lose MCP_WHY with it).
MCP_TOOLS=""
mcp_tools() {
  MCP_TOOLS=""
  case "$1" in
    http) mcp_http "$2" tools/list '{}' || return 1 ;;
    stdio) mcp_stdio "$2" tools/list '{}' 30 || return 1 ;;
  esac
  MCP_TOOLS="$(printf '%s' "$MCP_OUT" | jq -r '.result.tools[]?.name' | sort)"
}

sfield() { printf '%s' "$MCP_STRUCT" | jq -r "$1 // empty" 2>/dev/null; } # a structured-output field
text_line() { printf '%s\n' "$MCP_TEXT" | sed -n "${1}p"; }              # one line of the text content

# ─── daemon helpers ─────────────────────────────────────────────────────────

daemon_status() { curl -s -m 5 "http://127.0.0.1:$PORT/status" 2>/dev/null; }
daemon_up() { [ "$(curl -s -o /dev/null -w '%{http_code}' -m 2 "http://127.0.0.1:$PORT/mcp/chat" 2>/dev/null)" != 000 ]; }
daemon_down() { ! daemon_up; }
# daemon_restart — what the service manager would do and this container has
# none to do it: stop the daemon on :$PORT, bring it back through the fence's
# own starter. Prints nothing; returns 1 with DAEMON_WHY.
DAEMON_WHY=""
daemon_restart() {
  local pid out
  DAEMON_WHY=""
  pid="$(daemon_status | jq -r '.pid // empty' 2>/dev/null)"
  if [ -n "$pid" ]; then
    kill "$pid" 2>/dev/null
    wait_for 30 daemon_down || { DAEMON_WHY="the daemon (pid $pid) still answers on :$PORT 30s after SIGTERM"; return 1; }
  fi
  out="$(bash /worktree/infra/demo/daemon.sh 2>&1)" || { DAEMON_WHY="daemon.sh could not bring the daemon back: $(one_line "$out")"; return 1; }
  wait_for 20 daemon_up || { DAEMON_WHY="daemon.sh exited 0 but :$PORT never answered"; return 1; }
  return 0
}

# tmp_config <name> <pfm-jq-filter> [harvester-jq-filter|ABSENT] — a copy of
# the machine config (and its harvester file) under $SCRATCH/<name>, edited,
# for `pfm --config` probes that must never touch the real files. Prints the
# pfm config path.
tmp_config() {
  local dir="$SCRATCH/cfg-$1"
  mkdir -p "$dir"
  jq "$2" "$CONFIG" >"$dir/pfm.config.json" || return 1
  if [ "${3:-}" != ABSENT ]; then
    if [ -f "$HARVESTER_CFG" ]; then
      jq "${3:-.}" "$HARVESTER_CFG" >"$dir/harvester.config.json" || return 1
    elif [ -n "${3:-}" ]; then
      jq -n "${3}" >"$dir/harvester.config.json" || return 1
    fi
  fi
  printf '%s' "$dir/pfm.config.json"
}

install_again() { (cd "$BLUEPRINT" && pfm install --yes 2>&1); }
INSTALL_OUT=""

# ─── M.01 — Claude registration: chat stdio + harvester HTTP, per account ───

beat M.01-register-claude M30 M31 M32 M33
spends none
bad=""
# The registry pfm itself names for this seat (doctor's row), never a guessed path.
doctor_out="$(pfm doctor 2>&1)"
REGISTRY="$(printf '%s\n' "$doctor_out" | awk -v want="account $SEAT " \
  '/^doctor: mcp client=claude registry=/ && index($0, want) { sub(/^doctor: mcp client=claude registry=/, ""); sub(/ \(.*$/, ""); print; exit }')"
if [ -z "$REGISTRY" ]; then
  bad="$bad pfm doctor prints no 'mcp client=claude registry=… (account $SEAT …)' row — the seat's registry cannot be located from pfm's own report; rows seen: $(one_line "$(printf '%s\n' "$doctor_out" | grep -F 'client=claude' || echo none)");"
  REGISTRY="$SEAT_DIR/.claude.json"
fi
if [ ! -f "$REGISTRY" ]; then
  bad="$bad the registry $REGISTRY does not exist;"
else
  jq -e --arg bin "$PFM_BIN" '.mcpServers.chat | .type == "stdio" and .command == $bin and .args == ["mcp", "chat", "serve"]' "$REGISTRY" >/dev/null 2>&1 ||
    bad="$bad M31: $REGISTRY mcpServers.chat is not the stdio shape {type stdio, command $PFM_BIN, args [mcp chat serve]}: $(one_line "$(jq -c '.mcpServers.chat' "$REGISTRY" 2>&1)");"
  jq -e --arg url "http://127.0.0.1:$PORT/mcp/harvester" '.mcpServers.harvester | .type == "http" and .url == $url' "$REGISTRY" >/dev/null 2>&1 ||
    bad="$bad M32: $REGISTRY mcpServers.harvester is not the HTTP shape {type http, url http://127.0.0.1:$PORT/mcp/harvester}: $(one_line "$(jq -c '.mcpServers.harvester' "$REGISTRY" 2>&1)");"
fi
# M30: with no mcp.servers key and no harvester file, BOTH servers read
# disabled from the default layer — a probe over a config copy, never the real one.
if defaults_cfg="$(tmp_config defaults 'del(.mcp.servers)' ABSENT)"; then
  ls_default="$(pfm --config "$defaults_cfg" mcp ls 2>&1)"
  ls_rc=$?
  [ "$ls_rc" -eq 0 ] || bad="$bad M30: pfm --config <copy without mcp.servers> mcp ls exited $ls_rc: $(one_line "$ls_default");"
  printf '%s\n' "$ls_default" | grep -qE '^chat	false	' || bad="$bad M30: chat is not default-disabled: $(one_line "$ls_default");"
  printf '%s\n' "$ls_default" | grep -qE '^harvester	false	' || bad="$bad M30: harvester is not default-disabled: $(one_line "$ls_default");"
else
  bad="$bad M30: could not write the config copy for the default-layer probe;"
fi
# M33: a re-install keeps the registration byte-for-byte (isPFMStdioClient /
# isPFMHTTPClient recognise their own shapes) and the ownership ledger names it.
before="$(jq -S '.mcpServers' "$REGISTRY" 2>/dev/null)"
INSTALL_OUT="$(install_again)"
install_rc=$?
after="$(jq -S '.mcpServers' "$REGISTRY" 2>/dev/null)"
[ "$install_rc" -eq 0 ] || bad="$bad M33: pfm install --yes exited $install_rc: $(one_line "$(printf '%s\n' "$INSTALL_OUT" | tail -3)");"
[ "$before" = "$after" ] || bad="$bad M33: the re-install changed $REGISTRY mcpServers (before: $(one_line "$before") after: $(one_line "$after"));"
if [ -f "$MANAGED/mcp-ownership.json" ]; then
  grep -qF "$(basename "$REGISTRY")" "$MANAGED/mcp-ownership.json" || bad="$bad M33: $MANAGED/mcp-ownership.json records no registration for $REGISTRY;"
else
  bad="$bad M33: no ownership ledger at $MANAGED/mcp-ownership.json;"
fi
if [ -n "$bad" ]; then fail "$bad"; else
  pass "$REGISTRY: chat stdio ($PFM_BIN mcp chat serve) + harvester http (:$PORT/mcp/harvester); default layer reads both disabled; re-install (exit 0) kept the shape, ownership ledger names the registry"
fi

# ─── M.02 — Codex registration: HTTP-only, fenced, foreign entry preserved ──

beat M.02-register-codex M34 M35
spends none
bad=""
TOML="$CODEX_HOME/config.toml"
FENCE_BEGIN='# BEGIN pfm mcp_servers — installer-owned'
FENCE_END='# END pfm mcp_servers — installer-owned'
fence_of() { awk -v b="$FENCE_BEGIN" -v e="$FENCE_END" '$0 == b { f = 1; next } $0 == e { f = 0 } f' "$1" 2>/dev/null; }
if [ ! -f "$TOML" ]; then
  fail "no Codex config at $TOML (Codex home $CODEX_HOME from $CONFIG) — nothing to inspect"
else
  fence="$(fence_of "$TOML")"
  [ -n "$fence" ] || bad="$bad M34: $TOML carries no installer fence ($FENCE_BEGIN … $FENCE_END);"
  printf '%s\n' "$fence" | grep -qxF '[mcp_servers.chat]' || bad="$bad M34: the fence has no [mcp_servers.chat] table;"
  printf '%s\n' "$fence" | grep -qxF "url = \"http://127.0.0.1:$PORT/mcp/chat\"" || bad="$bad M34: the fence does not point chat at http://127.0.0.1:$PORT/mcp/chat;"
  printf '%s\n' "$fence" | grep -qxF '[mcp_servers.harvester]' || bad="$bad M34: the fence has no [mcp_servers.harvester] table;"
  printf '%s\n' "$fence" | grep -qxF "url = \"http://127.0.0.1:$PORT/mcp/harvester\"" || bad="$bad M34: the fence does not point harvester at http://127.0.0.1:$PORT/mcp/harvester;"
  printf '%s\n' "$fence" | grep -qE '^(command|args) *=' && bad="$bad M34: the Codex fence carries a stdio command/args line — Codex wiring is HTTP-only: $(one_line "$fence");"
  # M35: a foreign [mcp_servers.harvester] OUTSIDE the fence survives a
  # re-install untouched, and the fence yields the name to it by name.
  cp "$TOML" "$SCRATCH/config.toml.orig"
  printf '\n[mcp_servers.harvester]\nurl = "http://127.0.0.1:1/lane-m-foreign"\n' >>"$TOML"
  foreign_out="$(install_again)"
  foreign_rc=$?
  fence_after="$(fence_of "$TOML")"
  [ "$foreign_rc" -eq 0 ] || bad="$bad M35: pfm install --yes with a foreign harvester entry exited $foreign_rc: $(one_line "$(printf '%s\n' "$foreign_out" | tail -3)");"
  printf '%s\n' "$foreign_out" | grep -qF "preserve conflicting manual MCP client harvester" ||
    bad="$bad M35: the install did not name the preserved conflict ('preserve conflicting manual MCP client harvester'): $(one_line "$(printf '%s\n' "$foreign_out" | grep -i harvester | head -3)");"
  grep -qxF 'url = "http://127.0.0.1:1/lane-m-foreign"' "$TOML" || bad="$bad M35: the foreign url line was rewritten or dropped;"
  printf '%s\n' "$fence_after" | grep -qxF '[mcp_servers.harvester]' && bad="$bad M35: the fence STILL declares [mcp_servers.harvester] beside the foreign one (a duplicate table Codex cannot parse);"
  printf '%s\n' "$fence_after" | grep -qxF '[mcp_servers.chat]' || bad="$bad M35: the fence lost [mcp_servers.chat] while yielding harvester;"
  cp "$SCRATCH/config.toml.orig" "$TOML"
  [ -n "$(fence_of "$TOML" | grep -xF '[mcp_servers.harvester]')" ] || bad="$bad restore: $TOML was not put back (the fence lacks harvester);"
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "$TOML fence wires chat + harvester over HTTP (:$PORT) with no stdio line; a foreign harvester table outside the fence survived a re-install by name and the fence yielded the name; restored"
  fi
fi

# ─── M.03 — OpenCode registration: chat local + harvester remote, doctor row ─
# The shared body (identical to E3.02) lives once in lib.sh's
# assert_opencode_mcp_registered — this lane and E3 must never drift apart on
# what "MCP registered" means.

beat M.03-register-opencode M36
spends none
assert_opencode_mcp_registered "$PFM_BIN" "$PORT"

# ─── M.04 — doctor: registration classes, Codex + project cutover, daemon ───

beat M.04-doctor-mcp M37 M38 M39
spends none
bad=""
doctor_out="$(pfm doctor 2>&1)"
doctor_rc=$?
[ "$doctor_rc" -le 1 ] || bad="$bad pfm doctor exited $doctor_rc, so its mcp rows cannot be trusted: $(one_line "$(printf '%s\n' "$doctor_out" | tail -3)");"
printf '%s\n' "$doctor_out" | grep -qE "^doctor: mcp client=claude registry=.*account $SEAT .*harvester=pfm chat=pfm" ||
  bad="$bad M37: no 'harvester=pfm chat=pfm' row for account $SEAT: $(one_line "$(printf '%s\n' "$doctor_out" | grep -F 'client=claude' || echo none)");"
# M37: every classification, provoked on an AMBIENT registry (CLAUDE_CONFIG_DIR
# adds one row for a directory this lane owns) — never on a live seat's file.
AMB="$SCRATCH/ambient-registry"
mkdir -p "$AMB"
amb_row() { CLAUDE_CONFIG_DIR="$AMB" pfm doctor 2>&1 | grep -F "registry=$AMB/.claude.json" | head -1; }
row="$(amb_row)"
printf '%s' "$row" | grep -q 'harvester=absent chat=absent' || bad="$bad M37 absent: $(one_line "${row:-no ambient registry row at all}");"
printf '%s' "$row" | grep -q 'remediation=' || bad="$bad M37 absent: the absent registry beside a pfm-wired one carries no remediation: $(one_line "$row");"
printf '{"mcpServers":{"chat":{"type":"stdio","command":"other-tool","args":["x"]},"harvester":{"command":"uvx","args":["harvester-mcp"]}}}\n' >"$AMB/.claude.json"
row="$(amb_row)"
printf '%s' "$row" | grep -q 'harvester=legacy-standalone chat=foreign-registration' || bad="$bad M37 legacy-standalone/foreign-registration: $(one_line "${row:-no row}");"
printf '{not json\n' >"$AMB/.claude.json"
row="$(amb_row)"
printf '%s' "$row" | grep -q 'harvester=unreadable chat=unreadable error=' || bad="$bad M37 unreadable: $(one_line "${row:-no row}");"
jq -n --arg bin "$PFM_BIN" --arg url "http://127.0.0.1:$PORT/mcp/harvester" \
  '{mcpServers: {chat: {type: "stdio", command: $bin, args: ["mcp", "chat", "serve"]}, harvester: {type: "http", url: $url}}}' >"$AMB/.claude.json"
row="$(amb_row)"
printf '%s' "$row" | grep -q 'harvester=pfm chat=pfm' || bad="$bad M37 pfm: $(one_line "${row:-no row}");"
printf '%s' "$row" | grep -q 'remediation=' && bad="$bad M37 pfm: a fully pfm-wired registry still carries a remediation: $(one_line "$row");"
rm -rf "$AMB"
# M38: the historical cutover — a Codex harvester entry pfm did not write, and
# a project-scope ~/.mcp.json standalone harvester — each named by client.
cp "$TOML" "$SCRATCH/config.toml.m04"
awk -v b="$FENCE_BEGIN" -v e="$FENCE_END" '
  $0 == b { f = 1 } $0 == e { f = 0 }
  f && ($0 == "[mcp_servers.harvester]" || index($0, "/mcp/harvester\"")) { next }
  { print }' "$SCRATCH/config.toml.m04" >"$TOML"
printf '\n[mcp_servers.harvester]\nurl = "http://127.0.0.1:1/lane-m-foreign"\n' >>"$TOML"
codex_row="$(pfm doctor 2>&1 | grep -F 'client=codex' | head -1)"
cp "$SCRATCH/config.toml.m04" "$TOML"
printf '%s' "$codex_row" | grep -q 'harvester=foreign-registration warning=consumer cutover incomplete' ||
  bad="$bad M38 codex: with a foreign harvester in $TOML doctor printed: $(one_line "${codex_row:-no client=codex row}");"
if [ -e "$HOME/.mcp.json" ]; then
  bad="$bad M38 project-scope: $HOME/.mcp.json already exists — the standalone-harvester probe would clobber it, so it was NOT provoked;"
else
  printf '{"mcpServers":{"harvester":{"command":"uvx","args":["harvester-mcp"]}}}\n' >"$HOME/.mcp.json"
  proj_row="$(pfm doctor 2>&1 | grep -F "path=$HOME/.mcp.json" | head -1)"
  rm -f "$HOME/.mcp.json"
  printf '%s' "$proj_row" | grep -q 'harvester=legacy-standalone' ||
    bad="$bad M38 project-scope: with a uvx harvester in $HOME/.mcp.json doctor printed: $(one_line "${proj_row:-no row naming that path}");"
fi
printf '%s\n' "$doctor_out" | grep -qF 'doctor: mcp client-cutover=complete' ||
  bad="$bad M38: on the clean install doctor does not print 'mcp client-cutover=complete': $(one_line "$(printf '%s\n' "$doctor_out" | grep -F 'client=' | grep -v 'client=claude' || echo none)");"
# M39: the live probe, both faces — running (pid + endpoint) and unreachable
# (the daemon told to stop answering for a moment).
printf '%s\n' "$doctor_out" | grep -qE "^doctor: mcp daemon=running pid=[0-9]+ since=.* endpoint=http://127.0.0.1:$PORT" ||
  bad="$bad M39: doctor does not report 'mcp daemon=running pid=… endpoint=http://127.0.0.1:$PORT': $(one_line "$(printf '%s\n' "$doctor_out" | grep -F 'daemon=' || echo none)");"
printf '%s\n' "$doctor_out" | grep -q 'daemon=version-skew' &&
  bad="$bad M39: doctor reports version-skew on a daemon built from this tree: $(one_line "$(printf '%s\n' "$doctor_out" | grep -F 'version-skew')");"
if skew_cfg="$(tmp_config skewport '.mcp.http.port = 1')"; then
  unreachable="$(pfm --config "$skew_cfg" doctor 2>&1 | grep -F 'mcp daemon=' | head -1)"
  printf '%s' "$unreachable" | grep -q 'daemon=unreachable error=unreachable at http://127.0.0.1:1/status' ||
    bad="$bad M39: with the port pointed at :1 doctor printed '$(one_line "${unreachable:-no daemon row}")' (want daemon=unreachable naming the URL);"
fi
if [ -n "$bad" ]; then fail "$bad (doctor exit $doctor_rc)"; else
  pass "doctor exit $doctor_rc · registry classes absent/legacy-standalone/foreign-registration/unreadable/pfm each named on an ambient registry · codex foreign + project-scope standalone named · daemon=running on :$PORT, =unreachable when pointed at :1"
fi

# ─── M.05 — the daemon: one loopback port, health, exit-75 restart ──────────

beat M.05-daemon-core M40 M41 M42 M43 M44 M45 M46
spends none
bad=""
status0="$(daemon_status)"
pid0="$(printf '%s' "$status0" | jq -r '.pid // empty' 2>/dev/null)"
if [ -z "$pid0" ]; then
  fail "GET /status on :$PORT returned no pid: $(one_line "${status0:-<empty>}") — the daemon the prelude saw is not answering as a daemon"
else
  # M41 — /status document, both routes mounted, the served chat roster = the status roster
  [ "$(printf '%s' "$status0" | jq -r '.endpoint')" = "http://127.0.0.1:$PORT" ] || bad="$bad M41: /status endpoint is '$(printf '%s' "$status0" | jq -r '.endpoint')', not http://127.0.0.1:$PORT;"
  [ "$(printf '%s' "$status0" | jq -r '.protocolVersion')" = "$MCP_PROTO" ] || bad="$bad M41: /status protocolVersion is '$(printf '%s' "$status0" | jq -r '.protocolVersion')', not $MCP_PROTO;"
  status_chat="$(printf '%s' "$status0" | jq -r '.servers.chat[]?' | sort)"
  [ "$(printf '%s\n' "$status_chat" | grep -c .)" -eq 18 ] || bad="$bad M41: /status lists $(printf '%s\n' "$status_chat" | grep -c .) chat tools, not 18;"
  if mcp_tools http chat; then
    served_chat="$MCP_TOOLS"
    [ "$served_chat" = "$status_chat" ] || bad="$bad M41: tools/list over /mcp/chat ($(printf '%s\n' "$served_chat" | grep -c .) tools) differs from /status servers.chat;"
  else
    bad="$bad M41: /mcp/chat tools/list: $MCP_WHY;"
  fi
  if mcp_http harvester tools/list '{}'; then
    [ "$(printf '%s' "$MCP_OUT" | jq -r '.result.tools | length')" -ge 3 ] || bad="$bad M41: /mcp/harvester serves $(printf '%s' "$MCP_OUT" | jq -r '.result.tools | length') tools (want read, download_file, search_literature, and search_web when configured: ≥ 3);"
  else
    bad="$bad M41: /mcp/harvester tools/list: $MCP_WHY;"
  fi
  curl -s -o /dev/null -w '%{http_code}' -m 5 "http://127.0.0.1:$PORT/mcp/nope" 2>/dev/null | grep -q '^404$' || bad="$bad M41: an unknown route did not answer 404;"
  # M41 — loopback only: the container's own non-loopback address must refuse
  ip="$(hostname -I 2>/dev/null | awk '{ print $1 }')"
  [ -n "$ip" ] || ip="$(ip -4 -o addr show scope global 2>/dev/null | awk '{ print $4 }' | cut -d/ -f1 | head -1)"
  if [ -n "$ip" ]; then
    curl -s -o /dev/null -m 3 "http://$ip:$PORT/status" 2>/dev/null
    lb_rc=$?
    [ "$lb_rc" -eq 7 ] || bad="$bad M41: the daemon answered (curl exit $lb_rc, want 7 refused) on the non-loopback address $ip:$PORT — it is not bound to 127.0.0.1 alone;"
    loopback_note="non-loopback $ip:$PORT refused (curl 7)"
  else
    loopback_note="no non-loopback address found (hostname -I / ip addr empty) — the loopback-only bind was NOT probed"
  fi
  # M43 — any Origin header is refused before routing
  origin="$(curl -s -m 5 -w '\n%{http_code}' -H 'Origin: http://evil.example' "http://127.0.0.1:$PORT/status" 2>/dev/null)"
  [ "${origin##*$'\n'}" = 403 ] || bad="$bad M43: GET /status with an Origin header answered HTTP ${origin##*$'\n'} (want 403);"
  printf '%s' "$origin" | grep -q 'browser-origin requests are forbidden' || bad="$bad M43: the Origin refusal does not name browser-origin: $(one_line "$origin");"
  # M40 — the entry point's three refusals, each by name
  dup="$(pfm mcp serve </dev/null 2>&1)"
  dup_rc=$?
  [ "$dup_rc" -eq 1 ] || bad="$bad M40: a second pfm mcp serve exited $dup_rc (want 1): $(one_line "$dup");"
  printf '%s' "$dup" | grep -q "already running (pid $pid0" || bad="$bad M40: the second serve did not name the running pid $pid0: $(one_line "$dup");"
  if port_cfg="$(tmp_config badport '.mcp.http.port = 70000')"; then
    badport="$(pfm --config "$port_cfg" mcp serve </dev/null 2>&1)"
    badport_rc=$?
    [ "$badport_rc" -eq 2 ] && printf '%s' "$badport" | grep -q 'outside 1..65535' ||
      bad="$bad M40: port 70000 exited $badport_rc '$(one_line "$badport")' (want 2 naming 1..65535);"
  fi
  if off_cfg="$(tmp_config alloff '.mcp.servers.chat.enabled = false' '.enabled = false')"; then
    alloff="$(pfm --config "$off_cfg" mcp serve </dev/null 2>&1)"
    alloff_rc=$?
    [ "$alloff_rc" -eq 1 ] && printf '%s' "$alloff" | grep -q 'every registered server is disabled' ||
      bad="$bad M40: with both servers disabled serve exited $alloff_rc '$(one_line "$alloff")' (want 1 naming every server disabled);"
  fi
  # M42 — a disabled route on a SECOND daemon (harvester off, another port): 503 + remedy
  ALT_PORT=$((PORT + 11))
  if alt_cfg="$(tmp_config disabledroute ".mcp.http.port = $ALT_PORT" '.enabled = false')"; then
    pfm --config "$alt_cfg" mcp serve >"$SCRATCH/alt-daemon.log" 2>&1 </dev/null &
    alt_pid=$!
    if wait_for 20 "[ \"\$(curl -s -o /dev/null -w '%{http_code}' -m 2 http://127.0.0.1:$ALT_PORT/status)\" = 200 ]"; then
      disabled="$(curl -s -m 5 -w '\n%{http_code}' -X POST "http://127.0.0.1:$ALT_PORT/mcp/harvester" \
        -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' --data "$INIT_FRAME" 2>/dev/null)"
      [ "${disabled##*$'\n'}" = 503 ] || bad="$bad M42: the disabled harvester route answered HTTP ${disabled##*$'\n'} (want 503, never a bare 404);"
      printf '%s' "$disabled" | grep -q 'harvester is disabled by config; enable it with: pfm mcp harvester enable' ||
        bad="$bad M42: the 503 body carries no named remedy: $(one_line "$disabled");"
      printf '%s' "$(curl -s -m 5 "http://127.0.0.1:$ALT_PORT/status")" | jq -e '.servers | has("harvester") | not' >/dev/null 2>&1 ||
        bad="$bad M42: /status on the harvester-disabled daemon still lists a harvester roster;"
    else
      bad="$bad M42: the second daemon on :$ALT_PORT never answered: $(one_line "$(cat "$SCRATCH/alt-daemon.log")");"
    fi
    kill "$alt_pid" 2>/dev/null
    wait "$alt_pid" 2>/dev/null
  fi
  # M44 — the daemon's chat is HTTP-only and never ambient: whoami cannot name a caller
  if mcp_call http chat chat_whoami '{}'; then
    [ "$(sfield .status)" = not_found ] && printf '%s' "$(sfield .message)" | grep -q 'shared HTTP daemon' ||
      bad="$bad M44: chat_whoami over the daemon answered status '$(sfield .status)' message '$(one_line "$(sfield .message)")' (want not_found naming the shared HTTP daemon);"
  else
    bad="$bad M44: chat_whoami over HTTP: $MCP_WHY;"
  fi
  # M45 — the external gateway state is reported, in one of its named states
  ext="$(printf '%s' "$status0" | jq -r '.harvesterExternal // empty')"
  case "$ext" in
    disabled|listening*|"off:"*|"failed:"*) ;;
    "") bad="$bad M45: /status carries no harvesterExternal field;" ;;
    *) bad="$bad M45: harvesterExternal reads '$ext' — none of disabled / listening… / off: … / failed: …;" ;;
  esac
  # M46 — restart on a replaced binary, observed as the supervisor would see it:
  # this shell owns a daemon, rebuilds pfm under it, and reads its exit status.
  expect-log 'own executable was replaced'
  expect-log 'shutdown for restart'
  expect-log 'connection refused'
  expect-log 'server closed'
  expect-log 'listen loopback'
  if ! command -v go >/dev/null 2>&1; then
    bad="$bad M46: TOOLCHAIN-MISSING — go is not on PATH, the binary cannot be rebuilt in-container;"
  else
    kill "$pid0" 2>/dev/null
    if ! wait_for 30 daemon_down; then
      bad="$bad M46: the daemon (pid $pid0) still answers 30s after SIGTERM — the restart could not be staged;"
    else
      # Dead daemon: the direct client must report an ERROR, never an empty list.
      if mcp_call http chat chat_ls '{}'; then
        bad="$bad M1/M46: with no daemon on :$PORT chat_ls still answered a frame ($(one_line "$MCP_OUT")) — a dead daemon must be an error, never a list;"
      else
        printf '%s' "$MCP_WHY" | grep -q 'curl exited 7' || bad="$bad M46: the dead-daemon call failed for another reason than connection refused: $MCP_WHY;"
      fi
      pfm mcp serve >"$SCRATCH/own-daemon.log" 2>&1 </dev/null &
      own_pid=$!
      if ! wait_for 20 "[ \"\$(daemon_status | jq -r '.pid // empty')\" = '$own_pid' ]"; then
        bad="$bad M46: this shell's own pfm mcp serve (pid $own_pid) never answered /status: $(one_line "$(cat "$SCRATCH/own-daemon.log")");"
      else
        inode_before="$(stat -c %i "$PFM_BIN" 2>/dev/null)"
        build_out="$( (cd /worktree/pfm && timeout 600 env GOFLAGS=-buildvcs=false go build \
          -ldflags "-X main.version=$(cat /worktree/VERSION)" -o "$PFM_BIN" ./cmd/pfm) 2>&1)"
        build_rc=$?
        inode_after="$(stat -c %i "$PFM_BIN" 2>/dev/null)"
        if [ "$build_rc" -ne 0 ]; then
          bad="$bad M46: go build exited $build_rc: $(one_line "$build_out");"
        elif [ "$inode_before" = "$inode_after" ] && [ -z "$(find "$PFM_BIN" -newer "$SCRATCH/own-daemon.log" 2>/dev/null)" ]; then
          bad="$bad M46: go build exited 0 but $PFM_BIN is unchanged (inode $inode_before) — nothing was replaced;"
        else
          exited=0
          for _ in $(seq 1 60); do
            kill -0 "$own_pid" 2>/dev/null || { exited=1; break; }
            sleep 1
          done
          if [ "$exited" -eq 0 ]; then
            bad="$bad M46: the daemon (pid $own_pid) still runs 60s after its binary changed (inode $inode_before → $inode_after) — binwatch did not fire;"
            kill "$own_pid" 2>/dev/null
            wait "$own_pid" 2>/dev/null
          else
            wait "$own_pid" 2>/dev/null
            own_rc=$?
            [ "$own_rc" -eq 75 ] || bad="$bad M46: the replaced daemon exited $own_rc, not 75 (EX_TEMPFAIL, the supervisor's restart signal);"
            grep -qF 'own executable was replaced by a new build' "$SCRATCH/own-daemon.log" ||
              bad="$bad M46: the daemon's stderr does not say why it left: $(one_line "$(tail -3 "$SCRATCH/own-daemon.log")");"
          fi
        fi
      fi
      # Restore — the supervisor's half, done by the fence's own starter.
      if daemon_restart; then
        status1="$(daemon_status)"
        pid1="$(printf '%s' "$status1" | jq -r '.pid // empty')"
        [ -n "$pid1" ] && [ "$pid1" != "$pid0" ] && [ "$pid1" != "${own_pid:-x}" ] || bad="$bad M46: after the restart /status reports pid '${pid1:-<none>}' (old $pid0, own ${own_pid:-<none>});"
        readlink "/proc/$pid1/exe" 2>/dev/null | grep -q ' (deleted)$' && bad="$bad M46: the restarted daemon (pid $pid1) still runs a deleted binary;"
        client_version="$(pfm version 2>/dev/null | awk '{ print $2 }')"
        [ "$(printf '%s' "$status1" | jq -r '.pfmVersion')" = "$client_version" ] ||
          bad="$bad M46: the restarted daemon reports pfmVersion '$(printf '%s' "$status1" | jq -r '.pfmVersion')' while pfm version says '$client_version' (skew);"
      else
        bad="$bad M46 restore: $DAEMON_WHY;"
      fi
    fi
  fi
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "/status pid $pid0 on http://127.0.0.1:$PORT (18 chat tools = tools/list; $loopback_note) · Origin → 403 · serve refuses: running/port/all-disabled by name · disabled route 503 + remedy · whoami not_found (HTTP-only) · external=$ext · rebuilt pfm under the daemon → exit 75 in place, daemon.sh brought pid $pid1 back on the new inode, no version skew"
  fi
fi

# ─── M.06 — service units: systemd staged, the absent manager named ─────────

beat M.06-daemon-units M47 M48
spends none
bad=""
UNIT="$MANAGED/systemd/pfm-mcp.service"
UNIT_LINK="$HOME/.config/systemd/user/pfm-mcp.service"
if [ ! -f "$UNIT" ]; then
  bad="$bad M47: no staged unit at $UNIT;"
else
  grep -qx 'Restart=on-failure' "$UNIT" || bad="$bad M47: $UNIT lacks Restart=on-failure (the exit-75 restart has no supervisor rule);"
  grep -qx 'ExecStart=%h/.local/bin/pfm mcp serve' "$UNIT" || bad="$bad M47: $UNIT ExecStart is not '%h/.local/bin/pfm mcp serve': $(one_line "$(grep ExecStart "$UNIT")");"
  grep -q '__PFM_SERVICE_PATH__' "$UNIT" && bad="$bad M47: $UNIT still carries the unrendered __PFM_SERVICE_PATH__ token;"
fi
[ -L "$UNIT_LINK" ] || [ -f "$UNIT_LINK" ] || bad="$bad M47: the unit is not linked into $UNIT_LINK;"
[ -e "$HOME/.config/systemd/user/default.target.wants/pfm-mcp.service" ] || bad="$bad M47: the unit is not enabled (no default.target.wants/pfm-mcp.service link);"
# The premise, asserted: this container has no user manager.
if systemctl --user show-environment >/dev/null 2>&1; then
  bad="$bad M47: systemctl --user answers in this container — the 'no service manager' premise of this beat does not hold here;"
fi
# The advisory, from pfm's own reports: the installer names the absent manager
# by name when it stages the units (M.01's re-install output) …
if [ -n "$INSTALL_OUT" ]; then
  printf '%s\n' "$INSTALL_OUT" | grep -qF 'systemd --user unavailable; units are staged and enabled for next login but not started now' ||
    bad="$bad M47: pfm install --yes did not name the absent user manager ('systemd --user unavailable; units are staged …'): $(one_line "$(printf '%s\n' "$INSTALL_OUT" | grep -i systemd | head -3 || echo none)");"
else
  bad="$bad M47: no pfm install --yes output to read the installer's advisory from (M.01 did not run it);"
fi
# … and doctor must name it too, never fall silent — a container with no
# systemd/launchd is the state the row exists for.
doctor_units="$(pfm doctor 2>&1)"
printf '%s\n' "$doctor_units" | grep -qiE 'systemd|launchd|service manager' ||
  bad="$bad M47/M48: pfm doctor says nothing about the absent service manager — in a container that row must be a NAMED advisory, not silence ($(printf '%s\n' "$doctor_units" | grep -c .) rows);"
# M48: the launchd plist is never staged on Linux by design (installer
# schedulerAsset) — its absence is the expected state here, and the embedded
# asset pfm ships carries KeepAlive so a darwin host restarts on exit 75.
[ -e "$MANAGED/launchd/com.professor.pfm.mcp.plist" ] && bad="$bad M48: a launchd plist is staged on a Linux host ($MANAGED/launchd/com.professor.pfm.mcp.plist) — schedulerAsset should have skipped it;"
PLIST_SRC=/worktree/pfm/internal/installer/assets/launchd/com.professor.pfm.mcp.plist
if [ -f "$PLIST_SRC" ]; then
  grep -A1 '<key>KeepAlive</key>' "$PLIST_SRC" | grep -q '<true/>' || bad="$bad M48: the embedded plist $PLIST_SRC does not set KeepAlive true;"
else
  bad="$bad M48: the embedded launchd asset $PLIST_SRC is not in the mounted tree — KeepAlive could not be read;"
fi
if [ -n "$bad" ]; then fail "$bad"; else
  pass "$UNIT staged (Restart=on-failure, ExecStart pfm mcp serve), linked + enabled under ~/.config/systemd/user; no user manager here and both the installer and doctor name it; launchd plist not staged on Linux, embedded asset KeepAlive=true"
fi

# ─── M.07 — the stdio transports ────────────────────────────────────────────

beat M.07-stdio-transports M49 M50 M51
spends none
bad=""
# M49 — `pfm mcp chat serve`: the full roster over stdio, and whoami resolving
# AMBIENTLY (this shell is no chat, so not_found — but by the ambient path,
# never the HTTP daemon's refusal).
if mcp_tools stdio chat; then
  stdio_chat="$MCP_TOOLS"
  [ "$(printf '%s\n' "$stdio_chat" | grep -c .)" -eq 18 ] || bad="$bad M49: chat stdio serves $(printf '%s\n' "$stdio_chat" | grep -c .) tools (want 18);"
  printf '%s\n' "$stdio_chat" | grep -qx chat_whoami || bad="$bad M49: chat stdio does not serve chat_whoami;"
else
  bad="$bad M49: chat stdio tools/list: $MCP_WHY;"
fi
if mcp_call stdio chat chat_whoami '{}'; then
  who_status="$(sfield .status)"
  who_msg="$(sfield .message)"
  case "$who_status" in
    not_found)
      printf '%s' "$who_msg" | grep -q 'shared HTTP daemon' && bad="$bad M49: the stdio server refused with the HTTP daemon's remedy — it did not take the ambient path: $(one_line "$who_msg");"
      [ -n "$who_msg" ] || bad="$bad M49: stdio whoami not_found carries no message naming why;"
      ;;
    ok)
      # Only an AMBIENT source can answer ok here (this shell is no chat, but a
      # by-hand run from inside one is); the daemon's own source never can.
      [ -n "$(sfield .source)" ] && [ "$(sfield .source)" != mcp-thread-meta ] ||
        bad="$bad M49: stdio whoami answered ok from source '$(sfield .source)' — not an ambient source;"
      who_msg="ok via ambient source $(sfield .source)"
      ;;
    *) bad="$bad M49: chat_whoami over stdio answered status '$who_status' (want not_found by the ambient path, or ok from an ambient source);" ;;
  esac
else
  bad="$bad M49: chat_whoami over stdio: $MCP_WHY;"
fi
# M50 — `pfm mcp harvester serve [--transport stdio]`, and every retired flag by name
if mcp_tools stdio harvester; then
  stdio_harv="$MCP_TOOLS"
  for t in read download_file search_literature; do
    printf '%s\n' "$stdio_harv" | grep -qx "$t" || bad="$bad M50: harvester stdio does not serve $t;"
  done
  extra="$(printf '%s\n' "$stdio_harv" | grep -vxE 'read|download_file|search_literature|search_web' | tr '\n' ' ')"
  [ -z "${extra// /}" ] || bad="$bad M50: harvester stdio serves tools outside the four: $extra;"
else
  bad="$bad M50: harvester stdio tools/list: $MCP_WHY;"
fi
retired="$(pfm mcp harvester serve --transport http </dev/null 2>&1)"
retired_rc=$?
[ "$retired_rc" -eq 2 ] && printf '%s' "$retired" | grep -q 'retired' || bad="$bad M50: --transport http exited $retired_rc '$(one_line "$retired")' (want 2 naming it retired);"
retired="$(pfm mcp harvester serve --port 1 </dev/null 2>&1)"
retired_rc=$?
[ "$retired_rc" -eq 2 ] && printf '%s' "$retired" | grep -q -- '--port is retired; external.port in harvester.config.json' ||
  bad="$bad M50: --port exited $retired_rc '$(one_line "$retired")' (want 2 naming the harvester.config.json key);"
retired="$(pfm mcp harvester serve --ignore-robots-txt </dev/null 2>&1)"
retired_rc=$?
[ "$retired_rc" -eq 2 ] && printf '%s' "$retired" | grep -q -- '--ignore-robots-txt is retired' || bad="$bad M50: --ignore-robots-txt exited $retired_rc '$(one_line "$retired")' (want 2 naming it);"
# M51 — a malformed frame is answered -32700 and the connection lives on
if MCP_PREFRAME='{"jsonrpc":"2.0","id":9,"method":' mcp_stdio chat tools/list '{}' 30; then
  printf '%s\n' "$MCP_FRAMES" | grep -q '"code":-32700' || bad="$bad M51: no -32700 parse-error frame after a malformed line: $(one_line "$MCP_FRAMES");"
  [ "$(printf '%s' "$MCP_OUT" | jq -r '.result.tools | length')" -eq 18 ] || bad="$bad M51: after the malformed frame tools/list did not answer the full roster;"
else
  bad="$bad M51: the connection did not survive a malformed frame: $MCP_WHY;"
fi
if [ -n "$bad" ]; then fail "$bad"; else
  pass "chat stdio: 18 tools, whoami not_found by the ambient path ('$(one_line "$who_msg" | cut -c1-80)'); harvester stdio: tools served, --transport http/--port/--ignore-robots-txt each refused by name (exit 2); malformed frame → -32700, connection kept"
fi

# ─── M.08 — the `pfm mcp` CLI surface ───────────────────────────────────────

beat M.08-mcp-cli M52 M53 M54 M55
spends none
bad=""
# M52 — bare `pfm mcp` IS the chat stdio server
if mcp_stdio bare tools/list '{}' 30; then
  [ "$(printf '%s' "$MCP_OUT" | jq -r '.result.tools | length')" -eq 18 ] || bad="$bad M52: bare pfm mcp served $(printf '%s' "$MCP_OUT" | jq -r '.result.tools | length') tools (want 18);"
  [ "$(printf '%s\n' "$MCP_FRAMES" | jq -r 'select(.id == 1) | .result.serverInfo.name' 2>/dev/null | head -1)" = pfm ] ||
    bad="$bad M52: bare pfm mcp's initialize names server '$(printf '%s\n' "$MCP_FRAMES" | jq -r 'select(.id == 1) | .result.serverInfo.name' 2>/dev/null | head -1)' (want pfm);"
else
  bad="$bad M52: bare pfm mcp: $MCP_WHY;"
fi
# M53 — ls: exactly the two registered servers, a boolean and a source each
ls_out="$(pfm mcp ls 2>&1)"
ls_rc=$?
[ "$ls_rc" -eq 0 ] || bad="$bad M53: pfm mcp ls exited $ls_rc: $(one_line "$ls_out");"
[ "$(printf '%s\n' "$ls_out" | grep -c .)" -eq 2 ] || bad="$bad M53: pfm mcp ls printed $(printf '%s\n' "$ls_out" | grep -c .) rows (want 2);"
printf '%s\n' "$ls_out" | grep -qE '^chat	(true|false)	(file|default|legacy)$' || bad="$bad M53: no 'chat<TAB>bool<TAB>source' row: $(one_line "$ls_out");"
printf '%s\n' "$ls_out" | grep -qE '^harvester	(true|false)	(file|default|legacy)$' || bad="$bad M53: no 'harvester<TAB>bool<TAB>source' row: $(one_line "$ls_out");"
harvester_was="$(printf '%s\n' "$ls_out" | awk -F'\t' '$1 == "harvester" { print $2 }')"
# M54 — enable/disable flip the file and the serve gate reads them; restored to what it was
dis="$(pfm mcp harvester disable 2>&1)"
dis_rc=$?
[ "$dis_rc" -eq 0 ] || bad="$bad M54: pfm mcp harvester disable exited $dis_rc: $(one_line "$dis");"
case "$harvester_was" in
  true) printf '%s' "$dis" | grep -qx 'harvester	disabled	updated' || bad="$bad M54: disable on an enabled server printed '$(one_line "$dis")' (want harvester<TAB>disabled<TAB>updated);" ;;
  false) printf '%s' "$dis" | grep -qx 'harvester	disabled	unchanged' || bad="$bad M54: disable on an already-disabled server printed '$(one_line "$dis")' (want …unchanged);" ;;
esac
pfm mcp ls 2>/dev/null | grep -qE '^harvester	false	' || bad="$bad M54: after disable, pfm mcp ls does not read harvester false;"
gated="$(pfm mcp harvester serve </dev/null 2>&1)"
gated_rc=$?
[ "$gated_rc" -eq 1 ] && printf '%s' "$gated" | grep -q 'disabled by config' || bad="$bad M54: serve on a disabled server exited $gated_rc '$(one_line "$gated")' (want 1 naming disabled by config);"
en="$(pfm mcp harvester enable 2>&1)"
en_rc=$?
[ "$en_rc" -eq 0 ] && printf '%s' "$en" | grep -qx 'harvester	enabled	updated' || bad="$bad M54: enable exited $en_rc '$(one_line "$en")' (want 0, harvester<TAB>enabled<TAB>updated);"
pfm mcp ls 2>/dev/null | grep -qE '^harvester	true	' || bad="$bad M54: after enable, pfm mcp ls does not read harvester true;"
if [ "$harvester_was" = false ]; then
  pfm mcp harvester disable >/dev/null 2>&1 || bad="$bad M54 restore: could not put harvester back to disabled;"
fi
unknown="$(pfm mcp nosuch enable 2>&1)"
unknown_rc=$?
[ "$unknown_rc" -eq 2 ] && printf '%s' "$unknown" | grep -q 'unknown server "nosuch"' || bad="$bad M54: an unknown server exited $unknown_rc '$(one_line "$unknown")' (want 2 naming it);"
usage="$(pfm mcp chat 2>&1)"
usage_rc=$?
[ "$usage_rc" -eq 2 ] && printf '%s' "$usage" | grep -q '^usage: pfm mcp' || bad="$bad M54: a bare server name exited $usage_rc '$(one_line "$usage")' (want 2 with usage);"
# M55 — exactly `pfm mcp serve` is the daemon dispatch: with one running it refuses by pid
running_pid="$(daemon_status | jq -r '.pid // empty')"
serve="$(pfm mcp serve </dev/null 2>&1)"
serve_rc=$?
[ "$serve_rc" -eq 1 ] && printf '%s' "$serve" | grep -q "already running (pid ${running_pid:-?}" || bad="$bad M55: pfm mcp serve beside the daemon exited $serve_rc '$(one_line "$serve")' (want 1 naming pid ${running_pid:-<none>});"
if [ -n "$bad" ]; then fail "$bad"; else
  pass "bare pfm mcp = chat stdio (18 tools, server pfm); ls: 2 rows (harvester was $harvester_was); disable/enable flip the file and gate serve (exit 1 by name), restored; unknown server + bare name exit 2; pfm mcp serve refuses beside pid $running_pid"
fi

# ─── M.09 — the chat fleet server's tools, driven directly ──────────────────

beat M.09-chat-tools M1 M2 M3 M4 M5 M6 M7 M8 M9 M10 M11 M12 M13 M14
spends "cc:$SEAT"
target_live "$CHAT"
# every refusal this beat provokes on purpose (chat_resolve/chat_inject
# validation and the unsigned-over-HTTP refusal), so Wave 6's activity log
# never turns a passing beat red on its own intentional errors.
expect-log 'kind must be label, session, or cxwin'
expect-log 'UNSIGNED'
expect-log 'shared HTTP daemon'
expect-log 'name is required'
expect-log 'must be a non-empty thread id'
expect-log 'limit must be between 1 and 50'
expect-log 'tail_lines must be between 1 and 1000'
expect-log 'focus must be one non-empty line'
expect-log 'is not a tmux key'
expect-log 'has no live Codex tmux seat'
expect-log 'engine and model require summary=true or ask=true'
if requires; then
  bad=""
  notes=""
  sid="$(live_field "$CHAT" 2)"
  sock="$(live_field "$CHAT" 11)"
  # M1 chat_ls — a row for this chat; a filter that matches nothing is rows [] matched 0, not an error
  if mcp_call http chat chat_ls '{}'; then
    [ "$MCP_ISERR" = false ] || bad="$bad M1: chat_ls isError: $(one_line "$MCP_TEXT");"
    printf '%s' "$MCP_STRUCT" | jq -e --arg n "$CHAT" '.rows[] | select(.name == $n and (.kind | startswith("live-")))' >/dev/null 2>&1 ||
      bad="$bad M1: chat_ls rows carry no live row named $CHAT (count $(sfield .count), matched $(sfield .matched));"
    [ -n "$(sfield .count)" ] && [ -n "$(sfield .matched)" ] || bad="$bad M1: chat_ls lacks count/matched;"
  else
    bad="$bad M1: chat_ls: $MCP_WHY;"
  fi
  if mcp_call http chat chat_ls '{"project":"zzz-lane-m-no-such-project"}'; then
    [ "$MCP_ISERR" = false ] && [ "$(sfield .matched)" = 0 ] && [ "$(sfield '.rows | length')" = 0 ] ||
      bad="$bad M1: a filter matching nothing answered isError=$MCP_ISERR matched=$(sfield .matched) rows=$(sfield '.rows | length') (want rows [] matched 0, no error);"
  else
    bad="$bad M1: chat_ls with a filter: $MCP_WHY;"
  fi
  # M2 chat_resolve — ok/not_found/ambiguous are results; a bad kind is a tool error
  if mcp_call http chat chat_resolve "$(jq -cn --arg n "$CHAT" '{kind: "label", name: $n}')"; then
    [ "$(sfield .status)" = ok ] && [ "$(sfield .code)" = 0 ] && [ "$(sfield .socket_path)" = "$sock" ] ||
      bad="$bad M2: chat_resolve $CHAT answered status $(sfield .status) code $(sfield .code) socket '$(sfield .socket_path)' (want ok/0/$sock);"
  else
    bad="$bad M2: chat_resolve: $MCP_WHY;"
  fi
  if mcp_call http chat chat_resolve '{"kind":"label","name":"NO_SUCH_CHAT_LANE_M"}'; then
    [ "$MCP_ISERR" = false ] && [ "$(sfield .status)" = not_found ] && [ "$(sfield .code)" = 1 ] ||
      bad="$bad M2: chat_resolve on a missing name answered isError=$MCP_ISERR status $(sfield .status) code $(sfield .code) (want not_found/1, no error);"
  else
    bad="$bad M2: chat_resolve missing: $MCP_WHY;"
  fi
  if mcp_call http chat chat_resolve '{"kind":"bogus","name":"x"}'; then
    [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -q 'kind must be label, session, or cxwin' ||
      bad="$bad M2: a bogus kind did not raise the named tool error: isError=$MCP_ISERR $(one_line "$MCP_TEXT");"
  else
    bad="$bad M2: chat_resolve bogus kind: $MCP_WHY;"
  fi
  # M3/M4 chat_inject — over the daemon a sender cannot be derived, so the
  # message is REFUSED unsigned by name (nothing typed); over stdio with the
  # sender stated it is delivered with proof; a missing target is a result
  # (code 4, the unknown-target code) and never a Go error.
  if mcp_call http chat chat_inject "$(jq -cn --arg t "$CHAT" '{target: $t, message: "lane M: this must not be typed unsigned"}')"; then
    [ "$MCP_ISERR" = false ] && [ "$(sfield .status)" = refused ] && [ "$(sfield .code)" = 6 ] &&
      printf '%s' "$(sfield .message)" | grep -q 'UNSIGNED' && printf '%s' "$(sfield .message)" | grep -q 'shared HTTP daemon' ||
      bad="$bad M3: an identity-less inject over the daemon answered isError=$MCP_ISERR status $(sfield .status) code $(sfield .code) '$(one_line "$(sfield .message)" | cut -c1-120)' (want refused/6 naming UNSIGNED and the shared HTTP daemon);"
    [ "$(sfield .typed)" != true ] || bad="$bad M3: the unsigned refusal still reports typed=true;"
  else
    bad="$bad M3: chat_inject over HTTP: $MCP_WHY;"
  fi
  needle="MCP-INJECT-OK-$$"
  if CHAT_SENDER_SESSION=lane-M CHAT_SENDER_LABEL=lane-M \
    mcp_call stdio chat chat_inject "$(jq -cn --arg t "$CHAT" --arg m "reply with exactly one word: $needle" '{target: $t, message: $m}')"; then
    case "$(sfield .status)" in
      delivered|queued) ;;
      *) bad="$bad M3: a signed inject over stdio answered status '$(sfield .status)' code $(sfield .code): $(one_line "$(sfield .message)");" ;;
    esac
    [ "$(sfield .typed)" = true ] || bad="$bad M3: the stdio inject reports typed=$(sfield .typed);"
    wait_last "$CHAT" "$needle" 240 || bad="$bad M3: the delivered inject never produced its needle: ${LANE_WAIT_WHY:-no wait reason recorded};"
  else
    bad="$bad M3: chat_inject over stdio: $MCP_WHY;"
  fi
  if mcp_call http chat chat_inject '{"target":"NO_SUCH_CHAT_LANE_M","message":"x"}'; then
    [ "$MCP_ISERR" = false ] && [ "$(sfield .code)" = 4 ] && printf '%s' "$(sfield .message)" | grep -q 'NO_SUCH_CHAT_LANE_M' ||
      bad="$bad M3: inject on a missing chat answered isError=$MCP_ISERR status $(sfield .status) code $(sfield .code) '$(one_line "$(sfield .message)")' (want a non-error result, code 4, naming the target);"
    case "$(sfield .status)" in
      not_found) ;;
      refused) notes="$notes M3 observed: a missing target reads status 'refused' (code 4) where the tool text documents 'not_found';" ;;
      *) bad="$bad M3: missing-target status '$(sfield .status)' is neither not_found nor refused;" ;;
    esac
  else
    bad="$bad M3: chat_inject missing target: $MCP_WHY;"
  fi
  if mcp_call http chat chat_inject '{"target":"self","message":"x"}'; then
    [ "$MCP_ISERR" = false ] && [ "$(sfield .status)" = not_found ] && [ "$(sfield .code)" = 4 ] && printf '%s' "$(sfield .message)" | grep -q 'shared HTTP daemon' ||
      bad="$bad M4: self over the daemon answered isError=$MCP_ISERR status $(sfield .status) code $(sfield .code) (want not_found/4 as a RESULT naming the shared HTTP daemon);"
  else
    bad="$bad M4: chat_inject self: $MCP_WHY;"
  fi
  # M5 chat_self_compact — the requesting seat only: over the daemon it refuses
  # by name with ITS OWN remedy (the CLI twin), and a bad focus is a tool error
  if mcp_call http chat chat_self_compact '{"focus":"lane M","then":"reply with exactly one word: NEVER"}'; then
    [ "$MCP_ISERR" = false ] && [ "$(sfield .status)" = not_found ] && printf '%s' "$(sfield .message)" | grep -q 'pfm chat self-compact' ||
      bad="$bad M5: self-compact over the daemon answered isError=$MCP_ISERR status $(sfield .status) '$(one_line "$(sfield .message)" | cut -c1-120)' (want not_found naming pfm chat self-compact);"
  else
    bad="$bad M5: chat_self_compact: $MCP_WHY;"
  fi
  if mcp_call http chat chat_self_compact '{"focus":"","then":"x"}'; then
    [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -q 'focus must be one non-empty line' || bad="$bad M5: an empty focus did not raise the named tool error: $(one_line "$MCP_TEXT");"
  else
    bad="$bad M5: chat_self_compact empty focus: $MCP_WHY;"
  fi
  # M6/M7 chat_keys — a real key lands; an unknown key name is a tool error
  # listing the valid ones; a missing target is a tool error naming it
  if mcp_call http chat chat_keys "$(jq -cn --arg t "$CHAT" '{target: $t, keys: ["Escape"]}')"; then
    [ "$MCP_ISERR" = false ] && [ "$(sfield .status)" = ok ] && [ "$(sfield .count)" = 1 ] && [ "$(sfield .pane)" != "" ] ||
      bad="$bad M6: chat_keys Escape answered isError=$MCP_ISERR status $(sfield .status) count $(sfield .count);"
  else
    bad="$bad M6: chat_keys: $MCP_WHY;"
  fi
  if mcp_call http chat chat_keys "$(jq -cn --arg t "$CHAT" '{target: $t, keys: ["NotAKeyLaneM"]}')"; then
    [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -q 'is not a tmux key' || bad="$bad M6: an unknown key did not raise the named tool error: $(one_line "$MCP_TEXT");"
  else
    bad="$bad M6: chat_keys bad key: $MCP_WHY;"
  fi
  if mcp_call http chat chat_keys '{"target":"NO_SUCH_CHAT_LANE_M","keys":["Escape"]}'; then
    [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -q 'NO_SUCH_CHAT_LANE_M' ||
      bad="$bad M6: chat_keys on a missing chat answered isError=$MCP_ISERR '$(one_line "$MCP_TEXT")' (want a tool error naming the target);"
  else
    bad="$bad M6: chat_keys missing: $MCP_WHY;"
  fi
  notes="$notes M7 (mixed KeysOutput+error on a pane dying mid-sequence) not provoked — needs the Wave 7 mock engine;"
  # M8 chat_capture — screen text with bounds; a missing chat is a not_found RESULT
  if mcp_call http chat chat_capture "$(jq -cn --arg t "$CHAT" '{target: $t, tail_lines: 20}')"; then
    [ "$MCP_ISERR" = false ] && [ "$(sfield .status)" = ok ] && [ "$(sfield .bytes)" -gt 0 ] 2>/dev/null ||
      bad="$bad M8: chat_capture answered isError=$MCP_ISERR status $(sfield .status) bytes $(sfield .bytes);"
    [ "$(sfield '.text | split("\n") | length')" -le 20 ] 2>/dev/null || bad="$bad M8: tail_lines 20 returned $(sfield '.text | split("\n") | length') lines;"
  else
    bad="$bad M8: chat_capture: $MCP_WHY;"
  fi
  if mcp_call http chat chat_capture '{"target":"NO_SUCH_CHAT_LANE_M"}'; then
    [ "$MCP_ISERR" = false ] && [ "$(sfield .status)" = not_found ] && [ "$(sfield .code)" = 4 ] && printf '%s' "$(sfield .message)" | grep -q 'NO_SUCH_CHAT_LANE_M' ||
      bad="$bad M8: capture of a missing chat answered isError=$MCP_ISERR status $(sfield .status) code $(sfield .code) (want not_found/4 naming it, no error);"
  else
    bad="$bad M8: chat_capture missing: $MCP_WHY;"
  fi
  if mcp_call http chat chat_capture "$(jq -cn --arg t "$CHAT" '{target: $t, tail_lines: 5000}')"; then
    [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -q 'tail_lines must be between 1 and 1000' || bad="$bad M8: tail_lines 5000 did not raise the bound error: $(one_line "$MCP_TEXT");"
  else
    bad="$bad M8: chat_capture bound: $MCP_WHY;"
  fi
  # M9 chat_whoami — the Codex _meta.threadId path, valid and invalid, over the daemon
  if mcp_call http chat chat_whoami '{}' '{"threadId":"lane-m-no-such-thread"}'; then
    [ "$MCP_ISERR" = false ] && [ "$(sfield .status)" = not_found ] && printf '%s' "$(sfield .message)" | grep -q 'has no live Codex tmux seat' ||
      bad="$bad M9: an unknown _meta.threadId answered isError=$MCP_ISERR status $(sfield .status) '$(one_line "$(sfield .message)")' (want not_found naming no live Codex seat);"
  else
    bad="$bad M9: chat_whoami with a bogus threadId: $MCP_WHY;"
  fi
  if mcp_call http chat chat_whoami '{}' '{"threadId":" "}'; then
    [ "$(sfield .status)" = not_found ] && printf '%s' "$(sfield .message)" | grep -q 'must be a non-empty thread id' || bad="$bad M9: a blank threadId was not refused by name: $(one_line "$(sfield .message)");"
  else
    bad="$bad M9: chat_whoami blank threadId: $MCP_WHY;"
  fi
  if live_chat "$E2_CHAT"; then
    e2_id="$(live_field "$E2_CHAT" 2)"
    if mcp_call http chat chat_whoami '{}' "$(jq -cn --arg id "$e2_id" '{threadId: $id}')"; then
      [ "$(sfield .status)" = ok ] && [ "$(sfield .engine)" = cx ] && [ "$(sfield .id)" = "$e2_id" ] && [ "$(sfield .source)" = mcp-thread-meta ] ||
        bad="$bad M9: whoami with $E2_CHAT's thread id answered status $(sfield .status) engine '$(sfield .engine)' id '$(sfield .id)' source '$(sfield .source)' (want ok/cx/$e2_id/mcp-thread-meta);"
    else
      bad="$bad M9: chat_whoami with $E2_CHAT's threadId: $MCP_WHY;"
    fi
  else
    notes="$notes M9 valid-thread path not asserted: $E2_CHAT has no live row (${E2_WHY:-it died after the prelude});"
  fi
  # M10 chat_find / M11 chat_read — the transcript index over this chat's own words
  find_needle="LANE-M-FIND-$$-$(date +%s)"
  if CHAT_SENDER_SESSION=lane-M CHAT_SENDER_LABEL=lane-M \
    mcp_call stdio chat chat_inject "$(jq -cn --arg t "$CHAT" --arg m "reply with exactly one word: $find_needle" '{target: $t, message: $m}')" &&
    wait_last "$CHAT" "$find_needle" 240; then
    if wait_for 90 "mcp_call http chat chat_find \"\$(jq -cn --arg e '$find_needle' '{excerpt: \$e}')\" && [ \"\$(sfield .count)\" -ge 1 ]"; then
      printf '%s' "$MCP_STRUCT" | jq -e --arg id "$sid" '.candidates[] | select(.id == $id)' >/dev/null 2>&1 ||
        bad="$bad M10: chat_find matched $(sfield .count) candidate(s) but none is this chat's session $sid: $(one_line "$(sfield '[.candidates[].id] | join(",")')");"
      [ -n "$(sfield '.needles | length')" ] || bad="$bad M10: chat_find answered without needles;"
    else
      bad="$bad M10: chat_find never found '$find_needle' in this chat's transcript within 90s (last answer: isError=$MCP_ISERR count $(sfield .count) $(one_line "$MCP_TEXT" | cut -c1-120));"
    fi
  else
    bad="$bad M10: the find needle could not be planted in $CHAT: ${MCP_WHY:-$LANE_WAIT_WHY};"
  fi
  if mcp_call http chat chat_find '{"excerpt":"zzz-lane-m-nobody-ever-said-this-4f2a9c"}'; then
    [ "$MCP_ISERR" = false ] && [ "$(sfield .count)" = 0 ] ||
      bad="$bad M10: an excerpt nobody said answered isError=$MCP_ISERR count $(sfield .count) '$(one_line "$MCP_TEXT" | cut -c1-100)' (want count 0, no error);"
    [ "$MCP_ISERR" = false ] && notes="$notes M10 observed: a miss is count 0 (the tool text documents a tool error 'no session contains the excerpt');"
  else
    bad="$bad M10: chat_find miss: $MCP_WHY;"
  fi
  if mcp_call http chat chat_find '{"excerpt":"x","limit":500}'; then
    [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -q 'limit must be between 1 and 50' || bad="$bad M10: limit 500 did not raise the bound error: $(one_line "$MCP_TEXT");"
  else
    bad="$bad M10: chat_find bound: $MCP_WHY;"
  fi
  TRANSCRIPT=""
  if mcp_call http chat chat_read "$(jq -cn --arg s "$sid" '{source: $s, last_n: 5}')"; then
    [ "$MCP_ISERR" = false ] && [ "$(sfield .id)" = "$sid" ] && [ "$(sfield .count)" -ge 1 ] 2>/dev/null && [ "$(sfield .count)" -le 5 ] 2>/dev/null ||
      bad="$bad M11: chat_read $sid answered isError=$MCP_ISERR id '$(sfield .id)' count $(sfield .count) (want ≤ 5 turns of this session);"
    TRANSCRIPT="$(sfield .path)"
    [ -f "$TRANSCRIPT" ] || bad="$bad M11: chat_read's path '$TRANSCRIPT' is not a file;"
  else
    bad="$bad M11: chat_read: $MCP_WHY;"
  fi
  if mcp_call http chat chat_read '{"source":"lane-m-no-such-transcript-id"}'; then
    [ "$MCP_ISERR" = true ] || bad="$bad M11: chat_read on an unknown id answered a result, not a tool error: $(one_line "$MCP_TEXT");"
  else
    bad="$bad M11: chat_read unknown: $MCP_WHY;"
  fi
  # M12 chat_last — the newest answer (the needle just planted); unknown target is a tool error naming resolution
  if mcp_call http chat chat_last "$(jq -cn --arg t "$CHAT" '{target: $t}')"; then
    [ "$MCP_ISERR" = false ] && printf '%s' "$(sfield .text)" | grep -qF "$find_needle" ||
      bad="$bad M12: chat_last answered isError=$MCP_ISERR text '$(one_line "$(sfield .text)" | cut -c1-80)' (want the newest answer carrying $find_needle);"
  else
    bad="$bad M12: chat_last: $MCP_WHY;"
  fi
  if mcp_call http chat chat_last '{"target":"NO_SUCH_CHAT_LANE_M"}'; then
    [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -q 'chat_last' || bad="$bad M12: chat_last on a missing chat answered isError=$MCP_ISERR '$(one_line "$MCP_TEXT")' (want a tool error naming the resolve failure);"
  else
    bad="$bad M12: chat_last missing: $MCP_WHY;"
  fi
  # M13 chat_status — name/state/idle_seconds; engine without summary is a tool error; unknown target is a tool error
  if mcp_call http chat chat_status "$(jq -cn --arg t "$CHAT" '{target: $t}')"; then
    st="$(sfield .state)"
    [ "$MCP_ISERR" = false ] && { [ "$st" = idle ] || [ "$st" = working ]; } && [ -n "$(sfield .name)" ] && [ -n "$(sfield .idle_seconds)" ] ||
      bad="$bad M13: chat_status answered isError=$MCP_ISERR state '$st' name '$(sfield .name)' idle_seconds '$(sfield .idle_seconds)';"
    [ "$st" = working ] && [ "$(sfield .idle_seconds)" != 0 ] && bad="$bad M13: state working with idle_seconds $(sfield .idle_seconds) (nonzero only while idle);"
  else
    bad="$bad M13: chat_status: $MCP_WHY;"
  fi
  if mcp_call http chat chat_status "$(jq -cn --arg t "$CHAT" '{target: $t, engine: "claude"}')"; then
    [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -q 'engine and model require summary=true or ask=true' || bad="$bad M13: engine without summary did not raise the named tool error: $(one_line "$MCP_TEXT");"
  else
    bad="$bad M13: chat_status engine: $MCP_WHY;"
  fi
  if mcp_call http chat chat_status '{"target":"NO_SUCH_CHAT_LANE_M"}'; then
    [ "$MCP_ISERR" = true ] || bad="$bad M13: chat_status on a missing chat answered a result: $(one_line "$MCP_TEXT");"
  else
    bad="$bad M13: chat_status missing: $MCP_WHY;"
  fi
  # M14 chat_new — a real spawn on this seat (ended in M.10), and two refusals that spawn nothing
  if mcp_call http chat chat_new '{"name":""}'; then
    [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -q 'name is required' || bad="$bad M14: an empty name did not raise the named tool error: $(one_line "$MCP_TEXT");"
  else
    bad="$bad M14: chat_new empty name: $MCP_WHY;"
  fi
  if mcp_call http chat chat_new "$(jq -cn --arg c "$CWD" '{name: "M_NEVER_SPAWNED", engine: "cc", account: 99, cwd: $c, effort: "lane-bogus"}')"; then
    [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -q 'account 99 is not in the configured roster' ||
      bad="$bad M14: account 99 did not refuse by name: isError=$MCP_ISERR $(one_line "$MCP_TEXT");"
    live_chat M_NEVER_SPAWNED && bad="$bad M14: a refused chat_new still spawned M_NEVER_SPAWNED;"
  else
    bad="$bad M14: chat_new bad account: $MCP_WHY;"
  fi
  if MCP_HTTP_TIMEOUT=280 mcp_call http chat chat_new \
    "$(jq -cn --arg n "$NEW_CHAT" --arg c "$CWD" --argjson a "$SEAT" '{name: $n, engine: "cc", account: $a, cwd: $c, prompt: "Reply with one word: ready. Then wait.", await: true, timeout: 240}')"; then
    [ "$MCP_ISERR" = false ] && [ "$(sfield .status)" = ok ] && [ "$(sfield .code)" = 0 ] ||
      bad="$bad M14: chat_new $NEW_CHAT answered isError=$MCP_ISERR status $(sfield .status) code $(sfield .code): $(one_line "$MCP_TEXT" | cut -c1-160);"
    live_chat "$NEW_CHAT" || bad="$bad M14: chat_new answered but $NEW_CHAT has no live row: $(one_line "$(pfm ls --plain 2>&1)" | cut -c1-160);"
    [ "$(live_field "$NEW_CHAT" 9)" = "$SEAT" ] || bad="$bad M14: $NEW_CHAT reports account '$(live_field "$NEW_CHAT" 9)', not the requested $SEAT;"
    [ "$(live_field "$NEW_CHAT" 4)" = "$CWD" ] || bad="$bad M14: $NEW_CHAT was born in '$(live_field "$NEW_CHAT" 4)', not the requested $CWD;"
  else
    bad="$bad M14: chat_new $NEW_CHAT: $MCP_WHY;"
  fi
  if [ -n "$bad" ]; then fail "$bad${notes:+ · notes:$notes}"; else
    pass "ls/resolve/inject/self_compact/keys/capture/whoami/find/read/last/status/new each asserted for result AND refusal shape over the daemon (inject delivered over signed stdio); $NEW_CHAT spawned on cc:$SEAT${notes:+ · notes:$notes}"
  fi
fi

# ─── M.10 — open / name / kill / unkill / save / issue_servicedesk ──────────

beat M.10-chat-tools-gap M15 M16 M17 M18 M19 M20
spends "cc:$SEAT"
target "$NEW_CHAT"
# the validation refusals this beat provokes on purpose, declared before the
# fleet's activity log (Wave 6) can turn them into an unexplained ✗.
expect-log 'CLAUDE_CODE_SESSION_ID is not set'
expect-log 'is not a file path'
expect-log 'name must be one non-empty line'
expect-log 'severity must be'
expect-log 'title is required'
bad=""
notes=""
if ! live_chat "$NEW_CHAT"; then
  # M.09's spawn is this beat's target; made here when that beat could not.
  new_out="$(pfm chat new --name "$NEW_CHAT" --engine cc --account "$SEAT" --cwd "$CWD" --await --timeout 300 "Reply with one word: ready. Then wait." 2>&1)" ||
    bad="$bad the target chat $NEW_CHAT could not be opened (pfm chat new: $(one_line "$new_out"));"
fi
if [ -n "$bad" ]; then
  fail "$bad"
else
  new_sock="$(live_field "$NEW_CHAT" 11)"
  # M16 chat_name — rename lands in the fleet's own row, and back; bad names are tool errors
  if mcp_call http chat chat_name "$(jq -cn --arg t "$NEW_CHAT" --arg n "${NEW_CHAT}_RENAMED" '{target: $t, name: $n}')"; then
    [ "$MCP_ISERR" = false ] && [ "$(sfield .status)" = ok ] || bad="$bad M16: chat_name answered isError=$MCP_ISERR status $(sfield .status): $(one_line "$MCP_TEXT");"
    wait_for 20 "[ \"\$(socket_field '$new_sock' 5)\" = '${NEW_CHAT}_RENAMED' ]" ||
      bad="$bad M16: the row on $new_sock still reads '$(socket_field "$new_sock" 5)' after chat_name → ${NEW_CHAT}_RENAMED;"
    if mcp_call http chat chat_name "$(jq -cn --arg t "${NEW_CHAT}_RENAMED" --arg n "$NEW_CHAT" '{target: $t, name: $n}')"; then
      wait_for 20 "[ \"\$(socket_field '$new_sock' 5)\" = '$NEW_CHAT' ]" || bad="$bad M16: the rename back to $NEW_CHAT never landed (row reads '$(socket_field "$new_sock" 5)');"
    else
      bad="$bad M16: chat_name back: $MCP_WHY;"
    fi
  else
    bad="$bad M16: chat_name: $MCP_WHY;"
  fi
  if mcp_call http chat chat_name "$(jq -cn --arg t "$NEW_CHAT" '{target: $t, name: "two\nlines"}')"; then
    [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -q 'name must be one non-empty line' || bad="$bad M16: a two-line name did not raise the named tool error: $(one_line "$MCP_TEXT");"
  else
    bad="$bad M16: chat_name bad name: $MCP_WHY;"
  fi
  if mcp_call http chat chat_name '{"target":"NO_SUCH_CHAT_LANE_M","name":"x"}'; then
    [ "$MCP_ISERR" = true ] || bad="$bad M16: chat_name on a missing chat answered a result: $(one_line "$MCP_TEXT");"
  else
    bad="$bad M16: chat_name missing: $MCP_WHY;"
  fi
  # M17 chat_kill — hide (killed=true, row still live: chat_ls says so as a contradiction, never smoothed),
  # M18 chat_unkill restores; then kill with exit ends the pane; unkill leaves a resume row
  if mcp_call http chat chat_kill "$(jq -cn --arg t "$NEW_CHAT" '{target: $t}')"; then
    [ "$MCP_ISERR" = false ] && [ "$(sfield .status)" = ok ] || bad="$bad M17: chat_kill answered isError=$MCP_ISERR status $(sfield .status): $(one_line "$MCP_TEXT");"
    wait_for 20 "[ \"\$(row_field '$NEW_CHAT' 10)\" = true ]" || bad="$bad M17: the row's killed column reads '$(row_field "$NEW_CHAT" 10)' after chat_kill;"
    if mcp_call http chat chat_ls '{"all":true}'; then
      printf '%s' "$MCP_STRUCT" | jq -e --arg n "$NEW_CHAT" '.rows[] | select(.name == $n and .killed == true and .state == "killed-but-live")' >/dev/null 2>&1 ||
        bad="$bad M17: chat_ls{all} does not report $NEW_CHAT as killed-but-live after a kill that closed no pane: $(one_line "$(printf '%s' "$MCP_STRUCT" | jq -c --arg n "$NEW_CHAT" '[.rows[] | select(.name == $n) | {kind, state, killed}]')");"
    fi
  else
    bad="$bad M17: chat_kill: $MCP_WHY;"
  fi
  if mcp_call http chat chat_unkill "$(jq -cn --arg t "$NEW_CHAT" '{target: $t}')"; then
    [ "$MCP_ISERR" = false ] && [ "$(sfield .status)" = ok ] || bad="$bad M18: chat_unkill answered isError=$MCP_ISERR status $(sfield .status): $(one_line "$MCP_TEXT");"
    wait_for 20 "[ \"\$(row_field '$NEW_CHAT' 10)\" = false ]" || bad="$bad M18: the row's killed column reads '$(row_field "$NEW_CHAT" 10)' after chat_unkill;"
  else
    bad="$bad M18: chat_unkill: $MCP_WHY;"
  fi
  if mcp_call http chat chat_unkill '{"target":"NO_SUCH_CHAT_LANE_M"}'; then
    [ "$MCP_ISERR" = true ] || bad="$bad M18: chat_unkill on a missing chat answered a result: $(one_line "$MCP_TEXT");"
  else
    bad="$bad M18: chat_unkill missing: $MCP_WHY;"
  fi
  if MCP_HTTP_TIMEOUT=180 mcp_call http chat chat_kill "$(jq -cn --arg t "$NEW_CHAT" '{target: $t, exit: true}')"; then
    [ "$MCP_ISERR" = false ] && [ "$(sfield .status)" = ok ] || bad="$bad M17: chat_kill{exit} answered isError=$MCP_ISERR status $(sfield .status): $(one_line "$MCP_TEXT");"
    wait_for 90 "! live_chat '$NEW_CHAT'" || bad="$bad M17: $NEW_CHAT still has a live row 90s after chat_kill{exit:true};"
  else
    bad="$bad M17: chat_kill exit: $MCP_WHY;"
  fi
  if mcp_call http chat chat_unkill "$(jq -cn --arg t "$NEW_CHAT" '{target: $t}')"; then
    wait_for 20 "[ -n \"\$(chat_row '$NEW_CHAT')\" ] && [ \"\$(row_field '$NEW_CHAT' 10)\" = false ]" ||
      bad="$bad M18: after the exit-kill, chat_unkill left no resume row for $NEW_CHAT with killed=false: $(one_line "$(chat_row "$NEW_CHAT")");"
  else
    bad="$bad M18: chat_unkill after exit: $MCP_WHY;"
  fi
  # M15 chat_open — the resumable row reopened in a pane: the proof is a LIVE
  # row for it, not the ok the action layer returns
  if MCP_HTTP_TIMEOUT=180 mcp_call http chat chat_open "$(jq -cn --arg t "$NEW_CHAT" '{target: $t}')"; then
    [ "$MCP_ISERR" = false ] && [ "$(sfield .status)" = ok ] || bad="$bad M15: chat_open answered isError=$MCP_ISERR status $(sfield .status): $(one_line "$MCP_TEXT");"
    if ! wait_for 90 "live_chat '$NEW_CHAT'"; then
      bad="$bad M15: chat_open returned ok with message '$(one_line "$(sfield .message)" | cut -c1-160)' but no live row for $NEW_CHAT appeared in 90s — the action line was printed, not executed (action.Dispatch prints when stdout is not a terminal);"
    fi
  else
    bad="$bad M15: chat_open: $MCP_WHY;"
  fi
  if mcp_call http chat chat_open '{"target":"NO_SUCH_CHAT_LANE_M"}'; then
    [ "$MCP_ISERR" = true ] || bad="$bad M15: chat_open on a missing chat answered a result: $(one_line "$MCP_TEXT");"
  else
    bad="$bad M15: chat_open missing: $MCP_WHY;"
  fi
  # The spawned chat is this lane's cost: ended here whatever chat_open did.
  if live_chat "$NEW_CHAT"; then
    end_out="$(pfm chat end "$NEW_CHAT" 2>&1)" || notes="$notes cleanup: pfm chat end $NEW_CHAT failed: $(one_line "$end_out");"
  fi
  # M19 chat_save — a bare word is refused; a path appends the transcript
  if mcp_call http chat chat_save '{"target":"lane-m-notes"}'; then
    [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -q 'is not a file path' || bad="$bad M19: a bare-word target was not refused by name: isError=$MCP_ISERR $(one_line "$MCP_TEXT");"
  else
    bad="$bad M19: chat_save bare word: $MCP_WHY;"
  fi
  save_file="$SCRATCH/save/lane-m-save.md"
  if [ -n "${TRANSCRIPT:-}" ] && [ -f "$TRANSCRIPT" ]; then
    if mcp_call http chat chat_save "$(jq -cn --arg t "$save_file" --arg x "$TRANSCRIPT" '{target: $t, transcript: $x}')"; then
      [ "$MCP_ISERR" = false ] && [ "$(sfield .status)" = ok ] || bad="$bad M19: chat_save answered isError=$MCP_ISERR status $(sfield .status): $(one_line "$MCP_TEXT");"
      grep -qF 'FULL TRANSCRIPT' "$save_file" 2>/dev/null || bad="$bad M19: $save_file was not written with the transcript dump;"
      grep -qF "$TRANSCRIPT" "$save_file" 2>/dev/null || bad="$bad M19: $save_file does not name its source transcript;"
    else
      bad="$bad M19: chat_save: $MCP_WHY;"
    fi
  else
    bad="$bad M19: no transcript path for $CHAT (M.09's chat_read did not yield one) — the path form was NOT asserted;"
  fi
  if mcp_call http chat chat_save "$(jq -cn --arg t "$SCRATCH/save/no-caller.md" '{target: $t}')"; then
    [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -q 'CLAUDE_CODE_SESSION_ID is not set' ||
      bad="$bad M19: chat_save with no transcript over the daemon did not refuse by name (no calling chat): isError=$MCP_ISERR $(one_line "$MCP_TEXT");"
  else
    bad="$bad M19: chat_save no transcript: $MCP_WHY;"
  fi
  # M20 issue_servicedesk — filed under UNIDENTIFIED from the daemon, read back from pfm issues
  issue_title="lane M issue $$ $(date +%s)"
  if mcp_call http chat issue_servicedesk "$(jq -cn --arg t "$issue_title" '{title: $t, detail: "filed by the Tier B lane M direct client; safe to close", severity: "low", area: "lanes/M"}')"; then
    issue_id="$(sfield .id)"
    [ "$MCP_ISERR" = false ] && [ "$(sfield .status)" = ok ] && [ "${issue_id:-0}" -gt 0 ] 2>/dev/null || bad="$bad M20: issue_servicedesk answered isError=$MCP_ISERR status $(sfield .status) id '$issue_id';"
    issues="$(pfm issues --json 2>&1)"
    printf '%s' "$issues" | jq -e --arg t "$issue_title" '.[] | select(.Title == $t and .ReporterSession == "UNIDENTIFIED")' >/dev/null 2>&1 ||
      bad="$bad M20: pfm issues --json carries no row titled '$issue_title' with ReporterSession UNIDENTIFIED: $(one_line "$(printf '%s' "$issues" | jq -c --arg t "$issue_title" '[.[] | select(.Title == $t) | {ID, ReporterSession}]' 2>/dev/null || echo "$issues")" | cut -c1-160);"
  else
    bad="$bad M20: issue_servicedesk: $MCP_WHY;"
  fi
  if mcp_call http chat issue_servicedesk '{"title":"","detail":"x"}'; then
    [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -q 'title is required' || bad="$bad M20: an empty title was not refused by name: $(one_line "$MCP_TEXT");"
  else
    bad="$bad M20: issue_servicedesk empty title: $MCP_WHY;"
  fi
  if mcp_call http chat issue_servicedesk '{"title":"x","detail":"y","severity":"urgent"}'; then
    [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -q 'severity must be' || bad="$bad M20: an unknown severity was not refused by name: $(one_line "$MCP_TEXT");"
  else
    bad="$bad M20: issue_servicedesk bad severity: $MCP_WHY;"
  fi
  if [ -n "$bad" ]; then fail "$bad${notes:+ · notes:$notes}"; else
    pass "name → row renamed and back · kill → killed=true (killed-but-live in chat_ls), unkill → false, kill{exit} → pane gone, unkill → resume row · open → live row · save refuses a bare word, writes $save_file · issue $issue_id filed as UNIDENTIFIED and read back from pfm issues${notes:+ · notes:$notes}"
  fi
fi

# ─── M.11 — the harvester server's tools, on a real small document ──────────

beat M.11-harvester-tools M21 M22 M23 M24 M25 M26 M27 M28 M29 H13
spends none
bad=""
notes=""
RFC_PATH=""
NOTE="$SCRATCH/lane-m-note.md"
printf '# lane M local note\n\nSENTINEL-LANE-M-%s\n' "$$" >"$NOTE"
if ! pfm mcp ls 2>/dev/null | grep -qE '^harvester	true	'; then
  bad="$bad the harvester is disabled in this root (pfm mcp ls) — infra/demo/setup.sh install fell back to --skip-harvest, so no harvester tool can be driven;"
fi
# item <group> <n> <jq-path> — one field of the n-th typed item of a group:
# read answers {urls, files, publications}, each in its input's order;
# download_file answers items
item() { printf '%s' "$MCP_STRUCT" | jq -r ".$1[$2]$3 | if . == null then empty else . end" 2>/dev/null; } # false stays false
# M21 read {urls} — a real page: the typed item carries the path of the cached
# artifact and the body; a second read is `cached`; include_content false
# drops the body and keeps chars and path; one url per item, order kept, a
# failing item beside a good one never makes the call isError
if [ -z "$bad" ]; then
  if MCP_HTTP_TIMEOUT=180 mcp_call http harvester read "$(jq -cn --arg u "$RFC_DIRECT" '{urls: [$u]}')"; then
    if [ "$MCP_ISERR" != false ] || [ -z "$MCP_STRUCT" ]; then
      bad="$bad M21: read $RFC_DIRECT isError=$MCP_ISERR or no structuredContent: $(one_line "$MCP_TEXT" | cut -c1-200);"
    elif [ -n "$(item urls 0 .error)" ]; then
      bad="$bad M21: read $RFC_DIRECT failed: $(one_line "$(item urls 0 .error)" | cut -c1-200) (network or policy — the item says which);"
    else
      RFC_PATH="$(item urls 0 .path)"
      [ "$(item urls 0 .source)" = "$RFC_DIRECT" ] || bad="$bad M21: the item's source is '$(item urls 0 .source)', not $RFC_DIRECT;"
      [ -f "$RFC_PATH" ] || bad="$bad M21: the item's path '$RFC_PATH' is not a file on disk;"
      item urls 0 .content | grep -qi 'coffee' || bad="$bad M21: the content of RFC 2324 does not mention coffee — not the document;"
      [ "$(item urls 0 '.gaps | type')" = array ] || bad="$bad M21: the item's gaps is not a list: $(one_line "$MCP_STRUCT" | cut -c1-160);"
      [ -n "$(item urls 0 .via)" ] || bad="$bad M21: the item names no via: $(one_line "$MCP_STRUCT" | cut -c1-160);"
      [ "$(sfield '[keys[] | select(. != "urls")] | length')" = 0 ] || bad="$bad M21: a urls-only read answered other groups: $(sfield 'keys | join(",")');"
      printf '%s\n' "$MCP_TEXT" | sed -n 's/^#\{1,\} //p' | grep -qxF "$RFC_DIRECT" || bad="$bad M21: the readable text carries no heading for $RFC_DIRECT: $(one_line "$MCP_TEXT" | cut -c1-160);"
      [ "$(text_line 1)" = '## urls (1)' ] || bad="$bad M21: the readable text does not open with its group heading '## urls (1)': $(one_line "$(text_line 1)");"
    fi
  else
    bad="$bad M21: read: $MCP_WHY;"
  fi
  if [ -n "$RFC_PATH" ]; then
    if MCP_HTTP_TIMEOUT=120 mcp_call http harvester read "$(jq -cn --arg u "$RFC_DIRECT" '{urls: [$u]}')"; then
      [ "$(item urls 0 .cached)" = true ] || bad="$bad M21: the second read of the same URL is not cached: $(one_line "$MCP_STRUCT" | cut -c1-160);"
    else
      bad="$bad M21: second read: $MCP_WHY;"
    fi
    if MCP_HTTP_TIMEOUT=120 mcp_call http harvester read "$(jq -cn --arg u "$RFC_DIRECT" '{urls: [$u], include_content: false}')"; then
      [ -z "$(item urls 0 .content)" ] && [ "$(item urls 0 .chars)" -gt 0 ] 2>/dev/null && [ "$(item urls 0 .path)" = "$RFC_PATH" ] ||
        bad="$bad M21: include_content false did not answer an item with chars and path and no content: $(one_line "$MCP_STRUCT" | cut -c1-160);"
    else
      bad="$bad M21: include_content false: $MCP_WHY;"
    fi
  fi
  if mcp_call http harvester read '{"urls":[]}'; then
    [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -qF 'read needs at least one item in urls, files or publications' || bad="$bad M21: a read with no item was not refused by name: $(one_line "$MCP_RPCERR$MCP_TEXT" | cut -c1-160);"
  else
    bad="$bad M21: read empty: $MCP_WHY;"
  fi
  # M21 batch limits — 50 items in total, 20 of them publications, each refused by name before any fetch
  if mcp_call http harvester read "$(jq -cn '{urls: [range(51) | "https://example.invalid/\(.)"]}')"; then
    [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -qF 'read takes at most 50 items in total across urls, files and publications; this call sent 51' || bad="$bad M21: 51 items were not refused by the total limit: $(one_line "$MCP_RPCERR$MCP_TEXT" | cut -c1-160);"
  else
    bad="$bad M21: read 51 items: $MCP_WHY;"
  fi
  if mcp_call http harvester read "$(jq -cn '{publications: [range(21) | "10.1000/lane-m.\(.)"]}')"; then
    [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -qF 'publications takes at most 20 items; this call sent 21' || bad="$bad M21: 21 publications were not refused by the publications limit: $(one_line "$MCP_RPCERR$MCP_TEXT" | cut -c1-160);"
  else
    bad="$bad M21: read 21 publications: $MCP_WHY;"
  fi
  # M29 misplaced input — a per-item error that names the right field, beside
  # a good item (M21's isolation and order ride on the same call)
  if MCP_HTTP_TIMEOUT=120 mcp_call http harvester read "$(jq -cn --arg u "$RFC_DIRECT" --arg p "$SCRATCH/no-such-document.md" '{urls: [$u, $p, "10.1038/nphys1170"]}')"; then
    [ "$MCP_ISERR" = false ] || bad="$bad M21: failing items made the whole call isError;"
    [ "$(sfield '.urls | length')" = 3 ] || bad="$bad M21: three urls answered $(sfield '.urls | length') item(s), not one per url;"
    [ "$(item urls 0 .source)" = "$RFC_DIRECT" ] && [ -z "$(item urls 0 .error)" ] || bad="$bad M21: item order not kept or the good item failed beside the bad ones: $(one_line "$MCP_STRUCT" | cut -c1-160);"
    [ "$(item urls 1 .error)" = 'this is a local path; put it in files.' ] || bad="$bad M29: a local path given in urls does not say 'this is a local path; put it in files.': $(one_line "$(item urls 1 .error)");"
    [ "$(item urls 2 .error)" = 'this is a DOI; put it in publications.' ] || bad="$bad M29: a DOI given in urls does not say 'this is a DOI; put it in publications.': $(one_line "$(item urls 2 .error)");"
    [ "$(text_line 1)" = '## urls (3)' ] || bad="$bad M21: three urls do not open the text with '## urls (3)': $(one_line "$(text_line 1)");"
  else
    bad="$bad M29: read misplaced items: $MCP_WHY;"
  fi
  # M22 search_literature — either typed candidates, each with a handle, or the named empty answer
  if MCP_HTTP_TIMEOUT=120 mcp_call http harvester search_literature '{"query":"Attention Is All You Need","limit":3,"type":"paper"}'; then
    if [ "$MCP_ISERR" != false ]; then
      bad="$bad M22: search_literature isError (discovery failed): $(one_line "$MCP_TEXT" | cut -c1-160);"
    elif [ "$(sfield '.candidates | length')" -gt 0 ] 2>/dev/null; then
      [ "$(sfield '[.candidates[] | select((.handle // "") == "")] | length')" = 0 ] || bad="$bad M22: a candidate carries no handle: $(one_line "$MCP_STRUCT" | cut -c1-160);"
      printf '%s' "$(text_line 1)" | grep -qE '^[0-9]+ candidate work\(s\) for "Attention Is All You Need"' || bad="$bad M22: the readable text does not count the candidates: $(one_line "$(text_line 1)");"
      find_note="candidates"
    elif [ "$(sfield '.candidates | length')" = 0 ] && printf '%s' "$(text_line 1)" | grep -q '^No candidate works found for "Attention Is All You Need"'; then
      find_note="none found (named)"
    else
      bad="$bad M22: search_literature answered neither typed candidates nor the named empty answer: $(one_line "$MCP_TEXT" | cut -c1-160);"
    fi
  else
    bad="$bad M22: search_literature: $MCP_WHY;"
  fi
  if mcp_call http harvester search_literature '{"query":"   "}'; then
    [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -q 'query must not be empty' || bad="$bad M22: a blank query was not refused by name: $(one_line "$MCP_TEXT");"
  else
    bad="$bad M22: search_literature blank: $MCP_WHY;"
  fi
  if mcp_call http harvester search_literature '{"query":"x","type":"article"}'; then
    [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -qF 'type must be any, paper or book, got "article"' || bad="$bad M22: an unknown type was not refused by name: $(one_line "$MCP_TEXT");"
  else
    bad="$bad M22: search_literature type: $MCP_WHY;"
  fi
  # M23/M24 search_web — served only when configured; unconfigured it is hidden
  # from tools/list and a call is the protocol's unknown-tool error; configured,
  # a backend failure is an isError RESULT (data), never a Go error
  search_cfg=false
  if [ -f "$HARVESTER_CFG" ]; then
    search_cfg="$(jq -r '((.search.enabled // true) and (((.search.searxngURL // "") != "") or ((.search.braveApiKey // "") != ""))) | tostring' "$HARVESTER_CFG" 2>/dev/null || echo false)"
  fi
  if mcp_tools http harvester; then
    harv_tools="$MCP_TOOLS"
    search_served=false
    printf '%s\n' "$harv_tools" | grep -qx search_web && search_served=true
    [ "$search_served" = "$search_cfg" ] || bad="$bad M23: search_web served=$search_served while harvester.config.json says configured=$search_cfg;"
    if mcp_call http harvester search_web '{"query":"HTCPCP teapot"}'; then
      if [ "$search_cfg" = true ]; then
        [ -z "$MCP_RPCERR" ] || bad="$bad M23: a configured search_web answered a protocol error: $MCP_RPCERR;"
        if [ "$MCP_ISERR" = true ]; then
          printf '%s' "$MCP_TEXT" | grep -q '^Web search failed' || bad="$bad M24: the search_web failure is isError but not the named 'Web search failed' rendering: $(one_line "$MCP_TEXT");"
          search_note="configured, backend failed as data (isError, no protocol error)"
        else
          search_note="configured, answered results"
        fi
        if mcp_call http harvester search_web '{"query":"x","limit":21}'; then
          [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -qF 'limit must be between 1 and 20' || bad="$bad M23: search_web limit 21 was not refused by name: $(one_line "$MCP_TEXT");"
        else
          bad="$bad M23: search_web limit: $MCP_WHY;"
        fi
      else
        printf '%s' "$MCP_RPCERR" | grep -q 'unknown tool "search_web"' || bad="$bad M23: with search_web unconfigured a call answered '$MCP_RPCERR' / isError=$MCP_ISERR (want the protocol's unknown tool \"search_web\");"
        search_note="unconfigured, hidden and unknown to tools/call"
      fi
    else
      bad="$bad M23: search_web call: $MCP_WHY;"
    fi
  else
    bad="$bad M23: harvester tools/list: $MCP_WHY;"
  fi
  # M25 download_file — a real file lands in the binary cache: path, kind, type,
  # bytes and sha256 agree with the file on disk; a local path is its own
  # error item naming read's files (M29)
  if MCP_HTTP_TIMEOUT=180 mcp_call http harvester download_file "$(jq -cn --arg u "$RFC_DIRECT" --arg p "$NOTE" '{urls: [$u, $p]}')"; then
    [ "$MCP_ISERR" = false ] || bad="$bad M25: download_file isError: $(one_line "$MCP_TEXT" | cut -c1-160);"
    dl_path="$(item items 0 .path)"
    if [ -n "$(item items 0 .error)" ] || [ ! -f "$dl_path" ]; then
      bad="$bad M25: download_file $RFC_DIRECT left no file on disk: $(one_line "$MCP_STRUCT" | cut -c1-200);"
    else
      [ "$(item items 0 .bytes)" = "$(wc -c <"$dl_path" | tr -d ' ')" ] || bad="$bad M25: bytes $(item items 0 .bytes) differs from the file's size;"
      [ "$(item items 0 .sha256)" = "$(sha256sum "$dl_path" | cut -d' ' -f1)" ] || bad="$bad M25: sha256 differs from the file's own hash;"
      [ -n "$(item items 0 .kind)" ] && [ -n "$(item items 0 .content_type)" ] || bad="$bad M25: the item names no kind or content_type: $(one_line "$MCP_STRUCT" | cut -c1-160);"
      [ -n "$(item items 0 .via)" ] || bad="$bad M25: the item names no via: $(one_line "$MCP_STRUCT" | cut -c1-160);"
    fi
    [ "$(item items 1 .error)" = 'this is a local path; download_file takes URLs — read a local document with `read` (files).' ] || bad="$bad M29: a local path given to download_file does not name read's files: $(one_line "$(item items 1 .error)");"
  else
    bad="$bad M25: download_file: $MCP_WHY;"
  fi
  # H13 `pfm harvest download-file` — the CLI face of the same download: a JSON
  # receipt per item; a refused header exits 2 before any request
  dl_json="$(pfm harvest download-file --json "$RFC_DIRECT" 2>"$SCRATCH/dl.err")"
  dl_rc=$?
  cli_path="$(printf '%s' "$dl_json" | jq -r '.[0].path // empty' 2>/dev/null)"
  [ "$dl_rc" -eq 0 ] && [ -f "$cli_path" ] && [ "$(printf '%s' "$dl_json" | jq -r '.[0].bytes')" = "$(wc -c <"$cli_path" | tr -d ' ')" ] ||
    bad="$bad H13: pfm harvest download-file --json exited $dl_rc with '$(one_line "$dl_json" | cut -c1-160)' $(one_line "$(cat "$SCRATCH/dl.err")" | cut -c1-120) (want exit 0 and a path whose size is bytes);"
  pfm harvest download-file --header 'Host: example.org' "$RFC_DIRECT" >/dev/null 2>"$SCRATCH/dl.err"
  dl_rc=$?
  [ "$dl_rc" -eq 2 ] && grep -q 'caller header refused' "$SCRATCH/dl.err" || bad="$bad H13: --header 'Host: …' exited $dl_rc '$(one_line "$(cat "$SCRATCH/dl.err")")' (want 2 naming the refused header);"
  # M26 read {files} — a local file is read via local; a missing one is its
  # own error item; a URL names urls (M29)
  if mcp_call http harvester read "$(jq -cn --arg n "$NOTE" --arg m "$SCRATCH/no-such-document.md" --arg u "$RFC_DIRECT" '{files: [$n, $m, $u]}')"; then
    [ "$MCP_ISERR" = false ] || bad="$bad M26: read files isError: $(one_line "$MCP_TEXT" | cut -c1-160);"
    item files 0 .content | grep -qF "SENTINEL-LANE-M-$$" || bad="$bad M26: the local note did not come back with its sentinel: $(one_line "$MCP_STRUCT" | cut -c1-160);"
    [ "$(item files 0 .via)" = local ] || bad="$bad M26: the local item's via is '$(item files 0 .via)', not local;"
    [ -n "$(item files 1 .error)" ] || bad="$bad M26: the missing local path is not an error item;"
    [ "$(item files 2 .error)" = 'this is a URL; put it in urls.' ] || bad="$bad M29: a URL given in files does not say 'this is a URL; put it in urls.': $(one_line "$(item files 2 .error)");"
    [ "$(text_line 1)" = '## files (3)' ] || bad="$bad M26: three files do not open the text with '## files (3)': $(one_line "$(text_line 1)");"
  else
    bad="$bad M26: read files: $MCP_WHY;"
  fi
  # M27 read {publications} — an arXiv id resolves to a cached work with its ids and via
  if MCP_HTTP_TIMEOUT=240 mcp_call http harvester read '{"publications":["arXiv:1706.03762"],"include_content":false}'; then
    if [ "$MCP_ISERR" != false ] || [ -n "$(item publications 0 .error)" ]; then
      bad="$bad M27: read publications arXiv:1706.03762 failed: $(one_line "$MCP_TEXT" | cut -c1-200);"
    else
      [ "$(item publications 0 .ids.arxiv)" = 1706.03762 ] || bad="$bad M27: the item's ids do not name arxiv 1706.03762: $(one_line "$MCP_STRUCT" | cut -c1-160);"
      [ -f "$(item publications 0 .path)" ] && [ "$(item publications 0 .chars)" -gt 0 ] 2>/dev/null && [ -n "$(item publications 0 .via)" ] || bad="$bad M27: the work item carries no path, chars or via: $(one_line "$MCP_STRUCT" | cut -c1-160);"
    fi
  else
    bad="$bad M27: read publications: $MCP_WHY;"
  fi
  # M28 caller headers — on read and download_file only; a refused
  # header is a named tool error; a bare identifier with headers is a per-item
  # error; a headered read is its own cache partition and never echoes a value
  if mcp_http harvester tools/list '{}'; then
    for t in read download_file; do
      printf '%s' "$MCP_OUT" | jq -e --arg t "$t" '.result.tools[] | select(.name == $t) | .inputSchema.properties.headers' >/dev/null 2>&1 || bad="$bad M28: $t takes no headers;"
    done
    for t in search_literature search_web; do
      printf '%s' "$MCP_OUT" | jq -e --arg t "$t" '.result.tools[] | select(.name == $t) | .inputSchema.properties.headers' >/dev/null 2>&1 && bad="$bad M28: $t takes headers;"
    done
  else
    bad="$bad M28: harvester tools/list: $MCP_WHY;"
  fi
  if mcp_call http harvester read "$(jq -cn --arg u "$RFC_DIRECT" '{urls: [$u], headers: {Host: "example.org"}}')"; then
    [ "$MCP_ISERR" = true ] && printf '%s' "$MCP_TEXT" | grep -q 'caller header refused: Host' || bad="$bad M28: a Host header was not refused by name: $(one_line "$MCP_TEXT" | cut -c1-160);"
  else
    bad="$bad M28: read Host header: $MCP_WHY;"
  fi
  if mcp_call http harvester read '{"publications":["arXiv:1706.03762"],"headers":{"X-Lane-M":"1"}}'; then
    [ "$MCP_ISERR" = false ] && item publications 0 .error | grep -q 'pass the landing URL' || bad="$bad M28: a bare identifier with headers is not the per-item 'pass the landing URL' error: $(one_line "$MCP_STRUCT$MCP_TEXT" | cut -c1-160);"
  else
    bad="$bad M28: read publications with headers: $MCP_WHY;"
  fi
  if [ -n "$RFC_PATH" ] && MCP_HTTP_TIMEOUT=180 mcp_call http harvester read "$(jq -cn --arg u "$RFC_DIRECT" --arg v "lane-m-secret-$$" '{urls: [$u], include_content: false, headers: {"X-Lane-M": $v}}')"; then
    [ -z "$(item urls 0 .error)" ] && [ "$(item urls 0 .cached)" = false ] || bad="$bad M28: a headered read of a page cached without headers was served from that cache (or failed): $(one_line "$MCP_STRUCT" | cut -c1-160);"
    printf '%s' "$MCP_OUT" | grep -qF "lane-m-secret-$$" && bad="$bad M28: the header value came back in the result;"
  elif [ -n "$RFC_PATH" ]; then
    bad="$bad M28: headered read: $MCP_WHY;"
  fi
fi
if [ -n "$bad" ]; then fail "$bad"; else
  pass "read urls $RFC_DIRECT (typed item + body, via, gaps list, cached on re-read, include_content false, per-item errors beside a good item) · search_literature: ${find_note:-?} · search_web: ${search_note:-?} · download_file path/bytes/sha256 + pfm harvest download-file · read files via local · read publications arXiv ids + via · caller headers refused by name, partitioned, never echoed · misplaced items name the right field"
fi

# ─── M.12 — the cache and the search gate back the tools ────────────────────

beat M.12-harvester-cache-gate H10 H11
spends none
if requires M.11-harvester-tools; then
  bad=""
  # H10: the artifact read wrote is what an include_content false re-read reports as cached
  [ -n "$RFC_PATH" ] && [ -f "$RFC_PATH" ] || bad="$bad H10: no cached artifact path from M.11 ($RFC_PATH);"
  case "$RFC_PATH" in "$HOME"/*) ;; *) bad="$bad H10: the cache artifact $RFC_PATH lives outside \$HOME — not the local cache;" ;; esac
  grep -qi 'coffee' "$RFC_PATH" 2>/dev/null || bad="$bad H10: the cached markdown $RFC_PATH does not carry the document;"
  if mcp_call http harvester read "$(jq -cn --arg u "$RFC_DIRECT" '{urls: [$u], include_content: false}')"; then
    [ "$(item urls 0 .cached)" = true ] && [ "$(item urls 0 .path)" = "$RFC_PATH" ] || bad="$bad H10: an include_content false re-read is not the cached artifact $RFC_PATH: $(one_line "$MCP_STRUCT" | cut -c1-160);"
  else
    bad="$bad H10: read include_content false: $MCP_WHY;"
  fi
  # H11: one gate, three surfaces — the config, the served roster, the server's
  # own instructions, and the daemon's /status roster must all agree
  search_cfg=false
  [ -f "$HARVESTER_CFG" ] && search_cfg="$(jq -r '((.search.enabled // true) and (((.search.searxngURL // "") != "") or ((.search.braveApiKey // "") != ""))) | tostring' "$HARVESTER_CFG" 2>/dev/null || echo false)"
  if mcp_http harvester tools/list '{}'; then
    served=false
    printf '%s' "$MCP_OUT" | jq -e '.result.tools[] | select(.name == "search_web")' >/dev/null 2>&1 && served=true
    [ "$served" = "$search_cfg" ] || bad="$bad H11: tools/list serves search_web=$served while the config gate says $search_cfg;"
    instr="$(printf '%s' "$MCP_INIT" | jq -r '.result.instructions // empty')"
    if [ "$search_cfg" = true ]; then
      printf '%s' "$instr" | grep -q 'Web search is not configured' && bad="$bad H11: search is configured but the server instructions still say it is not;"
      printf '%s' "$instr" | grep -q 'search_web' || bad="$bad H11: search is configured but the instructions do not name search_web;"
    else
      printf '%s' "$instr" | grep -q 'Web search is not configured on this server' || bad="$bad H11: search is unconfigured but the instructions do not say so: $(one_line "$instr" | cut -c1-120);"
    fi
    printf '%s' "$instr" | grep -qF '"read this local document" is read with its path in files (this machine only)' && printf '%s' "$instr" | grep -qF 'is download_file (the bytes, unparsed; nothing is converted)' ||
      bad="$bad H11: the local server's instructions do not route read's files and download_file: $(one_line "$instr" | cut -c1-160);"
    status_has_search=false
    daemon_status | jq -e '.servers.harvester[]? | select(. == "search_web")' >/dev/null 2>&1 && status_has_search=true
    [ "$status_has_search" = "$served" ] || bad="$bad H11: /status servers.harvester lists search_web=$status_has_search while tools/list serves search_web=$served — the daemon's status roster is a static list, not the served surface;"
  else
    bad="$bad H11: harvester tools/list: $MCP_WHY;"
  fi
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "cache artifact $RFC_PATH backs read (cached on an include_content false re-read); search_web gate config=$search_cfg agrees with tools/list, the server instructions and /status"
  fi
fi

# ─── M.13 — a dropped seat leaves the daemon's roster ───────────────────────

beat M.13-dropped-seat-roster I38
spends none
# the roster/effort refusals this beat provokes on purpose from the spare seat.
expect-log 'is not in the configured roster'
expect-log 'unknown Claude effort'
if [ -z "$SPARE" ]; then
  blocked "seats $LANE_SEATS" "no second seat in this run — dropping the only seat would take the lane's own chat with it (run with --seats cc:1,cc:2)"
else
  bad=""
  notes=""
  # probe <transport> — chat_new on the spare seat with a bogus effort: the
  # roster check runs BEFORE the effort check and both refuse before any spawn,
  # so the error text says which roster the server holds. Prints roster|stale|<other>.
  probe() {
    if ! mcp_call "$1" chat chat_new "$(jq -cn --arg c "$CWD" --argjson a "$SPARE" '{name: "M_DROPPED_PROBE", engine: "cc", account: $a, cwd: $c, effort: "lane-bogus"}')"; then
      printf 'transport: %s' "$MCP_WHY"
      return 0
    fi
    if printf '%s' "$MCP_TEXT" | grep -q "account $SPARE is not in the configured roster"; then printf 'dropped'
    elif printf '%s' "$MCP_TEXT" | grep -q 'unknown Claude effort'; then printf 'offered'
    else printf 'other: isError=%s %s' "$MCP_ISERR" "$(one_line "$MCP_TEXT" | cut -c1-120)"
    fi
  }
  control="$(probe stdio)"
  [ "$control" = offered ] || bad="$bad control: with seat $SPARE configured the probe must reach the effort check ('offered'), got '$control' — the probe cannot discriminate;"
  cp "$CONFIG" "$SCRATCH/pfm.config.json.m13"
  jq --argjson drop "$SPARE" '.accounts |= map(select(.id != $drop))' "$CONFIG" >"$CONFIG.tmp" && mv "$CONFIG.tmp" "$CONFIG"
  before_restart="$(probe http)"
  stdio_verdict="$(probe stdio)"
  [ "$stdio_verdict" = dropped ] || bad="$bad stdio (a fresh server per chat) still offers dropped seat $SPARE: '$stdio_verdict';"
  # The daemon read its roster at start; the installer's post-install
  # `systemctl restart pfm-mcp.service` is what refreshes it and this container
  # has no manager — so the restart is done by hand, as the supervisor would.
  if daemon_restart; then
    http_verdict="$(probe http)"
    [ "$http_verdict" = dropped ] || bad="$bad the restarted daemon still offers dropped seat $SPARE: '$http_verdict';"
  else
    bad="$bad daemon restart after the drop: $DAEMON_WHY;"
  fi
  notes="$notes before the restart the running daemon answered '$before_restart' (its roster is read at start — a supervisor's restart, not a config edit, refreshes it);"
  live_chat M_DROPPED_PROBE && bad="$bad a probe spawned M_DROPPED_PROBE — the refusal did not precede the spawn;"
  cp "$SCRATCH/pfm.config.json.m13" "$CONFIG"
  if daemon_restart; then
    restored="$(probe http)"
    [ "$restored" = offered ] || bad="$bad after the config was restored the daemon still refuses seat $SPARE: '$restored';"
  else
    bad="$bad daemon restart after the restore: $DAEMON_WHY;"
  fi
  if [ -n "$bad" ]; then fail "$bad · notes:$notes"; else
    pass "seat $SPARE dropped from the config → stdio server and the restarted daemon each refuse it by name, nothing spawned; restored, both offer it again · notes:$notes"
  fi
fi

# ─── M.14 — one chat-driven call per server, both engines ───────────────────

# e2e_drive <chat> <url> <needle> <rename> — the stimulus: the chat is asked to
# call chat_status on itself, rename itself (a chat_* call whose effect the
# fleet records), fetch one URL through the harvester (a call the cache
# records), then say the needle. Waits BY SESSION ID — the name changes under
# the wait. Returns 1 with E2E_WHY.
E2E_WHY=""
e2e_drive() {
  local chat="$1" url="$2" needle="$3" rename="$4" sid out
  E2E_WHY=""
  sid="$(live_field "$chat" 2)"
  [ -n "$sid" ] || { E2E_WHY="$chat has no live row to drive"; return 1; }
  cat >"$SCRATCH/stimulus-$chat.txt" <<EOF
Do exactly these steps in order, using tools only, and say nothing until the last step: (1) call the chat_status tool with target "self"; (2) call the chat_name tool with target "self" and name "$rename"; (3) call the harvester read tool with urls ["$url"]; (4) reply with exactly one word: $needle
EOF
  out="$(pfm chat inject --allow-unsigned --file "$SCRATCH/stimulus-$chat.txt" "$chat" 2>&1)" || {
    E2E_WHY="pfm chat inject into $chat refused the stimulus: $(one_line "$out")"
    return 1
  }
  wait_last "$sid" "$needle" 420
  case $? in
    0) return 0 ;;
    2) E2E_WHY="$LANE_WAIT_WHY (waiting for $needle from $chat)"; return 1 ;;
    *) E2E_WHY="no $needle from $chat in 420s; its last: $(one_line "$(pfm chat last "$sid" 2>&1)" | cut -c1-160)"; return 1 ;;
  esac
}
# cache_lists <url> — 0 when an include_content false read of that exact URL reports
# `cached`. The URL carries this run's stamp, so no earlier run cached it; the
# probe itself reads and caches a miss, so it is asked ONCE, after the chat's
# own call, never polled and never before the stimulus.
cache_lists() {
  mcp_call http harvester read "$(jq -cn --arg u "$1" '{urls: [$u], include_content: false}')" || return 1
  [ "$(item urls 0 .cached)" = true ]
}
# e2e_evidence <chat> <sid> <sock> <url> <rename> — the fleet's and the cache's
# own records of the two calls; renames the chat back. Appends to $bad.
e2e_evidence() {
  local chat="$1" sid="$2" sock="$3" url="$4" rename="$5" back
  [ "$(socket_field "$sock" 5)" = "$rename" ] ||
    bad="$bad $chat: the fleet row on $sock reads '$(socket_field "$sock" 5)', not '$rename' — its chat_name self call left no record;"
  if ! cache_lists "$url"; then
    bad="$bad $chat: the harvester cache carries no page for $url after the chat's read (read include_content false: ${MCP_WHY:-$(one_line "$MCP_STRUCT$MCP_TEXT" | cut -c1-160)});"
  fi
  back="$(pfm chat name "$sid" "$chat" 2>&1)" || bad="$bad $chat: could not be renamed back (pfm chat name exited non-zero: $(one_line "$back"));"
  sleep 2
  live_chat "$chat" || bad="$bad $chat: no live row under its own name after the rename back;"
}

beat M.14-end-to-end
spends "cc:$SEAT+cx"
target_live "$CHAT"
if requires; then
  bad=""
  sid="$(live_field "$CHAT" 2)"
  sock="$(live_field "$CHAT" 11)"
  anchor_socket "$sock" # the chat renames itself mid-beat; the socket is the handle
  if e2e_drive "$CHAT" "$RFC_CLAUDE" MCP-E2E-CLAUDE "$CHAT:e2e"; then
    e2e_evidence "$CHAT" "$sid" "$sock" "$RFC_CLAUDE" "$CHAT:e2e"
  else
    bad="$bad Claude ($CHAT, stdio chat + HTTP harvester): $E2E_WHY;"
  fi
  if live_chat "$E2_CHAT"; then
    e2_sid="$(live_field "$E2_CHAT" 2)"
    e2_sock="$(live_field "$E2_CHAT" 11)"
    if e2e_drive "$E2_CHAT" "$RFC_CODEX" MCP-E2E-CODEX "$E2_CHAT:e2e"; then
      e2e_evidence "$E2_CHAT" "$e2_sid" "$e2_sock" "$RFC_CODEX" "$E2_CHAT:e2e"
    else
      bad="$bad Codex ($E2_CHAT, HTTP both): $E2E_WHY;"
    fi
  else
    bad="$bad Codex: $E2_CHAT has no live row (${E2_WHY:-it died after the prelude}) — the HTTP wiring was NOT proven;"
  fi
  if [ -n "$bad" ]; then fail "$bad"; else
    pass "$CHAT (Claude) and $E2_CHAT (Codex) each renamed themselves through chat_name self (recorded in pfm ls) and fetched their RFC through the daemon (recorded in the harvester cache); renamed back"
  fi
fi

# ─── M.15 — cross-lane: live chats survive the daemon restart ───────────────

beat M.15-live-chats-survive-daemon-restart M31 M34
spends "cc:$SEAT+cx"
target_live "$E1_CHAT"
if [ -n "$E1_WHY" ] && ! live_chat "$E1_CHAT"; then
  blocked "need $E1_CHAT" "$E1_WHY"
elif requires M.05-daemon-core; then
  bad=""
  if ! live_chat "$E2_CHAT"; then
    blocked "need $E2_CHAT" "${E2_WHY:-$E2_CHAT has no live row — the Codex chat of lane E2 could not be asserted after the restart}"
  else
    e1_sid="$(live_field "$E1_CHAT" 2)"
    e1_sock="$(live_field "$E1_CHAT" 11)"
    e2_sid="$(live_field "$E2_CHAT" 2)"
    e2_sock="$(live_field "$E2_CHAT" 11)"
    anchor_socket "$e1_sock"
    if e2e_drive "$E1_CHAT" "$RFC_E1" MCP-E2E-E1 "$E1_CHAT:e2e"; then
      e2e_evidence "$E1_CHAT" "$e1_sid" "$e1_sock" "$RFC_E1" "$E1_CHAT:e2e"
    else
      bad="$bad E1's Claude chat (stdio chat server, HTTP harvester): $E2E_WHY;"
    fi
    if e2e_drive "$E2_CHAT" "$RFC_E2" MCP-E2E-E2 "$E2_CHAT:e2e"; then
      e2e_evidence "$E2_CHAT" "$e2_sid" "$e2_sock" "$RFC_E2" "$E2_CHAT:e2e"
    else
      bad="$bad E2's Codex chat (HTTP chat + harvester, its session died with the old daemon): $E2E_WHY;"
    fi
    if [ -n "$bad" ]; then fail "$bad"; else
      pass "after M.05's exit-75 restart, $E1_CHAT (Claude, stdio) and $E2_CHAT (Codex, HTTP) each made their next chat_* call (self-rename recorded in pfm ls) and harvester read (recorded in the cache) through the new daemon"
    fi
  fi
fi

lane_end

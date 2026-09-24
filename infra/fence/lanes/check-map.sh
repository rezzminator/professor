#!/usr/bin/env bash
# check-map.sh — the map gate: every pfm command and MCP tool has a beat that
# tests it, and every beat map.tsv names exists.
#
#   check-map.sh [--pfm PATH] [--no-derive]
#
# map.tsv is `name · lane · beat`: a command (`pfm config show`,
# `pfm chat new`, `pfm internal git-guard`) or an MCP tool (`chat_inject`,
# `read`), the lane script that tests it and the beat inside it. A beat that
# tests no command (an install behaviour, a fleet capability) needs no row.
#
# Two checks, each naming its own broken state:
#   1. every beat in map.tsv exists in its lane script. A lane that is not
#      written yet is a named line (`lane F: NOT WRITTEN (12 beats pending)`)
#      and is tolerated ONLY while pending.txt lists it — and pending.txt must
#      shrink to empty: a pending lane whose script exists is a red row.
#      → `MISSING-BEAT: <beat>` / `PENDING-STALE: <lane>` / `UNDECLARED-LANE: <lane>`
#   2. machine-derived, so the map cannot drift from pfm: every command in the
#      built `pfm --help` tree and every tool served by pfm's two MCP servers
#      has a map.tsv row, and every row names a command or tool pfm still has.
#      → `UNMAPPED-COMMAND: <name>` / `UNMAPPED-TOOL: <name>` /
#        `STALE-NAME: <name>` (a row for a command or tool pfm no longer serves)
#      A command row absent from the help tree (a `pfm internal` verb, a
#      hidden verb or alias) is asked of pfm itself: stale only when pfm
#      answers `unknown command` / `unknown subcommand`.
#
# The derive runs the REAL binary in a jail ($PFM_HOME and friends point at a
# scratch dir, the config is a temp file with both servers enabled), so it never
# reads or writes the host's fleet. The tool names come from a `tools/list`
# JSON-RPC call over each server's stdio transport — the served surface, not a
# list copied into this script.
#
# BROKEN STATE: a derive that cannot run — no pfm binary, no jq, a server that
# answers nothing — prints `DERIVE-FAILED: <why>` and exits 2. It never prints a
# clean verdict for a check it could not perform. `--no-derive` prints
# `derive: NOT RUN` and keeps check 1; exit 1 on any map finding, 2 on a
# failed derive or an unreadable map (failing to look outranks a finding), 0
# only when every check ran.
set -uo pipefail

HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
MAP="$HERE/map.tsv"
PENDING="$HERE/pending.txt"
PFM="" DERIVE=1

while [ $# -gt 0 ]; do
  case "$1" in
    --pfm) PFM="$2"; shift 2 ;;
    --no-derive) DERIVE=0; shift ;;
    -h|--help) sed -n '2,5p' "$0"; exit 0 ;;
    *) echo "usage: check-map.sh [--pfm PATH] [--no-derive]" >&2; exit 2 ;;
  esac
done

map_bad=0 derive_failed=0
red() { printf 'check-map: ✗ %s\n' "$1" >&2; map_bad=$((map_bad + 1)); }
say() { printf 'check-map: %s\n' "$1"; }
derive_fail() { printf 'check-map: DERIVE-FAILED: %s\n' "$1" >&2; derive_failed=1; }

[ -f "$MAP" ] || { echo "check-map: MAP-UNREADABLE — $MAP does not exist" >&2; exit 2; }
[ "$(head -n 1 "$MAP")" = "$(printf 'name\tlane\tbeat')" ] ||
  { echo "check-map: MAP-UNREADABLE — line 1 of $(basename "$MAP") is not the 'name<TAB>lane<TAB>beat' header" >&2; exit 2; }
n_rows="$(awk -F'\t' 'NR > 1 && NF == 3' "$MAP" | grep -c .)"
[ "$n_rows" -gt 0 ] || { echo "check-map: MAP-UNREADABLE — $(basename "$MAP") has no 'name<TAB>lane<TAB>beat' rows (the enumerator found nothing to check)" >&2; exit 2; }
awk -F'\t' 'NR > 1 && NF != 3 { printf "%d\n", NR }' "$MAP" | while read -r n; do
  printf 'check-map: ✗ MALFORMED-ROW: line %s of %s is not three tab-separated fields\n' "$n" "$(basename "$MAP")" >&2
done
n_malformed="$(awk -F'\t' 'NR > 1 && NF != 3' "$MAP" | grep -c .)"
map_bad=$((map_bad + n_malformed))
say "$n_rows map rows"

# ─── 1. every mapped beat exists (or its lane is declared pending) ──────────

written_lane() { [ -f "$HERE/$1.sh" ]; }
pending_lane() { grep -qx "$1" "$PENDING" 2>/dev/null; }

for lane in $(awk -F'\t' 'NR > 1 && NF == 3 { print $2 }' "$MAP" | sort -u); do
  n_beats="$(awk -F'\t' -v l="$lane" '$2 == l { print $3 }' "$MAP" | sort -u | grep -c .)"
  if written_lane "$lane"; then
    if pending_lane "$lane"; then
      red "PENDING-STALE: $PENDING lists $lane but $lane.sh exists — the pending list must shrink to empty"
    fi
    missing=0
    for beat in $(awk -F'\t' -v l="$lane" '$2 == l { print $3 }' "$MAP" | sort -u); do
      grep -qE "^[[:space:]]*beat $beat( |$)" "$HERE/$lane.sh" && continue
      red "MISSING-BEAT: $beat is mapped to lane $lane but $lane.sh has no 'beat $beat' line"
      missing=$((missing + 1))
    done
    say "lane $lane: written · $((n_beats - missing))/$n_beats mapped beats present"
  elif pending_lane "$lane"; then
    say "lane $lane: NOT WRITTEN ($n_beats beats pending) — declared in $(basename "$PENDING")"
  else
    red "UNDECLARED-LANE: $lane has neither $lane.sh nor a line in $(basename "$PENDING")"
  fi
done
if [ -f "$PENDING" ]; then
  n_pending="$(grep -c . "$PENDING" || true)"
  say "pending lanes: ${n_pending} — this list must reach 0"
fi

# ─── 2. the machine-derived surface ─────────────────────────────────────────

names="$(awk -F'\t' 'NR > 1 && NF == 3 { print $1 }' "$MAP" | sort -u)"
# mapped <name> — 0 when a row names it or one of its subcommands
mapped() { printf '%s\n' "$names" | grep -qE "^$1( |$)"; }

if [ "$DERIVE" -eq 0 ]; then
  say "derive: NOT RUN (--no-derive) — pfm's command tree and MCP tool surface were NOT checked against the map"
else
  [ -n "$PFM" ] || PFM="${LANE_PFM_BIN:-$(command -v pfm 2>/dev/null || true)}"
  if [ -z "$PFM" ] || [ ! -x "$PFM" ]; then
    derive_fail "no pfm binary to ask (--pfm PATH, \$LANE_PFM_BIN, or pfm on PATH); build one: dev.sh iso run 'cd pfm && go build -o /tmp/pfm ./cmd/pfm'"
  elif ! command -v jq >/dev/null; then
    derive_fail "TOOLCHAIN-MISSING — jq (the MCP tools/list answer cannot be read)"
  else
    JAIL="$(mktemp -d "${TMPDIR:-/tmp}/lane-checkmap.XXXXXX")"
    trap 'rm -rf "$JAIL"' EXIT
    cat >"$JAIL/pfm.config.json" <<'JSON'
{"version": 2, "mcp": {"servers": {"chat": {"enabled": true}, "harvester": {"enabled": true}}}}
JSON
    # A search backend configured, so the harvester serves search_web too.
    printf '%s\n' '{"search": {"searxngURL": "http://127.0.0.1:9"}}' >"$JAIL/harvester.config.json"
    # Jailed: the derive must never touch the host's fleet, socket dir or tmux.
    pfm_jailed() {
      env PFM_HOME="$JAIL/home" PFM_DB="$JAIL/index.db" PFM_FLEET_DB="$JAIL/fleet.db" \
        PFM_SID_DIR="$JAIL/sid" PFM_TMUX_DIR="$JAIL/tmux" PFM_TMUX_CONF=/dev/null \
        "$PFM" --config "$JAIL/pfm.config.json" "$@"
    }
    with_timeout() { if command -v timeout >/dev/null; then timeout "$@"; else shift; "$@"; fi; }
    pfm_jailed_timed() {
      with_timeout 10 env PFM_HOME="$JAIL/home" PFM_DB="$JAIL/index.db" PFM_FLEET_DB="$JAIL/fleet.db" \
        PFM_SID_DIR="$JAIL/sid" PFM_TMUX_DIR="$JAIL/tmux" PFM_TMUX_CONF=/dev/null \
        "$PFM" --config "$JAIL/pfm.config.json" "$@"
    }

    cmds_ok=0
    top_help="$(pfm_jailed --help 2>&1)"
    chat_help="$(pfm_jailed chat --help 2>&1)"
    cmds="$(printf '%s\n' "$top_help" | awk '/^  [a-z]/ { print $1 }' | sort -u)"
    subs="$(printf '%s\n' "$chat_help" | awk '/^  [a-z]/ { print $1 }' | tr '/' '\n' | sort -u)"
    n_cmds="$(printf '%s\n' "$cmds" | grep -c .)"
    n_subs="$(printf '%s\n' "$subs" | grep -c .)"
    if [ "$n_cmds" -lt 5 ] || [ "$n_subs" -lt 5 ]; then
      derive_fail "pfm --help produced $n_cmds command(s) and pfm chat --help $n_subs subcommand(s) — the help tree could not be read: $(printf '%s' "$top_help" | tr '\n' ' ' | cut -c1-200)"
    else
      cmds_ok=1
      bad_cmds=0
      for c in $cmds; do
        mapped "pfm $c" && continue
        red "UNMAPPED-COMMAND: pfm $c — no row in $(basename "$MAP") names it"
        bad_cmds=$((bad_cmds + 1))
      done
      for c in $subs; do
        mapped "pfm chat $c" && continue
        red "UNMAPPED-COMMAND: pfm chat $c — no row in $(basename "$MAP") names it"
        bad_cmds=$((bad_cmds + 1))
      done
      say "derived commands: $n_cmds top-level + $n_subs chat subcommands · $bad_cmds unmapped"
    fi

    # tools/list over each server's stdio transport: the served surface itself.
    # The frames are written newline-terminated and stdin is HELD OPEN for a
    # moment after them — on EOF the server closes at once ("server is closing:
    # EOF") and a response still in flight is lost, which reads exactly like a
    # server with no tools.
    tools_of() { # tools_of <server>
      local out
      out="$( {
        printf '%s\n' \
          '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"check-map","version":"0"}}}' \
          '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
          '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
        sleep 5
      } | with_timeout 60 env PFM_HOME="$JAIL/home" PFM_DB="$JAIL/index.db" \
        PFM_FLEET_DB="$JAIL/fleet.db" PFM_SID_DIR="$JAIL/sid" PFM_TMUX_DIR="$JAIL/tmux" PFM_TMUX_CONF=/dev/null \
        "$PFM" --config "$JAIL/pfm.config.json" mcp "$1" serve 2>"$JAIL/$1.err")"
      printf '%s\n' "$out" >"$JAIL/$1.frames"
      printf '%s\n' "$out" | jq -r 'select(.id == 2) | .result.tools[]?.name' 2>/dev/null
    }
    tools_ok=1 all_tools=""
    for server in chat harvester; do
      tools="$(tools_of "$server")"
      n_tools="$(printf '%s\n' "$tools" | grep -c .)"
      if [ "$n_tools" -eq 0 ]; then
        derive_fail "the $server MCP server listed no tool over stdio — stderr: $(tr '\n' ' ' <"$JAIL/$server.err" 2>/dev/null | cut -c1-200); frames it did answer: $(tr '\n' ' ' <"$JAIL/$server.frames" 2>/dev/null | cut -c1-200)"
        tools_ok=0
        continue
      fi
      all_tools="$all_tools $tools"
      bad_tools=0
      for t in $tools; do
        mapped "$t" && continue
        red "UNMAPPED-TOOL: $server/$t — no row in $(basename "$MAP") names it"
        bad_tools=$((bad_tools + 1))
      done
      say "derived $server tools: $n_tools · $bad_tools unmapped"
    done

    # The reverse: a row for a command or tool pfm no longer serves is stale —
    # a removed command takes its map row with it. Judged only against a
    # surface that was actually read, never against a failed derive.
    if [ "$cmds_ok" -eq 1 ] && [ "$tools_ok" -eq 1 ]; then
      stale=0 hidden=0
      for n in $(printf '%s\n' "$names" | tr ' ' '~'); do
        n="${n//\~/ }"
        set -- $n
        case "$1 ${2:-}" in
          "pfm chat") [ -n "${3:-}" ] && printf '%s\n' "$subs" | grep -qxF "$3" && continue ;;
          "pfm internal") ;;
          pfm\ *) [ $# -eq 2 ] && printf '%s\n' "$cmds" | grep -qxF "$2" && continue ;;
          *) [ $# -eq 1 ] && printf '%s\n' $all_tools | grep -qxF "$1" && continue ;;
        esac
        # Outside the help tree (an internal verb, a hidden verb or alias):
        # pfm's own dispatcher decides; only its "unknown" answer is stale.
        if [ "$1" = pfm ] && [ $# -ge 2 ]; then
          shift
          # Captured first: `grep -q` quitting early would SIGPIPE the writer,
          # and pipefail would read that as "not unknown".
          answer="$(pfm_jailed_timed "$@" --help </dev/null 2>&1)"
          if ! printf '%s\n' "$answer" | grep -qE 'unknown (sub)?command'; then
            hidden=$((hidden + 1)); continue
          fi
        fi
        red "STALE-NAME: $n — a $(basename "$MAP") row names a command or tool pfm does not serve"
        stale=$((stale + 1))
      done
      say "map names: $(printf '%s\n' "$names" | grep -c .) · $stale stale · $hidden judged by pfm's dispatcher (verbs the help tree does not list)"
    fi
  fi
fi

if [ "$derive_failed" -ne 0 ]; then
  echo "check-map: a check could NOT be run (the FAILED line above) — this is not a clean verdict" >&2
  exit 2
fi
if [ "$map_bad" -ne 0 ]; then
  echo "check-map: ✗ $map_bad finding(s)" >&2
  exit 1
fi
if [ "$DERIVE" -eq 0 ]; then
  say "clean — every mapped beat present or its lane declared pending; the derived surface was NOT checked"
else
  say "clean — every mapped beat present or its lane declared pending, every derived command and tool mapped, no stale row"
fi
exit 0

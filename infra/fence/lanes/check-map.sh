#!/usr/bin/env bash
# check-map.sh — the map gate: the landscape, the beats and pfm's own surface
# must agree, or the coverage claim is a coincidence.
#
#   check-map.sh [--pfm PATH] [--no-derive]
#
# Four checks, each naming its own broken state:
#   0. docs/dev/testing/landscape.md is machine-read: line 1 carries
#      `<!-- rumdl-disable -->`, the inline marker rumdl honours in `fmt` as well
#      as `check`, so the format-md hook and a bare `rumdl fmt` both leave its
#      bare `<id> ·` rows alone (measured 2026-09-18: unmarked, 429 rows reflowed
#      into 27). With rumdl on PATH the check also formats a COPY and demands
#      byte-identity; without it, that half is a named NOT-PERFORMED line.
#      → `LANDSCAPE-FORMATTABLE` (no marker) / `LANDSCAPE-MUTABLE` (rumdl
#        changed the copy anyway) / `LANDSCAPE-FMT-FAILED` (rumdl would not run)
#   1. every id in docs/dev/testing/landscape.md has a row in map.tsv
#      → `UNMAPPED-ID: <id>`
#   2. BOTH directions between map.tsv and a written lane script agree, or the
#      gate is a coincidence detector that can only ever look one way:
#        a. every beat in map.tsv exists in its lane script. A lane that is not
#           written yet is a named line (`lane F: NOT WRITTEN (12 beats
#           pending)`) and is tolerated ONLY while pending.txt lists it — and
#           pending.txt must shrink to empty: a pending lane whose script
#           exists is a red row.
#           → `MISSING-BEAT: <beat>` / `PENDING-STALE: <lane>` /
#             `UNDECLARED-LANE: <lane>`
#        b. every `beat <ID>` line in a WRITTEN lane script has a map.tsv row —
#           unless beats.md marks that beat's landscape ids `(none)`, in which
#           case the beat is deliberately code-only and this check leaves it
#           alone. A beat added with real landscape ids but no map.tsv row is
#           otherwise invisible to direction (a) forever, since (a) only ever
#           walks FROM the map — this is the check that would have caught it.
#           → `UNMAPPED-BEAT: <beat>`
#      BROKEN STATE: a beat with landscape ids and no map row prints clean
#      here today only if it slips past both a and b; b existing at all is
#      what keeps that from being silent.
#   3. machine-derived, so the doc cannot drift: every command in the built
#      `pfm --help` tree and every tool name served by pfm's two MCP servers is
#      carried by a MAPPED landscape row.
#      → `UNMAPPED-COMMAND: <name>` / `UNMAPPED-TOOL: <name>`
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
# `derive: NOT RUN` and keeps the map checks; exit 1 on any map finding, 2 on a
# failed derive (failing to look outranks a finding), 0 only when every check ran.
set -uo pipefail

HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
ROOT="$(cd -- "$HERE/../../.." && pwd -P)"
LANDSCAPE="${LANE_LANDSCAPE:-$ROOT/docs/dev/testing/landscape.md}"
MAP="$HERE/map.tsv"
PENDING="$HERE/pending.txt"
BEATS="$HERE/beats.md"
PFM="" DERIVE=1

while [ $# -gt 0 ]; do
  case "$1" in
    --pfm) PFM="$2"; shift 2 ;;
    --no-derive) DERIVE=0; shift ;;
    -h|--help) sed -n '2,6p' "$0"; exit 0 ;;
    *) echo "usage: check-map.sh [--pfm PATH] [--no-derive]" >&2; exit 2 ;;
  esac
done

map_bad=0 derive_failed=0
red() { printf 'check-map: ✗ %s\n' "$1" >&2; map_bad=$((map_bad + 1)); }
say() { printf 'check-map: %s\n' "$1"; }
derive_fail() { printf 'check-map: DERIVE-FAILED: %s\n' "$1" >&2; derive_failed=1; }

[ -f "$LANDSCAPE" ] || { echo "check-map: LANDSCAPE-UNREADABLE — $LANDSCAPE does not exist" >&2; exit 2; }
[ -f "$MAP" ] || { echo "check-map: MAP-UNREADABLE — $MAP does not exist" >&2; exit 2; }

# ─── 0. the landscape is machine-read: a formatter leaves it byte-identical ─

MARKER='<!-- rumdl-disable -->'
if [ "$(head -n 1 "$LANDSCAPE")" != "$MARKER" ]; then
  red "LANDSCAPE-FORMATTABLE: line 1 of $(basename "$LANDSCAPE") is not '$MARKER' — a markdown formatter would reflow its id rows into paragraphs"
elif command -v rumdl >/dev/null 2>&1; then
  # rumdl reads its config from the CURRENT directory: the copy sits beside a
  # copy of the repo's own policy, so this run judges the real rules.
  scratch="$(mktemp -d "${TMPDIR:-/tmp}/check-map-fmt.XXXXXX")"
  cp "$LANDSCAPE" "$scratch/landscape.md"
  [ -f "$ROOT/.rumdl.toml" ] && cp "$ROOT/.rumdl.toml" "$scratch/.rumdl.toml"
  if ! (cd "$scratch" && rumdl fmt landscape.md >/dev/null 2>&1); then
    derive_fail "LANDSCAPE-FMT-FAILED: rumdl fmt exited non-zero on a copy of $(basename "$LANDSCAPE") — byte-stability could not be judged"
  elif ! cmp -s "$LANDSCAPE" "$scratch/landscape.md"; then
    red "LANDSCAPE-MUTABLE: rumdl fmt changed $(diff "$LANDSCAPE" "$scratch/landscape.md" | grep -c '^[<>]') line(s) of $(basename "$LANDSCAPE") despite the marker"
  else
    say "landscape machine-read: marker on line 1, byte-stable under rumdl fmt"
  fi
  rm -rf -- "$scratch"
else
  say "landscape machine-read: marker on line 1; rumdl not on PATH — the formatter run itself was NOT PERFORMED"
fi

# ─── 1. every landscape id is mapped ────────────────────────────────────────

ids="$(grep -oE '^[A-Z][0-9]+ ·' "$LANDSCAPE" | sed 's/ ·$//' | sort -u)"
n_ids="$(printf '%s\n' "$ids" | grep -c .)"
[ "$n_ids" -gt 0 ] || { echo "check-map: LANDSCAPE-UNREADABLE — no '<id> ·' rows in $LANDSCAPE (the enumerator found nothing to check)" >&2; exit 2; }
unmapped=0
for id in $ids; do
  awk -F'\t' -v i="$id" '$1 == i { f = 1 } END { exit(f ? 0 : 1) }' "$MAP" && continue
  red "UNMAPPED-ID: $id has no row in $(basename "$MAP")"
  unmapped=$((unmapped + 1))
done
say "$((n_ids - unmapped))/$n_ids landscape ids mapped"

# ─── 2. every mapped beat exists (or its lane is declared pending) ──────────

written_lane() { [ -f "$HERE/$1.sh" ]; }
pending_lane() { grep -qx "$1" "$PENDING" 2>/dev/null; }

lanes_in_map="$(awk -F'\t' 'NR > 1 { print $2 }' "$MAP" | sort -u)"
# The lane universe is the scripts on disk UNION the map — never the map
# alone, or a lane script whose rows a merge-conflict or a rename dropped from
# map.tsv is simply never visited by the loop below and exits clean.
NON_LANE_SCRIPTS=" lib.sh run.sh container.sh root.sh creds.sh check-map.sh "
lanes_on_disk="$(
  for f in "$HERE"/*.sh; do
    [ -f "$f" ] || continue
    b="$(basename "$f")"
    case "$NON_LANE_SCRIPTS" in *" $b "*) continue ;; esac
    printf '%s\n' "${b%.sh}"
  done | sort -u
)"
lanes_union="$(printf '%s\n%s\n' "$lanes_in_map" "$lanes_on_disk" | sort -u | sed '/^$/d')"
for lane in $lanes_union; do
  if ! printf '%s\n' "$lanes_in_map" | grep -qxF "$lane"; then
    red "ON-DISK-LANE-UNMAPPED: $lane.sh exists on disk but $(basename "$MAP") carries no row for lane $lane at all"
    continue
  fi
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
    # direction (b): every `beat <ID>` in the script has a map.tsv row, unless
    # beats.md marks that beat's landscape ids `(none)` — a beat added with
    # real ids but no row is otherwise invisible to direction (a) above.
    unmapped_beats=0
    for b in $(grep -oE '^[[:space:]]*beat [A-Za-z0-9_.-]+' "$HERE/$lane.sh" | awk '{ print $2 }' | sort -u); do
      awk -F'\t' -v l="$lane" -v b="$b" '$2 == l && $3 == b { f = 1 } END { exit(f ? 0 : 1) }' "$MAP" && continue
      # The ids field is the 4th " · "-separated segment of the row
      # (backtick-id · description · spends X · ids …) — read BY POSITION,
      # never as "the last segment": a row with a trailing annotation after
      # its ids (O2.01b's `blocked wave7-mock-engine` note) has a 5th
      # segment, and the last-segment form would read the annotation as the
      # ids field instead.
      ids_field="$(grep -E "^- \`$b\`" "$BEATS" | awk -F' · ' '{ print $4 }')"
      [ "$ids_field" = "(none)" ] && continue
      red "UNMAPPED-BEAT: $b is in $lane.sh with no row in $(basename "$MAP") (beats.md does not mark it (none))"
      unmapped_beats=$((unmapped_beats + 1))
    done
    say "lane $lane: reverse (script → map) · $unmapped_beats beat(s) with no map row and no (none) in $(basename "$BEATS")"
  elif pending_lane "$lane"; then
    say "lane $lane: NOT WRITTEN ($n_beats beats pending) — declared in $(basename "$PENDING")"
  else
    red "UNDECLARED-LANE: $lane has neither $lane.sh nor a line in $(basename "$PENDING")"
  fi
done
if [ -f "$PENDING" ]; then
  n_pending="$(grep -c . "$PENDING" || true)"
  say "pending lanes: ${n_pending} — this list must reach 0 (spec build order step 2)"
fi

# ─── 3. the machine-derived surface ─────────────────────────────────────────

# landscape_carries <regex> — 0 when a MAPPED landscape row matches
landscape_carries() {
  local row id
  while IFS= read -r row; do
    id="${row%% *}"
    awk -F'\t' -v i="$id" '$1 == i { f = 1 } END { exit(f ? 0 : 1) }' "$MAP" && return 0
  done < <(grep -E "$1" "$LANDSCAPE" | grep -E '^[A-Z][0-9]+ ·')
  return 1
}

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
    # Jailed: the derive must never touch the host's fleet, socket dir or tmux.
    pfm_jailed() {
      env PFM_HOME="$JAIL/home" PFM_DB="$JAIL/index.db" PFM_FLEET_DB="$JAIL/fleet.db" \
        PFM_SID_DIR="$JAIL/sid" PFM_TMUX_DIR="$JAIL/tmux" PFM_TMUX_CONF=/dev/null \
        "$PFM" --config "$JAIL/pfm.config.json" "$@"
    }
    with_timeout() { if command -v timeout >/dev/null; then timeout "$@"; else shift; "$@"; fi; }

    top_help="$(pfm_jailed --help 2>&1)"
    chat_help="$(pfm_jailed chat --help 2>&1)"
    cmds="$(printf '%s\n' "$top_help" | awk '/^  [a-z]/ { print $1 }' | sort -u)"
    subs="$(printf '%s\n' "$chat_help" | awk '/^  [a-z]/ { print $1 }' | tr '/' '\n' | sort -u)"
    n_cmds="$(printf '%s\n' "$cmds" | grep -c .)"
    n_subs="$(printf '%s\n' "$subs" | grep -c .)"
    if [ "$n_cmds" -lt 5 ] || [ "$n_subs" -lt 5 ]; then
      derive_fail "pfm --help produced $n_cmds command(s) and pfm chat --help $n_subs subcommand(s) — the help tree could not be read: $(printf '%s' "$top_help" | tr '\n' ' ' | cut -c1-200)"
    else
      bad_cmds=0
      for c in $cmds; do
        landscape_carries "\`pfm ${c}[ \`]" && continue
        red "UNMAPPED-COMMAND: pfm $c — no MAPPED landscape row names it"
        bad_cmds=$((bad_cmds + 1))
      done
      for c in $subs; do
        landscape_carries "\`pfm chat ${c}[ \`]" && continue
        red "UNMAPPED-COMMAND: pfm chat $c — no MAPPED landscape row names it"
        bad_cmds=$((bad_cmds + 1))
      done
      say "derived commands: $n_cmds top-level + $n_subs chat subcommands · $bad_cmds unmapped"
      say "derive NOTE: \`pfm internal --help\` is not a registered subcommand (it answers 'unknown subcommand'), so the internal verbs are NOT machine-derived here — they are carried by landscape rows X20-X40"
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
    for server in chat harvester; do
      tools="$(tools_of "$server")"
      n_tools="$(printf '%s\n' "$tools" | grep -c .)"
      if [ "$n_tools" -eq 0 ]; then
        derive_fail "the $server MCP server listed no tool over stdio — stderr: $(tr '\n' ' ' <"$JAIL/$server.err" 2>/dev/null | cut -c1-200); frames it did answer: $(tr '\n' ' ' <"$JAIL/$server.frames" 2>/dev/null | cut -c1-200)"
        continue
      fi
      bad_tools=0
      for t in $tools; do
        landscape_carries "\`$t\`" && continue
        red "UNMAPPED-TOOL: $server/$t — no MAPPED landscape row names it"
        bad_tools=$((bad_tools + 1))
      done
      say "derived $server tools: $n_tools · $bad_tools unmapped"
    done
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
say "clean — landscape machine-read, every landscape id mapped, every mapped beat present or its lane declared pending, every coded beat mapped or marked (none), every derived command and tool carried"
exit 0

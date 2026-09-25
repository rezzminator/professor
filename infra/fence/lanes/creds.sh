#!/usr/bin/env bash
# creds.sh — stages REAL seat credentials into a lane container, and owns the
# ONE host→container seat mapping the whole harness uses (`--print-config`).
#
#   creds.sh --container NAME [--config PATH] [--accounts 1,2,3]
#   creds.sh --print-config    [--config PATH] [--accounts 1,2,3]
#
# Seats are discovered from pfm's OWN config (`accounts[]`, the same map
# infra/demo/up.sh reads), never from a hardcoded list. Each host seat lands on
# the deterministic container path `~/.cc/<id>`, so a container's config is the
# host's seat roster with container-local dirs — `--print-config` prints exactly
# that JSON and root.sh writes it, so the mapping has one author.
#
# The roster carries ONLY seats this host can actually log in: a seat with no
# usable credential is dropped by name (`seat 2 (🥈): NO CREDENTIAL — …;
# dropped from the container roster`) in BOTH modes. A config that offers a seat
# nothing was staged for makes the lane spend a beat switching onto a seat that
# cannot answer, and the ✗ lands on the product for the harness's mistake.
#
# Two credential paths, one per platform:
#   darwin  each seat's OAuth blob comes from the Keychain — infra/demo/creds.sh
#           is the reader, called once per seat so a failure names the seat id.
#   linux   each seat's `<configDir>/.credentials.json` is copied in, plus the
#           Codex home's auth.json and OpenCode's auth.json when present. (The
#           demo's reader is darwin-only; this is the devbox path Wave 4 adds.)
#
# Nothing is ever printed but paths, byte counts and verdicts: a credential body
# never reaches stdout, stderr or a log — lanes/tests/creds_test.sh pins that.
#
# A copied seat shares the host's refresh token; the first refresh inside the
# container rotates it and one side is logged out. That is the accepted cost of
# a reusable root image — the alternative is a login per seat per rebuild.
#
# BROKEN STATE: every configured seat is reported by name — `seat 2 (🥈): NO
# CREDENTIAL — <why>` — and the closing line counts both sides
# (`creds: 3 staged · 2 NO CREDENTIAL · Claude seats staged: 1`). Exit 1 when no
# requested seat holds a credential at all (the roster would be empty) and,
# separately, when a roster seat could not be STAGED — a logged-out host and a
# container that would not take the copy are different findings; a missing Codex or
# OpenCode auth is named and non-fatal, since only E2/E3 need them. An
# unreadable config, or docker not answering, exits 2 before anything is copied.
set -uo pipefail

NAME="" MODE=stage ACCOUNTS=""
CONFIG="${XDG_CONFIG_HOME:-$HOME/.config}/pfm/pfm.config.json"
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
ROOT="$(cd -- "$HERE/../../.." && pwd -P)"

while [ $# -gt 0 ]; do
  case "$1" in
    --container) NAME="$2"; shift 2 ;;
    --config) CONFIG="$2"; shift 2 ;;
    --accounts) ACCOUNTS="$2"; shift 2 ;;
    --print-config) MODE=print; shift ;;
    -h|--help) sed -n '2,12p' "$0"; exit 0 ;;
    *) echo "usage: creds.sh --container NAME [--config PATH] [--accounts 1,2,3] | creds.sh --print-config" >&2; exit 2 ;;
  esac
done

fatal() { echo "creds: $1" >&2; exit 2; }
command -v jq >/dev/null || fatal "TOOLCHAIN-MISSING — jq"
[ -f "$CONFIG" ] || fatal "host pfm config $CONFIG not found (the seats to mirror come from it)"

expand() { case "$1" in "~"*) printf '%s' "$HOME${1#\~}" ;; *) printf '%s' "$1" ;; esac; }

staged=0 seats_staged=0 absent=0
report_absent() { echo "creds: $1" >&2; absent=$((absent + 1)); }

is_darwin() { [ "$(uname -s)" = Darwin ]; }

# seat_credential_why <host-config-dir> — empty when this host holds a usable
# credential for that seat, otherwise the reason it cannot be staged. ONE
# implementation: the roster filter below and the staging loop ask the same
# question, so a seat can never be offered to a lane and then not staged.
seat_credential_why() {
  if is_darwin; then
    # Only the Keychain reader (infra/demo/creds.sh) can answer, and it needs a
    # container — so on darwin presence is proven at staging time, per seat.
    return 0
  fi
  if [ ! -s "$1/.credentials.json" ]; then
    printf '%s' "$1/.credentials.json is missing or empty (log this seat in on the host, or inside the container)"
    return 0
  fi
  if ! jq -e '.claudeAiOauth.accessToken | strings | length > 0' "$1/.credentials.json" >/dev/null 2>&1; then
    printf '%s' "$1/.credentials.json carries no claudeAiOauth.accessToken (logged out on this host)"
  fi
}

# The seats this container may offer: the requested ones MINUS every seat this
# host cannot log in. A roster listing a seat with no credential is the harness
# lying to the lane — the lane reads it as a second seat, spends a beat
# switching onto it, and the ✗ belongs to nobody.
KEPT=""
while IFS=$'\t' read -r id host_dir; do
  [ -n "$id" ] || continue
  why="$(seat_credential_why "$(expand "$host_dir")")"
  emoji="$(jq -r --argjson want "$id" '.accounts[] | select(.id == $want) | .emoji // ""' "$CONFIG")"
  if [ -n "$why" ]; then
    report_absent "seat $id ($emoji): NO CREDENTIAL — $why; dropped from the container roster"
    continue
  fi
  KEPT="$KEPT${KEPT:+,}$id"
done < <(jq -r --arg ids "$ACCOUNTS" '
  (if $ids == "" then [.accounts[].id] else ($ids | split(",") | map(tonumber)) end) as $want
  | .accounts[] | select(.id as $id | $want | index($id)) | "\(.id)\t\(.configDir)"' "$CONFIG")

if [ -z "$KEPT" ]; then
  echo "creds: not one requested Claude seat holds a credential (asked for ${ACCOUNTS:-<all>}) — the container roster would be empty and the container can run no lane" >&2
  exit 1
fi

# The container's config: the KEPT seats re-homed on ~/.cc/<id>, one Codex
# home, the OpenCode home, both MCP families on. `accounts` order is the host's.
container_config() {
  jq -c --arg ids "$KEPT" '
    ($ids | split(",") | map(tonumber)) as $want
    | {version: 2,
       accounts: [.accounts[] | select(.id as $id | $want | index($id)) | {id, configDir: ("~/.cc/" + (.id | tostring)), emoji}],
       codex: {homes: [{id: 1, home: "~/.codex", emoji: "🥇"}]},
       claude: {systemPrompt: "professor"},
       mcp: {servers: {chat: {enabled: true}, harvester: {enabled: true}}}}' "$CONFIG"
}

CC="$(container_config)" || fatal "could not read accounts[] from $CONFIG"
[ "$(jq '.accounts | length' <<<"$CC")" -gt 0 ] ||
  fatal "no account in $CONFIG matches --accounts ${ACCOUNTS:-<all>}"

if [ "$MODE" = print ]; then
  printf '%s\n' "$CC"
  exit 0
fi

[ -n "$NAME" ] || fatal "--container is required"
command -v docker >/dev/null || fatal "TOOLCHAIN-MISSING — docker"
docker inspect "$NAME" >/dev/null 2>&1 || fatal "container $NAME does not exist"

# put <container path> <host source path> — body on stdin (must be
# `<"$2"`), mode 0600, parent created, size read back from the container (the
# demo reader's mechanic, kept identical) — `set -e` in the sh -c so a `cat`
# that fails mid-write (quota, revoked permission, full container fs) aborts
# BEFORE `wc -c` reads back a truncated file as a clean size (mirrors
# root.sh's `|| step_failed` fail-loud shape). The read-back size is then
# compared against the HOST source's own size — root.sh:157's shape, applied
# to a byte count instead of an exit code.
put() {
  local dest="$1" src="$2" size src_size
  size="$(docker exec -i "$NAME" sh -c \
    'set -e; umask 077; p="$1"; case "$p" in "~"*) p="$HOME${p#\~}";; esac; mkdir -p "$(dirname "$p")" && cat > "$p" && wc -c < "$p"' \
    sh "$dest" <"$src")" || return 1
  src_size="$(wc -c <"$src" | tr -d ' ')"
  if [ "$size" != "$src_size" ]; then
    echo "put: staged $size bytes into $NAME:$dest but source $src is $src_size bytes" >&2
    return 1
  fi
  printf '%s' "$size"
}

# ── Claude seats ────────────────────────────────────────────────────────────
while IFS=$'\t' read -r id cont_dir emoji; do
  # The container dir comes from the mapped config; the HOST dir from the host
  # config, joined on the seat id — the two never drift into one another.
  host_dir="$(expand "$(jq -r --argjson want "$id" '.accounts[] | select(.id == $want) | .configDir' "$CONFIG")")"
  if [ -z "$host_dir" ]; then
    report_absent "seat $id ($emoji): NO CREDENTIAL — no accounts[] entry with id $id in $CONFIG"
    continue
  fi
  if is_darwin; then
    # The Keychain reader lives in infra/demo/creds.sh — one implementation, called
    # per seat so its failure is attributable to a seat id rather than a batch.
    if out="$(bash "$ROOT/infra/demo/creds.sh" --container "$NAME" --seat "$host_dir=$cont_dir" 2>&1)"; then
      echo "creds: seat $id ($emoji) staged from the Keychain → $cont_dir/.credentials.json"
      staged=$((staged + 1)) seats_staged=$((seats_staged + 1))
    else
      report_absent "seat $id ($emoji): NO CREDENTIAL — $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-200)"
    fi
    continue
  fi
  # The roster filter above already refused every seat with no usable
  # credential, so reaching here means the file was readable a moment ago.
  if size="$(put "$cont_dir/.credentials.json" "$host_dir/.credentials.json")"; then
    echo "creds: seat $id ($emoji) staged → $cont_dir/.credentials.json ($size bytes)"
    staged=$((staged + 1)) seats_staged=$((seats_staged + 1))
  else
    report_absent "seat $id ($emoji): NO CREDENTIAL — the copy into $NAME failed (docker exec returned non-zero)"
  fi
done < <(jq -r '.accounts[] | "\(.id)\t\(.configDir)\t\(.emoji)"' <<<"$CC")

# ── Codex home ──────────────────────────────────────────────────────────────
codex_host="$(expand "$(jq -r '.codex.homes[0].home // "~/.codex"' "$CONFIG")")"
codex_cont="$(jq -r '.codex.homes[0].home' <<<"$CC")"
if is_darwin; then
  if out="$(bash "$ROOT/infra/demo/creds.sh" --container "$NAME" --codex "$codex_host=$codex_cont" 2>&1)"; then
    echo "creds: codex home staged from the Keychain path → $codex_cont/auth.json"
    staged=$((staged + 1))
  else
    report_absent "codex home: NO CREDENTIAL — $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-200)"
  fi
elif [ ! -s "$codex_host/auth.json" ]; then
  report_absent "codex home: NO CREDENTIAL — $codex_host/auth.json is missing or empty (sign in with codex on the host); lane E2 cannot run"
elif ! jq -e '.tokens | objects' "$codex_host/auth.json" >/dev/null 2>&1; then
  report_absent "codex home: NO CREDENTIAL — $codex_host/auth.json carries no tokens object"
elif size="$(put "$codex_cont/auth.json" "$codex_host/auth.json")"; then
  echo "creds: codex $codex_cont/auth.json staged ($size bytes)"
  staged=$((staged + 1))
else
  report_absent "codex home: NO CREDENTIAL — the copy into $NAME failed"
fi

# ── OpenCode home ───────────────────────────────────────────────────────────
oc_host="${OPENCODE_AUTH:-$HOME/.local/share/opencode/auth.json}"
# shellcheck disable=SC2088 # the ~ is deliberate: put() expands it inside the CONTAINER's HOME
oc_cont="~/.local/share/opencode/auth.json"
if is_darwin; then
  if out="$(bash "$ROOT/infra/demo/creds.sh" --container "$NAME" --opencode "$oc_host=$oc_cont" 2>&1)"; then
    echo "creds: opencode auth staged → $oc_cont"
    staged=$((staged + 1))
  else
    report_absent "opencode: NO CREDENTIAL — $(printf '%s' "$out" | tr '\n' ' ' | cut -c1-200)"
  fi
elif [ ! -s "$oc_host" ]; then
  report_absent "opencode: NO CREDENTIAL — $oc_host is missing or empty (opencode auth login on the host); lane E3 cannot run"
elif ! jq -e '.openai | objects | select(.type == "oauth")' "$oc_host" >/dev/null 2>&1; then
  report_absent "opencode: NO CREDENTIAL — $oc_host carries no openai oauth entry"
elif size="$(put "$oc_cont" "$oc_host")"; then
  echo "creds: opencode $oc_cont staged ($size bytes)"
  staged=$((staged + 1))
else
  report_absent "opencode: NO CREDENTIAL — the copy into $NAME failed"
fi

echo "creds: $staged staged · $absent NO CREDENTIAL · Claude seats staged: $seats_staged"
if [ "$seats_staged" -eq 0 ]; then
  echo "creds: the roster held seat(s) $KEPT but not one could be STAGED into $NAME — the container can run no lane" >&2
  exit 1
fi
exit 0

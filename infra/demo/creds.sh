#!/usr/bin/env bash
# creds.sh — copies REAL seats into the demo container: each Claude seat's OAuth
# credential from the macOS Keychain (the entry Claude Code keeps per config
# dir), the Codex home's auth.json, and OpenCode's auth.json (its ChatGPT
# OAuth, under ~/.local/share/opencode). Everything travels over docker-exec
# stdin; nothing is written to the host disk and nothing is printed.
#
#   creds.sh --container NAME --seat HOST_DIR=CONTAINER_DIR [--seat …] [--codex HOST_HOME=CONTAINER_HOME] [--opencode HOST_FILE=CONTAINER_FILE]
#
# CONTAINER_DIR may start with ~ (the container's HOME). The Keychain entry for
# $HOME/.claude is the unhashed "Claude Code-credentials"; every other config
# dir uses "Claude Code-credentials-<sha256(dir)[:8]>" — the same rule pfm's
# usage hook applies (internal/usagehook/credential.go).
#
# BROKEN STATE: a seat whose Keychain entry is missing, or whose blob is not the
# claudeAiOauth JSON Claude Code writes, stops the run naming that seat; a Codex
# auth.json without a tokens object does the same, as does an OpenCode auth.json
# without an openai oauth entry. A copy that lands is
# verified by reading its size back from the container.
set -euo pipefail

NAME="" CODEX="" OPENCODE=""
seats=()
while [ $# -gt 0 ]; do
  case "$1" in
    --container) NAME="$2"; shift 2 ;;
    --seat) seats+=("$2"); shift 2 ;;
    --codex) CODEX="$2"; shift 2 ;;
    --opencode) OPENCODE="$2"; shift 2 ;;
    *) echo "usage: creds.sh --container NAME --seat HOST_DIR=CONTAINER_DIR … [--codex HOST_HOME=CONTAINER_HOME] [--opencode HOST_FILE=CONTAINER_FILE]" >&2; exit 2 ;;
  esac
done
[ -n "$NAME" ] || { echo "creds: --container is required" >&2; exit 2; }
[ "${#seats[@]}" -gt 0 ] || [ -n "$CODEX" ] || [ -n "$OPENCODE" ] || { echo "creds: nothing to copy — give --seat, --codex or --opencode" >&2; exit 2; }
[ "$(uname -s)" = Darwin ] || { echo "creds: the Keychain reader runs on macOS only" >&2; exit 1; }
command -v jq >/dev/null || { echo "creds: TOOLCHAIN-MISSING — jq" >&2; exit 1; }

service_for() { # service_for <host config dir>
  local dir="$1"
  if [ "$dir" = "$HOME/.claude" ]; then echo "Claude Code-credentials"; return; fi
  printf 'Claude Code-credentials-%s' "$(printf '%s' "$dir" | shasum -a 256 | cut -c1-8)"
}
put() { # put <container path> — file body on stdin, mode 0600, parent created
  local path="$1"
  docker exec -i "$NAME" sh -c 'umask 077; p="$1"; case "$p" in "~"*) p="$HOME${p#\~}";; esac; mkdir -p "$(dirname "$p")"; cat > "$p"; wc -c < "$p"' sh "$path"
}

for pair in ${seats[@]+"${seats[@]}"}; do  # no --seat at all is valid: the seats log in inside the container (up.sh --login)
  host_dir="${pair%%=*}"; cont_dir="${pair#*=}"
  service="$(service_for "$host_dir")"
  blob="$(security find-generic-password -s "$service" -w 2>/dev/null)" || {
    # The unhashed entry can be absent on a host that only ever used hashed ones.
    alt="Claude Code-credentials-$(printf '%s' "$host_dir" | shasum -a 256 | cut -c1-8)"
    blob="$(security find-generic-password -s "$alt" -w 2>/dev/null)" || { echo "creds: no Keychain entry for seat $host_dir (tried '$service' and '$alt')" >&2; exit 1; }
  }
  jq -e '.claudeAiOauth | objects' <<<"$blob" >/dev/null 2>&1 || { echo "creds: the Keychain blob for $host_dir is not a claudeAiOauth credential" >&2; exit 1; }
  if ! jq -e '.claudeAiOauth.accessToken | strings | length > 0' <<<"$blob" >/dev/null 2>&1; then
    echo "creds: SKIPPED seat $host_dir — logged out on this host (empty access token); it stays configured in the container and logs in there: CLAUDE_CONFIG_DIR=$cont_dir claude, then /login" >&2
    continue
  fi
  size="$(printf '%s' "$blob" | put "$cont_dir/.credentials.json")"
  echo "creds: seat $host_dir → $cont_dir/.credentials.json ($size bytes)"
done

if [ -n "$CODEX" ]; then
  host_home="${CODEX%%=*}"; cont_home="${CODEX#*=}"
  [ -f "$host_home/auth.json" ] || { echo "creds: $host_home/auth.json not found — sign in to Codex on the host first" >&2; exit 1; }
  jq -e '.tokens | objects' "$host_home/auth.json" >/dev/null 2>&1 || { echo "creds: $host_home/auth.json carries no tokens object" >&2; exit 1; }
  size="$(put "$cont_home/auth.json" < "$host_home/auth.json")"
  echo "creds: codex $host_home/auth.json → $cont_home/auth.json ($size bytes)"
fi

if [ -n "$OPENCODE" ]; then
  host_file="${OPENCODE%%=*}"; cont_file="${OPENCODE#*=}"
  [ -f "$host_file" ] || { echo "creds: $host_file not found — sign OpenCode in to ChatGPT on the host first (opencode auth login)" >&2; exit 1; }
  jq -e '.openai | objects | select(.type == "oauth")' "$host_file" >/dev/null 2>&1 || { echo "creds: $host_file carries no openai oauth entry" >&2; exit 1; }
  size="$(put "$cont_file" < "$host_file")"
  echo "creds: opencode $host_file → $cont_file ($size bytes)"
fi

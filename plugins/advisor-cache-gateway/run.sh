#!/bin/sh
# Supervises gateway.py for launchd (macOS) or systemd (Linux) and keeps Claude Code pointed at it only
# while it answers. Once the gateway is healthy, it sets env.ANTHROPIC_BASE_URL
# in Claude Code's settings. It releases that setting (fail open: it points the
# URL at the upstream itself, https://api.anthropic.com) when Python is missing,
# when the gateway fails to start or exits, and when the agent is stopped.
#
# Release rewrites the URL instead of deleting it: a running Claude Code session
# follows a changed env.ANTHROPIC_BASE_URL within about a second, but keeps a
# removed one until it restarts (measured on 2.1.294). A URL other than this
# gateway's or the upstream is never touched.
#
# Signals:
#   HUP   graceful restart: re-reads config.env, starts a new gateway on the same
#         port (SO_REUSEPORT; new connections go to the newest), and once it
#         answers, tells the old one to drain. No in-flight stream is cut.
#   TERM  stop: releases the setting first, keeps serving ADVISOR_GATEWAY_SETTLE_SECONDS (3) while running
#         sessions follow it, then lets every gateway drain and exits.
#
# Usage: run.sh serve. Configuration: the service's environment, then config.env
# next to this script (TTLs, max tokens, capture dir, drain limit, upstream, settle).
set -u

PY=${ADVISOR_GATEWAY_PYTHON:-/usr/bin/python3}
JQ=${ADVISOR_GATEWAY_JQ:-/usr/bin/jq}
SETTINGS=${CLAUDE_SETTINGS:-$HOME/.claude/settings.json}
PORT=${ADVISOR_CACHE_GATEWAY_PORT:-18787}
RETRY=${ADVISOR_GATEWAY_RETRY_SECONDS:-60}
URL="http://127.0.0.1:$PORT"
DIR=$(cd "$(dirname "$0")" && pwd)
pid=
olds=
reload=

log() { printf '%s run.sh %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*" >&2; }

load_config() { if [ -f "$DIR/config.env" ]; then set -a; . "$DIR/config.env"; set +a; fi; }

# Where Claude Code goes without the gateway: the upstream the gateway forwards to.
direct() { printf '%s' "${ADVISOR_CACHE_UPSTREAM:-https://api.anthropic.com}"; }

current() { "$JQ" -r '.env.ANTHROPIC_BASE_URL // ""' "$SETTINGS" 2>/dev/null; }

# GNU stat, then BSD stat.
mode_of() { stat -c %a "$1" 2>/dev/null || stat -f %Lp "$1"; }

# Rewrites the settings file through jq with $1 as the filter, atomically, keeping its mode.
edit() {
  mode=$(mode_of "$SETTINGS") || return 1
  tmp=$(mktemp "$SETTINGS.XXXXXX") || return 1
  if "$JQ" --arg url "$URL" --arg direct "$(direct)" "$1" "$SETTINGS" > "$tmp"; then
    chmod "$mode" "$tmp" && mv "$tmp" "$SETTINGS"
  else
    rm -f "$tmp"
    return 1
  fi
}

link() {
  cur=$(current)
  if [ "$cur" = "$URL" ]; then return 0; fi
  if [ -n "$cur" ] && [ "$cur" != "$(direct)" ]; then log "settings point at $cur, not this gateway; left unchanged"; return 0; fi
  edit '.env = ((.env // {}) + {ANTHROPIC_BASE_URL: $url})' && log "linked: Claude Code uses $URL"
}

release() {
  if [ "$(current)" != "$URL" ]; then return 0; fi
  edit '.env.ANTHROPIC_BASE_URL = $direct' && log "released: Claude Code, running sessions included, talks to $(direct) directly"
}

serving_pid() {
  curl -sf --max-time 1 "$URL/__gateway/health" 2>/dev/null | sed -n 's/^ok pid=\([0-9]*\).*/\1/p'
}

# Starts a gateway with the current config.env and waits until it answers on the port.
# Sets $new on success; on failure kills it and returns 1.
start_gateway() {
  load_config
  "$PY" -I "$DIR/gateway.py" &
  new=$!
  i=0
  until [ "$(serving_pid)" = "$new" ]; do
    if ! kill -0 "$new" 2>/dev/null; then
      wait "$new"
      log "gateway exited with status $? before answering $URL"
      return 1
    fi
    i=$((i + 1))
    if [ $i -ge 60 ]; then
      kill -TERM "$new" 2>/dev/null; wait "$new" 2>/dev/null
      log "gateway did not answer $URL/__gateway/health within 15 seconds"
      return 1
    fi
    sleep 0.25
  done
}

stop() {
  release
  # Running sessions follow the release within about a second; until then they still call here.
  sleep "${ADVISOR_GATEWAY_SETTLE_SECONDS:-3}" & wait $!
  # $new: a gateway a restart had started when the stop arrived; wait below waits for it too.
  # shellcheck disable=SC2086
  kill -TERM $pid $olds ${new:-} 2>/dev/null
  log "stopping: gateways draining their in-flight requests"
  wait
  log "stopped"
  exit 0
}

# Fails open, then waits before exiting so launchd's restarts stay slow.
fail() {
  log "$1"
  release
  sleep "$RETRY" & wait $!
  exit 1
}

restart() {
  if start_gateway; then
    kill -TERM "$pid" 2>/dev/null
    olds="$olds $pid"
    log "restarted: gateway $new took over, $pid drains its in-flight requests"
    pid=$new
  else
    log "restart failed; gateway $pid keeps serving"
  fi
}

serve() {
  # Under systemd socket activation the listener is fd 3; every gateway generation inherits and shares it.
  if [ "${LISTEN_PID:-}" = "$$" ] && [ "${LISTEN_FDS:-0}" -ge 1 ]; then
    export ADVISOR_GATEWAY_LISTEN_FD=3
  fi
  unset LISTEN_PID LISTEN_FDS LISTEN_FDNAMES
  trap stop TERM INT
  trap 'reload=1' HUP
  load_config
  # On macOS without the developer tools, /usr/bin/python3 is a stub that opens an install dialog.
  if [ "$(uname -s)" = Darwin ] && [ "$PY" = /usr/bin/python3 ] && ! xcode-select -p >/dev/null 2>&1; then
    fail "no command line developer tools, so no $PY"
  fi
  "$PY" -I -c 'import sys; sys.exit(sys.version_info < (3, 9))' >/dev/null 2>&1 \
    || fail "$PY is missing or older than Python 3.9"
  start_gateway || fail "the gateway could not start"
  pid=$new
  link

  while :; do
    # A HUP that arrived during a restart set reload while no wait was running: act on it first.
    if [ -n "$reload" ]; then
      reload=
      restart
      continue
    fi
    wait "$pid"
    status=$?
    if [ -n "$reload" ]; then continue; fi
    if kill -0 "$pid" 2>/dev/null; then continue; fi  # wait was interrupted, the gateway still runs
    log "gateway exited with status $status"
    release
    exit 1
  done
}

case "${1:-}" in
  serve) serve ;;
  *) echo "usage: run.sh serve" >&2; exit 2 ;;
esac

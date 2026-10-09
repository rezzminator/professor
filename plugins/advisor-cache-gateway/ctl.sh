#!/bin/sh
# Turns advisor-cache-gateway on or off by hand. The state holds across logins
# until changed here; nothing else turns it on or off. The service is a launchd
# agent on macOS and a systemd user unit on Linux.
#
#   ctl.sh on        start it; Claude Code uses it once it answers
#   ctl.sh off       Claude Code, running sessions included, goes straight to
#                    the API within about a second; in-flight streams finish on
#                    the gateway, then it stops
#   ctl.sh restart   graceful restart: re-reads config.env, no stream is cut
#   ctl.sh status    on or off, its health, and where Claude Code points
set -eu

DIR=$(cd "$(dirname "$0")" && pwd)
[ -f "$DIR/install.env" ] || { echo "ctl.sh: not installed ($DIR/install.env missing); run install.sh" >&2; exit 1; }
# shellcheck source=/dev/null
. "$DIR/install.env"
URL="http://127.0.0.1:$PORT"
CONF="$DIR/config.env"
# Where Claude Code goes without the gateway: the upstream it forwards to (config.env may name another).
# shellcheck source=/dev/null
DIRECT=$( [ -f "$CONF" ] && . "$CONF"; printf '%s' "${ADVISOR_CACHE_UPSTREAM:-https://api.anthropic.com}")

health() { curl -sf --max-time 1 "$URL/__gateway/health" 2>/dev/null; }
health_pid() { health | sed -n 's/^ok pid=\([0-9]*\).*/\1/p'; }
linked() { [ "$("$JQ" -r '.env.ANTHROPIC_BASE_URL // ""' "$SETTINGS")" = "$URL" ]; }

if [ "$(uname -s)" = Darwin ]; then
  DOMAIN="gui/$(id -u)"
  loaded() { launchctl print "$DOMAIN/$LABEL" >/dev/null 2>&1; }
  disabled() { launchctl print-disabled "$DOMAIN" 2>/dev/null | grep -q "\"$LABEL\" => \(disabled\|true\)"; }
  svc_on() { launchctl enable "$DOMAIN/$LABEL"; loaded || launchctl bootstrap "$DOMAIN" "$SERVICE_FILE"; }
  # bootout returns once in-flight streams have drained, so it runs in the background.
  svc_off() { launchctl disable "$DOMAIN/$LABEL"; if loaded; then launchctl bootout "$DOMAIN/$LABEL" >/dev/null 2>&1 & fi; }
  svc_hup() { launchctl kill HUP "$DOMAIN/$LABEL"; }
else
  UNIT="$LABEL.service"
  SOCKET="$LABEL.socket"
  loaded() { systemctl --user is-active --quiet "$UNIT"; }
  disabled() { [ "$(systemctl --user is-enabled "$UNIT" 2>/dev/null)" != enabled ]; }
  svc_on() { systemctl --user -q enable --now "$SOCKET" "$UNIT"; }
  # --no-block: the stop returns once in-flight streams have drained.
  svc_off() { systemctl --user -q disable "$SOCKET" "$UNIT"; systemctl --user stop --no-block "$UNIT" "$SOCKET"; }
  # HUP to run.sh alone: a gateway process would die on it.
  svc_hup() { systemctl --user kill -s HUP --kill-whom=main "$UNIT"; }
fi

case "${1:-}" in
  on)
    svc_on
    i=0
    until health >/dev/null && linked; do
      i=$((i + 1))
      [ $i -ge 80 ] && { echo "ctl.sh: the gateway did not come up within 20 seconds; see $LOGS/advisor-cache-gateway.stderr.log" >&2; exit 1; }
      sleep 0.25
    done
    echo "on: $(health)"
    ;;
  off)
    svc_off
    # run.sh releases Claude Code at once, then serves a few seconds more and drains.
    i=0
    while linked && [ $i -lt 40 ]; do sleep 0.25; i=$((i + 1)); done
    if linked; then
      echo "ctl.sh: the setting still points at the gateway; set env.ANTHROPIC_BASE_URL in $SETTINGS to $DIRECT by hand (deleting it does not reach running sessions)" >&2
      exit 1
    fi
    echo "off: Claude Code, running sessions included, talks to $DIRECT directly; in-flight streams finish on the gateway first"
    ;;
  restart)
    loaded || { echo "ctl.sh: the gateway is off; use: ctl.sh on" >&2; exit 1; }
    before=$(health_pid)
    svc_hup
    i=0
    until after=$(health_pid) && [ -n "$after" ] && [ "$after" != "$before" ]; do
      i=$((i + 1))
      [ $i -ge 80 ] && { echo "ctl.sh: no new gateway took over within 20 seconds; the old one keeps serving" >&2; exit 1; }
      sleep 0.25
    done
    echo "restarted: $(health)"
    ;;
  status)
    if disabled; then state=off
    elif loaded; then state=on
    else state="on, but not running now"
    fi
    echo "state:  $state"
    echo "health: $(health || echo 'not answering')"
    cur=$("$JQ" -r '.env.ANTHROPIC_BASE_URL // ""' "$SETTINGS")
    case "$cur" in
      "$URL") echo "Claude Code: through the gateway ($URL)" ;;
      ""|"$DIRECT") echo "Claude Code: straight to the API${cur:+ ($cur)}" ;;
      *) echo "Claude Code: at $cur, not this gateway" ;;
    esac
    echo "logs:   $LOGS/advisor-cache-gateway.log, $LOGS/advisor-cache-gateway.stderr.log"
    ;;
  *) echo "usage: ctl.sh on|off|restart|status" >&2; exit 2 ;;
esac

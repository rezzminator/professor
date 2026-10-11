#!/bin/sh
# Removes advisor-cache-gateway. Stopping the service makes run.sh point Claude
# Code, running sessions included, at the API and lets in-flight streams finish;
# if the setting still points at the gateway (run.sh was not running), this
# script does that itself. Then it deletes the setting, which only new sessions
# notice, the service and the installed copy.
# --purge also deletes the logs.
set -eu

DEST=${ADVISOR_GATEWAY_DEST:-$HOME/.local/share/advisor-cache-gateway}
BACKUPS="$HOME/.local/state/advisor-cache-gateway/backups"
if [ -f "$DEST/install.env" ]; then
  # shellcheck source=/dev/null
  . "$DEST/install.env"
elif [ "$(uname -s)" = Darwin ]; then
  LABEL=${ADVISOR_GATEWAY_LABEL:-local.advisor-cache-gateway}
  SERVICE_FILE="$HOME/Library/LaunchAgents/$LABEL.plist"
  LOGS="$HOME/Library/Logs"
  PORT=$(plutil -extract EnvironmentVariables.ADVISOR_CACHE_GATEWAY_PORT raw "$SERVICE_FILE" 2>/dev/null || echo 18787)
  SETTINGS=$(plutil -extract EnvironmentVariables.CLAUDE_SETTINGS raw "$SERVICE_FILE" 2>/dev/null || echo "$HOME/.claude/settings.json")
else
  LABEL=${ADVISOR_GATEWAY_LABEL:-advisor-cache-gateway}
  SERVICE_FILE="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/$LABEL.service"
  LOGS="$HOME/.local/state/advisor-cache-gateway/logs"
  PORT=18787
  SETTINGS=${CLAUDE_SETTINGS:-$HOME/.claude/settings.json}
fi
URL="http://127.0.0.1:$PORT"
CONF="$DEST/config.env"
# Where Claude Code goes without the gateway: the upstream it forwards to (config.env may name another).
# shellcheck source=/dev/null
DIRECT=$( [ -f "$CONF" ] && . "$CONF"; printf '%s' "${ADVISOR_CACHE_UPSTREAM:-https://api.anthropic.com}")

echo "stopping the service; in-flight streams finish first"
if [ "$(uname -s)" = Darwin ]; then
  DOMAIN="gui/$(id -u)"
  if launchctl print "$DOMAIN/$LABEL" >/dev/null 2>&1; then
    launchctl bootout "$DOMAIN/$LABEL" 2>/dev/null || true
    while launchctl print "$DOMAIN/$LABEL" >/dev/null 2>&1; do sleep 0.25; done
  fi
  launchctl enable "$DOMAIN/$LABEL" 2>/dev/null || true  # drop any off-state record
  rm -f "$SERVICE_FILE"
else
  systemctl --user -q disable --now "$LABEL.service" "$LABEL.socket" 2>/dev/null || true
  rm -f "$SERVICE_FILE" "${SOCKET_FILE:-${SERVICE_FILE%.service}.socket}"
  systemctl --user daemon-reload
  systemctl --user reset-failed "$LABEL.service" 2>/dev/null || true
fi
echo "service stopped and removed"

mode_of() { stat -c %a "$1" 2>/dev/null || stat -f %Lp "$1"; }
# $1 is the jq filter; the file is replaced atomically and keeps its mode.
edit_settings() {
  MODE=$(mode_of "$SETTINGS")
  TMP=$(mktemp "$SETTINGS.XXXXXX")
  jq --arg direct "$DIRECT" "$1" "$SETTINGS" > "$TMP"
  chmod "$MODE" "$TMP"
  mv "$TMP" "$SETTINGS"
}
current() { jq -r '.env.ANTHROPIC_BASE_URL // ""' "$SETTINGS"; }

if [ -f "$SETTINGS" ] && { [ "$(current)" = "$URL" ] || [ "$(current)" = "$DIRECT" ]; }; then
  mkdir -p "$BACKUPS"
  cp -p "$SETTINGS" "$BACKUPS/settings.json.pre-uninstall-$(date +%Y%m%d-%H%M%S)"
  if [ "$(current)" = "$URL" ]; then
    # A running session keeps a deleted URL, so point it at the API first and give it time to follow.
    edit_settings '.env.ANTHROPIC_BASE_URL = $direct'
    sleep 3
  fi
  edit_settings 'del(.env.ANTHROPIC_BASE_URL)'
fi
echo "Claude Code talks to the API directly ($SETTINGS)"

rm -rf "$DEST"
if [ "${1:-}" = "--purge" ]; then
  rm -f "$LOGS/advisor-cache-gateway.log" "$LOGS/advisor-cache-gateway.log.1" "$LOGS/advisor-cache-gateway.stderr.log"
  echo "logs deleted"
fi
echo "advisor-cache-gateway removed"

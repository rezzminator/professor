#!/bin/sh
# Installs or updates advisor-cache-gateway for the current user. Re-runnable.
# The service is a launchd agent on macOS and a systemd user unit on Linux.
#
# Copies gateway.py, run.sh, ctl.sh and README.md to ~/.local/share/advisor-cache-gateway,
# writes config.env and install.env there, and writes the service. A first install
# turns the gateway on. An update keeps the current on/off state:
#   - on: the change applies through a graceful restart, so no stream is cut;
#   - off: only the files change, and `ctl.sh on` turns it on.
#
# Options: --ttl 1h|5m|off for main sessions (default 5m), --subagent-ttl 1h|5m|off
# (default 5m), --max-tokens N (default none), --port N (default 18787).
# Environment, for a second install beside the first: ADVISOR_GATEWAY_LABEL, ADVISOR_GATEWAY_DEST,
# ADVISOR_GATEWAY_LOGS, CLAUDE_SETTINGS, ADVISOR_GATEWAY_PYTHON.
set -eu

TTL=5m
SUB_TTL=5m
MAX_TOKENS=
PORT=18787
while [ $# -gt 0 ]; do
  case "$1" in
    --ttl) TTL=$2; shift 2 ;;
    --subagent-ttl) SUB_TTL=$2; shift 2 ;;
    --max-tokens) MAX_TOKENS=$2; shift 2 ;;
    --port) PORT=$2; shift 2 ;;
    *) echo "usage: install.sh [--ttl 1h|5m|off] [--subagent-ttl 1h|5m|off] [--max-tokens N] [--port N]" >&2; exit 2 ;;
  esac
done
case "$TTL" in 1h|5m|off) ;; *) echo "install.sh: --ttl must be 1h, 5m or off" >&2; exit 2 ;; esac
case "$SUB_TTL" in 1h|5m|off) ;; *) echo "install.sh: --subagent-ttl must be 1h, 5m or off" >&2; exit 2 ;; esac
if [ -n "$MAX_TOKENS" ] && ! [ "$MAX_TOKENS" -ge 1024 ] 2>/dev/null; then
  echo "install.sh: --max-tokens must be a number >= 1024" >&2; exit 2
fi
case "$PORT" in ''|*[!0-9]*) echo "install.sh: --port must be a number" >&2; exit 2 ;; esac

SRC=$(cd "$(dirname "$0")" && pwd)
OS=$(uname -s)
DEST=${ADVISOR_GATEWAY_DEST:-$HOME/.local/share/advisor-cache-gateway}
BACKUPS="$HOME/.local/state/advisor-cache-gateway/backups"
PY=${ADVISOR_GATEWAY_PYTHON:-/usr/bin/python3}
URL="http://127.0.0.1:$PORT"
# A stop drains in-flight requests for up to DRAIN seconds; the service manager must wait longer than that.
DRAIN=600
CONF="$DEST/config.env"
# Where Claude Code goes without the gateway: the upstream it forwards to (config.env may name another).
# shellcheck source=/dev/null
DIRECT=$( [ -f "$CONF" ] && . "$CONF"; printf '%s' "${ADVISOR_CACHE_UPSTREAM:-https://api.anthropic.com}")
case "$OS" in
  Darwin)
    LABEL=${ADVISOR_GATEWAY_LABEL:-local.advisor-cache-gateway}
    SERVICE_FILE="$HOME/Library/LaunchAgents/$LABEL.plist"
    LOGS=${ADVISOR_GATEWAY_LOGS:-$HOME/Library/Logs}
    DOMAIN="gui/$(id -u)"
    ;;
  Linux)
    systemctl --user show-environment >/dev/null 2>&1 || { echo "install.sh: no systemd user manager (systemctl --user)" >&2; exit 1; }
    LABEL=${ADVISOR_GATEWAY_LABEL:-advisor-cache-gateway}
    SERVICE_FILE="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/$LABEL.service"
    # The socket unit holds the listener that every gateway generation inherits and shares (see run.sh).
    SOCKET_FILE="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/$LABEL.socket"
    LOGS=${ADVISOR_GATEWAY_LOGS:-$HOME/.local/state/advisor-cache-gateway/logs}
    UNIT="$LABEL.service"
    SOCKET="$LABEL.socket"
    ;;
  *) echo "install.sh: $OS is not supported (macOS or Linux)" >&2; exit 1 ;;
esac

JQ=$(command -v jq) || { echo "install.sh: jq is required" >&2; exit 1; }
command -v curl >/dev/null || { echo "install.sh: curl is required" >&2; exit 1; }
"$PY" -I -c 'import sys; assert sys.version_info >= (3, 9)' || { echo "install.sh: $PY must be Python 3.9+" >&2; exit 1; }
# run.sh rewrites the file the setting lives in, so it gets the real path, not a symlink to it.
SETTINGS=$("$PY" -I -c 'import os, sys; print(os.path.realpath(sys.argv[1]))' "${CLAUDE_SETTINGS:-$HOME/.claude/settings.json}")
[ -f "$SETTINGS" ] || { echo "install.sh: no Claude Code settings at $SETTINGS" >&2; exit 1; }
"$PY" -I -m unittest discover -s "$SRC/tests" >/dev/null 2>&1 || { echo "install.sh: tests fail; run: $PY -I -m unittest discover -s $SRC/tests" >&2; exit 1; }

health() { curl -sf --max-time 1 "$URL/__gateway/health" 2>/dev/null; }
serving_pid() { health | sed -n 's/^ok pid=\([0-9]*\).*/\1/p'; }
mode_of() { stat -c %a "$1" 2>/dev/null || stat -f %Lp "$1"; }

CUR=$("$JQ" -r '.env.ANTHROPIC_BASE_URL // ""' "$SETTINGS")
if [ -n "$CUR" ] && [ "$CUR" != "$URL" ] && [ "$CUR" != "$DIRECT" ]; then
  # Another URL (an earlier port, another proxy) would keep run.sh from linking; this install points it
  # at the API. Rewritten, not deleted: running Claude Code sessions follow a change but keep a removed URL.
  mkdir -p "$BACKUPS"
  cp -p "$SETTINGS" "$BACKUPS/settings.json.pre-install-$(date +%Y%m%d-%H%M%S)"
  MODE=$(mode_of "$SETTINGS")
  TMP=$(mktemp "$SETTINGS.XXXXXX")
  "$JQ" --arg direct "$DIRECT" '.env.ANTHROPIC_BASE_URL = $direct' "$SETTINGS" > "$TMP"
  chmod "$MODE" "$TMP"
  mv "$TMP" "$SETTINGS"
  echo "pointed the earlier ANTHROPIC_BASE_URL $CUR at $DIRECT (backup in $BACKUPS)"
fi

LOADED=no
DISABLED=no
if [ "$OS" = Darwin ]; then
  launchctl print "$DOMAIN/$LABEL" >/dev/null 2>&1 && LOADED=yes
  launchctl print-disabled "$DOMAIN" 2>/dev/null | grep -q "\"$LABEL\" => \(disabled\|true\)" && DISABLED=yes
else
  systemctl --user is-active --quiet "$UNIT" && LOADED=yes
  [ -f "$SERVICE_FILE" ] && [ "$(systemctl --user is-enabled "$UNIT" 2>/dev/null)" = disabled ] && DISABLED=yes
fi
# run.sh supervises from memory: a new run.sh needs the service itself restarted, not just a HUP.
RUN_CHANGED=yes
cmp -s "$SRC/run.sh" "$DEST/run.sh" 2>/dev/null && RUN_CHANGED=no

mkdir -p "$DEST" "$(dirname "$SERVICE_FILE")" "$LOGS"
for f in gateway.py run.sh ctl.sh README.md; do
  cp "$SRC/$f" "$DEST/$f.new" && mv "$DEST/$f.new" "$DEST/$f"
done
chmod +x "$DEST/run.sh" "$DEST/ctl.sh"
HEAD1="# Read by run.sh at every start and graceful restart (ctl.sh restart)."
HEAD2="# install.sh rewrites the four settings below; every other line is yours and is kept."
{
  printf '%s\n%s\n' "$HEAD1" "$HEAD2"
  cat <<EOF
ADVISOR_CACHE_TTL=$TTL
ADVISOR_CACHE_TTL_SUBAGENT=$SUB_TTL
ADVISOR_MAX_TOKENS=$MAX_TOKENS
ADVISOR_CACHE_DRAIN_SECONDS=$DRAIN
EOF
  if [ -f "$CONF" ]; then
    grep -vE '^(ADVISOR_CACHE_TTL|ADVISOR_CACHE_TTL_SUBAGENT|ADVISOR_MAX_TOKENS|ADVISOR_CACHE_DRAIN_SECONDS)=' "$CONF" \
      | grep -vxF -e "$HEAD1" -e "$HEAD2" || true
  fi
} > "$CONF.new"
mv "$CONF.new" "$CONF"
cat > "$DEST/install.env.new" <<EOF
# Written by install.sh; read by ctl.sh and uninstall.sh.
LABEL='$LABEL'
SERVICE_FILE='$SERVICE_FILE'
SOCKET_FILE='${SOCKET_FILE:-}'
PORT='$PORT'
SETTINGS='$SETTINGS'
JQ='$JQ'
PY='$PY'
LOGS='$LOGS'
EOF
mv "$DEST/install.env.new" "$DEST/install.env"

if [ "$OS" = Darwin ]; then
  cat > "$SERVICE_FILE.new" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$LABEL</string>
  <key>ProgramArguments</key>
  <array><string>/bin/sh</string><string>$DEST/run.sh</string><string>serve</string></array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>ADVISOR_CACHE_GATEWAY_PORT</key><string>$PORT</string>
    <key>ADVISOR_CACHE_GATEWAY_LOG</key><string>$LOGS/advisor-cache-gateway.log</string>
    <key>ADVISOR_GATEWAY_PYTHON</key><string>$PY</string>
    <key>ADVISOR_GATEWAY_JQ</key><string>$JQ</string>
    <key>CLAUDE_SETTINGS</key><string>$SETTINGS</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ThrottleInterval</key><integer>2</integer>
  <key>ExitTimeOut</key><integer>$((DRAIN + 30))</integer>
  <key>ProcessType</key><string>Interactive</string>
  <key>StandardErrorPath</key><string>$LOGS/advisor-cache-gateway.stderr.log</string>
</dict>
</plist>
EOF
  plutil -lint "$SERVICE_FILE.new" >/dev/null
else
  cat > "$SERVICE_FILE.new" <<EOF
[Unit]
Description=advisor-cache-gateway: prompt cache for Claude Code's advisor tool

Requires=$SOCKET
After=$SOCKET

[Service]
ExecStart=/bin/sh $DEST/run.sh serve
Sockets=$SOCKET
Environment=ADVISOR_CACHE_GATEWAY_PORT=$PORT
Environment=ADVISOR_CACHE_GATEWAY_LOG=$LOGS/advisor-cache-gateway.log
Environment=ADVISOR_GATEWAY_PYTHON=$PY
Environment=ADVISOR_GATEWAY_JQ=$JQ
Environment=CLAUDE_SETTINGS=$SETTINGS
# A stop sends SIGTERM to run.sh alone: it releases Claude Code first, then drains its gateways.
KillMode=mixed
TimeoutStopSec=$((DRAIN + 30))
Restart=always
RestartSec=2
StandardError=append:$LOGS/advisor-cache-gateway.stderr.log

[Install]
WantedBy=default.target
EOF
  cat > "$SOCKET_FILE.new" <<EOF
[Unit]
Description=advisor-cache-gateway listener on $URL

[Socket]
ListenStream=127.0.0.1:$PORT
Backlog=512

[Install]
WantedBy=sockets.target
EOF
fi
SERVICE_CHANGED=yes
cmp -s "$SERVICE_FILE.new" "$SERVICE_FILE" 2>/dev/null && SERVICE_CHANGED=no
mv "$SERVICE_FILE.new" "$SERVICE_FILE"
if [ "$OS" = Linux ]; then
  cmp -s "$SOCKET_FILE.new" "$SOCKET_FILE" 2>/dev/null || SERVICE_CHANGED=yes
  mv "$SOCKET_FILE.new" "$SOCKET_FILE"
  systemctl --user daemon-reload
fi

if [ "$DISABLED" = yes ]; then
  echo "installed; the gateway is off. Turn it on with: $DEST/ctl.sh on"
  exit 0
fi

if [ "$LOADED" = yes ] && [ "$SERVICE_CHANGED" = no ] && [ "$RUN_CHANGED" = no ]; then
  BEFORE=$(serving_pid)
  if [ "$OS" = Darwin ]; then
    launchctl kill HUP "$DOMAIN/$LABEL"
  else
    systemctl --user kill -s HUP --kill-whom=main "$UNIT"
  fi
  i=0
  until AFTER=$(serving_pid) && [ -n "$AFTER" ] && [ "$AFTER" != "$BEFORE" ]; do
    i=$((i + 1))
    if [ $i -ge 80 ]; then
      echo "install.sh: the graceful restart did not take over within 20 seconds; the old gateway keeps serving" >&2
      tail -5 "$LOGS/advisor-cache-gateway.stderr.log" >&2 2>/dev/null || true
      exit 1
    fi
    sleep 0.25
  done
  echo "updated through a graceful restart: $(health)"
  exit 0
fi

if [ "$OS" = Darwin ]; then
  if [ "$LOADED" = yes ]; then
    # The agent itself must reload. Stopping releases Claude Code to the API first, so new requests
    # go straight to it while the old gateway drains its streams; none is cut.
    echo "reloading the agent (the old gateway drains in the background)"
    launchctl bootout "$DOMAIN/$LABEL" 2>/dev/null &
    i=0
    while launchctl print "$DOMAIN/$LABEL" >/dev/null 2>&1 && [ $i -lt $((DRAIN * 4 + 120)) ]; do sleep 0.25; i=$((i + 1)); done
  fi
  launchctl bootstrap "$DOMAIN" "$SERVICE_FILE"
else
  if [ "$LOADED" = yes ]; then
    # Same reason: the stop releases Claude Code first and returns once the old gateway drained. The
    # listener restarts with it, since a changed socket unit cannot take over a port still held.
    echo "restarting the service (the old gateway drains first)"
    systemctl --user stop "$UNIT" "$SOCKET"
  fi
  systemctl --user -q enable --now "$SOCKET" "$UNIT"
fi

i=0
until health >/dev/null && [ "$("$JQ" -r '.env.ANTHROPIC_BASE_URL // ""' "$SETTINGS")" = "$URL" ]; do
  i=$((i + 1))
  if [ $i -ge 80 ]; then
    echo "install.sh: the gateway did not come up within 20 seconds; Claude Code still talks to the API directly" >&2
    tail -5 "$LOGS/advisor-cache-gateway.stderr.log" 2>/dev/null >&2 || true
    exit 1
  fi
  sleep 0.25
done
echo "gateway on: $(health)"
echo "Claude Code uses $URL ($SETTINGS)"
echo "on/off: $DEST/ctl.sh on|off|restart|status; logs in $LOGS; captures in ${ADVISOR_CACHE_CAPTURE_DIR:-$HOME/.professor/tmp/gw-logs}"

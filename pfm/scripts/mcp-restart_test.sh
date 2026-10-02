#!/usr/bin/env bash
# Self-test for make mcp-restart: a failed systemd restart prints the unit's
# last log lines, or says that journalctl is unavailable, under a private HOME.
set -uo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
# shellcheck disable=SC2034 # read by shtest.sh
SHTEST_TAG=pfm-mcp-restart-test
# shellcheck source=/dev/null
source "$ROOT/../scripts/shtest.sh"

STUBS="$T/stubs"
STATE="$T/state"
CALLS="$T/calls"
mkdir -p "$STUBS" "$STATE"
printf '#!/usr/bin/env bash\nexit 0\n' > "$STUBS/sleep"
cat > "$STUBS/systemctl" <<EOF
#!/usr/bin/env bash
echo "systemctl \$*" >> "$CALLS"
case "\$2" in
  cat) exit 0;;
  restart) exit 0;;
  is-active) cat "$STATE/active"; [ "\$(cat "$STATE/active")" = active ];;
  show) echo 4242;;
esac
EOF
cat > "$STUBS/journalctl" <<EOF
#!/usr/bin/env bash
echo "journalctl \$*" >> "$CALLS"
[ -e "$STATE/nojournal" ] && exit 1
echo "crash line"
EOF
printf '#!/usr/bin/env bash\necho Linux\n' > "$STUBS/uname"
chmod 755 "$STUBS"/*

newhome() {
  H="$T/home"
  mkdir -p "$H"
  echo active > "$STATE/active"
}

mk() {
  env -u MAKEFLAGS -u MAKELEVEL -u MFLAGS -u FORCE -u SKIP_INSTALL_CHECK HOME="$H" PATH="$STUBS:$PATH" \
    make --no-print-directory -C "$ROOT" "$@" 2>&1
}

# ---- make mcp-restart shows the unit's logs on failure ----------------------
newhome
echo activating > "$STATE/active"
out="$(mk mcp-restart)"; rc=$?
if [ $rc -ne 0 ] && [[ "$out" == *"MCP-RESTART-FAILED"*$'\n'"mcp: last log lines of pfm-mcp.service:"$'\n'"crash line"* ]]; then
  ok "mcp-restart: a failed restart prints the unit's last log lines"
else
  bad "mcp-restart: expected the failure, the log header and the log" "$out (rc $rc)"
fi
: > "$STATE/nojournal"
out="$(mk mcp-restart)"; rc=$?
if [ $rc -ne 0 ] && [[ "$out" == *"mcp: last log lines of pfm-mcp.service:"$'\n'"(journalctl unavailable)"* ]]; then
  ok "mcp-restart: without journalctl it says so, same failure"
else
  bad "mcp-restart: expected (journalctl unavailable), exit non-zero" "$out (rc $rc)"
fi

shtest_end

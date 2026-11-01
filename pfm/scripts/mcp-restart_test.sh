#!/usr/bin/env bash
# Self-test for make mcp-restart: a failed systemd restart prints the unit's
# last log lines, or says that journalctl is unavailable, under a private HOME;
# with no unit or agent, an unsupervised daemon is listed with its
# identity-checked stop command and left running.
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
  cat) [ ! -e "$STATE/nounit" ];;
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
# pgrep answers only the pids this suite started, exiting 1 on none as pgrep
# does; pkill records, never signals, so no host process matching the daemon's
# pattern is ever touched.
cat > "$STUBS/pgrep" <<EOF
#!/usr/bin/env bash
[ ! -e "$STATE/pgrepfails" ] || { echo "pgrep: cannot read /proc" >&2; exit 3; }
[ -s "$STATE/pids" ] || exit 1
cat "$STATE/pids"
EOF
cat > "$STUBS/pkill" <<EOF
#!/usr/bin/env bash
echo "pkill \$*" >> "$CALLS"
EOF
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

# ---- make mcp-restart lists an unsupervised daemon, never kills it ----------
newhome
: > "$STATE/nounit"
sleep 300 &
daemon=$!
echo "$daemon" > "$STATE/pids"
out="$(mk mcp-restart)"; rc=$?
want="mcp: no pfm-mcp.service unit or com.professor.pfm.mcp agent supervises 'pfm mcp serve'; make stops no process by name — each one below still runs the binary it started on:
  pid $daemon: sleep 300
    stop it: ps -o args= -p $daemon | grep -qxF 'sleep 300' && kill $daemon
mcp: make sweep-stale stops each one that runs a replaced binary; the next client then starts a fresh daemon"
if [ $rc -eq 0 ] && [ "$out" = "$want" ] && kill -0 "$daemon" 2>/dev/null; then
  ok "mcp-restart: an unsupervised daemon is listed with its identity-checked stop and keeps running"
else
  bad "mcp-restart: expected the listing, rc 0 and a live pid $daemon" "want: $want" "got:  $out (rc $rc)"
fi
kill "$daemon"
wait "$daemon" 2>/dev/null
: > "$STATE/pids"
out="$(mk mcp-restart)"; rc=$?
if [ $rc -eq 0 ] && [ "$out" = "mcp: no daemon running and no pfm-mcp.service unit or com.professor.pfm.mcp agent — nothing to restart" ]; then
  ok "mcp-restart: no unit, no agent and no daemon says nothing to restart"
else
  bad "mcp-restart: expected the nothing-to-restart line, rc 0" "$out (rc $rc)"
fi
: > "$STATE/pgrepfails"
out="$(mk mcp-restart)"; rc=$?
want="pgrep: cannot read /proc
mcp: pgrep failed (rc 3) — could not look for an unsupervised 'pfm mcp serve'; no pfm-mcp.service unit or com.professor.pfm.mcp agent either"
if [ $rc -eq 0 ] && [ "$out" = "$want" ]; then
  ok "mcp-restart: a pgrep that cannot run says it could not look, never nothing to restart"
else
  bad "mcp-restart: expected the could-not-look line, rc 0" "want: $want" "got:  $out (rc $rc)"
fi
rm -f "$STATE/pgrepfails"

shtest_end

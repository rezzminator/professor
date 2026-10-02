#!/usr/bin/env bash
# Self-test for the swap-to-migrate window in make: host-migration-probe.sh's
# three answers, then the real pfm/Makefile's host-install, install, rollback
# and mcp-restart targets under a private HOME. Between make host-install
# swapping the binary and `pfm install --yes` migrating a legacy host, every
# target must say what to do instead of failing obscurely: host-install names
# the pending migration and still exits 0; install stops before restarting or
# rolling back anything; rollback runs its guard first; a failed mcp-restart
# shows the unit's last log lines.
#
# Stubs first on PATH play the host: `go` builds a stub pfm (its --version
# carries VERSION; `version -m` fails, so the downgrade guard warns and
# passes), `systemctl`, `journalctl`, `pgrep` and `uname` (Linux). Every stub
# call is appended to $T/calls.
set -uo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
PROBE="$ROOT/scripts/host-migration-probe.sh"
# shellcheck disable=SC2034 # read by shtest.sh
SHTEST_TAG=pfm-host-window-test
# shellcheck source=/dev/null
source "$ROOT/../scripts/shtest.sh"

VERSION="$(cat "$ROOT/../VERSION")"
STUBS="$T/stubs"
STATE="$T/state"
CALLS="$T/calls"
mkdir -p "$STUBS" "$STATE"
printf '#!/usr/bin/env bash\nexit 0\n' > "$STUBS/sleep"
chmod 755 "$STUBS/sleep"

UNMIGRATED_LINE='pfm config show: configuration error: config not migrated: run pfm install (legacy present)'
# shellcheck disable=SC2016 # the backticks are literal text
SWAPPED_LINE='host-install: binary swapped; pfm refuses until `pfm install --yes` migrates this host — run it now'

# The stub pfm: its behaviour follows $STATE at call time.
cat > "$T/pfm-stub" <<EOF
#!/usr/bin/env bash
echo "pfm \$*" >> "$CALLS"
case "\$1" in
  --version) echo "pfm $VERSION (stub)";;
  config) [ -e "$STATE/boom" ] && { echo boom >&2; exit 1; }
    [ -e "$STATE/unmigrated" ] && echo '$UNMIGRATED_LINE' >&2; exit 0;;
  internal) echo "stale: none";;
  install) [ "\$2" = --check ] || exit 0
    [ -e "$STATE/gateblock" ] && { printf '%s\n' 'pfm install: refused before any change:' '  refuse  layout chats claude — live chat pid 4242' 'install check: blocked — close what it names, then rerun make -C clone/pfm host-install' >&2; exit 4; }
    [ -e "$STATE/checkerr" ] && { echo 'pfm install --check: layout environment: boom' >&2; exit 1; }
    echo 'install check: ok — the install gate would pass';;
esac
exit 0
EOF
chmod 755 "$T/pfm-stub"

cat > "$STUBS/go" <<EOF
#!/usr/bin/env bash
echo "go \$*" >> "$CALLS"
case "\$1" in
  env) [ "\$2" = GOOS ] && echo linux || echo amd64; exit 0;;
  build)
    out=""
    while [ \$# -gt 0 ]; do [ "\$1" = -o ] && out="\$2"; shift; done
    cp -f "$T/pfm-stub" "\$out" && chmod 755 "\$out"; exit \$?;;
esac
exit 1
EOF
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
printf '#!/usr/bin/env bash\nexit 1\n' > "$STUBS/pgrep"
printf '#!/usr/bin/env bash\necho Linux\n' > "$STUBS/uname"
chmod 755 "$STUBS"/*

N=0
# newhome <migrated|unmigrated>: a fresh HOME with an installed pfm; sets H, BINP.
newhome() {
  N=$((N + 1))
  H="$T/home$N"
  BINP="$H/.local/bin/pfm"
  mkdir -p "$H/.local/bin"
  cp -f "$T/pfm-stub" "$BINP"
  rm -f "$STATE"/* "$CALLS"
  [ "$1" = unmigrated ] && : > "$STATE/unmigrated"
  echo active > "$STATE/active"
}

mk() {
  env -u MAKEFLAGS -u MAKELEVEL -u MFLAGS -u FORCE -u SKIP_INSTALL_CHECK HOME="$H" PATH="$STUBS:$PATH" \
    make --no-print-directory -C "$ROOT" "$@" 2>&1
}

# ---- the probe --------------------------------------------------------------
newhome unmigrated
out="$(bash "$PROBE" "$BINP" 2>&1)"; rc=$?
if [ $rc -eq 3 ] && [ "$out" = "$SWAPPED_LINE" ]; then
  ok "probe: an unmigrated host exits 3 with the swapped line"
else
  bad "probe: expected exit 3 and the swapped line" "$out (rc $rc)"
fi

newhome migrated
out="$(bash "$PROBE" "$BINP" 2>&1)"; rc=$?
if [ $rc -eq 0 ] && [ -z "$out" ]; then
  ok "probe: a migrated host exits 0 silently"
else
  bad "probe: expected silent exit 0" "$out (rc $rc)"
fi

printf '#!/usr/bin/env bash\necho boom >&2\nexit 1\n' > "$T/pfm-boom"
chmod 755 "$T/pfm-boom"
out="$(bash "$PROBE" "$T/pfm-boom" 2>&1)"; rc=$?
if [ $rc -eq 2 ] && [ "$out" = "host-install: could not ask $T/pfm-boom whether this host is migrated: boom" ]; then
  ok "probe: a binary that cannot answer exits 2 naming it"
else
  bad "probe: expected exit 2 and the could-not-ask line" "$out (rc $rc)"
fi

# ---- make host-install on a legacy host -------------------------------------
newhome unmigrated
out="$(mk host-install)"; rc=$?
if [ $rc -eq 0 ] && [[ "$out" == *"installed: $BINP ("*$'\n'"$SWAPPED_LINE"* ]] && [ -x "$BINP.prev" ]; then
  ok "host-install: a legacy host swaps, keeps pfm.prev, names the pending migration, exit 0"
else
  bad "host-install: expected installed then the swapped line, pfm.prev kept, exit 0" "$out (rc $rc)"
fi

# ---- make host-install while the new binary's install check refuses --------
# The installed pfm differs from the build, so a swap would change its bytes.
REFUSES_LINE="host-install: the new binary's install check refuses this host — BINP untouched; close or resolve what it names above, then rerun (a live chat includes Claude's daemon and bg-spare sessions: claude daemon stop --any)"
# refused <name> <state-file> <want>: an unmigrated host keeps pfm; the
# literal BINP in <want> stands for this home's pfm.
refused() {
  newhome unmigrated
  local want="${3//BINP/$BINP}"
  printf '# installed before\n' >> "$BINP"
  : > "$STATE/$2"
  before="$(cksum < "$BINP")"
  out="$(mk host-install)"; rc=$?
  if [ $rc -ne 0 ] && [[ "$out" == *"host-install] Error 1"* ]] && [ "$(cksum < "$BINP")" = "$before" ] \
    && [ ! -e "$BINP.prev" ] && grep -q 'pfm install --check' "$CALLS" && [[ "$out" == *"$want"* ]] \
    && [[ "$out" != *"installed:"* ]] && ! compgen -G "$BINP.new.*" >/dev/null; then
    ok "host-install: $1"
  else
    bad "host-install: $1" "$out (rc $rc)"
  fi
}
refused "an unmigrated host whose gate refuses fails before the swap, pfm byte-identical, no pfm.prev" gateblock \
  "install check: blocked — close what it names, then rerun make -C clone/pfm host-install"$'\n'"$REFUSES_LINE"
refused "an unmigrated host whose check cannot answer fails naming its error, pfm untouched" checkerr \
  "pfm install --check: layout environment: boom"$'\n'"host-install: the new binary's install check FAILED (exit 1) — BINP untouched"

newhome migrated
printf '# installed before\n' >> "$BINP"
: > "$STATE/gateblock"
before="$(cksum < "$BINP")"
out="$(mk host-install)"; rc=$?
if [ $rc -eq 0 ] && [ "$(cksum < "$BINP")" != "$before" ] && [ -x "$BINP.prev" ] \
  && [[ "$out" == *"refuse  layout chats claude"*"host-install: WARNING — the new binary's install check refuses (exit 4), but this host is migrated"*"installed: $BINP ("* ]]; then
  ok "host-install: a migrated host whose gate refuses warns and swaps"
else
  bad "host-install: expected the gate's lines, the WARNING and the swap" "$out (rc $rc)"
fi

newhome unmigrated
printf '# installed before\n' >> "$BINP"
: > "$STATE/gateblock"
before="$(cksum < "$BINP")"
out="$(env -u MAKEFLAGS -u MAKELEVEL -u MFLAGS -u SKIP_INSTALL_CHECK FORCE=1 HOME="$H" PATH="$STUBS:$PATH" \
  make --no-print-directory -C "$ROOT" host-install 2>&1)"; rc=$?
if [ $rc -ne 0 ] && [ "$(cksum < "$BINP")" = "$before" ] && grep -q 'pfm install --check' "$CALLS" \
  && [[ "$out" == *"${REFUSES_LINE/BINP/$BINP}"* ]]; then
  ok "host-install: FORCE=1 is the downgrade guard's alone — the check still runs and refuses"
else
  bad "host-install: expected FORCE=1 to leave the check armed" "$out (rc $rc)" "$(cat "$CALLS")"
fi

newhome unmigrated
printf '# installed before\n' >> "$BINP"
: > "$STATE/gateblock"
before="$(cksum < "$BINP")"
out="$(env -u MAKEFLAGS -u MAKELEVEL -u MFLAGS -u FORCE SKIP_INSTALL_CHECK=1 HOME="$H" PATH="$STUBS:$PATH" \
  make --no-print-directory -C "$ROOT" host-install 2>&1)"; rc=$?
if [ $rc -eq 0 ] && [ "$(cksum < "$BINP")" != "$before" ] && [ -x "$BINP.prev" ] \
  && ! grep -q 'pfm install --check' "$CALLS" \
  && [[ "$out" == *"host-install: SKIPPED the install check (SKIP_INSTALL_CHECK=1) — pfm install --check not run"* ]]; then
  ok "host-install: SKIP_INSTALL_CHECK=1 skips the check with a SKIPPED line and swaps"
else
  bad "host-install: expected the SKIPPED line, no check, the swap" "$out (rc $rc)" "$(cat "$CALLS")"
fi

newhome unmigrated
: > "$STATE/gateblock"
out="$(mk install)"; rc=$?
if [ $rc -ne 0 ] && [[ "$out" == *"${REFUSES_LINE/BINP/$BINP}"* ]] \
  && ! grep -q 'systemctl --user restart' "$CALLS" && [[ "$out" != *"rollback:"* ]] \
  && [[ "$out" != *"install: stopped after host-install"* ]]; then
  ok "install: a refused check stops at host-install, nothing restarted or rolled back"
else
  bad "install: expected the refuses line, no restart, no rollback" "$out (rc $rc)" "$(cat "$CALLS")"
fi

# ---- make install on a legacy host ------------------------------------------
newhome unmigrated
out="$(mk install)"; rc=$?
if [ $rc -ne 0 ] \
  && [[ "$out" == *"install: stopped after host-install; nothing restarted or rolled back — run pfm install --yes now, then make -C $(cd "$ROOT/.." && pwd -P)/pfm sweep-stale"* ]] \
  && ! grep -q 'systemctl --user restart' "$CALLS" && [[ "$out" != *"rollback:"* ]]; then
  ok "install: a legacy host stops after host-install, nothing restarted or rolled back"
else
  bad "install: expected the stop line, no restart, no rollback" "$out (rc $rc)" "$(cat "$CALLS")"
fi

# ---- make install on a migrated host ----------------------------------------
newhome migrated
out="$(mk install)"; rc=$?
if [ $rc -eq 0 ] && grep -q 'systemctl --user restart pfm-mcp.service' "$CALLS" \
  && grep -q 'pfm internal stale --sweep' "$CALLS" && [[ "$out" == *"install: complete"* ]]; then
  ok "install: a migrated host restarts the daemon and sweeps as before"
else
  bad "install: expected restart, sweep and complete, exit 0" "$out (rc $rc)" "$(cat "$CALLS")"
fi

# ---- make install when the probe could not ask -------------------------------
# Exit 2 is no migration: host-install printed it; the restart judges the binary.
newhome migrated
: > "$STATE/boom"
out="$(mk install)"; rc=$?
if [ $rc -eq 0 ] && [[ "$out" != *"install: stopped after host-install"* ]] \
  && [[ "$out" == *"host-install: could not ask $BINP whether this host is migrated: boom"* ]] \
  && grep -q 'systemctl --user restart pfm-mcp.service' "$CALLS"; then
  ok "install: a probe that could not ask names it and does not send the operator to migrate"
else
  bad "install: expected the could-not-ask line, no stop line, the restart" "$out (rc $rc)" "$(cat "$CALLS")"
fi

# ---- make mcp-restart shows the unit's logs on failure ----------------------
newhome migrated
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

# ---- make rollback runs the guard first --------------------------------------
newhome migrated
printf 'previous\n' > "$BINP.prev"
chmod 755 "$BINP.prev"
touch -d '2026-01-01T00:00:00Z' "$BINP.prev"
mkdir -p "$H/.local/state/pfm/migrations/20260102T000000Z"
printf '[\n  {\n    "row": "config",\n    "result": "done"\n  }\n]\n' \
  > "$H/.local/state/pfm/migrations/20260102T000000Z/journal.json"
before="$(cksum < "$BINP")"
out="$(mk rollback)"; rc=$?
if [ $rc -ne 0 ] && [[ "$out" == *"rollback: REFUSED"* ]] && [ "$(cksum < "$BINP")" = "$before" ]; then
  ok "rollback: the guard refuses first and pfm is unchanged"
else
  bad "rollback: expected the guard's refusal and an untouched pfm" "$out (rc $rc)"
fi

newhome migrated
out="$(mk rollback)"; rc=$?
if [ $rc -ne 0 ] && [[ "$out" == *"rollback: no $BINP.prev to restore"* ]]; then
  ok "rollback: without pfm.prev the guard passes and the existing message prints"
else
  bad "rollback: expected the no-pfm.prev message" "$out (rc $rc)"
fi

shtest_end

#!/usr/bin/env bash
# Asks a freshly swapped pfm whether this host still waits for `pfm install
# --yes` to migrate its legacy layout. Between make host-install and that
# migration the new binary refuses every command but the config-free ones;
# make host-install and make install call this to say so instead of failing
# obscurely later.
#
# usage: host-migration-probe.sh <pfm-binary>
#
# The signal is `pfm config show`'s stderr: it prints
# `pfm config show: configuration error: {err}` and exits 0 while the config
# is not migrated (cmd/pfm TestConfigShowNamesNotMigrated pins it).
#   exit 3: not migrated — the pinned `host-install: binary swapped; …` line
#   exit 2: the binary could not be asked — the question and its first stderr line
#   exit 0: migrated — silent
set -uo pipefail

BIN="${1:-}"
[ -n "$BIN" ] || { echo "usage: host-migration-probe.sh <pfm-binary>" >&2; exit 2; }

err="$("$BIN" config show 2>&1 >/dev/null)"
rc=$?
if [[ "$err" == *"config not migrated"* ]]; then
  # shellcheck disable=SC2016 # the backticks are literal text
  echo 'host-install: binary swapped; pfm refuses until `pfm install --yes` migrates this host — run it now' >&2
  exit 3
fi
if [ "$rc" -ne 0 ]; then
  echo "host-install: could not ask $BIN whether this host is migrated: $(printf '%s\n' "$err" | head -n 1)" >&2
  exit 2
fi
exit 0

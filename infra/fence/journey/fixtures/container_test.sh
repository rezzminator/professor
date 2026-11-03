#!/usr/bin/env bash
# Faked Docker enters through the CLI seam; the manager readiness door runs unchanged.
set -euo pipefail
HERE="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
. "$HERE/container.sh"
scratch="$(mktemp -d)"
trap 'rm -rf "$scratch"' EXIT
expect() {
  [ "$1" = "$2" ] || { printf 'expected=%q actual=%q artifact=%s\n' "$1" "$2" "$3" >&2; exit 1; }
}
docker() {
  case "$1" in
    inspect)
      if [ "$3" = '{{json .State}}' ]; then printf '{"Running":%s}\n' "$FAKE_RUNNING"
      else printf '%s\n' "$FAKE_RUNNING"; fi ;;
    logs) printf 'fixture container log\n' ;;
    ps) printf 'fixture-container\taaaaaaaaaaaaaaaaaaaa\n' ;;
    exec)
      case "$*" in
        *is-system-running)
          count="$(cat "$scratch/probes")"; count="$((count + 1))"; printf '%s\n' "$count" >"$scratch/probes"
          if [ "$MODE" = pending ] && [ "$count" -eq 1 ]; then return 1; fi
          printf '%s\n' "$FAKE_STATE"
          [ "$FAKE_STATE" = running ] ;;
        *journalctl*) printf 'fixture journal\n' ;;
        *) printf 'fixture-broken.service loaded failed failed\n' ;;
      esac ;;
    *) printf 'unexpected Docker operation %s\n' "$*" >&2; return 2 ;;
  esac
}
if [ "${1:-}" = --mutate ]; then
  case "${2:-readiness}" in
    readiness) eval "$(declare -f journey_boot | sed 's/\[ "\$state" = running \]/true/')" ;;
    failure) eval "$(declare -f journey_boot | sed 's/return 2/return 0/')" ;;
    journal) eval "$(declare -f journey_boot | sed '/journalctl/d')" ;;
    *) printf 'unknown boot mutation\n' >&2; exit 2 ;;
  esac
fi
MODE=pending FAKE_RUNNING=true FAKE_STATE=running
printf '0\n' >"$scratch/probes"
journey_boot fixture "$scratch" >"$scratch/ready.out"
expect 2 "$(cat "$scratch/probes")" "$scratch/probes"
expect 'journey machine-ready systemd=running wall_ms=<duration>' \
  "$(sed -E 's/wall_ms=[0-9]+/wall_ms=<duration>/' "$scratch/ready.out")" "$scratch/ready.out"
for MODE in degraded exited; do
  FAKE_RUNNING=true FAKE_STATE=degraded
  if [ "$MODE" = exited ]; then FAKE_RUNNING=false FAKE_STATE=unreachable; fi
  printf '0\n' >"$scratch/probes"
  if journey_boot fixture "$scratch" >"$scratch/failed.out" 2>"$scratch/failed.err"; then status=0; else status="$?"; fi
  expect 2 "$status" "$scratch/failed.err"
  expect "journey BOOT-FAILED systemd=$FAKE_STATE evidence=$scratch/root-boot.log" "$(cat "$scratch/failed.err")" "$scratch/failed.err"
  expect "BOOT-FAILED docker-running=$FAKE_RUNNING systemd=$FAKE_STATE" "$(head -n 1 "$scratch/root-boot.log")" "$scratch/root-boot.log"
  expect 'fixture journal' "$(tail -n 1 "$scratch/root-boot.log")" "$scratch/root-boot.log"
done
JOURNEY_CACHE_CONTROL="$scratch/control" JOURNEY_CACHE_PROJECTED="$scratch/projected"
mkdir -p "$JOURNEY_CACHE_CONTROL" "$JOURNEY_CACHE_PROJECTED"
touch "$JOURNEY_CACHE_CONTROL/build.lock"
ln "$JOURNEY_CACHE_CONTROL/build.lock" "$JOURNEY_CACHE_PROJECTED/build.lock"
# Python inventory still enters through the real subprocess door, using a fixture CLI.
mkdir "$scratch/bin"
printf '#!/bin/sh\nprintf "fixture-container\\taaaaaaaaaaaaaaaaaaaa\\n"\n' >"$scratch/bin/docker"
chmod +x "$scratch/bin/docker"
PATH="$scratch/bin:$PATH" journey_cache_inventory
expect "$(cat "$JOURNEY_CACHE_CONTROL/containers.json")" "$(cat "$JOURNEY_CACHE_PROJECTED/containers.json")" "$JOURNEY_CACHE_PROJECTED/containers.json"
expect "$(stat -c '%d:%i' "$JOURNEY_CACHE_CONTROL/build.lock")" "$(stat -c '%d:%i' "$JOURNEY_CACHE_PROJECTED/build.lock")" "$JOURNEY_CACHE_PROJECTED/build.lock"
python3 "$HERE/root-cache.py" --self-test
(
  python3() { printf 'retain\n' >>"$scratch/checkpoint-order"; }
  docker() { printf '%s\n' "$*" >>"$scratch/checkpoint-order"; }
  journey_checkpoint_save fixture abc
  expect $'retain\ncommit fixture pfm-journey-checkpoint:abc\nretain' "$(cat "$scratch/checkpoint-order")" "$scratch/checkpoint-order"
  python3() { return 1; }
  if journey_checkpoint_save fixture forbidden; then echo 'unchecked checkpoint commit'; exit 1; fi
  expect $'retain\ncommit fixture pfm-journey-checkpoint:abc\nretain' "$(cat "$scratch/checkpoint-order")" "$scratch/checkpoint-order"
)
python3 - "$HERE" "$scratch" <<'PY'
from pathlib import Path
import os, sys
sys.path.insert(0, sys.argv[1])
from runtime import command, write_text
source = Path(sys.argv[1]) / 'root-cache.py'
private = Path(sys.argv[2]) / 'root-cache.py'
output = Path(os.environ['PFM_TEST_TIMING_DIR']) / 'journey-cache-mutants'
for index, (old, new) in enumerate((('KEEP_NEWEST = 2', 'KEEP_NEWEST = 3'),
    ("images[:KEEP_NEWEST]} | used", "images[:KEEP_NEWEST]}"),
    ('archives[KEEP_NEWEST:]', 'archives[KEEP_NEWEST + 1:]'))):
    content = source.read_text()
    assert content.count(old) == 1, old
    write_text(private, content.replace(old, new))
    result = {}
    command([sys.executable, str(private), '--self-test'], allowed_codes=(0, 1), capture=result,
            env={**os.environ, 'PYTHONPATH': sys.argv[1]})
    write_text(output / (str(index) + '.log'), result['stdout'] + result['stderr'])
    assert result['status'] == 1 and 'AssertionError' in result['stderr'], result
print('CHECKPOINT RETENTION WATCHED-RED PASS: budget, active image and OS archive guards')
PY
python3 "$HERE/checkpoint.py" --self-test
go -C "$HERE/../../../pfm" build -o "$scratch/mock-engine" ./cmd/mock-engine
python3 "$HERE/claude_versions.py" --self-test "$scratch/mock-engine"
printf 'CONTAINER READINESS PASS: pending bus polled; degraded and exited preserve evidence\n'

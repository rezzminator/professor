#!/usr/bin/env bash
# sim-entry.sh — the pfm-sim container's entrypoint (`dev.sh iso sim`): turns a
# fresh pfm-dev container into a machine that has run `pfm install` from the
# fenced worktree, with a real Google Chrome (headless only, no display) and
# the harvester's browser rung on — then runs the caller's command.
#
#   1. pfm built from /worktree/pfm into ~/.local/bin (the code under test,
#      rebuilt every run; the Go caches are volumes, so a warm build is quick),
#      and engine-sync from the /engine-sync mount, exported as ENGINE_SYNC_BIN.
#   2. `pfm install` into the container HOME, which provisions the converter
#      sidecar into the per-worktree harvest volume (a no-op once that
#      worktree's pinned environment is present).
#   3. harvester.config.json with fetch.browser on: the same file a person
#      writes to opt in, so the rung runs exactly as it does for them.
#
# The browser sidecar (env-browser) is provisioned lazily by pfm on the first
# browser fetch and persists in the same volume.
#
# BROKEN STATE: a failed build, a failed install, or a
# missing Chrome each print `sim: BOOTSTRAP-FAILED — <step>` plus that step's
# log tail and exit 1. The caller's command never runs on a half-built machine,
# so a sim run that printed no proof line did not run.
set -euo pipefail

fail() { # fail <step> <log>
  echo "sim: BOOTSTRAP-FAILED — $1" >&2
  [[ -f "$2" ]] && tail -40 "$2" >&2
  exit 1
}

chrome="$(google-chrome-stable --version 2>/dev/null)" || fail "google-chrome-stable is not runnable" /dev/null

mkdir -p "$HOME/.local/bin"
go -C /worktree/pfm build -o "$HOME/.local/bin/pfm" ./cmd/pfm >/tmp/sim-build.log 2>&1 ||
  fail "go build ./cmd/pfm from /worktree" /tmp/sim-build.log
# The Codex/OpenCode compiler pfm install execs and the lanes run as engine-sync, from the
# engine-sync source docker-compose.yml mounts at /engine-sync.
. /worktree/infra/fence/engine-sync.sh
export ENGINE_SYNC_BIN="$HOME/.local/libexec/engine-sync/engine-sync"
engine_sync_build "$ENGINE_SYNC_BIN" >/tmp/sim-engine-sync.log 2>&1 ||
  fail "engine-sync build from /engine-sync" /tmp/sim-engine-sync.log

# The plugin wrapper's stable link, the target of the PATH link pfm install writes.
engine_sync_link_stable "$ENGINE_SYNC_BIN" 2>/tmp/sim-engine-sync-link.log ||
  fail "engine-sync stable link" /tmp/sim-engine-sync-link.log

export PFM_CONFIG="${PFM_CONFIG:-$HOME/.local/state/pfm/pfm.config.json}"
mkdir -p "$(dirname "$PFM_CONFIG")"
# Install writes generated prompts into a writable clone, never the mount.
bash /worktree/infra/fence/source-clone.sh /worktree "$HOME/.professor" >/tmp/sim-source-clone.log 2>&1 ||
  fail "writable source snapshot" /tmp/sim-source-clone.log
(cd "$HOME/.professor" && pfm install --yes --skip-engine codex --skip-themes) >/tmp/sim-install.log 2>&1 ||
  fail "pfm install (converter sidecar into the harvest volume)" /tmp/sim-install.log
[ "$(command -v engine-sync)" = "$HOME/.local/bin/engine-sync" ] ||
  fail "engine-sync on PATH after pfm install (got: $(command -v engine-sync || echo none))" /tmp/sim-install.log

printf '{\n  "fetch": {\n    "browser": true\n  }\n}\n' >"$(dirname "$PFM_CONFIG")/harvester.config.json"

echo "sim: chrome=\"$chrome\" harvest=$HOME/.local/state/pfm/harvest-python browser-rung=on pfm=$(command -v pfm)"
exec "$@"

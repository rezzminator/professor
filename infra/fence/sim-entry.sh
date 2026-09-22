#!/usr/bin/env bash
# sim-entry.sh — the pfm-sim container's entrypoint (`dev.sh iso sim`): turns a
# fresh pfm-dev container into a machine that has run `pfm install` from the
# fenced worktree, with a real Google Chrome, an X display and the harvester's
# browser rung on — then runs the caller's command.
#
#   1. Xvfb on :99, so the harvester's one headed retry after a wall can open
#      a real window.
#   2. pfm built from /worktree/pfm into ~/.local/bin (the code under test,
#      rebuilt every run; the Go caches are volumes, so a warm build is quick).
#   3. `pfm install` into the container HOME, which provisions the converter
#      sidecar into the per-worktree harvest volume (a no-op once that
#      worktree's pinned environment is present).
#   4. harvester.config.json with fetch.browser on: the same file a person
#      writes to opt in, so the rung runs exactly as it does for them.
#
# The browser sidecar (env-browser) is provisioned lazily by pfm on the first
# browser fetch and persists in the same volume.
#
# BROKEN STATE: Xvfb not answering, a failed build, a failed install, or a
# missing Chrome each print `sim: BOOTSTRAP-FAILED — <step>` plus that step's
# log tail and exit 1. The caller's command never runs on a half-built machine,
# so a sim run that printed no proof line did not run.
set -euo pipefail

fail() { # fail <step> <log>
  echo "sim: BOOTSTRAP-FAILED — $1" >&2
  [[ -f "$2" ]] && tail -40 "$2" >&2
  exit 1
}

Xvfb :99 -screen 0 1920x1080x24 -nolisten tcp >/tmp/sim-xvfb.log 2>&1 &
for _ in $(seq 100); do
  [[ -S /tmp/.X11-unix/X99 ]] && break
  sleep 0.1
done
[[ -S /tmp/.X11-unix/X99 ]] || fail "Xvfb did not open display :99 within 10s" /tmp/sim-xvfb.log

chrome="$(google-chrome-stable --version 2>/dev/null)" || fail "google-chrome-stable is not runnable" /dev/null

mkdir -p "$HOME/.local/bin"
go -C /worktree/pfm build -o "$HOME/.local/bin/pfm" ./cmd/pfm >/tmp/sim-build.log 2>&1 ||
  fail "go build ./cmd/pfm from /worktree" /tmp/sim-build.log

pfm install --yes --skip-engine codex --skip-themes >/tmp/sim-install.log 2>&1 ||
  fail "pfm install (converter sidecar into the harvest volume)" /tmp/sim-install.log

mkdir -p "$HOME/.config/pfm"
printf '{\n  "fetch": {\n    "browser": true\n  }\n}\n' >"$HOME/.config/pfm/harvester.config.json"

echo "sim: chrome=\"$chrome\" display=$DISPLAY harvest=$HOME/.local/state/pfm/harvest-python browser-rung=on pfm=$(command -v pfm)"
exec "$@"

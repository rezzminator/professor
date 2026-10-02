#!/usr/bin/env bash
# Gate fixture (PFM_GATE_FIXTURES=1, step fixture.shell-hang): `cat` opens a fifo nobody writes, so
# it blocks in the kernel until the step's bound ends it; the tree snapshot names the fifo and the
# wait. TERM removes the fifo's directory.
set -euo pipefail

dir="$(mktemp -d "${TMPDIR:-/tmp}/gate-fixture-hang.XXXXXX")"
cat_pid=""
# cat may already be gone when TERM lands; the directory goes either way.
trap 'if [[ -n "$cat_pid" ]]; then kill "$cat_pid" 2>/dev/null || true; fi; rm -rf "$dir"; exit 143' TERM
mkfifo "$dir/fifo"
cat "$dir/fifo" &
cat_pid=$!
wait "$cat_pid"

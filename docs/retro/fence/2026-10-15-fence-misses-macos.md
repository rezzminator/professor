# 2026-10-15 — the Linux fence passed a BSD-incompatible chmod that silently disabled plugin level logs on macOS

Observed: `chmod 700 -- dir` fails on BSD/macOS chmod and silently disabled the level log in sub-agent-compact, effort-live-control and cache-live-control; the fence runs GNU coreutils, so every gate passed.

Amend: docs/dev/testing/plugin-lab.md#Run — shell that plugins run on the host needs a macOS run (plugin-lab on the Mac, or a BSD-flag lint) before landing; a fence pass is not evidence for macOS.

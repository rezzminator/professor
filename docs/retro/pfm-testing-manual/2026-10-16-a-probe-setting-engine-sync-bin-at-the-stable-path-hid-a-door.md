# 2026-10-16 — a fence probe setting `ENGINE_SYNC_BIN` at the stable path hid a missing PATH link

Observed: the engine-sync-tidy phase-4 gate probed the lanes PATH door with `ENGINE_SYNC_BIN` pointing at engine-sync's stable link path, so the probe passed while the lane and demo containers, which never ran the plugin wrapper that makes that link, had no `engine-sync` on PATH after `pfm install`. `infra/fence/lanes/container.sh` sets `ENGINE_SYNC_BIN=/usr/local/libexec/pfm-lanes/engine-sync`; `engine_sync_link_stable` in `infra/fence/engine-sync.sh` now makes the link in `lanes/provision.sh`, `infra/demo/setup.sh` and `infra/fence/sim-entry.sh`.

Amend: .claude/commands/pfm-testing-manual.md#Tricks and traps — a fence probe of a lanes or demo door keeps every variable where `lanes/container.sh` sets it (`ENGINE_SYNC_BIN` included); a probe that sets a value the door is meant to produce passes with the door missing.

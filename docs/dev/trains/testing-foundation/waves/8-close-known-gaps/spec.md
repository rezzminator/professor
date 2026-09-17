# Wave 8 — close the known gaps; the ledger ships empty

**Law (the user's):** a known issue is fixed, not ledgered. `lanes/known-gaps.yml` stays only as an escape hatch for a failure that cannot be fixed here (an upstream engine bug, hardware we lack): every entry needs `expires:`; past it, or on an unexpected pass, the run is red.

1. **OpenCode MCP wiring** (beats `M.03`, `E3.02`): the installer registers both pfm MCP servers in OpenCode's config the way it does for Claude (stdio/HTTP) and Codex (HTTP); `pfm doctor` gains the OpenCode MCP rows; uninstall removes exactly what it wrote. Tracer-map the three engines' registration doors before the build (map-before-dispatch).
2. **OpenCode compile layer for adopters** (beat `A.13`): what `build-opencode.mjs` does for this repo becomes reachable for an adopter through pfm (the same single-writer rule `pfm codex build` follows), scaffolded by `pfm init`, checked by `pfm update check`. Touches `templates/**` → through `/pcm`.
3. **harvestpy on linux-arm64** (beat `O.11`): provision and verify the pinned Python sidecar on arm64 (the Mac's fence is linux-arm64 — the test bed exists); if a pinned wheel truly has no arm64 build, that single dependency is the one legitimate ledger entry, with an expiry.
4. **Duplicate seat login** (found 2026-09-17): `pfm doctor` names two seats holding one account (also listed in Wave 3 B5a — built once, here or there, whichever dispatches first).

Each fix: regression test watched red first; its lane beat flips from `known` to a real assertion in the same commit; the ledger entry is deleted in that commit.

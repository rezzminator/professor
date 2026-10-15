# 2026-10-15 — new test infrastructure and the fixes it verifies were bundled in one flight

Observed: flight compaction-fixes built plugin-lab and the plugin fixes it verifies together; that doubled the review surface and made the merge conflict with a develop that had moved in parallel (the BSD chmod fix and the startup-log drop landed separately in cd9da2e and 16d0f28).

Amend: templates/global/agents/flights-foreman.md#The run — new test infrastructure and the fixes it verifies are two flights, infra first, fixes second.

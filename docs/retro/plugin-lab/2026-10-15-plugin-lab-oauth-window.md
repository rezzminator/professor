# 2026-10-15 — a plugin-lab live run needs 20 to 50 minutes of OAuth token life

Observed: plugin-lab live scenarios S1-S10 need 20 to 50 minutes left on the OAuth token; a mid-run expiry fails scenarios for reasons unrelated to the code.

Amend: docs/dev/testing/plugin-lab.md#Run — check the token's remaining life against the whole S1-S10 run before starting it, not per scenario.

# 2026-10-22 — fixtures cut from real transcripts carried private names into a public plugin repo

Observed: The forward-handoff integration suite for the sub-agent-compact plugin took its fixtures from real transcripts, as the integration-suite law asks. One fixture carried a private project's name and two private place names, and the plugin is a public repository whose submodule runs no leak gate of its own, so only a manual scan against the leak terms caught them before the commit.

Amend: templates/global/commands/quality/integration-suite.md#Laws — a fixture cut from a real transcript or recording passes the leak-terms scan before it is written; in a repository without a leak gate, such as a plugin submodule, that scan is the only guard.

# Flight compaction-fixes — requirements

Source: the user's audit of the sub-agent-compact plugin (reports in `audit/` beside this file: audit-code.md, audit-main.md, audit-sub.md, table.md) and the user's rulings on 2026-10-15. Every finding ID below refers to those reports; read them for path:line evidence.

## User rulings (verbatim intent)

- One worktree holds ALL of the work, built and tested end to end in isolation, then the flight STOPS AND WAITS: no merge into any develop branch, no version bump, no live install, no push. Commits on the flight's own branches inside the worktree are allowed (through gitter).
- Isolation: changes are tested in isolation from the user's live install and running chats. It need not be a container — isolated for the chat that runs it is enough. If proper infrastructure to test a plugin from a worktree in isolation does not exist, build it end to end, replicable for other plugins too, designed by flights-speccer.
- The shared plugin live-test infrastructure lives in the professor repo (one copy; every plugin checkout runs under it; plugins keep their own unit tests and public CI unchanged).
- Live end-to-end runs spend real tokens on the account the session is already logged into. Total live spend for the flight stays under about USD 10, metered and reported. No account switch, no login, no credential copy into tracked files, no write to the user's live Claude config store, no touching running chats.
- Every fix is confirmed working end to end (live, in isolation) and by tests.

## Requirements

R1 (audit finding 1) — The plugin level log never writes on macOS: `chmod 700 -- "$dir"` and `chmod 600 -- "$owned"` fail under `set -eu` with macOS /bin/chmod ("chmod: --: No such file or directory"). Fix in sub-agent-compact `plugins/sub-agent-compact/src/log.ts` (~lines 92, 98) AND its twins `effort-live-control/plugins/effort-live-control/src/plugin-log.ts` and `cache-live-control/plugins/cache-live-control/src/plugin-log.ts` (grep each repo for any other `chmod … --`). Acceptance: on this macOS host, in isolation, each of the three plugins writes its level log under PLUGIN_LOG_DIR (and the README-promised logger-ready row appears); a regression test that would have caught it on BSD/macOS chmod exists and was watched failing first (the fence is Linux and GNU chmod accepts `--`, which is how it slipped through). The setup failure itself must also reach the decision log, not only the screen.

R2 (finding 2, unseen compactions) — NO FIX by user ruling (likely an older-version chat).

R3 (finding 3) — Haiku is fully hands-off: for a Haiku party the plugin does nothing — no nudges, no holds, no forced point, no handoff refusal/hold, no forward_handoff prompting, no brief re-seat after native compaction; every compaction ask passes and Claude Code's native protocol runs on its own. Docs (README options/behaviour, CHANGELOG [Unreleased]) state it. Acceptance: live Haiku sub-agent in isolation crosses Claude Code's native ask point and compacts natively with zero plugin nudges/holds/brief rows; unit tests pin it.

R4 (finding 4, decision log) — The decision log keeps its evidence: routine skip rows never evict compaction, nudge, handoff, error or start rows (coalesce/aggregate skips or keep them in a separate bounded stream — design choice is yours); no whole-file non-atomic rewrite per write (append, or atomic replace) and safe under concurrent writers (main + sub-agents of one session, several sessions); rows queued at session end are flushed before exit; old per-session files are cleaned by a bounded retention policy (state the policy in the README). Acceptance: tests for each property, plus a live run showing the start row and every compaction row survive a long skip-heavy session.

R5 (finding 5) — A limit never exceeds the party's real window: a limit in tokens (e.g. main 600k) is clamped to the model's real context window (and its "prompt too long" recovery ask is never skipped as "below limit"); the main window is re-read when the model changes (e.g. /model to a 200k model) instead of once at start. Acceptance: tests; live run switching a 1M main to a 200k model shows the clamped limit in the decision row.

R6 (finding 6) — Main state resets after a compaction the plugin's hook did not handle (the compact-unseen drop): stored handoff cleared (no stale install later), nudge levels re-armed. Acceptance: tests; live proof if the condition can be induced, else a named gap with why.

R7 (finding 7, all lower items) — fix every one:
- a. look_back reads task-notification `attachment` entries (child results lost at compaction are recoverable); an unsupported entry is reported as an error, never as routine success.
- b. A handoff chain survives a CLAUDE_CONFIG_DIR / account switch of the parent (record lookup not tied to one config dir).
- c. A stored forward handoff survives a plugin reload (recovery like the old handoff tool's).
- d. The dev.22 symlink guard covers the direct `<type>.md` lookup too (symlinked agent definitions resolve consistently on every path).
- e. Plugin-namespaced agent types (`plugin:agent`) get their own frontmatter policy.
- f. A relative `logFile` is logged by name as a bad option and falls back to the default (never turns logging off).
- g. Each warning is written once to the level log.
- h. Level-log rotation is race-free between concurrent sessions.
- i. A failed native compaction does not delay the forced one by a turn.
- j. A party that lacks the forward_handoff tool in its tools list is never nudged toward it nor held for it (e.g. `rr`, non-Haiku).
- k. look_back: `entry: null` beside a query is treated as absent; the party's own forward_handoff call is not returned as the top hit; calling it with nothing before any compaction returns a clear "no earlier transcript" answer, not an argument error.
- l. Verify finding F8 (Files block rebuilding a path under the session cwd, seen on dev.20); fix if still present on HEAD.
- m. `mcp__sub-agent-compact__compact` is listed in 35 professor agent files' tools lists but never registered while forwardHandoffTool is on (the default): remove it from the agent tools lists at their source (the `from:` declarations / templates/global/agents sources, then re-render templates/generated/agents via its generator). This is a prompt/template edit: route it through the `pcm` agent per the professor CLAUDE.md.
- n. Every other lower finding in audit-code.md's findings list not named above.

R8 (infrastructure) — A replicable plugin live-test lab in the professor repo: runs any plugin checkout (from a worktree) in a real Claude Code session isolated from the user's live install and running chats (own run folder, private tmux socket, `--setting-sources project --settings {run}/settings.json`, `--plugin-dir {worktree plugin}`, the installed copy of the same plugin disabled, nothing written into the live config store), drives turns and sub-agents, reads transcripts and plugin logs, meters spend, and reports a pass/fail table per check. Existing hand-rolled examples to learn from: `plugins/cache-live-control/scripts/live-proof.sh`, `plugins/buddy/scripts/live-proof.sh`, `plugins/buddy/scripts/live-configs.sh`. sub-agent-compact is its first user (scenarios for R1, R3–R7), and effort-live-control and cache-live-control use it for R1. Documented so another plugin can adopt it. Migrating the existing per-plugin scripts onto it is out of scope (report as follow-up).

## Done when

Every R row above (except R2) has: the diff, its unit tests green (each regression watched failing first), and a live isolated run that proves it — or a named gap with the reason a live proof was impossible. One flights-lander review over the whole diff of every repo. Then stop and report; nothing merged to develop, nothing installed, nothing pushed.

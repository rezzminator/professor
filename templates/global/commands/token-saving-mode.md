---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through the `pcm` agent); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: token-saving-mode
description: 'USER-ONLY Runs this chat cheaper — `/token-saving-mode` starts a lean loop with no ceremony and reloads the chat onto a cheaper model; `/token-saving-mode off` restores auto effort and the previous model.'
argument-hint: "[off]"
disable-model-invocation: true
---

# Token-saving mode

Arguments: $ARGUMENTS — empty runs § On, `off` runs § Off. The user typing this command is the explicit ask `/reload` requires.

## On

1. Lean loop, from this turn until `/token-saving-mode off`:
   - Effort: call `mcp__auto-effort__set_effort` with `medium` in the first response of every turn; a level holds only until its turn ends.
   - Replies: 5 lines at most, plus the Verdict.
   - Delegation: mechanical work → `model: sonnet`, collection and lookups → `model: haiku`; judgment keeps its tier per the fleet prompt's § Model Selection.
   - Calls: independent calls batched, every output trimmed (`tail`, `rg`, a line range).
   - Ceremony: only the steps the work needs. No plan-file checkpoint beyond the compaction frontier. A change small enough to make and test by hand takes the flights rule's small-work path, with no reviewer or lander round. No foreman fan-out where one hand does it. No AskUserQuestion where a sensible default exists: decide, and name the ruling in the report. No interim status. Nothing that passed is re-verified; no optional review round, and no docs or twin polish past what correctness needs.
   - Never dropped, and no cut corner reaches it: safety gates, a flight's lander, tests a rule mandates, PHI and consent guards — among them publication asks (push, tag, release), git writes through `gitter`, the leak gate, the user's confirmation of a destructive or outward-facing act, and tests that must pass before a commit.
   - A compaction frontier carries `token-saving-mode on, previous model {previous}` as a standing order.
2. Model switch, skipped when this chat already runs on sonnet or haiku:
   - `{previous}`: the model ID this chat runs on now.
   - Reload guard: a reload kills running sub-agents, background shells, wakeups and crons. While a sub-agent or background shell runs or a wakeup is pending, the lean loop applies now: say the switch waits, and reload in the turn the last one returns. A session cron is re-armed by the `--then` prompt.
   - As the turn's last action: `pfm chat reload --model sonnet --then "token-saving-mode on, previous model {previous}: re-apply the lean loop of the /token-saving-mode command above, then {the open work's next step, or wait for the user}"`; one line, then end the turn.

## Off

1. Call `mcp__auto-effort__set_effort` with `auto`; the lean loop ends.
2. Only when token-saving-mode switched the model, under the same reload guard, as the turn's last action: `pfm chat reload --model {previous} --then "token-saving-mode off: {the open work's next step, or wait for the user}"`; `{previous}` lost → drop `--model`, and the account's default model loads.

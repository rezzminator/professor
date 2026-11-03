---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through the `pcm` agent); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: shoestring
description: 'USER-ONLY Runs this chat cheaper — `/shoestring` starts a lean loop and reloads the chat onto a cheaper model; `/shoestring off` restores auto effort and the previous model.'
argument-hint: "[off]"
disable-model-invocation: true
---

# Shoestring

Arguments: $ARGUMENTS — empty runs § On, `off` runs § Off. The user typing this command is the explicit ask `/reload` requires.

## On

1. Lean loop, from this turn until `/shoestring off`:
   - Effort: call `mcp__auto-effort__set_effort` with `medium` in the first response of every turn; a level holds only until its turn ends.
   - Replies: 5 lines at most, plus the Verdict.
   - Delegation: mechanical work → `model: sonnet`, collection and lookups → `model: haiku`; judgment keeps its tier per the fleet prompt's § Model Selection.
   - Calls: independent calls batched, every output trimmed (`tail`, `rg`, a line range).
   - Nothing that passed is re-verified; no optional review round.
   - Never dropped: safety gates, landers, tests a rule mandates, PHI and consent guards.
   - A compaction frontier carries `shoestring on, previous model {previous}` as a standing order.
2. Model switch, skipped when this chat already runs on sonnet or haiku:
   - `{previous}`: the model ID this chat runs on now.
   - Reload guard: a reload kills running sub-agents, background shells, wakeups and crons. While a sub-agent or background shell runs or a wakeup is pending, the lean loop applies now: say the switch waits, and reload in the turn the last one returns. A session cron is re-armed by the `--then` prompt.
   - As the turn's last action: `pfm chat reload --model sonnet --then "shoestring on, previous model {previous}: re-apply the lean loop of the /shoestring command above, then {the open work's next step, or wait for the user}"`; one line, then end the turn.

## Off

1. Call `mcp__auto-effort__set_effort` with `auto`; the lean loop ends.
2. Only when shoestring switched the model, under the same reload guard, as the turn's last action: `pfm chat reload --model {previous} --then "shoestring off: {the open work's next step, or wait for the user}"`; `{previous}` lost → drop `--model`, and the account's default model loads.

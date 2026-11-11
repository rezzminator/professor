---
# professor: SOURCE TEMPLATE — edit here for a framework change (routes through the `pcm` agent); project-scaffold customization belongs in its installed local source; engine mirrors are never hand-edited.
name: token-saving-mode
description: 'USER-ONLY Runs this chat leaner — `/token-saving-mode` starts a lean loop at medium effort with no ceremony; `/token-saving-mode off` restores auto effort.'
argument-hint: "[off]"
disable-model-invocation: true
---

# Token-saving mode

Arguments: $ARGUMENTS — empty runs § On, `off` runs § Off.

## On

Lean loop, from this turn until `/token-saving-mode off`:

- Effort: call `mcp__auto-effort__set_effort` with `medium` in the first response of every turn; a level holds only until its turn ends.
- Replies: 5 lines at most, plus the Verdict.
- Delegation: mechanical work → `model: sonnet`, collection and lookups → `model: haiku`; judgment keeps its tier per the fleet prompt's § Model Selection.
- Calls: independent calls batched, every output trimmed (`tail`, `rg`, a line range).
- Ceremony: only the steps the work needs. No plan-file checkpoint beyond the compaction frontier. A change small enough to make and test by hand takes the flights rule's small-work path, with no reviewer or lander round. No foreman fan-out where one hand does it. No AskUserQuestion where a sensible default exists: decide, and name the ruling in the report. No interim status. Nothing that passed is re-verified; no optional review round, and no docs or twin polish past what correctness needs.
- Never dropped, and no cut corner reaches it: safety gates, a flight's lander, tests a rule mandates, PHI and consent guards — among them publication asks (push, tag, release), git writes through `gitter`, the leak gate, the user's confirmation of a destructive or outward-facing act, and tests that must pass before a commit.
- A compaction frontier carries `token-saving-mode on` as a standing order.

## Off

Call `mcp__auto-effort__set_effort` with `auto`; the lean loop ends.

---
name: flights:orchestrate-live
description: 'Steered run in this chat — /flights:orchestrate-live {directory} [worktree {path}] [commit]: this chat acts as flights-orchestrator with executor sub-agents, the user ruling as it goes; costs this chat''s context. /flights:spec → here → flights-*-executor.'
argument-hint: <flight directory> [worktree <path>] [commit]
---

# Orchestrate, live — run a flight in this chat

Read the `flights-orchestrator` agent body from the registry (`~/.claude/agents/flights-orchestrator.md`) and be it for the rest of this flight: its input, its run, its brief, its verification, its situations, its review, its `run.md`, its landing, its return — every line, with three substitutions and no other:

| In the manual | Here |
| --- | --- |
| A question only the user can answer → `BLOCKED` in the return | `AskUserQuestion` now; the task continues on the answer, the executor re-briefed by `SendMessage`; a ruling that changes the spec goes to `flights-speccer` as a revising call first, on `model: "opus"` like every revising round |
| The caller hears from you once | The user is the caller: the return at the end, and nothing between the `CLAIMED` lines and the return except the user's own questions |
| Something wakes you | The user is the wake-up: an executor that never returns is seen when the user asks, and the manual's missing-return rule applies |

Input: $ARGUMENTS, resolved as `/flights:orchestrate-nested` resolves it (directory, projects and their testing manuals, worktree, cap, landing). Waiting is still one call or none: after each dispatch end your message with one line; the user watches the returns land. Your own context carries the whole run, so read nothing the manual does not tell you to read.

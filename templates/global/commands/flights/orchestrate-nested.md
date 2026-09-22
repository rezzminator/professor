---
name: flights:orchestrate-nested
description: 'The default run — /flights:orchestrate-nested {directory} [worktree {path}] [commit] [revised {ids}]: briefs one flights-orchestrator sub-agent and presents its one return, keeping this chat''s context small. /flights:spec → here → flights-orchestrator.'
argument-hint: <flight directory> [worktree <path>] [commit] [revised <ids>]
---

# Orchestrate, nested — run a flight in a sub-agent

The `flights-orchestrator` agent is the manual; you run it once and read its return. Input: $ARGUMENTS.

1. Resolve the directory: the argument, else the newest under `/tmp/{project}/flights/`. It must hold `index.md`; a `run.md` with `DONE` lines means a resume, and you say so.
2. Assemble the brief, and nothing more: the directory; the standing rules — what the project contract does not carry: the worktree, the fenced command that runs one package's affected tests (the full suite is the gate's, never an executor's), the checks by command, anything the user adds; never a `CLAUDE.md`, pasted or named, since the harness hands it to every executor; each project the index's `files` touch with the path of its testing manual (`.claude/commands/{project}-testing-manual.md`, or `testing-manual.md`; a project without one is named as such); the worktree when given; the cap when given; the landing: the project's standing checks by command, `commit` when asked; `revised {ids}` when given.
3. Spawn `Agent(subagent_type: "flights-orchestrator", model: "sonnet")` with the brief. End your message with one line and no tool call; the return arrives on its own. Never poll, never read `run.md` while it runs.
4. On the return: present it verbatim, then one line of your own on what the user owes (a `BLOCKED` question, a commit not made, a missing return). A `BLOCKED` question: put it to the user, send the ruling to `Agent(subagent_type: "flights-speccer", model: "opus")` — every revising round runs on opus — as a revising call with the directory, the ruling and the completed ids from `run.md`, then run this command again on the same directory with `revised {the ids its NOTES name}`: the orchestrator resumes from `run.md` and dispatches the revised tasks. A missing return: run this command again; the orchestrator treats the `CLAIMED` task as not started.

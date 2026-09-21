---
name: flights:orchestrate-cross-harness
description: Runs one flight with chat seats as executors — the chat reads the flights-orchestrator agent body and acts as it, spawning one Codex, OpenCode or Claude seat per task file through the chat MCP instead of a sub-agent, briefing it by inject, and recording its injected return. Use it when a task file should run on a different engine. /flights:orchestrate-cross-harness {directory} [engine claude|codex|opencode] [executor {role}] [worktree {path}] [commit]
argument-hint: <flight directory> [engine <claude|codex|opencode>] [executor <role>] [worktree <path>] [commit]
---

# Orchestrate, cross-harness — run a flight on chat seats

Read the `flights-orchestrator` agent body from the registry (`~/.claude/agents/flights-orchestrator.md`) and be it for the rest of this flight, with the transport substituted and nothing else:

| In the manual | Here |
| --- | --- |
| Spawn `Agent(subagent_type, model)` | A seat of the engine (the argument; default `codex`), named `{flight}-{id}`, in the project directory or the worktree: `chat_new` with the engine and the directory. When the executor type is a registered role, the shell `pfm chat new {flight}-{id} --engine {engine} --cwd {dir} --role {role}` instead — the MCP verb carries no role. A `chat_new` error is a seat not born: no `CLAIMED` line; one retry, then the task holds and the return names it |
| `model:` from the pin or the rating | `chat_new`'s `model` and `effort`, in the engine's own names; unset, the engine's default |
| The brief in the spawn prompt | `chat_inject` on the seat: the brief verbatim; the transport pastes any size. It closes with the way home: "when done, write your return to a file under `/tmp/` and send it with `pfm chat inject {this chat's name} --file {path}`" — your name from `chat_whoami`; a seat's plain inject carries one line |
| `/code-review low` as the executor's last step | The engine's own review command in the brief: `/code-review low` on a Claude seat, `/review` on a Codex seat; a seat without one says so in its return |
| Wait: end the message, the return arrives | The same; the seat's return arrives as an inject into this chat |
| Verify from the return and `git diff {baseline} --stat -- {files}` | The same; `chat_last` on the seat when the inject arrived cut short |
| A question back by `SendMessage` | `chat_inject` on the seat |
| The executor's transcript, sent to `flights-speccer` with every `FAILED` and `SPEC-DRIFT` and named on the `run.md` line | The seat's name and its transcript id (`chat_find` by the seat's name; `pfm chat save` when the speccer needs a file) |
| An executor never returns | On any wake-up (a return, the user): a seat silent 20 minutes past its last status change gets `chat_status` once, then a full-screen `chat_capture` judged from process evidence (tokens streaming, context growing), never from rendered text. Still silent: `{id} STALE · {what the capture showed}` in `run.md` and the return, and the seat is left alone — never killed blind, never re-dispatched blind. A seat that is dead by evidence: a fresh seat, same task file, after the `STALE` line |
| A question only the user can answer → `BLOCKED` | `AskUserQuestion` now; the seat re-briefed by inject; a ruling that changes the spec goes to `flights-speccer` as a revising call first |
| The caller hears from you once | The user is the caller: the return at the end |
| After a verdict is recorded | `chat_kill` the seat; `DISPATCHED` counts seats born against returns received |

Input: $ARGUMENTS, resolved as `/flights:orchestrate-nested` resolves it, plus the engine. Every seat is one-shot: born for one task file, killed after its verdict. Waiting is one call or none; a seat is never polled by `chat_status` before its stale bound.

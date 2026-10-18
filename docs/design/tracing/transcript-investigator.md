# transcript-investigator

`transcript-investigator` answers one mission about past runs — main chats, their sub-agents, Codex threads, OpenCode sessions — and audits them for faults: failures, mistakes, broken rules, waste, misbehaviour. It judges; `transcript-reader` agents on `haiku` read, one per transcript, so no transcript enters the judge's context whole. Both stand on the chatlog skill's reader kit, `templates/global/skills/chatlog/chatlog.py`, which does every mechanical step in code. The pair replaces three agents: `chat-investigator` (missions over transcripts), `agent-optimizer` (one agent's run audited against its prompt) and `sub-investigator` (its digest reader).

## Contents

- [Identity](#identity)
- [The brief](#the-brief)
- [The flow](#the-flow)
- [The reader](#the-reader)
- [The kit](#the-kit)
- [Why its spawning is capped](#why-its-spawning-is-capped)
- [Bench](#bench)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Identity

| Field | `transcript-investigator` | `transcript-reader` |
| --- | --- | --- |
| Kind | original agent, `templates/global/agents/transcript-investigator.md`, linked by `pfm install` | original agent, `templates/global/agents/transcript-reader.md`, linked by `pfm install` |
| Class | none: any caller, the user included | visible to `transcript-investigator` only |
| Model, effort | `opus`, `high`: the work is judgment | `haiku`, `medium`: the work is reading and labelling, never judging |
| Tools | `Read, Grep, Glob, Bash, Write, Agent`, the chat MCP's `ls`, `find`, `status`, `last` | `Read, Bash` |
| Budget | 40 tool calls, two rounds of readers (at most 8, then at most 4) | the windows `prep` prints |
| Writes | an AUDIT report under `$HOME/.local/state/pfm/transcript-investigator/`; the answer is its final message | its kit directory under the investigator's run directory |

## The brief

| Line | Holds |
| --- | --- |
| `Mission:` | the question |
| `Targets:` | optional: names, session or agent ids, paths, a flight directory, a project, a time window |
| `Agent:` | optional: an agent type to audit, which turns on AUDIT |
| `Prompt:` | optional: the audited prompt file, overriding the lookup |
| `Caller:` | optional: the sender's session id; its own transcript is never a target |

## The flow

`LOCATE -> READ (one transcript-reader per target, all in one message) -> chatlog.py verify -> AUDIT (when Agent: is set) -> LIVE STATE (only when asked about now) -> JUDGE -> final message`

- LOCATE resolves each target through `pfm transcript locate`, `pfm transcript agents` or `chatlog.py runs` (the kit resolves the same targets natively); an `AMBIGUOUS` name lists every candidate and the ones left out are named.
- READ hands each reader a card: `Target:`, `Dir:` (`{run dir}/{id}`), optional `Lines:` and numbered `Orders:` that name lines, never conclusions. A reader returning `SPLIT` gets one reader per range in round two.
- `verify` checks every reader row against its source line in one call and prints the rows that held; a `BAD` row is dropped, a `FAILED` report goes in `NOT READ`.
- AUDIT walks the run against the prompt in force (the run's own prompt snapshot when it carries one), classes each finding `friction`, `repetition`, `hang`, `mistake` or `waste` with its cost and cause, and writes the report once. It identifies, never prescribes.
- The final message: the answer first, then `TARGETS`, `FINDINGS` or `EVIDENCE` by transcript line, `NOT READ`. The investigator never ends its turn while a reader is out: its caller receives only the last message.

## The reader

The reader runs `prep`, reads every window in order, and adds only the rows that need reading — `CORRECTION`, `CLAIM`, `BEARS`, `WRITE`, `O{n}` — through `note`, which refuses a row whose line, label, ref or anchor does not hold. It ends with `report` and returns one line: `READ {report path} · {rows} rows · MISS {orders without a row}`. No row carries a verdict, a cause, a severity or advice.

## The kit

`chatlog.py` (stdlib Python, run `python3 -I`) owns every mechanical step: the digest with noise collapsed, the timeline and its costs, compactions, spawns, errors, denials, waits, writes, repeats, reading windows, the split of a transcript too large for one reader, row checking, and the state on disk of every file a run wrote. It is a package (`chatlog.py` entry, `chatlogkit/` modules per concern) that reads Claude, Codex and OpenCode stores natively into one event model, so every mechanical label is computed the same way on every engine and an engine without a concept says so in the digest header; costs are priced per call from each record's usage against `pfm model-cost --json --all`, the one price table. Its verbs, modules and per-engine label mapping are documented in `templates/global/skills/chatlog/SKILL.md` § The reader kit. A `--dir` already holding another target's digest gets a fresh subdirectory, so two readers handed one `Dir:` never overwrite each other's rows.

## Why its spawning is capped

An uncapped investigation once fanned out to 17 agents three levels deep in 13 minutes for $29.47. The tree is two levels by construction: `transcript-reader` holds no `Agent` tool, and the investigator spawns at most 8 readers and then 4. A mission larger than that returns what was read and the targets left.

## Bench

Run on real transcripts on this host: a `tracer` audit, a 56 MB main chat (digest 904 KB, split into four reader ranges in 10 s) for its user corrections, and a 5.6 MB `flights-foreman` run audited end to end — 41 calls, 11 minutes, every finding cited by transcript line and every reader row verified.

## Surfaces that stay in sync

| Surface | File |
| --- | --- |
| The agents | `templates/global/agents/transcript-investigator.md`, `templates/global/agents/transcript-reader.md` |
| The kit | `templates/global/skills/chatlog/chatlog.py`, `templates/global/skills/chatlog/chatlogkit/`, `templates/global/skills/chatlog/chatlog_test.py`, `templates/global/skills/chatlog/SKILL.md` |
| The report timestamp metavariable | `docs/PLACEHOLDERS.md` (`{HHMMSS}`) |

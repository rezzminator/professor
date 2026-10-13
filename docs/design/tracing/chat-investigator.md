# chat-investigator

`chat-investigator` answers one mission about what happened in other chats — main chats, their sub-agents, Codex threads, OpenCode sessions — from their transcripts, each claim tied to a transcript line with its excerpt quoted. It is the open-hand investigation a main chat used to run in its own context, made specific: the prompt keeps what those runs did well and fixes what they did wrong or wasted, measured by six `agent-optimizer` audits of real investigation runs. It reads through `pfm transcript`, uses the chat MCP only to locate a chat and read its live state, writes no file but its digests and its readers' evidence rows, and spawns only `sub-investigator` readers on `haiku` for digests over 30 KB.

## Contents

- [Use cases](#use-cases)
- [Identity](#identity)
- [The brief](#the-brief)
- [The prompt](#the-prompt)
- [The readers](#the-readers)
- [Why its spawning is capped](#why-its-spawning-is-capped)
- [Bench](#bench)
- [Not part of the design](#not-part-of-the-design)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Use cases

| Ask | What it needs |
| --- | --- |
| A post-mortem of a named chat that has ended: "read my chat with the builder, why was it so slow" | Resolve a name to a killed or resumable session, digest it, attribute time and tokens by phase |
| Context gathered across sibling chats: "read the last three answers of each reviewer chat, then answer" | Every chat sharing a name prefix, each one's newest answers |
| Liveness of running chats: "why is none of them working", "is it working" | State, idle time and the newest answer of each, the cause in the first line |
| Another chat's runtime or configuration: its cache TTL, a command it does not list | The chat's transcript beside its process and settings evidence |
| Metrics and judgement over many runs of one agent type: an executor's effort, a flight's slowest phase, past foreman runs | Sub-agent transcripts selected by `.meta.json`, timelines and dollars from `pfm tokens` |
| A claim checked: "did that agent really file the evidence" | The artifact on disk against the chat's own progress line |
| A session found by what was said in it | `chat_find` hits, each confirmed by a quoted line |

## Identity

| Field | Value |
| --- | --- |
| Kind | original agent, `templates/global/agents/chat-investigator.md`, linked by `pfm install` |
| Class | none: any caller, the user included, may ask for it |
| Model, effort | `sonnet`, `high` — the work is locating, digesting and quoting, as `tracer`'s is; a caller whose mission is judgment over many runs passes `opus` at spawn |
| Tools | `Read, Grep, Glob, Bash, Agent`, and `chat_ls`, `chat_find`, `chat_status`, `chat_last` from the professor MCP |
| Budget | 40 tool calls |
| Spawns | `sub-investigator` only, one per digest over 30 KB: at most 6 in a first round and 3 in a second |
| Writes | digests under `/tmp/chat-investigator/`, through `pfm transcript show --out`, and its readers' `.ev` rows beside them; the answer is its final message |

## The brief

| Line | Holds |
| --- | --- |
| `Mission:` | the question |
| `Targets:` | optional: chat names, session or agent ids, transcript paths, a flight directory, a project, a time window |
| `Caller:` | optional: the sender's session id. `chat_whoami` answers nothing inside a sub-agent, so only the caller can say which transcript and which `subagents/` directory are its own |

## The prompt

Each rule and the failure in the audited runs it fixes:

| Rule | The open-hand failure |
| --- | --- |
| The first line is the answer, never a question | A "why is none of them working" turn spent 15 calls and $3.09, then asked nine questions at once before naming the cause |
| A name resolves through `pfm transcript locate`, which matches each chat's latest title; every chat sharing it is a candidate, the ones left out are named; `pfm ls --all --tsv` answers whether it is live, resumable or killed | `chat_ls` scoped to the caller's project hid the target; `all:true` returned a 100 KB error; two names each shared by two chats were resolved without saying so |
| `chat_find` is a supplement: a hit counts once a quoted line confirms it, an error is retried once and then reported | A daemon mismatch never retried; a paraphrased excerpt returned eight confirmed, unrelated hits; one search found 4 of 22 transcripts |
| Search `~/.claude/projects` itself, never through `~/.cc/*/projects` | Those are links: a plain `find` reported "no transcript file" for one that existed; `find -L` counted every run three times (396 for 72) and a per-file loop timed out at 120 s |
| `pfm transcript agents` lists a session's sub-agents in one call; one is chosen by what its calls read, never by its type | Half the sub-agents handed to one audit read no transcript |
| One `pfm transcript show --out` per target, then `grep` and `sed` on the digest | Hand-written parsers cost about 140 s; one investigator re-ran `show` six times on one file; `chat_read` counts tool turns and returns tool inputs without results |
| Dollars from `pfm tokens` or a ledger, never a typed rate | An answer claimed prices "checked within 5%" while its own check printed $2.16 against $5.42 |
| `chat_status` without `summary` or `ask`; a `chat_ls` state is a hint | `summary:true` timed out at 63 s; a `chat_ls` row read idle while the chat was working |
| A chat's progress line is its claim; the artifact on disk is the fact | "Filed its own evidence" relayed without a listing, beside a status that said the ask had failed |
| A contrary datum stands beside the answer; an unread conclusion is `INFERRED` | A fresh-chat probe answering against the verdict was left out; "left out its own orchestration cost" was false, the chat had disclosed it |
| A failed lookup goes under `NOT READ`, never reads as an absent target | An `ls` error printed "NOT FOUND" and the empty path was written into a flight ledger |
| The answer is the final message, no report file | Two runs had a report `Write` refused and generated the report twice |
| Every Bash command opens with `emulate sh 2>/dev/null;` | zsh aborted `echo ====` chains in three runs |

## The readers

Every call re-sends the investigator's whole context, so a digest it reads is paid for again on every later call at `sonnet` price. A digest under 30 KB it reads itself; a larger one goes to a `sub-investigator` on `haiku` at `medium` (`templates/global/agents/sub-investigator.md`, visible to `chat-investigator` only), whose card names the digest, the target, its `.ev` output and numbered orders. Orders name lines — "every user prompt that rejects a result", "each Write and its path" — never a conclusion. The reader writes one tab-separated row per line found: `D{n}` (the digest line), the excerpt copied character for character, the order, what the line is. The investigator checks every `D{n}` row against its digest in one `awk` call, drops a `BAD` row, then judges from the rows, opening the digest only at a cited line.

## Why its spawning is capped

An investigation brief without a cap fanned out to 17 agents three levels deep in 13 minutes for $29.47, with 25 more forks planned; stopping the two parents left 14 children running. The same mission's sibling, one agent reading 17 digests alone, cost $3.91. So the tree is two levels by construction: `sub-investigator` holds no `Agent` tool, and the investigator spawns at most 6 readers and then 3. A mission larger than that returns what was read and the targets left, and the caller decides the split, dispatching several investigators in one message, one target set each.

## Bench

Three rounds of iterative improvement, each version run twice per rung through a throwaway relay chat, every answer graded blind against an answer key; Q is the mean sub-ask score times the share of sampled evidence claims correct.

| Rung | v0, one `sonnet` reader | v1, `haiku` readers (shipped) |
| --- | --- | --- |
| 7 sub-agent digests under 30 KB | Q .90 / .91, $0.24 / $0.32 | Q 1.00 / 1.00, $0.19 / $0.21, no reader spawned |
| One main chat, 523 KB digest, 170 prompts | Q 1.00 / 1.00, $0.61 / $0.54, peak context 124–128K | Q 1.00 at $0.45 with 4 readers, peak context 76K; Q .89 at $0.50 in the run that read the digest itself |

Two candidates lost on the large rung and were reverted. Barring the investigator from a large digest entirely (v2) cut the cost to $0.24 / $0.37 but dropped precision to .77 / .89: with no exchange around a reader's row, it counted plain questions and status pings as corrections. Letting it open 20-line windows around cited rows (v3) restored Q 1.00 / 1.00 at $0.50 / $0.60, no better per dollar than v1. The split pays only on digests well over 30 KB; most of a large run's cost is still the judge reading enough context to rule.

## Not part of the design

| Left out | Reason |
| --- | --- |
| `chat_read` | It reads backwards from the end, counts tool turns, and drops tool results; `pfm transcript show` with `--lines`, `--since` or `--grep` reads any window with results |
| `chat_digest` | The same `pfm transcript show` digest, but the whole result lands in the investigator's context on every read; `show --out` writes it once and `grep` and `sed` read a slice of it |
| `chat_capture`, `chat_resolve` | The pane and the tmux address answer live-control questions, not what a chat did; `chat_status` and `chat_last` carry the state and the newest answer |
| Writing a report file | Its caller consumes the final message; a file would be a second copy with no reader |

## Surfaces that stay in sync

| Surface | File |
| --- | --- |
| The agent and its reader | `templates/global/agents/chat-investigator.md`, `templates/global/agents/sub-investigator.md` |
| The digest it reads through | `pfm transcript` (`pfm/internal/transcript/digest/`) and `templates/global/skills/chatlog/SKILL.md` |
| The timeline and dollar source | `pfm tokens --timeline` |
| Name, sub-agent and liveness lookups | `pfm transcript locate`, `pfm transcript agents`, `pfm ls --all --tsv` |

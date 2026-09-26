# agent-optimizer

`agent-optimizer` audits how one agent actually ran against what it was built to do. It reads the agent's prompt, its design doc when one exists, the brief the run received and the run's transcript, walks the run end to end, and reports where the run lost time, tokens or correctness, each finding traced to the prompt line, design decision or harness behaviour that caused it, with the edit that would remove it. The prompt is the claim under test, never evidence that the run was right.

## Contents

- [Identity](#identity)
- [The brief it receives](#the-brief-it-receives)
- [What it reads](#what-it-reads)
- [The digest](#the-digest)
- [The audit](#the-audit)
- [Finding classes](#finding-classes)
- [The report](#the-report)
- [The return](#the-return)
- [Marks](#marks)
- [Not part of the design](#not-part-of-the-design)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)

## Identity

| Field | Value |
| --- | --- |
| Kind | original agent, `templates/global/agents/agent-optimizer.md`, linked by `pfm install` |
| Class | none: any caller, the user included, may ask for it |
| Model, effort | `opus`, `high` — the audit's value is catching the subtly wrong step, which is judgment over a large input |
| Tools | `Read, Grep, Glob, Bash, Write` |
| Budget | 40 tool calls |
| Spawns | nothing |
| Writes | one report under `$HOME/.local/state/pfm/agent-optimizer/`; never the prompt it audits |

Description, verbatim: `Audits a run end-to-end — "optimize agent X", "audit this run", "why was X slow or costly". Pass the agent name, its transcript paths or agent ids, and the run's intent. Returns the report path, then findings ranked by cost.`

## The brief it receives

| Line | Holds |
| --- | --- |
| `Agent:` | the agent type as registered, a variant name included |
| `Transcripts:` | one or more transcript paths, or agent ids; every transcript is a run of that one agent type |
| `Intent:` | optional: what the caller needed from the run; absent, the brief inside the transcript and the agent's description stand in |
| `Prompt:` | optional: the prompt file, overriding the lookup — for a prompt not yet installed, such as one under review in a worktree |
| `Blueprint:` | optional: the blueprint clone root; absent, `~/.professor` |

One report per agent type. A lead and its diggers are two audits: the lead's report judges the diggers only as the lead saw their returns, and the diggers' report covers every transcript of that type from the run, so a pattern across diggers counts once with its frequency.

## What it reads

- The prompt: `Prompt:` when given; else `{cwd}/.claude/agents/{name}.md` for the `cwd` the transcript records, since a project agent wins over a machine one; else `${CLAUDE_CONFIG_DIR:-~/.claude}/agents/{name}.md` resolved through its link. A variant resolves into pfm's generated directory; the audit then reads the source agent and the variant's `variants.json` entry too, since a finding may belong to either.
- The design doc: `{Blueprint}/docs/design/**/{name}.md`, and the source agent's doc for a variant. None found is stated, never guessed around.
- Each transcript and its `.meta.json` beside it (agent type, model, spawn depth, the description the caller gave). An agent id resolves by `find {CLAUDE_CONFIG_DIR or ~/.claude}/projects -name 'agent-{id}.jsonl'`.
- What the run produced: files it wrote, read from disk when they exist, since a claim in the return is only a claim.

## The digest

A transcript can run to megabytes; it is never read whole. One call to `token-audit.mjs --timeline` (repeatable, one flag per transcript) turns every run into a timeline:

- A header per run: agent type, model, effort, calls, wall time first to last record, peak context, output tokens, tool errors, results over 20 KB, and USD from the same replay and price table as `token-audit`'s default report.
- One row per model call (several `assistant` records sharing one `requestId` are one call): clock, seconds since the previous tool result, context (`input + cache read + cache write`), output, USD, and each tool the call issued with its target, result size, `ERR` and wait.
- `UNREADABLE — {path}: {error}` or `NO CALLS — {path}` for a run it cannot digest, a mark the report carries rather than an estimate.

A second call prints each run's content with one `jq` recipe the prompt carries: one line per record with its transcript line number, holding the brief, every tool input, the start of every result and the return. The timeline's tool targets are cut at 100 characters and often share one prefix, so without that view every audit built its own transcript dumper (about 21 calls over four self-audited runs). The prompt, a variant's source and the design doc are read whole with `cat -n` in the same first message, so later citations never re-grep for line numbers. Every Bash command opens with `emulate sh 2>/dev/null;`, since zsh aborts on `echo ====`. The transcript is opened beyond that only at a line number a finding needs. The script is the blueprint's (`{Blueprint}/templates/global/commands/tokens/`), so an audit run against a worktree reads that worktree's version.

## The audit

1. Intent: what the run was supposed to deliver, from `Intent:`, the brief and the description.
2. Flow: the prompt's procedure step by step against the timeline: where each step ran, which were skipped, repeated or reordered, and where the run did something no step asks for.
3. Output: the delivered artifact against the intent and against the prompt's own output contract; claims in it checked against the files and sources the transcript shows it read.
4. Cost: which calls and results carried the tokens and the seconds, and whether the work needed them.
5. Cause: each finding traced to the prompt line (`path:line` quoted), the design decision (doc section), the brief, or the harness or a tool. An instruction that produced the waste is a finding against the prompt; a run that broke a sound instruction is a compliance finding, whose fix escalates per `/quality:prompt` (a contrastive example from this run, then a mechanism) rather than louder wording.

## Finding classes

| Class | What it catches |
| --- | --- |
| `friction` | a step fighting the tools or harness: denied calls, retries, format errors, a tool used for what another does in one call |
| `repetition` | the same read, search, fetch or clone done twice; a file re-read; work a sibling already did |
| `hang` | a tool call whose result took over 60 s, a wait with no progress, a timeout, a prompt left waiting for input |
| `mistake` | a wrong or unbacked claim, a broken rule, a missed part of the intent, an error rendered as an absence |
| `waste` | tokens or seconds spent on nothing the output uses: oversized tool output, context carried forward, calls that could have been one |

## The report

`$HOME/.local/state/pfm/agent-optimizer/{name}-{YYYY-MM-DD}-{first agent id's first 8 characters}.md`, written once:

- a header: the prompt and design doc paths the audit resolved (the brief is not echoed back);
- the totals line from the digest;
- the flow table: each prompt step, where it ran (call numbers), and its verdict, which names what worked so a fix does not break it;
- findings ranked by cost, heaviest first, one `###` each: class, cost (calls, seconds, tokens), evidence (call number, timestamp, an excerpt of 200 characters at most), cause, fix. A fix is an exact edit (old text → new text in the named file) or a named mechanism, never "consider improving";

## The return

The report path, then one line of totals, then the findings, one line each: class, cost, the fix in a clause, and nothing else. The report holds the evidence and the caveats: in the self-audit, every one of four returns ran two to three thousand characters of sub-bullets the caller never needed.

## Marks

- `NO TRANSCRIPT — {what was searched}`: a path or id that resolved to nothing.
- `UNREADABLE — {path}: {the error}`: a transcript or prompt that exists but could not be parsed; an error never reads as a clean run.
- `NO DESIGN DOC — {where it looked}`: the audit ran on the prompt alone.
- `NOT SAVED — {the error}`: the report did not land; the findings follow in the return.

## Not part of the design

| Left out | Reason |
| --- | --- |
| Editing the audited prompt | The auditor is not the author; fixes go to `/pcm`, where the prompt laws and the guard apply |
| One report for a lead and its diggers together | Different prompts, different fixes; a mixed report ranks a digger's waste against a lead's |

## Surfaces that stay in sync

| Surface | File |
| --- | --- |
| The agent | `templates/global/agents/agent-optimizer.md` |
| The transcript record shape it digests | Claude Code's `projects/{project}/{session}/subagents/agent-{id}.jsonl` and `.meta.json` |
| The digest and dollar source | `templates/global/commands/tokens/token-audit.mjs --timeline` |
| The roster line | `docs/BLUEPRINT.md` |

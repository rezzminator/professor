# tracer

`tracer` answers a spec writer's numbered questions about a codebase from its code, in prose, each fact a `path:line` with the line quoted. It is a `general-purpose` reader made specific to that caller: the prompt keeps what an open-hand agent does well and fixes what it did wrong or wasted, measured against an independent key. It uses no script and writes no file; the final message is the answer. Exact text only goes to `collector`, a whole area to `mapper`. It ships in three tiers of one body: `tracer` → `tracer-pro` → `tracer-pro-max`.

## Contents

- [The prompt](#the-prompt)
- [Tiers](#tiers)
- [Open items](#open-items)

## The prompt

| Rule | The open-hand failure it fixes |
| --- | --- |
| Every read of a round in one message; a file read once is not read again | 30 tool calls reading one file each |
| Read to the line that raises, returns, writes, prints or decides; a fact not read goes under NOT READ | Facts stated one hop past what was read |
| A list claim names each item, or counts the share and names the exceptions; one read site never stands for a grep list | "All 12 catches give X" when 5 do not |
| `grep -rn` over the whole repo unless the brief narrows it | Callers under `scripts/` missed |
| Read-only, never run the project's code | A `uv run` that created a `.venv` |
| No preamble, side-effect notes, restated question or summary | Output padding |
| Always carry test homes, the scoped check command from the Makefile, anchors, and fenced verbatim lines to paste | The spec writer's recurring asks, so its brief can shrink to the questions |

Budget 25 calls, every tier.

## Tiers

Price, accuracy and clean blocks are each tier's measured share of `tracer-pro-max`'s, from [tracer-bench.md](tracer-bench.md).

| Agent | Kind | Model, effort | Price · accuracy · clean blocks | Pick it when |
| --- | --- | --- | --- | --- |
| `tracer` | original, `templates/global/agents/tracer.md` | `sonnet`, `high` | 43% · 98% · 58% | The default: the answer is the facts, and a quote is re-read before it is pasted |
| `tracer-pro` | variant, `templates/global/agents/variants.json` | `sonnet`, `xhigh` | 79% · 101% · 92% | Its quoted lines go into task files |
| `tracer-pro-max` | variant, `templates/global/agents/variants.json` | `opus`, `medium` | 100% · 100% · 100% | Every quoted line must paste exactly, at a steadier cost and latency |

- A variant overrides `name`, `description`, `model` and `effort` only; it carries no `replace`, so the three bodies are one text.
- The bench measured Sonnet 5.5 behind the alias `sonnet`; a new Sonnet release behind the alias re-opens the bench.
- Codex: only the executors carry a Codex pin (`TestGlobalAgentsWithoutCodexOverridesKeepOriginalBytes`), so the tiers compile through the alias map: `tracer` and `tracer-pro` to `gpt-5.6-luna` at `high` and `xhigh`, `tracer-pro-max` to `gpt-5.6-sol` at `medium`. The Codex tiers are unmeasured.
- A caller spawns `tracer` unless its prompt names a tier: `flights-speccer`, `/flights:spec` and `reviewer` name none.

## Open items

- Reading every item of a list the brief asks to classify: the rule is in the prompt, and the opus run still stopped at the grep hits.
- `flights-speccer` pastes a tracer's fenced lines as task-file shapes and spawns tier 1, whose blocks were clean 58% of the time; spawning `tracer-pro` for shape questions is a `flights-speccer` prompt change.
- The tier numbers rest on one run per setup, two for Sonnet at `xhigh`.

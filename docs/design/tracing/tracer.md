# tracer

`tracer` answers a spec writer's numbered questions about a codebase from its code, in prose, each fact a `path:line` with the line quoted. It is a `general-purpose` reader made specific to that caller: the prompt keeps what an open-hand agent does well and fixes what it did wrong or wasted, measured against an independent key. It uses no script and writes no file; the final message is the answer. Exact text only goes to `collector`, a whole area to `mapper`.

## Contents

- [The prompt](#the-prompt)
- [Measured](#measured)
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

Model `opus`, effort `medium`, 25 calls.

## Measured

One real spec-writer brief from an `opus` open-hand speccer replay: 4 questions on a Python repo. An independent `opus` judge built a 30-facet key from the code before any return existed and scored every return; DELIVERED 1, PARTIAL 0.5; a declared NOT READ scores partial, never wrong. Cost at assumed list rates. The baseline is the speccer's own `general-purpose` collector, inheriting `opus` at effort `high`.

| Run | Accuracy /30 | Wrong | Cost (USD) | Requests | Seconds | Return (KB) | Sub-asks half-answered | Fenced lines exact |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Baseline `general-purpose` | 29.5 | 3 | 2.26 | 25 | 164 | 17.9 | 0 | all |
| 1: `medium` | 27.0 | 2 | 1.09 | 15 | 133 | 16.5 | 2 | 73/77 |
| 1: `high` | 28.0 | 3 | 1.32 | 13 | 196 | 21.3 | 0 | 79/85 |
| 2: `medium`, read every listed item, fences with indentation | 28.0 | 2 | 1.17 | 15 | 158 | 19.5 | 2 | 89/89 |

- Run 2 wins on wrong statements, cost, requests, latency and verbatim fidelity, and loses on accuracy by 1.5 points, on size by 9% and on completeness.
- Its lost points: a list of 12 catch sites it declared NOT READ instead of reading, a traceback claim its own NOT READ contradicted, and the URL scheme missing from a cache key, which every run missed.

## Open items

- Reading every item of a list the brief asks to classify: the rule is in the prompt, and run 2 still stopped at the grep hits.

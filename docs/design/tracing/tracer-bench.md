# tracer bench

Every measured `tracer` run: the prompt bench that shaped its body on `opus`, and the model bench behind its three tiers.

## Contents

- [The bench](#the-bench)
- [Prompt bench](#prompt-bench)
- [Model bench](#model-bench)
- [Against tracer-pro-max](#against-tracer-pro-max)

## The bench

One real spec-writer brief from an `opus` open-hand speccer replay: 4 numbered questions, 2.4 KB, on a private Python repository of about 430 tracked source files. An independent `opus` judge built a 30-facet key from the code before any return existed.

A blind `opus` `general-purpose` judge scores each return, handed the key, the brief, the return under a neutral letter and two earlier scorecards for calibration: DELIVERED 1, PARTIAL 0.5, MISSING and WRONG 0; a declared NOT READ scores partial or missing, never wrong. Two facets were corrected against the code after the first passes, and every later judge carries the corrections. A second blind judge re-scoring the `opus` run landed on the same 28.0.

## Prompt bench

`tracer` on `opus` while its body was designed. The baseline is the speccer's own `general-purpose` collector, inheriting `opus` at effort `high`. Cost at the assumed list rates of the time; the model bench re-prices the two runs it shares. Fenced lines exact as the judge counted them.

| Run | Accuracy /30 | Wrong | Cost (USD) | Requests | Seconds | Return (KB) | Sub-asks half-answered | Fenced lines exact |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Baseline `general-purpose` | 29.5 | 3 | 2.26 | 25 | 164 | 17.9 | 0 | all |
| 1: `medium` | 27.0 | 2 | 1.09 | 15 | 133 | 16.5 | 2 | 73/77 |
| 1: `high` | 28.0 | 3 | 1.32 | 13 | 196 | 21.3 | 0 | 79/85 |
| 2: `medium`, read every listed item, fences with indentation | 28.0 | 2 | 1.17 | 15 | 158 | 19.5 | 2 | 89/89 |

- Run 2 wins on wrong statements, cost, requests, latency and verbatim fidelity, and loses on accuracy by 1.5 points, on size by 9% and on completeness.
- Its lost points: a list of 12 catch sites it declared NOT READ instead of reading, a traceback claim its own NOT READ contradicted, and the URL scheme missing from a cache key, which every run missed.

## Model bench

The final body on each model and effort. A Sonnet run spawns `tracer` with a `sonnet` model override and the effort in an `[effort: X]` brief prefix; every request's model and effort is read back from its transcript. Cost at official list rates per MTok (input / 5m cache write / 1h cache write / cache read / output): Opus 5.5 4 / 5 / 8 / 0.20 / 20, Sonnet 5.5 2 / 2.50 / 4 / 0.20 / 10.

Lines exact: a non-blank fenced line counts when it appears verbatim in the repository's tracked source, config or Makefile lines once the fence's own indent is stripped. A clean block has every line exact, so it pastes into a task file as it stands.

| Setup | Tier | Accuracy /30 | Wrong | Cost (USD) | Requests | Seconds | Return (KB) | Lines exact | Clean blocks |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Baseline `general-purpose`, `opus` `high` | — | 29.5 | 3 | 1.37 | 25 | 164 | 17.9 | 26/81 | 5/10 |
| `opus` `medium` (prompt run 2) | `tracer-pro-max` | 28.0 | 2 | 0.80 | 15 | 158 | 19.5 | 84/84 | 9/9 |
| Sonnet 5.5 `xhigh`, run 1 | `tracer-pro` | 28.5 | 2 | 0.50 | 10 | 146 | 22.5 | 77/78 | 12/13 |
| Sonnet 5.5 `xhigh`, run 2 | `tracer-pro` | 28.0 | 2 | 0.77 | 16 | 242 | 22.7 | 87/88 | 11/12 |
| Sonnet 5.5 `high` | `tracer` | 27.5 | 3 | 0.34 | 7 | 94 | 23.7 | 32/96 | 7/12 |
| Sonnet 5.5 `medium`, run 1 | — | 27.0 | 2 | 0.25 | 6 | 67 | 17.3 | 54/76 | 5/12 |
| Sonnet 5.5 `medium`, run 2 | — | 27.5 | 3 | 0.24 | 7 | 75 | 19.3 | 77/86 | 6/11 |

- Accuracy moves within 3% across every setup; price and quoting are what the tiers trade.
- Quoting does not rise with effort: `high` quoted worst, one 38-line shell script carrying its line numbers.
- Both `xhigh` runs elided one line of the same block with `...`, their one quoting fault. Run 1 made 26 tool calls against the 25-call budget.
- `xhigh`'s cost swings from $0.50 to $0.77 between two runs of one brief, its latency from 146 s to 242 s.
- Wrong statements of the second `xhigh` run: a `text/plain` robots body on a 403 or 301 read as zero rules, where the code parses and enforces whatever rules it carries; one logger named the only literal-name exception when a second exists.

## Against tracer-pro-max

Each tier's share of `tracer-pro-max`; `tracer-pro` is the mean of its two runs.

| Tier | Price | Accuracy | Clean blocks | Seconds |
| --- | --- | --- | --- | --- |
| `tracer-pro-max` | 100% | 100% | 100% | 158 |
| `tracer-pro` | 79% (62–96%) | 101% | 92% | 194 |
| `tracer` | 43% | 98% | 58% | 94 |
| Sonnet 5.5 `medium`, untiered | 31% | 97% | 48% | 71 |

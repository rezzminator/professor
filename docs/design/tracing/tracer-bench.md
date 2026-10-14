# tracer bench

Every measured `tracer` run: the single-agent bench behind the model, the tree bench behind the lead and its sub-tracers, the replay of past heavy briefs, the caller probes and their audits, the caller bench behind the final-is-the-answer contract and the intake hook, the rejected-hop survey behind the resolver and drift, the runs that checked the kit, and the field runs swept from disk. The design is [tracer.md](tracer.md), the kit [tracetree.md](tracetree.md).

## Contents

- [Single-agent bench](#single-agent-bench)
- [Tree bench](#tree-bench)
- [Replay bench](#replay-bench)
- [Caller probes](#caller-probes)
- [Caller bench](#caller-bench)
- [Rejected-hop survey](#rejected-hop-survey)
- [Resolver and drift](#resolver-and-drift)
- [Engine trace](#engine-trace)
- [Live probe](#live-probe)
- [Field runs](#field-runs)

## Single-agent bench

The body before the tree, one agent reading everything itself, measured on one real spec-writer brief: 4 numbered questions, 2.4 KB, on a private Python repo of about 430 tracked source files. An independent `opus` judge built a 30-facet key from the code before any return existed.

A blind `opus` `general-purpose` judge scored each return, handed the key, the brief, the return under a neutral letter and two earlier scorecards for calibration: DELIVERED 1, PARTIAL 0.5, MISSING and WRONG 0; a declared NOT READ scores partial or missing, never wrong. Two facets were corrected against the code after the first passes, and every later judge carried the corrections. A second blind judge re-scoring the `opus` run landed on the same 28.0.

### Prompt bench

`tracer` on `opus` while its body was designed. The baseline is the speccer's own `general-purpose` collector, inheriting `opus` at effort `high`. Cost at the assumed list rates of the time; the model bench re-prices the two runs it shares.

| Run | Accuracy /30 | Wrong | Cost (USD) | Requests | Seconds | Return (KB) | Sub-asks half-answered | Fenced lines exact |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Baseline `general-purpose` | 29.5 | 3 | 2.26 | 25 | 164 | 17.9 | 0 | all |
| 1: `medium` | 27.0 | 2 | 1.09 | 15 | 133 | 16.5 | 2 | 73/77 |
| 1: `high` | 28.0 | 3 | 1.32 | 13 | 196 | 21.3 | 0 | 79/85 |
| 2: `medium`, read every listed item, fences with indentation | 28.0 | 2 | 1.17 | 15 | 158 | 19.5 | 2 | 89/89 |

Run 2 wins on wrong statements, cost, requests, latency and verbatim fidelity, and loses 1.5 points of accuracy: a list of 12 items declared NOT READ instead of read, a claim its own NOT READ contradicted, and one facet every run missed.

### Model bench

The final single-agent body on each model and effort. A Sonnet run spawns `tracer` with a `sonnet` override and the effort in an `[effort: X]` brief prefix; every request's model and effort is read back from its transcript. Cost at list rates per MTok (input / 5m cache write / 1h cache write / cache read / output): Opus 5.5 4 / 5 / 8 / 0.20 / 20, Sonnet 5.5 2 / 2.50 / 4 / 0.20 / 10.

Lines exact: a non-blank fenced line counts when it appears verbatim in the repository's tracked source, config or Makefile lines once the fence's own indent is stripped. A clean block has every line exact, so it pastes into a task file as it stands.

| Setup | Accuracy /30 | Wrong | Cost (USD) | Requests | Seconds | Return (KB) | Lines exact | Clean blocks |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Baseline `general-purpose`, `opus` `high` | 29.5 | 3 | 1.37 | 25 | 164 | 17.9 | 26/81 | 5/10 |
| `opus` `medium` (prompt run 2) | 28.0 | 2 | 0.80 | 15 | 158 | 19.5 | 84/84 | 9/9 |
| Sonnet 5.5 `xhigh`, run 1 | 28.5 | 2 | 0.50 | 10 | 146 | 22.5 | 77/78 | 12/13 |
| Sonnet 5.5 `xhigh`, run 2 | 28.0 | 2 | 0.77 | 16 | 242 | 22.7 | 87/88 | 11/12 |
| Sonnet 5.5 `high`, the shipped `tracer` | 27.5 | 3 | 0.34 | 7 | 94 | 23.7 | 32/96 | 7/12 |
| Sonnet 5.5 `medium`, run 1 | 27.0 | 2 | 0.25 | 6 | 67 | 17.3 | 54/76 | 5/12 |
| Sonnet 5.5 `medium`, run 2 | 27.5 | 3 | 0.24 | 7 | 75 | 19.3 | 77/86 | 6/11 |

- Accuracy moves within 3% across every setup; price and quoting are what the setups trade.
- Quoting does not rise with effort: `high` quoted worst, one 38-line shell script carrying its line numbers.
- Both `xhigh` runs elided one line of the same block with `...`, their one quoting fault; run 1 made 26 tool calls against a 25-call budget.
- `xhigh`'s cost swings from $0.50 to $0.77 between two runs of one brief, its latency from 146 s to 242 s.

### Against an opus lead

Each setup's share of the `opus` `medium` lead; Sonnet 5.5 `xhigh` is the mean of its two runs.

| Setup | Price | Accuracy | Clean blocks | Seconds |
| --- | --- | --- | --- | --- |
| `opus` `medium` | 100% | 100% | 100% | 158 |
| Sonnet 5.5 `xhigh` | 79% (62–96%) | 101% | 92% | 194 |
| Sonnet 5.5 `high`, the shipped `tracer` | 43% | 98% | 58% | 94 |
| Sonnet 5.5 `medium` | 31% | 97% | 48% | 71 |

## Tree bench

The tree — a `sonnet` `high` lead over `haiku` `low` sub-tracers — against the single-agent body it replaced, `sonnet` `high`, on three briefs over this repository's `pfm/`:

- B1, one question: the `exit` MCP tool from its handler down to the operating system — every function, process, tmux, file and database effect, and each failure's text and receiver. A 33-facet key built from the code before any run.
- B3, four questions: every caller of three pricing loaders and what it does with the error, the table served when the price file fails to decode, how a model id finds its price row, and the tests that pin `pfm model-cost --check`. A 21-facet key.
- B4, two questions: six MCP chat tools each traced to its system effect and failure texts, and the tests that pin each. No key: the two finals were read side by side.

Scored by script, no model judge. Facets: the key's facets a final names, matched by anchor patterns, half a point for a partial. Cites ok: the `[path:N]` cites and backticked quotes whose text sits at the cited line or within 3 lines of it. Cost: every request of every agent of the run, read from its transcripts and priced at `pfm/internal/pricing/prices.json` rates per MTok — Sonnet 5.5 2 in / 10 out / 0.10 cache read / 2.50 5m cache write, Haiku 5.5 0.10 / 0.50 / 0.01 / 0.125. Value: facet share × cite-ok share ÷ USD.

One run per setup unless marked; each version changed several rules at once, so a row shows why a rule exists, not its isolated effect. v1: the first tree, no file-boundary split or hop cap; v2: one wide look, fuel, the 40-hop cap; v3: sub-tracers at `low`, ANSWER blocks, the answers view; v4: `tt wait` and cite re-addressing; v5: `tt write` once; v6: `tt start`, the shipped prompts; v7: measured and reverted.

| Run | Brief | Facets | Cites ok | Cost (USD) | Lead's share | Sub-tracers | Seconds | Value |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Single agent | B1 | 33/33 | 30/45 | 0.344 | 100% | 0 | 89 | 1.94 |
| v2, `haiku` `medium` | B1 | 32.5/33 | 67/94 | 0.451 | 66% | 5 | 243 | 1.56 |
| v3, lead `medium` | B1 | 30/33 | 18/31 | 0.221 | 77% | 5 | 118 | 2.39 |
| v4 | B1 | 30.5/33 | 16/34 | 0.468 | 71% | 6 | 304 | 0.93 |
| v4, lead `medium` | B1 | 23/33 | 23/36 | 0.235 | 65% | 4 | 209 | 1.89 |
| v5 | B1 | 32/33 | 21/31 | 0.433 | 79% | 5 | 626 | 1.52 |
| **v6**, lab run | B1 | 31/33 | 31/38 | 0.313 | 70% | 6 | 245 | **2.45** |
| **v6**, from the installed files | B1 | 27.5/33 | 28/49 | 0.340 | 70% | 6 | 210 | **1.40** |
| v7, run 1 | B1 | 31.5/33 | 28/40 | 0.389 | 73% | 6 | 203 | 1.72 |
| v7, run 2 | B1 | 25.5/33 | 33/48 | 0.254 | 60% | 7 | 227 | 2.09 |
| v7 with the path fix, run 1 | B1 | 30/33 | 22/56 | 0.491 | 64% | 14 | 180 | 0.73 |
| v7 with the path fix, run 2 | B1 | 32/33 | 50/79 | 0.693 | 77% | 10 | 491 | 0.89 |
| Single agent | B3 | 17.5/21 | 16/26 | 0.141 | 100% | 0 | 21 | 3.64 |
| v4 | B3 | 18/21 | 15/18 | 0.193 | 79% | 4 | 104 | 3.70 |
| v4, lead `medium` | B3 | 14/21 | 6/8 | 0.140 | 73% | 4 | 82 | 3.58 |
| v5 | B3 | 16.5/21 | 9/11 | 0.171 | 77% | 4 | 105 | 3.75 |
| Single agent | B4 | — | 34/51 | 0.513 | 100% | 0 | 134 | — |
| v5 | B4 | — | 51/69 | 0.424 | 76% | 7 | 653 | — |
| **v6** | B4 | — | 52/71 | 0.364 | 62% | 9 | 216 | — |
| v7 | B4 | — | 28/57 | 0.486 | 62% | 12 | 205 | — |

- One setup swings far between runs: v6's two B1 runs score 2.45 and 1.40, mean 1.93 against the single agent's one run at 1.94. On B4 the tree cost 29% less at an equal answer, deeper on `new`, `status` and `chat call`, a slightly thinner `new` test list. On B3, which the single agent settles in 7 requests, the two tie on value and the tree costs more.
- The lead is 60–80% of every tree run's cost: its context written to cache and its synthesis output. A `haiku` `low` sub-tracer costs $0.01–0.02; at `medium` one cost about $0.04, mostly thinking.
- v1, not in the table: on B3 the lead delegated nothing and read everything itself; on B1 no node spawned a grandchild and each walked about 70 hops alone.
- v2's printed tree was as large as the code: the lead's peak context reached 61–69K tokens against the single agent's 72K.
- v3: a lead counting wakes answered one of two children finishing 13 s apart, the second never woke it, and the final was lost; the `medium` lead made four reads of its own, cut three threads and wrote 13 cites wrong.
- A `medium` lead is cheaper and drops facts while writing the final: 23/33 on B1 and 14/21 on B3 against `high`'s 30.5 and 18.
- v4: a node rewrote its file 45 s after its first write, finished after the lead's final and woke it; the lead wrote the final again, its output reaching 18K tokens.
- v5's 626 and 653 s are its wait hanging to the 540 s timeout on SPLIT lines naming children never spawned.
- The tree is slower: 2–3 times the single agent's seconds.
- v7 listed the run's other threads on each card, had a sub-tracer split any needed branch no other thread walked, and had the lead's gap round take any such branch left in a LEAD. It fixed what v6's installed run lacked — no node had split, and the lead had passed over a needed branch a node left as `belongs to the caller` — and bought half a facet on average, 29.75 against 29.25, at 40% more cost. More threads and gap rounds grew the answers view the lead re-sends, and in 3 of 5 runs the lead read code itself before spawning, against none of 3 v6 runs. Mean value 1.36 at $0.457 over four B1 runs, against v6's 1.93 at $0.327.
- One v7 run wrote bare file names, `server.go:236`, and 25 correct hops were rejected as `no such file`; tail resolution now places such a path by its quote, and a re-check rejects none.
- Across the nine v4–v6 runs, 7 of 1,601 checked hops were rejected, their quote in no file; 69 were re-addressed. Answer lines held less well: 93 of 670 were flagged, 3–20% per run.

## Replay bench

The shipped tree re-ran five of the costliest past `tracer` calls made by spec writers, each past brief verbatim. There were 395 past runs costing $336 in total; 276 came from `flights-speccer` or a `/flights:spec` main chat. Two briefs were over a private Python repo and three over this one.

A blind judge (`opus`, `high`) graded each pair, the past answer and the tree's, without being told which design wrote which. It graded each sub-ask FULL, PARTIAL, MISSING or WRONG and checked claims against the code: the past answer against the code of its own day, the tree's against today's. A past fact the code has since changed counts as drift, not an error.

The past runs used the tracer body of their day, mostly on `opus`, so the past column is what callers paid, not a controlled A/B.

| Replay | Brief | Past model | Past USD | Tree USD | Seconds, past → tree | Sub-tracers | Lead's share | Judge |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| P0 | private: a 24-row table read from 24 docs, plus 5 questions | `opus` 5 | 4.00 | 0.44 | 340 → 322 | 5 | 64% | past better |
| P5 | private: 5 questions over 50-odd modules | `opus` 5.5 | 2.04 | 1.15 | 206 → 472 | 26 | 70% | tree better |
| P14 | 7 questions mapping a `pfm doctor` row | `sonnet` 5 | 1.61 | 0.30 | 608 → 145 | 6 | 72% | tree better |
| P20 | 8 questions on merging two MCP servers; the merge has since landed | `opus` 5.5 | 1.56 | 0.39 | 160 → 156 | 8 | 76% | past better |
| P22 | 12 questions mapping the release surface | `opus` 5.5 | 1.54 | 0.72 | 194 → 279 | 14 | 79% | tree better |
| Total | | | 10.75 | 2.99 | | | | 3 of 5 to the tree |

- Cost: 72% less. Price per token, not fewer tokens, makes the saving: the tree used 2.4 times the requests and 2.9 times the tool calls, and wrote 4.2 times the output and 2.6 times the cache, on `sonnet` and `haiku` where the past ran mostly on `opus`.
- P5's 26 sub-tracers wrote 727 hops, 724 of them verified.
- Sub-asks graded FULL: the past 44 of 65, the tree 37. The tree had no MISSING or WRONG sub-ask; the past had 2 MISSING and 1 WRONG, and many of its PARTIALs are drift. Per FULL sub-ask, the tree cost $0.08 and the past $0.24.
- Correctness: the judges checked 261 of the tree's claims and found 16 wrong, against 4 wrong of 182 past claims; every one of the tree's is a line number or a label, never a false fact. P20 holds 12 of them: its lead read 156 lines in its wide-look call with `sed -n 20,175p | cat -n`, which numbers from 1, and wrote six cites 19 lines off. Its sub-tracers' answers for the same file were right, because the kit had re-addressed their hops. The sub-tracer prompt had taught that pipe; both prompts now number before slicing.
- P0 lost on completeness: the lead put the 24 docs into three threads of 8, two went unread, the answer marked them "not read", and the lead ran no gap round.
- P5 and P22 won on breadth: the tree found an existing test matrix and the current release machinery. P14 won because only the tree saw that the row the brief would add already exists, a row added after the past run, so the win is partly the code moving on.
- P20's brief had gone stale: the merge it plans has landed, and the tree said so and mapped today's combined server.

## Caller probes

A Sonnet chat given a vague task and told to use tracer, its whole session then audited by `agent-optimizer`. Each round's findings became the rules and kit mechanisms the round after; the loop stopped after round 3.

| Probe | Task | Session cost | Notes |
| --- | --- | --- | --- |
| 1 | an inject task | $2.29 | 10 caller calls, two tracer rounds |
| 2 | an await task | $1.67 | 3 caller calls; never loaded the skill |
| 3 | a `pfm install` task | $3.56 ($2.47 agents) | 6 tracer calls, 3 of them follow-ups; re-asked facts cost $0.63, 26% of agent spend |

### Probe 1

Two tracer rounds, $0.42 and $0.58, the lead 76% and 73% of each; 21 sub-tracers, none of which split.

- The gap round respawned three round-1 threads: 4 sub-tracers and 3 lead calls, $0.17.
- 71 of 509 hops kept one line because the quote skipped a line, and the caller re-read the blocks with `sed`.
- 44 answer lines were flagged `?` for an argv assembled from several lines; it was re-asked twice and still reported unconfirmed.
- Each gap-round wait reprinted round 1, and the lead spent 2 extra calls, $0.067 and 44.6K characters, reading its own printout back.
- A `T="python3 ~/.claude/…"; $T check` call failed with `can't open file`, the quoted `~` never expanding; a `for` loop over four `--hops` IDs poured 24.5K characters into every later call.
- One ten-line sub-ask left open cost the caller three calls.
- `map.txt`, 10.8K and 13.8K characters, was grepped once and never opened: files sorted by name put the function the caller's plan turned on, `engine.go::injectResolved` (10 threads, 62 hops), after 30 lines of fixtures.
- 2,903 fenced characters went unused by the plan.

### Probe 2

Two rounds, $0.44 each; 19 sub-tracers. The caller, never told to load `read-tracer-run`, did not.

- The round-2 lead read 636 lines in three calls, 30% of its round, and still handed on, as "found by grep only", the twenty lines holding the rule the caller's main fix needed.
- 11 questions got 7 threads; the other two spawned in a second wave 83 s later.
- The caller ignored "drill in with /read-tracer-run", called 4 of its 8 open items unsettled while the run held parts of 2, and re-asked three facts round 1 had cited.

### Probe 3

Six leads, $1.76 over 31 requests, and 53 sub-tracers, $0.70; 41 numbered questions. The caller loaded no skill.

- Four follow-up calls re-asked 14 threads' worth of facts earlier runs held, $0.63, one sent while the sibling holding its answer still ran.
- 12 `run: node` items were handed on with 0 of 12 drills spent; 3 came back as follow-up threads.
- 12 of 34 NOT READ items carried no next step, and 5 of 6 leads handed on grep-only lines, after two rounds of prose rules.
- Own reads ran past the bound in 4 of 6 leads, blocks to 55 lines, and 13 fenced blocks came from own reads instead of `--full`.
- One lead respawned two threads instead of drilling them.
- Cards repeating 350–500-character questions cost 4.5–7.6K output tokens and 28–96 s per lead.
- Three leads' wait printouts of 30–36 KB overflowed the inline result and cost a Read each.

These drove the kit mechanisms: `new --seek` PRIOR hops, `--questions` with `QUESTION: Q{n}` cards, the `wait` OPEN block, the 28000-character ceiling with `answers.txt`, and `tt read`; and the final's last line became a runnable `check --hops | grep` command.

### The foreman flight

`flights-foreman`'s first flight under its tracer rule, audited for its use of tracer. The tracer run cost $0.56 — lead $0.41, 13 sub-tracers $0.15 — 20% of the foreman's round-1 spend.

- The foreman never loaded `read-tracer-run`: no Skill call and no `tracetree.py` call in 113 rows.
- Its tracer questions were survey-shaped where the build needed each door's surface and test fixture.
- A compaction dropped the `RUN`, so the return reported as unchecked a door the run held verified.
- The run directory landed under the worktree's name instead of the project's; the kit now names it from git's common directory.

## Caller bench

Callers spawning tracer through the [intake hook](tracer.md#intake-hook), with `tt answer` to pull raw reader answers early, against callers with no tracer.

### Reviewer rung

The large reviewer rung: a review target holding 23 known defects. Recall counts the defects a review found; false SAFE counts the defects it called safe. The pull wording told callers the final repeats what `answer` printed; the shipped wording makes the final the answer and pulled answers facts to start work on, never a ruling.

| Arm | Recall /23 | False SAFE /23 |
| --- | --- | --- |
| Tracer seats and kit before `tt answer` | 12, 9 | 2, 4 |
| Tracer, the final said to repeat `answer` | 4, 5, 3 | 8–9 |
| Tracer, the final as the answer (shipped) | 8, 9 | 4, 5 |
| No tracer | 9, 7 | 8, 9 |

- Told the final only repeats the pulls, reviewers wrote their review from raw pulls before the final arrived: recall 3–5 against the baseline's 12 and 9.
- With the final restored as the answer, recall matched the no-tracer arms' and false SAFE was 4 and 5 against their 8 and 9.

### Foreman probes

`flights-foreman` probes stopped at their first write, 20 key files per target.

| Arm | Key files known before the first write (of 20) |
| --- | --- |
| Tracer with the hook | 18, 19, 18, 16 |
| No tracer | 14 |
| Tracer with an erroring hook | 11 |

The foreman's own code reading before its first write:

| Arm | Calls (mean) | Chars (mean) |
| --- | --- | --- |
| No tracer | 22 | 169k |
| Tracer with the hook | 16 | 92k |

- With the tracer the foreman read 45% fewer characters itself; adding about 42k characters of tracer answers, the code text in its context was 20% lower.
- The tracer cost about 1 minute and $0.60 more to the first write.
- The first pulled answers arrived 1.7, 2.2, 1.8 and 2.7 minutes after the spawn.

## Rejected-hop survey

Every rejected hop of the day's runs, 239 in all, classified by a script against the files and their git history:

| Cause | Hops |
| --- | --- |
| Drift: the quote held at the run's start commit | 90 |
| Invented directory: the path named no file, and exactly one file of that basename held the quote at the line | 49 |
| Renamed or deleted file, or a `...` elision | about 36 |
| Basename found nowhere: lab fixtures, and one run on a submodule plugin since renamed | 29 |
| Paraphrased or invented quote line | 20 |
| Sibling file holding the quote at the line | 6 |
| Line unique elsewhere in the repository | 4 |
| Truncated line | 2 |
| Directory cited as a file | 1 |

One Haiku node wrote 27 `pkg/...` paths in a single run, the engine trace, for files that live elsewhere. The resolver's `basename and quote`, `sibling file` and `unique line` steps answer the 49, 6 and 4; drift answers the 90; write-time refusal answers the 20 and the elisions, truncations and directory.

## Resolver and drift

The day's runs re-checked with the resolver and drift:

- Across all runs, rejected 234 → 88, drifted 70, verified +76.
- The engine trace went from 40 rejected to 0, with 12 drifted.
- The foreman flight's run went from 14 rejected to 0, with 11 drifted.
- 55 of the 88 still rejected are one run on a submodule plugin since renamed: a renamed file is not followed.

## Engine trace

A trace of the Claude engine's launch path on this repository: $0.82, 18 threads, 583 hops, 161 to 165 components across its re-checks. The Sonnet lead was about 80% of the cost; the Haiku nodes about $0.01 each.

- 40 hops were rejected at first; one Haiku node wrote 27 of them as invented `pkg/...` paths. After the resolver and drift none stayed rejected: the invented paths were placed, and 12 hops drifted when a flight rewrote the launcher two hours later.
- The map: `--to Render` printed the launch chain from `main.go::run` down to it, eight frames, and `--from run` the whole chain down to the tmux session. Qualifying Go calls by package removed 4 false links a same-named method had made; the first build had 77 call links in 84 files.
- Placing invented paths silently hid a node that broke the hop contract. The user ruled that it must scream if it happens again: the WARNINGS block, the COUNTS fields, the MAP suffix, the page's red block, the JSON field and `write`'s stderr warning.

## Live probe

A live tracer run after write-time refusal and drift landed: 2 nodes, 62 of 62 hops verified, no refusal, both sidecars and `BASE` written; $0.23 in 109 s.

## Field runs

Every run directory written under `/tmp/{project}/tracer/` from 2026-10-09 21:00, when the tree landed, to 2026-10-11 01:04: 259 runs, each re-checked with `tt check` on 2026-10-11 and joined to the lead transcript that ran its `tt new`, the lead's agent type read from the transcript's `.meta.json`. Claude-led runs by caller: `reviewer` and its bench variants 159, main chats 11, `clone-distiller` 10, `general-purpose` 3, `gpt-foreman` 3, `flights-foreman` 2.

### Kit counts by lead

Drift is measured at the re-check, not the walk, so a worktree edited after its trace drifts; a bad address is a hop citing a line past its file's end that did not hold when walked.

| Lead | Runs | No hops | Hops | Rejected | Bad address | Refused once, of nodes | Flagged, of answer lines | Drifted |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `tracer`, `sonnet` `high` | 171 | 2 | 30,617 | 0.17% | 0.11% | 6.1% | 11.1% | 2.8% |
| `gpt-tracer`, `gpt-6-sol` | 85 | 1 | 6,746 | 0.84% | 8.3% | 8.6% | 89.0% | 84.6% |

- `gpt-foreman` edited the worktrees its twins had traced, so most of their hops drifted and their flags follow; rejections and bad addresses do not depend on drift.
- No sub-tracer ran `tt new`: the 11 sub-tracer transcripts naming it quote it, never call it.

### Lead flow

Over the 183 Claude lead transcripts:

| Measure | Value |
| --- | --- |
| Tool calls | median 15, p90 24, max 40 |
| First spawn at call 2 | 157; after call 3: 10; never: 1 |
| Polls (`sleep`, `true`, listing the run) | 3 in all |
| `tt wait` without the 600000 timeout | 6 of 241 |
| More than two drills | 23 |
| Final carrying RUN, MAP and NOT READ | 180 |
| Final length | median 18,369 characters, p90 25,320 |

Spend over the 72 hours to 2026-10-11 01:37, from `pfm tokens`: `tracer` 219 runs, $84.89, median $0.35, p90 $0.59, max $0.97; `sub-tracer` 1,801 runs, $21.85, median $0.01; `gpt-tracer` 61 runs, $33.30, max $11.23; `gpt-sub-tracer` 562 runs, $4.83.

### Lead audit

`agent-optimizer` audited ten Claude leads picked as outliers: the three finals the flow count read as empty, the two runs with no hops, two leads over the drill cap, the two heaviest and one `clone-distiller` caller. Ten runs, 200 tool calls, 17 tool errors, $4.79.

- Empty finals: one was a false reading and its caller got the whole final; one lead was cut off mid-spawn after its caller had returned; one wait ended with exit 137 at 240 s as its session shut down.
- No hops: one lead never spawned and read the code itself; the other spawned 7 threads, all refused at the harness's 50-agent concurrency cap, and the lead read whole files instead of running the gap round. Another lead had 7 of 11 spawns refused, respawned all 7 in the gap round, and every one started.
- Large file: a root holding a 59 MB minified bundle (415,089 lines) cost 12 calls, about 1,650 s and $0.55 of a $0.97 run: one wait returned after 597 s against its 540 s timeout, the next was moved to the background at 600 s, and the lead spent ten calls on short re-waits, `pkill`, a killed `check` and a `sleep` loop. Re-checked on 2026-10-11, one `check --hops` on that run did not finish in 120 s. With files over 1 MiB searched only near an address ([Very large files](tracetree.md#very-large-files)), the same run's `check --hops 1` takes 0.9 s and `check --full 9` 0.5 s.
- Drills: the third and fourth drills of the two over-cap leads supplied 8 to 28 cites each that their finals used; one node was drilled twice.
- Bypassed reads: a refused second `tt read` was bypassed with an unnumbered `sed` in two runs; in one, 9 of 20 cites landed one or two lines off.
- Correctness: a reviewed final's 32 first-question cites all match the run's `BASE`, and its flagged lines were re-quoted correctly.

### Caller audit

`chat-investigator` read six callers that spawned tracer in more than one message: a main chat and five reviewer seats.

- 0 of 10 later spawns re-asked what an earlier final held; four went out before any tracer had returned. Seven finals held a fact a later spawn partly re-found, never a whole later question.
- No caller ran `check`, `map` or `read` on a returned run or loaded `read-tracer-run`; across all 84 callers 3 ran `check` or `map` and 1 loaded the skill.
- Neither caller whose finals carried `⚠` lines relayed them; one review file reports its seats with no mention of the rejected or placed hops.
- All five reviewer seats re-read ranges the finals had quoted, with `sed`, under "Verify candidate lines".

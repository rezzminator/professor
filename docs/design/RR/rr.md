# rr

`rr` maps the knowledge area one query asks about. A lead opens the query, plans its sub-areas, sends diggers down the rabbit holes that serve the plan, checks the facts its answer rests on against their pages, and saves one cited document. This file holds every decision of the family.

A change lands in this file first, then in the templates, then in every surface listed under [Surfaces that stay in sync](#surfaces-that-stay-in-sync).

## Contents

- [The family](#the-family)
- [The run](#the-run)
- [The document](#the-document)
- [Why diggers never write](#why-diggers-never-write)
- [The lead-call budget](#the-lead-call-budget)
- [The plan](#the-plan)
- [Findings are quote-anchored](#findings-are-quote-anchored)
- [Verification](#verification)
- [The frontier and the stop rule](#the-frontier-and-the-stop-rule)
- [Marks](#marks)
- [Not part of the design](#not-part-of-the-design)
- [What the evidence says](#what-the-evidence-says)
- [Measuring a run](#measuring-a-run)
- [Surfaces that stay in sync](#surfaces-that-stay-in-sync)
- [Open items](#open-items)

## The family

| Member | Kind | Does | Runs at |
| --- | --- | --- | --- |
| `rr` | agent | The lead: opens, plans, aggregates, dispatches, verifies, synthesizes, saves; the only member holding `Write`, and it holds no `Read` | frontier-judgment (`opus`), effort `low` |
| `super-rr` | variant of `rr` | The same body at higher effort, rendered from `variants.json` by `pfm codex agents` | `opus`, effort `medium` |
| `sub-rr` | agent | The digger: answers a numbered batch of sub-queries, returns findings and rabbit holes | spec-execution (`sonnet`), effort `low` |
| `rr-dir` | `SubagentStart` hook | Puts the `RR-DIR:` line (the ledger directory) into the lead's context; matcher `rr\|super-rr` | `pfm internal rr-dir` |

Vocabulary, one term per concept: a rabbit hole is a link, gap or missing piece a source raises and leaves unexplained; a sub-area is one part of the knowledge area, named in the plan; a finding is a digger's answer to one sub-query; a fact is a load-bearing statement in the map; a quote is the verbatim sentence that states a fact.

## The run

1. OPEN. One message carries the WebSearch and, for a scholarly question, the harvester lookups; the next message fetches the 1-3 best starting pages. A failed or empty search ends the run with a one-line report.
2. PLAN. The lead splits the knowledge area into 3-7 sub-areas, each with a `settled when` line.
3. AGGREGATE. Findings and rabbit holes are pooled, each sub-area is judged against its `settled when` line, and the frontier is rebuilt from the rabbit holes that serve an unsettled sub-area.
4. DIG. At most 4 diggers a round, the frontier grouped by sub-area so no two diggers own the same ground, every digger of the round in one message. The lead waits by ending its message.
5. LOOP. Steps 3-4 repeat until the stop rule fires.
6. VERIFY. One message of WebFetch checks over the facts the answer rests on.
7. SYNTHESIZE. Answer, map, coverage, verification, open rabbit holes.
8. SAVE, then RETURN. One Write of the finished document; the return is the saved path, then the synthesis.

## The document

One run produces one markdown document, `{RR dir}/{slug}-{YYYY-MM-DD}.md`, and nothing beside it: no part files, no sidecar directory, no log.

- One writer. The lead is the only agent that writes it. `sub-rr` holds no `Write`, `Edit` or `Bash`; its `tools:` line carries that invariant, so the harness enforces it and no prose has to.
- One write. The finished document is written once, at SAVE. Nothing is written before it and nothing is appended, so the lead needs no `Edit` tool and no sentinel lines.
- No `Read`, on purpose. The harness refuses a Write over an existing file the agent has not read (`File has not been read yet`), and relaxes that refusal for an agent that holds the `Read` tool: such an agent overwrites an unread file without a word. The lead's missing `Read` is therefore load-bearing. It is what turns a second run on a taken name into a refusal instead of the silent loss of the first run's document. Adding `Read` to the lead's `tools:` line removes the guard. It would also let a lead open the ledger and inherit an earlier map's conclusions instead of deriving its own; for the same reason both prompts admit only web URLs and document identifiers as sources, because harvester `fetch` accepts a local path.
- A failed save is loud. With no `RR-DIR:` line, an `RR-DIR-ERROR:` line, or a Write that fails for any reason but a taken name, the return opens `NOT SAVED — {reason}` and carries the synthesis inline.

Layout of the finished document:

```text
# RR — {question in one line}
Question: {the query, verbatim}

{answer, one to two sentences}
{the map, by sub-area}
Coverage       the plan, each sub-area settled | partial | open
Verification   facts checked; every NOT ON PAGE and UNCHECKED by name
{rabbit holes left open}
```

Two runs on one name: the slug is chosen by each lead alone, so a second run on one subject, the same day, can pick a name already taken. The harness settles it: the second lead's Write lands on a file it has not read, is refused, and that lead takes the suffix `-2`, then `-3`. The refused Write costs the document a second time as output tokens, the price of a rare case.

## Why diggers never write

Up to four diggers run at once. Any design where they write into the run's one document is a race, and every design that avoids the race by other means costs lead calls or breaks the one-document rule:

| Design | What goes wrong |
| --- | --- |
| Diggers `Write` the document | `Write` replaces the file: the last digger to finish erases the others |
| Diggers `Edit` the document | Read-modify-write: the harness refuses an edit over a file changed since the read, so every collision is a re-read of a growing file and a retry, and the window between read and edit stays open |
| One part file per digger, the lead merges | The lead either re-emits every finding as output tokens or reads each part file, one more lead call per round |
| One part file per digger, a shell `cat` merges | `Bash` on an agent that reads the open web |
| One part file per digger, a stop hook merges | The document's completeness would hang on a process that runs after the lead has returned and can no longer report on it |
| Part files kept beside the document | Two artifacts per run |
| The digger returns a path, the lead reads the file | The same tokens enter the lead's context, plus one call per round |

The findings have to reach the lead's context whatever happens, because the lead steers and synthesizes from them. A digger's return already does that at no extra call. So the return is the only channel, and the document has one writer.

## The lead-call budget

A lead call is one model invocation of the lead; each re-sends the lead's whole context, so the run's cost is calls × context. With `r` dig rounds of `d` diggers:

| Phase | Lead calls | Note |
| --- | --- | --- |
| OPEN | 2 | The harvester lookups ride in the search message |
| PLAN | 0 | Reasoning only, inside the call that sends round 1 |
| Each dig round | `d + 1` | One spawn message, one wait line, one note per return; the last return's call sends the next step |
| VERIFY | 1 | Every check in one message |
| SAVE, RETURN | 2 | The Write, then the final message |

A run costs `r(d + 1) + 5` lead calls: 15 for two rounds of four, 20 at the ceiling of three; a round of fewer diggers costs fewer, so three rounds of 4, 3 and 2 diggers cost 17. Every digger costs the lead one call when it returns, which is why width is bought inside a digger (more sub-queries per batch) and never as more diggers.

What enters the lead's context is held flat: a finding keeps its 2-4 sentence size with quotes in place of paraphrase, a verification answer is a YES or NO with one sentence (8 pages came to about 1K tokens), and `Coverage` and `Verification` add a few hundred output tokens a run.

## The plan

The frontier of a run that starts from its opening pages holds only what those pages happen to mention. The plan closes that: before the first dig, the lead names every part the query needs, including the ones no opening page mentioned, from what it knows and what the opening pages showed.

- 3-7 sub-areas, each with a `settled when` line: the evidence that must exist for the sub-area to count as answered.
- Round 1's frontier is every sub-area of the plan plus the opening pages' rabbit holes, so the first round is the wide one.
- The plan can grow: a rabbit hole that reveals a sub-area the plan missed adds it, and `Coverage` marks it as added.
- The rabbit-hole footer on every content fetch carries the goal in one line, so the model that reads the page proposes rabbit holes for this run and not for the page in general.

## Findings are quote-anchored

A fact passes through three writers on its way to the document: the model that reads the page for WebFetch, the digger, and the lead. Paraphrase at each step is where figures drift, and the first step loses the most.

- Every content fetch asks for the exact sentence behind each figure and date.
- A finding is 2-4 sentences with inline source links. Every figure and date, and the claim the finding turns on, sits inside a verbatim quote from the page that states it. One the digger could not quote is marked `unquoted`.
- The lead copies a figure from its quote and never re-derives it.
- The 2-4 sentence bound on a finding stays, so a quote replaces a paraphrase and adds no length.

## Verification

After the last dig round the lead picks the facts the answer rests on, on at most 8 source pages, `unquoted` ones first, and sends one message of WebFetch checks: one call per source page, carrying that page's facts as a numbered list, each asked as a closed question that must be answered YES with the sentence quoted, or NO.

| Verdict | Condition | Consequence |
| --- | --- | --- |
| `confirmed` | YES, and the quoted sentence carries the fact's figure or wording | The fact stays |
| `NOT ON PAGE` | NO, or a YES whose sentence does not carry it | The fact leaves the map and the answer; it is never reworded to fit |
| `UNCHECKED — {error}` | The fetch failed | The fact stays, marked |

The judge is neither the digger that reported the fact nor the lead that will write it: it is a separate model reading the page with one closed question and no view of the run. The lead's part is mechanical, matching a returned sentence against a figure. The cap counts pages because the cost is one fetch result per page; the facts a page carries ride free.

## The frontier and the stop rule

- A rabbit hole is kept when it serves an unsettled sub-area; when unsure whether it serves one, it does. One that serves no sub-area is listed open in the document and is not dug.
- The frontier is grouped by sub-area and each group goes to one digger, so overlap between parallel diggers is removed by construction. Parallel diggers share no state, and a shared visited-URL list would be a shared file (the race above) or URLs relayed through the lead (output tokens in every brief). A page fetched twice costs one digger fetch; the harm that matters is closed at aggregation.
- Support is counted by independent source. Pages repeating one origin (a press release, a paper, each other) are one source, so no page counts twice toward corroboration.
- Digging ends when every sub-area is settled, when a round settled nothing and added no sub-area, or at the end of round 3. New relevant documents stop arriving after a few iterations, so depth past that point buys re-reads; the ceiling is a safety stop, never a target.

## Marks

One vocabulary across the digger's return, the lead's messages and the document, so a mark is matched and never interpreted.

| Mark | Written by | Means |
| --- | --- | --- |
| `SEARCH FAILED — {error}` | lead | The opening search errored |
| `NOTHING FOUND — {queries tried}` | lead, digger | The searches ran and answered nothing |
| `DIG FAILED — {error}` | digger | The sub-query's search errored or every fetch failed |
| `unquoted` | digger, lead | A fact with no verbatim quote behind it, and nothing else: a quoted fact the verification did not sample carries no mark |
| `DISPUTED` | digger, lead | Sources disagree; both sides are given with their quotes, unresolved |
| `unverified` | lead | A claim with no fetched source behind it |
| `confirmed`, `NOT ON PAGE`, `UNCHECKED — {error}` | lead | The verification verdicts |
| `NOT SAVED — {reason}` | lead | The document was not written; the synthesis travels inline |

An error and an absence never share a mark: `SEARCH FAILED` against `NOTHING FOUND`, `DIG FAILED` against `NOTHING FOUND`, `UNCHECKED` against `NOT ON PAGE`.

## Not part of the design

| Left out | Reason |
| --- | --- |
| Verification by grep over the harvester cache | `fetch` with `size_only` works (12 pages cached for about 1K tokens of result), but `searchCache` names a match by `harvest:` handle and `md_path`, never by URL, so a match cannot be tied to the page a fact cites; 7 of 34 probe patterns errored and 14 hit the result cap |
| A verifier digger | Two lead calls (the spawn's wait line and the return) against one for the lead's own check message, plus a digger's run |
| More than 4 diggers a round | Each digger is one more lead call on return; width goes into the batch |
| A fourth dig round | Depth saturates; the three-round ceiling keeps the run under its former cost with verification included |
| A log of findings written round by round | Every finding would be emitted twice, once by the digger and once by the lead |
| A `RUNNING` stub written at the start and replaced at the end | Replacing it is a Write over an existing file: refused without `Read`, even for the lead's own stub, and allowed for every file with it, which removes the name guard. A dead run leaves no document; its plan and findings are in the transcript |
| A human gate on the plan | The lead is a sub-agent and holds no way to ask; the plan is reported in `Coverage` |
| Source-routing modules per domain | The harvester covers the scholarly lane; nothing measured supports more |

## What the evidence says

The rulings rest on the research survey saved in the ledger as `.professor/RR/deep-research-agent-implementations-2026-09-20.md`. Each source below was fetched again and its sentence found in the page text; a figure that could not be matched that way is not cited here.

- Briefs are self-contained and bounded: "Each subagent needs an objective, an output format" and clear task boundaries ([Anthropic, multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system)). The same post attributes most of the performance variance to tokens spent, which is why the budget counts lead calls.
- Stop on convergence, and default to fewer agents: "Your last 2 searches returned similar information" and "Bias towards single agent" ([open_deep_research prompts](https://raw.githubusercontent.com/langchain-ai/open_deep_research/main/src/open_deep_research/prompts.py)).
- Relay faithfully and the loss stays at the first writer: "under faithful-relay instructions a strong relay is nearly lossless", with the residual loss at the first encoding ([message-format effects in multi-hop relays](https://arxiv.org/html/2607.09678v1)). Quote-anchoring is that instruction.
- Count support by source: "Evidence nodes are deduplicated at the source-URL level, preventing any single page from inflating the support count of a claim" ([Argus](https://arxiv.org/html/2605.16217v1)).
- Depth saturates: "over time, agents retrieve fewer new documents (both relevant and irrelevant) and increasingly revisit previously seen ones" ([RAAC](https://arxiv.org/html/2608.15191v1)).
- Remove overlap by construction: "Coverage is guaranteed by construction: the deterministic pass produces a finite work queue, every shard is assigned to an investigation agent" ([Devin, agentic MapReduce](https://devin.ai/blog/agentic-map-reduce)).
- Citations need an independent check: deep research agents produce cited reports, "yet these citations cannot be reliably verified" ([Cited but Not Verified](https://arxiv.org/abs/2605.06635)); the reliability of citation URLs "has not been systematically measured" ([urlhealth](https://arxiv.org/html/2604.03173v1)).

## Measuring a run

`/tokens --filter rr` lists each lead and digger with its tokens and tool uses. Three numbers say whether a run held the budget: the lead's calls against `r(d + 1) + 5`, the rounds used against 3, and the `NOT ON PAGE` count in the document's `Verification` section. A `NOT ON PAGE` rate that stays above zero across runs means findings are still being paraphrased, and the fix is in the digger's fetch prompt, not in a larger verification sample.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agents | `templates/global/agents/rr.md`, `sub-rr.md`, `variants.json` | The two protocols; the `super-rr` override |
| The rendered variant | pfm's generated directory, linked into `~/.claude/agents/` and `~/.codex/agents/` | `super-rr`, re-rendered by `pfm codex agents` after every edit of `rr.md` |
| The hook | `pfm internal rr-dir`, wired by `pfm/internal/installer/expected_hooks.go` | The `RR-DIR:` line; its matcher names every agent rendered from `rr.md` |
| The roster line | `docs/BLUEPRINT.md` | The family's members and the variant mechanism |
| This directory | `docs/design/RR/` | This file |

## Open items

- `searchCache` reporting the source URL of each match, as its description already says it does, and taking a source filter. With both, the first pass of VERIFY becomes a grep over pages cached by `fetch` with `size_only`, and WebFetch stays as the second look.
- The scout footer in `workflows/deep-rr/engine/src/agents/scout/prompts.ts` is goal-blind the way this family's footer was. It has its own engine and snapshot tests, so it is its own pass.
- The return repeats the synthesis the document already holds, the lead's largest single output. A return of path, answer and open rabbit holes trades that for one Read by a caller that wants the map. Undecided.
- A document name made unique by a mechanism: the `rr-dir` hook handing the lead a run token for the file name. A name no other run can hold makes an early stub safe to replace, which would put a dead run's plan on disk.
- The three numbers under [Measuring a run](#measuring-a-run) have two measured runs behind them: 13 lead calls against a budget of 13 for rounds of 4 and 2 diggers, and one `NOT ON PAGE` in 25 facts on the other run. The next ten runs set the baseline.

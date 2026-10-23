# rr

`rr` maps the knowledge area one query asks about. A lead opens the query, plans its sub-areas by subject with a field's lens on each, sends diggers down the rabbit holes that serve the plan, checks the facts its answer rests on against their pages, and saves a run directory: its cited synthesis and every digger's full report. This file holds the family's shared decisions and the `rr` lead's; each other member's own decisions live in its file beside this one.

A change lands in this file first, then in the templates, then in every surface listed under [Surfaces that stay in sync](#surfaces-that-stay-in-sync).

## Contents

- [The family](#the-family)
- [The run](#the-run)
- [The document](#the-document)
- [Why each digger writes its own report](#why-each-digger-writes-its-own-report)
- [The lead-call budget](#the-lead-call-budget)
- [The plan](#the-plan)
- [The lens](#the-lens)
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

| Member | Kind | Does | Runs at | Design |
| --- | --- | --- | --- | --- |
| `rr` | agent | The lead: opens, plans, aggregates, dispatches, verifies, synthesizes, saves; the only writer of the run's synthesis, and it holds no `Read` | smart (`opus`), effort `low` | this file |
| `rr-pro` | variant of `rr` | The same body at higher effort with wider caps and the repository lane set in `rr.md`'s § Budget and § Spawning by `rr-pro.md` and its shared `shared/repo-lane.md` section overrides, rendered by `pfm install` | `opus`, effort `medium` | [rr-pro.md](rr-pro.md) |
| `rr-pro-max` | variant of `rr` | As `rr-pro`, 8 diggers a round, no round ceiling, and `Read` added to its tools | `opus`, effort `medium` | [rr-pro-max.md](rr-pro-max.md) |
| `sub-rr` | agent | The digger: answers a numbered batch of sub-queries, writes its full report into the run directory, returns the report's path, findings and rabbit holes | collector (`haiku`), effort `medium` | [sub-rr.md](sub-rr.md) |
| `clone-distiller` | agent | The general repository digger, the code lane of `rr-pro` and `rr-pro-max`: clones a public repository, answers a numbered batch of sub-queries from its code, writes a result file of absolute `path:line` evidence | smart (`opus`), effort `medium` | [clone-distiller.md](../clone-distiller/clone-distiller.md) |
| `rr-dir` | `SubagentStart` hook | Puts the `RR-DIR:` line (the ledger directory) into the lead's context; matcher `rr\|rr-pro\|rr-pro-max\|gpt-rr\|gpt-rr-pro\|gpt-rr-pro-max` | `pfm internal rr-dir` | this file |

The leads differ in their caps and in the repository lane, which sends a sub-area whose answer lives in a repository's code to `clone-distiller` ([clone-distiller.md](../clone-distiller/clone-distiller.md), § Who spawns it); every other line of the body is `rr.md`'s:

| Lead | Diggers a round | Round ceiling | Verification pages | Repository lane |
| --- | --- | --- | --- | --- |
| `rr` | 4 | 3 | 8 | no |
| `rr-pro` | 6 | 5 | 12 | yes |
| `rr-pro-max` | 8 | none: the convergence stops alone end the run | 16 | yes |

Vocabulary, one term per concept: a rabbit hole is a link, gap or missing piece a source raises and leaves unexplained; a sub-area is one part of the knowledge area, named in the plan; a finding is a digger's answer to one sub-query; a fact is a load-bearing statement in the map; a quote is the verbatim sentence that states a fact.

## The run

1. OPEN. One message carries the WebSearch and, for a scholarly question, the harvester lookups; the next message fetches the 1-3 best starting pages. An opening read that shows the lead no page text is marked `UNREAD` and its question goes to a round-1 digger, never into the map as an absence. A failed or empty search ends the run with a one-line report.
2. PLAN. The lead restates the query as verbs, names the fields that do that work, and splits the knowledge area by subject into 3-7 sub-areas, each with a plan line, `{sub-area} — settled when {evidence}`, and a `Lens:` line under it (§ The lens); both open every brief that owns the sub-area. The step ends with the Write that claims the run directory (§ The document).
3. AGGREGATE. Findings and rabbit holes are pooled, each sub-area is judged against its plan line (`settled` names the evidence that meets it; a digger's unsettled question or unverified inference on a part the line names makes it `partial`), and the frontier is rebuilt from the rabbit holes that serve an unsettled sub-area.
4. DIG. One digger per unsettled sub-area, each carrying that sub-area alone under its lens, up to the lead's cap a round (§ The family); the smallest groups merge only when they outnumber the cap, so no two diggers own the same ground, every digger of the round in one message, which opens with its count line, `Round {n}: {u} unsettled sub-areas → {d} diggers (cap {c})`, `d` = min(u, c), so a merge with a seat free shows as arithmetic on the record. Every `sub-rr` brief ends with `Report: {run dir}/sub-rr/{nn}-{group-slug}.md`, `nn` counting every digger of the run in spawn order, or `Report: none — {reason}` when the run has no directory. The lead waits by ending its message.
5. LOOP. Steps 3-4 repeat until the stop rule fires. Each round closes with a status block — one `{sub-area}: settled | partial | open — {why}` line each, then `Next: round {n}` or `Digging ended: {condition}` — so the stop decision is on the record.
6. VERIFY. One message of WebFetch checks over the facts the answer rests on.
7. SYNTHESIZE. Answer, map, coverage (each sub-area with its lens's field and its report files relative to the run directory), verification, open rabbit holes. A report is never a source: the map cites web URLs and document identifiers only.
8. SAVE, then RETURN. One Write of `{run dir}/{slug}.md`, a new file holding the finished synthesis; the return is the saved path, then the answer, `Coverage`, `Verification`, the open rabbit holes and a `Reports` line copied from it, plain text with no bullets, bold or headings. The map stays in the file, the findings' detail in the reports.

## The document

One run produces one run directory, `{RR dir}/{slug}-{YYYY-MM-DD}/`:

```text
{run dir}/plan.md                         the lead's plan, the run's claim
{run dir}/{slug}.md                       the lead's synthesis
{run dir}/sub-rr/{nn}-{group-slug}.md     each digger's full report
{run dir}/clone-distiller/{file}          each repository digger's result file (rr-pro, rr-pro-max)
```

- One writer per file, written once. The lead writes only `plan.md` and `{slug}.md`; each `sub-rr` writes only the `Report:` path its brief names, once, as its last call; `clone-distiller` gets the run directory as its `OUT-DIR:` and its own code appends `clone-distiller/`. No two agents ever hold one path, so nothing races.
- The claim. At the end of PLAN the lead Writes `plan.md` with the heading, the `Question:` line and the plan lines; that Write claims the directory. At SAVE it Writes `{slug}.md`, a new file, with the heading, the `Question:` line and the finished synthesis; nothing ever rewrites a file. A second Write of one path would be refused: the lead ends its turn while its diggers run and is re-invoked by their notifications, and after that its own earlier Write no longer counts as a read (`File has not been read yet. Read it first before writing to it.`). A run that step 1 ended writes no plan, so its `{slug}.md` Write is the claim. A dead run leaves its plan on disk.
- No `Read`, on purpose. The harness refuses a Write over an existing file the agent has not read (`File has not been read yet. Read it first before writing to it.`), and relaxes that refusal for an agent that holds the `Read` tool: such an agent overwrites an unread file without a word. The lead's missing `Read` is therefore load-bearing: it turns a claim on a taken name into a refusal, and the lead takes `-2`, then `-3`, on the directory name, instead of erasing the first run's synthesis. Adding `Read` to the lead's `tools:` line removes the guard. It would also invite the lead to re-read its diggers' reports, the tokens the reports exist to keep out of its context, and to inherit an earlier map's conclusions instead of deriving its own; for the same reason both prompts admit only web URLs and document identifiers as sources, because Harvester's `read` tool accepts a local path in `files`. `rr-pro-max` alone opts out: its `tools` override adds `Read`, accepting both costs ([rr-pro-max.md](rr-pro-max.md) § Read, and the name guard it gives up).
- The reports are for the caller. The lead never reads one; what it aggregates is each digger's return. A report keeps everything the digger found (each fact with its quote and URL, every page fetched and what it lacked, every failed fetch), so the main chat or whoever called the lead can investigate past the synthesis. `Coverage` names each sub-area's report files and the return's `Reports` line lists them, relative to the run directory, so a tracked synthesis carries no machine path.
- Tracked and local. `plan.md` and `{slug}.md` are tracked; `sub-rr/` and `clone-distiller/` under each run directory are git-ignored, machine-local dig evidence (verbatim third-party text, paths into `/tmp` clones).
- A failed save is loud. With no `RR-DIR:` line, an `RR-DIR-ERROR:` line, or a claim Write that fails for any reason but a taken name, the run has no directory: every brief carries `Report: none — {reason}`, each digger returns its full report inline, and the lead's return opens `NOT SAVED — {reason}` with the synthesis inline. A digger whose own Write fails returns `NOT SAVED — {the Write error}` and its full report inline; the `Reports` line marks it `NOT SAVED — {group}`.

Layout of the finished synthesis:

```text
# RR — {question in one line}
Question: {the query, verbatim}

{answer, one to two sentences of prose, each clause carrying the inline link of the map fact it states; lists belong in the map}
{the map, by sub-area}
Coverage       the plan, each sub-area (its field) settled | partial | open with its report files; Digging ended
Verification   facts checked; every NOT ON PAGE and UNCHECKED by name
{rabbit holes left open}
```

Two runs on one name: the slug is chosen by each lead alone, so a second run on one subject, the same day, can pick a directory already taken. The harness settles it at the claim: the second lead's Write lands on a `plan.md` it has not read, is refused, and that lead takes the suffix `-2`, then `-3`, before any brief names a report path. A run that step 1 ended claims with `{slug}.md` and leaves no `plan.md`, so a later run on its name claims the directory and finds its own `{slug}.md` Write refused at SAVE: that return opens `NOT SAVED` with the synthesis inline, loud, and the first run's line stays intact.

## Why each digger writes its own report

Diggers read far more than they return: on one measured run they read about 423K characters of pages, returned about 28K, and the saved synthesis kept 9.9K. The detail between those numbers is what a caller investigates. Every route for it to reach disk was weighed:

| Design | What goes wrong |
| --- | --- |
| Diggers `Write` the synthesis | `Write` replaces the file: the last digger to finish erases the others |
| Diggers `Edit` the synthesis | Read-modify-write: the harness refuses an edit over a file changed since the read, so every collision is a re-read of a growing file and a retry |
| The lead writes the detail | It re-emits every finding as output tokens, and the detail must first enter its context |
| The digger returns a path, the lead reads the file | The same tokens enter the lead's context, plus one call per round |
| A stop hook merges part files | Completeness would hang on a process that runs after the lead has returned and can no longer report on it |
| Each digger Writes its own file, the lead never reads it | Chosen: one writer per path, so no race; no lead call and no lead tokens; the return still carries what the lead steers from |

The findings reach the lead's context through the return, because the lead steers and synthesizes from them, so the return keeps its 2-4 sentence findings and adds only the report's path. The report sits beside the synthesis for the caller.

## The lead-call budget

A lead call is one model invocation of the lead; each re-sends the lead's whole context, so the run's cost is calls × context. With `r` dig rounds of `d` diggers:

| Phase | Lead calls | Note |
| --- | --- | --- |
| OPEN | 2 | The harvester lookups ride in the search message |
| PLAN | 1 | The claim Write; the next call sends round 1 |
| Each dig round | `d + 1` | One spawn message, one wait line, one note per return; the last return's call sends the next step |
| VERIFY | 1 | Every check in one message |
| SAVE, RETURN | 2 | The synthesis Write, then the final message |

A run costs `r(d + 1) + 6` lead calls: 16 for two rounds of four, 21 at `rr`'s ceiling of three, 41 at `rr-pro`'s ceiling of five rounds of six; `rr-pro-max` has no ceiling, so its cost is bounded only by convergence, at 9 lead calls a round of eight; a round of fewer diggers costs fewer, so three rounds of 4, 3 and 2 diggers cost 18. Every digger costs the lead one call when it returns, which is why width is bought inside a digger (more sub-queries per batch) and never as more diggers.

What enters the lead's context is held flat: a digger's detail goes to its report on disk, a finding keeps its 2-4 sentence size with quotes in place of paraphrase, a verification answer is a YES or NO with one sentence (8 pages came to about 1K tokens), and `Coverage` and `Verification` add a few hundred output tokens a run.

## The plan

The frontier of a run that starts from its opening pages holds only what those pages happen to mention. The plan closes that: before the first dig, the lead names every part the query needs, including the ones no opening page mentioned, from what it knows and what the opening pages showed.

- 3-7 sub-areas, split by subject, each with a plan line, `{sub-area} — settled when {evidence}`: the evidence that must exist for the sub-area to count as answered, to the standard of proof of its lens's field, so one line carries both the subject's evidence and the field's bar.
- The plan lines open every brief that owns them, rather than standing as free text: a lead that reasons its plan without writing it down skipped a text line in trial runs, while a brief is written in every run, so the plan reaches the transcript with no step left to skip, and each status block has the lines on record to judge against. The digger also learns what evidence settles its sub-area.
- Round 1's frontier is every sub-area of the plan plus the opening pages' rabbit holes, so the first round is the wide one.
- The plan can grow: a rabbit hole that reveals a sub-area the plan missed adds it with its plan and `Lens:` lines, and `Coverage` marks it as added.
- The rabbit-hole footer on every content fetch carries the goal in one line, so the model that reads the page proposes rabbit holes for this run and not for the page in general.

## The lens

A query names a task at its surface; the fields whose practitioners settle that kind of question start from different raw material and judge it by different methods. The lens carries one such field into each sub-area's dig, as a second axis inside the brief; the split by subject stays the partition.

- The lead restates the query as verbs (what is being done, decided or proved) and names the fields whose practitioners do that work, past the obvious one. The fields become lenses, never sub-areas: a sub-area per field would put two diggers on one subject, the overlap the split by subject removes by construction, and a theme cutting across sub-areas still becomes a sub-query inside each one it touches.
- Each sub-area gets one lens, written under its plan line: `Lens: {field} — method: {how its practitioners settle this kind of question}; sources: {the raw material they start from}`. The sources are registries, datasets, filings, standards, source code and primary texts, never a digest of them.
- The lenses of one plan differ in kind, so a run reads its question through several methods instead of one method under several names.
- A lens names methods and sources, never a voice. No brief says "act as", "write as" or "you are a {field}": a persona changes the digger's vocabulary and leaves its searches as they were (§ What the evidence says).
- A digger owning several sub-areas (the smallest groups merged past the cap) gets each sub-area's plan line, `Lens:` line and sub-queries as one block, numbered on across the brief, so every sub-query sits under its own lens and the digger matches each one to the nearest `Lens:` line above it, with no judgment left to make.
- The repository lane of `rr-pro` and `rr-pro-max` is its own lens: `clone-distiller`'s method (the code read at a pinned commit) is fixed by the agent, and it holds no web tool to search a lens's sources, so its brief carries no `Lens:` line (`shared/repo-lane.md`).
- `Coverage` names each sub-area's field beside its state, so a reader sees which method settled what.

## Findings are quote-anchored

A fact passes through three writers on its way to the document: the model that reads the page for WebFetch, the digger, and the lead. Paraphrase at each step is where figures drift, and the first step loses the most.

- Every content fetch asks for the exact sentence behind each figure and date.
- A finding is 2-4 sentences with inline source links. Every figure and date, and the claim the finding turns on, sits inside a verbatim quote from the page that states it. One the digger could not quote is marked `unquoted`.
- The lead copies a figure from its quote and never re-derives it.
- The 2-4 sentence bound on a finding stays, so a quote replaces a paraphrase and adds no length.

## Verification

After the last dig round the lead picks the facts the answer rests on, on at most its verification pages (§ The family), `unquoted` ones first, and sends one message of WebFetch checks: one call per source page, carrying that page's facts as a numbered list, each asked as a closed question that must be answered YES with the sentence quoted, or NO.

| Verdict | Condition | Consequence |
| --- | --- | --- |
| `confirmed` | A quoted sentence carries the fact's figure or wording, whatever the YES or NO beside it | The fact stays |
| `NOT ON PAGE` | No quoted sentence carries it | The fact leaves the map and the answer; it is never reworded to fit, nor moved to a page no check quoted it from |
| `UNCHECKED — {error}` | The fetch failed | The fact stays, marked |

The judge is neither the digger that reported the fact nor the lead that will write it: it is a separate model reading the page with one closed question and no view of the run. The lead's part is mechanical, matching a returned sentence against a figure; the YES or NO is not the verdict, because a checker can answer a wider question than the one asked (NO to "does it hold across every dataset" while quoting the sentence that states the figure), and its commentary never enters the map. The pages behind `unquoted` facts are checked first; a quoted fact is re-checked only when pages remain. The cap counts pages because the cost is one fetch result per page; the facts a page carries ride free.

## The frontier and the stop rule

- A rabbit hole is kept when it serves an unsettled sub-area; when unsure whether it serves one, it does. One that serves no sub-area is listed open in the document and is not dug.
- The frontier is grouped by sub-area and each group goes to one digger, so overlap between parallel diggers is removed by construction. Parallel diggers share no state, and a shared visited-URL list would be a shared file (the race above) or URLs relayed through the lead (output tokens in every brief). A page fetched twice costs one digger fetch; the harm that matters is closed at aggregation.
- Support is counted by independent source. Pages repeating one origin (a press release, a paper, each other) are one source, so no page counts twice toward corroboration.
- Digging ends when every sub-area is settled, when a round settled nothing and added no sub-area, or at the lead's round ceiling (§ The family); `rr-pro-max` has none. `Digging ended:` names the one that fired in its fixed wording — `every sub-area settled`, `a round settled nothing and added no sub-area`, `round ceiling reached` — any comment after it. The lead's own judgment that enough is known, that what remains needs a hands-on test or that no web source has it is no stop condition: it is the why on that sub-area's `partial` line, and with rounds left the next line is `Next: round {n}`. Observed leads ended digging on that judgment with rounds left; three closed conditions leave the lead nothing to decide. New relevant documents stop arriving after a few iterations, so depth past that point buys re-reads; the ceiling is a safety stop, never a target.

## Marks

One vocabulary across the digger's return, the lead's messages and the document, so a mark is matched and never interpreted.

| Mark | Written by | Means |
| --- | --- | --- |
| `SEARCH FAILED — {error}` | lead | The opening search errored |
| `NOTHING FOUND — {queries tried}` | lead, digger, collector | The searches ran and answered nothing |
| `DIG FAILED — {error}` | digger | The sub-query's search errored or every fetch failed |
| `CLONE FAILED — {error}` | repository digger | The repository could not be cloned; the return is this line alone |
| `clone-only` | repository digger, lead | A code fact on a host with no commit-pinned link form, cited by its absolute path in the result file |
| `FETCH FAILED — {error}` | collector | An order's source could not be read |
| `PARTIAL READ — {part read}` | collector | The source was cut short before the item could be ruled out |
| `unquoted` | digger, lead | A fact with no verbatim quote behind it, and nothing else: a quoted fact the verification did not sample carries no mark |
| `DISPUTED` | digger, lead | Sources disagree; both sides are given with their quotes, unresolved |
| `unverified` | lead | A claim with no fetched source behind it |
| `UNREAD — {url}: {why}` | lead | An opening read showed no page text (a result saved to a file the lead cannot open, an error, a fetch model reporting no match on a long page); its question goes to a round-1 digger |
| `confirmed`, `NOT ON PAGE`, `UNCHECKED — {error}` | lead | The verification verdicts; `NOT ON PAGE` is also the collector's mark for a source read that does not carry the item |
| `NOT SAVED — {reason}` | lead, digger | The synthesis (lead) or the report (digger) was not written; its content travels inline |

An error and an absence never share a mark: `SEARCH FAILED` against `NOTHING FOUND`, `DIG FAILED`, `CLONE FAILED` and `FETCH FAILED` against `NOTHING FOUND`, `UNCHECKED`, `UNREAD` and `PARTIAL READ` against `NOT ON PAGE`.

## Not part of the design

| Left out | Reason |
| --- | --- |
| A verifier digger | Two lead calls (the spawn's wait line and the return) against one for the lead's own check message, plus a digger's run |
| More than 4 diggers a round for `rr` | Each digger is one more lead call on return; width goes into the batch. `rr-pro` and `rr-pro-max` buy width at that price by design |
| A fourth dig round for `rr` | Depth saturates; the three-round ceiling keeps the run under its former cost with verification included. `rr-pro` and `rr-pro-max` pay for depth by design |
| A log of findings written round by round | Every finding would be emitted twice, once by the digger and once by the lead |
| A human gate on the plan | The lead is a sub-agent and holds no way to ask; the plan is reported in `Coverage` |
| A sub-area per field | Two diggers on one subject; the field rides as a lens inside the brief instead (§ The lens) |
| A persona in the brief ("act as", "you are a {field}") | It changes the digger's vocabulary, not its searches or its sources |
| Source-routing modules per domain | The harvester covers the scholarly lane and `clone-distiller` the code lane of the deeper leads; nothing measured supports more |

## What the evidence says

The rulings rest on the research survey saved in the ledger as `.professor/RR/deep-research-agent-implementations-2026-09-20.md`, and the lens on Felix Rieseberg's essay. Each source below was fetched and its sentence found in the page text; a figure that could not be matched that way is not cited here.

- Briefs are self-contained and bounded: "Each subagent needs an objective, an output format" and clear task boundaries ([Anthropic, multi-agent research system](https://www.anthropic.com/engineering/multi-agent-research-system)). The same post attributes most of the performance variance to tokens spent, which is why the budget counts lead calls.
- Stop on convergence, and default to fewer agents: "Your last 2 searches returned similar information" and "Bias towards single agent" ([open_deep_research prompts](https://raw.githubusercontent.com/langchain-ai/open_deep_research/main/src/open_deep_research/prompts.py)).
- Relay faithfully and the loss stays at the first writer: "under faithful-relay instructions a strong relay is nearly lossless", with the residual loss at the first encoding ([message-format effects in multi-hop relays](https://arxiv.org/html/2607.09678v1)). Quote-anchoring is that instruction.
- Count support by source: "Evidence nodes are deduplicated at the source-URL level, preventing any single page from inflating the support count of a claim" ([Argus](https://arxiv.org/html/2605.16217v1)).
- Depth saturates: "over time, agents retrieve fewer new documents (both relevant and irrelevant) and increasingly revisit previously seen ones" ([RAAC](https://arxiv.org/html/2608.15191v1)).
- Remove overlap by construction: "Coverage is guaranteed by construction: the deterministic pass produces a finite work queue, every shard is assigned to an investigation agent" ([Devin, agentic MapReduce](https://devin.ai/blog/agentic-map-reduce)).
- Citations need an independent check: deep research agents produce cited reports, "yet these citations cannot be reliably verified" ([Cited but Not Verified](https://arxiv.org/abs/2605.06635)); the reliability of citation URLs "has not been systematically measured" ([urlhealth](https://arxiv.org/html/2604.03173v1)).
- Read a question through several fields, found from its verbs: Range is "Drawing on multiple fields rather than just the one the task might need at the surface, using knowledge, tools, and techniques you normally wouldn't.", and "Having the verbs makes it easier for both me and Claude to understand which fields might bring in a unique and useful perspective." ([Rieseberg, How to be more ambitious](https://felixrieseberg.com/how-to-be-more-ambitious-a-tutorial-for-humans-with-models/)). That is the verbs step and the lens.
- Start from raw material and the field's methods: the essay's depth prompt asks to "name the rawest material they would start from instead of what has already been digested for me; (3) name the specific methods and tools they would use" (same essay). That is the lens's `method:` and `sources:`, and the source-class bracket on each finding.
- Methods, never a voice: "When prompted to act like a professional, models often switch vocabulary without actually switching methods.", and "It is easy to accidentally engage in role play, which will make your transcript weirder without improving the actual outcome." (same essay).

## Measuring a run

`pfm tokens --family {the chat's session-id prefix}` (`--since` widening its 24 h window to the run's start) prices the chat's agents in a ranked report. For the lead's calls one by one, run `python3 -I "$HOME/.claude/skills/chatlog/chatlog.py" prep "{the lead's transcript path}"` and read `timeline.txt` under the printed `DIR`: each model call has its clock, wait since the last tool result, context, output, price and tool uses. Three numbers say whether a run held the budget: the lead's calls against `r(d + 1) + 6`, the rounds used against the lead's round ceiling, and the `NOT ON PAGE` count in the document's `Verification` section. A `NOT ON PAGE` rate that stays above zero across runs means findings are still being paraphrased, and the fix is in the digger's fetch prompt, not in a larger verification sample. A fourth says whether the lens pays: the share of findings whose bracket names a `sources:` item rather than `other:`.

## Surfaces that stay in sync

| Surface | File | Holds |
| --- | --- | --- |
| The agents | `templates/global/agents/rr.md`, `sub-rr.md`, `clone-distiller.md`, `rr-pro.md`, `rr-pro-max.md`, `shared/repo-lane.md` | The three protocols, the lens rules in `rr.md` step 2 and step 4, and the digger's side in `sub-rr.md`; the `rr-pro` and `rr-pro-max` overrides of `rr.md`'s § Budget and § Spawning, each hash-pinned, and their repository lane, the shared file `shared/repo-lane.md` |
| The rendered variants | `templates/generated/agents/` (tracked), linked into `~/.claude/agents/` and `~/.codex/agents/` | `rr-pro`, `rr-pro-max`, `gpt-rr`, `gpt-rr-pro` and `gpt-rr-pro-max`, re-rendered by `pfm install` (`engine-sync global`) after every edit of `rr.md` or `shared/repo-lane.md`; the fence step `templates.generated-agents` fails when the tracked files differ from a fresh render |
| The hook | `pfm internal rr-dir`, registered by `HookTemplates` in `pfm/internal/claudelaunch/hooks.go` (matcher `HookRRDirMatcher`) | The `RR-DIR:` line; its matcher names every agent rendered from `rr.md` |
| The ignore lines | `.gitignore` | `sub-rr/` and `clone-distiller/` under each run directory stay machine-local |
| This directory | `docs/design/RR/` | This file and one file per other member |

## Open items

- A run directory made unique by a mechanism for `rr-pro-max`, whose `Read` lifts the claim's refusal: the `rr-dir` hook handing the lead a run token for the directory name.
- The lens has no measured run: the next runs set the source-class share under [Measuring a run](#measuring-a-run).
- The three numbers under [Measuring a run](#measuring-a-run) have two measured runs behind them: 13 lead calls against a budget of 13 for rounds of 4 and 2 diggers, counted before the claim Write, and one `NOT ON PAGE` in 25 facts on the other run. The next ten runs set the baseline.

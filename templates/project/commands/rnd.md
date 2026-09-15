---
name: rnd
description: 'Runs research on {AI_SERVICE_NAME} LLM calls — under .professor/RND/<call>/<N>-<slug>/, one `general-purpose` spawn per run (inline when small); `new <call> <slug> <goal>` (or a bare `<goal>`), `continue <call> [<goal>]`, `verify <call>/<run>`, `land <call>/<run>` (user-ratified only). Triggers "RND <goal>", "research and develop", "iterate until", "find the best prompt for".'
argument-hint: "[new <call> <slug> <goal> | continue <call> [<goal>] | verify <call>/<run> | land <call>/<run> | <goal>]"
---

# RND — lifecycle

Request: $ARGUMENTS

An RND takes a measurable goal against ONE {AI_SERVICE_NAME} call and reaches it by rounds: arms → real calls → sealed judging → verdict. This command owns the lifecycle; each run executes in one `general-purpose` spawn briefed from its `BRIEF.md` (§ Run protocol), or inline in this session when the run is small — one arm, one input class, under an hour of calls. § Layout is the layout law; read it before acting.

## Boundaries (inviolable)

- Sandbox only: every artifact lives in the run dir. An RND never edits a project file; its deliverable is `PROPOSED_DIFF.md`, landed by hand or via `/wave:builder` only after the user ratifies the completed result. An authorization to research ("RND this", "fix it via RND") never authorizes landing.
- Independent of {AI_SERVICE_NAME}: the run imports or points at nothing under the {AI_SERVICE_NAME} source tree (`{PROJECT}/src/{ai_module}/**`); production enters twice, as a black box — the baseline (invoked whole) and the final in-process monkey-patch validation of `PROPOSED_DIFF.md`.
- Sensitive-data discipline: reports, ledgers, logs, agent messages carry ids, counts, enums — never transcript text. Corpus transcripts are synthetic seeds; the rule holds anyway. {SECONDARY_LANG} evidence surfaced to the user is translated.
- Money: hard caps for the model under test and for judges, frozen in `STATE.md` before the first paid call; provider keys come from the environment.
- Git: no RND agent runs a git write; gitter commits run dirs at milestones on the Professor's dispatch.

## Layout

- `.professor/RND/<call>/` — one dir per {AI_SERVICE_NAME} call, named as the {AI_SERVICE_NAME} module names it (the module directories under `src/{ai_module}/**` that own a prompt — one RND directory per such call); `_cross/` holds experiments spanning several calls.
- `<call>/<N>-<slug>/` — one numbered run per experiment, chronological. A new run COPIES the previous run's whole harness (`lab new <call> <slug>`), then changes only what the experiment changes.
- `<call>/WINNER.md` — the winning run + artefact + status: shipped · awaits the user's ratification · none.
- `<call>/_corpus/` — another call's output this call consumes, copied in with `SOURCE.md` (path, date, md5). Plain transcriptions live in the shared `.professor/RND/_corpus/` and are referenced, never copied.
- Traps, results, judge verdicts, `REPORT.md` / `STATE.md` / `PROPOSED_DIFF.md` live inside the run.
- `lab` is the only dependency: `.professor/RND/_tools/lab new|run|judge|stats|report|selftest` (package `.professor/RND/_lab`, contract `SPEC.md`).

## Modes

Dispatch by the first word of `$ARGUMENTS`.

### `new <call> <slug> <goal>` — and a bare `<goal>`

1. Resolve the call: the {AI_SERVICE_NAME} module dir under `{PROJECT}/src/{ai_module}/**` that owns the prompt under study. A bare goal names its call by inference; ambiguity → one question.
2. `lab new <call> <slug>` creates the run dir (the copy's `COPIED_FROM.md` names its source).
3. Write `BRIEF.md` in the run dir: goal verbatim · call + prompt anchors · the cited defect and the input that reproduces it · bar (metric, threshold, denominator, minimum class size, who ruled it) · arms (≤5, most promising first, each with hypothesis, prediction and what falsifies it) · corpus (which `_corpus` sessions and traps; another call's output → `<call>/_corpus/`; the whole-session stress inputs marked) · draws (5 per prompt × transcript, parallel, drawn once) and concurrency · budgets · judge engines (lab defaults unless ruled) · gates G1..Gn, each with the exact number that passes · the stop rule · deliverables (§ `verify`) · every standing ruling that binds this run, quoted.
4. Execute the run — a `general-purpose` spawn (Opus, xhigh) whose brief carries the five briefing fields, each read from `BRIEF.md`: the goal and the artifacts it returns (`REPORT.md` + `PROPOSED_DIFF.md` + `STATE.md` in the run dir); the boundary (§ Boundaries verbatim — sandbox only, no project-file edits, no git writes, ids/counts/enums only); the anchors (the run dir and `BRIEF.md` as absolute paths, `<call>/WINNER.md`, the previous run's `REPORT.md`/`STATE.md` when `COPIED_FROM.md` names one); the budgets as hard caps; and its failure shape (a cap reached, an unreproducible baseline, an instrument that will not fire → stop, hold, report the gate that blocked — never route around it). Every standing ruling that binds the run is quoted in the brief, never summarized. One run per spawn; another round is another spawn on a new N — never a duplicate of a live one (check the roster first). A small run executes inline under the same § Run protocol.

### `continue <call> [<goal>]`

Read `<call>/WINNER.md` and the latest run's `REPORT.md` residuals (R-items); the next run's goal is the residual the user chose, or `<goal>`. Then `new` on the next N.

### `verify <call>/<run>`

Performed on disk, never asserted:

- Deliverables present: `BRIEF.md`, `STATE.md` (gates frozen before the first ledger timestamp), `REPORT.md`, `PROPOSED_DIFF.md` or an explicit no-diff section, `results/ledger.jsonl`, `judge_receipts.jsonl`, `stats.json`, `report.md`, `test_*.py` for harness code with a logged run (`tmp/<run>.log`, rc captured — the run is re-executed here).
- Instrument before science: recompute one gate row from the ledger; confirm every judge call carried the whole transcript and the Reader ran before the Adjudicator (receipts); confirm no denominator was thinned; confirm every arm's calls are receipted and within budget.
- Verdict rule: PROVEN only when every frozen gate passes; otherwise Best Effort. Residuals by signature (input id, exact fault, rate) as R-items.
- Report to the user in plain words: what the call does, what was measured, one line per gate with the number and the bar, cost, residuals, what needs the user's ruling. Every label (G1, t4, R2) is defined where it appears.
- Update `<call>/WINNER.md` — status "awaits the user's ratification". Nothing lands.

### `land <call>/<run>`

Only on the user's explicit ratification of that run's `PROPOSED_DIFF.md` in the current turn. Land the change by hand or route it to `/wave:builder` (cross-project) with the monkey-patch validation evidence and the report's numbers; after merge, `WINNER.md` status → shipped, with the SHA.

## Run protocol (binds every run, spawned or inline)

0. Instrument, zero network: before the first paid call prove the sandbox render is byte-identical to production's on every corpus item; the ledger and receipt paths round-trip (an unparseable cost prints `UNKNOWN`, never $0); every detector, scorer and key fires and does not over-fire on hand-read specimens; the trap key is audited against its own files; the model string is pinned and the harness exits on mismatch. The brief's claims about the corpus are hypotheses — verify them here.
1. Freeze `STATE.md` § Gates before the first paid call — gates with passing numbers, arms with one falsifiable prediction each, draws and concurrency, corpus md5, production's call spec verbatim, budgets, judge engines, the stop rule (gate pass · three consecutive non-improving arms · the cap). `STATE.md` is the resume file: append every round and every mid-run constraint change verbatim; never rewrite the frozen section.
2. Baseline (gate 0): the shipped prompt runs as arm `prod`, byte-identical, copied into the run with its md5; reproduce the brief's cited defect on it before measuring any fix.
3. Arms move one lever each, every untouched section byte-identical; the prompt is the first cure — a mechanical guard or schema change is an arm only after a prompt arm is measured and loses. A red gate sends you to the scorer first: fix a broken detector once and rescore every arm from stored outputs before drawing a comparison. A dead-end arm is recorded with its numbers and reason so it is never re-derived.
4. Deliver in the run dir per § `verify`; the final message carries goal · winner · one line per gate · cost · residuals · the numbered rulings queue · every deliverable's path — no transcript text. Hand up, never decide: {DOMAIN_ADJ} taxonomy, definition widening, the user's ruled wording, spend beyond the cap, any change to what the model receives that the brief did not name.
5. Long work runs detached through the harness's own mechanism with an explicit timeout and a monitor; every parallel worker gets uniquely named output and log files; liveness is judged from the process table and disk artifacts, never from a monitor's timeout message or a sub-agent's recap.

## Judging law (binds every run)

- Two sealed headless judges, run in parallel across inputs. The Reader codes the transcript first and independently — it never sees the results; its coding is the truth, cached by hash of prompt × transcript × engine. The Adjudicator rules a blind docket — items where the truth and the K draws disagree plus an audit sample — with the whole transcript in front of it. Windows (±k lines, segments or turns) are banned. Engines default to lab's measured pair (Reader opus/low → Adjudicator sonnet/low) unless the brief rules otherwise; both phases send the identical system prompt so the cache pairs.
- The docket holds items where the draws' majority differs from the Reader's key or the draws split, plus the audit sample; every item and every verdict carries the paths to its draws, diff, key and transcript, so any step can be opened. A one-line focus instruction may reach the Adjudicator only; the Reader stays pure.
- A discrimination control (planted decoys the judge must reject) precedes every verdict; preference comparisons carry an A-vs-A panel.
- Every κ is model–model reliability (the project's κ-measurement law, where one exists) and is labelled so; human–human κ exists only when the user supplies a human key. Judge-vs-judge agreement is reported as the ceiling, never as validity.
- Unanimous items outside the blind audit sample (≥10 % or 30, whichever is larger) count as right; the report states that as an ASSUMPTION.

## Depth mandate (binds every run)

Whole sessions, never excerpts; adversarial inputs by design (traps inside the run with a sealed key, the key audited before it gates); the real model at production config, pinned and asserted per call; 5 draws per prompt × transcript in parallel, drawn once and read thereafter (3 on whole-session stress inputs); live data between dependent arms; the artefact meant to ship validated in-process. A step too expensive to run is reported as omitted with confidence lowered, never substituted.

## Prompt RNDs

Build the fix from ranked failures: collect every failure across runs, rank by frequency, cluster by shared confusion, add the fewest contrastive ✗→✓ examples that resolve the cluster (with a counterweight so the label keeps its legitimate uses), confirm on a held-out session. Every prompt under test is a copy inside the run; the shipped prompt is quoted with its md5, never referenced.

## Loop discipline

Execute every arm or remove it with a stated reason; check in with the user before a 6th arm; the goal stays fixed while arms evolve — a wrong goal is surfaced, never silently reframed.

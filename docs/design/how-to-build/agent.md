# how-to-build-agent

`/how-to-build-agent` is the fleet's law for an agent definition: what the file carries, and the method that grows it on a fixed bench, run by run. Its modes are `build <name>`, `improve <agent>` and `check [path…]`. This file holds why each law and each method step exists and the evidence behind it: a commit, a bench ledger or a design doc in this repository. The loop's mechanics belong to `/how-to-build-iterative-improvement` and the design push to `/ultimate`; the skill names them and restates neither.

## Contents

- [Parts](#parts)
- [The pattern](#the-pattern)
- [How tracer and rr grew](#how-tracer-and-rr-grew)
- [Why the question round](#why-the-question-round)
- [Why each method step](#why-each-method-step)
- [Why each file law](#why-each-file-law)
- [Decisions](#decisions)
- [Open items](#open-items)

## Parts

| Part | File |
| --- | --- |
| The law | `templates/global/skills/how-to-build-agent/SKILL.md` |
| The loop it runs | `templates/global/skills/how-to-build-iterative-improvement/SKILL.md` |
| The design push it loads | `templates/global/skills/ultimate/SKILL.md` |
| Prose and description laws it defers to | `templates/global/skills/how-to-build-prompt/SKILL.md`, `templates/global/skills/how-to-build-description/SKILL.md` |
| The declaration mechanism its tier law cites | `plugins/engine-sync/docs/interface.md` § Declarations |

## The pattern

Every agent this repository improved on purpose was improved the same way. Each row is one element, with every place it shows.

| Element | Where it shows |
| --- | --- |
| A real brief from history, fixed before any run | tracer: one spec-writer brief, 4 questions, then B1, B3, B4 over `pfm/` (`docs/design/tracing/tracer-bench.md` § Single-agent bench, § Tree bench); collector: two real extraction briefs from spec-writer transcripts (`docs/design/tracing/collector.md` § Measured); reviewer: past commits whose defects later fix commits proved (`docs/design/review/reviewer.md` § Benchmark); flights-lander: replayed landings (`docs/design/flights/flights-lander.md` § The bench) |
| A key an independent judge builds from the source before any return | tracer: a 30-facet key by an `opus` judge, then 33- and 21-facet keys (`tracer-bench.md`); mapper against tracer: a 42-facet key built before either return was read (commit b7176eac) |
| Blind scoring under a neutral letter | tracer's single-agent bench: DELIVERED 1, PARTIAL 0.5, MISSING and WRONG 0, a declared NOT READ never wrong, a second blind judge landing on the same 28.0; the replay bench's judge told neither design; reviewer's grader with hidden labels |
| Each version one named theory, every run a ledger row | tracer tree v1–v7, each with its one-line change (`tracer-bench.md` § Tree bench); collector revisions 1–4 ("text retyped into the message", "manifest and file", "`--expect`, zero-hit grep is a MISS", "one-call prompt") |
| Every run priced from its transcripts, with the lead's share | tracer's tree bench prices every request of every agent at the price table's rates, lead 60–80%; rr's lead-call budget `r(d + 1) + 6` (`docs/design/RR/rr.md` § The lead-call budget); reviewer's score divides by the whole run's USD |
| Run-to-run spread treated as the noise floor | tracer v6's two B1 runs at value 2.45 and 1.40 (`docs/design/tracing/tracer.md` § Open items); reviewer's spread of 0.11 a rung |
| Losers reverted, their numbers kept | tracer v7: half a facet for 40% more cost, reverted; rr's red-team digger added in 642f6254, dropped in c58aac7e |
| A model and effort bench picks the tier | tracer: Sonnet 5.5 `high` at 43% of the `opus` lead's price, 98% of its accuracy (`tracer-bench.md` § Model bench, commit 763041e9); reviewer: every cheaper lead lost R1 recall; clone-distiller: `sonnet` `high` matched `opus` per dollar on its bench |
| Tiers born from the bench and retired by need | tracer-pro and tracer-pro-max shipped in 763041e9, retired in 4bb529c7; rr's three tiers kept, each with the caller need it serves (`docs/design/RR/rr-pro.md` § When a caller picks it); every tier a declaration since bb3a550b |
| Harder rungs: the heaviest past briefs replayed | tracer's replay of five of the costliest of its 395 past runs, $10.75 paid against $2.99 (`tracer-bench.md` § Replay bench) |
| A rule disobeyed as prose becomes a mechanism | tracer's read bounds failed in 4 of 6 leads as prose, so the kit enforces them (`tracer.md` § Rules); collector's retyped text became a script-written file; the foreman cut rule became a kit decision and a cut-gate hook (1bb04200, deabcbff) |
| Callers probed, their sessions audited | three caller-probe rounds whose findings became kit mechanisms (`tracer-bench.md` § Caller probes); the caller manual preloaded by `skills:` (26ecad5e) |
| Field runs swept from disk after shipping | 259 tracer runs re-checked and joined to 183 lead transcripts, outliers audited (`tracer-bench.md` § Field runs), fixes in 05197dec |
| Each rule recorded with its why and its row | `tracer.md` § Rules ("Each rule holds in the prompts or the kit today; the clause after it is why"); "What the runs measured" in `rr-pro.md`, `rr-pro-max.md`, `sub-rr.md` |

## How tracer and rr grew

What git and the bench ledgers show, in order.

tracer:

1. The single-agent body was designed on `opus` against the 30-facet key: prompt runs 1 and 2, the second winning on wrong statements, cost and verbatim fidelity (`tracer-bench.md` § Prompt bench).
2. The model bench moved it to `sonnet` `high` and shipped two tiers beside it (763041e9).
3. A head-to-head on one 42-facet key deleted `mapper` (b7176eac).
4. The tree redesign followed from the cost evidence: on B1, $0.181 of the single agent's $0.344 was context written to cache, so a lead over readers at a twentieth of the price per token. Versions v1–v6 each fixed what the run before showed, and v7 was measured and reverted (c1e79562, `tracer-bench.md` § Tree bench).
5. The tiers were retired (4bb529c7); three caller-probe rounds drove the kit mechanisms; the caller manual shipped (26ecad5e); the field sweep drove the field fixes (05197dec); the run cap `BUDGET` followed (e64c5b34).

rr:

1. Born with one digger per rabbit hole, up to 8 a round and a 6-round ceiling (bfa8c33b).
2. Its first live run spent 21 diggers over 3 rounds, so the lead batched the frontier into at most 4 diggers a round (5563b950).
3. Lenses and a red-team digger were added (642f6254); the red team was dropped the same day, the lenses kept (c58aac7e). The lenses rest on the essay `docs/design/RR/rr.md` § What the evidence says quotes for Range (a question read as verbs, through several fields) and Depth (the rawest material, the field's methods), the two steps `/ultimate`'s § Raise the ask, before the first edit carries under those names.
4. Each run saves a directory holding every digger's report (27c37fab), and the deeper tiers became pinned declarations (bb3a550b).

The method's own record: `/how-to-build-iterative-improvement` was committed (3a17d3e6) 68 minutes after the tracer tree (c1e79562), its rules — the ladder, replay of the heaviest past runs, blind grading, a change kept only beyond the spread, a rule disobeyed twice moved into a mechanism — the tree bench's practice written down. `/ultimate` predates the tree (265825ed).

Unproven: the user's account is that both agents were grown from a greenfield brief with goals on named axes, Opus trying theories stage by stage under `/how-to-build-iterative-improvement` and `/ultimate`. The opening prompts of the tracer tree work and of the rr rework sit in no transcript this host holds (an `agent-investigator` search over this repository's transcripts for the windows before c1e79562 and 642f6254 found none). The stages are proven by the ledgers above; the greenfield brief and the named axes are the user's testimony.

## Why the question round

- The metric is fixed before the first run and changes only on the user's word (`how-to-build-iterative-improvement/SKILL.md` § Frame — before round 1), so the axes are settled before any work.
- The four default axes are the four every bench above measured: accuracy as key facets, cost as USD, requests and seconds, wrong claims as the Wrong column and the cites checked against their lines, and leanness as bytes (the description law's sweep cut the framework's 93 descriptions from 29,223 to 20,474 characters, read by every session and spawn, 38ef8987; tracer's cards repeating long questions cost 4.5–7.6K output tokens a lead).
- Target, input and budget are asked with the axes because each bench above stood on one fixed brief and one spend cap.
- The value formula follows tracer's tree bench, value = facet share × cite-ok share ÷ USD; a broken return contract blocks shipping outright because a caller parses the return, and a caller that cannot is a failed run at any score.

## Why each method step

1. Greenfield: the large gains came from a new shape, never a rewording — the tree over the single agent, the batched diggers over one digger per hole, the script-written file over retyped text. The foreman's cut rule, disobeyed as prose, was not reworded a third time: it became a cut-gate hook (deabcbff), then a kit decision the foreman follows or overrides on a checkable fact (1bb04200).
2. Bench fixed first: a key built after the returns grades the returns' own framing; tracer's facets corrected against the code were carried to every later judge, so scores stay comparable across versions.
3. One theory per version, two runs each: tracer's tree versions changed several rules at once, so a row shows why a rule exists and not its isolated effect (`tracer-bench.md` § Tree bench); v6's two runs on one brief differed by 75%, and reviewer's spread reached 0.11 a rung.
4. Model and effort: accuracy moved within 3% across every setup while price moved more than threefold; quoting did not rise with effort, and a `medium` lead dropped facts (23/33 on B1 against `high`'s 30.5).
5. Tiers by need: tracer's tiers served no caller need the base did not, and were retired; rr's tiers differ in caps and the repository lane, which callers pick by the cost of a wrong fact (`rr-pro.md` § When a caller picks it). mapper lost its head-to-head and was deleted.
6. Harder inputs: the replay bench found what the lab briefs never showed — the grouped-list miss on P0, the line-numbering pipe on P20. In the foreman cut lab (a machine-local ledger, not shipped), a single-unit replay tied both versions and could not discriminate; a must-split replay, the input that triggers the decision under test, separated them two runs to two.
7. Callers: across 84 field callers, 3 ran `check` or `map` on a returned run and 1 loaded the caller's skill (`tracer-bench.md` § Caller audit); the return's last line became a runnable command and the manual was preloaded through `skills:`.
8. Field runs: the outlier audit found what no lab run did — refused spawns at the harness's concurrency cap, a backgrounded wait, a 59 MB file — each a rule in 05197dec.
9. Record: every rule in `tracer.md` and `rr.md` carries its why and the run it rests on, so a later edit knows what it would undo; an unbenched claim sits under open items.

## Why each file law

- `description:` carries `Pass …` and `Returns …`: tracer's and collector's descriptions name the brief's fields and the return kind; `/how-to-build-description` requires the Returns clause of an agent.
- Model on the bench's pick: tracer `sonnet` `high`, sub-tracer `haiku` `low` (a `medium` node cost about $0.04, mostly thinking, `low` $0.01–0.02), sub-rr and collector `haiku` `medium`.
- A tool omission can be the guard: rr holds no `Read`, so a Write over a taken run directory is refused and the lead takes `-2` instead of erasing the first run; rr-pro-max adds `Read` and gives the guard up, a recorded ruling (`rr.md` § The document, `rr-pro-max.md` § Read, and the name guard it gives up).
- A stale tool name fails silently: sub-rr's harvester calls rose from 0–2 to 10–22 a run once `tools:` named the live tools (`docs/design/RR/sub-rr.md` § What the runs measured).
- `visibility.visible-to` with the class token: sub-rr, sub-tracer and the lander's hunters are reachable only from their callers.
- `skills:` and `hooks:` load only for a registered `subagent_type`: the caller manual reaches reviewer, reviewer-pro and clone-distiller through `skills: [tracer:use]` with no Skill tool (26ecad5e).
- `engines.codex.sandbox: workspace-write`: Codex compiles a tools list without a write tool read-only (`interface.md` § Agent frontmatter), and tracer's kit writes its run directory (`tracer.md` § Model).
- Brief fields spelled as written: tracer's `RECALL: {run}` and `BUDGET {n}`, sub-rr's `Report: {path}` and `Report: none — {reason}`.
- Counted costly steps: every call re-sends the agent's whole context; tracer's one wide Bash call, two drills a wait and one gap round each came from a run that overspent without them (`tracer.md` § The lead).
- Waiting by ending the message or inside one blocking call: the Agent tool exposes no background parameter, so the rr lead waits by ending its message (16374faf); a tracer lead counting wakes lost its final (v3), so it waits inside `tt wait`.
- A computable budget: rr's `r(d + 1) + 6` lead calls let a run be checked against its budget — 13 calls against 13 (`rr-pro.md` § What the runs measured).
- A fixed return: rr's return opens with the saved path and shrank from about 10 KB to 2 KB once the map stayed in the file; collector's return is the manifest alone because text passing through the agent came out shifted, merged or invented.
- Marks: rr's one vocabulary keeps every error apart from every absence (`rr.md` § Marks); a failed save opens `NOT SAVED` with the content inline.
- NOT READ with a next step: probe 3's leads handed on 12 of 34 items with none; a list claim naming its items answers the single agent's "all 12 catches" when 5 did not.
- Contrastive examples only on a leak: sub-rr's ✗/✓ at the return step cut citations of never-fetched pages from 12–36% to 5% of links; its example against a preamble backfired, one digger opening with the ✗ sentence word for word, so the plain rule stays alone (`sub-rr.md` § What the runs measured).
- Derived tiers as declarations: the variants file became one declaration per tier with section pins (bb3a550b); an edited base section fails the render with `SECTION-DRIFT` until the override is reviewed and re-pinned (`rr-pro.md` § How the variant is built).
- Load at session start: an edited agent runs in the next session (`tracer.md` § Harness behaviour), and a new type answers `Agent type 'rr-pro-max' not found` until the registry reloads (`rr-pro-max.md` § Running it).

## Decisions

Each is overturnable; the flip is named.

- The question round asks the four axes, target and goal, bench input and budget in one AskUserQuestion. Under god speed it asks nothing, takes all four axes and records one ruling line in the lab brief. Flip: delete that sentence from § Settle first.
- `check` takes an optional path list defaulting to every agent under the project's and the user's agents directories, as its siblings' `check` does. Flip: make the path required.
- The description carries `MANDATORY`, since every agent edit is meant to load the law. Flip: drop the token; the skill then routes on the model's judgment.
- The value formula multiplies accuracy by the share of claims right and divides by USD; leanness breaks ties and a broken return contract blocks shipping. Flip: rewrite the formula paragraph in § Settle first.
- The method stays as agent-specific deltas over `/how-to-build-iterative-improvement` and `/ultimate`, no loop step restated.

## Open items

- The greenfield brief and the named axes behind the tracer tree and the rr rework are unproven from transcripts (§ How tracer and rr grew).
- The skill itself is unbenched: no agent has yet been built or improved under it.
- The fleet's agents disagree on the compaction tools for `haiku` agents (sub-rr omits them, sub-tracer and collector carry them); the skill defers to the `pcm` agent's new-agent rule.

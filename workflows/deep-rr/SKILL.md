---
name: deep-rr
version: "3.2.4"
description: >-
  USER-ONLY — the user asks for it by name; never start a run inside another task. Launches a background research run that returns a cited report. Use for "research X", "look into X", "deep-rr X" when one web search will not do; modes goal (one question) and collect (inventory a topic); "deep-rr fast X" answers inline. Returns the report path under RR/{slug}/, resumable.
---

# Research and Report (RR)

## Purpose

RR's job is to **answer the question** — by reasoning over everything it can gather, not merely finding and collating facts. It *derives* the answer. Reach for it above all when the answer has to be **built**: a synthesis, a quantitative estimate, or a judgment that no single source holds — e.g. *"estimate the distance to the nearest yet-undetected stellar-mass black hole with error bars, and say which observational method finds it first."* RR gathers the components (population estimates, local densities, detection precedents, instrument forecasts) and reasons them into one answer.

It never stops because a fact wasn't found. A missing piece is a reason to gather more and reason harder — not a dead end. An ingredient it cannot pin down becomes **stated uncertainty** (assumptions, wider error bars, open questions), never an early exit.

## How it works

One Opus **brainer** drives the whole run; everything else is its instrument. Evidence lives in an append-only **claim ledger**, not in prose — every claim is pinned to a verbatim quote, mechanically audited, and clustered by independent lineage; a claim's status (settled/tentative/contested) and the report's confidence are **computed** from ledger topology — models may lower confidence, never raise it.

1. **Scout swarm** — a sonnet **planner** runs a couple of grounding searches, decomposes the query, and proposes 3–5 distinct search angles (always including a direct angle, a **skeptic** angle hunting counter-evidence, and a **recent** angle); a haiku **probe** sweeps each angle in parallel (search → fetch ≤3 sources); a sonnet **merger** folds every surviving probe into one landscape, explicitly naming the tensions between angles. Each stage degrades to a deterministic fallback (single direct probe / plain JS merge) if its agent dies.
2. **Prospector** (opus) — names the high-value authoritative source venues, and when the topic is more active in another language, the native venues to search in (tagged by language).
3. **Research waves** — the brainer steers an id-keyed rabbit-hole frontier: it scores the open leads, picks the lanes worth pursuing (≤5 per wave), and authors a per-lane **`note`** (the research directive plus ranked fallbacks). A **researchScheduler** (sonnet) batch-discovers + sizes the best sources per lane; code bin-packs each lane into ≤130k-token reader-units and runs **one sequential haiku reader thread per lane**, parallel across lanes. Every read yields quote-pinned **claims** into the ledger, which a haiku **claimAuditor** mechanically quote-audits (greps the cache for the verbatim quote) and a sonnet **lineageClerk** clusters by independent provenance (authors/funder/dataset, union-find) — corroboration counts **clusters**, not sources. Attack lanes hunt counter-evidence for tentative claims; a counter-search that finds nothing is recorded as a survived challenge (a `nullAttack`), not silence. For build-the-answer queries the brainer authors a stored, seeded Python **derivation** once; a haiku **rerunner** re-executes it each wave the moment an input claim changes, and its variance decomposition steers which leads get read next (a value-of-information stop test). Lead selection is self-calibrating — a per-kind predicted-vs-realized yield table tempers scores. A per-wave **validator** (sonnet) checks coverage and reopens thin lanes; each wave also logs a zero-cost `⏺CKPT` recovery line — no agent, no extra latency.
4. **Finalize** — an **initiator** groups the load-bearing facts → a **refine** pass hardens each group against the sources, folding every counter-search outcome into the ledger (contested mark or another `nullAttack`) → an Opus **judge**, the sole terminal skeptic in both goal and collect modes, stress-tests the hardened answer, sees the leftover open rabbit-holes, and can **retract** a discredited ledger claim (recomputing every downstream status/confidence), steering a bounded remediation loop: the brain derives the answer when one is needed (running code, propagating error bars), refine re-checks a flagged fact, or the crawl reopens on a real gap → a **synthesiser** writes the report; its `[cN]` citations are linted against the ledger (unknown ids stripped) and its stated confidence is floored by the computed one (never raised).
5. **Collect mode** additionally gets a Chao1 coverage estimate (species = near-duplicate claim groups, abundance = distinct sources) that gates the novelty-plateau dry stop — a plateau alone does not stop the crawl while a lot of ground estimably remains unseen.
6. **The brainer tree** (`maxParallelBrainers`) is unchanged in shape — a brainer may spawn a focused child onto a rich branch; the first whose answer the judge upholds wins. Every other brainer's non-retracted claims MERGE back into the winner's ledger before it finalizes, so a losing branch's evidence still reaches the report.
7. **Debug** (opt-in) — a final analyst writes `_debug.md` with metrics + raw agent I/O.

## Launch

**Preflight — the fetch MCP (Harvester) must be live.** RR fetches only through `mcp__harvester__fetch` (built-in WebFetch is hook-denied), so a missing or dead server makes every fetch error and yields a snippet-only run. Before launching, check for the `mcp__harvester__*` tools (via ToolSearch):

- **Missing** — hold the launch and point the user to the Harvester MCP (`https://github.com/rezzminator/harvester-web-mcp`) to install, then `/mcp` to connect.
- **Present** — smoke-test with one `mcp__harvester__fetch` on a stable URL (`https://example.com`); a clean fetch means go, an error means have the user reconnect (`/mcp`) or restart the server — hold until it passes.

Call the **Workflow tool**. It runs in the background; a completion notification returns the result — do not block on it. Use the **absolute** path to `workflow.js` (the skill's base dir is printed when the skill loads) — the working directory may sit inside a child project, where a relative path resolves against the CWD and 404s the bundle.

```
Workflow({
  scriptPath: "<skill-base-dir>/workflow.js",
  args: { query, mode, maxParallelBrainers, compute, computeNote, thinkerNote, researcherNote, tag, debug, debugPrompt, agents }
})
```

### args

- `query` (required) — the research question (goal) or collect-target. The crawl sees only this string.
- `mode` — `'goal'` (default) or `'collect'`. See Modes.
- `maxWave` — `'auto'` (default, brainer-stopped, hard-capped at 15) or an integer clamped to `[5,15]`. The wave-cap override; leave `'auto'` unless you need to force a shorter or longer crawl.
- `chaoCoverageStop` — collect-mode exhaustiveness dial, a number in `(0,1]` (default `0.9`). The dry-stop fires when the frontier plateaus AND estimated Chao1 coverage ≥ this; lower it (e.g. `0.65`) to stop a collect run well before full saturation.
- `maxParallelBrainers` — `1` (default) … `5`. The brainer-tree width. `1` is the single global brainer. With `2`–`5`, a brainer may SPAWN a focused child onto a rich branch; the children run in parallel, the first whose answer the judge upholds wins (its report becomes `result.md`), a child may abandon a dead-end branch, and every other brainer's evidence still merges into the winner's ledger. More brainers ≈ proportionally more cost — raise it only for a goal with several deep, separable branches.
- `parallelLaneResearchAgentsPerWave` — `'auto'` (default, brainer-assigned, hidden cap of 5) or an integer clamped to `[1,5]`. How many lanes (rabbit-holes) a wave pursues at once.
- `parallelSourcesPerLaneResearchAgent` — `'auto'` (default) or an integer clamped to `[1,5]`. Governs `MAX_SOURCES_PER_LANE`, the cap on sources bin-packed into one lane's reader thread per wave: `'auto'` keeps the fixed cap of 12; an integer override REPLACES it with that (clamped) value — lower it to bound a lane's reading cost, e.g. on a wide, shallow crawl.
- `agents` (optional) — per-seat model/effort override, keyed by seat name: `{ <seat>: { model?: 'haiku'|'sonnet'|'opus', effort?: 'low'|'medium'|'high'|'xhigh' } }`. Valid seats: `scout`, `scoutPlanner`, `scoutMerger`, `prospector`, `brainer`, `validator`, `researcher`, `researchScheduler`, `initiator`, `refiner`, `judge`, `synthesiser`, `debugAnalyst`, `claimAuditor`, `lineageClerk`, `rerunner`. Unknown seat / bad model / bad effort all throw. Retunes ONLY the named seat/field — everything else keeps its documented default. Downgrading `brainer.model` below `'opus'` is allowed but logs a loud warning (measured: a Haiku brainer scored erratically and drifted off-goal) — reach for this only to deliberately trade quality for cost/speed on a specific seat. Example: `agents: { researcher: { model: 'sonnet' }, judge: { effort: 'high' } }`.
- `compute` — `true` (default) or `false`. The master switch for derivation: `false` runs no compute agents (no stored/rerun derivation mid-wave, no finalize brain-derive) for a faster, gather-and-reason-only run. The decision rule: compute is for a run whose answer must be BUILT BY RUNNING CODE — a quantitative estimate, arithmetic over gathered inputs, uncertainty propagation. Numbers merely READ from sources (effect sizes, prices, benchmark scores) are collation, not derivation — a qualitative synthesis or a collect-mode inventory wants `compute: false`.
- `computeNote` (optional) — extra run-specific guidance for the compute-aware agents (a method to use, a constraint to respect). Appends to the always-present note that the compute environment ships a scientific Python stack (scipy, sympy, uncertainties, pandas, statsmodels, scikit-learn, networkx, pint, rdkit).
- `thinkerNote` (optional) — operator run-steering for the reasoning tier: priorities, framing, constraints, audience. Shapes HOW the run is approached and what the report emphasizes — not additional questions to research. Reaches the prospector, brainer, initiator, judge, and synthesiser — never the cheap workers.
- `researcherNote` (optional) — a terse one-line note (≈6-7 words) to the web-research agents — scout, prospector, brainer, researchScheduler, and researcher. Steers HOW they search and fetch (which sources to favour, what to skip). Passthrough, injected verbatim.
- `checkpoint` — `true` (default) or `false`. Per-wave crash-safety: logs a `⏺CKPT` recovery line (the open leads, running answer, live claims, and derivation state) into the workflow's own output each wave — zero-cost, no agent. `false` turns the line off.
- `tag` (optional) — suffixes the output dir so parallel variants of one query write to distinct dirs.
- `debug` (optional, default `true`) — writes `_debug.md` (run log + metrics + raw agent I/O); ON by default, pass `debug: false` to turn it off. Pair with `debugPrompt` (string) to focus it on a question.

## Writing the args — what to put in each, per audience

Each free-text arg reaches a DIFFERENT set of agents (see `design.md` § note plumbing). Write for that audience; the wrong content in the wrong arg is silently wasted or, worse, actively mis-steers.

### `query` — the contract (every agent sees it)

The only string the whole crawl shares; the judge holds the final answer against it, so anything not in the query is not enforced. Full authoring rules in **Writing the query** below. Rule of thumb: constraints, scale, time horizon, and the DELIVERABLE all live here — never in a note.

### `thinkerNote` — brief the analysts (prospector, brainer, initiator, judge, synthesiser)

The reasoning tier reads this as an operator's steering memo. It shapes prioritization, judgment, and the report's emphasis — the same evidence, approached differently.

Belongs here: who the answer is FOR and what they'll do with it; priority order among the query's sub-questions; honesty demands ("be explicit about what the evidence can NOT support"); framing constraints ("engineering audience — mechanisms over narrative"); report emphasis ("the deliverable shapes a build decision — lead with the decision"). Does NOT belong: extra research questions (query), source/venue steering (researcherNote), derivation method (computeNote).

- Good: `Audience: the design team of a clinical tool — never diagnosing. Prioritize (1) fidelity to real clinical practice over pop-science, (2) what is detectable from text alone vs what requires video — be honest about the limits, (3) what practitioners actually read vs ignore.`
- Bad: `Also find out what EFT therapists track.` (a second research question — widen the query instead)
- Bad: `Prefer .gov and peer-reviewed sources.` (venue steering — that's researcherNote)

### `researcherNote` — steer the hunters (scout, prospector, brainer, researchScheduler, researcher)

A terse ONE-LINE note injected verbatim into every search-and-fetch agent, including the haiku readers — every extra word taxes the cheapest, most-called workers. Steer WHERE to look and WHAT to skip, never what to conclude.

- Good: `Favor Gottman Institute, peer-reviewed process research, therapist training material`
- Good: `Primary sources + official docs; skip SEO listicles and vendor blogs`
- Good: `German-language coverage is richer — search de sources too`
- Bad: `Make sure the report emphasizes cost tradeoffs` (report shaping — thinkerNote)
- Bad: anything ≥2 sentences — it dilutes the haiku readers' actual reading instructions.

### `computeNote` — brief the mathematician (brainer, judge — the derivation authors/validators)

Read when a derivation is authored, re-run, and judged. Name the METHOD, the error model, and any hard constraint; the stack note (scipy/sympy/uncertainties/pandas/statsmodels/scikit-learn/networkx/pint/rdkit) is always present, so name what to do with it.

- Good: `Propagate uncertainties end-to-end (uncertainties pkg); report 90% CI, not point estimates`
- Good: `Monte Carlo over the population priors (n≥10k draws); state which prior dominates variance`
- Bad: `Double-check the math.` (the judge already does — says nothing)

### Knob heuristics

- `maxParallelBrainers`: raise above 1 only when the goal has ≥2 genuinely separable deep branches (e.g. "what works today AND the 5-year migration path"); a single-question goal on 2+ brainers buys coordination cost, not coverage.
- `maxWave`: force below `'auto'` only for a deadline; force high only when a collect run must saturate a long tail.
- `chaoCoverageStop`: drop toward `0.65` when a collect answer is needed soon and a representative inventory beats an exhaustive one.
- `compute: false`: pick whenever nothing needs to be computed by running code — qualitative synthesis, a collect inventory, numbers that are read off sources rather than derived. Skipping derivation agents saves a finalize round-trip; compute earns its cost only when the answer itself is a calculation.
- `tag`: always set when launching variants of one query in parallel — same slug would collide.
- `debugPrompt`: pose ONE diagnostic question ("why did lane X starve?") — the analyst answers it against the raw I/O log.
- `agents`: reach for it to trade quality for cost/speed on ONE seat (e.g. `researcher: { model: 'haiku', effort: 'low' }` on a run where reading is already cheap) or to harden ONE seat further (e.g. `judge: { effort: 'xhigh' }` — it already defaults there, so this is a no-op; raise a seat that defaults lower instead). Leave every seat alone unless you have a specific reason — the defaults are the measured-good policy.

## Modes

- **goal** — satisficing. Answers ONE question and stops once it is answered; the judge guards against a premature stop. Pick for a decision or a direct answer.
- **collect** — exhaustive. Inventories breadth and runs until saturation. Pick for a landscape or a roster.

## Fast mode

When the user says **"deep-rr fast <query>"**, skip the background Workflow and run a miniature of it inline — the same shape (a lead that follows rabbit-holes by nesting researchers), at a smaller model and fewer rounds, answered right now. Spawn ONE **Sonnet** lead sub-agent (Task-capable, so it can nest) with this prompt:

```
Research and answer this directly: "<query>".
1. Run a targeted WebSearch to map the question; pick the 2-4 highest-value rabbit-holes (each a concrete sub-query).
2. Spawn one HAIKU sub-agent per rabbit-hole, in PARALLEL, each tasked: "Dig this rabbit-hole for «<query>»: <rabbit-hole>. WebSearch, then WebFetch the 2-3 best sources; in each WebFetch prompt ask the key question first, then append this footer verbatim: <<FOOTER>>. Return 2-4 sentences of findings with inline source links AND the rabbit-holes the pages surfaced."
3. Read what came back. If 1-2 of the newly-surfaced rabbit-holes are load-bearing and still unanswered, dispatch ONE more parallel Haiku round on them (same task). Then stop.
4. Synthesize everything into the answer: lead with the answer, then the load-bearing facts with inline source links, then any open questions.
```

The `<<FOOTER>>` each Haiku digger appends to its WebFetch prompts (this is what makes it surface deeper rabbit-holes instead of stopping at the first hit):

```
Then append a section titled "Rabbit holes": 0-5 rabbit-holes worth a researcher's time, prioritizing the biggest gaps the page raises but does not explain. Each rabbit-hole: a concrete next web-search query and one line on why it matters. If the page is a dead end or self-contained, give 1 or none — do not pad. Skip anything the page already explains.
```

## Getting results

After the completion notification, persist the artifacts:

```
node <skill-base-dir>/persist.js <completion-output-file>
```

This writes to `RR/{slug}/`:

- `result.md` — the deliverable. Read this first; it opens with a compact **Run arguments** record (the complete launch args).
- `_claims.json`, `_claims.md` — the full claim ledger: every quote-pinned claim with its status/cluster/audit verdict and the `nullAttacks` log, machine- and human-readable.
- `_sources.json` — every cache file a live claim actually pins to (claim-referenced, never a raw cache dump).
- `_rabbitHoles.json`, `_tree.md` — the rabbit-holes (with the run's launch `args` at the top) + the crawl tree; diagnostics.
- per-wave files (`03-wave-0.md`, `04-wave-1.md`, …), plus `NN-validator.md`, `NN-initiator.md`, `NN-refinement.md`, `NN-judge.md`, and `_finalize-compute.md` when the judge sent the brain to derive — the full prompt/response trail of the run.
- `_brainers.json`, `_brainers-tree.md`, `result-<name>.md` — only when `maxParallelBrainers > 1`: the brainer tree (who spawned whom, the winner) plus each non-winning brainer's preserved partial.
- `_debug.md` — when launched with `debug: true`.

`persist.js` also mirrors the claim-referenced cache files into `RR/{slug}/resources/` — provenance-filtered via `_sources.json`, so only files a ledger claim actually pins to are archived, never a raw cache dump. On an aborted or failed run (unparseable output, or a completed run with no files) it writes an auditable tombstone under `RR/_aborted/` instead of exiting with an error, so a retry chain never loses the forensics of what died on earlier attempts.

Crash-safety no longer writes a file — each wave logs a `⏺CKPT` recovery line instead (`checkpoint: true` by default); see Recovery below.

## Recovery

When a run crashes, wedges, or gets killed mid-crawl, find the checkpoint: each wave logs one `⏺CKPT w<N> {json}` line into the workflow's live output — the LAST one holds the run's state at that wave's end (resultSoFar; the open frontier: id, keyword, score, kind; pursued lanes; the claim ledger, clipped, no quotes; nullAttacks count; derivation inputs + last quantiles). Search the workflow output/logs for `⏺CKPT`.

- **Same session:** true resume — `Workflow({scriptPath, resumeFromRunId: "<the run id>"})`; completed agent calls replay from the harness cache, only unfinished work re-runs.
- **Cross-session:** read the last `⏺CKPT` line (and, for deeper forensics, the workflow transcript dir's `journal.jsonl` — one line per completed agent with its full return). Answer directly from resultSoFar + the ledger if it's already solid; otherwise relaunch a FRESH run with resultSoFar/openGaps woven into `query` + `thinkerNote` so it starts warm, not cold.

## Mid-run inspection

```
node <skill-base-dir>/midrun.js [status|findings] [run-dir]
```

The task output file stays empty until the run completes — mid-run, the only truth is the workflow transcript dir (`journal.jsonl` + per-agent files). `status` (default) reports progress, pipeline shape, and derived health flags — a dead agent is inferred from a dispatch with no completion plus a stale transcript, never from error text (there isn't any). `findings` reports what the run has learned so far — the latest brainer coord's memory, or (on a degraded run with no coord) a reconstruction from the finalize agents. Path omitted auto-discovers the newest live run. Reports land in `tmp/rr-midrun/`.

## Writing the query

- Make it specific and self-contained — state the constraints, scale, time horizon, and the deliverable inside the query.
- **goal:** pose one clear question. A two-axis question ("what works today AND the migration path") is fine.
- **collect:** frame as "exhaustively inventory X across these dimensions: …" and name the candidates + dimensions.
- Avoid self-referential queries about the local repo — they make the crawl over-explore local files instead of researching.

<example>
Vague: "vector databases" Good (goal): { query: "Which self-hosted open-source vector database has the best recall-vs-latency tradeoff for ~10M embeddings on a single 16-core box, and what should a small team deploy?", mode: "goal" } Why: names the constraints, poses one decision, states the deliverable.
</example>

<example>
Vague: "tell me about Tailscale" Good (collect): { query: "Exhaustively inventory Tailscale across these dimensions: products, funding history, leadership, security posture, notable incidents, and how the WireGuard mesh works.", mode: "collect" } Why: the collect frame + named dimensions give a saturation target, not an open browse.
</example>

<example>
Good (goal, derive): { query: "Estimate the distance to the nearest yet-undetected stellar-mass black hole, with error bars, and say which method (Gaia astrometry, microlensing, X-ray, radial velocity) finds it first and roughly when." } Why: no single source holds this — the brainer authors a stored, seeded Python derivation once, a rerunner re-executes it every wave as evidence lands, and its variance decomposition steers which components get chased next (compute defaults on).
</example>

## Build & maintenance

`workflow.js` is a **build artifact** — never hand-edit it. The source is the modular project under `engine/`:

```
cd engine && npm install        # first time
npm run build                   # bundles engine/src/ → ../workflow.js, then validates the sandbox contract
npm test                        # the vitest suite (unit + integration tests)
```

Edit `engine/src/` (the modules + each agent's `prompts.ts`, plus the prompt fragments in `config.ts`), rebuild, and commit both the source and the regenerated `workflow.js`.

## Working with it

- Pick **goal** for a decision/answer, **collect** for a landscape, **deep-rr fast** for a quick inline take.
- Full runs are ~45–90 min and token-heavy — launch, then move on; don't predict findings while it runs.
- Read `result.md` first; everything else is diagnostic.
- Use a `tag` for side-by-side variants; set `compute: false` for a faster gather-only run.

# RR — Research and Report

**Version:** 3.2.4 · **License:** MIT · **Repo:** [github.com/rezzminator/rr](https://github.com/rezzminator/rr)

A Claude Code skill that runs a deterministic background **Workflow** to research a question — an unbounded, best-first, brainer-steered web crawl that **derives** the answer and writes a cited, multi-section report with a verdict, stated assumptions, and a plan.

## Why this exists

Ask an LLM to "research X" and it runs one search, reads the top hits, and summarizes. RR is for the questions that need more than that — where the answer has to be **built**, not found: a synthesis, a quantitative estimate, or a judgment no single source holds (*"estimate the distance to the nearest yet-undetected stellar-mass black hole, with error bars"*). RR gathers the components and **reasons them into one answer**, computing it when the answer is a number. It never stops because a fact wasn't found — a missing piece becomes stated uncertainty, not a dead end.

## How it works

One Opus **brainer** drives the whole run; everything else is its instrument. Evidence itself lives in an append-only **claim ledger** owned by deterministic code, not in prose the brainer rewrites each wave.

```
Scout swarm:
        sonnet PLANNER runs a couple of grounding searches, decomposes the query, proposes
        3-5 search angles (always direct + a SKEPTIC angle + a RECENT angle) → one haiku
        PROBE per angle, in parallel, sweeps its angle only → a sonnet MERGER folds every
        surviving probe into one landscape, naming the tensions between angles. Each stage
        degrades to a deterministic fallback if its agent dies.
  → Prospector (opus) names the authoritative source venues — and, when the topic is more
        active in another language, the native venues (tagged by language) to search in
  → Research waves:
        the brainer steers an id-keyed rabbit-hole frontier via deltas → a Scheduler (sonnet)
        batch-discovers + sizes the best sources per lane → code bin-packs each lane into
        <=130k-token reader-units and runs one sequential haiku reader thread per lane. Every
        read yields quote-pinned CLAIMS into the ledger: a haiku claimAuditor mechanically
        greps the cache to verify each quote, a sonnet lineageClerk clusters claims by
        independent provenance (union-find over authors/funder/dataset) — corroboration counts
        CLUSTERS, not sources. Claim status and the run's confidence are COMPUTED from ledger
        topology; models may lower confidence, never raise it. Attack lanes hunt counter-
        evidence for tentative claims; a counter-search that lands nothing is a survived
        challenge (nullAttack), not silence. For build-the-answer queries the brainer authors a
        stored, seeded Python DERIVATION once; a haiku rerunner re-executes it whenever an input
        claim changes, and its variance decomposition steers which leads get read next (a
        value-of-information stop test). Lead selection is self-calibrating (predicted-vs-
        realized yield per lead kind). A per-wave validator (sonnet) checks coverage and reopens
        thin lanes; each wave also logs a zero-cost ⏺CKPT recovery line — no agent involved.
  → Finalize: initiator groups the load-bearing facts → refine adversarially hardens each
        group against the sources, folding every counter-search outcome into the ledger → an
        Opus JUDGE (the sole terminal skeptic) stress-tests the hardened answer — goal met?
        verification sound? compute needed and valid? — can RETRACT a discredited ledger claim
        (recomputing every downstream status/confidence), and steers a bounded remediation
        loop: the brain derives the answer (reruns/extends the derivation, propagates error
        bars), refine re-checks a flagged fact, or the crawl reopens on a real gap → a
        synthesiser writes the report; its [cN] citations are linted against the ledger and its
        stated confidence is floored by the computed one (never raised)
  → collect mode additionally gets a Chao1 coverage estimate that gates the plateau dry-stop
  → Debug (opt-in) writes _debug.md
```

Fetching runs through **Harvester** (an MCP server) — it resolves walled sources via the legal open-access chain (DOI → Unpaywall / OpenAlex / Europe PMC / …), reads PDFs and books, views images, and falls back through Chrome-impersonation + the Wayback Machine when a URL is blocked — so the readers work on primary literature, not just the open web.

The brainer never re-emits the whole frontier — it returns **deltas** (rescore / add / look-up / rename / drop) against a persistent id-keyed store, and carries a structured **resultSoFar** (the answer + `keyClaimIds` it rests on + open gaps + derivation + assumptions) wave to wave. Evidence itself is never in that struct — it lives only in the ledger, referenced by claim id.

**The brainer tree (`maxParallelBrainers`).** When a goal holds two or more genuinely independent investigations, a brainer can **spawn** a focused child onto one branch — a clean deep-copy of its store + memory, aimed by a mandate. The children run in parallel; the first whose answer the judge upholds wins (its report becomes `result.md`), a child may abandon a dead-end branch, every non-winner's partial is preserved, and every other brainer's non-retracted claims MERGE back into the winner's ledger before it finalizes. Default `1` (single brainer); raise to `2`–`5` for goals with several deep, separable branches.

**Adversarial by construction.** The agent that *derives* the answer is never the one that *approves* it. A per-wave validator catches a wave that quietly missed its goal; attack lanes and the refine pass hunt counter-evidence for every claim as it settles; a terminal Opus judge then tries to break the finished answer — chasing disconfirming and null results, funding / conflicts of interest, replication, and retractions — and can retract a claim outright before it ships.

**Multilingual.** When the subject is more active in another language, the prospector surfaces the native venues; the brainer routes the relevant lanes there; the reader translates the query into that language, reads the native results, and translates the findings back — carrying each source's original-language title alongside the English.

## Modes

| Mode | Trigger | What happens |
| --- | --- | --- |
| **goal** | `RR <question>` (default) | Satisficing — answers ONE question, stops when solid; the judge guards against stopping early |
| **collect** | `mode: 'collect'` | Exhaustive — inventories breadth until saturation (a landscape or roster) |
| **fast** | `rr fast <query>` | Skips the background run — a **Sonnet** lead nests parallel **Haiku** diggers down the rabbit-holes and answers inline, right now |

## Install anywhere

**Per-project** — clone, then copy the 3 runtime files in (`engine/` is dev-only, not needed at runtime):

```bash
git clone https://github.com/rezzminator/rr deep-rr
mkdir -p <project>/.claude/skills/deep-rr
cp deep-rr/{SKILL.md,workflow.js,persist.js} <project>/.claude/skills/deep-rr/
```

**Global** (every project on the machine) — same 3 files into `~/.claude/skills/deep-rr/` instead.

**Requirements:**

- Claude Code with the Workflow tool.
- The **Harvester** MCP server ([github.com/rezzminator/harvester-web-mcp](https://github.com/rezzminator/harvester-web-mcp)) connected — without it, every fetch errors and the run is snippet-only.
- `python3` with a scientific stack (scipy, sympy, uncertainties, pandas) for compute/derivation — optional, pass `compute: false` without it.

**Verify:** in Claude Code say `rr fast <any question>` (instant, no Workflow needed), then `RR <question>` for a full background run. Results persist to `RR/{slug}/`.

**Updating:** re-copy the 3 files from a fresh clone/pull — `SKILL.md`'s version pins compatibility.

## Launch + results

```
Workflow({ scriptPath: "<skill-base-dir>/workflow.js",   // absolute — the CWD may be a child project
           args: { query, mode, maxParallelBrainers, compute, computeNote, thinkerNote, researcherNote, tag, debug, debugPrompt } })
```

- `compute` defaults `true` — the master switch for all derivation; set `false` for a faster gather-only run.
- `maxParallelBrainers` defaults `1` — raise to `2`–`5` to let the brainer spawn focused children for independent investigations (the brainer tree).
- `thinkerNote` steers the Opus reasoning tier (priorities, framing, audience); `researcherNote` steers the web agents (which sources to favour); `computeNote` adds run-specific guidance for derivations.

The run returns its artifacts in the completion output; persist them with `node <skill-base-dir>/persist.js <output-file>` → `RR/{slug}/result.md` (read this first) plus `_claims.json` / `_claims.md` (the full claim ledger + nullAttacks), `_rabbitHoles.json`, `_tree.md`, and the per-wave/initiator/refinement/judge trail. With `debug: true` (default) it also writes `_debug.md`. A multi-brainer run additionally writes `_brainers.json` + `_brainers-tree.md` and each non-winning brainer's `result-<name>.md`. Crash-safety no longer writes a file: each wave logs a `⏺CKPT` recovery line straight into the workflow's live output (`checkpoint: true` by default), recoverable mid-run from the logs; the harness also journals every agent result natively and supports `resumeFromRunId` for a true resume.

## Build & development

`workflow.js` is a **build artifact** — never hand-edit it. The source is the modular TypeScript project under `engine/`, which bundles to a single self-contained file the Workflow sandbox can run:

```bash
cd engine
npm install
npm run build      # bundles engine/src/ → ../workflow.js, then validates the sandbox contract
npm test           # vitest: unit + integration coverage of the engine logic
```

Edit `engine/src/` — one directory per agent under `src/agents/<agent>/` (a `prompts.ts` template + an `index.ts` schema/tier) — then rebuild and commit both the source and the regenerated `workflow.js`.

## Key design decisions

- **The belief substrate is a typed ledger, not prose.** v2 kept evidence inside `resultSoFar` and rewrote it wholesale every wave; v3 moves evidence into an append-only, quote-pinned claim ledger owned by deterministic JS — the brainer references it by id (`keyClaimIds`), it never re-emits the facts themselves.
- **Independence is arithmetic, not an exhortation.** Corroboration counts lineage **clusters** (authors/funder/dataset, union-found by a sonnet clerk), not source URLs or a model's say-so — unknown lineage is guilty until proven independent (a shared cluster 0).
- **Confidence is computed, and only ever lowers.** A claim's status (settled/tentative/contested) and the run's final confidence are pure functions of ledger topology (clusters × attack-survival); a model may state a lower confidence for a reason, but the engine floors it — it can never talk itself up.
- **Compute steers the crawl mid-run, not just at the end.** The brainer authors a derivation once as a stored, seeded artifact; a haiku rerunner re-executes it every wave an input claim changes, and its variance decomposition — not intuition — ranks which leads are worth reading next (a value-of-information stop test).
- **A committed, sandbox-safe engine.** The bundle is a single self-contained file: `export const meta` at the top, no runtime imports, no non-deterministic globals — enforced by a build-time validator. The bundler is an acorn-AST pass, so it can never mis-strip an import or an export.
- **One brainer, delta-driven — that can fork.** A single Opus brain scores and steers an id-keyed rabbit-hole store via deltas and carries a structured running answer; for a goal with independent branches it can spawn focused child brainers (the brainer tree) that race to the first judge-upheld answer, merging their evidence back into the winner's ledger either way.
- **Separate the deriver from the judge.** A per-wave validator, attack lanes that hunt counter-evidence as claims settle, and a terminal Opus judge with retraction power pressure-test the work; none of them is the brain that produced it, so the answer is never self-certified.
- **Fetch through Harvester.** All fetching routes through the Harvester MCP — legal open-access resolution, PDF / book / image parsing, and a wall-bypass chain — so the readers reach primary literature, not just snippets.
- **Tiered models.** Haiku scout probes + readers + claimAuditor + rerunner; Sonnet scout planner/merger + scheduler + validator + refine + lineageClerk; Opus brainer / prospector / initiator / judge / synthesiser / debug-analyst.
- **Prompts as code.** Each agent is a `src/agents/<agent>/` module — a backtick prompt template plus its schema and tier; the build inlines them into the bundle.

## License

MIT

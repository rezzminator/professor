# RR — Engine Design

> How a run actually flows, stage by stage, and why each piece is built the way it is. Audience: anyone modifying `engine/src/` or debugging a run. The operator-facing interface lives in `SKILL.md`; this is the machinery.

## 0. The three invariants everything else serves

1. **Evidence is a ledger, not prose.** Every claim is pinned to a verbatim quote from a cached source, mechanically audited, and clustered by independent provenance. A claim's `status` (settled / tentative / contested) and the report's confidence are **computed from ledger topology — never asserted by a model**. Models may LOWER confidence, never raise it. This rule is repeated at the three places a model could plausibly override it (status computation, judge prompt, synthesiser prompt) because it is the difference between a research engine and a persuasive-essay generator.
2. **The engine owns every mutation; agents are pure request/response.** Every `bs.* =` and `files[name] =` write happens in `engine.ts`; every `agents/*/run.ts` returns data and touches nothing. One place to audit state transitions, one place where a dead agent's `null` has to be handled — which enables invariant 3.
3. **Degrade loudly, never destroy value.** Any single agent can die (terminal API error, safety-classifier block, structured-output failure). Each has a named fallback: a deterministic JS substitute, a skip-with-reopen, a drain with an honest `stopReason`, or — worst case — a loudly-bannered degraded report. The run finishes with the best deliverable the surviving evidence supports, labelled for exactly what it is. The only fatal path is a total scout wipeout (nothing to reason over at all).

## 1. Architecture at a glance

```
scout swarm ──► prospector ──► wave 0 (brainer scores the seeds)
                                   │
                     ┌─────────────▼──────────────┐   per wave:
                     │  brainer picks ≤5 lanes    │   scheduler → bin-pack → readers
                     │  (COORD deltas, never the  │   → claim ingest (audit ∥ lineage)
                     │   whole store)             │   → attacks/nullAttacks → derivation
                     └─────────────▲──────────────┘   rerun → validator → ⏺CKPT
                                   │ until stop.done / starved / dry / cap
                                   ▼
        finalize: initiator ──► grouped refine ──► judge (≤3 passes, may retract /
        derive / re-refine / reopen the crawl) ──► synthesiser ──► buildResult
```

- **Modules:** `engine.ts` (orchestration + all state mutation), `store.ts` (the id-keyed rabbit-hole frontier), `brainerState.ts` (one crawl branch's full state; deep-cloned on spawn), `runtime.ts` (retryAgent + logging), `config.ts` (all knobs, tiers, prompt fragments), `agents/*/` (one dir per agent: `run.ts` pure caller + `prompts.ts` template + `index.ts` tier/schema).
- **One shared ingest path:** wave-0 scout claims and every research wave's claims funnel through the identical `ingestClaimSeeds()`. Two code paths for the same bookkeeping would drift; the scout is simply "lane `'scout'`, wave 0".

## 2. Scout swarm — the wave-0 seed

Three stages replaced v2's single broad sweep, because one sweep anchored the whole run on whatever angle the first search happened to surface.

1. **Planner** (sonnet/high): runs 1–2 grounding WebSearches FIRST — the angle vocabulary "MUST come from what these searches actually surface — never from imagination" — then decomposes the query into 3–5 angles. Three slots are mandatory when applicable: **DIRECT**, **SKEPTIC** (counter-evidence hunting from minute zero — the ledger needs attack pressure before anyone is attached to an answer), **RECENT** (staleness check).
2. **Probes** (haiku/medium, parallel, one per angle): one WebSearch + ≤3 Harvester fetches each, all under the shared FOOTER contract (§5). Haiku deliberately — reading pages is the bounded worker job; a sonnet probe once crashed a run and bought nothing.
3. **Merger** (sonnet/high, no tools): folds surviving probes into one landscape and **names the tensions between angles** — "that tension is the single most valuable seed the next stage can receive — surface it explicitly, never bury it."

**Fallbacks:** planner dead → FALLBACK A, one probe on the naive query (v2 behavior). Merger dead → FALLBACK B, `mechanicalMerge()`, a deterministic JS dedupe-union. All probes dead → `throw 'scout died'` — the one legitimately fatal error; with zero pages there is nothing to degrade TO.

**What seeds the run:** the landscape (read-only global), every page's rabbit-holes (kind `seed`, unscored — the brainer scores them, not the probes: scoring is judgment), followed citations (kind `citation` — "never dropped on the floor just because there is no digest yet at wave 0"), and the probes' quote-pinned claims straight into the ledger.

## 3. Prospector — name the venues before the crawl

One opus/high call, after scout, before any brainer: name 6–8 **domain-specific** authoritative venues (arXiv/USENIX for GPU serving; SEC EDGAR for a stock; NOAA/ECMWF for weather), verified by real WebSearch — "memory alone misses recent venues." When a topic's literature is stronger in another language, venues carry a `lang` tag and `languageGuidance` steers lanes to native-language search (the readers translate back).

Why a separate agent: venue judgment is one global decision, not a per-lane one. The brainer then *assigns a venue subset per lane* in its COORD; the scheduler prefers assigned venues; and a per-venue yield tally warns the brainer off venues that produced nothing across ≥2 lanes (`⚠ 0 yield` suffix) — a feedback loop instead of a static list.

## 4. The brainer and the COORD contract

One opus/xhigh brainer is the run's only global reasoner — "ALWAYS Opus (measured: a Haiku brainer scored erratically + drifted off-goal)." It never re-emits the store; it returns **deltas**: `rescore` (only what changed), `add` (park for later), `lookupNext` (pursue NOW, each with a steering `note` + venue subset), `rename`, `drop` ("a MERGE = drop the duplicate, rescore the survivor"), optional `spawn` (≤1/wave, tree mode), optional `derivation` (authors the stored Python artifact), and `stop {done, reason}`. Deltas because the store is engine state (invariant 2): the model expresses intent, code applies it in a fixed order (rename → drop → rescore → add) so a rename can never race its own rescore.

**The COORD schema is built per call, not shipped static** (`buildCoord({compute, canSpawn})`, v3.0.2). The platform classifier that screens agent spawns rejects oversized output schemas — "output schema too large to classify safely," observed killing wave-0 brainers live, deterministically, across models and sessions. So the contract prunes what the call cannot use: `derivation` ships only when compute is on, `spawn` only on a wave allowed to spawn (the prompt's spawn/derivation clauses are conditional on the same flags, so prompt and schema always agree), the deprecated `evidence` brick is gone from `resultSoFar` (no code read it), and schema descriptions stay terse — steering prose lives in the prompt. Serialized: full shape 6,234 → 5,311 chars; a compute-off single-brainer wave ships 3,966. `COORD` (the full shape) stays exported as the canonical contract for types and tests.

`resultSoFar` is the run's living memory, re-emitted every wave and keyed to `keyClaimIds` — the ledger rows the answer currently rests on. That keying is what makes confidence computable (§7): the engine knows exactly which claims are load-bearing.

Lane selection is **calibration-weighted**: each lead-kind (seed/gap/citation/attack/entity/origin) carries an EMA of predicted-vs-realized yield; the sort key is `score × calibFactor(kind)` clamped to [0.5, 1.5]. Selection only — stored scores are never touched. The brainer's optimism about, say, citation-leads gets tempered by what citation-leads actually delivered *in this run*. The lane cap itself is **hidden** from the brainer (JS clamps to 5) so it ranks honestly instead of gaming a quota.

## 5. A research wave, end to end

1. **Scheduler** (sonnet/high, ONE call for the whole wave): batches every lane's searches in one parallel round, then sizes every candidate via `harvester fetch size_only` in a second parallel round, returns chosen sources per lane. Batched because discovery is I/O-bound: N sequential per-lane agents would multiply latency for zero judgment gain. Hallucinated lane ids are dropped in code. The prompt now carries a deduplicated VENUE LEGEND (venues by number, never repeated per lane), mandatory suspect-surface sourcing for attack lanes, a REFETCH contract for corrupted-cache lanes plus a corrupted-cache exclusion list, and an identical-payload sanity rule (two urls returning the same `{size, chars}` is a poisoning signature — the engine also detects and flags this independently). Per-lane honesty returns: `venuesServed` (assigned-vs-served reconciliation → `venueStats.served`) and `unsourced` (directive-named refs that could not be fetched); both flow into the next brainer prompt as the SCHEDULER REPORT clause (`bs.lastUnsourced`). A prospector venue never assigned by wave 2 earns a `⚠ never assigned` suffix (`CONFIG.VENUE_UNROUTED_MIN_WAVE`).
2. **Bin-packing** (pure code): each lane's sources pack into ≤130k-token reader-units — small sources combine (≤8 per unit), an oversized source splits with a 2,000-char overlap so every split makes forward progress. The budget is asserted against the reader tier's real context window at construction ("fail loudly otherwise"). Why code: packing is arithmetic; an LLM packing tokens would be paying judgment prices for a knapsack loop.
3. **Readers** (haiku/medium, one *sequential thread per lane*, parallel across lanes): each reads its slices off the local Harvester cache, carries a running answer to the next reader (clipped to 16k chars — handoff hygiene, no tool-call serialization), and emits quote-pinned claims under the FOOTER contract: rabbit-holes (gap searches), next sources (followed citations), claims, new vocabulary, surprise. The reader schema now REQUIRES `claims`/`rabbitHoles`/`deadEnds` (empty allowed) so a degraded reader cannot silently omit the channels the reopen machinery watches. A reader that hits poisoned cache content reports it via a `CORRUPT: <cachePath>` deadEnd — the engine quarantines the path (scheduler exclusion list + `knownCachePaths` removal), and the brainer may set `refetch:true` on the lane. **B3 discipline:** if ANY reader in a lane dies, the whole lane returns null — "never emit a confident summary that silently dropped a chunk." The validator reopens null lanes.
4. **Ledger ingest** (code, nine ordered steps): sanitize (strip structured-output artifacts that once bled `</parameter>` tags into findings) → claim ingest (dedupe by quote+source; hallucinated stance targets dropped — a stance may only point at a claim that existed before this batch) → **claimAuditor** ∥ **lineageClerk** → status recompute → attack bookkeeping → vocabulary merge → yield calibration → venue tally → Chao1 (collect mode).
   - **claimAuditor** (haiku, batched 50, concurrent): mechanically greps the cache file for the verbatim quote — "you are grepping for a pin, not judging truth." The audit is NORMALIZED before matching (unicode dashes/curly quotes/the ellipsis character/NBSP/markdown emphasis folded, lowercased, whitespace collapsed) with ordered ellipsis-fragment matching, and can REPIN: when the quote as sent is broken but a contiguous span carrying the claim exists in the file, the auditor returns verdict `repinned` + `newQuote`, and the engine replaces the quote and records audit `pass` (`metrics.quotesRepinned`). A repin without a `newQuote` degrades to `fail`. Claims whose `cachePath` is untrusted (never scheduler-returned, outside the harvester `.fetch` cache signature) are stripped to `unpinned` at ingest (`metrics.cachePathsRejected`). Dead auditor → claims stay `pending` (treated unpinned; they can never reach settled).
   - **Stance ingest:** target ids are coerced at ingest — prose like `'c36 (…)'` is parsed down to `36` — and the stance sub-schema now requires both `target` and `kind`. An attack-kind brainer lane must name the target c-id in its lane note so `nullAttacks` can link back to what it failed to hit.
   - **lineageClerk** (sonnet, batched 80): canonicalizes provenance entities into stable keys ("the SAME real-world entity spelled differently must map to the SAME key") — the one clerk promoted off haiku because fuzzy entity resolution is judgment, not grep. The actual clustering is a persistent union-find in code; a later claim can retroactively merge two clusters, and every member is rewritten. **Corroboration counts clusters, not sources** — ten articles reheating one press release are one voice.
   - **Attack lanes:** an attack-kind lane that lands a counter-claim marks the target contested; one that lands *nothing* records a `nullAttack` — "a completed counter-search that found nothing is first-class state, not silence." Survived challenges are what let a claim settle (§7).
5. **Derivation + rerunner** (compute mode): the brainer authors ONE stored, seeded Python artifact `{code, inputs}`; a haiku/low **rerunner** re-executes it whenever an input claim changed — "NEVER repair or rewrite it, even to fix an obvious bug: a broken artifact is the brainer's problem, not yours" (repair would fork the artifact away from what the brainer reasons about). Its variance decomposition feeds the brainer a sensitivity ranking — the value-of-information stop test: stop chasing inputs the answer is insensitive to.
6. **Validator** (sonnet/medium, conditional — runs only when a lane died or a finding is thin): the per-wave coverage gate, distinct from the terminal judge. Failed lanes reopen at most twice (`MAX_LANE_REFAILS`), then surface as a capped known-gap instead of looping forever. Its `missing` list threads into the next brainer prompt. The gate now ALSO fires on corrupt-cache reports and on lanes with `deadEnds` but zero claims, and sees each lane's `deadEnds`.
7. **⏺CKPT** (code, one log line): the wave's final state — open frontier, running answer, load-bearing ledger shape, derivation quantiles — into the workflow's live output. Zero agents, off the critical path, excluded from the debug log so checkpoint spam never bloats `_debug.md`. Recovery = grep the output for the last `⏺CKPT`.

## 6. Stop conditions (priority order)

`brainer-done` (the satisficing signal) → `scheduler-starved` (2 consecutive waves with zero usable sources — "break with an explicit stopReason instead of grinding to HARD_CAP with nothing to read"; an all-null wave whose schedule HAD sources is a reader failure and belongs to the validator path, which this guard must not preempt) → `rabbithole-empty` (labelled before the plateau so an empty store never masquerades as saturation) → `collect-dry-plateau` (collect only: ≥3 research waves, last 2 top-scores ≤ 70% of peak, computed over research waves only so the inflated wave-0 seed score never masks a plateau — **and** gated by Chao1: a plateau alone does not stop the crawl while estimated coverage < 0.9, because "novelty pausing" and "nothing left to find" are different states) → `wave-cap` (ran out of waves, not leads) → `brainer-dead` (§9).

## 7. The claim ledger — status and confidence are computed

- **contested**: an unretracted attack targets it (or `counter` set).
- **tentative**: default; also forced by `audit: fail` — but the audit override is checked AFTER contested, so a still-attacked audit-fail claim reads contested, not tentative.
- **settled**: ≥2 independent clusters support it AND (≥1 survived challenge OR ≥3 clusters). Survived = max(attacksSurvived, matching nullAttacks) — max, not sum, so one challenge recorded twice never double-counts. Cluster 0 (unknown lineage) counts at most once no matter how many claims sit in it.
- **Confidence** over `keyClaimIds` only: any key claim audit-failed/retracted → low ("the answer rests on a disproven pin"); any contested → low; all settled → high; else medium. The synthesiser's stated confidence is **floored** by this computed value, with a visible note when lowered.
- **Retraction** (judge-only): real ids only, recomputes every downstream status, refreshes a derivation whose input died (one bounded rerun), and citation-linting strips any `[cN]` in the report whose id is no longer live. `lintCitations` now ALSO strips any `[cN]` whose claim audit reads `fail` (`metrics.citationsAuditFailed`) — a citation may never wear the authority of a failed pin; the synthesiser is told the same.
- **Metrics surface:** the run's metrics accumulate `auditCounts`, `quotesRepinned`, `cachePathsRejected`, `citationsAuditFailed`, `venuesUnrouted`, `goalMet`, `judgePasses`, and `reopenedLanes` — every counter this section and §5/§8 refer to above lands here for the debug analyst and the completion summary. A parallel artifact, `_sources.json`, lists every cache file a live claim actually pins to (claim-referenced, never a raw cache dump); `persist.js` mirrors those into `RR/{slug}/resources/` on the host, and writes an auditable tombstone under `RR/_aborted/` instead of erroring out when a run's output can't be parsed or ships no files.

## 8. Finalize — initiator → refine → judge → synthesiser

- **Initiator** (opus/xhigh): names the load-bearing facts, *aggressively grouped* — "bundle facts that share sources or stand or fall together into ONE item" — because refine cost scales per group and atomized facts re-verify the same source N times. Also sets the report focus.
- **Refine** (sonnet, one per group, parallel, single dispatch path `refineAndLedger`): adversarially hunts counter-evidence per group and records every counter-search query verbatim — "the record of the attack matters as much as its outcome." Outcomes fold into the ledger exactly like crawl attack lanes (counter → contested; nothing → nullAttack). One dispatch path so no refine call can bypass the ledger bookkeeping. A `counterFound` outcome is now MECHANICALLY appended to `resultSoFar.answer` and `tensions` before the judge runs — a single-point-of-failure removal: the judge no longer has to notice a synthesis/refine contradiction on its own.
- **Judge** (opus/xhigh, ≤3 passes): the terminal skeptic — four strict booleans (goalMet, verificationSound, needsCompute, computeSound), sees the challenged-vs-never-challenged split and the leftover open rabbit-holes, and must **reconcile the crawl's stop reason**: "if that reason names remaining work, either return reopenRabbitHoles for it or explicitly justify the override — never silently convert remaining work into a caveat." Its three levers, in order: brain-compute derive (code-capable brainer writes + runs the derivation, propagates error bars, self-checks a second way) → refine recheck (directive threads into every group) → crawl reopen (rare; the reopened wave's harvest flows into the ledger exactly like a normal wave — a v2 weak point where reopened reads were mostly discarded). A reopen may carry a `reopenDirective` — the reader-facing extraction directive for the reopened lane, distinct from the refine-facing `directive`. When no remediation lever fits, the directive is forwarded into the synthesiser focus instead of silently dropped, and the log says so honestly; the judge gates quality, not delivery. `goalMet` and the pass count (`judgePasses`) land in metrics.
- **Synthesiser** (opus/xhigh, always runs): writes the 8-section report from the hardened facts + verbatim derivation + final resultSoFar; cites ledger ids inline (`[c12]`); citations linted, confidence floored (§7).

## 9. Resilience — the null economy

`retryAgent` wraps every agent call (2 retries): a transient structured-output failure often clears on a clean re-spawn. **The harness resolves terminal API errors (safety-classifier blocks, mid-run skips) to `null` without throwing** — so a null return is routed through the SAME retry ladder as a thrown error; otherwise the ladder never engages on exactly the failure class it exists for. A borderline classifier block is often probabilistic; a fresh spawn frequently passes. Only after exhaustion does the call degrade to null — and then the engine's per-agent fallback table takes over:

| dead agent | fallback |
| --- | --- |
| scout planner | single direct probe on the naive query (v2 behavior) |
| scout probe | dropped; ALL probes dead → fatal (`scout died`) |
| scout merger | deterministic JS merge |
| prospector | no venues → general search |
| brainer, wave 0 | crawl drained, `stopReason: brainer-dead` → finalize runs on scout material, report ships under a **DEGRADED RUN** banner |
| brainer, mid-crawl | loop breaks; last good coord stands; normal stop classification |
| scheduler | starved wave (→ starved-stop after 2) |
| any reader in a lane | whole lane null → validator reopen (≤2), then capped known-gap |
| claimAuditor | claims stay `pending` (never settle) |
| lineageClerk | deterministic key: funder ∥ venue ∥ source domain |
| rerunner | last good run kept, marked stale |
| validator | no reopen judgment; raw lane failures still reopen |
| initiator | zero facts → finalize proceeds unhardened |
| refine group | `(refine failed)` marker; ledger untouched for that group |
| judge | treated as "no actionable remediation" → report proceeds |
| synthesiser | **salvage result.md**: the running answer + open gaps under a DEGRADED REPORT banner — never a run with no deliverable |
| debug analyst | placeholder narrative; raw metrics/log still written |

Design rule behind the whole table: **a report the operator must distrust-by-reading beats no report, IF AND ONLY IF the degradation is announced at the top.** Both degraded banners exist because the failure mode they replace was worse: a fully-judged report destroyed by a null-deref at the finish line.

## 10. The brainer tree (opt-in, `maxParallelBrainers` 2–5)

The single-brainer path is the proven default and is left untouched; the tree is a separate orchestration. A brainer may `spawn` one focused child per wave (mandate-scoped, depth ≤3, cap-guarded, processed sequentially post-wave so caps are race-free). The child deep-clones the parent's entire state (zero shared refs) and the parent **releases** the delegated lead — divided frontier, no double-pursuit. A brainer that declares done fires a speculative **gate** (initiator → refine → judge, no synthesiser) without pausing the others; the FIRST upheld gate wins, triggers one last collect-don't-regate wave for everyone, and — the load-bearing merge — **every other brainer's non-retracted claims fold into the winner's ledger before it finalizes** (fresh ids, re-clustered through the winner's union-find; stances dropped because their target ids are meaningless across ledgers). A losing branch's evidence reaches the report; only its narrative dies. Loser states persist as `result-<name>.md`.

## 11. Model tiering — why each agent sits where it sits

| tier | agents | rationale |
| --- | --- | --- |
| opus/xhigh | brainer, initiator, judge, synthesiser | global judgment: scoring, grouping, skepticism, synthesis — "measured: a Haiku brainer scored erratically + drifted off-goal" |
| opus/high | prospector | one global venue decision, domain expertise |
| sonnet/high | scout planner, scout merger, scheduler | mid-weight judgment: decomposition, tension-naming, source-value triage + batched tool I/O |
| sonnet/medium | validator, lineageClerk | bounded checks; entity resolution is judgment-not-grep (the one clerk promoted off haiku) |
| haiku/medium | scout probes, readers, claimAuditor | bounded, mechanical, high-volume; "a SONNET researcher crashed the vector-DB run" and bought nothing |
| haiku/low | rerunner | pure re-execution, forbidden to repair |

## 12. Known discrepancies (state, not folklore)

- `⏺CKPT` recovery is read by humans/resuming agents from the live output stream; the engine itself never reads a checkpoint back (resume rides the harness's own agent-call cache instead).

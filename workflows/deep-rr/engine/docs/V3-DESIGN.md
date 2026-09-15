# RR v3 — the claim ledger engine

The v3 thesis, from a blind-design tournament (6 independent ground-up designs, 3 adversarial judges): **the v2 chassis is right — the belief substrate is wrong.** v2 crawls with a typed, delta-driven frontier but *believes* in prose: evidence lives inside `resultSoFar` and is rewritten wholesale by the brainer each wave, independence is a prompt exhortation, confidence is asserted, and computation arrives only at finalize — too late to steer. v3 keeps the chassis verbatim and replaces the nervous system.

Six mechanisms, in convergence order (how many of the 6 blind designs independently invented each):

1. **Quote-pinned claims + mechanical audit** (6/6) — evidence becomes an append-only ledger of claims, each pinned to a verbatim quote + cache file; a haiku auditor greps the cache to verify.
2. **Computed independence** (3/6 + all judges) — corroboration counts *lineage clusters* (authors/funders/datasets union-found), not source URLs. Unknown lineage = one shared cluster (guilty until proven independent).
3. **Compute in the loop** (4/6) — the derivation is authored once as a stored, pure, seeded Python artifact; a haiku rerunner re-executes it each wave; its variance decomposition re-scores the frontier; a VOI test defines done.
4. **Computed confidence, lower-only** (6/6) — confidence is a deterministic function of ledger topology; models may lower it with a reason, never raise it.
5. **NullAttacks** — a counter-search that finds nothing is first-class state ("challenged and survived" ≠ "never challenged").
6. **The 6-channel footer** — gaps (in the source's own vocabulary), citations (with expected stance), attack queries, lineage leads, entities/terms, surprise flag.

Judges' hard warnings honored throughout: keep the central brainer; keep the reading substrate (size-probe → bin-pack → honest read → null lanes) verbatim; keep multilingual prospecting; keep the validator; every graft degrades to null (its agent dying reverts behavior to v2); every new loop carries a named cap.

## Naming (canonical, everywhere)

| Concept | Name |
| --- | --- |
| One quote-pinned fact (the evidence unit) | **claim** (`Claim`, ledger `bs.claims`) |
| Independence group of a claim's provenance | **cluster** (`claim.cluster`) |
| Completed counter-search that found nothing | **nullAttack** (`bs.nullAttacks`) |
| Stored seeded Python artifact + latest run | **derivation** (`bs.derivation`) |
| Predicted-vs-realized lead-yield table | **yieldCalib** (`bs.yieldCalib`) |
| A lead's origin channel | **kind** (`RabbitHole.kind`) |
| Community terms-of-art store | **vocabulary** (`bs.vocabulary`) |

## State (BrainerState additions — all cloned in spawnBrainer)

```ts
claims: Claim[]                    // append-only; JS assigns ids c1..; never rewritten by an LLM
// Claim = { id: number, claim: string, value?: string, quote: string, source: string,
//   cachePath?: string, entities?: { authors?: string[], funder?: string, dataset?: string, venue?: string },
//   cluster: number,              // union-find root; 0 = the shared "unknown lineage" cluster
//   audit: 'pending'|'pass'|'fail'|'unpinned',   // unpinned = no cachePath to grep (e.g. search-snippet claim)
//   status: 'settled'|'tentative'|'contested',   // COMPUTED (utils/claimStatus), never asserted
//   stance?: { target: number, kind: 'supports'|'attacks' },  // when the claim bears on an existing claim
//   attacksSurvived: number, counter?: string, retracted: boolean, wave: number, lane: string }
nullAttacks: NullAttack[]          // { topic: string, claimIds: number[], queries: string[], wave: number, phase: string }
vocabulary: Term[]                 // { term: string, gloss?: string, uses: number }
derivation: Derivation | null      // { code: string, inputs: DerivInput[], lastRun: { quantiles: Record<string,number>, sensitivity: Record<string,number>, wave: number } | null }
// DerivInput = { name: string, dist: string, claimIds: number[], prior: boolean }  // prior=true ⇒ wide-prior placeholder, not evidence-backed
yieldCalib: Record<string, { n: number, ratio: number }>   // per RabbitHole.kind; EMA of realized/predicted
```

`resultSoFar` change: the `evidence` array is REMOVED from the brainer contract (the ledger owns evidence); the brainer instead returns `keyClaimIds: number[]` — the claims the answer currently rests on. All other resultSoFar fields stay (answer, assumptions, resolved, openGaps, tensions, working, confidence). `RabbitHole.kind: 'seed'|'gap'|'citation'|'attack'|'entity'|'origin'|'inject'`.

## New agents (each with tier/effort in CONFIG, null-tolerant)

| Agent | Tier | Job |
| --- | --- | --- |
| **claimAuditor** | haiku | Batched per wave: for each new claim with a cachePath, verify (Bash/python grep) the quote EXISTS in the cache file and carries the claim on its own → `{id, verdict: pass\|fail, note?}`. Dies → claims stay `pending` (treated as unpinned downstream). |
| **lineageClerk** | haiku | Batched per wave: canonicalize new claims' entities against the known entity list → same-as links; JS union-finds clusters. Dies → deterministic JS fallback: cluster by `norm(funder \|\| venue \|\| source-domain)`; claims with nothing resolvable → cluster 0 (shared unknown). |
| **rerunner** | haiku | Execute `bs.derivation.code` (pure, seeded) with current inputs via Bash python3 → `{quantiles, sensitivity}`. Contract: script reads one JSON arg, prints one JSON result. Dies → lastRun kept stale; brainer told it is stale. |

## The 6-channel footer (CONFIG.FOOTER + reader/scout schemas)

Readers return alongside `runningAnswer`:

- `claims[]` — `{claim, value?, quote (verbatim, ≤QUOTE_MAX_CHARS), source, entities{authors,funder,dataset,venue}, stance?{target,kind}}`. The reader prompt includes a compact digest of existing key claims (`#id claim` one-liners, ≤CLAIM_DIGEST_CAP) so stances can target them.
- `rabbitHoles[]` — `{keyword, why, kind: 'gap'|'attack'|'entity'}`; gaps phrased in the SOURCE'S OWN vocabulary; for each claim the page supports, the single strongest realistic counter-evidence search as kind 'attack'.
- `nextSources[]` — `{ref, why, expect: 'support'|'attack'|'neutral', target?: claimId}`.
- `newTerms[]` — `{term, gloss?}` (community terms of art).
- `surprise?` — string, only when the page contradicts the current key claims.

## Per-wave engine order (ONE shared helper used by runCrawl AND runOneWave — kill the duplication)

schedule → lane readers → collect claims (JS assigns ids, dedups near-identical quotes) → parallel(claimAuditor batch, lineageClerk batch) → JS: union-find clusters, compute statuses, update chao + yieldCalib + vocabulary → rerunner (iff derivation && inputs changed) → validator (unchanged) → brainer (new prompt sections) → applyDeltas + store derivation updates → attack-lane bookkeeping (lane kind 'attack' with no counter-evidence → nullAttack; with counter → target claim contested + `counter` set).

checkpoint = a ⏺CKPT log line, zero-cost — no agent. Logged last, after applyDeltas, so it captures the wave's final state (CONFIG.checkpoint gates it; default true).

**Status (utils/claimStatus, pure):** `fail`/`retracted` → excluded. `contested` iff an unretracted attacking claim targets it. `settled` iff supporting clusters ≥ SETTLED_MIN_CLUSTERS AND (attacksSurvived ≥ 1 OR clusters ≥ SETTLED_MIN_CLUSTERS + 1). Else `tentative`.

**Computed confidence (utils/computedConfidence, pure):** over keyClaimIds — all settled → 'high'; any contested → 'low'; else 'medium'. Models may LOWER (final = min(model, computed)), never raise.

**yieldCalib:** per pursued lead — predicted = score/100; realized = (auditedPassClaims + 0.3 × freshLeads) / CALIB_NORM, clamped [0,2]. EMA per kind (α=0.3). Applied in resolveLookupNext as a sort-key multiplier clamp(ratio, CALIB_CLAMP_LO, CALIB_CLAMP_HI) — selection only, scores untouched. Rendered to the brainer as a CALIBRATION section.

**Chao1 (collect mode):** species = claim groups (Jaccard near-dup over `norm(claim)`); abundance = distinct sources per group. unseen ≈ n1²/(2·n2) (n2=0 ⇒ n1(n1−1)/2). coverage = groups/(groups+unseen). Reported in metrics + report; assists the dry stop: plateau AND coverage ≥ CHAO_COVERAGE_STOP → dry.

**VOI stop assist (goal mode, derivation present):** rendered to the brainer: "no open lead targets a derivation input with sensitivity > VOI_SENS_THRESHOLD and the goal is answered ⇒ set done".

## Brainer changes

Prompt gains: LEDGER digest (`#id [status·clusters·audit] claim = value` lines) replacing the old evidence memory; CALIBRATION section; SENSITIVITY section (variance shares + which inputs are priors, stale flag); attack discipline ("every keyClaim still tentative and never-attacked → originate one kind:'attack' lane"); VOI clause. Schema: resultSoFar drops `evidence`, gains `keyClaimIds`; new optional `derivation` delta `{code, inputs}` — author once for derive-questions, re-emit only to change it; engine stores + reruns it (replaces v2's inline COMPUTE TO STEER).

## Finalize changes

- **refiner** schema += `queriesTried: string[]`, `counterFound: boolean`, `counterNote?` → nullAttacks (or contested marks). Refiner receives the claim's quote + source, not just fact text.
- **initiator** reads the ledger; when a derivation exists, JS pre-ranks facts by sensitivity share.
- **judge** sees nullAttacks (challenged-and-survived vs never-challenged), computed confidence, status counts; a discredited claim → `retracted` → JS recomputes statuses/confidence → if it was a derivation input, ONE rerunner call (bounded inside the existing MAX_JUDGE_PASSES loop).
- **synthesiser** cites claim ids `[c12]` for load-bearing facts; JS lints ids against the ledger (unknown ids stripped + logged); final confidence = min(stated, computed). New artifacts: `_claims.json` + `_claims.md` (the ledger with provenance chains + nullAttacks), `_derivation.md` (+ the .py), `_vocabulary.md`.

## v2 weak-point fixes riding along

- `reopenCrawl` now harvests: applyDeltas + fresh rabbitHoles/nextSources from reopen readers.
- `runOneWave` newRabbitHoles no longer hardcoded 0; child plateau slices from its spawn baseline (record `topScoresBase` at spawn).
- runCrawl/runOneWave wave-body duplication collapsed into the shared helper.

## Config additions

`TIER/EFFORT` for claimAuditor, lineageClerk, rerunner. Knobs: `QUOTE_MAX_CHARS=300`, `CLAIM_DIGEST_CAP=30`, `SETTLED_MIN_CLUSTERS=2`, `VOI_SENS_THRESHOLD=0.15`, `CALIB_CLAMP_LO=0.5`, `CALIB_CLAMP_HI=1.5`, `CALIB_NORM=4`, `CHAO_COVERAGE_STOP=0.9`, `checkpoint=true` (arg-controllable), `CHECKPOINT_MARK='⏺CKPT'`. Version → 3.0.0.

## Non-negotiables (from the judges)

- The reading substrate (scheduler size-probe, dual-dimension bin-packing, sequential capped handoff, honest read, null lanes), multilingual prospecting, validator reopen, degrade-to-null retries, bounded loops, prompt logging: **kept verbatim**.
- No unbounded cascades: replay/rerun is bounded by the judge-pass loop; audits/clerk are batched (one call per wave each).
- Every new mechanism has an explicit null path back to v2 behavior.

## Status

Implemented in full at v3.0.0 — 404 vitest tests green (`cd engine && npm test`). Both review HIGHs from the implementation pass are fixed: `claimStatus` and `computedConfidence` (`engine/src/utils/index.ts`) carry the subject-audit guard — a claim the auditor actively failed can never read as `settled`/high- confidence no matter how many clusters back it, checked ahead of the cluster count; and a spawned child's collect-mode plateau window is sliced from its own `topScoresBase` (recorded at spawn, `brainerState.ts`), not the parent's inherited `topScores` history, so a child's dry-stop judges its own waves only.

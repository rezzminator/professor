import { CONFIG } from './config.js';
import {
  norm,
  clip,
  lab,
  padIdx,
  lastScore,
  trailOf,
  withPrompt,
  waveMd,
  resultSoFarMd,
  laneCount,
  scrubArtifacts,
  scrubEntities,
  claimsMd,
  claimStatus,
  lineageKeyOf,
  chao1,
  updateCalib,
  topSensitivityInput,
  computedConfidence,
  minConfidence,
  lintCitations,
  compactCheckpoint,
} from './utils/index.js';
import { brainer, refiner } from './agents/index.js';
import { runScout } from './agents/scout/run.js';
import { runProspector } from './agents/prospector/run.js';
import { runBrainer, runBrainerCompute } from './agents/brainer/run.js';
import { runValidator } from './agents/validator/run.js';
import { runScheduler } from './agents/researchScheduler/run.js';
import { runResearchers } from './agents/researcher/run.js';
import { runInitiator } from './agents/initiator/run.js';
import { runRefine } from './agents/refiner/run.js';
import { runJudge, COMPUTE_LIMIT_PREFIX } from './agents/judge/run.js';
import { runSynthesiser } from './agents/synthesiser/run.js';
import { runDebug } from './agents/debugAnalyst/run.js';
import { runClaimAuditor } from './agents/claimAuditor/run.js';
import { runLineageClerk } from './agents/lineageClerk/run.js';
import { runRerunner } from './agents/rerunner/run.js';
import {
  addRabbitHole,
  applyDeltas,
  resolveLookupNext,
  pursue,
  reopenRabbitHole,
  nearDup,
} from './store.js';
import { BrainerState, spawnBrainer } from './brainerState.js';
import type {
  Claim,
  ClaimSeed,
  CleanReport,
  Coord,
  FactToHarden,
  Files,
  Finding,
  JudgeOut,
  LaneRecord,
  Metrics,
  RabbitHole,
  RabbitHoleOut,
  RabbitHoleSeed,
  RefineOut,
  ReportOut,
  ResearchOut,
  ResultLogEntry,
  ResultSoFar,
  RunResult,
  SchedulerSource,
  ScoutOut,
  SeedLead,
  StopReason,
  TermSeed,
  ValidatorLogEntry,
  Venue,
  WaveLogEntry,
} from './types/index.js';

log('▶ RR START · mode=' + CONFIG.mode + ' · maxWave=' + CONFIG.maxWave + ' · dir=' + CONFIG.DIR);

// compact "Run arguments" record — the COMPLETE launch args (CONFIG.rawArgs), verbatim as received. Surfaced at the top of result.md
// (and persisted as the `args` object in _rabbitHoles.json) so every output file records exactly how the run was launched.
const runArgsMd = (): string => '> **Run arguments:** `' + JSON.stringify(CONFIG.rawArgs) + '`\n\n';

// idsInText — recover the claim ids an attack-lane's own note/why text names (the brainer/reader reference
// an existing claim the same way the ledger digest renders it: `c12`). Filters to ids that are actually in
// the ledger — never fabricates one — so a nullAttack's claimIds degrades to [] when nothing is recoverable.
const idsInText = (text: string, validIds: Set<number>): number[] => {
  if (!text) return [];
  const found: number[] = [];
  for (const m of text.matchAll(/\bc(\d+)\b/g)) {
    const id = Number(m[1]);
    if (validIds.has(id) && !found.includes(id)) found.push(id);
  }
  return found;
};

// ─────────────────────────────────────────────────────────────────────────────
// ResearchReport — the pipeline backbone. Holds the crawl state; each phase method (runCrawl / runFinalize /
// reopenCrawl / buildResult / run) orchestrates the loop and owns every `files[name] = …` write. The per-agent
// work (buildPrompt → retryAgent → artifact) lives in each agent's src/agents/<agent>/run.ts; the store reducers
// live in store.ts; the shared agent caller (retryAgent + the debug I/O buffers) lives in runtime.ts.
// ─────────────────────────────────────────────────────────────────────────────
export class ResearchReport {
  // ── run globals — set ONCE by the scout + prospector before any brainer exists, then shared read-only
  // (by reference) into every BrainerState. Nothing mutates these during the crawl. ──
  scout: ScoutOut | null;
  scoutRabbitHoles: SeedLead[];
  highValueSources: Venue[]; // prospector's high-value source venues the brainer assigns per lane
  languageGuidance: string; // prospector's non-English routing note (''=English-dominated)
  sourcesReasoning: string;
  laneRecords: LaneRecord[]; // debug: per-lane reader feed (venue-utilization analysis) — a GLOBAL sink across all brainers

  files: Files; // every output artifact — written ONLY by the engine
  liveBrainers: BrainerState[]; // the brainer tree: the root + every spawned child (one entry when maxParallelBrainers=1)
  winner: BrainerState | null; // the first brainer whose speculative gate the judge upheld — owns result.md
  lastWaveTriggered: boolean; // a gate passed → run ONE more global wave (wrap-up), then drain

  constructor() {
    this.scout = null;
    this.scoutRabbitHoles = [];
    this.highValueSources = [];
    this.languageGuidance = '';
    this.sourcesReasoning = '';
    this.laneRecords = [];
    this.files = {};
    this.liveBrainers = [];
    this.winner = null;
    this.lastWaveTriggered = false;
  }

  // SCHEDULER (B4) — thin delegator to runScheduler (researchScheduler/run.ts); the crawl + finalize reopen route
  // their source discovery through here. Returns a Map<lane id, sources> the engine bin-packs into reader-units
  // (the external signature is unchanged so both call sites stay untouched); internally it also folds runScheduler's
  // honesty side-channels into bs: the scheduler's own unsourced report, the served-cache-path allowlist
  // (knownCachePaths — the ingest cachePath-trust check reads this), the assigned-vs-served venue reconciliation,
  // and a mechanical cache-poisoning tripwire (the Zur spam incident: two different URLs, byte-identical spam
  // payload, flowed into three lanes undetected — a same-size+same-chars group spanning ≥2 distinct urls is
  // near-certainly one payload served under two names, so it is surfaced loudly rather than silently trusted).
  async scheduleSources(
    bs: BrainerState,
    picks: RabbitHole[],
    tag: string,
    phaseName: string,
  ): Promise<Map<number, SchedulerSource[]>> {
    const res = await runScheduler(bs, picks, tag, phaseName);
    bs.lastUnsourced = res.unsourced; // each wave overwrites; '' clears a prior wave's report
    for (const srcs of res.schedule.values())
      for (const s of srcs || []) if (s && s.path) bs.knownCachePaths.add(s.path);
    for (const p of picks) {
      const served = res.venuesServed.get(p.id) || [];
      for (const src of p.sources || []) {
        const entry = bs.venueStats[src] || (bs.venueStats[src] = { assigned: 0, yielded: 0, served: 0 });
        if (served.includes(src)) entry.served++;
      }
    }
    // IDENTICAL-PAYLOAD DETECTION — flatten every scheduled source across all lanes; a group sharing the exact
    // same size+chars pair that spans ≥2 DISTINCT source urls is the cache-poisoning signature (one payload,
    // multiple names) — never inferred from a single lane alone, since that could be a legitimate re-cited source.
    const bySize = new Map<string, SchedulerSource[]>();
    for (const srcs of res.schedule.values())
      for (const s of srcs || []) {
        const key = s.size + '|' + s.chars;
        const g = bySize.get(key);
        if (g) g.push(s);
        else bySize.set(key, [s]);
      }
    for (const g of bySize.values()) {
      const distinctSources = [...new Set(g.map((s) => s.source))];
      if (distinctSources.length >= 2) {
        const urls = distinctSources.join(', ');
        log('  ⚠ identical payloads across distinct urls (cache-poisoning signature): ' + urls);
        bs.lastUnsourced =
          (bs.lastUnsourced ? bs.lastUnsourced + '\n' : '') + '⚠ identical payloads: ' + urls;
      }
    }
    return res.schedule;
  }

  // ═══════════════════════════════════════════════════════════════════════════════════════════════
  // CLAIM-LEDGER INGESTION (v3) — the ONE shared pipeline both wave paths (runCrawl / runOneWave) and
  // the wave-0 scout seed feed through. Order (mirrors V3-DESIGN.md "Per-wave engine order"): sanitize →
  // claim ingest → audit ∥ lineage → status → attack bookkeeping → vocabulary → surprise → yieldCalib →
  // chao. Every agent in the middle (claimAuditor/lineageClerk) degrades to a named null path — a dead
  // agent leaves claims 'pending'/lineageKeyOf-clustered, never blocks the wave. Engine owns every
  // mutation here; the agents' own run.ts stay pure request/response.
  // ═══════════════════════════════════════════════════════════════════════════════════════════════

  // steps 2–4 — claim ingest (clip/dedupe/hallucinated-stance-filter) + the audited/lineage-clerked batch
  // + status recompute. Shared by ingestWave (N real lanes) and ingestScoutClaims (one wave-0 pseudo-lane)
  // so there is exactly ONE claim-ingest code path, not two. Returns the FRESH claims this call ledgered.
  async ingestClaimSeeds(
    bs: BrainerState,
    lanes: { claims: ClaimSeed[] | undefined; lane: string }[],
    wave: number,
    tag: string,
    phaseName: string,
  ): Promise<Claim[]> {
    // stance targets may only point at claims the reader's digest could actually have shown it — i.e. ones
    // that existed BEFORE this call started, never a sibling claim minted later in this very batch.
    const priorIds = new Set(bs.claims.map((c) => c.id));
    const fresh: Claim[] = [];
    for (const { claims, lane } of lanes) {
      for (const c of claims || []) {
        if (!c || !c.claim || !c.quote) continue; // drop: no claim text or no quote
        const quote = clip(c.quote, CONFIG.QUOTE_MAX_CHARS);
        const dedupeKey = norm(quote) + '|' + norm(c.source);
        // dedupe: an identical quote+source already ledgered (from an earlier wave or another lane this wave)
        if (bs.claims.some((e) => norm(e.quote) + '|' + norm(e.source) === dedupeKey)) continue;
        // STANCE COERCION — run forensics: readers emitted prose targets ('c36 (Dutch GGZ…)') despite the
        // numeric schema, and the whole attack graph was silently dropped at ingest (a non-numeric target
        // failed the priorIds check below no matter what). Salvage the recoverable ones: pull the first
        // digit-run out of the prose and coerce to a number; only a target with NO digit-run at all is
        // truly unrecoverable and drops the stance (never fabricates a target that was not named).
        let stanceIn = c.stance;
        if (stanceIn && typeof stanceIn.target !== 'number') {
          const m = String(stanceIn.target).match(/\d+/);
          stanceIn = m ? { ...stanceIn, target: Number(m[0]) } : undefined;
        }
        const stance = stanceIn && priorIds.has(stanceIn.target) ? stanceIn : undefined; // hallucinated-id filter
        // CACHEPATH TRUST — run forensics: a reader once pinned claims to /tmp scratch files and session
        // tool-result paths, non-reproducible provenance an archiver/persist pass can never follow later.
        // Only a path the scheduler actually returned this run (bs.knownCachePaths), or one matching the
        // harvester's own cache-directory signature (/.fetch/), is trusted — everything else is honestly
        // unpinned rather than silently kept. A null/non-string cachePath (the null-tolerant schema) is
        // simply absent — unpinned without counting as a trust rejection.
        let cachePath = typeof c.cachePath === 'string' && c.cachePath ? c.cachePath : undefined;
        if (cachePath && !bs.knownCachePaths.has(cachePath) && !/\/\.fetch\//.test(cachePath)) {
          cachePath = undefined;
          bs.cachePathsRejected++;
        }
        const claim: Claim = {
          id: bs.nextClaimId++,
          claim: c.claim,
          value: typeof c.value === 'string' && c.value ? c.value : undefined,
          quote,
          source: c.source,
          cachePath,
          entities: scrubEntities(c.entities),
          cluster: -1, // resolved below (applyLineage) — never left unset
          audit: cachePath ? 'pending' : 'unpinned',
          status: 'tentative',
          stance,
          attacksSurvived: 0,
          retracted: false,
          wave,
          lane,
        };
        bs.claims.push(claim);
        fresh.push(claim);
      }
    }
    if (fresh.length) {
      // bs.clusterOf's OWN keys are the canonical "known keys" list — one structure, never a second copy.
      const knownKeys = Object.keys(bs.clusterOf);
      const [auditMap, lineageMap] = await Promise.all([
        runClaimAuditor(bs, fresh, tag, phaseName),
        runLineageClerk(bs, fresh, knownKeys, tag, phaseName),
      ]);
      for (const c of fresh) {
        const v = auditMap.get(c.id);
        if (!v) continue; // a dead auditor leaves it 'pending' (unpinned downstream) — never guessed
        // REPIN CONTRACT — 'repinned' means the sent quote did not match verbatim, but the auditor located
        // a verified contiguous span that DOES carry the claim: adopt it as the claim's quote (through the
        // same scrub + clip every quote passes through at ingest) and count it a genuine pass, not a guess.
        // A 'repinned' verdict with NO newQuote is a malformed repin — never trust an unverifiable
        // replacement, so it reads as a failed pin instead of a silent pass.
        if (v.verdict === 'repinned' && v.newQuote) {
          c.quote = clip(scrubArtifacts(v.newQuote), CONFIG.QUOTE_MAX_CHARS);
          c.audit = 'pass';
          bs.quotesRepinned++;
        } else if (v.verdict === 'repinned') {
          c.audit = 'fail';
        } else {
          c.audit = v.verdict;
        }
      }
      const keyMap = new Map<number, string[]>();
      for (const c of fresh)
        keyMap.set(
          c.id,
          lineageMap.has(c.id) ? lineageMap.get(c.id)! : [lineageKeyOf(c)].filter(Boolean),
        );
      this.applyLineage(bs, fresh, keyMap);
    }
    // STATUS — recompute for every non-retracted claim (cheap at this scale); a new claim can change an
    // OLDER claim's status too (a fresh supporter/attacker), so this is a full pass, not just `fresh`.
    for (const c of bs.claims)
      if (!c.retracted)
        c.status = claimStatus(c, bs.claims, bs.nullAttacks, {
          SETTLED_MIN_CLUSTERS: CONFIG.SETTLED_MIN_CLUSTERS,
        });
    return fresh;
  }

  // lineage clustering — a PERSISTENT, incremental union-find keyed by lineage KEY (bs.clusterOf), so a
  // later claim can retroactively MERGE two previously-separate clusters (every existing member of the
  // merged-away cluster is rewritten, both in bs.clusterOf and on every already-ledgered claim). Empty keys
  // (the clerk + the lineageKeyOf fallback both resolved to nothing) → cluster 0, the shared unknown-lineage bucket.
  applyLineage(bs: BrainerState, fresh: Claim[], keyMap: Map<number, string[]>): void {
    for (const c of fresh) {
      const keys = [...new Set((keyMap.get(c.id) || []).filter(Boolean))];
      if (!keys.length) {
        c.cluster = 0;
        continue;
      }
      const existingIds = [
        ...new Set(keys.map((k) => bs.clusterOf[k]).filter((v) => v !== undefined)),
      ];
      let target: number;
      if (!existingIds.length) target = bs.nextClusterId++;
      else {
        target = Math.min(...existingIds);
        const merge = new Set(existingIds.filter((id) => id !== target));
        if (merge.size) {
          for (const k of Object.keys(bs.clusterOf))
            if (merge.has(bs.clusterOf[k])) bs.clusterOf[k] = target;
          for (const other of bs.claims) if (merge.has(other.cluster)) other.cluster = target;
        }
      }
      for (const k of keys) bs.clusterOf[k] = target;
      c.cluster = target;
    }
  }

  // step 6 — VOCABULARY: merge newTerms into bs.vocabulary by norm(term); a repeat bumps `uses` and keeps
  // the FIRST gloss (never overwritten by a later, possibly thinner, one).
  mergeVocabulary(bs: BrainerState, terms: TermSeed[] | undefined): void {
    for (const t of terms || []) {
      if (!t || !t.term) continue;
      const key = norm(t.term);
      const existing = bs.vocabulary.find((v) => norm(v.term) === key);
      if (existing) existing.uses++;
      else bs.vocabulary.push({ term: t.term, gloss: t.gloss, uses: 1 });
    }
  }

  // step 9 — CHAO (collect mode only): group non-retracted claims by near-dup of norm(claim) (the store's
  // OWN Jaccard near-dup — one definition, reused, never a second copy); abundance = distinct sources/group.
  updateChao(bs: BrainerState): void {
    const active = bs.claims.filter((c) => !c.retracted);
    const groups: Claim[][] = [];
    for (const c of active) {
      const g = groups.find((g) => nearDup(c.claim, g[0].claim));
      if (g) g.push(c);
      else groups.push([c]);
    }
    bs.chao = chao1(groups.map((g) => ({ sources: new Set(g.map((m) => norm(m.source))).size })));
  }

  // the ONE shared per-wave ingest step — called from BOTH runCrawl and runOneWave immediately after
  // runResearchers, replacing nothing in the existing validator/brainer flow. `raw`/its ClaimSeeds are
  // MUTATED in place (scrub + the surprise append) so the caller's existing `findings = raw.map(...)`
  // construction picks up the scrubbed/annotated text with no change to that code.
  async ingestWave(
    bs: BrainerState,
    toPursue: RabbitHole[],
    raw: (ResearchOut | null)[],
    wave: number,
    phaseName: string,
  ): Promise<void> {
    const tag = (bs.isRoot ? '' : bs.name + '-') + 'w' + wave;
    // 1 — SANITIZE: strip structured-output/tool-call leakage from every reader-returned text field
    // (quotes are scrubbed BEFORE the auditor ever sees them).
    for (const r of raw) {
      if (!r) continue;
      r.summary = scrubArtifacts(r.summary);
      for (const c of r.claims || []) {
        c.claim = scrubArtifacts(c.claim);
        c.quote = scrubArtifacts(c.quote);
      }
    }
    // 1b — CORRUPT CACHE QUARANTINE: readers report poisoned cache content via the CORRUPT deadEnds
    // convention (run forensics: a remediation lane was re-pointed at the same poisoned hash because
    // nothing downstream ever consumed a reader's deadEnds). Pull the cache path out of every CORRUPT
    // deadEnd, quarantine it (bs.corruptCachePaths — the scheduler is told to never re-serve it) and drop
    // it from knownCachePaths so the ingest cachePath-trust check stops treating it as reproducible.
    for (const r of raw) {
      for (const d of r?.deadEnds || []) {
        if (!/^\s*CORRUPT/i.test(d)) continue;
        const m = d.match(/(\/[^\s'"«»)]+)/);
        if (!m) continue;
        bs.corruptCachePaths.add(m[1]);
        bs.knownCachePaths.delete(m[1]);
        log('  ⚠ corrupt cache quarantined: ' + m[1]);
      }
    }
    // 2–4 — claim ingest + audit ∥ lineage + status (shared with the scout seed). Snapshot every claim's
    // status BEFORE the ingest so the diff below can tell the derivation-rerun test (D2) which ids actually
    // moved this wave — added, or flipped status (e.g. an attack just landed) — not just "the ledger grew".
    const beforeStatus = new Map(bs.claims.map((c) => [c.id, c.status]));
    const lanes = toPursue.map((p, i) => ({ claims: raw[i]?.claims, lane: p.keyword }));
    const fresh = await this.ingestClaimSeeds(bs, lanes, wave, tag, phaseName);
    const changedIds = new Set<number>(fresh.map((c) => c.id));
    for (const c of bs.claims) {
      if (c.retracted) continue;
      const prior = beforeStatus.get(c.id);
      if (prior !== undefined && prior !== c.status) changedIds.add(c.id);
    }
    bs.lastChangedClaimIds = changedIds;
    // 5 — ATTACK BOOKKEEPING: an 'attack'-kind lane that landed a counter-claim contests its target; one
    // that landed NOTHING is a completed counter-search that found nothing — first-class state, not silence.
    const validIds = new Set(bs.claims.map((c) => c.id));
    toPursue.forEach((p, i) => {
      if (p.kind !== 'attack') return;
      const rawClaims = raw[i]?.claims || [];
      const landedAnAttack = rawClaims.some((c) => c.stance && c.stance.kind === 'attacks');
      if (landedAnAttack) {
        for (const a of fresh.filter((c) => c.lane === p.keyword && c.stance?.kind === 'attacks')) {
          const target = bs.claims.find((c) => c.id === a.stance!.target);
          if (target) target.counter = a.claim; // status recompute (step 4, next wave) picks up 'contested'
        }
      } else {
        const claimIds = idsInText((p.note || '') + ' ' + (p.why || ''), validIds);
        bs.nullAttacks.push({
          topic: p.keyword,
          claimIds,
          queries: [p.keyword],
          wave,
          phase: phaseName,
        });
        for (const id of claimIds) {
          const t = bs.claims.find((c) => c.id === id);
          if (t) t.attacksSurvived = (t.attacksSurvived || 0) + 1;
        }
      }
    });
    // 6 — VOCABULARY
    for (const r of raw) if (r) this.mergeVocabulary(bs, r.newTerms);
    // 7 — SURPRISE: fold into that lane's own finding summary — no schema change, the brainer just sees it.
    raw.forEach((r, i) => {
      if (r && r.surprise)
        r.summary =
          (r.summary || '') + '\n\n⚡ SURPRISE (lane «' + toPursue[i].keyword + '»): ' + r.surprise;
    });
    // 8 — YIELDCALIB: predicted from the lane's last score, realized from what it actually produced.
    toPursue.forEach((p, i) => {
      const kind = p.kind ?? 'origin';
      const predicted = (lastScore(p) ?? CONFIG.CALIB_DEFAULT_SCORE) / 100;
      const auditedPass = fresh.filter((c) => c.lane === p.keyword && c.audit === 'pass').length;
      const freshLeads = (raw[i]?.rabbitHoles?.length || 0) + (raw[i]?.nextSources?.length || 0);
      const realized = Math.min(
        CONFIG.CALIB_REALIZED_MAX,
        (auditedPass + CONFIG.CALIB_LEAD_WEIGHT * freshLeads) / CONFIG.CALIB_NORM,
      );
      bs.yieldCalib[kind] = updateCalib(
        bs.yieldCalib,
        kind,
        predicted,
        realized,
        CONFIG.CALIB_ALPHA,
      );
    });
    // 8b — VENUE YIELD: per pursued lane, tally each assigned venue's assigned/yielded count — a lane
    // "yielded" when it landed ≥1 ledgered claim or ≥1 fresh lead. Flags a persistently-dry venue to the
    // brainer (see utils venuesWithYieldWarn) without ever touching the prospector's own venue schema.
    toPursue.forEach((p, i) => {
      const yielded =
        fresh.some((c) => c.lane === p.keyword) ||
        (raw[i]?.rabbitHoles?.length || 0) + (raw[i]?.nextSources?.length || 0) > 0;
      for (const src of p.sources || []) {
        const s = bs.venueStats[src] || (bs.venueStats[src] = { assigned: 0, yielded: 0, served: 0 });
        s.assigned++;
        if (yielded) s.yielded++;
      }
    });
    // 9 — CHAO (collect mode only)
    if (CONFIG.mode === 'collect') this.updateChao(bs);
  }

  // D1 — STORE the brainer's freshly-authored derivation delta (v3 STEERING). Engine owns the mutation:
  // same code as already stored ⇒ keep the last good rerun (only the inputs metadata changed); new/changed
  // code ⇒ reset lastRun to null (a fresh rerun is owed before the sensitivity numbers can be trusted
  // again). Always dirties the derivation so maybeRerunDerivation's next check fires at least once.
  applyDerivation(bs: BrainerState, coord: Coord): void {
    const d = coord.derivation;
    if (!CONFIG.compute || !d || !d.code) return;
    const sameCode = !!(bs.derivation && bs.derivation.code === d.code);
    bs.derivation = {
      code: d.code,
      inputs: d.inputs || [],
      lastRun: sameCode ? bs.derivation!.lastRun : null,
    };
    bs.derivationDirty = true;
  }

  // D2 — RERUN the stored derivation when it is dirty (freshly authored/re-authored this wave) OR when a
  // claim one of its inputs cites changed this wave (ingestWave's lastChangedClaimIds). A dead rerunner or a
  // script error degrades to a stale flag — lastRun is kept, the brainer is told it is stale (see
  // sensitivityClause), never blocked. Called right after ingestWave, before the validator gate.
  async maybeRerunDerivation(bs: BrainerState, wave: number, phaseName: string): Promise<void> {
    if (!CONFIG.compute || !bs.derivation) return;
    const inputIds = new Set(bs.derivation.inputs.flatMap((i) => i.claimIds || []));
    const inputsChanged = [...bs.lastChangedClaimIds].some((id) => inputIds.has(id));
    if (!bs.derivationDirty && !inputsChanged) return;
    const run = await runRerunner(bs, phaseName);
    if (run) {
      bs.derivation.lastRun = { quantiles: run.quantiles, sensitivity: run.sensitivity, wave };
      bs.derivationDirty = false;
      bs.derivationStale = false;
      log(
        '  · derivation rerun · p50=' +
          (run.quantiles.p50 ?? '?') +
          ' · top=' +
          (topSensitivityInput(run.sensitivity) || '?'),
      );
    } else {
      bs.derivationStale = true;
      log('  · derivation rerun FAILED — lastRun kept, marked stale');
    }
  }

  // ADOPT RESULT SO FAR (v3 batch 6) — the ONE call site every `bs.resultSoFar = coord.resultSoFar`-style
  // assignment goes through. A degenerate wave (e.g. a brain-compute pass distracted by its derivation) can
  // return an EMPTY keyClaimIds even though the prior resultSoFar carried a real one — adopting it verbatim
  // would silently wipe the confidence floor's own input. Guard: when the incoming result carries no
  // keyClaimIds but the prior one did, preserve the prior ids; a genuinely new non-empty set always replaces.
  adoptResultSoFar(bs: BrainerState, rs: ResultSoFar): void {
    const priorIds = bs.resultSoFar && bs.resultSoFar.keyClaimIds;
    bs.resultSoFar =
      (!rs.keyClaimIds || !rs.keyClaimIds.length) && priorIds && priorIds.length
        ? { ...rs, keyClaimIds: priorIds }
        : rs;
  }

  // SCOUT INGEST (wave 0) — the scout's own claims/newTerms flow through the SAME claim-ingest machinery
  // (steps 2/3/4/6) as one pseudo-lane 'scout', kind 'seed'. No digest existed yet (scout claims never
  // carry a stance) and there is no lane to attack/calibrate/chao yet — those steps are skipped outright.
  async ingestScoutClaims(bs: BrainerState, scoutOut: ScoutOut): Promise<void> {
    if (!(scoutOut.claims || []).length && !(scoutOut.newTerms || []).length) return;
    await this.ingestClaimSeeds(
      bs,
      [{ claims: scoutOut.claims, lane: 'scout' }],
      0,
      'scout',
      CONFIG.PHASE.scout,
    );
    this.mergeVocabulary(bs, scoutOut.newTerms);
  }

  // Crawl: wave 0 = score the scout rabbit-holes; waves 1..N = pursue → research → re-coordinate until the brainer stops.
  async runCrawl(bs: BrainerState, scoutRabbitHoles: SeedLead[]): Promise<void> {
    const scoutOut = this.scout!;
    await this.ingestScoutClaims(bs, scoutOut); // v3: seed the claim ledger from the scout's own claims/newTerms

    this.files['01-scout.md'] = withPrompt(
      'scout',
      '# 01 — Scout\n\n**Query:** ' +
        CONFIG.query +
        '\n\n## Landscape\n\n' +
        scoutOut.landscape +
        '\n\n## Sources\n\n' +
        scoutOut.pages
          .map(
            (p, i) =>
              '### ' +
              (i + 1) +
              ' — ' +
              p.url +
              '\n\n' +
              p.summary +
              '\n\n' +
              (p.rabbitHoles || []).map((l) => '- **' + l.keyword + '** — ' + l.why).join('\n'),
          )
          .join('\n\n') +
        '\n\n## Dead ends\n\n' +
        ((scoutOut.deadEnds || []).map((d) => '- ' + d).join('\n') || '_none_') +
        '\n',
    );

    // seed the open store with the scout rabbit-holes (UNSCORED — the brainer scores them this wave via rescore).
    scoutRabbitHoles.forEach((l) =>
      addRabbitHole(bs, {
        keyword: l.keyword,
        why: l.why,
        path: l.path || [],
        wave: 0,
        kind: l.kind,
      }),
    );
    // v3: the scout's own followed-citation leads seed the store the same way a crawl wave's nextSources do —
    // never dropped on the floor just because there is no digest yet at wave 0.
    (scoutOut.nextSources || []).forEach((s) =>
      addRabbitHole(bs, {
        keyword: s.why,
        why: 'followed citation',
        ref: s.ref,
        kind: 'citation',
        path: [],
        wave: 0,
      }),
    );

    const seedFindings: Finding[] = scoutOut.pages.map((p) => ({
      rabbitHole: p.url,
      summary: p.summary,
    }));
    log(
      '· brainer-w0 DISPATCH · ' +
        brainer.tier +
        ' · scoring ' +
        bs.rabbitHoles.length +
        ' rabbit-hole(s)',
    );
    let coord = await runBrainer(bs, 0, seedFindings, CONFIG.PHASE.scout);
    if (!coord) {
      // Wave-0 brainer dead after all retries (terminal API error / safety-classifier
      // block). Do NOT throw — the scout + prospector material and the seeded claim
      // ledger still carry real verified value. Drain the crawl, stamp the honest
      // stopReason, and let finalize produce a loudly-labelled degraded report
      // (see the DEGRADED banner in runFinalize) instead of destroying the run.
      log('✗ brainer-w0 DIED — crawl aborted; finalizing on scout material only');
      bs.status = 'drained';
      bs.stopReason = 'brainer-dead';
      return;
    }
    applyDeltas(bs, coord, 0);
    this.applyDerivation(bs, coord);
    if (coord.resultSoFar) this.adoptResultSoFar(bs, coord.resultSoFar);
    bs.resultLog.push({ wave: 0, resultSoFar: bs.resultSoFar });
    let lookupNext = resolveLookupNext(bs, coord, 0, laneCount);
    bs.topScores.push(lookupNext.length ? Math.max(...lookupNext.map((p) => p.score ?? 0)) : 0);
    bs.waveLog.push({
      wave: 0,
      pursued: [],
      newRabbitHoles: scoutRabbitHoles.length,
      rabbitHoles: bs.rabbitHoles.length,
      topScore: bs.topScores[bs.topScores.length - 1],
      done: coord.stop.done,
      reason: coord.stop.reason,
    });
    log(
      '· brainer-w0 RETURN · rabbitHoles=' +
        bs.rabbitHoles.length +
        ' · lookupNext=' +
        lookupNext.length +
        '/' +
        (coord.lookupNext || []).length +
        ' · topScore=' +
        bs.topScores[bs.topScores.length - 1] +
        ' · done=' +
        coord.stop.done,
    );
    lookupNext.forEach((p, i) =>
      log(
        '    look-up ' +
          (i + 1) +
          ' · [' +
          (p.score ?? '?') +
          '] #' +
          p.id +
          ' ' +
          p.keyword +
          (p.sources && p.sources.length ? ' · venues=[' + p.sources.join(', ') + ']' : ''),
      ),
    );

    this.files['03-wave-0.md'] = withPrompt(
      'brainer-w0',
      waveMd(0, coord, lookupNext, [], bs.rabbitHoles),
    );

    phase(CONFIG.PHASE.crawl); // scout → prospector → seed brainer = the Scout phase; waves 1..N = Crawl
    let wave = 1;
    let dryStop = false; // collect-mode dry: set when the novelty trajectory has plateaued (diminishing returns)
    let starvedStop = false; // B6: set when the scheduler/readers starved for MAX_STARVED_WAVES in a row
    let starvedWaves = 0; // consecutive all-null / empty-schedule waves
    const baseCap = CONFIG.maxWave === 'auto' ? CONFIG.HARD_CAP : CONFIG.maxWave; // effective wave cap; 'auto' rides up to HARD_CAP, the brainer stops it sooner
    // the crawl runs waves until the brainer declares done, the store dries up, the collect novelty plateaus, or the wave cap is hit.
    while (
      wave <= Math.min(CONFIG.HARD_CAP, baseCap) &&
      !coord.stop.done &&
      lookupNext.length &&
      !dryStop
    ) {
      bs.wave = wave; // keep bs.wave in step with the loop (the rerunner's label + lastRun.wave read it mid-wave)
      // PURSUE — move lookupNext into the pursued-archive (keeps id + scoreHistory + path) and out of the open store, so the brainer
      // re-scores a clean open-only set next wave.
      pursue(bs, lookupNext);
      log(
        '— wave ' +
          wave +
          ' · pursuing ' +
          lookupNext.length +
          ' rabbit-hole(s) · pursued-total=' +
          bs.pursuedList.length +
          ' · archived=' +
          bs.pursuedArchive.length,
      );

      // RESEARCH wave — the SCHEDULER discovers + sizes the sources for the wave's lanes, then code bin-packs
      // each lane and runs ONE sequential reader thread per lane (parallel across lanes); each carries its full TRAIL.
      const toPursue = lookupNext;
      const tag = 'w' + wave;
      const schedule = await this.scheduleSources(bs, toPursue, tag, CONFIG.PHASE.crawl);
      const raw = await runResearchers(bs, toPursue, schedule, tag, CONFIG.PHASE.crawl);
      await this.ingestWave(bs, toPursue, raw, wave, CONFIG.PHASE.crawl); // v3: claim-ledger ingest (mutates raw's text fields + bs)
      await this.maybeRerunDerivation(bs, wave, CONFIG.PHASE.crawl); // v3 STEERING: rerun the stored derivation iff dirty or an input claim changed
      // B6 — guard scheduler-DEATH starvation: a wave is "starved" when the scheduler returned NO usable sources at
      // all (an empty map, or every lane's source list empty). After MAX_STARVED_WAVES in a row, break with an
      // explicit stopReason instead of grinding to HARD_CAP with nothing to read. (An all-null wave whose schedule
      // DID carry sources is a reader failure — left to the validator reopen/cap + brainer-convergence path, which
      // this guard must not preempt.)
      const waveStarved = [...schedule.values()].every((srcs) => !srcs || !srcs.length);
      starvedWaves = waveStarved ? starvedWaves + 1 : 0;
      if (starvedWaves >= CONFIG.MAX_STARVED_WAVES) {
        starvedStop = true;
        log(
          '  wave ' +
            wave +
            ' · scheduler-starved (' +
            starvedWaves +
            ' consecutive empty waves) → stopping',
        );
        break;
      }
      const findings: Finding[] = raw.map((r, i) => ({
        rabbitHole: toPursue[i].keyword,
        trail: trailOf(toPursue[i].path, toPursue[i].keyword),
        summary: r ? r.summary : '(researcher failed)',
      }));
      if (CONFIG.debug)
        raw.forEach((r, i) =>
          this.laneRecords.push({
            wave,
            keyword: toPursue[i].keyword,
            assignedVenues: toPursue[i].sources || [],
            summary: r ? r.summary : null,
            rabbitHoles: r ? (r.rabbitHoles || []).map((l) => l.keyword) : [],
          }),
        );

      // PATH: each freshly-surfaced rabbit-hole inherits its parent's trail (parent path + parent keyword). The engine adds them to the open
      // store UNSCORED (scoreHistory=[]); deduped against pursued + the current store; the brainer scores them next wave (shown as "new").
      const fresh: SeedLead[] = raw.flatMap((r, i) =>
        r && r.rabbitHoles
          ? r.rabbitHoles.map((l) => ({
              keyword: l.keyword,
              why: l.why,
              path: [...(toPursue[i].path || []), toPursue[i].keyword],
              kind: l.kind ?? 'gap',
            }))
          : [],
      );
      // FOLLOW-THE-LINKS: each page's top outbound citations become ref-carrying leads the next lane fetches directly.
      const freshSources: SeedLead[] = raw.flatMap((r, i) =>
        r && r.nextSources
          ? r.nextSources.map((s) => ({
              keyword: s.why,
              why: 'followed citation',
              ref: s.ref,
              path: [...(toPursue[i].path || []), toPursue[i].keyword],
              kind: 'citation' as const,
            }))
          : [],
      );
      const beforeAdd = bs.rabbitHoles.length;
      fresh.forEach((l) =>
        addRabbitHole(bs, { keyword: l.keyword, why: l.why, path: l.path, wave, kind: l.kind }),
      );
      freshSources.forEach((l) =>
        addRabbitHole(bs, {
          keyword: l.keyword,
          why: l.why,
          path: l.path,
          wave,
          ref: l.ref,
          kind: l.kind,
        }),
      );
      const newCount = bs.rabbitHoles.length - beforeAdd;
      log(
        '  wave ' +
          wave +
          ' · researchers=' +
          raw.filter(Boolean).length +
          '/' +
          toPursue.length +
          ' · freshRabbitHoles=' +
          (fresh.length + freshSources.length) +
          ' → +' +
          newCount +
          ' new after dedup',
      );

      // VALIDATOR GATE — the per-wave coverage check. Runs only when a lane died, a finding is thin, a read came
      // back CORRUPT, or a lane reported dead ends but landed no claims at all (keeps it cheap otherwise). The
      // reader prompt has always promised "the engine will reopen the lane" on a dead read — before this,
      // deadEnds were never consumed by anything downstream. Re-opens every lane that returned null OR
      // fulfilled:false (bounded per-lane by MAX_LANE_REFAILS) so the next brainer can re-pursue; a lane past
      // the cap is surfaced as a known gap; `missing` threads into the next brainer.
      const anyNull = raw.some((r) => !r);
      const anyThin = findings.some((f) => !f.summary || f.summary.length < CONFIG.VALIDATOR_THIN);
      const anyCorrupt = raw.some((r) => r && (r.deadEnds || []).some((d) => /^\s*CORRUPT/i.test(d)));
      const anyDeadNoClaims = raw.some(
        (r) => r && (r.deadEnds || []).length > 0 && !(r.claims || []).length,
      );
      if (anyNull || anyThin || anyCorrupt || anyDeadNoClaims) {
        const requests = toPursue.map((p) => ({ id: p.id, keyword: p.keyword, why: p.why }));
        const vFindings = findings.map((f, i) => ({
          keyword: f.rabbitHole,
          intro:
            (f.summary || '').slice(0, CONFIG.VALIDATOR_INTRO_CHARS) +
            (raw[i] && (raw[i]!.deadEnds || []).length
              ? ' [deadEnds: ' + raw[i]!.deadEnds!.join('; ').slice(0, 200) + ']'
              : ''),
        }));
        const nullLanes = toPursue.filter((p, i) => !raw[i]).map((p) => p.keyword);
        const val = await runValidator(bs, wave, requests, vFindings, nullLanes);
        const failedIds = new Set<number>();
        toPursue.forEach((p, i) => {
          if (!raw[i]) failedIds.add(p.id);
        });
        if (val && Array.isArray(val.checks))
          val.checks.forEach((c) => {
            if (c && c.fulfilled === false && typeof c.id === 'number') failedIds.add(c.id);
          });
        const reopened: string[] = [];
        const cappedGaps: string[] = [];
        for (const id of failedIds) {
          const rh = bs.pursuedArchive.find((r) => r.id === id);
          if (!rh) continue;
          if ((rh.failCount || 0) >= CONFIG.MAX_LANE_REFAILS) cappedGaps.push(rh.keyword);
          else reopened.push(reopenRabbitHole(bs, rh).keyword);
        }
        const missing = (val && val.missing) || [];
        bs.lastValidatorMissing = [
          ...missing,
          ...cappedGaps.map((k) => k + ' (lane retried twice — treat as a known gap)'),
        ]
          .join('; ')
          .slice(0, CONFIG.VALIDATOR_MISSING_CHARS);
        bs.validatorLog.push({
          wave,
          enough: val ? val.enough : null,
          reopened,
          cappedGaps,
          missing,
        });
        log(
          '  wave ' +
            wave +
            ' · validator · enough=' +
            (val ? val.enough : '?') +
            ' · reopened=' +
            reopened.length +
            ' · cappedGaps=' +
            cappedGaps.length,
        );
      } else {
        bs.lastValidatorMissing = '';
      }

      // BRAINER — the single Opus brain re-scores the open store via deltas, updates the running result, and sets the next direction.
      log(
        '  wave ' +
          wave +
          ' · brainer DISPATCH · ' +
          brainer.tier +
          ' · open=' +
          bs.rabbitHoles.length,
      );
      const nextCoord = await runBrainer(bs, wave, findings);
      if (!nextCoord) {
        log('✗ brainer-w' + wave + ' DIED — stopping');
        break;
      }
      coord = nextCoord;
      applyDeltas(bs, coord, wave);
      this.applyDerivation(bs, coord);
      if (coord.resultSoFar) this.adoptResultSoFar(bs, coord.resultSoFar);
      // crash-safety checkpoint — a single zero-cost log line, the wave's FINAL state (after the brainer's
      // deltas have landed): recoverable from the workflow's live output, off the critical path.
      if (CONFIG.checkpoint)
        log(CONFIG.CHECKPOINT_MARK + ' w' + wave + ' ' + JSON.stringify(compactCheckpoint(bs)));
      bs.resultLog.push({ wave, resultSoFar: bs.resultSoFar });
      lookupNext = resolveLookupNext(bs, coord, wave, laneCount);
      bs.topScores.push(lookupNext.length ? Math.max(...lookupNext.map((p) => p.score ?? 0)) : 0);
      bs.waveLog.push({
        wave,
        pursued: toPursue.map((p) => p.keyword),
        newRabbitHoles: newCount,
        rabbitHoles: bs.rabbitHoles.length,
        topScore: bs.topScores[bs.topScores.length - 1],
        done: coord.stop.done,
        reason: coord.stop.reason,
      });
      this.files[padIdx(wave + 3) + '-wave-' + wave + '.md'] = withPrompt(
        'brainer-w' + wave,
        waveMd(wave, coord, lookupNext, findings, bs.rabbitHoles),
      );
      log(
        '  wave ' +
          wave +
          ' · rabbitHoles=' +
          bs.rabbitHoles.length +
          ' · lookupNext=' +
          lookupNext.length +
          '/' +
          (coord.lookupNext || []).length +
          ' · topScore=' +
          bs.topScores[bs.topScores.length - 1] +
          ' · done=' +
          coord.stop.done +
          (coord.stop.done ? ' (' + coord.stop.reason + ')' : ''),
      );
      lookupNext.forEach((p, i) =>
        log(
          '    next ' +
            (i + 1) +
            ' · [' +
            (p.score ?? '?') +
            '] #' +
            p.id +
            ' ' +
            p.keyword +
            (p.sources && p.sources.length ? ' · venues=[' + p.sources.join(', ') + ']' : ''),
        ),
      );

      // collect-mode DRY stop: diminishing returns relative to the run's OWN peak novelty (adapts per topic — no magic absolute floor).
      // B9 — peak/window are computed over the RESEARCH waves only (topScores.slice(1)) so the inflated wave-0 SEED
      // score never masks a plateau; PLATEAU_MIN_WAVES gates on the research-wave count (3 = 3 research waves).
      const crawlScores = bs.topScores.slice(1);
      if (
        CONFIG.mode === 'collect' &&
        !coord.stop.done &&
        crawlScores.length >= CONFIG.PLATEAU_MIN_WAVES
      ) {
        const peak = Math.max(...crawlScores);
        const window = crawlScores.slice(-CONFIG.PLATEAU_WINDOW);
        if (peak > 0 && window.every((s) => s <= peak * CONFIG.QUERY_PLATEAU)) {
          // CHAO STOP ASSIST — a plateau alone is not enough in collect mode when the coverage estimate says
          // there is still a lot unseen: gate the dry-stop on coverage ≥ CHAO_COVERAGE_STOP (no chao yet ⇒ the
          // old plateau-only behavior, degrade-to-null).
          if (bs.chao == null || bs.chao.coverage >= CONFIG.CHAO_COVERAGE_STOP) {
            dryStop = true;
            log(
              '  wave ' +
                wave +
                ' · collect DRY — top novelty plateaued (' +
                window.join(',') +
                ' ≤ ' +
                CONFIG.QUERY_PLATEAU +
                '×peak ' +
                peak +
                ') → stopping',
            );
          } else {
            log(
              '  wave ' +
                wave +
                ' · plateau but coverage ' +
                bs.chao.coverage.toFixed(2) +
                ' < ' +
                CONFIG.CHAO_COVERAGE_STOP +
                ' — continuing',
            );
          }
        }
      }
      wave++;
    }

    // L2 stop classification: the brainer's own satisficing `done` (primary), else why the loop stopped.
    const bestOpen = bs.rabbitHoles.length
      ? Math.max(...bs.rabbitHoles.map((r) => lastScore(r) ?? 0))
      : 0;
    // Classify in priority order: the brainer's own `done`, then a starved scheduler, then an EMPTY store (an
    // empty-store exit is labelled BEFORE the plateau label — B9), then the collect plateau, the wave cap, and a dry store.
    const stopReason: StopReason = coord.stop.done
      ? 'brainer-done'
      : starvedStop
        ? 'scheduler-starved'
        : !bs.rabbitHoles.length
          ? 'rabbithole-empty'
          : dryStop
            ? 'collect-dry-plateau'
            : lookupNext.length
              ? 'wave-cap'
              : 'rabbithole-dry';
    log(
      '■ crawl DONE · stopReason=' +
        stopReason +
        ' · waves=' +
        (wave - 1) +
        ' · rabbitHoles=' +
        bs.rabbitHoles.length,
    );

    // VALIDATOR output → file (every wave it ran: enough verdict + reopened lanes + capped known-gaps + missing)
    if (bs.validatorLog.length) {
      this.files[padIdx(wave + 3) + '-validator.md'] =
        '# Validator — per-wave crawl coverage gate\n\n' +
        bs.validatorLog
          .map(
            (v) =>
              '## Wave ' +
              v.wave +
              ' — enough=' +
              v.enough +
              (v.reopened.length ? '\n\n**Reopened (re-pursue):** ' + v.reopened.join(', ') : '') +
              (v.cappedGaps.length
                ? '\n\n**Known gaps (retried twice, not reopened):** ' + v.cappedGaps.join(', ')
                : '') +
              (v.missing.length ? '\n\n**Missing:** ' + v.missing.join('; ') : ''),
          )
          .join('\n\n') +
        '\n';
    }

    // hand the crawl outcome to the later phases
    bs.coord = coord;
    bs.wave = wave;
    bs.bestOpen = bestOpen;
    bs.stopReason = stopReason;
  }

  // CRAWL REOPEN (rare) — the judge found a real evidence gap: pursue its leads through lane readers and fold the
  // findings back into resultSoFar via a brainer pass. Bounded by the leads the judge returns (≤ laneCount, deduped vs pursued).
  async reopenCrawl(bs: BrainerState, leads: RabbitHoleSeed[], directive: string): Promise<void> {
    const picks = leads
      .filter((l) => l && l.keyword && !bs.pursuedKeys.has(norm(l.keyword)))
      .slice(0, laneCount)
      .map((l) => {
        const rh = addRabbitHole(bs, {
          keyword: l.keyword,
          why: l.why,
          path: ['⚖ judge'],
          score: CONFIG.INJECT_SCORE,
          wave: bs.wave,
          kind: 'inject',
        });
        // steer the finalize lane: the judge's directive becomes the lane `note` (so the scheduler + reader get
        // the same WHAT-to-find steering the crawl lanes get); fall back to the lead's own `why` if no directive.
        if (rh) rh.note = directive || l.why || '';
        return rh;
      })
      .filter(Boolean) as RabbitHole[];
    if (!picks.length) {
      log('· finalize · judge reopen · no fresh leads (all already pursued)');
      return;
    }
    pursue(bs, picks);
    bs.reopenedLaneCount += picks.length; // crawl-vs-finalize lane-count reconciliation for metrics
    const schedule = await this.scheduleSources(bs, picks, 'reopen', CONFIG.PHASE.finalize);
    const raw = await runResearchers(bs, picks, schedule, 'reopen', CONFIG.PHASE.finalize);
    // v3 HARVEST (a v2 weak point: this used to discard almost everything the readers gathered) — claims/
    // attacks/vocab flow into the ledger exactly as a crawl wave's do (mutates raw's text fields too, so the
    // findings built below already carry the scrubbed/annotated summaries).
    await this.ingestWave(bs, picks, raw, bs.wave, CONFIG.PHASE.finalize);
    await this.maybeRerunDerivation(bs, bs.wave, CONFIG.PHASE.finalize); // v3: a reopened lane can change a derivation input too — mirror the two wave-loop call sites
    const findings: Finding[] = raw.map((r, i) => ({
      rabbitHole: picks[i].keyword,
      trail: trailOf(picks[i].path, picks[i].keyword),
      summary: r ? r.summary : '(researcher failed)',
    }));
    // harvest fresh leads into the store too — same inheritance pattern as the crawl wave loop (never dropped
    // on the floor: a judge-reopened lane that surfaces its own follow-ons used to lose them outright).
    const fresh: SeedLead[] = raw.flatMap((r, i) =>
      r && r.rabbitHoles
        ? r.rabbitHoles.map((l) => ({
            keyword: l.keyword,
            why: l.why,
            path: [...(picks[i].path || []), picks[i].keyword],
            kind: l.kind ?? 'gap',
          }))
        : [],
    );
    const freshSources: SeedLead[] = raw.flatMap((r, i) =>
      r && r.nextSources
        ? r.nextSources.map((s) => ({
            keyword: s.why,
            why: 'followed citation',
            ref: s.ref,
            path: [...(picks[i].path || []), picks[i].keyword],
            kind: 'citation' as const,
          }))
        : [],
    );
    fresh.forEach((l) =>
      addRabbitHole(bs, {
        keyword: l.keyword,
        why: l.why,
        path: l.path,
        wave: bs.wave,
        kind: l.kind,
      }),
    );
    freshSources.forEach((l) =>
      addRabbitHole(bs, {
        keyword: l.keyword,
        why: l.why,
        path: l.path,
        wave: bs.wave,
        ref: l.ref,
        kind: l.kind,
      }),
    );
    const coord = await runBrainer(bs, bs.wave, findings, CONFIG.PHASE.finalize);
    if (coord) {
      applyDeltas(bs, coord, bs.wave); // rename/drop/rescore/add — the coord's deltas, not just resultSoFar
      this.applyDerivation(bs, coord);
      if (coord.resultSoFar) this.adoptResultSoFar(bs, coord.resultSoFar);
    }
    log('· finalize · judge reopen · folded ' + picks.length + ' lane(s) into the answer');
  }

  // ═══════════════════════════════════════════════════════════════════════════════════════════════
  // FINALIZE LEDGER MUTATIONS (v3 batch 4) — the refiner/judge/synthesiser run.ts fns stay pure request/
  // response; every ledger write lives here, engine-owned, mirroring the crawl-phase ingestWave discipline.
  // ═══════════════════════════════════════════════════════════════════════════════════════════════

  // REFINE → LEDGER (attack-recording) — a fact bound to a claimId (the initiator named one) folds its
  // refiner's counter-search outcome into that claim: counterFound sets a contested `counter` note (status
  // recompute picks up `contested` — see utils claimStatus); !counterFound is a completed counter-search
  // that found nothing — first-class nullAttack state, and the claim's attacksSurvived grows. A fact with
  // no claimId, or a dead refiner (refined[i] null), never touches the ledger — exactly v2.
  applyRefineAttacks(
    bs: BrainerState,
    facts: FactToHarden[],
    refined: (RefineOut | null)[],
    phaseName: string,
  ): void {
    facts.forEach((f, i) => {
      if (!f.claimId) return;
      const r = refined[i];
      if (!r) return;
      const claim = bs.claims.find((c) => c.id === f.claimId && !c.retracted);
      if (!claim) return;
      if (r.counterFound) {
        claim.counter = r.counterNote || 'refiner found counter-evidence';
      } else {
        bs.nullAttacks.push({
          topic: f.fact,
          claimIds: [f.claimId],
          queries: r.queriesTried || [],
          wave: bs.wave,
          phase: phaseName,
        });
        claim.attacksSurvived = (claim.attacksSurvived || 0) + 1;
      }
    });
    // MECHANICAL COUNTER-PROPAGATION — run forensics: a refine pass falsified the headline claim but the
    // synthesis rubber-stamped the pre-correction answer; only the judge caught it, one pass later than it
    // should have. The engine now propagates every counterFound into the answer + tensions BEFORE the judge
    // ever sees it — single point of failure removed. Runs over EVERY fact the refiner touched, not just
    // claimId-bound ones (a refine pass can falsify the headline answer itself with no ledger claim behind it).
    const corrections = facts
      .map((f, i) =>
        refined[i] && refined[i]!.counterFound
          ? f.fact + ' → ' + (refined[i]!.counterNote || 'counter-evidence found')
          : null,
      )
      .filter((l): l is string => !!l);
    if (corrections.length && bs.resultSoFar) {
      const existingAnswer = bs.resultSoFar.answer || '';
      // dedupe across passes — a correction already folded into the answer by an earlier judge pass never repeats.
      const fresh = corrections.filter((l) => !existingAnswer.includes(l));
      if (fresh.length)
        bs.resultSoFar = {
          ...bs.resultSoFar,
          answer:
            existingAnswer +
            '\n\n**Corrections from the refine pass (machine-appended):**\n' +
            fresh.map((l) => '- ' + l).join('\n'),
          tensions: [...(bs.resultSoFar.tensions || []), ...fresh],
        };
    }
    for (const c of bs.claims)
      if (!c.retracted)
        c.status = claimStatus(c, bs.claims, bs.nullAttacks, {
          SETTLED_MIN_CLUSTERS: CONFIG.SETTLED_MIN_CLUSTERS,
        });
  }

  // runRefine + write the refinement file + fold its attack outcomes into the ledger — the ONE call site
  // every refine dispatch (initial pass, judge re-refine, post-reopen re-refine, the speculative gate) goes
  // through, so applyRefineAttacks never gets skipped on any path.
  async refineAndLedger(
    bs: BrainerState,
    facts: FactToHarden[],
    directive: string,
    passTag: string,
    fileKey: string,
    phaseName: string,
  ): Promise<CleanReport[]> {
    const r = await runRefine(bs, facts, directive, passTag);
    this.files[fileKey] = r.artifact;
    this.applyRefineAttacks(bs, facts, r.refined, phaseName);
    return r.cleanReports;
  }

  // JUDGE RETRACTION — a judge naming real (non-hallucinated) ledger ids discredits them: retracted,
  // statuses recomputed, the computed confidence recomputed (logged — the next judge/synthesiser call
  // reads it fresh off the mutated ledger regardless). When a retracted claim backed a derivation input, ONE
  // bounded rerunner call refreshes lastRun (bounded naturally by the MAX_JUDGE_PASSES loop this rides
  // inside — never a second call for the same judgement). No real id named → nothing (degrade-to-null).
  async applyJudgeRetractions(
    bs: BrainerState,
    judgement: JudgeOut | null,
    phaseName: string,
  ): Promise<void> {
    const ids = (judgement && judgement.retractClaimIds) || [];
    const real = ids.filter((id) => bs.claims.some((c) => c.id === id && !c.retracted));
    if (!real.length) return;
    for (const id of real) {
      const c = bs.claims.find((c) => c.id === id);
      if (c) c.retracted = true;
    }
    for (const c of bs.claims)
      if (!c.retracted)
        c.status = claimStatus(c, bs.claims, bs.nullAttacks, {
          SETTLED_MIN_CLUSTERS: CONFIG.SETTLED_MIN_CLUSTERS,
        });
    const confidence = computedConfidence(
      (bs.resultSoFar && bs.resultSoFar.keyClaimIds) || [],
      bs.claims,
    );
    log(
      '⚖ judge retracted claims: ' +
        real.map((id) => 'c' + id).join(', ') +
        ' · confidence now ' +
        confidence,
    );
    const inputIds = new Set(
      (bs.derivation ? bs.derivation.inputs : []).flatMap((i) => i.claimIds || []),
    );
    if (bs.derivation && real.some((id) => inputIds.has(id))) {
      const run = await runRerunner(bs, phaseName);
      if (run)
        bs.derivation.lastRun = {
          quantiles: run.quantiles,
          sensitivity: run.sensitivity,
          wave: bs.wave,
        };
    }
  }

  // SYNTHESISER FINISH (citation lint + confidence floor) — shared by the single-brainer finalize + the
  // multi-brainer winner path. Lints [cN] markers against the ledger (strips + logs + counts the bogus
  // ones), then applies the lower-only confidence floor (final = min(stated, computed) — the computed value
  // can never RAISE it) and appends a note to the report when it lowered the stated confidence. Mutates
  // bs.reportOk/citationsBogus/synthesiserOut and writes result.md; every RunResult/metrics field downstream
  // reads bs.synthesiserOut, which now carries the floored `confidence` + linted `report` in place.
  applyReportFinish(bs: BrainerState, agg: ReportOut | null, label: string): void {
    bs.synthesiserOut = agg;
    bs.reportOk = !!(agg && agg.report);
    bs.citationsBogus = 0;
    bs.citationsAuditFailed = 0;
    if (!bs.reportOk) {
      log('✗ ' + label + ' FAILED — no report returned');
      // Salvage: the run's evidence still has value — deliver a degraded result.md from
      // the running answer instead of finishing with no deliverable at all. Same
      // philosophy as the brainer-dead banner: degrade loudly, never destroy value.
      const rsf = bs.resultSoFar;
      const salvage: string[] = [
        '> ⚠️ **DEGRADED REPORT — the synthesiser died (terminal agent failure after all retries).** Below is the raw running answer, not a synthesized report; the full evidence lives in `_claims.md` and the per-wave files.\n',
      ];
      if (rsf && rsf.answer) salvage.push('## Running answer\n\n' + rsf.answer + '\n');
      if (rsf && rsf.working) salvage.push('## Working notes\n\n' + rsf.working + '\n');
      if (rsf && rsf.openGaps && rsf.openGaps.length)
        salvage.push('## Open gaps\n\n' + rsf.openGaps.map((q) => '- ' + q).join('\n') + '\n');
      if (rsf && rsf.tensions && rsf.tensions.length)
        salvage.push('## Tensions\n\n' + rsf.tensions.map((q) => '- ' + q).join('\n') + '\n');
      this.files['result.md'] = runArgsMd() + salvage.join('\n');
      return;
    }
    const { report: linted, bogus, auditFailed } = lintCitations(agg!.report, bs.claims);
    bs.citationsBogus = bogus.length;
    bs.citationsAuditFailed = auditFailed.length;
    if (bogus.length)
      log(
        '⚠ ' +
          label +
          ' citation lint — stripped bogus id(s): ' +
          bogus.map((id) => 'c' + id).join(', '),
      );
    if (auditFailed.length)
      log(
        '⚠ ' +
          label +
          ' citation lint — stripped audit-failed id(s): ' +
          auditFailed.map((id) => 'c' + id).join(', '),
      );
    const keyClaimIds = (bs.resultSoFar && bs.resultSoFar.keyClaimIds) || [];
    const computed = computedConfidence(keyClaimIds, bs.claims);
    const final = minConfidence(agg!.confidence, computed);
    let report = linted;
    if (final !== agg!.confidence)
      report +=
        '\n\n> Confidence adjusted from ' +
        agg!.confidence +
        ' to ' +
        final +
        ' — computed from evidence topology (clusters × attack-survival).';
    agg!.report = report;
    agg!.confidence = final;
    // DEGRADED banner — the crawl never coordinated (wave-0 brainer dead): the report
    // below is built from scout + refine material only, with no steered crawl behind it.
    // Say so at the very top; a silently-normal-looking report would overstate coverage.
    const degradedBanner =
      bs.stopReason === 'brainer-dead'
        ? '> ⚠️ **DEGRADED RUN — the wave-0 brainer died (terminal agent failure after all retries); no steered crawl ran.** This report is synthesized from the scout sweep + refine verification only. Treat coverage as shallow: re-run RR to get the full crawl.\n\n'
        : '';
    this.files['result.md'] = runArgsMd() + degradedBanner + report;
    log(
      '· ' +
        label +
        ' DONE · confidence=' +
        final +
        ' · plan=' +
        (agg!.plan || []).length +
        ' step(s) · openQ=' +
        (agg!.openQuestions || []).length +
        (bogus.length ? ' · citationsBogus=' + bogus.length : ''),
    );
  }

  // CHILD→PARENT CLAIM MERGE (v3 batch 4, multi-brainer only) — right before the winner (or root, if no
  // gate ever passed) finalizes, fold every OTHER brainer's non-retracted claims into its ledger so a
  // branch that did not win still contributes its evidence (run-forensics fix: a child's four-source
  // [settled] finding used to flatten back to "in-flight" crossing branches). Dedupe by norm(quote)+
  // norm(source) against the target's OWN ledger (a fact both branches independently found is one row, not
  // two); new rows get fresh ids from the target's nextClaimId. Stances are DROPPED — a stance.target id is
  // a claim id in the LOSER's ledger, which does not exist (or means something else) in the target's, so it
  // cannot be remapped; re-cluster each merged claim through the target's OWN clusterOf/union-find via the
  // SAME applyLineage helper ingestClaimSeeds uses, using the deterministic lineageKeyOf fallback (a merge
  // never re-invokes the lineageClerk agent — a pure bookkeeping operation, not a fresh-evidence wave).
  // nullAttacks always merge in, with claimIds remapped through the ids that survived the merge (topic-only
  // [] for any that did not — a dupe or an id from a lane the loser never actually ledgered).
  mergeChildClaims(target: BrainerState, losers: BrainerState[]): void {
    const others = losers.filter((l) => l !== target);
    if (!others.length) return;
    for (const loser of others) {
      const idMap = new Map<number, number>(); // loser claim id → target claim id, MERGED claims only
      let dupes = 0;
      const freshMerged: Claim[] = [];
      for (const c of loser.claims) {
        if (c.retracted) continue;
        const key = norm(c.quote) + '|' + norm(c.source);
        const dupe = target.claims.find(
          (t) => !t.retracted && norm(t.quote) + '|' + norm(t.source) === key,
        );
        if (dupe) {
          dupes++;
          continue;
        }
        const merged: Claim = { ...c, id: target.nextClaimId++, cluster: -1, stance: undefined };
        target.claims.push(merged);
        freshMerged.push(merged);
        idMap.set(c.id, merged.id);
      }
      if (freshMerged.length) {
        const keyMap = new Map(freshMerged.map((m) => [m.id, [lineageKeyOf(m)].filter(Boolean)]));
        this.applyLineage(target, freshMerged, keyMap);
      }
      for (const na of loser.nullAttacks)
        target.nullAttacks.push({
          ...na,
          claimIds: na.claimIds
            .map((id) => idMap.get(id))
            .filter((id): id is number => id !== undefined),
        });
      log(
        '⇄ merged ' +
          freshMerged.length +
          ' claims (+' +
          loser.nullAttacks.length +
          ' nullAttacks) from ' +
          loser.name +
          ' into ' +
          target.name +
          ' (' +
          dupes +
          ' dupes dropped)',
      );
    }
    for (const c of target.claims)
      if (!c.retracted)
        c.status = claimStatus(c, target.claims, target.nullAttacks, {
          SETTLED_MIN_CLUSTERS: CONFIG.SETTLED_MIN_CLUSTERS,
        });
  }

  // Finalize (end-only). An opus INITIATOR names the load-bearing facts + the report focus → REFINEMENT: one sonnet REFINE pass per fact,
  // hardening it against the sources → an opus JUDGE judges the hardened answer and drives a bounded remediation loop (the brain DERIVES the
  // answer when one is needed / refine re-checks a mis-hardened fact / the crawl reopens on a real gap) → the opus SYNTHESISER writes the END report.
  async runFinalize(bs: BrainerState): Promise<void> {
    phase(CONFIG.PHASE.finalize);
    const rabbitHolesOut: RabbitHoleOut[] = bs.rabbitHoles
      .map((f) => ({
        id: f.id,
        keyword: f.keyword,
        why: f.why,
        path: f.path || [],
        score: lastScore(f),
        scoreHistory: f.scoreHistory,
        ...(f.note ? { note: f.note } : {}), // B8: surface the per-lane directive in _rabbitHoles.json
      }))
      .sort((a, b) => (b.score ?? -1) - (a.score ?? -1));
    bs.rabbitHolesOut = rabbitHolesOut;
    const topOpen = rabbitHolesOut.slice(0, CONFIG.FINALIZE_TOP_OPEN).map((f) => f.keyword);

    // ── INITIATOR — names the load-bearing facts to harden + sets the report focus ──
    const { facts, synthFocus, artifact: initiatorMd } = await runInitiator(bs, topOpen);
    this.files[padIdx(bs.wave + 4) + '-initiator.md'] = initiatorMd;

    // ── REFINEMENT — one REFINE agent per fact (parallel): adversarially fact-checks, returns the corrected
    // solid claim; refineAndLedger also folds each fact's counter-search outcome into the ledger (v3 batch 4)
    // via applyRefineAttacks — the ONE call site every refine dispatch on this path goes through.
    const refinementFileKey = padIdx(bs.wave + 5) + '-refinement.md';
    log('· finalize · refinement · ' + facts.length + ' fact(s) → refine · ' + refiner.tier);
    let cleanReports = await this.refineAndLedger(
      bs,
      facts,
      '',
      '',
      refinementFileKey,
      CONFIG.PHASE.finalize,
    );
    log('· finalize · refinement DONE · ' + cleanReports.length + ' hardened fact(s)');

    // ── JUDGE loop — terminal skeptic judges, then a bounded remediation loop fixes the single biggest problem and re-judges ──
    // fold the judge's compute-off limitation (returned, not applied by the judge) into openGaps, deduped — the engine
    // is the sole layer that mutates resultSoFar (replace, never alias-mutate a shared object across the loop).
    const foldComputeLimitation = (j: JudgeOut | null): JudgeOut | null => {
      if (j && j.computeLimitation && bs.resultSoFar) {
        const gaps = bs.resultSoFar.openGaps || [];
        if (!gaps.some((g) => g.startsWith(COMPUTE_LIMIT_PREFIX)))
          bs.resultSoFar = { ...bs.resultSoFar, openGaps: [...gaps, j.computeLimitation] };
      }
      return j;
    };
    const judgeLog: JudgeOut[] = [];
    const computeDirectives: string[] = [];
    let pendingDirective = ''; // set only when the judge's last directive was a report-layer fix the
    // remediation loop had no lever for — forwarded to the synthesiser instead of silently dropped.
    let judgement = foldComputeLimitation(await runJudge(bs, cleanReports, synthFocus, 0));
    if (judgement) judgeLog.push(judgement);
    await this.applyJudgeRetractions(bs, judgement, CONFIG.PHASE.finalize);
    let pass = 0;
    while (
      judgement &&
      pass < CONFIG.MAX_JUDGE_PASSES &&
      !(judgement.goalMet && judgement.verificationSound && judgement.computeSound)
    ) {
      pass++;
      const directive = judgement.directive || '';
      const reason = judgement.reasoning || '';
      if (CONFIG.compute && judgement.needsCompute && !judgement.computeSound) {
        // brain FINALIZE-COMPUTE — the brain (code-capable) derives the answer on the hardened facts, per the judge directive
        log('· finalize · judge pass ' + pass + ' → brain finalize-compute · ' + brainer.tier);
        const out = await runBrainerCompute(bs, cleanReports, directive, reason, pass);
        if (out && out.resultSoFar) this.adoptResultSoFar(bs, out.resultSoFar);
        computeDirectives.push(directive || '(derive the answer the goal needs)');
      } else if (!judgement.verificationSound) {
        // RE-REFINE — the judge flagged a mis-hardened / rubber-stamped fact; re-run refine with its directive
        log('· finalize · judge pass ' + pass + ' → re-refine the flagged fact(s)');
        cleanReports = await this.refineAndLedger(
          bs,
          facts,
          directive,
          'r' + pass + '-',
          refinementFileKey,
          CONFIG.PHASE.finalize,
        );
      } else if (
        !judgement.goalMet &&
        judgement.reopenRabbitHoles &&
        judgement.reopenRabbitHoles.length
      ) {
        // CRAWL REOPEN (rare) — a real evidence/coverage gap; reopen the crawl on the judge's leads, then re-harden.
        // reopenDirective (when the judge supplied one) is the reader-facing EXTRACTION directive for the
        // reopened lane; `directive` alone is a poor lane brief — run forensics: "Rewrite the open-differentiator
        // section" (a report-layer fix) was once handed straight to a haiku reader as its research mandate.
        log('· finalize · judge pass ' + pass + ' → reopen the crawl on a real gap');
        await this.reopenCrawl(bs, judgement.reopenRabbitHoles, judgement.reopenDirective || directive);
        cleanReports = await this.refineAndLedger(
          bs,
          facts,
          directive,
          'r' + pass + '-',
          refinementFileKey,
          CONFIG.PHASE.finalize,
        );
      } else {
        // the directive WAS actionable — just at the report layer, not one the crawl/refine/compute levers
        // above can fix. Forward it to the synthesiser instead of the dishonest "no actionable remediation" label.
        log(
          '· finalize · judge pass ' +
            pass +
            ' → no remediation lever fits — directive forwarded to the synthesiser',
        );
        pendingDirective = directive;
        break;
      }
      judgement = foldComputeLimitation(await runJudge(bs, cleanReports, synthFocus, pass));
      if (judgement) judgeLog.push(judgement);
      await this.applyJudgeRetractions(bs, judgement, CONFIG.PHASE.finalize);
    }
    // the FINAL judge verdict's goalMet (null when no judge ever ran) + how many passes ran — feeds metrics.
    bs.goalMet = judgeLog.length ? judgeLog[judgeLog.length - 1].goalMet : null;
    bs.judgePasses = judgeLog.length;

    // JUDGE file — every pass: the four-flag verdict + reasoning + directive + any reopen
    if (judgeLog.length)
      this.files[padIdx(bs.wave + 6) + '-judge.md'] = withPrompt(
        'judge-' + (judgeLog.length - 1),
        '# Judge — finalize-phase terminal skeptic\n\n' +
          judgeLog
            .map(
              (a, i) =>
                '## Pass ' +
                i +
                ' — ' +
                (a.goalMet && a.verificationSound && a.computeSound
                  ? '✓ UPHELD'
                  : '⚔ flagged a problem') +
                '\n\n- goalMet: ' +
                a.goalMet +
                '\n- verificationSound: ' +
                a.verificationSound +
                '\n- needsCompute: ' +
                a.needsCompute +
                '\n- computeSound: ' +
                a.computeSound +
                '\n\n' +
                (a.reasoning || '') +
                (a.directive ? '\n\n**Directive:** ' + a.directive : '') +
                (a.reopenRabbitHoles && a.reopenRabbitHoles.length
                  ? '\n\n**Reopen:**\n' +
                    a.reopenRabbitHoles.map((l) => '- **' + l.keyword + '** — ' + l.why).join('\n')
                  : ''),
            )
            .join('\n\n') +
          '\n',
      );

    // FINALIZE-COMPUTE file — when the brain derived the answer, capture the directive(s) + the derivation folded into `working`
    if (computeDirectives.length)
      this.files['_finalize-compute.md'] =
        '# Finalize compute — the brain derived the answer on the hardened facts\n\n' +
        '## Directive(s)\n\n' +
        computeDirectives.map((d, i) => i + 1 + '. ' + d).join('\n') +
        '\n\n## Derivation (resultSoFar.working)\n\n' +
        ((bs.resultSoFar && bs.resultSoFar.working) || '_none_') +
        '\n';

    // ── SYNTHESISER — writes the END report (always) from the judged answer + the hardened facts; the
    // citation-lint + confidence-floor finish (v3 batch 4) is shared with the multi-brainer winner path.
    // pendingDirective rides along when the judge's last word was a report-layer fix no remediation lever
    // fit — the synthesiser is the one agent that CAN act on it.
    const agg = await runSynthesiser(
      bs,
      cleanReports,
      synthFocus + (pendingDirective ? '\nJudge directive (apply in the report): ' + pendingDirective : ''),
      topOpen,
    );
    this.applyReportFinish(bs, agg, 'finalize');
  }

  // metrics + _rabbitHoles.json + the crawl-tree render, then the final return shape.
  buildResult(bs: BrainerState): RunResult {
    const { synthesiserOut, reportOk, rabbitHolesOut, coord } = bs;
    // D — the real last wave, not `wave - 1` (which under-reported in multi-brainer mode: a child's `wave`
    // field tracks the GLOBAL wave counter, not its own wave count).
    const wavesRun = bs.waveLog.length ? bs.waveLog[bs.waveLog.length - 1].wave : 0;

    const metrics: Metrics = {
      mode: CONFIG.mode,
      dir: CONFIG.DIR,
      wavesRun,
      stopReason: bs.stopReason,
      scoutRabbitHoles: this.scoutRabbitHoles.length,
      prospectorVenues: this.highValueSources.length,
      pursuedTotal: bs.pursuedList.length,
      rabbitHolesFinal: bs.rabbitHoles.length,
      bestOpenScore: bs.bestOpen,
      topScores: bs.topScores,
      // coord is legitimately null when the wave-0 brainer died (stopReason 'brainer-dead')
      // — a degraded run must still deliver its files, never crash here at the finish line.
      done: coord ? coord.stop.done : false,
      reportWritten: reportOk,
      confidence: reportOk ? synthesiserOut!.confidence : null,
      claimsTotal: bs.claims.length,
      nullAttacksTotal: bs.nullAttacks.length,
      chao: bs.chao,
      citationsBogus: bs.citationsBogus,
      citationsAuditFailed: bs.citationsAuditFailed,
      auditCounts: {
        pass: bs.claims.filter((c) => c.audit === 'pass').length,
        fail: bs.claims.filter((c) => c.audit === 'fail').length,
        repinned: bs.quotesRepinned, // those claims already read audit 'pass' above — a distinct count, not a 4th audit value
        unpinned: bs.claims.filter((c) => c.audit === 'unpinned').length,
        pending: bs.claims.filter((c) => c.audit === 'pending').length,
      },
      quotesRepinned: bs.quotesRepinned,
      cachePathsRejected: bs.cachePathsRejected,
      venuesUnrouted: this.highValueSources.filter((v) => !bs.venueStats[v.source]).length,
      goalMet: bs.goalMet,
      judgePasses: bs.judgePasses,
      reopenedLanes: bs.reopenedLaneCount,
    };
    log('■ RR DONE · ' + JSON.stringify(metrics));

    // CLAIM LEDGER artifacts — the full machine-readable ledger, and a human-readable grouped view.
    this.files['_claims.json'] = JSON.stringify(
      { claims: bs.claims, nullAttacks: bs.nullAttacks, vocabulary: bs.vocabulary, chao: bs.chao },
      null,
      2,
    );
    this.files['_claims.md'] = claimsMd(bs);

    // SOURCES artifact (run forensics: an ad-hoc cache-dir sweep once archived 136 foreign files out of
    // 191) — the claim-referenced cache files only, so an archiver's copy step follows claim provenance,
    // never a blind directory sweep. Unique by cachePath — a source many claims share is listed once.
    {
      const seenPaths = new Set<string>();
      const sources: { cachePath: string; source: string }[] = [];
      for (const c of bs.claims) {
        if (c.retracted || !c.cachePath || seenPaths.has(c.cachePath)) continue;
        seenPaths.add(c.cachePath);
        sources.push({ cachePath: c.cachePath, source: c.source });
      }
      this.files['_sources.json'] = JSON.stringify(
        {
          note: 'claim-referenced cache files — the provenance-filtered set an archiver should copy (persist.js mirrors these into resources/)',
          sources,
        },
        null,
        2,
      );
    }

    this.files['_rabbitHoles.json'] = JSON.stringify(
      {
        args: CONFIG.rawArgs, // the COMPLETE set of arguments the run was launched with, verbatim
        query: CONFIG.query,
        mode: CONFIG.mode,
        stopReason: bs.stopReason,
        topScores: bs.topScores,
        highValueSources: this.highValueSources,
        rabbitHoles: rabbitHolesOut,
        pursued: bs.pursuedArchive,
      },
      null,
      2,
    );

    // CRAWL TREE — reconstruct the branching from the pursued-archive paths (the global trail record) and render it visually.
    type TreeNode = {
      kw: string;
      children: Map<string, TreeNode>;
      score: number | null;
      note?: string;
    };
    const treeRoot: TreeNode = { kw: CONFIG.query, children: new Map(), score: null };
    for (const l of bs.pursuedArchive) {
      let cur = treeRoot;
      for (const kw of [...(l.path || []), l.keyword]) {
        const k = norm(kw);
        if (!cur.children.has(k)) cur.children.set(k, { kw, children: new Map(), score: null });
        cur = cur.children.get(k)!;
      }
      cur.score =
        l.scoreHistory && l.scoreHistory.length
          ? l.scoreHistory[l.scoreHistory.length - 1].score
          : null;
      if (l.note) cur.note = l.note; // B8: surface the per-lane directive on the tree leaf
    }
    // treeLines + goalLine are FULL (unclipped) — they feed the persisted _tree.md and the returned `tree`; only the
    // live terminal stream is clipped per-line (CONFIG.TREE_LOG_WIDTH) so the file keeps every keyword/note in full.
    const treeLines: string[] = [];
    const walkTree = (node: TreeNode, prefix: string): void => {
      const kids = [...node.children.values()];
      kids.forEach((c, i) => {
        const last = i === kids.length - 1;
        treeLines.push(
          prefix +
            (last ? '└─ ' : '├─ ') +
            c.kw +
            (c.score != null ? '  [' + c.score + ']' : '') +
            (c.note ? '  ‹' + c.note + '›' : ''),
        );
        walkTree(c, prefix + (last ? '   ' : '│  '));
      });
    };
    walkTree(treeRoot, '');
    const goalLine = 'GOAL: ' + CONFIG.query;
    log('');
    log('🌳 CRAWL TREE — how it branched (goal → lanes pursued · [score]):');
    log(clip(goalLine, CONFIG.TREE_LOG_WIDTH));
    treeLines.forEach((l) => log(clip(l, CONFIG.TREE_LOG_WIDTH)));
    this.files['_tree.md'] =
      '# Crawl tree — how the lanes branched\n\n```\n' +
      goalLine +
      '\n' +
      treeLines.join('\n') +
      '\n```\n';

    return {
      query: CONFIG.query,
      mode: CONFIG.mode,
      dir: CONFIG.DIR,
      stopReason: bs.stopReason,
      done: coord ? coord.stop.done : false, // null on a brainer-dead degraded run
      tree: [goalLine, ...treeLines],
      verdict: reportOk ? synthesiserOut!.verdict : null,
      confidence: reportOk ? synthesiserOut!.confidence : null,
      plan: reportOk ? synthesiserOut!.plan : [],
      openQuestions: reportOk ? synthesiserOut!.openQuestions : [],
      pursued: bs.pursuedList,
      pursuedArchive: bs.pursuedArchive,
      highValueSources: this.highValueSources,
      rabbitHoles: rabbitHolesOut,
      resultSoFar: bs.resultSoFar,
      waveLog: bs.waveLog,
      metrics,
      files: this.files,
    };
  }

  // ═══════════════════════════════════════════════════════════════════════════════════════════════
  // MULTI-BRAINER (maxParallelBrainers > 1) — the brainer tree. The single-brainer path above
  // (runCrawl / runFinalize) is left UNTOUCHED so its proven behavior is preserved; this parallel
  // orchestration runs only when the operator opts in. Shape: a global wave loop runs every ACTIVE
  // brainer's wave concurrently; a brainer that declares done fires a speculative GATE (initiator →
  // refine → judge) WITHOUT pausing the others; the FIRST gate the judge upholds wins → one more
  // last wave → drain → the winner's report is result.md, the rest preserved as result-<name>.md.
  // ═══════════════════════════════════════════════════════════════════════════════════════════════

  // the 01-scout.md artifact (shared/global; the single path writes its own inline copy).
  writeScoutFile(): void {
    const s = this.scout!;
    this.files['01-scout.md'] = withPrompt(
      'scout',
      '# 01 — Scout\n\n**Query:** ' +
        CONFIG.query +
        '\n\n## Landscape\n\n' +
        s.landscape +
        '\n\n## Sources\n\n' +
        s.pages
          .map(
            (p, i) =>
              '### ' +
              (i + 1) +
              ' — ' +
              p.url +
              '\n\n' +
              p.summary +
              '\n\n' +
              (p.rabbitHoles || []).map((l) => '- **' + l.keyword + '** — ' + l.why).join('\n'),
          )
          .join('\n\n') +
        '\n\n## Dead ends\n\n' +
        ((s.deadEnds || []).map((d) => '- ' + d).join('\n') || '_none_') +
        '\n',
    );
  }

  // a brainer's INITIAL pick — one brainer call with NO preceding research (root: scores the scout seeds;
  // child: re-scores its inherited store through its mandate) → sets its first lookupNext. Mirrors wave 0.
  async pickFirst(
    bs: BrainerState,
    seedFindings: Finding[],
    gw: number,
    phaseName: string,
  ): Promise<void> {
    bs.wave = gw;
    const coord = await runBrainer(bs, gw, seedFindings, phaseName, {
      canSpawn: false,
      lastWave: false,
    });
    if (!coord) {
      bs.status = 'drained';
      // Honest stop classification: this lane never coordinated a single wave.
      // For the ROOT this is the whole crawl dying at wave 0 — buildResult and the
      // DEGRADED banner in runFinalize key off stopReason + the missing coord.
      bs.stopReason = 'brainer-dead';
      log('  ✗ ' + bs.name + ' pick-first brainer died → drained');
      return;
    }
    bs.coord = coord;
    applyDeltas(bs, coord, gw);
    this.applyDerivation(bs, coord);
    if (coord.resultSoFar) this.adoptResultSoFar(bs, coord.resultSoFar);
    bs.resultLog.push({ wave: gw, resultSoFar: bs.resultSoFar });
    bs.lookupNext = resolveLookupNext(bs, coord, gw, laneCount);
    bs.topScores.push(
      bs.lookupNext.length ? Math.max(...bs.lookupNext.map((p) => p.score ?? 0)) : 0,
    );
    bs.waveLog.push({
      wave: gw,
      pursued: [],
      newRabbitHoles: 0,
      rabbitHoles: bs.rabbitHoles.length,
      topScore: bs.topScores[bs.topScores.length - 1],
      done: coord.stop.done,
      reason: coord.stop.reason,
    });
    this.files[bs.name + '/wave-' + gw + '.md'] = withPrompt(
      'brainer-' + (bs.isRoot ? '' : bs.name + '-') + 'w' + gw,
      waveMd(gw, coord, bs.lookupNext, seedFindings, bs.rabbitHoles),
    );
    if (coord.stop.lost && !bs.isRoot) bs.status = 'lost';
    else if (coord.stop.done) bs.status = 'done';
    else if (!bs.lookupNext.length) bs.status = 'drained';
  }

  // ONE research wave for ONE brainer: pursue its pending lanes → schedule → read → validate → re-coordinate.
  // Mutates bs; sets bs.status (done / lost / drained) and leaves bs.coord (carrying any spawn) for the caller.
  async runOneWave(
    bs: BrainerState,
    gw: number,
    isLastWave: boolean,
    phaseName: string,
    canSpawn: boolean,
  ): Promise<void> {
    bs.wave = gw;
    const toPursue = bs.lookupNext;
    pursue(bs, toPursue);
    const tag = (bs.isRoot ? '' : bs.name + '-') + 'w' + gw;
    const schedule = await this.scheduleSources(bs, toPursue, tag, phaseName);
    const raw = await runResearchers(bs, toPursue, schedule, tag, phaseName);
    await this.ingestWave(bs, toPursue, raw, gw, phaseName); // v3: claim-ledger ingest (mutates raw's text fields + bs)
    await this.maybeRerunDerivation(bs, gw, phaseName); // v3 STEERING: rerun the stored derivation iff dirty or an input claim changed
    const waveStarved = [...schedule.values()].every((s) => !s || !s.length);
    bs.starvedWaves = waveStarved ? bs.starvedWaves + 1 : 0;
    // B6 — mirror runCrawl's early break: a scheduler-starved wave stops BEFORE findings/validator/brainer
    // ever see it (there is nothing for them to read). Checked right here, not after the brainer dispatch below,
    // so a starved wave never burns a brainer call it cannot do anything useful with.
    if (bs.starvedWaves >= CONFIG.MAX_STARVED_WAVES) {
      bs.status = 'drained';
      bs.stopReason = 'scheduler-starved';
      log(
        '  · ' +
          bs.name +
          ' w' +
          gw +
          ' · scheduler-starved (' +
          bs.starvedWaves +
          ' consecutive empty waves) → drained',
      );
      return;
    }
    const findings: Finding[] = raw.map((r, i) => ({
      rabbitHole: toPursue[i].keyword,
      trail: trailOf(toPursue[i].path, toPursue[i].keyword),
      summary: r ? r.summary : '(researcher failed)',
    }));
    if (CONFIG.debug)
      raw.forEach((r, i) =>
        this.laneRecords.push({
          wave: gw,
          keyword: bs.name + ':' + toPursue[i].keyword,
          assignedVenues: toPursue[i].sources || [],
          summary: r ? r.summary : null,
          rabbitHoles: r ? (r.rabbitHoles || []).map((l) => l.keyword) : [],
        }),
      );
    const beforeAdd = bs.rabbitHoles.length; // D — honest newRabbitHoles count (was hardcoded 0)
    raw.forEach((r, i) => {
      if (!r) return;
      const par = [...(toPursue[i].path || []), toPursue[i].keyword];
      for (const l of r.rabbitHoles || [])
        addRabbitHole(bs, {
          keyword: l.keyword,
          why: l.why,
          path: par,
          wave: gw,
          kind: l.kind ?? 'gap',
        });
      for (const s of r.nextSources || [])
        addRabbitHole(bs, {
          keyword: s.why,
          why: 'followed citation',
          path: par,
          wave: gw,
          ref: s.ref,
          kind: 'citation',
        });
    });
    const newCount = bs.rabbitHoles.length - beforeAdd;
    // mirrors runCrawl's VALIDATOR GATE widening — the reader prompt has always promised "the engine will
    // reopen the lane" on a dead read; before this, deadEnds were never consumed by anything downstream.
    const anyNull = raw.some((r) => !r);
    const anyThin = findings.some((f) => !f.summary || f.summary.length < CONFIG.VALIDATOR_THIN);
    const anyCorrupt = raw.some((r) => r && (r.deadEnds || []).some((d) => /^\s*CORRUPT/i.test(d)));
    const anyDeadNoClaims = raw.some(
      (r) => r && (r.deadEnds || []).length > 0 && !(r.claims || []).length,
    );
    if (anyNull || anyThin || anyCorrupt || anyDeadNoClaims) {
      const requests = toPursue.map((p) => ({ id: p.id, keyword: p.keyword, why: p.why }));
      const vFindings = findings.map((f, i) => ({
        keyword: f.rabbitHole,
        intro:
          (f.summary || '').slice(0, CONFIG.VALIDATOR_INTRO_CHARS) +
          (raw[i] && (raw[i]!.deadEnds || []).length
            ? ' [deadEnds: ' + raw[i]!.deadEnds!.join('; ').slice(0, 200) + ']'
            : ''),
      }));
      const nullLanes = toPursue.filter((p, i) => !raw[i]).map((p) => p.keyword);
      const val = await runValidator(bs, gw, requests, vFindings, nullLanes);
      const failedIds = new Set<number>();
      toPursue.forEach((p, i) => {
        if (!raw[i]) failedIds.add(p.id);
      });
      if (val && Array.isArray(val.checks))
        val.checks.forEach((c) => {
          if (c && c.fulfilled === false && typeof c.id === 'number') failedIds.add(c.id);
        });
      const reopened: string[] = [];
      const cappedGaps: string[] = [];
      for (const id of failedIds) {
        const rh = bs.pursuedArchive.find((r) => r.id === id);
        if (!rh) continue;
        if ((rh.failCount || 0) >= CONFIG.MAX_LANE_REFAILS) cappedGaps.push(rh.keyword);
        else reopened.push(reopenRabbitHole(bs, rh).keyword);
      }
      bs.lastValidatorMissing = [
        ...((val && val.missing) || []),
        ...cappedGaps.map((k) => k + ' (lane retried twice — known gap)'),
      ]
        .join('; ')
        .slice(0, CONFIG.VALIDATOR_MISSING_CHARS);
      bs.validatorLog.push({
        wave: gw,
        enough: val ? val.enough : null,
        reopened,
        cappedGaps,
        missing: (val && val.missing) || [],
      });
    } else bs.lastValidatorMissing = '';
    const coord = await runBrainer(bs, gw, findings, phaseName, { canSpawn, lastWave: isLastWave });
    if (!coord) {
      bs.status = 'drained';
      log('  ✗ ' + bs.name + ' brainer died w' + gw + ' → drained');
      return;
    }
    bs.coord = coord;
    applyDeltas(bs, coord, gw);
    this.applyDerivation(bs, coord);
    if (coord.resultSoFar) this.adoptResultSoFar(bs, coord.resultSoFar);
    // crash-safety checkpoint — a single zero-cost log line, the wave's FINAL state (after the brainer's
    // deltas have landed): recoverable from the workflow's live output, off the critical path.
    if (CONFIG.checkpoint)
      log(CONFIG.CHECKPOINT_MARK + ' w' + gw + ' ' + JSON.stringify(compactCheckpoint(bs)));
    bs.resultLog.push({ wave: gw, resultSoFar: bs.resultSoFar });
    bs.lookupNext = isLastWave ? [] : resolveLookupNext(bs, coord, gw, laneCount);
    bs.topScores.push(
      bs.lookupNext.length ? Math.max(...bs.lookupNext.map((p) => p.score ?? 0)) : 0,
    );
    bs.waveLog.push({
      wave: gw,
      pursued: toPursue.map((p) => p.keyword),
      newRabbitHoles: newCount,
      rabbitHoles: bs.rabbitHoles.length,
      topScore: bs.topScores[bs.topScores.length - 1],
      done: coord.stop.done,
      reason: coord.stop.reason,
    });
    this.files[bs.name + '/wave-' + gw + '.md'] = withPrompt(
      'brainer-' + (bs.isRoot ? '' : bs.name + '-') + 'w' + gw,
      waveMd(gw, coord, bs.lookupNext, findings, bs.rabbitHoles),
    );
    // D — bestOpen kept honest every wave (was never set in this path) so metrics never report bestOpenScore:0
    // while scored leads sit open — mirrors runCrawl's end-of-crawl computation, just refreshed per wave here.
    bs.bestOpen = bs.rabbitHoles.length
      ? Math.max(...bs.rabbitHoles.map((r) => lastScore(r) ?? 0))
      : 0;
    log(
      '  · ' +
        bs.name +
        ' w' +
        gw +
        ' · researchers=' +
        raw.filter(Boolean).length +
        '/' +
        toPursue.length +
        ' · open=' +
        bs.rabbitHoles.length +
        ' · done=' +
        coord.stop.done +
        (coord.stop.lost ? ' · LOST' : ''),
    );
    // note: the scheduler-starved case is handled by the early return above (right after waveStarved is
    // computed) — by the time this classification runs, bs.starvedWaves can no longer be over the cap.
    if (coord.stop.lost && !bs.isRoot) bs.status = 'lost';
    else if (isLastWave)
      bs.status = 'drained'; // the last wave: collect, do not re-gate
    else if (coord.stop.done) bs.status = 'done';
    else if (!bs.lookupNext.length) {
      bs.status = 'drained';
      bs.stopReason = 'rabbithole-dry';
    } else if (CONFIG.mode === 'collect') {
      // a spawned child's plateau window starts at ITS OWN spawn point (topScoresBase), not the parent's
      // history it inherited a clone of — root has topScoresBase=0 so this is slice(1), unchanged.
      const cs = bs.topScores.slice(bs.topScoresBase + 1);
      if (cs.length >= CONFIG.PLATEAU_MIN_WAVES) {
        const peak = Math.max(...cs);
        const win = cs.slice(-CONFIG.PLATEAU_WINDOW);
        if (peak > 0 && win.every((s) => s <= peak * CONFIG.QUERY_PLATEAU)) {
          // CHAO STOP ASSIST — see runCrawl's mirrored gate: a plateau alone does not drain the brainer when
          // the coverage estimate says a lot remains unseen (no chao yet ⇒ the old plateau-only behavior).
          if (bs.chao == null || bs.chao.coverage >= CONFIG.CHAO_COVERAGE_STOP) {
            bs.status = 'drained';
            bs.stopReason = 'collect-dry-plateau';
          } else {
            log(
              '  · ' +
                bs.name +
                ' plateau but coverage ' +
                bs.chao.coverage.toFixed(2) +
                ' < ' +
                CONFIG.CHAO_COVERAGE_STOP +
                ' — continuing',
            );
          }
        }
      }
    }
  }

  // process a brainer's spawn delta (≤1) — spawn a focused child if the maxParallelBrainers / depth caps allow, then aim + seed it.
  // Called SEQUENTIALLY after the parallel wave so the caps are race-free; delegate-and-release drops the branch.
  async processSpawn(parent: BrainerState, gw: number, phaseName: string): Promise<void> {
    const sp = parent.coord && parent.coord.spawn;
    if (!sp || !sp.mandate) return;
    const live = this.liveBrainers.filter(
      (b) => b.status !== 'lost' && b.status !== 'drained',
    ).length;
    if (live >= CONFIG.maxParallelBrainers) {
      log(
        '  · ' +
          parent.name +
          ' spawn refused — maxParallelBrainers cap (' +
          CONFIG.maxParallelBrainers +
          ' live)',
      );
      return;
    }
    if (parent.depth + 1 > CONFIG.MAX_BRAINER_DEPTH) {
      log('  · ' + parent.name + ' spawn refused — depth cap');
      return;
    }
    let branch: RabbitHole | undefined;
    if (typeof sp.id === 'number') branch = parent.rabbitHoles.find((r) => r.id === sp.id);
    const trail = branch
      ? [...(branch.path || []), branch.keyword].join('  →  ')
      : (parent.trail ? parent.trail + '  →  ' : '') + (sp.keyword || sp.mandate);
    const name = 'b' + this.liveBrainers.length + '-' + lab(sp.keyword || sp.mandate);
    const child = spawnBrainer(parent, { name, mandate: sp.mandate, trail });
    this.liveBrainers.push(child); // reserve the live slot synchronously (before the pickFirst await)
    if (branch) parent.rabbitHoles = parent.rabbitHoles.filter((r) => r.id !== branch!.id); // delegate-and-release
    log(
      '  ✚ ' +
        parent.name +
        ' spawned ' +
        name +
        ' — ‹' +
        clip(sp.mandate, CONFIG.MANDATE_CLIP) +
        '›',
    );
    await this.pickFirst(child, [], gw, phaseName); // the child's initial pick, aimed by its mandate
  }

  // the speculative GATE for a brainer that declared done — initiator → refine → judge (no synthesiser). Returns the
  // judge verdict; caches the hardened facts so the winner's report reuses them. Namespaced artifacts; never pauses others.
  async runGate(bs: BrainerState): Promise<JudgeOut | null> {
    const rabbitHolesOut: RabbitHoleOut[] = bs.rabbitHoles
      .map((f) => ({
        id: f.id,
        keyword: f.keyword,
        why: f.why,
        path: f.path || [],
        score: lastScore(f),
        scoreHistory: f.scoreHistory,
        ...(f.note ? { note: f.note } : {}),
      }))
      .sort((a, b) => (b.score ?? -1) - (a.score ?? -1));
    bs.rabbitHolesOut = rabbitHolesOut;
    const topOpen = rabbitHolesOut.slice(0, CONFIG.FINALIZE_TOP_OPEN).map((f) => f.keyword);
    const { facts, synthFocus, artifact: initiatorMd } = await runInitiator(bs, topOpen);
    this.files[bs.name + '/initiator.md'] = initiatorMd;
    const cleanReports = await this.refineAndLedger(
      bs,
      facts,
      '',
      '',
      bs.name + '/refinement.md',
      CONFIG.PHASE.finalize,
    );
    const verdict = await runJudge(bs, cleanReports, synthFocus, 0);
    await this.applyJudgeRetractions(bs, verdict, CONFIG.PHASE.finalize);
    bs.gateCache = { facts, synthFocus, cleanReports, topOpen };
    // gate-vs-finalize judge-pass reconciliation: the gate IS a judge pass on the multi-brainer path.
    bs.goalMet = verdict ? verdict.goalMet : null;
    bs.judgePasses += 1;
    if (verdict)
      this.files[bs.name + '/judge.md'] =
        '# Gate judge — ' +
        bs.name +
        '\n\n- goalMet: ' +
        verdict.goalMet +
        '\n- verificationSound: ' +
        verdict.verificationSound +
        '\n- computeSound: ' +
        verdict.computeSound +
        '\n\n' +
        (verdict.reasoning || '') +
        (verdict.directive ? '\n\n**Directive:** ' + verdict.directive : '') +
        '\n';
    return verdict;
  }

  // the WINNER's report — its gate already upheld the hardened facts, so reuse them and run the synthesiser → result.md.
  async finalizeWinner(bs: BrainerState): Promise<void> {
    phase(CONFIG.PHASE.finalize);
    const gc = bs.gateCache;
    if (!gc) {
      log('  ✗ winner ' + bs.name + ' has no gate cache — falling back to full finalize');
      await this.runFinalize(bs);
      return;
    }
    const agg = await runSynthesiser(bs, gc.cleanReports, gc.synthFocus, gc.topOpen);
    this.applyReportFinish(bs, agg, 'winner ' + bs.name);
  }

  // a non-winning brainer's wrapped-up state — preserved for later review (its living memory + gate verdict if any).
  writeLoserResult(bs: BrainerState): void {
    this.files['result-' + bs.name + '.md'] =
      '# ' +
      bs.name +
      ' — ' +
      (bs.status === 'lost' ? 'LOST branch (dead end)' : 'wrapped up — not the winner') +
      '\n\n_Mandate: ' +
      (bs.mandate || '(root)') +
      '_\n\n_Trail: ' +
      (bs.trail || '(root)') +
      '_\n\n' +
      resultSoFarMd(bs.resultSoFar) +
      '\n';
  }

  // _brainers.json + _brainers-tree.md — the brainer tree: who spawned whom, mandate, outcome, final status.
  writeBrainerTree(): void {
    const recs = this.liveBrainers.map((b) => ({
      name: b.name,
      parent: b.parentName,
      depth: b.depth,
      status: b.status,
      mandate: b.mandate,
      trail: b.trail,
      waves: b.waveLog.length,
      topScores: b.topScores,
      gate: b.gate
        ? {
            goalMet: b.gate.goalMet,
            verificationSound: b.gate.verificationSound,
            computeSound: b.gate.computeSound,
          }
        : null,
      confidence: b.resultSoFar ? b.resultSoFar.confidence : null,
      answer: b.resultSoFar ? clip(b.resultSoFar.answer || '', CONFIG.TREE_ANSWER_CLIP) : null,
    }));
    this.files['_brainers.json'] = JSON.stringify(
      { winner: this.winner ? this.winner.name : null, brainers: recs },
      null,
      2,
    );
    const lines: string[] = [];
    const walk = (parentName: string | null, pre: string): void => {
      const cs = this.liveBrainers.filter((b) => b.parentName === parentName);
      cs.forEach((c, i) => {
        const last = i === cs.length - 1;
        const mark =
          this.winner && c.name === this.winner.name
            ? ' ⚑WINNER'
            : c.status === 'lost'
              ? ' ✗lost'
              : ' (' + c.status + ')';
        lines.push(
          pre +
            (last ? '└─ ' : '├─ ') +
            c.name +
            mark +
            (c.mandate ? '  ‹' + clip(c.mandate, CONFIG.MANDATE_CLIP) + '›' : ''),
        );
        walk(c.name, pre + (last ? '   ' : '│  '));
      });
    };
    walk(null, '');
    log('');
    log('🧠 BRAINER TREE:');
    lines.forEach((l) => log(clip(l, CONFIG.TREE_LOG_WIDTH)));
    this.files['_brainers-tree.md'] =
      '# Brainer tree — the brainer-tree run\n\n```\n' +
      (lines.join('\n') || '(root only)') +
      '\n```\n';
  }

  // the GLOBAL multi-brainer loop (maxParallelBrainers > 1): run every active brainer's wave concurrently each global wave; a done brainer
  // fires its gate without pausing the others; the first upheld gate wins → one last wave → drain.
  async runCrawlMulti(root: BrainerState, scoutRabbitHoles: SeedLead[]): Promise<void> {
    this.writeScoutFile();
    await this.ingestScoutClaims(root, this.scout!); // v3: seed the root's claim ledger from the scout's own claims/newTerms
    scoutRabbitHoles.forEach((l) =>
      addRabbitHole(root, {
        keyword: l.keyword,
        why: l.why,
        path: l.path || [],
        wave: 0,
        kind: l.kind,
      }),
    );
    // v3: the scout's own followed-citation leads seed the store the same way a crawl wave's nextSources do.
    (this.scout!.nextSources || []).forEach((s) =>
      addRabbitHole(root, {
        keyword: s.why,
        why: 'followed citation',
        ref: s.ref,
        kind: 'citation',
        path: [],
        wave: 0,
      }),
    );
    const seedFindings: Finding[] = this.scout!.pages.map((p) => ({
      rabbitHole: p.url,
      summary: p.summary,
    }));
    // brainer-0 (the root's first pick) belongs to the Scout phase, beside scout + prospector — NOT a crawl wave.
    phase(CONFIG.PHASE.scout);
    await this.pickFirst(root, seedFindings, 0, CONFIG.PHASE.scout);

    type GateRec = { bs: BrainerState; promise: Promise<void>; settled: boolean };
    const gates: GateRec[] = [];
    const cap = CONFIG.maxWave === 'auto' ? CONFIG.HARD_CAP : CONFIG.maxWave;
    let gw = 1;
    while (gw <= Math.min(CONFIG.HARD_CAP, cap)) {
      const isLastWave = this.lastWaveTriggered;
      const active = this.liveBrainers.filter((b) => b.status === 'active' && b.lookupNext.length);
      if (!active.length) {
        const pending = gates.filter((g) => !g.settled);
        if (pending.length) {
          await Promise.race(pending.map((g) => g.promise));
          continue; // a gate settled — it may have reactivated a brainer or set the winner; re-evaluate
        }
        break; // no active brainers and no gates in flight → the run is done
      }
      const phaseName = 'Research w' + gw;
      phase(phaseName);
      const canSpawnNow =
        !isLastWave &&
        this.liveBrainers.filter((b) => b.status !== 'lost' && b.status !== 'drained').length <
          CONFIG.maxParallelBrainers;
      log(
        '— global wave ' +
          gw +
          ' · active=' +
          active.length +
          ' · live=' +
          this.liveBrainers.length +
          (isLastWave ? ' · LAST WAVE' : ''),
      );
      await parallel(
        active.map((bs) => () => this.runOneWave(bs, gw, isLastWave, phaseName, canSpawnNow)),
      );
      // ── post-wave (sequential) — spawns first (cap-safe), then fire gates for brainers that declared done ──
      for (const bs of active)
        if (canSpawnNow && bs.status === 'active' && bs.coord && bs.coord.spawn)
          await this.processSpawn(bs, gw, phaseName);
      for (const bs of active) {
        if (bs.status !== 'done') continue;
        bs.status = 'finalizing';
        const rec: GateRec = { bs, settled: false, promise: Promise.resolve() };
        rec.promise = this.runGate(bs).then((verdict) => {
          rec.settled = true;
          bs.gate = verdict;
          const upheld = !!(
            verdict &&
            verdict.goalMet &&
            verdict.verificationSound &&
            verdict.computeSound
          );
          if (upheld) {
            if (!this.winner) {
              this.winner = bs;
              this.lastWaveTriggered = true;
              log('  ⚑ ' + bs.name + ' GATE PASSED — winner; triggering the last wave');
            }
            bs.status = 'won';
          } else {
            const reopen = (verdict && verdict.reopenRabbitHoles) || [];
            if (reopen.length) {
              bs.lookupNext = resolveLookupNext(
                bs,
                {
                  lookupNext: reopen.map((l) => ({
                    keyword: l.keyword,
                    why: l.why,
                    score: CONFIG.INJECT_SCORE,
                    kind: 'inject' as const,
                  })),
                },
                bs.wave,
                laneCount,
              );
              bs.lastValidatorMissing = (verdict && verdict.directive) || '';
              bs.status = bs.lookupNext.length ? 'active' : 'drained';
              log(
                '  ↺ ' +
                  bs.name +
                  ' gate REJECTED — ' +
                  (bs.status === 'active' ? 'continuing on a flagged gap' : 'drained'),
              );
            } else {
              bs.status = 'drained';
              log('  ↺ ' + bs.name + ' gate REJECTED — no concrete gap → drained');
            }
          }
        });
        gates.push(rec);
      }
      if (isLastWave) break;
      gw++;
    }
    await Promise.all(gates.map((g) => g.promise)); // DRAIN — let every in-flight gate settle
    log(
      '■ multi-crawl DONE · brainers=' +
        this.liveBrainers.length +
        ' · winner=' +
        (this.winner ? this.winner.name : 'none'),
    );
  }

  async run(): Promise<RunResult> {
    const scoutRabbitHoles = await runScout(this);
    this.files['02-prospector.md'] = await runProspector(this); // name the high-value venues before the crawl
    // the ROOT brainer — globals (scout + prospector) are now set, copied by reference into it; the root can never declare lost.
    const root = new BrainerState(this, {
      name: 'root',
      parentName: null,
      mandate: '',
      trail: '',
      depth: 0,
    });
    this.liveBrainers = [root];
    let result: RunResult;
    if (CONFIG.maxParallelBrainers <= 1) {
      // ── single-brainer (default) — the proven path, unchanged ──
      await this.runCrawl(root, scoutRabbitHoles);
      await this.runFinalize(root);
      result = this.buildResult(root);
    } else {
      // ── multi-brainer brainer tree (opt-in via maxParallelBrainers > 1) ──
      await this.runCrawlMulti(root, scoutRabbitHoles);
      const winner = this.winner;
      const target = winner || root;
      // CHILD→PARENT CLAIM MERGE — fold every OTHER brainer's evidence into the target's ledger BEFORE it
      // finalizes, so a losing branch's findings still reach the report instead of vanishing with it.
      this.mergeChildClaims(
        target,
        this.liveBrainers.filter((b) => b !== target),
      );
      if (winner) await this.finalizeWinner(winner);
      else await this.runFinalize(root); // no gate ever passed → full finalize on the root
      for (const bs of this.liveBrainers) if (bs !== (winner || root)) this.writeLoserResult(bs);
      this.writeBrainerTree();
      result = this.buildResult(winner || root);
    }
    if (CONFIG.debug)
      this.files['_debug.md'] = await runDebug(this, this.winner || root, result.metrics); // opt-in Debug & Analysis agent → _debug.md
    return result;
  }
}

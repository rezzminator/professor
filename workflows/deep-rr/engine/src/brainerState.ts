import type {
  ChaoStats,
  Claim,
  CleanReport,
  Coord,
  Derivation,
  FactToHarden,
  JudgeOut,
  NullAttack,
  RabbitHole,
  RabbitHoleOut,
  ReportOut,
  ResultLogEntry,
  ResultSoFar,
  ScoutOut,
  SeedLead,
  StopReason,
  Term,
  ValidatorLogEntry,
  Venue,
  WaveLogEntry,
  YieldCalib,
} from './types/index.js';

// the speculative GATE's reusable outputs — runGate hardens these once; finalizeWinner reuses them for the report.
export interface GateCache {
  facts: FactToHarden[];
  synthFocus: string;
  cleanReports: CleanReport[];
  topOpen: string[];
}

// ─────────────────────────────────────────────────────────────────────────────
// BrainerState — ONE brainer's private crawl state in the brainer tree.
// The single-brainer ResearchReport split its state in two: the RUN GLOBALS (scout
// + prospector seed, set once before any brainer exists) stay on ResearchReport;
// the PER-CRAWL state (the store + resultSoFar + the per-wave logs + the crawl
// outcome) moves here, so N brainers each carry their own. The store reducers in
// store.ts already operate on a structural `state` first-arg, so a BrainerState IS
// a StoreState and the reducers work on it unchanged.
//
// The run.ts agent fns read the same FIELD NAMES they read off ResearchReport — the
// per-crawl ones live here, the read-only globals (scout/highValueSources/…) are
// copied BY REFERENCE at construction (set once, shared, never mutated mid-crawl) —
// so each fn only changed its param TYPE, not its field accesses. The engine still
// owns every mutation + every files[name] = … write; it passes the active brainer in.
// ─────────────────────────────────────────────────────────────────────────────

// active = waving · done = declared done, awaiting gate dispatch (transient) · finalizing = a speculative gate is in
// flight · won = its gate passed (the run's winner) · drained = wrapped up without winning · lost = a CHILD abandoned
// its branch as a dead end.
export type BrainerStatus = 'active' | 'done' | 'finalizing' | 'won' | 'drained' | 'lost';

// the read-only run globals every BrainerState shares (ResearchReport satisfies this — the scout + prospector
// populate them before the first brainer is constructed, and nothing mutates them during the crawl).
export interface RunGlobals {
  scout: ScoutOut | null;
  scoutRabbitHoles: SeedLead[];
  highValueSources: Venue[];
  languageGuidance: string;
}

// the identity a brainer is born with — the root has parentName=null (and can never declare lost).
export interface BrainerIdentity {
  name: string; // descriptive, derived from the mandate/branch
  parentName: string | null; // null ⇒ the ROOT brainer
  mandate: string; // the parent-authored directive that aims a child ('' for the root)
  trail: string; // the branch path the child came from ('' for the root)
  depth: number; // root = 0; each spawn increments it (capped by MAX_BRAINER_DEPTH)
}

export class BrainerState {
  // ── identity ──
  name: string;
  parentName: string | null;
  mandate: string;
  trail: string;
  depth: number;
  status: BrainerStatus;
  gate: JudgeOut | null; // the last speculative-gate judge verdict (null until a gate runs)

  // ── run globals (shared by reference — read-only during the crawl) ──
  scout: ScoutOut | null;
  scoutRabbitHoles: SeedLead[];
  highValueSources: Venue[];
  languageGuidance: string;

  // ── StoreState (own, mutable — the reducers in store.ts mutate these) ──
  rabbitHoles: RabbitHole[];
  nextId: number;
  pursuedKeys: Set<string>;
  pursuedRefs: Set<string>;
  pursuedList: string[];
  pursuedArchive: RabbitHole[];

  // ── claim ledger (own — the v3 belief substrate) ──
  claims: Claim[]; // append-only quote-pinned facts; JS assigns ids + computes cluster/status
  nextClaimId: number; // the ledger's own auto-counter (mirrors nextId on the rabbit-hole store)
  nullAttacks: NullAttack[]; // counter-searches that found nothing (challenged-and-survived state)
  vocabulary: Term[]; // community terms of art collected from the pages
  derivation: Derivation | null; // the stored seeded Python artifact + its latest rerun (null until authored)
  derivationDirty: boolean; // a fresh derivation delta landed this wave — the next ingest forces a rerun regardless of which claims changed
  derivationStale: boolean; // the last rerun attempt failed — lastRun is kept but the brainer is told it is stale; cleared on the next successful rerun
  lastChangedClaimIds: Set<number>; // claim ids added/status-changed THIS wave (ingestWave) — drives the "did a derivation input change" rerun test
  yieldCalib: YieldCalib; // per-kind predicted-vs-realized lead-yield EMAs
  clusterOf: Record<string, number>; // lineage KEY → cluster id — the persistent union-find; its OWN keys ARE the "known keys" the lineageClerk is told about (one canonical structure, no second list)
  nextClusterId: number; // next fresh cluster id to mint; 0 is reserved for the shared "unknown lineage" cluster
  chao: ChaoStats | null; // collect-mode coverage estimate over the claim ledger; null until the first collect wave computes it
  venueStats: Record<string, { assigned: number; yielded: number; served: number }>; // per-venue-source lane assignment/yield tally — flags a 0-yield venue to the brainer; served = lanes where the scheduler's chosen sources actually came from this venue (assigned-vs-served reconciliation)
  knownCachePaths: Set<string>; // every cache path the scheduler returned this run; the ingest trust-check accepts a claim cachePath only when known or matching the harvester cache signature
  corruptCachePaths: Set<string>; // cache paths readers reported CORRUPT (spam/mismatched content); the scheduler is told to never return them

  // ── crawl accumulators (own) ──
  resultSoFar: ResultSoFar | null; // this brainer's living memory
  topScores: number[]; // its own decay signal (plateau detection)
  topScoresBase: number; // where THIS brainer's own waves start in topScores (a child slices its plateau window from its spawn point)
  waveLog: WaveLogEntry[];
  resultLog: ResultLogEntry[];
  validatorLog: ValidatorLogEntry[];
  lastValidatorMissing: string;
  lastUnsourced: string; // the last wave's scheduler honesty report (unsourced refs + venue substitutions), threaded into the next brainer prompt
  coord: Coord | null;
  lookupNext: RabbitHole[]; // the pending lanes this brainer pursues next wave (was a runCrawl local — per-brainer now)
  starvedWaves: number; // consecutive empty-schedule waves for THIS brainer (B6 starvation guard)
  gateCache: GateCache | null; // the speculative gate's hardened outputs, reused by finalizeWinner (no re-harden)
  wave: number; // its own wave counter (root: wave 0 scores the scout seeds, 1..N research)
  bestOpen: number;
  stopReason: StopReason | null;

  // ── finalize outcome (own) ──
  rabbitHolesOut: RabbitHoleOut[];
  synthesiserOut: ReportOut | null;
  reportOk: boolean;
  citationsBogus: number; // synthesiser citation lint: [cN] markers stripped because the id was unknown/retracted
  citationsAuditFailed: number; // synthesiser citation lint: [cN] markers stripped because the claim's quote-pin audit failed
  quotesRepinned: number; // claims whose broken quote the auditor replaced with a verified contiguous span
  cachePathsRejected: number; // claims whose cachePath was untrusted (never scheduled + outside the harvester cache) and was stripped to unpinned
  reopenedLaneCount: number; // finalize judge-reopen lanes — feeds metrics.reopenedLanes so crawl-vs-finalize counts reconcile
  goalMet: boolean | null; // this brainer's FINAL judge verdict's goalMet (null when no judge ran)
  judgePasses: number; // how many judge passes ran in finalize for this brainer (including the gate on the multi-brainer path)

  constructor(g: RunGlobals, id: BrainerIdentity) {
    this.name = id.name;
    this.parentName = id.parentName;
    this.mandate = id.mandate;
    this.trail = id.trail;
    this.depth = id.depth;
    this.status = 'active';
    this.gate = null;
    // globals by reference (set once, shared, never mutated mid-crawl)
    this.scout = g.scout;
    this.scoutRabbitHoles = g.scoutRabbitHoles;
    this.highValueSources = g.highValueSources;
    this.languageGuidance = g.languageGuidance;
    // own store
    this.rabbitHoles = [];
    this.nextId = 1;
    this.pursuedKeys = new Set();
    this.pursuedRefs = new Set();
    this.pursuedList = [];
    this.pursuedArchive = [];
    // own claim ledger
    this.claims = [];
    this.nextClaimId = 1;
    this.nullAttacks = [];
    this.vocabulary = [];
    this.derivation = null;
    this.derivationDirty = false;
    this.derivationStale = false;
    this.lastChangedClaimIds = new Set();
    this.yieldCalib = {};
    this.clusterOf = {};
    this.nextClusterId = 1;
    this.chao = null;
    this.venueStats = {};
    this.knownCachePaths = new Set();
    this.corruptCachePaths = new Set();
    // own accumulators
    this.resultSoFar = null;
    this.topScores = [];
    this.topScoresBase = 0;
    this.waveLog = [];
    this.resultLog = [];
    this.validatorLog = [];
    this.lastValidatorMissing = '';
    this.lastUnsourced = '';
    this.coord = null;
    this.lookupNext = [];
    this.starvedWaves = 0;
    this.gateCache = null;
    this.wave = 1;
    this.bestOpen = 0;
    this.stopReason = null;
    // own finalize outcome
    this.rabbitHolesOut = [];
    this.synthesiserOut = null;
    this.reportOk = false;
    this.citationsBogus = 0;
    this.citationsAuditFailed = 0;
    this.quotesRepinned = 0;
    this.cachePathsRejected = 0;
    this.reopenedLaneCount = 0;
    this.goalMet = null;
    this.judgePasses = 0;
  }

  get isRoot(): boolean {
    return this.parentName === null;
  }
}

// SPAWN — a clean deep-copy of the parent into a focused CHILD brainer. New arrays + new Sets (JSON-cloned plain
// data, zero shared references) so neither brainer can corrupt the other's store; the parent's resultSoFar is
// cloned as the child's SEED (the parent-authored `mandate` is what aims it). The run globals propagate by
// reference (the constructor copies them off the parent, which already holds them). depth = parent.depth + 1;
// the child joins at the parent's current wave. The engine calls this when it honors a `spawn` delta (caps first).
export function spawnBrainer(
  parent: BrainerState,
  id: { name: string; mandate: string; trail: string },
): BrainerState {
  const child = new BrainerState(parent, {
    name: id.name,
    parentName: parent.name,
    mandate: id.mandate,
    trail: id.trail,
    depth: parent.depth + 1,
  });
  // deep-copy the parent's store — clean branch (no shared refs): JSON-clone the plain-data arrays, fresh Sets.
  child.rabbitHoles = JSON.parse(JSON.stringify(parent.rabbitHoles));
  child.pursuedArchive = JSON.parse(JSON.stringify(parent.pursuedArchive));
  child.nextId = parent.nextId;
  child.pursuedKeys = new Set(parent.pursuedKeys);
  child.pursuedRefs = new Set(parent.pursuedRefs);
  child.pursuedList = parent.pursuedList.slice();
  child.topScores = parent.topScores.slice();
  // the claim ledger branches with the brainer — JSON-clone the plain-data stores; yieldCalib entries are
  // never mutated in place (updateCalib is pure), so a shallow copy into a fresh object suffices. clusterOf
  // is plain data too (string → number), so a shallow copy is rebuild-safe — no shared reference back to
  // the parent's union-find.
  child.claims = JSON.parse(JSON.stringify(parent.claims));
  child.nextClaimId = parent.nextClaimId;
  child.nullAttacks = JSON.parse(JSON.stringify(parent.nullAttacks));
  child.vocabulary = JSON.parse(JSON.stringify(parent.vocabulary));
  child.derivation = parent.derivation ? JSON.parse(JSON.stringify(parent.derivation)) : null;
  child.derivationDirty = parent.derivationDirty;
  child.derivationStale = parent.derivationStale;
  child.lastChangedClaimIds = new Set(parent.lastChangedClaimIds);
  child.yieldCalib = { ...parent.yieldCalib };
  child.clusterOf = { ...parent.clusterOf };
  child.nextClusterId = parent.nextClusterId;
  child.chao = parent.chao ? { ...parent.chao } : null;
  child.venueStats = JSON.parse(JSON.stringify(parent.venueStats));
  child.knownCachePaths = new Set(parent.knownCachePaths);
  child.corruptCachePaths = new Set(parent.corruptCachePaths);
  child.topScoresBase = parent.topScores.length; // the child's plateau window starts at ITS spawn point
  // the parent AUTHORS the child's resultSoFar — a deep clone of its own living memory, the seed the mandate aims.
  child.resultSoFar = parent.resultSoFar ? JSON.parse(JSON.stringify(parent.resultSoFar)) : null;
  child.wave = parent.wave;
  child.lastUnsourced = parent.lastUnsourced;
  child.citationsAuditFailed = parent.citationsAuditFailed;
  child.quotesRepinned = parent.quotesRepinned;
  child.cachePathsRejected = parent.cachePathsRejected;
  child.reopenedLaneCount = parent.reopenedLaneCount;
  child.goalMet = parent.goalMet;
  child.judgePasses = parent.judgePasses;
  return child;
}

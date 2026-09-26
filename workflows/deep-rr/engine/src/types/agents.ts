// Agent contracts — one entry per agent. For each: an `*Args` type (what its buildPrompt receives) and an
// `*Out` type (its StructuredOutput shape, what the engine reads back). The generic `Agent<Args>` ties an
// agent object's buildPrompt to its Args.
import type { Schema } from './schema.js';
import type { Metrics } from './run.js';
import type {
  ChaoStats,
  ClaimEntities,
  ClaimSeed,
  DerivInput,
  DerivationDelta,
  TermSeed,
  YieldCalib,
} from './claims.js';
import type {
  Confidence,
  CleanReport,
  Effort,
  FactToHarden,
  Finding,
  LaneRecord,
  LeadKind,
  LookupItem,
  Mode,
  NextSource,
  Page,
  RabbitHoleSeed,
  ReadSlice,
  RenameItem,
  RescoreItem,
  ResultLogEntry,
  ResultSoFar,
  SchedulerLane,
  SchedulerSource,
  ScoredLead,
  Spawn,
  Stop,
  Tier,
  Venue,
  WaveLogEntry,
} from './domain.js';

// the shape every agent object exposes; the engine reads tier/effort/schema and calls buildPrompt(args).
export interface Agent<Args> {
  tier: Tier;
  effort: Effort;
  schema: Schema;
  buildPrompt: (args: Args) => string;
}

// ── scout swarm (v3 batch 2s): scoutPlanner decomposes the query + proposes search angles → a `scout`
// probe sweeps each angle → scoutMerger folds every probe into the FINAL, same-shape ScoutOut ──

// stage 1 — scoutPlanner: senses the landscape (1-2 quick WebSearches) then decomposes + proposes angles.
export interface ScoutPlannerArgs {
  query: string;
  mode: Mode;
  net: string;
  researcherNote?: string;
}
// one search angle the planner proposes; each spawns exactly one probe.
export interface ScoutAngle {
  name: string;
  searchQuery: string; // a concrete, distinct query for THIS angle
  why: string;
  lens?: string; // the interpretive lens this probe reads its sources through
}
export interface ScoutPlannerOut {
  decomposition: string; // the query's distinct axes, one line each
  angles: ScoutAngle[];
}

// stage 2 — scout: one PROBE of the swarm, scoped to a single angle (schema unchanged from v2's single
// scout — field-compatible so the merger + both fallback paths can reuse the exact same shape).
export interface ScoutArgs {
  query: string;
  net: string;
  footer: string;
  angleName: string;
  angleWhy: string;
  angleLens: string;
  searchQuery: string;
  index: number; // 1-based: probe i of total
  total: number;
  researcherNote?: string;
}
export interface ScoutOut {
  landscape: string; // 2-3 sentences on what THIS ANGLE revealed (a probe); the merger's FINAL landscape weaves every angle's note into one paragraph
  pages: Page[];
  deadEnds?: string[];
  claims?: ClaimSeed[]; // union of the fetched pages' load-bearing facts, each pinned to a verbatim quote (no digest yet — never carries `stance`)
  nextSources?: NextSource[]; // union of the fetched pages' highest-value outbound citations — no ledger exists yet, so never carries expect/target
  newTerms?: TermSeed[]; // union of the community terms of art the fetched pages use that we did not
}

// stage 3 — scoutMerger: folds every surviving probe's ScoutOut into ONE final ScoutOut (same schema);
// no tools — a plain subagent reducing material the probes already gathered.
export interface ScoutProbeResult {
  name: string; // the angle name
  landscape: string; // the probe's angle note
  pages: Page[];
  claims?: ClaimSeed[];
  nextSources?: NextSource[];
  newTerms?: TermSeed[];
  deadEnds?: string[];
}
export interface ScoutMergerArgs {
  query: string;
  decomposition: string;
  probes: ScoutProbeResult[];
}

// ── prospector ──
export interface ProspectorArgs {
  query: string;
  landscape: string;
  sources: string[];
  thinkerNote?: string;
  researcherNote?: string;
}
export interface SourcesOut {
  highValueSources: Venue[];
  languageGuidance?: string; // routing note for non-English literatures; '' when the topic is English-dominated
  reasoning?: string;
}

// ── brainer ──
export interface BrainerArgs {
  wave: number;
  query: string;
  rubric: string;
  landscape: string;
  pursuedList: string[];
  open: string[];
  findings: Finding[];
  topScores: number[];
  resultSoFar: ResultSoFar | null;
  stop: string;
  mode: Mode;
  venues: Venue[];
  languageGuidance?: string; // non-empty ⇒ route some lanes to the non-English venues
  lastValidatorMissing?: string; // the last wave's validator gaps (reopened lanes + capped known-gaps), 1 line; '' when none
  unsourced?: string; // pre-rendered scheduler honesty report from the last wave (unsourced refs + venue substitutions + payload anomalies); '' when clean — omits the clause
  compute?: boolean;
  computeNote?: string;
  thinkerNote?: string;
  researcherNote?: string;
  // ── brainer-tree context (multi-brainer; all absent/false in a single-brainer run) ──
  isChild?: boolean; // a spawned CHILD brainer — knows its parent + branch, and MAY declare stop.lost
  parentName?: string; // the brainer it spawned from
  mandate?: string; // the parent-authored directive aiming this child at its branch
  trail?: string; // the branch path this child came from
  canSpawn?: boolean; // spawning is still permitted this wave (caps not yet hit) — only then may it emit spawn
  lastWave?: boolean; // last wave: wrap up the answer, request no new research
  // ── v3 STEERING (batch 3) — the ledger-aware frontier; each degrades to '' / undefined / null when its
  // engine-side state is empty, omitting the clause entirely (see buildBrainer) ──
  ledger?: string; // pre-rendered CLAIM LEDGER lines (utils ledgerLines), '' before any claim is ledgered
  calib?: YieldCalib; // predicted-vs-realized lead-yield EMA per kind — the CALIBRATION section (only kinds with n>0 render)
  derivation?: {
    quantiles: Record<string, number>;
    sensitivity: Record<string, number>;
    inputs: DerivInput[];
    stale: boolean;
  } | null; // set only once the rerunner has produced a lastRun; null/undefined ⇒ no DERIVATION STATE / STOP TEST clause
  chao?: ChaoStats | null; // collect-mode coverage estimate — the COVERAGE clause
}
export interface Coord {
  resultSoFar: ResultSoFar;
  rescore: RescoreItem[];
  add: ScoredLead[];
  lookupNext: LookupItem[];
  rename?: RenameItem[];
  drop?: number[];
  spawn?: Spawn; // OPTIONAL, ≤1 per wave: spawn a focused child brainer onto a branch (engine honors the maxParallelBrainers / depth / 1-per-wave caps)
  derivation?: DerivationDelta; // OPTIONAL: author (or re-author) the stored derivation artifact — the engine stores it + reruns it (see engine.ts applyDerivation)
  stop: Stop;
}

// ── brain finalize-compute (the brainer, re-invoked code-capable to derive the final answer on the hardened facts) ──
export interface BrainerComputeArgs {
  query: string;
  resultSoFar: ResultSoFar | null;
  hardenedFacts: CleanReport[];
  directive: string; // the judge's precise derivation directive
  reason: string; // the judge's reasoning, fed forward
  computeNote?: string;
  thinkerNote?: string;
}
export interface BrainComputeOut {
  resultSoFar: ResultSoFar; // the updated memory with the derivation folded into `working`
}

// ── researchScheduler — discovery: picks + sizes the highest-value sources per lane ──
export interface SchedulerLaneInput {
  id: number; // the rabbit-hole id (the scheduler echoes it back so the engine maps sources → lane)
  keyword: string;
  why: string;
  note: string; // the brainer's research directive (what to find + ranked fallbacks)
  venues: Venue[]; // the prospector venues the brainer assigned this lane (prefer these)
  ref?: string; // a concrete url/DOI to take directly instead of searching (a followed citation)
  kind?: LeadKind; // the lane's origin channel — 'attack' lanes get mandatory suspect-surface sourcing
  refetch?: boolean; // the cached copy is corrupted — bypass the cache, never return the existing cache path
}
export interface ResearchSchedulerArgs {
  query: string;
  lanes: SchedulerLaneInput[];
  researcherNote?: string;
  vocabulary?: string; // pre-rendered top community terms of art (utils vocabSummary), '' when bs.vocabulary is empty
  corruptCache?: string[]; // cache paths reported corrupted by earlier readers — never returned as a source path
}
export interface SchedulerOut {
  lanes: SchedulerLane[]; // the chosen, sized sources grouped per lane id
}
// what runScheduler hands back to the engine: the bin-packable schedule plus the honesty side-channels
// (assigned-vs-served venue reconciliation + directive-named refs that could not be sourced). The ENGINE
// folds these into bs (lastUnsourced, venueStats.served) — the run fn stays pure request/response.
export interface ScheduleResult {
  schedule: Map<number, SchedulerSource[]>;
  unsourced: string; // pre-rendered "lane #id: ref — reason" lines; '' when everything named was sourced
  venuesServed: Map<number, string[]>; // per lane id: the subset of its ASSIGNED venues its chosen sources actually came from
}

// ── researcher (a lane READER: reads its assigned cache slice(s) from disk via code, then digests) ──
export interface ResearcherArgs {
  query: string;
  trail: string;
  keyword: string;
  why: string;
  note: string; // the brainer's directive (noteFromBrainer) — what to extract + ranked fallbacks
  footer: string;
  reads: ReadSlice[]; // the char window(s) of the cache file(s) this reader-unit covers
  readerIndex: number; // 1-based: reader i of N on this lane
  readerCount: number; // N — total readers on this lane
  priorAnswer: string; // the running answer handed forward from the earlier readers ('' for reader 1) — renders LAST
  claimDigest: string; // 'c12 claim' one-liners of the existing KEY CLAIMS this reader may target with a stance ('' before any claim exists)
  laneKind?: LeadKind; // this lane's origin channel (RabbitHole.kind); 'attack' ⇒ the reader's primary output is counter-evidence
  researcherNote?: string;
}
// one reader's StructuredOutput: the updated running answer + the leads/claims the slice surfaced.
export interface ReaderOut {
  runningAnswer: string; // the accumulated lane answer after merging this slice into the prior one
  rabbitHoles?: RabbitHoleSeed[];
  nextSources?: NextSource[];
  claims?: ClaimSeed[];
  newTerms?: TermSeed[];
  surprise?: string; // set ONLY when this slice contradicts one of the KEY CLAIMS in the digest
  deadEnds?: string[];
}
// the lane thread's aggregated return (final running answer as `summary` + the collected leads) — what runResearchers yields.
export interface ResearchOut {
  summary: string;
  rabbitHoles?: RabbitHoleSeed[];
  nextSources?: NextSource[]; // top outbound citations the next lane fetches directly
  claims?: ClaimSeed[];
  newTerms?: TermSeed[];
  surprise?: string; // the FIRST non-empty contradiction note across the lane's readers
  deadEnds?: string[];
}

// ── initiator ──
export interface InitiatorArgs {
  query: string;
  resultSoFar: ResultSoFar | null;
  waveLog: WaveLogEntry[];
  landscape: string;
  openRabbitHoles: string[];
  mode?: Mode; // collect ⇒ harden breadth/coverage, not the shape of a single answer
  thinkerNote?: string;
  // ── v3 FINALIZE (batch 4) — the ledger-fed, sensitivity-ranked initiator ──
  ledger?: string; // pre-rendered CLAIM LEDGER digest (utils ledgerLines); '' before any claim is ledgered
  sensitivity?: string; // pre-rendered SENSITIVITY RANKING body (utils sensitivityRanking); '' when no derivation rerun has completed
}
export interface InitiatorOut {
  refinement: { facts: FactToHarden[] };
  synthesiser: { focus: string };
}

// ── refiner ──
export interface RefineArgs {
  net: string;
  query: string;
  fact: string;
  why: string;
  directive?: string; // on a re-run, the judge's precise re-check directive; '' on the first pass
  // ── v3 FINALIZE (batch 4) — the claim this fact is pinned to, when the initiator named one ──
  claimQuote?: string; // the ledger claim's verbatim quote (THE CLAIM AS PINNED)
  claimSource?: string; // the ledger claim's source
}
export interface RefineOut {
  report: string;
  queriesTried: string[]; // the exact counter-searches run — attack-recording, not just fact-hardening
  counterFound: boolean; // true only when a real counter-example/contradiction turned up
  counterNote?: string; // what the counter-evidence was, when counterFound is true
}

// ── judge — the sole terminal skeptic (FINALIZE phase) ──
export interface JudgeArgs {
  query: string;
  resultSoFar: ResultSoFar | null;
  cleanReports: CleanReport[];
  focus: string; // the deliverable spec the answer must meet (the initiator's report focus)
  openRabbitHoles: string[]; // the leftover/unpursued open rabbit-holes — the judge weighs whether a real gap remains and may reopen the crawl on it
  compute: boolean; // false ⇒ no derivation path; the judge sets needsCompute false
  mode?: Mode; // collect ⇒ gate goalMet on inventory-completeness + per-item verification, not a single-answer goal
  computeNote?: string;
  thinkerNote?: string;
  // ── v3 FINALIZE (batch 4) — reconcile + retraction powers + independence discipline ──
  ledger?: string; // pre-rendered CLAIM LEDGER digest (utils ledgerLines); '' before any claim is ledgered
  survivedAttacks?: string[]; // "topic → cN (queries: …)" lines — challenged AND survived (completed counter-searches that found nothing)
  neverChallenged?: string[]; // key claims with no nullAttack and no attacksSurvived — never put to the test
  computedConfidence?: Confidence; // the machinery-computed confidence over the current key claims (lower-only discipline)
  stop?: { done: boolean; reason: string }; // THE CRAWL'S LAST WORD — the crawl's own final stop, so the judge cannot silently override remaining work
}
export interface JudgeOut {
  goalMet: boolean;
  verificationSound: boolean;
  needsCompute: boolean;
  computeSound: boolean;
  reasoning: string;
  directive?: string; // the precise fix/derivation to perform when not satisfied; '' when satisfied
  reopenRabbitHoles?: RabbitHoleSeed[]; // only for a genuine evidence/coverage gap that needs the crawl; else empty
  reopenDirective?: string; // ONLY with reopenRabbitHoles: the reader-facing EXTRACTION directive for the reopened lane (what to find in the fetched pages) — distinct from `directive`, which fixes the refine/report layer
  computeLimitation?: string; // engine-applied (not from the model): the compute-off stated limitation runJudge returns for the engine to fold into openGaps
  retractClaimIds?: number[]; // ledger claim ids whose evidence is discredited (retraction/fabrication/misattribution) — the engine retracts them and recomputes everything downstream
}

// ── validator — the per-wave crawl coverage gate (distinct from the terminal judge) ──
export interface ValidatorRequest {
  id: number;
  keyword: string;
  why: string;
}
export interface ValidatorFinding {
  keyword: string;
  intro: string;
}
export interface ValidatorArgs {
  query: string;
  requests: ValidatorRequest[]; // the wave's lookupNext picks
  findings: ValidatorFinding[]; // each lane's return, intro only — kept cheap
  nullLanes: string[]; // keywords of lanes that returned nothing
}
export interface ValidatorCheck {
  id: number;
  fulfilled: boolean;
  reason?: string;
}
export interface ValidatorOut {
  checks: ValidatorCheck[];
  enough: boolean;
  missing?: string[]; // gaps still open, threaded into the next brainer
}

// ── synthesiser ──
export interface SynthesiserArgs {
  mode: Mode;
  query: string;
  landscape: string;
  resultSoFar: ResultSoFar | null;
  waveLog: WaveLogEntry[];
  cleanReports: CleanReport[];
  focus: string;
  openRabbitHoles: string[];
  compute?: boolean; // false ⇒ no derivation was run; never present a `working` derivation even if one leaked into resultSoFar
  thinkerNote?: string;
  // ── v3 FINALIZE (batch 4) — cite the ledger, confidence lower-only ──
  ledger?: string; // pre-rendered CLAIM LEDGER digest (utils ledgerLines); '' before any claim is ledgered
  nullAttacksSummary?: string[]; // "topic → cN" one-liners — challenged and survived, for the report's own honesty
  computedConfidence?: Confidence; // the machinery-computed confidence over the current key claims (lower-only discipline)
}
export interface ReportOut {
  report: string;
  verdict: string;
  confidence: Confidence;
  plan: string[];
  openQuestions: string[];
}

// ── debug analyst ──
export interface DebugAnalystArgs {
  query: string;
  focus: string;
  metrics: Metrics; // the run's diagnostic metrics, rendered via plain()
  waveLog: WaveLogEntry[];
  resultLog: ResultLogEntry[];
  highValueSources: Venue[];
  laneRecords: LaneRecord[];
}
export interface DiagOut {
  diagnosis: string;
}

// ── claimAuditor — batched mechanical quote audit (v3 ledger clerk) ──
export interface ClaimAuditItem {
  id: number;
  claim: string;
  quote: string;
  cachePath: string;
}
export interface ClaimAuditArgs {
  items: ClaimAuditItem[];
}
export interface ClaimAuditCheck {
  id: number;
  verdict: 'pass' | 'fail' | 'repinned'; // repinned = quote not found as sent, but a contiguous span carrying the claim WAS located — newQuote holds it
  note?: string;
  newQuote?: string; // repinned only: the verbatim contiguous span from the file that replaces the broken quote
}
export interface ClaimAuditOut {
  checks: ClaimAuditCheck[];
}

// ── lineageClerk — canonicalizes provenance entities for JS union-find clustering (v3 ledger clerk) ──
export interface LineageClerkItem {
  id: number;
  source: string;
  entities?: ClaimEntities;
}
export interface LineageClerkArgs {
  items: LineageClerkItem[];
  knownKeys: string[]; // canonical keys already in use this run — same-as spellings must reuse them
}
export interface LineageLink {
  id: number;
  keys: string[];
}
export interface LineageClerkOut {
  links: LineageLink[];
}

// ── rerunner — re-executes the stored derivation artifact with current inputs (v3 ledger clerk) ──
export interface RerunnerArgs {
  code: string;
  inputsJson: string; // bs.derivation.inputs, JSON-stringified verbatim — the script's one JSON argument
}
export interface RerunnerOut {
  ok: boolean;
  quantiles?: Record<string, number>;
  sensitivity?: Record<string, number>;
  note?: string; // the error message when ok is false
}

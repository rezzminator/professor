// Domain types — the crawl's data model, shared across the engine, store, utils, and agents.
import type { YieldCalib } from './claims.js';

// Run-level enums.
export type Mode = 'goal' | 'collect';
export type Tier = 'haiku' | 'sonnet' | 'opus';
export type Effort = 'low' | 'medium' | 'high' | 'xhigh';
export type Confidence = 'high' | 'medium' | 'low';

// ── resultSoFar — the brainer's living memory, carried wave to wave ──
export type EvidenceStatus = 'settled' | 'tentative' | 'contested';
export interface Evidence {
  fact: string;
  value: string;
  source: string;
  status: EvidenceStatus;
}
export interface Assumption {
  claim: string; // a working assumption the answer currently leans on
  basis: string; // what it rests on, and how firm that is
}
export interface ResultSoFar {
  answer: string;
  confidence: string;
  evidence?: Evidence[]; // DEPRECATED — the ledger (bs.claims) is now the source of truth; optional so a finalize agent still rendering it does not break
  keyClaimIds: number[]; // the ledger claim ids the answer currently rests on (load-bearing) — the brainer's evidence pointer, REQUIRED now that the ledger is the source of truth
  assumptions?: Assumption[];
  resolved: string[];
  openGaps: string[];
  tensions: string[];
  working: string;
}

// ── rabbit-hole store ──
export interface ScoreEntry {
  wave: number;
  score: number;
}
// a lead's origin channel — which footer/engine channel surfaced it (keys the yieldCalib table).
export type LeadKind = 'seed' | 'gap' | 'citation' | 'attack' | 'entity' | 'origin' | 'inject';
// an OPEN rabbit-hole in the id-keyed store.
export interface RabbitHole {
  id: number;
  keyword: string;
  why: string;
  score: number | null;
  scoreHistory: ScoreEntry[];
  path: string[];
  sources?: string[];
  note?: string; // the brainer's research directive for this lane (what to find + ranked fallbacks); rides to the scheduler + reader as noteFromBrainer
  ref?: string; // a concrete fetch target (URL/DOI) this lane should fetch directly instead of WebSearching
  kind?: LeadKind; // the lead's origin channel — keys the yieldCalib table
  failCount?: number; // how many times the validator has reopened this lane after a failed/thin return (capped)
  refetch?: boolean; // the cached copy is corrupted — the scheduler must bypass the cache for this lane (never reuse the poisoned path)
}
// a footer-surfaced lead (scout / researcher) before it enters the store.
export interface RabbitHoleSeed {
  keyword: string;
  why: string;
  kind?: LeadKind; // the lead's origin channel — gap (source's own vocabulary), attack (counter-evidence), entity (a recurring author/venue/dataset)
}
// a follow-the-citation target the page-reader surfaces: a concrete URL/DOI the page points to, worth fetching directly.
export interface NextSource {
  ref: string;
  why: string;
  expect?: 'support' | 'attack' | 'neutral'; // whether following it is expected to bear on `target`, or is neutral
  target?: number; // id of the existing claim this source is expected to support or attack
}
// a scout/fresh lead carrying its inherited trail, ready to seed the store.
export interface SeedLead {
  keyword: string;
  why: string;
  path: string[];
  ref?: string; // when set, the lane fetches this URL/DOI directly (a followed citation)
  kind?: LeadKind; // the lead's origin channel — gap/attack/entity (reader-surfaced), citation (followed link), seed (scout)
}
// a brainer-originated lead to PARK in the store (`add`).
export interface ScoredLead {
  keyword: string;
  why: string;
  score: number;
  kind?: LeadKind; // the lead's origin channel — gap/attack/entity/origin, when the brainer sets one
}
// arguments to addRabbitHole — a seed plus optional score/path/ref/kind and the wave it is added on.
export interface AddRabbitHoleArgs {
  keyword: string;
  why?: string;
  path?: string[];
  score?: number;
  wave: number;
  ref?: string; // a concrete fetch target (URL/DOI) for the lane to fetch directly
  kind?: LeadKind; // the lead's origin channel, stored on the new entry
}

// ── scout / prospector / researcher findings ──
export interface Page {
  url: string;
  summary: string;
  rabbitHoles?: RabbitHoleSeed[];
}
export interface Venue {
  source: string;
  goodFor: string;
  lang?: string; // venue's language (ISO-ish code or name); absent ⇒ English
}
// a researcher's page-reading handed back to the brainer.
export interface Finding {
  rabbitHole: string;
  summary: string;
  trail?: string;
}

// ── research scheduler / reader (B4/B5) ──
// a sized source the scheduler discovered for a lane: the cache path + its token/char size, ready for bin-packing.
export interface SchedulerSource {
  source: string; // the exact url or DOI
  path: string; // the local cache file path returned by the size_only fetch
  size: number; // size in tokens (the over-count heuristic from size_only)
  chars: number; // size in raw characters (drives the char windows)
}
// one scheduler-discovered lane: its rabbit-hole id + the sources chosen for it, plus the honesty
// side-channels — the assigned-vs-served venue reconciliation and directive-named refs that could not be sourced.
export interface SchedulerLane {
  id: number;
  sources: SchedulerSource[];
  venuesServed?: string[]; // the subset of this lane's ASSIGNED venue source strings its chosen sources actually came from; [] when none
  unsourced?: { ref: string; reason: string }[]; // directive-named refs/venues that could not be fetched — reported honestly, never silently substituted
}
// one packed read instruction handed to a reader: a char window [offset, offset+limit) of one cache file.
export interface ReadSlice {
  source: string; // the url/DOI this slice came from (a label for the reader)
  cachePath: string; // the cache file to read from disk
  offset: number; // char offset to start at
  limit: number; // number of chars to read
}

// ── brainer deltas (its per-wave output, see agents.ts Coord) ──
export interface RescoreItem {
  id: number;
  score: number;
}
export interface RenameItem {
  id: number;
  keyword: string;
  why?: string;
}
// one `lookupNext` item: EITHER {id} (a stored lead) OR {keyword,why,score,…} (originate-and-pursue-now).
export interface LookupItem {
  id?: number;
  keyword?: string;
  why?: string;
  score?: number;
  sources?: string[];
  note?: string; // the research directive for this lane — what to find + ranked fallbacks; steers BOTH the scheduler (source pick) and the reader (extraction); distinct from `why`
  ref?: string; // a concrete fetch target (URL/DOI) the lane fetches directly instead of WebSearching
  kind?: LeadKind; // the lead's origin channel when ORIGINATING a lane — passed through to the store
  refetch?: boolean; // brainer-set corrupted-cache remediation — rides to the store entry and on to the scheduler
}
export interface Stop {
  done: boolean;
  reason: string;
  lost?: boolean; // CHILD brainers only: the branch proved a dead end → abandon it (quiet death, no finalize). The root can never set this.
}
// a brainer's OPTIONAL, at-most-one-per-wave spawn directive: aim a focused child brainer at a branch worth a dedicated
// brain. `id` hands off an open rabbit-hole; or `keyword`+`why` originate the branch. `mandate` is the parent-authored
// directive that aims the child. Only emitted when the brainer is SURE the branch deserves the (expensive) spawn.
export interface Spawn {
  id?: number;
  keyword?: string;
  why?: string;
  mandate: string;
}

// ── finalize ──
export interface FactToHarden {
  fact: string;
  why: string;
  claimId?: number; // the ledger claim this fact corresponds to, when the initiator names one — threads into the refiner (its pinned quote/source) and the engine's post-refine ledger mutation
}
// a hardened fact handed to the brain finalize-compute + judge + synthesiser.
export interface CleanReport {
  fact: string;
  why: string;
  clean: string;
}

// ── per-wave / per-run logs ──
export interface WaveLogEntry {
  wave: number;
  pursued: string[];
  newRabbitHoles?: number;
  rabbitHoles?: number;
  topScore: number;
  done: boolean;
  reason: string;
}
export interface ValidatorLogEntry {
  wave: number;
  enough: boolean | null;
  reopened: string[]; // lanes moved back to the open store for re-pursuit
  cappedGaps: string[]; // lanes that failed too often — surfaced to the brainer as known gaps, not reopened
  missing: string[];
}
export interface ResultLogEntry {
  wave: number;
  resultSoFar: ResultSoFar | null;
}
export interface LaneRecord {
  wave: number;
  keyword: string;
  assignedVenues: string[];
  summary: string | null;
  rabbitHoles: string[];
}

// the minimal store surface the reducers in store.ts read+mutate; ResearchReport satisfies it too.
export interface StoreState {
  rabbitHoles: RabbitHole[];
  nextId: number;
  pursuedKeys: Set<string>;
  pursuedRefs: Set<string>; // norm(ref) of every pursued URL/DOI — so a followed citation is never fetched twice
  pursuedList: string[];
  pursuedArchive: RabbitHole[];
  yieldCalib?: YieldCalib; // per-kind predicted-vs-realized lead-yield EMAs — resolveLookupNext's selection-only multiplier; absent ⇒ neutral (calibFactor defaults to 1)
}

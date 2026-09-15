// Claim-ledger types (v3) — the belief substrate: quote-pinned claims, completed counter-searches
// (nullAttacks), the community vocabulary, the stored derivation, and the lead-yield calibration
// table. The ledger is APPEND-ONLY: JS assigns claim ids and computes cluster/status — an LLM never
// rewrites it (the brainer points at it via keyClaimIds).

// audit verdict — pending: not yet checked; pass/fail: the claimAuditor's grep verdict;
// unpinned: no cachePath to grep (e.g. a search-snippet claim).
export type ClaimAudit = 'pending' | 'pass' | 'fail' | 'unpinned';
// status — COMPUTED from ledger topology (utils claimStatus), never asserted by a model.
export type ClaimStatus = 'settled' | 'tentative' | 'contested';
export type StanceKind = 'supports' | 'attacks';
// the provenance entities a claim's source exposes — the lineageClerk's clustering signal.
export interface ClaimEntities {
  authors?: string[];
  funder?: string;
  dataset?: string;
  venue?: string;
}
// a claim's bearing on an EXISTING claim: it supports or attacks the target claim id.
export interface ClaimStance {
  target: number;
  kind: StanceKind;
}
// one quote-pinned fact — the evidence unit.
export interface Claim {
  id: number;
  claim: string;
  value?: string;
  quote: string; // VERBATIM from the source (≤ QUOTE_MAX_CHARS) — what the auditor greps the cache for
  source: string;
  cachePath?: string; // the cache file the quote is pinned to; absent ⇒ audit 'unpinned'
  entities?: ClaimEntities;
  cluster: number; // union-find root; 0 = the shared "unknown lineage" cluster (guilty until proven independent)
  audit: ClaimAudit;
  status: ClaimStatus;
  stance?: ClaimStance;
  attacksSurvived: number; // completed counter-searches this claim has survived
  counter?: string; // the counter-evidence that contested it, when one was found
  retracted: boolean; // the judge discredited it — excluded from all counting
  wave: number;
  lane: string; // the lane keyword that produced it
}
// a claim as a reader/scout EMITS it — the pre-ledger form (mirrors RabbitHoleSeed vs RabbitHole in
// domain.ts): the engine assigns id/cluster/audit/status/etc when it enters bs.claims. The scout's
// items never carry `stance` (there is no digest yet — nothing to bear on).
export interface ClaimSeed {
  claim: string;
  value?: string;
  quote: string; // VERBATIM span (≤ QUOTE_MAX_CHARS) that carries the claim
  source: string;
  cachePath?: string; // local cache file this quote can be verified against, ONLY as reported by the fetch tool
  entities?: ClaimEntities;
  stance?: ClaimStance; // researcher only — bears on one of the KEY CLAIMS named in its digest
}
// a counter-search that found NOTHING — first-class state ("challenged and survived" ≠ "never challenged").
export interface NullAttack {
  topic: string;
  claimIds: number[];
  queries: string[];
  wave: number;
  phase: string;
}
// one community term of art (the vocabulary store).
export interface Term {
  term: string;
  gloss?: string;
  uses: number;
}
// a vocabulary term as a reader/scout EMITS it — the pre-ledger form (mirrors ClaimSeed vs Claim);
// `uses` is engine-owned (incremented on ingest), never supplied by a model.
export interface TermSeed {
  term: string;
  gloss?: string;
}
// one input of the stored derivation; prior=true ⇒ a wide-prior placeholder, not evidence-backed.
export interface DerivInput {
  name: string;
  dist: string;
  claimIds: number[];
  prior: boolean;
}
// the stored, pure, seeded Python artifact + its latest rerun (lastRun null until the rerunner has run).
export interface Derivation {
  code: string;
  inputs: DerivInput[];
  lastRun: {
    quantiles: Record<string, number>;
    sensitivity: Record<string, number>;
    wave: number;
  } | null;
}
// what the brainer emits when authoring/re-authoring a derivation (a COORD delta) — the engine adds
// lastRun when it stores it (see Derivation above); re-emitted only to change the code or the inputs.
export interface DerivationDelta {
  code: string;
  inputs: DerivInput[];
}
// per-kind predicted-vs-realized lead-yield table — n observations, ratio = the EMA of realized/predicted.
export type YieldCalib = Record<string, { n: number; ratio: number }>;
// the chao1 coverage estimate (collect mode) — unseen ≈ estimated undiscovered claim groups; coverage =
// observed groups / (observed + unseen). null on BrainerState until the first collect-mode wave computes it.
export interface ChaoStats {
  unseen: number;
  coverage: number;
}

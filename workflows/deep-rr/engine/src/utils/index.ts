import { CONFIG } from '../config.js';
import type {
  ChaoStats,
  Claim,
  ClaimEntities,
  ClaimStatus,
  Confidence,
  Derivation,
  DerivInput,
  Finding,
  LeadKind,
  NullAttack,
  ReadSlice,
  ResultSoFar,
  SchedulerSource,
  ScoreEntry,
  Stop,
  Term,
  Venue,
  YieldCalib,
} from '../types/index.js';

// ─────────────────────────────────────────────────────────────────────────────
// Pure helpers — stateless transforms + the file/markdown renderers.
// ─────────────────────────────────────────────────────────────────────────────
export const norm = (s: string | null | undefined): string =>
  (s || '')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, ' ')
    .trim();
// normalize a URL/DOI for dedup — drop scheme, www, the doi.org resolver prefix, and any trailing slash so
// "https://doi.org/10.x" and "10.X" collapse to one key (the fetch tool resolves either form).
export const normRef = (s: string | null | undefined): string =>
  (s || '')
    .toLowerCase()
    .trim()
    .replace(/^https?:\/\//, '')
    .replace(/^www\./, '')
    .replace(/^(?:dx\.)?doi\.org\//, '')
    .replace(/\/+$/, '');
export const lab = (s: string): string => norm(s).replace(/ /g, '-').slice(0, 24);
export const padIdx = (n: number): string => String(n).padStart(2, '0');
// clip — when s exceeds n chars, keep the first n-3 and append '…'; shorter strings pass through unchanged.
export const clip = (s: string, n: number): string => (s.length > n ? s.slice(0, n - 3) + '…' : s);
export const lastScore = (r: { scoreHistory: ScoreEntry[] }): number | null =>
  r.scoreHistory.length ? r.scoreHistory[r.scoreHistory.length - 1].score : null;
// one-line render of an open store entry for the brainer: `#id [last score or "new"] keyword — why`
// (a ` ↪ ref` suffix flags a lead that carries a concrete URL/DOI to fetch directly; a trailing kind tag
// surfaces the lead's origin channel when set, ⚔-prefixed for 'attack' so a pending attack lane stands out).
export const openLine = (r: {
  id: number;
  keyword: string;
  why: string;
  scoreHistory: ScoreEntry[];
  ref?: string;
  kind?: LeadKind;
}): string =>
  '#' +
  r.id +
  ' [' +
  (r.scoreHistory.length ? r.scoreHistory[r.scoreHistory.length - 1].score : 'new') +
  '] ' +
  r.keyword +
  ' — ' +
  r.why +
  (r.ref ? ' ↪ ' + r.ref : '') +
  (r.kind ? (r.kind === 'attack' ? ' ⚔attack' : ' ·' + r.kind) : '');

// plain() — render a value as compact PLAIN TEXT for interpolation INTO an agent prompt (replaces JSON.stringify-in-prompts: less noise, no
// braces/quotes). string/number/boolean → as-is; array → one "- el" line each (recursing, nested indented two spaces); object → "key: value"
// per key, SKIPPING any key whose value is null/undefined/''/[]/{} unless opts.keep names it (those render "key: (none)"); nested indent two spaces.
export const isEmpty = (v: unknown): boolean =>
  v == null ||
  v === '' ||
  (Array.isArray(v) && v.length === 0) ||
  (typeof v === 'object' && !Array.isArray(v) && Object.keys(v as object).length === 0);
export function plain(value: unknown, opts?: { keep?: string[] }): string {
  opts = opts || {};
  const keep = opts.keep || [];
  if (value == null) return '';
  const t = typeof value;
  if (t === 'string' || t === 'number' || t === 'boolean') return String(value);
  if (Array.isArray(value)) {
    return value
      .map((el: unknown) =>
        plain(el, opts)
          .split('\n')
          .map((l, i) => (i === 0 ? '- ' : '  ') + l)
          .join('\n'),
      )
      .join('\n');
  }
  const lines: string[] = [];
  for (const k of Object.keys(value as Record<string, unknown>)) {
    const v = (value as Record<string, unknown>)[k];
    if (isEmpty(v)) {
      if (keep.includes(k)) lines.push(k + ': (none)');
      continue;
    }
    if (typeof v === 'object') {
      lines.push(k + ':');
      for (const ln of plain(v, opts).split('\n')) lines.push('  ' + ln);
    } else {
      lines.push(k + ': ' + String(v));
    }
  }
  return lines.join('\n');
}

// the HIDDEN lane cap: the brainer is never told a count — JS clamps to laneCount (AUTO_CAP in auto) here.
export const laneCount: number =
  CONFIG.parallelLaneResearchAgentsPerWave === 'auto'
    ? CONFIG.AUTO_CAP
    : CONFIG.parallelLaneResearchAgentsPerWave;
export const trailOf = (path: string[], keyword?: string): string =>
  [clip(CONFIG.query, CONFIG.MANDATE_CLIP)]
    .concat(path || [], keyword ? [keyword] : [])
    .join('  →  ');

// chunk — split an array into groups of at most `size` items each (the v3 ledger clerks — claimAuditor/
// lineageClerk — batch a wave's claim list into bounded agent calls this way). size ≤ 0 defensively
// degrades to one whole-array chunk (never an infinite loop) — empty input still yields [].
export function chunk<T>(items: T[], size: number): T[][] {
  if (!items.length) return [];
  if (size <= 0) return [items];
  const out: T[][] = [];
  for (let i = 0; i < items.length; i += size) out.push(items.slice(i, i + size));
  return out;
}

// map the brainer's assigned source-identifier strings back to the full {source, goodFor} venue objects (for the
// researcher prompt) — looked up against the prospector's high-value venue set on rr; an unknown id renders bare.
export const venuesFor = (rr: { highValueSources: Venue[] }, sources?: string[]): Venue[] => {
  if (!sources || !sources.length) return [];
  return sources.map(
    (s) => rr.highValueSources.find((v) => v.source === s) || { source: s, goodFor: '' },
  );
};

// ─────────────────────────────────────────────────────────────────────────────
// Claim-ledger helpers (v3) — pure functions over the append-only claim ledger.
// Status + confidence are COMPUTED here from ledger topology, never asserted by a model.
// ─────────────────────────────────────────────────────────────────────────────

// scrubArtifacts — deterministic removal of structured-output/tool-call leakage that occasionally bleeds
// into a reader's returned text (real bug from run forensics: raw `</parameter>` tags bled into lane
// findings and propagated downstream). Strips every COMPLETE open/close tag matching the known leakage
// patterns, then a trailing UNCLOSED fragment of one (the harness cut the emission off mid-tag) — ordinary
// text is left alone: a lone '<' is only stripped when what follows it is a genuine prefix of one of these
// tag names (so "5 < 10" or "x<y" survive untouched).
const ARTIFACT_TAG = /<\/?(?:StructuredOutput|parameter|invoke|function_[a-z]+)[^>]*>/g;
const ARTIFACT_TAG_NAMES = ['StructuredOutput', 'parameter', 'invoke'];
const isArtifactTagPrefix = (frag: string): boolean =>
  !!frag &&
  (ARTIFACT_TAG_NAMES.some((n) => n.startsWith(frag)) ||
    'function'.startsWith(frag) ||
    /^function_[a-z]*$/.test(frag));
export function scrubArtifacts(s: string): string {
  if (!s) return s;
  const out = s.replace(ARTIFACT_TAG, '');
  const lastLt = out.lastIndexOf('<');
  if (lastLt === -1 || out.slice(lastLt).includes('>')) return out;
  const frag = out.slice(lastLt + 1).replace(/^\//, ''); // a closing-tag fragment drops its leading '/' first
  return isArtifactTagPrefix(frag) ? out.slice(0, lastLt) : out;
}

// scrubEntities — null-scrub a claim's provenance as emitted by a worker model. The entity schema is
// null-tolerant (a hard 'must be string' on funder/dataset failed whole reader payloads into retries), so
// ingest normalizes here: keep only non-empty strings, drop null/junk, undefined when nothing survives.
export function scrubEntities(e: unknown): ClaimEntities | undefined {
  if (!e || typeof e !== 'object' || Array.isArray(e)) return undefined;
  const raw = e as Record<string, unknown>;
  const out: ClaimEntities = {};
  const str = (v: unknown): string | undefined =>
    typeof v === 'string' && v.trim() ? v : undefined;
  const authors = Array.isArray(raw.authors)
    ? (raw.authors.filter((a) => typeof a === 'string' && a.trim()) as string[])
    : [];
  if (authors.length) out.authors = authors;
  const funder = str(raw.funder);
  if (funder) out.funder = funder;
  const dataset = str(raw.dataset);
  if (dataset) out.dataset = dataset;
  const venue = str(raw.venue);
  if (venue) out.venue = venue;
  return Object.keys(out).length ? out : undefined;
}

// domainOf — the lineage signal inside a source ref: the host without www, or a DOI's registrant
// prefix ('10.x' = the publisher, per the normRef conventions). '' when nothing resolvable.
export const domainOf = (url: string | null | undefined): string => normRef(url).split('/')[0];

// lineageKeyOf — the DETERMINISTIC lineage fallback when the lineageClerk dies: cluster a claim by
// norm(funder || venue || source-domain). '' when nothing resolvable (the caller maps '' → cluster 0).
export const lineageKeyOf = (claim: { entities?: ClaimEntities; source: string }): string => {
  const e = claim.entities || {};
  return norm(e.funder || e.venue || domainOf(claim.source));
};

// claimStatus — COMPUTED, never asserted. contested: an unretracted attacking claim targets it.
// settled: supporting clusters ≥ SETTLED_MIN_CLUSTERS AND (a survived attack OR one cluster beyond the
// minimum). Else tentative. Support = the claim's own cluster plus the clusters of unretracted,
// non-failed claims whose stance supports it; the Set counts cluster 0 (shared unknown lineage) at
// most ONCE no matter how many claims sit in it.
export function claimStatus(
  claim: Claim,
  allClaims: Claim[],
  nullAttacks: NullAttack[],
  cfg: { SETTLED_MIN_CLUSTERS: number },
): ClaimStatus {
  const bearsOn = (c: Claim, kind: 'supports' | 'attacks'): boolean =>
    !!c.stance && c.stance.target === claim.id && c.stance.kind === kind;
  // a set `counter` (the refiner's own counter-search landed something, or an attack-lane's finding) contests
  // the claim just as an unretracted attacking ledger claim does — same signal, no ledger row required for it.
  if (claim.counter || allClaims.some((c) => !c.retracted && bearsOn(c, 'attacks')))
    return 'contested';
  // the SUBJECT's own mechanical audit verdict: a claim the auditor actively disproved is treated like
  // retracted for THIS purpose — it can never settle, no matter how many independent clusters back it
  // (checked AFTER the contested guard above, so a still-attacked audit-fail claim reads as contested, not tentative).
  if (claim.audit === 'fail') return 'tentative';
  const clusters = new Set<number>([claim.cluster]);
  for (const c of allClaims)
    if (!c.retracted && c.audit !== 'fail' && bearsOn(c, 'supports')) clusters.add(c.cluster);
  // a nullAttack naming the claim counts as a survived attack even before the attack-lane bookkeeping
  // bumps the counter; max (not sum) so the two records never double-count one challenge.
  const survived = Math.max(
    claim.attacksSurvived || 0,
    (nullAttacks || []).filter((na) => (na.claimIds || []).includes(claim.id)).length,
  );
  return clusters.size >= cfg.SETTLED_MIN_CLUSTERS &&
    (survived >= 1 || clusters.size >= cfg.SETTLED_MIN_CLUSTERS + 1)
    ? 'settled'
    : 'tentative';
}

// computedConfidence — deterministic over the key claims the answer rests on: every one settled →
// high; any contested → low; else medium. No key claims → medium; an unknown id can never ground high.
// A key claim the mechanical audit disproved (or the judge retracted) is worse than merely unsettled —
// the answer rests on a disproven pin, not just an unstressed one — so it forces 'low' outright, same as contested.
export function computedConfidence(keyClaimIds: number[], claims: Claim[]): Confidence {
  if (!keyClaimIds || !keyClaimIds.length) return 'medium';
  const byId = new Map(claims.map((c) => [c.id, c]));
  const keys = keyClaimIds.map((id) => byId.get(id));
  if (keys.some((c) => c && (c.audit === 'fail' || c.retracted))) return 'low';
  if (keys.some((c) => c && c.status === 'contested')) return 'low';
  return keys.every((c) => c && !c.retracted && c.status === 'settled') ? 'high' : 'medium';
}

// claimDigestOf — the compact "KEY CLAIMS SO FAR" digest woven into a lane reader's prompt: non-retracted
// claims, most recent first (highest id), capped at CLAIM_DIGEST_CAP, one line each `c12 clip(claim,90)`
// (ids look like c12 — never '#12', which the ledger digest never renders and a stance target must not
// be confused with a cluster's `clu2` notation). '' when the ledger is empty (the reader's own fallback
// then renders "first wave"). Structural param (mirrors venuesFor's `rr`) so this stays a pure function
// over any claims-bearing state, not just BrainerState.
export const claimDigestOf = (bs: { claims: Claim[] }): string =>
  bs.claims
    .filter((c) => !c.retracted)
    .sort((a, b) => b.id - a.id)
    .slice(0, CONFIG.CLAIM_DIGEST_CAP)
    .map((c) => 'c' + c.id + ' ' + clip(c.claim, CONFIG.CLAIM_DIGEST_CLIP))
    .join('\n');

// minConfidence — the lower-only rule: models may LOWER confidence, never raise it (low < medium < high).
const CONF_RANK: Record<Confidence, number> = { low: 0, medium: 1, high: 2 };
export const minConfidence = (a: Confidence, b: Confidence): Confidence =>
  CONF_RANK[a] <= CONF_RANK[b] ? a : b;

// lintCitations — v3 SYNTHESISER citation lint (pure): scan `report` for every [cN] marker the model wrote.
// One whose id is not a LIVE (non-retracted) ledger claim is stripped from the text in place (never left
// dangling, never fabricated back into a real id) and its id collected in `bogus` for the engine to log +
// count. One that IS a live claim but whose mechanical quote-pin audit came back 'fail' is ALSO stripped —
// a citation must never wear the authority of a pin the auditor actively disproved — and its id collected
// in `auditFailed` instead (a distinct count from `bogus`: this is a real claim, just a discredited one).
// No markers / empty report ⇒ passthrough, bogus: [], auditFailed: [].
export function lintCitations(
  report: string,
  claims: Claim[],
): { report: string; bogus: number[]; auditFailed: number[] } {
  const live = new Set(claims.filter((c) => !c.retracted).map((c) => c.id));
  const auditFail = new Set(
    claims.filter((c) => !c.retracted && c.audit === 'fail').map((c) => c.id),
  );
  const bogus: number[] = [];
  const auditFailed: number[] = [];
  const cleaned = (report || '').replace(/\[c(\d+)\]/g, (marker, idStr: string) => {
    const id = Number(idStr);
    if (!live.has(id)) {
      bogus.push(id);
      return '';
    }
    if (auditFail.has(id)) {
      auditFailed.push(id);
      return '';
    }
    return marker;
  });
  return { report: cleaned, bogus, auditFailed };
}

// chao1 — the coverage estimator (collect mode): from claim groups and how many distinct sources saw
// each, estimate the unseen groups (n1 = singletons, n2 = doubletons; the bias-corrected form when
// n2 = 0) and the coverage share. No groups yet → coverage 0 (nothing observed ≠ complete).
export function chao1(groups: { sources: number }[]): ChaoStats {
  if (!groups || !groups.length) return { unseen: 0, coverage: 0 };
  const n1 = groups.filter((g) => g.sources === 1).length;
  const n2 = groups.filter((g) => g.sources === 2).length;
  // Math.max also normalizes the n1 = 0 corner (0·(0−1)/2 = −0) to a clean +0
  const unseen = Math.max(0, n2 > 0 ? (n1 * n1) / (2 * n2) : (n1 * (n1 - 1)) / 2);
  return { unseen, coverage: groups.length / (groups.length + unseen) };
}

// updateCalib — one EMA step of a kind's realized/predicted yield ratio. PURE: returns the new entry,
// never mutates `calib`. predicted ≤ 0 → identity (an unpredicted lead teaches nothing). A kind starts
// from the neutral prior ratio 1, so early observations pull it gradually rather than whipsaw it.
export function updateCalib(
  calib: YieldCalib,
  kind: string,
  predicted: number,
  realized: number,
  alpha: number,
): { n: number; ratio: number } {
  const prev = calib[kind] || { n: 0, ratio: 1 };
  if (!(predicted > 0)) return { n: prev.n, ratio: prev.ratio };
  return { n: prev.n + 1, ratio: prev.ratio + alpha * (realized / predicted - prev.ratio) };
}

// calibFactor — the selection-only multiplier a kind's calibration earns, clamped to [lo, hi]
// (an unseen kind is neutral: 1). Applied to sort keys only — stored scores are never touched.
export const calibFactor = (calib: YieldCalib, kind: string, lo: number, hi: number): number =>
  Math.min(hi, Math.max(lo, calib[kind] ? calib[kind].ratio : 1));

// ledgerLines — the brainer's CLAIM LEDGER digest (v3 STEERING): every non-retracted claim, one line
// `c12 [status·clu2·audit] claim = value — source` (claim ids look like c12, clusters like clu2 — kept
// visually distinct so a model never confuses a cluster number with a claim id, or either with a [cN]
// citation marker), ordered latest-keyClaimIds-first (the ids the LIVING answer currently rests on),
// then contested, then settled, then tentative by recency (highest id first). Capped at `cap` with a
// "(+N more)" tail; '' when the ledger is empty (the caller's clause then omits the whole CLAIM LEDGER section).
export function ledgerLines(
  bs: { claims: Claim[]; resultSoFar: ResultSoFar | null },
  cap: number,
): string {
  const active = bs.claims.filter((c) => !c.retracted);
  const used = new Set<number>();
  const ordered: Claim[] = [];
  const byId = new Map(active.map((c) => [c.id, c]));
  for (const id of (bs.resultSoFar && bs.resultSoFar.keyClaimIds) || []) {
    const c = byId.get(id);
    if (c && !used.has(id)) {
      ordered.push(c);
      used.add(id);
    }
  }
  const rest = active.filter((c) => !used.has(c.id)).sort((a, b) => b.id - a.id);
  for (const status of ['contested', 'settled', 'tentative'] as ClaimStatus[])
    for (const c of rest)
      if (c.status === status && !used.has(c.id)) {
        ordered.push(c);
        used.add(c.id);
      }
  const shown = ordered.slice(0, cap);
  const line = (c: Claim): string =>
    'c' +
    c.id +
    ' [' +
    c.status +
    '·clu' +
    c.cluster +
    '·' +
    c.audit +
    '] ' +
    clip(c.claim, CONFIG.CLAIM_LINE_CLIP) +
    (c.value ? ' = ' + c.value : '') +
    ' — ' +
    c.source;
  const remaining = ordered.length - shown.length;
  return shown.map(line).join('\n') + (remaining > 0 ? '\n(+' + remaining + ' more)' : '');
}

// topSensitivityInput — the derivation input name whose variance share is largest ('' when there are
// none). Shared by the rerunner's log line and the brainer's DERIVATION STATE clause — one definition.
export const topSensitivityInput = (sensitivity: Record<string, number> | undefined): string => {
  const entries = Object.entries(sensitivity || {});
  return entries.length ? entries.reduce((a, b) => (b[1] > a[1] ? b : a))[0] : '';
};

// sensitivityRanking — the initiator's SENSITIVITY RANKING body (v3 FINALIZE): derivation inputs ordered by
// variance share (highest first), each with the ledger claims backing it (`c12 clip(claim,SENSITIVITY_CLIP)`, joined —
// same c-id notation as ledgerLines/claimDigestOf) or a PRIOR flag when it is an unevidenced placeholder.
// '' when there is no completed rerun yet (the caller's clause is then omitted entirely — a freshly-authored,
// not-yet-run derivation has no sensitivity to rank).
export function sensitivityRanking(
  derivation: { inputs: DerivInput[]; sensitivity: Record<string, number> } | null | undefined,
  claims: Claim[],
): string {
  if (!derivation) return '';
  const byId = new Map(claims.map((c) => [c.id, c]));
  const ranked = [...derivation.inputs].sort(
    (a, b) => (derivation.sensitivity[b.name] || 0) - (derivation.sensitivity[a.name] || 0),
  );
  return ranked
    .map((inp) => {
      const share = derivation.sensitivity[inp.name];
      const backing = (inp.claimIds || [])
        .map((id) => byId.get(id))
        .filter((c): c is Claim => !!c && !c.retracted)
        .map((c) => 'c' + c.id + ' ' + clip(c.claim, CONFIG.SENSITIVITY_CLIP))
        .join('; ');
      return (
        '- ' +
        inp.name +
        ' (' +
        (share != null ? share.toFixed(2) : '?') +
        ')' +
        (backing ? ' — backed by ' + backing : inp.prior ? ' — PRIOR (no backing claim yet)' : '')
      );
    })
    .join('\n');
}

// compactCheckpoint — the per-wave crash-safety recovery snapshot (replaces the old scribe-agent
// checkpoint file): LEAN by design — a human or a resuming agent needs the open frontier, the running
// answer, and the ledger's LOAD-BEARING shape, not its full evidentiary backing (the harness's own
// per-agent journal + the persisted _claims.json already hold quotes/cachePaths/entities in full, so
// none of those ride here). The caller (engine.ts) logs this JSON-stringified behind CONFIG.CHECKPOINT_MARK
// at the END of a wave, after the brainer's deltas have landed — so it reflects the wave's FINAL state.
export interface CheckpointSnapshot {
  wave: number;
  resultSoFar: ResultSoFar | null;
  open: { id: number; keyword: string; score: number | null; kind?: LeadKind }[];
  pursued: string[];
  claims: {
    id: number;
    claim: string;
    value?: string;
    source: string;
    status: ClaimStatus;
    cluster: number;
  }[];
  nullAttacks: number;
  derivation: { inputs: string[]; lastRun: Record<string, number> | null } | null;
}
export function compactCheckpoint(bs: {
  wave: number;
  resultSoFar: ResultSoFar | null;
  rabbitHoles: { id: number; keyword: string; scoreHistory: ScoreEntry[]; kind?: LeadKind }[];
  pursuedList: string[];
  claims: Claim[];
  nullAttacks: NullAttack[];
  derivation: Derivation | null;
}): CheckpointSnapshot {
  return {
    wave: bs.wave,
    resultSoFar: bs.resultSoFar,
    open: bs.rabbitHoles.map((r) => ({
      id: r.id,
      keyword: r.keyword,
      score: lastScore(r),
      kind: r.kind,
    })),
    pursued: bs.pursuedList,
    claims: bs.claims
      .filter((c) => !c.retracted)
      .map((c) => ({
        id: c.id,
        claim: clip(c.claim, CONFIG.CLAIM_LINE_CLIP),
        value: c.value,
        source: c.source,
        status: c.status,
        cluster: c.cluster,
      })),
    nullAttacks: bs.nullAttacks.length,
    derivation: bs.derivation
      ? {
          inputs: bs.derivation.inputs.map((i) => i.name),
          lastRun: bs.derivation.lastRun ? bs.derivation.lastRun.quantiles : null,
        }
      : null,
  };
}

// venuesWithYieldWarn — per-wave copy of the prospector venues for the brainer prompt, with a
// ' — ⚠ 0 yield in N lanes' suffix baked into `goodFor` for any venue assigned to ≥2 lanes that yielded
// NOTHING (no ingested claim, no fresh lead) across the run so far (assigned-vs-served reconciliation:
// venueStats.served — set by the engine's scheduleSources — is not read here; this suffix is about
// yield, not about whether the scheduler actually served the assignment). Pure: never mutates `venues`
// or the stats map; a venue with a healthy yield passes through unchanged. NEW — unrouted-venue
// surfacing: a venue with NO stats entry at all (the prospector named it but no lane was ever assigned
// it) earns its own '⚠ never assigned to any lane yet' suffix once the run is far enough along
// (`wave` given AND ≥ CONFIG.VENUE_UNROUTED_MIN_WAVE) that "not yet assigned" has become a real signal
// rather than early-run noise; `wave` omitted (single-brainer wave-0 seed call, or a caller that does
// not track wave) ⇒ the old passthrough-on-no-entry behavior.
export function venuesWithYieldWarn(
  venues: Venue[],
  venueStats: Record<string, { assigned: number; yielded: number }>,
  wave?: number,
): Venue[] {
  return venues.map((v) => {
    const s = venueStats[v.source];
    if (!s) {
      if (wave !== undefined && wave >= CONFIG.VENUE_UNROUTED_MIN_WAVE)
        return { ...v, goodFor: v.goodFor + ' — ⚠ never assigned to any lane yet' };
      return v;
    }
    if (s.assigned < CONFIG.VENUE_WARN_MIN || s.yielded !== 0) return v;
    return { ...v, goodFor: v.goodFor + ' — ⚠ 0 yield in ' + s.assigned + ' lanes' };
  });
}

// vocabSummary — the scheduler's COMMUNITY VOCABULARY clause input: the top `cap` terms by uses,
// "term (n)" comma-joined; '' when the vocabulary is empty (the caller's clause is then omitted).
export const vocabSummary = (vocab: Term[], cap: number): string =>
  [...(vocab || [])]
    .sort((a, b) => b.uses - a.uses)
    .slice(0, cap)
    .map((t) => t.term + ' (' + t.uses + ')')
    .join(', ');

// packReaders — the MECHANICAL splitter (B5/B2/B7). Bin-pack a lane's sized sources into reader-units, each ≤
// `budget` tokens AND ≤ budget×charsPerToken CHARS. The UNIT is the budget, not the source: small sources that
// fit together COMBINE into one reader (its `reads` list holds several whole files); a source bigger than the
// budget SPLITS across enough readers, with `overlap` chars re-read at each split boundary.
//
// B2 — UNIT CONSISTENCY: the read window is in CHARS but the budget is in TOKENS, so `budget` is converted to a
//   char ceiling (`budgetChars = budget × charsPerToken`, the SAME over-count heuristic the scheduler sizes with)
//   and BOTH dimensions gate every pack: a source is split when size > budget OR chars > budgetChars, and the
//   split count is the MAX of both estimates — so a source the scheduler under-counts in tokens but is huge in
//   chars still splits. The scheduler's `size`/`chars` are treated as HINTS only (re-confirmed here defensively).
//   (The true byte-length / file-existence check lives in the reader — the workflow VM has no fs; see B3.)
// B7 — bounded packing: at most `maxSlices` whole sources combine into one reader (a lane of 40 tiny files never
//   packs 40 into one turn). Caller also caps sources-per-lane before this.
// A path-less / zero-char source is DROPPED (never emit a doomed reader). Returns one ReadSlice[] per reader.
export function packReaders(
  sources: SchedulerSource[],
  budget: number,
  overlap: number,
  maxSlices: number = Infinity,
  charsPerToken: number,
): ReadSlice[][] {
  const budgetChars = Math.max(1, Math.floor(budget * charsPerToken));
  const readers: ReadSlice[][] = [];
  let cur: ReadSlice[] = [];
  let curTokens = 0;
  let curChars = 0;
  const flush = () => {
    if (cur.length) {
      readers.push(cur);
      cur = [];
      curTokens = 0;
      curChars = 0;
    }
  };
  for (const s of sources || []) {
    if (!s || !s.path) continue; // a path-less source is dropped (the reader has nothing on disk to read)
    // mechanical guard: derive chars + size defensively (inverse heuristic when one is missing), never trust junk.
    const chars =
      Number.isFinite(s.chars) && s.chars > 0
        ? Math.floor(s.chars)
        : Math.max(0, Math.round((Number(s.size) || 0) * charsPerToken));
    if (chars <= 0) continue;
    const size =
      Number.isFinite(s.size) && s.size > 0
        ? Math.floor(s.size)
        : Math.max(1, Math.round(chars / charsPerToken));
    // a source must split when it overflows EITHER the token budget OR the char ceiling (units kept consistent).
    if (size <= budget && chars <= budgetChars) {
      // whole source fits a reader-unit; flush first if combining it would overflow tokens/chars OR the slice cap.
      if (
        cur.length &&
        (curTokens + size > budget || curChars + chars > budgetChars || cur.length >= maxSlices)
      )
        flush();
      cur.push({ source: s.source, cachePath: s.path, offset: 0, limit: chars });
      curTokens += size;
      curChars += chars;
    } else {
      // oversized source — flush the current pack, then split it across enough readers (by BOTH dimensions) with overlap.
      flush();
      const parts = Math.max(Math.ceil(size / budget), Math.ceil(chars / budgetChars));
      const per = Math.ceil(chars / parts); // ≤ budgetChars by construction (forward progress past the overlap)
      for (let p = 0; p < parts; p++) {
        const start = Math.max(0, p * per - (p > 0 ? overlap : 0));
        const end = Math.min(chars, (p + 1) * per); // capped by the real remaining chars
        if (end > start)
          readers.push([
            { source: s.source, cachePath: s.path, offset: start, limit: end - start },
          ]);
      }
    }
  }
  flush();
  return readers;
}

// PROMPT_LOG — the exact prompt sent for EVERY agent call, keyed by label (always populated, not just in debug). The numbered phase files
// prepend their own prompt (Change E): the prompt lives in the phase file, not a separate _io.md.
export const PROMPT_LOG: Record<string, string> = {};
// CHANGE E — prepend the exact prompt sent for `label` ahead of a numbered phase file's body. result.md stays clean (never wrapped).
export const withPrompt = (label: string, body: string): string =>
  (PROMPT_LOG[label] ? '## Prompt sent\n\n' + PROMPT_LOG[label] + '\n\n---\n\n' : '') + body;

// render — fill a template's `{{key}}` holes from a vars object. A tiny deterministic
// `String.replace` replacer (no engine, no globals): (1) strip ONE trailing template newline
// (editors add one; the inline templates carried none); (2) strip standalone `{{! … }}` comment
// lines whole — line + newline — so they stay invisible; (3) substitute each `{{key}}` with its
// value, an absent key rendering as ''. Single-pass, so a substituted value is never re-scanned.
export const render = (tpl: string, vars: Record<string, unknown>): string =>
  tpl
    .replace(/\n$/, '')
    .replace(/^[ \t]*\{\{![\s\S]*?\}\}[ \t]*(?:\r?\n|$)/gm, '')
    .replace(/\{\{(\w+)\}\}/g, (_, k: string) => {
      const v = vars[k];
      return v == null ? '' : String(v);
    });

// markdown render of a wave's resultSoFar (the brainer's living memory) — this IS the kept per-wave log.
export const bullets = (arr: string[] | null | undefined): string =>
  arr && arr.length ? arr.map((x) => '- ' + x).join('\n') : '_none_';
export function resultSoFarMd(r: ResultSoFar | null): string {
  if (!r || typeof r !== 'object') return '_none_';
  return (
    '**Answer:** ' +
    (r.answer || '_(none)_') +
    '\n\n**Confidence:** ' +
    (r.confidence || '_(none)_') +
    (r.working ? '\n\n**Working:**\n\n' + r.working : '') +
    (r.assumptions && r.assumptions.length
      ? '\n\n**Assumptions:**\n' +
        r.assumptions.map((a) => '- **' + (a.claim || '') + '** — ' + (a.basis || '')).join('\n')
      : '') +
    '\n\n**Resolved:**\n' +
    bullets(r.resolved) +
    '\n\n**Open gaps:**\n' +
    bullets(r.openGaps) +
    '\n\n**Tensions:**\n' +
    bullets(r.tensions)
  );
}

// per-wave brainer markdown (one file per crawl wave). `store` = the open rabbit-hole store snapshot at write time.
type WavePick = {
  id: number;
  keyword: string;
  why: string;
  score: number | null;
  path: string[];
  sources?: string[];
  note?: string; // the brainer's per-lane directive — surfaced so degraded runs are debuggable without debug mode
};
type WaveStoreEntry = { id: number; keyword: string; scoreHistory: ScoreEntry[] };
export function waveMd(
  wave: number,
  coord: { stop: Stop; resultSoFar: ResultSoFar | null },
  picks: WavePick[],
  finds: Finding[],
  store: WaveStoreEntry[],
): string {
  const sc = (p: { score: number | null }) => (p.score != null ? p.score : 'new');
  return (
    '# Wave ' +
    wave +
    ' — Brainer\n\n**done:** ' +
    coord.stop.done +
    ' — ' +
    coord.stop.reason +
    '\n\n## Result so far\n\n' +
    resultSoFarMd(coord.resultSoFar) +
    (finds.length
      ? '\n\n## Findings pursued this wave\n\n' +
        finds
          .map(
            (f) => '### ' + f.rabbitHole + '\n\n_trail: ' + (f.trail || '') + '_\n\n' + f.summary,
          )
          .join('\n\n')
      : '') +
    '\n\n## Looking up next (' +
    picks.length +
    ')\n\n' +
    (picks
      .map(
        (p, i) =>
          i +
          1 +
          '. **[' +
          sc(p) +
          ']** #' +
          p.id +
          ' ' +
          p.keyword +
          '\n   - trail: ' +
          trailOf(p.path) +
          (p.sources && p.sources.length ? '\n   - venues: ' + p.sources.join(', ') : '') +
          (p.note ? '\n   - note: ' + p.note : '') +
          '\n   - ' +
          p.why,
      )
      .join('\n') || '_none_') +
    '\n\n## Open rabbit-holes (scored)\n\n' +
    ([...store]
      .sort((a, b) => (lastScore(b) ?? -1) - (lastScore(a) ?? -1))
      .map(
        (r) =>
          '- **[' +
          (lastScore(r) != null ? lastScore(r) : 'new') +
          ']** #' +
          r.id +
          ' ' +
          r.keyword,
      )
      .join('\n') || '_none_') +
    '\n'
  );
}

// claimsMd — the human-readable ledger artifact (_claims.md): every non-retracted claim, grouped by its
// COMPUTED status (settled → tentative → contested), one line each: `- c<id> [status·cluster·audit] claim
// = value — source (wave N, lane)`; then the nullAttacks ("Challenged and survived" — a completed
// counter-search that found nothing is first-class, distinct from "never challenged"); then the vocabulary.
export function claimsMd(bs: {
  claims: Claim[];
  nullAttacks: NullAttack[];
  vocabulary: Term[];
}): string {
  const line = (c: Claim): string =>
    '- c' +
    c.id +
    ' [' +
    c.status +
    '·' +
    c.cluster +
    '·' +
    c.audit +
    '] ' +
    c.claim +
    (c.value ? ' = ' + c.value : '') +
    ' — ' +
    c.source +
    ' (wave ' +
    c.wave +
    ', ' +
    c.lane +
    ')';
  const group = (status: ClaimStatus): string =>
    bs.claims
      .filter((c) => !c.retracted && c.status === status)
      .map(line)
      .join('\n') || '_none_';
  const nullSection =
    bs.nullAttacks
      .map(
        (na) =>
          '- **' +
          na.topic +
          '** — queries: ' +
          na.queries.join(', ') +
          (na.claimIds.length ? ' → c' + na.claimIds.join(', c') : ''),
      )
      .join('\n') || '_none_';
  const vocabSection =
    bs.vocabulary
      .map((t) => '- **' + t.term + '**' + (t.gloss ? ' — ' + t.gloss : '') + ' (' + t.uses + ')')
      .join('\n') || '_none_';
  return (
    '# Claim ledger\n\n## Settled\n\n' +
    group('settled') +
    '\n\n## Tentative\n\n' +
    group('tentative') +
    '\n\n## Contested\n\n' +
    group('contested') +
    '\n\n## Challenged and survived\n\n' +
    nullSection +
    '\n\n## Vocabulary\n\n' +
    vocabSection +
    '\n'
  );
}

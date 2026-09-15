import { CONFIG } from './config.js';
import { calibFactor, norm, normRef } from './utils/index.js';
import type {
  AddRabbitHoleArgs,
  LookupItem,
  RabbitHole,
  RenameItem,
  RescoreItem,
  ScoredLead,
  StoreState,
} from './types/index.js';

// ─────────────────────────────────────────────────────────────────────────────
// Store reducers — pure functions over a `state` object that carries the crawl's
// rabbit-hole store (rabbitHoles, nextId, pursuedKeys, pursuedList, pursuedArchive).
// In the original these were methods on ResearchReport; here `state` is the first
// arg (the engine passes `this`). Logic is identical.
// ─────────────────────────────────────────────────────────────────────────────

// the brainer-delta subset applyDeltas consumes (the test passes partial coords, so each field is optional).
type DeltaInput = {
  rename?: RenameItem[];
  drop?: number[];
  rescore?: RescoreItem[];
  add?: ScoredLead[];
};

// light near-duplicate check — Jaccard token-set overlap ≥ CONFIG.NEAR_DUP counts as "the same lead, reworded"
// (catches re-orderings the exact norm() match misses); the threshold is kept high so distinct leads are never
// merged. Exported: the v3 chao1 coverage estimate (engine.ts) reuses it to group near-duplicate claims —
// ONE Jaccard near-dup definition for the whole engine, never a second copy.
const tokenSet = (s: string): Set<string> => new Set(norm(s).split(' ').filter(Boolean));
export const nearDup = (a: string, b: string): boolean => {
  const A = tokenSet(a);
  const B = tokenSet(b);
  if (!A.size || !B.size) return false;
  let inter = 0;
  for (const t of A) if (B.has(t)) inter++;
  return inter / (A.size + B.size - inter) >= CONFIG.NEAR_DUP;
};

// add-or-find an OPEN rabbit-hole. Dedup by norm(keyword), a near-duplicate keyword, AND normRef(ref) against the
// open store AND the pursued sets; returns the existing/new entry, or null when the keyword/ref is already pursued
// (never re-open a pursued lane or re-fetch a pursued citation). New entries get a fresh id; scoreHistory seeded only when scored.
export function addRabbitHole(
  state: StoreState,
  { keyword, why, path, score, wave, ref, kind }: AddRabbitHoleArgs,
): RabbitHole | null {
  const k = norm(keyword);
  const r = ref ? normRef(ref) : '';
  if (!k && !r) return null;
  if (k && state.pursuedKeys.has(k)) return null;
  if (r && state.pursuedRefs.has(r)) return null;
  if (k && state.pursuedList.some((p) => nearDup(keyword, p))) return null; // near-duplicate of a pursued lane
  const existing = state.rabbitHoles.find(
    (x) =>
      (k && norm(x.keyword) === k) ||
      (r && x.ref && normRef(x.ref) === r) ||
      (k && nearDup(keyword, x.keyword)),
  );
  if (existing) return existing;
  const scored = typeof score === 'number';
  const rh: RabbitHole = {
    id: state.nextId++,
    keyword: keyword || ref || '',
    why: why || '',
    score: scored ? score : null,
    scoreHistory: scored ? [{ wave, score }] : [],
    path: path || [],
  };
  if (ref) rh.ref = ref;
  if (kind) rh.kind = kind; // the lead's origin channel — keys the yieldCalib table
  state.rabbitHoles.push(rh);
  return rh;
}

// apply the brainer's DELTAS to the open store, in order: rename → drop → rescore → add. scoreHistory carried natively by id (no reconcile).
export function applyDeltas(state: StoreState, coord: DeltaInput, wave: number): void {
  for (const r of coord.rename || []) {
    const rh = state.rabbitHoles.find((x) => x.id === r.id);
    if (rh) {
      rh.keyword = r.keyword;
      if (r.why) rh.why = r.why;
    }
  }
  if (coord.drop && coord.drop.length) {
    const gone = new Set(coord.drop);
    state.rabbitHoles = state.rabbitHoles.filter((x) => !gone.has(x.id));
  }
  for (const r of coord.rescore || []) {
    const rh = state.rabbitHoles.find((x) => x.id === r.id);
    if (rh) {
      rh.score = r.score;
      rh.scoreHistory.push({ wave, score: r.score });
    }
  }
  for (const a of coord.add || [])
    addRabbitHole(state, {
      keyword: a.keyword,
      why: a.why,
      path: [],
      score: a.score,
      wave,
      kind: a.kind,
    });
}

// resolve the brainer's `lookupNext` into open-store entries to pursue NOW: id → existing lead; keyword → originate (or find). Drop any
// already pursued, attach the lane's assigned venues, dedup, then take the highest-scoring up to laneCount (the hard ceiling).
export function resolveLookupNext(
  state: StoreState,
  coord: { lookupNext?: LookupItem[] },
  wave: number,
  laneCount: number,
): RabbitHole[] {
  const picks: RabbitHole[] = [];
  for (const item of coord.lookupNext || []) {
    let rh: RabbitHole | null | undefined = null;
    if (typeof item.id === 'number') rh = state.rabbitHoles.find((x) => x.id === item.id);
    else if (item.keyword || item.ref)
      rh = addRabbitHole(state, {
        keyword: item.keyword || '',
        why: item.why,
        path: [],
        score: item.score,
        wave,
        ref: item.ref,
        kind: item.kind, // origin channel rides along when the brainer originates a lane
      });
    if (!rh || state.pursuedKeys.has(norm(rh.keyword))) continue;
    if (item.sources) rh.sources = item.sources;
    if (item.note) rh.note = item.note; // the brainer's per-lane directive → rides to the scheduler + reader as noteFromBrainer
    if (item.ref && !rh.ref) rh.ref = item.ref;
    if (item.refetch) rh.refetch = true; // corrupted-cache remediation rides to the scheduler
    if (!picks.some((p) => p.id === rh.id)) picks.push(rh);
  }
  // v3 CALIBRATION — the sort key is the score weighted by its kind's predicted-vs-realized yield (selection
  // only; the stored score itself is never touched). kind = the stored lead's own kind (an id-resolved pick
  // already carries it; an originated one got it from addRabbitHole's `kind: item.kind` above); an unseen
  // kind (or none) is neutral (calibFactor defaults to 1) — degrades to the old plain-score sort untouched.
  const weighted = (p: RabbitHole): number =>
    (p.score ?? 0) *
    calibFactor(
      state.yieldCalib ?? {},
      p.kind ?? 'origin',
      CONFIG.CALIB_CLAMP_LO,
      CONFIG.CALIB_CLAMP_HI,
    );
  return picks.sort((a, b) => weighted(b) - weighted(a)).slice(0, laneCount);
}

// REOPEN — the inverse of pursue (validator-driven): move a pursued lead back into the open store so the next
// brainer can re-pursue it. Clears its pursued keys/ref + drops it from the archive, and bumps failCount (the cap).
export function reopenRabbitHole(state: StoreState, rh: RabbitHole): RabbitHole {
  const ai = state.pursuedArchive.indexOf(rh);
  if (ai >= 0) state.pursuedArchive.splice(ai, 1);
  state.pursuedKeys.delete(norm(rh.keyword));
  if (rh.ref) state.pursuedRefs.delete(normRef(rh.ref));
  const li = state.pursuedList.indexOf(rh.keyword);
  if (li >= 0) state.pursuedList.splice(li, 1);
  rh.failCount = (rh.failCount || 0) + 1;
  // clear the stale directive: the `note` that already produced a dead lane must NOT be re-sent verbatim — the
  // next brainer re-authors a fresh directive (or the scheduler/reader fall back to the rabbit-hole + goal).
  delete rh.note;
  if (!state.rabbitHoles.some((x) => x.id === rh.id)) state.rabbitHoles.push(rh);
  return rh;
}

// PURSUE — MOVE picks out of the open store into the pursued-archive (no delete-on-pursue): the archive keeps each lead's id + scoreHistory + path.
export function pursue(state: StoreState, picks: RabbitHole[]): void {
  for (const p of picks) {
    state.pursuedKeys.add(norm(p.keyword));
    if (p.ref) state.pursuedRefs.add(normRef(p.ref));
    state.pursuedList.push(p.keyword);
    state.pursuedArchive.push(p);
  }
  const gone = new Set(picks.map((p) => p.id));
  state.rabbitHoles = state.rabbitHoles.filter((r) => !gone.has(r.id));
}

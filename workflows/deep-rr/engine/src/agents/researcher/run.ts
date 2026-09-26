import { CONFIG } from '../../config.js';
import { researcher } from './index.js';
import { retryAgent } from '../../runtime.js';
import { claimDigestOf, clip, lab, packReaders, trailOf } from '../../utils/index.js';
import type { BrainerState } from '../../brainerState.js';
import type {
  ClaimSeed,
  LeadKind,
  NextSource,
  RabbitHole,
  RabbitHoleSeed,
  ReaderOut,
  ReadSlice,
  ResearchOut,
  SchedulerSource,
  TermSeed,
} from '../../types/index.js';

// LANE THREAD (B5) — one SEQUENTIAL reader thread for a lane, carrying the running answer across every read.
// `readers` is the bin-packed list of reader-units (each a set of cache char-windows). Each reader reads its
// slice(s) off disk + digests; only the clean parsed `runningAnswer` is forwarded (handoff hygiene — no
// tool-call/StructuredOutput serialization). Yields one accumulated ResearchOut (or null if every reader failed).
// `claimDigest` + `laneKind` come from the caller (runResearchers computes claimDigest ONCE per wave off the
// ledger; laneKind is this lane's RabbitHole.kind — 'attack' flips the reader's brief to counter-evidence).
export async function runLaneThread(
  p: RabbitHole,
  readers: ReadSlice[][],
  tag: string,
  phaseName: string,
  claimDigest: string,
  laneKind?: LeadKind,
): Promise<ResearchOut | null> {
  const N = readers.length;
  let priorAnswer = '';
  const rabbitHoles: RabbitHoleSeed[] = [];
  const nextSources: NextSource[] = [];
  const deadEnds: string[] = [];
  const claims: ClaimSeed[] = [];
  const newTerms: TermSeed[] = [];
  let surprise = ''; // keep the FIRST non-empty surprise note — the earliest contradiction this lane hit
  let any = false;
  let failed = false; // B3: any reader returned null (retries exhausted / open() threw) → the lane is INCOMPLETE
  for (let i = 0; i < N; i++) {
    const out = await retryAgent<ReaderOut>(
      researcher.buildPrompt({
        query: CONFIG.query,
        trail: trailOf(p.path, p.keyword),
        keyword: p.keyword,
        why: p.why,
        note: p.note || '',
        footer: CONFIG.FOOTER,
        reads: readers[i],
        readerIndex: i + 1,
        readerCount: N,
        priorAnswer, // clean parsed running answer from the prior reader ('' for reader 1) — renders LAST
        claimDigest,
        laneKind,
        researcherNote: CONFIG.RESEARCHER_NOTE,
      }),
      {
        label: 'lane-' + tag + ':' + lab(p.keyword) + '-r' + (i + 1) + 'of' + N,
        phase: phaseName,
        model: researcher.tier,
        effort: researcher.effort,
        agentType: CONFIG.GENERAL_PURPOSE,
        schema: researcher.schema,
      },
    );
    if (out) {
      any = true;
      if (typeof out.runningAnswer === 'string' && out.runningAnswer.trim())
        priorAnswer = clip(out.runningAnswer, CONFIG.HANDOFF_CHARS); // handoff hygiene + B7: bound the forwarded running answer
      for (const rh of out.rabbitHoles || []) rabbitHoles.push(rh);
      for (const ns of out.nextSources || []) nextSources.push(ns);
      for (const d of out.deadEnds || []) deadEnds.push(d);
      for (const c of out.claims || []) claims.push(c);
      for (const t of out.newTerms || []) newTerms.push(t);
      if (!surprise && out.surprise && out.surprise.trim()) surprise = out.surprise;
    } else {
      failed = true; // a dropped chunk must NOT hide behind the surviving readers
    }
  }
  // B3 — if ANY reader on the lane failed (or none produced anything), return null so the validator gate
  // (anyNull) reopens the lane; never emit a confident summary that silently dropped a chunk.
  if (!any || failed) return null;
  const out: ResearchOut = {
    summary: priorAnswer || '(reader returned no answer)',
    rabbitHoles,
    nextSources,
    deadEnds,
    claims,
    newTerms,
  };
  if (surprise) out.surprise = surprise;
  return out;
}

// RUN RESEARCHERS (B5) — consume the scheduler's per-lane source sets: bin-pack each lane into ≤budget
// reader-units, then spawn ONE sequential reader thread per lane in PARALLEL across lanes. A lane with no
// scheduled source returns null (a dead lane → the validator gate reopens it). Returns each lane's accumulated
// ResearchOut (or null), in pick order. The claim digest is computed ONCE here (the ledger only turns over
// between waves, not within one) and threaded into every lane, alongside each pick's own kind (attack-lane
// awareness) — down through runLaneThread into every reader's prompt.
export async function runResearchers(
  bs: BrainerState,
  picks: RabbitHole[],
  schedule: Map<number, SchedulerSource[]>,
  tag: string,
  phaseName: string,
): Promise<(ResearchOut | null)[]> {
  const claimDigest = claimDigestOf(bs);
  return parallel(
    picks.map((p) => () => {
      // B7: cap sources-per-lane before packing; B2/B7: hand packReaders the slice cap + the token→char ratio.
      const laneSources = (schedule.get(p.id) || []).slice(0, CONFIG.MAX_SOURCES_PER_LANE);
      const readers = packReaders(
        laneSources,
        CONFIG.RESEARCHER_TOKEN_BUDGET,
        CONFIG.CHUNK_OVERLAP_CHARS,
        CONFIG.MAX_SLICES_PER_READER,
        CONFIG.CHARS_PER_TOKEN,
      );
      if (!readers.length) {
        log('    lane #' + p.id + ' ' + lab(p.keyword) + ' — no source scheduled → skipped');
        return Promise.resolve(null);
      }
      return runLaneThread(p, readers, tag, phaseName, claimDigest, p.kind);
    }),
  );
}

import { CONFIG } from '../../config.js';
import { researchScheduler } from './index.js';
import { retryAgent } from '../../runtime.js';
import { clip, venuesFor, vocabSummary } from '../../utils/index.js';
import type { BrainerState } from '../../brainerState.js';
import type { RabbitHole, ScheduleResult, SchedulerOut, SchedulerSource } from '../../types/index.js';

// SCHEDULER (B4) — discovery. One Sonnet researchScheduler over the WHOLE wave's lanes: per lane (the
// rabbit-hole + its steering `note` + assigned venues + kind/refetch flags), it batches the searches, sizes
// every candidate via mcp__harvester__fetch size_only, and returns the chosen sources grouped per lane id —
// plus two HONESTY side-channels: `venuesServed` (assigned-vs-served venue reconciliation, so a silent
// tier-substitution shows up) and `unsourced` (directive-named refs/venues it could not fetch, reported
// instead of silently dropped). Returns a ScheduleResult; the ENGINE folds the honesty channels into bs
// (lastUnsourced, venueStats.served) — this fn stays pure request/response. Degrades to an empty schedule
// when the scheduler dies.
export async function runScheduler(
  bs: BrainerState,
  picks: RabbitHole[],
  tag: string,
  phaseName: string,
): Promise<ScheduleResult> {
  const empty: ScheduleResult = {
    schedule: new Map<number, SchedulerSource[]>(),
    unsourced: '',
    venuesServed: new Map<number, string[]>(),
  };
  if (!picks.length) return empty;
  const out = await retryAgent<SchedulerOut>(
    researchScheduler.buildPrompt({
      query: CONFIG.query,
      lanes: picks.map((p) => ({
        id: p.id,
        keyword: p.keyword,
        why: p.why,
        note: p.note || '',
        venues: venuesFor(bs, p.sources),
        ref: p.ref,
        kind: p.kind,
        refetch: p.refetch,
      })),
      researcherNote: CONFIG.RESEARCHER_NOTE,
      vocabulary: vocabSummary(bs.vocabulary, CONFIG.SCHED_VOCAB_CAP),
      corruptCache: [...bs.corruptCachePaths],
    }),
    {
      label: 'scheduler-' + tag,
      phase: phaseName,
      model: researchScheduler.tier,
      effort: researchScheduler.effort,
      agentType: CONFIG.GENERAL_PURPOSE,
      schema: researchScheduler.schema,
    },
  );
  // B6 — only accept lanes whose id is a REAL pick this wave: drop hallucinated ids the scheduler may invent;
  // a duplicate id is last-wins (map.set), which is fine — the engine bin-packs whatever set lands on the id.
  const schedule = new Map<number, SchedulerSource[]>();
  const venuesServed = new Map<number, string[]>();
  const unsourcedLines: string[] = [];
  const keywordOf = new Map(picks.map((p) => [p.id, p.keyword]));
  const pickIds = new Set(picks.map((p) => p.id));
  for (const l of (out && out.lanes) || [])
    if (l && typeof l.id === 'number' && pickIds.has(l.id) && Array.isArray(l.sources)) {
      schedule.set(l.id, l.sources);
      venuesServed.set(
        l.id,
        Array.isArray(l.venuesServed) ? l.venuesServed.filter((v) => typeof v === 'string') : [],
      );
      for (const u of l.unsourced || [])
        if (u && typeof u.ref === 'string' && typeof u.reason === 'string')
          unsourcedLines.push(
            'lane #' + l.id + ' ' + clip(keywordOf.get(l.id) || '', 40) + ': ' + u.ref + ' — ' + u.reason,
          );
    }
  log(
    '    scheduler · ' +
      schedule.size +
      '/' +
      picks.length +
      ' lane(s) sourced · sources=' +
      [...schedule.values()].reduce((n, s) => n + s.length, 0),
  );
  if (unsourcedLines.length) log('    scheduler · ⚠ unsourced: ' + unsourcedLines.length + ' ref(s)');
  return { schedule, unsourced: unsourcedLines.join('\n'), venuesServed };
}

// LINEAGE CLERK dispatch — batches new claims into ≤LINEAGE_BATCH-item chunks, one retryAgent call per
// chunk, all chunks dispatched CONCURRENTLY via parallel(). A dead (null) chunk contributes nothing — its
// claims fall back to the deterministic lineageKeyOf clustering (the caller's job, not this module's).
// Hallucinated ids (not in the chunk's own input set) are dropped. Empty input → no agent spawned at all.
import { CONFIG } from '../../config.js';
import { lineageClerk } from './index.js';
import { retryAgent } from '../../runtime.js';
import { chunk } from '../../utils/index.js';
import type { BrainerState } from '../../brainerState.js';
import type { Claim, LineageClerkOut } from '../../types/index.js';

export async function runLineageClerk(
  bs: BrainerState,
  claims: Claim[],
  knownKeys: string[],
  tag: string,
  phaseName: string,
): Promise<Map<number, string[]>> {
  const out = new Map<number, string[]>();
  if (!claims.length) return out;
  const chunks = chunk(claims, CONFIG.LINEAGE_BATCH);
  // parallel() journals thunk results as JSON (a Set would come back as {}), so the thunk returns
  // the bare agent result and the id set is rebuilt per chunk on the consumer side (order-aligned).
  const results = await parallel(
    chunks.map((ch, i) => () =>
      retryAgent<LineageClerkOut>(
        lineageClerk.buildPrompt({
          items: ch.map((c) => ({ id: c.id, source: c.source, entities: c.entities })),
          knownKeys,
        }),
        {
          label: 'lineage-' + tag + (chunks.length > 1 ? '-b' + i : ''),
          phase: phaseName,
          model: lineageClerk.tier,
          effort: lineageClerk.effort,
          schema: lineageClerk.schema,
        },
      ),
    ),
  );
  results.forEach((res, i) => {
    if (!res) return; // dead chunk — its claims fall back to lineageKeyOf
    const ids = new Set(chunks[i].map((c) => c.id));
    for (const l of res.links || [])
      if (l && ids.has(l.id) && Array.isArray(l.keys)) out.set(l.id, l.keys);
  });
  return out;
}

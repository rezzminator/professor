import { CONFIG } from '../../config.js';
import { refiner } from './index.js';
import { retryAgent } from '../../runtime.js';
import type { BrainerState } from '../../brainerState.js';
import type { CleanReport, FactToHarden, RefineOut } from '../../types/index.js';

// REFINE the named load-bearing facts in parallel — one sonnet refine agent per fact; on a re-run the judge `directive`
// rides into each so it re-checks what the judge flagged. A fact carrying a `claimId` gets that ledger claim's
// pinned quote + source rendered into its prompt (THE CLAIM AS PINNED). Returns the hardened reports + the
// refinement artifact markdown (the engine writes/overwrites the refinement file) + the RAW per-fact outputs
// (queriesTried/counterFound/counterNote) so the engine — never this pure run fn — can fold the attack outcome
// into the ledger. passTag keeps labels unique per pass.
export async function runRefine(
  bs: BrainerState,
  facts: FactToHarden[],
  directive: string,
  passTag: string,
): Promise<{ cleanReports: CleanReport[]; artifact: string; refined: (RefineOut | null)[] }> {
  const refined = await parallel(
    facts.map((f, i) => () => {
      const pinned =
        typeof f.claimId === 'number' ? bs.claims.find((c) => c.id === f.claimId) : undefined;
      return retryAgent<RefineOut>(
        refiner.buildPrompt({
          net: CONFIG.NET,
          query: CONFIG.query,
          fact: f.fact,
          why: f.why,
          directive,
          claimQuote: pinned?.quote,
          claimSource: pinned?.source,
        }),
        {
          label: 'refine-' + passTag + i,
          phase: CONFIG.PHASE.finalize,
          model: refiner.tier,
          effort: refiner.effort,
          schema: refiner.schema,
        },
      );
    }),
  );
  const cleanReports: CleanReport[] = facts.map((f, i) => ({
    fact: f.fact,
    why: f.why,
    clean: (refined[i] && refined[i]!.report) || '(refine failed)',
  }));
  const artifact =
    '# Refinement — fact-check & harden the load-bearing facts\n\n' +
    (facts.length
      ? facts
          .map((f, i) => {
            const r = refined[i];
            const attackNote = r
              ? '\n\n_counter-search: ' +
                (r.counterFound
                  ? 'FOUND — ' + (r.counterNote || '(unspecified)')
                  : 'none found (survived)') +
                (r.queriesTried && r.queriesTried.length
                  ? ' · queries: ' + r.queriesTried.join('; ')
                  : '') +
                '_'
              : '';
            return (
              '## ' +
              (i + 1) +
              ' — ' +
              f.fact +
              '\n\n_' +
              f.why +
              '_\n\n' +
              ((r && r.report) || '_(refine failed)_') +
              attackNote
            );
          })
          .join('\n\n')
      : '_no facts to harden_') +
    '\n';
  return { cleanReports, artifact, refined };
}

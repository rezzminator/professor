import { CONFIG } from '../../config.js';
import { prospector } from './index.js';
import { retryAgent } from '../../runtime.js';
import { withPrompt } from '../../utils/index.js';
import type { ResearchReport } from '../../engine.js';
import type { SourcesOut } from '../../types/index.js';

// PROSPECT — one Opus prospector after the scout names the high-value source venues; the brainer assigns the
// relevant subset per lane. Sets rr.highValueSources/languageGuidance/sourcesReasoning and RETURNS the
// 02-prospector.md markdown (the engine writes it); on failure the venues degrade to none.
export async function runProspector(rr: ResearchReport): Promise<string> {
  log('· prospector DISPATCH · ' + prospector.tier);
  const res = await retryAgent<SourcesOut>(
    prospector.buildPrompt({
      query: CONFIG.query,
      landscape: rr.scout!.landscape,
      sources: rr.scout!.pages.map((p) => p.url),
      thinkerNote: CONFIG.THINKER_NOTE,
      researcherNote: CONFIG.RESEARCHER_NOTE,
    }),
    {
      label: 'prospector',
      phase: CONFIG.PHASE.scout,
      model: prospector.tier,
      effort: prospector.effort,
      schema: prospector.schema,
    },
  );
  rr.highValueSources = (res && res.highValueSources) || [];
  rr.languageGuidance = (res && res.languageGuidance) || '';
  rr.sourcesReasoning = (res && res.reasoning) || '';
  log(
    '· prospector RETURN · venues=' +
      rr.highValueSources.length +
      (rr.languageGuidance ? ' · languages="' + rr.languageGuidance.slice(0, 80) + '"' : '') +
      (res ? '' : ' (FAILED → none; researchers fall back to general search)'),
  );
  rr.highValueSources.forEach((s, i) =>
    log('    venue ' + (i + 1) + ' · ' + s.source + ' — ' + s.goodFor),
  );
  return withPrompt(
    'prospector',
    '# 02 — Prospector\n\n**Query:** ' +
      CONFIG.query +
      (rr.sourcesReasoning ? '\n\n_' + rr.sourcesReasoning + '_' : '') +
      (rr.languageGuidance ? '\n\n**Language routing:** ' + rr.languageGuidance : '') +
      '\n\n## High-value source venues\n\n' +
      (rr.highValueSources
        .map(
          (s, i) =>
            i +
            1 +
            '. **' +
            s.source +
            '**' +
            (s.lang ? ' [' + s.lang + ']' : '') +
            ' — ' +
            s.goodFor,
        )
        .join('\n') || '_(none returned)_') +
      '\n',
  );
}

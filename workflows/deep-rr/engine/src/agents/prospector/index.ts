// PROSPECTOR — runs after the scout, first agent of the Crawl phase. Names the high-value
// AUTHORITATIVE source venues for THIS topic (domain-specific); output rides with the brainer, which
// assigns the relevant subset to each lane. Tier: opus (cross-domain venue judgment). Effort: high.
import { CONFIG } from '../../config.js';
import { buildProspector } from './prompts.js';
import type { Agent, ProspectorArgs, Schema } from '../../types/index.js';

// PROSPECTOR schema — names the high-value AUTHORITATIVE source venues for THIS topic (domain-specific); output rides with the brainer.
export const SOURCES: Schema = {
  type: 'object',
  properties: {
    highValueSources: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          source: {
            type: 'string',
            description:
              'the venue + how to reach/search it, e.g. "arXiv (site:arxiv.org)", "SemiAnalysis (semianalysis.com)"',
          },
          goodFor: {
            type: 'string',
            description:
              'the kinds of sub-questions/rabbit-holes this venue is BEST for — specific enough for the brainer to match a research lane to it',
          },
          lang: {
            type: ['string', 'null'],
            description:
              "the venue's language as an ISO-ish code (zh, ja, es, pt, ru, ko, …) or language name; OMIT for English venues",
          },
        },
        required: ['source', 'goodFor'],
      },
    },
    languageGuidance: {
      type: ['string', 'null'],
      description:
        'one line routing the brainer to the non-English literatures that matter for this topic and why; "" when the topic is English-dominated',
    },
    reasoning: {
      type: ['string', 'null'],
      description: '2-3 sentences max: how you chose these venues / what you searched to confirm',
    },
  },
  required: ['highValueSources'],
};

export const prospector: Agent<ProspectorArgs> = {
  tier: CONFIG.TIER.prospector,
  effort: CONFIG.EFFORT.prospector,
  schema: SOURCES,
  buildPrompt: buildProspector,
};

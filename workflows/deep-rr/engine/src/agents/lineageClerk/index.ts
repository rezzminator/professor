// LINEAGE CLERK — batched per wave: canonicalizes new claims' provenance entities against the known
// canonical-key list so JS can union-find independence clusters. Tier: haiku (bounded, mechanical
// canonicalization — no tools, a plain subagent). Effort: medium. Dies → deterministic JS fallback
// (utils.lineageKeyOf clusters by norm(funder || venue || source-domain); unresolvable → cluster 0).
import { CONFIG } from '../../config.js';
import { buildLineageClerk } from './prompts.js';
import type { Agent, LineageClerkArgs, Schema } from '../../types/index.js';

export const LINEAGE: Schema = {
  type: 'object',
  properties: {
    links: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          id: { type: 'number', description: 'the claim id these canonical keys belong to' },
          keys: {
            type: 'array',
            items: { type: 'string' },
            description: 'canonical entity keys present on this claim, e.g. "funder:pfizer"',
          },
        },
        required: ['id', 'keys'],
      },
      description: 'one canonical-key set per claim',
    },
  },
  required: ['links'],
};

export const lineageClerk: Agent<LineageClerkArgs> = {
  tier: CONFIG.TIER.lineageClerk,
  effort: CONFIG.EFFORT.lineageClerk,
  schema: LINEAGE,
  buildPrompt: buildLineageClerk,
};

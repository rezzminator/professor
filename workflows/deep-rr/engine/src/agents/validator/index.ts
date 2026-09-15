// VALIDATOR — the per-wave coverage gate of the Crawl phase (distinct from the terminal judge). After each
// research wave it asks, cheaply, whether every lane fulfilled its request; the engine re-opens any lane that
// returned null or fulfilled:false (bounded by a per-lane failCount) so the next brainer can re-pursue it.
// Tier: sonnet (a bounded, cheap per-wave check). Effort: medium.
import { CONFIG } from '../../config.js';
import { buildValidator } from './prompts.js';
import type { Agent, Schema, ValidatorArgs } from '../../types/index.js';

export const VALIDATE: Schema = {
  type: 'object',
  properties: {
    checks: {
      type: 'array',
      items: {
        type: 'object',
        properties: {
          id: { type: 'number', description: 'the request id this verdict is for' },
          fulfilled: {
            type: 'boolean',
            description:
              'true = the lane answered its request; false = off-target, empty, or too thin',
          },
          reason: {
            type: 'string',
            description: 'one line — why it fell short (when fulfilled is false)',
          },
        },
        required: ['id', 'fulfilled'],
      },
      description: 'one verdict per request',
    },
    enough: { type: 'boolean', description: 'true = the wave made real progress overall' },
    missing: {
      type: 'array',
      items: { type: 'string' },
      description: 'the specific gaps still open, for the next brainer to re-pursue',
    },
  },
  required: ['checks', 'enough'],
};

export const validator: Agent<ValidatorArgs> = {
  tier: CONFIG.TIER.validator,
  effort: CONFIG.EFFORT.validator,
  schema: VALIDATE,
  buildPrompt: buildValidator,
};

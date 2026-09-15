// REFINER — one per load-bearing fact (parallel) in the Finalize phase. Adversarially fact-checks a fact
// against the web and returns its corrected, hardened claim. Tier: sonnet (adversarial verification on the
// web — modest middle tier). Effort: high.
import { CONFIG } from '../../config.js';
import { buildRefiner } from './prompts.js';
import type { Agent, RefineArgs, Schema } from '../../types/index.js';

export const REFINE: Schema = {
  type: 'object',
  properties: {
    report: {
      type: 'string',
      description:
        'markdown: the clean / corrected claim(s) for this fact after adversarial fact-checking against the sources',
    },
    queriesTried: {
      type: 'array',
      items: { type: 'string' },
      description: 'the exact counter-searches you ran',
    },
    counterFound: {
      type: 'boolean',
      description: 'true only when a real counter-example/contradiction turned up',
    },
    counterNote: {
      type: 'string',
      description: 'what the counter-evidence was, when counterFound is true',
    },
  },
  required: ['report', 'queriesTried', 'counterFound'],
};

export const refiner: Agent<RefineArgs> = {
  tier: CONFIG.TIER.refiner,
  effort: CONFIG.EFFORT.refiner,
  schema: REFINE,
  buildPrompt: buildRefiner,
};

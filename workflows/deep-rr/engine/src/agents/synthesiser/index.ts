// SYNTHESISER — writes the END report (always) from the hardened facts (source of truth) + the computed
// derivation (verbatim) + the final resultSoFar. Tier: opus (final synthesis). Effort: xhigh.
import { CONFIG } from '../../config.js';
import { buildSynthesiser } from './prompts.js';
import type { Agent, SynthesiserArgs, Schema } from '../../types/index.js';

export const REPORT: Schema = {
  type: 'object',
  properties: {
    report: {
      type: 'string',
      description: 'the FULL report as markdown, all 8 sections in order per the contract',
    },
    verdict: { type: 'string', description: '1-3 sentence headline answer' },
    confidence: { type: 'string', enum: ['high', 'medium', 'low'] },
    plan: {
      type: 'array',
      items: { type: 'string' },
      description: 'concrete, opinionated operator actions',
    },
    openQuestions: { type: 'array', items: { type: 'string' } },
  },
  required: ['report', 'verdict', 'confidence', 'plan', 'openQuestions'],
};

export const synthesiser: Agent<SynthesiserArgs> = {
  tier: CONFIG.TIER.synthesiser,
  effort: CONFIG.EFFORT.synthesiser,
  schema: REPORT,
  buildPrompt: buildSynthesiser,
};

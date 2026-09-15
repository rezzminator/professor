// JUDGE — the TERMINAL skeptic of the Finalize phase, the inverse of the synthesiser. Runs AFTER refine:
// sees the hardened facts + the brain's resultSoFar + the goal/deliverable, and judges whether the answer
// is sound (goal met, verification real, derivation valid). Drives a bounded remediation loop in the engine.
// Tier: opus (adversarial judgment). Effort: xhigh.
import { CONFIG } from '../../config.js';
import { RABBITHOLE } from '../shared.js';
import { buildJudge } from './prompts.js';
import type { Agent, JudgeArgs, Schema } from '../../types/index.js';

export const JUDGE: Schema = {
  type: 'object',
  properties: {
    goalMet: {
      type: 'boolean',
      description:
        'true = the answer fully meets the goal AND delivers the spec; false = it falls short',
    },
    verificationSound: {
      type: 'boolean',
      description:
        'true = the refine pass genuinely verified the facts; false = it rubber-stamped or mis-hardened a load-bearing fact',
    },
    needsCompute: {
      type: 'boolean',
      description: 'true = the answer rests on a quantitative derivation it does not yet hold',
    },
    computeSound: {
      type: 'boolean',
      description:
        'true = any derivation already present is valid (or none is needed); false = an existing derivation is wrong / lacks error bars',
    },
    reasoning: {
      type: 'string',
      description:
        'the load-bearing reason for the verdict — why it is sound, or the single biggest problem',
    },
    directive: {
      type: 'string',
      description: "the precise fix or derivation to perform when not satisfied; '' when satisfied",
    },
    reopenRabbitHoles: {
      type: 'array',
      items: RABBITHOLE,
      description:
        '1-3 concrete gap searches ONLY when a real evidence/coverage gap needs more crawling (NONE already pursued); empty otherwise',
    },
    reopenDirective: {
      type: 'string',
      description:
        'only with reopenRabbitHoles: the reader-facing extraction directive for the reopened lane — what to find; distinct from directive (the refine/report fix)',
    },
    retractClaimIds: {
      type: 'array',
      items: { type: 'number' },
      description:
        'ledger claim ids whose evidence is discredited (retraction/fabrication/misattribution) — the engine retracts them and recomputes everything downstream',
    },
  },
  required: ['goalMet', 'verificationSound', 'needsCompute', 'computeSound', 'reasoning'],
};

export const judge: Agent<JudgeArgs> = {
  tier: CONFIG.TIER.judge,
  effort: CONFIG.EFFORT.judge,
  schema: JUDGE,
  buildPrompt: buildJudge,
};
